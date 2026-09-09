//go:build !windows

package supervisor

import (
	"os/exec"
	"syscall"
	"time"
)

// killDelay bounds how long Wait blocks on pipes still held open after the
// process group is killed. A child that ignores the signal must not keep the
// supervisor's Shutdown waiting on it.
const killDelay = 3 * time.Second

// detachProcessGroup puts the agent in its own process group and kills that
// whole group on cancellation. exec's default cancel kills only the process it
// started, which for a coding agent is the least interesting one — the tool
// subprocesses it spawned are where the work (and the runtime) actually is,
// and they would survive to write into a project nobody is watching.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return killProcess(cmd)
	}
	cmd.WaitDelay = killDelay
}

func interruptProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoActiveProviderProcess
	}
	// Negative pid addresses the group. The group id is the child's pid
	// because Setpgid made it the leader.
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Negative pid addresses the whole provider/tool process group, not just
	// the CLI parent that owns the stdio pipes.
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
