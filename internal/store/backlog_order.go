package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

const backlogOrderFilename = "BACKLOG_ORDER"

// BacklogOrder returns the root IDs that currently belong in the explicit
// backlog order. A missing order file is a migration case: the result follows
// the order the old activity-based view would have shown, but the file is not
// created until a lifecycle change or an explicit move needs to persist it.
func (s *Store) BacklogOrder() ([]string, error) {
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return nil, err
	}
	return s.BacklogOrderFor(items)
}

// BacklogOrderFor returns the backlog order using an item snapshot the caller
// already loaded. This avoids rescanning and reparsing the store when a TUI
// refresh needs both the item list and its backlog order.
func (s *Store) BacklogOrderFor(items []models.Item) ([]string, error) {
	ids, exists, err := s.readBacklogOrder()
	if err != nil {
		return nil, err
	}
	if !exists {
		return initialBacklogOrder(items), nil
	}
	return normalizeBacklogOrder(ids, items, true), nil
}

// MoveBacklogRoot moves a root family by delta backlog positions. Children are
// accepted as a convenience for the grouped TUI, but the root remains the
// persisted ordering unit. A move past either end is a successful no-op.
func (s *Store) MoveBacklogRoot(id string, delta int) (models.Item, error) {
	item, err := s.GetItem(id)
	if err != nil {
		return models.Item{}, err
	}
	rootID := item.ID
	if item.Parent != "" {
		rootID = item.Parent
		item, err = s.GetItem(rootID)
		if err != nil {
			return models.Item{}, err
		}
	}
	if item.Parent != "" {
		return models.Item{}, errNotRootBacklog(item.ID)
	}
	if item.Status != models.StatusBacklog {
		return models.Item{}, errNotRootBacklog(item.ID)
	}

	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return models.Item{}, err
	}
	ids, exists, err := s.readBacklogOrder()
	if err != nil {
		return models.Item{}, err
	}
	if !exists {
		ids = initialBacklogOrder(items)
	} else {
		ids = normalizeBacklogOrder(ids, items, true)
	}
	index := indexOf(ids, rootID)
	if index < 0 {
		// The item was just returned to backlog through a path that did not yet
		// materialize the order file. Treat it like newly parked work.
		ids = append(ids, rootID)
		index = len(ids) - 1
	}
	if delta == 0 {
		return item, nil
	}
	target := index + delta
	if target < 0 || target >= len(ids) {
		return item, nil
	}
	ids[index], ids[target] = ids[target], ids[index]
	if err := s.writeBacklogOrder(ids); err != nil {
		return models.Item{}, err
	}
	return item, nil
}

// SetBacklogOrder persists a complete ordering of the current backlog roots.
// Requiring a full permutation prevents a stale TUI preview from dropping a
// root that entered backlog while the preview was open.
func (s *Store) SetBacklogOrder(ids []string) error {
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return err
	}
	roots := backlogRoots(items)
	if len(ids) != len(roots) {
		return fmt.Errorf("backlog order has %d roots, want %d", len(ids), len(roots))
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := roots[id]; !ok {
			return fmt.Errorf("item %q is not a backlog root", id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("backlog order contains duplicate root %q", id)
		}
		seen[id] = struct{}{}
	}
	return s.writeBacklogOrder(ids)
}

func errNotRootBacklog(id string) error {
	return &backlogOrderError{id: id}
}

type backlogOrderError struct{ id string }

func (e *backlogOrderError) Error() string {
	return "item " + e.id + " is not a backlog root"
}

// addBacklogRoot appends a root entering backlog. If this is the first use of
// the feature, existing roots are seeded from their old activity order and
// the newly parked root is appended after them.
func (s *Store) addBacklogRoot(id string) error {
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return err
	}
	ids, exists, err := s.readBacklogOrder()
	if err != nil {
		return err
	}
	if !exists {
		ids = initialBacklogOrderWithout(items, id)
	} else {
		ids = normalizeBacklogOrder(ids, items, true)
		ids = removeID(ids, id)
	}
	if !containsID(ids, id) {
		ids = append(ids, id)
	}
	return s.writeBacklogOrder(ids)
}

