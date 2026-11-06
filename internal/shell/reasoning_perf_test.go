package shell

// Performance guards for the streaming reasoning row: once the reasoning text
// stops changing, the row must stop requesting animation frames, and a cached
// preview must be reused instead of rescanning the whole reasoning text on
// every frame. The 100 KiB texts below are the long-thinking case from
// REVIEW-3 P2-4.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/state"
)

// longReasoningText builds roughly 100 KiB of live reasoning with two
// paragraphs so the collapsed preview has a completed paragraph to summarize.
func longReasoningText() string {
	var builder strings.Builder
	for builder.Len() < 100<<10 {
		builder.WriteString("Let me trace through the transcript renderer and check the fold timing. ")
	}
	builder.WriteString("\n\nAnd here is the paragraph the collapsed preview should show.\nand it is still being written")
	return builder.String()
}

// TestReasoningAnimationStopsWhenTextStopsChanging: the running row may animate
// its pulse, but a text that stopped changing must not keep the whole view
// rebuilding at 60fps forever.
func TestReasoningAnimationStopsWhenTextStopsChanging(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.SetLanguageForTest(LangEN)
	text := longReasoningText()
	tt := ui.NewTester(func(c *ui.Context) {
		h.app.liveReasoningRow(c, design.Light(), "msg-static", text, true)
	}, 1200, 400)

	// The pulse window is bounded: two pulse cycles without a new delta must
	// leave the tester with no frame requested.
	tt.Advance(2 * design.DurationPulse)
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("running reasoning row kept requesting animation frames after its text stopped changing")
	}
}

// TestReasoningPreviewCachedUntilTextChanges: the collapsed preview is a pure
// function of the reasoning text, so repeating the same text (every frame of a
// static running row) must reuse the cached summary instead of rescanning up
// to 100 KiB per frame.
func TestReasoningPreviewCachedUntilTextChanges(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	cache := h.app.reasoningFrameCache()
	now := time.Unix(1, 0)
	text := longReasoningText()

	trimmed, preview, _ := cache.preview("msg-1", text, now)
	if trimmed == "" || preview == "" {
		t.Fatalf("first preview observation = (%q, %q), want non-empty", trimmed, preview)
	}
	if !strings.Contains(preview, "paragraph the collapsed preview should show") {
		t.Fatalf("preview = %q, want the completed paragraph's first line", preview)
	}
	for i := 0; i < 200; i++ {
		again, againPreview, _ := cache.preview("msg-1", text, now.Add(time.Duration(i)*time.Millisecond))
		if again != trimmed || againPreview != preview {
			t.Fatalf("cached preview changed on an unchanged text: (%q, %q) != (%q, %q)", again, againPreview, trimmed, preview)
		}
	}
	if got := h.app.reasoningStats().previewComputes; got != 1 {
		t.Fatalf("preview computations = %d after 201 unchanged observations, want 1", got)
	}

	changed := text + "\n\nA brand new completed paragraph.\nstill writing"
	_, changedPreview, _ := cache.preview("msg-1", changed, now)
	if !strings.Contains(changedPreview, "A brand new completed paragraph.") {
		t.Fatalf("preview after a text change = %q, want the new paragraph", changedPreview)
	}
	if got := h.app.reasoningStats().previewComputes; got != 2 {
		t.Fatalf("preview computations = %d after one text change, want 2", got)
	}
	cache.preview("msg-2", changed, now)
	if got := h.app.reasoningStats().previewComputes; got != 3 {
		t.Fatalf("preview computations = %d for a second message, want 3", got)
	}
}

// TestReasoningPreviewCachedLookupAllocatesNothing: the unchanged-text hot path
// must not allocate: a 100 KiB text is compared by signature, not copied.
func TestReasoningPreviewCachedLookupAllocatesNothing(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	cache := h.app.reasoningFrameCache()
	text := longReasoningText()
	now := time.Unix(2, 0)
	cache.preview("msg-alloc", text, now)
	allocs := testing.AllocsPerRun(200, func() {
		_, _, _ = cache.preview("msg-alloc", text, now)
	})
	if allocs > 0.5 {
		t.Fatalf("unchanged 100 KiB preview lookup allocated %.1f objects per call, want < 0.5", allocs)
	}
}

// TestReasoningTurnTextCachedAcrossFrames: the turn aggregation (joining the
// assistant reasoning of one turn) must run once per snapshot, not on every
// frame of a long streaming thought; a new delta must refresh it live.
func TestReasoningTurnTextCachedAcrossFrames(t *testing.T) {
	reasoning := longReasoningText()
	first := reasoningTestSnapshot(state.PhaseRunning)
	first.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "explain"),
		reasoningAssistantMsg("a1", 2, 2, "", reasoning),
	}
	tt, h := newReasoningTester(t, first)
	// The global memo is shared by every App render in the process, so measure
	// the delta this fixture adds rather than an absolute count.
	baseline := h.app.reasoningStats().turnComputes
	tt.Frame()
	if got := h.app.reasoningStats().turnComputes; got != baseline {
		t.Fatalf("turn reasoning computations = %d after repeated frames, want %d (cached)", got, baseline)
	}
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if got := h.app.reasoningStats().turnComputes; got != baseline {
		t.Fatalf("turn reasoning computations = %d after repeated frames, want %d (cached)", got, baseline)
	}

	later := reasoningTestSnapshot(state.PhaseRunning)
	later.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "explain"),
		reasoningAssistantMsg("a1", 2, 2, "", reasoning+"\n\nA new delta that must show up live.\nstill writing"),
	}
	h.app.SetSnapshot(later)
	tt.Frame()
	if got := h.app.reasoningStats().turnComputes; got != baseline+1 {
		t.Fatalf("turn reasoning computations = %d after a new delta, want %d", got, baseline+1)
	}
	if !tt.HasText("A new delta that must show up live.") {
		t.Fatalf("preview did not follow the new delta; texts:\n%s", reasoningTexts(tt))
	}
}

// TestReasoningLongPreviewStillLive: a 100 KiB reasoning keeps a usable live
// preview through the cache, and a newly completed paragraph updates it.
func TestReasoningLongPreviewStillLive(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	cache := h.app.reasoningFrameCache()
	text := longReasoningText()
	_, preview, _ := cache.preview("msg-live", text, time.Unix(3, 0))
	if !strings.Contains(preview, "paragraph the collapsed preview should show") {
		t.Fatalf("long preview = %q, want the completed paragraph summary", preview)
	}
	grown := text + "\n\nAnother completed paragraph for the live preview.\nstill writing"
	_, next, _ := cache.preview("msg-live", grown, time.Unix(4, 0))
	if !strings.Contains(next, "Another completed paragraph for the live preview.") {
		t.Fatalf("grown preview = %q, want the new paragraph", next)
	}
}

// TestReasoningAnimationRestartsOnNewDelta: a streaming delta restarts the
// bounded pulse window, so the live preview still shimmers while the thought is
// being written (the static case above stops after the window).
func TestReasoningAnimationRestartsOnNewDelta(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.SetLanguageForTest(LangEN)
	text := longReasoningText()
	tt := ui.NewTester(func(c *ui.Context) {
		h.app.liveReasoningRow(c, design.Light(), "msg-live-delta", text, true)
	}, 1200, 400)
	tt.Advance(2 * design.DurationPulse)
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("static reasoning still requests frames before the delta")
	}

	text += "\n\nA new delta\nstill being written"
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("a new reasoning delta did not restart the pulse animation")
	}
}
