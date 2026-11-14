package contracts

// Prompt queue (DSH inbox parity).
//
// A user prompt sent while a session is running waits in the session's durable
// inbox instead of being rejected or being queued in the desktop UI module. The
// agent backend owns the queue: it drains one prompt when the current run
// settles and only then considers an automatic goal round, so human input
// always wins over a goal continuation and a queue survives a restart.
//
// The optional interface is type-asserted on ChatService, like the other chat
// halves; a host that does not implement it keeps the old conflict behavior.

import (
	"context"
	"time"
)

// QueuedPrompt is one human prompt waiting in a session's inbox.
type QueuedPrompt struct {
	// ID identifies the queued prompt (stable across restarts).
	ID string
	// SessionID is the conversation the prompt belongs to.
	SessionID string
	// Text is the prompt body the user sent.
	Text string
	// CreatedAt is when the prompt was accepted.
	CreatedAt time.Time
}

// PromptQueue is the optional backend half of ChatService that owns prompts
// waiting for a running session to settle.
type PromptQueue interface {
	// EnqueuePrompt appends one prompt to the session inbox and returns the
	// durable entry. It rejects an empty prompt or an unknown session.
	EnqueuePrompt(ctx context.Context, sessionID, prompt string) (QueuedPrompt, error)
	// QueuedPrompts returns the session's inbox in send order (oldest first).
	QueuedPrompts(sessionID string) []QueuedPrompt
	// RemoveQueuedPrompt removes one queued prompt by ID (idempotent).
	RemoveQueuedPrompt(ctx context.Context, sessionID, id string) error
}
