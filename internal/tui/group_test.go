package tui

import (
	"reflect"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	tea "github.com/charmbracelet/bubbletea"
)

func TestRankGroupCandidates(t *testing.T) {
	groups := []string{"v1", "post-v1", "provider-work", "post-v1"}
	if got, want := rankGroupCandidates("post", groups), []string{"post-v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rankGroupCandidates(post) = %v, want %v", got, want)
	}
	if got, want := rankGroupCandidates("", groups), []string{"post-v1", "provider-work", "v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rankGroupCandidates(empty) = %v, want %v", got, want)
	}
}

func TestGroupPickerStartsOpenAndSelectsExistingGroup(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeGroup
	m.groupItemID = "item-1"
	m.input.SetValue("v1")
	m.input.CursorEnd()
	m.allItems = []models.Item{
		{ID: "root-v1", Group: "v1"},
		{ID: "root-post", Group: "post-v1"},
		{ID: "child", Parent: "root-v1", Group: "v1"},
	}
	m.openGroupPicker("v1")
	if !m.groupPicker.open || len(m.groupPicker.groups) != 2 {
		t.Fatalf("picker = %#v, want two groups open", m.groupPicker)
	}
	if got := m.groupPicker.groups[m.groupPicker.selected]; got != "v1" {
		t.Fatalf("initial selection = %q, want current group v1", got)
	}

	next, _ := m.handleGroupKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	next, _ = m.handleGroupKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.groupPicker.open {
		t.Fatal("picker remained open after choosing a group")
	}
	if got := m.input.Value(); got != "post-v1" {
		t.Fatalf("chosen group = %q, want post-v1", got)
	}
}

func TestGroupPickerFiltersAfterEditing(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.mode = modeGroup
	m.groupItemID = "item-1"
	m.input.SetValue("post")
	m.input.CursorEnd()
	m.allItems = []models.Item{
		{ID: "root-v1", Group: "v1"},
		{ID: "root-post", Group: "post-v1"},
	}
	m.openGroupPicker("")
	m.refreshGroupPicker()
	if got, want := m.groupPicker.groups, []string{"post-v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered groups = %v, want %v", got, want)
	}
}
