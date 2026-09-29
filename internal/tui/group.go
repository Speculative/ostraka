package tui

import (
	"sort"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type groupPickerState struct {
	open     bool
	query    string
	groups   []string
	selected int
}

func (m *model) closeGroupPicker() {
	m.groupPicker = groupPickerState{}
}

func (m model) groupSourceItems() []models.Item {
	if m.allItems != nil {
		return m.allItems
	}
	return m.items
}

func groupNames(items []models.Item) []string {
	seen := make(map[string]struct{})
	for _, item := range items {
		if item.Parent != "" {
			continue
		}
		group := strings.TrimSpace(item.Group)
		if group != "" {
			seen[group] = struct{}{}
		}
	}
	groups := make([]string, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func groupMatchClass(query, group string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	group = strings.ToLower(strings.TrimSpace(group))
	if query == "" {
		return 3, true
	}
	switch {
	case group == query:
		return 0, true
	case strings.HasPrefix(group, query):
		return 1, true
	case strings.Contains(group, query):
		return 2, true
	default:
		return 0, false
	}
}

func rankGroupCandidates(query string, groups []string) []string {
	type candidate struct {
		group string
		class int
	}
	candidates := make([]candidate, 0, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		class, ok := groupMatchClass(query, group)
		if ok {
			candidates = append(candidates, candidate{group: group, class: class})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].class != candidates[j].class {
			return candidates[i].class < candidates[j].class
		}
		return strings.ToLower(candidates[i].group) < strings.ToLower(candidates[j].group)
	})
	result := make([]string, len(candidates))
	for i, candidate := range candidates {
		result[i] = candidate.group
	}
	return result
}

func (m *model) openGroupPicker(current string) {
	groups := groupNames(m.groupSourceItems())
	selected := 0
	current = strings.TrimSpace(current)
	for i, group := range groups {
		if group == current {
			selected = i
			break
		}
	}
	m.groupPicker = groupPickerState{
		open:     true,
		groups:   groups,
		selected: selected,
	}
}

func (m *model) refreshGroupPicker() {
	if m.mode != modeGroup || !m.groupPicker.open {
		return
	}
	old := m.groupPicker
	groups := rankGroupCandidates(strings.TrimSpace(m.input.Value()), groupNames(m.groupSourceItems()))
	selected := 0
	if old.selected < len(old.groups) {
		oldGroup := old.groups[old.selected]
		for i, group := range groups {
			if group == oldGroup {
				selected = i
				break
			}
		}
	}
	if selected >= len(groups) {
		selected = 0
	}
	m.groupPicker.query = strings.TrimSpace(m.input.Value())
	m.groupPicker.groups = groups
	m.groupPicker.selected = selected
}

func (m model) chooseGroup() (tea.Model, tea.Cmd) {
	if !m.groupPicker.open || len(m.groupPicker.groups) == 0 {
		return m, nil
	}
	selected := clampMentionOffset(m.groupPicker.selected, len(m.groupPicker.groups)-1)
	group := strings.TrimSpace(m.groupPicker.groups[selected])
	oldHeight := m.currentInputHeight()
	m.input.SetValue(group)
	m.input.CursorEnd()
	m.closeGroupPicker()
	if height := m.currentInputHeight(); height != oldHeight {
		m = m.adjustInputHeight(height)
	}
	return m, nil
}

func (m model) groupPickerBox() string {
	const maxRows = 6
	contentWidth := max(1, m.conv.Width-4)
	header := ansi.Truncate("group names", contentWidth, "…")
	rows := []string{header}
	if len(m.groupPicker.groups) == 0 {
		rows = append(rows, dimStyle.Render("(no matching groups)"))
	} else {
		start := 0
		if m.groupPicker.selected >= maxRows {
			start = m.groupPicker.selected - maxRows + 1
		}
		end := min(len(m.groupPicker.groups), start+maxRows)
		for i := start; i < end; i++ {
			marker := "  "
			style := lipgloss.NewStyle()
			if i == m.groupPicker.selected {
				marker = "› "
				style = style.Bold(true).Foreground(pendingFg)
			}
			row := marker + m.groupPicker.groups[i]
			rows = append(rows, style.Render(ansi.Truncate(row, contentWidth, "…")))
		}
	}
	return popupStyle.Render(strings.Join(rows, "\n"))
}

func (m model) overlayGroupPickerAtCursor(panel string) string {
	if !m.groupPicker.open {
		return panel
	}
	lines := strings.Split(panel, "\n")
	if len(lines) == 0 {
		return panel
	}
	inputTop := m.conv.Height + 2
	visualRow, column := textareaVisualPosition(m.input, textareaCursorOffset(m.input))
	textareaViewportOffset := textareaViewport(&m.input).YOffset
	row := inputTop + visualRow - textareaViewportOffset
	left := 1 + textareaGutterWidth(m.input) + column
	box := m.groupPickerBox()
	boxHeight := lipgloss.Height(box)
	return strings.Join(overlayBoxAt(lines, box, row-boxHeight, left), "\n")
}
