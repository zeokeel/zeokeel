package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/sessionreference"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// sessionMentionLabel finds the "@" menu row of one session by its display
// title and returns the candidate the row inserts.
func sessionMentionLabel(t *testing.T, a *App, label string) (detail string, ok bool) {
	t.Helper()
	for _, choice := range mentionChoices(a.mentionEntries()) {
		if choice.session && choice.label == label {
			return choice.detail, true
		}
	}
	return "", false
}

// TestSessionMentionWritesCanonicalMention covers the @ menu's session pick:
// it inserts the canonical @[label](dsh-session:<base64url(id)>) mention, so the
// reference is structured text the controller can resolve, not a "#title"
// fallback. The label may contain Chinese and spaces.
func TestSessionMentionWritesCanonicalMention(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	otherID := createOtherWorkspaceSession(t, h, "Plans", "方案 讨论")
	setMentionDraft(h, "compare @方案")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if _, ok := sessionMentionLabel(t, h.app, "方案 讨论"); !ok {
		t.Fatalf("mention menu is missing the session row: %v", tt.Texts())
	}
	if err := tt.Click("Session \u00b7 方案 讨论"); err != nil {
		t.Fatalf("click session mention: %v", err)
	}
	tt.Frame()

	want := "compare " + sessionreference.FormatMention("方案 讨论", otherID) + " "
	if h.app.draft != want {
		t.Fatalf("draft after session mention = %q, want %q", h.app.draft, want)
	}
	// The inserted mention parses back to the exact session id and label.
	rendered, refs, err := sessionreference.ParseText(h.app.draft)
	if err != nil {
		t.Fatalf("inserted mention does not parse: %v", err)
	}
	if len(refs) != 1 || refs[0].SessionID != otherID || refs[0].Label != "方案 讨论" {
		t.Fatalf("parsed refs = %+v, want the picked session", refs)
	}
	if rendered != "compare @方案 讨论 " {
		t.Fatalf("readable rendering = %q", rendered)
	}
}

// TestTranscriptRendersCanonicalMentionAsLabel covers the transcript half of
// the known mygo limitation: the composer holds the canonical mention text, but
// the sent message bubble shows the readable @label instead of the raw URI.
func TestTranscriptRendersCanonicalMentionAsLabel(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	id := h.ctrl.CurrentSessionID()
	mention := sessionreference.FormatMention("Plan 重点", id)
	snap := state.NewSnapshot()
	snap.SessionID = id
	snap.Messages = []state.Message{{
		MsgID: "user-1",
		Role:  state.RoleUser,
		Text:  "look at " + mention + " now",
	}}
	h.app.SetSnapshot(snap)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("@Plan 重点") {
		t.Fatalf("transcript did not render the readable mention: %v", tt.Texts())
	}
	if tt.HasText("dsh-session:") {
		t.Fatalf("transcript leaked the raw canonical URI: %v", tt.Texts())
	}
}

// TestTranscriptKeepsMalformedMention proves an unparseable reference is shown
// exactly as typed: rendering a transcript never fails or silently drops text.
func TestTranscriptKeepsMalformedMention(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	id := h.ctrl.CurrentSessionID()
	snap := state.NewSnapshot()
	snap.SessionID = id
	snap.Messages = []state.Message{{
		MsgID: "user-1",
		Role:  state.RoleUser,
		Text:  "bad @[x](dsh-session:bad!) mention",
	}}
	h.app.SetSnapshot(snap)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("dsh-session:bad!") {
		t.Fatalf("malformed mention was not preserved as typed: %v", tt.Texts())
	}
}

// TestSendMalformedSessionMentionShowsError covers the user-visible side of a
// bad canonical mention: Enter fails the send, shows a presentable error toast
// and keeps the draft, instead of silently dropping the message.
func TestSendMalformedSessionMentionShowsError(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	const draft = "bad @[x](dsh-session:bad!) mention"
	h.app.draft = draft
	h.app.setComposerCaretAfterInsert()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("invalid session reference URI") {
		t.Fatalf("missing the malformed-reference error toast: %v", tt.Texts())
	}
	if h.app.draft != draft {
		t.Fatalf("failed send changed the draft to %q, want %q", h.app.draft, draft)
	}
	if got := h.ctrl.Snapshot().Messages; len(got) != 0 {
		t.Fatalf("rejected prompt reached the session history: %+v", got)
	}
}

