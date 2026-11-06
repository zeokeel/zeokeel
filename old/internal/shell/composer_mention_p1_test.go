package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// accessRoleListItem is platform.AccessRole's RoleListItem value: ui.RoleListItem
// minus the RoleGroup base that the access tree maps with (the other a11y test
// helpers define RoleMenuItem/RoleHeading the same way).
const accessRoleListItem uint8 = 27

// writeQuotedFixture creates workspace entries whose paths contain spaces, for
// the quoted "@" mention tests.
func writeQuotedFixture(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"my docs", filepath.Join("my docs", "sub dir")} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	for name, body := range map[string]string{
		filepath.Join("my docs", "my file.txt"):         "f",
		filepath.Join("my docs", "sub dir", "note.txt"): "n",
		"plain.txt": "p",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// openQuotedMention types s (a draft ending in a quoted "@" token) and advances
// the frames that record the caret and rebuild the menu.
func openQuotedMention(t *testing.T, app *App, tt *ui.Tester, draft string) {
	t.Helper()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type(draft)
	tt.Frame()
	tt.Frame()
}

// TestQuotedMentionTriggerUnits pins the quoted-token parser: @"..." and @'...'
// are one mention token that may contain spaces, the closing quote is optional
// while typing, and the nearest token to the caret wins.
func TestQuotedMentionTriggerUnits(t *testing.T) {
	cases := []struct {
		name   string
		draft  string
		caret  int // -1 means end of draft
		kind   composerTriggerKind
		query  string
		start  int
		end    int
		quoted bool
	}{
		{"double quoted closed", `@"my docs/my file.txt"`, -1, composerTriggerMention,
			"my docs/my file.txt", 0, 22, true},
		{"single quoted closed", `@'my docs'`, -1, composerTriggerMention, "my docs", 0, 10, true},
		{"quoted unterminated", `@"my fi`, -1, composerTriggerMention, "my fi", 0, 7, true},
		{"quoted mid text", `look @"my fi`, -1, composerTriggerMention, "my fi", 5, 12, true},
		{"caret before closing quote", `@"my docs/my file.txt"`, 9, composerTriggerMention,
			"my docs", 0, 22, true},
		{"caret after quoted token", `@"my docs" `, -1, composerTriggerNone, "", 0, 0, false},
		{"plain token after quoted", `@"a b" @fm`, -1, composerTriggerMention, "fm", 7, 10, false},
		{"quoted token after plain", `@fm @"a b"`, -1, composerTriggerMention, "a b", 4, 10, true},
		{"not a boundary", `x@"a b"`, -1, composerTriggerNone, "", 0, 0, false},
		{"plain still works", `@probe`, -1, composerTriggerMention, "probe", 0, 6, false},
		{"quoted path out of workspace parses", `@"../out`, -1, composerTriggerMention, "../out", 0, 8, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caret := tc.caret
			if caret < 0 {
				caret = len([]rune(tc.draft))
			}
			got := activeComposerTrigger(tc.draft, caret)
			if got.kind != tc.kind || got.query != tc.query || got.quoted != tc.quoted {
				t.Fatalf("trigger(%q,%d) = {%v,%q,quoted=%v}, want {%v,%q,quoted=%v}",
					tc.draft, caret, got.kind, got.query, got.quoted, tc.kind, tc.query, tc.quoted)
			}
			if tc.kind == composerTriggerMention && (got.start != tc.start || got.end != tc.end) {
				t.Fatalf("trigger span = [%d,%d), want [%d,%d)", got.start, got.end, tc.start, tc.end)
			}
		})
	}
}

// TestQuotedMentionFilterAndInsert proves filtered completion of a spaced path
// shows the quoted spelling and inserts the canonical @"..." token.
func TestQuotedMentionFilterAndInsert(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeQuotedFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openQuotedMention(t, h.app, tt, `@"my docs/`)

	if !tt.HasText(`@"my docs/my file.txt"`) {
		t.Fatalf("quoted menu missing the spaced file: %v", tt.Texts())
	}
	if !tt.HasText(`@"my docs/sub dir/"`) {
		t.Fatalf("quoted menu missing the spaced folder: %v", tt.Texts())
	}
	if tt.HasText("plain.txt") {
		t.Fatalf("quoted filter shows entries outside the browsed folder: %v", tt.Texts())
	}
	if err := tt.Click(`@"my docs/my file.txt"`); err != nil {
		t.Fatalf("click quoted file row: %v", err)
	}
	tt.Frame()
	if want := `@"my docs/my file.txt" `; h.app.draft != want {
		t.Fatalf("quoted pick draft = %q, want %q", h.app.draft, want)
	}
}

