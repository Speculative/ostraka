package store_test

import (
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

func TestPartialTracesRoundTripAndIgnoreEmptyContent(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{ID: "trace-1", Timestamp: started, Status: "interrupted", Content: "partial output"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{Content: "  "}); err != nil {
		t.Fatal(err)
	}
	traces, err := s.ListPartialTraces(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 1 || traces[0].ID != "trace-1" || traces[0].Content != "partial output" {
		t.Fatalf("traces = %+v, want one retained trace", traces)
	}
}

func TestDeleteItemRemovesPartialTraceJournal(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{Content: "partial"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem(item.ID); err != nil {
		t.Fatal(err)
	}
	traces, err := s.ListPartialTraces(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 0 {
		t.Fatalf("traces after delete = %+v", traces)
	}
}

func TestAddActivityPersistsInterruptions(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(item.ID, models.Activity{Type: "agent.interrupted", Actor: models.ActorAgent}); err != nil {
		t.Fatal(err)
	}
	activities, err := s.ListActivities(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Type != "agent.interrupted" || activities[0].Timestamp.IsZero() {
		t.Fatalf("activities = %+v", activities)
	}
}
