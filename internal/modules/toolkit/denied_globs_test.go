package toolkit

// Opt-in denied-globs wiring (2026-11 audit): the settings key
// toolkit.deniedGlobs hides workspace files (secrets like .env/*.pem) from the
// FILE tools. The default is empty — opt-in, no friction for personal use.
// Disclosure that must stay true: this is defense in depth for the file tools
// only; the shell bypass remains (bash/pwsh can still cat a denied file, and
// the command allowlist is advisory), so the key is not a security boundary.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
)

// TestResolveDeniedGlobs pins the settings-row semantics: absent keeps the
// assembly config, a valid list overrides it (an explicit empty list opts back
// out), a malformed row keeps the config instead of failing activation.
func TestResolveDeniedGlobs(t *testing.T) {
	cfg := Config{DeniedGlobs: []string{"**/*.assembly-secret"}}
	cases := []struct {
		name string
		row  string
		want []string
	}{
		{"absent keeps assembly", "", []string{"**/*.assembly-secret"}},
		{"valid list overrides", `["**/.env","**/*.pem"]`, []string{"**/.env", "**/*.pem"}},
		{"explicit empty opts out", `[]`, []string{}},
		{"blank entries dropped", `[" **/.env ",""]`, []string{"**/.env"}},
		{"malformed keeps assembly", `not-json`, []string{"**/*.assembly-secret"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			settings := newMutableSettings(map[string]string{})
			if testCase.row != "" {
				settings.Set("toolkit.deniedGlobs", testCase.row)
			}
			got := resolveDeniedGlobs(cfg, settings, true, t.Logf)
			if len(got) != len(testCase.want) {
				t.Fatalf("resolveDeniedGlobs(%q) = %v, want %v", testCase.row, got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("resolveDeniedGlobs(%q) = %v, want %v", testCase.row, got, testCase.want)
				}
			}
		})
	}
	// Without a settings service the assembly config is the only source.
	if got := resolveDeniedGlobs(cfg, nil, false, t.Logf); len(got) != 1 || got[0] != "**/*.assembly-secret" {
		t.Fatalf("resolveDeniedGlobs(no settings) = %v, want the assembly list", got)
	}
}

// writeSecretWorkspace creates a root with a .env secret and a visible file.
func writeSecretWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDeniedGlobsSettingHidesFilesFromTools: with the opt-in row set, read_file
// denies the secret and glob does not list it, while normal files stay usable.
func TestDeniedGlobsSettingHidesFilesFromTools(t *testing.T) {
	root := writeSecretWorkspace(t)
	settings := newMutableSettings(map[string]string{
		"toolkit.deniedGlobs": `["**/.env"]`,
	})
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, settings)
	ctx := context.Background()

	result, err := registry.Invoke(ctx, rig.ToolCall{Name: "read_file", Arguments: `{"path":".env"}`})
	if err == nil && !result.IsError {
		t.Errorf("read_file(.env) succeeded although toolkit.deniedGlobs hides it: %q", result.Content)
	}
	listed, err := registry.Invoke(ctx, rig.ToolCall{Name: "glob", Arguments: `{"pattern":"*"}`})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if strings.Contains(listed.Content, ".env") {
		t.Errorf("glob still lists the denied secret: %q", listed.Content)
	}
	visible, err := registry.Invoke(ctx, rig.ToolCall{Name: "read_file", Arguments: `{"path":"visible.txt"}`})
	if err != nil || visible.IsError {
		t.Errorf("control invalid: visible.txt must stay readable, got %q/%v", visible.Content, err)
	}
}

// TestDeniedGlobsDefaultEmptyKeepsFilesVisible pins the personal-scale default:
// without the opt-in row nothing is hidden (no friction), and an explicit
// empty list behaves the same.
func TestDeniedGlobsDefaultEmptyKeepsFilesVisible(t *testing.T) {
	for _, row := range []string{"", `[]`} {
		root := writeSecretWorkspace(t)
		values := map[string]string{}
		if row != "" {
			values["toolkit.deniedGlobs"] = row
		}
		settings := newMutableSettings(values)
		_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, settings)
		result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "read_file", Arguments: `{"path":".env"}`})
		if err != nil || result.IsError {
			t.Errorf("row %q: default must keep .env readable (opt-in), got %q/%v", row, result.Content, err)
		}
	}
}

// TestDeniedGlobsAssemblyConfigStillApplies: the assembly-level deniedGlobs
// keep working when the settings row is absent (deployer choice).
func TestDeniedGlobsAssemblyConfigStillApplies(t *testing.T) {
	root := writeSecretWorkspace(t)
	settings := newMutableSettings(map[string]string{})
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{
		"root": root, "deniedGlobs": []any{"**/.env"},
	}, settings)
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "read_file", Arguments: `{"path":".env"}`})
	if err == nil && !result.IsError {
		t.Errorf("assembly deniedGlobs stopped applying: read_file(.env) = %q", result.Content)
	}
}
