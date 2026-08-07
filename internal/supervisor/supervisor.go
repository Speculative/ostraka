package supervisor

import (
	"context"
	"log"
	"os"
)

const nudgePrompt = "There is new activity in ostraka (a pending-agent item). " +
	"Run `ostraka item list --status pending-agent --json` to see what changed, " +
	"then respond via `ostraka item turn <id> --actor agent \"<content>\"` per the ostraka protocol."

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
	queue   chan enqueueMsg
	logger  *log.Logger
}

// New constructs a Supervisor rooted at the given .ostraka directory using
// the default (Claude Code) harness. It does not start the worker goroutine
// — call Start for that.
func New(root string) *Supervisor {
	os.MkdirAll(supervisorDir(root), 0755) //nolint:errcheck
	return &Supervisor{
		root:    root,
		harness: newClaudeHarness(),
		queue:   make(chan enqueueMsg, queueCapacity),
		logger:  newLogger(root),
	}
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

func (s *Supervisor) dispatch(msg enqueueMsg) {
	sessionID, err := loadSessionID(s.root)
	if err != nil {
		s.logger.Printf("item %s: failed to load session id, starting fresh: %v", msg.itemID, err)
		sessionID = ""
	}

	if sessionID == "" {
		s.logger.Printf("item %s: dispatching (fresh session)", msg.itemID)
	} else {
		s.logger.Printf("item %s: dispatching (resuming session %s)", msg.itemID, sessionID)
	}

	result, err := s.harness.RunTurn(context.Background(), nudgePrompt, sessionID)
	if err != nil {
		s.logger.Printf("item %s: dispatch failed: %v", msg.itemID, err)
		return
	}
	if result.SessionID != "" {
		if saveErr := saveSessionID(s.root, result.SessionID); saveErr != nil {
			s.logger.Printf("item %s: failed to persist session id %s: %v", msg.itemID, result.SessionID, saveErr)
		}
	}
	s.logger.Printf("item %s: dispatch complete (session=%s is_error=%v duration_ms=%d cost_usd=%.4f num_turns=%d)",
		msg.itemID, result.SessionID, result.IsError, result.DurationMs, result.TotalCostUSD, result.NumTurns)
}
