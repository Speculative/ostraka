package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Speculative/ostraka/internal/models"
)

const childOrderFilename = "CHILD_ORDERS.json"

// ChildOrdersFor returns a complete child ordering for every parent in the
// supplied snapshot. Families without a saved order retain creation order.
func (s *Store) ChildOrdersFor(items []models.Item) (map[string][]string, error) {
	saved, _, err := s.readChildOrders()
	if err != nil {
		return nil, err
	}
	return normalizedChildOrders(saved, items), nil
}

// SetChildOrder persists a complete sibling order. expected is the order the
// caller previewed; comparing it with the current order prevents a stale TUI
// from overwriting another ordering change or dropping a new sibling.
func (s *Store) SetChildOrder(parentID string, expected, ids []string) error {
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return err
	}
	var parent models.Item
	found := false
	children := make(map[string]struct{})
	for _, item := range items {
		if item.ID == parentID && item.Parent == "" {
			parent, found = item, true
		}
		if item.Parent == parentID {
			children[item.ID] = struct{}{}
		}
	}
	if !found || models.TerminalStatuses[parent.Status] {
		return fmt.Errorf("item %q is not an open parent", parentID)
	}
	if len(children) < 2 {
		return fmt.Errorf("item %q has fewer than two reorderable subthreads", parentID)
	}
	saved, _, err := s.readChildOrders()
	if err != nil {
		return err
	}
	current := normalizeChildOrder(saved[parentID], parentID, items)
	if !equalStringIDs(current, expected) {
		return fmt.Errorf("subthread order for %q changed; refresh and try again", parentID)
	}
	if err := validateChildOrder(parentID, ids, children); err != nil {
		return err
	}
	saved[parentID] = append([]string(nil), ids...)
	return s.writeChildOrders(saved)
}

func validateChildOrder(parentID string, ids []string, children map[string]struct{}) error {
	if len(ids) != len(children) {
		return fmt.Errorf("subthread order for %q has %d items, want %d", parentID, len(ids), len(children))
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := children[id]; !ok {
			return fmt.Errorf("item %q is not a subthread of %q", id, parentID)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("subthread order for %q contains duplicate item %q", parentID, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *Store) appendChildOrderIfPresent(parentID string, ids ...string) error {
	saved, exists, err := s.readChildOrders()
	if err != nil || !exists || len(saved[parentID]) == 0 {
		return err
	}
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return err
	}
	order := normalizeChildOrder(saved[parentID], parentID, items)
	for _, id := range ids {
		order = removeID(order, id)
		order = append(order, id)
	}
	saved[parentID] = order
	return s.writeChildOrders(saved)
}

func (s *Store) readChildOrders() (map[string][]string, bool, error) {
	data, err := os.ReadFile(s.childOrderPath())
	if os.IsNotExist(err) {
		return map[string][]string{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var orders map[string][]string
	if err := json.Unmarshal(data, &orders); err != nil {
		return nil, true, fmt.Errorf("parse child orders: %w", err)
	}
	if orders == nil {
		orders = map[string][]string{}
	}
	return orders, true, nil
}

func (s *Store) childOrderPath() string {
	return filepath.Join(s.Root, childOrderFilename)
}

func (s *Store) writeChildOrders(orders map[string][]string) error {
	if len(orders) == 0 {
		if err := os.Remove(s.childOrderPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeJSONAtomic(s.childOrderPath(), orders)
}

func normalizedChildOrders(saved map[string][]string, items []models.Item) map[string][]string {
	orders := make(map[string][]string)
	parents := make(map[string]struct{})
	for _, item := range items {
		if item.Parent != "" {
			parents[item.Parent] = struct{}{}
		}
	}
	for parentID := range parents {
		orders[parentID] = normalizeChildOrder(saved[parentID], parentID, items)
	}
	return orders
}

func normalizeChildOrder(saved []string, parentID string, items []models.Item) []string {
	children := make(map[string]models.Item)
	for _, item := range items {
		if item.Parent == parentID {
			children[item.ID] = item
		}
	}
	order := make([]string, 0, len(children))
	seen := make(map[string]struct{}, len(children))
	for _, id := range saved {
		if _, ok := children[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		order = append(order, id)
	}
	missing := make([]models.Item, 0, len(children)-len(order))
	for id, item := range children {
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
		order = append(order, item.ID)
	}
	return order
}

func equalStringIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type childOrderSnapshot struct {
	data   []byte
	exists bool
}

func (s *Store) prepareChildOrdersForReparent(items []models.Item, itemID, sourceParent, targetParent string, incomingIDs []string) (childOrderSnapshot, map[string][]string, error) {
	saved, exists, err := s.readChildOrders()
	if err != nil {
		return childOrderSnapshot{}, nil, err
	}
	var data []byte
	if exists {
		data, err = os.ReadFile(s.childOrderPath())
		if err != nil {
			return childOrderSnapshot{}, nil, err
		}
	}
	snapshot := childOrderSnapshot{data: data, exists: exists}
	orders := normalizedChildOrders(saved, items)
	if len(saved[targetParent]) > 0 || len(orders[targetParent]) > 0 {
		order := append([]string(nil), orders[targetParent]...)
		for _, id := range incomingIDs {
			order = removeID(order, id)
			order = append(order, id)
		}
		saved[targetParent] = order
	} else if len(incomingIDs) > 0 {
		// Materialize the destination order so an older item moved here still
		// follows the siblings already present.
		order := append([]string(nil), orders[targetParent]...)
		order = append(order, incomingIDs...)
		saved[targetParent] = order
	}
	if sourceParent != "" && sourceParent != targetParent && len(saved[sourceParent]) > 0 {
		order := append([]string(nil), orders[sourceParent]...)
		order = removeID(order, itemID)
		if len(order) == 0 {
			delete(saved, sourceParent)
		} else {
			saved[sourceParent] = order
		}
	}
	// The moved item no longer owns children after flattening, whether it was
	// a root or an existing child.
	delete(saved, itemID)
	return snapshot, saved, nil
}

func (s *Store) restoreChildOrders(snapshot childOrderSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(s.childOrderPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(s.childOrderPath(), snapshot.data)
}
