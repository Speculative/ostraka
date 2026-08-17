package models

import "time"

type Channel string
type ItemType string
type Status string
type Actor string

const (
	ChannelInbox Channel = "inbox"
)

const (
	TypeThread ItemType = "thread"
	TypeDoc    ItemType = "doc"
)

const (
	StatusBacklog      Status = "backlog"
	StatusActive       Status = "active"
	StatusPendingUser  Status = "pending-user"
	StatusPendingAgent Status = "pending-agent"
	StatusProposed     Status = "proposed"
	// StatusAgentAcknowledged sits between pending-agent and the agent's
	// reply: the supervisor sets it when a dispatch starts, so a turn that is
	// being worked on is distinguishable from one still sitting in the queue.
	StatusAgentAcknowledged Status = "agent-acknowledged"
	StatusDone              Status = "done"
	StatusArchived          Status = "archived"
)

const (
	ActorUser  Actor = "user"
	ActorAgent Actor = "agent"
)

var Channels = []Channel{ChannelInbox}

// ValidChannel reports whether channel is part of the supported item schema.
func ValidChannel(channel Channel) bool {
	return channel == ChannelInbox
}

var TerminalStatuses = map[Status]bool{
	StatusDone:     true,
	StatusArchived: true,
}

type Turn struct {
	Actor     Actor
	Timestamp time.Time
	Content   string
}

// Activity is structured metadata about a related conversation. Activities
// are persisted separately from turns so lifecycle notifications do not
// masquerade as something a user or agent said.
type Activity struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	ChildID    string    `json:"child_id,omitempty"`
	ChildTitle string    `json:"child_title,omitempty"`
	Result     string    `json:"result,omitempty"`
	Actor      Actor     `json:"actor"`
	Timestamp  time.Time `json:"timestamp"`
	Handled    bool      `json:"handled"`
}

type Item struct {
	ID      string
	Channel Channel
	Type    ItemType
	Status  Status
	Created time.Time
	Parent  string // empty if none
	Related []string
	// Title is a single line: it is the item's label in list views, where a
	// multi-line one would crowd out every other row. Body carries the
	// opening description at whatever length it needs.
	Title string
	Body  string
	Turns []Turn
}
