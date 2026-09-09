//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"time"
)

// detachProcessGroup is a no-op on Windows, which has no process groups in the
// POSIX sense. Cancellation falls back to exec's default of killing the agent
// process alone; its tool subprocesses are not reachable this way.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 3 * time.Second
}

func interruptProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoActiveProviderProcess
	}
	return cmd.Process.Signal(os.Interrupt)
}

func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
