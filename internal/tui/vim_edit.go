package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type vimSnapshot struct {
	text   string
	cursor int
}

func (m model) vimSnapshot() vimSnapshot {
	return vimSnapshot{m.input.Value(), textareaCursorOffset(m.input)}
}

// History stacks use copy-on-write: Bubble Tea model updates and tests may
// retain earlier model values, so append must not mutate their backing arrays.
func vimPush(stack []vimSnapshot, s vimSnapshot) []vimSnapshot {
	if len(stack) >= 100 {
		stack = stack[len(stack)-99:]
	}
	return append(append([]vimSnapshot(nil), stack...), s)
}

func (m *model) vimRemember(before vimSnapshot) {
	if before.text == m.input.Value() {
		return
	}
	m.vim.undo = vimPush(m.vim.undo, before)
	m.vim.redo = nil
}

func (m model) vimRestore(s vimSnapshot) (model, tea.Cmd, bool) {
	m.input.SetValue(s.text)
	setTextareaCursorOffset(&m.input, vimNormalCursor([]rune(s.text), s.cursor))
	m.closeMentionPicker()
	m = m.recalcLayout()
	next, cmd := m.scheduleDraftCheckpoint(nil)
	return next.(model), cmd, true
}

func (m model) handleVimEdit(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	before := m.vimSnapshot()
	key := msg.String()
	if msg.Paste {
		m.vim.visual = false
		if m.vim.normal {
			// The textarea path below records the literal insertion as one unit.
			m.vim.insertBefore = nil
		}
		return m.handleVimMovement(msg)
	}
	if !m.vim.normal {
		if m.vim.insertBefore == nil {
			m.vim.insertBefore = &before
		}
		next, cmd, handled := m.handleVimMovement(msg)
		if key == "esc" {
			next.vimRemember(*m.vim.insertBefore)
			next.vim.insertBefore = nil
		}
		return next, cmd, handled
	}
	if m.vim.visual && key == "esc" {
		m.vim.visual = false
		m.vim.clearPending()
		return m, nil, true
	}
	if key == "ctrl+s" || key == "ctrl+c" || key == "ctrl+n" || key == "pgup" || key == "pgdown" || key == "shift+tab" || (key == "esc" && !m.vim.visual) {
		return m.handleVimMovement(msg)
	}
	r := []rune(before.text)
	p := vimNormalCursor(r, before.cursor)
	n := max(1, m.vim.count)
	if m.vim.visual && m.vim.pending == "" {
		switch key {
		case "v", "V":
			line := key == "V"
			if m.vim.visualLine == line {
				m.vim.visual = false
			} else {
				m.vim.visualLine = line
			}
			m.vim.clearPending()
			return m, nil, true
		case "d", "c", "y", "x":
			a, b := m.vimSelection(r, p)
			line := m.vim.visualLine
			m.vim.visual = false
			if key == "x" {
				key = "d"
			}
			return m.vimApplyRange(before, r, a, b, key, line)
		case "i", "a", "I", "A", "o", "O", "p", "P", "r", "s", "S", "D", "C", "u", "ctrl+r":
			m.vim.clearPending()
			return m, nil, true
		}
	}
	if m.vim.pending == "replace" {
		m.vim.clearPending()
		if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 || msg.Runes[0] == '\n' {
			return m, nil, true
		}
		_, end := vimLine(r, p)
		if p+n > end {
			return m, nil, true
		}
		for i := p; i < p+n; i++ {
			r[i] = msg.Runes[0]
		}
		m.input.SetValue(string(r))
		setTextareaCursorOffset(&m.input, p+n-1)
		m.vimRemember(before)
		m = m.recalcLayout()
		next, cmd := m.scheduleDraftCheckpoint(nil)
		return next.(model), cmd, true
	}
	if m.vim.operator != "" {
		return m.handleVimOperator(msg, before, r, p)
	}
	// Prefixes such as f and g take their next key literally. Do not steal
	// their operands as editing commands (fr must find r, not replace).
	if m.vim.pending == "" {
		switch key {
		case "v", "V":
			m.vim.visual, m.vim.visualLine, m.vim.anchor = true, key == "V", p
			m.vim.clearPending()
			return m, nil, true
		case "u", "ctrl+r":
			m.vim.clearPending()
			for range n {
				from, to := &m.vim.undo, &m.vim.redo
				if key == "ctrl+r" {
					from, to = &m.vim.redo, &m.vim.undo
				}
				if len(*from) == 0 {
					break
				}
				current := m.vimSnapshot()
				s := (*from)[len(*from)-1]
				*from = (*from)[:len(*from)-1]
				*to = vimPush(*to, current)
				m.input.SetValue(s.text)
				setTextareaCursorOffset(&m.input, vimNormalCursor([]rune(s.text), s.cursor))
			}
			return m.vimRestore(m.vimSnapshot())
		case "d", "c", "y":
			m.vim.operator, m.vim.operatorCount, m.vim.count = key, n, 0
			return m, nil, true
		case "r":
			m.vim.pending = "replace"
			return m, nil, true
		case "x", "X", "s", "S", "D", "C":
			start, end := vimLine(r, p)
			a, b, op, linewise := p, min(p+n, end), "d", false
			switch key {
			case "X":
				a, b = max(start, p-n), p
			case "s":
				op = "c"
			case "S":
				a, b, op, linewise = vimLineRange(r, p, n)
				op = "c"
			case "D", "C":
				last := vimVertical(r, p, n-1, 0)
				_, b = vimLine(r, last)
				if key == "C" {
					op = "c"
				}
			}
			return m.vimApplyRange(before, r, a, b, op, linewise)
		case "p", "P":
			m.vim.clearPending()
			if m.vim.register == "" {
				return m, nil, true
			}
			text := strings.Repeat(m.vim.register, n)
			at := p
			if m.vim.linewise {
				start, end := vimLine(r, p)
				at = start
				if key == "p" {
					at = end
					if end < len(r) {
						at++
					} else {
						text = "\n" + strings.TrimSuffix(text, "\n")
					}
				}
			} else if key == "p" {
				_, end := vimLine(r, p)
				at = min(p+1, end)
			}
			m.input.SetValue(string(r[:at]) + text + string(r[at:]))
			cursor := at + len([]rune(text)) - 1
			if m.vim.linewise {
				cursor = at
				if strings.HasPrefix(text, "\n") {
					cursor++
				}
				fresh := []rune(m.input.Value())
				start, end := vimLine(fresh, cursor)
				cursor = vimFirstNonblank(fresh, start, end)
			}
			setTextareaCursorOffset(&m.input, vimNormalCursor([]rune(m.input.Value()), cursor))
			m.vimRemember(before)
			m = m.recalcLayout()
			next, cmd := m.scheduleDraftCheckpoint(nil)
			return next.(model), cmd, true
		}
	}
	next, cmd, handled := m.handleVimMovement(msg)
	if m.vim.normal && !next.vim.normal {
		// The opening o/O newline and ensuing inserted text undo together.
		next.vim.insertBefore = &before
	}
	return next, cmd, handled
}

