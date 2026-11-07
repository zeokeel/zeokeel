package toolkit

// Tests for the workspace mcp.json opt-in merge.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
	return contracts.Workspace{}, nil
}

func (w *fakeMCPWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
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

func activateMCPToolkitWithWorkspace(t *testing.T, config map[string]any, settings contracts.SettingsService,
	logger contracts.Logger, workspace contracts.WorkspaceService) (*rigtool.Registry, contracts.PluginsService) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if settings != nil {
		if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	if logger != nil {
		if _, err := root.Provide(contracts.KeyLogger, logger); err != nil {
			t.Fatalf("provide logger: %v", err)
		}
	}
	if workspace != nil {
		if _, err := root.Provide(contracts.KeyWorkspace, workspace); err != nil {
			t.Fatalf("provide workspace: %v", err)
		}
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: config,
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	plugins, err := root.Require(contracts.KeyPlugins)
	if err != nil {
		t.Fatalf("require plugins: %v", err)
	}
	return tools, plugins
}

// writeWorkspaceMCPFile creates a workspace root with mcp.json and returns the
// fake service pointing at it.
func writeWorkspaceMCPFile(t *testing.T, content string) *fakeMCPWorkspace {
	t.Helper()
	root := t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write workspace mcp.json: %v", err)
		}
	}
	return &fakeMCPWorkspace{
		current: contracts.Workspace{ID: "ws", Name: "ws", Root: root},
		has:     true,
	}
}

func pluginNames(plugins contracts.PluginsService) []string {
	out := make([]string, 0)
	for _, plugin := range plugins.MCP() {
		out = append(out, plugin.Name)
	}
	return out
}

func TestWorkspaceMCPObjectAndArrayShapes(t *testing.T) {
	for name, content := range map[string]string{
		"object": `{"servers":[{"name":"ws-one","url":"http://127.0.0.1:1/one"},{"name":"ws-two","url":"http://127.0.0.1:1/two"}]}`,
		"array":  `[{"name":"ws-one","url":"http://127.0.0.1:1/one"},{"name":"ws-two","url":"http://127.0.0.1:1/two"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			settings := newMutableSettings(map[string]string{contracts.SettingMCPWorkspaceOptIn: "true"})
			workspace := writeWorkspaceMCPFile(t, content)
			_, plugins := activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, nil, workspace)
			names := pluginNames(plugins)
			if len(names) != 2 || names[0] != "ws-one" || names[1] != "ws-two" {
				t.Fatalf("workspace MCP entries = %v", names)
			}
		})
	}
}

func TestWorkspaceMCPPriorityAndDisabled(t *testing.T) {
	workspace := writeWorkspaceMCPFile(t, `{"servers":[
		{"name":"assembly-dup","url":"http://127.0.0.1:1/asm"},
		{"name":"settings-dup","url":"http://127.0.0.1:1/settings"},
		{"name":"workspace-new","url":"http://127.0.0.1:1/ws"}
	]}`)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPWorkspaceOptIn: "true",
		contracts.SettingMCPServers:        `[{"name":"settings","url":"http://127.0.0.1:1/settings"}]`,
	})
	logger := &captureLogger{}
	_, plugins := activateMCPToolkitWithWorkspace(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "assembly", "url": "http://127.0.0.1:1/asm"},
		},
	}, settings, logger, workspace)
	names := pluginNames(plugins)
	if len(names) != 3 || names[0] != "assembly" || names[1] != "settings" || names[2] != "workspace-new" {
		t.Fatalf("priority MCP entries = %v", names)
	}
	if !logger.contains("workspace mcp.json") {
		t.Fatalf("warnings = %v, want workspace duplicate/merge warning", logger.lines)
	}

	// Opt-in off (and malformed values) leave the workspace file unread.
	for _, value := range []string{"", "false", "yes", "1"} {
		settings := newMutableSettings(map[string]string{contracts.SettingMCPWorkspaceOptIn: value})
		_, plugins := activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, nil, workspace)
		if names := pluginNames(plugins); len(names) != 0 {
			t.Fatalf("opt-in %q read workspace MCP entries: %v", value, names)
		}
	}
}

func TestWorkspaceMCPMissingSourcesAndBadJSON(t *testing.T) {
	// No workspace service: nothing to read.
	settings := newMutableSettings(map[string]string{contracts.SettingMCPWorkspaceOptIn: "true"})
	_, plugins := activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, nil, nil)
	if names := pluginNames(plugins); len(names) != 0 {
		t.Fatalf("entries without workspace = %v", names)
	}

	// Opt-in on but no current workspace.
	noCurrent := &fakeMCPWorkspace{}
	_, plugins = activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, nil, noCurrent)
	if names := pluginNames(plugins); len(names) != 0 {
		t.Fatalf("entries without current workspace = %v", names)
	}

	// Opt-in on, current workspace, but no mcp.json file.
	missing := writeWorkspaceMCPFile(t, "")
	_, plugins = activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, nil, missing)
	if names := pluginNames(plugins); len(names) != 0 {
		t.Fatalf("entries without mcp.json = %v", names)
	}

	// Bad JSON is a warning, never an activation failure.
	bad := writeWorkspaceMCPFile(t, `{"servers":not-json}`)
	logger := &captureLogger{}
	_, plugins = activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, logger, bad)
	if names := pluginNames(plugins); len(names) != 0 {
		t.Fatalf("entries after bad JSON = %v", names)
	}
	if !logger.contains("workspace mcp.json") {
		t.Fatalf("warnings = %v, want workspace parse warning", logger.lines)
	}

	// Invalid rows are skipped; valid rows still load.
	mixed := writeWorkspaceMCPFile(t, `{"servers":[
		{"name":"","url":"http://127.0.0.1:1/empty"},
		{"name":"bad\u0000name","url":"http://127.0.0.1:1/nul"},
		{"name":"good","url":"http://127.0.0.1:1/good"}
	]}`)
	_, plugins = activateMCPToolkitWithWorkspace(t, map[string]any{"root": "."}, settings, &captureLogger{}, mixed)
	if names := pluginNames(plugins); len(names) != 1 || names[0] != "good" {
		t.Fatalf("entries after invalid rows = %v", names)
	}
}

func TestWorkspaceMCPOptInSettingNames(t *testing.T) {
	if contracts.SettingMCPWorkspaceOptIn != "mcp.workspaceOptIn" {
		t.Fatalf("workspace opt-in key = %q", contracts.SettingMCPWorkspaceOptIn)
	}
	if !mcpWorkspaceOptInEnabled(" TRUE ") || mcpWorkspaceOptInEnabled("yes") || mcpWorkspaceOptInEnabled("") {
		t.Fatal("mcpWorkspaceOptInEnabled normalization is wrong")
	}
}
