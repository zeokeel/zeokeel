package contracts

import (
	"context"
	"time"
)

// MessageSearchHit is one full-text search result across the stored sessions
// (the old shell's session search).
type MessageSearchHit struct {
	// SessionID is the session the message belongs to.
	SessionID string
	// SessionTitle is that session's display title (for the result row).
	SessionTitle string
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
