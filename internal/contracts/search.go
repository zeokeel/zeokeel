package contracts

import (
	"context"
	"strconv"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// MessageSearchHit is one full-text search result across the stored sessions
// (the old shell's session search).
type MessageSearchHit struct {
	// SessionID is the session the message belongs to.
	SessionID string
	// SessionTitle is that session's display title (for the result row).
	SessionTitle string
	// MessageID locates the message durably within its session. The stored
	// history is the agent's rig message payload, which carries no in-memory
	// chat id, so the store emits its stable 1-based position
	// (MessageOrdinalLocator); the conversation screen resolves it against the
	// transcript. A backend that does keep real message ids may put them here
	// instead, and the screen accepts those too.
	MessageID string
	// Role is the message role (user/assistant/tool).
	Role string
	// Snippet is a short excerpt around the match.
	Snippet string
	// CreatedAt is the message timestamp.
	CreatedAt time.Time
}

// MessageSearcher is the optional durable half that searches message text
// across all sessions. UI modules type-assert contracts.Store so stores that
// predate search keep working.
type MessageSearcher interface {
	// SearchMessages finds messages whose text contains query
	// (case-insensitive), newest first, bounded by limit.
	SearchMessages(ctx context.Context, query string, limit int) ([]MessageSearchHit, error)
}

// MessageOrdinalLocatorPrefix marks a durable message locator that is the
// message's 1-based position in its session's stored history. The agent's rig
// history has no message ids, so the position is the one durable identity a
// search hit and the conversation transcript can agree on across a restart.
const MessageOrdinalLocatorPrefix = "ordinal:"

// MessageOrdinalLocator encodes a 1-based stored-history position as the
// durable locator carried by MessageSearchHit.MessageID.
func MessageOrdinalLocator(ordinal int) string {
	if ordinal <= 0 {
		return ""
	}
	return MessageOrdinalLocatorPrefix + strconv.Itoa(ordinal)
}

// ParseMessageOrdinalLocator reads a locator made by MessageOrdinalLocator. It
// reports false for any other string (a live message id, an empty locator), so
// a caller can fall back to matching the in-memory id first.
func ParseMessageOrdinalLocator(locator string) (int, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(locator), MessageOrdinalLocatorPrefix)
	if !ok {
		return 0, false
	}
	ordinal, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || ordinal <= 0 {
		return 0, false
	}
	return ordinal, true
}

// SessionFocus is the optional cross-module handoff from a search surface (the
// sidebar's message hits) to the conversation screen: it opens a session at one
// message. The provider is the session UI module; a host without it keeps the
// plain "open the conversation" click. Call it on the UI thread.
type SessionFocus interface {
	// FocusMessage asks the conversation screen to show sessionID and scroll
	// its transcript to messageID on the next frame. messageID is a durable
	// locator from MessageSearchHit.MessageID (or a live chat message id); a
	// target the screen cannot resolve is ignored.
	FocusMessage(sessionID, messageID string)
}

// KeySessionFocus is provided by the session UI module and consumed by search
// surfaces that can locate one message. It is optional: consumers must keep
// working when the key is absent.
var KeySessionFocus = cordis.NewServiceKey[SessionFocus]("zeokeel/session-focus")
