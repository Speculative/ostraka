package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ostraka/internal/models"
	agentprompt "ostraka/internal/prompt"
	"ostraka/internal/store"
)

// nudgePrompt is deliberately short because the provider session already has
// the bootstrap context. It carries only the event that woke the agent and the
// invariant most likely to be violated by a continuation.
func nudgePrompt(itemID, userTurn string) string {
	if userTurn != "" {
		return fmt.Sprintf(
			"There is a new user turn on Ostraka item %s. Continue the item's work; do not post an acknowledgement before doing the requested work.\n\n"+
				"--- latest user turn ---\n%s\n--- end latest user turn ---\n\n"+
				"Post one substantive `ostraka item turn` only after work and validation are complete; it ends this dispatch. Use actual multiline content, never literal `\\n` text.",
			itemID, userTurn)
	}
	return fmt.Sprintf(
		"Resume Ostraka item %s. Read it with `ostraka item show %s --json`, carry out any outstanding request, and send one final Ostraka item reply only after work and validation are complete. That reply ends this dispatch.",
		itemID, itemID)
}

const bootstrapItemContextMaxChars = 24000

func bootstrapPrompt(itemID, instructions, brief, itemContext, replyCmd string) string {
	return fmt.Sprintf(`You are starting a new agent session for Ostraka item %s.

%s

--- user-owned project instructions ---
%s
--- end user-owned project instructions ---

--- agent-curated project brief ---
%s
--- end agent-curated project brief ---

--- Ostraka item context ---
%s
--- end Ostraka item context ---`, itemID, agentprompt.AgentOrientation(replyCmd), emptyContext(instructions), emptyContext(brief), itemContext)
}

func itemContext(item models.Item) string {
	var sb strings.Builder
	sb.WriteString(item.Title + "\n")
	sb.WriteString(fmt.Sprintf("channel: %s  type: %s  status: %s  created: %s\n\n", item.Channel, item.Type, item.Status, item.Created.Format(time.RFC3339)))
	sb.WriteString(item.Body)
	for _, turn := range item.Turns {
		sb.WriteString(fmt.Sprintf("\n\n--- %s · %s ---\n%s", turn.Actor, turn.Timestamp.Format(time.RFC3339), turn.Content))
	}
	return sb.String()
}

func boundedItemContext(item models.Item) string {
	full := itemContext(item)
	if len([]rune(full)) <= bootstrapItemContextMaxChars {
		return full
	}
	header := item.Title + "\n" + fmt.Sprintf("channel: %s  type: %s  status: %s\n\n", item.Channel, item.Type, item.Status)
	body := trimRunes(item.Body, bootstrapItemContextMaxChars/2)
	parts := []string{header + body}
	used := len([]rune(parts[0]))
	omitted := 0
	for i := len(item.Turns) - 1; i >= 0; i-- {
		turn := fmt.Sprintf("\n\n--- %s · %s ---\n%s", item.Turns[i].Actor, item.Turns[i].Timestamp.Format(time.RFC3339), item.Turns[i].Content)
		if used+len([]rune(turn)) > bootstrapItemContextMaxChars {
			omitted++
			continue
		}
		parts = append(parts, turn)
		used += len([]rune(turn))
	}
	// Newest turns were collected backwards; preserve chronological order after
	// the opening body.
	for i, j := 1, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if omitted > 0 {
		parts = append(parts, fmt.Sprintf("\n\n[Context limit reached: %d earlier reply/replies omitted. Retrieve them with `ostraka item show %s --json` if needed.]", omitted, item.ID))
	}
	return strings.Join(parts, "")
}

func trimRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n[opening body truncated]"
}

func replyCommand(root, itemID string) string {
	return agentprompt.ReplyCommand(filepath.Dir(root), itemID)
}

