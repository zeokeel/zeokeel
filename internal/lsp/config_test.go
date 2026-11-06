package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkspaceConfig writes <root>/.zeokeel/lsp.json.
func writeWorkspaceConfig(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".zeokeel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lsp.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write workspace lsp.json: %v", err)
	}
}

func TestWorkspaceConfigAllowed(t *testing.T) {
	for _, value := range []string{"", "off", "OFF", " on", "on", "true", "1", "yes", "ask", "enabled"} {
		if WorkspaceConfigAllowed(value) {
			t.Errorf("lsp.workspace_config=%q enabled the workspace file; want fail-closed", value)
		}
	}
	for _, value := range []string{"allow", "ALLOW", " Allow "} {
		if !WorkspaceConfigAllowed(value) {
			t.Errorf("lsp.workspace_config=%q did not enable the workspace file", value)
		}
	}
}

func TestParseServersConfig(t *testing.T) {
	raw := `{
		"go": {"command": "gopls", "args": ["-remote=auto"], "rootURI": "file:///tmp/project"},
		"python": {"command": "pyright-langserver", "args": ["--stdio"], "env": ["FOO=bar"]}
	}`
	servers, err := ParseServersConfig(raw)
	if err != nil {
		t.Fatalf("ParseServersConfig: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %v, want go and python", servers)
	}
	if got := servers["go"]; got.Command != "gopls" || len(got.Args) != 1 || got.Args[0] != "-remote=auto" || got.RootURI != "file:///tmp/project" {
		t.Errorf("go server = %+v", got)
	}
	if got := servers["python"]; got.Command != "pyright-langserver" || len(got.Args) != 1 || got.Args[0] != "--stdio" {
		t.Errorf("python server = %+v", got)
	}
	if got := servers["python"]; len(got.Env) != 1 || got.Env[0] != "FOO=bar" {
		t.Errorf("python env = %v, want [FOO=bar]", got.Env)
	}

	for _, empty := range []string{"", "  ", "{}", "null"} {
		servers, err := ParseServersConfig(empty)
		if err != nil {
			t.Errorf("ParseServersConfig(%q): %v", empty, err)
			continue
		}
		if len(servers) != 0 {
			t.Errorf("ParseServersConfig(%q) = %v, want no servers", empty, servers)
		}
	}
}

func TestParseServersConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"invalid json", "{not json", "lsp.servers"},
		{"top level array", `[{"command":"gopls"}]`, "object"},
		{"missing command", `{"go": {"args": ["-remote=auto"]}}`, "go"},
		{"empty command", `{"go": {"command": "  "}}`, "go"},
		{"wrong args type", `{"go": {"command": "gopls", "args": "x"}}`, "go"},
		{"wrong language value type", `{"go": "gopls"}`, "go"},
	}
	for _, tc := range cases {
		_, err := ParseServersConfig(tc.raw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ParseServersConfig error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

func TestLoadServersWorkspaceGate(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceConfig(t, root, `{"go": {"command": "workspace-gopls", "args": ["serve"]}}`)

	// The default (and every ambiguous truthy alias) must not read the
	// untrusted workspace file: it only configures third-party code execution.
	for _, value := range []string{"", WorkspaceConfigOff, "on", "true"} {
		servers, err := LoadServers("", root, value)
		if err != nil {
			t.Errorf("LoadServers(workspace_config=%q): %v", value, err)
			continue
		}
		if len(servers) != 0 {
			t.Errorf("LoadServers(workspace_config=%q) read the workspace file: %v", value, servers)
		}
	}

	servers, err := LoadServers("", root, WorkspaceConfigAllow)
	if err != nil {
		t.Fatalf("LoadServers(allow): %v", err)
	}
	if got, ok := servers["go"]; !ok || got.Command != "workspace-gopls" {
		t.Fatalf("LoadServers(allow) = %v, want the workspace go server", servers)
	}
}

func TestLoadServersPrecedence(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceConfig(t, root, `{"go": {"command": "workspace-gopls"}}`)

	// An explicit lsp.servers setting is authoritative and the workspace file
	// is not even read.
	servers, err := LoadServers(`{"go": {"command": "settings-gopls"}}`, root, WorkspaceConfigAllow)
	if err != nil {
		t.Fatalf("LoadServers(settings): %v", err)
	}
	if got := servers["go"]; got.Command != "settings-gopls" {
		t.Fatalf("settings lsp.servers lost to the workspace file: %+v", servers)
	}

	// An invalid explicit setting is reported instead of silently falling back
	// to the workspace file.
	if _, err := LoadServers(`{"go": {"args": []}}`, root, WorkspaceConfigAllow); err == nil {
		t.Fatal("LoadServers with an invalid setting returned no error")
	}
}

func TestLoadServersMissingWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	servers, err := LoadServers("", root, WorkspaceConfigAllow)
	if err != nil {
		t.Fatalf("LoadServers without a workspace file: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %v, want empty", servers)
	}
}

func TestLoadServersMalformedWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceConfig(t, root, `{"go": `)
	if _, err := LoadServers("", root, WorkspaceConfigAllow); err == nil {
		t.Fatal("LoadServers with a malformed workspace file returned no error")
	}
}

func TestLoadServersEmptyRoot(t *testing.T) {
	servers, err := LoadServers("", "", WorkspaceConfigAllow)
	if err != nil {
		t.Fatalf("LoadServers without a root: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %v, want empty", servers)
	}
}
