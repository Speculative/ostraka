package models

import "time"

type Channel string
type ItemType string
type Status string
type Actor string

const (
	ChannelInbox   Channel = "inbox"
	ChannelAsks    Channel = "asks"
	ChannelHandoff Channel = "handoff"
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
	StatusDone         Status = "done"
	StatusArchived     Status = "archived"
)

const (
	ActorUser  Actor = "user"
	ActorAgent Actor = "agent"
)

var Channels = []Channel{ChannelInbox, ChannelAsks, ChannelHandoff}

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
	Body    string
	Turns   []Turn
}
