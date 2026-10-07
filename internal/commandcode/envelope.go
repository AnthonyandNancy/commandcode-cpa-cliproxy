package commandcode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// wireToolImagePlaceholder replaces tool screenshots dropped by the image budget
// so the model knows a screenshot existed instead of assuming none ever did.
const wireToolImagePlaceholder = "[older tool screenshot omitted: image budget exceeded]"

// toolNameAliases mirrors the CLI's resolveToolNameAlias table.
var toolNameAliases = map[string]string{
	"bash_output":         "shell_output",
	"task_output":         "shell_output",
	"tool_search":         "search_tools",
	"read_multiple_files": "read_file",
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type envelopeConfig struct {
	WorkingDir    string `json:"workingDir"`
	Date          string `json:"date"`
	Environment   string `json:"environment"`
	Structure     []any  `json:"structure"`
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	MainBranch    string `json:"mainBranch"`
	GitStatus     string `json:"gitStatus"`
	RecentCommits []any  `json:"recentCommits"`
}

type envelopeSystemBlock struct {
	Type         string         `json:"type"`
	Text         string         `json:"text"`
	CacheControl map[string]any `json:"cache_control,omitempty"`
}

type envelopeTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type envelopeParams struct {
	Model             string                `json:"model"`
	Messages          []any                 `json:"messages"`
	MaxTokens         int                   `json:"max_tokens"`
	Stream            bool                  `json:"stream"`
	System            []envelopeSystemBlock `json:"system,omitempty"`
	Temperature       *float64              `json:"temperature,omitempty"`
	ReasoningEffort   string                `json:"reasoning_effort,omitempty"`
	Tools             []envelopeTool        `json:"tools"`
	ToolChoice        any                   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                 `json:"parallel_tool_calls,omitempty"`
}

// envelopeBody is the CommandCode request. Field order is load-bearing: it
// mirrors the CLI's own key order (and omits threadId entirely when the session
// identity is not a UUID).
type envelopeBody struct {
	Config         envelopeConfig `json:"config"`
	Memory         any            `json:"memory"`
	Taste          any            `json:"taste"`
	Skills         any            `json:"skills"`
	PermissionMode string         `json:"permissionMode"`
	ThreadID       string         `json:"threadId,omitempty"`
	Mode           string         `json:"mode"`
	PromptCache    any            `json:"promptCache,omitempty"`
	Params         envelopeParams `json:"params"`
}

// BuildEnvelope converts one chat-completions payload into the CommandCode
// envelope. It never invents context: an absent system prompt becomes a single
// space placeholder (which keeps upstream from injecting its own ~7.5K-token
// default), and an absent session identity simply leaves threadId out.
func BuildEnvelope(payload []byte, sessionID string, profile DeviceProfile, cfg RuntimeConfig) ([]byte, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, &root); err != nil {
			return nil, fmt.Errorf("request payload is not valid JSON: %w", err)
		}
	}

	model := strings.TrimSpace(stringValue(root["model"]))
	if model == "" {
		model = "deepseek/deepseek-v4-flash"
	}

	messages, _ := root["messages"].([]any)
	systemBlocks, chatMessages := splitSystemMessages(messages)
	wireMessages := toWireMessages(chatMessages)
	if cfg.MaxToolImageMB > 0 {
		ApplyToolImageBudget(wireMessages, int64(cfg.MaxToolImageMB)*1024*1024)
	}
	if systemBlocks == nil {
		systemBlocks = []envelopeSystemBlock{}
	}

	if len(systemBlocks) > 0 {
		for i := 0; i < len(systemBlocks)-1; i++ {
			systemBlocks[i].Text += "\n"
		}
	} else if cfg.EmptySystemPlaceholder {
		systemBlocks = []envelopeSystemBlock{{Type: "text", Text: " "}}
	}

	// Cache breakpoints: keep client-supplied markers, otherwise anchor one on
	// the last system block when the client sent an OpenAI prompt_cache_key
	// (the system prompt is the prefix that is actually cached).
	if key := strings.TrimSpace(stringValue(root["prompt_cache_key"])); key != "" && !hasCacheMarker(systemBlocks, wireMessages) && len(systemBlocks) > 0 {
		systemBlocks[len(systemBlocks)-1].CacheControl = map[string]any{"type": "ephemeral"}
	}

	maxTokens := intValue(root["max_tokens"])
	if maxTokens <= 0 {
		maxTokens = 64000
	}
	if maxTokens > 200000 {
		maxTokens = 200000
	}

	params := envelopeParams{
		Model:     model,
		Messages:  wireMessages,
		MaxTokens: maxTokens,
		Stream:    true, // the CommandCode API is stream-only
		System:    systemBlocks,
		Tools:     toWireTools(root["tools"]),
	}
	if temperature := floatPointer(root["temperature"]); temperature != nil {
		params.Temperature = temperature
	}
	if effort := strings.TrimSpace(stringValue(root["reasoning_effort"])); effort != "" {
		params.ReasoningEffort = effort
	}
	if choice, ok := root["tool_choice"]; ok && choice != nil {
		params.ToolChoice = toWireToolChoice(choice)
	}
	if parallel := boolPointer(root["parallel_tool_calls"]); parallel != nil {
		params.ParallelToolCalls = parallel
	}

	body := envelopeBody{
		Config: envelopeConfig{
			WorkingDir:    profile.ProjectDir,
			Date:          todayUTC(),
			Environment:   profile.Platform,
			Structure:     []any{},
			IsGitRepo:     false,
			CurrentBranch: "",
			MainBranch:    "",
			GitStatus:     "",
			RecentCommits: []any{},
		},
		Memory:         nil,
		Taste:          nil,
		Skills:         nil, // the CLI sends null, not an empty string
		PermissionMode: "standard",
		Mode:           firstNonEmpty(cfg.CLIMode, DefaultCLIMode),
		Params:         params,
	}
	if uuidPattern.MatchString(strings.TrimSpace(sessionID)) {
		body.ThreadID = strings.TrimSpace(sessionID)
	}

	return json.Marshal(body)
}

