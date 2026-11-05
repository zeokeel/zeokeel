package shell

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// textsOrder returns the order of wanted texts in the last frame, -1 when one
// is missing.
func textsOrder(tt *ui.Tester, wanted ...string) []int {
	texts := tt.Texts()
	out := make([]int, len(wanted))
	for i, want := range wanted {
		out[i] = -1
		for j, text := range texts {
			if text == want {
				out[i] = j
				break
			}
		}
	}
	return out
}

// TestFilesNaturalSort covers the directory ordering: directories first, then
// natural case-insensitive names where digit runs compare by value (a2 before
// a10).
func TestFilesNaturalSort(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	for _, dir := range []string{"dir10", "dir2"} {
		writeTestFile(t, h.dir, dir+"/keep.txt", "x")
	}
	for _, file := range []string{"a10.txt", "a2.txt", "a1.txt", "B.txt", "z.txt"} {
		writeTestFile(t, h.dir, file, "x")
	}
	tt := showFilesTab(t, h)

	order := textsOrder(tt, "dir2/", "dir10/", "a1.txt", "a2.txt", "a10.txt", "B.txt", "z.txt")
	for i, index := range order {
		if index < 0 {
			t.Fatalf("entry %d missing from the listing: %v", i, tt.Texts())
		}
		if i > 0 && index < order[i-1] {
			t.Fatalf("directory order is not natural: %v (indexes %v)", order, order)
		}
	}
}

// TestFilesEntryCapTruncation covers the 2000-entry cap: the listing stops at
// the cap and says how many entries were left out.
func TestFilesEntryCapTruncation(t *testing.T) {
	old := maxFilesEntries
	maxFilesEntries = 5
	t.Cleanup(func() { maxFilesEntries = old })

	h := newLiveApp(t, &shellProvider{})
	for i := 1; i <= 9; i++ {
		writeTestFile(t, h.dir, fmt.Sprintf("cap/f%d.txt", i), "x")
	}
	h.app.filesDir = "cap"
	tt := showFilesTab(t, h)

	if !tt.HasText("Showing the first 5 of 9 entries.") {
		t.Fatalf("truncation notice missing: %v", tt.Texts())
	}
	if !tt.HasText("f5.txt") {
		t.Fatalf("entry at the cap missing: %v", tt.Texts())
	}
	if tt.HasText("f6.txt") {
		t.Fatalf("listing kept entries past the cap: %v", tt.Texts())
	}
}

// TestFilesDirCacheAndReload covers the directory listing cache: a file that
// appears while the cache is warm is not picked up until Reload invalidates it.
func TestFilesDirCacheAndReload(t *testing.T) {
	oldTTL := filesDirCacheTTL
	filesDirCacheTTL = time.Hour
	t.Cleanup(func() { filesDirCacheTTL = oldTTL })

	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "first.txt", "1")
	tt := showFilesTab(t, h)
	if !tt.HasText("first.txt") {
		t.Fatalf("first listing missing first.txt: %v", tt.Texts())
	}

	writeTestFile(t, h.dir, "second.txt", "2")
	tt.Frame()
	if tt.HasText("second.txt") {
		t.Fatalf("warm cache picked up a new file before Reload: %v", tt.Texts())
	}

	if err := tt.Click("Reload"); err != nil {
		t.Fatalf("click Reload: %v", err)
	}
	tt.Frame()
	if !tt.HasText("second.txt") {
		t.Fatalf("Reload did not invalidate the directory cache: %v", tt.Texts())
	}
}

