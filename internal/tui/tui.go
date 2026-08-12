package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unsafe"

	"ostraka/internal/models"
	"ostraka/internal/store"
	"ostraka/internal/supervisor"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
)

// ── messages ─────────────────────────────────────────────────────────────────

// itemsLoadedMsg carries the rows for a view plus the count it is suppressing,
// so the list can report the hidden ones instead of dropping them silently.
type itemsLoadedMsg struct {
	items         []models.Item
	hiddenBacklog int
}
type watchEventMsg struct{}
type errMsg error

// ── styles ───────────────────────────────────────────────────────────────────

var (
	headerBg   = lipgloss.Color("18")
	footerBg   = lipgloss.Color("235")
	selectedBg = lipgloss.Color("237")
	pendingFg  = lipgloss.Color("11")
	workingFg  = lipgloss.Color("10")
	// 245, not 8. ANSI 8 ("bright black") is unreadably dark on some terminal
	// themes — it is the colour that made both the live pane and the hidden-
	// backlog marker invisible. 245 is the grey the item meta lines already
	// use, so it is known to be legible here.
	dimFg = lipgloss.Color("245")

	headerStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("12")).
			Background(headerBg)
	headerTabActiveStyle = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(headerBg)
	headerTabStyle = lipgloss.NewStyle().
			Foreground(dimFg).
			Background(headerBg)
	footerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(footerBg)
	selectedStyle    = lipgloss.NewStyle().Background(selectedBg).Bold(true)
	dimStyle         = lipgloss.NewStyle().Foreground(dimFg)
	scrollTrackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	scrollThumbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	newBelowStyle    = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("16")).
				Background(pendingFg)
	liveHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(workingFg)
	popupStyle      = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(0, 1)
	borderStyle = lipgloss.NewStyle().BorderRight(true).BorderStyle(lipgloss.NormalBorder())
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

// loadItemsCmd loads the items for a view in a bubbletea goroutine. It reads
// the whole store and filters in memory: a view is a question about status as
// well as channel, and the store's one-status filter cannot express "every
// live status" or "either terminal status".
func loadItemsCmd(s *store.Store, v listView, showBacklog bool) tea.Cmd {
	return func() tea.Msg {
		items, err := s.ListItems(store.ListOpts{})
		if err != nil {
			return errMsg(err)
		}
		shown, hidden := v.prepare(items, showBacklog)
		return itemsLoadedMsg{items: shown, hiddenBacklog: hidden}
	}
}

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	store   *store.Store
	watchCh <-chan struct{}
	sup     *supervisor.Supervisor

	view  listView
	views []listView
	// showBacklog reveals parked items in the channel views. Off by default:
	// the list is meant to be what still needs someone.
	showBacklog bool
	// hiddenBacklog is how many items the current view is suppressing.
	hiddenBacklog int
	items         []models.Item
	selected      int

	conv  viewport.Model
	input textarea.Model
	title textinput.Model
	mode  uiMode

	// draft marks an unsaved new item occupying a synthetic last row of the
	// list while its title is typed. selected points one past the real items
	// for its duration, which the existing range guards already handle.
	draft bool
	// statusIdx is the cursor into allStatuses while the selector is open.
	statusIdx int
	// sessionIdx picks a fresh harness session in the deliberately small v0
	// session menu. Full session history and switching comes later.
	sessionIdx int

	// convTurns is the turn count of the item currently rendered into conv,
	// so a reload can tell "new turn arrived" from "same item, redrawn".
	convTurns int
	// convLive is the length of the live progress block currently rendered,
	// so a reload can tell a growing in-flight run from a static redraw.
	convLive int
	// convItemID is the item the pane is currently rendering. A reload that
	// changes it is a move to a different conversation, not an update to the
	// one being read.
	convItemID string
	// newBelow marks that a turn landed off-screen below the reader, who was
	// scrolled up at the time and so was not auto-followed down to it.
	newBelow bool

	width  int
	height int
	err    error
}

const (
	inputMinHeight = 2
	inputMaxHeight = 8
)

// uiMode is which widget owns the keyboard. Everything except modeNav is a
// transient editing state entered from, and returning to, modeNav.
type uiMode int

const (
	modeNav uiMode = iota
	modeCompose
	modeTitle
	modeStatus
	modeSession
)

// allStatuses is the selector's running order, coarsest lifecycle first.
var allStatuses = []models.Status{
	models.StatusBacklog,
	models.StatusActive,
	models.StatusPendingUser,
	models.StatusPendingAgent,
	models.StatusAgentAcknowledged,
	models.StatusDone,
	models.StatusArchived,
}

var sessionProviders = []supervisor.Provider{
	supervisor.ProviderClaude,
	supervisor.ProviderCodex,
}

// dispatchable reports whether submitting a turn on an item in this status
// should wake the agent. Backlog deliberately does not: it is where an item
// is parked precisely so the agent does not see it yet. Terminal statuses do
// not either — there is no one left to answer.
func dispatchable(s models.Status) bool {
	switch s {
	case models.StatusActive, models.StatusPendingUser, models.StatusPendingAgent,
		models.StatusAgentAcknowledged:
		return true
	}
	return false
}

