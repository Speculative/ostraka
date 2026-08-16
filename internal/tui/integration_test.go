package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
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
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/hinshun/vt10x"
	"github.com/muesli/termenv"
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

// settle drains redraws produced by the last injected message. The TUI's
// event loop is asynchronous, so an explorer command needs to wait for the
// renderer to go quiet before printing its screen.
func (c *capturedOutput) settle(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	quiet := 0
	for time.Now().Before(deadline) {
		b, err := io.ReadAll(tm.Output())
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > 0 {
			c.all = append(c.all, b...)
			quiet = 0
		} else {
			quiet++
			if quiet >= 3 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
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

func parseTerminal(t *testing.T, output []byte) vt10x.Terminal {
	t.Helper()
	terminal := vt10x.New(
		vt10x.WithSize(integrationTermWidth, integrationTermHeight),
		vt10x.WithWriter(io.Discard),
	)
	if _, err := terminal.Write(output); err != nil {
		t.Fatal(err)
	}
	return terminal
}

func terminalGridFrom(terminal vt10x.Terminal) string {
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

func terminalGrid(t *testing.T, output []byte) string {
	t.Helper()
	return terminalGridFrom(parseTerminal(t, output))
}

// terminalInspection includes the state that terminalGrid deliberately omits:
// the cursor and the styled runs that reveal selected rows, bold text, and
// colored panels. This is printed by the manual explorer, not used as a
// brittle assertion.
func terminalInspection(t *testing.T, output []byte) string {
	t.Helper()
	terminal := parseTerminal(t, output)
	cursor := terminal.Cursor()
	var out strings.Builder
	fmt.Fprintf(&out, "cursor=(%d,%d) visible=%t\n", cursor.X, cursor.Y, terminal.CursorVisible())
	out.WriteString(terminalGridFrom(terminal))
	out.WriteString("\nstyled runs (non-default background or mode):\n")
	styled := false
	for y := 0; y < integrationTermHeight; y++ {
		for x := 0; x < integrationTermWidth; {
			cell := terminal.Cell(x, y)
			if cell.BG == vt10x.DefaultBG && cell.Mode == 0 {
				x++
				continue
			}
			styled = true
			start := x
			fg, bg, mode := cell.FG, cell.BG, cell.Mode
			var text strings.Builder
			for x < integrationTermWidth {
				cell = terminal.Cell(x, y)
				if cell.FG != fg || cell.BG != bg || cell.Mode != mode {
					break
				}
				if cell.Char == 0 {
					text.WriteByte(' ')
				} else {
					text.WriteRune(cell.Char)
				}
				x++
			}
			fmt.Fprintf(&out, "  row %d cols %d-%d fg=%d bg=%d mode=%d text=%q\n",
				y, start, x-1, fg, bg, mode, text.String())
		}
	}
	if !styled {
		out.WriteString("  (none)\n")
	}
	return out.String()
}

func explorerKey(name string) (tea.KeyMsg, bool) {
	keyTypes := map[string]tea.KeyType{
		"enter":  tea.KeyEnter,
		"esc":    tea.KeyEsc,
		"up":     tea.KeyUp,
		"down":   tea.KeyDown,
		"left":   tea.KeyLeft,
		"right":  tea.KeyRight,
		"pgup":   tea.KeyPgUp,
		"pgdn":   tea.KeyPgDown,
		"ctrl+c": tea.KeyCtrlC,
		"ctrl+s": tea.KeyCtrlS,
	}
	if keyType, ok := keyTypes[name]; ok {
		return tea.KeyMsg{Type: keyType}, true
	}
	runes := []rune(name)
	if len(runes) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}, true
	}
	return tea.KeyMsg{}, false
}

func runVisualExplorer(t *testing.T, tm *teatest.TestModel, output *capturedOutput) {
	t.Helper()
	scanner := bufio.NewScanner(os.Stdin)
	t.Log("visual explorer ready; commands: key <name>, type <text>, screen, wait, quit")
	t.Logf("initial screen:\n%s", terminalInspection(t, output.all))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		switch parts[0] {
		case "key":
			if len(parts) != 2 {
				t.Log("usage: key <b|enter|up|down|esc|ctrl+s|...>")
				continue
			}
			key, ok := explorerKey(strings.TrimSpace(parts[1]))
			if !ok {
				t.Logf("unknown key %q", parts[1])
				continue
			}
			tm.Send(key)
			output.settle(t, tm)
			t.Logf("after %s:\n%s", line, terminalInspection(t, output.all))
		case "type":
			if len(parts) != 2 {
				t.Log("usage: type <text>")
				continue
			}
			for _, r := range parts[1] {
				tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			output.settle(t, tm)
			t.Logf("after %s:\n%s", line, terminalInspection(t, output.all))
		case "screen", "wait":
			output.settle(t, tm)
			t.Logf("screen:\n%s", terminalInspection(t, output.all))
		case "quit", "exit":
			return
		default:
			t.Logf("unknown command %q", parts[0])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
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

func TestProgramStartupShowsSavedDraftInCollapsedComposer(t *testing.T) {
	tm, _, output := newIntegrationProgram(t, func(s *store.Store) {
		writeIntegrationItem(t, s, "20260816-120001", "Draft target", models.StatusActive)
		if err := s.SaveDraft("20260816-120001", "saved draft body"); err != nil {
			t.Fatal(err)
		}
	})

	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("saved draft body"))
	})
	output.finish(t, tm)

	grid := terminalGrid(t, output.all)
	if !strings.Contains(grid, "saved draft body") {
		t.Fatalf("startup grid does not show the saved draft:\n%s", grid)
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

// Set OSTRAKA_VISUAL_EXPLORE=1 to drive this path from stdin without editing
// the test for every scenario. For example:
//
//	go test ./internal/tui -c -o /tmp/ostraka-tui.test
//	OSTRAKA_VISUAL_EXPLORE=1 /tmp/ostraka-tui.test -test.run TestProgramVisualExplore -test.count=1 -test.v
//
// Then enter commands such as "key b", "key down", "key t", "type hello",
// "key ctrl+s", "screen", and "quit". This remains an in-memory Bubble Tea
// program with captured output; it does not open a PTY.
func TestProgramVisualExplore(t *testing.T) {
	if os.Getenv("OSTRAKA_VISUAL_EXPLORE") != "1" {
		t.Skip("set OSTRAKA_VISUAL_EXPLORE=1 to drive the TUI manually")
	}
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	tm, _, output := newIntegrationProgram(t, func(s *store.Store) {
		writeIntegrationItem(t, s, "20260816-120001", "Explore live item one", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120002", "Explore live item two", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120003", "Explore live item three", models.StatusActive)
		writeIntegrationItem(t, s, "20260816-120004", "Explore backlog item", models.StatusBacklog)
	})
	output.waitFor(t, tm, func(got []byte) bool {
		return bytes.Contains(got, []byte("1 backlog item (b)"))
	})
	output.settle(t, tm)
	runVisualExplorer(t, tm, output)
	output.finish(t, tm)
}

var _ tea.Model = model{}
