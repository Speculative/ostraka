package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"ostraka/internal/models"
	"ostraka/internal/store"
	"ostraka/internal/supervisor"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsnotify/fsnotify"
)

// ── messages ─────────────────────────────────────────────────────────────────

// itemsLoadedMsg carries the rows for a view plus the count it is suppressing,
// so the list can report the hidden ones instead of dropping them silently.
type itemsLoadedMsg struct {
	items         []models.Item
	allItems      []models.Item
	hiddenBacklog int
	view          listView
	showBacklog   bool
}
type watchEventMsg struct{}
type errMsg error
type draftCheckpointMsg struct{ sequence int }
type draftSafetyMsg struct{}

// modelsLoadedMsg carries the result of fetching a provider's AvailableModels
// for the modeSessionModel popup. provider is included so a stale response
// (the user backed out and picked a different provider before this arrived)
// can be told apart from the one the popup is currently showing.
type modelsLoadedMsg struct {
	provider supervisor.Provider
	models   []supervisor.ModelOption
	err      error
}

// supervisorClient is the small part of the agent supervisor the TUI needs.
// Keeping the UI against this seam lets the program-level teatest suite drive
// real user flows without starting a provider process.
type supervisorClient interface {
	Enqueue(string)
	Session(string) (supervisor.Provider, string, string, string, time.Time)
	SessionIsStale(string) bool
	LastTurnInfo(string) (supervisor.TurnInfo, bool)
	PreferredModel(supervisor.Provider) string
	PreferredEffort(supervisor.Provider) string
	StartNewSession(string, supervisor.Provider, string, string) error
	AvailableModels(context.Context, supervisor.Provider) ([]supervisor.ModelOption, error)
	Busy() (string, bool)
}

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
	// The confirmation is the one popup that reports a consequence rather than
	// offering a choice, so it is drawn heavier than the selectors: a thick
	// border in the pending amber, and a blank line of padding so the warning
	// is not sitting against the frame.
	confirmStyle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(pendingFg).
			Padding(1, 3)
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
		return itemsLoadedMsg{
			items:         shown,
			allItems:      items,
			hiddenBacklog: hidden,
			view:          v,
			showBacklog:   showBacklog,
		}
	}
}

// modelDiscoveryTimeout bounds one AvailableModels call. Codex's is a
// subprocess round trip (spawn app-server, initialize, model/list); Claude's
// is in-process and instant. Either way the popup must not hang forever if a
// CLI never answers.
const modelDiscoveryTimeout = 15 * time.Second

// loadModelsCmd fetches provider's model list in a bubbletea goroutine, so
// spawning Codex's app-server subprocess cannot freeze the UI.
func loadModelsCmd(sup supervisorClient, provider supervisor.Provider) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		defer cancel()
		models, err := sup.AvailableModels(ctx, provider)
		return modelsLoadedMsg{provider: provider, models: models, err: err}
	}
}

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	store   *store.Store
	watchCh <-chan struct{}
	sup     supervisorClient

	view  listView
	views []listView
	// showBacklog reveals parked items in the channel views. At startup the
	// inbox includes them when all its rows fit; after that, b is an explicit
	// user choice that reloads preserve.
	showBacklog                  bool
	backlogVisibilityInitialized bool
	// hiddenBacklog is how many items the current view is suppressing.
	hiddenBacklog int
	items         []models.Item
	selected      int
	// listOffset is the index of the first row shown in the list panel. It
	// persists across renders so the panel stays put as selection moves
	// within the visible window, only scrolling once selection would
	// otherwise leave it. See ensureListOffsetVisible.
	listOffset int

	conv  viewport.Model
	input textarea.Model
	title textinput.Model
	mode  uiMode
	// draftItemID is the item whose body is currently being composed. It is
	// separate from selected so switching views cannot make a checkpoint land
	// on the wrong item.
	draftItemID   string
	draftSequence int

	// draft marks an unsaved new item occupying a synthetic last row of the
	// list while its title is typed. selected points one past the real items
	// for its duration, which the existing range guards already handle.
	draft bool
	// statusIdx is the cursor into allStatuses while the selector is open.
	statusIdx int
	// sessionIdx picks a fresh harness session in the deliberately small v0
	// session menu. Full session history and switching comes later.
	sessionIdx int
	// sessionProvider is the provider chosen in the modeSession step, carried
	// forward into modeSessionModel — the second, provider-specific step of
	// the same "S" flow.
	sessionProvider supervisor.Provider
	// sessionModelIdx, sessionModels, sessionModelsLoading, and
	// sessionModelsErr back the modeSessionModel popup: the list fetched from
	// sessionProvider's AvailableModels, which options, and any fetch error.
	sessionModelIdx      int
	sessionModels        []supervisor.ModelOption
	sessionModelsLoading bool
	sessionModelsErr     error
	// The third session-picker step uses the selected model's supported
	// efforts; no additional provider round trip is needed.
	sessionEffortIdx int
	sessionEfforts   []string

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
	// projectPane is 0 for item views, 1 for user instructions, and 2 for the
	// agent-curated brief. Project documents deliberately use the existing
	// reader/editor rather than becoming synthetic conversation items.
	projectPane    int
	editingProject bool

	width  int
	height int
	err    error
}

