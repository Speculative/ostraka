package tui

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestShiftTabGoalCycleAndExclusiveOwner(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.CreateItem(models.ChannelInbox, "first", "body", models.TypeThread, models.StatusActive, "")
	second, _ := s.CreateItem(models.ChannelInbox, "second", "body", models.TypeThread, models.StatusActive, "")
	m := newModel(s, nil, nil)
	m.items, m.allItems = []models.Item{first, second}, []models.Item{first, second}
	m.selected = 0
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.items[0].Mode != models.AgentModeGoal || !strings.Contains(ansi.Strip(m.renderHeader()), "goal armed: "+first.ID) {
		t.Fatal("CODE did not become armed GOAL")
	}
	m.selected = 1
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.err == nil || m.items[1].Mode == models.AgentModeGoal {
		t.Fatal("second goal was accepted")
	}
	m.selected = 0
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.items[0].Mode != models.AgentModeChat {
		t.Fatal("GOAL did not become CHAT")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.items[0].Mode != models.AgentModeCode {
		t.Fatal("CHAT did not become CODE")
	}
}

func TestGoalBlueIndicatorAndHeaderAcrossViews(t *testing.T) {
	goal := models.Item{ID: "goal", Mode: models.AgentModeGoal, Goal: &models.Goal{Prompt: "work"}, Status: models.StatusAgentAcknowledged}
	glyph, color, ok := itemStatusIndicator(goal)
	if !ok || glyph != "●" || color != goalFg {
		t.Fatalf("working goal dot = %s, %s, %v", glyph, color, ok)
	}
	goal.Status = models.StatusPendingAgent
	glyph, color, ok = itemStatusIndicator(goal)
	if !ok || glyph != "○" || color != goalFg {
		t.Fatal("queued goal did not retain blue")
	}
	m := newModel(nil, nil, nil)
	m.allItems = []models.Item{goal}
	m.width = 60
	if !strings.Contains(ansi.Strip(m.renderHeader()), "goal active: goal") {
		t.Fatal("goal hidden with its row filtered out")
	}
}
