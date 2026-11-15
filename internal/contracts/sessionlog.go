package contracts

// Raw session-log export.

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// SessionLogExporter is the optional capability that archives one session's raw
// durable event log (the same events the trajectory view and the restore path
// read) as a ZIP. The agent backend implements it over its durable store; a
// host without the capability shows no export entry.
//
// The archive is the product's honest audit artifact: the durable events are
// exported verbatim (the archive's session.jsonl), plus an export header
// (meta.json). It contains no model prompt injections, no rendered UI state and
// no in-memory-only data.
type SessionLogExporter interface {
	// SessionLogArchive returns the ZIP bytes for one session's event log.
	//
	// Errors: ErrSessionLogNotFound for an unknown session, ErrSessionLogNoStore
	// when the backend has no durable store, ErrSessionLogTooLarge when the log
	// exceeds the exporter's bound; transport/store failures are returned as-is.
	SessionLogArchive(ctx context.Context, sessionID string) ([]byte, error)
}

// KeySessionLogExporter is provided by the agent module and consumed by the
// session's conversation-actions menu. It is optional.
var KeySessionLogExporter = cordis.NewServiceKey[SessionLogExporter]("zeokeel/session-log-exporter")

// Errors the raw-log export reports; the UI localizes its own prefix around
// them, so callers should match with errors.Is rather than compare strings.
var (
	// ErrSessionLogNotFound means the durable store does not know the session.
	ErrSessionLogNotFound = errors.New("session log: unknown session")
	// ErrSessionLogNoStore means the backend has no durable store to export.
	ErrSessionLogNoStore = errors.New("session log: no durable store")
	// ErrSessionLogTooLarge means the event log exceeds the archive bound.
	ErrSessionLogTooLarge = errors.New("session log: archive exceeds its bound")
)
