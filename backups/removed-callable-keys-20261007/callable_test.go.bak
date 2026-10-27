package commandcode

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// withConfigFile points the plugin at a fixture config.yaml for one test.
func withConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	previous := configFilePath
	configFilePath = func() string { return path }
	t.Cleanup(func() { configFilePath = previous })
	return path
}

// callableHeader builds the header the page sends. Header.Set canonicalizes the
// key the same way net/http does on the live path, so Header.Get finds it.
func callableHeader(key string) http.Header {
	headers := http.Header{}
	headers.Set("X-CommandCode-Callable-Key", key)
	return headers
}

func TestCallerScopeMatchesHostVector(t *testing.T) {
	// Vector pinned from the host algorithm in sdk/cliproxy/session.CallerScope:
	// hex(sha256("cli-proxy-api:caller-scope:v1\x00" + key)). Importing that SDK
	// package would pull gin into this plugin's go.sum, which the plan's global
	// constraint forbids; the pinned digest keeps the same contract.
	const want = "645081fcc27fdc374fa19d452d5d5458b87ae5bbf30817920b497babdd4c1607"
	if got := callerScopeFor("user_contract_key"); got != want {
		t.Errorf("callerScopeFor() = %q, want %q", got, want)
	}
	if got := callerScopeFor("  user_contract_key  "); got != want {
		t.Errorf("callerScopeFor(trimmed) = %q, want %q", got, want)
	}
	if got := callerScopeFor(""); got != "" {
		t.Errorf("callerScopeFor(empty) = %q, want empty", got)
	}
}

