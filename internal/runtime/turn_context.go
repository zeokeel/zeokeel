package runtime

import (
	"errors"
	"strings"
)

// Turn context messages extend one turn's provider request with extra user-role
// messages that must follow the prompt, without changing the prompt itself.
// DSH's referenced-session snapshots are the first user of this: the host
// accepts a message citing other sessions and appends the bounded untrusted
// snapshot as a second user message right after it. The runtime keeps that
// snapshot out of the History seam (a history rewrite would duplicate the
// prompt) and instead appends it after the current prompt when building the
// request.
//
// The queue is per agent and per session: a rebuilt agent (workspace or
// permission switch) starts empty, and consuming a turn's context removes it,
// so nothing is retained across agent instances.

// QueueTurnContext registers message to follow sessionID's next prompt in the
// provider request. The controller queues before Run and consumes the message
// in the turn's prepare step; ClearTurnContext removes it again when the turn
// never reaches the provider (a synchronous Run error).
func (a *Agent) QueueTurnContext(sessionID string, message Message) error {
	if a == nil {
		return errors.New("runtime: queue turn context: nil agent")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("runtime: queue turn context: empty session id")
	}
	if message.Role != RoleUser {
		return errors.New("runtime: queue turn context: only user messages are supported")
	}
	a.turnContextMu.Lock()
	if a.turnContexts == nil {
		a.turnContexts = make(map[string][]Message)
	}
	a.turnContexts[sessionID] = append(a.turnContexts[sessionID], message)
	a.turnContextMu.Unlock()
	return nil
}

// ClearTurnContext discards any queued context for sessionID. It is safe to
// call when nothing is queued.
func (a *Agent) ClearTurnContext(sessionID string) {
	if a == nil {
		return
	}
	a.turnContextMu.Lock()
	delete(a.turnContexts, strings.TrimSpace(sessionID))
	a.turnContextMu.Unlock()
}

// takeTurnContext consumes and returns the queued context messages for one
// session. The turn prepare step calls it before compaction so a failed turn
// cannot leave a stale snapshot to leak into a later request.
func (a *Agent) takeTurnContext(sessionID string) []Message {
	if a == nil {
		return nil
	}
	a.turnContextMu.Lock()
	defer a.turnContextMu.Unlock()
	messages := a.turnContexts[sessionID]
	delete(a.turnContexts, sessionID)
	return messages
}
