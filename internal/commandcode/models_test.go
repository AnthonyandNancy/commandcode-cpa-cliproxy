package commandcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStaticModelsNeverEmpty(t *testing.T) {
	models := StaticModels()
	if len(models) < 10 {
		t.Fatalf("StaticModels() = %d entries, want a usable fallback catalog", len(models))
	}
	byID := map[string]bool{}
	for _, model := range models {
		if model.ID == "" || model.DisplayName == "" {
			t.Errorf("model entry is incomplete: %+v", model)
		}
		if byID[model.ID] {
			t.Errorf("duplicate model id %q", model.ID)
		}
		byID[model.ID] = true
		if model.OwnedBy != ProviderID {
			t.Errorf("model %q OwnedBy = %q", model.ID, model.OwnedBy)
		}
		if len(model.SupportedParameters) == 0 {
			t.Errorf("model %q declares no supported parameters", model.ID)
		}
	}
	if !byID["deepseek/deepseek-v4-flash"] {
		t.Error("the default model is missing from the fallback catalog")
	}
}

func TestModelInfoVisionDeclaration(t *testing.T) {
	vision := modelInfo("xiaomi/mimo-v2.5", "")
	if len(vision.SupportedInputModalities) != 2 {
		t.Errorf("vision model modalities = %v, want text+image", vision.SupportedInputModalities)
	}
	text := modelInfo("deepseek/deepseek-v4-flash", "")
	if len(text.SupportedInputModalities) != 0 {
		t.Errorf("text-only model modalities = %v, want none declared", text.SupportedInputModalities)
	}
}

func TestModelsForAccountFetchesAndCaches(t *testing.T) {
	resetModelCache()
	t.Cleanup(resetModelCache)

	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		if r.URL.Path != EndpointModels {
			t.Errorf("path = %q, want %q", r.URL.Path, EndpointModels)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user_test1234567890" {
			t.Errorf("Authorization = %q, want the credential's own key", got)
		}
		if got := r.Header.Get("x-cli-environment"); got != "production" {
			t.Errorf("x-cli-environment = %q", got)
		}
		if got := r.Header.Get("x-cmd-zdr"); got != "" {
			t.Errorf("x-cmd-zdr = %q, want none on the catalog call", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-v4-flash"},{"id":"claude-sonnet-4-6"}]}`))
	}))
	defer server.Close()

	cfg := DefaultConfig()
	account := newTestAccount(server.URL)

	models, err := ModelsForAccount(context.Background(), account, cfg)
	if err != nil {
		t.Fatalf("ModelsForAccount() error = %v", err)
	}
	if len(models) != 2 || models[0].ID != "deepseek/deepseek-v4-flash" {
		t.Fatalf("models = %#v, want the upstream catalog", models)
	}
	if models[0].Name != "deepseek/deepseek-v4-flash" {
		t.Errorf("model Name = %q, want the upstream id", models[0].Name)
	}

	// Second call inside the TTL must not hit upstream again.
	if _, err := ModelsForAccount(context.Background(), account, cfg); err != nil {
		t.Fatalf("cached ModelsForAccount() error = %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (cached)", got)
	}
}

func TestModelsForAccountFallsBackToStatic(t *testing.T) {
	resetModelCache()
	t.Cleanup(resetModelCache)

	cases := map[string]http.HandlerFunc{
		"http error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"empty list": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) },
		"not json":   func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`nope`)) },
	}
	for name, handler := range cases {
		server := httptest.NewServer(handler)
		models, err := ModelsForAccount(context.Background(), newTestAccount(server.URL), DefaultConfig())
		server.Close()

		if err == nil {
			t.Errorf("%s: error = nil, want the discovery failure reported for logging", name)
		}
		if len(models) != len(StaticModels()) {
			t.Errorf("%s: models = %d, want the static fallback (%d)", name, len(models), len(StaticModels()))
		}
	}
}