const (
	inputMinHeight = 2
	inputMaxHeight = 8
	draftDebounce  = 2 * time.Second
	draftSafety    = 10 * time.Second
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
	modeSessionModel
	modeSessionEffort
	modeQuit
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

func newModel(s *store.Store, watchCh <-chan struct{}, sup supervisorClient) model {
	ta := textarea.New()
	ta.Placeholder = "Add turn… (ctrl+s to submit, esc to cancel)"
	ta.ShowLineNumbers = false
	ta.Placeholder = "" // first char of placeholder text renders as cursor char when empty
	ta.Prompt = ""      // remove default "┃ " prompt
	ta.SetWidth(40)     // recalculate promptWidth=0 (will be overridden in recalcLayout)
	ta.SetHeight(inputMinHeight)
	// Match the word-navigation shortcuts terminals and editors commonly send.
	// Bubbles' textarea defaults to the Alt variants only.
	ta.KeyMap.WordBackward.SetKeys("alt+left", "ctrl+left", "alt+b")
	ta.KeyMap.WordForward.SetKeys("alt+right", "ctrl+right", "alt+f")
	ta.KeyMap.DeleteWordBackward.SetKeys("alt+backspace", "ctrl+w")
	// Ctrl+V invokes Bubbles' host-clipboard helper, which is unavailable in
	// sandboxed/container sessions. Terminal bracketed paste (for example
	// Ctrl+Shift+V) remains supported and does not need any clipboard utility.
	ta.KeyMap.Paste.SetEnabled(false)
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
	ti.KeyMap.DeleteWordBackward.SetKeys("alt+backspace", "ctrl+w")
	ti.KeyMap.Paste.SetEnabled(false)
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
		// Bubble Tea enables this by default, but requesting it explicitly is
		// important to the TUI contract: terminals then send a whole paste as
		// one KeyMsg marked Paste instead of dribbling its bytes through the
		// global key bindings.
		tea.EnableBracketedPaste,
		loadItemsCmd(m.store, m.view, m.showBacklog),
		waitForWatch(m.watchCh),
	)
}

// ── update ───────────────────────────────────────────────────────────────────

