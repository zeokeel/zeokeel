package toolkit

// SDK sandbox affordances: the model can read the policy it is under
// (sandbox_status) and ask for one writable directory when a tool insists on a
// fixed absolute path (request_sandbox_access). A personal harness needs both:
// without them the only answer to "write denied" is to guess or give up.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
)

// TestSandboxStatusToolReportsPolicy pins sandbox_status: it is registered and
// reports the effective mode and workspace root of this build.
func TestSandboxStatusToolReportsPolicy(t *testing.T) {
	root := t.TempDir()
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, newMutableSettings(nil))

	result := invoke(t, registry, "sandbox_status", `{}`)
	if result.IsError {
		t.Fatalf("sandbox_status failed: %q", result.Content)
	}
	var report struct {
		Mode           string   `json:"mode"`
		WorkspaceRoots []string `json:"workspaceRoots"`
		Network        bool     `json:"network"`
	}
	if err := json.Unmarshal([]byte(result.Content), &report); err != nil {
		t.Fatalf("sandbox_status is not JSON: %v\n%s", err, result.Content)
	}
	if report.Mode != "workspace-write" {
		t.Fatalf("sandbox_status mode = %q, want workspace-write", report.Mode)
	}
	found := false
	for _, candidate := range report.WorkspaceRoots {
		if filepath.Clean(candidate) == filepath.Clean(root) {
			found = true
		}
	}
	if !found {
		t.Fatalf("sandbox_status workspaceRoots = %v, want %q", report.WorkspaceRoots, root)
	}
}

// TestRequestSandboxAccessGrantsWritableDirectory pins request_sandbox_access:
// asking for one fixed directory widens the policy's writable surface so a tool
// that insists on that path can work.
func TestRequestSandboxAccessGrantsWritableDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	grantee := filepath.Join(outside, "tool-state")
	if err := os.MkdirAll(grantee, 0o755); err != nil {
		t.Fatalf("mkdir grantee: %v", err)
	}
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, newMutableSettings(nil))

	// The directory is outside the workspace, so writing there fails first.
	blocked, err := registry.Invoke(context.Background(), rig.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":` + quoteJSON(filepath.Join(grantee, "state.txt")) + `,"content":"x"}`,
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !blocked.IsError {
		t.Fatalf("write_file outside the workspace succeeded before a grant: %q", blocked.Content)
	}

	grant := invoke(t, registry, "request_sandbox_access",
		`{"path":`+quoteJSON(grantee)+`,"reason":"the tool keeps its cache in this fixed directory"}`)
	if grant.IsError {
		t.Fatalf("request_sandbox_access failed: %q", grant.Content)
	}
	var result struct {
		Granted bool   `json:"granted"`
		Path    string `json:"path"`
		Budget  int    `json:"budgetLeft"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal([]byte(grant.Content), &result); err != nil {
		t.Fatalf("request_sandbox_access is not JSON: %v\n%s", err, grant.Content)
	}
	if !result.Granted {
		t.Fatalf("request_sandbox_access = %+v, want granted", result)
	}
	if !strings.EqualFold(filepath.Clean(result.Path), filepath.Clean(grantee)) {
		t.Fatalf("grant path = %q, want %q", result.Path, grantee)
	}

	// The grant is visible in the policy's own report (sandbox_status): the
	// writable surface of command execution now includes the directory. (The
	// grant widens what commands may write — the runner's fence — not the
	// in-process file tools' readable roots.)
	status := invoke(t, registry, "sandbox_status", `{}`)
	if status.IsError {
		t.Fatalf("sandbox_status after grant failed: %q", status.Content)
	}
	var report struct {
		Grants []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"grants"`
	}
	if err := json.Unmarshal([]byte(status.Content), &report); err != nil {
		t.Fatalf("sandbox_status is not JSON: %v\n%s", err, status.Content)
	}
	granted := false
	for _, entry := range report.Grants {
		if filepath.Clean(entry.Path) == filepath.Clean(grantee) {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("sandbox_status grants = %+v, want %q", report.Grants, grantee)
	}
}
