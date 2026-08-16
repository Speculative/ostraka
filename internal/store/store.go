package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ostraka/internal/models"
)

var channelDirs = map[models.Channel]string{
	models.ChannelInbox: "INBOX",
}

const archiveDir = "ARCHIVE"

type ListOpts struct {
	Channel *models.Channel
	Status  *models.Status
}

type Store struct {
	Root string
}

func ValidateChannel(channel models.Channel) error {
	if !models.ValidChannel(channel) {
		return fmt.Errorf("unsupported channel %q (only inbox is supported)", channel)
	}
	return nil
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

// listRetries bounds the re-scan below. One retry closes the window in
// practice; the cap is only there so a pathological writer cannot spin us.
const listRetries = 2

func (s *Store) ListItems(opts ListOpts) ([]models.Item, error) {
	if opts.Channel != nil {
		if err := ValidateChannel(*opts.Channel); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		items, unstable, err := s.listOnce(opts)
		if err != nil {
			return nil, err
		}
		// A path that existed during the directory scan but not by the time we
		// read it is an item being moved between a channel and the archive,
		// not a missing one. Re-scanning finds it at its new location; dropping
		// it would report the item as gone for one reload, which is enough to
		// lose the selection sitting on it.
		if !unstable || attempt == listRetries {
			return items, nil
		}
	}
}

func (s *Store) listOnce(opts ListOpts) (items []models.Item, unstable bool, err error) {
	paths, err := s.allPaths()
	if err != nil {
		return nil, false, err
	}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		item, err := ParseItem(p)
		if err != nil {
			if os.IsNotExist(err) {
				unstable = true
			}
			continue
		}
		// A move writes the new location before removing the old, so an item
		// can legitimately be readable from both at once. Report it once.
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true

		if opts.Channel != nil && item.Channel != *opts.Channel {
			continue
		}
		if opts.Status != nil && item.Status != *opts.Status {
			continue
		}
		items = append(items, item)
	}
	// A move renames across directories, and allPaths globs them one at a
	// time — so a scan can miss an item that was in the directory already
	// globbed and arrives in one not yet globbed. Nothing fails to parse in
	// that case, so re-scan and compare: a listing that changed underneath us
	// is one we cannot trust to be complete.
	if after, err := s.allPaths(); err == nil && !sameStrings(paths, after) {
		unstable = true
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.Before(items[j].Created)
	})
	return items, unstable, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func (s *Store) GetItem(id string) (models.Item, error) {
	path, err := s.pathForID(id)
	if err != nil {
		return models.Item{}, err
	}
	return ParseItem(path)
}

func (s *Store) CreateItem(channel models.Channel, title, body string, itemType models.ItemType, status models.Status, parent string) (models.Item, error) {
	if err := ValidateChannel(channel); err != nil {
		return models.Item{}, err
	}
	if err := ValidateTitle(title); err != nil {
		return models.Item{}, err
	}
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
		Title:   strings.TrimSpace(title),
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
	if actor == models.ActorAgent {
		if next, ok := StatusAfterAgentTurn(item.Status); ok {
			item.Status = next
			// A status change can move the file between channel and archive
			// directories, so re-derive the path rather than writing to the
			// one the item was read from.
			newPath := s.itemPath(item)
			if newPath != path {
				// Write the new location before dropping the old one. The
				// reverse order leaves a window where the item exists nowhere,
				// and a concurrent reload would show it as deleted.
				if err := WriteItem(item, newPath); err != nil {
					return models.Item{}, err
				}
				if err := os.Remove(path); err != nil {
					return models.Item{}, err
				}
				return item, nil
			}
		}
	}
	return item, WriteItem(item, path)
}

// StatusAfterAgentTurn gives the status an item moves to once the agent has
// spoken, and whether it moves at all. An agent turn hands the ball back to
// the user, mirroring the advance to pending-agent that a user turn triggers;
// without it an item stays flagged for the agent after it has already been
// answered and keeps resurfacing in the pending-agent queue.
//
// Backlog and the terminal statuses are left alone: those are parked states,
// and an agent turn is not a request for the user to do anything.
func StatusAfterAgentTurn(current models.Status) (models.Status, bool) {
	switch current {
	case models.StatusActive, models.StatusPendingAgent, models.StatusAgentAcknowledged:
		return models.StatusPendingUser, true
	}
	return current, false
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
	if err := WriteItem(item, newPath); err != nil {
		return models.Item{}, err
	}
	// Only after the new file exists, so the item is never absent from both
	// locations at once. A reader that lands in between sees it twice, which
	// is harmless — a reader that saw it in neither would show it as gone.
	if newPath != oldPath {
		if err := os.Remove(oldPath); err != nil {
			return models.Item{}, err
		}
	}
	return item, nil
}

func (s *Store) DeleteItem(id string) error {
	path, err := s.pathForID(id)
	if err != nil {
		return err
	}
	return os.Remove(path)
}