// Update dispatches msg and then reconciles listOffset against wherever
// selection and items ended up. Rows vary in height and the list panel's
// available space depends on layout, so this runs after every message
// rather than being threaded through each of update's many return points.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(model)
	nm.listOffset = nm.ensureListOffsetVisible()
	return nm, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.recalcLayout()
		// Init starts its asynchronous load before Bubble Tea tells us the
		// terminal size. Reload once dimensions are known so the initial inbox
		// can make its one-time backlog choice from the real list capacity.
		if !m.backlogVisibilityInitialized && m.view == channelView(models.ChannelInbox) {
			return m, loadItemsCmd(m.store, m.view, false)
		}
		return m, nil

	case itemsLoadedMsg:
		// Loads run asynchronously. A response from before a b toggle must not
		// replace the list after the toggle has requested the opposite filter;
		// otherwise the first keypress appears to do nothing until the next
		// response happens to arrive. The same guard prevents a former tab from
		// repainting the one currently being viewed.
		if msg.view != m.view || (m.backlogVisibilityInitialized && msg.showBacklog != m.showBacklog) {
			return m, nil
		}
		prevID := m.selectedID()
		prevRendered := m.convItemID
		prevTurns := m.convTurns
		prevLive := m.convLive
		// Sample before SetContent: appending lines can change the answer.
		wasAtBottom := m.conv.AtBottom()

		if !m.backlogVisibilityInitialized && msg.allItems != nil && m.view == channelView(models.ChannelInbox) && m.listBaseAvailRows() > 0 {
			m.showBacklog = m.initialBacklogFits(msg.allItems)
			m.backlogVisibilityInitialized = true
			m.items, m.hiddenBacklog = m.view.prepare(msg.allItems, m.showBacklog)
		} else {
			m.items = msg.items
			m.hiddenBacklog = msg.hiddenBacklog
		}
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

	case modelsLoadedMsg:
		// A stale response — the user backed out and reopened with a
		// different provider before this arrived — has nothing to update.
		if m.mode != modeSessionModel || msg.provider != m.sessionProvider {
			return m, nil
		}
		m.sessionModelsLoading = false
		m.sessionModels = msg.models
		m.sessionModelsErr = msg.err
		// Preselect the item's already-chosen model, if it's one of the
		// options this provider offers — reopening the picker must not look
		// like it forgot a prior selection. Only an item that never had one
		// (or is switching provider) falls back to the provider's own
		// default.
		m.sessionModelIdx = 0
		currentProvider, currentModel, _, _, _ := m.sup.Session(m.selectedID())
		if currentProvider != msg.provider {
			currentModel = m.sup.PreferredModel(msg.provider)
		}
		matched := false
		for i, opt := range msg.models {
			if opt.ID == currentModel {
				m.sessionModelIdx = i
				matched = true
				break
			}
		}
		if !matched {
			for i, opt := range msg.models {
				if opt.Default {
					m.sessionModelIdx = i
					break
				}
			}
		}
		return m, nil

	case draftCheckpointMsg:
		if m.mode == modeCompose && !m.draft && msg.sequence == m.draftSequence {
			m.checkpointTurnDraft()
		}
		return m, nil

	case draftSafetyMsg:
		if m.mode == modeCompose && !m.draft {
			m.checkpointTurnDraft()
			return m, draftSafetyCheckpoint()
		}
		return m, nil

	case tea.KeyMsg:
		// A bracketed paste is content, never a command. In particular, pasted
		// q, esc, or ctrl+s must not quit or submit the editor. Bubble Tea's
		// KeyMsg.String protects bindings by wrapping pasted text, but routing it
		// directly keeps that guarantee local to Ostraka as well.
		if msg.Paste {
			switch m.mode {
			case modeCompose:
				return m.handleInputKey(msg)
			case modeTitle:
				var cmd tea.Cmd
				m.title, cmd = m.title.Update(msg)
				return m, cmd
			}
			return m, nil
		}
		switch m.mode {
		case modeCompose:
			return m.handleInputKey(msg)
		case modeTitle:
			return m.handleTitleKey(msg)
		case modeStatus:
			return m.handleStatusKey(msg)
		case modeSession:
			return m.handleSessionKey(msg)
		case modeSessionModel:
			return m.handleSessionModelKey(msg)
		case modeSessionEffort:
			return m.handleSessionEffortKey(msg)
		case modeQuit:
			return m.handleQuitKey(msg)
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
	if m.projectPane != 0 {
		switch msg.String() {
		case "1", "2", "3", "4":
			m.projectPane = 0
		case "tab", "5":
			m.projectPane = 3 - m.projectPane
			m.showProjectContext()
			return m, nil
		case "e":
			m.editingProject = true
			m.mode = modeCompose
			m.input.Reset()
			if m.projectPane == 1 {
				v, _ := m.store.ProjectInstructions()
				m.input.SetValue(v)
			} else {
				v, _ := m.store.ProjectBrief()
				m.input.SetValue(v)
			}
			m.input.CursorEnd()
			m = m.recalcLayout()
			return m, m.input.Focus()
		}
	}
	switch msg.String() {
	case "q", "ctrl+c":
		// Quitting now kills the running turn rather than leaving it to die
		// whenever it next writes to a stdout nobody is reading, so the agent's
		// work is genuinely lost — worth one keystroke of confirmation.
		if _, busy := m.busyDispatch(); busy {
			m.mode = modeQuit
			return m, nil
		}
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
	case "5":
		m.projectPane = 1
		m.showProjectContext()
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
		if itemID := m.selectedID(); itemID != "" {
			m.mode = modeSession
			provider, _, _, _, _ := m.sup.Session(itemID)
			m.sessionIdx = sessionProviderIndex(provider)
		}
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
	m.draftItemID = m.selectedID()
	m.input.Reset()
	if content, err := m.store.LoadDraft(m.draftItemID); err != nil {
		m.err = err
	} else {
		m.input.SetValue(content)
		m.input.CursorEnd()
	}
	m = m.recalcLayout()
	return m, tea.Batch(m.input.Focus(), draftSafetyCheckpoint())
}

func draftSafetyCheckpoint() tea.Cmd {
	return tea.Tick(draftSafety, func(time.Time) tea.Msg { return draftSafetyMsg{} })
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

// handleSessionKey is step one of "S": pick a provider for a fresh context.
// Enter advances to modeSessionModel to pick that provider's model rather
// than starting the session immediately — the persisted provider and model
// travel together with the empty cursor, so the next dispatch cannot resume
// a session from the other CLI or launch with the wrong model.
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
		m.sessionProvider = sessionProviders[m.sessionIdx]
		m.mode = modeSessionModel
		m.sessionModelIdx = 0
		m.sessionModels = nil
		m.sessionModelsErr = nil
		m.sessionModelsLoading = true
		return m, loadModelsCmd(m.sup, m.sessionProvider)
	}
	return m, nil
}

