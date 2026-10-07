package commandcode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// upstreamLineMaxBytes bounds one upstream line. A reasoning delta stays far
// below this; the ceiling only guards against an unbounded line.
const upstreamLineMaxBytes = 16 << 20

// usageInfo is the token accounting the CommandCode API reports.
type usageInfo struct {
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
}

func (u usageInfo) openAIUsage() map[string]any {
	return map[string]any{
		"prompt_tokens":     u.InputTokens,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      u.InputTokens + u.OutputTokens,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": u.CachedInputTokens,
		},
	}
}

// streamTranslator converts the CommandCode event stream into OpenAI
// chat-completion chunks. It is the single place where event names are known.
type streamTranslator struct {
	completionID  string
	created       int64
	model         string
	chunkIndex    int
	toolCallIndex int
	finishReason  string
	usage         usageInfo
	sawFinish     bool
	sawContent    bool
	upstreamErr   *UpstreamError
}

func newStreamTranslator(completionID, model string, created int64) *streamTranslator {
	return &streamTranslator{completionID: completionID, model: model, created: created}
}

// translateLine converts one upstream line into at most one chunk payload.
// Malformed or non-JSON lines are ignored: a keepalive or comment must never
// abort a healthy stream.
func (t *streamTranslator) translateLine(line []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) || bytes.HasPrefix(trimmed, []byte(":")) {
		return nil, false
	}
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		trimmed = bytes.TrimSpace(trimmed[len("data:"):])
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
			return nil, false
		}
	}

	var event struct {
		Type         string          `json:"type"`
		Text         string          `json:"text"`
		Delta        string          `json:"delta"`
		ToolCallID   string          `json:"toolCallId"`
		ToolName     string          `json:"toolName"`
		Input        json.RawMessage `json:"input"`
		FinishReason string          `json:"finishReason"`
		Usage        *upstreamUsage  `json:"usage"`
		TotalUsage   *upstreamUsage  `json:"totalUsage"`
		Error        *struct {
			Message     string `json:"message"`
			Code        string `json:"code"`
			IsRetryable bool   `json:"isRetryable"`
			Status      int    `json:"status"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(trimmed, &event); err != nil {
		Logf("warn", "ignoring unparsable upstream line", map[string]any{"bytes": len(trimmed)})
		return nil, false
	}

	switch event.Type {
	case "", "text-start", "reasoning-start", "start", "start-step",
		"reasoning-end", "provider-metadata", "tool-input-start", "tool-input-delta",
		"tool-input-end", "tool-error", "text-end":
		return nil, false

	case "text-delta":
		text := firstNonEmpty(event.Text, event.Delta)
		if text == "" {
			return nil, false
		}
		t.sawContent = true
		return t.emit(map[string]any{"content": text}), true

	case "reasoning-delta":
		if event.Text == "" {
			return nil, false
		}
		t.sawContent = true
		return t.emit(map[string]any{"reasoning_content": event.Text}), true

	case "tool-call":
		id := strings.TrimSpace(event.ToolCallID)
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", time.Now().UnixMilli(), t.toolCallIndex)
		}
		arguments := "{}"
		if len(event.Input) > 0 {
			if raw := strings.TrimSpace(string(event.Input)); strings.HasPrefix(raw, `"`) {
				// input arrived as a JSON string: unwrap it
				var unwrapped string
				if err := json.Unmarshal(event.Input, &unwrapped); err == nil {
					arguments = unwrapped
				} else {
					arguments = raw
				}
			} else {
				arguments = string(event.Input)
			}
		}
		t.sawContent = true
		toolCallIndex := t.toolCallIndex
		t.toolCallIndex++
		return t.emit(map[string]any{
			"content": nil,
			"tool_calls": []any{map[string]any{
				"index": toolCallIndex,
				"id":    id,
				"type":  "function",
				"function": map[string]any{
					"name":      strings.TrimSpace(event.ToolName),
					"arguments": arguments,
				},
			}},
		}), true

	case "finish-step":
		t.sawFinish = true
		if event.FinishReason != "" {
			t.finishReason = mapFinishReason(event.FinishReason)
		}
		if event.Usage != nil {
			t.usage = event.Usage.toUsageInfo()
		}
		return nil, false

	case "finish":
		t.sawFinish = true
		if t.finishReason == "" {
			t.finishReason = mapFinishReason(firstNonEmpty(event.FinishReason, "stop"))
		}
		if event.TotalUsage != nil {
			t.usage = event.TotalUsage.toUsageInfo()
		} else if event.Usage != nil {
			t.usage = event.Usage.toUsageInfo()
		}
		finish := t.finishReason
		if finish == "" {
			finish = "stop"
		}
		if finish == "pause_turn" {
			finish = "length"
		}
		return t.emitWithFinish(map[string]any{}, finish, t.usage.openAIUsage()), true

	case "error":
		message := "Unknown error"
		status := 502
		code := ""
		if event.Error != nil {
			if strings.TrimSpace(event.Error.Message) != "" {
				message = event.Error.Message
			}
			code = event.Error.Code
			if event.Error.Status > 0 {
				status = event.Error.Status
			}
		} else if strings.TrimSpace(event.Message) != "" {
			message = event.Message
		}
		classified := ClassifyUpstream(status, nil)
		classified.Message = message
		if code != "" {
			classified.Code = code
		}
		t.upstreamErr = classified
		return nil, false

	default:
		Logf("warn", "unknown upstream event type", map[string]any{"type": event.Type})
		return nil, false
	}
}

