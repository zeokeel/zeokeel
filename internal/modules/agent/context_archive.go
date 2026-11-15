package agent

// Compaction archive read-back (contracts.ContextArchiveService).
//
// A manual, automatic or recovery compaction preserves the raw history it
// replaces in one bounded durable `compact_archive` event (see compact_archive.go
// for the write side). The folded context timeline shows that event's metadata
// only; this file is the optional read-back half: given the session and the
// durable sequence of the archive event, it returns the archived messages as a
// bounded, read-only view for the Context UI's archive viewer.
//
// The archive is an audit copy and is never injected into a model request: no
// prompt assembly path calls this service, and the read never mutates the folded
// timeline or the stored history.

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var _ contracts.ContextArchiveService = (*service)(nil)

// contextArchiveReadTimeout bounds the durable read of one archive event. The
// event itself is bounded (2 MiB / 200 messages), so the read stays small.
const contextArchiveReadTimeout = 5 * time.Second

// ContextArchive implements contracts.ContextArchiveService: the raw history one
// compact_archive event preserved, addressed by the session and the event's
// durable sequence. ok is false for an empty session or non-positive sequence, a
// backend without a durable store, an unknown event, or an event that is not a
// compact_archive.
func (s *service) ContextArchive(sessionID string, seq int64) (contracts.ContextArchive, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || s.store == nil || sessionID == "" || seq <= 0 {
		return contracts.ContextArchive{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextArchiveReadTimeout)
	defer cancel()
	events, err := s.store.Events(ctx, sessionID, seq, seq)
	if err != nil {
		return contracts.ContextArchive{}, false
	}
	for _, event := range events {
		if event.Seq != seq || event.Type != compactArchiveEventType {
			continue
		}
		var payload compactArchivePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return contracts.ContextArchive{}, false
		}
		return contextArchiveFromPayload(event, payload), true
	}
	return contracts.ContextArchive{}, false
}

// contextArchiveFromPayload turns one durable archive event into the read model:
// the metadata stays the payload's own counts (never recomputed into a different
// story), and the messages are bounded by the read caps in the contract.
func contextArchiveFromPayload(event rig.SessionEvent, payload compactArchivePayload) contracts.ContextArchive {
	archive := contracts.ContextArchive{
		Seq:              event.Seq,
		Kind:             strings.TrimSpace(payload.Kind),
		At:               event.Time,
		RawMessageCount:  payload.MessageCount,
		RetainedMessages: payload.RetainedMessages,
		OmittedMessages:  payload.OmittedMessages,
		Bytes:            payload.Bytes,
		Truncated:        payload.Truncated,
	}
	if archive.RetainedMessages <= 0 {
		archive.RetainedMessages = len(payload.Messages)
	}
	limit := len(payload.Messages)
	if limit > contracts.ContextArchiveReadMaxMessages {
		limit = contracts.ContextArchiveReadMaxMessages
	}
	archive.Messages = make([]contracts.ContextArchiveMessage, 0, limit)
	for _, raw := range payload.Messages[:limit] {
		archive.Messages = append(archive.Messages, readArchivedMessage(raw))
	}
	if omitted := len(payload.Messages) - limit; omitted > 0 {
		// The reader's own bound cut the list; say so with the event's truncation
		// metadata instead of pretending the archive is complete.
		if archive.OmittedMessages == 0 {
			archive.OmittedMessages = omitted
		}
		archive.Truncated = true
	}
	return archive
}

// readArchivedMessage decodes one archived rig.Message into the bounded read
// model: role, text and tool calls, with the truncation flags the reader applied.
func readArchivedMessage(raw json.RawMessage) contracts.ContextArchiveMessage {
	var message rig.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		// A message that cannot be decoded is still part of the archive: report
		// it as an unreadable/truncated entry instead of dropping it silently.
		return contracts.ContextArchiveMessage{Role: "unknown", Truncated: true}
	}
	read := contracts.ContextArchiveMessage{Role: string(message.Role), Text: message.Text()}
	if len(read.Text) > contracts.ContextArchiveReadMaxTextBytes {
		read.Text = boundArchiveText(read.Text, contracts.ContextArchiveReadMaxTextBytes)
		read.Truncated = true
	}
	for _, call := range message.ToolCalls {
		arguments := call.Arguments
		if len(arguments) > contracts.ContextArchiveReadMaxArgumentsBytes {
			arguments = boundArchiveText(arguments, contracts.ContextArchiveReadMaxArgumentsBytes)
			read.Truncated = true
		}
		read.ToolCalls = append(read.ToolCalls, contracts.ContextArchiveToolCall{
			Name: call.Name, Arguments: arguments,
		})
	}
	return read
}

// boundArchiveText cuts s to at most limit bytes on a rune boundary, so the
// excerpt never ends mid-rune.
func boundArchiveText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
