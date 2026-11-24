package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The right inspector (R8–R10) and the composer's `@` menu read these endpoints.
// They were never served: GET /api/files/tree answered 404 on a live build, so the
// tree rendered empty and `@` offered nothing while the acceptance table said done.
//
// These tests pin the transport: the workspace service owns the filesystem rules,
// so what is asserted here is shape, bounds, honesty about binary files, the escape
// check, and that none of it is reachable without the token.

// diskWorkspace is a WorkspaceService over a real temp directory.
type diskWorkspace struct {
	root    string
	changes []contracts.FileChange
}

func (d *diskWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return []contracts.Workspace{{ID: "w1", Name: "w", Root: d.root}}, nil
}

func (d *diskWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return contracts.Workspace{ID: "w1", Name: "w", Root: d.root}, true, nil
}

func (d *diskWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, fmt.Errorf("not used")
}

func (d *diskWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, fmt.Errorf("not used")
}

func (d *diskWorkspace) Delete(context.Context, string) error { return nil }

func (d *diskWorkspace) Files(_ context.Context, dir string) ([]contracts.FileEntry, error) {
	absolute := d.root
	if dir != "" {
		absolute = filepath.Join(d.root, filepath.FromSlash(dir))
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := entry.Name()
		if dir != "" {
			path = dir + "/" + entry.Name()
		}
		out = append(out, contracts.FileEntry{
			Name: entry.Name(), Path: path, Dir: entry.IsDir(), Size: info.Size(), ModTime: info.ModTime(),
		})
	}
	return out, nil
}

func (d *diskWorkspace) ReadFile(_ context.Context, path string, limit int) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(d.root, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(data) > limit {
		data = data[:limit]
	}
	return data, nil
}

func (d *diskWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (d *diskWorkspace) Changes(context.Context) ([]contracts.FileChange, error) {
	return d.changes, nil
}

func newFilesGateway(t *testing.T) (*runningGateway, *diskWorkspace) {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(relative, body string) {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	mustWrite("README.md", "# demo\n")
	mustWrite("src/main.go", "package main\n")
	mustWrite("src/deep/nested.txt", "nested\n")
	mustWrite("logo.bin", "\x00\x01\x02binary\x00")
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	workspace := &diskWorkspace{root: root, changes: []contracts.FileChange{
		{Path: "README.md", Status: "M", Diff: "--- a/README.md\n+++ b/README.md\n@@\n-# demo\n+# demo\n+added line\n"},
	}}

	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	testRoot := testRoot(t, chat)
	if _, err := testRoot.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	gateway, err := New(testRoot, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(testRoot); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway), workspace
}

func filesGet(t *testing.T, gw *runningGateway, target string, authenticated bool) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+gw.address+target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+gw.info.Token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return response.StatusCode, string(body)
}

func TestFilesTreeListsTheWorkspaceAndMarksChanges(t *testing.T) {
	gw, _ := newFilesGateway(t)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/files/tree", true)
	if status != http.StatusOK {
		t.Fatalf("GET /api/files/tree = %d %q", status, body)
	}
	var tree fileTreeDTO
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		t.Fatalf("decode tree: %v (%q)", err, body)
	}
	if tree.Root == "" {
		t.Fatal("the tree must name its root")
	}
	paths := make([]string, 0, len(tree.Nodes))
	for _, node := range tree.Nodes {
		paths = append(paths, node.Path)
	}
	for _, want := range []string{"README.md", "src", "src/main.go", "src/deep/nested.txt", "logo.bin"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("tree misses %q: %v", want, paths)
		}
	}
	// The node budget must not be spent on directories no preview needs.
	for _, path := range paths {
		if strings.HasPrefix(path, "node_modules") {
			t.Fatalf("tree walked node_modules: %v", paths)
		}
	}
	// The dirty file carries the inspector's change marker; a clean one does not.
	for _, node := range tree.Nodes {
		switch node.Path {
		case "README.md":
			if !node.Changed {
				t.Fatal("README.md has uncommitted changes and must be marked")
			}
		case "src/main.go":
			if node.Changed {
				t.Fatal("src/main.go is clean and must not be marked")
			}
		}
	}
	if !sort.SliceIsSorted(paths, func(i, j int) bool { return paths[i] < paths[j] }) {
		t.Fatalf("tree nodes are not stable-sorted: %v", paths)
	}
}

