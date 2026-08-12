package store

import (
	"errors"
	"os"
	"path/filepath"
)

const draftsDir = "drafts"

// LoadDraft returns the unsent body draft for an item. A missing draft is not
// an error: it simply means the item has not been composed on yet.
func (s *Store) LoadDraft(id string) (string, error) {
	data, err := os.ReadFile(s.draftPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveDraft atomically checkpoints an unsent body. An empty draft is removed
// rather than left behind as an indistinguishable empty file.
func (s *Store) SaveDraft(id, content string) error {
	path := s.draftPath(id)
	if content == "" {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(content)); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

// ClearDraft discards an unsent body draft after submission or cancellation.
func (s *Store) ClearDraft(id string) error {
	return s.SaveDraft(id, "")
}

func (s *Store) draftPath(id string) string {
	return filepath.Join(s.Root, draftsDir, id+".txt")
}
