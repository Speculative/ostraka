//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockReviews serializes review-object read/modify/write operations across
// Ostraka CLI and TUI processes. Review-to-item operations always acquire this
// lock before lockItems to keep a consistent cross-process lock order.
func (s *Store) lockReviews() (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Root, reviewDir), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, "reviews.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
