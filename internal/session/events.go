package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// Event type names persisted in store.Event.Type. They are the wire names of
// the state.Event set, plus eventMessage for a persisted user message.
const (
	EventToken           = "token"
	EventReasoning       = "reasoning"
	EventToolStart       = "tool_start"
	EventToolEnd         = "tool_end"
	EventApprovalRequest = "approval_request"
	EventUsage           = "usage"
	EventStatus          = "status"
	EventError           = "error"
	EventMessage         = "message"
	// EventQuestionAnswered records that the user answered an ask_user call.
	// Replay uses it to drop the pending question instead of resurrecting it
	// from the original tool_end row.
	EventQuestionAnswered = "question_answered"
)

// questionAnsweredPayload is the resolution row for one ask_user call.
type questionAnsweredPayload struct {
	CallID string `json:"call_id"`
}

// Wire payloads. They are explicit rather than the state structs themselves so
// the on-disk JSON (and therefore the JSONL mirror) keeps stable snake_case
// keys even if a state field is renamed.

type tokenPayload struct {
	SessionID string `json:"session_id,omitempty"`
	MsgID     string `json:"msg_id"`
	Text      string `json:"text"`
}

type reasoningPayload struct {
	SessionID string `json:"session_id,omitempty"`
	MsgID     string `json:"msg_id"`
	Text      string `json:"text"`
}

type toolStartPayload struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args,omitempty"`
}

type toolEndPayload struct {
	CallID     string `json:"call_id"`
	Result     string `json:"result"`
	IsError    bool   `json:"is_error"`
	DurationNS int64  `json:"duration_ns"`
	// Cancelled marks a call closed by the turn ending rather than by a real
	// result. Omitted when false so old traces replay unchanged.
	Cancelled bool `json:"cancelled,omitempty"`
}

type approvalPayload struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args,omitempty"`
	Kind   string          `json:"kind"`
	// ReviewReason is the automatic reviewer's recommended-denial reason.
	// Omitted for ordinary approvals and for events written before the field
	// existed, so replay stays backward compatible.
	ReviewReason string `json:"review_reason,omitempty"`
}

type usagePayload struct {
	Turns      int     `json:"turns"`
	Steps      int     `json:"steps"`
	In         int     `json:"in"`
	Out        int     `json:"out"`
	CacheHit   int     `json:"cache_hit"`
	ContextPct float64 `json:"context_pct,omitempty"`
	Advisory   string  `json:"advisory,omitempty"`
}

type statusPayload struct {
	Phase string `json:"phase"`
	// Reason explains a terminal phase ("stopped", "failed", "interrupted").
	// Omitted for a normal completion and for events written before the field
	// existed, so old traces replay unchanged.
	Reason string `json:"reason,omitempty"`
}

type errorPayload struct {
	MsgID string `json:"msg_id,omitempty"`
	Text  string `json:"text"`
}

type messagePayload struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

// appendEvent encodes ev and appends it to the session's event trace. The
// stored sequence number is allocated by the store, so concurrent turns on
// different sessions cannot interleave sequences.
func (c *Controller) appendEvent(sessionID string, ev state.Event) error {
	rec, err := encodeEvent(ev)
	if err != nil {
		return err
	}
	if _, err := c.appendStoredEvent(sessionID, rec); err != nil {
		return fmt.Errorf("session: append %s event: %w", rec.Type, err)
	}
	return nil
}

// appendStoredEvent writes one event row and mirrors it to the session's
// JSONL transcript. The mirror is best effort: SQLite stays the source of
// truth for a turn, but the documented crash-recovery/readable transcript
// exists in production instead of only in tests.
func (c *Controller) appendStoredEvent(sessionID string, rec store.Event) (*store.Event, error) {
	out, err := c.store.AppendEvent(sessionID, rec)
	if err != nil {
		return nil, err
	}
	if out != nil {
		_ = store.AppendTranscript(c.store.Dir(), sessionID, out)
	}
	return out, nil
}

