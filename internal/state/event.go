package state

import (
	"encoding/json"
	"time"
)

// Event is one runtime event in the stream that drives the session UI.
//
// Event is a closed set: only the concrete value types declared in this file
// implement Event. Runtime code publishes them through Broker.Publish; the
// projector folds them, in publish order, into an immutable Snapshot.
type Event interface {
	isEvent()
}

// Token is a chunk of assistant-visible text for message MsgID. Successive
// tokens with the same MsgID are appended in publish order.
type Token struct {
	SessionID string
	MsgID     string
	Text      string
}

func (Token) isEvent() {}

// Reasoning is a chunk of model reasoning text for message MsgID. Reasoning is
// accumulated separately from Token text and is never mixed into Message.Text.
type Reasoning struct {
	SessionID string
	MsgID     string
	Text      string
}

func (Reasoning) isEvent() {}

// ToolStart announces that tool call CallID (tool Name, raw JSON Args) has
// begun executing.
type ToolStart struct {
	CallID string
	Name   string
	Args   json.RawMessage
}

func (ToolStart) isEvent() {}

// ToolEnd reports that tool call CallID finished. Duration is the measured
// execution time and IsError reports a failed or non-zero-exit call.
type ToolEnd struct {
	CallID   string
	Result   string
	IsError  bool
	Duration time.Duration
}

func (ToolEnd) isEvent() {}

// ApprovalKind classifies what an ApprovalRequest is asking the user to allow.
type ApprovalKind string

const (
	// ApprovalCommand is a shell / process execution approval.
	ApprovalCommand ApprovalKind = "command"
	// ApprovalFileWrite is a file create / modify / delete approval.
	ApprovalFileWrite ApprovalKind = "file_write"
	// ApprovalNetwork is an outbound network access approval.
	ApprovalNetwork ApprovalKind = "network"
	// ApprovalOther is any approval that does not fit the categories above.
	ApprovalOther ApprovalKind = "other"
)

// ApprovalRequest announces that tool call CallID is waiting for the user's
// approval. Name and Args describe the pending call.
type ApprovalRequest struct {
	CallID string
	Name   string
	Args   json.RawMessage
	Kind   ApprovalKind
}

func (ApprovalRequest) isEvent() {}

// Usage replaces the snapshot's usage counters with a consistent point-in-time
// measurement. Values are copied as-is; the projector does not clamp them.
type Usage struct {
	Turns      int
	Steps      int
	In         int
	Out        int
	CacheHit   int
	ContextPct float64
}

func (Usage) isEvent() {}

// Status sets the session phase. Status is the only event that changes
// Snapshot.Phase.
type Status struct {
	Phase Phase
}

func (Status) isEvent() {}

// Error records the most recent session error for message MsgID. The snapshot
// keeps the error text (ErrorInfo.Text), not the error value, so snapshots stay
// immutable and safe to read after the producer's error is gone.
type Error struct {
	MsgID string
	Err   error
}

func (Error) isEvent() {}
