package supervisor

import (
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

// previousDispatchMissedReply uses durable run records so the reminder survives
// supervisor restarts and provider session rotation, including silent runs.
func (s *Supervisor) previousDispatchMissedReply(item models.Item) bool {
	traces, err := s.store.ListPartialTraces(item.ID)
	if err != nil {
		s.logger.Printf("item %s: cannot load traces for reply reminder: %v", item.ID, err)
		return false
	}
	activities, err := s.store.ListActivities(item.ID)
	if err != nil {
		s.logger.Printf("item %s: cannot load activities for reply reminder: %v", item.ID, err)
		return false
	}
	var latest time.Time
	missing := false
	for _, trace := range traces {
		if trace.Timestamp.After(latest) {
			latest = trace.Timestamp
			missing = trace.Status == "completed" && trace.TurnTimestamp.IsZero()
		}
	}
	for _, activity := range activities {
		if activity.Type != store.ActivityAgentEndedWithoutFinalResponse && activity.Type != store.ActivityAgentInterrupted {
			continue
		}
		if activity.Timestamp.After(latest) {
			latest = activity.Timestamp
			missing = activity.Type == store.ActivityAgentEndedWithoutFinalResponse && activity.Result == "completed"
		}
	}
	for _, turn := range item.Turns {
		if turn.Actor == models.ActorAgent && !turn.Timestamp.Before(latest) {
			return false
		}
	}
	return missing
}
