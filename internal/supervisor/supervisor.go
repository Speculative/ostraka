package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"

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
	s := &Supervisor{
		root:    root,
		harness: newClaudeHarness(),
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
	}
	// The store is only used to mark items as being worked on. A failure here
	// is not fatal: dispatching without the marker is better than not
	// dispatching at all, so nil is handled at the call sites.
	if st, err := store.NewStore(root); err == nil {
		s.store = st
	} else {
		s.logger.Printf("no store, status will not be marked during dispatch: %v", err)
	}
	return s
}

// Start launches the single worker goroutine that drains the queue serially,
// never running two harness turns concurrently. Safe to call once per
// Supervisor. Non-blocking.
func (s *Supervisor) Start() {
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
	for msg := range s.queue {
		s.dispatch(msg)
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

	s.markAcknowledged(msg.itemID)

	live := newLiveLog(s.root, msg.itemID)
	defer live.clear()

	result, err := s.harnessFor(sf.Provider).RunTurn(context.Background(), nudgePrompt(msg.itemID), sf.SessionID, live.append)
	if err != nil {
		s.logger.Printf("item %s: dispatch failed: %v", msg.itemID, err)
		// Put it back in the queue's state so it doesn't sit forever showing
		// as in-progress for a run that is already over.
		s.revertAcknowledged(msg.itemID, models.StatusPendingAgent)
		return
	}
	// A successful run whose agent never posted a turn leaves the marker
	// behind — hand it back rather than showing work that isn't happening.
	s.revertAcknowledged(msg.itemID, models.StatusPendingUser)
	if result.SessionID != "" {
		s.session.mu.Lock()
		current, loadErr := loadSession(s.root)
		var saveErr error
		if loadErr == nil && current == sf {
			saveErr = saveSession(s.root, sessionFile{Provider: sf.Provider, SessionID: result.SessionID})
		}
		s.session.mu.Unlock()
		if saveErr != nil {
			s.logger.Printf("item %s: failed to persist session id %s: %v", msg.itemID, result.SessionID, saveErr)
		}
	}
	s.logger.Printf("item %s: dispatch complete (session=%s is_error=%v duration_ms=%d cost_usd=%.4f num_turns=%d)",
		msg.itemID, result.SessionID, result.IsError, result.DurationMs, result.TotalCostUSD, result.NumTurns)
}
