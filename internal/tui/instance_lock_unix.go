//go:build !windows

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type instanceLock struct {
	file *os.File
}

func acquireInstanceLock(root string) (*instanceLock, error) {
	dir := filepath.Join(root, "supervisor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create supervisor state directory: %w", err)
	}
	path := filepath.Join(dir, "tui.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open TUI lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("another Ostraka TUI is already running for this project")
		}
		return nil, fmt.Errorf("acquire TUI lock: %w", err)
	}
	return &instanceLock{file: f}, nil
}

func (l *instanceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