func newModel(s *store.Store, watchCh <-chan struct{}, sup *supervisor.Supervisor) model {
	ta := textarea.New()
	ta.Placeholder = "Add turn… (ctrl+s to submit, esc to cancel)"
	ta.ShowLineNumbers = false
	ta.Placeholder = "" // first char of placeholder text renders as cursor char when empty
	ta.Prompt = ""      // remove default "┃ " prompt
	ta.SetWidth(40)     // recalculate promptWidth=0 (will be overridden in recalcLayout)
	ta.SetHeight(inputMinHeight)
	// Inline(true) is hardcoded in computedCursorLine(); clear the background so
	// the cursor line doesn't show a 1-char-wide highlight on an empty textarea.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "new item title…"
	// textinput's default placeholder colour (240) is almost indistinguishable
	// from selectedBg (237), making the draft hint look blank except under the
	// cursor. Use the same legible grey as item metadata instead.
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(dimFg)
	return model{
		store:   s,
		watchCh: watchCh,
		sup:     sup,
		// Opens on inbox: it is the channel with the work in it. Asks is where
		// the agent puts questions, so it is empty until there is one.
		view: channelView(models.ChannelInbox),
		views: []listView{
			channelView(models.ChannelInbox),
			channelView(models.ChannelAsks),
			channelView(models.ChannelHandoff),
			archiveView,
		},
		input: ta,
		title: ti,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		loadItemsCmd(m.store, m.view, m.showBacklog),
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
		prevRendered := m.convItemID
		prevTurns := m.convTurns
		prevLive := m.convLive
		// Sample before SetContent: appending lines can change the answer.
		wasAtBottom := m.conv.AtBottom()

		m.items = msg.items
		m.hiddenBacklog = msg.hiddenBacklog
		m.restoreSelection(prevID)
		m.updateConv()

		// A load that brings a different conversation into the pane parks it
		// at the newest content, the same as moving there by hand. This is the
		// asynchronous half of showSelected: switching channel empties the list
		// and the replacement arrives here, a frame later.
		if m.convItemID != "" && m.convItemID != prevRendered {
			m.conv.GotoBottom()
			m.newBelow = false
			return m, nil
		}

		// Only a genuinely new turn on the item already being read counts.
		// A first load, or a reload that landed on a different item, has no
		// "before" to compare against.
		sameItem := !m.draft && prevID != "" && m.selectedID() == prevID
		if sameItem && m.convTurns > prevTurns {
			if wasAtBottom {
				m.conv.GotoBottom()
			} else {
				m.newBelow = true
			}
		} else if sameItem && m.convLive > prevLive && wasAtBottom {
			// Live progress follows the same way, but never raises the
			// "new messages below" bar: a run emitting a line a second would
			// leave it permanently lit and stop meaning anything.
			m.conv.GotoBottom()
		}
		return m, nil

	case watchEventMsg:
		// Re-arm the watcher and reload current channel.
		return m, tea.Batch(waitForWatch(m.watchCh), loadItemsCmd(m.store, m.view, m.showBacklog))

	case errMsg:
		m.err = msg
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeCompose:
			return m.handleInputKey(msg)
		case modeTitle:
			return m.handleTitleKey(msg)
		case modeStatus:
			return m.handleStatusKey(msg)
		case modeSession:
			return m.handleSessionKey(msg)
		}
		return m.handleNavKey(msg)
	}

	// Pass other messages to sub-components.
	switch m.mode {
	case modeCompose:
		prevLines := m.currentInputHeight()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		if m.currentInputHeight() != prevLines {
			m = m.recalcLayout()
		}
	case modeTitle:
		var cmd tea.Cmd
		m.title, cmd = m.title.Update(msg)
		cmds = append(cmds, cmd)
	default:
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
			m.showSelected()
		}
	case "k", "up":
		if m.selected > 0 {
			m.selected--
			m.showSelected()
		}
	case "pgdown":
		m.conv.PageDown()
		m.syncNewBelow()
	case "pgup":
		m.conv.PageUp()
		m.syncNewBelow()
	case "1":
		return m.switchView(channelView(models.ChannelInbox))
	case "2":
		return m.switchView(channelView(models.ChannelAsks))
	case "3":
		return m.switchView(channelView(models.ChannelHandoff))
	case "4":
		return m.switchView(archiveView)
	case "b":
		// Backlog is hidden by default, so this is also the only way back to an
		// item parked there — it must stay reachable, not just tidy.
		m.showBacklog = !m.showBacklog
		return m, loadItemsCmd(m.store, m.view, m.showBacklog)
	case "r":
		return m, loadItemsCmd(m.store, m.view, m.showBacklog)
	case "t":
		if len(m.items) > 0 {
			return m.openComposer()
		}
	case "a":
		// The archive is a lifecycle state, not a channel, so there is nothing
		// for a new item to be created *in*. Refuse rather than write an item
		// with an empty channel, which has no directory to live in.
		if m.view.archive {
			return m, nil
		}
		// A synthetic row past the end of items; selected follows it there so
		// the conversation pane clears to the draft hint.
		m.draft = true
		m.selected = len(m.items)
		m.mode = modeTitle
		m.title.Reset()
		m.title.Width = m.titleWidth()
		m.updateConv()
		return m, m.title.Focus()
	case "s":
		if m.selected < len(m.items) {
			m.mode = modeStatus
			m.statusIdx = statusIndex(m.items[m.selected].Status)
		}
	case "S":
		m.mode = modeSession
		provider, _ := m.sup.Session()
		m.sessionIdx = sessionProviderIndex(provider)
	}
	return m, nil
}