// TestReferencedSessionContextIsFoldedInChat is the P1-2 regression: the
// injected `## Referenced sessions` row persists (the model history needs it)
// but Chat must not render it as a user bubble or count it as a turn. It is
// folded into a compact disclosure instead, live and after a reopen.
func TestReferencedSessionContextIsFoldedInChat(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	source, err := h.store.CreateSession(h.ctrl.CurrentWorkspaceID(), "Source 会话")
	if err != nil {
		t.Fatalf("CreateSession(source): %v", err)
	}
	if _, err := h.store.AppendMessage(store.Message{
		SessionID: source.ID,
		Role:      string(state.RoleUser),
		Content:   "SOURCE-BODY-MARKER",
	}); err != nil {
		t.Fatalf("seed source message: %v", err)
	}
	liveID := h.ctrl.CurrentSessionID()
	prompt := "cite " + sessionreference.FormatMention("Source 会话", source.ID)
	if err := h.ctrl.Submit(context.Background(), liveID, prompt); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertFoldedReferenceContext(t, h, tt, liveID, prompt)

	// Reopen replays the same rows from the store; the fold must be identical.
	snapshot, err := h.ctrl.OpenSession(context.Background(), liveID)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	h.app.SetSnapshot(snapshot)
	reopened := ui.NewTester(h.app.View, 1600, 1000)
	reopened.Frame()
	assertFoldedReferenceContext(t, h, reopened, liveID, prompt)
}

// assertFoldedReferenceContext checks one rendered transcript of a session that
// cited another session.
func assertFoldedReferenceContext(t *testing.T, h *liveApp, tt *ui.Tester, sessionID, prompt string) {
	t.Helper()
	// The prompt itself still renders as one user bubble (readable @label).
	if !tt.HasText("@Source 会话") {
		t.Fatalf("prompt bubble missing the readable session reference: %v", tt.Texts())
	}
	// The raw snapshot never renders: no JSON, no source body, no turn count.
	if tt.HasText(`"conversation"`) || tt.HasText("SOURCE-BODY-MARKER") || tt.HasText("dsh-session:") {
		t.Fatalf("raw reference snapshot leaked into the chat transcript: %v", tt.Texts())
	}
	if tt.HasText("Jump to turn 2") {
		t.Fatalf("injected context produced a phantom turn: %v", tt.Texts())
	}
	// It is folded into one compact disclosure row.
	if !tt.HasText("Referenced sessions") {
		t.Fatalf("folded reference-context row missing: %v", tt.Texts())
	}
	stored, err := h.store.ListMessages(sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	injected := 0
	for _, message := range stored {
		if strings.HasPrefix(strings.TrimSpace(message.Content), sessionreference.SectionHeading) {
			injected++
		}
	}
	if injected != 1 {
		t.Fatalf("persisted model history has %d injected context rows, want 1", injected)
	}
}

// TestMentionCandidatesRankSameCwdFirst is the P2-1 regression: the @ menu uses
// sessionreference.Candidates ordering, so a session in the current workspace
// comes before a more recently updated session from another workspace.
func TestMentionCandidatesRankSameCwdFirst(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	// The current session "live" predates the other workspace's session, so a
	// pure UpdatedAt order would put the other workspace first.
	createOtherWorkspaceSession(t, h, "Other WS", "lively other")
	h.app.Screen = Home
	setMentionDraft(h, "read @liv")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	texts := tt.Texts()
	sameAt := textIndex(texts, "Session \u00b7 live")
	otherAt := textIndex(texts, "Session \u00b7 lively other")
	if sameAt < 0 || otherAt < 0 {
		t.Fatalf("mention session rows missing (same=%d other=%d): %v", sameAt, otherAt, texts)
	}
	if sameAt > otherAt {
		t.Fatalf("cross-workspace session ranked before the current workspace one: same=%d other=%d",
			sameAt, otherAt)
	}
}

// TestReferencedSessionContextFoldExpands lists the snapshot summary on demand:
// expanding the folded row shows the source labels and message counts, never
// the untrusted snapshot body.
func TestReferencedSessionContextFoldExpands(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	source, err := h.store.CreateSession(h.ctrl.CurrentWorkspaceID(), "Source 会话")
	if err != nil {
		t.Fatalf("CreateSession(source): %v", err)
	}
	if _, err := h.store.AppendMessage(store.Message{
		SessionID: source.ID,
		Role:      string(state.RoleUser),
		Content:   "SOURCE-BODY-MARKER",
	}); err != nil {
		t.Fatalf("seed source message: %v", err)
	}
	liveID := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), liveID,
		"cite "+sessionreference.FormatMention("Source 会话", source.ID)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	const disclosure = "Referenced sessions · 1 source. Show referenced session summary"
	if err := tt.Click(disclosure); err != nil {
		t.Fatalf("expand the folded reference context: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Source 会话 · 1 message") {
		t.Fatalf("expanded fold is missing the source summary: %v", tt.Texts())
	}
	if tt.HasText("SOURCE-BODY-MARKER") || tt.HasText(`"conversation"`) {
		t.Fatalf("expanded fold leaked the snapshot body: %v", tt.Texts())
	}
}
