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
	"errors"
	"time"
)

// ErrQueueDraining is returned when a reorder arrives after the session went
// idle: the queue is about to be consumed, so there is nothing to reorder. The
// gateway maps it to a retryable not-ready error.
var ErrQueueDraining = errors.New("prompt queue is being drained")

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

// PromptMover is the optional reorder half. It is a separate interface so a host
// that only implements PromptQueue keeps compiling; the gateway type-asserts it
// and answers `prompts.move` only when it is present.
type PromptMover interface {
	// MoveQueuedPrompt puts the prompt at the given zero-based position in the
	// session inbox, keeping its ID. An out-of-range index clamps to the ends.
	MoveQueuedPrompt(ctx context.Context, sessionID, id string, index int) error
}
