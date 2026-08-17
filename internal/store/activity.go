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
	ActivitySubthreadCreated = "subthread.created"
	ActivitySubthreadClosed  = "subthread.closed"
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

// PendingActivities returns the events which have not yet been included in a
// successful root dispatch. It deliberately does not mark them while reading:
// a provider run may fail or may finish without posting its final item turn.
func (s *Store) PendingActivities(rootID string) ([]models.Activity, error) {
	all, err := s.ListActivities(rootID)
	if err != nil {
		return nil, err
	}
	var pending []models.Activity
	for _, activity := range all {
		if !activity.Handled {
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
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return s.markActivities(rootID, set)
}

func activityID(now time.Time) string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return now.Format("20060102-150405.000000000")
	}
	return now.Format("20060102-150405.000000000") + "-" + hex.EncodeToString(b)
}

func (s *Store) appendActivity(rootID string, activity models.Activity) error {
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

func (s *Store) addSubthreadActivity(root models.Item, child models.Item, kind, result string) error {
	return s.appendActivity(root.ID, models.Activity{
		ID:         activityID(time.Now().UTC()),
		Type:       kind,
		ChildID:    child.ID,
		ChildTitle: child.Title,
		Result:     result,
		Actor:      models.ActorUser,
		Timestamp:  time.Now().UTC(),
	})
}
