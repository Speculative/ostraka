package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ostraka/internal/models"
	"ostraka/internal/store"
)

type fakeHarness struct {
	mu       sync.Mutex
	calls    []string // sessionID seen per call, in order
	sessions []string // sessionID to return per call, in order
	err      error
}

func (f *fakeHarness) RunTurn(_ context.Context, _ string, sessionID string) (TurnResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionID)
	if f.err != nil {
		return TurnResult{}, f.err
	}
	idx := len(f.calls) - 1
	newSession := "session-" + string(rune('a'+idx))
	if idx < len(f.sessions) {
		newSession = f.sessions[idx]
	}
	return TurnResult{SessionID: newSession, ResultText: "ok"}, nil
}

func newTestSupervisor(t *testing.T, h Harness) *Supervisor {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	return &Supervisor{
		root:    root,
		harness: h,
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
	}
}

func TestDispatchStartsFreshThenResumes(t *testing.T) {
	fh := &fakeHarness{}
	s := newTestSupervisor(t, fh)

	s.dispatch(enqueueMsg{itemID: "item-1"})
	s.dispatch(enqueueMsg{itemID: "item-2"})

	fh.mu.Lock()
	defer fh.mu.Unlock()
	if len(fh.calls) != 2 {
		t.Fatalf("expected 2 harness calls, got %d", len(fh.calls))
	}
	if fh.calls[0] != "" {
		t.Errorf("first dispatch should start fresh (empty session), got %q", fh.calls[0])
	}
	if fh.calls[1] != "session-a" {
		t.Errorf("second dispatch should resume the session persisted by the first, got %q", fh.calls[1])
	}

	got, err := loadSessionID(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "session-b" {
		t.Errorf("expected persisted session id session-b, got %q", got)
	}
}

func TestEnqueueSerializesThroughQueue(t *testing.T) {
	fh := &fakeHarness{}
	s := newTestSupervisor(t, fh)
	s.Start()

	for i := 0; i < 5; i++ {
		s.Enqueue("item")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		fh.mu.Lock()
		n := len(fh.calls)
		fh.mu.Unlock()
		if n == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for 5 dispatches, got %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatchErrorIsLoggedNotFatal(t *testing.T) {
	fh := &fakeHarness{err: context.DeadlineExceeded}
	s := newTestSupervisor(t, fh)

	s.dispatch(enqueueMsg{itemID: "item-1"}) // must not panic

	logBytes, err := os.ReadFile(filepath.Join(supervisorDir(s.root), "log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "dispatch failed") {
		t.Errorf("expected log to record dispatch failure, got: %s", logBytes)
	}

	// No session should have been persisted since the harness call failed.
	if _, err := os.Stat(sessionPath(s.root)); !os.IsNotExist(err) {
		t.Errorf("expected no session file after a failed dispatch, stat err=%v", err)
	}
}

// statusSpyHarness records the item's status as observed from inside the run,
// which is the only place the in-progress marker is visible.
type statusSpyHarness struct {
	st     *store.Store
	itemID string
	during models.Status
	err    error
}

func (h *statusSpyHarness) RunTurn(_ context.Context, _ string, _ string) (TurnResult, error) {
	if item, err := h.st.GetItem(h.itemID); err == nil {
		h.during = item.Status
	}
	if h.err != nil {
		return TurnResult{}, h.err
	}
	return TurnResult{SessionID: "s", ResultText: "ok"}, nil
}

func newStoreBackedSupervisor(t *testing.T, h Harness) (*Supervisor, *store.Store) {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".ostraka")
	st, err := store.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	return &Supervisor{
		root:    root,
		harness: h,
		store:   st,
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
	}, st
}

func TestDispatchMarksItemInProgress(t *testing.T) {
	spy := &statusSpyHarness{}
	s, st := newStoreBackedSupervisor(t, spy)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	spy.st, spy.itemID = st, item.ID

	s.dispatch(enqueueMsg{itemID: item.ID})

	if spy.during != models.StatusAgentAcknowledged {
		t.Errorf("status during run: got %q want %q", spy.during, models.StatusAgentAcknowledged)
	}
	// The agent posted no turn, so the marker must not be left behind.
	after, _ := st.GetItem(item.ID)
	if after.Status != models.StatusPendingUser {
		t.Errorf("status after run: got %q want %q", after.Status, models.StatusPendingUser)
	}
}

func TestDispatchFailureRestoresPendingAgent(t *testing.T) {
	spy := &statusSpyHarness{err: context.DeadlineExceeded}
	s, st := newStoreBackedSupervisor(t, spy)
	item, _ := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	spy.st, spy.itemID = st, item.ID

	s.dispatch(enqueueMsg{itemID: item.ID})

	// A failed run must not leave the item showing work that isn't happening.
	after, _ := st.GetItem(item.ID)
	if after.Status != models.StatusPendingAgent {
		t.Errorf("got %q want %q", after.Status, models.StatusPendingAgent)
	}
}

func TestDispatchLeavesNonPendingAgentItemsAlone(t *testing.T) {
	// The user may have moved the item since it was queued; a stale dispatch
	// must not drag it back into the agent's column.
	spy := &statusSpyHarness{}
	s, st := newStoreBackedSupervisor(t, spy)
	item, _ := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusBacklog, "")
	spy.st, spy.itemID = st, item.ID

	s.dispatch(enqueueMsg{itemID: item.ID})

	if spy.during != models.StatusBacklog {
		t.Errorf("status during run: got %q want %q", spy.during, models.StatusBacklog)
	}
	after, _ := st.GetItem(item.ID)
	if after.Status != models.StatusBacklog {
		t.Errorf("status after run: got %q want %q", after.Status, models.StatusBacklog)
	}
}

func TestNudgePromptNamesTheItem(t *testing.T) {
	// The prompt must not send the agent hunting via a status query: dispatch
	// marks the item agent-acknowledged, so a pending-agent search finds
	// nothing by the time the agent runs.
	got := nudgePrompt("20260808-054612")
	if !strings.Contains(got, "20260808-054612") {
		t.Errorf("prompt does not name the item: %q", got)
	}
	if strings.Contains(got, "--status pending-agent") {
		t.Errorf("prompt still sends the agent to a query that excludes the dispatched item: %q", got)
	}
}
