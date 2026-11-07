package sessionstore

// Tests for the session ↔ workspace association: the workspace_id migration
// for databases created before the column existed, SetSessionWorkspace and the
// Sessions/GetSessionMeta backfill.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// legacySchema creates the sessions table as it looked before workspace
// association existed and inserts one row.
func createLegacyDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		title TEXT,
		created_at INTEGER,
		updated_at INTEGER
	)`); err != nil {
		t.Fatalf("create legacy sessions table: %v", err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := db.Exec(`INSERT INTO sessions(id, title, created_at, updated_at) VALUES(?, ?, ?, ?)`,
		"legacy-session", "Legacy title", now, now); err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
}

// workspaceColumnCount counts the sessions.workspace_id columns.
func workspaceColumnCount(t *testing.T, current *store) int {
	t.Helper()
	rows, err := current.db.Query("PRAGMA table_info(sessions)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue any
			primaryKey   int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == "workspace_id" {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return count
}

// TestWorkspaceColumnMigration covers opening a pre-workspace database: the
// column is added, existing rows get "", and the migration is idempotent.
func TestWorkspaceColumnMigration(t *testing.T) {
	dataDir := t.TempDir()
	createLegacyDatabase(t, filepath.Join(dataDir, DBFileName))

	current := openTestStoreAt(t, dataDir)
	if got := workspaceColumnCount(t, current); got != 1 {
		t.Fatalf("workspace_id columns = %d, want exactly 1", got)
	}
	meta := metaOf(t, current, "legacy-session")
	if meta.Title != "Legacy title" || meta.WorkspaceID != "" {
		t.Fatalf("migrated meta = %+v, want the legacy row with an empty workspace", meta)
	}
	// The migrated row accepts an association.
	if err := current.SetSessionWorkspace(context.Background(), "legacy-session", "ws-legacy"); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if meta := metaOf(t, current, "legacy-session"); meta.WorkspaceID != "ws-legacy" {
		t.Fatalf("workspace after set = %q, want ws-legacy", meta.WorkspaceID)
	}

	// Reopening the same file must not add a second column.
	if err := current.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openTestStoreAt(t, dataDir)
	if got := workspaceColumnCount(t, reopened); got != 1 {
		t.Fatalf("workspace_id columns after reopen = %d, want 1", got)
	}
	if meta := metaOf(t, reopened, "legacy-session"); meta.WorkspaceID != "ws-legacy" {
		t.Fatalf("persisted workspace = %q, want ws-legacy", meta.WorkspaceID)
	}
}

// TestSetSessionWorkspace covers the setter, clearing, unknown sessions,
// validation and persistence across reopen.
func TestSetSessionWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	current := openTestStoreAt(t, dataDir)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "s1", "Session one"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if meta := metaOf(t, current, "s1"); meta.WorkspaceID != "" {
		t.Fatalf("new session workspace = %q, want empty", meta.WorkspaceID)
	}
	if err := current.SetSessionWorkspace(ctx, "s1", " ws-one "); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if meta := metaOf(t, current, "s1"); meta.WorkspaceID != "ws-one" {
		t.Fatalf("workspace = %q, want trimmed ws-one", meta.WorkspaceID)
	}
	metas, err := current.Sessions(ctx)
	if err != nil || len(metas) != 1 || metas[0].WorkspaceID != "ws-one" {
		t.Fatalf("Sessions = %+v/%v, want the workspace backfilled", metas, err)
	}

	// Clearing the association is allowed.
	if err := current.SetSessionWorkspace(ctx, "s1", ""); err != nil {
		t.Fatalf("clear workspace: %v", err)
	}
	if meta := metaOf(t, current, "s1"); meta.WorkspaceID != "" {
		t.Fatalf("cleared workspace = %q, want empty", meta.WorkspaceID)
	}

	if err := current.SetSessionWorkspace(ctx, "missing", "ws"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("SetSessionWorkspace(unknown) = %v, want ErrUnknownSession", err)
	}
	if err := current.SetSessionWorkspace(ctx, "  ", "ws"); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("SetSessionWorkspace(empty id) = %v, want a validation error", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := current.SetSessionWorkspace(cancelled, "s1", "ws"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetSessionWorkspace(cancelled) = %v, want context.Canceled", err)
	}

	// The association survives reopening the database.
	if err := current.SetSessionWorkspace(ctx, "s1", "ws-persisted"); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if err := current.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openTestStoreAt(t, dataDir)
	if meta := metaOf(t, reopened, "s1"); meta.WorkspaceID != "ws-persisted" {
		t.Fatalf("reopened workspace = %q, want ws-persisted", meta.WorkspaceID)
	}
}

// TestModuleExposesSessionWorkspaceStore covers the module-provided store
// implementing the optional contracts.SessionWorkspaceStore.
func TestModuleExposesSessionWorkspaceStore(t *testing.T) {
	dataDir := t.TempDir()
	_, _, service, _ := loadStoreModule(t, dataDir, nil)
	recorder, ok := service.(contracts.SessionWorkspaceStore)
	if !ok {
		t.Fatalf("module store %T does not implement contracts.SessionWorkspaceStore", service)
	}
	ctx := context.Background()
	if err := service.CreateSession(ctx, "module-session", "Module"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := recorder.SetSessionWorkspace(ctx, "module-session", "ws-module"); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	meta, found, err := service.GetSessionMeta(ctx, "module-session")
	if err != nil || !found || meta.WorkspaceID != "ws-module" {
		t.Fatalf("GetSessionMeta = %+v/%v/%v, want WorkspaceID ws-module", meta, found, err)
	}
}