func emptyContext(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func (s *Supervisor) latestUserTurn(itemID string) string {
	if s.store == nil {
		return ""
	}
	item, err := s.store.GetItem(itemID)
	if err != nil {
		s.logger.Printf("item %s: cannot read latest user turn: %v", itemID, err)
		return ""
	}
	if len(item.Turns) == 0 {
		return ""
	}
	latest := item.Turns[len(item.Turns)-1]
	if latest.Actor != models.ActorUser {
		return ""
	}
	return latest.Content
}

const queueCapacity = 64

type enqueueMsg struct {
	itemID string
}

// Supervisor drives a background coding-agent harness, resuming it whenever
// the user submits a turn on an ostraka item. One Supervisor serializes all
// dispatches for a single .ostraka root (project) through a FIFO queue.
type Supervisor struct {
	root    string
	harness Harness
	store   *store.Store
	queue   chan enqueueMsg
	logger  *log.Logger
	session sessionGuard

	turnInfoMu sync.Mutex
	turnInfo   map[string]TurnInfo

	// A dispatch runs under this context, so Shutdown can end an agent turn
	// that is still in flight. Without it the agent outlives the process that
	// dispatched it, and how long it survives depends on when it next writes
	// to a stdout nobody is reading.
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
	started  bool
	done     chan struct{}

	busy busyGuard
}

// busyGuard tracks the item being dispatched right now, so the UI can ask
// before quitting out from under a running turn.
type busyGuard struct {
	mu     sync.Mutex
	itemID string
}

// Busy reports the item currently being dispatched, if any.
func (s *Supervisor) Busy() (string, bool) {
	s.busy.mu.Lock()
	defer s.busy.mu.Unlock()
	return s.busy.itemID, s.busy.itemID != ""
}

func (s *Supervisor) setBusy(itemID string) {
	s.busy.mu.Lock()
	defer s.busy.mu.Unlock()
	s.busy.itemID = itemID
}

// Shutdown ends any in-flight dispatch and waits for the worker to stop. It is
// what makes "no ostraka process means no dispatch" true rather than likely,
// which is the assumption recoverStaleDispatches makes on the way back up.
// Safe to call more than once, and on a Supervisor that was never started.
func (s *Supervisor) Shutdown() {
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.started {
			<-s.done
		}
	})
}

// runContext is the context a dispatch runs under. Tests construct a
// Supervisor directly and never set one; a dispatch with no cancellation is
// the old behaviour, which is the right fallback.
func (s *Supervisor) runContext() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// Session returns the provider, model, ID, and last activity time for
// itemID. model is the one the next fresh session would launch with (or the
// one the current session is already running, once a turn has confirmed it);
// it may be "" for the harness's own default.
func (s *Supervisor) Session(itemID string) (provider Provider, model, effort, sessionID string, updatedAt time.Time) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	sf, err := loadItemSession(s.root, itemID)
	if err != nil {
		return ProviderClaude, "", "", "", time.Time{}
	}
	return sf.Provider, sf.Model, sf.Effort, sf.SessionID, sf.UpdatedAt
}

// TurnInfo is the model and context-window usage from an item's most recent
// dispatch. It lives in process memory only, never on disk: it is only ever
// a byproduct of a live turn, so a stale session cannot rederive it anyway.
type TurnInfo struct {
	Model   string
	Context ContextUsage
}

// LastTurnInfo returns itemID's most recent TurnInfo, if a turn has run for
// it since this process started.
func (s *Supervisor) LastTurnInfo(itemID string) (TurnInfo, bool) {
	s.turnInfoMu.Lock()
	defer s.turnInfoMu.Unlock()
	info, ok := s.turnInfo[itemID]
	return info, ok
}

func (s *Supervisor) setTurnInfo(itemID string, info TurnInfo) {
	s.turnInfoMu.Lock()
	defer s.turnInfoMu.Unlock()
	if s.turnInfo == nil {
		s.turnInfo = make(map[string]TurnInfo)
	}
	s.turnInfo[itemID] = info
}

// SessionIsStale reports whether a provider's documented cache-reuse window
// has elapsed for itemID. The TUI uses it to recommend a fresh context, but
// leaves the decision to the user: providers may retain cache entries longer.
func (s *Supervisor) SessionIsStale(itemID string) bool {
	provider, _, _, id, updated := s.Session(itemID)
	return sessionIsStale(sessionFile{Provider: provider, SessionID: id, UpdatedAt: updated}, time.Now())
}

// StartNewSession switches itemID to a fresh harness context, to be launched
// with model on its next dispatch. model may be "" for the harness's own
// default. It is safe to call during a running turn: dispatch will not let
// that old turn overwrite the freshly selected session state when it
// finishes.
func (s *Supervisor) StartNewSession(itemID string, provider Provider, model, effort string) error {
	if !provider.valid() {
		return fmt.Errorf("unknown provider %q", provider)
	}
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if err := saveItemSession(s.root, itemID, sessionFile{Provider: provider, Model: model, Effort: effort}); err != nil {
		return err
	}
	if err := saveModelDefault(s.root, provider, model); err != nil {
		return err
	}
	return saveEffortDefault(s.root, provider, effort)
}

func (s *Supervisor) PreferredEffort(provider Provider) string {
	effort, _ := loadEffortDefault(s.root, provider)
	return effort
}

// PreferredModel returns the last model explicitly selected for provider.
// New items and the model picker use it until the user chooses another one.
func (s *Supervisor) PreferredModel(provider Provider) string {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	model, _ := loadModelDefault(s.root, provider)
	return model
}