type upstreamUsage struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
}

func (u upstreamUsage) toUsageInfo() usageInfo {
	info := usageInfo{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedInputTokens: u.CachedInputTokens}
	// Zero output means the turn produced nothing: reporting the billed input
	// would be a false charge, so both fields are zeroed (mirrors the proxy).
	if u.OutputTokens == 0 {
		info.InputTokens = 0
		info.CachedInputTokens = 0
	}
	return info
}

func (t *streamTranslator) emit(delta map[string]any) []byte {
	return t.emitWithFinish(delta, "", nil)
}

func (t *streamTranslator) emitWithFinish(delta map[string]any, finish string, usage map[string]any) []byte {
	if t.chunkIndex == 0 {
		if _, ok := delta["role"]; !ok {
			delta = withRole(delta)
		}
	} else {
		delete(delta, "role")
	}
	t.chunkIndex++

	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": nil,
	}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	chunk := map[string]any{
		"id":      t.completionID,
		"object":  "chat.completion.chunk",
		"created": t.created,
		"model":   t.model,
		"choices": []any{choice},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return nil
	}
	return raw
}

func withRole(delta map[string]any) map[string]any {
	out := make(map[string]any, len(delta)+1)
	out["role"] = "assistant"
	for key, value := range delta {
		out[key] = value
	}
	return out
}

// mapFinishReason mirrors the proxy's finish-reason normalisation.
func mapFinishReason(reason string) string {
	trimmed := strings.ToLower(strings.TrimSpace(reason))
	switch trimmed {
	case "":
		return "stop"
	case "tool-calls", "tool_calls", "tool_use":
		return "tool_calls"
	case "length", "max_tokens", "max_output_tokens", "model_context_window_exceeded":
		return "length"
	case "network-error", "network_error", "connection-error", "connection_error", "upstream-error", "upstream_error":
		return "upstream_error"
	default:
		return trimmed
	}
}

// consumeUpstream reads the upstream stream line by line, handing each decoded
// chunk to emit. A false return stops reading (the client is gone); an error
// reports a transport or upstream failure.
func (t *streamTranslator) consumeUpstream(body io.Reader, idle time.Duration, emit func([]byte) bool) error {
	reader := bufio.NewReaderSize(body, 64*1024)
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()

	type readResult struct {
		line []byte
		err  error
		done bool
	}
	results := make(chan readResult)
	stop := make(chan struct{})
	defer close(stop)

	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case results <- readResult{line: line}:
				case <-stop:
					return
				}
			}
			if err != nil {
				select {
				case results <- readResult{err: err, done: true}:
				case <-stop:
				}
				return
			}
		}
	}()

	for {
		select {
		case result := <-results:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idle)
			if result.done {
				if result.err == io.EOF {
					return nil
				}
				return result.err
			}
			if chunk, ok := t.translateLine(result.line); ok && chunk != nil {
				if !emit(chunk) {
					return errClientGone
				}
			}
		case <-idleTimer.C:
			return fmt.Errorf("upstream idle for %s", idle)
		}
	}
}

// errClientGone reports that the downstream client disappeared.
var errClientGone = fmt.Errorf("downstream client is gone")

// foldStream consumes the upstream stream and folds it into one non-streaming
// chat.completion object.
func foldStream(body io.Reader, model, completionID string, created int64, idle time.Duration) ([]byte, usageInfo, error) {
	translator := newStreamTranslator(completionID, model, created)

	var mu sync.Mutex
	content := strings.Builder{}
	reasoning := strings.Builder{}
	var toolCalls []map[string]any

	err := translator.consumeUpstream(body, idle, func(chunk []byte) bool {
		var parsed struct {
			Choices []struct {
				Delta map[string]any `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(chunk, &parsed); err != nil || len(parsed.Choices) == 0 {
			return true
		}
		delta := parsed.Choices[0].Delta
		mu.Lock()
		defer mu.Unlock()
		if text, ok := delta["content"].(string); ok {
			content.WriteString(text)
		}
		if text, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(text)
		}
		if rawCalls, ok := delta["tool_calls"].([]any); ok {
			for _, rawCall := range rawCalls {
				call, ok := rawCall.(map[string]any)
				if !ok {
					continue
				}
				toolCalls = append(toolCalls, call)
			}
		}
		return true
	})
	if err != nil {
		return nil, usageInfo{}, err
	}
	if translator.upstreamErr != nil {
		return nil, usageInfo{}, translator.upstreamErr
	}

	message := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		calls := make([]any, 0, len(toolCalls))
		for _, call := range toolCalls {
			calls = append(calls, call)
		}
		message["tool_calls"] = calls
		// OpenAI allows content and tool_calls on the same message; only an
		// empty text is nulled so clients do not print an empty bubble.
		if content.Len() == 0 {
			message["content"] = nil
		}
	}

	finish := translator.finishReason
	if finish == "" {
		finish = "stop"
	}
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}

	payload := map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": translator.usage.openAIUsage(),
	}
	raw, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, usageInfo{}, errMarshal
	}
	if !translator.sawContent && translator.usage.OutputTokens == 0 {
		return raw, translator.usage, errZeroOutput
	}
	return raw, translator.usage, nil
}

// errZeroOutput reports a turn that produced nothing visible: retryable, so the
// caller maps it onto a 429 like the reference proxy does.
var errZeroOutput = fmt.Errorf("upstream produced no output")
