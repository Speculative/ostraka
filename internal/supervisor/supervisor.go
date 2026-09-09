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

	"github.com/Speculative/ostraka/internal/models"
	agentprompt "github.com/Speculative/ostraka/internal/prompt"
	"github.com/Speculative/ostraka/internal/store"
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

func activityNudgePrompt(itemID, userTurn string, activities []models.Activity) string {
	base := nudgePrompt(itemID, userTurn)
	if len(activities) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n--- unhandled subthread activity ---\n")
	for _, activity := range activities {
		title := activity.ChildTitle
		if title == "" {
			title = activity.ChildID
		}
		sb.WriteString(fmt.Sprintf("%s: %s (%s, result=%s, actor=%s)\n", activity.Type, title, activity.ChildID, activity.Result, activity.Actor))
	}
	sb.WriteString("Review each affected subthread with `ostraka item show <child-id> --json`, reconcile its decision with the root item, and include the consequences in your final root reply.")
	return sb.String()
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
	if item.Parent != "" {
		sb.WriteString("parent: " + item.Parent + "\n\n")
	}
	if len(item.Related) > 0 {
		sb.WriteString("related: " + strings.Join(item.Related, ", ") + "\n\n")
	}
	sb.WriteString(item.Body)
	for _, turn := range item.Turns {
		sb.WriteString(fmt.Sprintf("\n\n--- %s · %s ---\n%s", turn.Actor, turn.Timestamp.Format(time.RFC3339), turn.Content))
	}
	return sb.String()
}

