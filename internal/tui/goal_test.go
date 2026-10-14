package tui

import (
	"fmt"
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
	if m.items[0].Mode != models.AgentModeGoal || strings.Contains(ansi.Strip(m.renderHeader()), "goal ") {
		t.Fatal("CODE did not become armed GOAL")
	}
	m.selected = 1
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.err != nil || m.items[1].Mode != models.AgentModeChat {
		t.Fatalf("occupied GOAL was not skipped: mode=%s error=%v", m.items[1].Mode, m.err)
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

func TestShiftTabSkipsOccupiedGoal(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, composing := range []bool{false, true} {
			t.Run(fmt.Sprintf("active=%v/composing=%v", active, composing), func(t *testing.T) {
				s, err := store.NewStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				owner, err := s.CreateItem(models.ChannelInbox, "owner", "body", models.TypeThread, models.StatusActive, "")
				if err != nil {
					t.Fatal(err)
				}
				item, err := s.CreateItem(models.ChannelInbox, "work", "body", models.TypeThread, models.StatusActive, "")
				if err != nil {
					t.Fatal(err)
				}
				m := newModel(s, nil, nil)
				// Keep the owner out of the view and arm it after the UI snapshot.
				m.items, m.allItems = []models.Item{item}, []models.Item{item}
				if _, err := s.SetMode(owner.ID, models.AgentModeGoal); err != nil {
					t.Fatal(err)
				}
				if active {
					if _, err := s.AddTurn(owner.ID, models.ActorUser, "keep working"); err != nil {
						t.Fatal(err)
					}
				}
				if composing {
					next, _ := m.openComposer()
					m = next.(model)
					m.input.SetValue("unsent turn")
				}
				for _, want := range []models.AgentMode{models.AgentModeChat, models.AgentModeCode, models.AgentModeChat} {
					next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
					m = next.(model)
					stored, err := s.GetItem(item.ID)
					if err != nil {
						t.Fatal(err)
					}
					if m.err != nil || stored.Mode != want || m.items[0].Mode != want || m.allItems[0].Mode != want {
						t.Fatalf("want %s: stored=%s visible=%s all=%s error=%v", want, stored.Mode, m.items[0].Mode, m.allItems[0].Mode, m.err)
					}
					if composing && (m.mode != modeCompose || m.input.Value() != "unsent turn") {
						t.Fatal("mode cycle did not preserve composed turn")
					}
				}
				goal, found, err := s.GoalItem()
				if err != nil || !found || goal.ID != owner.ID || (goal.Goal != nil) != active {
					t.Fatalf("mode cycle changed goal owner: goal=%+v found=%v error=%v", goal, found, err)
				}
			})
		}
	}
}

func TestShiftTabCyclesModeWhileComposingTurn(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "work", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.items, m.allItems = []models.Item{item}, []models.Item{item}
	m.selected = 0
	next, _ := m.openComposer()
	m = next.(model)
	m.input.SetValue("unsent turn")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.mode != modeCompose || m.input.Value() != "unsent turn" || m.items[0].Mode != models.AgentModeGoal {
		t.Fatal("Shift+Tab did not change mode while preserving the composed turn")
	}

	stored, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Mode != models.AgentModeGoal {
		t.Fatal("composed turn mode change was not persisted")
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

func TestBacklogModeCycleRejectsGoalTurnAndPreservesDraft(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "parked", "body", models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		t.Fatal(err)
	}
	sup := &fakeSupervisor{}
	m := newModel(s, nil, sup)
	m.items, m.allItems = []models.Item{item}, []models.Item{item}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.err != nil || m.items[0].Mode != models.AgentModeGoal {
		t.Fatalf("could not cycle backlog to GOAL: %v", m.err)
	}

	next, _ = m.openComposer()
	m = next.(model)
	m.input.SetValue("keep this turn")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if m.err == nil || !strings.Contains(m.err.Error(), "goal mode requires a live item") || m.mode != modeCompose || m.input.Value() != "keep this turn" {
		t.Fatalf("goal submission did not preserve composer: error=%v mode=%v text=%q", m.err, m.mode, m.input.Value())
	}
	draft, err := s.LoadDraft(item.ID)
	if err != nil || draft != "keep this turn" {
		t.Fatalf("rejected turn draft = %q, %v", draft, err)
	}
	saved, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != models.StatusBacklog || saved.Goal != nil || len(saved.Turns) != 0 || len(sup.enqueued) != 0 {
		t.Fatalf("goal submission changed backlog or queued work: %+v, %v", saved, sup.enqueued)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.err != nil || m.items[0].Mode != models.AgentModeChat || m.input.Value() != "keep this turn" {
		t.Fatalf("could not cycle backlog to CHAT with draft intact: %v", m.err)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	saved, err = s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.err != nil || m.mode != modeNav || saved.Mode != models.AgentModeChat || saved.Status != models.StatusBacklog || len(saved.Turns) != 1 || saved.Turns[0].Content != "keep this turn" || len(sup.enqueued) != 0 {
		t.Fatalf("chat turn was not saved without dispatch: error=%v item=%+v queued=%v", m.err, saved, sup.enqueued)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.err != nil || m.items[0].Mode != models.AgentModeCode {
		t.Fatalf("could not cycle backlog back to CODE: %v", m.err)
	}
}