// handleSessionModelKey is step two of "S": pick a model from
// sessionProvider's AvailableModels, then advance to effort selection.
// Esc returns to the provider list rather than all the way to modeNav — the
// user backed out of one step, not the whole flow.
func (m model) handleSessionModelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeSession
	case "j", "down":
		if m.sessionModelIdx < len(m.sessionModels)-1 {
			m.sessionModelIdx++
		}
	case "k", "up":
		if m.sessionModelIdx > 0 {
			m.sessionModelIdx--
		}
	case "enter":
		if m.sessionModelIdx < len(m.sessionModels) {
			opt := m.sessionModels[m.sessionModelIdx]
			m.sessionEfforts = append([]string(nil), opt.SupportedReasoningEfforts...)
			m.sessionEffortIdx = effortIndex(m.selectedEffortPreference(), m.sessionEfforts)
			if m.sessionEffortIdx < 0 {
				m.sessionEffortIdx = effortIndex(opt.DefaultReasoningEffort, m.sessionEfforts)
			}
			if m.sessionEffortIdx < 0 {
				m.sessionEffortIdx = 0
			}
			m.mode = modeSessionEffort
		}
	}
	return m, nil
}

func effortIndex(effort string, efforts []string) int {
	for i, candidate := range efforts {
		if candidate == effort {
			return i
		}
	}
	return -1
}

func (m model) selectedEffortPreference() string {
	provider, _, effort, _, _ := m.sup.Session(m.selectedID())
	if provider == m.sessionProvider {
		return effort
	}
	return m.sup.PreferredEffort(m.sessionProvider)
}

func (m model) handleSessionEffortKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeSessionModel
	case "j", "down":
		if m.sessionEffortIdx < len(m.sessionEfforts)-1 {
			m.sessionEffortIdx++
		}
	case "k", "up":
		if m.sessionEffortIdx > 0 {
			m.sessionEffortIdx--
		}
	case "enter":
		if m.sessionModelIdx < len(m.sessionModels) && m.sessionEffortIdx < len(m.sessionEfforts) {
			err := m.sup.StartNewSession(m.selectedID(), m.sessionProvider, m.sessionModels[m.sessionModelIdx].ID, m.sessionEfforts[m.sessionEffortIdx])
			if err != nil {
				m.err = err
			}
			m.mode = modeNav
		}
	}
	return m, nil
}

// handleQuitKey answers the confirmation shown when quitting would kill a
// running turn. Confirmation is deliberately an explicit y rather than any key
// or a second q, since the keystroke that reaches it is one the user pressed
// expecting to leave, not to decide anything.
func (m model) handleQuitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m, tea.Quit
	default:
		m.mode = modeNav
		return m, nil
	}
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
		if m.editingProject {
			var err error
			if m.projectPane == 1 {
				err = m.store.ReplaceProjectInstructions(content)
			} else {
				err = m.store.ReplaceProjectBrief(content)
			}
			if err != nil {
				m.err = err
				return m, nil
			}
			m.editingProject = false
			m.mode = modeNav
			m.input.Blur()
			m = m.recalcLayout()
			m.showProjectContext()
			return m, nil
		}
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
			if err := m.store.ClearDraft(m.draftItemID); err != nil {
				m.err = err
			}
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
		m.draftItemID = ""
		m.input.Blur()
		m = m.recalcLayout()
		// Your own turn is never "new messages below" — go to the bottom now so
		// the reload that follows sees AtBottom and auto-follows onto it.
		m.conv.GotoBottom()
		return m, loadItemsCmd(m.store, m.view, m.showBacklog)
	case "esc":
		if m.editingProject {
			m.editingProject = false
			m.mode = modeNav
			m.input.Blur()
			m = m.recalcLayout()
			m.showProjectContext()
			return m, nil
		}
		m.checkpointTurnDraft()
		m.input.Blur()
		if m.draft {
			// Abandoning the body abandons the whole unwritten item.
			m = m.cancelDraft()
		}
		m.mode = modeNav
		m.draftItemID = ""
		m = m.recalcLayout()
		return m, nil
	case "ctrl+c":
		if err := m.store.ClearDraft(m.draftItemID); err != nil {
			m.err = err
		}
		m.input.Reset()
		m.input.Blur()
		m.mode = modeNav
		m.draftItemID = ""
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
	if !m.draft {
		m.draftSequence++
		sequence := m.draftSequence
		cmd = tea.Batch(cmd, tea.Tick(draftDebounce, func(time.Time) tea.Msg {
			return draftCheckpointMsg{sequence: sequence}
		}))
	}
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

func (m *model) checkpointTurnDraft() {
	if m.draftItemID == "" {
		return
	}
	if err := m.store.SaveDraft(m.draftItemID, m.input.Value()); err != nil {
		m.err = err
	}
}

func (m model) switchView(v listView) (model, tea.Cmd) {
	m.projectPane = 0
	m.view = v
	m.selected = 0
	m.items = nil
	m.showSelected()
	return m, loadItemsCmd(m.store, v, m.showBacklog)
}

