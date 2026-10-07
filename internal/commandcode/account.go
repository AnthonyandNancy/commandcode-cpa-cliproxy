package commandcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Account is one CommandCode credential. The auth file is the single source of
// truth: fields the host owns (type/disabled/priority/weight/note/prefix/
// proxy_url) stay in Raw and are never dropped by a rewrite, and only the keys
// this plugin owns are overwritten.
type Account struct {
	APIKey   string
	BaseURL  string
	Label    string
	Disabled bool
	Priority *int
	Weight   *int
	Note     string

	// Optional per-account overrides of the global device profile.
	ProjectDir      string
	FingerprintSalt string
	LifecycleMode   string

	// FileName is the credential file name the host reported, when known.
	FileName string

	// Raw is the whole credential file, kept for read-modify-write.
	Raw map[string]any
}

// ParseAccount recognises and validates one credential file. handled reports
// whether the file belongs to this plugin ("type": "commandcode"); an error
// means the file is ours but unusable.
func ParseAccount(raw []byte) (Account, bool, error) {
	return ParseAccountFile(raw, "")
}

// ParseAccountFile is ParseAccount with the credential file name the host used.
func ParseAccountFile(raw []byte, fileName string) (Account, bool, error) {
	if len(raw) == 0 {
		return Account{}, false, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Account{}, false, fmt.Errorf("invalid credential json: %w", err)
	}
	if !strings.EqualFold(stringValue(doc["type"]), ProviderID) {
		// Another provider's file: let the host ask the next plugin.
		return Account{}, false, nil
	}

	account := Account{Raw: doc, FileName: strings.TrimSpace(fileName)}
	account.APIKey = strings.TrimSpace(stringValue(doc["api_key"]))
	if account.APIKey == "" {
		return account, true, fmt.Errorf("credential file is missing api_key")
	}
	if !strings.HasPrefix(account.APIKey, "user_") {
		return account, true, fmt.Errorf("api_key must start with user_")
	}
	account.BaseURL = strings.TrimRight(strings.TrimSpace(stringValue(doc["base_url"])), "/")
	account.Label = strings.TrimSpace(stringValue(doc["label"]))
	account.Disabled = boolValue(doc["disabled"])
	account.Priority = intPointer(doc["priority"])
	account.Weight = intPointer(doc["weight"])
	account.Note = strings.TrimSpace(stringValue(doc["note"]))
	account.ProjectDir = strings.TrimSpace(stringValue(doc["project_dir"]))
	account.FingerprintSalt = strings.TrimSpace(stringValue(doc["fingerprint_salt"]))
	account.LifecycleMode = strings.TrimSpace(stringValue(doc["lifecycle_mode"]))
	return account, true, nil
}

// KeyHash is the stable, non-reversible account identifier used in file names.
func KeyHash(apiKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
	return hex.EncodeToString(sum[:])[:16]
}

// FileNameFor returns the deterministic credential file name for an API key.
// Deterministic naming makes re-adding the same account idempotent: the same key
// can never produce a second credential.
func FileNameFor(apiKey string) string {
	return ProviderID + "-" + KeyHash(apiKey) + ".json"
}

// FileNameOrDefault returns the credential file name, deriving it when the host
// did not report one (the plugin page asks for it before the file exists).
func (a Account) FileNameOrDefault() string {
	if name := strings.TrimSpace(a.FileName); name != "" {
		return name
	}
	if a.APIKey == "" {
		return ""
	}
	return FileNameFor(a.APIKey)
}

// AuthID mirrors the host's own id for the same file (the file name relative to
// the auth directory), so both credential load paths agree on one identity.
func (a Account) AuthID() string {
	return a.FileNameOrDefault()
}

// LabelOrDefault is the human-readable credential label.
func (a Account) LabelOrDefault() string {
	if label := strings.TrimSpace(a.Label); label != "" {
		return label
	}
	return ProviderID + " · " + KeyHash(a.APIKey)
}

// ResolvedBaseURL returns the account's upstream base, falling back to config.
func (a Account) ResolvedBaseURL(cfg RuntimeConfig) string {
	if base := strings.TrimSpace(a.BaseURL); base != "" {
		return base
	}
	return cfg.BaseURL
}

