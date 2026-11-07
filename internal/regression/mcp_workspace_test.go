package regression

// Adversarial audit: the workspace mcp.json opt-in merge in the toolkit. Negative
// cases: opt-in normalization (only a recognized true value enables it), missing
// workspace service, workspace lookup errors, bad JSON/invalid rows/read errors,
// both documented file shapes, source priority (assembly > settings > workspace)
// and URL/stdio deduplication.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
)

// fakeMCPWorkspace is the minimal WorkspaceService the opt-in read needs.
type fakeMCPWorkspace struct {
	current    contracts.Workspace
	has        bool
	currentErr error
}

func (w *fakeMCPWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	if !w.has {
		return nil, nil
	}
	return []contracts.Workspace{w.current}, nil
}

func (w *fakeMCPWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	if w.currentErr != nil {
		return contracts.Workspace{}, false, w.currentErr
	}
	return w.current, w.has, nil
}

func (w *fakeMCPWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: create unsupported")
}

func (w *fakeMCPWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: open unsupported")
}

func (w *fakeMCPWorkspace) Delete(context.Context, string) error { return nil }

func (w *fakeMCPWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *fakeMCPWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}

func (w *fakeMCPWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// workspaceWithMCPFile writes content to <root>/mcp.json and returns the fake
// service positioned on that workspace.
func workspaceWithMCPFile(t *testing.T, content string) *fakeMCPWorkspace {
	t.Helper()
	root := t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write workspace mcp.json: %v", err)
		}
	}
	return &fakeMCPWorkspace{current: contracts.Workspace{ID: "ws", Name: "ws", Root: root}, has: true}
}

// startToolkitWithWorkspace activates the toolkit module with settings rows, the
// opt-in value and an optional workspace service, then returns the read model.
func startToolkitWithWorkspace(t *testing.T, h *harness, assembly []toolkit.MCPServer,
	settingsRaw, optIn string, workspace contracts.WorkspaceService) contracts.PluginsService {
	t.Helper()
	if settingsRaw != "" {
		if err := h.settings.Set(contracts.SettingMCPServers, settingsRaw); err != nil {
			t.Fatalf("set mcp.servers: %v", err)
		}
	}
	if err := h.settings.Set(contracts.SettingMCPWorkspaceOptIn, optIn); err != nil {
		t.Fatalf("set workspace opt-in: %v", err)
	}
	if workspace != nil {
		provide(t, h.ctx, contracts.KeyWorkspace, contracts.WorkspaceService(workspace))
	}
	activate(t, h.ctx, toolkit.Module(), toolkit.Config{MCPServers: assembly})
	return requireService(t, h.ctx, contracts.KeyPlugins)
}

// hasPluginName reports whether the plugin read model lists name.
func hasPluginName(plugins []contracts.MCPPlugin, name string) bool {
	for _, plugin := range plugins {
		if plugin.Name == name {
			return true
		}
	}
	return false
}

// TestMCPWorkspaceOptInNormalization covers the opt-in flag: only true-like
// spellings enable the workspace file; everything else normalizes to off.
func TestMCPWorkspaceOptInNormalization(t *testing.T) {
	file := `{"servers":[{"name":"ws-server","url":"http://127.0.0.1:1/ws"}]}`
	for _, enabled := range []string{"true", "TRUE", " True "} {
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", enabled, workspaceWithMCPFile(t, file))
		if got := plugins.MCP(); !hasPluginName(got, "ws-server") {
			t.Fatalf("opt-in %q: MCP() = %v, want the workspace server", enabled, pluginNames(got))
		}
	}
	for _, disabled := range []string{"", "false", "0", "1", "yes", "on", "garbage", " tru e "} {
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", disabled, workspaceWithMCPFile(t, file))
		if got := plugins.MCP(); hasPluginName(got, "ws-server") {
			t.Fatalf("opt-in %q: workspace server was merged, want it disabled", disabled)
		}
	}
}