// AvailableModels lists the models selectable for a fresh provider session.
func (s *Supervisor) AvailableModels(ctx context.Context, provider Provider) ([]ModelOption, error) {
	return s.harnessFor(provider).AvailableModels(ctx)
}

func (s *Supervisor) harnessFor(provider Provider) Harness {
	if provider == ProviderCodex {
		return newCodexHarness()
	}
	return s.harness
}

// New constructs a Supervisor rooted at the given .ostraka directory using
// the default (Claude Code) harness. It does not start the worker goroutine
// — call Start for that.
func New(root string) *Supervisor {
	os.MkdirAll(supervisorDir(root), 0755) //nolint:errcheck
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{
		root:    root,
		harness: newClaudeHarness(),
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	// The store is only used to mark items as being worked on. A failure here
	// is not fatal: dispatching without the marker is better than not
	// dispatching at all, so nil is handled at the call sites.
	if st, err := store.NewStore(root); err == nil {
		s.store = st
	} else {
		s.logger.Printf("no store, status will not be marked during dispatch: %v", err)
	}
	s.recoverStaleDispatches()
	return s
}

// recoverStaleDispatches clears state left behind by a run that never finished
// — a kill, a crash, a closed terminal. Both halves of that state agree with
// each other, so the reading pane replays a dead run's trace under a live
// working header until something overwrites it.
//
// This runs in New, before Start launches the worker: nothing can be in flight
// yet, so every live log on disk is by definition an orphan and every
// agent-acknowledged item is one no agent is working on. That reasoning
// assumes one supervisor per root, which is also what the serial queue and the
// single session file already assume.
func (s *Supervisor) recoverStaleDispatches() {
	for _, itemID := range sweepLive(s.root) {
		s.logger.Printf("item %s: discarded live log from an unfinished dispatch", itemID)
	}
	if s.store == nil {
		return
	}
	acknowledged := models.StatusAgentAcknowledged
	items, err := s.store.ListItems(store.ListOpts{Status: &acknowledged})
	if err != nil {
		s.logger.Printf("cannot scan for interrupted dispatches: %v", err)
		return
	}
	for _, item := range items {
		// Back to pending-agent rather than pending-user: the turn that
		// triggered the dispatch was never answered, so the item is still
		// owed a reply and should be redispatchable.
		if _, err := s.store.SetStatus(item.ID, models.StatusPendingAgent); err != nil {
			s.logger.Printf("item %s: cannot clear interrupted dispatch marker: %v", item.ID, err)
			continue
		}
		s.logger.Printf("item %s: returned to pending-agent after an unfinished dispatch", item.ID)
	}
}

// Start launches the single worker goroutine that drains the queue serially,
// never running two harness turns concurrently. Safe to call once per
// Supervisor. Non-blocking.
func (s *Supervisor) Start() {
	s.started = true
	go s.run()
}

// Enqueue requests a harness dispatch for itemID. Non-blocking: buffers into
// the internal queue and returns immediately. If the queue is full, the
// request is dropped and logged rather than blocking the caller (the TUI
// event loop).
func (s *Supervisor) Enqueue(itemID string) {
	select {
	case s.queue <- enqueueMsg{itemID: itemID}:
	default:
		s.logger.Printf("queue full, dropping dispatch for item %s", itemID)
	}
}

func (s *Supervisor) run() {
	defer close(s.done)
	ctx := s.runContext()
	for {
		// Shutdown wins over a full queue: once it is cancelled, the remaining
		// requests belong to a session that is ending, and draining them would
		// start turns nobody is left to watch.
		select {
		case <-ctx.Done():
			return
		default:
		}
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-s.queue:
			if !ok {
				return
			}
			s.dispatch(msg)
		}
	}
}

// markAcknowledged flags an item as being worked on, but only from
// pending-agent: any other status means the user has moved the item since it
// was queued, and a stale dispatch should not drag it back.
func (s *Supervisor) markAcknowledged(itemID string) {
	if s.store == nil {
		return
	}
	item, err := s.store.GetItem(itemID)
	if err != nil {
		s.logger.Printf("item %s: cannot read to mark acknowledged: %v", itemID, err)
		return
	}
	if item.Status != models.StatusPendingAgent {
		return
	}
	if _, err := s.store.SetStatus(itemID, models.StatusAgentAcknowledged); err != nil {
		s.logger.Printf("item %s: cannot mark acknowledged: %v", itemID, err)
	}
}

