package toolkit

// Behavioral contract for the opt-in hidden-paths setting (toolkit.deniedGlobs,
// set from 设置 → 数据与隐私 → 文件隐藏) on the rig v0.26.1 SDK.
//
// History: until rig v0.24.0 the enforcement lived in rig's sandbox policy
// ("denied globs"); v0.24.0 downgraded it to record-only and the product gated
// the tools itself; v0.25.0 replaced it with first-class hidden paths
// (sandbox.WithHidden) that participate in every path decision. The product now
// only widens the configured patterns (see hiddenPatterns) and hands them to the
// SDK — these tests pin the promise the setting makes:
//
//   - a configured path is invisible to the model: reads, directory listings,
//     glob/grep results and writes are all refused;
//   - a configured directory hides its whole subtree (a user who hides .git
//     must not be able to read .git/config by guessing the path);
//   - a pattern without "/" matches that name at any depth (basename rule);
//   - the default (empty list) hides nothing at all.
//
// Everything runs through the real toolkit registry, so the tests cover the
// tools the model actually gets (read_file/list_dir/glob/grep/editor/image and
// the LSP wrappers) instead of a policy helper in isolation.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
)

// hiddenWorkspace writes a workspace with a secret file, a secret directory and
// a normal file, and returns its root.
func hiddenWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(root, "visible.txt"), "public\n")
	writeFile(t, filepath.Join(root, ".git", "config"), "[remote]\n\turl = git@example.test:origin.git\n")
	writeFile(t, filepath.Join(root, "keys", "id_rsa"), "PRIVATE KEY\n")
	writeFile(t, filepath.Join(root, "secrets", "token"), "ghp_SECRET\n")
	return root
}

// writeFile creates one file (and its parents) or fails the test.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// hiddenRegistry activates the toolkit over root with the given assembly-level
// deniedGlobs (the settings row is exercised by denied_globs_test.go).
func hiddenRegistry(t *testing.T, root string, globs ...string) *rigtool.Registry {
	t.Helper()
	values := make([]any, 0, len(globs))
	for _, glob := range globs {
		values = append(values, glob)
	}
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{
		"root": root, "deniedGlobs": values,
	}, newMutableSettings(map[string]string{}))
	return registry
}

// invoke runs one tool through the registry and fails on a transport error.
func invoke(t *testing.T, registry *rigtool.Registry, name, arguments string) rig.ToolResult {
	t.Helper()
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s(%s): %v", name, arguments, err)
	}
	return result
}

// TestHiddenPathsCoverDirectorySubtree is the regression that motivated the
// pattern widening: hiding "**/.git" must also hide .git/config, not just the
// directory entry (rig's matcher alone compares one path, so ".git/config"
// would not match "**/.git").
func TestHiddenPathsCoverDirectorySubtree(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root, "**/.git")

	for name, call := range map[string]struct{ tool, args string }{
		"read_file":       {"read_file", `{"path":".git/config"}`},
		"grep_inside":     {"grep", `{"pattern":"origin","path":".git"}`},
		"list_dir_inside": {"list_dir", `{"path":".git"}`},
	} {
		result := invoke(t, registry, call.tool, call.args)
		if !result.IsError {
			t.Errorf("%s: %s reached the hidden directory: %q", name, call.tool, result.Content)
		}
	}

	// An enumeration tool that names the hidden directory pattern-match style
	// simply finds nothing (listing tools hide matches instead of failing).
	if inside := invoke(t, registry, "glob", `{"pattern":".git/*"}`); strings.Contains(inside.Content, ".git") {
		t.Errorf("glob(.git/*) leaked a hidden path: %q", inside.Content)
	}
	// The directory entry itself stays out of listings, and its content never
	// shows up in content search results.
	listed := invoke(t, registry, "list_dir", `{"path":"."}`)
	if strings.Contains(listed.Content, ".git") {
		t.Errorf("list_dir still shows the hidden directory: %q", listed.Content)
	}
	globbed := invoke(t, registry, "glob", `{"pattern":"**"}`)
	if strings.Contains(globbed.Content, ".git") {
		t.Errorf("glob still lists the hidden directory: %q", globbed.Content)
	}
	grep := invoke(t, registry, "grep", `{"pattern":"origin","path":"."}`)
	if strings.Contains(grep.Content, ".git/config") || strings.Contains(grep.Content, "origin.git") {
		t.Errorf("grep leaked hidden directory content: %q", grep.Content)
	}
}

// TestHiddenPathsBasenamePatternMatchesAnyDepth pins the documented basename
// rule: a pattern without "/" (the settings placeholder "id_rsa") hides that
// file name anywhere in the tree, subtree included.
func TestHiddenPathsBasenamePatternMatchesAnyDepth(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root, "id_rsa")

	result := invoke(t, registry, "read_file", `{"path":"keys/id_rsa"}`)
	if !result.IsError {
		t.Errorf("read_file(keys/id_rsa) succeeded although id_rsa is hidden: %q", result.Content)
	}
	globbed := invoke(t, registry, "glob", `{"pattern":"**/*"}`)
	if strings.Contains(globbed.Content, "id_rsa") {
		t.Errorf("glob still lists the hidden key: %q", globbed.Content)
	}
	// Control: an unrelated file in the same directory stays visible.
	if control := invoke(t, registry, "read_file", `{"path":"visible.txt"}`); control.IsError {
		t.Errorf("control file became unreadable: %q", control.Content)
	}
}

