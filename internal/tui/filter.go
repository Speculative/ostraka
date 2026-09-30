package tui

import (
	"strings"
	"unicode"

	"github.com/Speculative/ostraka/internal/models"
)

// listFilter is the parsed form of the text entered by the item-list search
// prompt. Field clauses are ANDed with one another and with every free-text
// term, so "cache group:v2 status:backlog" narrows the list in the expected
// way.
type listFilter struct {
	terms    []string
	groups   []string
	statuses []models.Status
	channels []models.Channel
}

func parseListFilter(query string) listFilter {
	var filter listFilter
	for _, token := range queryTokens(query) {
		key, value, hasValue := strings.Cut(token, ":")
		if hasValue {
			switch strings.ToLower(key) {
			case "group":
				if value != "" {
					filter.groups = append(filter.groups, strings.ToLower(value))
				}
				continue
			case "status":
				if value != "" {
					filter.statuses = append(filter.statuses, models.NormalizeStatus(models.Status(strings.ToLower(value))))
				}
				continue
			case "channel":
				if value != "" {
					filter.channels = append(filter.channels, models.Channel(strings.ToLower(value)))
				}
				continue
			}
		}
		if !hasValue && incompleteFieldToken(token) {
			// While the user is still typing a known field name, keep the
			// field clause inert. Treating "g", "gr", ... as free text makes
			// the list flicker before the colon turns it into group syntax.
			continue
		}
		filter.terms = append(filter.terms, strings.ToLower(token))
	}
	return filter
}

func incompleteFieldToken(token string) bool {
	token = strings.ToLower(token)
	for _, field := range []string{"group", "status", "channel"} {
		if len(token) < len(field) && strings.HasPrefix(field, token) {
			return true
		}
	}
	return token == "group" || token == "status" || token == "channel"
}

func (f listFilter) matches(item models.Item) bool {
	return f.matchesText(item, itemSearchText(item))
}

func (f listFilter) matchesText(item models.Item, searchable string) bool {
	for _, group := range f.groups {
		if group == "none" {
			if item.Group != "" {
				return false
			}
		} else if !strings.HasPrefix(strings.ToLower(item.Group), group) {
			return false
		}
	}
	for _, status := range f.statuses {
		if !strings.HasPrefix(string(models.NormalizeStatus(item.Status)), string(status)) {
			return false
		}
	}
	for _, channel := range f.channels {
		if !strings.HasPrefix(strings.ToLower(string(item.Channel)), strings.ToLower(string(channel))) {
			return false
		}
	}
	return f.matchesSearchTerms(searchable)
}

func filterItems(items []models.Item, query string) []models.Item {
	return filterItemsIndexed(items, query, nil)
}

func filterItemsIndexed(items []models.Item, query string, searchIndex map[string]string) []models.Item {
	filter := parseListFilter(query)
	if len(filter.terms) == 0 && len(filter.groups) == 0 && len(filter.statuses) == 0 && len(filter.channels) == 0 {
		return items
	}
	filtered := make([]models.Item, 0, len(items))
	for _, item := range items {
		searchable := ""
		if len(filter.terms) > 0 {
			if indexed, ok := searchIndex[item.ID]; ok {
				searchable = indexed
			} else {
				searchable = itemSearchText(item)
			}
		}
		if filter.matchesText(item, searchable) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (f listFilter) matchesSearchTerms(searchable string) bool {
	if len(f.terms) == 0 {
		return true
	}
	searchable = strings.ToLower(searchable)
	for _, term := range f.terms {
		if !strings.Contains(searchable, term) {
			return false
		}
	}
	return true
}

func buildItemSearchIndex(items []models.Item) map[string]string {
	index := make(map[string]string, len(items))
	for _, item := range items {
		index[item.ID] = strings.ToLower(itemSearchText(item))
	}
	return index
}

func itemSearchText(item models.Item) string {
	var b strings.Builder
	for _, value := range []string{
		item.ID,
		string(item.Channel),
		string(item.Type),
		string(item.Status),
		item.Group,
		item.Title,
		item.Body,
	} {
		b.WriteString(value)
		b.WriteByte('\n')
	}
	for _, turn := range item.Turns {
		b.WriteString(string(turn.Actor))
		b.WriteByte('\n')
		b.WriteString(turn.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// queryTokens accepts quoted phrases while keeping the common unquoted form
// fast and unsurprising. Quotes are removed, and a backslash quotes the next
// rune, which also makes it possible to search for a literal colon.
func queryTokens(query string) []string {
	var tokens []string
	var current []rune
	inQuote := rune(0)
	escaped := false
	flush := func() {
		if len(current) > 0 {
			tokens = append(tokens, string(current))
			current = nil
		}
	}
	for _, r := range query {
		if escaped {
			current = append(current, r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				current = append(current, r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			inQuote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		current = append(current, r)
	}
	if escaped {
		current = append(current, '\\')
	}
	flush()
	return tokens
}
