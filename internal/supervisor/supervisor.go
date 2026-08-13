package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"ostraka/internal/models"
	"ostraka/internal/store"
)

// nudgePrompt names the item directly. It used to say "run `item list
// --status pending-agent`", which stopped working the moment dispatch began
// marking items agent-acknowledged: by the time the agent ran, the item it
// was dispatched for no longer matched the query it was told to run. The
// supervisor already knows the id, so telling the agent beats making it search.
func nudgePrompt(itemID string) string {
	return fmt.Sprintf(
		"There is new activity in ostraka on item %s. "+
			"Run `ostraka item show %s --json` to see it, "+
			"then respond via `ostraka item turn %s --actor agent \"<content>\"` "+
			"per the ostraka protocol.",
		itemID, itemID, itemID)
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

// Session returns the provider and ID that the next queued turn will use.
func (s *Supervisor) Session() (Provider, string) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	sf, err := loadSession(s.root)
	if err != nil {
		return ProviderClaude, ""
	}
	return sf.Provider, sf.SessionID
}

// StartNewSession switches harness and clears its resume cursor. It is safe to
// call during a running turn: dispatch will not let the old turn overwrite the
// freshly selected session state when it finishes.
func (s *Supervisor) StartNewSession(provider Provider) error {
	if !provider.valid() {
		return fmt.Errorf("unknown provider %q", provider)
	}
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	return saveSession(s.root, sessionFile{Provider: provider})
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
	sf, err := loadSession(s.root)
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

	result, err := s.harnessFor(sf.Provider).RunTurn(s.runContext(), nudgePrompt(msg.itemID), sf.SessionID, live.append)
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
	s.logger.Printf("item %s: dispatch complete (session=%s is_error=%v duration_ms=%d cost_usd=%.4f num_turns=%d)",
		msg.itemID, result.SessionID, result.IsError, result.DurationMs, result.TotalCostUSD, result.NumTurns)
}

// persistSession advances the resume cursor, but only if the session it was
// dispatched under is still the selected one: the user may have started a
// fresh session mid-turn, and the finishing turn must not resurrect the old.
func (s *Supervisor) persistSession(itemID string, dispatched sessionFile, sessionID string) {
	if sessionID == "" {
		return
	}
	s.session.mu.Lock()
	current, loadErr := loadSession(s.root)
	var saveErr error
	if loadErr == nil && current == dispatched {
		saveErr = saveSession(s.root, sessionFile{Provider: dispatched.Provider, SessionID: sessionID})
	}
	s.session.mu.Unlock()
	if saveErr != nil {
		s.logger.Printf("item %s: failed to persist session id %s: %v", itemID, sessionID, saveErr)
	}
}
