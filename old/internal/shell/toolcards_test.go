package shell

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// m6ToolSnapshot builds one finished call per variant.
func m6ToolSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.Tools = map[string]state.ToolState{
		"c1": {
			CallID: "c1", Name: "bash", Status: state.ToolDone,
			Args: json.RawMessage(`{"cmd":"go test ./..."}`), Result: "ok\nPASS",
		},
		"c2": {
			CallID: "c2", Name: "read_file", Status: state.ToolDone,
			Args: json.RawMessage(`{"path":"README.md"}`), Result: "one\ntwo\nthree",
		},
		"c3": {
			CallID: "c3", Name: "write_file", Status: state.ToolDone,
			Args: json.RawMessage(`{"path":"out.txt","content":"a\nb"}`), Result: "written",
		},
		"c4": {
			CallID: "c4", Name: "edit_file", Status: state.ToolDone,
			Args: json.RawMessage(`{"path":"main.go","old_string":"a\nb","new_string":"a\nb\nc"}`), Result: "edited",
		},
		"c5": {
			CallID: "c5", Name: "glob", Status: state.ToolDone,
			Args: json.RawMessage(`{"pattern":"**/*.go"}`), Result: "a.go\nb.go\nc.go",
		},
		"c6": {
			CallID: "c6", Name: "grep", Status: state.ToolDone,
			Args: json.RawMessage(`{"pattern":"TODO"}`), Result: "Found 2 matches",
		},
		"c7": {
			CallID: "c7", Name: "web_search", Status: state.ToolDone,
			Args: json.RawMessage(`{"url":"https://example.com/docs"}`), Result: "Example Domain",
		},
		"c8": {
			CallID: "c8", Name: "read_image", Status: state.ToolDone,
			Args: json.RawMessage(`{"path":"pic.png"}`), Result: "image 640×480",
		},
		"c9": {
			CallID: "c9", Name: "ask_user", Status: state.ToolDone,
			Args: json.RawMessage(`{"question":"Which database should I use?"}`), Result: "sqlite",
		},
		"c10": {
			CallID: "c10", Name: "echo", Status: state.ToolDone,
			Args: json.RawMessage(`{"x":1}`), Result: "ok",
		},
	}
	return snap
}

func TestToolCardsVariantRows(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, m6ToolSnapshot())
	// Work details defaults to Detailed, which starts every tool card open.
	// This test keeps the collapsed state's product intent (the title row
	// alone), so pin the setting that starts cards collapsed.
	if err := h.store.SetSetting(session.SettingWorkDetails, session.WorkDetailsStandard); err != nil {
		t.Fatalf("work details setting: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Every card keeps the existing title line.
	for _, title := range []string{
		"bash · done", "read_file · done", "write_file · done", "edit_file · done",
		"glob · done", "grep · done", "web_search · done", "read_image · done",
		"ask_user · done", "echo · done",
	} {
		if !tt.HasText(title) {
			t.Fatalf("tool title %q missing; texts: %v", title, tt.Texts())
		}
	}
	// Each variant contributes its own summary.
	for _, summary := range []string{
		"go test ./...",                // command
		"README.md · read · 3 lines",   // file read
		"out.txt · written · 2 lines",  // file write
		"main.go · edited · +3 −2",     // file edit
		"**/*.go · 3 matches",          // search glob
		"TODO · 2 matches",             // search grep
		"https://example.com/docs",     // web
		"pic.png · 640×480",            // image
		"Which database should I use?", // question
	} {
		if !tt.HasText(summary) {
			t.Fatalf("variant summary %q missing; texts: %v", summary, tt.Texts())
		}
	}
	// A generic tool keeps its arguments collapsed.
	if tt.HasText(`{"x":1}`) {
		t.Fatalf("generic card leaks arguments while collapsed: %v", tt.Texts())
	}
}

func TestToolCardExpandShowsVariantDetail(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, m6ToolSnapshot())
	if err := h.store.SetSetting(session.SettingWorkDetails, session.WorkDetailsStandard); err != nil {
		t.Fatalf("work details setting: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Tool bash"); err != nil {
		t.Fatalf("open command card: %v", err)
	}
	tt.Frame()
	if !tt.HasText("$ go test ./...") || !tt.HasText("PASS") {
		t.Fatalf("command card detail lacks command/output: %v", tt.Texts())
	}
	if err := tt.Click("Tool bash"); err != nil {
		t.Fatalf("close command card: %v", err)
	}
	tt.Frame()
	if tt.HasText("PASS") {
		t.Fatalf("closed command card still shows output: %v", tt.Texts())
	}

	// A question card opens to its prompt and answer.
	if err := tt.Click("Tool ask_user"); err != nil {
		t.Fatalf("open question card: %v", err)
	}
	tt.Frame()
	if !tt.HasText("sqlite") {
		t.Fatalf("question card detail lacks its result: %v", tt.Texts())
	}
}

func TestSearchCount(t *testing.T) {
	cases := map[string]int{
		"":                       0,
		"a.go\nb.go":             2,
		"Found 2 matches":        2,
		"no matches found":       0,
		`["a.go","b.go","c.go"]`: 3,
		"12 results":             12,
	}
	for result, want := range cases {
		if got := searchCount(result); got != want {
			t.Errorf("searchCount(%q) = %d, want %d", result, got, want)
		}
	}
}

// toolCardSnapshot builds one card of each result kind the native rendering
// distinguishes: command output, Markdown, a missing image and a failed call.
func toolCardSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.Tools = map[string]state.ToolState{
		"c1": {
			CallID: "c1", Name: "bash", Status: state.ToolDone,
			Args:   json.RawMessage(`{"cmd":"go test ./..."}`),
			Result: "ok  github.com/zeokeel/zeokeel/old/internal/shell  37s\nPASS",
		},
		"c2": {
			CallID: "c2", Name: "web_search", Status: state.ToolDone,
			Args:   json.RawMessage(`{"query":"brand guidelines"}`),
			Result: "## Brand notes\n\n- primary blue `#0D44F6`\n- radius 10",
		},
		"c3": {
			CallID: "c3", Name: "read_image", Status: state.ToolDone,
			Args:   json.RawMessage(`{"path":"assets/missing-chart.png"}`),
			Result: "image 640×480",
		},
		"c4": {
			CallID: "c4", Name: "bash", Status: state.ToolError,
			Args:    json.RawMessage(`{"cmd":"git status"}`),
			Result:  "fatal: not a git repository",
			IsError: true,
		},
	}
	return snap
}

