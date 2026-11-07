package toolkit

// Workspace mcp.json opt-in: when the setting is enabled, activation reads
// <current workspace>/mcp.json and merges its server rows after the assembly
// and settings lists. The file supports {"servers":[...]} and a top-level
// array; malformed input is a warning, never an activation failure.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpWorkspaceOptInEnabled reports whether the raw setting enables the feature.
// Only a recognized true value does; malformed/absent values normalize to off.
func mcpWorkspaceOptInEnabled(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
}

// readWorkspaceMCPServers loads <current workspace>/mcp.json. A missing file is
// a no-op; read/parse failures are logged and also yield no rows (activation
// must not fail because of a repository file).
func readWorkspaceMCPServers(ctx context.Context, workspace contracts.WorkspaceService,
	logf func(format string, args ...any)) []contracts.MCPServerConfig {
	if workspace == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	current, ok, err := workspace.Current(ctx)
	if err != nil {
		logf("[toolkit] workspace mcp.json: current workspace: %v", err)
		return nil
	}
	if !ok || strings.TrimSpace(current.Root) == "" {
		return nil
	}
	path := filepath.Join(current.Root, "mcp.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		logf("[toolkit] workspace mcp.json: read %s: %v", path, err)
		return nil
	}
	rows, err := parseWorkspaceMCPServers(data)
	if err != nil {
		logf("[toolkit] workspace mcp.json: parse %s: %v", path, err)
		return nil
	}
	return rows
}

// parseWorkspaceMCPServers accepts both documented shapes.
func parseWorkspaceMCPServers(data []byte) ([]contracts.MCPServerConfig, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var rows []contracts.MCPServerConfig
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, err
		}
		return rows, nil
	}
	var wrapper struct {
		Servers []contracts.MCPServerConfig `json:"servers"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Servers, nil
}
