package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ostraka/internal/models"
	"ostraka/internal/store"
	"ostraka/internal/supervisor"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/hinshun/vt10x"
)

const (
	integrationTermWidth  = 120
	integrationTermHeight = 12
)

// fakeSupervisor keeps the integration suite at the TUI boundary: the real
// Bubble Tea program runs, but a test never starts claude or codex.
type fakeSupervisor struct {
	enqueued []string
}

func (s *fakeSupervisor) Enqueue(itemID string) {
	s.enqueued = append(s.enqueued, itemID)
}

func (s *fakeSupervisor) Session(string) (supervisor.Provider, string, string, string, time.Time) {
	return supervisor.ProviderClaude, "", "", "", time.Time{}
}

func (s *fakeSupervisor) SessionIsStale(string) bool { return false }

func (s *fakeSupervisor) LastTurnInfo(string) (supervisor.TurnInfo, bool) {
	return supervisor.TurnInfo{}, false
}

func (s *fakeSupervisor) PreferredModel(supervisor.Provider) string { return "" }

func (s *fakeSupervisor) PreferredEffort(supervisor.Provider) string { return "" }

func (s *fakeSupervisor) StartNewSession(string, supervisor.Provider, string, string) error {
	return nil
}

func (s *fakeSupervisor) AvailableModels(context.Context, supervisor.Provider) ([]supervisor.ModelOption, error) {
	return []supervisor.ModelOption{{DisplayName: "Default", Default: true}}, nil
}

func (s *fakeSupervisor) Busy() (string, bool) { return "", false }

func writeIntegrationItem(t *testing.T, s *store.Store, id, title string, status models.Status) {
	t.Helper()
	item := models.Item{
		ID:      id,
		Channel: models.ChannelInbox,
		Type:    models.TypeThread,
		Status:  status,
		Created: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
		Title:   title,
		Body:    "fixture body",
	}
	path := filepath.Join(s.Root, "INBOX", id+".md")
	if err := store.WriteItem(item, path); err != nil {
		t.Fatal(err)
	}
}

type capturedOutput struct {
	all []byte
}

func (c *capturedOutput) waitFor(t *testing.T, tm *teatest.TestModel, condition func([]byte) bool) {
	t.Helper()
	var current []byte
	teatest.WaitFor(t, tm.Output(), func(output []byte) bool {
		current = append(current[:0], output...)
		return condition(output)
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	c.all = append(c.all, current...)
}

func (c *capturedOutput) finish(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	c.all = append(c.all, rest...)
}

func newIntegrationProgram(t *testing.T, setup func(*store.Store)) (*teatest.TestModel, *fakeSupervisor, *capturedOutput) {
	t.Helper()
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setup(s)

	sup := &fakeSupervisor{}
	tm := teatest.NewTestModel(
		t,
		newModel(s, nil, sup),
		teatest.WithInitialTermSize(integrationTermWidth, integrationTermHeight),
	)
	output := &capturedOutput{}
	t.Cleanup(func() {
		// A test that fails before finish still has to release the Bubble Tea
		// goroutine. Quit is idempotent from the test's point of view.
		_ = tm.Quit()
	})
	return tm, sup, output
}

func terminalGrid(t *testing.T, output []byte) string {
	t.Helper()
	terminal := vt10x.New(
		vt10x.WithSize(integrationTermWidth, integrationTermHeight),
		vt10x.WithWriter(io.Discard),
	)
	if _, err := terminal.Write(output); err != nil {
		t.Fatal(err)
	}

	lines := make([]string, integrationTermHeight)
	for y := range lines {
		var line strings.Builder
		for x := 0; x < integrationTermWidth; x++ {
			cell := terminal.Cell(x, y)
			if cell.Char == 0 {
				line.WriteByte(' ')
				continue
			}
			line.WriteRune(cell.Char)
		}
		lines[y] = strings.TrimRight(line.String(), " ")
	}
	return strings.Join(lines, "\n")
}

func TestProgramStartupShowsBacklogWhenTheInboxFits(t *testing.T) {
	tm, sup, output := newIntegrationProgram(t, func(s *store.Store) {
		writeIntegrationItem(t, s, "20260816-120001", "Visible active item", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120002", "Visible backlog item", models.StatusBacklog)
	})

	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("Visible active item")) &&
			bytes.Contains(got, []byte("Visible backlog item"))
	})
	output.finish(t, tm)

	grid := terminalGrid(t, output.all)
	if !strings.Contains(grid, "Visible active item") || !strings.Contains(grid, "Visible backlog item") {
		t.Fatalf("startup grid does not show both fixtures:\n%s", grid)
	}
	if len(sup.enqueued) != 0 {
		t.Fatalf("startup enqueued turns: %v", sup.enqueued)
	}
}

func TestProgramBacklogToggleRevealsHiddenItems(t *testing.T) {
	tm, sup, output := newIntegrationProgram(t, func(s *store.Store) {
		writeIntegrationItem(t, s, "20260816-120001", "Live item one", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120002", "Live item two", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120003", "Live item three", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120004", "Parked backlog item", models.StatusBacklog)
	})

	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("1 backlog item (b)")) &&
			bytes.Contains(got, []byte("Live item one"))
	})
	initialGrid := terminalGrid(t, output.all)
	if strings.Contains(initialGrid, "Parked backlog item") {
		t.Fatalf("backlog was visible before b:\n%s", initialGrid)
	}
	if !strings.Contains(initialGrid, "1 backlog item (b)") {
		t.Fatalf("hidden-backlog marker missing before b:\n%s", initialGrid)
	}

	tm.Type("b")
	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("Parked backlog item"))
	})
	output.finish(t, tm)

	finalGrid := terminalGrid(t, output.all)
	if !strings.Contains(finalGrid, "Parked backlog item") {
		t.Fatalf("backlog did not appear after b:\n%s", finalGrid)
	}
	if strings.Contains(finalGrid, "1 backlog item (b)") {
		t.Fatalf("hidden-backlog marker remained after b:\n%s", finalGrid)
	}
	if len(sup.enqueued) != 0 {
		t.Fatalf("backlog toggle enqueued turns: %v", sup.enqueued)
	}
}

// Set OSTRAKA_VISUAL_SMOKE=1 to run this path. It drives the same deterministic
// scenario as the assertion tests, parses captured ANSI output through a
// virtual terminal, and logs the final character-cell grid for direct agent
// inspection without requiring a golden or a visual assertion.
func TestProgramVisualSmoke(t *testing.T) {
	if os.Getenv("OSTRAKA_VISUAL_SMOKE") != "1" {
		t.Skip("set OSTRAKA_VISUAL_SMOKE=1 to inspect the final terminal grid")
	}

	tm, _, output := newIntegrationProgram(t, func(s *store.Store) {
		writeIntegrationItem(t, s, "20260816-120001", "Smoke live item one", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120002", "Smoke live item two", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120003", "Smoke live item three", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120004", "Smoke backlog item", models.StatusBacklog)
	})
	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("1 backlog item (b)"))
	})
	tm.Type("b")
	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("Smoke backlog item"))
	})
	output.finish(t, tm)

	t.Logf("final %dx%d terminal grid:\n%s", integrationTermWidth, integrationTermHeight, terminalGrid(t, output.all))
}

var _ tea.Model = model{}
