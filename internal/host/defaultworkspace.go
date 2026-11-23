package host

// The host's single default workspace: the product opens exactly one work
// folder, so a launch without a current workspace must not leave the shell
// pointing at nothing. After the assembly loads (and before the desktop's first
// meaningful frame) the host resolves the workspace backend from the kernel and,
// only when no workspace is current, creates <dataDir>/workspace and opens it.
//
// An existing current workspace is never replaced: a user coming from the old
// multi-workspace model keeps the folder they had open, and the other registered
// workspaces stay on disk (unreachable from the single-folder chrome, but never
// deleted by startup).

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// defaultWorkspaceDirName is the folder the host creates inside the product data
// directory when this launch has no current workspace.
const defaultWorkspaceDirName = "workspace"

// ensureDefaultWorkspace guarantees one open workspace at startup. It is a
// no-op without the workspace backend (a stripped composition), on a read
// failure (never create over an unknown state) and when a workspace is already
// current. Create derives the folder name from the path and makes the workspace
// current; Open then follows the explicit startup contract so a backend that
// changes that behavior still ends with the folder open.
func ensureDefaultWorkspace(kernel cordis.Context, dataDir string, logger contracts.Logger) {
	workspace, ok := kernel.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return
	}
	ctx := context.Background()
	if current, open, err := workspace.Current(ctx); err != nil {
		if logger != nil {
			logger.Printf("%s: read the current workspace: %v", appName, err)
		}
		return
	} else if open {
		if logger != nil {
			logger.Printf("%s: workspace folder %q already open", appName, current.Root)
		}
		return
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		if logger != nil {
			logger.Printf("%s: no data directory, cannot create the default workspace", appName)
		}
		return
	}
	root := filepath.Join(dataDir, defaultWorkspaceDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		if logger != nil {
			logger.Printf("%s: create the default workspace folder %q: %v", appName, root, err)
		}
		return
	}
	created, err := workspace.Create(ctx, "", root)
	if err != nil {
		if logger != nil {
			logger.Printf("%s: register the default workspace %q: %v", appName, root, err)
		}
		return
	}
	if strings.TrimSpace(created.ID) == "" {
		if logger != nil {
			logger.Printf("%s: register the default workspace %q: no workspace ID returned", appName, root)
		}
		return
	}
	if _, err := workspace.Open(ctx, created.ID); err != nil {
		if logger != nil {
			logger.Printf("%s: open the default workspace %q: %v", appName, created.Root, err)
		}
		return
	}
	if logger != nil {
		logger.Printf("%s: opened the default workspace folder %q", appName, created.Root)
	}
}
