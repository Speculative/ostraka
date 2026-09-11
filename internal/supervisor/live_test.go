package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".ostraka")
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLiveLogAccumulatesWholeLines(t *testing.T) {
	root := newTestRoot(t)
	l := newLiveLog(root, "item-1")

	l.append("first")
	l.append("second")

	got := ReadLive(root, "item-1")
	if got != "first\nsecond\n" {
		t.Errorf("got %q, want both lines in order", got)
	}
}

func TestLiveLogStartCreatesEmptyActiveMarker(t *testing.T) {
	root := newTestRoot(t)
	l := newLiveLog(root, "item-1")

	l.start()
	content, active := ReadLiveState(root, "item-1")
	if !active || content != "" {
		t.Fatalf("started live state = (%q, %v), want empty active trace", content, active)
	}
}

func TestLiveLogSkipsBlankLines(t *testing.T) {
	root := newTestRoot(t)
	l := newLiveLog(root, "item-1")

	l.append("  ")
	l.append("")
	l.append("real")

	if got := ReadLive(root, "item-1"); got != "real\n" {
		t.Errorf("got %q, want only the non-blank line", got)
	}
}

func TestLiveLogClearRemovesTheFile(t *testing.T) {
	// A leftover log would show a finished run as though it were still going.
	root := newTestRoot(t)
	l := newLiveLog(root, "item-1")
	l.append("working")
	l.clear()

	if got := ReadLive(root, "item-1"); got != "" {
		t.Errorf("got %q after clear, want empty", got)
	}
	if _, err := os.Stat(livePath(root, "item-1")); !os.IsNotExist(err) {
		t.Errorf("file still exists after clear: %v", err)
	}
}

func TestReadLiveIsEmptyForUnknownItem(t *testing.T) {
	// Every item that is not mid-dispatch takes this path, so a missing file
	// must be silent rather than an error the pane would have to render.
	if got := ReadLive(newTestRoot(t), "never-dispatched"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestLiveLogsAreIsolatedPerItem(t *testing.T) {
	root := newTestRoot(t)
	newLiveLog(root, "item-a").append("a-line")
	newLiveLog(root, "item-b").append("b-line")

	if got := ReadLive(root, "item-a"); !strings.Contains(got, "a-line") || strings.Contains(got, "b-line") {
		t.Errorf("item-a log leaked: %q", got)
	}
}

func TestDispatchWritesAndClearsLiveLog(t *testing.T) {
	// End to end: the log exists while the harness runs and is gone after.
	spy := &liveSpyHarness{}
	s, st := newStoreBackedSupervisor(t, spy)
	item, err := st.CreateItem("inbox", "t", "b", "thread", "pending-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	spy.root, spy.itemID = s.root, item.ID

	s.dispatch(enqueueMsg{itemID: item.ID})

	if !spy.beforeActive {
		t.Error("live dispatch marker was not active when the harness started")
	}
	if !strings.Contains(spy.during, "mid-run line") {
		t.Errorf("live log during run was %q", spy.during)
	}
	if got := ReadLive(s.root, item.ID); got != "" {
		t.Errorf("live log survived the dispatch: %q", got)
	}
	if _, active := ReadLiveState(s.root, item.ID); active {
		t.Error("empty live marker survived the dispatch")
	}
}