// TestFilesCacheClearedOnSessionSwitch covers the session boundary: switching
// the shown session drops the cached listing (and the preview navigation), so a
// file added meanwhile shows up without a manual Reload.
func TestFilesCacheClearedOnSessionSwitch(t *testing.T) {
	oldTTL := filesDirCacheTTL
	filesDirCacheTTL = time.Hour
	t.Cleanup(func() { filesDirCacheTTL = oldTTL })

	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "first.txt", "1")
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	h.app.openFilePreview("first.txt")
	tt.Frame()
	if h.app.rightPanelUI().dirCachePath == "" {
		t.Fatal("fixture did not cache the listing")
	}

	writeTestFile(t, h.dir, "third.txt", "3")
	sessB, err := h.ctrl.CreateSession(context.Background(), "second")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	snap, err := h.ctrl.OpenSession(context.Background(), sessB)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	h.app.SetSnapshot(snap)
	tt.Frame()

	if h.app.filePath != "" {
		t.Fatalf("session switch kept the preview path %q", h.app.filePath)
	}
	if !tt.HasText("third.txt") {
		t.Fatalf("session switch kept the stale directory cache: %v", tt.Texts())
	}
}

// TestFilesChinesePaths covers CJK paths end to end: directory rows, the
// breadcrumb and the rendered Markdown preview all keep the names intact.
func TestFilesChinesePaths(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "文档/设计/说明.md", "# 中文标题\n\n正文内容\n")
	tt := showFilesTab(t, h)

	if !tt.HasText("文档/") {
		t.Fatalf("Chinese directory row missing: %v", tt.Texts())
	}
	if err := tt.Click("文档/"); err != nil {
		t.Fatalf("enter Chinese directory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("设计/") {
		t.Fatalf("nested Chinese directory missing: %v", tt.Texts())
	}
	if err := tt.Click("设计/"); err != nil {
		t.Fatalf("enter nested Chinese directory: %v", err)
	}
	tt.Frame()
	if !tt.HasText("说明.md") {
		t.Fatalf("Chinese file row missing: %v", tt.Texts())
	}
	if err := tt.Click("说明.md"); err != nil {
		t.Fatalf("preview Chinese file: %v", err)
	}
	tt.Frame()
	if !tt.HasText("中文标题") || !tt.HasText("正文内容") {
		t.Fatalf("Chinese Markdown preview missing: %v", tt.Texts())
	}
}

// TestFilesRootTooltip covers the hover path shared by the Files breadcrumb and
// the Start page's Workspace files entry: both show the absolute workspace
// root, so equal display names stay distinguishable.
func TestFilesRootTooltip(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := showFilesTab(t, h)

	if got, want := h.app.filesRootTooltip(), h.app.filesRoot(); got != want {
		t.Fatalf("filesRootTooltip() = %q, want the absolute root %q", got, want)
	}
	if h.app.filesRootTooltip() == "" {
		t.Fatal("workspace root tooltip is empty")
	}
	// The breadcrumb root still renders, and the Start page entry exists.
	if _, ok := tt.Find("⌂ test"); !ok {
		t.Fatalf("breadcrumb root missing: %v", tt.Texts())
	}
	h.app.rightTab = startTab
	tt.Frame()
	if !tt.HasText("Workspace files") {
		t.Fatalf("Start page workspace entry missing: %v", tt.Texts())
	}
}

// TestFilesPreviewKindsA11yLabels covers the accessible names of the preview
// kinds: the panel labels its scroll body per kind so assistive technology can
// tell a Markdown document from plain text and from an image.
func TestFilesPreviewKindsA11yLabels(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "readme.md", "# 标题\n")
	writePreviewPNG(t, h, "mark.png")
	tt := showFilesTab(t, h)

	h.app.openFilePreview("readme.md")
	tt.Frame()
	if !tt.HasText("Markdown preview") {
		t.Fatalf("Markdown preview label missing: %v", tt.Texts())
	}
	h.app.openFilePreview("mark.png")
	tt.Frame()
	if !tt.HasText("Image preview") {
		t.Fatalf("image preview label missing: %v", tt.Texts())
	}
	h.app.openFilePreview("readme.md")
	tt.Frame()
	if !tt.HasText("Markdown preview") {
		t.Fatalf("Markdown preview label missing after switching back: %v", tt.Texts())
	}
}
