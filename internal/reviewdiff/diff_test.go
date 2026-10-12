package reviewdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndSelectBothSides(t *testing.T) {
	patch := "diff --git a/space name.go b/space name.go\nindex 123..456 100644\n--- a/space name.go\n+++ b/space name.go\n@@ -4,3 +4,4 @@ function\n same\n-old\n+new\n+extra\n end\n\\ No newline at end of file\n"
	files, err := Parse(patch)
	if err != nil || len(files) != 1 {
		t.Fatalf("parse: %v %#v", err, files)
	}
	f := files[0]
	if f.Path != "space name.go" {
		t.Fatalf("path %q", f.Path)
	}
	old, err := Select(f, 1, 5, "old")
	if err != nil || old.StartLine != 4 || old.EndLine != 6 || old.Excerpt != "same\nold\nend" {
		t.Fatalf("old: %#v %v", old, err)
	}
	new, err := Select(f, 1, 5, "new")
	if err != nil || new.StartLine != 4 || new.EndLine != 7 || new.Excerpt != "same\nnew\nextra\nend" {
		t.Fatalf("new: %#v %v", new, err)
	}
}

func TestParseQuotedBinaryAndDeleted(t *testing.T) {
	files, err := Parse("diff --git \"a/a\\tb\" \"b/a\\tb\"\nnew file mode 100644\nBinary files /dev/null and \"b/a\\tb\" differ\ndiff --git a/gone b/gone\ndeleted file mode 100644\n--- a/gone\n+++ /dev/null\n@@ -1 +0,0 @@\n-gone\n")
	if err != nil || len(files) != 2 {
		t.Fatalf("%#v %v", files, err)
	}
	if files[0].Path != "a\tb" || !files[0].Binary || files[0].Status != "added" {
		t.Fatalf("%#v", files[0])
	}
	s, err := Select(files[1], 1, 1, "old")
	if err != nil || s.Path != "gone" || s.Excerpt != "gone" || files[1].Status != "deleted" {
		t.Fatalf("%#v %v", s, err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

func write(t *testing.T, root, path, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCumulativeAndUntracked(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	write(t, root, "tracked", "base\n")
	runGit(t, root, "add", "tracked")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", "base")
	write(t, root, "tracked", "staged\n")
	runGit(t, root, "add", "tracked")
	write(t, root, "tracked", "working\n")
	write(t, root, "untracked", "question\n")
	write(t, root, "binary", "a\x00b")
	write(t, root, ".gitignore", "ignored\n")
	write(t, root, "ignored", "hidden\n")
	d, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, f := range d.Files {
		byPath[f.Path] = f
	}
	if _, ok := byPath["ignored"]; ok {
		t.Fatal("ignored file displayed")
	}
	f := byPath["tracked"]
	var added []string
	for _, l := range f.Lines {
		if l.Kind == '+' {
			added = append(added, l.Text)
		}
	}
	if strings.Join(added, "\n") != "working" {
		t.Fatalf("not cumulative: %#v", f)
	}
	if byPath["untracked"].Status != "untracked" || !byPath["binary"].Binary {
		t.Fatalf("untracked/binary: %#v", byPath)
	}
}

func TestCaptureUnbornBranch(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	write(t, root, "first", "staged\n")
	runGit(t, root, "add", "first")
	write(t, root, "first", "current\n")
	d, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if d.Head != "" || len(d.Files) != 1 || d.Files[0].Status != "added" || d.Files[0].Lines[1].Text != "current" {
		t.Fatalf("%#v", d)
	}
}

func TestSelectionRejectsSeparateHunks(t *testing.T) {
	f := File{Path: "x", Lines: []Line{{Text: "first", NewLine: 1}, {Text: "later", NewLine: 10}}}
	if _, err := Select(f, 0, 1, "new"); err == nil {
		t.Fatal("accepted discontinuous excerpt")
	}
}

func TestCaptureFullContextPreservesBothSides(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	original := strings.Repeat("context\n", 20)
	write(t, root, "file", original)
	runGit(t, root, "add", "file")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	write(t, root, "file", strings.Repeat("context\n", 10)+"changed\n"+strings.Repeat("context\n", 9))
	d, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	f := d.Files[0]
	if len(f.FullLines) <= len(f.Lines) {
		t.Fatal("full context does not expand diff")
	}
	f.Lines = f.FullLines
	old, err := Select(f, 0, len(f.Lines)-1, "old")
	if err != nil || old.Excerpt != strings.TrimSuffix(original, "\n") {
		t.Fatalf("old full context: %#v %v", old, err)
	}
	next, err := Select(f, 0, len(f.Lines)-1, "new")
	if err != nil || next.StartLine != 1 || next.EndLine != 20 || !strings.Contains(next.Excerpt, "changed") {
		t.Fatalf("new full context: %#v %v", next, err)
	}
}

func TestCaptureFullContextForModeOnlyChange(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "core.filemode", "true")
	write(t, root, "script", "echo hello\n")
	runGit(t, root, "add", "script")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	if err := os.Chmod(filepath.Join(root, "script"), 0755); err != nil {
		t.Fatal(err)
	}
	d, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 1 || len(d.Files[0].Lines) != 0 || len(d.Files[0].FullLines) != 1 {
		t.Fatalf("mode-only context: %#v", d.Files)
	}
	line := d.Files[0].FullLines[0]
	if line.OldLine != 1 || line.NewLine != 1 || line.Text != "echo hello" {
		t.Fatalf("mode-only source mapping: %#v", line)
	}
}

func TestCaptureFilesFollowDirectoryWalkOrder(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	for _, path := range []string{"z.go", "a.go", "a/z.go", "a/b/a.go"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
			t.Fatal(err)
		}
		write(t, root, path, "before\n")
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	for _, path := range []string{"z.go", "a.go", "a/z.go", "a/b/a.go"} {
		write(t, root, path, "after\n")
	}
	// Untracked files must interleave with tracked changes in the same walk.
	write(t, root, "a/b/new.go", "new\n")
	write(t, root, "b.go", "new\n")
	d, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range d.Files {
		paths = append(paths, file.Path)
	}
	want := "a/b/a.go,a/b/new.go,a/z.go,a.go,b.go,z.go"
	if strings.Join(paths, ",") != want {
		t.Fatalf("walk order = %v, want %s", paths, want)
	}
}
