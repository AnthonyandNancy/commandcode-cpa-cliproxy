package commandcode

import "fmt"

// This file holds the not-yet-implemented entry points while the plan is being
// executed task by task. Each declaration is replaced by its real
// implementation in its own task (2, 5, 7, 8). The file disappears once every
// task has landed.

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