func (m model) vimSelection(r []rune, p int) (a, b int) {
	a, b = min(m.vim.anchor, p), min(len(r), max(m.vim.anchor, p)+1)
	if m.vim.visualLine {
		a, _ = vimLine(r, a)
		_, b = vimLine(r, max(m.vim.anchor, p))
		if b < len(r) {
			b++
		}
	}
	return
}

// Half-open linewise ranges include the terminating newline when present.
func vimLineRange(r []rune, p, n int) (a, b int, op string, linewise bool) {
	a, _ = vimLine(r, p)
	last := vimVertical(r, p, n-1, 0)
	_, b = vimLine(r, last)
	if b < len(r) {
		b++
	}
	return a, b, "d", true
}

func (m model) vimApplyRange(before vimSnapshot, r []rune, a, b int, op string, linewise bool) (model, tea.Cmd, bool) {
	m.vim.clearPending()
	a, b = min(max(a, 0), len(r)), min(max(b, 0), len(r))
	if a > b {
		a, b = b, a
	}
	if a == b && op != "c" && !linewise {
		return m, nil, true
	}
	m.vim.register = string(r[a:b])
	m.vim.linewise = linewise
	if linewise && !strings.HasSuffix(m.vim.register, "\n") {
		m.vim.register += "\n"
	}
	if op == "y" {
		setTextareaCursorOffset(&m.input, vimNormalCursor(r, a))
		return m, nil, true
	}
	replacement := ""
	if linewise && op == "c" && b < len(r) {
		replacement = "\n"
	}
	// Deleting the last logical line also removes its preceding separator.
	if linewise && op == "d" && b == len(r) && a > 0 {
		a--
	}
	m.input.SetValue(string(r[:a]) + replacement + string(r[b:]))
	cursor := min(a, len([]rune(m.input.Value())))
	if op == "c" {
		m.vim.normal = false
		m.vim.insertBefore = &before
	} else {
		fresh := []rune(m.input.Value())
		cursor = vimNormalCursor(fresh, cursor)
		if linewise {
			start, end := vimLine(fresh, cursor)
			cursor = vimNormalCursor(fresh, vimFirstNonblank(fresh, start, end))
		}
		m.vimRemember(before)
	}
	setTextareaCursorOffset(&m.input, cursor)
	m.closeMentionPicker()
	m = m.recalcLayout()
	if op == "c" {
		m.refreshMentionPicker()
	}
	next, cmd := m.scheduleDraftCheckpoint(nil)
	return next.(model), cmd, true
}

