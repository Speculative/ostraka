package store

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

// ReparentItem changes itemID's direct root owner. A root with children can
// only be moved with flattenChildren: the moved item and each of its direct
// children then become siblings beneath newRootID, preserving the one-level
// hierarchy invariant.
func (s *Store) ReparentItem(itemID, newRootID string, flattenChildren bool) (models.Item, error) {
	if strings.TrimSpace(itemID) == "" {
		return models.Item{}, fmt.Errorf("item ID must not be blank")
	}
	if strings.TrimSpace(newRootID) == "" {
		return models.Item{}, fmt.Errorf("new root ID must not be blank")
	}
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return models.Item{}, err
	}
	byID := make(map[string]models.Item, len(items))
	for _, candidate := range items {
		byID[candidate.ID] = candidate
	}
	item, ok := byID[itemID]
	if !ok {
		return models.Item{}, fmt.Errorf("item %q not found", itemID)
	}
	newRoot, ok := byID[newRootID]
	if !ok {
		return models.Item{}, fmt.Errorf("item %q not found", newRootID)
	}
	if item.ID == newRoot.ID {
		return models.Item{}, fmt.Errorf("an item cannot be reparented to itself")
	}
	if newRoot.Parent != "" {
		return models.Item{}, fmt.Errorf("new root %q is a subthread; reparenting requires a root item", newRoot.ID)
	}
	if models.TerminalStatuses[newRoot.Status] {
		return models.Item{}, fmt.Errorf("cannot reparent under terminal root %q", newRoot.ID)
	}
	if item.Channel != newRoot.Channel {
		return models.Item{}, fmt.Errorf("item %q and new root %q must use the same channel", item.ID, newRoot.ID)
	}
	if item.Parent == newRoot.ID {
		return models.Item{}, fmt.Errorf("item %q is already a subthread of %q", item.ID, newRoot.ID)
	}

	children := make([]models.Item, 0)
	for _, candidate := range items {
		if candidate.Parent == item.ID {
			children = append(children, candidate)
		}
	}
	if len(children) > 0 && !flattenChildren {
		return models.Item{}, fmt.Errorf("item %q has subthreads; use --flatten-children to reparent it", item.ID)
	}
	if len(children) == 0 && flattenChildren {
		return models.Item{}, fmt.Errorf("--flatten-children is only valid for item %q when it has subthreads", item.ID)
	}
	if flattenChildren {
		for _, child := range children {
			for _, candidate := range items {
				if candidate.Parent == child.ID {
					return models.Item{}, fmt.Errorf("item %q has nested subthread %q; reparenting cannot preserve the one-level hierarchy", item.ID, candidate.ID)
				}
			}
		}
	}

	type change struct {
		before  models.Item
		after   models.Item
		oldRoot string
		path    string
	}
	changes := make([]change, 0, len(children)+1)

	oldRootID := item.Parent
	if oldRootID == "" {
		oldRootID = item.ID
	}
	item.Parent = newRoot.ID
	changes = append(changes, change{
		before:  byID[item.ID],
		after:   item,
		oldRoot: oldRootID,
		path:    s.itemPath(item),
	})
	for i := range children {
		before := children[i]
		oldChildRootID := before.Parent
		if oldChildRootID == "" {
			oldChildRootID = before.ID
		}
		children[i].Parent = newRoot.ID
		changes = append(changes, change{
			before:  before,
			after:   children[i],
			oldRoot: oldChildRootID,
			path:    s.itemPath(children[i]),
		})
	}

	// All validation happens before the first write. Parent changes do not
	// change channel or status, so itemPath deliberately keeps every file in
	// its existing INBOX/ARCHIVE location; supervisor sessions are similarly
	// keyed by item ID and need no migration.
	type journal struct {
		before []models.Activity
		after  []models.Activity
		exists bool
	}
	journals := make(map[string]journal)
	loadJournal := func(rootID string) error {
		if _, loaded := journals[rootID]; loaded {
			return nil
		}
		activities, err := s.ListActivities(rootID)
		if err != nil {
			return err
		}
		_, statErr := os.Stat(s.activityPath(rootID))
		journals[rootID] = journal{
			before: append([]models.Activity(nil), activities...),
			after:  append([]models.Activity(nil), activities...),
			exists: statErr == nil,
		}
		return nil
	}
	for _, changed := range changes {
		if err := loadJournal(changed.oldRoot); err != nil {
			return models.Item{}, err
		}
	}
	if err := loadJournal(newRoot.ID); err != nil {
		return models.Item{}, err
	}
	for _, changed := range changes {
		activity := movedActivity(changed.after, changed.oldRoot, newRoot.ID)
		oldJournal := journals[changed.oldRoot]
		oldJournal.after = append(oldJournal.after, activity)
		journals[changed.oldRoot] = oldJournal
		newJournal := journals[newRoot.ID]
		newJournal.after = append(newJournal.after, movedActivity(changed.after, changed.oldRoot, newRoot.ID))
		journals[newRoot.ID] = newJournal
	}

	written := 0
	for _, changed := range changes {
		if err := WriteItem(changed.after, changed.path); err != nil {
			for i := written; i >= 0; i-- {
				_ = WriteItem(changes[i].before, changes[i].path)
			}
			return models.Item{}, fmt.Errorf("write reparented item %q: %w", changed.after.ID, err)
		}
		written++
	}

	for rootID, state := range journals {
		if err := writeJSONAtomic(s.activityPath(rootID), state.after); err != nil {
			for _, changed := range changes {
				_ = WriteItem(changed.before, changed.path)
			}
			for restoreID, restore := range journals {
				if restore.exists {
					_ = writeJSONAtomic(s.activityPath(restoreID), restore.before)
				} else {
					_ = os.Remove(s.activityPath(restoreID))
				}
			}
			return models.Item{}, fmt.Errorf("record reparenting activity: %w", err)
		}
	}
	return item, nil
}

func movedActivity(child models.Item, fromRootID, toRootID string) models.Activity {
	now := time.Now().UTC()
	return models.Activity{
		ID:         activityID(now),
		Type:       ActivitySubthreadMoved,
		ChildID:    child.ID,
		ChildTitle: child.Title,
		FromRootID: fromRootID,
		ToRootID:   toRootID,
		Result:     "moved",
		Actor:      models.ActorUser,
		Timestamp:  now,
	}
}

// MoveItem is an API alias for callers that describe the operation as a move.
// ReparentItem remains the precise name because the item keeps its identity.
func (s *Store) MoveItem(itemID, newRootID string, flattenChildren bool) (models.Item, error) {
	return s.ReparentItem(itemID, newRootID, flattenChildren)
}
