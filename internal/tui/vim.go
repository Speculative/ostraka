package tui

import (
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// vimEditor is local to the turn composer, independent of application focus
// and the item's execution mode. Its zero value starts in insert mode.
type vimEditor struct {
	normal        bool
	count         int
	pending       string
	find          rune
	findCommand   string
	desiredCol    int
	keepColumn    bool
	operator      string
	operatorCount int
	register      string
	linewise      bool
	undo          []vimSnapshot
	redo          []vimSnapshot
	insertBefore  *vimSnapshot
	visual        bool
	visualLine    bool
	anchor        int
	commandKeys   []tea.KeyMsg
	lastKeys      []tea.KeyMsg
	lastPatch     *vimInsertPatch
	insertBase    *vimSnapshot
	insertRepeat  int
	insertEntry   string
	replaying     bool
}

func (m model) vimTurnEditor() bool {
	return m.mode == modeCompose && !m.editingProject && !m.editingNewItemDraft() && !m.reviewComposer.active
}

func (m model) handleVimMovement(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	key := msg.String()
	if msg.Paste {
		// Bracketed paste is literal content even in normal mode. The existing
		// textarea path performs insertion and draft checkpointing.
		m.vim.clearPending()
		return m, nil, false
	}
	switch key {
	case "ctrl+s", "ctrl+c", "ctrl+n", "pgup", "pgdown", "shift+tab":
		m.vim.clearPending()
		return m, nil, false
	case "esc":
		m.vim.clearPending()
		if m.vim.normal {
			return m, nil, false // existing save-and-blur path
		}
		m.vim.normal = true
		m.closeMentionPicker()
		p := textareaCursorOffset(m.input)
		r := []rune(m.input.Value())
		start, _ := vimLine(r, p)
		if p > start {
			p--
		}
		setTextareaCursorOffset(&m.input, vimNormalCursor(r, p))
		return m, nil, true
	}
	if !m.vim.normal {
		return m, nil, false
	}
	r := []rune(m.input.Value())
	p := vimNormalCursor(r, textareaCursorOffset(m.input))
	n := max(1, m.vim.count)
	if m.vim.pending == "f" || m.vim.pending == "F" || m.vim.pending == "t" || m.vim.pending == "T" {
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
			m.vim.find, m.vim.findCommand = msg.Runes[0], m.vim.pending
			p = vimFind(r, p, m.vim.find, m.vim.findCommand, n, false)
		}
		m.vim.clearPending()
	} else if m.vim.pending == "g" {
		switch key {
		case "g":
			p = vimLineNumber(r, n)
		case "j", "k":
			// Bubbles' arrows move through soft-wrapped display rows.
			m.vim.clearPending()
			arrow := tea.KeyDown
			if key == "k" {
				arrow = tea.KeyUp
			}
			for range n {
				m.input, _ = m.input.Update(tea.KeyMsg{Type: arrow})
			}
			setTextareaCursorOffset(&m.input, vimNormalCursor(r, textareaCursorOffset(m.input)))
			return m, nil, true
		}
		m.vim.clearPending()
	} else {
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && (key != "0" || m.vim.count > 0) {
			// Bound counts so an accidental held digit cannot stall the TUI.
			m.vim.count = min(10000, m.vim.count*10+int(key[0]-'0'))
			return m, nil, true
		}
		switch key {
		case "g", "f", "F", "t", "T":
			m.vim.pending = key
			return m, nil, true
		case "i", "a", "I", "A", "o", "O":
			start, end := vimLine(r, p)
			switch key {
			case "a":
				if p < end {
					p++
				}
			case "I":
				p = vimFirstNonblank(r, start, end)
			case "A":
				p = end
			case "o":
				p = end
				r = append(append(append([]rune{}, r[:p]...), '\n'), r[p:]...)
				p++
				m.input.SetValue(string(r))
			case "O":
				p = start
				r = append(append(append([]rune{}, r[:p]...), '\n'), r[p:]...)
				m.input.SetValue(string(r))
			}
			m.vim.normal = false
			m.vim.clearPending()
			setTextareaCursorOffset(&m.input, p)
			m = m.recalcLayout()
			m.refreshMentionPicker()
			if key == "o" || key == "O" {
				next, cmd := m.scheduleDraftCheckpoint(nil)
				return next.(model), cmd, true
			}
			return m, nil, true
		case "j", "k", "up", "down":
			start, _ := vimLine(r, p)
			if !m.vim.keepColumn {
				m.vim.desiredCol = p - start
			}
			m.vim.keepColumn = true
			direction := 1
			if key == "k" || key == "up" {
				direction = -1
			}
			p = vimVertical(r, p, direction*n, m.vim.desiredCol)
			m.vim.count = 0
			setTextareaCursorOffset(&m.input, p)
			return m, nil, true
		case "G":
			if m.vim.count == 0 {
				p, _ = vimLine(r, len(r))
			} else {
				p = vimLineNumber(r, n)
			}
		case ";", ",":
			command := m.vim.findCommand
			if key == "," {
				command = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[command]
			}
			p = vimFind(r, p, m.vim.find, command, n, true)
		default:
			p = vimMotion(r, p, key, n)
		}
		m.vim.clearPending()
	}
	setTextareaCursorOffset(&m.input, vimNormalCursor(r, p))
	return m, nil, true
}