func TestFilesContentServesTextAndRefusesTheRest(t *testing.T) {
	gw, _ := newFilesGateway(t)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/files/content?path=src/main.go", true)
	if status != http.StatusOK {
		t.Fatalf("GET content = %d %q", status, body)
	}
	var content fileContentDTO
	if err := json.Unmarshal([]byte(body), &content); err != nil {
		t.Fatalf("decode content: %v (%q)", err, body)
	}
	if content.Text != "package main\n" || content.Truncated {
		t.Fatalf("content = %+v", content)
	}

	// A binary file is an honest 415, not a screen of replacement characters.
	if status, body := filesGet(t, gw, "/api/files/content?path=logo.bin", true); status != http.StatusUnsupportedMediaType {
		t.Fatalf("binary content = %d %q, want 415", status, body)
	}
	// A path that leaves the workspace never reaches the service.
	for _, escape := range []string{"../etc/passwd", "/etc/passwd", "src/../../etc/passwd"} {
		if status, _ := filesGet(t, gw, "/api/files/content?path="+escape, true); status != http.StatusBadRequest {
			t.Fatalf("escape %q = %d, want 400", escape, status)
		}
	}
	// A missing file is a 404, and a missing parameter a 400.
	if status, _ := filesGet(t, gw, "/api/files/content?path=nope.txt", true); status != http.StatusNotFound {
		t.Fatalf("missing file = %d, want 404", status)
	}
	if status, _ := filesGet(t, gw, "/api/files/content", true); status != http.StatusBadRequest {
		t.Fatalf("missing path param = %d, want 400", status)
	}
}

func TestFilesDiffReportsUnifiedAndCounts(t *testing.T) {
	gw, _ := newFilesGateway(t)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/files/diff?path=README.md", true)
	if status != http.StatusOK {
		t.Fatalf("GET diff = %d %q", status, body)
	}
	var diff fileDiffDTO
	if err := json.Unmarshal([]byte(body), &diff); err != nil {
		t.Fatalf("decode diff: %v (%q)", err, body)
	}
	// The sample replaces one line and adds another: two `+` content lines, one
	// `-` line. The `+++`/`---` file headers must not be counted as content.
	if diff.Additions != 2 || diff.Deletions != 1 {
		t.Fatalf("counts = +%d -%d, want +2 -1 (%q)", diff.Additions, diff.Deletions, diff.Unified)
	}
	if !strings.Contains(diff.Unified, "+added line") {
		t.Fatalf("unified diff lost its added line: %q", diff.Unified)
	}
	// The +++/--- headers are not counted as content.
	if strings.Count(diff.Unified, "\n+++") != 1 {
		t.Fatalf("unexpected diff shape: %q", diff.Unified)
	}
	// A file without changes has no diff to show.
	if status, _ := filesGet(t, gw, "/api/files/diff?path=src/main.go", true); status != http.StatusNotFound {
		t.Fatalf("clean file diff = %d, want 404", status)
	}
}

func TestFilesEndpointsNeedTheToken(t *testing.T) {
	gw, _ := newFilesGateway(t)
	defer gw.stop(t)

	for _, target := range []string{"/api/files/tree", "/api/files/content?path=README.md", "/api/files/diff?path=README.md"} {
		if status, body := filesGet(t, gw, target, false); status != http.StatusUnauthorized {
			t.Fatalf("%s without a token = %d %q, want 401", target, status, body)
		}
	}
}
