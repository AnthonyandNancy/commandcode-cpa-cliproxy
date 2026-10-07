package commandcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// staticModelList is the fallback catalog. It mirrors the model list
// commandcode-proxy ships with, so a failed discovery never leaves the provider
// empty. The live per-credential catalog always wins when it can be fetched.
var staticModelList = []struct {
	ID   string
	Name string
}{
	{"claude-sonnet-4-6", "Claude Sonnet 4.6"},
	{"claude-opus-4-8", "Claude Opus 4.8"},
	{"claude-opus-4-7", "Claude Opus 4.7"},
	{"claude-haiku-4-5-20251001", "Claude Haiku 4.5"},
	{"gpt-5.5", "GPT-5.5"},
	{"gpt-5.4", "GPT-5.4"},
	{"gpt-5.4-mini", "GPT-5.4 Mini"},
	{"gpt-5.3-codex", "GPT-5.3 Codex"},
	{"deepseek/deepseek-v4-pro", "DeepSeek V4 Pro"},
	{"deepseek/deepseek-v4-flash", "DeepSeek V4 Flash"},
	{"moonshotai/Kimi-K2.6", "Kimi K2.6"},
	{"moonshotai/Kimi-K2.5", "Kimi K2.5"},
	{"zai-org/GLM-5.1", "GLM 5.1"},
	{"zai-org/GLM-5", "GLM 5"},
	{"MiniMaxAI/MiniMax-M3", "MiniMax M3"},
	{"MiniMaxAI/MiniMax-M2.7", "MiniMax M2.7"},
	{"MiniMaxAI/MiniMax-M2.5", "MiniMax M2.5"},
	{"Qwen/Qwen3.6-Max-Preview", "Qwen 3.6 Max Preview"},
	{"Qwen/Qwen3.6-Plus", "Qwen 3.6 Plus"},
	{"Qwen/Qwen3.7-Max", "Qwen 3.7 Max"},
	{"stepfun/Step-3.7-Flash", "Step 3.7 Flash"},
	{"stepfun/Step-3.5-Flash", "Step 3.5 Flash"},
	{"xiaomi/mimo-v2.5-pro", "MiMo V2.5 Pro"},
	{"xiaomi/mimo-v2.5", "MiMo V2.5"},
	{"google/gemini-3.5-flash", "Gemini 3.5 Flash"},
	{"google/gemini-3.1-flash-lite", "Gemini 3.1 Flash Lite"},
}

// visionModelHints lists the families known to accept images. Everything else
// stays text-only so the model picker never promises vision it cannot deliver.
var visionModelHints = []string{"mimo-v2.5", "kimi-k2.5", "gemini", "claude-sonnet-4", "claude-opus-4"}

// StaticModels returns the fallback catalog.
func StaticModels() []pluginapi.ModelInfo {
	models := make([]pluginapi.ModelInfo, 0, len(staticModelList))
	for _, entry := range staticModelList {
		models = append(models, modelInfo(entry.ID, entry.Name))
	}
	return models
}

// modelInfo renders one model entry with the capabilities the CommandCode API
// guarantees: every model is tool-capable (the CLI always sends tools), images
// only for the known vision families.
func modelInfo(id, displayName string) pluginapi.ModelInfo {
	info := pluginapi.ModelInfo{
		ID:                  id,
		Object:              "model",
		OwnedBy:             ProviderID,
		Name:                id,
		DisplayName:         firstNonEmpty(displayName, id),
		Type:                "chat",
		SupportedParameters: []string{"tools", "tool_choice", "reasoning_effort"},
	}
	if supportsVision(id) {
		info.SupportedInputModalities = []string{"text", "image"}
	}
	return info
}