// TestMCPWorkspaceFileShapes covers both documented file shapes: a top-level JSON
// array and a {"servers":[...]} object.
func TestMCPWorkspaceFileShapes(t *testing.T) {
	for name, content := range map[string]string{
		"array":  `[{"name":"ws-array","url":"http://127.0.0.1:1/ws-array"}]`,
		"object": `{"servers":[{"name":"ws-object","url":"http://127.0.0.1:1/ws-object"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			plugins := startToolkitWithWorkspace(t, h, nil, "", "true", workspaceWithMCPFile(t, content))
			want := "ws-" + name
			if got := plugins.MCP(); !hasPluginName(got, want) {
				t.Fatalf("%s shape: MCP() = %v, want %s", name, pluginNames(got), want)
			}
		})
	}
}

// TestMCPWorkspaceBadJsonAndBadRows covers hostile workspace files: malformed
// JSON is logged and ignored, and invalid rows (NUL name, missing URL/command)
// are skipped (workspace rows never become diagnostic failure entries).
func TestMCPWorkspaceBadJsonAndBadRows(t *testing.T) {
	t.Run("bad json", func(t *testing.T) {
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true", workspaceWithMCPFile(t, `{"servers":[not json`))
		if got := plugins.MCP(); len(got) != 0 {
			t.Fatalf("MCP() = %v after bad JSON, want no plugins", pluginNames(got))
		}
		if !h.logger.contains("workspace mcp.json: parse") {
			t.Fatalf("bad workspace JSON was not logged: %v", h.logger.lines)
		}
	})

	t.Run("invalid rows", func(t *testing.T) {
		h := newHarness(t)
		content := `[{"name":"bad\u0000name","url":"http://127.0.0.1:1/a"},` +
			`{"name":"no-target"},` +
			`{"name":"bad-cmd","command":"bad\u0000cmd"},` +
			`{"name":"ws-valid","url":"http://127.0.0.1:1/valid"}]`
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true", workspaceWithMCPFile(t, content))
		got := plugins.MCP()
		if !hasPluginName(got, "ws-valid") || len(got) != 1 {
			t.Fatalf("MCP() = %v, want only the valid workspace row", pluginNames(got))
		}
		if !h.logger.contains("skip workspace mcp.json") {
			t.Fatalf("invalid workspace rows were not logged: %v", h.logger.lines)
		}
	})

	t.Run("read error", func(t *testing.T) {
		// A workspace root that is a regular file makes <root>/mcp.json unreadable.
		rootFile := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(rootFile, []byte("x"), 0o644); err != nil {
			t.Fatalf("write root file: %v", err)
		}
		workspace := &fakeMCPWorkspace{
			current: contracts.Workspace{ID: "ws", Name: "ws", Root: rootFile + "/child"},
			has:     true,
		}
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true", workspace)
		if got := plugins.MCP(); len(got) != 0 {
			t.Fatalf("MCP() = %v after a read error, want no plugins", pluginNames(got))
		}
		if !h.logger.contains("workspace mcp.json: read") {
			t.Fatalf("workspace read error was not logged: %v", h.logger.lines)
		}
	})
}

// TestMCPWorkspacePriorityAndURLDedup covers the source priority and duplicate
// URL handling: assembly > settings > workspace; the first occurrence wins.
func TestMCPWorkspacePriorityAndURLDedup(t *testing.T) {
	assembly := []toolkit.MCPServer{{Name: "assembly-a", URL: "http://127.0.0.1:1/shared-a", TimeoutMs: 50}}
	settingsRaw := `[{"name":"settings-b","url":"http://127.0.0.1:1/shared-b"}]`
	workspaceFile := `{"servers":[` +
		`{"name":"ws-dup-a","url":"HTTP://127.0.0.1:1/SHARED-A"},` + // case-folded duplicate of assembly
		`{"name":"ws-dup-b","url":"http://127.0.0.1:1/shared-b"},` + // duplicate of settings
		`{"name":"ws-dup-c","url":"http://127.0.0.1:1/shared-c"},` +
		`{"name":"ws-dup-c2","url":"http://127.0.0.1:1/shared-c"},` + // duplicate within the file
		`{"name":"ws-new","url":"http://127.0.0.1:1/workspace-only"}]}`

	h := newHarness(t)
	plugins := startToolkitWithWorkspace(t, h, assembly, settingsRaw, "true", workspaceWithMCPFile(t, workspaceFile))
	names := pluginNames(plugins.MCP())
	want := []string{"assembly-a", "settings-b", "ws-dup-c", "ws-new"}
	if len(names) != len(want) {
		t.Fatalf("MCP() names = %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("MCP() names = %v, want %v (source order)", names, want)
		}
	}
	if !h.logger.contains("duplicate URL") {
		t.Fatalf("duplicate URLs were not logged: %v", h.logger.lines)
	}
	if !h.logger.contains("earlier source wins") {
		t.Fatalf("workspace duplicate log lacks the earlier-source note: %v", h.logger.lines)
	}
}

// TestMCPWorkspaceStdioDedup covers stdio identity: command+args duplicates are
// skipped, a new stdio row is kept as a (failed but visible) diagnostic entry.
func TestMCPWorkspaceStdioDedup(t *testing.T) {
	assembly := []toolkit.MCPServer{{
		Name: "assembly-stdio", Command: "zeokeel-audit-missing-binary", Args: []string{"--mode", "a"},
	}}
	workspaceFile := `{"servers":[` +
		`{"name":"ws-dup-stdio","command":"zeokeel-audit-missing-binary","args":["--mode","a"]},` +
		`{"name":"ws-new-stdio","command":"zeokeel-audit-missing-binary-2","args":["b"]}]}`
	h := newHarness(t)
	plugins := startToolkitWithWorkspace(t, h, assembly, "", "true", workspaceWithMCPFile(t, workspaceFile))
	names := pluginNames(plugins.MCP())
	if !hasPluginName(plugins.MCP(), "assembly-stdio") || !hasPluginName(plugins.MCP(), "ws-new-stdio") {
		t.Fatalf("MCP() names = %v, want the assembly and new workspace stdio servers", names)
	}
	if hasPluginName(plugins.MCP(), "ws-dup-stdio") {
		t.Fatalf("duplicate stdio server was merged: %v", names)
	}
	if !h.logger.contains("duplicate stdio MCP server") {
		t.Fatalf("duplicate stdio was not logged: %v", h.logger.lines)
	}
}

// TestMCPWorkspaceMissingOrUnavailable covers the opt-in with no workspace
// service, an empty/absent current workspace and a current-workspace error.
func TestMCPWorkspaceMissingOrUnavailable(t *testing.T) {
	t.Run("no workspace service", func(t *testing.T) {
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true", nil)
		if got := plugins.MCP(); len(got) != 0 {
			t.Fatalf("MCP() = %v without a workspace service, want none", pluginNames(got))
		}
	})
	t.Run("no current workspace", func(t *testing.T) {
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true", &fakeMCPWorkspace{})
		if got := plugins.MCP(); len(got) != 0 {
			t.Fatalf("MCP() = %v with no current workspace, want none", pluginNames(got))
		}
	})
	t.Run("empty workspace root", func(t *testing.T) {
		h := newHarness(t)
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true",
			&fakeMCPWorkspace{current: contracts.Workspace{ID: "ws"}, has: true})
		if got := plugins.MCP(); len(got) != 0 {
			t.Fatalf("MCP() = %v with an empty workspace root, want none", pluginNames(got))
		}
	})
	t.Run("current error", func(t *testing.T) {
		h := newHarness(t)
		workspace := &fakeMCPWorkspace{currentErr: errors.New("workspace index corrupted")}
		plugins := startToolkitWithWorkspace(t, h, nil, "", "true", workspace)
		if got := plugins.MCP(); len(got) != 0 {
			t.Fatalf("MCP() = %v after a workspace error, want none", pluginNames(got))
		}
		if !h.logger.contains("workspace mcp.json: current workspace") {
			t.Fatalf("workspace lookup error was not logged: %v", h.logger.lines)
		}
	})
}

// TestMCPWorkspaceDisabledEvenWithFile covers the opt-in gate: with the flag off
// an existing workspace mcp.json is never read (its servers stay invisible).
func TestMCPWorkspaceDisabledEvenWithFile(t *testing.T) {
	h := newHarness(t)
	plugins := startToolkitWithWorkspace(t, h, nil, "", "false",
		workspaceWithMCPFile(t, `[{"name":"ws-disabled","url":"http://127.0.0.1:1/ws"}]`))
	if got := plugins.MCP(); hasPluginName(got, "ws-disabled") {
		t.Fatalf("the workspace file was read while opted out: %v", pluginNames(got))
	}
	if h.logger.contains("workspace mcp.json") {
		t.Fatalf("the opted-out module logged a workspace read: %v", h.logger.lines)
	}
}

// TestMCPWorkspaceDuplicateLogWording is a small wording guard: the assembly-wins
// case for settings rows and the earlier-source case for workspace rows stay
// distinguishable (a UI/operator diagnostic requirement, not a behaviour change).
func TestMCPWorkspaceDuplicateLogWording(t *testing.T) {
	assembly := []toolkit.MCPServer{{Name: "assembly", URL: "http://127.0.0.1:1/x", TimeoutMs: 50}}
	settingsRaw := `[{"name":"settings-dup","url":"http://127.0.0.1:1/x"}]`
	h := newHarness(t)
	startToolkitWithWorkspace(t, h, assembly, settingsRaw, "true", nil)
	if !h.logger.contains("assembly wins") {
		t.Fatalf("settings duplicate did not log assembly-wins: %v", h.logger.lines)
	}
	if strings.Contains(strings.Join(h.logger.lines, "\n"), "workspace mcp.json") {
		t.Fatalf("workspace file was read without a workspace service")
	}
}
