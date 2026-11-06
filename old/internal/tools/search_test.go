package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

func TestGlob(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	writeTestFile(t, filepath.Join(root, "a.txt"), "a")
	writeTestFile(t, filepath.Join(root, "b.txt"), "b")
	writeTestFile(t, filepath.Join(root, "sub", "c.txt"), "c")
	writeTestFile(t, filepath.Join(root, "sub", "d.log"), "d")
	writeTestFile(t, filepath.Join(root, "dir", "keep"), "keep")

	if got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "*.txt"}); got != "a.txt\nb.txt" {
		t.Fatalf("glob *.txt = %q", got)
	}
	if got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "sub/*.txt"}); got != "sub/c.txt" {
		t.Fatalf("glob sub/*.txt = %q", got)
	}
	if got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "dir/*"}); got != "dir/keep" {
		t.Fatalf("glob dir/* = %q", got)
	}
	if got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "*.nope"}); got != "" {
		t.Fatalf("glob no matches = %q, want empty", got)
	}
	if got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "sub"}); got != "sub" {
		t.Fatalf("glob directory = %q", got)
	}
	for _, bad := range []string{"[", "a[", "../*", "/tmp/*"} {
		if _, err := invokeTool(t, ts["glob"], map[string]string{"pattern": bad}); err == nil {
			t.Fatalf("glob accepted invalid pattern %q", bad)
		}
	}
	if _, err := invokeTool(t, ts["glob"], map[string]string{}); err == nil {
		t.Fatal("glob without pattern should fail")
	}

	for i := 0; i < 205; i++ {
		writeTestFile(t, filepath.Join(root, fmt.Sprintf("many-%03d.lim", i)), "x")
	}
	got := mustInvoke(t, ts["glob"], map[string]string{"pattern": "many-*.lim"})
	lines := strings.Split(got, "\n")
	if len(lines) != maxGlobEntries {
		t.Fatalf("glob limit returned %d entries, want %d", len(lines), maxGlobEntries)
	}
	if lines[0] != "many-000.lim" || lines[len(lines)-1] != "many-199.lim" {
		t.Fatalf("glob limit bounds = %q .. %q", lines[0], lines[len(lines)-1])
	}
}

func TestGrep(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	writeTestFile(t, filepath.Join(root, "a.txt"), "alpha\nbeta\nalpha\n")
	writeTestFile(t, filepath.Join(root, "sub", "c.txt"), "gamma alpha\n")
	writeTestFile(t, filepath.Join(root, "bin.dat"), "alpha\x00binary")
	writeTestFile(t, filepath.Join(root, "crlf.txt"), "one\r\ntwo\r\n")

	want := "a.txt:1: alpha\na.txt:3: alpha\nsub/c.txt:1: gamma alpha"
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "alpha"}); got != want {
		t.Fatalf("grep alpha =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(mustInvoke(t, ts["grep"], map[string]string{"pattern": "alpha"}), "bin.dat") {
		t.Fatal("grep searched a binary file")
	}
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "^alpha$"}); !strings.Contains(got, "a.txt:1") || strings.Contains(got, "gamma") {
		t.Fatalf("grep anchored = %q", got)
	}
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "alpha", "path": "sub"}); got != "sub/c.txt:1: gamma alpha" {
		t.Fatalf("grep sub = %q", got)
	}
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "alpha", "path": "a.txt"}); got != "a.txt:1: alpha\na.txt:3: alpha" {
		t.Fatalf("grep file = %q", got)
	}
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "alpha", "path": "."}); got != want {
		t.Fatalf("grep root = %q", got)
	}
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "one$"}); got != "crlf.txt:1: one" {
		t.Fatalf("grep CRLF = %q", got)
	}
	if got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "not-there"}); got != "" {
		t.Fatalf("grep no matches = %q, want empty", got)
	}
	if _, err := invokeTool(t, ts["grep"], map[string]string{"pattern": "["}); err == nil {
		t.Fatal("grep accepted an invalid regexp")
	}
	if _, err := invokeTool(t, ts["grep"], map[string]string{"pattern": "alpha", "path": "missing"}); err == nil {
		t.Fatal("grep accepted a missing path")
	}
	if _, err := invokeTool(t, ts["grep"], map[string]string{"pattern": "alpha", "path": "bin.dat"}); err != nil {
		t.Fatalf("grep on an explicit binary file should return no matches, got %v", err)
	}
}

func TestGrepMatchLimit(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("needle\n")
	}
	writeTestFile(t, filepath.Join(root, "many.txt"), b.String())

	got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "needle"})
	lines := strings.Split(got, "\n")
	if len(lines) != maxGrepMatches {
		t.Fatalf("grep returned %d matches, want %d", len(lines), maxGrepMatches)
	}
	if lines[0] != "many.txt:1: needle" || lines[len(lines)-1] != fmt.Sprintf("many.txt:%d: needle", maxGrepMatches) {
		t.Fatalf("grep limit bounds = %q .. %q", lines[0], lines[len(lines)-1])
	}
}

var _ runtime.Tool = (*globTool)(nil)
var _ runtime.Tool = (*grepTool)(nil)

func TestGrepFollowsInsideSymlinks(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	writeTestFile(t, filepath.Join(root, "a.txt"), "alpha\n")
	if err := os.Symlink("a.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "alpha outside\n")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
		t.Fatalf("symlink outside: %v", err)
	}

	got := mustInvoke(t, ts["grep"], map[string]string{"pattern": "alpha"})
	if !strings.Contains(got, "a.txt:1: alpha") {
		t.Fatalf("grep missed a regular file: %q", got)
	}
	if !strings.Contains(got, "alias.txt:1: alpha") {
		t.Fatalf("grep did not follow an inside symlink: %q", got)
	}
	if strings.Contains(got, "escape.txt") || strings.Contains(got, "outside") {
		t.Fatalf("grep followed a symlink outside the root: %q", got)
	}
}
