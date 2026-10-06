package store_test

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestSubthreadsStayOneLevelAndCreateActivity(t *testing.T) {
	s := newTestStore(t)
	root, err := s.CreateItem(models.ChannelInbox, "root", "root body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateSubthread(root.ID, "first", "first body", models.TypeThread, models.StatusPendingUser)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateSubthread(first.ID, "sibling", "sibling body", models.TypeThread, models.StatusBacklog)
	if err != nil {
		t.Fatal(err)
	}
	if second.Parent != root.ID {
		t.Fatalf("sibling parent = %q, want root %q", second.Parent, root.ID)
	}
	if _, err := s.CreateItem(models.ChannelInbox, "grandchild", "body", models.TypeThread, models.StatusBacklog, first.ID); err == nil {
		t.Fatal("CreateItem accepted a nested subthread")
	}
	activities, err := s.ListActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 || activities[0].Type != "subthread.created" {
		t.Fatalf("activities = %+v, want two creation events", activities)
	}
}

func TestClosingSubthreadNotifiesRootAndRootCannotCloseEarly(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	if _, err := s.SetStatus(root.ID, models.StatusArchived); err == nil {
		t.Fatal("root closed while child was open")
	}
	if _, err := s.SetStatus(child.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	activities, err := s.PendingActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	var closed models.Activity
	for _, activity := range activities {
		if activity.Type == "subthread.closed" {
			closed = activity
		}
	}
	if closed.ChildID != child.ID || closed.Result != string(models.StatusArchived) {
		t.Fatalf("closed activity = %+v", closed)
	}
	if err := s.MarkActivitiesHandled(root.ID, []string{closed.ID}); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingActivities(root.ID); err != nil || len(pending) != 1 {
		t.Fatalf("pending after handling close = %+v, err=%v; creation events should remain", pending, err)
	}
	if _, err := s.SetStatus(root.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
}

func TestCannotCreateSubthreadUnderTerminalRoot(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusActive, "")
	if _, err := s.SetStatus(root.ID, models.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubthread(root.ID, "late child", "body", models.TypeThread, models.StatusBacklog); err == nil {
		t.Fatal("created a subthread under an archived root")
	}
}

func TestMentionsAreDirectedAndMayUseChildren(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateItem(models.ChannelInbox, "root", "root body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(root.ID, "child", "child body", models.TypeThread, models.StatusActive)
	other, _ := s.CreateItem(models.ChannelInbox, "other", "other body", models.TypeThread, models.StatusActive, "")

	if _, err := s.AddMention(child.ID, other.ID); err != nil {
		t.Fatalf("child mention: %v", err)
	}
	updated, err := s.GetItem(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Parent != root.ID {
		t.Fatalf("parent changed while adding mention: got %q want %q", updated.Parent, root.ID)
	}
	if updated.Body != "child body\n\n@"+other.ID {
		t.Fatalf("mention body = %q", updated.Body)
	}
	if _, err := s.AddMention(child.ID, other.ID); err != nil {
		t.Fatalf("duplicate mention: %v", err)
	}
	updated, _ = s.GetItem(child.ID)
	if strings.Count(updated.Body, "@"+other.ID) != 1 {
		t.Fatalf("duplicate mention was appended: %q", updated.Body)
	}

	mentioned, err := s.MentionedItems(child.ID)
	if err != nil || len(mentioned) != 1 || mentioned[0].ID != other.ID {
		t.Fatalf("mentions = %+v, err=%v", mentioned, err)
	}
	backlinks, err := s.BacklinkItems(other.ID)
	if err != nil || len(backlinks) != 1 || backlinks[0].ID != child.ID {
		t.Fatalf("backlinks = %+v, err=%v", backlinks, err)
	}
}
