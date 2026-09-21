package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestReparentSubthreadPreservesItemAndActivityHistory(t *testing.T) {
	s := newTestStore(t)
	oldRoot, _ := s.CreateItem(models.ChannelInbox, "old root", "old body", models.TypeThread, models.StatusActive, "")
	newRoot, _ := s.CreateItem(models.ChannelInbox, "new root", "new body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(oldRoot.ID, "child", "child body", models.TypeThread, models.StatusPendingUser)
	if _, err := s.AddMention(child.ID, newRoot.ID); err != nil {
		t.Fatal(err)
	}
	childWithRelation, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	childWithRelation.Related = []string{oldRoot.ID}
	path := filepath.Join(s.Root, "INBOX", child.ID+".md")
	if err := store.WriteItem(childWithRelation, path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(child.ID, models.ActorUser, "keep this turn"); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("child path before move: %v", err)
	}

	got, err := s.ReparentItem(child.ID, newRoot.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parent != newRoot.ID {
		t.Fatalf("returned parent = %q, want %q", got.Parent, newRoot.ID)
	}
	moved, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Parent != newRoot.ID || moved.Channel != models.ChannelInbox || moved.Status != models.StatusPendingUser {
		t.Fatalf("moved item metadata = %+v", moved)
	}
	if moved.Body != before.Body || len(moved.Turns) != 1 || moved.Turns[0].Content != "keep this turn" {
		t.Fatalf("moved conversation changed: %+v", moved)
	}
	if len(moved.Related) != 1 || moved.Related[0] != oldRoot.ID || len(moved.Mentions) != 1 || moved.Mentions[0] != newRoot.ID {
		t.Fatalf("moved relations changed: related=%v mentions=%v", moved.Related, moved.Mentions)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("child path after move: %v", err)
	}

	oldActivities, err := s.ListActivities(oldRoot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldActivities) != 2 || oldActivities[0].Type != store.ActivitySubthreadCreated {
		t.Fatalf("old root activity history = %+v", oldActivities)
	}
	move := oldActivities[1]
	if move.Type != store.ActivitySubthreadMoved || move.ChildID != child.ID || move.FromRootID != oldRoot.ID || move.ToRootID != newRoot.ID {
		t.Fatalf("old root move activity = %+v", move)
	}
	newActivities, err := s.ListActivities(newRoot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(newActivities) != 1 || newActivities[0].Type != store.ActivitySubthreadMoved || newActivities[0].ChildID != child.ID {
		t.Fatalf("new root move activity = %+v", newActivities)
	}
}

func TestReparentParentRequiresAndAppliesFlattenChildren(t *testing.T) {
	s := newTestStore(t)
	oldRoot, _ := s.CreateItem(models.ChannelInbox, "old root", "old body", models.TypeThread, models.StatusActive, "")
	newRoot, _ := s.CreateItem(models.ChannelInbox, "new root", "new body", models.TypeThread, models.StatusActive, "")
	first, _ := s.CreateSubthread(oldRoot.ID, "first", "first body", models.TypeThread, models.StatusActive)
	second, _ := s.CreateSubthread(oldRoot.ID, "second", "second body", models.TypeThread, models.StatusArchived)

	if _, err := s.ReparentItem(oldRoot.ID, newRoot.ID, false); err == nil || !strings.Contains(err.Error(), "--flatten-children") {
		t.Fatalf("reparent without flatten error = %v, want flatten guidance", err)
	}
	unchanged, _ := s.GetItem(oldRoot.ID)
	if unchanged.Parent != "" {
		t.Fatalf("failed move changed old root parent to %q", unchanged.Parent)
	}

	if _, err := s.ReparentItem(oldRoot.ID, newRoot.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{oldRoot.ID, first.ID, second.ID} {
		item, err := s.GetItem(id)
		if err != nil {
			t.Fatal(err)
		}
		if item.Parent != newRoot.ID {
			t.Errorf("%s parent = %q, want %q", id, item.Parent, newRoot.ID)
		}
	}
	if root, _ := s.GetItem(newRoot.ID); root.Parent != "" {
		t.Fatalf("destination root parent = %q", root.Parent)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "ARCHIVE", second.ID+".md")); err != nil {
		t.Fatalf("archived child moved out of archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "INBOX", oldRoot.ID+".md")); err != nil {
		t.Fatalf("live parent moved out of inbox: %v", err)
	}
	if _, err := s.SetStatus(newRoot.ID, models.StatusArchived); err == nil || !strings.Contains(err.Error(), oldRoot.ID) {
		t.Fatalf("destination root closed despite moved open child: %v", err)
	}

	activities, err := s.ListActivities(newRoot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 3 {
		t.Fatalf("destination activities = %d, want one move per flattened item: %+v", len(activities), activities)
	}
	for _, activity := range activities {
		if activity.Type != store.ActivitySubthreadMoved || activity.ToRootID != newRoot.ID {
			t.Errorf("destination activity = %+v", activity)
		}
	}
}

func TestReparentRejectsInvalidFlattenAndRootRelationships(t *testing.T) {
	s := newTestStore(t)
	source, _ := s.CreateItem(models.ChannelInbox, "source", "source body", models.TypeThread, models.StatusActive, "")
	target, _ := s.CreateItem(models.ChannelInbox, "target", "target body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(target.ID, "target child", "target child body", models.TypeThread, models.StatusActive)

	if _, err := s.ReparentItem(source.ID, target.ID, true); err == nil || !strings.Contains(err.Error(), "only valid") {
		t.Fatalf("leaf flatten error = %v, want explicit invalid-flag error", err)
	}
	if _, err := s.ReparentItem(source.ID, child.ID, false); err == nil || !strings.Contains(err.Error(), "root item") {
		t.Fatalf("subthread destination error = %v", err)
	}
	if _, err := s.SetStatus(child.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(target.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReparentItem(source.ID, target.ID, false); err == nil || !strings.Contains(err.Error(), "terminal root") {
		t.Fatalf("terminal destination error = %v", err)
	}
	unchanged, _ := s.GetItem(source.ID)
	if unchanged.Parent != "" {
		t.Fatalf("invalid move changed source parent to %q", unchanged.Parent)
	}
}

func TestReparentArchivedRootKeepsArchivePlacement(t *testing.T) {
	s := newTestStore(t)
	source, _ := s.CreateItem(models.ChannelInbox, "source", "source body", models.TypeThread, models.StatusActive, "")
	target, _ := s.CreateItem(models.ChannelInbox, "target", "target body", models.TypeThread, models.StatusActive, "")
	if _, err := s.SetStatus(source.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReparentItem(source.ID, target.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "ARCHIVE", source.ID+".md")); err != nil {
		t.Fatalf("archived source changed file placement: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "INBOX", source.ID+".md")); !os.IsNotExist(err) {
		t.Fatalf("archived source unexpectedly exists in inbox, stat error = %v", err)
	}
}
