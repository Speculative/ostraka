package store_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
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

func TestRenameItemPreservesConversationAndUpdatesTitle(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "old title", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(item.ID, models.ActorUser, "a reply"); err != nil {
		t.Fatal(err)
	}

	updated, err := s.RenameItem(item.ID, "  new title  ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "new title" {
		t.Fatalf("renamed title = %q, want %q", updated.Title, "new title")
	}
	if updated.Body != "body" || len(updated.Turns) != 1 || updated.Turns[0].Content != "a reply" {
		t.Fatalf("rename changed conversation: %+v", updated)
	}

	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new title" {
		t.Errorf("persisted title = %q, want %q", got.Title, "new title")
	}
}

func TestRenameItemRejectsInvalidTitleWithoutChangingItem(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "old title", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, title := range []string{"", "   ", "two\nlines", "carriage\rreturn"} {
		if _, err := s.RenameItem(item.ID, title); err == nil {
			t.Errorf("RenameItem(%q) accepted invalid title", title)
		}
	}
	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "old title" {
		t.Errorf("title after rejected rename = %q, want %q", got.Title, "old title")
	}
}

func TestCreateUsesShortCrockfordID(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "short ID", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}

	want := regexp.MustCompile(`^[0123456789abcdefghjkmnpqrstvwxyz]{4}-[0123456789abcdefghjkmnpqrstvwxyz]{4}$`)
	if !want.MatchString(item.ID) {
		t.Fatalf("item ID = %q, want four-four lowercase Crockford base32 symbols", item.ID)
	}
}

func TestLegacyTimestampIDCoexistsWithNewIDs(t *testing.T) {
	s := newTestStore(t)
	legacy := models.Item{
		ID:      "20260819-055535",
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  models.StatusActive,
		Created: time.Date(2026, 8, 19, 5, 55, 35, 0, time.UTC),
		Title:   "legacy",
		Body:    "legacy body",
	}
	if err := store.WriteItem(legacy, filepath.Join(s.Root, "INBOX", legacy.ID+".md")); err != nil {
		t.Fatal(err)
	}

	child, err := s.CreateSubthread(legacy.ID, "new child", "body", models.TypeThread, models.StatusBacklog)
	if err != nil {
		t.Fatalf("CreateSubthread under legacy ID: %v", err)
	}
	if child.Parent != legacy.ID {
		t.Errorf("new child parent = %q, want %q", child.Parent, legacy.ID)
	}
	if _, err := s.GetItem(legacy.ID); err != nil {
		t.Fatalf("legacy item is no longer readable: %v", err)
	}
	if _, err := s.GetItem(child.ID); err != nil {
		t.Fatalf("new item is not readable: %v", err)
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

func TestGroupsPersistOnRootsAndInheritThroughFamilies(t *testing.T) {
	s := newTestStore(t)
	root, err := s.CreateItemWithGroup(models.ChannelInbox, "grouped root", "body", models.TypeThread, models.StatusActive, "", "post-v1")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusPendingUser)
	if err != nil {
		t.Fatal(err)
	}
	ungrouped, err := s.CreateItem(models.ChannelInbox, "ungrouped", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}

	if child.Group != root.Group {
		t.Fatalf("created child group = %q, want %q", child.Group, root.Group)
	}
	childPath := filepath.Join(s.Root, "INBOX", child.ID+".md")
	childData, err := os.ReadFile(childPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(childData), "group:") {
		t.Fatalf("child persisted its derived group:\n%s", childData)
	}

	items, err := s.ListItems(store.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == root.ID || item.ID == child.ID {
			if item.Group != "post-v1" {
				t.Errorf("item %q group = %q, want post-v1", item.ID, item.Group)
			}
		}
	}
	group := "post-v1"
	grouped, err := s.ListItems(store.ListOpts{Group: &group})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped) != 2 || grouped[0].ID != root.ID || grouped[1].ID != child.ID {
		t.Fatalf("grouped items = %+v, want root and child", grouped)
	}
	ungroupedGroup := ""
	withoutGroup, err := s.ListItems(store.ListOpts{Group: &ungroupedGroup})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutGroup) != 1 || withoutGroup[0].ID != ungrouped.ID {
		t.Fatalf("ungrouped items = %+v, want %s", withoutGroup, ungrouped.ID)
	}

	if _, err := s.SetGroup(child.ID, "v1"); err != nil {
		t.Fatal(err)
	}
	updatedRoot, err := s.GetItem(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	updatedChild, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedRoot.Group != "v1" || updatedChild.Group != "v1" {
		t.Fatalf("after child assignment root=%q child=%q, want v1", updatedRoot.Group, updatedChild.Group)
	}
}

