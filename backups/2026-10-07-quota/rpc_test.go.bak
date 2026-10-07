package commandcode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleMethodUnknown(t *testing.T) {
	_, err := HandleMethod("no.such.method", nil)
	if err == nil {
		t.Fatal("HandleMethod(unknown) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "no.such.method") {
		t.Fatalf("HandleMethod(unknown) error = %q, want it to name the method", err.Error())
	}
}

func TestRegistrationCapabilities(t *testing.T) {
	raw := Registration()

	var got struct {
		SchemaVersion uint32 `json:"schema_version"`
		Metadata      struct {
			Name             string `json:"Name"`
			Version          string `json:"Version"`
			Author           string `json:"Author"`
			GitHubRepository string `json:"GitHubRepository"`
			ConfigFields     []any  `json:"ConfigFields"`
		} `json:"metadata"`
		Capabilities struct {
			ModelProvider         bool     `json:"model_provider"`
			AuthProvider          bool     `json:"auth_provider"`
			Executor              bool     `json:"executor"`
			ExecutorModelScope    string   `json:"executor_model_scope"`
			ExecutorInputFormats  []string `json:"executor_input_formats"`
			ExecutorOutputFormats []string `json:"executor_output_formats"`
			ManagementAPI         bool     `json:"management_api"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Registration() is not valid JSON: %v", err)
	}

	if got.SchemaVersion != 6 {
		t.Errorf("schema_version = %d, want 6", got.SchemaVersion)
	}
	if got.Metadata.Name != ProviderID {
		t.Errorf("metadata.Name = %q, want %q", got.Metadata.Name, ProviderID)
	}
	if got.Metadata.Version == "" || got.Metadata.Author == "" || got.Metadata.GitHubRepository == "" {
		t.Errorf("metadata is incomplete: %+v", got.Metadata)
	}
	if len(got.Metadata.ConfigFields) == 0 {
		t.Error("metadata.ConfigFields is empty, want the plugin's settings to be declared")
	}
	if !got.Capabilities.ModelProvider || !got.Capabilities.AuthProvider || !got.Capabilities.Executor || !got.Capabilities.ManagementAPI {
		t.Errorf("capabilities = %+v, want model/auth/executor/management all enabled", got.Capabilities)
	}
	if got.Capabilities.ExecutorModelScope != "both" {
		t.Errorf("executor_model_scope = %q, want both", got.Capabilities.ExecutorModelScope)
	}
	if len(got.Capabilities.ExecutorInputFormats) != 1 || got.Capabilities.ExecutorInputFormats[0] != "chat-completions" {
		t.Errorf("executor_input_formats = %v, want [chat-completions]", got.Capabilities.ExecutorInputFormats)
	}
	if len(got.Capabilities.ExecutorOutputFormats) != 1 || got.Capabilities.ExecutorOutputFormats[0] != "chat-completions" {
		t.Errorf("executor_output_formats = %v, want [chat-completions]", got.Capabilities.ExecutorOutputFormats)
	}
}