// openToolCall renders the Session screen with one tool card open.
func openToolCall(t *testing.T, snap *state.Snapshot, callID string) (*liveApp, *ui.Tester) {
	t.Helper()
	h := newLiveSnapshotApp(t, Session, snap)
	h.app.Language = LangEN
	h.app.live().expanded[callID] = true
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	return h, tt
}

// TestToolCardCommandOutputIsCodeBlockWithCopy covers the shell-output card:
// markdown.View renders it as a highlighted code block with its language label
// and the library's Copy button, which copies the output.
func TestToolCardCommandOutputIsCodeBlockWithCopy(t *testing.T) {
	_, tt := openToolCall(t, toolCardSnapshot(), "c1")
	if !tt.HasText("Code, bash") || !tt.HasText("Copy code") {
		t.Fatalf("command output is not a code block: %v", tt.Texts())
	}
	if !tt.HasText("PASS") {
		t.Fatalf("command output missing: %v", tt.Texts())
	}
	if err := tt.Click("Copy code"); err != nil {
		t.Fatalf("click Copy code: %v", err)
	}
	if got := tt.Clipboard(); !strings.Contains(got, "PASS") {
		t.Fatalf("Copy code copied %q, want the command output", got)
	}
}

// TestToolCardMarkdownResultRendersMarkdown covers a text result going through
// the same Markdown view as assistant messages.
func TestToolCardMarkdownResultRendersMarkdown(t *testing.T) {
	_, tt := openToolCall(t, toolCardSnapshot(), "c2")
	for _, want := range []string{"Brand notes", "primary blue", "#0D44F6", "radius 10"} {
		if !tt.HasText(want) {
			t.Fatalf("Markdown result missing %q: %v", want, tt.Texts())
		}
	}
}

// TestToolCardImageFallsBackToDescription covers a missing image: the Markdown
// image loader fails and the card shows its description instead of an empty
// box or a crash.
func TestToolCardImageFallsBackToDescription(t *testing.T) {
	_, tt := openToolCall(t, toolCardSnapshot(), "c3")
	if !tt.HasText("missing-chart.png") {
		t.Fatalf("image card lost its path: %v", tt.Texts())
	}
	// The alt/description is what Markdown shows while (and if) the file never
	// loads; it must mention the image.
	if !tt.HasText("640×480") {
		t.Fatalf("image card lost its description: %v", tt.Texts())
	}
}

// TestToolCardErrorUsesDangerPanel covers the failed result: a distinct danger
// panel with the error label, the output and a Copy error control.
func TestToolCardErrorUsesDangerPanel(t *testing.T) {
	_, tt := openToolCall(t, toolCardSnapshot(), "c4")
	if !tt.HasText("Error") {
		t.Fatalf("failed result has no Error label: %v", tt.Texts())
	}
	if !tt.HasText("fatal: not a git repository") {
		t.Fatalf("failed result text missing: %v", tt.Texts())
	}
	if !tt.HasText("Copy error") {
		t.Fatalf("failed result has no Copy error control: %v", tt.Texts())
	}
	if err := tt.Click("Copy error"); err != nil {
		t.Fatalf("click Copy error: %v", err)
	}
	if got := tt.Clipboard(); !strings.Contains(got, "fatal: not a git repository") {
		t.Fatalf("Copy error copied %q", got)
	}
}

