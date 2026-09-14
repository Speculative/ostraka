package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
)

const draftsDir = "drafts"

const newItemDraftFile = "new-item.json"

// NewItemDraft is the complete editor state for the one unwritten item the
// TUI can have open at a time. BodyStarted distinguishes an empty body editor
// from a title that has not advanced to the body step yet.
type NewItemDraft struct {
	Channel     models.Channel `json:"channel"`
	Parent      string         `json:"parent,omitempty"`
	RelatedFrom string         `json:"related_from,omitempty"`
	Title       string         `json:"title,omitempty"`
	Body        string         `json:"body,omitempty"`
	BodyStarted bool           `json:"body_started,omitempty"`
}

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

// LoadNewItemDraft returns the unfinished title/body flow. A missing draft is
// represented by the zero value, just like a missing turn draft is empty.
func (s *Store) LoadNewItemDraft() (NewItemDraft, error) {
	data, err := os.ReadFile(s.newItemDraftPath())
	if errors.Is(err, os.ErrNotExist) {
		return NewItemDraft{}, nil
	}
	if err != nil {
		return NewItemDraft{}, err
	}
	var draft NewItemDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		return NewItemDraft{}, err
	}
	return draft, nil
}

// SaveNewItemDraft atomically checkpoints an unfinished new item. A wholly
// empty title-stage draft has no useful state and is removed instead.
func (s *Store) SaveNewItemDraft(draft NewItemDraft) error {
	if strings.TrimSpace(draft.Title) == "" && strings.TrimSpace(draft.Body) == "" && !draft.BodyStarted {
		return s.ClearNewItemDraft()
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	path := s.newItemDraftPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

// ClearNewItemDraft discards the unfinished new-item flow after successful
// submission or an explicit cancellation.
func (s *Store) ClearNewItemDraft() error {
	err := os.Remove(s.newItemDraftPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) newItemDraftPath() string {
	return filepath.Join(s.Root, draftsDir, newItemDraftFile)
}
