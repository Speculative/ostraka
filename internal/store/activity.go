package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

const activityDir = "ACTIVITY"

// ActivityType values are intentionally strings: the activity journal is a
// small protocol boundary shared by the store, TUI, and supervisor.
const (
	ActivitySubthreadCreated               = "subthread.created"
	ActivitySubthreadTurn                  = "subthread.turn"
	ActivitySubthreadClosed                = "subthread.closed"
	ActivitySubthreadMoved                 = "subthread.moved"
	ActivitySubthreadUnparented            = "subthread.unparented"
	ActivitySubthreadDeleted               = "subthread.deleted"
	ActivityItemRenamed                    = "item.renamed"
	ActivityItemGroupChanged               = "item.group_changed"
	ActivityProjectBriefChanged            = "project.brief_changed"
	ActivityProjectInstructionsChanged     = "project.instructions_changed"
	ActivityAgentInterrupted               = "agent.interrupted"
	ActivityAgentEndedWithoutFinalResponse = "agent.ended_without_final_response"
	ActivityAgentSessionStarted            = "agent.session_started"
)

func (s *Store) activityPath(rootID string) string {
	return filepath.Join(s.Root, activityDir, rootID+".json")
}

func (s *Store) ListActivities(rootID string) ([]models.Activity, error) {
	b, err := os.ReadFile(s.activityPath(rootID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var activities []models.Activity
	if err := json.Unmarshal(b, &activities); err != nil {
		return nil, fmt.Errorf("parse activities for %s: %w", rootID, err)
	}
	return activities, nil
}

// PendingActivities returns unhandled user activity and child turns belonging
// to the current root goal. Reading does not consume notifications: failed or
// reply-less dispatches must leave them available for retry.
func (s *Store) PendingActivities(rootID string) ([]models.Activity, error) {
	all, err := s.ListActivities(rootID)
	if err != nil {
		return nil, err
	}
	var pending []models.Activity
	var owner models.Item
	for _, activity := range all {
		if activity.Type == ActivitySubthreadTurn {
			if owner.ID == "" {
				owner, err = s.GetItem(rootID)
				if err != nil {
					return nil, err
				}
			}
			if !activity.Handled && owner.Parent == "" && owner.Mode == models.AgentModeGoal && owner.Goal != nil && owner.Goal.StartedAt.Equal(activity.GoalStartedAt) {
				pending = append(pending, activity)
			}
			continue
		}
		if !activity.Handled && activity.Actor != models.ActorAgent {
			pending = append(pending, activity)
		}
	}
	return pending, nil
}

func (s *Store) markActivities(rootID string, ids map[string]bool) error {
	activities, err := s.ListActivities(rootID)
	if err != nil {
		return err
	}
	changed := false
	for i := range activities {
		if ids[activities[i].ID] && !activities[i].Handled {
			activities[i].Handled = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeJSONAtomic(s.activityPath(rootID), activities)
}

// MarkActivitiesHandled is called only after the root provider run has both
// succeeded and posted an agent turn. Keeping the operation explicit prevents
// a failed dispatch from silently losing a child decision.
func (s *Store) MarkActivitiesHandled(rootID string, ids []string) error {
	unlock, err := s.lockItems()
	if err != nil {
		return err
	}
	defer unlock()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return s.markActivities(rootID, set)
}

// AddActivity records a non-turn timeline event. It is used by the supervisor
// for agent lifecycle events, while subthread lifecycle events continue to use
// the more specific store methods below.
func (s *Store) AddActivity(itemID string, activity models.Activity) error {
	unlock, err := s.lockItems()
	if err != nil {
		return err
	}
	defer unlock()
	if activity.Timestamp.IsZero() {
		activity.Timestamp = time.Now().UTC()
	}
	return s.appendActivity(itemID, activity)
}

// AgentSessionLabel returns the provider and, when recorded, the model and
// effort selected for a fresh agent session. Older activity records have no
// model or effort fields, so they naturally retain their provider-only label.
func AgentSessionLabel(activity models.Activity) string {
	provider := strings.TrimSpace(activity.Result)
	if provider == "" {
		provider = "provider"
	}
	parts := []string{provider}
	if model := strings.TrimSpace(activity.Model); model != "" {
		parts = append(parts, model)
	}
	if effort := strings.TrimSpace(activity.Effort); effort != "" {
		parts = append(parts, effort)
	}
	return strings.Join(parts, " ")
}

func activityID(now time.Time) string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return now.Format("20060102-150405.000000000")
	}
	return now.Format("20060102-150405.000000000") + "-" + hex.EncodeToString(b)
}

func (s *Store) appendActivity(rootID string, activity models.Activity) error {
	if activity.Actor == models.ActorAgent && activity.Type != ActivitySubthreadTurn {
		// The agent already knows about changes made during its own dispatch.
		// Keep them in the timeline, but do not feed them back as root work.
		activity.Handled = true
	}
	if strings.TrimSpace(activity.ID) == "" {
		activity.ID = activityID(activity.Timestamp)
	}
	activities, err := s.ListActivities(rootID)
	if err != nil {
		return err
	}
	activities = append(activities, activity)
	return writeJSONAtomic(s.activityPath(rootID), activities)
}

func writeJSONAtomic(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'))
}

func (s *Store) addSubthreadActivity(root models.Item, child models.Item, kind, result string, actor models.Actor) error {
	return s.appendActivity(root.ID, models.Activity{
		ID:         activityID(time.Now().UTC()),
		Type:       kind,
		ChildID:    child.ID,
		ChildTitle: child.Title,
		Result:     result,
		Actor:      actor,
		Timestamp:  time.Now().UTC(),
	})
}

// recordGoalChildTurn runs under the item mutation lock. Only turns written
// during an active root goal create notifications; rearming cannot replay them.
func (s *Store) recordGoalChildTurn(child models.Item) error {
	if child.Parent == "" {
		return nil
	}
	owner, err := s.GetItem(child.Parent)
	if err != nil {
		return err
	}
	if owner.Parent != "" || owner.Mode != models.AgentModeGoal || owner.Goal == nil {
		return nil
	}
	turn := child.Turns[len(child.Turns)-1]
	return s.appendActivity(owner.ID, models.Activity{
		Type: ActivitySubthreadTurn, ChildID: child.ID, ChildTitle: child.Title,
		Actor: turn.Actor, Timestamp: turn.Timestamp, TurnContent: turn.Content,
		GoalStartedAt: owner.Goal.StartedAt,
	})
}
