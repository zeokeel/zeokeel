package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// buildSnapshot reconstructs a session's view from the store.
//
// Messages come from the messages table, which is the source of truth for the
// transcript: it holds both user and assistant messages, whereas a Token event
// can only create an assistant message. Events then rebuild the parts of the
// snapshot that are not messages: tool cards, usage counters, the last error.
// Token and Reasoning events are skipped because their text is already in the
// messages table; folding them too would duplicate assistant text.
//
// The session row's status is authoritative for Phase: a process that died
// mid-turn must not reopen a session that still looks "running".
func (c *Controller) buildSnapshot(sess *store.Session) (*state.Snapshot, error) {
	messages, err := c.store.ListMessages(sess.ID)
	if err != nil {
		return nil, fmt.Errorf("session: load messages for %q: %w", sess.ID, err)
	}

	snap := state.NewSnapshot()
	snap.SessionID = sess.ID
	if len(messages) > 0 {
		snap.Messages = make([]state.Message, 0, len(messages))
	}
	for _, m := range messages {
		snap.Messages = append(snap.Messages, state.Message{
			MsgID:     m.ID,
			Role:      state.Role(m.Role),
			Text:      m.Content,
			Reasoning: m.Reasoning,
		})
	}

	// Reattach every persisted file: pending rows stay in the composer's chip
	// list, sent rows move onto the user message they were submitted with.
	if attachments, err := c.store.ListAttachments(sess.ID); err == nil {
		byMessage := make(map[string][]state.Attachment)
		for _, a := range attachments {
			chip := stateAttachment(a)
			if strings.TrimSpace(a.MessageID) == "" {
				snap.Attachments = append(snap.Attachments, chip)
				continue
			}
			byMessage[a.MessageID] = append(byMessage[a.MessageID], chip)
		}
		if len(byMessage) > 0 {
			for i := range snap.Messages {
				if chips := byMessage[snap.Messages[i].MsgID]; len(chips) > 0 {
					snap.Messages[i].Attachments = chips
				}
			}
		}
	}

	// Feedback is a separate append-only table, so a reopened session has to
	// reattach the newest rating to each message for the action bar state.
	if ratings, err := c.store.FeedbackByMessage(sess.ID); err == nil && len(ratings) > 0 {
		snap.Feedback = make(map[string]string, len(ratings))
		for messageID, fb := range ratings {
			snap.Feedback[messageID] = fb.Rating
		}
	}

	events, err := c.store.ListEvents(sess.ID)
	if err != nil {
		return nil, fmt.Errorf("session: load events for %q: %w", sess.ID, err)
	}
	// Tool results are also folded back into the controller's derived state
	// (todos, goal, questions, artifacts, team). The snapshot alone does not
	// carry those maps, so without this replay a reopened session would lose
	// its panels even though the events are persisted.
	type replayedCall struct {
		callID string
		name   string
		args   json.RawMessage
		result string
		isErr  bool
		at     time.Time
	}
	calls := make(map[string]string)
	args := make(map[string]json.RawMessage)
	answeredQuestions := make(map[string]bool)
	var results []replayedCall
	for _, rec := range events {
		if rec.Type == EventQuestionAnswered {
			var p questionAnsweredPayload
			if json.Unmarshal(rec.Payload, &p) == nil && strings.TrimSpace(p.CallID) != "" {
				answeredQuestions[p.CallID] = true
			}
			continue
		}
		ev, ok := decodeEvent(rec)
		if !ok {
			continue
		}
		switch e := ev.(type) {
		case state.Token, state.Reasoning:
			continue
		case state.ToolStart:
			calls[e.CallID] = e.Name
			if e.Name == "subagent" {
				args[e.CallID] = append(json.RawMessage(nil), e.Args...)
			}
		case state.ToolEnd:
			if name := calls[e.CallID]; name != "" {
				results = append(results, replayedCall{
					callID: e.CallID, name: name, args: args[e.CallID],
					result: e.Result, isErr: e.IsError, at: rec.TS,
				})
				delete(calls, e.CallID)
				delete(args, e.CallID)
			}
		}
		snap = state.Apply(snap, ev)
	}
	if len(results) > 0 {
		c.mu.Lock()
		for _, r := range results {
			if r.isErr {
				continue
			}
			if r.name == "subagent" {
				c.recordTeammateAtLocked(sess.ID, r.callID, r.args, r.result, r.at)
				continue
			}
			c.applyToolResultAtLocked(sess.ID, r.callID, r.name, r.result, r.at)
		}
		for callID := range answeredQuestions {
			delete(c.questions[sess.ID], callID)
		}
		c.mu.Unlock()
	}

	// Copy before setting the derived fields so the snapshot handed back is
	// immutable and never aliases a value that apply could mutate.
	out := *snap
	out.SessionID = sess.ID
	out.Phase = phaseFromStore(sess.Status)
	return &out, nil
}

// phaseFromStore maps a persisted session status to the UI phase.
func phaseFromStore(status store.SessionStatus) state.Phase {
	switch status {
	case store.SessionRunning:
		return state.PhaseRunning
	case store.SessionWaitingApproval:
		return state.PhaseWaitingApproval
	case store.SessionWaitingUser:
		return state.PhaseWaitingUser
	case store.SessionPaused:
		return state.PhasePaused
	case store.SessionFailed:
		return state.PhaseFailed
	default:
		return state.PhaseIdle
	}
}

// phaseToStore maps a terminal phase to the session status persisted with it.
// Only terminal phases are queried; transitional phases are written directly
// as store.SessionRunning.
func phaseToStore(phase state.Phase, failed bool) store.SessionStatus {
	if phase == state.PhaseFailed || failed {
		return store.SessionFailed
	}
	return store.SessionIdle
}
