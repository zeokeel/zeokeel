package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// writeMentionFixture creates the files and folders the "@" tests browse.
func writeMentionFixture(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"probe.txt", "probe2.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "guide.md"), []byte("g"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "sub", "note.md"), []byte("n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
}

// focusedDraftComposer clicks the composer and types draft, then advances the
// frames that record the caret and rebuild the popup. With arrows the caret is
// moved left before the frames, so the trigger token is mid-message.
func focusedDraftComposer(t *testing.T, tt *ui.Tester, draft string, left int) {
	t.Helper()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type(draft)
	tt.Frame()
	for i := 0; i < left; i++ {
		tt.Key(0, ui.KeyLeft)
	}
	tt.Frame()
	tt.Frame()
}

// TestComposerTriggerUnits pins the pure trigger rules both popups share.
func TestComposerTriggerUnits(t *testing.T) {
	cases := []struct {
		name  string
		draft string
		caret int
		kind  composerTriggerKind
		query string
	}{
		{"empty", "", 0, composerTriggerNone, ""},
		{"slash start", "/", 1, composerTriggerSlash, ""},
		{"slash query", "/pl", 3, composerTriggerSlash, "pl"},
		{"slash mid text", "read /pl", 8, composerTriggerSlash, "pl"},
		{"slash after punctuation", "hi(/pl", 6, composerTriggerSlash, "pl"},
		{"url scheme", "https:/x", 8, composerTriggerNone, ""},
		{"double slash", "//cmd", 5, composerTriggerNone, ""},
		{"not a boundary", "a/b", 3, composerTriggerNone, ""},
		{"at start", "@", 1, composerTriggerMention, ""},
		{"at query", "@pr", 3, composerTriggerMention, "pr"},
		{"at mid text", "read @pr", 8, composerTriggerMention, "pr"},
		{"email is not a trigger", "a@b.com", 7, composerTriggerNone, ""},
		{"at wins over slash before caret", "/plan @fi", 9, composerTriggerMention, "fi"},
		{"slash token after at token", "@x /pl", 6, composerTriggerSlash, "pl"},
		{"at after space ends slash scan", "/pl @", 5, composerTriggerMention, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := activeComposerTrigger(tc.draft, tc.caret)
			if got.kind != tc.kind || got.query != tc.query {
				t.Fatalf("activeComposerTrigger(%q,%d) = {%v,%q}, want {%v,%q}",
					tc.draft, tc.caret, got.kind, got.query, tc.kind, tc.query)
			}
		})
	}
}

// TestMentionQueryPathUnits covers the path/browse split and rune-safe
// replacement helpers.
func TestMentionQueryPathUnits(t *testing.T) {
	cases := []struct {
		query, dir, prefix string
	}{
		{"", "", ""},
		{"do", "", "do"},
		{"docs/", "docs/", ""},
		{"docs/gu", "docs/", "gu"},
		{"docs/sub/", "docs/sub/", ""},
	}
	for _, tc := range cases {
		dir, prefix := mentionQueryPath(tc.query)
		if dir != tc.dir || prefix != tc.prefix {
			t.Fatalf("mentionQueryPath(%q) = (%q,%q), want (%q,%q)",
				tc.query, dir, prefix, tc.dir, tc.prefix)
		}
	}

	h := newLiveApp(t, &shellProvider{})
	h.app.draft = "看看 @pr 这个"
	st := h.app.slashMenu()
	st.caret = draftRuneLen("看看 @pr")
	st.caretDraft = h.app.draft
	st.caretSet = true
	if kind := h.app.composerTrigger().kind; kind != composerTriggerMention {
		t.Fatalf("mid-text trigger kind = %v", kind)
	}
	h.app.replaceComposerTrigger("@probe.txt ")
	if want := "看看 @probe.txt 这个"; h.app.draft != want {
		t.Fatalf("replacement draft = %q, want %q", h.app.draft, want)
	}
	if st.caretDraft != h.app.draft || st.caret != draftRuneLen(h.app.draft) {
		t.Fatalf("caret after replacement = %d/%q", st.caret, st.caretDraft)
	}
}

// TestComposerCaretRecorded proves both live composers feed the caret back to
// the trigger detection.
func TestComposerCaretRecorded(t *testing.T) {
	fixtures := []struct {
		name   string
		screen Screen
	}{
		{"session", Session},
		{"home", Home},
	}
	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			h.app.Screen = tc.screen
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			focusedDraftComposer(t, tt, "/pl", 0)
			st := h.app.slashMenu()
			if !st.caretSet || st.caret != 3 || st.caretDraft != "/pl" {
				t.Fatalf("recorded caret = %d/%q (set=%v), want 3/%q",
					st.caret, st.caretDraft, st.caretSet, "/pl")
			}
			tt.Key(0, ui.KeyLeft)
			tt.Frame()
			tt.Frame()
			if st.caret != 2 {
				t.Fatalf("caret after Left = %d, want 2", st.caret)
			}
		})
	}
}

