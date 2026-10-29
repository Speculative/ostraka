package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/store"
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

func TestStartWatcherSignalsForReviewReply(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "REVIEWS"), 0755); err != nil {
		t.Fatal(err)
	}
	watchCh, err := startWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "REVIEWS", "review.json"), []byte(`{"reply":"answer"}`), 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watchCh:
	case <-time.After(time.Second):
		t.Fatal("watcher did not signal for review-only update")
	}
}

func TestStartWatcherSignalsForSharedDrafts(t *testing.T) {
	root := t.TempDir()
	watchCh, err := startWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	// Drain store initialization events before testing draft-only changes.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-watchCh:
	default:
	}
	for _, content := range []string{"first draft", "replacement draft", ""} {
		if err := s.SaveDraft("shared", content); err != nil {
			t.Fatal(err)
		}
		select {
		case <-watchCh:
		case <-time.After(time.Second):
			t.Fatalf("watcher did not signal for draft %q", content)
		}
		// Let the atomic-write burst finish before the next mutation.
		time.Sleep(50 * time.Millisecond)
		select {
		case <-watchCh:
		default:
		}
	}
}