func (m *model) showProjectContext() {
	m.items = nil
	m.selected = 0
	m.convItemID = ""
	var title, content string
	if m.projectPane == 1 {
		title = "User-owned project instructions"
		content, _ = m.store.ProjectInstructions()
	} else {
		title = "Agent-curated project brief"
		content, _ = m.store.ProjectBrief()
	}
	if content == "" {
		content = "(empty)"
	}
	m.conv.SetContent(wrapText(title+"\n\n"+content, m.conv.Width))
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
	mainH := m.height - 2 // subtract header and footer
	listContent, listScrollbarStr := m.renderList(mainH - 2)
	listWithScrollbar := lipgloss.JoinHorizontal(lipgloss.Top, listContent, listScrollbarStr)
	listPanelStyle := lipgloss.NewStyle().
		// No right padding: the scrollbar occupies that column instead,
		// mirroring the conv pane and composer below.
		Padding(1, 0, 1, 1).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		Height(mainH)
	listPanel := listPanelStyle.Width(listW).Render(listWithScrollbar)
	convAreaW := m.width - (m.listWidth() + 1)
	convScrollbar := renderScrollbar(m.conv.Height, m.conv.TotalLineCount(), m.conv.YOffset)
	convWithScrollbar := lipgloss.JoinHorizontal(lipgloss.Top, m.renderConv(), convScrollbar)
	var convPanel string
	if m.mode == modeCompose {
		// Per-element padding so the separator spans the full column width,
		// giving │──────── instead of │ ──────── at the corner. Scrollbar
		// occupies the 1-char right padding slot, mirroring the input below.
		viewportBlock := lipgloss.NewStyle().Padding(1, 0, 0, 1).Render(convWithScrollbar)
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
		convPanel = lipgloss.NewStyle().Padding(1, 0, 1, 1).Render(convWithScrollbar)
	}
	mainRow := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, convPanel)

	frame := lipgloss.JoinVertical(lipgloss.Left, header, mainRow, footer)
	if m.mode == modeQuit {
		// Over the finished frame, not inside a pane: the question is about the
		// whole session, and centring it in the reading pane put it off-centre
		// on the screen, which is where the user is actually looking.
		frame = overlayCentered(frame, m.quitConfirmBox(), m.width)
	}
	return frame
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
	// The quit confirmation is not overlaid here: it belongs to the whole
	// application rather than the conversation, so View places it over the
	// composed frame instead.
	if m.mode == modeStatus {
		lines = m.overlayStatusPopup(lines)
	} else if m.mode == modeSession {
		lines = m.overlaySessionPopup(lines)
	} else if m.mode == modeSessionModel {
		lines = m.overlaySessionModelPopup(lines)
	} else if m.mode == modeSessionEffort {
		lines = m.overlaySessionEffortPopup(lines)
	}
	return strings.Join(lines, "\n")
}

func (m model) overlaySessionPopup(lines []string) []string {
	itemID := m.selectedID()
	current, currentModel, currentEffort, id, updated := m.sup.Session(itemID)
	rows := make([]string, len(sessionProviders))
	for i, provider := range sessionProviders {
		marker, sty := "  ", lipgloss.NewStyle()
		if i == m.sessionIdx {
			marker, sty = "› ", lipgloss.NewStyle().Bold(true).Foreground(pendingFg)
		}
		rows[i] = sty.Render(marker + "new " + string(provider) + " session")
	}
	active := string(current)
	if currentModel != "" {
		active += " " + currentModel
	}
	if currentEffort != "" {
		active += " " + currentEffort
	}
	if id == "" {
		active += " (none)"
	} else if m.sup.SessionIsStale(itemID) {
		if current == supervisor.ProviderClaude {
			active += " (over 1h old; fresh recommended)"
		} else {
			active += " (over 30m old; fresh recommended)"
		}
	} else if current == supervisor.ProviderCodex {
		active += " (resumes this item; 30m cache window)"
	} else if !updated.IsZero() {
		active += " (resumes this item; 1h cache window)"
	}
	box := popupStyle.Render("agent session · this item " + active + "\n" + strings.Join(rows, "\n"))
	return overlayBox(lines, box, m.conv.Width)
}

func (m model) overlaySessionEffortPopup(lines []string) []string {
	rows := make([]string, len(m.sessionEfforts))
	for i, effort := range m.sessionEfforts {
		marker, sty := "  ", lipgloss.NewStyle()
		if i == m.sessionEffortIdx {
			marker, sty = "› ", lipgloss.NewStyle().Bold(true).Foreground(pendingFg)
		}
		rows[i] = sty.Render(marker + effort)
	}
	box := popupStyle.Render("effort · new " + string(m.sessionProvider) + " session\n" + strings.Join(rows, "\n"))
	return overlayBox(lines, box, m.conv.Width)
}

