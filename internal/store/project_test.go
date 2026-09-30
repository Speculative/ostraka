package store

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestProjectBriefReplaceCapsAndVersions(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProjectBrief("first"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProjectBrief("second"); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ProjectBriefHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Content != "first" {
		t.Fatalf("history = %#v", versions)
	}
	if err := s.ReplaceProjectBrief(strings.Repeat("x", ProjectBriefMaxChars+1)); err == nil {
		t.Fatal("oversized brief was accepted")
	}
	if got, _ := s.ProjectBrief(); got != "second" {
		t.Fatalf("brief after rejected update = %q", got)
	}
}

func TestProjectBriefKeepsTenPreviousVersions(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := s.ReplaceProjectBrief(string(rune('a' + i))); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := s.ProjectBriefHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 10 {
		t.Fatalf("history count = %d, want 10", len(versions))
	}
}

func TestAgentProjectChangesStayOnDispatchingItemAndUserChangesStaySilent(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateItem(models.ChannelInbox, "root", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateSubthread(root.ID, "child", "body", models.TypeThread, models.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ReplaceProjectBrief("user brief"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProjectInstructions("user instructions"); err != nil {
		t.Fatal(err)
	}
	if activities, err := s.ListActivities(root.ID); err != nil || len(activities) != 1 {
		t.Fatalf("user project edits changed root activities = %+v, err=%v", activities, err)
	}

	if err := s.ReplaceProjectBriefForItem(child.ID, "agent brief"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProjectInstructionsForItem(child.ID, "agent instructions"); err != nil {
		t.Fatal(err)
	}
	activities, err := s.ListActivities(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 {
		t.Fatalf("child project activities = %+v, want two", activities)
	}
	if activities[0].Type != ActivityProjectBriefChanged || activities[1].Type != ActivityProjectInstructionsChanged {
		t.Fatalf("child project activity types = %+v", activities)
	}
	for _, activity := range activities {
		if activity.Actor != models.ActorAgent || !activity.Handled || activity.Result != "changed" {
			t.Errorf("agent project activity = %+v", activity)
		}
	}

	if err := s.ReplaceProjectBriefForItem(child.ID, "agent brief"); err != nil {
		t.Fatal(err)
	}
	if activities, err := s.ListActivities(child.ID); err != nil || len(activities) != 2 {
		t.Fatalf("no-op brief created activity = %+v, err=%v", activities, err)
	}
}
