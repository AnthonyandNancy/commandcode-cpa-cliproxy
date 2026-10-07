package commandcode

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// This file holds the not-yet-implemented entry points while the plan is being
// executed task by task. Each declaration is replaced by its real
// implementation in its own task (2, 5, 7, 8). The file disappears once every
// task has landed.

// StaticModels is implemented in models.go (Task 5).
var StaticModels = func() []pluginapi.ModelInfo { return nil }

// handleModelForAuth is implemented in models.go (Task 5).
var handleModelForAuth = func(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.ModelResponse{Provider: ProviderID, Models: StaticModels()}), nil
}

// handleExecute / handleExecuteStream are implemented in executor.go (Task 7).
var handleExecute = func(request []byte) ([]byte, error) {
	return nil, fmt.Errorf("executor.execute is not implemented yet")
}

var handleExecuteStream = func(request []byte) ([]byte, error) {
	return nil, fmt.Errorf("executor.execute_stream is not implemented yet")
}

// ManagementResources / handleManagementRequest are implemented in management.go (Task 8).
var ManagementResources = func() []map[string]string { return nil }

var handleManagementRequest = func(request []byte) ([]byte, error) {
	return nil, fmt.Errorf("management.handle is not implemented yet")
}
