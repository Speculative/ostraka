//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"syscall"
)

// Serialize session configuration and dispatch completion across processes.
func lockSessions(root string) (func(), error) {
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(supervisorDir(root), "session.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
