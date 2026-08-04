package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ostraka/internal/models"
	"ostraka/internal/store"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
)

// ── messages ─────────────────────────────────────────────────────────────────

type itemsLoadedMsg []models.Item
type watchEventMsg struct{}
type errMsg error

// ── styles ───────────────────────────────────────────────────────────────────

var (
	headerBg    = lipgloss.Color("18")
	footerBg    = lipgloss.Color("235")
	selectedBg  = lipgloss.Color("237")
	pendingFg   = lipgloss.Color("11")
	dimFg       = lipgloss.Color("8")

	headerStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("12")).
			Background(headerBg)
	headerTabActiveStyle = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(headerBg)
	headerTabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Background(headerBg)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(footerBg)
	selectedStyle = lipgloss.NewStyle().Background(selectedBg).Bold(true)
	pendingStyle  = lipgloss.NewStyle().Foreground(pendingFg)
	dimStyle      = lipgloss.NewStyle().Foreground(dimFg)
	borderStyle   = lipgloss.NewStyle().BorderRight(true).BorderStyle(lipgloss.NormalBorder())
)

// ── watcher goroutine ────────────────────────────────────────────────────────

// startWatcher returns a channel that receives a signal whenever any file under
// root changes. Rapid events are coalesced: at most one pending signal at a time.
func startWatcher(root string) (<-chan struct{}, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(root); err != nil {
		w.Close()
		return nil, err
	}
	// Also watch all immediate subdirectories (INBOX, ASKS, HANDOFF, ARCHIVE).
	entries, err := os.ReadDir(root)
	if err != nil {
		w.Close()
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			w.Add(filepath.Join(root, e.Name()))
		}
	}

	ch := make(chan struct{}, 1) // buffered: coalesces rapid bursts
	go func() {
		defer w.Close()
		for {
			select {
			case _, ok := <-w.Events:
				if !ok {
					return
				}
				// Non-blocking send: if a signal is already pending, drop this one.
				select {
				case ch <- struct{}{}:
				default:
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return ch, nil
}

// waitForWatch bridges the fsnotify goroutine into bubbletea. It blocks until a
// signal arrives, returns a watchEventMsg, and is re-issued after each event so
// the watch loop continues indefinitely.
func waitForWatch(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return watchEventMsg{}
	}
}

// loadItemsCmd loads items for the given channel in a bubbletea goroutine.
func loadItemsCmd(s *store.Store, ch models.Channel) tea.Cmd {
	return func() tea.Msg {
		items, err := s.ListItems(store.ListOpts{Channel: &ch})
		if err != nil {
			return errMsg(err)
		}
		return itemsLoadedMsg(items)
	}
}

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	store   *store.Store
	watchCh <-chan struct{}

	channel  models.Channel
	channels []models.Channel
	items    []models.Item
	selected int

	conv      viewport.Model
	input     textinput.Model
	inputMode bool

	width  int
	height int
	err    error
}

func newModel(s *store.Store, watchCh <-chan struct{}) model {
	inp := textinput.New()
	inp.Placeholder = "Add turn… (Enter to submit, Esc to cancel)"
	return model{
		store:    s,
		watchCh:  watchCh,
		channel:  models.ChannelAsks,
		channels: []models.Channel{models.ChannelInbox, models.ChannelAsks, models.ChannelHandoff},
		input:    inp,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		loadItemsCmd(m.store, m.channel),
		waitForWatch(m.watchCh),
	)
}

// ── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.recalcLayout()
		return m, nil

	case itemsLoadedMsg:
		prevID := m.selectedID()
		m.items = []models.Item(msg)
		m.restoreSelection(prevID)
		m.updateConv()
		return m, nil

	case watchEventMsg:
		// Re-arm the watcher and reload current channel.
		return m, tea.Batch(waitForWatch(m.watchCh), loadItemsCmd(m.store, m.channel))

	case errMsg:
		m.err = msg
		return m, nil

	case tea.KeyMsg:
		if m.inputMode {
			return m.handleInputKey(msg)
		}
		return m.handleNavKey(msg)
	}

	// Pass other messages to sub-components.
	if m.inputMode {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	} else {
		var cmd tea.Cmd
		m.conv, cmd = m.conv.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleNavKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.selected < len(m.items)-1 {
			m.selected++
			m.updateConv()
		}
	case "k", "up":
		if m.selected > 0 {
			m.selected--
			m.updateConv()
		}
	case "1":
		return m.switchChannel(models.ChannelInbox)
	case "2":
		return m.switchChannel(models.ChannelAsks)
	case "3":
		return m.switchChannel(models.ChannelHandoff)
	case "r":
		return m, loadItemsCmd(m.store, m.channel)
	case "t":
		if len(m.items) > 0 {
			m.inputMode = true
			m.input.SetValue("")
			return m, m.input.Focus()
		}
	}
	return m, nil
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		content := strings.TrimSpace(m.input.Value())
		if content != "" && m.selected < len(m.items) {
			id := m.items[m.selected].ID
			m.store.AddTurn(id, models.ActorUser, content) //nolint:errcheck
		}
		m.inputMode = false
		m.input.Blur()
		return m, loadItemsCmd(m.store, m.channel)
	case "esc":
		m.inputMode = false
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) switchChannel(ch models.Channel) (model, tea.Cmd) {
	m.channel = ch
	m.selected = 0
	m.items = nil
	m.updateConv()
	return m, loadItemsCmd(m.store, ch)
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.err != nil {
		return fmt.Sprintf("error: %v\n\nPress q to quit.", m.err)
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	listW := m.listWidth()
	listContent := m.renderList()
	mainH := m.height - 2 // subtract header and footer
	listPanelStyle := lipgloss.NewStyle().
		Padding(1).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		Height(mainH)
	listPanel := listPanelStyle.Width(listW).Render(listContent)
	convPanel := lipgloss.NewStyle().Padding(1).Render(m.conv.View())
	mainRow := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, convPanel)

	parts := []string{header, mainRow}
	if m.inputMode {
		parts = append(parts, m.input.View())
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m model) renderHeader() string {
	tabs := make([]string, len(m.channels))
	for i, ch := range m.channels {
		label := strings.ToUpper(string(ch[0])) + string(ch[1:])
		n := m.pendingCount(ch)
		if n > 0 {
			label = fmt.Sprintf("%s (%d)", label, n)
		}
		if ch == m.channel {
			tabs[i] = headerTabActiveStyle.Render(" [" + label + "] ")
		} else {
			tabs[i] = headerTabStyle.Render("  " + label + "  ")
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	// Pad to full width with header background.
	pad := m.width - lipgloss.Width(row)
	if pad > 0 {
		row += lipgloss.NewStyle().Background(headerBg).Render(strings.Repeat(" ", pad))
	}
	return row
}

func (m model) renderFooter() string {
	text := "q quit  j/k navigate  1/2/3 channel  t add turn  r refresh"
	pad := m.width - len(text)
	if pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return footerStyle.Render(text)
}

func (m model) renderList() string {
	if len(m.items) == 0 {
		return dimStyle.Render("(empty)")
	}
	colW := m.listWidth() - 2 // panel uses Width(listW) with Padding(1), so content = listW-2

	const badgeW = 2
	textW := colW - badgeW

	lines := make([]string, len(m.items))
	for i, item := range m.items {
		isSelected := i == m.selected
		isPending := item.Status == models.StatusPendingUser

		preview := strings.ReplaceAll(item.Body, "\n", " ")
		meta := fmt.Sprintf("%s [%s]", item.ID, item.Status)

		// Word-wrap the preview manually so we control each line's prefix and
		// background independently — JoinHorizontal pads shorter columns with
		// unstyled spaces, losing the background on wrapped continuation lines.
		previewLines := wordWrap(preview, textW)

		var rowLineSty, metaSty lipgloss.Style
		if isSelected {
			rowLineSty = lipgloss.NewStyle().Width(colW).Background(selectedBg).Bold(true)
			metaSty = lipgloss.NewStyle().Width(colW).Background(selectedBg).Foreground(lipgloss.Color("245"))
		} else {
			rowLineSty = lipgloss.NewStyle().Width(colW)
			metaSty = lipgloss.NewStyle().Width(colW).Foreground(lipgloss.Color("245"))
		}

		var parts []string
		for j, pl := range previewLines {
			prefix := "  "
			if j == 0 {
				if isPending {
					prefix = "● "
				}
				if isSelected && isPending {
					// Apply pending colour directly in the style rather than via
					// an embedded ANSI string that would clobber the background.
					parts = append(parts, lipgloss.NewStyle().Width(colW).Background(selectedBg).Bold(true).
						Foreground(pendingFg).Render(prefix+pl))
					continue
				}
			}
			if j == 0 && isPending && !isSelected {
				parts = append(parts, rowLineSty.Render(pendingStyle.Render(prefix)+pl))
				continue
			}
			parts = append(parts, rowLineSty.Render(prefix+pl))
		}
		parts = append(parts, metaSty.Render("  "+meta))
		lines[i] = strings.Join(parts, "\n")
	}
	return strings.Join(lines, "\n")
}

// wordWrap splits s into lines of at most width runes, breaking at word boundaries.
func wordWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := ""
	for _, w := range words {
		switch {
		case current == "":
			current = w
		case len(current)+1+len(w) <= width:
			current += " " + w
		default:
			lines = append(lines, current)
			current = w
		}
	}
	return append(lines, current)
}

func (m *model) updateConv() {
	if len(m.items) == 0 || m.selected >= len(m.items) {
		m.conv.SetContent("")
		return
	}
	item := m.items[m.selected]
	var sb strings.Builder
	meta := fmt.Sprintf("[%s]  %s  %s", item.Channel, item.Status, item.ID)
	if item.Parent != "" {
		meta += "  parent: " + item.Parent
	}
	sb.WriteString(meta + "\n" + strings.Repeat("─", 60) + "\n\n")
	sb.WriteString(item.Body)
	for _, turn := range item.Turns {
		ts := turn.Timestamp.Format("2006-01-02 15:04")
		sb.WriteString(fmt.Sprintf("\n\n%s\n%s  ·  %s\n\n%s",
			strings.Repeat("─", 40), turn.Actor, ts, turn.Content))
	}
	m.conv.SetContent(sb.String())
}

// ── layout helpers ────────────────────────────────────────────────────────────

func (m model) listWidth() int {
	w := m.width * 30 / 100
	if w < 24 {
		w = 24
	}
	return w
}

func (m model) recalcLayout() model {
	const pad = 1
	headerH := 1
	footerH := 1
	inputH := 0
	if m.inputMode {
		inputH = 1
	}
	// Width(n) includes padding, so Padding(1) on the list panel leaves listW-2 for
	// content and makes the total rendered width listW+1 (content+padding+border).
	listPanelTotal := m.listWidth() + 1 // +1 for border-right
	convW := m.width - listPanelTotal - pad*2 // subtract conv's own 1-unit padding each side
	if convW < 1 {
		convW = 1
	}
	convH := m.height - headerH - footerH - inputH - pad*2
	if convH < 1 {
		convH = 1
	}
	m.conv = viewport.New(convW, convH)
	m.updateConv()
	return m
}

// ── selection helpers ─────────────────────────────────────────────────────────

func (m model) selectedID() string {
	if m.selected < len(m.items) {
		return m.items[m.selected].ID
	}
	return ""
}

func (m *model) restoreSelection(id string) {
	if id == "" {
		return
	}
	for i, item := range m.items {
		if item.ID == id {
			m.selected = i
			return
		}
	}
}

func (m model) pendingCount(ch models.Channel) int {
	if ch != m.channel {
		return 0 // only count for the currently loaded channel
	}
	n := 0
	for _, item := range m.items {
		if item.Status == models.StatusPendingUser {
			n++
		}
	}
	return n
}

// ── entry point ───────────────────────────────────────────────────────────────

func Run(s *store.Store) error {
	watchCh, err := startWatcher(s.Root)
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	p := tea.NewProgram(newModel(s, watchCh), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
