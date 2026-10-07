package commandcode

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// rpcEnvelope is the frame CPA exchanges with the plugin for every RPC call.
type rpcEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcEnvelopeErr `json:"error,omitempty"`
}

type rpcEnvelopeErr struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// capabilities mirrors the host's rpcCapabilities frame. Only the flags this
// plugin implements are set.
type capabilities struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
	ManagementAPI         bool                         `json:"management_api"`
}

// registration is the plugin.register / plugin.reconfigure payload.
type registration struct {
	SchemaVersion uint32       `json:"schema_version"`
	Metadata      rpcMetadata  `json:"metadata"`
	Capabilities  capabilities `json:"capabilities"`
}

// rpcMetadata mirrors pluginapi.Metadata. It deliberately carries no JSON tags:
// the host decodes it into the SDK struct, whose fields have no tags either, so
// the wire keys must be the Go field names (snake_case would not match).
type rpcMetadata struct {
	Name             string
	Version          string
	Author           string
	GitHubRepository string
	Logo             string
	ConfigFields     []pluginapi.ConfigField
}

// lifecycleRequest is what the host sends with plugin.register /
// plugin.reconfigure: the plugin's own block from config.yaml plus the schema
// version. ConfigYAML arrives base64-encoded because the field is []byte.
type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

// HandleMethod dispatches one plugin RPC call. A returned error is rendered by
// the ABI shim as a failure frame; the host treats status-less failures as
// credential-scoped, so only executor paths attach a status code.
func HandleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var req lifecycleRequest
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, fmt.Errorf("decode lifecycle request: %w", err)
			}
		}
		ApplyConfigYAML(req.ConfigYAML)
		return okEnvelope(registrationPayload()), nil

	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier:
		return okEnvelope(map[string]string{"identifier": ProviderID}), nil

	case pluginabi.MethodModelStatic:
		return okEnvelope(pluginapi.ModelResponse{Provider: ProviderID, Models: StaticModels()}), nil

	case pluginabi.MethodModelForAuth:
		return handleModelForAuth(request)

	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)

	case pluginabi.MethodExecutorExecute:
		return handleExecute(request)

	case pluginabi.MethodExecutorExecuteStream:
		return handleExecuteStream(request)

	case pluginabi.MethodManagementRegister:
		return okEnvelope(map[string]any{"resources": ManagementResources()}), nil

	case pluginabi.MethodManagementHandle:
		return handleManagementRequest(request)
	}
	return nil, fmt.Errorf("unknown method: %s", method)
}

// Registration returns the registration payload as JSON. Exposed for tests and
// for the ABI shim's convenience.
func Registration() []byte {
	raw, err := json.Marshal(registrationPayload())
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func registrationPayload() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: rpcMetadata{
			Name:             ProviderID,
			Version:          Version,
			Author:           Author,
			GitHubRepository: RepositoryURL,
			ConfigFields:     ConfigFields(),
		},
		Capabilities: capabilities{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeBoth,
			ExecutorInputFormats:  []string{ExecutorFormat},
			ExecutorOutputFormats: []string{ExecutorFormat},
			ManagementAPI:         true,
		},
	}
}

func okEnvelope(v any) []byte {
	result, err := json.Marshal(v)
	if err != nil {
		return errorEnvelope("plugin_error", err.Error(), 0)
	}
	raw, err := json.Marshal(rpcEnvelope{OK: true, Result: result})
	if err != nil {
		return errorEnvelope("plugin_error", err.Error(), 0)
	}
	return raw
}

func errorEnvelope(code, message string, httpStatus int) []byte {
	raw, _ := json.Marshal(rpcEnvelope{OK: false, Error: &rpcEnvelopeErr{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}})
	return raw
}
