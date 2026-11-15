package agent

// Compaction archive (fail-closed raw-history preservation).
//
// Compaction replaces the session's message rows; without an archive the raw
// conversation (including tool calls/results) would be gone forever. Before the
// replacement the backend writes one bounded durable `compact_archive` event
// carrying the raw messages that will be replaced:
//
//   - the event is bounded (2 MiB and 200 messages): past the bound the payload
//     records how many messages were omitted (truncated=true) instead of
//     silently dropping the fact;
//   - the compaction only replaces the history AFTER the archive event was
//     appended durably: a failed archive aborts the compaction and keeps the
//     original history (fail-closed);
//   - the context inspector shows the archive metadata (kind=archive, counts,
//     bytes, truncated); the archived message text is never injected into the
//     read model.
//
// The archive is an append-only audit record: it is intentionally not pruned by
// this task (each event is bounded; the store own cleanup policy applies).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
)

const (
	// compactArchiveEventType is the durable archive event.
	compactArchiveEventType = "compact_archive"
	// compactArchiveMaxBytes bounds one archive event payload.
	compactArchiveMaxBytes = 2 << 20
	// compactArchiveMaxMessages bounds the message count of one archive event.
	compactArchiveMaxMessages = 200
)

// compactArchivePayload is the durable compact_archive event payload.
type compactArchivePayload struct {
	// Kind is "manual" (/compact) or "recovery" (context-overflow recovery).
	Kind string `json:"kind"`
	// MessageCount is how many raw messages the replaced history had.
	MessageCount int `json:"messageCount"`
	// RetainedMessages is how many of them fit in this bounded archive.
	RetainedMessages int `json:"retainedMessages"`
	// Bytes is the serialized size of the retained messages.
	Bytes int `json:"bytes"`
	// LimitBytes is the bound that was applied.
	LimitBytes int `json:"limitBytes"`
	// Truncated reports that some messages were omitted because of the bound.
	Truncated bool `json:"truncated"`
	// OmittedMessages is the omitted count (0 when not truncated).
	OmittedMessages int `json:"omittedMessages,omitempty"`
	// BeforeTokens/AfterTokens/Replaced/Saved mirror the compaction audit.
	BeforeTokens int `json:"beforeTokens"`
	AfterTokens  int `json:"afterTokens"`
	Replaced     int `json:"replaced"`
	Saved        int `json:"saved"`
	// Messages are the raw replaced messages (bounded).
	Messages []json.RawMessage `json:"messages,omitempty"`
}

// buildCompactArchive serializes the raw history for the durable archive within
// the size/count bounds; the truncation metadata is always recorded.
func buildCompactArchive(kind string, messages []rig.Message, beforeTokens, afterTokens, replaced, saved int) compactArchivePayload {
	payload := compactArchivePayload{
		Kind:         kind,
		MessageCount: len(messages),
		LimitBytes:   compactArchiveMaxBytes,
		BeforeTokens: beforeTokens,
		AfterTokens:  afterTokens,
		Replaced:     replaced,
		Saved:        saved,
	}
	totalBytes := 0
	for _, message := range messages {
		if len(payload.Messages) >= compactArchiveMaxMessages {
			break
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			// A message that cannot be serialized is omitted; the count below
			// records the gap, never silently pretends the archive is complete.
			continue
		}
		if totalBytes+len(encoded) > compactArchiveMaxBytes {
			break
		}
		payload.Messages = append(payload.Messages, encoded)
		totalBytes += len(encoded)
	}
	payload.RetainedMessages = len(payload.Messages)
	payload.Bytes = totalBytes
	payload.OmittedMessages = len(messages) - len(payload.Messages)
	payload.Truncated = payload.OmittedMessages > 0
	return payload
}

// archiveReplacedHistory writes the durable archive of the messages a
// compaction is about to replace. It returns an error when the archive could not
// be written, and the caller must then keep the original history (fail-closed).
// A service without a durable store has nothing to archive into; the in-memory
// fallback keeps the previous behavior (documented, not silent data loss of
// durable data).
func (s *service) archiveReplacedHistory(ctx context.Context, sessionID, kind string,
	messages []rig.Message, beforeTokens, afterTokens, replaced, saved int) error {
	if s.store == nil {
		return nil
	}
	if s.sessionDeleted(sessionID) {
		return fmt.Errorf("agent: session %q was deleted", sessionID)
	}
	payload := buildCompactArchive(kind, messages, beforeTokens, afterTokens, replaced, saved)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("agent: encode compact archive: %w", err)
	}
	at := time.Now()
	if err := s.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: sessionID,
		Type:      compactArchiveEventType,
		Time:      at,
		Payload:   encoded,
	}); err != nil {
		return fmt.Errorf("agent: append compact archive: %w", err)
	}
	// The store assigns the durable sequence, so read the fresh event back by its
	// exact payload: the folded archive event carries that sequence, which is how
	// the Context UI addresses the raw-history read-back (also after a restart).
	seq := s.lookupCompactArchiveSeq(ctx, sessionID, encoded)
	s.applyContextEvent(sessionID, compactArchiveEventType, encoded, at, seq)
	return nil
}

// lookupCompactArchiveSeq finds the durable sequence of the archive event just
// appended. The store assigns sequences, and another event of the same session
// may be appended in between (a queue change, a notification), so the newest
// window is matched on the event type and the exact payload; a miss (0) leaves
// the folded event without a sequence rather than guessing one.
func (s *service) lookupCompactArchiveSeq(ctx context.Context, sessionID string, payload []byte) int64 {
	events, err := s.store.Last(ctx, sessionID, 32)
	if err != nil {
		return 0
	}
	var seq int64
	for _, event := range events {
		if event.Type != compactArchiveEventType || len(event.Payload) == 0 {
			continue
		}
		if !bytes.Equal(event.Payload, payload) {
			continue
		}
		if event.Seq > seq {
			seq = event.Seq
		}
	}
	return seq
}
