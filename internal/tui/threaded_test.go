package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/Speculative/ostraka/internal/supervisor"
)

func TestPrepareGroupedSortsByFamilyAttentionAndFoldsPerRoot(t *testing.T) {
	rootA := models.Item{ID: "a", Channel: models.ChannelInbox, Status: models.StatusActive, Created: time.Now(), Title: "A"}
	childA := models.Item{ID: "a-child", Parent: "a", Channel: models.ChannelInbox, Status: models.StatusPendingAgent, Created: rootA.Created.Add(time.Minute), Title: "A child"}
	rootB := models.Item{ID: "b", Channel: models.ChannelInbox, Status: models.StatusPendingUser, Created: rootA.Created.Add(time.Hour), Title: "B"}
	items, hidden := channelView(models.ChannelInbox).prepareGrouped([]models.Item{rootA, childA, rootB}, false, nil)
	if hidden != 0 || len(items) != 3 {
		t.Fatalf("items=%+v hidden=%d, want three visible rows", items, hidden)
	}
	if items[0].ID != rootA.ID || items[1].ID != childA.ID {
		t.Fatalf("family with urgent child did not sort first: %+v", items)
	}
	items, _ = channelView(models.ChannelInbox).prepareGrouped([]models.Item{rootA, childA, rootB}, false, map[string]bool{"a": true})
	if len(items) != 2 || items[0].ID != rootA.ID || items[1].ID != rootB.ID {
		t.Fatalf("folded family = %+v", items)
	}
}

func TestPrepareGroupedDetachesChildWhenParentIsOutsideView(t *testing.T) {
	child := models.Item{ID: "child", Parent: "archived-root", Channel: models.ChannelInbox, Status: models.StatusBacklog, Created: time.Now(), Title: "Child"}
	items, _ := channelView(models.ChannelInbox).prepareGrouped([]models.Item{child}, true, nil)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one detached row", items)
	}
	if items[0].Parent != "" {
		t.Fatalf("detached child parent = %q, want empty display parent", items[0].Parent)
	}
}

func TestPrepareGroupedArchiveGhostsLiveRootForArchivedChildren(t *testing.T) {
	created := time.Date(2026, 8, 17, 5, 0, 0, 0, time.UTC)
	root := models.Item{ID: "root", Channel: models.ChannelInbox, Status: models.StatusActive, Created: created, Title: "Root"}
	first := models.Item{ID: "first", Parent: root.ID, Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created.Add(time.Minute), Title: "First"}
	second := models.Item{ID: "second", Parent: root.ID, Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created.Add(2 * time.Minute), Title: "Second"}

	items, _ := archiveView.prepareGrouped([]models.Item{root, second, first}, false, nil)
	if len(items) != 3 || items[0].ID != root.ID || items[1].ID != first.ID || items[2].ID != second.ID {
		t.Fatalf("archived family = %+v, want ghost root followed by both children", items)
	}
	if items[0].Status != models.StatusActive {
		t.Fatalf("ghost root status = %q, want unchanged live status", items[0].Status)
	}
	for _, item := range items[1:] {
		if item.Parent != root.ID {
			t.Errorf("child %q parent = %q, want %q", item.ID, item.Parent, root.ID)
		}
	}
}

func TestPrepareGroupedArchiveExpandsArchivedFamily(t *testing.T) {
	created := time.Date(2026, 8, 17, 5, 0, 0, 0, time.UTC)
	root := models.Item{ID: "root", Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created, Title: "Root"}
	first := models.Item{ID: "first", Parent: root.ID, Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created.Add(time.Minute), Title: "First"}
	second := models.Item{ID: "second", Parent: root.ID, Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created.Add(2 * time.Minute), Title: "Second"}
	all := []models.Item{root, second, first}

	items, _ := archiveView.prepareGrouped(all, false, map[string]bool{root.ID: false})
	if len(items) != 3 || items[0].ID != root.ID || items[1].ID != first.ID || items[2].ID != second.ID {
		t.Fatalf("expanded archived family = %+v, want root followed by both children", items)
	}
	items, _ = archiveView.prepareGrouped(all, false, nil)
	if len(items) != 1 || items[0].ID != root.ID {
		t.Fatalf("default collapsed archived family = %+v, want root only", items)
	}
}