func sessionProviderIndex(provider supervisor.Provider) int {
	for i, p := range sessionProviders {
		if p == provider {
			return i
		}
	}
	return 0
}

// openComposer focuses the turn textarea for the selected item.
func (m model) openComposer() (tea.Model, tea.Cmd) {
	m.mode = modeCompose
	m.input.Reset()
	m = m.recalcLayout()
	return m, m.input.Focus()
}

func statusIndex(s models.Status) int {
	for i, c := range allStatuses {
		if c == s {
			return i
		}
	}
	return 0
}

// handleTitleKey drives the inline title editor for a draft item. Enter
// commits the title — titles are single-line, so there is nothing else Enter
// could mean here — and moves on to the body, which is the other half of a
// mandatory pair. The item is not written until the body is submitted.
func (m model) handleTitleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m = m.cancelDraft()
		return m, nil
	case "enter":
		if store.ValidateTitle(m.title.Value()) != nil {
			return m, nil // refuse to advance rather than create a bad item
		}
		m.title.Blur()
		return m.openComposer()
	}
	var cmd tea.Cmd
	m.title, cmd = m.title.Update(msg)
	return m, cmd
}

// commitDraft writes the pending item from the title and the body just typed.
func (m model) commitDraft(body string) model {
	// New items start in backlog: created, but not yet the agent's problem.
	// Which the default filter hides — so creating one reveals backlog, or the
	// item you just wrote would disappear the moment you finished it.
	m.showBacklog = true
	item, err := m.store.CreateItem(m.view.channel, m.title.Value(), body, models.TypeThread, models.StatusBacklog, "")
	if err != nil {
		m.err = err
		return m.cancelDraft()
	}
	m.draft = false
	// Reload synchronously so the new item is selectable in this same frame
	// rather than after a round trip through loadItemsCmd.
	m.reload()
	m.restoreSelection(item.ID)
	m.updateConv()
	return m
}

func (m model) cancelDraft() model {
	m.draft = false
	m.mode = modeNav
	m.title.Blur()
	if m.selected >= len(m.items) {
		m.selected = max(0, len(m.items)-1)
	}
	m.updateConv()
	return m
}

// handleStatusKey drives the status selector popup.
func (m model) handleStatusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNav
	case "j", "down":
		if m.statusIdx < len(allStatuses)-1 {
			m.statusIdx++
		}
	case "k", "up":
		if m.statusIdx > 0 {
			m.statusIdx--
		}
	case "enter":
		if m.selected < len(m.items) {
			item := m.items[m.selected]
			status := allStatuses[m.statusIdx]
			// Moving an item the agent already owes a reply on into a working
			// status is itself the "go" signal — otherwise you have to set
			// active and then post a turn you have nothing to say in.
			if wakesAgent(status) && awaitingAgent(item) {
				status = models.StatusPendingAgent
				m.store.SetStatus(item.ID, status) //nolint:errcheck
				m.sup.Enqueue(item.ID)
			} else {
				m.store.SetStatus(item.ID, status) //nolint:errcheck
			}
		}
		m.mode = modeNav
		return m, loadItemsCmd(m.store, m.view, m.showBacklog)
	}
	return m, nil
}

// handleSessionKey exposes only the cheap operation we need today: begin a
// fresh Claude or Codex context. The persisted provider travels with the empty
// cursor, so the next dispatch cannot resume a session from the other CLI.
func (m model) handleSessionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNav
	case "j", "down":
		if m.sessionIdx < len(sessionProviders)-1 {
			m.sessionIdx++
		}
	case "k", "up":
		if m.sessionIdx > 0 {
			m.sessionIdx--
		}
	case "enter":
		if err := m.sup.StartNewSession(sessionProviders[m.sessionIdx]); err != nil {
			m.err = err
		}
		m.mode = modeNav
	}
	return m, nil
}

// statusDot gives the colour of an item's list marker, and whether it has one
// at all. Only the two statuses that mean "something is happening" are marked:
// a dot on every row would carry no information.
func statusDot(s models.Status) (lipgloss.Color, bool) {
	switch s {
	case models.StatusPendingUser:
		return pendingFg, true // your turn
	case models.StatusAgentAcknowledged:
		return workingFg, true // agent is on it
	}
	return "", false
}

// wakesAgent reports whether moving an item *into* this status is a request
// for the agent to pick it up. Narrower than dispatchable: choosing
// pending-user is a deliberate hand-off in the other direction, so it parks
// the item however recently the user wrote.
func wakesAgent(s models.Status) bool {
	return s == models.StatusActive || s == models.StatusPendingAgent
}

// awaitingAgent reports whether the last word on an item is the user's, and so
// whether there is anything for a dispatch to respond to. An item with no
// turns counts: its body is the user's opening statement.
func awaitingAgent(item models.Item) bool {
	if len(item.Turns) == 0 {
		return true
	}
	return item.Turns[len(item.Turns)-1].Actor == models.ActorUser
}

