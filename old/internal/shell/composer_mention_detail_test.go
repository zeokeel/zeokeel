package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
)

// accessRoleButton is platform.AccessRole's RoleButton value: ui.RoleButton
// minus the RoleGroup base that the access tree maps with (the other a11y test
// helpers define RoleListItem/RoleHeading the same way).
const accessRoleButton uint8 = 2

// createOtherWorkspaceSession registers a second workspace with one session
// and restores the first workspace, so the current session list and the
// workspace root stay those of the original fixture.
func createOtherWorkspaceSession(t *testing.T, h *liveApp, workspace, title string) string {
	t.Helper()
	current := h.ctrl.CurrentWorkspaceID()
	other, err := h.store.CreateWorkspace(workspace, t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace(%q): %v", workspace, err)
	}
	id, err := h.ctrl.CreateSessionInWorkspace(context.Background(), other.ID, title)
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	if err := h.ctrl.SelectWorkspace(context.Background(), current); err != nil {
		t.Fatalf("SelectWorkspace back: %v", err)
	}
	return id
}

// setMentionDraft places draft in the composer and puts the caret at its end,
// so the "@" trigger is derived exactly as it is after typing the text.
func setMentionDraft(h *liveApp, draft string) {
	h.app.draft = draft
	h.app.setComposerCaretAfterInsert()
	h.app.Screen = Home
}

// TestQuotedMentionOnlySearchesFiles pins DSH's rule for the quoted form: an
// unquoted token lists files then sessions, while @"... searches files only.
// The quoted spelling is kept on the inserted pick, including for a directory.
func TestQuotedMentionOnlySearchesFiles(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	writeQuotedFixture(t, h.dir)

	// The unquoted menu lists the matching session after the files.
	setMentionDraft(h, "@")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Session \u00b7 live") {
		t.Fatalf("unquoted @ menu is missing the session: %v", tt.Texts())
	}

	// Opening the quoted token searches the workspace files only.
	setMentionDraft(h, `@"`)
	tt.Frame()
	tt.Frame()
	if tt.HasText("Session \u00b7 live") {
		t.Fatalf("quoted @\" menu listed sessions: %v", tt.Texts())
	}
	if !tt.HasText("plain.txt") {
		t.Fatalf("quoted @\" menu is missing the files: %v", tt.Texts())
	}
	if _, ok := sessionMentionLabel(t, h.app, "live"); ok {
		t.Fatal("quoted @\" entries contain a session candidate")
	}

	// A directory keeps the quoted spelling when it is picked as a reference.
	if err := tt.Click(`@"my docs/"`); err != nil {
		t.Fatalf("click the quoted directory row: %v", err)
	}
	tt.Frame()
	if want := `@"my docs/" `; h.app.draft != want {
		t.Fatalf("quoted directory pick draft = %q, want %q", h.app.draft, want)
	}

	// A plain file typed inside the quoted token keeps the quotes too.
	setMentionDraft(h, `@"plain.txt`)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("plain.txt"); err != nil {
		t.Fatalf("click the quoted file row: %v", err)
	}
	tt.Frame()
	if want := `@"plain.txt" `; h.app.draft != want {
		t.Fatalf("quoted file pick draft = %q, want %q", h.app.draft, want)
	}
}