// TestSlashMentionMutualExclusion proves only the popup for the token under the
// caret is on screen, and the "+" menu hides both.
func TestSlashMentionMutualExclusion(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeMentionFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The "@" token at the caret wins even though the draft starts with "/plan".
	focusedDraftComposer(t, tt, "/plan @pro", 0)
	if !tt.HasText("probe.txt") {
		t.Fatalf("mention menu missing for a mid-draft @ token: %v", tt.Texts())
	}
	if tt.HasText("Attach files") {
		t.Fatalf("slash palette rendered together with the mention menu: %v", tt.Texts())
	}

	// A slash token after the @ token opens only the palette.
	h.app.draft = "@x /pl"
	h.app.Screen = Session
	tt2 := ui.NewTester(h.app.View, 1600, 1000)
	tt2.Frame()
	if !tt2.HasText("Plan mode") {
		t.Fatalf("slash palette missing for /pl: %v", tt2.Texts())
	}
	if tt2.HasText("Mention suggestions") {
		t.Fatalf("mention menu rendered together with the slash palette: %v", tt2.Texts())
	}

	// The "+" attach menu is the only derived popup: it hides both lists.
	h.app.draft = "/"
	h.app.attachOpen = true
	tt3 := ui.NewTester(h.app.View, 1600, 1000)
	tt3.Frame()
	if tt3.HasText("Attach files") || tt3.HasText("Mention suggestions") {
		t.Fatalf("derived popups rendered while the attach menu is open: %v", tt3.Texts())
	}
	h.app.attachOpen = false
}

// TestMentionMidTextReplacement picks a file while the caret is in the middle
// of the message and keeps the surrounding text intact.
func TestMentionMidTextReplacement(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeMentionFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	// Caret right after "@pro" (before " 这个").
	focusedDraftComposer(t, tt, "看看 @pro 这个", 3)
	if !tt.HasText("probe.txt") {
		t.Fatalf("mention menu missing for the mid-message @ token: %v", tt.Texts())
	}
	if err := tt.Click("probe.txt"); err != nil {
		t.Fatalf("click probe.txt: %v", err)
	}
	tt.Frame()
	if want := "看看 @probe.txt 这个"; h.app.draft != want {
		t.Fatalf("mid-text mention draft = %q, want %q", h.app.draft, want)
	}
	if tt.HasText("Mention suggestions") {
		t.Fatalf("mention menu stayed open after the pick: %v", tt.Texts())
	}
}

// TestMentionFolderDrill covers the P0 drill: Tab opens the highlighted folder
// in place, the breadcrumb is shown, typing filters inside the folder, and Esc
// walks up one level at a time before it closes the menu.
func TestMentionFolderDrill(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeMentionFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	focusedDraftComposer(t, tt, "@do", 0)
	if !tt.HasText("docs/") {
		t.Fatalf("mention menu missing the docs folder: %v", tt.Texts())
	}
	// The folder row advertises the real drill verb now.
	if !tt.HasText("Open folder") {
		t.Fatalf("folder row hint missing: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Frame()
	if h.app.draft != "@docs/" {
		t.Fatalf("Tab drill draft = %q, want @docs/", h.app.draft)
	}
	if !tt.HasText("guide.md") {
		t.Fatalf("drilled menu does not list the folder contents: %v", tt.Texts())
	}
	if !tt.HasText("Esc to go up") {
		t.Fatalf("drill breadcrumb missing: %v", tt.Texts())
	}

	// Typing inside the folder filters its entries only.
	tt.Type("gu")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("guide.md") {
		t.Fatalf("folder-qualified filter missing guide.md: %v", tt.Texts())
	}
	if tt.HasText("docs/sub/") {
		t.Fatalf("folder filter still shows unrelated entries: %v", tt.Texts())
	}

	// Esc walks up one level per press, then closes the menu.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.draft != "@docs/" {
		t.Fatalf("first Esc draft = %q, want @docs/", h.app.draft)
	}
	if !tt.HasText("guide.md") {
		t.Fatalf("first Esc should show the folder list: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.draft != "@" {
		t.Fatalf("second Esc draft = %q, want @", h.app.draft)
	}
	if !tt.HasText("probe.txt") {
		t.Fatalf("second Esc should show the workspace root: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Mention suggestions") {
		t.Fatalf("third Esc did not close the mention menu: %v", tt.Texts())
	}
	if h.app.draft != "@" {
		t.Fatalf("closing Esc changed the draft to %q", h.app.draft)
	}
}

// TestMentionNarrow checks the drilled "@" menu fits a 900x600 window: the
// breadcrumb stays inside it and a file can still be picked.
func TestMentionNarrow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeMentionFixture(t, h.dir)
	h.app.draft = "@docs/"
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	rect, ok := tt.Find("@docs/")
	if !ok {
		t.Fatalf("narrow drilled menu missing the breadcrumb: %v", tt.Texts())
	}
	if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > 900 || rect.Y+rect.H > 600 {
		t.Fatalf("narrow breadcrumb outside the window: %+v", rect)
	}
	if !tt.HasText("Esc to go up") {
		t.Fatalf("narrow drilled menu missing the Esc hint: %v", tt.Texts())
	}
	if err := tt.Click("docs/guide.md"); err != nil {
		t.Fatalf("click the nested file: %v", err)
	}
	tt.Frame()
	if want := "@docs/guide.md "; h.app.draft != want {
		t.Fatalf("narrow nested pick draft = %q, want %q", h.app.draft, want)
	}
}

// TestMentionFileRowTabKeepsFocus keeps the old list-focus behavior for plain
// files: Tab must not drill or change the draft for them.
func TestMentionFileRowTabKeepsFocus(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeMentionFixture(t, h.dir)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	focusedDraftComposer(t, tt, "@probe", 0)
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if h.app.draft != "@probe" {
		t.Fatalf("Tab on a file row changed the draft to %q", h.app.draft)
	}
	if !tt.Focused("probe.txt") {
		t.Fatalf("Tab did not focus the first mention row: %v", tt.Texts())
	}
}

// TestMentionEmptyFolderEscGoesUp covers drilling into an empty folder: the
// list has no rows, but Esc still walks back up instead of closing the draft.
func TestMentionEmptyFolderEscGoesUp(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	writeMentionFixture(t, h.dir)
	h.app.draft = "@empty/"
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.draft != "@" {
		t.Fatalf("Esc from an empty folder draft = %q, want @", h.app.draft)
	}
}
