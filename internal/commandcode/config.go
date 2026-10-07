package commandcode

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

// RuntimeConfig is the plugin's effective configuration. It is rebuilt from the
// plugins.configs.commandcode YAML block on every register/reconfigure call and
// swapped atomically, because a panel save can race with in-flight requests.
type RuntimeConfig struct {
	BaseURL                string
	FingerprintSalt        string
	ProjectDir             string
	CLIMode                string
	CLIVersion             string
	LifecycleMode          string
	ZDR                    bool
	ModelCacheTTL          time.Duration
	EmptySystemPlaceholder bool
	MaxToolImageMB         int
	MaxBodyMB              int
	AllowPageWrite         bool
	UpstreamProxyURL       string
	RequestTimeout         time.Duration
	StreamIdleTimeout      time.Duration
	NonStreamIdleTimeout   time.Duration
	MasqueradePlatform     string
	MasqueradeArch         string
	Language               string
}

// DefaultConfig returns the shipped defaults. Every value mirrors the behaviour
// of commandcode-proxy unless the requirement says otherwise.
func DefaultConfig() RuntimeConfig {
	return RuntimeConfig{
		BaseURL:                DefaultBaseURL,
		FingerprintSalt:        "",
		ProjectDir:             DefaultProjectDir,
		CLIMode:                DefaultCLIMode,
		CLIVersion:             DefaultCLIVersion,
		LifecycleMode:          DefaultLifecycleMode,
		ZDR:                    false,
		ModelCacheTTL:          DefaultModelCacheTTL,
		EmptySystemPlaceholder: true,
		MaxToolImageMB:         DefaultMaxToolImageMB,
		MaxBodyMB:              DefaultMaxBodyMB,
		AllowPageWrite:         true,
		StreamIdleTimeout:      DefaultStreamIdleTimeout,
		NonStreamIdleTimeout:   DefaultNonStreamIdleTimeout,
		MasqueradePlatform:     DefaultPlatform,
		MasqueradeArch:         DefaultArch,
		Language:               "auto",
	}
}

// yamlConfig mirrors the plugin's config block. Pointers distinguish "key
// absent" from "explicitly set to the zero value".
type yamlConfig struct {
	BaseURL                *string `yaml:"base-url"`
	FingerprintSalt        *string `yaml:"fingerprint-salt"`
	ProjectDir             *string `yaml:"project-dir"`
	CLIMode                *string `yaml:"cli-mode"`
	CLIVersion             *string `yaml:"cli-version"`
	LifecycleMode          *string `yaml:"lifecycle-mode"`
	ZDR                    *bool   `yaml:"zdr"`
	ModelCacheTTL          *string `yaml:"model-cache-ttl"`
	EmptySystemPlaceholder *bool   `yaml:"empty-system-placeholder"`
	MaxToolImageMB         *int    `yaml:"max-tool-image-mb"`
	MaxBodyMB              *int    `yaml:"max-body-mb"`
	AllowPageWrite         *bool   `yaml:"allow-page-write"`
	UpstreamProxyURL       *string `yaml:"upstream-proxy-url"`
	RequestTimeout         *string `yaml:"request-timeout"`
	StreamIdleTimeout      *string `yaml:"stream-idle-timeout"`
	NonStreamIdleTimeout   *string `yaml:"non-stream-idle-timeout"`
	MasqueradePlatform     *string `yaml:"masquerade-platform"`
	MasqueradeArch         *string `yaml:"masquerade-arch"`
	Language               *string `yaml:"language"`
}

var runtimeConfigValue atomic.Value // RuntimeConfig

func init() {
	runtimeConfigValue.Store(DefaultConfig())
}

// CurrentConfig returns the effective configuration.
func CurrentConfig() RuntimeConfig {
	if cfg, ok := runtimeConfigValue.Load().(RuntimeConfig); ok {
		return cfg
	}
	return DefaultConfig()
}

