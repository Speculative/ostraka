package store

import (
	"strings"
	"testing"
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