// overlaySessionModelPopup is step two of "S": choose a model for the
// provider picked in overlaySessionPopup. Its content is asynchronous —
// AvailableModels can be a subprocess round trip for Codex — so it renders a
// loading line until modelsLoadedMsg lands, or the fetch error in its place.
func (m model) overlaySessionModelPopup(lines []string) []string {
	header := "model · new " + string(m.sessionProvider) + " session"
	var body string
	switch {
	case m.sessionModelsLoading:
		body = dimStyle.Render("  loading models…")
	case m.sessionModelsErr != nil:
		body = dimStyle.Render("  could not list models: " + m.sessionModelsErr.Error())
	default:
		rows := make([]string, len(m.sessionModels))
		for i, opt := range m.sessionModels {
			marker, sty := "  ", lipgloss.NewStyle()
			if i == m.sessionModelIdx {
				marker, sty = "› ", lipgloss.NewStyle().Bold(true).Foreground(pendingFg)
			}
			label := opt.DisplayName
			if opt.Default {
				label += " (default)"
			}
			rows[i] = sty.Render(marker + label)
		}
		body = strings.Join(rows, "\n")
	}
	box := popupStyle.Render(header + "\n" + body)
	return overlayBox(lines, box, m.conv.Width)
}

// overlayBox draws a rendered popup over the top rows of the conversation.
// Each popup row replaces a whole line rather than being spliced into one —
// the underlying content is styled, and cutting a line mid-way would mean
// slicing ANSI sequences. Whole-row replacement is the cheap version, and it
// still reads as a floating box.
func overlayBox(lines []string, box string, width int) []string {
	const topRow = 1 // one row of breathing space above the popup
	pad := lipgloss.NewStyle().Width(width)
	for i, boxLine := range strings.Split(box, "\n") {
		row := topRow + i
		if row >= len(lines) {
			break
		}
		lines[row] = pad.Render(boxLine)
	}
	return lines
}

// busyDispatch reports the item being worked on right now. Tests build models
// without a supervisor, and "nobody is working" is the honest answer for one.
func (m model) busyDispatch() (string, bool) {
	if m.sup == nil {
		return "", false
	}
	return m.sup.Busy()
}

// quitConfirmBox renders the confirmation, naming the item by id. The id
// rather than the title: the busy item is not necessarily the selected one,
// and naming it is what makes the warning checkable.
func (m model) quitConfirmBox() string {
	itemID, _ := m.busyDispatch()
	box := confirmStyle.Render(
		lipgloss.NewStyle().Bold(true).Render("a turn is running on "+itemID) + "\n" +
			"quitting stops it and loses its work\n\n" +
			dimStyle.Render("y quit anyway   any other key stay"))
	if lipgloss.Width(box) > m.width {
		// A frame wider than the screen wraps, and a wrapped border reads as a
		// broken box rather than a narrow one. Drop to the short wording and
		// the selectors' padding, which is the widest thing that still fits.
		box = confirmStyle.Padding(0, 1).Render(
			lipgloss.NewStyle().Bold(true).Render("stop the running turn?") + "\n" +
				dimStyle.Render("y quit   other stay"))
	}
	return box
}

// overlayCentered draws a rendered box in the middle of a composed frame,
// leaving the rest of the frame visible around it. Unlike the pane popups,
// which replace whole rows, this splices each box row into the row underneath
// it — a modal that blanked full-width bands through the list and the borders
// would read as a rendering fault rather than as a box on top.
//
// Both cuts are ANSI-aware: the frame's rows carry styling, and slicing them
// by byte or rune would cut a colour sequence in half and bleed it across the
// rest of the line.
func overlayCentered(frame, box string, width int) string {
	rows := strings.Split(frame, "\n")
	boxRows := strings.Split(box, "\n")
	boxW := lipgloss.Width(box)

	top := (len(rows) - len(boxRows)) / 2
	left := (width - boxW) / 2
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}

	for i, boxRow := range boxRows {
		row := top + i
		if row >= len(rows) {
			break
		}
		under := rows[row]
		before := ansi.Truncate(under, left, "")
		// Pad a short row out to the box, so a frame row that ends early does
		// not pull the box left of centre.
		if w := lipgloss.Width(before); w < left {
			before += strings.Repeat(" ", left-w)
		}
		after := ansi.TruncateLeft(under, left+boxW, "")
		rows[row] = before + ansiReset + boxRow + ansiReset + after
	}
	return strings.Join(rows, "\n")
}