func supportsVision(modelID string) bool {
	lower := strings.ToLower(modelID)
	for _, hint := range visionModelHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// modelCacheEntry caches one credential's catalog.
type modelCacheEntry struct {
	models    []pluginapi.ModelInfo
	expiresAt time.Time
}

var (
	modelCacheMu sync.Mutex
	modelCache   = map[string]modelCacheEntry{}
)

// resetModelCache clears the catalog cache (tests and credential edits).
func resetModelCache() {
	modelCacheMu.Lock()
	modelCache = map[string]modelCacheEntry{}
	modelCacheMu.Unlock()
}

// ModelsForAccount returns the catalog this credential can actually use, asking
// upstream and falling back to the static list on any failure. The returned
// error is informational: the static list is always usable.
func ModelsForAccount(ctx context.Context, account Account, cfg RuntimeConfig) ([]pluginapi.ModelInfo, error) {
	key := KeyHash(account.APIKey)
	if key == "" {
		return StaticModels(), fmt.Errorf("account has no api key")
	}
	if ttl := cfg.ModelCacheTTL; ttl > 0 {
		modelCacheMu.Lock()
		if entry, ok := modelCache[key]; ok && time.Now().Before(entry.expiresAt) {
			models := entry.models
			modelCacheMu.Unlock()
			return models, nil
		}
		modelCacheMu.Unlock()
	}

	models, err := fetchUpstreamModels(ctx, account, cfg)
	if err != nil || len(models) == 0 {
		if err == nil {
			err = fmt.Errorf("upstream returned an empty model list")
		}
		return StaticModels(), err
	}
	if ttl := cfg.ModelCacheTTL; ttl > 0 {
		modelCacheMu.Lock()
		modelCache[key] = modelCacheEntry{models: models, expiresAt: time.Now().Add(ttl)}
		modelCacheMu.Unlock()
	}
	return models, nil
}

// fetchUpstreamModels performs the discovery call: GET /provider/v1/models with
// the credential's own key, like the CLI does.
func fetchUpstreamModels(ctx context.Context, account Account, cfg RuntimeConfig) ([]pluginapi.ModelInfo, error) {
	base := account.ResolvedBaseURL(cfg)
	if base == "" {
		return nil, fmt.Errorf("base url is empty")
	}
	profile := DeriveDevice(account.APIKey, account.DeviceSalt(cfg), account.ProjectDirValue(cfg), cfg.MasqueradePlatform, cfg.MasqueradeArch)

	reqCtx := ctx
	if cfg.RequestTimeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, cfg.RequestTimeout)
		defer cancel()
	}
	req, errRequest := http.NewRequestWithContext(reqCtx, http.MethodGet, base+EndpointModels, nil)
	if errRequest != nil {
		return nil, errRequest
	}
	// The catalog endpoint takes the same credential headers as generation, but
	// deliberately no ZDR header: the CLI does not send one here.
	for key, value := range upstreamHeaders(account, profile, cfg, "", true) {
		req.Header.Set(key, value)
	}

	resp, errDo := newUpstreamClient(cfg).Do(req)
	if errDo != nil {
		return nil, errDo
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("model discovery failed: upstream %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if errDecode := json.NewDecoder(resp.Body).Decode(&payload); errDecode != nil {
		return nil, fmt.Errorf("model discovery response: %w", errDecode)
	}
	models := make([]pluginapi.ModelInfo, 0, len(payload.Data))
	for _, entry := range payload.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		models = append(models, modelInfo(id, firstNonEmpty(entry.Name, id)))
	}
	return models, nil
}

// handleModelForAuth implements model.for_auth: one credential's catalog.
func handleModelForAuth(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := CurrentConfig()
	account, handled, err := ParseAccount(req.StorageJSON)
	if err != nil || !handled {
		// Unknown or unusable credential: serve the fallback catalog so the
		// provider is never empty, and log why.
		if err != nil {
			Logf("warn", "model.for_auth: unusable credential", map[string]any{"reason": err.Error()})
		}
		return okEnvelope(pluginapi.ModelResponse{Provider: ProviderID, Models: StaticModels()}), nil
	}
	models, discoverErr := ModelsForAccount(context.Background(), account, cfg)
	if discoverErr != nil {
		Logf("warn", "model.for_auth: discovery failed, serving the fallback catalog", map[string]any{
			"account": MaskKey(account.APIKey),
			"reason":  discoverErr.Error(),
		})
	}
	return okEnvelope(pluginapi.ModelResponse{Provider: ProviderID, Models: models}), nil
}
