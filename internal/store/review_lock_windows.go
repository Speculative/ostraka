//go:build windows

package store

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func (s *Store) lockReviews() (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Root, reviewDir), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, "reviews.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	overlap := &windows.Overlapped{}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlap); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlap); _ = f.Close() }, nil
}