func (m model) handleVimOperator(msg tea.KeyMsg, before vimSnapshot, r []rune, p int) (model, tea.Cmd, bool) {
	key := msg.String()
	op := m.vim.operator
	n := min(10000, max(1, m.vim.operatorCount)*max(1, m.vim.count))
	if m.vim.pending == "" && len(key) == 1 && key[0] >= '0' && key[0] <= '9' && (key != "0" || m.vim.count > 0) {
		m.vim.count = min(10000, m.vim.count*10+int(key[0]-'0'))
		return m, nil, true
	}
	if m.vim.pending == "" && key == op {
		a, b, _, linewise := vimLineRange(r, p, n)
		return m.vimApplyRange(before, r, a, b, op, linewise)
	}
	if m.vim.pending == "" && (key == "i" || key == "a") {
		m.vim.pending = key
		return m, nil, true
	}
	if m.vim.pending == "i" || m.vim.pending == "a" {
		if key != "w" {
			m.vim.clearPending()
			return m, nil, true
		}
		a, b := vimWordObject(r, p, n, m.vim.pending == "a")
		return m.vimApplyRange(before, r, a, b, op, false)
	}
	if m.vim.pending == "" && (key == "f" || key == "F" || key == "t" || key == "T" || key == "g") {
		m.vim.pending = key
		return m, nil, true
	}
	q, inclusive, linewise := p, false, false
	pending := m.vim.pending
	switch pending {
	case "f", "F", "t", "T":
		if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
			m.vim.clearPending()
			return m, nil, true
		}
		m.vim.find, m.vim.findCommand = msg.Runes[0], pending
		var found bool
		q, found = vimFindPosition(r, p, msg.Runes[0], pending, n, false)
		if !found {
			m.vim.clearPending()
			return m, nil, true
		}
		inclusive = true
	case "g":
		switch key {
		case "g":
			q, linewise = vimLineNumber(r, n), true
		case "j", "k":
			arrow := tea.KeyDown
			if key == "k" {
				arrow = tea.KeyUp
			}
			for range n {
				m.input, _ = m.input.Update(tea.KeyMsg{Type: arrow})
			}
			q = vimNormalCursor(r, textareaCursorOffset(m.input))
		default:
			m.vim.clearPending()
			return m, nil, true
		}
	default:
		switch key {
		case "j", "k", "up", "down":
			direction := 1
			if key == "k" || key == "up" {
				direction = -1
			}
			q, linewise = vimVertical(r, p, direction*n, 0), true
		case "G":
			q, linewise = vimLineNumber(r, n), true
			if m.vim.count == 0 && m.vim.operatorCount == 1 {
				q, _ = vimLine(r, len(r))
			}
		case "h", "l", "left", "right", "0", "^", "$", "home", "end", "w", "W", "b", "B", "e", "E", "%", ";", ",":
			q = vimMotion(r, p, key, n)
			if key == "l" || key == "right" {
				_, end := vimLine(r, p)
				q = min(p+n, end)
			}
			if key == "%" && q == p {
				m.vim.clearPending()
				return m, nil, true
			}
			inclusive = key == "e" || key == "E" || key == "$" || key == "end" || key == "%"
			if key == ";" || key == "," {
				command := m.vim.findCommand
				if key == "," {
					command = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[command]
				}
				var found bool
				q, found = vimFindPosition(r, p, m.vim.find, command, n, true)
				if !found {
					m.vim.clearPending()
					return m, nil, true
				}
				inclusive = true
			}
			// cw changes the current word without consuming its following space.
			if op == "c" && (key == "w" || key == "W") && p < len(r) && vimWordClass(r[p], key == "W") != 0 {
				class := vimWordClass(r[p], key == "W")
				q = p
				for q+1 < len(r) && vimWordClass(r[q+1], key == "W") == class {
					q++
				}
				if n > 1 {
					q = vimMotion(r, q, map[string]string{"w": "e", "W": "E"}[key], n-1)
				}
				inclusive = true
			}
			// A word motion to EOF must include the final character.
			if (key == "w" || key == "W") && q == vimNormalCursor(r, len(r)) {
				inclusive = true
			}
		default:
			m.vim.clearPending()
			return m, nil, true
		}
	}
	a, b := min(p, q), max(p, q)
	if linewise {
		a, _ = vimLine(r, a)
		_, b = vimLine(r, b)
		if b < len(r) {
			b++
		}
	} else if inclusive && b < len(r) {
		b++
	}
	return m.vimApplyRange(before, r, a, b, op, linewise)
}

func vimWordObject(r []rune, p, n int, around bool) (a, b int) {
	if p >= len(r) {
		return p, p
	}
	c := vimWordClass(r[p], false)
	a, b = p, p+1
	for a > 0 && r[a-1] != '\n' && vimWordClass(r[a-1], false) == c {
		a--
	}
	for b < len(r) && r[b] != '\n' && vimWordClass(r[b], false) == c {
		b++
	}
	for i := 1; i < n && b < len(r); i++ {
		for b < len(r) && vimWordClass(r[b], false) == 0 {
			b++
		}
		if b == len(r) {
			break
		}
		c = vimWordClass(r[b], false)
		for b < len(r) && vimWordClass(r[b], false) == c {
			b++
		}
	}
	if around {
		end := b
		for b < len(r) && (r[b] == ' ' || r[b] == '\t') {
			b++
		}
		if b == end {
			for a > 0 && (r[a-1] == ' ' || r[a-1] == '\t') {
				a--
			}
		}
	}
	return a, b
}
