package commandcode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// TestAcceptance walks the Requirement's acceptance list through the plugin's
// public RPC surface: registration, credential parsing, per-credential model
// catalog, the executor (session pinning + error scoping) and the account page.
func TestAcceptance(t *testing.T) {
	host := &stubHost{}
	host.install(t)
	restoreDefaults(t)
	resetInitState()
	resetModelCache()

	// ---- 1. plugin.register: one provider, chat-completions, management page.
	frame, err := HandleMethod(pluginabi.MethodPluginRegister, []byte(`{"config_yaml":"","schema_version":6}`))
	if err != nil {
		t.Fatalf("plugin.register error = %v", err)
	}
	if !strings.Contains(string(frame), `"executor_model_scope":"both"`) {
		t.Errorf("registration frame = %s", frame)
	}
	if !strings.Contains(string(frame), "CommandCode 账号") && !strings.Contains(string(frame), "ConfigFields") {
		t.Errorf("registration does not declare the account page/config: %s", frame)
	}

	// ---- 2. auth.parse: our credential is claimed, a foreign one is declined.
	credential := `{"type":"commandcode","api_key":"user_acceptance_key_1234","label":"验收账号","priority":1,"note":"keep"}`
	parseFrame, err := HandleMethod(pluginabi.MethodAuthParse, mustJSONRaw(t, map[string]any{
		"Provider": ProviderID,
		"FileName": FileNameFor("user_acceptance_key_1234"),
		"RawJSON":  []byte(credential),
	}))
	if err != nil {
		t.Fatalf("auth.parse error = %v", err)
	}
	if !strings.Contains(string(parseFrame), `"Handled":true`) {
		t.Errorf("auth.parse did not claim a commandcode credential: %s", parseFrame)
	}
	foreignFrame, err := HandleMethod(pluginabi.MethodAuthParse, mustJSONRaw(t, map[string]any{
		"Provider": "codex",
		"RawJSON":  []byte(`{"type":"codex","access_token":"x"}`),
	}))
	if err != nil {
		t.Fatalf("auth.parse(foreign) error = %v", err)
	}
	if !strings.Contains(string(foreignFrame), `"Handled":false`) {
		t.Errorf("auth.parse claimed a foreign credential: %s", foreignFrame)
	}

	// ---- 3+4. executor: session pinning, error scoping, model catalog.
	var seenSession string
	var seenEnvelope map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EndpointModels:
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-v4-flash"},{"id":"xiaomi/mimo-v2.5"}]}`))
			return
		case EndpointGenerate:
			seenSession = r.Header.Get("x-session-id")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			seenEnvelope = body
			_, _ = w.Write([]byte(`{"type":"text-delta","text":"hi"}` + "\n" +
				`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":9,"outputTokens":2,"cachedInputTokens":4}}` + "\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	storage := `{"type":"commandcode","api_key":"user_acceptance_key_1234","base_url":"` + server.URL + `"}`

	// The model catalog is per credential and keeps upstream ids.
	modelFrame, err := HandleMethod(pluginabi.MethodModelForAuth, mustJSONRaw(t, map[string]any{
		"AuthID":      FileNameFor("user_acceptance_key_1234"),
		"StorageJSON": []byte(storage),
	}))
	if err != nil {
		t.Fatalf("model.for_auth error = %v", err)
	}
	if !strings.Contains(string(modelFrame), "xiaomi/mimo-v2.5") {
		t.Errorf("per-credential catalog = %s", modelFrame)
	}

	sessionID := "3d813cbb-47fb-32ba-91df-831e1593ac29"
	execFrame, err := HandleMethod(pluginabi.MethodExecutorExecute, mustJSONRaw(t, map[string]any{
		"AuthID":          FileNameFor("user_acceptance_key_1234"),
		"AuthProvider":    ProviderID,
		"Model":           "deepseek/deepseek-v4-flash",
		"Payload":         []byte(`{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`),
		"OriginalRequest": []byte(`{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`),
		"StorageJSON":     []byte(storage),
		"Metadata":        map[string]any{"canonical_session_id": sessionID},
		"Headers":         http.Header{},
	}))
	if err != nil {
		t.Fatalf("executor.execute error = %v", err)
	}
	if !strings.Contains(string(execFrame), `"ok":true`) {
		t.Fatalf("executor frame = %s", execFrame)
	}
	if seenSession != sessionID || seenEnvelope["threadId"] != sessionID {
		t.Errorf("session identity not pinned upstream: header=%q threadId=%v", seenSession, seenEnvelope["threadId"])
	}
	if seenEnvelope["permissionMode"] != "standard" || seenEnvelope["mode"] != DefaultCLIMode {
		t.Errorf("envelope statics wrong: %v", seenEnvelope)
	}
	params, _ := seenEnvelope["params"].(map[string]any)
	if params["stream"] != true {
		t.Error("upstream request must be streamed")
	}
	config, _ := seenEnvelope["config"].(map[string]any)
	if config["workingDir"] != DefaultProjectDir || config["environment"] != DefaultPlatform {
		t.Errorf("device profile not applied: %v", config)
	}

	// ---- 5. request-level faults never rotate; credential-level faults do.
	rejectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EndpointGenerate {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST","message":"bad tool schema"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer rejectServer.Close()
	_, err = HandleMethod(pluginabi.MethodExecutorExecute, mustJSONRaw(t, map[string]any{
		"Model":       "m",
		"Payload":     []byte(`{"model":"m","messages":[]}`),
		"StorageJSON": []byte(`{"type":"commandcode","api_key":"user_acceptance_key_1234","base_url":"` + rejectServer.URL + `"}`),
	}))
	if got := statusOf(err); got != http.StatusBadRequest {
		t.Errorf("request-level fault reported status %d, want 400 (no credential rotation)", got)
	}

	rateLimitServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EndpointGenerate {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"USAGE_EXCEEDED","message":"window exhausted"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer rateLimitServer.Close()
	_, err = HandleMethod(pluginabi.MethodExecutorExecute, mustJSONRaw(t, map[string]any{
		"Model":       "m",
		"Payload":     []byte(`{"model":"m","messages":[]}`),
		"StorageJSON": []byte(`{"type":"commandcode","api_key":"user_acceptance_key_1234","base_url":"` + rateLimitServer.URL + `"}`),
	}))
	if got := statusOf(err); got != http.StatusTooManyRequests {
		t.Errorf("credential-level fault reported status %d, want 429 (cooldown + rotate)", got)
	}

	// ---- 6. account page: menu declared, bilingual, CPAMP-styled, key masked.
	menuFrame, err := HandleMethod(pluginabi.MethodManagementRegister, []byte(`{}`))
	if err != nil {
		t.Fatalf("management.register error = %v", err)
	}
	if !strings.Contains(string(menuFrame), "/accounts") {
		t.Errorf("management.register frame = %s", menuFrame)
	}

	page := string(decodeManagement(t, mustFrame(t, managementRequest(
		"GET", "/v0/resource/plugins/commandcode/accounts", map[string]string{"lang": "en"}, http.Header{}))).Body)
	if !strings.Contains(page, "CommandCode Accounts") || !strings.Contains(page, "--app-surface") {
		t.Error("account page is not bilingual / CPAMP-styled")
	}
	if strings.Contains(page, "user_acceptance_key_1234") {
		t.Error("account page leaks a key")
	}

	// ---- 7. unchanged behaviour: unknown RPCs are rejected, not silently ok.
	if _, err := HandleMethod("no.such.method", nil); err == nil {
		t.Error("unknown method did not fail")
	}
}

func mustJSONRaw(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
