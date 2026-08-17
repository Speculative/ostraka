package supervisor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

type activityHarness struct {
	noModelDiscovery
	store  *store.Store
	rootID string
	prompt string
	calls  int
}

func (h *activityHarness) RunTurn(_ context.Context, prompt, _ string, _, _ string, _ func(string)) (TurnResult, error) {
	h.calls++
	h.prompt = prompt
	if _, err := h.store.AddTurn(h.rootID, models.ActorAgent, "synthesized child decision"); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{SessionID: "activity-session", ResultText: "ok"}, nil
}

func TestChildActivityDispatchIncludesAndHandlesEvents(t *testing.T) {
	h := &activityHarness{}
	s, st := newStoreBackedSupervisor(t, h)
	root, _ := st.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusPendingAgent, "")
	child, _ := st.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	if _, err := st.SetStatus(child.ID, models.StatusDone); err != nil {
		t.Fatal(err)
	}
	h.store, h.rootID = st, root.ID
	s.dispatch(enqueueMsg{itemID: root.ID, activity: true})
	if !strings.Contains(h.prompt, "subthread.closed") || !strings.Contains(h.prompt, child.ID) {
		t.Fatalf("activity prompt = %q", h.prompt)
	}
	if pending, err := st.PendingActivities(root.ID); err != nil || len(pending) != 0 {
		t.Fatalf("pending activities after synthesis = %+v, err=%v", pending, err)
	}
}

func TestStaleActivityDispatchDoesNotLaunchSecondTurn(t *testing.T) {
	h := &activityHarness{}
	s, st := newStoreBackedSupervisor(t, h)
	root, _ := st.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusPendingAgent, "")
	child, _ := st.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	if _, err := st.SetStatus(child.ID, models.StatusDone); err != nil {
		t.Fatal(err)
	}
	h.store, h.rootID = st, root.ID

	// A normal user-turn dispatch can consume an activity that was already
	// queued for a follow-up. The queued activity message is then stale and
	// must not launch another provider turn after the first one replies.
	s.dispatch(enqueueMsg{itemID: root.ID})
	s.dispatch(enqueueMsg{itemID: root.ID, activity: true})

	if h.calls != 1 {
		t.Fatalf("harness calls = %d, want one after stale activity dispatch", h.calls)
	}
	after, err := st.GetItem(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != models.StatusPendingUser {
		t.Fatalf("root status after stale activity dispatch = %q, want pending-user", after.Status)
	}
}

type activityOverlapHarness struct {
	noModelDiscovery
	store        *store.Store
	rootID       string
	firstStarted chan struct{}
	releaseFirst chan struct{}

	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
}

func (h *activityOverlapHarness) RunTurn(_ context.Context, _ string, _ string, _, _ string, _ func(string)) (TurnResult, error) {
	h.mu.Lock()
	h.calls++
	call := h.calls
	h.active++
	if h.active > h.maxActive {
		h.maxActive = h.active
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.active--
		h.mu.Unlock()
	}()

	if call == 1 {
		close(h.firstStarted)
		<-h.releaseFirst
	}
	if _, err := h.store.AddTurn(h.rootID, models.ActorAgent, "synthesized child decision"); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{SessionID: "activity-overlap-session", ResultText: "ok"}, nil
}

func TestChildActivityDuringDispatchQueuesOneSerializedFollowUp(t *testing.T) {
	h := &activityOverlapHarness{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	s, st := newStoreBackedSupervisor(t, h)
	root, _ := st.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusPendingAgent, "")
	child, _ := st.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	h.store, h.rootID = st, root.ID
	s.Start()
	defer s.Shutdown()

	s.Enqueue(root.ID)
	select {
	case <-h.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial dispatch did not start")
	}
	if _, err := st.SetStatus(child.ID, models.StatusDone); err != nil {
		t.Fatal(err)
	}
	// The watcher can call this while the root is busy; it must defer the
	// activity dispatch until the current turn has finished.
	s.EnqueuePendingActivityRoots()
	close(h.releaseFirst)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := st.PendingActivities(root.ID)
		if err == nil && len(pending) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	pending, err := st.PendingActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("follow-up did not handle child activity: %+v", pending)
	}

	h.mu.Lock()
	calls, maxActive := h.calls, h.maxActive
	h.mu.Unlock()
	if calls != 2 {
		t.Fatalf("harness calls = %d, want initial turn plus one follow-up", calls)
	}
	if maxActive != 1 {
		t.Fatalf("maximum concurrent harness calls = %d, want 1", maxActive)
	}
}
