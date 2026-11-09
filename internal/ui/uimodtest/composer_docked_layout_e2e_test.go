package uimodtest

// Docked-inspector layout E2E: the reported regression where opening the right
// inspector made the main composer unusable or invisible. The host used to
// build the docked content through an inner ui.Split with no height
// constraint, so the content column sized to its intrinsic height: an empty
// conversation left the composer floating in the middle of the window, a long
// one pushed it offscreen, and the transcript List could not scroll.
//
// These tests drive the real desktop assembly through mygo's in-memory Tester
// and read the laid-out rects of the composer's visible controls.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// composerControls are the composer's visible pieces: the placeholder (its
// TextArea label), the send control and the attachment menu.
var composerControls = []string{"输入消息…", "发送", "附件"}

// requireComposerAtBottom asserts the composer is built, fully inside the
// window and in its bottom band. A FillHeight content column that failed to
// take the window height leaves the composer near the top (empty
// conversation) or below the window (long conversation).
func requireComposerAtBottom(t *testing.T, tester *ui.Tester, width, height int) {
	t.Helper()
	bottomBand := float32(height) * 0.6
	rects := make(map[string]ui.Rect, len(composerControls))
	for _, text := range composerControls {
		rect, ok := tester.Find(text)
		// The evidence line the report cites: the composer's boxes in the
		// frame the user gets after the inspector opens.
		t.Logf("%dx%d composer %q rect = {X:%.1f Y:%.1f W:%.1f H:%.1f} (want Y > %.1f)",
			width, height, text, rect.X, rect.Y, rect.W, rect.H, bottomBand)
		if !ok {
			t.Fatalf("%dx%d: composer control %q missing; texts = %v", width, height, text, tester.Texts())
		}
		if rect.W <= 0 || rect.H <= 0 {
			t.Fatalf("%dx%d: composer control %q has no visible area: %+v", width, height, text, rect)
		}
		if !windowHasRect(width, height, rect) {
			t.Fatalf("%dx%d: composer control %q is outside the window: %+v", width, height, text, rect)
		}
		if rect.Y <= bottomBand {
			t.Fatalf("%dx%d: composer control %q is not in the bottom band: y=%.1f, want y > %.1f",
				width, height, text, rect.Y, bottomBand)
		}
		rects[text] = rect
	}
	if input, send := rects["输入消息…"], rects["发送"]; input.Y >= send.Y {
		t.Fatalf("%dx%d: composer input (y=%.1f) is not above the send row (y=%.1f)",
			width, height, input.Y, send.Y)
	}
}

// TestDockedInspectorKeepsComposerAtBottom covers the plain reporting case: the
// right inspector docked open on wide windows, with the sidebar both expanded
// and in its icon rail, and the inspector closed and reopened, must keep the
// composer inside the window's bottom band.
func TestDockedInspectorKeepsComposerAtBottom(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	for _, size := range [][2]int{{1534, 1140}, {1280, 800}} {
		width, height := size[0], size[1]
		tester.SetSize(width, height)
		tester.Frame()
		for _, collapsed := range []bool{false, true} {
			product.uiRoot.SetSidebarCollapsed(collapsed)
			tester.Frame()
			product.uiRoot.SetRightPanelOpen(true)
			tester.Frame()
			tester.Frame()
			if !product.uiRoot.RightPanelOpen() {
				t.Fatalf("%dx%d collapsed=%v: inspector did not stay open", width, height, collapsed)
			}
			// A docked inspector is no modal dialog: the drawer names itself
			// 检查器 as a RoleDialog, the docked pane does not.
			if _, drawer := tester.AccessByRole(ui.RoleDialog, "检查器"); drawer {
				t.Fatalf("%dx%d collapsed=%v: inspector rendered as the drawer, want docked; texts = %v",
					width, height, collapsed, tester.Texts())
			}
			if !tester.HasText("概览") {
				t.Fatalf("%dx%d collapsed=%v: docked inspector content missing; texts = %v",
					width, height, collapsed, tester.Texts())
			}
			requireComposerAtBottom(t, tester, width, height)

			// Close and reopen: the content column must settle back at the
			// window height both ways (no auto-height frame).
			product.uiRoot.SetRightPanelOpen(false)
			tester.Frame()
			requireComposerAtBottom(t, tester, width, height)
			product.uiRoot.SetRightPanelOpen(true)
			tester.Frame()
			requireComposerAtBottom(t, tester, width, height)
		}
	}

	// A narrow window keeps the modal-drawer path: the composer is still laid
	// out at the window bottom behind the drawer, and the drawer names itself.
	product.uiRoot.SetSidebarCollapsed(false)
	tester.SetSize(1024, 720)
	tester.Frame()
	tester.Frame()
	product.uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	if !tester.HasText("检查器") {
		t.Fatalf("1024x720: inspector did not render as the drawer; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDialog, "检查器"); !ok {
		t.Fatalf("1024x720: drawer is not an accessible dialog; texts = %v", tester.Texts())
	}
	requireComposerAtBottom(t, tester, 1024, 720)
}

// usageChatServer answers every chat request with one short reply and token
// usage, so the session status strip (the usage line under the composer) has
// data, as it does in the user's screenshot.
func usageChatServer(t *testing.T) *httptest.Server {
	t.Helper()
	var calls atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reply := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"回复 %d\"},\"index\":0}]}\n\n", reply)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],"+
			"\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":34,\"total_tokens\":46,\"prompt_cache_hit_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// newLongTranscriptProduct boots the desktop assembly on a 1534x1140 window
