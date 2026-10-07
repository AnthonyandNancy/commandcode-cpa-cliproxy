package commandcode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestAccountParseAndNaming(t *testing.T) {
	raw := []byte(`{"type":"commandcode","api_key":"user_abc123456789","label":"工作号","priority":2,"note":"primary"}`)

	account, handled, err := ParseAccountFile(raw, "commandcode-ffffffffffffffff.json")
	if !handled {
		t.Fatal("ParseAccountFile() did not claim a commandcode credential")
	}
	if err != nil {
		t.Fatalf("ParseAccountFile() error = %v", err)
	}
	if account.APIKey != "user_abc123456789" {
		t.Errorf("APIKey = %q", account.APIKey)
	}
	if account.LabelOrDefault() != "工作号" {
		t.Errorf("LabelOrDefault() = %q, want the file label", account.LabelOrDefault())
	}
	if account.Priority == nil || *account.Priority != 2 {
		t.Errorf("Priority = %v, want 2", account.Priority)
	}
	if account.AuthID() != "commandcode-ffffffffffffffff.json" {
		t.Errorf("AuthID() = %q, want the host's file name", account.AuthID())
	}

	// Same key -> same file name (idempotent re-add), different key -> different name.
	if got, want := FileNameFor("user_abc123456789"), FileNameFor("user_abc123456789"); got != want {
		t.Fatalf("FileNameFor is not deterministic: %q vs %q", got, want)
	}
	if FileNameFor("user_abc123456789") == FileNameFor("user_zzz999") {
		t.Fatal("FileNameFor collided for different keys")
	}
	derived := Account{APIKey: "user_abc123456789"}
	if derived.FileNameOrDefault() != FileNameFor("user_abc123456789") {
		t.Errorf("FileNameOrDefault() = %q", derived.FileNameOrDefault())
	}
	if !strings.HasPrefix(derived.FileNameOrDefault(), ProviderID+"-") {
		t.Errorf("file name %q must be prefixed with the provider key", derived.FileNameOrDefault())
	}
}

func TestAccountNotClaimed(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"type":"codex","access_token":"x"}`),
		[]byte(`{"api_key":"user_x"}`),
		[]byte(``),
	} {
		if _, handled, err := ParseAccount(raw); handled || err != nil {
			t.Errorf("ParseAccount(%s) = handled %v, err %v; want handled=false", raw, handled, err)
		}
	}
}

func TestAccountRejectsUnusableFile(t *testing.T) {
	cases := map[string]string{
		"missing api_key": `{"type":"commandcode"}`,
		"bad prefix":      `{"type":"commandcode","api_key":"sk-123"}`,
	}
	for name, raw := range cases {
		_, handled, err := ParseAccount([]byte(raw))
		if !handled {
			t.Errorf("%s: handled = false, want true (the file is ours but broken)", name)
		}
		if err == nil {
			t.Errorf("%s: err = nil, want an error", name)
		}
	}
}

func TestMaskKey(t *testing.T) {
	if got := MaskKey("user_abcdef123456"); !strings.HasSuffix(got, "3456") || strings.Contains(got, "abcdef") {
		t.Errorf("MaskKey() = %q, want only the last four characters visible", got)
	}
	if got := MaskKey(""); got != "" {
		t.Errorf("MaskKey(\"\") = %q, want empty", got)
	}
}

func TestAccountJSONPreservesHostFields(t *testing.T) {
	raw := []byte(`{"type":"commandcode","api_key":"user_old","label":"旧","priority":3,"weight":2,"note":"keep me","custom":{"a":1}}`)
	account, handled, err := ParseAccount(raw)
	if !handled || err != nil {
		t.Fatalf("ParseAccount() = %v, %v", handled, err)
	}
	account.APIKey = "user_new"
	account.Label = "新"
	account.BaseURL = "https://example.test/"

	encoded, err := AccountJSON(account)
	if err != nil {
		t.Fatalf("AccountJSON() error = %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatalf("AccountJSON() produced invalid JSON: %v", err)
	}
	if doc["type"] != ProviderID {
		t.Errorf("type = %v, want %q (must never be dropped)", doc["type"], ProviderID)
	}
	if doc["api_key"] != "user_new" || doc["label"] != "新" {
		t.Errorf("plugin-owned fields not updated: %v", doc)
	}
	if doc["base_url"] != "https://example.test" {
		t.Errorf("base_url = %v, want it trimmed", doc["base_url"])
	}
	for _, key := range []string{"priority", "weight", "note", "custom"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("host-owned key %q was dropped by a rewrite", key)
		}
	}
}

func TestDisabledJSONTogglesOnlyDisabled(t *testing.T) {
	raw := []byte(`{"type":"commandcode","api_key":"user_x","priority":5}`)
	account, _, err := ParseAccount(raw)
	if err != nil {
		t.Fatalf("ParseAccount() error = %v", err)
	}
	encoded, err := DisabledJSON(account, true)
	if err != nil {
		t.Fatalf("DisabledJSON() error = %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatalf("DisabledJSON() produced invalid JSON: %v", err)
	}
	if doc["disabled"] != true {
		t.Errorf("disabled = %v, want true", doc["disabled"])
	}
	if doc["priority"] == nil || doc["api_key"] != "user_x" {
		t.Errorf("DisabledJSON() touched unrelated keys: %v", doc)
	}
}

func TestAuthDataFor(t *testing.T) {
	raw := []byte(`{"type":"commandcode","api_key":"user_abc123456789"}`)
	account, _, err := ParseAccount(raw)
	if err != nil {
		t.Fatalf("ParseAccount() error = %v", err)
	}
	data := AuthDataFor(account, raw)
	if data.Provider != ProviderID {
		t.Errorf("Provider = %q", data.Provider)
	}
	if data.ID != data.FileName || data.ID != FileNameFor(account.APIKey) {
		t.Errorf("ID/FileName = %q/%q, want the deterministic file name", data.ID, data.FileName)
	}
	if data.Label == "" {
		t.Error("Label is empty")
	}
	if len(data.StorageJSON) == 0 {
		t.Error("StorageJSON is empty")
	}
	if data.Metadata["type"] != ProviderID {
		t.Errorf("Metadata[type] = %v", data.Metadata["type"])
	}
	if _, ok := any(data).(pluginapi.AuthData); !ok {
		t.Error("AuthDataFor must return pluginapi.AuthData")
	}
}