// DeviceSalt returns the salt used to derive this account's device identity.
func (a Account) DeviceSalt(cfg RuntimeConfig) string {
	if salt := strings.TrimSpace(a.FingerprintSalt); salt != "" {
		return salt
	}
	return cfg.FingerprintSalt
}

// ProjectDirValue returns the faked project directory for this account.
func (a Account) ProjectDirValue(cfg RuntimeConfig) string {
	if dir := strings.TrimSpace(a.ProjectDir); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(cfg.ProjectDir); dir != "" {
		return dir
	}
	return DefaultProjectDir
}

// LifecycleModeValue returns the lifecycle metadata mode for this account.
func (a Account) LifecycleModeValue(cfg RuntimeConfig) string {
	if mode := strings.TrimSpace(a.LifecycleMode); mode != "" {
		return mode
	}
	if mode := strings.TrimSpace(cfg.LifecycleMode); mode != "" {
		return mode
	}
	return DefaultLifecycleMode
}

// MaskKey hides everything but the last four characters of a credential.
func MaskKey(apiKey string) string {
	key := strings.TrimSpace(apiKey)
	if len(key) <= 4 {
		if key == "" {
			return ""
		}
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", 4) + key[len(key)-4:]
}

// AccountJSON renders the credential file for a new or edited account. Host-owned
// keys present in Raw (disabled / priority / weight / note / prefix / proxy_url)
// are preserved verbatim; only the plugin-owned keys are written.
func AccountJSON(a Account) ([]byte, error) {
	doc := cloneMap(a.Raw)
	if doc == nil {
		doc = map[string]any{}
	}
	if a.APIKey == "" {
		return nil, fmt.Errorf("api_key is required")
	}
	doc["type"] = ProviderID
	doc["api_key"] = strings.TrimSpace(a.APIKey)
	if label := strings.TrimSpace(a.Label); label != "" {
		doc["label"] = label
	}
	if base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/"); base != "" {
		doc["base_url"] = base
	} else {
		delete(doc, "base_url")
	}
	return json.Marshal(doc)
}

// DisabledJSON flips the host-owned disabled flag without touching anything else.
func DisabledJSON(a Account, disabled bool) ([]byte, error) {
	if a.APIKey == "" && len(a.Raw) == 0 {
		return nil, fmt.Errorf("credential is empty")
	}
	doc := cloneMap(a.Raw)
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["type"]; !ok {
		doc["type"] = ProviderID
	}
	doc["disabled"] = disabled
	return json.Marshal(doc)
}

// AuthDataFor converts an account into the host's auth record description.
func AuthDataFor(a Account, raw []byte) pluginapi.AuthData {
	storage := raw
	if len(storage) == 0 {
		if encoded, err := json.Marshal(a.Raw); err == nil {
			storage = encoded
		}
	}
	return pluginapi.AuthData{
		Provider:    ProviderID,
		ID:          a.AuthID(),
		FileName:    a.FileNameOrDefault(),
		Label:       a.LabelOrDefault(),
		StorageJSON: storage,
		Metadata:    map[string]any{"type": ProviderID},
	}
}

// handleAuthParse implements auth.parse: it claims credentials whose type is
// "commandcode" and hands the host their auth record. Files belonging to other
// providers are declined so the host can ask the next plugin. A file we own but
// cannot use is claimed with no record, which makes the host ignore it instead
// of loading a broken credential.
func handleAuthParse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	account, handled, err := ParseAccountFile(req.RawJSON, req.FileName)
	if !handled {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false}), nil
	}
	if err != nil {
		Logf("warn", "commandcode credential rejected", map[string]any{
			"file":   account.FileNameOrDefault(),
			"reason": err.Error(),
		})
		return okEnvelope(pluginapi.AuthParseResponse{Handled: true}), nil
	}
	return okEnvelope(pluginapi.AuthParseResponse{Handled: true, Auth: AuthDataFor(account, req.RawJSON)}), nil
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", typed), "0"), ".")
	case nil:
		return ""
	default:
		return ""
	}
}

func boolValue(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func intPointer(v any) *int {
	switch typed := v.(type) {
	case float64:
		value := int(typed)
		return &value
	case int:
		value := typed
		return &value
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			value := int(parsed)
			return &value
		}
	}
	return nil
}
