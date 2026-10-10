//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock read/modify/write item mutations across CLI processes and the TUI.
// Atomic replacement alone does not prevent a stale writer losing a goal
// cancellation or a user turn. OS locks are released on process exit.
func (s *Store) lockItems() (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.Root, "items.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
