package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentOrientationIncludesExactReplyCommand(t *testing.T) {
	command := "go run ./cmd/ostraka item turn <item-id> --actor agent --content-stdin"
	got := AgentOrientation(command)
	for _, want := range []string{command, "real multiline content", "ends this dispatch"} {
		if !strings.Contains(got, want) {
			t.Fatalf("orientation missing %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "Final reply command:") {
		t.Fatalf("orientation omitted guidance: %q", got)
	}
}

func TestReplyCommandPrefersRepositoryLocalCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "ostraka"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "ostraka", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := ReplyCommand(root, "<item-id>")
	want := "go run ./cmd/ostraka item turn <item-id> --actor agent --content-stdin"
	if got != want {
		t.Errorf("ReplyCommand() = %q, want %q", got, want)
	}
}

func TestReplyCommandFallsBackToInstalledCLI(t *testing.T) {
	got := ReplyCommand(t.TempDir(), "<item-id>")
	want := "ostraka item turn <item-id> --actor agent --content-stdin"
	if got != want {
		t.Errorf("ReplyCommand() = %q, want %q", got, want)
	}
}
