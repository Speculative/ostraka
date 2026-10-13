package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// An insert session is stored as a patch around its entry cursor. This captures
// backspaces, literal paste, and completed mentions without replaying picker UI.
type vimInsertPatch struct {
	left, right int
	text        string
}

func vimInsertionPatch(base vimSnapshot, after string) vimInsertPatch {
	a, b := []rune(base.text), []rune(after)
	if base.text == after {
		return vimInsertPatch{}
	}
	start := 0
	for start < base.cursor && start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > max(start, base.cursor) && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	if start == len(a) && start == len(b) {
		return vimInsertPatch{}
	}
	return vimInsertPatch{base.cursor - start, endA - base.cursor, string(b[start:endB])}
}

func (m model) vimApplyInsertPatch(patch vimInsertPatch) model {
	r := []rune(m.input.Value())
	p := textareaCursorOffset(m.input)
	a := min(max(0, p-patch.left), len(r))
	b := min(max(a, p+patch.right), len(r))
	m.input.SetValue(string(r[:a]) + patch.text + string(r[b:]))
	setTextareaCursorOffset(&m.input, a+len([]rune(patch.text)))
	return m
}

func vimCopyKeys(keys []tea.KeyMsg) []tea.KeyMsg {
	copy := make([]tea.KeyMsg, len(keys))
	for i, key := range keys {
		copy[i] = key
		copy[i].Runes = append([]rune(nil), key.Runes...)
	}
	return copy
}

func (m model) handleVimKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	key := msg.String()
	before := m.vimSnapshot()
	insertOriginal := before.text
	if m.vim.insertBefore != nil {
		insertOriginal = m.vim.insertBefore.text
	}
	if !msg.Paste && m.vim.normal && !m.vim.visual && m.vim.pending == "" && m.vim.operator == "" && key == "." {
		return m.vimRepeatChange()
	}
	finishingInsert := !m.vim.normal && !msg.Paste && key == "esc"
	var patch vimInsertPatch
	if !m.vim.normal && m.vim.insertBase == nil {
		m.vim.insertBase = &before
	}
	if finishingInsert {
		patch = vimInsertionPatch(*m.vim.insertBase, m.input.Value())
		if m.vim.insertRepeat > 1 {
			base := *m.vim.insertBase
			expanded := patch
			if m.vim.insertEntry == "o" || m.vim.insertEntry == "O" {
				expanded.text = strings.TrimSuffix(strings.Repeat(patch.text+"\n", m.vim.insertRepeat), "\n")
			} else {
				expanded.text = strings.Repeat(patch.text, m.vim.insertRepeat)
			}
			m.input.SetValue(base.text)
			setTextareaCursorOffset(&m.input, base.cursor)
			m = m.vimApplyInsertPatch(expanded)
		}
	}
	control := msg.Paste || key == "ctrl+s" || key == "ctrl+c" || key == "ctrl+n" || key == "pgup" || key == "pgdown" || key == "shift+tab" || key == "esc" || key == "u" || key == "ctrl+r"
	if !m.vim.replaying && m.vim.normal && !control {
		m.vim.commandKeys = append(vimCopyKeys(m.vim.commandKeys), msg)
	}
	next, cmd, handled := m.handleVimEdit(msg)
	if m.vim.normal && !next.vim.normal {
		base := next.vimSnapshot()
		next.vim.insertBase = &base
		next.vim.insertRepeat, next.vim.insertEntry = 1, key
		if m.vim.pending == "" && m.vim.operator == "" && (key == "i" || key == "a" || key == "I" || key == "A" || key == "o" || key == "O") {
			next.vim.insertRepeat = max(1, m.vim.count)
		}
	}
	if finishingInsert {
		if !m.vim.replaying && next.input.Value() != insertOriginal {
			keys := next.vim.commandKeys
			if len(keys) == 0 {
				keys = []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'i'}}}
			}
			next.vim.lastKeys, next.vim.lastPatch = vimCopyKeys(keys), &patch
		}
		next.vim.commandKeys, next.vim.insertBase = nil, nil
		next.vim.insertRepeat, next.vim.insertEntry = 0, ""
		result, checkpoint := next.scheduleDraftCheckpoint(cmd)
		return result.(model), checkpoint, handled
	}
	if !m.vim.replaying && next.vim.normal && !next.vim.visual && next.vim.pending == "" && next.vim.operator == "" && next.vim.count == 0 {
		if before.text != next.input.Value() && !control {
			next.vim.lastKeys, next.vim.lastPatch = vimCopyKeys(next.vim.commandKeys), nil
		}
		next.vim.commandKeys = nil
	}
	return next, cmd, handled
}

func (m model) vimRepeatChange() (model, tea.Cmd, bool) {
	if m.vim.replaying || len(m.vim.lastKeys) == 0 {
		m.vim.clearPending()
		return m, nil, true
	}
	keys := vimCopyKeys(m.vim.lastKeys)
	patch := m.vim.lastPatch
	if m.vim.count > 0 {
		for len(keys) > 0 && len(keys[0].Runes) == 1 && keys[0].Runes[0] >= '0' && keys[0].Runes[0] <= '9' {
			keys = keys[1:]
		}
		if len(keys) > 1 && (keys[0].String() == "d" || keys[0].String() == "c" || keys[0].String() == "y") && keys[1].String() != "0" {
			for len(keys) > 1 && len(keys[1].Runes) == 1 && keys[1].Runes[0] >= '0' && keys[1].Runes[0] <= '9' {
				keys = append(keys[:1], keys[2:]...)
			}
		}
		var prefix []tea.KeyMsg
		for _, c := range []rune(strconv.Itoa(m.vim.count)) {
			prefix = append(prefix, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
		}
		keys = append(prefix, keys...)
	}
	m.vim.clearPending()
	m.vim.commandKeys = nil
	m.vim.replaying = true
	var cmds []tea.Cmd
	for _, key := range keys {
		next, cmd := m.handleInputKey(key)
		m = next.(model)
		cmds = append(cmds, cmd)
	}
	if patch != nil && !m.vim.normal {
		m = m.vimApplyInsertPatch(*patch)
		// Repeat inserts the completed literal text directly. A picker opened
		// at the insertion point must not consume its synthetic escape.
		m.closeMentionPicker()
		next, cmd := m.handleInputKey(tea.KeyMsg{Type: tea.KeyEsc})
		m = next.(model)
		cmds = append(cmds, cmd)
	}
	m.vim.replaying = false
	m.closeMentionPicker()
	return m, tea.Batch(cmds...), true
}
