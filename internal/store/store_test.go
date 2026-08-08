package store_test

import (
	"testing"
	"time"

	"ostraka/internal/models"
	"ostraka/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelAsks, "hello", "hello", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Error("expected non-empty ID")
	}

	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "hello" {
		t.Errorf("Body: got %q want %q", got.Body, "hello")
	}
	if got.Channel != models.ChannelAsks {
		t.Errorf("Channel: got %q want %q", got.Channel, models.ChannelAsks)
	}
}

func TestListItems(t *testing.T) {
	s := newTestStore(t)
	s.CreateItem(models.ChannelAsks, "ask 1", "ask 1", models.TypeThread, models.StatusActive, "")
	s.CreateItem(models.ChannelAsks, "ask 2", "ask 2", models.TypeThread, models.StatusPendingUser, "")
	s.CreateItem(models.ChannelInbox, "inbox 1", "inbox 1", models.TypeThread, models.StatusActive, "")

	ch := models.ChannelAsks
	items, err := s.ListItems(store.ListOpts{Channel: &ch})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Errorf("want 2 asks, got %d", len(items))
	}

	st := models.StatusPendingUser
	pending, err := s.ListItems(store.ListOpts{Status: &st})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("want 1 pending, got %d", len(pending))
	}
}

func TestListSortedByCreated(t *testing.T) {
	s := newTestStore(t)
	for _, body := range []string{"first", "second", "third"} {
		s.CreateItem(models.ChannelHandoff, body, body, models.TypeThread, models.StatusActive, "")
		time.Sleep(time.Millisecond) // ensure distinct timestamps
	}
	items, err := s.ListItems(store.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3, got %d", len(items))
	}
	if items[0].Body != "first" || items[2].Body != "third" {
		t.Errorf("wrong order: %v", []string{items[0].Body, items[1].Body, items[2].Body})
	}
}

func TestAddTurn(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelAsks, "question", "question", models.TypeThread, models.StatusActive, "")

	updated, err := s.AddTurn(item.ID, models.ActorAgent, "my answer")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(updated.Turns))
	}
	if updated.Turns[0].Content != "my answer" {
		t.Errorf("turn content: got %q", updated.Turns[0].Content)
	}

	// Verify persisted
	got, _ := s.GetItem(item.ID)
	if len(got.Turns) != 1 {
		t.Errorf("persisted turns: want 1, got %d", len(got.Turns))
	}
}

func TestSetStatus(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelAsks, "q", "q", models.TypeThread, models.StatusActive, "")

	_, err := s.SetStatus(item.ID, models.StatusPendingUser)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetItem(item.ID)
	if got.Status != models.StatusPendingUser {
		t.Errorf("Status: got %q want %q", got.Status, models.StatusPendingUser)
	}
}

func TestSetStatusArchives(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelAsks, "q", "q", models.TypeThread, models.StatusActive, "")

	if _, err := s.SetStatus(item.ID, models.StatusDone); err != nil {
		t.Fatal(err)
	}

	// Should still be retrievable after move to ARCHIVE
	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatalf("item not found after archiving: %v", err)
	}
	if got.Status != models.StatusDone {
		t.Errorf("Status: got %q want %q", got.Status, models.StatusDone)
	}
}

func TestDeleteItem(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelAsks, "q", "q", models.TypeThread, models.StatusActive, "")

	if err := s.DeleteItem(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(item.ID); err == nil {
		t.Error("expected error after deletion")
	}
}

func TestGetNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetItem("nonexistent"); err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestIDCollisionHandled(t *testing.T) {
	s := newTestStore(t)
	// Create two items rapidly; IDs should be unique
	a, _ := s.CreateItem(models.ChannelAsks, "a", "a", models.TypeThread, models.StatusActive, "")
	b, _ := s.CreateItem(models.ChannelAsks, "b", "b", models.TypeThread, models.StatusActive, "")
	if a.ID == b.ID {
		t.Errorf("ID collision: both got %q", a.ID)
	}
}

func TestStatusAfterAgentTurn(t *testing.T) {
	for _, tc := range []struct {
		current models.Status
		want    models.Status
		moved   bool
	}{
		{models.StatusPendingAgent, models.StatusPendingUser, true},
		{models.StatusActive, models.StatusPendingUser, true},
		// Parked states: an agent turn is not a request for the user to act.
		{models.StatusBacklog, models.StatusBacklog, false},
		{models.StatusPendingUser, models.StatusPendingUser, false},
		{models.StatusDone, models.StatusDone, false},
		{models.StatusArchived, models.StatusArchived, false},
	} {
		got, moved := store.StatusAfterAgentTurn(tc.current)
		if got != tc.want || moved != tc.moved {
			t.Errorf("StatusAfterAgentTurn(%q) = (%q, %v), want (%q, %v)",
				tc.current, got, moved, tc.want, tc.moved)
		}
	}
}

func TestAddTurnHandsBackOnAgentTurn(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelAsks, "q", "q", models.TypeThread, models.StatusPendingAgent, "")

	got, err := s.AddTurn(item.ID, models.ActorAgent, "answered")
	if err != nil {
		t.Fatalf("AddTurn: %v", err)
	}
	if got.Status != models.StatusPendingUser {
		t.Errorf("in-memory status: got %q want %q", got.Status, models.StatusPendingUser)
	}
	// The advance has to survive the write, or the next list still shows it
	// waiting on the agent.
	reread, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if reread.Status != models.StatusPendingUser {
		t.Errorf("persisted status: got %q want %q", reread.Status, models.StatusPendingUser)
	}
}

func TestAddTurnLeavesUserTurnsAlone(t *testing.T) {
	// User-turn status logic belongs to the caller: the TUI decides whether a
	// turn dispatches, and a backlog item must stay quiet.
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelAsks, "q", "q", models.TypeThread, models.StatusBacklog, "")

	got, err := s.AddTurn(item.ID, models.ActorUser, "note to self")
	if err != nil {
		t.Fatalf("AddTurn: %v", err)
	}
	if got.Status != models.StatusBacklog {
		t.Errorf("got %q want %q", got.Status, models.StatusBacklog)
	}
}
