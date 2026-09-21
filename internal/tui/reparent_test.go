package tui

import (
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestReparentPickerPreservesSelectionAndGrouping(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldRoot, _ := s.CreateItem(models.ChannelInbox, "old root", "old body", models.TypeThread, models.StatusActive, "")
	newRoot, _ := s.CreateItem(models.ChannelInbox, "new root", "new body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(oldRoot.ID, "child", "child body", models.TypeThread, models.StatusActive)

	m := newModel(s, nil, nil)
	m.showBacklog = true
	m.reload()
	for i, item := range m.items {
		if item.ID == oldRoot.ID {
			m.selected = i
			break
		}
	}
	m.updateConv()

	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = next.(model)
	if m.mode != modeReparent || len(m.reparentTargets) != 1 || m.reparentTargets[0].ID != newRoot.ID {
		t.Fatalf("reparent picker = mode %v targets %+v", m.mode, m.reparentTargets)
	}

	next, _ = m.handleReparentKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.err == nil || !strings.Contains(m.err.Error(), "--flatten-children") {
		t.Fatalf("move without flatten error = %v", m.err)
	}
	unchanged, _ := s.GetItem(oldRoot.ID)
	if unchanged.Parent != "" {
		t.Fatalf("failed TUI move changed parent to %q", unchanged.Parent)
	}

	next, _ = m.handleReparentKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(model)
	if !m.reparentFlatten {
		t.Fatal("f did not enable flattening")
	}
	next, _ = m.handleReparentKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.mode != modeNav {
		t.Fatalf("mode after move = %v, want navigation", m.mode)
	}
	moved, _ := s.GetItem(oldRoot.ID)
	movedChild, _ := s.GetItem(child.ID)
	if moved.Parent != newRoot.ID || movedChild.Parent != newRoot.ID {
		t.Fatalf("flattened parents = %q and %q, want %q", moved.Parent, movedChild.Parent, newRoot.ID)
	}
	if m.selectedID() != oldRoot.ID {
		t.Fatalf("selected after move = %q, want %q", m.selectedID(), oldRoot.ID)
	}
	for _, item := range m.items {
		if item.ID == oldRoot.ID || item.ID == child.ID {
			if item.Parent != newRoot.ID {
				t.Errorf("rendered item %q parent = %q, want %q", item.ID, item.Parent, newRoot.ID)
			}
		}
	}
}

func TestReparentPickerRejectsFlattenForLeaf(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, _ := s.CreateItem(models.ChannelInbox, "source", "source body", models.TypeThread, models.StatusActive, "")
	target, _ := s.CreateItem(models.ChannelInbox, "target", "target body", models.TypeThread, models.StatusActive, "")
	m := newModel(s, nil, nil)
	m.showBacklog = true
	m.reload()
	for i, item := range m.items {
		if item.ID == source.ID {
			m.selected = i
			break
		}
	}
	next, _ := m.handleNavKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = next.(model)
	if m.mode != modeReparent || len(m.reparentTargets) != 1 || m.reparentTargets[0].ID != target.ID {
		t.Fatalf("leaf picker = mode %v targets %+v", m.mode, m.reparentTargets)
	}
	next, _ = m.handleReparentKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(model)
	if m.err == nil || !strings.Contains(m.err.Error(), "only valid") {
		t.Fatalf("leaf flatten error = %v", m.err)
	}
}