// ansiReset closes any style the frame had open where the box interrupts it,
// and again where the frame resumes, so neither bleeds into the other.
const ansiReset = "\x1b[0m"

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
	return overlayBox(lines, box, m.conv.Width)
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
	project := "  Project Context  "
	if m.projectPane != 0 {
		project = " [Project Context] "
	}
	if m.projectPane != 0 {
		tabs = append(tabs, headerTabActiveStyle.Render(project))
	} else {
		tabs = append(tabs, headerTabStyle.Render(project))
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
		if m.editingProject {
			text = "ctrl+s save project document  esc cancel"
		}
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
		text = "j/k select  enter choose model  esc cancel"
	case modeSessionModel:
		text = "j/k select  enter choose effort  esc back"
	case modeSessionEffort:
		text = "j/k select  enter start fresh session  esc back"
	case modeQuit:
		text = "y quit and stop the running turn  any other key stay"
	default:
		if m.projectPane != 0 {
			text = "tab switch document  e edit  1-4 item views  q quit"
			break
		}
		text = "q quit  j/k nav  a add  s status  S session  t turn  1-4 view  b backlog  pgup/pgdn scroll  r refresh"
		if itemID := m.selectedID(); itemID != "" && m.sup.SessionIsStale(itemID) {
			text = "q quit  j/k nav  a add  s status  S fresh context recommended  t turn  1-4 view  b backlog  pgup/pgdn scroll  r refresh"
		}
		if m.showBacklog {
			text = "q quit  j/k nav  a add  s status  S session  t turn  1-4 view  b hide backlog  pgup/pgdn scroll  r refresh"
			if itemID := m.selectedID(); itemID != "" && m.sup.SessionIsStale(itemID) {
				text = "q quit  j/k nav  a add  s status  S fresh context recommended  t turn  1-4 view  b hide backlog  pgup/pgdn scroll  r refresh"
			}
		}
	}

	// Right-aligned agent/context info for the selected item: the model and
	// remaining context % from its most recent turn. This is process-memory
	// only (supervisor.TurnInfo) — it goes blank again after a restart, since
	// there is no way to rederive it without running another turn.
	right := m.renderAgentInfo(m.selectedID())

	// The hint text grows with the keymap; drop it rather than overflow the row.
	if len(text)+len(right) > m.width {
		text = ""
	}
	if pad := m.width - len(text) - len(right); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return footerStyle.Render(text + right)
}

// renderAgentInfo formats the model and remaining context % from itemID's
// most recent turn this process. Before any turn has run — a freshly picked
// session, or the TUI having just started against an item resumed from an
// earlier process — it falls back to the explicitly selected model, if any,
// so the user can tell what they are about to get without dispatching first.
// An untouched item inherits the last explicit selection for its default
// provider. If there is no saved preference, the harness default genuinely
// is not knowable ahead of a turn (see 20260809-073211), so it renders "".
func (m model) renderAgentInfo(itemID string) string {
	if itemID == "" || m.sup == nil {
		return ""
	}
	if info, ok := m.sup.LastTurnInfo(itemID); ok {
		if info.Context.WindowTokens <= 0 {
			return info.Model + " "
		}
		remaining := 100 - info.Context.UsedTokens*100/info.Context.WindowTokens
		if remaining < 0 {
			remaining = 0
		}
		return fmt.Sprintf("%s %d%% left ", info.Model, remaining)
	}
	if _, sessionModel, effort, _, _ := m.sup.Session(itemID); sessionModel != "" {
		return strings.TrimSpace(sessionModel+" "+effort) + " "
	}
	return ""
}

func (m model) renderList(availH int) (content, scrollbar string) {
	marker := hiddenBacklogLabel(m.hiddenBacklog)
	if len(m.items) == 0 && !m.draft {
		// A view holding nothing but suppressed rows is not empty, and saying
		// so would be a lie the toggle can't be discovered from.
		if marker != "" {
			return dimStyle.Render(hiddenBacklogRow(marker, m.listWidth()-2)), listScrollbar(availH, 1, 0)
		}
		return dimStyle.Render("(empty)"), listScrollbar(availH, 1, 0)
	}
	// panel uses Width(listW) with 1 col left padding + 1 col scrollbar, so
	// content = listW-2, same budget as the old uniform Padding(1).
	colW := m.listWidth() - 2
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
	// Clamp to the panel's height so a long list scrolls instead of pushing
	// the header and footer off screen. Reserve a line for the marker row
	// below, if any, before windowing so the two stay within budget together.
	rowBudget := availH
	if marker != "" {
		rowBudget--
	}
	window, offset, total := windowListRows(lines, m.listOffset, m.selected, rowBudget)
	if marker != "" {
		total++ // the marker row itself, appended below outside the window
	}
	lines = window

	// Sits below the rows and is not selectable: selection indexes m.items,
	// which this is deliberately not part of.
	if marker != "" {
		lines = append(lines, dimStyle.Render(hiddenBacklogRow(marker, colW)))
	}
	return strings.Join(lines, "\n"), listScrollbar(availH, total, offset)
}

// listScrollbar guards renderScrollbar against a non-positive height, which
// would otherwise ask strings.Repeat for a negative count.
func listScrollbar(availH, total, offset int) string {
	if availH <= 0 {
		return ""
	}
	return renderScrollbar(availH, total, offset)
}

