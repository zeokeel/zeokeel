package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/lsp"
	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

func buildLSPRegistry(t *testing.T, st *store.Store, dir string) *runtime.Registry {
	t.Helper()
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("registerAllTools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	return reg
}

func invokeRegistryTool(t *testing.T, reg *runtime.Registry, name string, args map[string]any) (string, error) {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal %s args: %v", name, err)
	}
	return tool.Invoke(context.Background(), raw)
}

func waitForPath(t *testing.T, path string, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		present := err == nil
		if present == want {
			if !want {
				// Give a buggy execution a chance to create it before passing.
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(path); err == nil {
					break
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if want {
		t.Fatalf("%s did not appear; the configured language server was not started", path)
	}
	t.Fatalf("%s exists although the workspace configuration must be ignored", path)
}

func hasName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestLSPToolRegisteredWithModeRouting pins the registry wiring: the lsp tool
// is registered by default, Read Only keeps it (it is read-only), Standard and
// Creator expose it, and Minimal mode's exact allowlist hides it.
func TestLSPToolRegisteredWithModeRouting(t *testing.T) {
	st, dir := newToolStore(t)
	reg := buildLSPRegistry(t, st, dir)
	if _, ok := reg.Get("lsp"); !ok {
		t.Fatal("the production registry does not contain the lsp tool")
	}
	names := make([]string, 0, len(reg.List()))
	for _, tool := range reg.List() {
		names = append(names, tool.Name())
	}
	for mode, want := range map[string]bool{
		runtime.ModeStandard: true,
		runtime.ModeCreator:  true,
		runtime.ModeMinimal:  false,
	} {
		if got := hasName(runtime.ModeTools(mode, names), "lsp"); got != want {
			t.Errorf("mode %s exposes lsp = %v, want %v", mode, got, want)
		}
	}
	readOnlyReg, _ := buildForPermission(t, session.PermissionAsk)
	if _, ok := readOnlyReg.Get("lsp"); !ok {
		t.Error("Read Only lost the read-only lsp tool")
	}
}

// TestLSPToolPluginToggle proves plugin.lsp.enabled removes the tool at build
// time instead of only persisting a preference.
func TestLSPToolPluginToggle(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting("plugin.lsp.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	reg := buildLSPRegistry(t, st, dir)
	if _, ok := reg.Get("lsp"); ok {
		t.Fatal("plugin.lsp.enabled=false kept the lsp tool registered")
	}
}

// TestLSPToolWithoutServersStableError covers the requested behavior: the tool
// is callable with no language server configured and fails with a stable,
// readable "not configured" error that points at lsp.servers.
func TestLSPToolWithoutServersStableError(t *testing.T) {
	st, dir := newToolStore(t)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := buildLSPRegistry(t, st, dir)
	_, err := invokeRegistryTool(t, reg, "lsp", map[string]any{
		"operation": "goToDefinition", "file": "main.go", "line": 1, "column": 1,
	})
	if err == nil {
		t.Fatal("lsp call without a configured server succeeded")
	}
	var noServer *lsp.NoServerError
	if !errors.As(err, &noServer) {
		t.Fatalf("error = %v (%T), want *lsp.NoServerError", err, err)
	}
	for _, want := range []string{"no language server is configured", "lsp.servers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TestLSPToolInvalidSettingsReported proves a malformed lsp.servers value does
// not fail the agent build; the error is reported by the tool instead.
func TestLSPToolInvalidSettingsReported(t *testing.T) {
	st, dir := newToolStore(t)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("lsp.servers", "{not json"); err != nil {
		t.Fatal(err)
	}
	reg := buildLSPRegistry(t, st, dir)
	_, err := invokeRegistryTool(t, reg, "lsp", map[string]any{
		"operation": "hover", "file": "main.go", "line": 1, "column": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "lsp.servers") {
		t.Fatalf("invalid lsp.servers error = %v, want the configuration problem reported", err)
	}
}

// TestLSPWorkspaceConfigGate is the RT-01-style security gate for the workspace
// language-server file: with the default policy .zeokeel/lsp.json must not
// start anything, and only lsp.workspace_config=allow may execute it.
func TestLSPWorkspaceConfigGate(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("the marker command uses /bin/sh")
	}
	st, dir := newToolStore(t)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "workspace-lsp-started")
	workspaceConfig, err := json.Marshal(map[string]any{
		"go": map[string]any{
			"command": "/bin/sh",
			"args":    []string{"-c", "touch " + marker},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".zeokeel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, lsp.WorkspaceConfigFile), workspaceConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"operation": "goToDefinition", "file": "main.go", "line": 1, "column": 1}

	// Default policy: the untrusted workspace file is ignored. The call sees
	// no configured server, and the declared command never runs.
	reg := buildLSPRegistry(t, st, dir)
	if _, err := invokeRegistryTool(t, reg, "lsp", args); err == nil {
		t.Fatal("lsp call with an ignored workspace config succeeded")
	} else if !strings.Contains(err.Error(), "no language server is configured") {
		t.Fatalf("default-policy error = %v, want the not-configured error", err)
	}
	waitForPath(t, marker, false)

	// Explicit opt-in: the declared command runs (it is not an LSP server, so
	// the query still fails, but the process must have started).
	if err := st.SetSetting(lsp.WorkspaceConfigKey, lsp.WorkspaceConfigAllow); err != nil {
		t.Fatal(err)
	}
	reg = buildLSPRegistry(t, st, dir)
	if _, err := invokeRegistryTool(t, reg, "lsp", args); err == nil {
		t.Fatal("lsp call against a non-LSP command succeeded")
	}
	waitForPath(t, marker, true)
}
