package tui

import (
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestDispatchableWakesOnlyLiveStatuses(t *testing.T) {
	for _, tc := range []struct {
		status models.Status
		want   bool
	}{
		// Backlog is where an item is parked so the agent does not see it.
		{models.StatusBacklog, false},
		{models.StatusActive, true},
		{models.StatusPendingUser, true},
		{models.StatusPendingAgent, true},
		{models.StatusAgentAcknowledged, true},
		// Terminal: there is no one left to answer.
		{models.StatusArchived, false},
	} {
		if got := dispatchable(tc.status); got != tc.want {
			t.Errorf("dispatchable(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestStatusIndexRoundTripsEveryStatus(t *testing.T) {
	for i, s := range allStatuses {
		if got := statusIndex(s); got != i {
			t.Errorf("statusIndex(%q) = %d, want %d", s, got, i)
		}
	}
}

func TestStatusIndexFallsBackForUnknown(t *testing.T) {
	// An item written by a newer CLI must still open the selector rather than
	// panicking on a -1 index.
	if got := statusIndex(models.Status("not-a-status")); got != 0 {
		t.Errorf("statusIndex(unknown) = %d, want 0", got)
	}
}

func TestAllStatusesCoversEveryModelStatus(t *testing.T) {
	// The selector is the only way to set a status from the TUI, so a status
	// missing here is unreachable.
	known := []models.Status{
		models.StatusBacklog, models.StatusActive, models.StatusPendingUser,
		models.StatusPendingAgent, models.StatusAgentAcknowledged,
		models.StatusArchived,
	}
	if len(allStatuses) != len(known) {
		t.Fatalf("allStatuses has %d entries, models defines %d", len(allStatuses), len(known))
	}
	for _, s := range known {
		found := false
		for _, c := range allStatuses {
			if c == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("status %q is not reachable from the selector", s)
		}
	}
}

func TestTruncateLinesKeepsShortInputIntact(t *testing.T) {
	in := []string{"one", "two"}
	got := truncateLines(in, 2, 10)
	if len(got) != 2 || got[1] != "two" {
		t.Errorf("got %q, want it unchanged", got)
	}
}

func TestTruncateLinesMarksTheCut(t *testing.T) {
	got := truncateLines([]string{"one", "two", "three"}, 2, 10)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	if got[1] != "two…" {
		t.Errorf("got %q, want %q", got[1], "two…")
	}
}

func TestTruncateLinesEllipsisFitsWidth(t *testing.T) {
	// Fed the way renderList feeds it — wordWrap output at the same width —
	// the ellipsis must not push the truncated row past its column.
	body := "a long item body that keeps going well past any sensible title length"
	for _, w := range []int{1, 2, 3, 5, 8, 20} {
		for _, line := range truncateLines(wordWrap(body, w), previewMaxLines, w) {
			if len([]rune(line)) > w {
				t.Errorf("width %d: line %q exceeds it", w, line)
			}
		}
	}
}

func TestWakesAgentIsNarrowerThanDispatchable(t *testing.T) {
	for _, tc := range []struct {
		status models.Status
		want   bool
	}{
		{models.StatusActive, true},
		{models.StatusPendingAgent, true},
		// Choosing pending-user is a hand-off toward the user, not the agent,
		// however recently the user wrote.
		{models.StatusPendingUser, false},
		{models.StatusBacklog, false},
		{models.StatusArchived, false},
	} {
		if got := wakesAgent(tc.status); got != tc.want {
			t.Errorf("wakesAgent(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestAwaitingAgentReadsTheLastTurn(t *testing.T) {
	user := models.Turn{Actor: models.ActorUser}
	agent := models.Turn{Actor: models.ActorAgent}

	for _, tc := range []struct {
		name  string
		turns []models.Turn
		want  bool
	}{
		// No turns: the body is the user's opening statement, so a dispatch
		// has something to respond to.
		{"no turns", nil, true},
		{"user last", []models.Turn{agent, user}, true},
		{"agent last", []models.Turn{user, agent}, false},
		{"only agent", []models.Turn{agent}, false},
	} {
		if got := awaitingAgent(models.Item{Turns: tc.turns}); got != tc.want {
			t.Errorf("%s: awaitingAgent = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStatusDotMarksOnlyLiveStates(t *testing.T) {
	for _, tc := range []struct {
		status models.Status
		want   bool
	}{
		{models.StatusPendingUser, true},       // your turn
		{models.StatusAgentAcknowledged, true}, // agent is on it
		// Everything else is at rest: a dot on every row carries no signal.
		{models.StatusBacklog, false},
		{models.StatusActive, false},
		{models.StatusPendingAgent, false},
		{models.StatusArchived, false},
	} {
		if _, got := statusDot(tc.status); got != tc.want {
			t.Errorf("statusDot(%q) marked = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestStatusDotColoursDifferByOwner(t *testing.T) {
	// The two marked states mean opposite things; sharing a colour would make
	// "waiting on you" and "agent working" indistinguishable at a glance.
	yours, _ := statusDot(models.StatusPendingUser)
	theirs, _ := statusDot(models.StatusAgentAcknowledged)
	if yours == theirs {
		t.Errorf("both dots render as %q", yours)
	}
}