// splitSystemMessages separates system/developer messages from the chat history,
// folding them into the text-block array the CLI sends.
func splitSystemMessages(messages []any) ([]envelopeSystemBlock, []map[string]any) {
	blocks := []envelopeSystemBlock{}
	chat := make([]map[string]any, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := strings.TrimSpace(stringValue(message["role"]))
		if role == "system" || role == "developer" {
			blocks = append(blocks, systemBlocksOf(message["content"])...)
			continue
		}
		chat = append(chat, message)
	}
	return blocks, chat
}

func systemBlocksOf(content any) []envelopeSystemBlock {
	out := []envelopeSystemBlock{}
	switch typed := content.(type) {
	case string:
		if typed != "" {
			out = append(out, envelopeSystemBlock{Type: "text", Text: typed})
		}
	case []any:
		for _, rawPart := range typed {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			text := firstNonEmpty(stringValue(part["text"]), stringValue(part["content"]))
			cache := mapValue(part["cache_control"])
			if text == "" && cache == nil {
				continue
			}
			out = append(out, envelopeSystemBlock{Type: "text", Text: text, CacheControl: cache})
		}
	default:
		if content != nil {
			out = append(out, envelopeSystemBlock{Type: "text", Text: stringValue(content)})
		}
	}
	return out
}

// toWireMessages converts chat history into the CommandCode message shape,
// including hoisting images returned by tools into a following user message.
func toWireMessages(messages []map[string]any) []any {
	nameByCallID := map[string]string{}
	for _, message := range messages {
		if strings.TrimSpace(stringValue(message["role"])) != "assistant" {
			continue
		}
		for _, rawCall := range arrayValue(message["tool_calls"]) {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			id := strings.TrimSpace(stringValue(call["id"]))
			if id == "" {
				continue
			}
			function := mapValue(call["function"])
			nameByCallID[id] = strings.TrimSpace(stringValue(function["name"]))
		}
	}

	out := make([]any, 0, len(messages))
	for _, message := range messages {
		switch role := strings.TrimSpace(stringValue(message["role"])); role {
		case "user":
			out = append(out, map[string]any{"role": "user", "content": userContent(message["content"])})
		case "assistant":
			out = append(out, map[string]any{"role": "assistant", "content": assistantContent(message)})
		case "tool":
			text, images := splitToolOutput(message["content"])
			out = append(out, map[string]any{
				"role": "tool",
				"content": []any{map[string]any{
					"type":       "tool-result",
					"toolCallId": stringValue(message["tool_call_id"]),
					"toolName":   nameByCallID[stringValue(message["tool_call_id"])],
					"output":     map[string]any{"type": "text", "value": text},
				}},
			})
			if len(images) > 0 {
				parts := []any{map[string]any{"type": "text", "text": "[image returned by tool call]"}}
				for _, url := range images {
					parts = append(parts, map[string]any{
						"type":       "image_url",
						"image_url":  map[string]any{"url": url},
						"_toolImage": true,
					})
				}
				// The hoisted images take the same user-image conversion as client
				// attachments, keeping the tool marker for the budget pass.
				out = append(out, map[string]any{"role": "user", "content": userContent(parts)})
			}
		default:
			// Unknown roles are normalised to user so upstream validation accepts them.
			out = append(out, map[string]any{"role": "user", "content": userContent(message["content"])})
		}
	}
	return out
}

