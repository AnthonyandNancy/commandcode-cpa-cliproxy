// Package commandcode implements the CommandCode provider for CLIProxyAPI.
//
// The package is deliberately free of cgo so that every business rule can be
// tested with plain `go test`; the C ABI shim lives in the root package.
//
// Layering (see docs/superpowers/plans/2026-10-07-commandcode-cpa-plugin.md):
//
//	account.go     credential file model, stable id/name, masking
//	identity.go    deterministic device identity + init pre-requests
//	envelope.go    chat-completions -> CommandCode envelope
//	models.go      per-credential model catalog with static fallback
//	upstream.go    upstream call + status mapping + error classification
//	stream.go      upstream SSE -> chat-completions chunks / folding
//	executor.go    executor.execute / execute_stream entry points
//	management.go  plugin-owned account page endpoints
//
// The plugin owns exactly two things the host cannot do itself: the CommandCode
// wire protocol and the account page. Credential selection, session affinity,
// rotation, cooldown and client protocol translation stay in CPA.
package commandcode

import "time"

// Version is the plugin release version. It can be overridden at build time:
//
//	go build -ldflags "-X commandcode-cliproxy-plugin/internal/commandcode.Version=1.2.3"
var Version = "0.1.1"

// RepositoryURL is advertised to the management panel. Replace it with the real
// repository before publishing, or override it at build time with -ldflags.
var RepositoryURL = "https://github.com/OWNER/commandcode-cpa-cliproxy"

const (
	// ProviderID is the provider key registered with CPA. It is also the value
	// written into credential files as their "type" field.
	ProviderID = "commandcode"
	// Author is shown in the plugin metadata.
	Author = "commandcode-cpa-cliproxy"
	// ExecutorFormat is the only request/response format this executor speaks.
	// CPA translates Anthropic / Responses / Gemini traffic into it.
	ExecutorFormat = "chat-completions"
)

const (
	// DefaultBaseURL is the CommandCode API base.
	DefaultBaseURL = "https://api.commandcode.ai"
	// DefaultCLIVersion is the x-command-code-version header value. It mirrors
	// the version commandcode-proxy pins (CC_PROTOCOL_VERSION).
	DefaultCLIVersion = "1.53.1"
	// DefaultProjectDir is the faked working directory reported upstream.
	DefaultProjectDir = `C:\Users\dev\projects\app`
	// DefaultPlatform / DefaultArch keep the device profile self-consistent with
	// the faked fingerprint; the host's real platform is never sent upstream.
	DefaultPlatform = "win32"
	DefaultArch     = "x64"
	// DefaultCLIMode is the envelope "mode" value (agent / learning / ...).
	DefaultCLIMode = "agent"
	// DefaultLifecycleMode is the lifecycle metadata "mode" (a different enum).
	DefaultLifecycleMode = "interactive"

	// DefaultModelCacheTTL bounds how often the per-credential catalog is fetched.
	DefaultModelCacheTTL = 5 * time.Minute
	// DefaultStreamIdleTimeout / DefaultNonStreamIdleTimeout bound upstream silence.
	DefaultStreamIdleTimeout    = 30 * time.Second
	DefaultNonStreamIdleTimeout = 90 * time.Second
	// DefaultMaxBodyMB mirrors commandcode-proxy's request body ceiling.
	DefaultMaxBodyMB = 100
	// DefaultMaxToolImageMB keeps the newest tool screenshots inside a budget so
	// a long session cannot blow up memory (measured x5-7 amplification upstream).
	DefaultMaxToolImageMB = 6
)

// Endpoint paths on the CommandCode API.
const (
	EndpointGenerate    = "/alpha/generate"
	EndpointFingerprint = "/alpha/fingerprint/record"
	EndpointLifecycle   = "/alpha/lifecycle-events"
	EndpointModels      = "/provider/v1/models"
)