func relationshipContext(s *store.Store, item models.Item) string {
	if s == nil {
		return ""
	}
	var sb strings.Builder
	if item.Parent != "" {
		if root, err := s.GetItem(item.Parent); err == nil {
			sb.WriteString(fmt.Sprintf("\n--- subthread relationship ---\nroot: %s — %s\n", root.ID, root.Title))
		}
	}
	if item.Parent == "" {
		if all, err := s.ListItems(store.ListOpts{}); err == nil {
			for _, child := range all {
				if child.Parent == item.ID {
					sb.WriteString(fmt.Sprintf("\nsubthread: %s — %s [%s]", child.ID, child.Title, child.Status))
				}
			}
		}
	}
	if len(item.Related) > 0 {
		sb.WriteString("\nrelated roots: " + strings.Join(item.Related, ", "))
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
	itemID   string
	activity bool
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
	active     activeTurnGuard

	// A dispatch runs under this context, so Shutdown can end an agent turn
	// that is still in flight. Without it the agent outlives the process that
	// dispatched it, and how long it survives depends on when it next writes
	// to a stdout nobody is reading.
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
	started  bool
	done     chan struct{}

	busy           busyGuard
	queueMu        sync.Mutex
	activityQueued map[string]bool
	activityBusy   map[string]bool
}

// busyGuard tracks the item being dispatched right now, so the UI can ask
// before quitting out from under a running turn.
type busyGuard struct {
	mu     sync.Mutex
	itemID string
}

type activeTurn struct {
	itemID    string
	cancel    context.CancelFunc
	interrupt func() error
	requested bool
}

type activeTurnGuard struct {
	mu   sync.Mutex
	turn *activeTurn
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

func (s *Supervisor) clearActiveTurn(turn *activeTurn) {
	s.active.mu.Lock()
	if s.active.turn == turn {
		s.active.turn = nil
	}
	s.active.mu.Unlock()
}

// Interrupt asks the active provider turn to stop. Providers with a native
// protocol use it first; the per-turn context is the fallback, which causes
// process cleanup without shutting down the supervisor itself. It is safe and
// successful when idle.
func (s *Supervisor) Interrupt() error {
	s.active.mu.Lock()
	turn := s.active.turn
	if turn != nil {
		turn.requested = true
	}
	s.active.mu.Unlock()
	if turn == nil {
		return nil
	}
	if turn.interrupt != nil {
		if err := turn.interrupt(); err == nil {
			return nil
		} else {
			s.logger.Printf("item %s: provider interrupt failed, using process cancellation: %v", turn.itemID, err)
		}
	}
	turn.cancel()
	return nil
}

func (s *Supervisor) interruptRequested(turn *activeTurn) bool {
	s.active.mu.Lock()
	defer s.active.mu.Unlock()
	return turn.requested
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

// DispatchError returns the most recent provider failure for itemID. It is
// persisted so the TUI can still show the warning after the transient live
// trace has been removed or the supervisor has been restarted.
func (s *Supervisor) DispatchError(itemID string) (string, bool) {
	message := ReadDispatchError(s.root, itemID)
	return message, message != ""
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

func (s *Supervisor) enqueueActivity(itemID string) {
	s.queueMu.Lock()
	if s.activityQueued == nil {
		s.activityQueued = make(map[string]bool)
	}
	if s.activityQueued[itemID] {
		s.queueMu.Unlock()
		return
	}
	s.activityQueued[itemID] = true
	s.queueMu.Unlock()
	select {
	case s.queue <- enqueueMsg{itemID: itemID, activity: true}:
	default:
		s.queueMu.Lock()
		delete(s.activityQueued, itemID)
		s.queueMu.Unlock()
		s.logger.Printf("queue full, dropping activity dispatch for item %s", itemID)
	}
}

// EnqueuePendingActivityRoots wakes roots affected by child closure events.
// It is safe to call after every filesystem notification; queue de-duplication
// keeps a burst of child writes to one family from launching duplicate runs.
func (s *Supervisor) EnqueuePendingActivityRoots() {
	if s.store == nil {
		return
	}
	items, err := s.store.ListItems(store.ListOpts{})
	if err != nil {
		s.logger.Printf("cannot scan activity roots: %v", err)
		return
	}
	for _, root := range items {
		if root.Parent != "" || root.Status == models.StatusBacklog || models.TerminalStatuses[root.Status] || root.Status == models.StatusProposed {
			continue
		}
		activities, err := s.store.PendingActivities(root.ID)
		if err != nil {
			s.logger.Printf("cannot read activities for %s: %v", root.ID, err)
			continue
		}
		shouldWake := false
		for _, activity := range activities {
			if activity.Type == store.ActivitySubthreadClosed {
				shouldWake = true
				break
			}
		}
		if !shouldWake {
			continue
		}
		s.queueMu.Lock()
		busy := s.activityBusy[root.ID]
		s.queueMu.Unlock()
		if busy {
			// The dispatch will rescan after it finishes. This suppresses the
			// repeated fsnotify writes caused by one activity journal update,
			// while still allowing genuinely new events to queue a follow-up.
			continue
		}
		if root.Status != models.StatusPendingAgent && root.Status != models.StatusAgentAcknowledged {
			if _, err := s.store.SetStatusBy(root.ID, models.StatusPendingAgent, models.ActorAgent); err != nil {
				s.logger.Printf("cannot queue root %s for child activity: %v", root.ID, err)
				continue
			}
		}
		s.enqueueActivity(root.ID)
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
			if msg.activity {
				s.queueMu.Lock()
				delete(s.activityQueued, msg.itemID)
				s.queueMu.Unlock()
			}
			s.dispatch(msg)
		}
	}
}

// markAcknowledged flags an item as being worked on, but only from
// pending-agent: any other status means the user has moved the item since it
// was queued, and a stale dispatch should not drag it back or launch a second
// harness turn. The bool is the dispatch admission result; callers must not
// run the harness when the queued request has gone stale.
func (s *Supervisor) markAcknowledged(itemID string) bool {
	if s.store == nil {
		return true
	}
	item, err := s.store.GetItem(itemID)
	if err != nil {
		s.logger.Printf("item %s: cannot read to mark acknowledged: %v", itemID, err)
		return false
	}
	if item.Status != models.StatusPendingAgent {
		return false
	}
	if _, err := s.store.SetStatus(itemID, models.StatusAgentAcknowledged); err != nil {
		s.logger.Printf("item %s: cannot mark acknowledged: %v", itemID, err)
		return false
	}
	return true
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
	s.queueMu.Lock()
	if s.activityBusy == nil {
		s.activityBusy = make(map[string]bool)
	}
	s.activityBusy[msg.itemID] = true
	s.queueMu.Unlock()
	defer func() {
		s.queueMu.Lock()
		delete(s.activityBusy, msg.itemID)
		s.queueMu.Unlock()
		s.EnqueuePendingActivityRoots()
	}()
	if s.store == nil {
		s.logger.Printf("item %s: no store available", msg.itemID)
	}
	var activities []models.Activity
	var beforeTurns int
	if s.store != nil {
		activities, _ = s.store.PendingActivities(msg.itemID)
		if item, err := s.store.GetItem(msg.itemID); err == nil {
			beforeTurns = len(item.Turns)
		}
	}
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

	if !s.markAcknowledged(msg.itemID) {
		s.logger.Printf("item %s: skipping stale dispatch; item is no longer pending-agent", msg.itemID)
		return
	}
	if err := clearDispatchError(s.root, msg.itemID); err != nil {
		s.logger.Printf("item %s: cannot clear previous dispatch error: %v", msg.itemID, err)
	}
	s.setBusy(msg.itemID)
	defer s.setBusy("")

	userTurn := s.latestUserTurn(msg.itemID)
	prompt := activityNudgePrompt(msg.itemID, userTurn, activities)
	if sf.SessionID == "" {
		instructions, brief := "", ""
		context := fmt.Sprintf("Ostraka item %s could not be read.", msg.itemID)
		if s.store != nil {
			instructions, _ = s.store.ProjectInstructions()
			brief, _ = s.store.ProjectBrief()
			if item, err := s.store.GetItem(msg.itemID); err == nil {
				context = boundedItemContext(item)
				context += relationshipContext(s.store, item)
			}
			if len(activities) > 0 {
				context += "\n\n" + activityNudgePrompt(msg.itemID, "", activities)
			}
		}
		prompt = bootstrapPrompt(msg.itemID, instructions, brief, context, replyCommand(s.root, msg.itemID))
	}
	harness := s.harnessFor(sf.Provider)
	turnCtx, turnCancel := context.WithCancel(s.runContext())
	active := &activeTurn{itemID: msg.itemID, cancel: turnCancel}
	if interruptible, ok := harness.(interruptibleHarness); ok {
		active.interrupt = interruptible.Interrupt
	}
	s.active.mu.Lock()
	s.active.turn = active
	s.active.mu.Unlock()
	defer func() {
		turnCancel()
		s.clearActiveTurn(active)
	}()
	result, err := harness.RunTurn(turnCtx, prompt, sf.SessionID, sf.Model, sf.Effort, live.append)
	if err == nil && result.IsError {
		err = fmt.Errorf("%s: turn failed: %s", sf.Provider, turnErrorText(result))
	}
	interrupted := s.interruptRequested(active)
	if result.Model != "" || result.Context.UsedTokens > 0 || result.Context.WindowTokens > 0 {
		model := result.Model
		// Codex's usage notification does not include the resolved model. The
		// selected session model is still the best label available for the
		// footer, and usage should not disappear just because that label is
		// absent from the provider result.
		if model == "" {
			model = sf.Model
		}
		s.setTurnInfo(msg.itemID, TurnInfo{Model: model, Context: result.Context})
	}
	if err != nil {
		if interrupted {
			s.logger.Printf("item %s: turn interrupted; retaining session recovery", msg.itemID)
			if clearErr := clearDispatchError(s.root, msg.itemID); clearErr != nil {
				s.logger.Printf("item %s: cannot clear dispatch error after interrupt: %v", msg.itemID, clearErr)
			}
		} else {
			s.logger.Printf("item %s: dispatch failed: %v", msg.itemID, err)
			if writeErr := writeDispatchError(s.root, msg.itemID, err); writeErr != nil {
				s.logger.Printf("item %s: cannot persist dispatch error: %v", msg.itemID, writeErr)
			}
		}
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
	if clearErr := clearDispatchError(s.root, msg.itemID); clearErr != nil {
		s.logger.Printf("item %s: cannot clear dispatch error after success: %v", msg.itemID, clearErr)
	}
	// A successful run whose agent never posted a turn leaves the marker
	// behind — hand it back rather than showing work that isn't happening.
	postedAgentTurn := false
	if s.store != nil {
		if item, readErr := s.store.GetItem(msg.itemID); readErr == nil {
			postedAgentTurn = len(item.Turns) > beforeTurns && len(item.Turns) > 0 && item.Turns[len(item.Turns)-1].Actor == models.ActorAgent
		}
	}
	if postedAgentTurn && len(activities) > 0 {
		ids := make([]string, len(activities))
		for i, activity := range activities {
			ids[i] = activity.ID
		}
		if err := s.store.MarkActivitiesHandled(msg.itemID, ids); err != nil {
			s.logger.Printf("item %s: cannot mark child activities handled: %v", msg.itemID, err)
		}
	}
	if len(activities) > 0 && !postedAgentTurn {
		s.revertAcknowledged(msg.itemID, models.StatusPendingAgent)
	} else {
		s.revertAcknowledged(msg.itemID, models.StatusPendingUser)
	}
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
