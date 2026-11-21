// Package sessionstore is ZeoKeel's SQLite session store kernel module.
//
// The durable half itself (schema, history, checkpoints, event log, session
// metadata, decoded-text search) is the SDK's session/sqlstore package: this
// module owns only the product wiring — the database lives in the product data
// directory, the driver/DSN/PRAGMAs are opened here, and the SDK store is
// exposed as contracts.Store (plus the workspace binding and search result
// shapes the desktop UI expects).
//
// The schema is byte-compatible with the database earlier product versions
// wrote, so pointing this store at an existing zeokeel.db needs no data copy.
package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/sqlstore"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// unnamedSessionTitle is shown when a session has no display title (the
	// SDK store returns its neutral English default; the desktop is localized).
	unnamedSessionTitle = "未命名会话"
	// defaultSearchMessageLimit / maxSearchMessageLimit mirror the SDK search
	// bounds (kept for the product's search tests).
	defaultSearchMessageLimit = 50
	maxSearchMessageLimit     = 200
)

// store adapts the SDK session store to the product contracts.
type store struct {
	inner  *sqlstore.Store
	db     *sql.DB
	dbPath string
	logger contracts.Logger
}

var (
	_ contracts.Store                 = (*store)(nil)
	_ contracts.SessionWorkspaceStore = (*store)(nil)
	_ contracts.MessageSearcher       = (*store)(nil)
)

// ErrUnknownSession is returned by operations that require an existing session
// (for example RenameSession).
var ErrUnknownSession = errors.New("sessionstore: unknown session")

// History returns the rig.History view.
func (s *store) History() rig.History { return s.inner.History() }

// CheckpointStore returns the rig.CheckpointStore view.
func (s *store) CheckpointStore() rig.CheckpointStore { return s.inner.CheckpointStore() }

// ReplaceHistory atomically replaces a session's messages.
func (s *store) ReplaceHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	return s.inner.ReplaceHistory(ctx, sessionID, messages)
}

// CreateSession registers a session row (idempotent for the same ID).
func (s *store) CreateSession(ctx context.Context, id, title string) error {
	return s.inner.CreateSession(ctx, id, title)
}

// DeleteSession removes a session and its messages, events and checkpoint.
func (s *store) DeleteSession(ctx context.Context, id string) error {
	return s.inner.DeleteSession(ctx, id)
}

// RenameSession updates the display title.
func (s *store) RenameSession(ctx context.Context, id, title string) error {
	err := s.inner.RenameSession(ctx, id, title)
	if errors.Is(err, sqlstore.ErrUnknownSession) {
		return fmt.Errorf("%w: %q", ErrUnknownSession, id)
	}
	return err
}

// SetSessionWorkspace records the workspace of an existing session.
func (s *store) SetSessionWorkspace(ctx context.Context, sessionID, workspaceID string) error {
	err := s.inner.SetSessionWorkspace(ctx, sessionID, workspaceID)
	if errors.Is(err, sqlstore.ErrUnknownSession) {
		return fmt.Errorf("%w: %q", ErrUnknownSession, sessionID)
	}
	return err
}

// Sessions lists every session, newest updated first.
func (s *store) Sessions(ctx context.Context) ([]contracts.SessionMeta, error) {
	metas, err := s.inner.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.SessionMeta, 0, len(metas))
	for _, meta := range metas {
		out = append(out, sessionMetaOf(meta))
	}
	return out, nil
}

// GetSessionMeta returns one session's metadata.
func (s *store) GetSessionMeta(ctx context.Context, id string) (contracts.SessionMeta, bool, error) {
	meta, found, err := s.inner.GetSessionMeta(ctx, id)
	if err != nil || !found {
		return contracts.SessionMeta{}, found, err
	}
	return sessionMetaOf(meta), true, nil
}

// sessionMetaOf maps the SDK metadata to the product shape.
func sessionMetaOf(meta sqlstore.Meta) contracts.SessionMeta {
	return contracts.SessionMeta{
		ID:           meta.ID,
		WorkspaceID:  meta.WorkspaceID,
		Title:        meta.Title,
		MessageCount: meta.MessageCount,
		CreatedAt:    meta.CreatedAt,
		UpdatedAt:    meta.UpdatedAt,
	}
}

// AppendEvent records one session event.
func (s *store) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	return s.inner.AppendEvent(ctx, event)
}

// Events reads the session's event log in sequence order.
func (s *store) Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error) {
	return s.inner.Events(ctx, sessionID, from, to)
}

// Last returns the most recent events of one session.
func (s *store) Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	return s.inner.Last(ctx, sessionID, limit)
}

// Search matches events by type and decoded payload text.
func (s *store) Search(ctx context.Context, query string, limit int) ([]rig.SessionEvent, error) {
	return s.inner.Search(ctx, query, limit)
}

// SearchMessages finds messages across sessions (the desktop search screen).
func (s *store) SearchMessages(ctx context.Context, query string, limit int) ([]contracts.MessageSearchHit, error) {
	hits, err := s.inner.SearchMessages(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.MessageSearchHit, 0, len(hits))
	for _, hit := range hits {
		title := hit.SessionTitle
		if title == "" || title == "Untitled session" {
			title = unnamedSessionTitle
		}
		out = append(out, contracts.MessageSearchHit{
			SessionID:    hit.SessionID,
			SessionTitle: title,
			MessageID:    contracts.MessageOrdinalLocator(hit.Ordinal),
			Role:         hit.Role,
			Snippet:      hit.Snippet,
			CreatedAt:    hit.CreatedAt,
		})
	}
	return out, nil
}

// Close closes the underlying database.
func (s *store) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.closeDatabase()
}

// timeFromNanos is kept for the module tests that decode stored timestamps.
func timeFromNanos(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}
