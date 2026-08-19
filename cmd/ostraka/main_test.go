package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/store"
	"github.com/spf13/cobra"
)

func TestTurnContent(t *testing.T) {
	tests := []struct {
		name         string
		stdin        string
		args         []string
		contentStdin bool
		want         string
		wantErr      bool
	}{
		{name: "positional", args: []string{"id", "short reply"}, want: "short reply"},
		{name: "stdin preserves multiline content", stdin: "first line\nsecond line\n", args: []string{"id"}, contentStdin: true, want: "first line\nsecond line\n"},
		{name: "blank positional content", args: []string{"id", " \t\n"}, wantErr: true},
		{name: "blank stdin content", stdin: "\n\t", args: []string{"id"}, contentStdin: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := turnContent(strings.NewReader(tt.stdin), tt.args, tt.contentStdin)
			if (err != nil) != tt.wantErr {
				t.Fatalf("turnContent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("turnContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestItemTurnArgumentRules(t *testing.T) {
	original := turnFlags.contentStdin
	t.Cleanup(func() { turnFlags.contentStdin = original })

	turnFlags.contentStdin = false
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id", "content"}); err != nil {
		t.Fatalf("positional form rejected: %v", err)
	}
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id"}); err == nil {
		t.Fatal("missing positional content was accepted")
	}

	turnFlags.contentStdin = true
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id"}); err != nil {
		t.Fatalf("stdin form rejected: %v", err)
	}
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id", "content"}); err == nil {
		t.Fatal("stdin form accepted positional content")
	}
}

func TestStoreForTUIInitializesAndContinues(t *testing.T) {
	t.Chdir(t.TempDir())

	originalRunTUI := runTUI
	t.Cleanup(func() { runTUI = originalRunTUI })
	var startedAt string
	runTUI = func(s *store.Store) error {
		startedAt = s.Root
		return nil
	}

	var output bytes.Buffer
	tuiCmd.SetIn(strings.NewReader("yes\n"))
	tuiCmd.SetOut(&output)
	t.Cleanup(func() {
		tuiCmd.SetIn(nil)
		tuiCmd.SetOut(nil)
	})

	if err := tuiCmd.RunE(tuiCmd, nil); err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(mustGetwd(t), ".ostraka")
	if startedAt != wantRoot {
		t.Fatalf("TUI started at root %q, want %q", startedAt, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(wantRoot, "INBOX")); err != nil {
		t.Fatalf("initialised project missing INBOX: %v", err)
	}
	for _, want := range []string{"Initialize Ostraka", "initialised " + wantRoot} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("prompt output missing %q:\n%s", want, output.String())
		}
	}
}

func TestStoreForTUICancellationDoesNotInitialize(t *testing.T) {
	t.Chdir(t.TempDir())

	originalRunTUI := runTUI
	t.Cleanup(func() { runTUI = originalRunTUI })
	runTUI = func(*store.Store) error {
		t.Fatal("TUI started after initialization was cancelled")
		return nil
	}

	var output bytes.Buffer
	cmd := newTUICommandForTest(strings.NewReader("n\n"), &output)
	s, err := storeForTUI(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		t.Fatalf("cancelled initialization returned store rooted at %q", s.Root)
	}
	if _, err := os.Stat(filepath.Join(mustGetwd(t), ".ostraka")); !os.IsNotExist(err) {
		t.Fatalf("cancelled initialization created project, stat error = %v", err)
	}
	if !strings.Contains(output.String(), "initialization cancelled") {
		t.Fatalf("cancellation output missing:\n%s", output.String())
	}
}

func TestStoreForTUIUsesExistingAncestorWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, ".ostraka")
	if _, err := store.NewStore(projectRoot); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)

	var output bytes.Buffer
	cmd := newTUICommandForTest(strings.NewReader(""), &output)
	s, err := storeForTUI(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Root != projectRoot {
		t.Fatalf("store root = %v, want %q", s, projectRoot)
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected initialization prompt:\n%s", output.String())
	}
}

func TestStoreForTUIReportsInitializationFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	root := filepath.Join(mustGetwd(t), ".ostraka")
	if err := os.WriteFile(root, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := newTUICommandForTest(strings.NewReader("y\n"), &output)
	_, err := storeForTUI(cmd)
	if err == nil || !strings.Contains(err.Error(), "initialize Ostraka") {
		t.Fatalf("initialization error = %v, want initialization failure", err)
	}
}

func newTUICommandForTest(in *strings.Reader, out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetIn(in)
	cmd.SetOut(out)
	return cmd
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}