func TestPrepareGroupedArchiveDoesNotTreatRelatedRootAsChild(t *testing.T) {
	created := time.Date(2026, 8, 17, 5, 0, 0, 0, time.UTC)
	root := models.Item{ID: "root", Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created, Title: "Root", Related: []string{"related"}}
	child := models.Item{ID: "child", Parent: root.ID, Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created.Add(time.Minute), Title: "Child"}
	related := models.Item{ID: "related", Channel: models.ChannelInbox, Status: models.StatusArchived, Created: created.Add(2 * time.Minute), Title: "Related", Related: []string{root.ID}}

	items, _ := archiveView.prepareGrouped([]models.Item{root, child, related}, false, nil)
	if len(items) != 2 {
		t.Fatalf("default collapsed archive = %+v, want two root rows", items)
	}
	for _, item := range items {
		if item.Parent != "" {
			t.Fatalf("related archive root %q rendered as child of %q", item.ID, item.Parent)
		}
	}
}

func TestConversationEventsFollowTimestamps(t *testing.T) {
	base := time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC)
	root := models.Item{Turns: []models.Turn{
		{Actor: models.ActorUser, Timestamp: base.Add(2 * time.Minute)},
		{Actor: models.ActorAgent, Timestamp: base.Add(4 * time.Minute)},
	}}
	activities := []models.Activity{
		{Type: "subthread.created", Timestamp: base.Add(time.Minute)},
		{Type: "subthread.closed", Timestamp: base.Add(3 * time.Minute)},
	}

	events := conversationEvents(root, activities)
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	want := []conversationEventKind{
		conversationActivity,
		conversationTurn,
		conversationActivity,
		conversationTurn,
	}
	for i, kind := range want {
		if events[i].kind != kind {
			t.Errorf("event %d kind = %d, want %d", i, events[i].kind, kind)
		}
	}
}

func TestActivityReloadKeepsConversationAtBottom(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := models.Item{
		ID:      "20260817-040000",
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  models.StatusAgentAcknowledged,
		Created: time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC),
		Title:   "Root",
		Body:    strings.Repeat("opening context ", 30),
	}
	root.Turns = append(root.Turns, models.Turn{
		Actor:     models.ActorAgent,
		Timestamp: root.Created.Add(time.Minute),
		Content:   strings.Repeat("prior response ", 30),
	})
	if err := store.WriteItem(root, filepath.Join(s.Root, "INBOX", root.ID+".md")); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.width = 100
	m.height = 20
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m = m.recalcLayout()
	msg := itemsLoadedMsg{
		allItems:    []models.Item{root},
		view:        channelView(models.ChannelInbox),
		showBacklog: true,
	}
	next, _ := m.update(msg)
	m = next.(model)
	m.conv.GotoBottom()
	if !m.conv.AtBottom() {
		t.Fatal("fixture did not produce a scrollable conversation")
	}

	if _, err := s.CreateSubthread(root.ID, "child", "child body", models.TypeThread, models.StatusPendingUser); err != nil {
		t.Fatal(err)
	}
	next, _ = m.update(msg)
	m = next.(model)
	if !m.conv.AtBottom() {
		t.Fatal("activity reload lost the bottom scroll anchor")
	}
}

