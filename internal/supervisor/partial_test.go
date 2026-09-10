package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestNewRetainsOrphanedLiveTraceAsInterrupted(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".ostraka")
	st, err := store.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(supervisorDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	item, err := st.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusAgentAcknowledged, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePath(root, item.ID), []byte("orphaned output\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := New(root)
	traces, err := st.ListPartialTraces(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].Status != "interrupted" || traces[0].Content != "orphaned output\n" {
		t.Fatalf("recovered traces = %+v", traces)
	}
	activities, err := st.ListActivities(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Type != store.ActivityAgentInterrupted {
		t.Fatalf("recovered activities = %+v", activities)
	}
	if got := ReadLive(root, item.ID); got != "" {
		t.Fatalf("orphaned live trace survived recovery: %q", got)
	}
	s.Shutdown()
}

type emittingTraceHarness struct {
	noModelDiscovery
	store  *store.Store
	itemID string
	post   bool
}

func (h *emittingTraceHarness) RunTurn(_ context.Context, _ string, _ string, _ string, _ string, onEvent func(string)) (TurnResult, error) {
	onEvent("thinking")
	onEvent("tool output")
	if h.post {
		if _, err := h.store.AddTurn(h.itemID, models.ActorAgent, "answer"); err != nil {
			return TurnResult{}, err
		}
	}
	return TurnResult{SessionID: "trace-session"}, nil
}

func TestDispatchRetainsPartialTraceAndLinksPostedTurn(t *testing.T) {
	h := &emittingTraceHarness{post: true}
	s, st := newStoreBackedSupervisor(t, h)
	item, err := st.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	h.store, h.itemID = st, item.ID

	s.dispatch(enqueueMsg{itemID: item.ID})

	updated, err := st.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := st.ListPartialTraces(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].Status != "completed" || traces[0].Content != "thinking\ntool output" {
		t.Fatalf("traces = %+v", traces)
	}
	if len(updated.Turns) != 1 || !traces[0].TurnTimestamp.Equal(updated.Turns[0].Timestamp) {
		t.Fatalf("trace turn timestamp = %v, turns = %+v", traces[0].TurnTimestamp, updated.Turns)
	}
	if got := ReadLive(s.root, item.ID); got != "" {
		t.Fatalf("live log survived dispatch: %q", got)
	}
}

type blockingTraceHarness struct {
	noModelDiscovery
	running chan struct{}
	once    bool
}

func (h *blockingTraceHarness) RunTurn(ctx context.Context, _ string, _ string, _ string, _ string, onEvent func(string)) (TurnResult, error) {
	onEvent("partial before interrupt")
	if !h.once {
		h.once = true
		close(h.running)
	}
	<-ctx.Done()
	return TurnResult{SessionID: "interrupted-session"}, ctx.Err()
}

func TestInterruptRetainsTraceAndAddsActivity(t *testing.T) {
	h := &blockingTraceHarness{running: make(chan struct{})}
	s, st := newStoreBackedSupervisor(t, h)
	item, err := st.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusPendingAgent, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	s.Enqueue(item.ID)
	select {
	case <-h.running:
	case <-time.After(2 * time.Second):
		t.Fatal("harness never started")
	}
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
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
	s.Shutdown()

	traces, err := st.ListPartialTraces(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].Status != "interrupted" {
		t.Fatalf("traces = %+v", traces)
	}
	activities, err := st.ListActivities(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Type != store.ActivityAgentInterrupted {
		t.Fatalf("activities = %+v", activities)
	}
}