// windowListRows returns the slice of rows that fits within availH lines,
// starting from start and scrolling forward only as far as needed to keep
// the selected row fully visible. Rows vary in height (word-wrapped
// previews), so the window is computed by summing per-row heights rather
// than counting rows. offset and total are line counts (not row counts),
// matching what renderScrollbar expects.
//
// start is not searched for here — it comes in already anchored by
// ensureListOffsetVisible, which is what keeps the window still while
// selection moves within it. Recomputing the tightest-fitting start from
// scratch on every call, as an earlier version of this function did, always
// picks the start closest to the selection, which scrolls by one row on
// every step once the selection is below the first page.
func windowListRows(rows []string, start, selected, availH int) (window []string, offset, total int) {
	heights := make([]int, len(rows))
	for i, r := range rows {
		heights[i] = strings.Count(r, "\n") + 1
		total += heights[i]
	}
	if availH <= 0 || len(rows) == 0 {
		return rows, 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(rows) {
		selected = len(rows) - 1
	}
	if start < 0 {
		start = 0
	}
	if start > selected {
		start = selected
	}
	// Defensive only: ensureListOffsetVisible should already guarantee this
	// fits, but if heights and start ever disagree, still show the selection
	// rather than clip it.
	for start < selected {
		sum := 0
		for i := start; i <= selected; i++ {
			sum += heights[i]
		}
		if sum <= availH {
			break
		}
		start++
	}
	for i := 0; i < start; i++ {
		offset += heights[i]
	}

	used := 0
	for i := start; i <= selected; i++ {
		used += heights[i]
	}
	end := selected + 1
	for end < len(rows) && used+heights[end] <= availH {
		used += heights[end]
		end++
	}
	return rows[start:end], offset, total
}

// listRowHeights mirrors the row heights renderList actually draws — each
// item's wrapped preview lines plus one metadata line, and a fixed two-line
// row for an open draft — without rendering the full styled rows. Used to
// keep listOffset in sync on every keystroke without paying for lipgloss
// styling each time.
func (m model) listRowHeights() []int {
	heights := make([]int, 0, len(m.items)+1)
	for _, it := range m.items {
		heights = append(heights, m.listRowHeight(it))
	}
	if m.draft {
		heights = append(heights, 2)
	}
	return heights
}

// listRowHeight is the number of screen rows a list item occupies. Keeping it
// separate from listRowHeights lets startup decide whether the whole inbox,
// including backlog, fits before choosing which slice to display.
func (m model) listRowHeight(item models.Item) int {
	colW := m.listWidth() - 2
	textW := colW - badgeW
	return len(truncateLines(wordWrap(item.Title, textW), previewMaxLines, textW)) + 1
}

// listBaseAvailRows is the list's row budget without the hidden-backlog
// marker. That is the relevant capacity when deciding whether to show every
// inbox row at startup.
func (m model) listBaseAvailRows() int {
	return m.height - 2 - 2 // header+footer, then the panel's top+bottom padding
}

// initialBacklogFits reports whether showing all live inbox items, including
// parked backlog, still leaves room in the list. A list exactly full is not
// considered a short inbox: hiding backlog then reserves the extra line for
// the marker and makes the overflow discoverable.
func (m model) initialBacklogFits(all []models.Item) bool {
	items, _ := channelView(models.ChannelInbox).prepare(all, true)
	used := 0
	for _, item := range items {
		used += m.listRowHeight(item)
	}
	return used < m.listBaseAvailRows()
}

// listAvailRows is the line budget renderList windows rows into: the list
// panel's height minus the header, footer, and panel padding, minus one more
// if the hidden-backlog marker is going to claim a line below the rows.
func (m model) listAvailRows() int {
	avail := m.listBaseAvailRows()
	if hiddenBacklogLabel(m.hiddenBacklog) != "" {
		avail--
	}
	return avail
}

// ensureListOffsetVisible adjusts listOffset by the minimum amount needed to
// keep the selected row visible, leaving it untouched otherwise: it scrolls
// up to selected's row if selected is above the current window, or down just
// far enough to fit selected if it is below. This is what keeps the list
// panel still while the selection moves within the visible page, instead of
// re-centring on every keypress.
func (m model) ensureListOffsetVisible() int {
	heights := m.listRowHeights()
	n := len(heights)
	if n == 0 {
		return 0
	}
	selected := m.selected
	if selected < 0 {
		selected = 0
	}
	if selected >= n {
		selected = n - 1
	}
	start := m.listOffset
	if start < 0 {
		start = 0
	}
	if start > selected {
		start = selected
	}
	if availH := m.listAvailRows(); availH > 0 {
		for start < selected {
			sum := 0
			for i := start; i <= selected; i++ {
				sum += heights[i]
			}
			if sum <= availH {
				break
			}
			start++
		}
	}
	return start
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

// badgeW is the width of the status-dot prefix ("● " or "  ") that precedes
// each row's text, shared between renderList and the offset-tracking helpers
// below so both agree on how much width wraps the preview.
const badgeW = 2

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
	// Shutdown, not a bare return: an agent turn started here must not outlive
	// the process that started it, or the next run comes up unable to tell a
	// dead dispatch's leftovers from a live one's.
	defer sup.Shutdown()

	watchCh, err := startWatcher(s.Root)
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	sup.Start()
	p := tea.NewProgram(newModel(s, watchCh, sup), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
