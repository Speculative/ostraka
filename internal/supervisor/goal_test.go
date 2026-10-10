package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

type goalCall struct {
	id      string
	prompt  string
	release chan struct{}
}

type goalHarness struct {
	noModelDiscovery
	calls chan goalCall
}

func (h *goalHarness) RunTurn(ctx context.Context, prompt, sessionID, model, effort string, onEvent func(string)) (TurnResult, error) {
	id, _ := ctx.Value(agentItemIDContextKey{}).(string)
	call := goalCall{id: id, prompt: prompt, release: make(chan struct{})}
	select {
	case h.calls <- call:
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	}
	select {
	case <-call.release:
		return TurnResult{SessionID: "goal-session"}, nil
	case <-ctx.Done():
		return TurnResult{SessionID: "goal-session"}, ctx.Err()
	}
}

func nextGoalCall(t *testing.T, h *goalHarness) goalCall {
	t.Helper()
	select {
	case call := <-h.calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("no goal dispatch")
		return goalCall{}
	}
}

func armGoal(t *testing.T, st *store.Store, item models.Item) models.Item {
	t.Helper()
	if _, err := st.SetMode(item.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	item, err := st.AddTurn(item.ID, models.ActorUser, "Keep working on the exact initiating goal.")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestGoalLoopsWithoutReplyAndReleasesUnrelatedWorkOnEnd(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	other, _ := st.CreateItem(models.ChannelInbox, "other", "body", models.TypeThread, models.StatusPendingAgent, "")
	owner = armGoal(t, st, owner)
	// A huge item history must not evict the durable goal from a fresh prompt.
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, strings.Repeat("history ", 5000)); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Shutdown()
	s.Enqueue(other.ID)
	first := nextGoalCall(t, h)
	if first.id != owner.ID || !strings.Contains(first.prompt, "Initiating user goal (repeated verbatim):\n"+owner.Goal.Prompt) || !strings.Contains(first.prompt, "ostraka goal end "+owner.ID) {
		t.Fatalf("first dispatch = %+v", first)
	}
	close(first.release) // No final item reply: supervisor must still continue.
	second := nextGoalCall(t, h)
	if second.id != owner.ID || !strings.Contains(second.prompt, owner.Goal.Prompt) || !strings.Contains(second.prompt, "active Ostraka goal") {
		t.Fatalf("continuation = %+v", second)
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	close(second.release)
	third := nextGoalCall(t, h)
	if third.id != other.ID {
		t.Fatalf("after end = %s, want unrelated item", third.id)
	}
	close(third.release)
	select {
	case extra := <-h.calls:
		t.Fatalf("unexpected restart: %s", extra.id)
	case <-time.After(650 * time.Millisecond):
	}
}

func TestRootGoalDelegatesBeforeContinuingAndIncludesUserClosure(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	s.Start()
	defer s.Shutdown()
	first := nextGoalCall(t, h)
	child, err := st.CreateSubthreadBy(owner.ID, "delegate", "do work", models.TypeThread, models.StatusActive, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := st.CreateSubthreadBy(owner.ID, "decision", "pick one", models.TypeThread, models.StatusPendingUser, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	close(first.release)
	delegated := nextGoalCall(t, h)
	if delegated.id != child.ID {
		t.Fatalf("delegate = %s, want %s", delegated.id, child.ID)
	}
	if _, err := st.AddTurn(child.ID, models.ActorAgent, "delegated work done"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(decision.ID, models.ActorUser, "Choose A"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(decision.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	close(delegated.release)
	continued := nextGoalCall(t, h)
	if continued.id != owner.ID || !strings.Contains(continued.prompt, "subthread.closed") || !strings.Contains(continued.prompt, decision.ID) || !strings.Contains(continued.prompt, "delegated work done") || !strings.Contains(continued.prompt, "Choose A") {
		t.Fatalf("closure missing from continuation: %+v", continued)
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	close(continued.release)
}

func TestChildGoalExcludesItsRootAndSiblings(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	root, _ := st.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusPendingAgent, "")
	owner, _ := st.CreateSubthread(root.ID, "goal", "body", models.TypeThread, models.StatusActive)
	sibling, _ := st.CreateSubthread(root.ID, "sibling", "body", models.TypeThread, models.StatusPendingAgent)
	owner = armGoal(t, st, owner)
	s.Start()
	defer s.Shutdown()
	s.Enqueue(root.ID)
	s.Enqueue(sibling.ID)
	call := nextGoalCall(t, h)
	if call.id != owner.ID || !strings.Contains(call.prompt, "Only this item belongs to the goal") {
		t.Fatalf("child dispatch = %+v", call)
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	close(call.release)
	next := nextGoalCall(t, h)
	if next.id == owner.ID {
		t.Fatal("child continued after end")
	}
	close(next.release)
}

func TestGoalInterruptPausesUntilExplicitResume(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	s.Start()
	defer s.Shutdown()
	_ = nextGoalCall(t, h)
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if !s.DispatchesPaused() {
		t.Fatal("goal interrupt did not pause queue")
	}
	select {
	case <-h.calls:
		t.Fatal("goal immediately restarted after interrupt")
	case <-time.After(650 * time.Millisecond):
	}
	if _, found, err := st.GoalItem(); err != nil || !found {
		t.Fatal("interrupt discarded goal")
	}
	s.ToggleDispatchPause()
	call := nextGoalCall(t, h)
	if call.id != owner.ID {
		t.Fatal("resume lost owner")
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	close(call.release)
}

func TestGoalProviderFailurePausesAndRetainsGoal(t *testing.T) {
	h := &fakeHarness{err: fmt.Errorf("provider unavailable")}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	s.dispatch(enqueueMsg{itemID: owner.ID, mode: models.AgentModeGoal})
	if !s.DispatchesPaused() {
		t.Fatal("provider failure would spin goal retries")
	}
	if goal, found, err := st.GoalItem(); err != nil || !found || goal.Goal == nil {
		t.Fatal("provider failure discarded goal")
	}
}

func TestGoalRestartsFromDurablePendingUserState(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, "checkpoint before shutdown"); err != nil {
		t.Fatal(err)
	}
	s.seedTurnCounts() // Neither a new user turn nor an explicit Enqueue exists.
	s.Start()
	defer s.Shutdown()
	call := nextGoalCall(t, h)
	if call.id != owner.ID {
		t.Fatal("durable goal did not recover")
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	close(call.release)
}

func TestGoalActivationAtAdmissionPreservesUnrelatedQueuedRequest(t *testing.T) {
	h := &fakeHarness{}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	other, _ := st.CreateItem(models.ChannelInbox, "other", "body", models.TypeThread, models.StatusPendingAgent, "")
	armGoal(t, st, owner)
	// Simulate a request selected immediately before goal activation, reaching
	// the atomic admission check just after activation.
	s.dispatch(enqueueMsg{itemID: other.ID, mode: models.AgentModeCode})
	if len(h.calls) != 0 {
		t.Fatal("unrelated work was admitted after activation")
	}
	s.queueMu.Lock()
	preserved := s.pendingQueued[other.ID]
	s.queueMu.Unlock()
	if !preserved {
		t.Fatal("goal admission lost queued work")
	}
}

type endedGoalFailureHarness struct {
	noModelDiscovery
	store   *store.Store
	ownerID string
}

func (h *endedGoalFailureHarness) RunTurn(context.Context, string, string, string, string, func(string)) (TurnResult, error) {
	if _, err := h.store.EndGoal(h.ownerID); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{}, fmt.Errorf("provider failed after cancellation")
}

func TestEndedGoalFailureDoesNotPauseUnrelatedWork(t *testing.T) {
	h := &endedGoalFailureHarness{}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	h.store, h.ownerID = st, owner.ID
	s.dispatch(enqueueMsg{itemID: owner.ID, mode: models.AgentModeGoal})
	if s.DispatchesPaused() {
		t.Fatal("an ended goal paused unrelated work")
	}
}

func TestGoalChildTurnsBatchRetryAndArriveDuringParentDispatch(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	a, err := st.CreateSubthreadBy(owner.ID, "a", "body", models.TypeThread, models.StatusPendingUser, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateSubthreadBy(owner.ID, "b", "body", models.TypeThread, models.StatusPendingUser, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Shutdown()
	first := nextGoalCall(t, h)
	for _, child := range []models.Item{a, b} {
		if _, err := st.AddTurn(child.ID, models.ActorAgent, "result-"+child.ID); err != nil {
			t.Fatal(err)
		}
	}
	close(first.release)
	second := nextGoalCall(t, h)
	for _, child := range []models.Item{a, b} {
		if !strings.Contains(second.prompt, "result-"+child.ID) {
			t.Fatalf("missing child result in prompt: %s", second.prompt)
		}
	}
	close(second.release) // No item reply: notifications must survive.
	third := nextGoalCall(t, h)
	if !strings.Contains(third.prompt, "result-"+a.ID) || !strings.Contains(third.prompt, "result-"+b.ID) {
		t.Fatal("reply-less dispatch consumed child turns")
	}
	if _, err := st.AddTurn(a.ID, models.ActorAgent, "late-child-result"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, "incorporated captured results"); err != nil {
		t.Fatal(err)
	}
	close(third.release)
	fourth := nextGoalCall(t, h)
	if !strings.Contains(fourth.prompt, "late-child-result") || strings.Contains(fourth.prompt, "result-"+a.ID) || strings.Contains(fourth.prompt, "result-"+b.ID) {
		t.Fatalf("wrong next batch: %s", fourth.prompt)
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, "done"); err != nil {
		t.Fatal(err)
	}
	close(fourth.release)
}

func TestGoalNormalPauseFinishesTurnAndResumesChildren(t *testing.T) {
	h := &goalHarness{calls: make(chan goalCall, 8)}
	s, st := newStoreBackedSupervisor(t, h)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	s.Start()
	defer s.Shutdown()
	first := nextGoalCall(t, h)
	if !s.ToggleDispatchPause() {
		t.Fatal("pause was not enabled")
	}
	child, err := st.CreateSubthreadBy(owner.ID, "delegate", "body", models.TypeThread, models.StatusActive, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	// Pausing leaves the provider context alive and permits a normal reply.
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, "finished while paused"); err != nil {
		t.Fatal(err)
	}
	close(first.release)
	select {
	case call := <-h.calls:
		t.Fatalf("dispatch while paused: %s", call.id)
	case <-time.After(650 * time.Millisecond):
	}
	item, err := st.GetItem(owner.ID)
	if err != nil || item.Goal == nil || item.Status != models.StatusPendingUser {
		t.Fatalf("running turn did not finish normally: %+v, %v", item, err)
	}
	if s.ToggleDispatchPause() {
		t.Fatal("resume did not clear pause")
	}
	delegated := nextGoalCall(t, h)
	if delegated.id != child.ID {
		t.Fatalf("resume skipped queued child: %s", delegated.id)
	}
	if _, err := st.AddTurn(child.ID, models.ActorAgent, "paused delegate result"); err != nil {
		t.Fatal(err)
	}
	close(delegated.release)
	continued := nextGoalCall(t, h)
	if continued.id != owner.ID || !strings.Contains(continued.prompt, "paused delegate result") || !strings.Contains(continued.prompt, owner.Goal.Prompt) {
		t.Fatalf("resume lost progress or goal: %+v", continued)
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, "done"); err != nil {
		t.Fatal(err)
	}
	close(continued.release)
	select {
	case call := <-h.calls:
		t.Fatalf("extra dispatch after end: %s", call.id)
	case <-time.After(650 * time.Millisecond):
	}
}

type postedReplyFailureHarness struct {
	noModelDiscovery
	store   *store.Store
	ownerID string
}

func (h *postedReplyFailureHarness) RunTurn(context.Context, string, string, string, string, func(string)) (TurnResult, error) {
	if _, err := h.store.AddTurn(h.ownerID, models.ActorAgent, "reply before provider failure"); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{}, fmt.Errorf("provider failed after posting reply")
}

func TestGoalFailedProviderAfterReplyRetainsNotificationsAcrossRestart(t *testing.T) {
	failing := &postedReplyFailureHarness{}
	s, st := newStoreBackedSupervisor(t, failing)
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	child, err := st.CreateSubthreadBy(owner.ID, "child", "body", models.TypeThread, models.StatusPendingUser, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(child.ID, models.ActorAgent, "unconsumed-child-result"); err != nil {
		t.Fatal(err)
	}
	failing.store, failing.ownerID = st, owner.ID
	s.dispatch(enqueueMsg{itemID: owner.ID, mode: models.AgentModeGoal, goalStartedAt: owner.Goal.StartedAt})
	if !s.DispatchesPaused() {
		t.Fatal("provider failure did not pause")
	}
	pending, err := st.PendingActivities(owner.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("failure consumed notifications: %+v, %v", pending, err)
	}
	// Construct a new supervisor/store from the same disk state.
	h := &goalHarness{calls: make(chan goalCall, 8)}
	restarted := New(st.Root)
	restarted.harness = h
	restarted.Start()
	defer restarted.Shutdown()
	call := nextGoalCall(t, h)
	if call.id != owner.ID || !strings.Contains(call.prompt, "unconsumed-child-result") {
		t.Fatalf("restart lost child result: %+v", call)
	}
	if _, err := st.AddTurn(owner.ID, models.ActorAgent, "successfully incorporated"); err != nil {
		t.Fatal(err)
	}
	close(call.release)
	next := nextGoalCall(t, h)
	if strings.Contains(next.prompt, "unconsumed-child-result") {
		t.Fatal("successful retry repeated handled notification")
	}
	if _, err := st.EndGoal(owner.ID); err != nil {
		t.Fatal(err)
	}
	close(next.release)
}

func TestGoalChildTurnsDoNotQueueIndependentParentWake(t *testing.T) {
	s, st := newStoreBackedSupervisor(t, &goalHarness{calls: make(chan goalCall, 8)})
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "body", models.TypeThread, models.StatusActive, "")
	owner = armGoal(t, st, owner)
	child, err := st.CreateSubthreadBy(owner.ID, "child", "body", models.TypeThread, models.StatusPendingUser, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	s.seedTurnCounts()
	for _, actor := range []models.Actor{models.ActorAgent, models.ActorUser} {
		if _, err := st.AddTurn(child.ID, actor, "new child turn"); err != nil {
			t.Fatal(err)
		}
		s.EnqueuePendingActivityRoots()
		for {
			select {
			case msg := <-s.queue:
				if msg.itemID == owner.ID {
					t.Fatalf("child turn queued a separate parent wake: %+v", msg)
				}
			default:
				goto drained
			}
		}
	drained:
	}
	pending, err := st.PendingActivities(owner.ID)
	if err != nil || len(pending) != 2 {
		t.Fatalf("notifications unavailable for normal goal continuation: %+v, %v", pending, err)
	}
}
