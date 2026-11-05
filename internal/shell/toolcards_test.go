package shell

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
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
	app := &App{Screen: Session}
	sessionRenderStates.Delete(app)
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	app.SetSnapshot(m6ToolSnapshot())
	tt := ui.NewTester(app.View, 1600, 1000)
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
	app := &App{Screen: Session}
	sessionRenderStates.Delete(app)
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	app.SetSnapshot(m6ToolSnapshot())
	tt := ui.NewTester(app.View, 1600, 1000)
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
