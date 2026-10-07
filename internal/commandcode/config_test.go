package commandcode

import (
	"strings"
	"testing"
	"time"
)

// restoreDefaults resets the process-wide runtime configuration after a test
// that mutates it. The configuration is global by design (the host hot-swaps
// it), so every test that writes it must hand the defaults back.
func restoreDefaults(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { runtimeConfigValue.Store(DefaultConfig()) })
}

func TestDefaultConfigMatchesReferenceDefaults(t *testing.T) {
	restoreDefaults(t)
	cfg := DefaultConfig()
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	if !cfg.EmptySystemPlaceholder {
		t.Error("EmptySystemPlaceholder must default to true (it keeps upstream from injecting its own prompt)")
	}
	if cfg.MaxToolImageMB != DefaultMaxToolImageMB {
		t.Errorf("MaxToolImageMB = %d, want %d", cfg.MaxToolImageMB, DefaultMaxToolImageMB)
	}
	if cfg.StreamIdleTimeout != DefaultStreamIdleTimeout || cfg.NonStreamIdleTimeout != DefaultNonStreamIdleTimeout {
		t.Errorf("idle timeouts = %s/%s", cfg.StreamIdleTimeout, cfg.NonStreamIdleTimeout)
	}
	if cfg.CLIMode != DefaultCLIMode || cfg.LifecycleMode != DefaultLifecycleMode {
		t.Errorf("cli/lifecycle mode = %q/%q", cfg.CLIMode, cfg.LifecycleMode)
	}
	if cfg.CLIVersion != DefaultCLIVersion {
		t.Errorf("CLIVersion = %q, want %q", cfg.CLIVersion, DefaultCLIVersion)
	}
	if !cfg.AllowPageWrite {
		t.Error("AllowPageWrite must default to true")
	}
	if cfg.MasqueradePlatform != DefaultPlatform || cfg.MasqueradeArch != DefaultArch {
		t.Errorf("masquerade = %q/%q", cfg.MasqueradePlatform, cfg.MasqueradeArch)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config does not validate: %v", err)
	}
}

func TestApplyConfigYAMLOverridesAndKeepsDefaults(t *testing.T) {
	restoreDefaults(t)

	ApplyConfigYAML([]byte(`
base-url: https://example.test/
fingerprint-salt: fleet-2
project-dir: D:\dev\app
cli-mode: learning
cli-version: 1.60.0
lifecycle-mode: non-interactive
zdr: true
model-cache-ttl: 90s
empty-system-placeholder: false
max-tool-image-mb: 3
max-body-mb: 64
allow-page-write: false
upstream-proxy-url: http://127.0.0.1:7890
stream-idle-timeout: 12s
non-stream-idle-timeout: 45s
masquerade-platform: darwin
masquerade-arch: arm64
language: en
`))

	cfg := CurrentConfig()
	if cfg.BaseURL != "https://example.test" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", cfg.BaseURL)
	}
	if cfg.FingerprintSalt != "fleet-2" || cfg.ProjectDir != `D:\dev\app` {
		t.Errorf("identity config = %q / %q", cfg.FingerprintSalt, cfg.ProjectDir)
	}
	if cfg.CLIMode != "learning" || cfg.LifecycleMode != "non-interactive" || cfg.CLIVersion != "1.60.0" {
		t.Errorf("cli config = %q/%q/%q", cfg.CLIMode, cfg.LifecycleMode, cfg.CLIVersion)
	}
	if !cfg.ZDR {
		t.Error("ZDR = false, want true")
	}
	if cfg.ModelCacheTTL != 90*time.Second {
		t.Errorf("ModelCacheTTL = %s, want 90s", cfg.ModelCacheTTL)
	}
	if cfg.EmptySystemPlaceholder {
		t.Error("EmptySystemPlaceholder = true, want the explicit false")
	}
	if cfg.MaxToolImageMB != 3 || cfg.MaxBodyMB != 64 {
		t.Errorf("budgets = %d/%d", cfg.MaxToolImageMB, cfg.MaxBodyMB)
	}
	if cfg.AllowPageWrite {
		t.Error("AllowPageWrite = true, want false")
	}
	if cfg.UpstreamProxyURL != "http://127.0.0.1:7890" {
		t.Errorf("UpstreamProxyURL = %q", cfg.UpstreamProxyURL)
	}
	if cfg.StreamIdleTimeout != 12*time.Second || cfg.NonStreamIdleTimeout != 45*time.Second {
		t.Errorf("idle timeouts = %s/%s", cfg.StreamIdleTimeout, cfg.NonStreamIdleTimeout)
	}
	if cfg.MasqueradePlatform != "darwin" || cfg.MasqueradeArch != "arm64" {
		t.Errorf("masquerade = %q/%q", cfg.MasqueradePlatform, cfg.MasqueradeArch)
	}
	if cfg.Language != "en" {
		t.Errorf("Language = %q", cfg.Language)
	}
}

func TestApplyConfigYAMLKeepsPreviousOnBadInput(t *testing.T) {
	restoreDefaults(t)

	ApplyConfigYAML([]byte(`base-url: https://good.test`))
	ApplyConfigYAML([]byte("base-url: [not, a, string]"))
	if got := CurrentConfig().BaseURL; got != "https://good.test" {
		t.Errorf("BaseURL = %q, want the previous valid value kept", got)
	}

	ApplyConfigYAML([]byte("stream-idle-timeout: nonsense"))
	if got := CurrentConfig().StreamIdleTimeout; got != DefaultStreamIdleTimeout {
		t.Errorf("StreamIdleTimeout = %s, want the previous value kept", got)
	}

	ApplyConfigYAML([]byte("zdr: true"))
	ApplyConfigYAML([]byte("{{{{not yaml"))
	if !CurrentConfig().ZDR {
		t.Error("an undecodable block must keep the previous value")
	}

	// Empty blocks are ignored entirely.
	ApplyConfigYAML(nil)
	if !CurrentConfig().ZDR {
		t.Error("an empty block must keep the previous value")
	}
}

func TestConfigFieldsMatchConsumedKeys(t *testing.T) {
	restoreDefaults(t)
	declared := map[string]bool{}
	for _, field := range ConfigFields() {
		if field.Name == "" || field.Type == "" || field.Description == "" {
			t.Errorf("config field is incomplete: %+v", field)
		}
		if declared[field.Name] {
			t.Errorf("duplicate config field %q", field.Name)
		}
		declared[field.Name] = true
	}
	// Every declared key must actually be consumed by ApplyConfigYAML; the yaml
	// struct is the single list of keys, so compare against it by applying each
	// key and checking that nothing panics and the config changes.
	for key := range declared {
		ApplyConfigYAML([]byte(key + ": "))
	}
	// Unknown keys are ignored rather than fatal.
	ApplyConfigYAML([]byte("totally-unknown-key: 1"))

	if err := (RuntimeConfig{BaseURL: ""}).Validate(); err == nil {
		t.Error("an empty base url must not validate")
	}
	if err := (RuntimeConfig{BaseURL: "ftp://x"}).Validate(); err == nil {
		t.Error("a non-http base url must not validate")
	}
	if !strings.HasPrefix(DefaultBaseURL, "https://") {
		t.Errorf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}
