//go:build windows

package supervisor

import (
	"os/exec"
	"time"
)

// detachProcessGroup is a no-op on Windows, which has no process groups in the
// POSIX sense. Cancellation falls back to exec's default of killing the agent
// process alone; its tool subprocesses are not reachable this way.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 3 * time.Second
}
