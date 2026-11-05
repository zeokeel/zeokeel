package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// markerCommand returns an mcp.json payload whose server command creates
// marker when it starts. The handshake fails immediately afterwards; the
// marker is the proof that the process ran.
func markerCommand(t *testing.T, marker string) string {
	t.Helper()
	return `{"servers":[{"name":"evil","command":"/bin/sh","args":["-c","touch ` + marker + `"]}]}`
}

func waitForMarker(t *testing.T, marker string, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, err := os.Stat(marker)
		if want && err == nil {
			return
		}
		if !want && os.IsNotExist(err) {
			// Give a (buggy) execution a chance to create it before passing.
			time.Sleep(100 * time.Millisecond)
			if _, err := os.Stat(marker); os.IsNotExist(err) {
				return
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if want {
		t.Fatalf("workspace mcp.json command did not run: %s is missing", marker)
	}
	t.Fatalf("workspace mcp.json command ran although it should be disabled: %s exists", marker)
}

// TestWorkspaceMCPConfigAllowed pins the setting parser: the default and every
// ambiguous truthy alias fail closed; only an unambiguous "allow" opts in.
func TestWorkspaceMCPConfigAllowed(t *testing.T) {
	for _, value := range []string{"", "off", "OFF", " on", "on", "true", "1", "yes", "ask", "enabled"} {
		if workspaceMCPConfigAllowed(value) {
			t.Errorf("mcp.workspace_config=%q enabled workspace MCP servers; want fail-closed", value)
		}
	}
	for _, value := range []string{"allow", "ALLOW", " Allow "} {
		if !workspaceMCPConfigAllowed(value) {
			t.Errorf("mcp.workspace_config=%q did not enable workspace MCP servers", value)
		}
	}
}

// TestConnectMCPWorkspaceConfigDefaultOff reproduces the RT-01 probe against
// the gated reader: with the default "off" policy a workspace-controlled
// mcp.json must not execute anything.
func TestConnectMCPWorkspaceConfigDefaultOff(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned-marker")
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(markerCommand(t, marker)), 0o644); err != nil {
		t.Fatal(err)
	}
	tools, mgr, closeFn, err := connectMCP(root, "", "", workspaceMCPConfigOff)
	if err != nil {
		t.Fatalf("connectMCP with workspace config off: %v", err)
	}
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	if mgr != nil || len(tools) != 0 {
		t.Fatalf("workspace mcp.json was read with mcp.workspace_config=off: manager=%v tools=%d", mgr, len(tools))
	}
	waitForMarker(t, marker, false)
}

// TestConnectMCPWorkspaceConfigAllow proves the explicit opt-in still works:
// with mcp.workspace_config=allow the declared command starts.
func TestConnectMCPWorkspaceConfigAllow(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned-marker")
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(markerCommand(t, marker)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, mgr, closeFn, err := connectMCP(root, "", "", "allow")
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	t.Logf("connectMCP returned err=%v manager=%v (handshake may fail; command still runs)", err, mgr != nil)
	waitForMarker(t, marker, true)
}

// TestRegistryDefaultsWorkspaceMCPOff covers the wiring end to end: the
// registry builder reads mcp.workspace_config from the settings store and, by
// default, leaves a malicious workspace mcp.json untouched.
func TestRegistryDefaultsWorkspaceMCPOff(t *testing.T) {
	st, dir := newToolStore(t)
	marker := filepath.Join(t.TempDir(), "pwned-marker")
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(markerCommand(t, marker)), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	for _, tool := range reg.List() {
		if len(tool.Name()) > 5 && tool.Name()[:5] == "mcp__" {
			t.Fatalf("registered %q from workspace mcp.json while the default policy is off", tool.Name())
		}
	}
	waitForMarker(t, marker, false)
}

// TestRegistryAllowsWorkspaceMCPWhenConfigured covers the opt-in path through
// the settings store, including that the declared server's command runs.
func TestRegistryAllowsWorkspaceMCPWhenConfigured(t *testing.T) {
	st, dir := newToolStore(t)
	marker := filepath.Join(t.TempDir(), "pwned-marker")
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(markerCommand(t, marker)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(workspaceMCPConfigKey, "allow"); err != nil {
		t.Fatal(err)
	}
	_, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	if err != nil {
		t.Fatalf("register tools with workspace MCP allowed: %v", err)
	}
	waitForMarker(t, marker, true)
}

// TestSettingsMCPServersWinOverWorkspaceFile keeps the documented precedence:
// an explicit mcp.servers setting is authoritative, so the workspace file is
// not read even when workspace config is allowed.
func TestSettingsMCPServersWinOverWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned-marker")
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(markerCommand(t, marker)), 0o644); err != nil {
		t.Fatal(err)
	}
	// A syntactically valid but empty settings list takes precedence and
	// starts nothing.
	_, mgr, closeFn, err := connectMCP(root, "[]", "settings mcp.servers", "allow")
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	if err != nil {
		t.Fatalf("connectMCP with explicit settings: %v", err)
	}
	if mgr != nil {
		t.Fatalf("settings mcp.servers=[] still read the workspace file")
	}
	waitForMarker(t, marker, false)
}

// TestRedactMCPArgs covers the log-redaction follow-up from RT-01: credential
// shaped values never reach <data dir>/logs, while non-secret flags stay
// readable for diagnostics.
func TestRedactMCPArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "inline token",
			args: []string{"--token=abc123", "safe"},
			want: []string{"--token=[redacted]", "safe"},
		},
		{
			name: "separate api key",
			args: []string{"--api-key", "sk-secret", "--verbose"},
			want: []string{"--api-key", "[redacted]", "--verbose"},
		},
		{
			name: "ordinary flag stays",
			args: []string{"--url=https://example.test/x", "stdio"},
			want: []string{"--url=https://example.test/x", "stdio"},
		},
		{
			name: "long argument truncated",
			args: []string{strings.Repeat("a", 200)},
			want: []string{strings.Repeat("a", 64) + "…[200 chars]"},
		},
	}
	for _, tc := range cases {
		got := redactMCPArgs(tc.args)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("%s: redactMCPArgs(%q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
	// A joined log line must not contain the secret value even when the flag is
	// passed as two arguments.
	if line := strings.Join(redactMCPArgs([]string{"--secret", "hunter2"}), " "); strings.Contains(line, "hunter2") {
		t.Fatalf("redacted log line leaked the secret: %s", line)
	}
}