// TestQuotedMentionDrillWithSpaces proves a folder whose path contains spaces
// drills and picks with the quoted form, and Esc walks back through the path.
func TestQuotedMentionDrillWithSpaces(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeQuotedFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openQuotedMention(t, h.app, tt, `@"my docs/su`)

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Frame()
	if want := `@"my docs/sub dir/`; h.app.draft != want {
		t.Fatalf("spaced folder drill draft = %q, want %q", h.app.draft, want)
	}
	if !tt.HasText(`@"my docs/sub dir/note.txt"`) {
		t.Fatalf("drilled spaced folder missing its file: %v", tt.Texts())
	}
	if !tt.HasText(`@"my docs/sub dir/"`) {
		t.Fatalf("drilled breadcrumb missing the quoted path: %v", tt.Texts())
	}

	if err := tt.Click(`@"my docs/sub dir/note.txt"`); err != nil {
		t.Fatalf("click nested spaced file: %v", err)
	}
	tt.Frame()
	if want := `@"my docs/sub dir/note.txt" `; h.app.draft != want {
		t.Fatalf("spaced nested pick draft = %q, want %q", h.app.draft, want)
	}

	// Esc walks the quoted path back one level at a time.
	h.app.draft = `@"my docs/sub dir/`
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if want := `@"my docs/`; h.app.draft != want {
		t.Fatalf("first quoted Esc draft = %q, want %q", h.app.draft, want)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if want := `@"`; h.app.draft != want {
		t.Fatalf("second quoted Esc draft = %q, want %q", h.app.draft, want)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Mention suggestions") {
		t.Fatalf("third quoted Esc did not close the menu: %v", tt.Texts())
	}
}

// TestQuotedMentionOutOfWorkspace proves a quoted traversal never lists or
// inserts workspace-outside entries.
func TestQuotedMentionOutOfWorkspace(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeQuotedFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openQuotedMention(t, h.app, tt, `@"../outside`)
	if tt.HasText("Mention suggestions") {
		t.Fatalf("quoted traversal opened a menu: %v", tt.Texts())
	}
	if h.app.draft != `@"../outside` {
		t.Fatalf("quoted traversal changed the draft to %q", h.app.draft)
	}
}

// TestMentionChevronPointerDrill proves the chevron control drills while the
// row body keeps the pick semantics.
func TestMentionChevronPointerDrill(t *testing.T) {
	const rowLabel = `@"my docs/sub dir/"`

	newRow := func(t *testing.T) (*liveApp, *ui.Tester, ui.Rect) {
		t.Helper()
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		writeQuotedFixture(t, h.dir)
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		openQuotedMention(t, h.app, tt, `@"my docs/`)
		rect, ok := tt.Find(rowLabel)
		if !ok {
			t.Fatalf("spaced folder row missing: %v", tt.Texts())
		}
		return h, tt, rect
	}

	t.Run("chevron drills", func(t *testing.T) {
		h, tt, rect := newRow(t)
		tt.ClickAt(rect.X+rect.W-10, rect.Y+rect.H/2)
		tt.Frame()
		tt.Frame()
		if want := `@"my docs/sub dir/`; h.app.draft != want {
			t.Fatalf("chevron click draft = %q, want drill %q", h.app.draft, want)
		}
		if !tt.HasText(`@"my docs/sub dir/note.txt"`) {
			t.Fatalf("chevron drill did not enter the folder: %v", tt.Texts())
		}
	})

	t.Run("row body picks", func(t *testing.T) {
		h, tt, rect := newRow(t)
		tt.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tt.Frame()
		if want := rowLabel + " "; h.app.draft != want {
			t.Fatalf("row-body click draft = %q, want pick %q", h.app.draft, want)
		}
	})
}

// TestMentionQuotedA11y checks the chevron adds no duplicate accessible names:
// the row keeps one list-item name and the hint is not an interactive control.
func TestMentionQuotedA11y(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeQuotedFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openQuotedMention(t, h.app, tt, `@"my docs/`)

	const rowLabel = `@"my docs/sub dir/"`
	nodes := testerAccessNodes(t, tt)
	rowItems := 0
	for _, node := range nodes {
		if node.Label != rowLabel {
			continue
		}
		if node.Role == accessRoleListItem {
			rowItems++
		}
	}
	if rowItems != 1 {
		t.Fatalf("accessible list item for %q = %d, want 1: %+v", rowLabel, rowItems, nodes)
	}
	for _, node := range nodes {
		if node.Label != "Open folder" {
			continue
		}
		if node.States&accessFocusableState != 0 {
			t.Fatalf("row hint became an interactive control: %+v", node)
		}
	}
}

// TestQuotedMentionNarrowDark proves the quoted menu and drill breadcrumb fit a
// narrow dark window.
func TestQuotedMentionNarrowDark(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeQuotedFixture(t, h.dir)
	h.app.draft = `@"my docs/sub dir/`
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.SetDark(true)
	tt.Frame()
	tt.Frame()
	rect, ok := tt.Find(`@"my docs/sub dir/"`)
	if !ok {
		t.Fatalf("narrow dark menu missing the quoted breadcrumb: %v", tt.Texts())
	}
	if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > 900 || rect.Y+rect.H > 600 {
		t.Fatalf("narrow quoted breadcrumb outside the window: %+v", rect)
	}
	if !tt.HasText(`@"my docs/sub dir/note.txt"`) {
		t.Fatalf("narrow dark menu missing the nested file: %v", tt.Texts())
	}
	if !strings.Contains(strings.Join(tt.Texts(), " "), "Esc") {
		t.Fatalf("narrow dark menu missing the Esc hint: %v", tt.Texts())
	}
}

// TestSlashHelpDocumentsMentionSyntax pins the /help note for quoted paths and
// the canonical session reference with its bounded read-only context.
func TestSlashHelpDocumentsMentionSyntax(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	slashComposer(t, h.app, tt, "/help")
	for _, want := range []string{
		"@ mentions complete workspace files",
		`@"my file.txt"`,
		"atomic @[label](dsh-session:",
		"bounded read-only context",
	} {
		if !strings.Contains(h.app.draft, want) {
			t.Fatalf("/help text missing %q: %q", want, h.app.draft)
		}
	}
}