// reload refreshes items in place. The async loadItemsCmd is still the normal
// path; this exists for the few spots that must see the new list immediately.
func (m *model) reload() {
	items, err := m.store.ListItems(store.ListOpts{})
	if err != nil {
		m.err = err
		return
	}
	m.items, m.hiddenBacklog = m.view.prepare(items, m.showBacklog)
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		content := strings.TrimSpace(m.input.Value())
		if m.draft {
			// The body is mandatory, so an empty one leaves the draft open
			// rather than writing a half-item.
			if content == "" {
				return m, nil
			}
			m = m.commitDraft(content)
			m.mode = modeNav
			m.input.Blur()
			m = m.recalcLayout()
			return m, loadItemsCmd(m.store, m.view, m.showBacklog)
		}
		if content != "" && m.selected < len(m.items) {
			item := m.items[m.selected]
			m.store.AddTurn(item.ID, models.ActorUser, content) //nolint:errcheck
			// Whether a turn wakes the agent is a property of the item's
			// status, not of the keystroke: advancing to pending-agent and
			// enqueueing are the same decision, so they move together. A
			// backlog item stays silent — see dispatchable.
			if dispatchable(item.Status) {
				m.store.SetStatus(item.ID, models.StatusPendingAgent) //nolint:errcheck
				m.sup.Enqueue(item.ID)
			}
		}
		m.mode = modeNav
		m.input.Blur()
		m = m.recalcLayout()
		// Your own turn is never "new messages below" — go to the bottom now so
		// the reload that follows sees AtBottom and auto-follows onto it.
		m.conv.GotoBottom()
		return m, loadItemsCmd(m.store, m.view, m.showBacklog)
	case "esc":
		m.input.Blur()
		if m.draft {
			// Abandoning the body abandons the whole unwritten item.
			m = m.cancelDraft()
		}
		m.mode = modeNav
		m = m.recalcLayout()
		return m, nil
	case "pgdown":
		// The textarea binds neither page key, so they stay available for
		// scrolling the conversation while composing a reply to it.
		m.conv.PageDown()
		m.syncNewBelow()
		return m, nil
	case "pgup":
		m.conv.PageUp()
		m.syncNewBelow()
		return m, nil
	}
	prevH := m.currentInputHeight()
	visualLines := m.inputVisualLineCount()
	li := m.input.LineInfo()
	cursorLine := m.input.Line()
	atLineStart := li.RowOffset == 0 && li.ColumnOffset == 0

	// Pre-adjust height BEFORE the textarea processes the keystroke so its
	// internal scroll logic sees the new viewport size and the cursor stays at
	// the same screen row.
	//   Enter     → pre-expand  (below max height; at max height textarea scrolls naturally)
	//   Backspace → pre-shrink  (below max height only; at max height handled post-update)
	switch msg.String() {
	case "enter":
		if visualLines >= prevH && prevH < inputMaxHeight {
			m = m.adjustInputHeight(prevH + 1)
		}
	case "backspace":
		if atLineStart && cursorLine > 0 && visualLines > inputMinHeight && visualLines == prevH && prevH < inputMaxHeight {
			m = m.adjustInputHeight(prevH - 1)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	newVisualLines := m.inputVisualLineCount()

	newH := m.currentInputHeight()
	if newH != prevH {
		m = m.recalcLayout()
		if newH > prevH {
			// A soft wrap is processed while the textarea still has its old
			// height, so it scrolls down to keep the cursor visible. Growing the
			// viewport afterwards must reclaim those newly visible rows or the
			// first wrapped line remains needlessly hidden.
			textareaScrollUp(&m.input, newH-prevH)
		}
	} else if newVisualLines < visualLines && prevH == inputMaxHeight {
		// A line was deleted while at max height: the textarea's repositionView
		// won't scroll up (cursor remains within the visible range), leaving an
		// empty row at the bottom. Decrement the internal viewport YOffset directly.
		textareaScrollUp(&m.input, 1)
	}
	return m, cmd
}

func (m model) switchView(v listView) (model, tea.Cmd) {
	m.view = v
	m.selected = 0
	m.items = nil
	m.showSelected()
	return m, loadItemsCmd(m.store, v, m.showBacklog)
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
	convAreaW := m.width - (m.listWidth() + 1)
	var convPanel string
	if m.mode == modeCompose {
		// Per-element padding so the separator spans the full column width,
		// giving │──────── instead of │ ──────── at the corner.
		viewportBlock := lipgloss.NewStyle().Padding(1, 1, 0, 1).Render(m.renderConv())
		sep := strings.Repeat("─", convAreaW)

		// Render textarea first (its View() updates the shared viewport via the
		// internal *viewport.Model pointer), then read the live TotalLineCount.
		taView := m.input.View()
		tvp := textareaViewport(&m.input)
		scrollbar := renderScrollbar(m.currentInputHeight(), tvp.TotalLineCount(), tvp.YOffset)
		// Scrollbar occupies the 1-char right padding slot; overall width = convAreaW.
		inputBlock := lipgloss.NewStyle().Padding(0, 0, 1, 1).Render(
			lipgloss.JoinHorizontal(lipgloss.Top, taView, scrollbar),
		)
		convPanel = lipgloss.JoinVertical(lipgloss.Left, viewportBlock, sep, inputBlock)
	} else {
		convPanel = lipgloss.NewStyle().Padding(1).Render(m.renderConv())
	}
	mainRow := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, convPanel)

	return lipgloss.JoinVertical(lipgloss.Left, header, mainRow, footer)
}

