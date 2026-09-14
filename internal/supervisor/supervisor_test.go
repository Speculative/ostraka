package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

// noModelDiscovery satisfies the Harness interface's discovery method for
// fakes that don't exercise model selection.
type noModelDiscovery struct{}

func (noModelDiscovery) AvailableModels(context.Context) ([]ModelOption, error) { return nil, nil }

type fakeHarness struct {
	noModelDiscovery
	mu       sync.Mutex
	calls    []string // sessionID seen per call, in order
	models   []string // model seen per call, in order
	efforts  []string // effort seen per call, in order
	prompts  []string // prompts seen per call, in order
	sessions []string // sessionID to return per call, in order
	result   TurnResult
	err      error
}

func (f *fakeHarness) RunTurn(_ context.Context, prompt string, sessionID string, model string, effort string, _ func(string)) (TurnResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sessionID)
	f.models = append(f.models, model)
	f.efforts = append(f.efforts, effort)
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return TurnResult{}, f.err
	}
	idx := len(f.calls) - 1
	newSession := "session-" + string(rune('a'+idx))
	if idx < len(f.sessions) {
		newSession = f.sessions[idx]
	}
	result := f.result
	if result.SessionID == "" {
		result.SessionID = newSession
	}
	if result.ResultText == "" {
		result.ResultText = "ok"
	}
	return result, nil
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

