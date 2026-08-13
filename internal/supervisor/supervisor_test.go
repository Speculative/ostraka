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

func (f *fakeHarness) RunTurn(_ context.Context, _ string, sessionID string, _ func(string)) (TurnResult, error) {
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Supervisor{
		root:    root,
		harness: h,
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
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

	// A failed run that never reached a session id has nothing to persist.
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

func (h *statusSpyHarness) RunTurn(_ context.Context, _ string, _ string, _ func(string)) (TurnResult, error) {
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Supervisor{
		root:    root,
		harness: h,
		store:   st,
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
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

func TestDispatchDiscardsAStaleLiveLogBeforeAcknowledging(t *testing.T) {
	spy := &liveSpyHarness{}
	s, st := newStoreBackedSupervisor(t, spy)
	item, _ := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	spy.root, spy.itemID = s.root, item.ID
	// A log left behind by a run that never cleared it.
	if err := os.WriteFile(livePath(s.root, item.ID), []byte("output from a dead run\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s.dispatch(enqueueMsg{itemID: item.ID})

	// The item is acknowledged for the whole run, so the pane would have
	// rendered whatever was readable before this run emitted its first line.
	if spy.before != "" {
		t.Errorf("stale log was still readable at the start of the run: %q", spy.before)
	}
	if _, err := os.Stat(livePath(s.root, item.ID)); !os.IsNotExist(err) {
		t.Errorf("live log outlived the dispatch, stat err=%v", err)
	}
}

func TestNewRecoversFromAnInterruptedDispatch(t *testing.T) {
	// Through New rather than the recovery method directly: the guarantee is
	// that a supervisor is never constructed on top of a dead run's state.
	root := filepath.Join(t.TempDir(), ".ostraka")
	st, err := store.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	interrupted, _ := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusAgentAcknowledged, "")
	untouched, _ := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingUser, "")
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePath(root, interrupted.ID), []byte("half a run\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := New(root)

	// The trace and the marker have to go together: either one left behind on
	// its own still renders as a run in progress.
	if got := ReadLive(s.root, interrupted.ID); got != "" {
		t.Errorf("orphaned live log survived: %q", got)
	}
	after, _ := st.GetItem(interrupted.ID)
	if after.Status != models.StatusPendingAgent {
		t.Errorf("interrupted item: got %q want %q", after.Status, models.StatusPendingAgent)
	}
	// Recovery is for interrupted dispatches only; nothing else moves.
	other, _ := st.GetItem(untouched.ID)
	if other.Status != models.StatusPendingUser {
		t.Errorf("unrelated item: got %q want %q", other.Status, models.StatusPendingUser)
	}
}

// blockingHarness parks inside the run until its context is cancelled, which
// is what an agent mid-turn looks like from the supervisor's side.
type blockingHarness struct {
	running   chan struct{}
	once      sync.Once
	cancelled bool
	sessionID string
}

func (h *blockingHarness) RunTurn(ctx context.Context, _ string, _ string, _ func(string)) (TurnResult, error) {
	h.once.Do(func() { close(h.running) })
	<-ctx.Done()
	h.cancelled = true
	// A cancelled turn still knows its session: the id arrives on the stream's
	// first event, not with the result.
	return TurnResult{SessionID: h.sessionID}, ctx.Err()
}

func TestShutdownStopsAnInFlightTurn(t *testing.T) {
	fh := &blockingHarness{running: make(chan struct{})}
	s := newTestSupervisor(t, fh)
	s.Start()
	s.Enqueue("item-1")

	select {
	case <-fh.running:
	case <-time.After(2 * time.Second):
		t.Fatal("harness never started")
	}
	if id, busy := s.Busy(); !busy || id != "item-1" {
		t.Errorf("Busy() = %q, %v; want item-1, true", id, busy)
	}

	done := make(chan struct{})
	go func() {
		s.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return; the worker outlived the process that owns it")
	}
	if !fh.cancelled {
		t.Error("the turn was not cancelled")
	}
	if _, busy := s.Busy(); busy {
		t.Error("still reports a dispatch in flight after shutdown")
	}
	s.Shutdown() // idempotent: a second call must not block or panic
}

func TestShutdownIsSafeWithoutStart(t *testing.T) {
	// Constructed then abandoned — Run returns this way when the watcher fails.
	s := newTestSupervisor(t, &fakeHarness{})
	done := make(chan struct{})
	go func() {
		s.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown blocked waiting for a worker that was never started")
	}
}

func TestInterruptedTurnStillPersistsItsSession(t *testing.T) {
	// The turn did happen, so the resume cursor has to advance to it. Leaving
	// it behind rewinds the thread past whatever the agent managed to do.
	fh := &blockingHarness{running: make(chan struct{}), sessionID: "session-partial"}
	s := newTestSupervisor(t, fh)
	s.Start()
	s.Enqueue("item-1")
	<-fh.running
	s.Shutdown()

	sf, err := loadSession(s.root)
	if err != nil {
		t.Fatalf("no session persisted after an interrupted turn: %v", err)
	}
	if sf.SessionID != "session-partial" {
		t.Errorf("session id = %q, want session-partial", sf.SessionID)
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

// liveSpyHarness reads the live log from inside the run, both before it emits
// anything and after — the run is the only point at which the log is supposed
// to exist, and the gap before the first event is where a stale one shows.
type liveSpyHarness struct {
	root   string
	itemID string
	before string
	during string
}

func (h *liveSpyHarness) RunTurn(_ context.Context, _ string, _ string, onEvent func(string)) (TurnResult, error) {
	h.before = ReadLive(h.root, h.itemID)
	if onEvent != nil {
		onEvent("mid-run line")
	}
	h.during = ReadLive(h.root, h.itemID)
	return TurnResult{SessionID: "s", ResultText: "ok"}, nil
}