// TestToolCardLongResultLineCap covers the performance cap: a long result shows
// the first toolResultMaxLines lines and a Show all control, which expands it
// and can collapse it again.
func TestToolCardLongResultLineCap(t *testing.T) {
	lines := make([]string, 0, toolResultMaxLines+30)
	for i := 0; i < toolResultMaxLines+30; i++ {
		lines = append(lines, fmt.Sprintf("line %d of the tool output", i))
	}
	snap := state.NewSnapshot()
	snap.Tools = map[string]state.ToolState{
		"c1": {
			CallID: "c1", Name: "bash", Status: state.ToolDone,
			Args:   json.RawMessage(`{"cmd":"seq"}`),
			Result: strings.Join(lines, "\n"),
		},
	}
	h, tt := openToolCall(t, snap, "c1")
	_ = h
	if !tt.HasText("line 0 of the tool output") {
		t.Fatalf("capped result lost its first line: %v", tt.Texts())
	}
	last := fmt.Sprintf("line %d of the tool output", len(lines)-1)
	if tt.HasText(last) {
		t.Fatalf("long result is not capped: %v", tt.Texts())
	}
	showAll := fmt.Sprintf("Show all %d lines", len(lines))
	if !tt.HasText(showAll) {
		t.Fatalf("long result has no %q control: %v", showAll, tt.Texts())
	}
	if err := tt.Click(showAll); err != nil {
		t.Fatalf("click Show all: %v", err)
	}
	tt.Frame()
	if !tt.HasText(last) {
		t.Fatalf("Show all did not expand the result: %v", tt.Texts())
	}
	if !tt.HasText("Collapse result") {
		t.Fatalf("expanded result has no collapse control: %v", tt.Texts())
	}
	if err := tt.Click("Collapse result"); err != nil {
		t.Fatalf("click Collapse result: %v", err)
	}
	tt.Frame()
	if tt.HasText(last) {
		t.Fatalf("Collapse result left the tail visible: %v", tt.Texts())
	}
}

// toolCardPreviewSnapshot is the Session screen for the tool-card preview: a
// short turn plus one open card of each result kind (command output, Markdown,
// an image, and a failed call). The Seq/StartedSeq values put the four calls in
// record order before the answer, so the preview shows the record-ordered
// transcript (user -> process block -> answer) instead of the legacy tail.
func toolCardPreviewSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Seq: 1, Role: state.RoleUser, Text: "帮我检查 README 和品牌资源，再跑一下测试。"},
		{MsgID: "a1", Seq: 6, Role: state.RoleAssistant, Text: "已完成：README 已读取，`go test ./...` 全部通过。"},
	}
	snap.Tools = map[string]state.ToolState{
		"c1": {
			CallID: "c1", StartedSeq: 2, Name: "bash", Status: state.ToolDone,
			Args:   json.RawMessage(`{"cmd":"go test ./..."}`),
			Result: "ok  github.com/zeokeel/zeokeel/old/internal/shell  37s\nPASS",
		},
		"c2": {
			CallID: "c2", StartedSeq: 3, Name: "web_search", Status: state.ToolDone,
			Args:   json.RawMessage(`{"query":"brand guidelines"}`),
			Result: "**品牌指南**：主色 `#0D44F6`，控件圆角 10。",
		},
		"c3": {
			CallID: "c3", StartedSeq: 4, Name: "read_image", Status: state.ToolDone,
			Args:   json.RawMessage(`{"path":"brand-mark.png","alt":"ZeoKeel 品牌图形标"}`),
			Result: "image 48×48",
		},
		"c4": {
			CallID: "c4", StartedSeq: 5, Name: "bash", Status: state.ToolError,
			Args:    json.RawMessage(`{"cmd":"git status"}`),
			Result:  "fatal: not a git repository (or any of the parent directories): .git",
			IsError: true,
		},
	}
	return snap
}

// TestRenderToolCardNativePreview renders the expanded tool cards for brand
// review: light and dark at the 1600x1000 design size. Ordinary runs write to
// the test's temp dir; set ZEOKEEL_UPDATE_PREVIEWS=1 to refresh
// docs/design/preview.
func TestRenderToolCardNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{"light", false},
		{"dark", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := toolCardPreviewSnapshot()
			h := newLiveSnapshotApp(t, Session, snap)
			h.app.Language = LangZH
			// A real image in the workspace root exercises the Markdown image
			// loader; the missing-image fallback is covered by the unit test.
			if err := os.WriteFile(filepath.Join(h.dir, "brand-mark.png"), markPNG, 0o600); err != nil {
				t.Fatalf("write preview image: %v", err)
			}
			theme := session.ThemeLight
			if tc.dark {
				theme = session.ThemeDark
			}
			if err := h.store.SetSetting(session.SettingTheme, theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			for id := range snap.Tools {
				h.app.live().expanded[id] = true
			}
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			// The image loads on its own goroutine; give it a few frames.
			for i := 0; i < 20; i++ {
				time.Sleep(5 * time.Millisecond)
				tt.Frame()
			}
			path := filepath.Join(dir, "toolcard-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close %s: %v", path, err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("preview %s is empty: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}
