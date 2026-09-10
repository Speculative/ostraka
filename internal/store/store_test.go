package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOnlyInboxChannelIsSupported(t *testing.T) {
	s := newTestStore(t)
	if len(models.Channels) != 1 || models.Channels[0] != models.ChannelInbox {
		t.Fatalf("supported channels = %v, want only inbox", models.Channels)
	}
	for _, channel := range []models.Channel{"asks", "handoff", "other"} {
		if _, err := s.CreateItem(channel, "title", "body", models.TypeThread, models.StatusActive, ""); err == nil {
			t.Errorf("CreateItem accepted unsupported channel %q", channel)
		}
	}
	if _, err := s.CreateItem(models.ChannelInbox, "title", "body", models.TypeThread, models.StatusActive, ""); err != nil {
		t.Fatalf("CreateItem rejected inbox: %v", err)
	}
}

func TestNewStoreCreatesOnlySupportedItemDirectories(t *testing.T) {
	root := t.TempDir()
	if _, err := store.NewStore(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"INBOX", "ARCHIVE"} {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || !info.IsDir() {
			t.Errorf("supported directory %s missing", name)
		}
	}
	for _, name := range []string{"ASKS", "HANDOFF"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("unsupported directory %s was created", name)
		}
	}
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "hello", "hello", models.TypeThread, models.StatusActive, "")
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
	if got.Channel != models.ChannelInbox {
		t.Errorf("Channel: got %q want %q", got.Channel, models.ChannelInbox)
	}
}

func TestListItems(t *testing.T) {
	s := newTestStore(t)
	s.CreateItem(models.ChannelInbox, "inbox 3", "inbox 3", models.TypeThread, models.StatusActive, "")
	s.CreateItem(models.ChannelInbox, "inbox 2", "inbox 2", models.TypeThread, models.StatusPendingUser, "")
	s.CreateItem(models.ChannelInbox, "inbox 1", "inbox 1", models.TypeThread, models.StatusActive, "")

	ch := models.ChannelInbox
	items, err := s.ListItems(store.ListOpts{Channel: &ch})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("want 3 inbox items, got %d", len(items))
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
		s.CreateItem(models.ChannelInbox, body, body, models.TypeThread, models.StatusActive, "")
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
	item, _ := s.CreateItem(models.ChannelInbox, "question", "question", models.TypeThread, models.StatusActive, "")

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
	item, _ := s.CreateItem(models.ChannelInbox, "q", "q", models.TypeThread, models.StatusActive, "")

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
	item, _ := s.CreateItem(models.ChannelInbox, "q", "q", models.TypeThread, models.StatusActive, "")

	if _, err := s.SetStatus(item.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}

	// Should still be retrievable after move to ARCHIVE
	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatalf("item not found after archiving: %v", err)
	}
	if got.Status != models.StatusArchived {
		t.Errorf("Status: got %q want %q", got.Status, models.StatusArchived)
	}
}

func TestSetStatusNormalizesLegacyDoneInput(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelInbox, "q", "q", models.TypeThread, models.StatusActive, "")

	updated, err := s.SetStatus(item.ID, models.Status("done"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != models.StatusArchived {
		t.Errorf("Status: got %q want %q", updated.Status, models.StatusArchived)
	}
}

func TestDeleteItem(t *testing.T) {
	s := newTestStore(t)
	item, _ := s.CreateItem(models.ChannelInbox, "q", "q", models.TypeThread, models.StatusActive, "")

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
	a, _ := s.CreateItem(models.ChannelInbox, "a", "a", models.TypeThread, models.StatusActive, "")
	b, _ := s.CreateItem(models.ChannelInbox, "b", "b", models.TypeThread, models.StatusActive, "")
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
		{models.StatusAgentAcknowledged, models.StatusPendingUser, true},
		// Parked states: an agent turn is not a request for the user to act.
		{models.StatusBacklog, models.StatusBacklog, false},
		{models.StatusPendingUser, models.StatusPendingUser, false},
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
	item, _ := s.CreateItem(models.ChannelInbox, "q", "q", models.TypeThread, models.StatusPendingAgent, "")

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
	item, _ := s.CreateItem(models.ChannelInbox, "q", "q", models.TypeThread, models.StatusBacklog, "")

	got, err := s.AddTurn(item.ID, models.ActorUser, "note to self")
	if err != nil {
		t.Fatalf("AddTurn: %v", err)
	}
	if got.Status != models.StatusBacklog {
		t.Errorf("got %q want %q", got.Status, models.StatusBacklog)
	}
}

// countAbsences reloads the list while a writer changes the item's status, and
// reports how many reloads failed to see it at all. pause paces the writer:
// zero hammers the store, which is useful for finding windows but is not how
// the app behaves.
func countAbsences(t *testing.T, pause time.Duration, rounds int, statuses ...models.Status) int {
	t.Helper()
	s := newTestStore(t)
	a, err := s.CreateItem(models.ChannelInbox, "A", "body", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateItem(models.ChannelInbox, "B", "body", models.TypeThread, models.StatusBacklog, ""); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < rounds; i++ {
			for _, st := range statuses {
				s.SetStatus(a.ID, st) //nolint:errcheck
				time.Sleep(pause)
			}
		}
	}()

	missing := 0
	for i := 0; i < 2000; i++ {
		items, err := s.ListItems(store.ListOpts{})
		if err != nil {
			continue
		}
		found := false
		for _, it := range items {
			if it.ID == a.ID {
				found = true
				break
			}
		}
		if !found {
			missing++
		}
	}
	<-done
	return missing
}

func TestItemStaysVisibleWhileItsStatusIsWritten(t *testing.T) {
	// os.WriteFile truncates before writing, so a reader arriving mid-write saw
	// a partial file, ParseItem failed, and ListItems silently skipped the item.
	// The TUI reloads on every file change and the supervisor writes a status
	// milliseconds after the TUI writes one, so reloads landed inside write
	// windows routinely — and the item vanished from the list, taking the
	// selection with it.
	if missing := countAbsences(t, 0, 200, models.StatusPendingAgent, models.StatusAgentAcknowledged); missing != 0 {
		t.Errorf("item was absent from %d reloads, want 0", missing)
	}
}

func TestItemStaysVisibleWhileItMovesToTheArchive(t *testing.T) {
	// A status change across the channel/archive boundary renames the file
	// between two directories, and allPaths globs them one at a time — so on
	// top of the write window there is a scan window. Writing before removing,
	// plus re-scanning when the listing changes underneath the read, closes it
	// at the pace a person actually archives things.
	//
	// It is not closed under an unpaced writer: a listing that never holds
	// still exhausts the bounded retry. Removing that last sliver would mean
	// not renaming across directories at all, which is a change to the on-disk
	// layout rather than to this function.
	if missing := countAbsences(t, time.Millisecond, 40, models.StatusArchived, models.StatusActive); missing != 0 {
		t.Errorf("item was absent from %d reloads, want 0", missing)
	}
}
