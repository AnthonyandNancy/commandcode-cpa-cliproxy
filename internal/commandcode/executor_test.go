package commandcode

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// stubHost records every host RPC so the tests can assert what the plugin asked
// CPA to do (stream frames, logs) without a running CPA.
type stubHost struct {
	mu       sync.Mutex
	calls    []hostCallRecord
	failEmit bool
}

type hostCallRecord struct {
	Method  string
	Request map[string]any
}

func (s *stubHost) install(t *testing.T) {
	t.Helper()
	resetInitState()
	resetModelCache()
	SetHostCaller(func(method string, request []byte) ([]byte, error) {
		record := hostCallRecord{Method: method}
		if len(request) > 0 {
			_ = json.Unmarshal(request, &record.Request)
		}
		s.mu.Lock()
		s.calls = append(s.calls, record)
		s.mu.Unlock()
		if method == "host.stream.emit" && s.failEmit {
			return nil, errClientGone
		}
		return []byte(`{"ok":true}`), nil
	})
	t.Cleanup(func() { SetHostCaller(nil) })
}

func (s *stubHost) records(method string) []hostCallRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []hostCallRecord{}
	for _, record := range s.calls {
		if record.Method == method {
			out = append(out, record)
		}
	}
	return out
}

func executorRequest(t *testing.T, baseURL, payload string, metadata map[string]any, headers http.Header) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"AuthID":          "commandcode-abc.json",
		"AuthProvider":    ProviderID,
		"Model":           "deepseek/deepseek-v4-flash",
		"Payload":         []byte(payload),
		"StorageJSON":     []byte(`{"type":"commandcode","api_key":"user_test1234567890","base_url":"` + baseURL + `"}`),
		"Metadata":        metadata,
		"Headers":         headers,
		"OriginalRequest": []byte(payload),
	})
	if err != nil {
		t.Fatalf("marshal executor request: %v", err)
	}
	return raw
}

