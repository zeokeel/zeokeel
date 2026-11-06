package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// presentArtifactApp runs one scripted turn that presents artifactArgs and
// returns the live app plus the presented artifact's workspace root.
func presentArtifactApp(t *testing.T, artifactArgs string) *App {
	t.Helper()
	provider := &m6Provider{steps: []m6Step{
		{tool: "present", args: artifactArgs},
		{text: "artifact presented"},
	}}
	app := m6LiveApp(t, provider, tools.NewPresentTool())
	app.Language = LangEN
	m6Submit(t, app, "present the artifact")
	return app
}

// writeArtifactFile writes a file under a live app's workspace root.
func writeArtifactFile(t *testing.T, app *App, rel, content string) string {
	t.Helper()
	path := filepath.Join(app.workspaceRoot(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

// TestArtifactCardPreviewsInSidebar proves a presented file's card opens the
// file in the Files tab preview, which is the parity action the reference
// product shows on a presented card.
func TestArtifactCardPreviewsInSidebar(t *testing.T) {
	app := presentArtifactApp(t, `{"title":"Report","path":"notes/unit.md","markdown":"inline body"}`)
	writeArtifactFile(t, app, "notes/unit.md", "hello artifact")

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Preview in sidebar") {
		t.Fatalf("card preview action missing: %v", tt.Texts())
	}
	if err := tt.Click("Preview in sidebar"); err != nil {
		t.Fatalf("click Preview in sidebar: %v", err)
	}
	tt.Frame()

	if !app.rightOpen || app.rightTab != filesTab {
		t.Fatalf("preview action open=%v tab=%v, want the Files tab", app.rightOpen, app.rightTab)
	}
	if app.filePath != "notes/unit.md" {
		t.Fatalf("previewed path = %q, want notes/unit.md", app.filePath)
	}
	if !tt.HasText("hello artifact") {
		t.Fatalf("artifact content missing from the preview: %v", tt.Texts())
	}
}

// TestArtifactCardOpensAndRevealsInSystemApp covers the card's native actions:
// Open hands the file's absolute path to the default-app opener and Reveal
// hands it to the file-manager revealer.
func TestArtifactCardOpensAndRevealsInSystemApp(t *testing.T) {
	app := presentArtifactApp(t, `{"title":"Report","path":"notes/unit.md"}`)
	absPath := writeArtifactFile(t, app, "notes/unit.md", "hello artifact")
	var opened, revealed string
	app.filesNativeUI().openPath = func(path string) error {
		opened = path
		return nil
	}
	app.filesNativeUI().revealPath = func(path string) error {
		revealed = path
		return nil
	}

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Preview in sidebar") {
		t.Fatalf("card preview action missing: %v", tt.Texts())
	}
	if err := tt.Click("Open presented file in system app"); err != nil {
		t.Fatalf("click Open: %v", err)
	}
	if opened != absPath {
		t.Fatalf("opener received %q, want %q", opened, absPath)
	}
	if err := tt.Click("Show presented file in file manager"); err != nil {
		t.Fatalf("click Reveal: %v", err)
	}
	if revealed != absPath {
		t.Fatalf("revealer received %q, want %q", revealed, absPath)
	}
}

// TestArtifactCardHostNoteWithoutDesktop covers the headless host: the card
// keeps the sidebar-preview action, hides the native ones, and explains why in
// the reference product's wording.
func TestArtifactCardHostNoteWithoutDesktop(t *testing.T) {
	app := presentArtifactApp(t, `{"title":"Report","path":"notes/unit.md"}`)
	writeArtifactFile(t, app, "notes/unit.md", "hello artifact")

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Preview in sidebar") {
		t.Fatalf("sidebar preview action missing without a desktop: %v", tt.Texts())
	}
	if !tt.HasText("no desktop available") {
		t.Fatalf("host note missing on the card: %v", tt.Texts())
	}
	if tt.HasText("Open presented file in system app") {
		t.Fatalf("native open action rendered without a desktop: %v", tt.Texts())
	}
}

// TestArtifactCardChineseHostNote pins the Chinese card labels and hint the
// user's reference screenshot shows.
func TestArtifactCardChineseHostNote(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "present", args: `{"title":"报告","path":"notes/unit.md"}`},
		{text: "已展示"},
	}}
	app := m6LiveApp(t, provider, tools.NewPresentTool())
	app.Language = LangZH
	m6Submit(t, app, "展示文件")
	writeArtifactFile(t, app, "notes/unit.md", "内容")

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{"在侧栏预览", "此主机没有可用的桌面"} {
		if !tt.HasText(want) {
			t.Fatalf("Chinese card text %q missing: %v", want, tt.Texts())
		}
	}
}

// TestArtifactCardOutsideWorkspaceRejected covers the workspace boundary: an
// absolute path outside the session workspace is not previewable and gets the
// explanation instead of an action or a read.
func TestArtifactCardOutsideWorkspaceRejected(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	app := presentArtifactApp(t, fmt.Sprintf(`{"title":"Outside","path":%q}`, outside))

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("outside the workspace") {
		t.Fatalf("outside-workspace explanation missing: %v", tt.Texts())
	}
	if tt.HasText("Preview in sidebar") {
		t.Fatalf("outside artifact still offers a sidebar preview: %v", tt.Texts())
	}
	if app.filePath != "" {
		t.Fatalf("outside artifact set the previewed path %q", app.filePath)
	}
}
