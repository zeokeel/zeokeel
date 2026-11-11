package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	rig "github.com/zeokeel/zeokeel-rig"
)

// KeyDataDir is provided by the process host: the product's per-user data
// directory (settings, logs, and the session store database live here).
var KeyDataDir = cordis.NewServiceKey[string]("zeokeel/datadir")

// SessionMeta is the durable list-item view of a conversation, independent of
// whether it currently runs.
type SessionMeta struct {
	ID           string
	WorkspaceID  string
	Title        string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SessionWorkspaceStore is the optional durable half that records which
// workspace a session belongs to. The agent backend type-asserts the store so
// stores that predate workspaces keep working.
type SessionWorkspaceStore interface {
	// SetSessionWorkspace records the workspace of an existing session.
	SetSessionWorkspace(ctx context.Context, sessionID, workspaceID string) error
}

// KeyStore is provided by the session store module: SQLite-backed persistence
// for conversations, session events and interruption checkpoints.
var KeyStore = cordis.NewServiceKey[Store]("zeokeel/store")

// Store is the product's durable session layer. It exposes the Rig contracts
// the agent loop needs (message history, checkpoint store, event log) as views
// plus the product metadata the session list needs (CRUD by session ID).
//
// The Rig History and CheckpointStore contracts both declare Load with
// different signatures, so they cannot be embedded together; the agent module
// asks for the views explicitly.
//
// All methods are safe for concurrent use.
type Store interface {
	// SessionLog is the durable event log view (rig.SessionLog).
	rig.SessionLog

	// History returns the message history view the agent loop persists to.
	History() rig.History
	// CheckpointStore returns the interruption-checkpoint view the agent loop
	// saves turn barriers to.
	CheckpointStore() rig.CheckpointStore
	// ReplaceHistory atomically replaces a session's messages (Rig
	// HistoryReplacer semantics; used by manual compaction).
	ReplaceHistory(ctx context.Context, sessionID string, messages []rig.Message) error

	// CreateSession registers a new session row (idempotent for the same ID).
	CreateSession(ctx context.Context, id, title string) error
	// DeleteSession removes the session and all its messages, events and
	// checkpoint. Deleting an unknown session is a no-op.
	DeleteSession(ctx context.Context, id string) error
	// RenameSession updates the display title of a session.
	RenameSession(ctx context.Context, id, title string) error
	// Sessions lists every session, newest updated first.
	Sessions(ctx context.Context) ([]SessionMeta, error)
	// GetSessionMeta returns one session's metadata; found is false when the
	// session does not exist.
	GetSessionMeta(ctx context.Context, id string) (meta SessionMeta, found bool, err error)
	// AppendEvent records one session event (used by session_query/report tools
	// and the trajectory view).
	AppendEvent(ctx context.Context, event rig.SessionEvent) error
}

// ApprovalDecision is one answer to an approval request.
type ApprovalDecision string

const (
	// ApprovalAllow permits this one tool call.
	ApprovalAllow ApprovalDecision = "allow"
	// ApprovalAllowAlways permits this tool from now on for the run.
	ApprovalAllowAlways ApprovalDecision = "allow_always"
	// ApprovalDeny rejects the tool call; the agent reports a tool error.
	ApprovalDeny ApprovalDecision = "deny"
)

// ApprovalRequest is one pending tool-call approval shown to the user.
type ApprovalRequest struct {
	// ID identifies the request for Resolve.
	ID string
	// Tool is the tool name being approved.
	Tool string
	// Summary is a short human-readable description of the call.
	Summary string
	// Arguments is the raw JSON arguments (for display; may be truncated).
	Arguments string
	// CreatedAt is when the request started waiting.
	CreatedAt time.Time
	// SessionID is the chat session whose run asked for the approval. Multiple
	// sessions (and automation runs) can execute concurrently, so the dialog
	// must say where a request comes from instead of rendering it in whatever
	// conversation is currently open.
	SessionID string
	// Source is the session provenance ("user", "automation", ...), so the
	// dialog can label background requests.
	Source string
}

// KeyApprovals is provided by the approvals backend module and consumed by the
// agent module (as a Rig gate) and the approval UI module.
var KeyApprovals = cordis.NewServiceKey[Approvals]("zeokeel/approvals")

// Approvals asks the user to approve tool calls and hands the answers back to
// the agent's gate. Headless products configure Mode "auto" (allow everything
// the toolkit policy already allows) so runs never block.
type Approvals interface {
	// Mode reports the configured policy: "ask", "auto" or "never".
	Mode() string
	// Request blocks until the request is resolved or ctx ends. In "auto" mode
	// it returns ApprovalAllow immediately; in "never" mode ApprovalDeny.
	Request(ctx context.Context, request ApprovalRequest) (ApprovalDecision, error)
	// Pending lists the requests currently waiting, oldest first.
	Pending() []ApprovalRequest
	// Resolve answers a pending request (idempotent after the first answer).
	Resolve(id string, decision ApprovalDecision) error
	// Subscribe receives a callback whenever the pending set changes. The
	// returned function unsubscribes. Callbacks run synchronously from any
	// goroutine; a UI must hop to its thread.
	Subscribe(fn func()) (unsubscribe func(), err error)
}
