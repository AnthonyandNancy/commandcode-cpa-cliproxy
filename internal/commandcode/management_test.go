package commandcode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// authStubHost emulates the host's credential RPCs over a real temporary auth
// directory, so the page endpoints can be exercised end to end.
type authStubHost struct {
	mu       sync.Mutex
	dir      string
	files    []pluginapi.HostAuthFileEntry
	contents map[string][]byte
	saved    []pluginapi.HostAuthSaveRequest
}

func newAuthStubHost(t *testing.T, files map[string]string) *authStubHost {
	t.Helper()
	dir := t.TempDir()
	stub := &authStubHost{dir: dir, contents: map[string][]byte{}}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
		var doc map[string]any
		_ = json.Unmarshal([]byte(content), &doc)
		provider, _ := doc["type"].(string)
		stub.files = append(stub.files, pluginapi.HostAuthFileEntry{
			ID:        name,
			AuthIndex: name,
			Name:      name,
			Provider:  provider,
			Label:     name,
			Path:      path,
			Status:    "active",
		})
		stub.contents[name] = []byte(content)
	}
	stub.install(t)
	return stub
}

func (s *authStubHost) install(t *testing.T) {
	t.Helper()
	SetHostCaller(func(method string, request []byte) ([]byte, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch method {
		case "host.auth.list":
			// Rebuild the list from disk so renames are visible immediately.
			entries := []pluginapi.HostAuthFileEntry{}
			names, _ := os.ReadDir(s.dir)
			for _, entry := range names {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				path := filepath.Join(s.dir, entry.Name())
				data, _ := os.ReadFile(path)
				var doc map[string]any
				_ = json.Unmarshal(data, &doc)
				provider, _ := doc["type"].(string)
				entries = append(entries, pluginapi.HostAuthFileEntry{
					ID: entry.Name(), AuthIndex: entry.Name(), Name: entry.Name(),
					Provider: provider, Label: entry.Name(), Path: path, Status: "active",
				})
			}
			body, _ := json.Marshal(map[string]any{"files": entries})
			return []byte(`{"ok":true,"result":` + string(body) + `}`), nil
		case "host.auth.get":
			var req pluginapi.HostAuthGetRequest
			_ = json.Unmarshal(request, &req)
			data, ok := s.contents[req.AuthIndex]
			if !ok {
				return []byte(`{"ok":false,"error":{"code":"not_found","message":"no such auth"}}`), nil
			}
			body, _ := json.Marshal(map[string]any{"auth_index": req.AuthIndex, "json": json.RawMessage(data)})
			return []byte(`{"ok":true,"result":` + string(body) + `}`), nil
		case "host.auth.save":
			var req pluginapi.HostAuthSaveRequest
			_ = json.Unmarshal(request, &req)
			s.saved = append(s.saved, req)
			s.contents[req.Name] = req.JSON
			if err := os.WriteFile(filepath.Join(s.dir, req.Name), req.JSON, 0o600); err != nil {
				return nil, err
			}
			body, _ := json.Marshal(map[string]any{"name": req.Name, "path": filepath.Join(s.dir, req.Name)})
			return []byte(`{"ok":true,"result":` + string(body) + `}`), nil
		case "host.log":
			return []byte(`{"ok":true}`), nil
		}
		return []byte(`{"ok":true}`), nil
	})
	t.Cleanup(func() { SetHostCaller(nil) })
}

func managementRequest(method, path string, query map[string]string, headers http.Header) []byte {
	// url.Values on the wire: every query key maps to a list of values.
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	raw, _ := json.Marshal(map[string]any{
		"Method":  method,
		"Path":    path,
		"Headers": headers,
		"Query":   values,
		"Body":    []byte{},
	})
	return raw
}