func (v *vimEditor) clearPending() {
	v.count, v.pending, v.keepColumn = 0, "", false
	v.operator, v.operatorCount = "", 0
}

// Line bounds exclude the newline. All positions count runes, not bytes.
func vimLine(r []rune, p int) (start, end int) {
	p = min(max(0, p), len(r))
	start, end = p, p
	for start > 0 && r[start-1] != '\n' {
		start--
	}
	for end < len(r) && r[end] != '\n' {
		end++
	}
	return
}

func vimNormalCursor(r []rune, p int) int {
	p = min(max(0, p), len(r))
	start, end := vimLine(r, p)
	if end > start && p == end {
		p--
	}
	return p
}

func vimFirstNonblank(r []rune, start, end int) int {
	for start < end && (r[start] == ' ' || r[start] == '\t') {
		start++
	}
	return start
}

func vimLineNumber(r []rune, n int) int {
	p := 0
	for p < len(r) && n > 1 {
		if r[p] == '\n' {
			n--
		}
		p++
	}
	if n > 1 {
		p, _ = vimLine(r, len(r))
	}
	start, end := vimLine(r, p)
	return vimNormalCursor(r, vimFirstNonblank(r, start, end))
}

func vimVertical(r []rune, p, delta, col int) int {
	start, end := vimLine(r, p)
	for delta != 0 {
		if delta > 0 {
			if end == len(r) {
				break
			}
			start, end = vimLine(r, end+1)
			delta--
		} else {
			if start == 0 {
				break
			}
			start, end = vimLine(r, start-1)
			delta++
		}
	}
	return vimNormalCursor(r, min(start+col, end))
}

func vimWordClass(c rune, big bool) int {
	if unicode.IsSpace(c) {
		return 0
	}
	if big || unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' {
		return 1
	}
	return 2
}

func vimMotion(r []rune, p int, key string, n int) int {
	start, end := vimLine(r, p)
	switch key {
	case "h", "left":
		return max(start, p-n)
	case "l", "right":
		return min(max(start, end-1), p+n)
	case "0", "home":
		return start
	case "^":
		return vimFirstNonblank(r, start, end)
	case "$", "end":
		if n > 1 {
			p = vimVertical(r, p, n-1, 0)
			_, end = vimLine(r, p)
		}
		return vimNormalCursor(r, end)
	case "%":
		return vimMatchBracket(r, p)
	case "w", "W", "b", "B", "e", "E":
		big := key == "W" || key == "B" || key == "E"
		class := func(i int) int { return vimWordClass(r[i], big) }
		for range n {
			switch key {
			case "w", "W":
				if p < len(r) {
					c := class(p)
					for p < len(r) && class(p) == c {
						p++
					}
				}
				for p < len(r) && class(p) == 0 {
					p++
				}
			case "b", "B":
				if p > 0 {
					p--
				}
				for p > 0 && class(p) == 0 {
					p--
				}
				if p < len(r) {
					c := class(p)
					for p > 0 && class(p-1) == c {
						p--
					}
				}
			case "e", "E":
				if p < len(r)-1 {
					p++
				}
				for p < len(r)-1 && class(p) == 0 {
					p++
				}
				if p < len(r) {
					c := class(p)
					for p < len(r)-1 && class(p+1) == c {
						p++
					}
				}
			}
		}
	}
	return vimNormalCursor(r, p)
}

func vimFind(r []rune, p int, target rune, command string, n int, repeat bool) int {
	q, _ := vimFindPosition(r, p, target, command, n, repeat)
	return q
}

func vimFindPosition(r []rune, p int, target rune, command string, n int, repeat bool) (int, bool) {
	if command == "" {
		return p, false
	}
	start, end := vimLine(r, p)
	direction := 1
	if command == "F" || command == "T" {
		direction = -1
	}
	i := p + direction
	if repeat && (command == "t" || command == "T") {
		i += direction
	}
	for ; i >= start && i < end; i += direction {
		if r[i] == target {
			n--
			if n == 0 {
				if command == "t" || command == "T" {
					i -= direction
				}
				return i, true
			}
		}
	}
	return p, false
}

func vimMatchBracket(r []rune, p int) int {
	_, end := vimLine(r, p)
	pairs := map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}
	for i := p; i < end; i++ {
		other, ok := pairs[r[i]]
		if !ok {
			continue
		}
		direction := 1
		if r[i] == ')' || r[i] == ']' || r[i] == '}' {
			direction = -1
		}
		depth := 1
		for j := i + direction; j >= 0 && j < len(r); j += direction {
			if r[j] == r[i] {
				depth++
			} else if r[j] == other {
				depth--
				if depth == 0 {
					return j
				}
			}
		}
		return p
	}
	return p
}