// (the reported screenshot's size) with a scripted chat server that reports
// usage, and opens a conversation.
func newLongTranscriptProduct(t *testing.T) *interactionProduct {
	t.Helper()
	server := usageChatServer(t)
	t.Cleanup(server.Close)
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	t.Cleanup(func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	})
	chat := requireChat(t, root)
	if _, err := chat.NewSession("长对话布局测试"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1534, 1140)
	tester.Frame()
	tester.Frame()
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester.Frame()
	return &interactionProduct{root: root, uiRoot: uiRoot, tester: tester, loader: loader, chat: chat}
}

// TestDockedInspectorLongTranscriptKeepsComposerAndFollowsEnd covers the
// screenshot scenario: a transcript longer than the window with the inspector
// docked open. The List must scroll/follow to the end inside its bounded
// height (the last message visible above the composer) and the composer must
// stay on-screen; before the fix the content column grew with the transcript,
// pushing the composer and the usage strip below the window and leaving the
// List unable to scroll.
func TestDockedInspectorLongTranscriptKeepsComposerAndFollowsEnd(t *testing.T) {
	const width, height = 1534, 1140
	product := newLongTranscriptProduct(t)
	tester := product.tester
	sessionID := product.chat.ActiveSession()
	if sessionID == "" {
		t.Fatal("no active session")
	}
	product.uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	if !tester.HasText("概览") {
		t.Fatalf("docked inspector content missing; texts = %v", tester.Texts())
	}

	// A dozen rounds comfortably exceed the transcript's viewport at 1140 DIP.
	const rounds = 12
	for i := 1; i <= rounds; i++ {
		if err := product.chat.Send(sessionID, fmt.Sprintf("消息 %d", i)); err != nil {
			t.Fatalf("Send(消息 %d): %v", i, err)
		}
		waitIdle(t, product.chat, sessionID, 10*time.Second)
		tester.Frame()
	}
	tester.Frame()

	// The composer and its usage strip stay at the window bottom.
	requireComposerAtBottom(t, tester, width, height)
	if !tester.HasText("轮") {
		t.Fatalf("session usage strip missing under the composer; texts = %v", tester.Texts())
	}

	send, _ := tester.Find("发送")
	last, ok := tester.Find(fmt.Sprintf("消息 %d", rounds))
	if !ok {
		t.Fatalf("newest message missing from the transcript; texts = %v", tester.Texts())
	}
	if !windowHasRect(width, height, last) || last.W <= 0 || last.H <= 0 {
		t.Fatalf("newest message is not visible in the %dx%d window: %+v (the List did not follow the end)",
			width, height, last)
	}
	if last.Y+last.H > send.Y {
		t.Fatalf("newest message (bottom=%.1f) overlaps the composer top (y=%.1f)", last.Y+last.H, send.Y)
	}
	if last.Y <= float32(height)*0.4 {
		t.Fatalf("newest message is too high (y=%.1f) for a List followed to its end", last.Y)
	}
	// The List scrolled: the first message left the viewport (virtualized or
	// clipped away) instead of the whole transcript being laid out past the
	// window bottom.
	if first, ok := tester.Find("消息 1"); ok && windowHasRect(width, height, first) && first.W > 0 && first.H > 0 {
		t.Fatalf("oldest message is still inside the window: %+v; the List did not scroll to the end", first)
	}
}
