package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

func testTools(t *testing.T, cfg Config) map[string]runtime.Tool {
	t.Helper()
	if cfg.WorkspaceRoot == "" {
		cfg.WorkspaceRoot = t.TempDir()
	}
	built, err := Builtins(cfg)
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	out := make(map[string]runtime.Tool, len(built))
	for _, tool := range built {
		if tool.Name() == "" {
			t.Fatalf("tool with empty name: %#v", tool)
		}
		if _, ok := out[tool.Name()]; ok {
			t.Fatalf("duplicate tool name %q", tool.Name())
		}
		if tool.Description() == "" {
			t.Fatalf("tool %q has empty description", tool.Name())
		}
		out[tool.Name()] = tool
	}
	return out
}

func mustArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func invokeTool(t *testing.T, tool runtime.Tool, v any) (string, error) {
	t.Helper()
	return tool.Invoke(context.Background(), mustArgs(t, v))
}

func mustInvoke(t *testing.T, tool runtime.Tool, v any) string {
	t.Helper()
	out, err := invokeTool(t, tool, v)
	if err != nil {
		t.Fatalf("%s: %v", tool.Name(), err)
	}
	return out
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestBuiltinsAndRegister(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	want := []string{"edit_file", "glob", "grep", "read_file", "web_fetch", "write_file"}
	if len(ts) != len(want) {
		t.Fatalf("Builtins returned %d tools, want %d: %v", len(ts), len(want), namesOf(ts))
	}
	for _, name := range want {
		tool, ok := ts[name]
		if !ok {
			t.Fatalf("missing tool %q in %v", name, namesOf(ts))
		}
		if _, ok := ts["bash"]; ok {
			t.Fatal("bash must be disabled when AllowShell is false")
		}
		assertSchema(t, tool)
	}

	shell := testTools(t, Config{WorkspaceRoot: root, AllowShell: true, AllowBash: true})
	if _, ok := shell["bash"]; !ok {
		t.Fatalf("bash missing when AllowShell and AllowBash are true: %v", namesOf(shell))
	}
	assertSchema(t, shell["bash"])
	if len(shell) != len(want)+1 {
		t.Fatalf("AllowShell tools = %v", namesOf(shell))
	}

	reg := runtime.NewRegistry()
	cfg := Config{WorkspaceRoot: root}
	if err := Register(reg, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	listed := reg.List()
	if len(listed) != len(want) {
		t.Fatalf("registry tools = %d, want %d", len(listed), len(want))
	}
	for i, name := range want {
		if listed[i].Name() != name {
			t.Fatalf("registry[%d] = %q, want %q", i, listed[i].Name(), name)
		}
	}
	if _, ok := reg.Get("bash"); ok {
		t.Fatal("bash registered without AllowShell")
	}
	read, ok := reg.Get("read_file")
	if !ok {
		t.Fatal("read_file not in registry")
	}
	writeTestFile(t, filepath.Join(root, "registered.txt"), "registered")
	if got := mustInvoke(t, read, map[string]string{"path": "registered.txt"}); got != "registered" {
		t.Fatalf("read via registry = %q", got)
	}
	if err := Register(reg, cfg); err == nil {
		t.Fatal("second Register should fail on duplicate tools")
	}
	if err := Register(nil, cfg); err == nil {
		t.Fatal("Register(nil) should fail")
	}

	if _, err := Builtins(Config{}); err == nil {
		t.Fatal("Builtins with empty WorkspaceRoot should fail")
	}
	if _, err := Builtins(Config{WorkspaceRoot: filepath.Join(root, "does-not-exist")}); err == nil {
		t.Fatal("Builtins with missing workspace root should fail")
	}
	notDir := filepath.Join(root, "file-root")
	writeTestFile(t, notDir, "x")
	if _, err := Builtins(Config{WorkspaceRoot: notDir}); err == nil {
		t.Fatal("Builtins with a file as workspace root should fail")
	}
}

func namesOf(ts map[string]runtime.Tool) []string {
	out := make([]string, 0, len(ts))
	for name := range ts {
		out = append(out, name)
	}
	return out
}

// assertSchema checks the JSON schema embedded in a tool description parses.
func assertSchema(t *testing.T, tool runtime.Tool) {
	t.Helper()
	const marker = "Args JSON schema:"
	desc := tool.Description()
	i := strings.Index(desc, marker)
	if i < 0 {
		t.Fatalf("%s description has no JSON schema marker", tool.Name())
	}
	raw := strings.TrimSpace(desc[i+len(marker):])
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		t.Fatalf("%s schema is not valid JSON: %v\n%s", tool.Name(), err, raw)
	}
	if schema["type"] != "object" {
		t.Fatalf("%s schema type = %v", tool.Name(), schema["type"])
	}
	if _, ok := schema["properties"].(map[string]any); !ok {
		t.Fatalf("%s schema has no properties object", tool.Name())
	}
}

func TestReadWriteEditRoundTrip(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	write := ts["write_file"]
	read := ts["read_file"]
	edit := ts["edit_file"]

	const content = "hello\nworld\n"
	out := mustInvoke(t, write, map[string]string{
		"path":    "nested/dir/note.txt",
		"content": content,
	})
	if !strings.Contains(out, "nested/dir/note.txt") {
		t.Fatalf("write result = %q", out)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "nested", "dir", "note.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(onDisk) != content {
		t.Fatalf("on-disk content = %q", onDisk)
	}
	if got := mustInvoke(t, read, map[string]string{"path": "nested/dir/note.txt"}); got != content {
		t.Fatalf("read_file = %q", got)
	}

	out = mustInvoke(t, edit, map[string]string{
		"path": "nested/dir/note.txt",
		"old":  "world",
		"new":  "gopher",
	})
	if !strings.Contains(out, "edited") {
		t.Fatalf("edit result = %q", out)
	}
	if got := mustInvoke(t, read, map[string]string{"path": "nested/dir/note.txt"}); got != "hello\ngopher\n" {
		t.Fatalf("after edit = %q", got)
	}

	if _, err := invokeTool(t, edit, map[string]string{"path": "nested/dir/note.txt", "old": "missing", "new": "x"}); err == nil {
		t.Fatal("edit with absent old text should fail")
	}
	mustInvoke(t, write, map[string]string{"path": "multi.txt", "content": "cat cat"})
	if _, err := invokeTool(t, edit, map[string]string{"path": "multi.txt", "old": "cat", "new": "dog"}); err == nil {
		t.Fatal("edit with multiple occurrences should fail")
	}
	if _, err := invokeTool(t, edit, map[string]string{"path": "multi.txt", "old": "", "new": "dog"}); err == nil {
		t.Fatal("edit with empty old text should fail")
	}
	if got := mustInvoke(t, read, map[string]string{"path": "multi.txt"}); got != "cat cat" {
		t.Fatalf("failed edits changed file: %q", got)
	}

	if _, err := invokeTool(t, read, map[string]string{"path": "nested"}); err == nil {
		t.Fatal("reading a directory should fail")
	}
	if _, err := invokeTool(t, write, map[string]string{"path": "nested", "content": "x"}); err == nil {
		t.Fatal("writing a directory should fail")
	}
	if _, err := invokeTool(t, read, map[string]any{"path": "nested/dir/note.txt", "extra": 1}); err != nil {
		t.Fatalf("unknown argument should be ignored: %v", err)
	}

	writeTestFile(t, filepath.Join(root, "empty.txt"), "")
	if got := mustInvoke(t, read, map[string]string{"path": "empty.txt"}); got != "" {
		t.Fatalf("empty file read = %q", got)
	}
	mustInvoke(t, write, map[string]string{"path": "empty-created.txt", "content": ""})
	if info, err := os.Stat(filepath.Join(root, "empty-created.txt")); err != nil || info.Size() != 0 {
		t.Fatalf("empty write: info=%v err=%v", info, err)
	}

	writeTestFile(t, filepath.Join(root, "binary.bin"), "abc\x00def")
	if _, err := invokeTool(t, read, map[string]string{"path": "binary.bin"}); err == nil {
		t.Fatal("reading a binary file should fail")
	}
	writeTestFile(t, filepath.Join(root, "invalid-utf8.txt"), string([]byte{0xff, 0xfe, 0xfd}))
	if _, err := invokeTool(t, read, map[string]string{"path": "invalid-utf8.txt"}); err == nil {
		t.Fatal("reading invalid UTF-8 should fail")
	}

	big := filepath.Join(root, "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), MaxReadFileBytes+1), 0o644); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	if _, err := invokeTool(t, read, map[string]string{"path": "big.txt"}); err == nil {
		t.Fatal("reading a file over 1 MiB should fail")
	}

	for _, tool := range []runtime.Tool{read, write, edit} {
		if _, err := tool.Invoke(context.Background(), nil); err == nil {
			t.Fatalf("%s with empty args should fail", tool.Name())
		}
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"path":`)); err == nil {
			t.Fatalf("%s with malformed JSON should fail", tool.Name())
		}
		if _, err := tool.Invoke(context.Background(), json.RawMessage("null")); err == nil {
			t.Fatalf("%s with null arguments should fail", tool.Name())
		}
	}
}

func TestPathTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	writeTestFile(t, secret, "top secret")
	ts := testTools(t, Config{WorkspaceRoot: root})

	badPaths := []string{
		"../secret.txt",
		"sub/../../secret.txt",
		"a/../b.txt",
		secret,
		"/etc/passwd",
	}
	for _, bad := range badPaths {
		if _, err := invokeTool(t, ts["read_file"], map[string]string{"path": bad}); err == nil {
			t.Fatalf("read_file accepted traversal path %q", bad)
		}
		if _, err := invokeTool(t, ts["write_file"], map[string]string{"path": bad, "content": "pwned"}); err == nil {
			t.Fatalf("write_file accepted traversal path %q", bad)
		}
		if _, err := invokeTool(t, ts["edit_file"], map[string]string{"path": bad, "old": "top", "new": "pwned"}); err == nil {
			t.Fatalf("edit_file accepted traversal path %q", bad)
		}
		if _, err := invokeTool(t, ts["grep"], map[string]string{"pattern": "secret", "path": bad}); err == nil {
			t.Fatalf("grep accepted traversal path %q", bad)
		}
	}
	for _, bad := range []string{"../*.txt", "/etc/*", "sub/../../*"} {
		if _, err := invokeTool(t, ts["glob"], map[string]string{"pattern": bad}); err == nil {
			t.Fatalf("glob accepted traversal pattern %q", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal write created b.txt: %v", err)
	}

	// A directory symlink that points out of the root must be unusable.
	dirLink := filepath.Join(root, "escape")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := invokeTool(t, ts["read_file"], map[string]string{"path": "escape/secret.txt"}); err == nil {
		t.Fatal("read through escaping directory symlink should fail")
	}
	if _, err := invokeTool(t, ts["write_file"], map[string]string{"path": "escape/pwn.txt", "content": "pwned"}); err == nil {
		t.Fatal("write through escaping directory symlink should fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwn.txt")); !os.IsNotExist(err) {
		t.Fatalf("write through symlink created file outside root: %v", err)
	}
	if _, err := invokeTool(t, ts["edit_file"], map[string]string{"path": "escape/secret.txt", "old": "top", "new": "pwned"}); err == nil {
		t.Fatal("edit through escaping directory symlink should fail")
	}
	if got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "escape/*.txt"}); strings.Contains(got, "secret") {
		t.Fatalf("glob leaked a file outside root: %q", got)
	}
	if _, err := invokeTool(t, ts["grep"], map[string]string{"pattern": "secret", "path": "escape"}); err == nil {
		t.Fatal("grep through escaping directory symlink should fail")
	}

	// A file symlink that points out of the root is equally unusable.
	fileLink := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, fileLink); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	if _, err := invokeTool(t, ts["read_file"], map[string]string{"path": "link.txt"}); err == nil {
		t.Fatal("read through escaping file symlink should fail")
	}
	if _, err := invokeTool(t, ts["write_file"], map[string]string{"path": "link.txt", "content": "pwned"}); err == nil {
		t.Fatal("write through escaping file symlink should fail")
	}
	if data, err := os.ReadFile(secret); err != nil || string(data) != "top secret" {
		t.Fatalf("secret file was modified: %q %v", data, err)
	}

	// A dangling symlink could otherwise be used to create a file elsewhere.
	dangling := filepath.Join(root, "dangling.txt")
	if err := os.Symlink(filepath.Join(outside, "created.txt"), dangling); err != nil {
		t.Fatalf("dangling symlink: %v", err)
	}
	if _, err := invokeTool(t, ts["write_file"], map[string]string{"path": "dangling.txt", "content": "pwned"}); err == nil {
		t.Fatal("write through dangling symlink should fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink created a file outside root: %v", err)
	}

	// A symlink that stays inside the root is allowed.
	writeTestFile(t, filepath.Join(root, "real.txt"), "inside")
	if err := os.Symlink("real.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Fatalf("inside symlink: %v", err)
	}
	if got := mustInvoke(t, ts["read_file"], map[string]string{"path": "alias.txt"}); got != "inside" {
		t.Fatalf("read through inside symlink = %q", got)
	}

	if _, err := invokeTool(t, ts["read_file"], map[string]string{"path": "."}); err == nil {
		t.Fatal("reading the root directory should fail")
	}
}
