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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
	if err := s.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: sessionID,
		Type:      compactArchiveEventType,
		Time:      time.Now(),
		Payload:   encoded,
	}); err != nil {
		return fmt.Errorf("agent: append compact archive: %w", err)
	}
	s.applyCompactArchive(sessionID, encoded, time.Now())
	return nil
}

// applyCompactArchive folds the archive metadata (never the message text) into
// the context timeline.
func (s *service) applyCompactArchive(sessionID string, payload []byte, at time.Time) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(payload) == 0 {
		return
	}
	var metadata struct {
		Kind             string `json:"kind"`
		MessageCount     int    `json:"messageCount"`
		RetainedMessages int    `json:"retainedMessages"`
		Bytes            int    `json:"bytes"`
		Truncated        bool   `json:"truncated"`
		OmittedMessages  int    `json:"omittedMessages"`
		BeforeTokens     int    `json:"beforeTokens"`
		AfterTokens      int    `json:"afterTokens"`
	}
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return
	}
	detail := fmt.Sprintf("%s archive: %d messages archived", metadata.Kind, metadata.RetainedMessages)
	if metadata.Truncated {
		detail += fmt.Sprintf(", %d omitted (truncated)", metadata.OmittedMessages)
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if s.contextTimelines == nil {
		s.contextTimelines = make(map[string]*contextTimeline)
	}
	timeline := s.contextTimelines[sessionID]
	if timeline == nil {
		timeline = &contextTimeline{}
		s.contextTimelines[sessionID] = timeline
	}
	timeline.revision++
	timeline.events = append(timeline.events, contracts.ContextEvent{
		Kind:             contracts.ContextEventArchive,
		At:               at,
		Detail:           detail,
		Before:           metadata.BeforeTokens,
		After:            metadata.AfterTokens,
		Replaced:         metadata.MessageCount,
		Saved:            metadata.RetainedMessages,
		ArchivedMessages: metadata.RetainedMessages,
		ArchivedBytes:    metadata.Bytes,
		Truncated:        metadata.Truncated,
	})
	if len(timeline.events) > contextTimelineEvents {
		timeline.events = append([]contracts.ContextEvent(nil), timeline.events[len(timeline.events)-contextTimelineEvents:]...)
	}
}
