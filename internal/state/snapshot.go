package state

import (
	"encoding/json"
	"time"
)

// Phase is a session phase from docs/PLAN.md §4.5.
type Phase string

const (
	// PhaseIdle is a session with no turn in flight.
	PhaseIdle Phase = "idle"
	// PhaseRunning is a session with a turn streaming or executing.
	PhaseRunning Phase = "running"
	// PhaseWaitingApproval is a session blocked on a tool approval.
	PhaseWaitingApproval Phase = "waiting_approval"
	// PhaseWaitingUser is a session blocked on a user answer.
	PhaseWaitingUser Phase = "waiting_user"
	// PhasePaused is a session the user paused.
	PhasePaused Phase = "paused"
	// PhaseFailed is a session whose last turn failed.
	PhaseFailed Phase = "failed"
)

// Role is a message author role.
type Role string

const (
	// RoleUser is a message authored by the user.
	RoleUser Role = "user"
	// RoleAssistant is a message authored by the model. Token and Reasoning
	// events always create assistant messages.
	RoleAssistant Role = "assistant"
	// RoleSystem is an injected system message.
	RoleSystem Role = "system"
	// RoleTool is a tool result message.
	RoleTool Role = "tool"
)

// Attachment is one user-visible file chip. ID is the durable attachment row
// id; Name is the display label. Pending attachments belong to
// Snapshot.Attachments, sent attachments belong to the user Message they were
// submitted with.
type Attachment struct {
	ID        string
	Name      string
	MIME      string
	Kind      string
	Size      int64
	MessageID string
}

// Message is one rendered message in the snapshot. Messages are immutable
// values; a fold that grows a message copies the slice and the one message it
// changes, so previously published snapshots never observe the update.
type Message struct {
	MsgID     string
	Role      Role
	Text      string
	Reasoning string
	// Attachments are the files sent with a user message.
	Attachments []Attachment
}

// ToolStatus is a tool call's state, derived from the most recent tool event
// for that CallID (see docs/PLAN.md §4.5).
type ToolStatus string

const (
	// ToolPending is a call announced but not started.
	ToolPending ToolStatus = "pending"
	// ToolAwaitingApproval is a call blocked on user approval.
	ToolAwaitingApproval ToolStatus = "awaiting_approval"
	// ToolRunning is a call executing.
	ToolRunning ToolStatus = "running"
	// ToolDone is a call that finished successfully.
	ToolDone ToolStatus = "done"
	// ToolError is a call that finished with an error.
	ToolError ToolStatus = "error"
	// ToolDenied is a call the user denied. Reserved for runtimes that publish
	// an approval decision; the current event set has no decision event.
	ToolDenied ToolStatus = "denied"
	// ToolCancelled is a call cancelled before completion. Reserved for
	// runtimes that publish a cancellation event.
	ToolCancelled ToolStatus = "cancelled"
)

// ToolState is one tool call as the UI sees it.
type ToolState struct {
	CallID string
	Name   string
	Args   json.RawMessage
	Status ToolStatus

	Result   string
	IsError  bool
	Duration time.Duration

	// StartedSeq is the snapshot Seq of the most recent ToolStart. It stays 0
	// when only an ApprovalRequest has been seen for the call.
	StartedSeq uint64
	// UpdatedSeq is the snapshot Seq of the event that last changed the state.
	UpdatedSeq uint64
}

// ErrorInfo is an immutable rendering of an Error event.
type ErrorInfo struct {
	MsgID string
	Text  string
}

// Snapshot is the immutable UI state for one session. The view reads it once
// per frame; producers never mutate a published snapshot.
//
// Do not modify any field, and do not modify Messages or Tools: the pointer
// stays live in an atomic.Pointer that other goroutines read concurrently.
type Snapshot struct {
	SessionID string
	Phase     Phase
	Messages  []Message
	Tools     map[string]ToolState
	// Attachments are the files staged for the next turn. They move onto the
	// user Message when that turn is sent.
	Attachments []Attachment
	// Feedback maps a message id to its newest rating ("good"/"bad"). It is
	// loaded from the store when a session is opened and updated when the
	// user rates a message, so the action bar reflects the persisted state.
	Feedback  map[string]string
	Usage     Usage
	LastError *ErrorInfo
	Seq       uint64
}

// NewSnapshot returns an empty snapshot in PhaseIdle with a non-nil Tools map.
func NewSnapshot() *Snapshot {
	return &Snapshot{
		Phase: PhaseIdle,
		Tools: make(map[string]ToolState),
	}
}

