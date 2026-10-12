// Package reviewdiff reads the current Git working tree for the review UI.
// Captures are in-memory display data, not persisted review snapshots.
package reviewdiff

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Line struct {
	Text    string
	Kind    byte // ' ', '+', '-', '@' (hunk), or '\\' (metadata)
	OldLine int
	NewLine int
}

type File struct {
	Path      string
	OldPath   string
	Status    string // modified, added, deleted, untracked
	Binary    bool
	Notice    string
	Lines     []Line
	FullLines []Line
}

type Diff struct {
	Root  string
	Head  string // empty for an unborn branch
	Files []File
}

type Selection struct {
	Path      string
	Side      string // old or new
	StartLine int
	EndLine   int
	Excerpt   string
}

// Select returns the source excerpt on one side of an inclusive display range.
// Hunk headers and lines belonging only to the other side are excluded.
func Select(f File, from, to int, side string) (Selection, error) {
	if side != "old" && side != "new" {
		return Selection{}, fmt.Errorf("side must be old or new")
	}
	if from > to {
		from, to = to, from
	}
	if from < 0 || to >= len(f.Lines) {
		return Selection{}, fmt.Errorf("selection outside diff")
	}
	s := Selection{Path: f.Path, Side: side}
	if side == "old" && f.OldPath != "" {
		s.Path = f.OldPath
	}
	var lines []string
	for _, l := range f.Lines[from : to+1] {
		n := l.NewLine
		if side == "old" {
			n = l.OldLine
		}
		if n == 0 {
			continue
		}
		if s.StartLine == 0 {
			s.StartLine = n
		} else if n != s.EndLine+1 {
			return Selection{}, fmt.Errorf("select within a single hunk")
		}
		s.EndLine = n
		lines = append(lines, l.Text)
	}
	if len(lines) == 0 {
		return Selection{}, fmt.Errorf("selection contains no code on the %s side", side)
	}
	s.Excerpt = strings.Join(lines, "\n")
	return s, nil
}

func git(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(e.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// Capture accepts any directory in a repository. Tracked changes are the
// cumulative diff against HEAD; untracked text is displayed as added lines.
func Capture(ctx context.Context, dir string) (Diff, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Diff{}, err
	}
	root = strings.TrimSpace(root)
	head, headErr := git(ctx, root, "rev-parse", "--verify", "HEAD")
	d := Diff{Root: root, Head: strings.TrimSpace(head)}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/"}
	if headErr == nil {
		args = append(args, "HEAD", "--")
	} else {
		// On an unborn branch all index entries are additions. Use the empty
		// tree as the comparison so staged and unstaged contents are combined.
		args = append(args, "4b825dc642cb6eb9a060e54bf8d69288fbee4904", "--")
	}
	patch, err := git(ctx, root, args...)
	if err != nil {
		return Diff{}, err
	}
	d.Files, err = Parse(patch)
	if err != nil {
		return Diff{}, err
	}
	fullArgs := append([]string(nil), args...)
	fullArgs = append(fullArgs[:1], append([]string{"--unified=1000000"}, fullArgs[1:]...)...)
	fullPatch, err := git(ctx, root, fullArgs...)
	if err != nil {
		return Diff{}, err
	}
	fullFiles, err := Parse(fullPatch)
	if err != nil {
		return Diff{}, err
	}
	for i := range d.Files {
		for _, full := range fullFiles {
			if full.Path == d.Files[i].Path {
				d.Files[i].FullLines = full.Lines
				break
			}
		}
	}
	// A mode-only text change has no Git hunk, but still has full-file context.
	for i := range d.Files {
		f := &d.Files[i]
		if f.Binary || len(f.FullLines) > 0 || f.Status != "modified" {
			continue
		}
		path := filepath.Join(root, f.Path)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || strings.ContainsRune(string(data), 0) {
			continue
		}
		text := strings.TrimSuffix(string(data), "\n")
		if len(data) == 0 {
			continue
		}
		for j, line := range strings.Split(text, "\n") {
			f.FullLines = append(f.FullLines, Line{Text: line, Kind: ' ', OldLine: j + 1, NewLine: j + 1})
		}
	}
	paths, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return Diff{}, err
	}
	for _, path := range strings.Split(paths, "\x00") {
		if path == "" {
			continue
		}
		f := File{Path: path, Status: "untracked"}
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil {
			f.Notice = "File changed while reading"
			d.Files = append(d.Files, f)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filepath.Join(root, path))
			if err != nil {
				f.Notice = err.Error()
			} else {
				f.Lines = []Line{{Text: target, Kind: '+', NewLine: 1}}
			}
		} else if !info.Mode().IsRegular() {
			f.Notice = "Not a regular file"
		} else if info.Size() > 2*1024*1024 {
			f.Notice = "File exceeds 2 MiB display limit"
		} else {
			data, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				f.Notice = err.Error()
			} else if strings.ContainsRune(string(data), 0) {
				f.Binary = true
			} else {
				lines := strings.Split(string(data), "\n")
				if len(lines) > 0 && lines[len(lines)-1] == "" {
					lines = lines[:len(lines)-1]
				}
				for i, line := range lines {
					f.Lines = append(f.Lines, Line{Text: line, Kind: '+', NewLine: i + 1})
				}
			}
		}
		f.FullLines = f.Lines
		d.Files = append(d.Files, f)
	}
	sort.Slice(d.Files, func(i, j int) bool { return directoryOrder(d.Files[i].Path, d.Files[j].Path) })
	return d, nil
}

