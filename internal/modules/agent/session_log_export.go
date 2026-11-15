package agent

// Raw session-log export (contracts.SessionLogExporter).
//
// The archive is the durable truth, not a rendered transcript: one JSONL line
// per persisted session event (seq / type / time / the payload's raw JSON) plus
// a meta.json header naming the session and the export. It deliberately contains
// no model prompt injections, no UI state and nothing that only lives in
// memory, and it is bounded: an event log past the limit fails instead of
// producing an unbounded archive.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// sessionLogArchiveVersion is the archive format version written to meta.json
	// ("version"); bump it when the archive layout changes.
	sessionLogArchiveVersion = "1.0"
	// sessionLogArchiveSchema is the session.jsonl record schema written to
	// meta.json ("schema").
	sessionLogArchiveSchema = 1
	// sessionLogArchiveMaxEvents bounds how many durable events one archive can
	// carry. A longer log is reported as too large instead of truncated: a
	// silently partial audit artifact would be worse than a refusal.
	sessionLogArchiveMaxEvents = 20000
	// sessionLogArchiveMaxBytes bounds the JSONL part of the archive (64 MiB).
	sessionLogArchiveMaxBytes = 64 << 20
)

// sessionLogArchiveStore is the narrow durable view the exporter needs: the
// session's metadata (existence, title) and its whole event log. The product
// store (contracts.Store) satisfies it; tests use a stub.
type sessionLogArchiveStore interface {
	GetSessionMeta(ctx context.Context, id string) (contracts.SessionMeta, bool, error)
	Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error)
}

// sessionLogExporter implements contracts.SessionLogExporter over the durable
// store. now is injectable for deterministic tests.
type sessionLogExporter struct {
	store sessionLogArchiveStore
	now   func() time.Time
}

var _ contracts.SessionLogExporter = (*sessionLogExporter)(nil)

// newSessionLogExporter returns the exporter for one durable store (nil is
// allowed and reported as ErrSessionLogNoStore when asked to export).
func newSessionLogExporter(store sessionLogArchiveStore) *sessionLogExporter {
	return &sessionLogExporter{store: store, now: time.Now}
}

// sessionLogEventLine is one session.jsonl record: the durable event, verbatim.
// The session id is not repeated on every line (meta.json owns identity).
type sessionLogEventLine struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Time    time.Time       `json:"time"`
	Payload json.RawMessage `json:"payload"`
}

// sessionLogMetaDocument is meta.json: the export header, not UI state.
type sessionLogMetaDocument struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ExportedAt string `json:"exportedAt"`
	Version    string `json:"version"`
	Schema     int    `json:"schema"`
}

// SessionLogArchive implements contracts.SessionLogExporter.
func (e *sessionLogExporter) SessionLogArchive(ctx context.Context, sessionID string) ([]byte, error) {
	if e == nil || e.store == nil {
		return nil, contracts.ErrSessionLogNoStore
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, contracts.ErrSessionLogNotFound
	}
	meta, found, err := e.store.GetSessionMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", contracts.ErrSessionLogNotFound, sessionID)
	}
	events, err := e.store.Events(ctx, sessionID, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(events) > sessionLogArchiveMaxEvents {
		return nil, fmt.Errorf("%w: %d events (limit %d)",
			contracts.ErrSessionLogTooLarge, len(events), sessionLogArchiveMaxEvents)
	}

	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	jsonl, err := archive.Create("session.jsonl")
	if err != nil {
		return nil, err
	}
	written := 0
	for _, event := range events {
		line, err := json.Marshal(sessionLogEventLine{
			Seq:     event.Seq,
			Type:    event.Type,
			Time:    event.Time,
			Payload: event.Payload,
		})
		if err != nil {
			return nil, err
		}
		written += len(line) + 1
		if written > sessionLogArchiveMaxBytes {
			return nil, fmt.Errorf("%w: more than %d bytes of events",
				contracts.ErrSessionLogTooLarge, sessionLogArchiveMaxBytes)
		}
		if _, err := jsonl.Write(append(line, '\n')); err != nil {
			return nil, err
		}
	}
	now := e.now
	if now == nil {
		now = time.Now
	}
	header, err := json.Marshal(sessionLogMetaDocument{
		ID:         meta.ID,
		Title:      meta.Title,
		ExportedAt: now().UTC().Format(time.RFC3339),
		Version:    sessionLogArchiveVersion,
		Schema:     sessionLogArchiveSchema,
	})
	if err != nil {
		return nil, err
	}
	metaFile, err := archive.Create("meta.json")
	if err != nil {
		return nil, err
	}
	if _, err := metaFile.Write(header); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
