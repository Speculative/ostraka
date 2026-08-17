//go:build !windows

package tui

import "testing"

func TestAcquireInstanceLockRejectsSecondTUI(t *testing.T) {
	root := t.TempDir()
	first, err := acquireInstanceLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := acquireInstanceLock(root)
	if err == nil {
		second.Close()
		t.Fatal("second TUI instance acquired the project lock")
	}
	if second != nil {
		t.Fatal("failed lock acquisition returned a lock")
	}
}
