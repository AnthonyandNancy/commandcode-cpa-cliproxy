package commandcode

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// responseHeaderTimeout bounds the wait for response headers. A generation call
// is answered by headers as soon as upstream accepts it, so this only catches a
// connect or handshake that never completes; body stalls are handled by the idle
// watchdogs instead of a total deadline (which would truncate long answers).
const responseHeaderTimeout = 5 * time.Minute

// newUpstreamClient builds the HTTP client used for every upstream call
// (generation, model catalog and the init pre-requests) so that all of them
// share one egress path.
func newUpstreamClient(cfg RuntimeConfig) *http.Client {
	transport := &http.Transport{
		Proxy:                 proxyFunc(cfg.UpstreamProxyURL),
		ResponseHeaderTimeout: responseHeaderTimeout,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: transport}
}

// proxyFunc resolves the configured egress proxy. Only http/https proxies are
// supported by the standard library; a malformed value is ignored (direct).
func proxyFunc(proxyURL string) func(*http.Request) (*url.URL, error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return nil
	}
	// "direct" / "none" explicitly bypass both the global and environment proxies.
	if trimmed == "direct" || trimmed == "none" {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		Logf("warn", "ignoring unsupported upstream-proxy-url", map[string]any{"scheme": parsed.Scheme})
		return nil
	}
	return http.ProxyURL(parsed)
}

// upstreamHeaders renders the request headers the official CLI sends. sessionID
// is empty when the caller supplied no session identity: nothing is invented.
func upstreamHeaders(account Account, profile DeviceProfile, cfg RuntimeConfig, sessionID string, preflight bool) map[string]string {
	headers := map[string]string{
		"Content-Type":           "application/json",
		"accept-encoding":        "identity",
		"Authorization":          "Bearer " + account.APIKey,
		"x-command-code-version": cfg.CLIVersion,
		"x-cli-environment":      "production",
		"traceparent":            traceparent(),
	}
	if !preflight {
		headers["User-Agent"] = "cli"
		headers["x-project-slug"] = profile.ProjectSlug()
		headers["x-taste-learning"] = "false"
		headers["x-co-flag"] = "false"
	}
	if strings.TrimSpace(sessionID) != "" {
		headers["x-session-id"] = sessionID
	}
	if cfg.ZDR {
		headers["x-cmd-zdr"] = "1"
	}
	return headers
}

// traceparent renders a W3C trace context header: 00-<trace>-<parent>-01.
func traceparent() string {
	return fmt.Sprintf("00-%s-%s-01", randomHex(16), randomHex(8))
}
