package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

const partialTraceDir = "PARTIALS"

func (s *Store) partialTracePath(itemID string) string {
	return filepath.Join(s.Root, partialTraceDir, itemID+".json")
}

// ListPartialTraces returns every retained provider trace for an item in the
// order it was recorded. A missing journal is the normal case for older or
// never-dispatched items.
func (s *Store) ListPartialTraces(itemID string) ([]models.PartialTrace, error) {
	b, err := os.ReadFile(s.partialTracePath(itemID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var traces []models.PartialTrace
	if err := json.Unmarshal(b, &traces); err != nil {
		return nil, fmt.Errorf("parse partial traces for %s: %w", itemID, err)
	}
	return traces, nil
}

// AppendPartialTrace retains a complete snapshot of one provider run. Empty
// traces are ignored: an agent that produced no visible progress should not
// create an empty disclosure row.
func (s *Store) AppendPartialTrace(itemID string, trace models.PartialTrace) error {
	if strings.TrimSpace(trace.Content) == "" {
		return nil
	}
	if trace.ID == "" {
		trace.ID = activityID(time.Now().UTC())
	}
	if trace.Timestamp.IsZero() {
		trace.Timestamp = time.Now().UTC()
	}
	traces, err := s.ListPartialTraces(itemID)
	if err != nil {
		return err
	}
	traces = append(traces, trace)
	b, err := json.MarshalIndent(traces, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.partialTracePath(itemID), append(b, '\n'))
}

func (s *Store) deletePartialTraces(itemID string) error {
	err := os.Remove(s.partialTracePath(itemID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
