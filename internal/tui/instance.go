package tui

import "io"

// AcquireSupervisorLock shares the project dispatcher lock with non-TUI hosts.
// Hold it before constructing a supervisor, until after Shutdown completes.
func AcquireSupervisorLock(root string) (io.Closer, error) {
	return acquireInstanceLock(root)
}
