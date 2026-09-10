package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestPartialTraceIsCollapsedAndCanBeSelectedAndShown(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	traceAt := time.Now().UTC()
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{
		ID:        "trace-1",
		Timestamp: traceAt,
		Status:    "interrupted",
		Content:   "secret partial provider output",
	}); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.selected = 0
	m.conv.Width = 80
	m.conv.Height = 20
	m.updateConv()
	collapsed := ansi.Strip(m.conv.View())
	header := "agent  ·  " + traceAt.Format("2006-01-02 15:04") + "  ·  interrupted"
	if !containsTurnWithBody(collapsed, header, agentEndedWithoutResponse) ||
		strings.Contains(collapsed, "partial response") || strings.Contains(collapsed, "collapsed") ||
		strings.Contains(collapsed, "expanded") || strings.Contains(collapsed, "secret partial provider output") {
		t.Fatalf("collapsed trace view = %q", collapsed)
	}

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.focus != focusReadingPane {
		t.Fatalf("focus = %v, want reading pane", m.focus)
	}
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	expanded := ansi.Strip(m.conv.View())
	if !strings.Contains(expanded, agentEndedWithoutResponse) ||
		!strings.Contains(expanded, "secret partial provider output") ||
		strings.Contains(expanded, "partial response") || strings.Contains(expanded, "collapsed") ||
		strings.Contains(expanded, "expanded") {
		t.Fatalf("expanded trace view = %q", expanded)
	}

	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	collapsed = ansi.Strip(m.conv.View())
	if strings.Contains(collapsed, "secret partial provider output") || strings.Contains(collapsed, "partial response") ||
		strings.Contains(collapsed, "collapsed") || strings.Contains(collapsed, "expanded") {
		t.Fatalf("re-collapsed trace view = %q", collapsed)
	}
}

func TestInterruptedActivityRendersAsAnActivityLine(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	interruptedAt := time.Now().UTC()
	if err := s.AddActivity(item.ID, models.Activity{
		Type:      store.ActivityAgentInterrupted,
		Actor:     models.ActorAgent,
		Timestamp: interruptedAt,
	}); err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.selected = 0
	m.conv.Width = 80
	m.conv.Height = 20
	m.updateConv()
	view := ansi.Strip(m.conv.View())
	header := "agent  ·  " + interruptedAt.Format("2006-01-02 15:04") + "  ·  interrupted"
	if !containsTurnWithBody(view, header, agentEndedWithoutResponse) {
		t.Fatalf("conversation omitted interruption: %q", ansi.Strip(m.conv.View()))
	}
}

func containsTurnWithBody(view, header, body string) bool {
	lines := strings.Split(view, "\n")
	for i := 0; i+2 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == header && strings.TrimSpace(lines[i+1]) == "" && strings.TrimSpace(lines[i+2]) == body {
			return true
		}
	}
	return false
}

func TestNoFinalResponseActivityRendersTheEndedMarker(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(item.ID, models.Activity{
		Type:      store.ActivityAgentEndedWithoutFinalResponse,
		Actor:     models.ActorAgent,
		Result:    "failed",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.selected = 0
	m.conv.Width = 80
	m.conv.Height = 20
	m.updateConv()
	if view := ansi.Strip(m.conv.View()); !strings.Contains(view, "agent  ·  ") || !strings.Contains(view, agentEndedWithoutResponse) {
		t.Fatalf("conversation omitted no-final-response marker: %q", view)
	}
}

func TestSpaceExpandsTraceAttachedToSelectedTurn(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "trace", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	turnAt := time.Now().UTC()
	item.Turns = []models.Turn{{Actor: models.ActorAgent, Timestamp: turnAt, Content: "final answer"}}
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{
		ID:            "trace-attached",
		Timestamp:     turnAt.Add(-time.Second),
		TurnTimestamp: turnAt,
		Status:        "completed",
		Content:       "attached provider trace",
	}); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.items = []models.Item{item}
	m.selected = 0
	m.conv.Width = 80
	m.conv.Height = 20
	m.updateConv()
	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	next, _ = m.handleNavKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(model)
	if !strings.Contains(ansi.Strip(m.conv.View()), "attached provider trace") {
		t.Fatalf("attached trace did not expand: %q", ansi.Strip(m.conv.View()))
	}
	view := ansi.Strip(m.conv.View())
	if strings.Contains(view, agentEndedWithoutResponse) {
		t.Fatalf("normal completed turn was labeled as an ended response: %q", view)
	}
	if !strings.Contains(view, "agent  ·  "+turnAt.Format("2006-01-02 15:04")+"  ·  completed") ||
		strings.Contains(view, "partial response") || strings.Contains(view, "collapsed") || strings.Contains(view, "expanded") {
		t.Fatalf("attached trace status presentation = %q", view)
	}
	traceAt := strings.Index(view, "attached provider trace")
	finalAt := strings.Index(view, "final answer")
	if traceAt < 0 || finalAt < 0 || traceAt >= finalAt {
		t.Fatalf("trace/final response order: trace=%d final=%d view=%q", traceAt, finalAt, view)
	}
	lines := strings.Split(m.conv.View(), "\n")
	headerLine, traceLine, finalLine, dividerLine := -1, -1, -1, -1
	for i, line := range lines {
		plain := ansi.Strip(line)
		switch {
		case strings.Contains(plain, "completed"):
			headerLine = i
		case strings.Contains(plain, "attached provider trace"):
			traceLine = i
		case strings.Contains(plain, "final answer"):
			finalLine = i
		}
		if strings.TrimSpace(plain) == strings.Repeat("─", 40) {
			dividerLine = i
		}
	}
	if headerLine < 0 || traceLine < 0 || finalLine < 0 || headerLine >= traceLine || traceLine >= finalLine {
		t.Fatalf("selected turn line order: header=%d trace=%d final=%d", headerLine, traceLine, finalLine)
	}
	if dividerLine < 0 {
		t.Fatalf("turn divider row was removed: %q", view)
	}
	if strings.Contains(lines[dividerLine], "48;5;237") {
		t.Fatalf("turn divider row is part of the selection: %q", lines[dividerLine])
	}
	for _, line := range lines[headerLine:finalLine] {
		if !strings.Contains(line, "48;5;237") {
			t.Fatalf("selected turn rectangle has an unhighlighted line: %q", line)
		}
	}
	if !strings.Contains(m.conv.View(), "38;5;245;") {
		t.Fatalf("expanded trace is not muted: %q", m.conv.View())
	}
}