// TestMentionBreadcrumbJumpsToLevel covers the multi-level breadcrumb: the
// current directory stays readable and every ancestor (plus the workspace root)
// is a crumb that returns the token to that level, for plain and quoted paths.
func TestMentionBreadcrumbJumpsToLevel(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	writeMentionFixture(t, h.dir)
	writeQuotedFixture(t, h.dir)

	setMentionDraft(h, "@docs/sub/")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("@docs/sub/") {
		t.Fatalf("drilled breadcrumb is missing the current path: %v", tt.Texts())
	}
	if _, ok := tt.Find("Go to @docs/"); !ok {
		t.Fatalf("breadcrumb is missing the clickable docs level: %v", tt.Texts())
	}

	// The ancestor crumb returns the token to that level and lists its folders.
	if err := tt.Click("Go to @docs/"); err != nil {
		t.Fatalf("click the docs crumb: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if h.app.draft != "@docs/" {
		t.Fatalf("docs crumb draft = %q, want @docs/", h.app.draft)
	}
	if !tt.HasText("guide.md") {
		t.Fatalf("docs crumb did not list the docs folder: %v", tt.Texts())
	}
	if tt.HasText("note.md") {
		t.Fatalf("docs crumb still lists the sub folder: %v", tt.Texts())
	}

	// The root crumb jumps all the way back to the workspace root.
	if _, ok := tt.Find("Back to the workspace root"); !ok {
		t.Fatalf("breadcrumb is missing the root crumb: %v", tt.Texts())
	}
	if err := tt.Click("Back to the workspace root"); err != nil {
		t.Fatalf("click the root crumb: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if h.app.draft != "@" {
		t.Fatalf("root crumb draft = %q, want @", h.app.draft)
	}
	if !tt.HasText("docs/") {
		t.Fatalf("root crumb did not list the workspace root: %v", tt.Texts())
	}

	// Quoted paths keep their quotes at every level of the jump.
	setMentionDraft(h, `@"my docs/sub dir/`)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find(`Go to @"my docs/"`); !ok {
		t.Fatalf("quoted breadcrumb is missing the my docs level: %v", tt.Texts())
	}
	if err := tt.Click(`Go to @"my docs/"`); err != nil {
		t.Fatalf("click the quoted docs crumb: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if want := `@"my docs/`; h.app.draft != want {
		t.Fatalf("quoted docs crumb draft = %q, want %q", h.app.draft, want)
	}
	if !tt.HasText(`@"my docs/my file.txt"`) {
		t.Fatalf("quoted docs crumb did not list the folder: %v", tt.Texts())
	}
	if err := tt.Click("Back to the workspace root"); err != nil {
		t.Fatalf("click the quoted root crumb: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if want := `@"`; h.app.draft != want {
		t.Fatalf("quoted root crumb draft = %q, want %q", h.app.draft, want)
	}
	if !tt.HasText("plain.txt") {
		t.Fatalf("quoted root crumb did not list the workspace root: %v", tt.Texts())
	}
	if tt.HasText("Session \u00b7 live") {
		t.Fatalf("quoted root crumb listed a session: %v", tt.Texts())
	}
}

// TestMentionRowControlsA11y pins the roles and labels of the two directory
// verbs and the breadcrumb crumb controls: the row keeps one list-item name,
// the chevron and each crumb are labelled buttons, and the hint text stays a
// plain label.
func TestMentionRowControlsA11y(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	writeMentionFixture(t, h.dir)

	setMentionDraft(h, "@docs/sub/")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	nodes := testerAccessNodes(t, tt)
	if role, ok := accessNodeRole(nodes, "Back to the workspace root"); !ok || role != accessRoleButton {
		t.Fatalf("root crumb node = role %d (found %v), want a RoleButton", role, ok)
	}
	if role, ok := accessNodeRole(nodes, "Go to @docs/"); !ok || role != accessRoleButton {
		t.Fatalf("docs crumb node = role %d (found %v), want a RoleButton", role, ok)
	}

	// A directory row's chevron drills, so it is a labelled button nested under
	// the row, while the row itself keeps the single list-item name.
	setMentionDraft(h, "@")
	tt.Frame()
	tt.Frame()
	nodes = testerAccessNodes(t, tt)
	if role, ok := accessNodeRole(nodes, "Open folder docs/"); !ok || role != accessRoleButton {
		t.Fatalf("chevron node = role %d (found %v), want a labelled RoleButton", role, ok)
	}
	rowItems := 0
	for _, node := range nodes {
		if node.Label == "docs/" && node.Role == accessRoleListItem {
			rowItems++
		}
	}
	if rowItems != 1 {
		t.Fatalf("list-item nodes for the docs row = %d, want 1: %+v", rowItems, nodes)
	}
}

// accessNodeRole returns the role of the first node with the given label.
func accessNodeRole(nodes []testerAccessNodeInfo, label string) (uint8, bool) {
	for _, node := range nodes {
		if node.Label == label {
			return node.Role, true
		}
	}
	return 0, false
}

// TestMentionSessionRowShowsOtherWorkspaceAndWhen covers the session row detail:
// the relative time is always shown, and the workspace name is added only for
// a session that belongs to a workspace other than the current one. Files stay
// in front of the sessions.
func TestMentionSessionRowShowsOtherWorkspaceAndWhen(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writeTestFile(t, h.dir, "lively.md", "# lively\n")
	otherID := createOtherWorkspaceSession(t, h, "Other WS", "lively other")
	setMentionDraft(h, "read @liv")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Files render before the session rows.
	texts := tt.Texts()
	fileAt := textIndex(texts, "lively.md")
	sameAt := textIndex(texts, "Session \u00b7 live")
	otherAt := textIndex(texts, "Session \u00b7 lively other")
	if fileAt < 0 || sameAt < 0 || otherAt < 0 {
		t.Fatalf("mention menu rows missing (file=%d same=%d other=%d): %v",
			fileAt, sameAt, otherAt, texts)
	}
	if !(fileAt < sameAt && fileAt < otherAt) {
		t.Fatalf("files do not render before sessions: file=%d same=%d other=%d",
			fileAt, sameAt, otherAt)
	}

	// The same-workspace row keeps the relative time only; the cross-workspace
	// row adds its workspace name in front of the same relative time.
	if detail, ok := sessionMentionLabel(t, h.app, "live"); !ok || detail != "now" {
		t.Fatalf("same-workspace session detail = %q (found %v), want now", detail, ok)
	}
	if detail, ok := sessionMentionLabel(t, h.app, "lively other"); !ok || detail != "Other WS \u00b7 now" {
		t.Fatalf("cross-workspace session detail = %q (found %v), want Other WS · now", detail, ok)
	}
	if !tt.HasText("Other WS \u00b7 now") {
		t.Fatalf("cross-workspace session row detail missing: %v", texts)
	}
	if tt.HasText("test \u00b7 now") {
		t.Fatalf("same-workspace session row shows its workspace name: %v", texts)
	}

	// The cross-workspace row inserts the canonical session reference.
	if err := tt.Click("Session \u00b7 lively other"); err != nil {
		t.Fatalf("click the cross-workspace session row: %v", err)
	}
	tt.Frame()
	if want := "read " + sessionreference.FormatMention("lively other", otherID) + " "; h.app.draft != want {
		t.Fatalf("session pick draft = %q, want %q", h.app.draft, want)
	}
}

// textIndex returns the index of the first tester label equal to text, or -1.
func textIndex(texts []string, text string) int {
	for i, got := range texts {
		if got == text {
			return i
		}
	}
	return -1
}
