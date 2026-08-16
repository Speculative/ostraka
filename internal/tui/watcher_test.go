package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartWatcherSignalsForLiveProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "supervisor"), 0755); err != nil {
		t.Fatal(err)
	}

	watchCh, err := startWatcher(root)
	if err != nil {
		t.Fatal(err)
	}

	livePath := filepath.Join(root, "supervisor", "live-item-1.txt")
	if err := os.WriteFile(livePath, []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-watchCh:
	case <-time.After(time.Second):
		t.Fatal("watcher did not signal for a live progress write")
	}
}
