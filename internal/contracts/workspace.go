package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Workspace is one registered project root (the old shell's workspace).
type Workspace struct {
	ID        string
	Name      string
	Root      string
	CreatedAt time.Time
	// Color is the rail palette id (empty = default).
	Color string
}

// FileEntry is one entry of a workspace directory listing.
type FileEntry struct {
	// Name is the base name.
	Name string
	// Path is the path relative to the workspace root (slash separated).
	Path string
	// Dir marks a directory.
	Dir bool
	// Size is the file size in bytes (directories: 0).
	Size int64
	// ModTime is the entry's modification time.
	ModTime time.Time
}

// WorkspaceService is the product's project-root registry and sandboxed file
// reader: the old shell's workspaces plus the Files inspector's data source.
type WorkspaceService interface {
	// List returns the registered workspaces ordered by creation time.
	List(ctx context.Context) ([]Workspace, error)
	// Current returns the active workspace (false when none is open).
	Current(ctx context.Context) (Workspace, bool, error)
	// Create registers a workspace rooted at root (which must be a directory)
	// and makes it current.
	Create(ctx context.Context, name, root string) (Workspace, error)
	// Open makes an existing workspace current.
	Open(ctx context.Context, id string) (Workspace, error)
	// Delete forgets a workspace (the files are not touched).
	Delete(ctx context.Context, id string) error
	// Files lists one directory of the current workspace; dir is relative to
	// the root ("" for the root itself). Directories come first, then files,
	// each alphabetically.
	Files(ctx context.Context, dir string) ([]FileEntry, error)
	// ReadFile reads one file of the current workspace (path relative to the
	// root). limit caps the returned bytes (0 = the service default).
	ReadFile(ctx context.Context, path string, limit int) ([]byte, error)
	// Search finds files of the current workspace whose name contains query
	// (case insensitive), bounded by limit.
	Search(ctx context.Context, query string, limit int) ([]FileEntry, error)
}

// FileChange is one entry of the current workspace's uncommitted changes
// (the old shell's workspace-changes view).
type FileChange struct {
	// Path is the path relative to the workspace root (slash separated).
	Path string
	// Status is the short git status: "M", "A", "D", "R", "??" (untracked).
	Status string
	// Diff is the unified diff for this file ("" for untracked/binary files).
	Diff string
}

// WorkspaceRenamer is the optional part of the workspace service that renames a
// registered workspace. UI modules type-assert it so a host without rename
// support still shows the rest of the workspace menu (and hides only the rename
// entry). The workspace keeps its ID, root, creation time and current selection.
type WorkspaceRenamer interface {
	// RenameWorkspace changes one workspace's display name.
	//
	// It returns ErrWorkspaceNameEmpty for an empty (or whitespace-only) name
	// and ErrWorkspaceNameTaken when another workspace already uses the name
	// (trimmed, case-insensitive); the registry is unchanged on either error.
	// An unknown ID reports a not-found error. The returned workspace carries
	// the new name with its identity and root preserved.
	RenameWorkspace(ctx context.Context, id, name string) (Workspace, error)
}

// The errors RenameWorkspace may report, so a UI can localize them instead of
// printing an English backend message (UI modules cannot import the workspace
// backend module).
// WorkspaceColorer is the optional palette half of the workspace service.
// Setting a color never changes identity, name or root.
type WorkspaceColorer interface {
	SetWorkspaceColor(ctx context.Context, id, color string) (Workspace, error)
}

var (
	// ErrWorkspaceNameEmpty reports a workspace rename with no display name.
	ErrWorkspaceNameEmpty = errors.New("workspace: name is empty")
	// ErrWorkspaceNameTaken reports a display name another workspace already
	// uses (trimmed, case-insensitive).
	ErrWorkspaceNameTaken = errors.New("workspace: name already used")
)

// WorkspaceDiffer is the optional part of the workspace service that reports
// the uncommitted changes of the current workspace. UI modules type-assert it
// so hosts without git support still implement WorkspaceService.
type WorkspaceDiffer interface {
	// Changes lists the workspace's uncommitted changes, bounded by the
	// service's configured limits.
	Changes(ctx context.Context) ([]FileChange, error)
}

// KeyWorkspace is provided by the workspace backend module and consumed by the
// Files inspector (and later by @-mentions and the workspace/session tree).
var KeyWorkspace = cordis.NewServiceKey[WorkspaceService]("zeokeel/workspace")