// renderConv renders the conversation viewport, overlaying a "new messages
// below" bar on its final row when a turn has landed out of sight. The bar
// replaces the last line rather than being appended, so the pane keeps its
// exact height and nothing else in the layout shifts.
func (m model) renderConv() string {
	view := m.conv.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		return view
	}
	if m.newBelow {
		lines[len(lines)-1] = newBelowStyle.Width(m.conv.Width).Align(lipgloss.Center).
			Render("↓ new messages below ↓")
	}
	if m.mode == modeStatus {
		lines = m.overlayStatusPopup(lines)
	} else if m.mode == modeSession {
		lines = m.overlaySessionPopup(lines)
	}
	return strings.Join(lines, "\n")
}

func (m model) overlaySessionPopup(lines []string) []string {
	current, id := m.sup.Session()
	rows := make([]string, len(sessionProviders))
	for i, provider := range sessionProviders {
		marker, sty := "  ", lipgloss.NewStyle()
		if i == m.sessionIdx {
			marker, sty = "› ", lipgloss.NewStyle().Bold(true).Foreground(pendingFg)
		}
		rows[i] = sty.Render(marker + "new " + string(provider) + " session")
	}
	active := string(current)
	if id == "" {
		active += " (none)"
	}
	box := popupStyle.Render("agent session · current " + active + "\n" + strings.Join(rows, "\n"))
	pad := lipgloss.NewStyle().Width(m.conv.Width)
	for i, boxLine := range strings.Split(box, "\n") {
		row := 1 + i
		if row >= len(lines) {
			break
		}
		lines[row] = pad.Render(boxLine)
	}
	return lines
}

// overlayStatusPopup draws the status selector over the top rows of the
// conversation. Each popup row replaces a whole line rather than being spliced
// into one — the underlying content is styled, and cutting a line mid-way
// would mean slicing ANSI sequences. Whole-row replacement is the cheap
// version, and it still reads as a floating box.
func (m model) overlayStatusPopup(lines []string) []string {
	rows := make([]string, len(allStatuses))
	for i, s := range allStatuses {
		marker, sty := "  ", lipgloss.NewStyle()
		if i == m.statusIdx {
			marker, sty = "› ", lipgloss.NewStyle().Bold(true).Foreground(pendingFg)
		}
		rows[i] = sty.Render(marker + string(s))
	}
	box := popupStyle.Render("set status\n" + strings.Join(rows, "\n"))

	const topRow = 1 // one row of breathing space above the popup
	pad := lipgloss.NewStyle().Width(m.conv.Width)
	for i, boxLine := range strings.Split(box, "\n") {
		row := topRow + i
		if row >= len(lines) {
			break
		}
		lines[row] = pad.Render(boxLine)
	}
	return lines
}

