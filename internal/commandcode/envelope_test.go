package commandcode

import (
	"encoding/json"
	"strings"
	"testing"
)

func buildTestEnvelope(t *testing.T, payload string, sessionID string, cfg RuntimeConfig) map[string]any {
	t.Helper()
	profile := DeriveDevice("user_env", "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)
	raw, err := BuildEnvelope([]byte(payload), sessionID, profile, cfg)
	if err != nil {
		t.Fatalf("BuildEnvelope() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("BuildEnvelope() produced invalid JSON: %v", err)
	}
	return body
}

func TestBuildEnvelopeKeyOrderAndThreadID(t *testing.T) {
	cfg := DefaultConfig()
	profile := DeriveDevice("user_env", "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	raw, err := BuildEnvelope([]byte(`{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`),
		"3d813cbb-47fb-32ba-91df-831e1593ac29", profile, cfg)
	if err != nil {
		t.Fatalf("BuildEnvelope() error = %v", err)
	}

	// The CLI's key order is load-bearing: config, memory, taste, skills,
	// permissionMode, threadId, mode, params.
	order := []string{"config", "memory", "taste", "skills", "permissionMode", "threadId", "mode", "params"}
	position := -1
	for _, key := range order {
		idx := strings.Index(string(raw), `"`+key+`":`)
		if idx < 0 {
			t.Fatalf("envelope is missing key %q: %s", key, raw)
		}
		if idx < position {
			t.Fatalf("key %q is out of order: %s", key, raw)
		}
		position = idx
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["threadId"] != "3d813cbb-47fb-32ba-91df-831e1593ac29" {
		t.Errorf("threadId = %v, want the session uuid", body["threadId"])
	}

	// A non-UUID session identity must omit the key entirely (CLI's toWireThreadId).
	body = buildTestEnvelope(t, `{"model":"m","messages":[]}`, "session-abc-123", cfg)
	if _, exists := body["threadId"]; exists {
		t.Error("threadId must be omitted when the session identity is not a UUID")
	}
	body = buildTestEnvelope(t, `{"model":"m","messages":[]}`, "", cfg)
	if _, exists := body["threadId"]; exists {
		t.Error("threadId must be omitted when no session identity was supplied")
	}
}

func TestBuildEnvelopeStaticFields(t *testing.T) {
	cfg := DefaultConfig()
	body := buildTestEnvelope(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, "", cfg)

	if body["memory"] != nil || body["taste"] != nil || body["skills"] != nil {
		t.Errorf("memory/taste/skills = %v/%v/%v, want null", body["memory"], body["taste"], body["skills"])
	}
	if body["permissionMode"] != "standard" {
		t.Errorf("permissionMode = %v, want standard", body["permissionMode"])
	}
	if body["mode"] != DefaultCLIMode {
		t.Errorf("mode = %v, want %q", body["mode"], DefaultCLIMode)
	}

	config, _ := body["config"].(map[string]any)
	if config["workingDir"] != DefaultProjectDir {
		t.Errorf("config.workingDir = %v, want the faked project dir", config["workingDir"])
	}
	environment, _ := config["environment"].(string)
	if !strings.Contains(environment, DefaultPlatform+"-"+DefaultArch) || !strings.Contains(environment, "Node.js ") {
		t.Errorf("config.environment = %q, want the CLI platform/arch/Node.js shape", environment)
	}

	params, _ := body["params"].(map[string]any)
	if params["stream"] != true {
		t.Error("params.stream must always be true: the CommandCode API is stream-only")
	}
	if params["model"] != "m" {
		t.Errorf("params.model = %v", params["model"])
	}
	if params["max_tokens"].(float64) != 64000 {
		t.Errorf("params.max_tokens = %v, want the 64000 default", params["max_tokens"])
	}
	if params["temperature"].(float64) != 0.3 {
		t.Errorf("params.temperature = %v, want the CLI default 0.3", params["temperature"])
	}
	tools, ok := params["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Errorf("params.tools = %#v, want an empty array (the CLI always sends the key)", params["tools"])
	}
}

func TestBuildEnvelopeSystemPromptHandling(t *testing.T) {
	cfg := DefaultConfig()

	// No system prompt -> single space placeholder (blocks upstream's 7.5K default).
	body := buildTestEnvelope(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ := body["params"].(map[string]any)
	system, _ := params["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %#v, want one placeholder block", params["system"])
	}
	if system[0].(map[string]any)["text"] != " " {
		t.Errorf("placeholder = %#v, want a single space", system[0])
	}

	// The switch turns the placeholder off.
	cfg.EmptySystemPlaceholder = false
	body = buildTestEnvelope(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	if _, exists := params["system"]; exists {
		t.Error("empty-system-placeholder=false must omit params.system")
	}

	// system/developer messages fold into text blocks with newlines appended.
	cfg = DefaultConfig()
	body = buildTestEnvelope(t, `{"model":"m","messages":[
		{"role":"system","content":"first"},
		{"role":"developer","content":[{"type":"text","text":"second","cache_control":{"type":"ephemeral"}}]},
		{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	system, _ = params["system"].([]any)
	if len(system) != 2 {
		t.Fatalf("system blocks = %#v, want 2", system)
	}
	first := system[0].(map[string]any)
	second := system[1].(map[string]any)
	if first["text"] != "first\n" {
		t.Errorf("first block text = %q, want it newline-terminated", first["text"])
	}
	if second["cache_control"] == nil {
		t.Error("client-supplied cache_control must be preserved")
	}
	messages, _ := params["messages"].([]any)
	if len(messages) != 1 {
		t.Errorf("messages = %#v, want only the chat history", messages)
	}
}

func TestBuildEnvelopePromptCacheKeyAnchorsBreakpoint(t *testing.T) {
	cfg := DefaultConfig()
	body := buildTestEnvelope(t, `{"model":"m","prompt_cache_key":"cache-1","messages":[
		{"role":"system","content":"sys"},{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ := body["params"].(map[string]any)
	system, _ := params["system"].([]any)
	if system[len(system)-1].(map[string]any)["cache_control"] == nil {
		t.Error("prompt_cache_key must anchor a cache breakpoint on the last system block")
	}

	// An explicit client breakpoint wins: no extra one is added.
	body = buildTestEnvelope(t, `{"model":"m","prompt_cache_key":"cache-1","messages":[
		{"role":"system","content":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}]},
		{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	system, _ = params["system"].([]any)
	if len(system) != 1 {
		t.Errorf("system = %#v, want the client's single block", system)
	}
}

func TestBuildEnvelopeToolsAndChoice(t *testing.T) {
	cfg := DefaultConfig()
	body := buildTestEnvelope(t, `{
		"model":"m",
		"tool_choice":"required",
		"parallel_tool_calls":true,
		"tools":[
			{"type":"function","function":{"name":"bash_output","description":"d","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"read","description":"r","parameters":{"type":"object","properties":{"p":{"type":"string"}}}}}
		],
		"messages":[{"role":"user","content":"hi"}]}`, "", cfg)

	params, _ := body["params"].(map[string]any)
	tools, _ := params["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %#v, want 2", tools)
	}
	first := tools[0].(map[string]any)
	if first["name"] != "shell_output" {
		t.Errorf("tool name = %v, want the CLI alias shell_output", first["name"])
	}
	if first["type"] != "function" {
		t.Errorf("tool type = %v, want function", first["type"])
	}
	if first["input_schema"] == nil {
		t.Error("wire tools must carry input_schema")
	}
	choice, _ := params["tool_choice"].(map[string]any)
	if choice["type"] != "any" {
		t.Errorf("tool_choice = %#v, want type=any for \"required\"", params["tool_choice"])
	}
	if params["parallel_tool_calls"] != true {
		t.Errorf("parallel_tool_calls = %v, want true", params["parallel_tool_calls"])
	}

	// Object form maps onto the Anthropic-style tool choice.
	body = buildTestEnvelope(t, `{"model":"m","tool_choice":{"type":"function","function":{"name":"read"}},
		"messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	choice, _ = params["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != "read" {
		t.Errorf("tool_choice = %#v, want {type:tool,name:read}", params["tool_choice"])
	}

	// "auto" stays auto.
	body = buildTestEnvelope(t, `{"model":"m","tool_choice":"auto","messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	choice, _ = params["tool_choice"].(map[string]any)
	if choice["type"] != "auto" {
		t.Errorf("tool_choice = %#v, want auto", params["tool_choice"])
	}
}

func TestBuildEnvelopeMessageMapping(t *testing.T) {
	cfg := DefaultConfig()
	body := buildTestEnvelope(t, `{
		"model":"m",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
			{"role":"assistant","reasoning_content":"thinking","content":"answer","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.txt\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"file body"},
			{"role":"weird","content":"x"}
		]}`, "", cfg)

	params, _ := body["params"].(map[string]any)
	messages, _ := params["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}

	userContent, _ := messages[0].(map[string]any)["content"].([]any)
	imagePart := userContent[1].(map[string]any)
	if imagePart["type"] != "image" || imagePart["image"] != "data:image/png;base64,AAAA" {
		t.Errorf("image part = %#v, want the CC image shape", imagePart)
	}
	if imagePart["mimeType"] != "image/png" {
		t.Errorf("mimeType = %v, want image/png", imagePart["mimeType"])
	}

	assistantParts, _ := messages[1].(map[string]any)["content"].([]any)
	if assistantParts[0].(map[string]any)["type"] != "reasoning" {
		t.Errorf("assistant parts = %#v, want reasoning first", assistantParts)
	}
	if assistantParts[1].(map[string]any)["type"] != "text" {
		t.Errorf("assistant parts = %#v, want text second", assistantParts)
	}
	toolCall := assistantParts[2].(map[string]any)
	if toolCall["type"] != "tool-call" || toolCall["toolName"] != "read" {
		t.Errorf("tool call = %#v", toolCall)
	}
	input, _ := toolCall["input"].(map[string]any)
	if input["path"] != "a.txt" {
		t.Errorf("tool call input = %#v, want the parsed arguments", toolCall["input"])
	}

	toolMessage, _ := messages[2].(map[string]any)
	if toolMessage["role"] != "tool" {
		t.Fatalf("tool message = %#v", toolMessage)
	}
	result := toolMessage["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool-result" || result["toolName"] != "read" {
		t.Errorf("tool result = %#v, want toolName resolved from history", result)
	}
	output := result["output"].(map[string]any)
	if output["value"] != "file body" {
		t.Errorf("tool output = %#v", output)
	}

	// Unknown roles are normalised to user.
	last, _ := messages[3].(map[string]any)
	if last["role"] != "user" {
		t.Errorf("unknown role mapped to %v, want user", last["role"])
	}
}

func TestBuildEnvelopeHoistsToolImages(t *testing.T) {
	cfg := DefaultConfig()
	payload := `{"model":"m","messages":[
		{"role":"tool","tool_call_id":"c1","content":[{"type":"text","text":"shot1"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}
	]}`
	body := buildTestEnvelope(t, payload, "", cfg)
	params, _ := body["params"].(map[string]any)
	messages, _ := params["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2 (tool result plus the hoisted image message)", len(messages))
	}
	toolMessage := messages[0].(map[string]any)
	result := toolMessage["content"].([]any)[0].(map[string]any)
	output := result["output"].(map[string]any)
	if output["value"] != "shot1" {
		t.Errorf("tool output = %#v, want only the text (never the base64)", output)
	}
	hoisted := messages[1].(map[string]any)
	if hoisted["role"] != "user" {
		t.Fatalf("hoisted message role = %v, want user", hoisted["role"])
	}
	parts := hoisted["content"].([]any)
	if parts[0].(map[string]any)["text"] != "[image returned by tool call]" {
		t.Errorf("hoisted parts = %#v, want a leading note", parts)
	}
	imagePart := parts[1].(map[string]any)
	if imagePart["type"] != "image" || imagePart["image"] != "data:image/png;base64,AAAA" {
		t.Errorf("hoisted image = %#v, want the CC image shape", imagePart)
	}
	if imagePart["mimeType"] != "image/png" {
		t.Errorf("hoisted mimeType = %v, want image/png", imagePart["mimeType"])
	}
	if imagePart["_toolImage"] != true {
		t.Errorf("hoisted image lost its tool marker: %#v", imagePart)
	}
}

func TestApplyToolImageBudgetKeepsNewest(t *testing.T) {
	older := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "note"},
		map[string]any{"type": "image", "image": "data:image/png;base64," + strings.Repeat("A", 200), "_toolImage": true},
	}}
	newer := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "image", "image": "data:image/png;base64," + strings.Repeat("B", 50), "_toolImage": true},
	}}
	client := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "image", "image": "data:image/png;base64," + strings.Repeat("C", 400)},
	}}
	messages := []any{older, client, newer}

	ApplyToolImageBudget(messages, 100)

	olderParts := older["content"].([]any)
	if olderParts[1].(map[string]any)["type"] != "text" {
		t.Errorf("older tool screenshot = %#v, want it replaced by the placeholder", olderParts[1])
	}
	if olderParts[1].(map[string]any)["text"] != wireToolImagePlaceholder {
		t.Errorf("placeholder = %v", olderParts[1].(map[string]any)["text"])
	}
	newerParts := newer["content"].([]any)
	if newerParts[0].(map[string]any)["type"] != "image" {
		t.Errorf("newest tool screenshot = %#v, want it kept", newerParts[0])
	}
	clientParts := client["content"].([]any)
	if clientParts[0].(map[string]any)["type"] != "image" {
		t.Errorf("client-attached image = %#v, want it untouched", clientParts[0])
	}
}

func TestBuildEnvelopeMaxTokensCapAndModelFallback(t *testing.T) {
	cfg := DefaultConfig()
	body := buildTestEnvelope(t, `{"model":"","max_tokens":900000,"messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ := body["params"].(map[string]any)
	if params["model"] != "deepseek/deepseek-v4-flash" {
		t.Errorf("model = %v, want the default model", params["model"])
	}
	if params["max_tokens"].(float64) != 200000 {
		t.Errorf("max_tokens = %v, want the 200000 cap", params["max_tokens"])
	}

	body = buildTestEnvelope(t, `{"model":"m","temperature":0.3,"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	if params["temperature"].(float64) != 0.3 {
		t.Errorf("temperature = %v", params["temperature"])
	}
	if params["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v", params["reasoning_effort"])
	}
}

func TestBuildEnvelopeRejectsInvalidJSON(t *testing.T) {
	cfg := DefaultConfig()
	profile := DeriveDevice("user_env", "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)
	if _, err := BuildEnvelope([]byte("{not json"), "", profile, cfg); err == nil {
		t.Fatal("BuildEnvelope(invalid json) = nil error, want a failure")
	}
}

func TestBuildEnvelopeRespectsExplicitTemperature(t *testing.T) {
	cfg := DefaultConfig()
	body := buildTestEnvelope(t, `{"model":"m","temperature":0,"messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ := body["params"].(map[string]any)
	if params["temperature"].(float64) != 0 {
		t.Errorf("temperature = %v, want explicit zero preserved", params["temperature"])
	}
	body = buildTestEnvelope(t, `{"model":"m","temperature":0.7,"messages":[{"role":"user","content":"hi"}]}`, "", cfg)
	params, _ = body["params"].(map[string]any)
	if params["temperature"].(float64) != 0.7 {
		t.Errorf("temperature = %v, want explicit 0.7 preserved", params["temperature"])
	}
}
