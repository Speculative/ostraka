package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ProjectBriefMaxChars = 6000

const (
	projectInstructionsFile = "PROJECT_INSTRUCTIONS.md"
	projectBriefFile        = "PROJECT_BRIEF.md"
	projectBriefHistoryDir  = "PROJECT_BRIEF_HISTORY"
)

// ProjectBriefVersion is an immutable saved brief. The current brief is not a
// version; history contains the previous complete values.
type ProjectBriefVersion struct {
	Name    string
	Created time.Time
	Content string
}

func (s *Store) projectPath(name string) string { return filepath.Join(s.Root, name) }

func (s *Store) ProjectInstructions() (string, error) {
	return readOptional(s.projectPath(projectInstructionsFile))
}

func (s *Store) ReplaceProjectInstructions(content string) error {
	return writeFileAtomic(s.projectPath(projectInstructionsFile), []byte(strings.TrimSpace(content)+"\n"))
}

func (s *Store) ProjectBrief() (string, error) {
	return readOptional(s.projectPath(projectBriefFile))
}

// ReplaceProjectBrief writes the whole new brief, retaining the prior version
// when it materially changes. The explicit API is the only supported agent
// write path, so the size limit is never bypassed by an append.
func (s *Store) ReplaceProjectBrief(content string) error {
	content = strings.TrimSpace(content)
	if len([]rune(content)) > ProjectBriefMaxChars {
		return fmt.Errorf("project brief exceeds %d characters", ProjectBriefMaxChars)
	}
	old, err := s.ProjectBrief()
	if err != nil {
		return err
	}
	if old == content {
		return nil
	}
	if old != "" {
		dir := s.projectPath(projectBriefHistoryDir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		name := time.Now().UTC().Format("20060102-150405.000000000") + ".md"
		if err := writeFileAtomic(filepath.Join(dir, name), []byte(old+"\n")); err != nil {
			return err
		}
		if err := s.pruneProjectBriefHistory(); err != nil {
			return err
		}
	}
	return writeFileAtomic(s.projectPath(projectBriefFile), []byte(content+"\n"))
}

func (s *Store) ProjectBriefHistory() ([]ProjectBriefVersion, error) {
	dir := s.projectPath(projectBriefHistoryDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	versions := make([]ProjectBriefVersion, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		t, err := time.Parse("20060102-150405.000000000", strings.TrimSuffix(e.Name(), ".md"))
		if err != nil {
			continue
		}
		content, err := readOptional(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		versions = append(versions, ProjectBriefVersion{Name: e.Name(), Created: t, Content: content})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Created.After(versions[j].Created) })
	return versions, nil
}

func (s *Store) pruneProjectBriefHistory() error {
	versions, err := s.ProjectBriefHistory()
	if err != nil {
		return err
	}
	if len(versions) <= 10 {
		return nil
	}
	for _, v := range versions[10:] {
		if err := os.Remove(filepath.Join(s.projectPath(projectBriefHistoryDir), v.Name)); err != nil {
			return err
		}
	}
	return nil
}

func readOptional(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}