// ApplyConfigYAML folds one plugin.register / plugin.reconfigure config block
// into the runtime configuration. A block that cannot be decoded keeps the
// previous value: a typo in the panel must never silently change live behaviour.
func ApplyConfigYAML(raw []byte) {
	if len(raw) == 0 {
		return
	}
	var parsed yamlConfig
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return
	}
	next := CurrentConfig()

	if parsed.BaseURL != nil {
		if v := strings.TrimSpace(*parsed.BaseURL); v != "" {
			next.BaseURL = strings.TrimRight(v, "/")
		}
	}
	if parsed.FingerprintSalt != nil {
		next.FingerprintSalt = strings.TrimSpace(*parsed.FingerprintSalt)
	}
	if parsed.ProjectDir != nil {
		if v := strings.TrimSpace(*parsed.ProjectDir); v != "" {
			next.ProjectDir = v
		}
	}
	if parsed.CLIMode != nil {
		if v := strings.TrimSpace(*parsed.CLIMode); v != "" {
			next.CLIMode = v
		}
	}
	if parsed.CLIVersion != nil {
		if v := strings.TrimSpace(*parsed.CLIVersion); v != "" {
			next.CLIVersion = v
		}
	}
	if parsed.LifecycleMode != nil {
		if v := strings.TrimSpace(*parsed.LifecycleMode); v != "" {
			next.LifecycleMode = v
		}
	}
	if parsed.ZDR != nil {
		next.ZDR = *parsed.ZDR
	}
	if parsed.ModelCacheTTL != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(*parsed.ModelCacheTTL)); err == nil && d >= 0 {
			next.ModelCacheTTL = d
		}
	}
	if parsed.EmptySystemPlaceholder != nil {
		next.EmptySystemPlaceholder = *parsed.EmptySystemPlaceholder
	}
	if parsed.MaxToolImageMB != nil && *parsed.MaxToolImageMB >= 0 {
		next.MaxToolImageMB = *parsed.MaxToolImageMB
	}
	if parsed.MaxBodyMB != nil && *parsed.MaxBodyMB > 0 {
		next.MaxBodyMB = *parsed.MaxBodyMB
	}
	if parsed.AllowPageWrite != nil {
		next.AllowPageWrite = *parsed.AllowPageWrite
	}
	if parsed.UpstreamProxyURL != nil {
		next.UpstreamProxyURL = strings.TrimSpace(*parsed.UpstreamProxyURL)
	}
	if parsed.RequestTimeout != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(*parsed.RequestTimeout)); err == nil && d >= 0 {
			next.RequestTimeout = d
		}
	}
	if parsed.StreamIdleTimeout != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(*parsed.StreamIdleTimeout)); err == nil && d > 0 {
			next.StreamIdleTimeout = d
		}
	}
	if parsed.NonStreamIdleTimeout != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(*parsed.NonStreamIdleTimeout)); err == nil && d > 0 {
			next.NonStreamIdleTimeout = d
		}
	}
	if parsed.MasqueradePlatform != nil {
		if v := strings.TrimSpace(*parsed.MasqueradePlatform); v != "" {
			next.MasqueradePlatform = v
		}
	}
	if parsed.MasqueradeArch != nil {
		if v := strings.TrimSpace(*parsed.MasqueradeArch); v != "" {
			next.MasqueradeArch = v
		}
	}
	if parsed.Language != nil {
		if v := strings.TrimSpace(*parsed.Language); v != "" {
			next.Language = v
		}
	}

	runtimeConfigValue.Store(next)
}

// ConfigFields declares the plugin's settings for management clients. Names must
// match the yaml keys consumed by ApplyConfigYAML.
func ConfigFields() []pluginapi.ConfigField {
	return []pluginapi.ConfigField{
		{Name: "base-url", Type: pluginapi.ConfigFieldTypeString, Description: "CommandCode API 基址 (upstream API base)"},
		{Name: "fingerprint-salt", Type: pluginapi.ConfigFieldTypeString, Description: "设备指纹盐：变更=全部账号换设备 (rotates every account's device)"},
		{Name: "project-dir", Type: pluginapi.ConfigFieldTypeString, Description: "伪装的项目目录 (faked working directory)"},
		{Name: "cli-mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"agent", "learning", "custom-agent", "custom-agent-create", "title-gen", "tool-desc", "compact", "vision"}, Description: "信封 mode 字段 (envelope mode)"},
		{Name: "cli-version", Type: pluginapi.ConfigFieldTypeString, Description: "x-command-code-version 上报值 (reported CLI version)"},
		{Name: "lifecycle-mode", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"interactive", "non-interactive"}, Description: "lifecycle 元数据 mode (lifecycle metadata mode)"},
		{Name: "zdr", Type: pluginapi.ConfigFieldTypeBoolean, Description: "请求上游 ZDR-only 路由 (request ZDR-only routing)"},
		{Name: "model-cache-ttl", Type: pluginapi.ConfigFieldTypeString, Description: "模型目录缓存时长，如 5m (model catalog cache TTL)"},
		{Name: "empty-system-placeholder", Type: pluginapi.ConfigFieldTypeBoolean, Description: "无 system 时发送空格占位 (empty system placeholder)"},
		{Name: "max-tool-image-mb", Type: pluginapi.ConfigFieldTypeInteger, Description: "工具截图总预算 MB，0=不限制 (tool screenshot budget)"},
		{Name: "max-body-mb", Type: pluginapi.ConfigFieldTypeInteger, Description: "请求体上限 MB (request body limit)"},
		{Name: "allow-page-write", Type: pluginapi.ConfigFieldTypeBoolean, Description: "允许插件页写账号，关闭后仅只读 (allow the account page to write)"},
		{Name: "upstream-proxy-url", Type: pluginapi.ConfigFieldTypeString, Description: "上游出口代理 http(s):// (upstream egress proxy)"},
		{Name: "request-timeout", Type: pluginapi.ConfigFieldTypeString, Description: "整请求超时，如 10m，空=不限 (overall request timeout)"},
		{Name: "stream-idle-timeout", Type: pluginapi.ConfigFieldTypeString, Description: "流式上游空闲超时 (stream idle timeout)"},
		{Name: "non-stream-idle-timeout", Type: pluginapi.ConfigFieldTypeString, Description: "非流式上游空闲超时 (non-stream idle timeout)"},
		{Name: "masquerade-platform", Type: pluginapi.ConfigFieldTypeString, Description: "伪装平台 win32/darwin (masqueraded platform)"},
		{Name: "masquerade-arch", Type: pluginapi.ConfigFieldTypeString, Description: "伪装架构 x64/arm64 (masqueraded architecture)"},
		{Name: "language", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"auto", "zh", "en"}, Description: "插件页语言 (account page language)"},
	}
}

// Validate reports whether the configuration can serve requests. It is used by
// the account page to show a clear error instead of failing per request.
func (c RuntimeConfig) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("base-url is empty")
	}
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return fmt.Errorf("base-url must start with http:// or https://")
	}
	return nil
}