// upstreamStreamServer answers the init pre-requests and serves one scripted
// generation stream.
func upstreamStreamServer(t *testing.T, lines []string, captured *http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EndpointFingerprint, EndpointLifecycle, EndpointModels:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		case EndpointGenerate:
			if captured != nil {
				clone := r.Clone(r.Context())
				body := make([]byte, 1<<20)
				n, _ := r.Body.Read(body)
				clone.Body = http.NoBody
				clone.Header = r.Header.Clone()
				_ = n
				*captured = *clone
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			for _, line := range lines {
				_, _ = w.Write([]byte(line + "\n"))
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func decodeExecutorPayload(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Payload []byte `json:"Payload"`
		} `json:"result"`
		Error *struct {
			Message    string `json:"message"`
			HTTPStatus int    `json:"http_status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("frame is not valid JSON: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("frame reported a failure: %s", frame)
	}
	var payload map[string]any
	if err := json.Unmarshal(envelope.Result.Payload, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	return payload
}

func TestHandleExecuteFoldsStream(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	lines := []string{
		`{"type":"start"}`,
		`{"type":"reasoning-delta","text":"thinking "}`,
		`{"type":"text-delta","text":"Hello"}`,
		`{"type":"tool-call","toolCallId":"call_1","toolName":"read","input":{"path":"a.txt"}}`,
		`{"type":"finish","finishReason":"tool-calls","totalUsage":{"inputTokens":120,"outputTokens":30,"cachedInputTokens":100}}`,
	}
	server := upstreamStreamServer(t, lines, nil)
	defer server.Close()

	raw := executorRequest(t, server.URL, `{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`, nil, nil)
	frame, err := handleExecute(raw)
	if err != nil {
		t.Fatalf("handleExecute() error = %v", err)
	}

	payload := decodeExecutorPayload(t, frame)
	if payload["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", payload["object"])
	}
	choices, _ := payload["choices"].([]any)
	choice := choices[0].(map[string]any)
	message := choice["message"].(map[string]any)
	if message["role"] != "assistant" {
		t.Errorf("message.role = %v", message["role"])
	}
	if message["reasoning_content"] != "thinking " {
		t.Errorf("message.reasoning_content = %v", message["reasoning_content"])
	}
	if message["content"] != "Hello" {
		t.Errorf("message.content = %v", message["content"])
	}
	toolCalls, _ := message["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls = %#v, want one folded call", message["tool_calls"])
	}
	call := toolCalls[0].(map[string]any)
	function := call["function"].(map[string]any)
	if function["name"] != "read" {
		t.Errorf("tool name = %v", function["name"])
	}
	if !strings.Contains(function["arguments"].(string), "a.txt") {
		t.Errorf("tool arguments = %v, want the parsed input", function["arguments"])
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice["finish_reason"])
	}
	usage := payload["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 120 || usage["completion_tokens"].(float64) != 30 {
		t.Errorf("usage = %#v", usage)
	}
	details := usage["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"].(float64) != 100 {
		t.Errorf("cached_tokens = %v, want 100", details["cached_tokens"])
	}

	// The init pre-requests must precede the generation call.
	generateOnly := false
	for _, record := range host.records("host.log") {
		if path, ok := record.Request["fields"].(map[string]any); ok {
			if _, exists := path["status"]; exists {
				generateOnly = true
			}
		}
	}
	if generateOnly {
		t.Error("an upstream rejection was logged although the call succeeded")
	}
}

func TestHandleExecuteRequestScopedErrorStopsRotation(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EndpointGenerate {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"BAD_REQUEST","message":"tool_choice must be a string"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	raw := executorRequest(t, server.URL, `{"model":"m","messages":[]}`, nil, nil)
	_, err := handleExecute(raw)
	if err == nil {
		t.Fatal("handleExecute() error = nil, want the upstream rejection reported")
	}
	if got := statusOf(err); got != http.StatusBadRequest {
		t.Errorf("StatusCode() = %d, want 400 so CPA never rotates credentials (err=%v)", got, err)
	}
	if !strings.Contains(err.Error(), "BAD_REQUEST") {
		t.Errorf("error = %q, want the upstream code preserved for operators", err.Error())
	}
}

func TestHandleExecuteCredentialScopedErrorAllowsRotation(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	for _, tc := range []struct {
		upstream   int
		wantStatus int
	}{
		{upstream: 429, wantStatus: 429},
		{upstream: 402, wantStatus: 429},
		{upstream: 503, wantStatus: 503},
		{upstream: 500, wantStatus: 502},
		{upstream: 401, wantStatus: 401},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == EndpointGenerate {
				w.WriteHeader(tc.upstream)
				_, _ = w.Write([]byte(`{"error":{"code":"USAGE_EXCEEDED","message":"usage window exhausted"}}`))
				return
			}
			w.WriteHeader(http.StatusOK)
		}))

		raw := executorRequest(t, server.URL, `{"model":"m","messages":[]}`, nil, nil)
		_, err := handleExecute(raw)
		server.Close()
		if err == nil {
			t.Fatalf("upstream %d: error = nil", tc.upstream)
		}
		if got := statusOf(err); got != tc.wantStatus {
			t.Errorf("upstream %d: StatusCode() = %d, want %d (err=%v)", tc.upstream, got, tc.wantStatus, err)
		}
	}
}

func TestHandleExecuteZeroOutputIsRetryable(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	server := upstreamStreamServer(t, []string{
		`{"type":"start"}`,
		`{"type":"finish","finishReason":"length","totalUsage":{"inputTokens":900,"outputTokens":0,"cachedInputTokens":0}}`,
	}, nil)
	defer server.Close()

	raw := executorRequest(t, server.URL, `{"model":"m","messages":[]}`, nil, nil)
	_, err := handleExecute(raw)
	if err == nil {
		t.Fatal("zero-output turn returned success, want a retryable failure")
	}
	if got := statusOf(err); got != http.StatusTooManyRequests {
		t.Errorf("StatusCode() = %d, want 429 (retryable, credential cooldown) (err=%v)", got, err)
	}
}

func TestHandleExecuteStreamEmitsChunks(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	var captured http.Request
	server := upstreamStreamServer(t, []string{
		`{"type":"text-delta","text":"Hel"}`,
		`{"type":"text-delta","text":"lo"}`,
		`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":10,"outputTokens":2,"cachedInputTokens":0}}`,
	}, &captured)
	defer server.Close()

	payload := `{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`
	metadata := map[string]any{"canonical_session_id": "9f1e0f66-3a2c-4d2f-9f0c-3a2c4d2f9f0c"}
	raw, err := json.Marshal(map[string]any{
		"AuthID":       "commandcode-abc.json",
		"AuthProvider": ProviderID,
		"Model":        "deepseek/deepseek-v4-flash",
		"Payload":      []byte(payload),
		"StorageJSON":  []byte(`{"type":"commandcode","api_key":"user_test1234567890","base_url":"` + server.URL + `"}`),
		"Metadata":     metadata,
		"Headers":      http.Header{},
		// The host sends the async stream id as stream_id (rpcExecutorRequest).
		"stream_id": "stream-1",
	})
	if err != nil {
		t.Fatalf("marshal stream request: %v", err)
	}

	frame, err := handleExecuteStream(raw)
	if err != nil {
		t.Fatalf("handleExecuteStream() error = %v", err)
	}
	if !strings.Contains(string(frame), `"ok":true`) {
		t.Fatalf("handshake frame = %s", frame)
	}

	// The async pump runs in a goroutine: wait until the stream is closed.
	deadline := 0
	for len(host.records("host.stream.close")) == 0 && deadline < 200 {
		sleepMillis(10)
		deadline++
	}
	emits := host.records("host.stream.emit")
	if len(emits) < 3 {
		host.mu.Lock()
		methods := []string{}
		for _, record := range host.calls {
			methods = append(methods, record.Method)
		}
		host.mu.Unlock()
		t.Fatalf("stream emits = %d, want the two deltas plus [DONE]; host calls: %v", len(emits), methods)
	}
	first := emits[0].Request
	if first["stream_id"] != "stream-1" {
		t.Errorf("stream_id = %v", first["stream_id"])
	}
	// The host wire carries []byte as base64: decode before asserting.
	joined := ""
	for _, emit := range emits {
		encoded, _ := emit.Request["payload"].(string)
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			joined += string(decoded)
		}
	}
	if !strings.Contains(joined, "Hel") || !strings.Contains(joined, "lo") {
		t.Errorf("emitted chunks = %q, want both deltas", joined)
	}
	if !strings.Contains(joined, "chat.completion.chunk") {
		t.Errorf("emitted chunks are not OpenAI chunks: %q", joined)
	}
	if !strings.Contains(joined, "[DONE]") {
		t.Errorf("emitted chunks are missing the terminator: %q", joined)
	}
	if len(host.records("host.stream.close")) != 1 {
		t.Errorf("stream close calls = %d, want 1", len(host.records("host.stream.close")))
	}
}

func TestExecuteForwardsSessionIdentity(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	var seenSession string
	var seenEnvelope map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EndpointGenerate:
			seenSession = r.Header.Get("x-session-id")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			seenEnvelope = body
			_, _ = w.Write([]byte(`{"type":"text-delta","text":"ok"}` + "\n" +
				`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":1,"outputTokens":1}}` + "\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	payload := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	uuid := "9f1e0f66-3a2c-4d2f-9f0c-3a2c4d2f9f0c"
	raw := executorRequest(t, server.URL, payload, map[string]any{"canonical_session_id": uuid}, nil)
	if _, err := handleExecute(raw); err != nil {
		t.Fatalf("handleExecute() error = %v", err)
	}
	if seenSession != uuid {
		t.Errorf("upstream x-session-id = %q, want the canonical session id", seenSession)
	}
	if seenEnvelope["threadId"] != uuid {
		t.Errorf("envelope threadId = %v, want the canonical session id", seenEnvelope["threadId"])
	}

	// A non-UUID canonical id still reaches upstream as the session header, but
	// never as threadId.
	seenEnvelope = nil
	raw = executorRequest(t, server.URL, payload, map[string]any{"canonical_session_id": "session-abc-123"}, nil)
	if _, err := handleExecute(raw); err != nil {
		t.Fatalf("handleExecute() error = %v", err)
	}
	if seenSession != "session-abc-123" {
		t.Errorf("upstream x-session-id = %q", seenSession)
	}
	if _, exists := seenEnvelope["threadId"]; exists {
		t.Error("threadId must be omitted for a non-UUID session identity")
	}

	// No session identity anywhere: nothing is invented.
	seenSession = "unset"
	seenEnvelope = nil
	raw = executorRequest(t, server.URL, payload, nil, http.Header{})
	if _, err := handleExecute(raw); err != nil {
		t.Fatalf("handleExecute() error = %v", err)
	}
	if seenSession != "" {
		t.Errorf("upstream x-session-id = %q, want no session header when the caller supplied none", seenSession)
	}
	if _, exists := seenEnvelope["threadId"]; exists {
		t.Error("threadId must not be invented")
	}
}

func TestHandleExecuteRejectsForeignCredential(t *testing.T) {
	host := &stubHost{}
	host.install(t)

	raw, _ := json.Marshal(map[string]any{
		"Model":        "m",
		"Payload":      []byte(`{"model":"m","messages":[]}`),
		"StorageJSON":  []byte(`{"type":"codex","access_token":"x"}`),
		"AuthProvider": "codex",
	})
	if _, err := handleExecute(raw); err == nil {
		t.Fatal("handleExecute(foreign credential) = nil error, want a rejection")
	}
}

func TestSessionIDFromRequestPrefersCanonical(t *testing.T) {
	req := &pluginapi.ExecutorRequest{
		Metadata: map[string]any{"canonical_session_id": "canonical-1"},
		Headers:  http.Header{"X-Session-Id": []string{"header-1"}},
	}
	if got := sessionIDFromRequest(req); got != "canonical-1" {
		t.Errorf("sessionIDFromRequest() = %q, want the canonical id", got)
	}
	req = &pluginapi.ExecutorRequest{Headers: http.Header{"X-Client-Request-Id": []string{"header-2"}}}
	if got := sessionIDFromRequest(req); got != "header-2" {
		t.Errorf("sessionIDFromRequest() = %q, want the header fallback", got)
	}
	if got := sessionIDFromRequest(&pluginapi.ExecutorRequest{}); got != "" {
		t.Errorf("sessionIDFromRequest() = %q, want empty", got)
	}
}

// statusOf reads the optional status a plugin error exposes to CPA.
func statusOf(err error) int {
	coder, ok := err.(interface{ StatusCode() int })
	if !ok || coder == nil {
		return 0
	}
	return coder.StatusCode()
}

func sleepMillis(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