func TestConfiguredAPIKeysReadsBothLayouts(t *testing.T) {
	withConfigFile(t, "api-keys:\n  - sk-flat\n  - sk-shared\naccess:\n  api-keys:\n    - sk-nested\n    - sk-shared\n")
	keys, err := configuredAPIKeys()
	if err != nil {
		t.Fatalf("configuredAPIKeys error = %v", err)
	}
	want := []string{"sk-flat", "sk-shared", "sk-nested"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
}

func TestConfiguredAPIKeysMissingFile(t *testing.T) {
	previous := configFilePath
	configFilePath = func() string { return filepath.Join(t.TempDir(), "missing.yaml") }
	t.Cleanup(func() { configFilePath = previous })
	if _, err := configuredAPIKeys(); err == nil {
		t.Fatal("a missing config file must be reported")
	}
}

func TestDefaultConfigFilePathParsesArgs(t *testing.T) {
	previous := os.Args
	os.Args = []string{"cpa", "--config", "/tmp/panel/config.yaml"}
	t.Cleanup(func() { os.Args = previous })
	if got := defaultConfigFilePath(); got != "/tmp/panel/config.yaml" {
		t.Fatalf("defaultConfigFilePath() = %q", got)
	}
}

func TestAllowCallerPolicy(t *testing.T) {
	restoreDefaults(t)
	newAuthStubHost(t, map[string]string{"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`})

	if err := allowCaller(nil); err != nil {
		t.Fatalf("an empty list must not restrict: %v", err)
	}

	allowed := callerScopeFor("sk-allowed")
	if err := saveCallableKeys(&callableKeysFile{Keys: []callableKeyEntry{
		{ID: KeyHash("sk-allowed"), Scope: allowed, Masked: MaskKey("sk-allowed")},
		{ID: KeyHash("sk-disabled"), Scope: callerScopeFor("sk-disabled"), Masked: MaskKey("sk-disabled"), Disabled: true},
	}}); err != nil {
		t.Fatalf("save callable keys: %v", err)
	}

	if err := allowCaller(map[string]any{callerScopeMetadataKey: allowed}); err != nil {
		t.Errorf("the allowed scope must pass: %v", err)
	}
	if err := allowCaller(map[string]any{callerScopeMetadataKey: callerScopeFor("sk-disabled")}); err == nil {
		t.Error("a disabled entry must be denied")
	}
	if err := allowCaller(map[string]any{callerScopeMetadataKey: callerScopeFor("sk-other")}); err == nil {
		t.Error("an unknown scope must be denied")
	}
	if err := allowCaller(nil); err == nil {
		t.Error("a missing caller_scope must be denied while the list is non-empty")
	}
	if err := allowCaller(map[string]any{}); err == nil {
		t.Error("an empty metadata map must be denied while the list is non-empty")
	}
}

func TestExecutorDeniesNonCallableKey(t *testing.T) {
	restoreDefaults(t)
	resetInitState()
	newAuthStubHost(t, map[string]string{"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`})
	if err := saveCallableKeys(&callableKeysFile{Keys: []callableKeyEntry{
		{ID: KeyHash("sk-allowed"), Scope: callerScopeFor("sk-allowed"), Masked: MaskKey("sk-allowed")},
	}}); err != nil {
		t.Fatalf("save callable keys: %v", err)
	}

	_, err := HandleMethod(pluginabi.MethodExecutorExecute, mustJSONRaw(t, map[string]any{
		"AuthID":          "commandcode-abc.json",
		"AuthProvider":    ProviderID,
		"Model":           "m",
		"Payload":         []byte(`{"model":"m","messages":[]}`),
		"OriginalRequest": []byte(`{"model":"m","messages":[]}`),
		"StorageJSON":     []byte(`{"type":"commandcode","api_key":"user_test1234567890"}`),
		"Metadata":        map[string]any{callerScopeMetadataKey: callerScopeFor("sk-other")},
		"Headers":         http.Header{},
	}))
	if got := statusOf(err); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-callable key", got)
	}
}

// decodeManagementOK reads the {ok,error} envelope shared by the page actions.
func decodeManagementOK(t *testing.T, response pluginapi.ManagementResponse) bool {
	t.Helper()
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("management body decode: %v", err)
	}
	return payload.OK
}

func TestCallableKeysManagementCRUD(t *testing.T) {
	restoreDefaults(t)
	newAuthStubHost(t, map[string]string{"commandcode-abc.json": `{"type":"commandcode","api_key":"user_test1234567890"}`})
	withConfigFile(t, "api-keys:\n  - sk-known\n  - sk-other\n")

	// List: both panel keys are candidates, none allowed yet.
	listResponse := decodeManagement(t, mustFrame(t, accountAction("keys", nil, nil)))
	var list struct {
		OK   bool `json:"ok"`
		Data struct {
			SourceError string `json:"source_error"`
			Keys        []struct {
				ID       string `json:"id"`
				Masked   string `json:"masked"`
				Allowed  bool   `json:"allowed"`
				Stale    bool   `json:"stale"`
				Disabled bool   `json:"disabled"`
			} `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listResponse.Body, &list); err != nil {
		t.Fatalf("keys body decode: %v", err)
	}
	if !list.OK || list.Data.SourceError != "" {
		t.Fatalf("keys payload = %s", listResponse.Body)
	}
	if len(list.Data.Keys) != 2 || list.Data.Keys[0].Allowed {
		t.Fatalf("keys = %+v, want two candidates, none allowed", list.Data.Keys)
	}
	if list.Data.Keys[0].Masked == "sk-known" {
		t.Fatal("the page must only ever see the masked key")
	}
	id := list.Data.Keys[0].ID

	// Add: the raw key travels in the header, never in the URL.
	add := decodeManagement(t, mustFrame(t, accountAction("key_add", map[string]string{"label": "laptop"},
		callableHeader("sk-known"))))
	if !decodeManagementOK(t, add) {
		t.Fatalf("key_add failed: %s", add.Body)
	}

	// A key outside the panel config is rejected.
	rejected := decodeManagement(t, mustFrame(t, accountAction("key_add", nil,
		callableHeader("sk-unknown"))))
	if decodeManagementOK(t, rejected) {
		t.Fatalf("key_add accepted an unknown key: %s", rejected.Body)
	}

	// Update: disable the entry.
	if !decodeManagementOK(t, decodeManagement(t, mustFrame(t, accountAction("key_update", map[string]string{"id": id, "disabled": "1"}, nil)))) {
		t.Fatal("key_update failed")
	}

	// Delete: the entry disappears from the allowed set.
	if !decodeManagementOK(t, decodeManagement(t, mustFrame(t, accountAction("key_delete", map[string]string{"id": id}, nil)))) {
		t.Fatal("key_delete failed")
	}
	after := decodeManagement(t, mustFrame(t, accountAction("keys", nil, nil)))
	if err := json.Unmarshal(after.Body, &list); err != nil {
		t.Fatalf("keys body decode: %v", err)
	}
	for _, entry := range list.Data.Keys {
		if entry.ID == id && entry.Allowed {
			t.Fatalf("deleted key is still allowed: %+v", entry)
		}
	}

	// Writes honour allow-page-write=false.
	ApplyConfigYAML([]byte("allow-page-write: false\n"))
	blocked := decodeManagement(t, mustFrame(t, accountAction("key_add", nil,
		callableHeader("sk-known"))))
	if decodeManagementOK(t, blocked) {
		t.Fatal("key_add must be rejected while writes are disabled")
	}
}
