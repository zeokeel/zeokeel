package agent

// Raw session-log export (contracts.SessionLogExporter).
//
// The archive is the durable truth, not a rendered transcript: one JSONL line
// per persisted session event (seq / type / time / the payload's raw JSON) plus
// a meta.json header naming the session and the export. It deliberately contains
// no model prompt injections, no UI state and nothing that only lives in
// memory, and it is bounded: an event log past the limit fails instead of
// producing an unbounded archive.
//
// The archive format (events.jsonl + meta.json, 20000 events / 64 MiB bounds)
// is owned by the host-agnostic session/export package in
// github.com/zeokeel/zeokeel-rig. This file is the product adapter over the
// durable store: it resolves the session metadata (existence, title), supplies
// the export clock and maps the rig package's sentinel errors onto the
// contracts.SessionLogExporter vocabulary the UI matches with errors.Is.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/export"

	"github.com/zeokeel/zeokeel/internal/contracts"
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

// SessionLogArchive implements contracts.SessionLogExporter over the rig
// session/export archive.
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
	now := e.now
	if now == nil {
		now = time.Now
	}
	data, err := export.Archive(ctx, e.store, sessionID, export.Meta{
		ID:         meta.ID,
		Title:      meta.Title,
		ExportedAt: now(),
	})
	if err != nil {
		// The product reports one sentinel for every bound violation; keep the
		// rig detail in the message for the status line.
		if errors.Is(err, export.ErrTooLarge) {
			return nil, fmt.Errorf("%w: %v", contracts.ErrSessionLogTooLarge, err)
		}
		return nil, err
	}
	return data, nil
}