// appendMessageEvent records the persisted user message as a "message" event.
// Token and tool events describe the model side of a turn; this event is what
// lets a trajectory view show the prompt that started it.
func (c *Controller) appendMessageEvent(m *store.Message) error {
	payload, err := json.Marshal(messagePayload{
		ID:        m.ID,
		Role:      m.Role,
		Content:   m.Content,
		Reasoning: m.Reasoning,
		TurnID:    m.TurnID,
	})
	if err != nil {
		return fmt.Errorf("session: encode message event: %w", err)
	}
	if _, err := c.appendStoredEvent(m.SessionID, store.Event{
		Type:    EventMessage,
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("session: append message event: %w", err)
	}
	return nil
}

// appendQuestionAnswered records a resolution event for an ask_user call so
// replay after a restart does not resurrect the answered question.
func (c *Controller) appendQuestionAnswered(sessionID, callID string) error {
	payload, err := json.Marshal(questionAnsweredPayload{CallID: callID})
	if err != nil {
		return fmt.Errorf("session: encode question answered event: %w", err)
	}
	if _, err := c.appendStoredEvent(sessionID, store.Event{
		Type:    EventQuestionAnswered,
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("session: append question answered event: %w", err)
	}
	return nil
}

// encodeEvent converts one state.Event to its store representation.
func encodeEvent(ev state.Event) (store.Event, error) {
	var (
		typ     string
		payload any
	)
	switch e := ev.(type) {
	case state.Token:
		typ = EventToken
		payload = tokenPayload{SessionID: e.SessionID, MsgID: e.MsgID, Text: e.Text}
	case state.Reasoning:
		typ = EventReasoning
		payload = reasoningPayload{SessionID: e.SessionID, MsgID: e.MsgID, Text: e.Text}
	case state.ToolStart:
		typ = EventToolStart
		payload = toolStartPayload{CallID: e.CallID, Name: e.Name, Args: e.Args}
	case state.ToolEnd:
		typ = EventToolEnd
		payload = toolEndPayload{
			CallID:     e.CallID,
			Result:     e.Result,
			IsError:    e.IsError,
			DurationNS: int64(e.Duration),
			Cancelled:  e.Cancelled,
		}
	case state.ApprovalRequest:
		typ = EventApprovalRequest
		payload = approvalPayload{
			CallID:       e.CallID,
			Name:         e.Name,
			Args:         e.Args,
			Kind:         string(e.Kind),
			ReviewReason: e.ReviewReason,
		}
	case state.Usage:
		typ = EventUsage
		payload = usagePayload{
			Turns:      e.Turns,
			Steps:      e.Steps,
			In:         e.In,
			Out:        e.Out,
			CacheHit:   e.CacheHit,
			ContextPct: e.ContextPct,
			Advisory:   e.Advisory,
		}
	case state.Status:
		typ = EventStatus
		payload = statusPayload{Phase: string(e.Phase), Reason: string(e.Reason)}
	case state.Error:
		typ = EventError
		payload = errorPayload{MsgID: e.MsgID, Text: errorText(e.Err)}
	default:
		return store.Event{}, fmt.Errorf("%w: unknown event %T", ErrInvalidArgument, ev)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return store.Event{}, fmt.Errorf("session: encode %s event: %w", typ, err)
	}
	return store.Event{Type: typ, Payload: raw}, nil
}

// decodeEvent rebuilds a state.Event from a store row. ok is false for event
// types that do not map to the state.Event set (currently "message") or for a
// malformed payload; history replay treats both as non-fatal because the
// messages table remains the source of truth for transcript text.
func decodeEvent(rec store.Event) (ev state.Event, ok bool) {
	switch rec.Type {
	case EventToken:
		var p tokenPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.Token{SessionID: p.SessionID, MsgID: p.MsgID, Text: p.Text}, true
	case EventReasoning:
		var p reasoningPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.Reasoning{SessionID: p.SessionID, MsgID: p.MsgID, Text: p.Text}, true
	case EventToolStart:
		var p toolStartPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.ToolStart{CallID: p.CallID, Name: p.Name, Args: p.Args}, true
	case EventToolEnd:
		var p toolEndPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.ToolEnd{
			CallID:    p.CallID,
			Result:    p.Result,
			IsError:   p.IsError,
			Duration:  time.Duration(p.DurationNS),
			Cancelled: p.Cancelled,
		}, true
	case EventApprovalRequest:
		var p approvalPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.ApprovalRequest{
			CallID:       p.CallID,
			Name:         p.Name,
			Args:         p.Args,
			Kind:         state.ApprovalKind(p.Kind),
			ReviewReason: p.ReviewReason,
		}, true
	case EventUsage:
		var p usagePayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.Usage{
			Turns:      p.Turns,
			Steps:      p.Steps,
			In:         p.In,
			Out:        p.Out,
			CacheHit:   p.CacheHit,
			ContextPct: p.ContextPct,
			Advisory:   p.Advisory,
		}, true
	case EventStatus:
		var p statusPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		return state.Status{Phase: state.Phase(p.Phase), Reason: state.StatusReason(p.Reason)}, true
	case EventError:
		var p errorPayload
		if json.Unmarshal(rec.Payload, &p) != nil {
			return nil, false
		}
		var err error
		if p.Text != "" {
			err = errors.New(p.Text)
		}
		return state.Error{MsgID: p.MsgID, Err: err}, true
	default:
		return nil, false
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