func decodeManagement(t *testing.T, frame []byte) pluginapi.ManagementResponse {
	t.Helper()
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			StatusCode int         `json:"StatusCode"`
			Body       []byte      `json:"Body"`
			Headers    http.Header `json:"Headers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("management frame is not valid JSON: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("management frame reported a failure: %s", frame)
	}
	return pluginapi.ManagementResponse{
		StatusCode: envelope.Result.StatusCode,
		Body:       envelope.Result.Body,
		Headers:    envelope.Result.Headers,
	}
}

func TestManagementResourcesDeclaration(t *testing.T) {
	resources := ManagementResources()
	if len(resources) == 0 {
		t.Fatal("no resources declared")
	}
	menus := 0
	paths := map[string]bool{}
	for _, resource := range resources {
		paths[resource.Path] = true
		if resource.Menu != "" {
			menus++
		}
	}
	if menus != 1 {
		t.Errorf("menu entries = %d, want exactly one visible page", menus)
	}
	for _, want := range []string{"/accounts", "/accounts/data", "/accounts/save", "/accounts/toggle", "/accounts/remove", "/accounts/restore", "/accounts/purge"} {
		if !paths[want] {
			t.Errorf("resource %q is missing", want)
		}
	}
}

func TestAccountsPageBilingualAndStyled(t *testing.T) {
	stub := newAuthStubHost(t, map[string]string{
		"commandcode-aaaa.json": `{"type":"commandcode","api_key":"user_secret_value_9999"}`,
	})
	_ = stub

	zh := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts", map[string]string{"lang": "zh"}, http.Header{})
	page := string(decodeManagement(t, mustFrame(t, zh)).Body)
	if !strings.Contains(page, "CommandCode 账号") {
		t.Error("zh page is missing its title")
	}
	if !strings.Contains(page, "cpamp") && !strings.Contains(page, "CPAMP") {
		t.Error("zh page does not mention CPAMP credential parity")
	}

	en := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts", map[string]string{"lang": "en"}, http.Header{})
	pageEN := string(decodeManagement(t, mustFrame(t, en)).Body)
	if !strings.Contains(pageEN, "CommandCode Accounts") {
		t.Error("en page is missing its title")
	}

	// The page must follow CPAMP's injected CSS variables and never leak a key.
	for _, token := range []string{"--app-bg", "--primary-color", "--app-surface", "--danger-color"} {
		if !strings.Contains(pageEN, token) {
			t.Errorf("page style does not use the CPAMP variable %s", token)
		}
	}
	if strings.Contains(pageEN, "user_secret_value_9999") {
		t.Error("the page HTML leaks an API key")
	}
}

func TestAccountsDataListsOnlyCommandCode(t *testing.T) {
	stub := newAuthStubHost(t, map[string]string{
		"commandcode-aaaa.json": `{"type":"commandcode","api_key":"user_abcd123456","label":"主号","priority":2,"note":"primary"}`,
		"codex-bbbb.json":       `{"type":"codex","access_token":"x"}`,
	})
	_ = stub

	raw := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/data", map[string]string{"format": "json"}, http.Header{})
	body := decodeManagement(t, mustFrame(t, raw)).Body

	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			Accounts     []accountView `json:"accounts"`
			WriteEnabled bool          `json:"write_enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("data endpoint returned invalid JSON: %v (%s)", err, body)
	}
	if !payload.OK {
		t.Fatalf("data endpoint reported an error: %s", body)
	}
	if len(payload.Data.Accounts) != 1 {
		t.Fatalf("accounts = %d, want only the commandcode credential", len(payload.Data.Accounts))
	}
	account := payload.Data.Accounts[0]
	if account.Label != "主号" {
		t.Errorf("label = %q", account.Label)
	}
	if account.Priority != 2 {
		t.Errorf("priority = %d", account.Priority)
	}
	if !strings.HasSuffix(account.MaskedKey, "3456") || strings.Contains(account.MaskedKey, "abcd") {
		t.Errorf("masked key = %q, want only the last four characters", account.MaskedKey)
	}
	if !payload.Data.WriteEnabled {
		t.Error("write_enabled = false, want the default (page writes allowed)")
	}
}

func TestAccountSaveIsIdempotentAndPreservesHostFields(t *testing.T) {
	key := "user_abcd123456"
	fileName := FileNameFor(key)
	stub := newAuthStubHost(t, map[string]string{
		fileName: `{"type":"commandcode","api_key":"` + key + `","priority":3,"note":"keep","custom":{"a":1}}`,
	})

	headers := http.Header{}
	headers.Set("X-CommandCode-Api-Key", key)
	raw := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/save",
		map[string]string{"file": fileName, "label": "工作号", "base_url": "https://api.commandcode.ai/"}, headers)

	body := decodeManagement(t, mustFrame(t, raw)).Body
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("save failed: %s", body)
	}
	if len(stub.saved) != 1 {
		t.Fatalf("host.auth.save calls = %d, want 1", len(stub.saved))
	}
	saved := stub.saved[0]
	if saved.Name != fileName {
		t.Errorf("saved file = %q, want the deterministic name %q (idempotent re-add)", saved.Name, fileName)
	}
	var doc map[string]any
	if err := json.Unmarshal(saved.JSON, &doc); err != nil {
		t.Fatalf("saved credential is not valid JSON: %v", err)
	}
	if doc["type"] != ProviderID || doc["api_key"] != key {
		t.Errorf("saved credential lost its identity: %v", doc)
	}
	if doc["label"] != "工作号" {
		t.Errorf("label = %v, want the page value", doc["label"])
	}
	if doc["base_url"] != "https://api.commandcode.ai" {
		t.Errorf("base_url = %v, want it trimmed", doc["base_url"])
	}
	for _, field := range []string{"priority", "note", "custom"} {
		if _, ok := doc[field]; !ok {
			t.Errorf("host-owned field %q was dropped by the page write", field)
		}
	}
}

