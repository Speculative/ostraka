package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Live progress is written next to the supervisor's other state rather than
// into the item file. Two reasons: it is explicitly ephemeral — the real turn
// replaces it — and putting it in the item would make every line of in-flight
// output a permanent part of the thread's history.
//
// It also sits directly in supervisor/ rather than a nested directory because
// the TUI's watcher only watches the root and its immediate subdirectories, so
// writes here already wake the UI with no watcher changes.
func livePath(root, itemID string) string {
	return filepath.Join(supervisorDir(root), "live-"+itemID+".txt")
}

// ReadLive returns the live progress log for an item, or "" when the item is
// not currently being worked on. Errors are reported as "" — a missing or
// unreadable progress file is not worth surfacing over the conversation.
func ReadLive(root, itemID string) string {
	b, err := os.ReadFile(livePath(root, itemID))
	if err != nil {
		return ""
	}
	return string(b)
}

// liveLog appends progress lines for one dispatch. Every write is a full
// rewrite of an append-only buffer: readers get whole lines rather than a
// partially-flushed file, which matters because the TUI reloads on any write.
type liveLog struct {
	path string
	mu   sync.Mutex
	buf  strings.Builder
}

func newLiveLog(root, itemID string) *liveLog {
	return &liveLog{path: livePath(root, itemID)}
}

func (l *liveLog) append(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(line)
	l.buf.WriteString("\n")
	os.WriteFile(l.path, []byte(l.buf.String()), 0644) //nolint:errcheck
}

// clear removes the log. Called when a dispatch ends, whichever way it ended:
// a leftover file would show a finished run as though it were still going.
func (l *liveLog) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	os.Remove(l.path) //nolint:errcheck
}