func TestValidateGroup(t *testing.T) {
	for _, group := range []string{"v1", "post-v1", "provider-work"} {
		if err := store.ValidateGroup(group); err != nil {
			t.Errorf("ValidateGroup(%q) = %v", group, err)
		}
	}
	for _, group := range []string{"V1", "post_v1", "post v1", "-v1", "v1-", "none"} {
		if err := store.ValidateGroup(group); err == nil {
			t.Errorf("ValidateGroup(%q) = nil, want error", group)
		}
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

func TestChildCannotBecomeLiveUnderArchivedRoot(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	if _, err := s.SetStatus(child.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(root.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetStatus(child.ID, models.StatusBacklog); err == nil ||
		!strings.Contains(err.Error(), "unarchive the parent or reparent the child") {
		t.Fatalf("reopened child under archived root: %v", err)
	}
	got, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusArchived {
		t.Fatalf("rejected status change persisted %q, want archived", got.Status)
	}

	if _, err := s.SetStatus(root.ID, models.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(child.ID, models.StatusBacklog); err != nil {
		t.Fatalf("reopened child after reopening root: %v", err)
	}
}

func TestAgentTurnCannotReopenChildUnderArchivedRoot(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	if _, err := s.SetStatus(child.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(root.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}

	// Simulate a legacy inconsistent file: an archived-root child was manually
	// made live. An agent turn would normally advance it to pending-user, but
	// that transition must use the same lifecycle guard as SetStatusBy.
	child.Status = models.StatusActive
	if err := os.Remove(filepath.Join(s.Root, "ARCHIVE", child.ID+".md")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteItem(child, filepath.Join(s.Root, "INBOX", child.ID+".md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(child.ID, models.ActorAgent, "answer"); err == nil ||
		!strings.Contains(err.Error(), "unarchive the parent or reparent the child") {
		t.Fatalf("agent turn reopened child under archived root: %v", err)
	}
	got, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusActive || len(got.Turns) != 0 {
		t.Fatalf("rejected agent turn changed child: status=%q turns=%d", got.Status, len(got.Turns))
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

func TestBacklogOrderFollowsExplicitMovesAndLifecycle(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateItem(models.ChannelInbox, "a", "a", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateItem(models.ChannelInbox, "b", "b", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateItem(models.ChannelInbox, "c", "c", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}

	assertBacklogOrder(t, s, []string{a.ID, b.ID, c.ID})
	if _, err := s.MoveBacklogRoot(b.ID, 1); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{a.ID, c.ID, b.ID})

	// Turns are conversation activity, not a reprioritization of parked work.
	if _, err := s.AddTurn(a.ID, models.ActorUser, "note"); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{a.ID, c.ID, b.ID})

	if _, err := s.SetStatus(c.ID, models.StatusActive); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{a.ID, b.ID})
	if _, err := s.SetStatus(c.ID, models.StatusBacklog); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{a.ID, b.ID, c.ID})
}

func TestBacklogOrderSeedsMissingFileFromActivity(t *testing.T) {
	s := newTestStore(t)
	old, err := s.CreateItem(models.ChannelInbox, "old", "old", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.CreateItem(models.ChannelInbox, "newer", "newer", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(newer.ID, models.ActorUser, "recent activity"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.Root, "BACKLOG_ORDER")); err != nil {
		t.Fatal(err)
	}

	order, err := s.BacklogOrder()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{newer.ID, old.ID}) {
		t.Fatalf("migration order = %v, want newer before old", order)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "BACKLOG_ORDER")); !os.IsNotExist(err) {
		t.Fatalf("reading a missing order file created it: stat error = %v", err)
	}

	if _, err := s.MoveBacklogRoot(old.ID, -1); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{old.ID, newer.ID})
}

func TestBacklogOrderUsesRootMembershipForChildrenAndCleanup(t *testing.T) {
	s := newTestStore(t)
	first, err := s.CreateItem(models.ChannelInbox, "first", "first", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateItem(models.ChannelInbox, "second", "second", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateSubthread(first.ID, "child", "child", models.TypeThread, models.StatusPendingAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(child.ID, models.StatusPendingUser); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{first.ID, second.ID})

	if _, err := s.MoveBacklogRoot(child.ID, 1); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{second.ID, first.ID})

	if _, err := s.SetStatus(first.ID, models.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(first.ID, models.StatusBacklog); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{second.ID, first.ID})

	if _, err := s.SetStatus(child.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(first.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem(second.ID); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{})
}

func TestBacklogOrderRemovesReparentedRoot(t *testing.T) {
	s := newTestStore(t)
	source, err := s.CreateItem(models.ChannelInbox, "source", "source", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "target", "target", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReparentItem(source.ID, target.ID, false); err != nil {
		t.Fatal(err)
	}
	assertBacklogOrder(t, s, []string{})
}

func assertBacklogOrder(t *testing.T, s *store.Store, want []string) {
	t.Helper()
	got, err := s.BacklogOrder()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backlog order = %v, want %v", got, want)
	}
	data, err := os.ReadFile(filepath.Join(s.Root, "BACKLOG_ORDER"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			persisted = append(persisted, strings.TrimSpace(line))
		}
	}
	if len(persisted) != len(want) {
		t.Fatalf("persisted backlog order = %v, want %v", persisted, want)
	}
	for i := range want {
		if persisted[i] != want[i] {
			t.Fatalf("persisted backlog order = %v, want %v", persisted, want)
		}
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