func TestAccountSaveValidationAndWriteSwitch(t *testing.T) {
	stub := newAuthStubHost(t, map[string]string{})
	_ = stub

	headers := http.Header{}
	headers.Set("X-CommandCode-Api-Key", "sk-not-a-commandcode-key")
	raw := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/save", map[string]string{}, headers)
	if body := string(decodeManagement(t, mustFrame(t, raw)).Body); !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "user_") {
		t.Errorf("prefix validation response = %s", body)
	}

	headers.Set("X-CommandCode-Api-Key", "")
	raw = managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/save", map[string]string{}, headers)
	if body := string(decodeManagement(t, mustFrame(t, raw)).Body); !strings.Contains(body, `"ok":false`) {
		t.Errorf("missing-key response = %s", body)
	}

	// With page writes disabled every action is refused and nothing is saved.
	ApplyConfigYAML([]byte("allow-page-write: false"))
	defer ApplyConfigYAML([]byte("allow-page-write: true"))
	savesBefore := len(stub.saved)
	headers.Set("X-CommandCode-Api-Key", "user_abcd123456")
	raw = managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/save", map[string]string{}, headers)
	if body := string(decodeManagement(t, mustFrame(t, raw)).Body); !strings.Contains(body, `"ok":false`) {
		t.Errorf("write-disabled response = %s", body)
	}
	if len(stub.saved) != savesBefore {
		t.Error("write-disabled page still saved a credential")
	}
}

func TestAccountToggleRemoveRestorePurge(t *testing.T) {
	key := "user_abcd123456"
	fileName := FileNameFor(key)
	stub := newAuthStubHost(t, map[string]string{
		fileName: `{"type":"commandcode","api_key":"` + key + `","priority":5}`,
	})

	disable := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/toggle",
		map[string]string{"file": fileName, "disabled": "1"}, http.Header{})
	if body := string(decodeManagement(t, mustFrame(t, disable)).Body); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("toggle failed: %s", body)
	}
	var doc map[string]any
	_ = json.Unmarshal(stub.contents[fileName], &doc)
	if doc["disabled"] != true {
		t.Errorf("disabled = %v, want true", doc["disabled"])
	}
	if doc["priority"] == nil {
		t.Error("toggle dropped the priority field")
	}

	// Remove renames the file out of the scan pattern.
	remove := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/remove",
		map[string]string{"file": fileName}, http.Header{})
	if body := string(decodeManagement(t, mustFrame(t, remove)).Body); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("remove failed: %s", body)
	}
	entries, _ := os.ReadDir(stub.dir)
	removedName := ""
	for _, entry := range entries {
		if strings.Contains(entry.Name(), removedSuffix) {
			removedName = entry.Name()
		}
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Errorf("the credential file is still scanned after removal: %s", entry.Name())
		}
	}
	if removedName == "" {
		t.Fatal("no removed file found")
	}

	// The data endpoint surfaces it as recoverable.
	data := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/data", map[string]string{"format": "json"}, http.Header{})
	body := decodeManagement(t, mustFrame(t, data)).Body
	var payload struct {
		Data struct {
			Accounts []accountView `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &payload)
	found := false
	for _, account := range payload.Data.Accounts {
		if account.Removed && account.File == removedName {
			found = true
			if account.RestoreName != fileName {
				t.Errorf("restore name = %q, want %q", account.RestoreName, fileName)
			}
		}
	}
	if !found {
		t.Fatalf("removed account is missing from the data endpoint: %s", body)
	}

	// Restore brings it back.
	restore := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/restore",
		map[string]string{"file": removedName}, http.Header{})
	if body := string(decodeManagement(t, mustFrame(t, restore)).Body); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("restore failed: %s", body)
	}
	if _, err := os.Stat(filepath.Join(stub.dir, fileName)); err != nil {
		t.Fatalf("restored file is missing: %v", err)
	}

	// Purge removes it for good.
	remove = managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/remove",
		map[string]string{"file": fileName}, http.Header{})
	_ = decodeManagement(t, mustFrame(t, remove))
	purge := managementRequest("GET", "/v0/resource/plugins/commandcode/accounts/purge",
		map[string]string{"file": removedName}, http.Header{})
	if body := string(decodeManagement(t, mustFrame(t, purge)).Body); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("purge failed: %s", body)
	}
	if _, err := os.Stat(filepath.Join(stub.dir, removedName)); !os.IsNotExist(err) {
		t.Error("purged file still exists")
	}
}

func TestUnknownManagementPath(t *testing.T) {
	stub := newAuthStubHost(t, map[string]string{})
	_ = stub
	raw := managementRequest("GET", "/v0/resource/plugins/commandcode/nope", map[string]string{}, http.Header{})
	response := decodeManagement(t, mustFrame(t, raw))
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
}

func mustFrame(t *testing.T, request []byte) []byte {
	t.Helper()
	frame, err := handleManagementRequest(request)
	if err != nil {
		t.Fatalf("handleManagementRequest() error = %v", err)
	}
	return frame
}

var _ = httptest.NewServer
