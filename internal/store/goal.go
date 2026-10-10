package store

import (
	"fmt"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

// GoalItem returns the sole goal-mode item, including an armed goal. Callers
// must fail closed if this invariant was broken by a manual file edit.
func (s *Store) GoalItem() (models.Item, bool, error) {
	items, err := s.ListItems(ListOpts{})
	if err != nil {
		return models.Item{}, false, err
	}
	var goal models.Item
	found := false
	for _, item := range items {
		if item.Mode != models.AgentModeGoal {
			continue
		}
		if found {
			return models.Item{}, false, fmt.Errorf("multiple goal-mode items: %s and %s", goal.ID, item.ID)
		}
		goal, found = item, true
	}
	return goal, found, nil
}

// EndGoal requires the initiating item ID. The CLI also checks the
// dispatch-scoped caller marker so a child cannot stop its parent's loop.
func (s *Store) EndGoal(itemID string) (models.Item, error) {
	unlock, err := s.lockItems()
	if err != nil {
		return models.Item{}, err
	}
	defer unlock()
	goal, found, err := s.GoalItem()
	if err != nil {
		return models.Item{}, err
	}
	if !found || goal.ID != itemID {
		return models.Item{}, fmt.Errorf("item %s does not own goal mode", itemID)
	}
	return s.setMode(itemID, models.AgentModeCode)
}

// GoalAllows defines dispatch membership. A root owns its direct children;
// a child goal is isolated even when siblings are subsequently created.
func GoalAllows(goal, item models.Item) bool {
	return goal.ID == item.ID || (goal.Parent == "" && item.Parent == goal.ID)
}

// PrepareGoalDispatch queues an automatic continuation only if this exact
// goal generation still owns the item. Cancellation/rearming can race the
// scheduler's snapshot, so the check and status write share the item lock.
func (s *Store) PrepareGoalDispatch(itemID string, startedAt time.Time) (bool, error) {
	unlock, err := s.lockItems()
	if err != nil {
		return false, err
	}
	defer unlock()
	item, err := s.GetItem(itemID)
	if err != nil {
		return false, err
	}
	if item.Mode != models.AgentModeGoal || item.Goal == nil || !item.Goal.StartedAt.Equal(startedAt) || item.Status == models.StatusAgentAcknowledged {
		return false, nil
	}
	if models.TerminalStatuses[item.Status] || item.Status == models.StatusBacklog || item.Status == models.StatusProposed {
		return false, nil
	}
	item.Status = models.StatusPendingAgent
	return true, WriteItem(item, s.itemPath(item))
}

// AcknowledgeDispatch rechecks both the normal status admission and the goal
// boundary under the same lock used by mode changes and goal cancellation.
func (s *Store) AcknowledgeDispatch(itemID string, expectedGoal time.Time) (bool, error) {
	unlock, err := s.lockItems()
	if err != nil {
		return false, err
	}
	defer unlock()
	item, err := s.GetItem(itemID)
	if err != nil {
		return false, err
	}
	if item.Status != models.StatusPendingAgent || (item.Mode == models.AgentModeGoal && item.Goal == nil) {
		return false, nil
	}
	if !expectedGoal.IsZero() && (item.Mode != models.AgentModeGoal || item.Goal == nil || !item.Goal.StartedAt.Equal(expectedGoal)) {
		return false, nil
	}
	goal, found, err := s.GoalItem()
	if err != nil {
		return false, err
	}
	if found && goal.Goal != nil && !GoalAllows(goal, item) {
		return false, nil
	}
	item.Status = models.StatusAgentAcknowledged
	return true, WriteItem(item, s.itemPath(item))
}