func TestLiveTraceRemovalRepairsConversationBottom(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := models.Item{
		ID:      "20260817-041000",
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  models.StatusAgentAcknowledged,
		Created: time.Date(2026, 8, 17, 4, 10, 0, 0, time.UTC),
		Title:   "Root",
		Body:    strings.Repeat("persisted history ", 80),
	}
	if err := store.WriteItem(root, filepath.Join(s.Root, "INBOX", root.ID+".md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "supervisor"), 0755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(s.Root, "supervisor", "live-"+root.ID+".txt")
	if err := os.WriteFile(livePath, []byte(strings.Repeat("working line\n", 5)), 0644); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.width = 100
	m.height = 20
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m = m.recalcLayout()
	msg := itemsLoadedMsg{
		allItems:    []models.Item{root},
		view:        channelView(models.ChannelInbox),
		showBacklog: true,
	}
	next, _ := m.update(msg)
	m = next.(model)
	m.conv.GotoBottom()
	if m.convLive == 0 || !m.conv.AtBottom() {
		t.Fatalf("fixture did not produce a live trace at the bottom: live=%d bottom=%v", m.convLive, m.conv.AtBottom())
	}

	if err := os.Remove(livePath); err != nil {
		t.Fatal(err)
	}
	next, _ = m.update(msg)
	m = next.(model)
	if m.convLive != 0 {
		t.Fatalf("live trace remained after removal: %d", m.convLive)
	}
	if !m.conv.AtBottom() {
		t.Fatal("removing the live trace left the reply viewport above its bottom")
	}
	if strings.Contains(supervisor.ReadLive(s.Root, root.ID), "working line") {
		t.Fatal("live fixture was not removed")
	}
}

func TestEmptyLiveTraceShowsWorkingHeader(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := models.Item{
		ID:      "20260817-041050",
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  models.StatusAgentAcknowledged,
		Created: time.Date(2026, 8, 17, 4, 10, 50, 0, time.UTC),
		Title:   "Root",
		Body:    "body",
	}
	if err := store.WriteItem(root, filepath.Join(s.Root, "INBOX", root.ID+".md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "supervisor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "supervisor", "live-"+root.ID+".txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.width = 100
	m.height = 20
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m = m.recalcLayout()
	next, _ := m.update(itemsLoadedMsg{
		allItems: []models.Item{root}, view: channelView(models.ChannelInbox), showBacklog: true,
	})
	m = next.(model)

	if m.convLive == 0 || !strings.Contains(m.conv.View(), "agent  ·  working") {
		t.Fatalf("empty live marker did not render the working header: live=%d view=%q", m.convLive, m.conv.View())
	}
}

func TestLiveTraceSurvivesPreAcknowledgementReload(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := models.Item{
		ID:      "20260817-041100",
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  models.StatusAgentAcknowledged,
		Created: time.Date(2026, 8, 17, 4, 11, 0, 0, time.UTC),
		Title:   "Root",
		Body:    "body",
	}
	root.Turns = []models.Turn{{
		Actor:     models.ActorAgent,
		Timestamp: root.Created.Add(-time.Minute),
		Content:   "previous reply",
	}}
	if err := store.WriteItem(root, filepath.Join(s.Root, "INBOX", root.ID+".md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "supervisor"), 0755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(s.Root, "supervisor", "live-"+root.ID+".txt")
	if err := os.WriteFile(livePath, []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.width = 100
	m.height = 20
	m.showBacklog = true
	m.backlogVisibilityInitialized = true
	m = m.recalcLayout()
	msg := itemsLoadedMsg{allItems: []models.Item{root}, view: channelView(models.ChannelInbox), showBacklog: true}
	next, _ := m.update(msg)
	m = next.(model)
	if m.convLive == 0 {
		t.Fatal("live trace was not rendered for acknowledged item")
	}

	stale := root
	stale.Status = models.StatusPendingAgent
	msg.allItems = []models.Item{stale}
	next, _ = m.update(msg)
	m = next.(model)
	if m.convLive == 0 {
		t.Fatal("stale pre-acknowledgement reload hid the live trace")
	}

	completed := stale
	completed.Status = models.StatusPendingUser
	completed.Turns = append(completed.Turns, models.Turn{Actor: models.ActorAgent, Timestamp: time.Now().UTC(), Content: "reply"})
	msg.allItems = []models.Item{completed}
	next, _ = m.update(msg)
	m = next.(model)
	if m.convLive != 0 {
		t.Fatal("live trace remained after a completed agent turn while its marker still existed")
	}

	if err := os.Remove(livePath); err != nil {
		t.Fatal(err)
	}
	msg.allItems = []models.Item{completed}
	next, _ = m.update(msg)
	m = next.(model)
	if m.convLive != 0 {
		t.Fatal("live trace remained after a completed agent turn")
	}
}

func TestCancelChildDraftSelectsParent(t *testing.T) {
	root := models.Item{ID: "root", Channel: models.ChannelInbox, Status: models.StatusActive, Title: "root"}
	child := models.Item{ID: "child", Parent: root.ID, Channel: models.ChannelInbox, Status: models.StatusBacklog, Title: "child"}
	other := models.Item{ID: "other", Channel: models.ChannelInbox, Status: models.StatusActive, Title: "other"}
	m := newModel(nil, nil, nil)
	m.items = []models.Item{root, child, other}
	m.allItems = m.items
	m.selected = len(m.items)
	m.draft = true
	m.draftSelected = true
	m.draftParent = root.ID
	m.mode = modeTitle

	m = m.cancelDraft()
	if m.draft {
		t.Fatal("cancelled child draft remained active")
	}
	if m.selected != 0 || m.selectedID() != root.ID {
		t.Fatalf("selected item after cancelling child draft = %q at %d, want parent %q at 0", m.selectedID(), m.selected, root.ID)
	}
}