// TestHiddenPathsRejectWritesAndKeepContent pins the write half: neither the
// hidden path nor a path inside a hidden directory may be written, and a
// rejected write leaves the original content intact.
func TestHiddenPathsRejectWritesAndKeepContent(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root, "**/.env", "**/.git")

	for name, call := range map[string]struct{ tool, args string }{
		"overwrite": {"write_file", `{"path":".env","content":"HACKED=1"}`},
		"inside":    {"write_file", `{"path":".git/hooks/pre-commit","content":"echo hi"}`},
	} {
		result := invoke(t, registry, call.tool, call.args)
		if !result.IsError {
			t.Errorf("%s: %s wrote to a hidden path: %q", name, call.tool, result.Content)
		}
	}
	content, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if string(content) != "SECRET=1\n" {
		t.Fatalf(".env content changed to %q; a denied write must not touch the file", content)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".git", "hooks", "pre-commit")); statErr == nil {
		t.Fatal(".git/hooks/pre-commit was created inside a hidden directory")
	}
}

// TestHiddenPathsAbsoluteSpellingRejected: the same decision applies when the
// model names the file by absolute path instead of a root-relative one.
func TestHiddenPathsAbsoluteSpellingRejected(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root, "**/.env")
	absolute := filepath.Join(root, ".env")
	result := invoke(t, registry, "read_file", `{"path":`+quoteJSON(absolute)+`}`)
	if !result.IsError {
		t.Fatalf("read_file(%q) succeeded although .env is hidden: %q", absolute, result.Content)
	}
}

// TestHiddenPathsDirectorySuffixPatternHidesEntryAndContents pins the
// "dir/**" spelling: the directory entry and everything below it are hidden,
// not only the contents.
func TestHiddenPathsDirectorySuffixPatternHidesEntryAndContents(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root, "secrets/**")

	if result := invoke(t, registry, "read_file", `{"path":"secrets/token"}`); !result.IsError {
		t.Errorf("read_file(secrets/token) succeeded although secrets/** is hidden: %q", result.Content)
	}
	if result := invoke(t, registry, "list_dir", `{"path":"secrets"}`); !result.IsError {
		t.Errorf("list_dir(secrets) succeeded although secrets/** is hidden: %q", result.Content)
	}
	listed := invoke(t, registry, "list_dir", `{"path":"."}`)
	if strings.Contains(listed.Content, "secrets") {
		t.Errorf("list_dir still shows the hidden directory entry: %q", listed.Content)
	}
}

// TestHiddenPathsFollowWorkingDirectory pins the cwd half: after the model
// changes directory, a relative path is resolved against that directory and the
// hidden decision follows the real file, not the spelling.
func TestHiddenPathsFollowWorkingDirectory(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root, "**/.env")
	if result := invoke(t, registry, "working_directory", `{"path":"keys"}`); result.IsError {
		t.Fatalf("working_directory(keys): %q", result.Content)
	}
	if result := invoke(t, registry, "read_file", `{"path":"../.env"}`); !result.IsError {
		t.Errorf("read_file(../.env) from keys/ succeeded although .env is hidden: %q", result.Content)
	}
	// Control: a file in the new cwd stays readable.
	if result := invoke(t, registry, "read_file", `{"path":"id_rsa"}`); result.IsError {
		t.Errorf("control file id_rsa became unreadable: %q", result.Content)
	}
}

// TestHiddenPathsDefaultHidesNothing is the opt-in guard: with no patterns the
// policy carries no hidden entries at all, so every path stays visible.
func TestHiddenPathsDefaultHidesNothing(t *testing.T) {
	root := hiddenWorkspace(t)
	registry := hiddenRegistry(t, root)
	for _, path := range []string{".env", ".git/config", "keys/id_rsa", "secrets/token"} {
		result := invoke(t, registry, "read_file", `{"path":`+quoteJSON(path)+`}`)
		if result.IsError {
			t.Errorf("default config hid %q: %q", path, result.Content)
		}
	}
}

// TestHiddenPatternsExpansion pins the widening rules that make rig's
// single-path matcher behave like "this path is invisible, subtree included".
func TestHiddenPatternsExpansion(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"blank entries dropped", []string{"", "  "}, nil},
		{"file pattern", []string{"**/.env"}, []string{"**/.env", "**/.env/**"}},
		{"basename pattern", []string{"id_rsa"}, []string{"id_rsa", "**/id_rsa/**"}},
		{"dir suffix", []string{"secrets/**"}, []string{"secrets", "secrets/**"}},
		{"trailing slash", []string{"secrets/"}, []string{"secrets", "secrets/**"}},
		{"dedup", []string{"**/.env", "**/.env/"}, []string{"**/.env", "**/.env/**"}},
		{"backslashes", []string{`secrets\**`}, []string{"secrets", "secrets/**"}},
	}
	for _, testCase := range cases {
		got := hiddenPatterns(testCase.in)
		if len(got) != len(testCase.want) {
			t.Fatalf("%s: hiddenPatterns(%v) = %v, want %v", testCase.name, testCase.in, got, testCase.want)
		}
		for index, want := range testCase.want {
			if got[index] != want {
				t.Fatalf("%s: hiddenPatterns(%v)[%d] = %q, want %q (all: %v)",
					testCase.name, testCase.in, index, got[index], want, got)
			}
		}
	}
}