func (m model) renderHeader() string {
	tabs := make([]string, len(m.views))
	for i, v := range m.views {
		label := v.label()
		n := m.pendingCount(v)
		if n > 0 {
			label = fmt.Sprintf("%s (%d)", label, n)
		}
		if v == m.view {
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
	var text string
	switch m.mode {
	case modeCompose:
		text = "ctrl+s submit  esc cancel  pgup/pgdn scroll"
		if m.draft {
			text = "ctrl+s create item  esc discard draft"
		} else if m.selected < len(m.items) && !dispatchable(m.items[m.selected].Status) {
			// Silent submit is the surprising case, so name it rather than
			// leaving the reader to discover the agent never woke up.
			text = "ctrl+s save (no dispatch — backlog)  esc cancel  pgup/pgdn scroll"
		}
	case modeTitle:
		text = "enter next (body)  esc cancel"
	case modeStatus:
		text = "j/k select  enter apply  esc cancel"
	case modeSession:
		text = "j/k select  enter start fresh session  esc cancel"
	default:
		text = "q quit  j/k nav  a add  s status  S session  t turn  1-4 view  b backlog  pgup/pgdn scroll  r refresh"
		if m.showBacklog {
			text = "q quit  j/k nav  a add  s status  S session  t turn  1-4 view  b hide backlog  pgup/pgdn scroll  r refresh"
		}
	}

	// Right-aligned scroll position, shown only when the conversation actually
	// overflows — otherwise there is nothing to tell the reader.
	var right string
	if m.conv.TotalLineCount() > m.conv.Height {
		switch {
		case m.conv.AtTop():
			right = "top "
		case m.conv.AtBottom():
			right = "bot "
		default:
			right = fmt.Sprintf("%3.0f%% ", m.conv.ScrollPercent()*100)
		}
	}

	// The hint text grows with the keymap; drop it rather than overflow the row.
	if len(text)+len(right) > m.width {
		text = ""
	}
	if pad := m.width - len(text) - len(right); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return footerStyle.Render(text + right)
}

func (m model) renderList() string {
	marker := hiddenBacklogLabel(m.hiddenBacklog)
	if len(m.items) == 0 && !m.draft {
		// A view holding nothing but suppressed rows is not empty, and saying
		// so would be a lie the toggle can't be discovered from.
		if marker != "" {
			return dimStyle.Render(hiddenBacklogRow(marker, m.listWidth()-2))
		}
		return dimStyle.Render("(empty)")
	}
	colW := m.listWidth() - 2 // panel uses Width(listW) with Padding(1), so content = listW-2

	const badgeW = 2
	textW := colW - badgeW

	lines := make([]string, len(m.items))
	for i, item := range m.items {
		isSelected := i == m.selected
		dotFg, hasDot := statusDot(item.Status)

		preview := item.Title
		meta := fmt.Sprintf("%s [%s]", item.ID, item.Status)

		// Word-wrap the preview manually so we control each line's prefix and
		// background independently — JoinHorizontal pads shorter columns with
		// unstyled spaces, losing the background on wrapped continuation lines.
		previewLines := truncateLines(wordWrap(preview, textW), previewMaxLines, textW)

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
			if j == 0 && hasDot {
				prefix = "● "
				if isSelected {
					// Apply the dot colour directly in the style rather than via
					// an embedded ANSI string that would clobber the background.
					parts = append(parts, lipgloss.NewStyle().Width(colW).Background(selectedBg).Bold(true).
						Foreground(dotFg).Render(prefix+pl))
				} else {
					parts = append(parts, rowLineSty.Render(
						lipgloss.NewStyle().Foreground(dotFg).Render(prefix)+pl))
				}
				continue
			}
			parts = append(parts, rowLineSty.Render(prefix+pl))
		}
		parts = append(parts, metaSty.Render("  "+meta))
		lines[i] = strings.Join(parts, "\n")
	}

	// The draft is a synthetic row: it has no file behind it yet, so it is
	// rendered from the title input rather than from an item.
	if m.draft {
		rowSty := lipgloss.NewStyle().Background(selectedBg).Bold(true)
		metaSty := lipgloss.NewStyle().Width(colW).Background(selectedBg).Foreground(lipgloss.Color("245"))
		m.title.Width = m.titleWidth()
		lines = append(lines,
			renderDraftTitle(rowSty, colW, m.title.View())+"\n"+metaSty.Render("  new item [backlog]"))
	}
	// Sits below the rows and is not selectable: selection indexes m.items,
	// which this is deliberately not part of.
	if marker != "" {
		lines = append(lines, dimStyle.Render(hiddenBacklogRow(marker, colW)))
	}
	return strings.Join(lines, "\n")
}

// renderDraftTitle paints the part of the draft row after textinput explicitly.
// textinput's cursor renderer closes its ANSI style when the cursor is visible;
// relying on a parent style with Width then left the trailing cells unpainted
// until the cursor blinked off.
func renderDraftTitle(style lipgloss.Style, width int, title string) string {
	prefix := "› "
	pad := max(0, width-lipgloss.Width(prefix+title))
	// textinput emits a reset after its cursor. Render each text segment in its
	// own selected style so that reset cannot erase the row background for the
	// rest of the input or its trailing fill.
	return renderStyledANSI(style, prefix+title) + style.Render(strings.Repeat(" ", pad))
}

func renderStyledANSI(style lipgloss.Style, text string) string {
	const reset = "\x1b[0m"
	parts := strings.Split(text, reset)
	for i, part := range parts {
		parts[i] = style.Render(part)
	}
	return strings.Join(parts, reset)
}

// previewMaxLines caps how much of an item's body the list will show. Bodies
// are only single-line by convention — the TUI's add flow enforces it, the
// CLI does not — so one long-bodied item could otherwise crowd out every
// other row in the list.
const previewMaxLines = 2

// truncateLines caps lines at n, marking the cut with an ellipsis so a
// shortened preview is visibly shortened rather than silently wrong. The
// ellipsis has to fit inside width, or the row it lands in overflows the
// column by exactly the character meant to signal the truncation. Callers
// pass lines that already fit width — wordWrap output, in practice — so only
// the line the ellipsis lands on needs re-trimming.
func truncateLines(lines []string, n, width int) []string {
	if len(lines) <= n || n <= 0 {
		return lines
	}
	out := append([]string(nil), lines[:n]...)
	r := []rune(out[n-1])
	if width > 0 && len(r) > width-1 {
		r = r[:max(0, width-1)]
	}
	out[n-1] = strings.TrimRight(string(r), " ") + "…"
	return out
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

// wrapText wraps s to width columns. The viewport splits content on "\n" and
// truncates anything wider, so wrapping has to happen before SetContent —
// there is no wrapping mode to switch on.
//
// Unlike wordWrap above, this preserves blank lines and each line's leading
// indentation, because turn bodies carry markdown: indented code blocks,
// quotes and list continuations all lose their shape if whitespace is
// collapsed.
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapLine(line string, width int) []string {
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	// A deep indent would otherwise squeeze the text column to nothing; past
	// half the pane it is worth more to keep the words readable.
	if lipgloss.Width(indent) > width/2 {
		indent = ""
	}
	avail := max(1, width-lipgloss.Width(indent))

	var lines []string
	cur := ""
	flush := func() {
		lines = append(lines, indent+cur)
		cur = ""
	}
	for _, w := range strings.Fields(line) {
		// An unbreakable token (URL, long path) has to be cut, or it runs off
		// the edge exactly as before.
		for lipgloss.Width(w) > avail {
			if cur != "" {
				flush()
			}
			r := []rune(w)
			lines = append(lines, indent+string(r[:avail]))
			w = string(r[avail:])
		}
		switch {
		case cur == "":
			cur = w
		case lipgloss.Width(cur)+1+lipgloss.Width(w) <= avail:
			cur += " " + w
		default:
			flush()
			cur = w
		}
	}
	if cur != "" {
		flush()
	}
	if len(lines) == 0 {
		return []string{line}
	}
	return lines
}

// showSelected renders the current selection and parks the pane at its newest
// content. Opening an item at the top means scrolling past the entire history
// to reach the part that changed, which is almost never what the reader wants.
func (m *model) showSelected() {
	m.updateConv()
	m.conv.GotoBottom()
	m.newBelow = false
}

// syncNewBelow retires the "new messages below" marker once the reader has
// actually reached the bottom, which is the only thing that makes it stale.
func (m *model) syncNewBelow() {
	if m.conv.AtBottom() {
		m.newBelow = false
	}
}

func (m *model) updateConv() {
	if len(m.items) == 0 || m.selected >= len(m.items) {
		if m.draft {
			m.conv.SetContent(wrapText(
				"New item.\n\n"+
					"1. Title — one line, typed in the list. Enter moves on.\n"+
					"2. Body — the opening description, any length. ctrl+s creates the item.\n\n"+
					"Both are required. It starts in backlog, so it won't wake the agent; "+
					"press s afterwards to move it to active if you want it dispatched.",
				m.conv.Width))
		} else {
			m.conv.SetContent("")
		}
		m.convTurns = 0
		m.convLive = 0
		m.convItemID = ""
		return
	}
	item := m.items[m.selected]
	m.convItemID = item.ID
	m.convTurns = len(item.Turns)
	w := m.conv.Width

	// Rules are drawn to the pane, not to fixed 60/40 — a fixed rule in a
	// narrow pane is just another line that overflows.
	headRule := strings.Repeat("─", clampRule(w, 60))
	turnRule := strings.Repeat("─", clampRule(w, 40))

	var sb strings.Builder
	meta := fmt.Sprintf("[%s]  %s  %s", item.Channel, item.Status, item.ID)
	if item.Parent != "" {
		meta += "  parent: " + item.Parent
	}
	sb.WriteString(wrapText(item.Title, w) + "\n")
	sb.WriteString(wrapText(meta, w) + "\n" + headRule + "\n\n")
	sb.WriteString(wrapText(item.Body, w))
	for _, turn := range item.Turns {
		ts := turn.Timestamp.Format("2006-01-02 15:04")
		sb.WriteString(fmt.Sprintf("\n\n%s\n%s  ·  %s\n\n%s",
			turnRule, turn.Actor, ts, wrapText(turn.Content, w)))
	}

	// Live progress from an in-flight dispatch, appended below the last real
	// turn. It is transient by construction: the supervisor deletes the log
	// when the run ends, and the turn the agent posts takes its place.
	live := ""
	if item.Status == models.StatusAgentAcknowledged {
		live = strings.TrimSpace(supervisor.ReadLive(m.store.Root, item.ID))
	}
	if live != "" {
		// Body deliberately unstyled — the default foreground is the one
		// colour guaranteed readable, since it is what every other line of
		// the conversation already uses. Dimming it made the live feed
		// invisible on darker terminals, and the green header already marks
		// the block as transient without tinting the text under it.
		sb.WriteString("\n\n" + turnRule + "\n" +
			liveHeaderStyle.Render("agent  ·  working") + "\n\n" +
			wrapText(live, w))
	}
	m.convLive = len(live)

	m.conv.SetContent(sb.String())
}

// clampRule returns the rule width: the preferred length, or the pane width
// when that is narrower. Width 0 (before the first WindowSizeMsg) keeps the
// preferred length rather than collapsing to nothing.
func clampRule(paneW, prefer int) int {
	if paneW > 0 && paneW < prefer {
		return paneW
	}
	return prefer
}

// ── layout helpers ────────────────────────────────────────────────────────────

// titleWidth is how wide the draft title editor may be.
//
// textinput renders Width+1 columns — it reserves a cell for the cursor past
// the end of the text — and the draft row prefixes two more for the badge
// column. Budgeting only for the prefix overflowed the row by exactly one
// column, so lipgloss wrapped every draft row onto two lines and the trailing
// cell flickered between them as the cursor blinked.
//
// The model's Width and the render-time Width must agree: textinput computes
// its horizontal scroll offset from Width during Update, so a mismatch would
// scroll against a different column count than the one being drawn.
func (m model) titleWidth() int {
	return max(1, m.listWidth()-5)
}

func (m model) listWidth() int {
	w := m.width * 30 / 100
	if w < 24 {
		w = 24
	}
	return w
}

func (m model) currentInputHeight() int {
	lines := m.inputVisualLineCount()
	if lines < inputMinHeight {
		lines = inputMinHeight
	}
	if lines > inputMaxHeight {
		lines = inputMaxHeight
	}
	return lines
}

// inputVisualLineCount asks textarea to populate its internal viewport, whose
// content is already soft-wrapped at the input's current width. Counting raw
// newlines here makes a long logical line look one row high until it contains
// an explicit Enter, which leaves the composer scrolling instead of growing.
func (m model) inputVisualLineCount() int {
	_ = m.input.View()
	// textarea.View appends Height() end-of-buffer rows so an empty input still
	// fills its viewport. Those are render padding, not content. Including them
	// here creates a feedback loop: increasing the composer makes the measured
	// content taller, which increases the composer again.
	// The rendered string terminates each row with a newline, and viewport splits
	// that final delimiter into one additional empty line.
	return max(1, textareaViewport(&m.input).TotalLineCount()-m.input.Height()-1)
}

// textareaViewport extracts the shared *viewport.Model from the textarea via
// reflect+unsafe. The field is a pointer, so modifications affect the live model
// even though textarea methods use value receivers.
func textareaViewport(ta *textarea.Model) *viewport.Model {
	v := reflect.ValueOf(ta).Elem()
	f := v.FieldByName("viewport")
	return *(**viewport.Model)(unsafe.Pointer(f.UnsafeAddr()))
}

func textareaScrollUp(ta *textarea.Model, n int) {
	vp := textareaViewport(ta)
	if off := vp.YOffset - n; off >= 0 {
		vp.YOffset = off
	} else {
		vp.YOffset = 0
	}
}

// renderScrollbar returns a visibleH-line string (one char wide) showing a
// proportional thumb. Returns spaces when all content is visible.
func renderScrollbar(visibleH, totalH, yOffset int) string {
	if totalH <= visibleH {
		return strings.Repeat(" \n", visibleH-1) + " "
	}
	thumbH := max(1, visibleH*visibleH/totalH)
	maxOff := totalH - visibleH
	thumbY := 0
	if maxOff > 0 {
		thumbY = yOffset * (visibleH - thumbH) / maxOff
	}
	var sb strings.Builder
	for i := 0; i < visibleH; i++ {
		if i >= thumbY && i < thumbY+thumbH {
			sb.WriteString(scrollThumbStyle.Render("▐"))
		} else {
			sb.WriteString(scrollTrackStyle.Render("│"))
		}
		if i < visibleH-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// adjustInputHeight sets the textarea height to h and adjusts the conv viewport
// height accordingly, without recomputing h from content. Used for pre-expand
// (Enter) and pre-shrink (backspace) so layout shifts before the keystroke lands.
func (m model) adjustInputHeight(h int) model {
	mainH := m.height - 2
	convH := mainH - h - 3
	if convH < 1 {
		convH = 1
	}
	m.input.SetHeight(h)
	m.updateConv()
	m.setConvHeight(convH)
	return m
}

// setConvHeight resizes the conversation viewport while keeping its *bottom*
// line pinned. The turn input grows upward from the bottom of the pane, so
// top-anchoring would slide the newest turns — the ones being replied to —
// out of view behind the input. Since keystrokes go to the textarea while
// composing, there is no way to scroll them back, so the shrink must not
// discard them. SetYOffset clamps, so this is safe at either extreme.
func (m *model) setConvHeight(h int) {
	delta := m.conv.Height - h
	m.conv.Height = h
	if delta != 0 {
		m.conv.SetYOffset(m.conv.YOffset + delta)
	}
}

func (m model) recalcLayout() model {
	// Width(n) includes padding, so Padding(1) on the list panel leaves listW-2 for
	// content and makes the total rendered width listW+1 (content+padding+border).
	listPanelTotal := m.listWidth() + 1 // +1 for border-right
	convAreaW := m.width - listPanelTotal
	convW := convAreaW - 2 // 1-unit padding each side
	if convW < 1 {
		convW = 1
	}
	// Set the input width first: its visual row count is width-dependent.
	// currentInputHeight then reads the textarea's wrapped viewport rather than
	// the number of physical newline-delimited lines.
	m.input.SetWidth(convW)
	inputH := m.currentInputHeight()

	mainH := m.height - 2 // subtract header and footer
	var convH int
	if m.mode == modeCompose {
		// per-element padding: 1(top) + convH + 1(sep) + inputH + 1(bottom) = mainH
		convH = mainH - inputH - 3
	} else {
		// Padding(1) all sides: 1 + convH + 1 = mainH
		convH = mainH - 2
	}
	if convH < 1 {
		convH = 1
	}
	m.title.Width = m.titleWidth()
	m.input.SetHeight(inputH)
	m.conv.Width = convW
	m.updateConv()
	m.setConvHeight(convH)
	return m
}

// ── selection helpers ─────────────────────────────────────────────────────────

func (m model) selectedID() string {
	if m.selected < len(m.items) {
		return m.items[m.selected].ID
	}
	return ""
}

// restoreSelection puts the cursor back on the item it was on, wherever that
// item has moved to — a status change re-sorts the list, so the row index is
// not stable across a reload but the id is.
//
// When the item is no longer in the view at all — archived, or parked in
// backlog with the filter on — the cursor holds its row instead, landing on
// whatever moved up into it, and clamps to the last row if the list shrank
// past it. Without the clamp, selected could point beyond the end and the
// reading pane would go blank with no way to tell why.
func (m *model) restoreSelection(id string) {
	for i, item := range m.items {
		if item.ID == id {
			m.selected = i
			return
		}
	}
	// A draft deliberately parks selected one past the end; clamping here
	// would drop the cursor onto a real item and hide the draft.
	if !m.draft && m.selected >= len(m.items) {
		m.selected = max(0, len(m.items)-1)
	}
}

func (m model) pendingCount(v listView) int {
	if v != m.view {
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
	// Construct the supervisor first: it creates .ostraka/supervisor/, and the
	// watcher only picks up subdirectories that exist when it starts. Without
	// this ordering, a freshly initialised project would never see live
	// progress, because the directory it is written to went unwatched.
	sup := supervisor.New(s.Root)

	watchCh, err := startWatcher(s.Root)
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	sup.Start()
	p := tea.NewProgram(newModel(s, watchCh, sup), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
