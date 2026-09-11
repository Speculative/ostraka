package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Live progress is written next to the supervisor's other state rather than
// into the item file. It is a staging buffer while a turn is in flight; the
// supervisor snapshots it into the item's durable partial-trace journal when
// the run ends.
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
	content, _ := ReadLiveState(root, itemID)
	return content
}

// ReadLiveState distinguishes an active dispatch that has not emitted its
// first progress line from an item with no live dispatch. The TUI uses the
// presence bit to show its working header immediately, even with an empty
// trace body.
func ReadLiveState(root, itemID string) (string, bool) {
	b, err := os.ReadFile(livePath(root, itemID))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// sweepLive removes every live progress log under root. Only safe to call when
// no dispatch can be running — the logs are per-dispatch state, and one that
// outlived its run is indistinguishable on disk from one still being written.
// It returns the ids whose logs were removed so the caller can report them.
func sweepLive(root string) []string {
	logs := sweepLiveLogs(root)
	ids := make([]string, 0, len(logs))
	for _, log := range logs {
		ids = append(ids, log.id)
	}
	return ids
}

type sweptLiveLog struct {
	id      string
	content string
}

func sweepLiveLogs(root string) []sweptLiveLog {
	matches, err := filepath.Glob(filepath.Join(supervisorDir(root), "live-*.txt"))
	if err != nil {
		return nil
	}
	var swept []sweptLiveLog
	for _, path := range matches {
		content, readErr := os.ReadFile(path)
		if err := os.Remove(path); err != nil {
			continue
		}
		name := filepath.Base(path)
		if readErr != nil {
			content = nil
		}
		swept = append(swept, sweptLiveLog{
			id:      strings.TrimSuffix(strings.TrimPrefix(name, "live-"), ".txt"),
			content: string(content),
		})
	}
	return swept
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

// start creates the in-flight marker before the provider has any progress
// content. An empty file is intentional: its presence drives the working
// header, while append adds the first body line later.
func (l *liveLog) start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = writeAtomic(l.path, nil)
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
	// Replace the log atomically. os.WriteFile truncates the existing path
	// before writing, so the TUI watcher can otherwise observe a brief empty
	// file between every two live lines and make the trace flicker.
	_ = writeAtomic(l.path, []byte(l.buf.String()))
}

// clear removes the log. Called when a dispatch ends, whichever way it ended:
// a leftover file would show a finished run as though it were still going.
func (l *liveLog) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	os.Remove(l.path) //nolint:errcheck
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below succeeds
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