// Apply folds ev into prev and returns the resulting snapshot. It never mutates
// prev (or any slice/map reachable from it): changed collections are copied, so
// previously published snapshots stay valid. A nil prev starts from
// NewSnapshot; a nil ev returns prev unchanged.
//
// Folding rules:
//
//	Token           append Text to message MsgID (creating an assistant message)
//	Reasoning       append Text to message MsgID's Reasoning field
//	ToolStart       set call CallID to ToolRunning, record Name/Args/StartedSeq
//	ApprovalRequest set call CallID to ToolAwaitingApproval
//	ToolEnd         set call CallID to ToolDone, or ToolError when IsError
//	Usage           replace Snapshot.Usage with the event's counters
//	Status          replace Snapshot.Phase
//	Error           replace Snapshot.LastError with immutable error info
//
// Seq is incremented once per applied event. Messages keep the order in which
// their MsgID first appeared. Token and Reasoning events carrying a non-empty
// SessionID set Snapshot.SessionID (the last such event wins).
func Apply(prev *Snapshot, ev Event) *Snapshot {
	if ev == nil {
		if prev != nil {
			return prev
		}
		return NewSnapshot()
	}
	if prev == nil {
		prev = NewSnapshot()
	}
	next := *prev
	next.Seq = prev.Seq + 1

	switch e := ev.(type) {
	case Token:
		if e.SessionID != "" {
			next.SessionID = e.SessionID
		}
		next.Messages = appendMessageText(prev.Messages, e.MsgID, e.Text)
	case Reasoning:
		if e.SessionID != "" {
			next.SessionID = e.SessionID
		}
		next.Messages = appendMessageReasoning(prev.Messages, e.MsgID, e.Text)
	case ToolStart:
		next.Tools = updateTool(prev.Tools, e.CallID, func(st *ToolState) {
			st.Name = e.Name
			st.Args = cloneJSON(e.Args)
			st.Status = ToolRunning
			st.StartedSeq = next.Seq
			st.UpdatedSeq = next.Seq
		})
	case ToolEnd:
		next.Tools = updateTool(prev.Tools, e.CallID, func(st *ToolState) {
			st.Result = e.Result
			st.IsError = e.IsError
			st.Duration = e.Duration
			if e.IsError {
				st.Status = ToolError
			} else {
				st.Status = ToolDone
			}
			st.UpdatedSeq = next.Seq
		})
	case ApprovalRequest:
		next.Tools = updateTool(prev.Tools, e.CallID, func(st *ToolState) {
			if e.Name != "" {
				st.Name = e.Name
			}
			if len(e.Args) > 0 {
				st.Args = cloneJSON(e.Args)
			}
			st.Status = ToolAwaitingApproval
			st.UpdatedSeq = next.Seq
		})
	case Usage:
		next.Usage = e
	case Status:
		next.Phase = e.Phase
	case Error:
		next.LastError = &ErrorInfo{MsgID: e.MsgID, Text: errorText(e.Err)}
	default:
		// Event is a closed set; an unknown implementation can only appear via
		// type embedding. Advance Seq so replay stays aligned, change nothing.
	}

	return &next
}

func appendMessageText(msgs []Message, msgID, text string) []Message {
	return updateMessage(msgs, msgID, func(m *Message) { m.Text += text })
}

func appendMessageReasoning(msgs []Message, msgID, text string) []Message {
	return updateMessage(msgs, msgID, func(m *Message) { m.Reasoning += text })
}

// updateMessage copies msgs, appends msgID if missing, and applies mutate to
// the single affected message.
func updateMessage(prev []Message, msgID string, mutate func(*Message)) []Message {
	for i := range prev {
		if prev[i].MsgID != msgID {
			continue
		}
		out := make([]Message, len(prev))
		copy(out, prev)
		mutate(&out[i])
		return out
	}
	msg := Message{MsgID: msgID, Role: RoleAssistant}
	mutate(&msg)
	out := make([]Message, len(prev), len(prev)+1)
	copy(out, prev)
	return append(out, msg)
}

// updateTool copies the tools map and applies mutate to callID, creating the
// entry when absent.
func updateTool(prev map[string]ToolState, callID string, mutate func(*ToolState)) map[string]ToolState {
	out := make(map[string]ToolState, len(prev)+1)
	for id, st := range prev {
		out[id] = st
	}
	st := prev[callID]
	st.CallID = callID
	mutate(&st)
	out[callID] = st
	return out
}

// cloneJSON copies b so later producer-side mutation of the argument buffer
// cannot change a published snapshot.
func cloneJSON(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	out := make(json.RawMessage, len(b))
	copy(out, b)
	return out
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