func TestDispatchStartsFreshThenResumesPerItem(t *testing.T) {
	fh := &fakeHarness{}
	s := newTestSupervisor(t, fh)

	s.dispatch(enqueueMsg{itemID: "item-1"})
	s.dispatch(enqueueMsg{itemID: "item-2"})
	s.dispatch(enqueueMsg{itemID: "item-1"})

	fh.mu.Lock()
	defer fh.mu.Unlock()
	if len(fh.calls) != 3 {
		t.Fatalf("expected 3 harness calls, got %d", len(fh.calls))
	}
	if fh.calls[0] != "" {
		t.Errorf("first dispatch should start fresh (empty session), got %q", fh.calls[0])
	}
	if fh.calls[1] != "" {
		t.Errorf("a different item should start fresh, got %q", fh.calls[1])
	}
	if fh.calls[2] != "session-a" {
		t.Errorf("third dispatch should resume item-1's session, got %q", fh.calls[2])
	}
	if !strings.Contains(fh.prompts[0], "Ostraka's initial prompt for this item") {
		t.Errorf("fresh dispatch did not use the bootstrap prompt: %q", fh.prompts[0])
	}
	if !strings.Contains(fh.prompts[2], "continues the conversation") {
		t.Errorf("resumed dispatch did not use the continuation prompt: %q", fh.prompts[2])
	}
	if strings.Contains(fh.prompts[2], "initial prompt for this item") {
		t.Errorf("resumed dispatch incorrectly used session-start wording: %q", fh.prompts[2])
	}

	got, err := loadItemSession(s.root, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "session-c" {
		t.Errorf("expected item-1 session id session-c, got %q", got.SessionID)
	}
	other, err := loadItemSession(s.root, "item-2")
	if err != nil || other.SessionID != "session-b" {
		t.Errorf("item-2 session = %+v, %v", other, err)
	}
}

func TestStartNewSessionModelIsForwardedOnTheFreshDispatchAndCarriesForward(t *testing.T) {
	fh := &fakeHarness{}
	s := newTestSupervisor(t, fh)

	if err := s.StartNewSession("item-1", ProviderClaude, "opus", "high"); err != nil {
		t.Fatal(err)
	}
	s.dispatch(enqueueMsg{itemID: "item-1"}) // fresh: session-a
	s.dispatch(enqueueMsg{itemID: "item-1"}) // resumed: session-a -> session-b

	fh.mu.Lock()
	defer fh.mu.Unlock()
	if len(fh.models) != 2 {
		t.Fatalf("expected 2 harness calls, got %d", len(fh.models))
	}
	if fh.models[0] != "opus" {
		t.Errorf("fresh dispatch model = %q, want opus", fh.models[0])
	}
	if fh.models[1] != "opus" {
		t.Errorf("resumed dispatch should still forward the session's model, got %q", fh.models[1])
	}
	if fh.efforts[0] != "high" || fh.efforts[1] != "high" {
		t.Errorf("efforts = %v, want high carried through dispatch", fh.efforts)
	}

	_, model, effort, _, _ := s.Session("item-1")
	if model != "opus" || effort != "high" {
		t.Errorf("Session selection = (%q, %q), want (opus, high)", model, effort)
	}
}

func TestDispatchCachesUsageWhenProviderOmitsResolvedModel(t *testing.T) {
	fh := &fakeHarness{result: TurnResult{Context: ContextUsage{UsedTokens: 20, WindowTokens: 100}}}
	s := newTestSupervisor(t, fh)
	if err := s.StartNewSession("item-1", ProviderClaude, "gpt-5.6-sol", "xhigh"); err != nil {
		t.Fatal(err)
	}

	s.dispatch(enqueueMsg{itemID: "item-1"})

	info, ok := s.LastTurnInfo("item-1")
	if !ok {
		t.Fatal("usage was not cached when the provider omitted its model")
	}
	if info.Model != "gpt-5.6-sol" {
		t.Errorf("cached model = %q, want selected session model", info.Model)
	}
	if info.Context != (ContextUsage{UsedTokens: 20, WindowTokens: 100}) {
		t.Errorf("cached context = %+v", info.Context)
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

func TestDispatchFailureIsPersistedForTheUser(t *testing.T) {
	fh := &fakeHarness{err: context.DeadlineExceeded}
	s, st := newStoreBackedSupervisor(t, fh)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}

	s.dispatch(enqueueMsg{itemID: item.ID})

	failure, ok := s.DispatchError(item.ID)
	if !ok || !strings.Contains(failure, "deadline exceeded") {
		t.Fatalf("dispatch error = %q, present=%v", failure, ok)
	}
	after, _ := st.GetItem(item.ID)
	if after.Status != models.StatusPendingAgent {
		t.Errorf("status after failed dispatch = %q, want pending-agent", after.Status)
	}
}

func TestProviderReportedFailureIsTreatedAsDispatchError(t *testing.T) {
	fh := &fakeHarness{result: TurnResult{IsError: true, ErrorText: "quota exceeded"}}
	s, st := newStoreBackedSupervisor(t, fh)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}

	s.dispatch(enqueueMsg{itemID: item.ID})

	failure, ok := s.DispatchError(item.ID)
	if !ok || !strings.Contains(failure, "quota exceeded") {
		t.Fatalf("dispatch error = %q, present=%v", failure, ok)
	}
}

func TestSuccessfulDispatchClearsPreviousError(t *testing.T) {
	fh := &fakeHarness{}
	s, st := newStoreBackedSupervisor(t, fh)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDispatchError(s.root, item.ID, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}

	s.dispatch(enqueueMsg{itemID: item.ID})

	if failure, ok := s.DispatchError(item.ID); ok || failure != "" {
		t.Errorf("stale dispatch error = %q, present=%v", failure, ok)
	}
}

// statusSpyHarness records the item's status as observed from inside the run,
// which is the only place the in-progress marker is visible.
type statusSpyHarness struct {
	noModelDiscovery
	st     *store.Store
	itemID string
	during models.Status
	called bool
	err    error
}

func (h *statusSpyHarness) RunTurn(_ context.Context, _ string, _ string, _ string, _ string, _ func(string)) (TurnResult, error) {
	h.called = true
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

	if spy.called {
		t.Error("stale dispatch launched a harness turn")
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
	noModelDiscovery
	running   chan struct{}
	once      sync.Once
	cancelled bool
	sessionID string
}

func (h *blockingHarness) RunTurn(ctx context.Context, _ string, _ string, _ string, _ string, _ func(string)) (TurnResult, error) {
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

func TestInterruptStopsOnlyTheActiveTurnAndPreservesRecovery(t *testing.T) {
	fh := &blockingHarness{running: make(chan struct{}), sessionID: "session-partial"}
	s, st := newStoreBackedSupervisor(t, fh)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	s.Enqueue(item.ID)
	select {
	case <-fh.running:
	case <-time.After(2 * time.Second):
		t.Fatal("harness never started")
	}

	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if s.ctx.Err() != nil {
		t.Fatalf("per-turn interrupt cancelled the supervisor context: %v", s.ctx.Err())
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, busy := s.Busy(); !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("interrupted dispatch did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	after, _ := st.GetItem(item.ID)
	if after.Status != models.StatusPendingUser {
		t.Errorf("status after interrupt = %q, want pending-user", after.Status)
	}
	if failure, ok := s.DispatchError(item.ID); ok || failure != "" {
		t.Errorf("intentional interrupt left a dispatch warning: %q", failure)
	}
	sf, err := loadItemSession(s.root, item.ID)
	if err != nil || sf.SessionID != "session-partial" {
		t.Errorf("session after interrupt = %+v, %v; want preserved partial session", sf, err)
	}
	s.Shutdown()
}

type userTurnFollowupHarness struct {
	noModelDiscovery
	store        *store.Store
	itemID       string
	firstStarted chan struct{}
	releaseFirst chan struct{}

	mu      sync.Mutex
	calls   int
	prompts []string
}

func (h *userTurnFollowupHarness) RunTurn(_ context.Context, prompt string, _ string, _ string, _ string, _ func(string)) (TurnResult, error) {
	h.mu.Lock()
	h.calls++
	call := h.calls
	h.prompts = append(h.prompts, prompt)
	h.mu.Unlock()
	if call == 1 {
		close(h.firstStarted)
		<-h.releaseFirst
	} else {
		if _, err := h.store.AddTurn(h.itemID, models.ActorAgent, "follow-up answer"); err != nil {
			return TurnResult{}, err
		}
	}
	return TurnResult{SessionID: "follow-up-session"}, nil
}

func TestUserTurnDuringDispatchQueuesSerializedFollowup(t *testing.T) {
	h := &userTurnFollowupHarness{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	s, st := newStoreBackedSupervisor(t, h)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	h.store, h.itemID = st, item.ID
	s.seedTurnCounts()
	s.Start()
	s.Enqueue(item.ID)
	defer s.Shutdown()

	select {
	case <-h.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial dispatch did not start")
	}
	if _, err := st.AddTurn(item.ID, models.ActorUser, "first request during the run"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(item.ID, models.ActorUser, "second request during the run"); err != nil {
		t.Fatal(err)
	}
	// This is the watcher response to the external item turn. The active
	// dispatch must not overlap it; its unwind path owns the follow-up queue.
	s.EnqueuePendingActivityRoots()
	close(h.releaseFirst)

	deadline := time.Now().Add(2 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		h.mu.Lock()
		calls := h.calls
		h.mu.Unlock()
		if calls == 2 {
			after, itemErr := st.GetItem(item.ID)
			sf, sessionErr := loadItemSession(s.root, item.ID)
			if itemErr == nil && sessionErr == nil && after.Status == models.StatusPendingUser &&
				sf.PromptedTurns != nil && *sf.PromptedTurns == 2 {
				completed = true
				break
			}
		}
		time.Sleep(time.Millisecond)
	}
	h.mu.Lock()
	calls := h.calls
	prompts := append([]string(nil), h.prompts...)
	h.mu.Unlock()
	if calls != 2 || !completed {
		t.Fatalf("follow-up did not complete; harness calls = %d, want two", calls)
	}
	after, err := st.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != models.StatusPendingUser {
		t.Fatalf("item status after follow-up = %q, want pending-user", after.Status)
	}
	if len(after.Turns) != 3 || after.Turns[2].Content != "follow-up answer" {
		t.Fatalf("turns after follow-up = %+v", after.Turns)
	}
	if len(prompts) != 2 {
		t.Fatalf("prompts = %d, want two", len(prompts))
	}
	first := strings.Index(prompts[1], "first request during the run")
	second := strings.Index(prompts[1], "second request during the run")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("follow-up prompt did not batch unseen user turns in order: %q", prompts[1])
	}
	sf, err := loadItemSession(s.root, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sf.PromptedTurns == nil || *sf.PromptedTurns != 2 {
		t.Fatalf("prompted-turn cursor = %v, want 2", sf.PromptedTurns)
	}
}

func TestUnseenUserTurnsUsesPersistedAndLegacyBoundaries(t *testing.T) {
	item := models.Item{Turns: []models.Turn{
		{Actor: models.ActorUser, Content: "old user turn"},
		{Actor: models.ActorAgent, Content: "old agent turn"},
		{Actor: models.ActorUser, Content: "first unseen"},
		{Actor: models.ActorUser, Content: "second unseen"},
		{Actor: models.ActorAgent, Content: "agent reply appended during dispatch"},
	}}

	got := unseenUserTurns(item, intPointer(2))
	if len(got) != 2 || got[0] != "first unseen" || got[1] != "second unseen" {
		t.Fatalf("cursor-based unseen turns = %v", got)
	}

	item.Turns = append(item.Turns, models.Turn{Actor: models.ActorUser, Content: "legacy pending turn"})
	got = unseenUserTurns(item, nil)
	if len(got) != 1 || got[0] != "legacy pending turn" {
		t.Fatalf("legacy unseen turns = %v", got)
	}
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

	sf, err := loadItemSession(s.root, "item-1")
	if err != nil {
		t.Fatalf("no session persisted after an interrupted turn: %v", err)
	}
	if sf.SessionID != "session-partial" {
		t.Errorf("session id = %q, want session-partial", sf.SessionID)
	}
}

func TestBoundedItemContextMarksOmission(t *testing.T) {
	item := models.Item{ID: "item-1", Title: "title", Body: strings.Repeat("b", bootstrapItemContextMaxChars), Turns: []models.Turn{{Actor: models.ActorUser, Content: strings.Repeat("old", bootstrapItemContextMaxChars)}, {Actor: models.ActorUser, Content: "recent"}}}
	got := boundedItemContext(item)
	if !strings.Contains(got, "Context limit reached") {
		t.Errorf("bounded context did not mark omission: %q", got)
	}
	if !strings.Contains(got, "recent") {
		t.Errorf("bounded context lost newest reply: %q", got)
	}
}

func TestDispatchIncludesFinalUserTurnInNudge(t *testing.T) {
	fh := &fakeHarness{}
	s, st := newStoreBackedSupervisor(t, fh)
	item, err := st.CreateItem(models.ChannelInbox, "t", "b", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(item.ID, models.ActorUser, "Please work on this now."); err != nil {
		t.Fatal(err)
	}

	s.dispatch(enqueueMsg{itemID: item.ID})

	fh.mu.Lock()
	defer fh.mu.Unlock()
	if len(fh.prompts) != 1 {
		t.Fatalf("expected one prompt, got %d", len(fh.prompts))
	}
	if !strings.Contains(fh.prompts[0], "Please work on this now.") {
		t.Errorf("dispatch prompt does not include the final user turn: %q", fh.prompts[0])
	}
}

// liveSpyHarness reads the live log from inside the run, both before it emits
// anything and after — the run is the only point at which the log is supposed
// to exist, and the gap before the first event is where a stale one shows.
type liveSpyHarness struct {
	noModelDiscovery
	root         string
	itemID       string
	before       string
	beforeActive bool
	during       string
}

func (h *liveSpyHarness) RunTurn(_ context.Context, _ string, _ string, _ string, _ string, onEvent func(string)) (TurnResult, error) {
	h.before, h.beforeActive = ReadLiveState(h.root, h.itemID)
	if onEvent != nil {
		onEvent("mid-run line")
	}
	h.during = ReadLive(h.root, h.itemID)
	return TurnResult{SessionID: "s", ResultText: "ok"}, nil
}
