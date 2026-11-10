package rightpanel

// Internal regression tests for the right sidebar's path-keyed caches: the
// workspace-switch reset that guards against stale editors/previews, the diff
// and preview caches dropped by close/refresh, the stale in-flight changes read
// rejected after a refresh, and the image-preview read bounds.

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- stubs ------------------------------------------------------------------

// stubWorkspace is a minimal contracts.WorkspaceService for the internal tests:
// only Current/ReadFile are meaningful.
type stubWorkspace struct {
	current contracts.Workspace
	has     bool
	err     error
	read    []byte
	readErr error
}

func (s *stubWorkspace) List(context.Context) ([]contracts.Workspace, error) { return nil, nil }

func (s *stubWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return s.current, s.has, s.err
}

func (s *stubWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (s *stubWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (s *stubWorkspace) Delete(context.Context, string) error { return nil }

func (s *stubWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (s *stubWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return append([]byte(nil), s.read...), s.readErr
}

func (s *stubWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// blockingDiffer is a contracts.WorkspaceDiffer whose read blocks until the
// test releases it, so a refresh can land while the read is in flight.
type blockingDiffer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	changes []contracts.FileChange
	err     error
}

func (d *blockingDiffer) Changes(context.Context) ([]contracts.FileChange, error) {
	d.once.Do(func() { close(d.started) })
	<-d.release
	return append([]contracts.FileChange(nil), d.changes...), d.err
}

// stubRuntime records posted callbacks instead of running them on a frame loop.
type stubRuntime struct {
	contracts.UIRuntime
	posts chan func()
}

func (r *stubRuntime) Post(fn func()) { r.posts <- fn }

func (r *stubRuntime) Invalidate() {}

// waitPosted runs (or waits for) one posted callback.
func waitPosted(t *testing.T, runtime *stubRuntime) {
	t.Helper()
	select {
	case fn := <-runtime.posts:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("the background read never posted its result")
	}
}

// --- diff/preview cache cleanup --------------------------------------------

func TestCloseTabDropsPathCaches(t *testing.T) {
	st := &state{
		activeWorkspace: "ws-a",
		fileTabs:        []fileTab{{path: "main.go"}},
		tab:             pageFixedCount,
		previews:        map[string]*filePreview{"main.go": {}},
		editors:         map[editorKey]*editor.State{{workspace: "ws-a", path: "main.go"}: {}},
		changesOpen:     map[string]bool{"main.go": true},
		diffs: map[string]*diffView{
			"main.go\x00M":  {lines: 1},
			"other.go\x00M": {},
		},
	}
	st.closeTab(pageFixedCount)

	if _, ok := st.previews["main.go"]; ok {
		t.Fatal("closing the tab kept its cached preview")
	}
	if len(st.editors) != 0 {
		t.Fatalf("closing the tab kept its cached editor states: %v", st.editors)
	}
	if len(st.changesOpen) != 0 {
		t.Fatalf("closing the tab kept its diff fold state: %v", st.changesOpen)
	}
	if _, ok := st.diffs["main.go\x00M"]; ok {
		t.Fatal("closing the tab kept its cached diff viewer (the audit's state leak)")
	}
	if _, ok := st.diffs["other.go\x00M"]; !ok {
		t.Fatal("closing one tab dropped another file's diff viewer")
	}
}

func TestRefreshDropsChangesCaches(t *testing.T) {
	st := &state{
		changes:         []contracts.FileChange{{Path: "main.go", Status: "M"}},
		changesLoaded:   true,
		changesLoading:  true,
		changesOpen:     map[string]bool{"main.go": true},
		diffs:           map[string]*diffView{"main.go\x00M": {}},
		previews:        map[string]*filePreview{},
		editors:         map[editorKey]*editor.State{},
		filesDirs:       map[string]*dirListing{},
		filesOpen:       map[string]*bool{},
		filesGeneration: 3,
	}
	generation := st.changesGeneration
	st.refreshFiles()

	if st.changes != nil || st.changesLoaded || st.changesLoading {
		t.Fatalf("refresh kept the changes snapshot: changes=%v loaded=%v loading=%v",
			st.changes, st.changesLoaded, st.changesLoading)
	}
	if len(st.diffs) != 0 || len(st.changesOpen) != 0 {
		t.Fatalf("refresh kept diff caches: diffs=%v open=%v", st.diffs, st.changesOpen)
	}
	if st.changesGeneration == generation {
		t.Fatal("refresh did not bump the changes generation")
	}
	if st.filesGeneration == 3 {
		t.Fatal("refresh did not bump the files generation")
	}
}

// TestRefreshRejectsInFlightChangesRead: a workspace-diff read that started
// before the user pressed Refresh must not land afterwards and restore the
// pre-refresh snapshot.
func TestRefreshRejectsInFlightChangesRead(t *testing.T) {
	workspace := &stubWorkspace{
		current: contracts.Workspace{ID: "ws", Name: "proj", Root: "/work/proj"},
		has:     true,
	}
	differ := &blockingDiffer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		changes: []contracts.FileChange{{Path: "stale.go", Status: "M"}},
	}
	runtime := &stubRuntime{posts: make(chan func(), 1)}
	st := &state{changesOpen: map[string]bool{}, diffs: map[string]*diffView{}}

	if loading := st.ensureChangesLoaded(workspace, differ, runtime); !loading {
		t.Fatal("the first changes read did not start")
	}
	<-differ.started
	// The Files page's refresh happens while the git read is still running.
	st.refreshFiles()
	close(differ.release)
	waitPosted(t, runtime)

	if st.changesLoaded || st.changes != nil {
		t.Fatalf("a pre-refresh changes read landed after the refresh: %+v", st.changes)
	}
	if st.changesLoading {
		t.Fatal("the changes loading flag is stuck after a rejected stale read")
	}

	// A fresh read after the refresh is accepted.
	differ2 := &blockingDiffer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		changes: []contracts.FileChange{{Path: "fresh.go", Status: "A"}},
	}
	close(differ2.release)
	if loading := st.ensureChangesLoaded(workspace, differ2, runtime); !loading {
		t.Fatal("the post-refresh changes read did not start")
	}
	waitPosted(t, runtime)
	if !st.changesLoaded || len(st.changes) != 1 || st.changes[0].Path != "fresh.go" {
		t.Fatalf("fresh changes snapshot = %+v (loaded=%v), want fresh.go", st.changes, st.changesLoaded)
	}
}

func TestWorkspaceIdentityChangeDropsPathCaches(t *testing.T) {
	st := &state{
		activeWorkspace: "ws-a\x00/work/alpha",
		fileTabs:        []fileTab{{path: "note.txt"}},
		tab:             pageFixedCount,
		previews:        map[string]*filePreview{"note.txt": {workspace: "ws-a\x00/work/alpha"}},
		editors:         map[editorKey]*editor.State{{workspace: "ws-a\x00/work/alpha", path: "note.txt"}: {}},
		changesOpen:     map[string]bool{"note.txt": true},
		diffs:           map[string]*diffView{"note.txt\x00M": {}},
		filesDirs:       map[string]*dirListing{},
		filesOpen:       map[string]*bool{},
	}
	generation := st.changesGeneration
	workspace := &stubWorkspace{current: contracts.Workspace{ID: "ws-b", Root: "/work/beta"}, has: true}
	st.syncWorkspaceIdentity(workspace)

	wantKey := "ws-b\x00/work/beta"
	if st.activeWorkspace != wantKey {
		t.Fatalf("activeWorkspace = %q, want %q", st.activeWorkspace, wantKey)
	}
	if len(st.previews) != 0 || len(st.editors) != 0 || len(st.changesOpen) != 0 || len(st.diffs) != 0 {
		t.Fatalf("path caches survived the workspace switch: previews=%d editors=%d open=%d diffs=%d",
			len(st.previews), len(st.editors), len(st.changesOpen), len(st.diffs))
	}
	if st.changesGeneration == generation {
		t.Fatal("the workspace switch did not bump the changes generation")
	}
	if len(st.fileTabs) != 1 || st.fileTabs[0].path != "note.txt" {
		t.Fatalf("the opened file tabs were dropped on the workspace switch: %v", st.fileTabs)
	}

	// Re-syncing the same workspace is a no-op (no cache flushes per frame).
	st.previews["kept.txt"] = &filePreview{}
	st.syncWorkspaceIdentity(workspace)
	if _, ok := st.previews["kept.txt"]; !ok {
		t.Fatal("an unchanged workspace identity flushed the caches again")
	}
}

// --- image preview bounds ---------------------------------------------------

// testPNG encodes a deterministic noisy image, so its PNG bytes are large.
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	source := rand.New(rand.NewSource(1))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(source.Intn(256)),
				G: uint8(source.Intn(256)),
				B: uint8(source.Intn(256)),
				A: 255,
			})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buffer.Bytes()
}