// Compare siblings by name, traversing a directory before the next sibling.
func directoryOrder(a, b string) bool {
	left, right := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < min(len(left), len(right)); i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}

var hunkRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func patchPath(s string) string {
	if strings.HasPrefix(s, "\"") {
		if p, err := strconv.Unquote(s); err == nil {
			s = p
		}
	}
	if s == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}

// Parse decodes a unified Git patch. Line.Text excludes the diff prefix.
// Metadata and hunk headers carry zero source line numbers.
func Parse(patch string) ([]File, error) {
	var files []File
	var f *File
	old, next := 0, 0
	inHunk := false
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			files = append(files, File{Status: "modified"})
			f = &files[len(files)-1]
			inHunk = false
			// Text patches have authoritative ---/+++ names. This fallback
			// also covers binary and mode-only changes, including quoted names.
			names := strings.TrimPrefix(line, "diff --git ")
			if strings.HasPrefix(names, "\"") {
				for i := 1; i < len(names); i++ {
					if names[i] == '\\' {
						i++
						continue
					}
					if names[i] == '"' {
						f.OldPath = patchPath(names[:i+1])
						f.Path = patchPath(strings.TrimSpace(names[i+1:]))
						break
					}
				}
			} else if i := strings.LastIndex(names, " b/"); i >= 0 {
				f.OldPath = patchPath(names[:i])
				f.Path = patchPath(names[i+1:])
			}
			continue
		}
		if f == nil {
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(line, "--- "):
				f.OldPath = patchPath(line[4:])
			case strings.HasPrefix(line, "+++ "):
				p := patchPath(line[4:])
				if p != "" {
					f.Path = p
				}
			case strings.HasPrefix(line, "new file mode "):
				f.Status = "added"
			case strings.HasPrefix(line, "deleted file mode "):
				f.Status = "deleted"
			case strings.HasPrefix(line, "Binary files "):
				f.Binary = true
			case strings.HasPrefix(line, "old mode "), strings.HasPrefix(line, "new mode "):
				f.Notice += line + "\n"
			}
		}
		if strings.HasPrefix(line, "@@ ") {
			m := hunkRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("invalid hunk header: %s", line)
			}
			old, _ = strconv.Atoi(m[1])
			next, _ = strconv.Atoi(m[2])
			inHunk = true
			f.Lines = append(f.Lines, Line{Text: line, Kind: '@'})
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		l := Line{Text: line[1:], Kind: line[0]}
		switch l.Kind {
		case ' ':
			l.OldLine = old
			l.NewLine = next
			old++
			next++
		case '-':
			l.OldLine = old
			old++
		case '+':
			l.NewLine = next
			next++
		case '\\':
		default:
			continue
		}
		f.Lines = append(f.Lines, l)
	}
	return files, nil
}
