package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"ostraka/internal/models"
)

var channelDirs = map[models.Channel]string{
	models.ChannelInbox:   "INBOX",
	models.ChannelAsks:    "ASKS",
	models.ChannelHandoff: "HANDOFF",
}

const archiveDir = "ARCHIVE"

type ListOpts struct {
	Channel *models.Channel
	Status  *models.Status
}

type Store struct {
	Root string
}

func FindRoot(start string) (string, error) {
	dir := start
	for {
		candidate := filepath.Join(dir, ".ostraka")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .ostraka/ directory found in %s or any parent", start)
}

func NewStore(root string) (*Store, error) {
	for _, sub := range append(dirValues(), archiveDir) {
		if err := os.MkdirAll(filepath.Join(root, sub), 0755); err != nil {
			return nil, err
		}
	}
	return &Store{Root: root}, nil
}

func dirValues() []string {
	dirs := make([]string, 0, len(channelDirs))
	for _, d := range channelDirs {
		dirs = append(dirs, d)
	}
	return dirs
}

func (s *Store) allPaths() ([]string, error) {
	var paths []string
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subdir := filepath.Join(s.Root, e.Name())
		files, err := filepath.Glob(filepath.Join(subdir, "*.md"))
		if err != nil {
			return nil, err
		}
		paths = append(paths, files...)
	}
	return paths, nil
}

func (s *Store) pathForID(id string) (string, error) {
	paths, err := s.allPaths()
	if err != nil {
		return "", err
	}
	for _, p := range paths {
		if filepath.Base(p) == id+".md" {
			return p, nil
		}
	}
	return "", fmt.Errorf("item %q not found", id)
}

func (s *Store) itemPath(item models.Item) string {
	if models.TerminalStatuses[item.Status] {
		return filepath.Join(s.Root, archiveDir, item.ID+".md")
	}
	return filepath.Join(s.Root, channelDirs[item.Channel], item.ID+".md")
}

func (s *Store) ListItems(opts ListOpts) ([]models.Item, error) {
	paths, err := s.allPaths()
	if err != nil {
		return nil, err
	}
	var items []models.Item
	for _, p := range paths {
		item, err := ParseItem(p)
		if err != nil {
			continue
		}
		if opts.Channel != nil && item.Channel != *opts.Channel {
			continue
		}
		if opts.Status != nil && item.Status != *opts.Status {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.Before(items[j].Created)
	})
	return items, nil
}

func (s *Store) GetItem(id string) (models.Item, error) {
	path, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	return ParseItem(path)
}

func (s *Store) CreateItem(channel models.Channel, body string, itemType models.ItemType, status models.Status, parent string) (models.Item, error) {
	now := time.Now().UTC()
	id := now.Format("20060102-150405")

	// Check for collision across all existing IDs
	paths, err := s.allPaths()
	if err != nil {
		return models.Item{}, err
	}
	existing := make(map[string]bool, len(paths))
	for _, p := range paths {
		existing[filepath.Base(p[:len(p)-3])] = true // strip .md
	}
	for existing[id] {
		b := make([]byte, 2)
		rand.Read(b)
		id = now.Format("20060102-150405") + "-" + hex.EncodeToString(b)
	}

	item := models.Item{
		ID:      id,
		Channel: channel,
		Type:    itemType,
		Status:  status,
		Created: now,
		Parent:  parent,
		Body:    body,
	}
	if err := WriteItem(item, s.itemPath(item)); err != nil {
		return models.Item{}, err
	}
	return item, nil
}

func (s *Store) AddTurn(id string, actor models.Actor, content string) (models.Item, error) {
	path, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	item, err := ParseItem(path)
	if err != nil {
		return models.Item{}, err
	}
	item.Turns = append(item.Turns, models.Turn{
		Actor:     actor,
		Timestamp: time.Now().UTC(),
		Content:   content,
	})
	return item, WriteItem(item, path)
}

func (s *Store) SetStatus(id string, status models.Status) (models.Item, error) {
	oldPath, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	item, err := ParseItem(oldPath)
	if err != nil {
		return models.Item{}, err
	}
	item.Status = status
	newPath := s.itemPath(item)
	if newPath != oldPath {
		if err := os.Remove(oldPath); err != nil {
			return models.Item{}, err
		}
	}
	return item, WriteItem(item, newPath)
}

func (s *Store) DeleteItem(id string) error {
	path, err := s.pathForID(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
