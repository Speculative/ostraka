package tui

import (
	"sort"
	"strings"
	"unicode"

	"github.com/Speculative/ostraka/internal/models"
)

// mentionToken is the @-token surrounding a textarea cursor. start and end
// are rune offsets into the complete buffer, rather than byte offsets. That
// keeps replacement correct for Unicode text and for a cursor in the middle
// of a line or a soft-wrapped row.
type mentionToken struct {
	start int
	end   int
	query string
}

type mentionPickerState struct {
	open     bool
	query    string
	start    int
	end      int
	items    []models.Item
	selected int
}

// mentionCandidate is kept separate from models.Item so ranking remains a
// pure operation and the UI can retain the selected item while results are
// refreshed as the query changes.
type mentionCandidate struct {
	item  models.Item
	class int
}

const (
	mentionExactID = iota
	mentionPrefixID
	mentionWordPrefixTitle
	mentionSubstringTitle
	mentionEmptyQuery
)

func (m *model) closeMentionPicker() {
	m.mentionPicker = mentionPickerState{}
}

func (m model) mentionSourceItems() []models.Item {
	if m.allItems != nil {
		return m.allItems
	}
	return m.items
}

// refreshMentionPicker derives picker state from the live textarea every time
// the user edits or moves the cursor. That makes the picker resilient to
// multiline input, soft wrapping, Unicode, and edits in the middle of a
// buffer without trying to maintain a second cursor model.
func (m *model) refreshMentionPicker() {
	if m.mode != modeCompose || m.editingProject {
		m.closeMentionPicker()
		return
	}
	token, ok := mentionTokenAt(m.input.Value(), textareaCursorOffset(m.input))
	if !ok {
		m.closeMentionPicker()
		return
	}
	old := m.mentionPicker
	items := rankMentionCandidates(token.query, m.mentionSourceItems())
	selected := 0
	if old.open && old.query == token.query && old.start == token.start {
		for i, item := range items {
			if old.selected < len(old.items) && item.ID == old.items[old.selected].ID {
				selected = i
				break
			}
		}
	}
	if selected >= len(items) {
		selected = 0
	}
	m.mentionPicker = mentionPickerState{
		open:     true,
		query:    token.query,
		start:    token.start,
		end:      token.end,
		items:    items,
		selected: selected,
	}
}

// mentionTokenAt returns the complete mention token containing cursor. A
// token is an @ followed by identifier-like runes. Punctuation and whitespace
// therefore end the picker naturally, while Unicode prose around the token is
// still handled as a proper word boundary.
func mentionTokenAt(text string, cursor int) (mentionToken, bool) {
	runes := []rune(text)
	cursor = clampMentionOffset(cursor, len(runes))

	startQuery := cursor
	for startQuery > 0 && mentionQueryRune(runes[startQuery-1]) {
		startQuery--
	}
	if startQuery == 0 || runes[startQuery-1] != '@' {
		return mentionToken{}, false
	}
	at := startQuery - 1
	if at > 0 && !mentionBoundaryRune(runes[at-1]) {
		return mentionToken{}, false
	}

	end := cursor
	for end < len(runes) && mentionQueryRune(runes[end]) {
		end++
	}
	return mentionToken{start: at, end: end, query: string(runes[startQuery:end])}, true
}

func clampMentionOffset(offset, length int) int {
	if offset < 0 {
		return 0
	}
	if offset > length {
		return length
	}
	return offset
}

func mentionQueryRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_' || r == '-'
}

func mentionBoundaryRune(r rune) bool {
	return !mentionQueryRune(r)
}

// rankMentionCandidates filters and ranks items for query. IDs are matched
// before titles: exact ID, ID prefix, title word-prefix, then title substring.
// Live/archive status is only consulted after that relevance class, as a
// tie-breaker. The final title/ID order makes otherwise equal results stable.
func rankMentionCandidates(query string, items []models.Item) []models.Item {
	query = strings.ToLower(query)
	candidates := make([]mentionCandidate, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		class, ok := mentionMatchClass(query, item)
		if ok {
			candidates = append(candidates, mentionCandidate{item: item, class: class})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].class != candidates[j].class {
			return candidates[i].class < candidates[j].class
		}
		iLive := !models.TerminalStatuses[candidates[i].item.Status]
		jLive := !models.TerminalStatuses[candidates[j].item.Status]
		if iLive != jLive {
			return iLive
		}
		iTitle := strings.ToLower(candidates[i].item.Title)
		jTitle := strings.ToLower(candidates[j].item.Title)
		if iTitle != jTitle {
			return iTitle < jTitle
		}
		return candidates[i].item.ID < candidates[j].item.ID
	})

	result := make([]models.Item, len(candidates))
	for i, candidate := range candidates {
		result[i] = candidate.item
	}
	return result
}

func mentionMatchClass(query string, item models.Item) (int, bool) {
	if query == "" {
		return mentionEmptyQuery, true
	}
	id := strings.ToLower(item.ID)
	switch {
	case id == query:
		return mentionExactID, true
	case strings.HasPrefix(id, query):
		return mentionPrefixID, true
	}

	title := strings.ToLower(item.Title)
	for _, word := range strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r)
	}) {
		if strings.HasPrefix(word, query) {
			return mentionWordPrefixTitle, true
		}
	}
	if strings.Contains(title, query) {
		return mentionSubstringTitle, true
	}
	return 0, false
}

// replaceMention replaces the complete token with the canonical @ID and a
// trailing space. It returns the new rune cursor offset, leaving all text
// outside the token untouched.
func replaceMention(text string, start, end int, canonicalID string) (string, int) {
	runes := []rune(text)
	if start < 0 || end < start || start > len(runes) || end > len(runes) {
		return text, clampMentionOffset(start, len(runes))
	}
	replacement := []rune("@" + canonicalID + " ")
	out := make([]rune, 0, len(runes)-(end-start)+len(replacement))
	out = append(out, runes[:start]...)
	out = append(out, replacement...)
	out = append(out, runes[end:]...)
	return string(out), start + len(replacement)
}