func TestHandleModelForAuth(t *testing.T) {
	resetModelCache()
	t.Cleanup(resetModelCache)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.5"}]}`))
	}))
	defer server.Close()

	storage := []byte(`{"type":"commandcode","api_key":"user_x","base_url":"` + server.URL + `"}`)
	request, err := json.Marshal(map[string]any{
		"AuthID":       "commandcode-abc.json",
		"AuthProvider": ProviderID,
		"StorageJSON":  storage, // []byte marshals to base64 on the wire, like the host does
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	frame, err := handleModelForAuth(request)
	if err != nil {
		t.Fatalf("handleModelForAuth() error = %v", err)
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Provider string `json:"Provider"`
			Models   []struct {
				ID string `json:"ID"`
			} `json:"Models"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("frame is not valid JSON: %v", err)
	}
	if !envelope.OK || envelope.Result.Provider != ProviderID {
		t.Fatalf("frame = %s", frame)
	}
	if len(envelope.Result.Models) != 1 || envelope.Result.Models[0].ID != "gpt-5.5" {
		t.Fatalf("models = %#v, want the credential's own catalog", envelope.Result.Models)
	}

	// A foreign credential still gets a non-empty provider.
	foreign, _ := json.Marshal(map[string]any{"StorageJSON": []byte(`{"type":"codex"}`)})
	frame, err = handleModelForAuth(foreign)
	if err != nil {
		t.Fatalf("handleModelForAuth(foreign) error = %v", err)
	}
	if !strings.Contains(string(frame), "deepseek/deepseek-v4-flash") {
		t.Errorf("foreign credential catalog = %s, want the static fallback", frame)
	}
}

func TestClassifyUpstream(t *testing.T) {
	cases := []struct {
		status     int
		downstream int
		scope      int // StatusCode() reported to CPA
		typ        string
	}{
		{status: 400, downstream: 400, scope: 400, typ: "invalid_request_error"},
		{status: 401, downstream: 401, scope: 401, typ: "authentication_error"},
		{status: 402, downstream: 429, scope: 429, typ: "rate_limit_error"},
		{status: 403, downstream: 401, scope: 401, typ: "authentication_error"},
		{status: 404, downstream: 404, scope: 400, typ: "not_found"},
		{status: 422, downstream: 400, scope: 400, typ: "invalid_request_error"},
		{status: 429, downstream: 429, scope: 429, typ: "rate_limit_error"},
		{status: 500, downstream: 502, scope: 502, typ: "upstream_error"},
		{status: 503, downstream: 503, scope: 503, typ: "temporarily_unavailable"},
		{status: 418, downstream: 502, scope: 502, typ: "upstream_error"},
	}
	for _, tc := range cases {
		err := ClassifyUpstream(tc.status, []byte(`{"success":false,"error":{"code":"USAGE_EXCEEDED","message":"no credits"}}`))
		if err.Downstream != tc.downstream {
			t.Errorf("status %d: downstream = %d, want %d", tc.status, err.Downstream, tc.downstream)
		}
		if got := err.StatusCode(); got != tc.scope {
			t.Errorf("status %d: StatusCode() = %d, want %d (request-scoped faults report 400 so CPA never rotates)", tc.status, got, tc.scope)
		}
		if err.Type != tc.typ {
			t.Errorf("status %d: type = %q, want %q", tc.status, err.Type, tc.typ)
		}
		if err.Code != "USAGE_EXCEEDED" {
			t.Errorf("status %d: code = %q, want the upstream code preserved", tc.status, err.Code)
		}
		if err.Message != "no credits" {
			t.Errorf("status %d: message = %q", tc.status, err.Message)
		}
		if err.RequestScoped != (tc.status == 400 || tc.status == 404 || tc.status == 422) {
			t.Errorf("status %d: RequestScoped = %v", tc.status, err.RequestScoped)
		}
	}

	// A non-JSON body is flattened into the message instead of being dropped.
	plain := ClassifyUpstream(502, []byte("  bad\n  gateway  "))
	if plain.Message != "bad gateway" {
		t.Errorf("message = %q, want the flattened body", plain.Message)
	}
}

func TestDoGenerate(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EndpointGenerate {
			t.Errorf("path = %q, want %q", r.URL.Path, EndpointGenerate)
		}
		gotHeaders = r.Header.Clone()
		gotBody, _ = io_ReadAll(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	account := newTestAccount(server.URL)
	profile := DeriveDevice(account.APIKey, "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	resp, err := DoGenerate(context.Background(), account, profile, "session-xyz", []byte(`{"params":{}}`), cfg)
	if err != nil {
		t.Fatalf("DoGenerate() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotHeaders.Get("User-Agent") != "cli" {
		t.Errorf("User-Agent = %q, want cli", gotHeaders.Get("User-Agent"))
	}
	if gotHeaders.Get("accept-encoding") != "identity" {
		t.Errorf("accept-encoding = %q, want identity", gotHeaders.Get("accept-encoding"))
	}
	if gotHeaders.Get("x-co-flag") != "false" {
		t.Errorf("x-co-flag = %q, want false", gotHeaders.Get("x-co-flag"))
	}
	if gotHeaders.Get("Authorization") != "Bearer "+account.APIKey {
		t.Errorf("Authorization = %q", gotHeaders.Get("Authorization"))
	}
	if gotHeaders.Get("x-session-id") != "session-xyz" {
		t.Errorf("x-session-id = %q, want the routed session identity forwarded", gotHeaders.Get("x-session-id"))
	}
	if gotHeaders.Get("x-project-slug") != profile.ProjectSlug() {
		t.Errorf("x-project-slug = %q", gotHeaders.Get("x-project-slug"))
	}
	if gotHeaders.Get("x-taste-learning") != "false" {
		t.Errorf("x-taste-learning = %q, want false", gotHeaders.Get("x-taste-learning"))
	}
	if string(gotBody) != `{"params":{}}` {
		t.Errorf("body = %s, want the envelope preserved", gotBody)
	}
}

func io_ReadAll(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer func() { _ = r.Body.Close() }()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}