func userContent(content any) []any {
	switch typed := content.(type) {
	case string:
		return []any{map[string]any{"type": "text", "text": typed}}
	case []any:
		parts := make([]any, 0, len(typed))
		for _, rawPart := range typed {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(stringValue(part["type"])) == "image_url" {
				imageURL := mapValue(part["image_url"])
				url := imageURLURL(part)
				imagePart := map[string]any{"type": "image", "image": url}
				if mime := mediaTypeOf(url); mime != "" {
					imagePart["mimeType"] = mime
				}
				if truthy(part["_toolImage"]) {
					imagePart["_toolImage"] = true
				}
				_ = imageURL
				parts = append(parts, imagePart)
				continue
			}
			parts = append(parts, part)
		}
		if parts == nil {
			parts = []any{}
		}
		return parts
	default:
		return []any{map[string]any{"type": "text", "text": stringValue(content)}}
	}
}

func assistantContent(message map[string]any) []any {
	parts := []any{}
	// Reasoning must be replayed first: upstream validates that thinking history
	// is carried back, and the CLI's captured order is [reasoning, text, tool-call].
	if reasoning := strings.TrimSpace(stringValue(message["reasoning_content"])); reasoning != "" {
		parts = append(parts, map[string]any{"type": "reasoning", "text": reasoning})
	}
	switch content := message["content"].(type) {
	case string:
		if content != "" {
			parts = append(parts, map[string]any{"type": "text", "text": content})
		}
	case []any:
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			switch strings.TrimSpace(stringValue(part["type"])) {
			case "text":
				parts = append(parts, part)
			case "reasoning":
				if strings.TrimSpace(stringValue(message["reasoning_content"])) == "" {
					parts = append(parts, part)
				}
			}
		}
	}
	for _, rawCall := range arrayValue(message["tool_calls"]) {
		call, ok := rawCall.(map[string]any)
		if !ok {
			continue
		}
		function := mapValue(call["function"])
		parts = append(parts, map[string]any{
			"type":       "tool-call",
			"toolCallId": stringValue(call["id"]),
			"toolName":   strings.TrimSpace(stringValue(function["name"])),
			"input":      parseArguments(function["arguments"]),
		})
	}
	return parts
}

// splitToolOutput mirrors the CLI's toWireToolOutput: text blocks joined by
// newlines, images returned separately so they are never tokenized as text.
func splitToolOutput(content any) (string, []string) {
	switch typed := content.(type) {
	case string:
		return typed, nil
	case []any:
		texts := []string{}
		images := []string{}
		for _, rawPart := range typed {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			switch strings.TrimSpace(stringValue(part["type"])) {
			case "text":
				texts = append(texts, stringValue(part["text"]))
			case "image_url":
				if url := stringValue(mapValue(part["image_url"])["url"]); url != "" {
					images = append(images, url)
				}
			}
		}
		return strings.Join(texts, "\n"), images
	case nil:
		return "", nil
	default:
		return stringValue(content), nil
	}
}

func toWireTools(raw any) []envelopeTool {
	tools := []envelopeTool{}
	for _, item := range arrayValue(raw) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		function := mapValue(entry["function"])
		name := firstNonEmpty(stringValue(function["name"]), stringValue(entry["name"]))
		if strings.TrimSpace(name) == "" {
			continue
		}
		description := firstNonEmpty(stringValue(function["description"]), stringValue(entry["description"]))
		schema := function["parameters"]
		if schema == nil {
			schema = entry["input_schema"]
		}
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, envelopeTool{
			Name:        wireToolName(name),
			Description: description,
			InputSchema: schema,
		})
	}
	return tools
}

