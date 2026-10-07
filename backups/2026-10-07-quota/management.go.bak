package commandcode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// removedSuffix marks a credential file that was taken out of rotation by the
// account page. The scanner only picks up *.json, so a renamed file stops being
// a credential immediately while staying recoverable.
const removedSuffix = ".removed-"

// ManagementResources declares the plugin-owned page. Everything the page needs
// is served from this one route and selected with ?action=..., because sibling
// sub-paths under a plugin resource base are not guaranteed to survive every
// deployment shape (CPA behind a panel proxy, custom reverse proxies, ...). The
// menu route itself is the one path the host always exposes.
func ManagementResources() []pluginapi.ResourceRoute {
	return []pluginapi.ResourceRoute{
		{Path: "/accounts", Menu: "CommandCode 账号 / Accounts", Description: "管理 CommandCode 账号（新增 / 编辑 / 启停 / 删除）"},
	}
}

// managementJSON is the {ok,data}/{ok,error} envelope the page's own fetch calls.
type managementJSON struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// accountView is one row of the account list shown by the page.
type accountView struct {
	File           string `json:"file"`
	AuthIndex      string `json:"auth_index,omitempty"`
	Label          string `json:"label"`
	MaskedKey      string `json:"masked_key,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusMessage  string `json:"status_message,omitempty"`
	Disabled       bool   `json:"disabled"`
	Unavailable    bool   `json:"unavailable"`
	Priority       int    `json:"priority,omitempty"`
	Note           string `json:"note,omitempty"`
	NextRetryAfter string `json:"next_retry_after,omitempty"`
	Success        int64  `json:"success,omitempty"`
	Failed         int64  `json:"failed,omitempty"`
	Removed        bool   `json:"removed,omitempty"`
	RestoreName    string `json:"restore_name,omitempty"`
}

// handleManagementRequest dispatches the plugin's resource endpoints.
func handleManagementRequest(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	path := strings.TrimRight(req.Path, "/")
	cfg := CurrentConfig()
	lang := resolveLang(req, cfg)
	action := strings.ToLower(strings.TrimSpace(req.Query.Get("action")))

	switch {
	case action == "data":
		views, errList := listAccountViews()
		if errList != nil {
			return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errList.Error()})
		}
		return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{
			"accounts":      views,
			"write_enabled": cfg.AllowPageWrite,
			"provider":      ProviderID,
			"version":       Version,
		}})

	case action == "save":
		return handleAccountSave(req, cfg, lang)
	case action == "toggle":
		return handleAccountToggle(req, cfg, lang)
	case action == "remove":
		return handleAccountRemove(req, cfg, lang)
	case action == "restore":
		return handleAccountRestore(req, cfg, lang)
	case action == "purge":
		return handleAccountPurge(req, cfg, lang)

	case action == "" && strings.HasSuffix(path, "/accounts"):
		if strings.EqualFold(strings.TrimSpace(req.Query.Get("format")), "json") {
			views, errList := listAccountViews()
			if errList != nil {
				return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errList.Error()})
			}
			return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: views})
		}
		return htmlResponse(http.StatusOK, AccountsPageHTML(lang))
	}
	return jsonResponse(http.StatusNotFound, managementJSON{OK: false, Error: "unknown action: " + action})
}

// listAccountViews merges the live credentials with the removed files so the
// page can offer restore and purge next to the active accounts.
func listAccountViews() ([]accountView, error) {
	views := []accountView{}
	auths := []pluginapi.HostAuthFileEntry{}
	if err := HostCallJSON("host.auth.list", map[string]any{}, &struct {
		Files *[]pluginapi.HostAuthFileEntry `json:"files"`
	}{Files: &auths}); err != nil {
		return nil, err
	}
	for _, entry := range auths {
		if !strings.EqualFold(strings.TrimSpace(entry.Provider), ProviderID) {
			continue
		}
		view := accountView{
			File:          entry.Name,
			AuthIndex:     entry.AuthIndex,
			Label:         firstNonEmpty(entry.Label, entry.Name),
			Status:        entry.Status,
			StatusMessage: entry.StatusMessage,
			Disabled:      entry.Disabled,
			Unavailable:   entry.Unavailable,
			Priority:      entry.Priority,
			Note:          entry.Note,
			Success:       entry.Success,
			Failed:        entry.Failed,
		}
		if !entry.NextRetryAfter.IsZero() {
			view.NextRetryAfter = entry.NextRetryAfter.UTC().Format(time.RFC3339)
		}
		if raw, errGet := accountRawJSON(entry.AuthIndex); errGet == nil {
			if account, handled, errParse := ParseAccount(raw); errParse == nil && handled {
				view.MaskedKey = MaskKey(account.APIKey)
				// The credential file is the source of truth: the host entry may
				// still carry the label/priority it derived before the last write.
				view.Label = firstNonEmpty(account.Label, view.Label, account.FileNameOrDefault())
				if account.Priority != nil {
					view.Priority = *account.Priority
				}
				if account.Note != "" {
					view.Note = account.Note
				}
				view.Disabled = account.Disabled
			}
		}
		views = append(views, view)
	}

	// Removed (recoverable) files.
	dir := accountDir()
	if dir != "" {
		entries, errRead := os.ReadDir(dir)
		if errRead == nil {
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || !strings.Contains(name, removedSuffix) {
					continue
				}
				if !strings.HasPrefix(name, ProviderID+"-") {
					continue
				}
				base := name[:strings.Index(name, removedSuffix)]
				view := accountView{File: name, Removed: true, RestoreName: base, Status: "removed", Label: base}
				if data, errReadFile := os.ReadFile(filepath.Join(dir, name)); errReadFile == nil {
					if account, handled, errParse := ParseAccount(data); errParse == nil && handled {
						view.MaskedKey = MaskKey(account.APIKey)
						view.Label = firstNonEmpty(account.Label, base)
					}
				}
				views = append(views, view)
			}
		}
	}

	sort.Slice(views, func(i, j int) bool {
		if views[i].Removed != views[j].Removed {
			return !views[i].Removed
		}
		return strings.ToLower(views[i].File) < strings.ToLower(views[j].File)
	})
	return views, nil
}

// lastAccountDir remembers the auth directory across a removal, because a
// removed credential no longer appears in the host's list and its file would
// otherwise become unreachable (no restore, no purge).
var lastAccountDir atomic.Value

// accountDir locates the auth directory. It prefers a credential of this
// provider, falls back to any credential (the directory is shared), and finally
// to the last directory it saw in this process.
func accountDir() string {
	auths := []pluginapi.HostAuthFileEntry{}
	if err := HostCallJSON("host.auth.list", map[string]any{}, &struct {
		Files *[]pluginapi.HostAuthFileEntry `json:"files"`
	}{Files: &auths}); err != nil {
		return cachedAccountDir()
	}
	for _, entry := range auths {
		if !strings.EqualFold(strings.TrimSpace(entry.Provider), ProviderID) {
			continue
		}
		if path := strings.TrimSpace(entry.Path); path != "" {
			return rememberAccountDir(filepath.Dir(path))
		}
	}
	for _, entry := range auths {
		if path := strings.TrimSpace(entry.Path); path != "" {
			return rememberAccountDir(filepath.Dir(path))
		}
	}
	return cachedAccountDir()
}

func rememberAccountDir(dir string) string {
	if strings.TrimSpace(dir) != "" {
		lastAccountDir.Store(dir)
	}
	return dir
}

func cachedAccountDir() string {
	if dir, ok := lastAccountDir.Load().(string); ok {
		return dir
	}
	return ""
}

// accountRawJSON reads one credential's stored JSON by auth index.
func accountRawJSON(authIndex string) ([]byte, error) {
	if strings.TrimSpace(authIndex) == "" {
		return nil, fmt.Errorf("auth index is empty")
	}
	var resp pluginapi.HostAuthGetResponse
	if err := HostCallJSON("host.auth.get", pluginapi.HostAuthGetRequest{AuthIndex: authIndex}, &resp); err != nil {
		return nil, err
	}
	return resp.JSON, nil
}

// handleAccountSave creates or edits one credential. The API key travels in the
// request header so it never lands in a URL, an access log or browser history.
func handleAccountSave(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	apiKey := strings.TrimSpace(req.Headers.Get("X-CommandCode-Api-Key"))
	file := strings.TrimSpace(req.Query.Get("file"))
	label := strings.TrimSpace(req.Query.Get("label"))
	baseURL := strings.TrimRight(strings.TrimSpace(req.Query.Get("base_url")), "/")

	if apiKey == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "key_required")})
	}
	if !strings.HasPrefix(apiKey, "user_") {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "key_prefix")})
	}
	if baseURL != "" && !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "base_url_invalid")})
	}

	// Read-modify-write: an edit keeps every field the page does not own.
	var existing map[string]any
	if file != "" {
		if views, errList := listAccountViews(); errList == nil {
			for _, view := range views {
				if strings.EqualFold(view.File, file) && view.AuthIndex != "" {
					if raw, errGet := accountRawJSON(view.AuthIndex); errGet == nil {
						_ = json.Unmarshal(raw, &existing)
					}
					break
				}
			}
		}
	}

	account := Account{Raw: existing, FileName: file}
	account.APIKey = apiKey
	if label != "" {
		account.Label = label
	}
	account.BaseURL = baseURL

	payload, errRender := AccountJSON(account)
	if errRender != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errRender.Error()})
	}
	if file == "" {
		file = FileNameFor(apiKey)
	}
	if !strings.HasSuffix(strings.ToLower(file), ".json") {
		file = FileNameFor(apiKey)
	}

	var saved pluginapi.HostAuthSaveResponse
	if err := HostCallJSON("host.auth.save", pluginapi.HostAuthSaveRequest{Name: file, JSON: payload}, &saved); err != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: err.Error()})
	}
	if path := strings.TrimSpace(saved.Path); path != "" {
		rememberAccountDir(filepath.Dir(path))
	}
	Logf("info", "commandcode account saved", map[string]any{"file": file, "account": MaskKey(apiKey)})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"file": saved.Name}})
}

// handleAccountToggle flips the host-owned disabled flag.
func handleAccountToggle(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	file := strings.TrimSpace(req.Query.Get("file"))
	disabled := parseBoolQuery(req.Query.Get("disabled"))
	account, file, errLoad := loadAccountByFile(file)
	if errLoad != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errLoad.Error()})
	}
	payload, errRender := DisabledJSON(account, disabled)
	if errRender != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: errRender.Error()})
	}
	var saved pluginapi.HostAuthSaveResponse
	if err := HostCallJSON("host.auth.save", pluginapi.HostAuthSaveRequest{Name: file, JSON: payload}, &saved); err != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: err.Error()})
	}
	Logf("info", "commandcode account toggled", map[string]any{"file": file, "disabled": disabled})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"file": file, "disabled": disabled}})
}

// handleAccountRemove takes a credential out of rotation without deleting it.
func handleAccountRemove(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	file := strings.TrimSpace(req.Query.Get("file"))
	if file == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "file_required")})
	}
	dir := accountDir()
	if dir == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "dir_unknown")})
	}
	source := filepath.Join(dir, filepath.Base(file))
	target := source + removedSuffix + fmt.Sprintf("%d", time.Now().Unix())
	if err := os.Rename(source, target); err != nil {
		if os.IsNotExist(err) {
			return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"file": file, "alreadyRemoved": true}})
		}
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: err.Error()})
	}
	Logf("info", "commandcode account removed (recoverable)", map[string]any{"file": filepath.Base(source)})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{
		"file": filepath.Base(target),
		"note": msg(lang, "remove_note"),
	}})
}

// handleAccountRestore puts a removed credential back into rotation.
func handleAccountRestore(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	file := strings.TrimSpace(req.Query.Get("file"))
	if file == "" || !strings.Contains(file, removedSuffix) {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "file_required")})
	}
	dir := accountDir()
	if dir == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "dir_unknown")})
	}
	source := filepath.Join(dir, filepath.Base(file))
	target := filepath.Join(dir, filepath.Base(file[:strings.Index(filepath.Base(file), removedSuffix)]))
	if err := os.Rename(source, target); err != nil {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: err.Error()})
	}
	Logf("info", "commandcode account restored", map[string]any{"file": filepath.Base(target)})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"file": filepath.Base(target)}})
}

// handleAccountPurge permanently deletes an already-removed credential.
func handleAccountPurge(req pluginapi.ManagementRequest, cfg RuntimeConfig, lang string) ([]byte, error) {
	if !cfg.AllowPageWrite {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "write_disabled")})
	}
	file := strings.TrimSpace(req.Query.Get("file"))
	if file == "" || !strings.Contains(file, removedSuffix) {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "file_required")})
	}
	dir := accountDir()
	if dir == "" {
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: msg(lang, "dir_unknown")})
	}
	target := filepath.Join(dir, filepath.Base(file))
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"file": file, "alreadyPurged": true}})
		}
		return jsonResponse(http.StatusOK, managementJSON{OK: false, Error: err.Error()})
	}
	Logf("info", "commandcode account purged", map[string]any{"file": filepath.Base(file)})
	return jsonResponse(http.StatusOK, managementJSON{OK: true, Data: map[string]any{"file": filepath.Base(file)}})
}

// loadAccountByFile finds a credential by file name and parses it.
func loadAccountByFile(file string) (Account, string, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		return Account{}, "", fmt.Errorf("file is required")
	}
	views, errList := listAccountViews()
	if errList != nil {
		return Account{}, file, errList
	}
	for _, view := range views {
		if !strings.EqualFold(view.File, file) || view.AuthIndex == "" {
			continue
		}
		raw, errGet := accountRawJSON(view.AuthIndex)
		if errGet != nil {
			return Account{}, file, errGet
		}
		account, handled, errParse := ParseAccount(raw)
		if errParse != nil {
			return Account{}, file, errParse
		}
		if !handled {
			return Account{}, file, fmt.Errorf("credential does not belong to the %s provider", ProviderID)
		}
		return account, view.File, nil
	}
	return Account{}, file, fmt.Errorf("credential %s was not found", file)
}

func parseBoolQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func jsonResponse(status int, payload managementJSON) ([]byte, error) {
	body, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}, "Cache-Control": []string{"no-store"}},
		Body:       body,
	}), nil
}

func htmlResponse(status int, body string) ([]byte, error) {
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}},
		Body:       []byte(body),
	}), nil
}

// resolveLang picks the page language: explicit query, then the plugin setting,
// then the browser preference.
func resolveLang(req pluginapi.ManagementRequest, cfg RuntimeConfig) string {
	if value := strings.ToLower(strings.TrimSpace(req.Query.Get("lang"))); value == "zh" || value == "en" {
		return value
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Language)) {
	case "zh", "en":
		return strings.ToLower(strings.TrimSpace(cfg.Language))
	}
	accept := strings.ToLower(req.Headers.Get("Accept-Language"))
	if strings.Contains(accept, "zh") {
		return "zh"
	}
	if strings.Contains(accept, "en") {
		return "en"
	}
	return "zh"
}
