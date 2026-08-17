//go:build windows

package tui

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
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
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if err == windows.ERROR_SHARING_VIOLATION {
			return nil, fmt.Errorf("another Ostraka TUI is already running for this project")
		}
		return nil, fmt.Errorf("acquire TUI lock: %w", err)
	}
	return &instanceLock{file: os.NewFile(uintptr(handle), path)}, nil
}

func (l *instanceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}
