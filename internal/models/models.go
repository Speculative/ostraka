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

type Item struct {
	ID      string
	Channel Channel
	Type    ItemType
	Status  Status
	Created time.Time
	Parent  string // empty if none
	// Title is a single line: it is the item's label in list views, where a
	// multi-line one would crowd out every other row. Body carries the
	// opening description at whatever length it needs.
	Title string
	Body  string
	Turns []Turn
}