// removeBacklogRoot removes a root from the persisted order. A missing order
// file needs no work: no explicit position exists yet to preserve.
func (s *Store) removeBacklogRoot(id string) error {
	ids, exists, err := s.readBacklogOrder()
	if err != nil || !exists {
		return err
	}
	return s.writeBacklogOrder(removeID(ids, id))
}

func (s *Store) reconcileBacklogMembership(before, after models.Item) error {
	if before.Parent != "" || after.Parent != "" || before.ID != after.ID {
		return nil
	}
	wasBacklog := before.Status == models.StatusBacklog
	isBacklog := after.Status == models.StatusBacklog
	switch {
	case !wasBacklog && isBacklog:
		return s.addBacklogRoot(after.ID)
	case wasBacklog && !isBacklog:
		return s.removeBacklogRoot(after.ID)
	default:
		return nil
	}
}

func (s *Store) readBacklogOrder() ([]string, bool, error) {
	data, err := os.ReadFile(s.backlogOrderPath())
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(string(data), "\n")
	ids := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, true, nil
}

func (s *Store) backlogOrderPath() string {
	return filepath.Join(s.Root, backlogOrderFilename)
}

func (s *Store) writeBacklogOrder(ids []string) error {
	content := ""
	if len(ids) > 0 {
		content = strings.Join(ids, "\n") + "\n"
	}
	return writeFileAtomic(s.backlogOrderPath(), []byte(content))
}

func normalizeBacklogOrder(ids []string, items []models.Item, appendMissing bool) []string {
	roots := backlogRoots(items)
	ordered := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, id := range ids {
		if _, eligible := roots[id]; !eligible {
			continue
		}
		if _, already := seen[id]; already {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}
	if !appendMissing {
		return ordered
	}
	missing := make([]models.Item, 0, len(roots))
	for id, item := range roots {
		if _, ok := seen[id]; !ok {
			missing = append(missing, item)
		}
	}
	sort.SliceStable(missing, func(i, j int) bool {
		if !missing[i].Created.Equal(missing[j].Created) {
			return missing[i].Created.Before(missing[j].Created)
		}
		return missing[i].ID < missing[j].ID
	})
	for _, item := range missing {
		ordered = append(ordered, item.ID)
	}
	return ordered
}

func initialBacklogOrder(items []models.Item) []string {
	roots := backlogRoots(items)
	values := make([]models.Item, 0, len(roots))
	for _, item := range roots {
		values = append(values, item)
	}
	sort.SliceStable(values, func(i, j int) bool {
		ai := backlogFamilyActivity(values[i].ID, items)
		aj := backlogFamilyActivity(values[j].ID, items)
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return values[i].ID < values[j].ID
	})
	ids := make([]string, len(values))
	for i, item := range values {
		ids[i] = item.ID
	}
	return ids
}

func initialBacklogOrderWithout(items []models.Item, excluded string) []string {
	filtered := make([]models.Item, 0, len(items))
	for _, item := range items {
		if item.ID != excluded {
			filtered = append(filtered, item)
		}
	}
	return initialBacklogOrder(filtered)
}

func backlogRoots(items []models.Item) map[string]models.Item {
	roots := make(map[string]models.Item)
	for _, item := range items {
		if item.Parent == "" && item.Status == models.StatusBacklog {
			roots[item.ID] = item
		}
	}
	return roots
}

func backlogFamilyActivity(rootID string, items []models.Item) time.Time {
	latest := time.Time{}
	for _, item := range items {
		if item.ID != rootID && item.Parent != rootID {
			continue
		}
		activity := item.Created
		if len(item.Turns) > 0 {
			activity = item.Turns[len(item.Turns)-1].Timestamp
		}
		if activity.After(latest) {
			latest = activity
		}
	}
	return latest
}

func containsID(ids []string, wanted string) bool {
	return indexOf(ids, wanted) >= 0
}

func indexOf(ids []string, wanted string) int {
	for i, id := range ids {
		if id == wanted {
			return i
		}
	}
	return -1
}

func removeID(ids []string, unwanted string) []string {
	out := ids[:0]
	for _, id := range ids {
		if id != unwanted {
			out = append(out, id)
		}
	}
	return out
}
