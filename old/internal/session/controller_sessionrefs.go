package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Session references (DSH @deepseek-ai/dsh-session-reference parity): a user
// prompt that cites other sessions carries canonical
// @[label](dsh-session:<base64url(id)>) mentions. Submit parses them, captures a
// bounded read-only snapshot of each cited session, and passes one extra
// user-role message to the runtime, which appends it right after the prompt in
// the provider request. The prompt itself is never rewritten, the snapshot is
// persisted as its own history row so a reopen replays it, and a later turn
// only injects again when its own prompt cites sessions.

const (
	// maxReferenceMessages bounds how many of a referenced session's most
	// recent stored messages are projected before the snapshot renderer
	// applies its own byte cap. The renderer drops tool rows and binary text.
	maxReferenceMessages = 40
)

// prepareSessionReferenceContext resolves the canonical mentions in one prompt
// into the bounded untrusted context message (DSH's "## Referenced sessions").
// It returns "" when the prompt cites no session. Malformed, self, too-many and
// unreadable references are returned as presentable sessionreference errors so
// Submit fails before any turn state exists.
func (c *Controller) prepareSessionReferenceContext(ctx context.Context, targetSessionID, text string) (string, error) {
	if c.store == nil {
		return "", nil
	}
	_, references, err := sessionreference.ParseText(text)
	if err != nil {
		return "", err
	}
	if len(references) == 0 {
		return "", nil
	}
	normalized, err := sessionreference.Normalize(references, targetSessionID)
	if err != nil {
		return "", err
	}
	sources := make([]sessionreference.Source, 0, len(normalized))
	for _, reference := range normalized {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		sess, err := c.store.GetSession(reference.SessionID)
		if err != nil {
			return "", &sessionreference.Error{
				Code:    sessionreference.CodeReadFailed,
				Message: "cannot read referenced session " + reference.SessionID,
				Err:     err,
			}
		}
		messages, err := c.store.ListMessages(reference.SessionID)
		if err != nil {
			return "", &sessionreference.Error{
				Code:    sessionreference.CodeReadFailed,
				Message: "cannot read referenced session " + reference.SessionID,
				Err:     err,
			}
		}
		sources = append(sources, sessionreference.Source{
			SessionID: sess.ID,
			Label:     reference.Label,
			Cwd:       c.workspaceRootForSession(reference.SessionID),
			Messages:  projectReferenceMessages(messages),
		})
	}
	return sessionreference.RenderContext(sources)
}

// projectReferenceMessages keeps the most recent stored rows in order and maps
// the ones the snapshot renderer accepts. Tool results, reasoning and earlier
// injected reference contexts are dropped: a snapshot must not recurse into
// another session's referenced-session context, and tools are excluded like
// DSH's projected surface.
func projectReferenceMessages(messages []store.Message) []sessionreference.Message {
	start := 0
	if len(messages) > maxReferenceMessages {
		start = len(messages) - maxReferenceMessages
	}
	out := make([]sessionreference.Message, 0, len(messages)-start)
	for _, message := range messages[start:] {
		switch message.Role {
		case string(state.RoleUser), string(state.RoleAssistant):
		default:
			continue
		}
		// An earlier injected context is not the source session's own
		// conversation: never project it (DSH's anti-recursion rule).
		if strings.HasPrefix(strings.TrimSpace(message.Content), sessionreference.SectionHeading) {
			continue
		}
		out = append(out, sessionreference.Message{Role: message.Role, Text: message.Content})
	}
	return out
}

// queueSessionReferenceContext hands the rendered context to the runtime as the
// turn's second user message. Called after the agent for this session exists
// and before Run; the runtime appends it after the prompt in the request.
func (c *Controller) queueSessionReferenceContext(agent *runtime.Agent, sessionID, contextText string) error {
	if contextText == "" {
		return nil
	}
	return agent.QueueTurnContext(sessionID, runtime.Message{
		Role:    runtime.RoleUser,
		Content: contextText,
	})
}

// persistSessionReferenceContext stores the injected context as its own user
// row plus event, so the model's next request replays it from history and a
// reopened transcript shows the same conversation. The original prompt row is
// untouched and comes first.
func (c *Controller) persistSessionReferenceContext(t *turn, sessionID, contextText string) error {
	msg, err := c.store.AppendMessage(store.Message{
		SessionID: sessionID,
		Role:      string(state.RoleUser),
		Content:   contextText,
		TurnID:    t.id,
	})
	if err != nil {
		return err
	}
	if err := c.appendMessageEvent(msg); err != nil {
		return fmt.Errorf("persist session reference event: %w", err)
	}
	c.addSessionReferenceMessage(t, msg)
	return nil
}

// addSessionReferenceMessage folds the persisted context row into the live
// snapshot right after the prompt (the same Seq axis the turn's events use), so
// the running view and a later reopen agree.
func (c *Controller) addSessionReferenceMessage(t *turn, msg *store.Message) {
	c.mu.Lock()
	snap := c.views[t.sessionID]
	if snap == nil {
		snap = state.NewSnapshot()
		snap.SessionID = t.sessionID
	}
	next := *snap
	next.SessionID = t.sessionID
	next.Seq = snap.Seq + 1
	messages := make([]state.Message, len(snap.Messages), len(snap.Messages)+1)
	copy(messages, snap.Messages)
	next.Messages = append(messages, state.Message{
		MsgID:   msg.ID,
		Seq:     next.Seq,
		LastSeq: next.Seq,
		Role:    state.RoleUser,
		Text:    msg.Content,
	})
	c.views[t.sessionID] = &next
	current := c.currentID == t.sessionID
	c.mu.Unlock()
	if current {
		c.notify(t.sessionID, &next)
	}
}
