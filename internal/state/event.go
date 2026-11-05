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
	// Cancelled reports that the call was closed by the turn ending (user Stop,
	// cancellation or crash recovery) instead of returning a real result. The
	// snapshot renders it as ToolCancelled and the persisted tool_calls row uses
	// the "cancelled" status, so a stopped turn no longer leaves a card spinning
	// forever or an open call in the next turn's history.
	Cancelled bool
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
	// ReviewReason, when non-empty, is the automatic reviewer's reason for
	// recommending denial. The call still waits for the user's decision: Allow
	// runs it, Deny rejects it. Empty for ordinary approvals and for events
	// persisted before this field existed.
	ReviewReason string
}

func (ApprovalRequest) isEvent() {}

// Usage is a delta on the session's cumulative counters. The projector adds
// Turns, Steps, In, Out and CacheHit to Snapshot.Usage and treats ContextPct as
// a gauge (the latest non-zero value wins). The runtime publishes one delta per
// completed model call, so the snapshot doubles as the live footer the moment
// work starts; summing the persisted usage events also rebuilds the same
// cumulative totals on reopen. Event traces written before this contract
// carried one whole-turn sample per successful turn with Turns=1; summing those
// also yields the session totals, so old sessions replay correctly.
type Usage struct {
	Turns      int
	Steps      int
	In         int
	Out        int
	CacheHit   int
	ContextPct float64
	// Advisory is the latest cost/budget advisory the runtime raised for the
	// current turn ("" when none). It lets the footer surface the same warning
	// the model received without inventing a second counter.
	Advisory string
}

func (Usage) isEvent() {}

// StatusReason explains how the latest turn ended. The zero value means a turn
// completed normally (or no turn has ended yet); it is persisted with the
// terminal Status event and survives a reopen.
type StatusReason string

const (
	// ReasonNone is the zero value: the turn completed normally, or no reason
	// was recorded (events persisted by older builds).
	ReasonNone StatusReason = ""
	// ReasonStopped is a turn the user stopped (or the app cancelled).
	ReasonStopped StatusReason = "stopped"
	// ReasonFailed is a turn that failed with an error.
	ReasonFailed StatusReason = "failed"
	// ReasonInterrupted is a turn left running by a process that exited; its
	// open tool calls were closed by crash recovery on the next open.
	ReasonInterrupted StatusReason = "interrupted"
)

// Status sets the session phase. Status is the only event that changes
// Snapshot.Phase. Reason is set on a terminal Status to explain how the latest
// turn ended; the zero value keeps older events and normal completions
// unchanged.
type Status struct {
	Phase  Phase
	Reason StatusReason
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