// revertAcknowledged clears the in-progress marker if it is still set. It is a
// no-op when the agent already replied, since that advanced the status itself.
func (s *Supervisor) revertAcknowledged(itemID string, to models.Status) {
	if s.store == nil {
		return
	}
	item, err := s.store.GetItem(itemID)
	if err != nil || item.Status != models.StatusAgentAcknowledged {
		return
	}
	if _, err := s.store.SetStatus(itemID, to); err != nil {
		s.logger.Printf("item %s: cannot clear acknowledged marker: %v", itemID, err)
	}
}

func (s *Supervisor) dispatch(msg enqueueMsg) {
	s.session.mu.Lock()
	sf, err := loadItemSession(s.root, msg.itemID)
	s.session.mu.Unlock()
	if err != nil {
		s.logger.Printf("item %s: failed to load session, starting fresh with Claude: %v", msg.itemID, err)
		sf = sessionFile{Provider: ProviderClaude}
	}

	if sf.SessionID == "" {
		s.logger.Printf("item %s: dispatching (fresh %s session)", msg.itemID, sf.Provider)
	} else {
		s.logger.Printf("item %s: dispatching (%s session %s)", msg.itemID, sf.Provider, sf.SessionID)
	}

	// Clear before marking, not after: the pane renders the live log only for
	// an acknowledged item, so an item that is acknowledged while a previous
	// log still exists shows that log as though it were this run's. The
	// startup sweep cannot cover this — a log can be orphaned after startup by
	// a clear that failed, or by another process writing under the same root.
	live := newLiveLog(s.root, msg.itemID)
	live.clear()
	defer live.clear()

	s.markAcknowledged(msg.itemID)
	s.setBusy(msg.itemID)
	defer s.setBusy("")

	userTurn := s.latestUserTurn(msg.itemID)
	prompt := nudgePrompt(msg.itemID, userTurn)
	if sf.SessionID == "" {
		instructions, brief := "", ""
		context := fmt.Sprintf("Ostraka item %s could not be read.", msg.itemID)
		if s.store != nil {
			instructions, _ = s.store.ProjectInstructions()
			brief, _ = s.store.ProjectBrief()
			if item, err := s.store.GetItem(msg.itemID); err == nil {
				context = boundedItemContext(item)
			}
		}
		prompt = bootstrapPrompt(msg.itemID, instructions, brief, context, replyCommand(s.root, msg.itemID))
	}
	result, err := s.harnessFor(sf.Provider).RunTurn(s.runContext(), prompt, sf.SessionID, sf.Model, sf.Effort, live.append)
	if result.Model != "" {
		s.setTurnInfo(msg.itemID, TurnInfo{Model: result.Model, Context: result.Context})
	}
	if err != nil {
		s.logger.Printf("item %s: dispatch failed: %v", msg.itemID, err)
		// Put it back in the queue's state so it doesn't sit forever showing
		// as in-progress for a run that is already over.
		s.revertAcknowledged(msg.itemID, models.StatusPendingAgent)
		// A turn cut short still happened: the provider has a session holding
		// whatever the agent did before it was stopped. Persisting the id is
		// what stops the resume cursor rewinding past that work — the id is
		// known from the stream's opening event, long before the result.
		s.persistSession(msg.itemID, sf, result.SessionID)
		return
	}
	// A successful run whose agent never posted a turn leaves the marker
	// behind — hand it back rather than showing work that isn't happening.
	s.revertAcknowledged(msg.itemID, models.StatusPendingUser)
	s.persistSession(msg.itemID, sf, result.SessionID)
	s.logger.Printf("item %s: dispatch complete (session=%s is_error=%v duration_ms=%d cost_usd=%.4f num_turns=%d model=%s context_used=%d context_window=%d)",
		msg.itemID, result.SessionID, result.IsError, result.DurationMs, result.TotalCostUSD, result.NumTurns,
		result.Model, result.Context.UsedTokens, result.Context.WindowTokens)
}

// persistSession advances the resume cursor, but only if the session it was
// dispatched under is still the selected one: the user may have started a
// fresh session mid-turn, and the finishing turn must not resurrect the old.
func (s *Supervisor) persistSession(itemID string, dispatched sessionFile, sessionID string) {
	if sessionID == "" {
		return
	}
	s.session.mu.Lock()
	current, loadErr := loadItemSession(s.root, itemID)
	var saveErr error
	if loadErr == nil && current == dispatched {
		saveErr = saveItemSession(s.root, itemID, sessionFile{Provider: dispatched.Provider, Model: dispatched.Model, Effort: dispatched.Effort, SessionID: sessionID})
	}
	s.session.mu.Unlock()
	if saveErr != nil {
		s.logger.Printf("item %s: failed to persist session id %s: %v", itemID, sessionID, saveErr)
	}
}
