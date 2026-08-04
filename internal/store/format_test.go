package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ostraka/internal/models"
	"ostraka/internal/store"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.md")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

func TestParseRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	item := models.Item{
		ID:      "20240101-120000",
		Channel: models.ChannelAsks,
		Type:    models.TypeThread,
		Status:  models.StatusActive,
		Created: now,
		Body:    "Should we use fsnotify or polling?",
	}

	path := filepath.Join(t.TempDir(), "item.md")
	if err := store.WriteItem(item, path); err != nil {
		t.Fatal(err)
	}
	got, err := store.ParseItem(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != item.ID {
		t.Errorf("ID: got %q want %q", got.ID, item.ID)
	}
	if got.Channel != item.Channel {
		t.Errorf("Channel: got %q want %q", got.Channel, item.Channel)
	}
	if got.Status != item.Status {
		t.Errorf("Status: got %q want %q", got.Status, item.Status)
	}
	if got.Body != item.Body {
		t.Errorf("Body: got %q want %q", got.Body, item.Body)
	}
	if !got.Created.Equal(item.Created) {
		t.Errorf("Created: got %v want %v", got.Created, item.Created)
	}
}

func TestParseWithTurns(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	item := models.Item{
		ID:      "20240101-120000",
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  models.StatusPendingUser,
		Created: now,
		Body:    "Task body",
		Turns: []models.Turn{
			{Actor: models.ActorAgent, Timestamp: now, Content: "Agent response"},
			{Actor: models.ActorUser, Timestamp: now.Add(time.Minute), Content: "User reply"},
		},
	}

	path := filepath.Join(t.TempDir(), "item.md")
	if err := store.WriteItem(item, path); err != nil {
		t.Fatal(err)
	}
	got, err := store.ParseItem(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(got.Turns))
	}
	if got.Turns[0].Actor != models.ActorAgent {
		t.Errorf("turn[0] actor: got %q want %q", got.Turns[0].Actor, models.ActorAgent)
	}
	if got.Turns[0].Content != "Agent response" {
		t.Errorf("turn[0] content: got %q", got.Turns[0].Content)
	}
	if got.Turns[1].Actor != models.ActorUser {
		t.Errorf("turn[1] actor: got %q want %q", got.Turns[1].Actor, models.ActorUser)
	}
}

func TestParseWithParent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	item := models.Item{
		ID:      "20240101-120001",
		Channel: models.ChannelAsks,
		Type:    models.TypeThread,
		Status:  models.StatusActive,
		Created: now,
		Parent:  "20240101-120000",
		Body:    "Sub-question",
	}

	path := filepath.Join(t.TempDir(), "item.md")
	if err := store.WriteItem(item, path); err != nil {
		t.Fatal(err)
	}
	got, err := store.ParseItem(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.Parent != "20240101-120000" {
		t.Errorf("Parent: got %q want %q", got.Parent, "20240101-120000")
	}
}

func TestParseMultilineTurnContent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	item := models.Item{
		ID:      "20240101-120000",
		Channel: models.ChannelHandoff,
		Type:    models.TypeThread,
		Status:  models.StatusActive,
		Created: now,
		Body:    "Session open",
		Turns: []models.Turn{
			{Actor: models.ActorAgent, Timestamp: now, Content: "Line one\nLine two\nLine three"},
		},
	}

	path := filepath.Join(t.TempDir(), "item.md")
	if err := store.WriteItem(item, path); err != nil {
		t.Fatal(err)
	}
	got, err := store.ParseItem(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.Turns[0].Content != "Line one\nLine two\nLine three" {
		t.Errorf("multiline content: got %q", got.Turns[0].Content)
	}
}

func TestParseMissingFrontmatter(t *testing.T) {
	path := writeTemp(t, "no frontmatter here\n")
	_, err := store.ParseItem(path)
	if err == nil {
		t.Error("expected error for missing frontmatter")
	}
}
