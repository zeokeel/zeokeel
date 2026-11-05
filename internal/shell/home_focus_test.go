package shell

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// homeFocusFixture is the live Home screen with one open session, the fixture
// the composer-focus regression tests drive. Its composer input is found by its
// accessible name, the same label the Session composer uses.
func homeFocusFixture(t *testing.T) (*liveApp, *shellProvider, *ui.Tester) {
	t.Helper()
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus Home composer: %v", err)
	}
	return h, provider, tt
}

// TestHomeComposerKeepsFocusWithSlashPalette is the regression for the Home
// composer losing the keyboard focus when the "/" palette opens above it: the
// composer was rebuilt with a new element id when the palette was inserted in
// front of it, so the focused id dangled and the following Enter never reached
// composerTextKey. Session's composer already restores its focus through
// AutoFocus; Home's did not (see homeComposerCard).
func TestHomeComposerKeepsFocusWithSlashPalette(t *testing.T) {
	h, provider, tt := homeFocusFixture(t)

	tt.Type("/")
	tt.Frame()
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus when the / palette opened: %q", tt.Texts())
	}
	tt.Type("help")
	tt.Frame()
	if h.app.draft != "/help" {
		t.Fatalf("draft after typing /help = %q, want %q", h.app.draft, "/help")
	}
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus while the / palette is open: %q", tt.Texts())
	}

	// Enter in the composer must run the command locally, exactly as it does
	// in the Session composer: /help writes the command list into the draft
	// and nothing is sent to the model.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.HasPrefix(h.app.draft, "Commands:") {
		t.Fatalf("Enter did not run /help on Home: draft = %q", h.app.draft)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("/help reached the model (%d calls)", got)
	}
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus after running /help: %q", tt.Texts())
	}
}

// TestHomeComposerKeepsFocusWithMentionMenu covers the other derived composer
// popup: the "@" mention list also sits in front of the composer, so the same
// id shift used to drop the focus. Typing must keep editing the draft while the
// menu is open.
func TestHomeComposerKeepsFocusWithMentionMenu(t *testing.T) {
	h, _, tt := homeFocusFixture(t)

	tt.Type("read @")
	tt.Frame()
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus when the @ mention menu opened: %q", tt.Texts())
	}
	// The menu lists the workspace's files; a following keystroke still edits
	// the composer draft. This build's fixture workspace is empty, so type a
	// query that matches nothing and check the draft itself.
	tt.Type("no-match")
	tt.Frame()
	if want := "read @no-match"; h.app.draft != want {
		t.Fatalf("draft after typing into the open mention menu = %q, want %q", h.app.draft, want)
	}
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus while the @ menu is open: %q", tt.Texts())
	}
}

// TestHomeComposerKeepsFocusAfterAttachMenu closes the "+" menu and types again:
// the focus the fix restores must survive the menu opening and closing instead
// of leaving the composer inert.
func TestHomeComposerKeepsFocusAfterAttachMenu(t *testing.T) {
	h, _, tt := homeFocusFixture(t)

	if err := tt.Click("Add files or run commands"); err != nil {
		t.Fatalf("open attachment menu: %v", err)
	}
	tt.Frame()
	if !h.app.attachOpen {
		t.Fatalf("attachment menu did not open: %q", tt.Texts())
	}
	// Close it with the same control, then keep typing: the draft must follow
	// the keys, so the composer still owns the focus after the menu closes.
	h.app.attachOpen = false
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("refocus Home composer: %v", err)
	}
	tt.Type("hello")
	tt.Frame()
	if want := "hello"; h.app.draft != want {
		t.Fatalf("draft after closing the attachment menu = %q, want %q", h.app.draft, want)
	}
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus after the attachment menu: %q", tt.Texts())
	}
}

// TestHomeComposerAutoFocusDoesNotStealFocusFromOtherControls proves the fix
// only restores the focus the popup shift dropped: it is not a per-frame focus
// grab. A control the user focuses outside the composer keeps the focus across
// frames, so the Home composer can no longer fight the rest of the screen.
func TestHomeComposerAutoFocusDoesNotStealFocusFromOtherControls(t *testing.T) {
	_, _, tt := homeFocusFixture(t)

	if err := tt.Click("View options"); err != nil {
		t.Fatalf("focus View options: %v", err)
	}
	tt.Frame()
	if !tt.Focused("View options") {
		t.Fatalf("View options did not take the focus: %q", tt.Texts())
	}
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if !tt.Focused("View options") {
		t.Fatalf("Home composer AutoFocus stole the focus from View options: %q", tt.Texts())
	}
	if tt.Focused("Composer input") {
		t.Fatal("Home composer took the focus while View options had it")
	}
}

// TestHomeComposerSlashCommandWithoutSession runs the Home no-session command
// through the keyboard, the path the focus bug broke: Enter executes /plan
// instead of leaving the raw command in the draft, and the composer keeps the
// focus. (The no-session explanation toast itself needs a build-time caller and
// is covered through the palette click in TestPlanCommandWithoutSessionToasts
// AndDoesNotPanic.)
func TestHomeComposerSlashCommandWithoutSession(t *testing.T) {
	h := newPlanHarnessNoSession(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	typeComposer(t, tt, "/plan")
	if got := h.app.draft; got != "" {
		t.Fatalf("Enter did not run /plan on Home without a session: draft = %q", got)
	}
	if got := h.provider.callCount(); got != 0 {
		t.Fatalf("/plan without a session reached the model (%d calls)", got)
	}
	if !tt.Focused("Composer input") {
		t.Fatalf("Home composer lost the keyboard focus after the /plan command: %q", tt.Texts())
	}
}