// toWireToolChoice maps the OpenAI spelling onto the Anthropic-style shape the
// CommandCode API expects.
func toWireToolChoice(choice any) any {
	switch typed := choice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "auto":
			return map[string]any{"type": "auto"}
		case "none":
			return map[string]any{"type": "none"}
		case "required", "any":
			return map[string]any{"type": "any"}
		default:
			return map[string]any{"type": "auto"}
		}
	case map[string]any:
		if strings.EqualFold(strings.TrimSpace(stringValue(typed["type"])), "function") {
			function := mapValue(typed["function"])
			return map[string]any{"type": "tool", "name": strings.TrimSpace(stringValue(function["name"]))}
		}
		return typed
	default:
		return nil
	}
}

func wireToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if alias, ok := toolNameAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

func hasCacheMarker(systemBlocks []envelopeSystemBlock, messages []any) bool {
	for _, block := range systemBlocks {
		if len(block.CacheControl) > 0 {
			return true
		}
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		for _, rawPart := range arrayValue(message["content"]) {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			if len(mapValue(part["cache_control"])) > 0 {
				return true
			}
		}
	}
	return false
}

// ApplyToolImageBudget keeps the newest tool screenshots inside the configured
// byte budget (at least one) and replaces the rest with a placeholder.
func ApplyToolImageBudget(messages []any, budgetBytes int64) {
	if budgetBytes <= 0 {
		return
	}
	type imageRef struct {
		message map[string]any
		part    map[string]any
		index   int
		kind    string
	}
	refs := []imageRef{}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for index, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			// Only tool screenshots are budgeted: images the client attached
			// itself are never dropped.
			if truthy(part["_toolImage"]) {
				refs = append(refs, imageRef{message: message, part: part, index: index, kind: "tool"})
			}
		}
	}
	if len(refs) == 0 {
		return
	}
	keep := map[int]bool{}
	var used int64
	for i := len(refs) - 1; i >= 0; i-- {
		length := int64(len(imageRefURL(refs[i].part)))
		if len(keep) == 0 || used+length <= budgetBytes {
			keep[i] = true
			used += length
		}
	}
	if len(keep) == len(refs) {
		return
	}
	for i := range refs {
		if keep[i] {
			continue
		}
		parts, ok := refs[i].message["content"].([]any)
		if !ok || refs[i].index >= len(parts) {
			continue
		}
		parts[refs[i].index] = map[string]any{"type": "text", "text": wireToolImagePlaceholder}
		refs[i].message["content"] = parts
	}
}

// imageURLURL reads the url from either the OpenAI image_url shape or the
// converted CC image shape.
func imageURLURL(part map[string]any) string {
	if url := stringValue(part["image"]); url != "" {
		return url
	}
	return stringValue(mapValue(part["image_url"])["url"])
}

func imageRefURL(part map[string]any) string {
	return imageURLURL(part)
}

func mediaTypeOf(url string) string {
	if !strings.HasPrefix(url, "data:") {
		return ""
	}
	rest := strings.TrimPrefix(url, "data:")
	if idx := strings.IndexAny(rest, ";,"); idx > 0 {
		return rest[:idx]
	}
	return ""
}

func parseArguments(raw any) any {
	switch typed := raw.(type) {
	case string:
		parsed := map[string]any{}
		if err := json.Unmarshal([]byte(typed), &parsed); err == nil {
			return parsed
		}
		return map[string]any{}
	case map[string]any:
		return typed
	case nil:
		return map[string]any{}
	default:
		return typed
	}
}

func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}

func mapValue(v any) map[string]any {
	if v == nil {
		return nil
	}
	if typed, ok := v.(map[string]any); ok {
		return typed
	}
	return nil
}

func arrayValue(v any) []any {
	if typed, ok := v.([]any); ok {
		return typed
	}
	return nil
}

func intValue(v any) int {
	switch typed := v.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 0
}

func floatPointer(v any) *float64 {
	switch typed := v.(type) {
	case float64:
		return &typed
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return &parsed
		}
	}
	return nil
}

func boolPointer(v any) *bool {
	if typed, ok := v.(bool); ok {
		return &typed
	}
	return nil
}

func truthy(v any) bool {
	typed, ok := v.(bool)
	return ok && typed
}
