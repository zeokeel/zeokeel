package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Workspace is one registered project root (the old shell's workspace).
type Workspace struct {
	ID        string
	Name      string
	Root      string
	CreatedAt time.Time
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

// KeyWorkspace is provided by the workspace backend module and consumed by the
// Files inspector (and later by @-mentions and the workspace/session tree).
var KeyWorkspace = cordis.NewServiceKey[WorkspaceService]("zeokeel/workspace")
