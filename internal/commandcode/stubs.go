package commandcode

import "fmt"

// This file holds the not-yet-implemented entry points while the plan is being
// executed task by task. Each declaration is replaced by its real
// implementation in its own task (2, 5, 7, 8). The file disappears once every
// task has landed.

// ManagementResources / handleManagementRequest are implemented in management.go (Task 8).
var ManagementResources = func() []map[string]string { return nil }

var handleManagementRequest = func(request []byte) ([]byte, error) {
	return nil, fmt.Errorf("management.handle is not implemented yet")
}
