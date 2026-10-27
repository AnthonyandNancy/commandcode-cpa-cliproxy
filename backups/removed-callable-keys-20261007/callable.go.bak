package commandcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// callableKeysFileName is the plugin-owned subset of the panel's api-keys that
// may call CommandCode models. The name must not end in .json: CPA treats every
// *.json file in the auth directory as a credential.
const callableKeysFileName = "commandcode-callable-keys.data"

// callerScopeMetadataKey mirrors sdk/cliproxy/executor.CallerScopeMetadataKey.
// The literal matches the host contract the same way canonical_session_id does
// in executor.go.
const callerScopeMetadataKey = "caller_scope"

// callableKeyEntry stores the facts the runtime needs without the raw key: the
// host's irreversible caller_scope is the matching value, KeyHash is the stable
// id, and Masked is all the page ever shows.
type callableKeyEntry struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	Masked    string `json:"masked"`
	Label     string `json:"label,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type callableKeysFile struct {
	Version int                `json:"version"`
	Keys    []callableKeyEntry `json:"keys"`
}

var (
	callableKeysMu     sync.RWMutex
	callableKeysCache  *callableKeysFile
	callableKeysSource string
	callableKeysStamp  time.Time
)

// callerScopeFor mirrors sdk/cliproxy/session.CallerScope, the host's
// irreversible namespace for one client API key. Contract test:
// TestCallerScopeMatchesHost.
func callerScopeFor(apiKey string) string {
	value := strings.TrimSpace(apiKey)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("cli-proxy-api:caller-scope:v1\x00" + value))
	return hex.EncodeToString(sum[:])
}

// configFilePath returns the host's config.yaml path. It mirrors the host's own
// plugin bootstrap discovery (cmd/server) so the plugin reads exactly the file
// the panel writes: -config/--config flags first, then ./config.yaml.
// Tests replace it to point at a fixture.
var configFilePath = defaultConfigFilePath

func defaultConfigFilePath() string {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-config" || args[i] == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(args[i], "-config="):
			return strings.TrimPrefix(args[i], "-config=")
		case strings.HasPrefix(args[i], "--config="):
			return strings.TrimPrefix(args[i], "--config=")
		}
	}
	if wd, errGetwd := os.Getwd(); errGetwd == nil {
		return filepath.Join(wd, "config.yaml")
	}
	return "config.yaml"
}

type configAPIKeys struct {
	APIKeys []string `yaml:"api-keys"`
	Access  struct {
		APIKeys []string `yaml:"api-keys"`
	} `yaml:"access"`
}

// configuredAPIKeys reads the auth api-keys list the panel manages. Both the
// flat key and the v8 access.api-keys alias are accepted; order is preserved and
// duplicates removed. The keys stay in memory only, never in a URL or log.
func configuredAPIKeys() ([]string, error) {
	path := strings.TrimSpace(configFilePath())
	if path == "" {
		return nil, fmt.Errorf("config path is unknown")
	}
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), errRead)
	}
	var parsed configAPIKeys
	if errUnmarshal := yaml.Unmarshal(data, &parsed); errUnmarshal != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", filepath.Base(path), errUnmarshal)
	}
	seen := map[string]bool{}
	keys := []string{}
	for _, list := range [][]string{parsed.APIKeys, parsed.Access.APIKeys} {
		for _, key := range list {
			key = strings.TrimSpace(key)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func callableKeysPath() (string, error) {
	dir := accountDir()
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("auth directory is unknown")
	}
	return filepath.Join(dir, callableKeysFileName), nil
}

// loadCallableKeys reads the state file with an mtime cache. A missing file is
// an empty list, not an error.
func loadCallableKeys() (*callableKeysFile, error) {
	path, errPath := callableKeysPath()
	if errPath != nil {
		return nil, errPath
	}
	stamp := time.Time{}
	if info, errStat := os.Stat(path); errStat == nil {
		stamp = info.ModTime()
	} else if !os.IsNotExist(errStat) {
		return nil, errStat
	}

	callableKeysMu.RLock()
	if callableKeysCache != nil && callableKeysSource == path && callableKeysStamp.Equal(stamp) {
		file := callableKeysCache
		callableKeysMu.RUnlock()
		return file, nil
	}
	callableKeysMu.RUnlock()

	file := &callableKeysFile{Version: 1, Keys: []callableKeyEntry{}}
	if data, errRead := os.ReadFile(path); errRead == nil {
		if errDecode := json.Unmarshal(data, file); errDecode != nil {
			return nil, fmt.Errorf("callable keys file is unreadable: %w", errDecode)
		}
	} else if !os.IsNotExist(errRead) {
		return nil, errRead
	}
	if file.Keys == nil {
		file.Keys = []callableKeyEntry{}
	}

	callableKeysMu.Lock()
	callableKeysCache = file
	callableKeysSource = path
	callableKeysStamp = stamp
	callableKeysMu.Unlock()
	return file, nil
}

func saveCallableKeys(file *callableKeysFile) error {
	path, errPath := callableKeysPath()
	if errPath != nil {
		return errPath
	}
	file.Version = 1
	if file.Keys == nil {
		file.Keys = []callableKeyEntry{}
	}
	data, errMarshal := json.MarshalIndent(file, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	if errWrite := os.WriteFile(path, append(data, '\n'), 0o600); errWrite != nil {
		return errWrite
	}
	if info, errStat := os.Stat(path); errStat == nil {
		callableKeysMu.Lock()
		callableKeysCache = file
		callableKeysSource = path
		callableKeysStamp = info.ModTime()
		callableKeysMu.Unlock()
	}
	return nil
}

// allowCaller enforces the callable-keys list for requests that reached the
// CommandCode executor.
//
// Policy:
//   - empty list: enforcement is off (backward compatible rollout; the page
//     shows "no restriction" so the state is never silent);
//   - non-empty list: only an enabled entry whose caller_scope matches passes,
//     a request without caller_scope is denied;
//   - the state file cannot be located/parsed: allow and log a warning, because
//     failing closed would take the provider down with the file.
func allowCaller(metadata map[string]any) error {
	scope := ""
	if metadata != nil {
		if value, ok := metadata[callerScopeMetadataKey].(string); ok {
			scope = strings.TrimSpace(value)
		}
	}
	file, errLoad := loadCallableKeys()
	if errLoad != nil {
		Logf("warn", "callable keys unavailable; request allowed", map[string]any{"reason": errLoad.Error()})
		return nil
	}
	if len(file.Keys) == 0 {
		return nil
	}
	if scope == "" {
		return callableKeyDenied()
	}
	for _, entry := range file.Keys {
		if entry.Disabled || entry.Scope == "" {
			continue
		}
		if entry.Scope == scope {
			return nil
		}
	}
	return callableKeyDenied()
}

func callableKeyDenied() error {
	return &downstreamError{
		status:  http.StatusForbidden,
		code:    "callable_key_required",
		message: "this API key is not allowed to call CommandCode models",
	}
}