// pngHeaderWithDimensions builds a PNG whose IHDR declares width×height but
// carries no image data: image.DecodeConfig reads the dimensions without
// decoding pixels, which is exactly the pre-decode bound the preview applies.
func pngHeaderWithDimensions(width, height uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	ihdr[8] = 8  // bit depth
	ihdr[9] = 6  // color type: RGBA
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace
	var buffer bytes.Buffer
	buffer.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	chunk := make([]byte, 0, 12+len(ihdr))
	chunk = binary.BigEndian.AppendUint32(chunk, uint32(len(ihdr)))
	chunk = append(chunk, "IHDR"...)
	chunk = append(chunk, ihdr...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	buffer.Write(chunk)
	return buffer.Bytes()
}

func TestLoadFilePreviewImageBounds(t *testing.T) {
	// A normal screenshot-sized image over the text preview cap still previews:
	// the image read uses its own, larger bound and decodes from the full bytes.
	large := testPNG(t, 512, 512)
	if len(large) <= maxPreviewBytes {
		t.Fatalf("test fixture too small to exercise the cap: %d bytes", len(large))
	}
	workspace := &stubWorkspace{read: large}
	result := loadFilePreview(workspace, "screenshot.png")
	if result.err != "" || result.bitmap == nil {
		t.Fatalf("a %d-byte PNG did not preview: err=%q bitmap=%v", len(large), result.err, result.bitmap)
	}
	if result.truncated {
		t.Fatal("an image preview was reported as truncated text")
	}

	// A corrupt image reports honestly instead of falling through to the
	// "preview is not supported" fallback.
	result = loadFilePreview(&stubWorkspace{read: []byte("not an image")}, "broken.png")
	if result.err == "" || result.bitmap != nil {
		t.Fatalf("corrupt image result = %+v, want an error", result)
	}

	// A tiny file whose header claims enormous dimensions is rejected before
	// the decoder allocates the bitmap.
	huge := pngHeaderWithDimensions(20000, 20000)
	result = loadFilePreview(&stubWorkspace{read: huge}, "bomb.png")
	if !strings.Contains(result.err, "image is too large") {
		t.Fatalf("oversized image result err = %q, want an image-size error", result.err)
	}
	if result.bitmap != nil {
		t.Fatal("an over-dimension image was decoded")
	}

	// A file over the image byte bound is rejected with a size error.
	oversized := make([]byte, maxImageBytes+1)
	copy(oversized, testPNG(t, 8, 8))
	result = loadFilePreview(&stubWorkspace{read: oversized}, "huge.png")
	if !strings.Contains(result.err, "image is too large") {
		t.Fatalf("oversized bytes result err = %q, want an image-size error", result.err)
	}
}

// --- path containment helpers ----------------------------------------------

func TestResolveWorkspacePathContainment(t *testing.T) {
	cases := []struct {
		name      string
		workspace *stubWorkspace
		path      string
		relative  string
		wantErr   bool
	}{
		{name: "relative", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "docs/a.md", relative: "docs/a.md"},
		{name: "relative cleaned", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "docs/../a.md", relative: "a.md"},
		{name: "absolute inside", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "/work/proj/docs/a.md", relative: "docs/a.md"},
		{name: "relative escape", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "../a.md", wantErr: true},
		{name: "nested relative escape", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "docs/../../a.md", wantErr: true},
		{name: "absolute outside", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "/etc/passwd", wantErr: true},
		{name: "absolute sibling", workspace: &stubWorkspace{current: contracts.Workspace{ID: "ws", Root: "/work/proj"}, has: true},
			path: "/work/proj-sibling/a.md", wantErr: true},
		{name: "no workspace relative", workspace: &stubWorkspace{},
			path: "notes/a.md", relative: "notes/a.md"},
		{name: "no workspace escape", workspace: &stubWorkspace{}, path: "../a.md", wantErr: true},
		{name: "no workspace absolute", workspace: &stubWorkspace{}, path: "/etc/passwd", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			st := &state{}
			relative, _, err := st.resolveWorkspacePath(contracts.WorkspaceService(testCase.workspace), testCase.path)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("resolveWorkspacePath(%q) = %q, nil; want an error", testCase.path, relative)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWorkspacePath(%q): %v", testCase.path, err)
			}
			if relative != testCase.relative {
				t.Fatalf("resolveWorkspacePath(%q) = %q, want %q", testCase.path, relative, testCase.relative)
			}
		})
	}
}
