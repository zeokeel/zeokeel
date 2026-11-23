package plugins

// Internal tests for the workspace-scoped MCP switches: disabled servers and
// tools live under a per-workspace settings key, so toggling one workspace never
// changes another.

import (
	"context"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// scopeWorkspace is a minimal WorkspaceService pinned to one current folder.
type scopeWorkspace struct{ id string }

func (w scopeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return []contracts.Workspace{{ID: w.id, Root: "/work/" + w.id}}, nil
}
func (w scopeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return contracts.Workspace{ID: w.id, Root: "/work/" + w.id}, true, nil
}
func (w scopeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (w scopeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (w scopeWorkspace) Delete(context.Context, string) error { return nil }
func (w scopeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (w scopeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }
func (w scopeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// scopeSettings is an in-memory settings store for the switch tests.
type scopeSettings struct{ values map[string]string }

func (s *scopeSettings) Get(key, def string) string {
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}
func (s *scopeSettings) Set(key, value string) error {
	s.values[key] = value
	return nil
}
func (s *scopeSettings) All() map[string]string {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func TestMCPDisabledSwitchesAreWorkspaceScoped(t *testing.T) {
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	if _, err := kernel.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(scopeWorkspace{id: "ws-a"})); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	st := &state{ctx: kernel}
	if got := st.workspaceScopeID(); got != "ws-a" {
		t.Fatalf("workspace scope = %q, want ws-a", got)
	}
	settings := &scopeSettings{values: map[string]string{
		contracts.MCPDisabledServersSettingKey("ws-a"): `["alpha"]`,
		// A legacy unscoped list belongs to the no-workspace scope: it must not
		// leak into ws-a.
		contracts.SettingMCPDisabledServers: `["beta"]`,
	}}
	st.loadDisabledServers(settings, true)
	if !st.serverDisabled("alpha") {
		t.Fatal("ws-a's disabled server is not applied")
	}
	if st.serverDisabled("beta") {
		t.Fatal("the unscoped legacy disabled list leaked into ws-a")
	}

	st.setServerEnabled("gamma", false, settings)
	if got := settings.Get(contracts.MCPDisabledServersSettingKey("ws-a"), ""); !strings.Contains(got, "gamma") {
		t.Fatalf("ws-a disabled list = %q, want gamma", got)
	}
	if got := settings.Get(contracts.SettingMCPDisabledServers, ""); strings.Contains(got, "gamma") {
		t.Fatalf("toggle wrote the unscoped key: %q", got)
	}
}

func TestWorkspaceScopeFallsBackToUnscopedWithoutWorkspace(t *testing.T) {
	st := &state{ctx: cordis.Root()}
	t.Cleanup(func() { _ = st.ctx.DisposeAll() })
	if got := st.workspaceScopeID(); got != "" {
		t.Fatalf("workspace scope = %q, want empty", got)
	}
	if got := contracts.MCPDisabledServersSettingKey(""); got != contracts.SettingMCPDisabledServers {
		t.Fatalf("unscoped disabled key = %q, want %q", got, contracts.SettingMCPDisabledServers)
	}
}

// mutableScopeWorkspace is scopeWorkspace with a switchable current ID.
type mutableScopeWorkspace struct {
	scopeWorkspace
}

func (w *mutableScopeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return contracts.Workspace{ID: w.id, Root: "/work/" + w.id}, true, nil
}

// TestWorkspaceScopeChangeInvalidatesConnectorCaches pins that a switch while
// the connectors page is mounted reloads the scoped rows and switches.
func TestWorkspaceScopeChangeInvalidatesConnectorCaches(t *testing.T) {
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	workspace := &mutableScopeWorkspace{scopeWorkspace: scopeWorkspace{id: "ws-a"}}
	if _, err := kernel.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	st := &state{ctx: kernel, connectors: &connectorsState{loaded: true, workspaceID: "ws-a"}}
	st.disabledServersLoaded = true
	if got := st.workspaceScopeID(); got != "ws-a" {
		t.Fatalf("scope = %q, want ws-a", got)
	}
	workspace.id = "ws-b"
	if got := st.workspaceScopeID(); got != "ws-b" {
		t.Fatalf("scope after switch = %q, want ws-b", got)
	}
	if st.connectors.loaded || st.connectors.workspaceID != "" {
		t.Fatalf("connectors cache survived the switch: %+v", st.connectors)
	}
	if st.disabledServersLoaded {
		t.Fatal("disabled-switch cache survived the switch")
	}
}
