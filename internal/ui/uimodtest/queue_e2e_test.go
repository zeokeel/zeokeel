package uimodtest

// Real-backend prompt inbox E2E: the agent module's PromptQueue owns the
// per-session queue (the UI only reads/removes it). A prompt queued from the
// composer while a run streams shows in the badge/popover and is drained by the
// backend when the run settles; a user stop keeps the inbox.
//
// The first turn is served from behind a gate (gatedInbox): while the gate is
// closed the run cannot settle, so the backend cannot drain the inbox and the
// composer keeps its 排队/×N action row. That is what makes these assertions
// deterministic under -race and on a slow machine — the old fixed 600 ms stream
// let the turn finish before the frame was read, and the queue was already
// drained (queue_e2e flake: "queue count chip ×1 missing"). The gate is opened
// explicitly when the test wants the turn to finish, and released in cleanup so
// httptest.Server.Close never waits on a parked handler.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// gatedInbox serves the first chat turn from behind a gate. The request streams
// one delta (so the UI shows the reply in flight), then blocks until Release is
// called; later turns answer at once. Nothing in the test depends on wall-clock
// speed: the first run stays running until the test says otherwise.
type gatedInbox struct {
	server  *httptest.Server
	release chan struct{}
	once    sync.Once
	calls   atomic.Int64
}

func newGatedInbox(t *testing.T) *gatedInbox {
	t.Helper()
	g := &gatedInbox{release: make(chan struct{})}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		if g.calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"先处理第一条\"}}]}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-g.release
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"，完成。\"}}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"排队消息已处理\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	// LIFO: the gate opens before the server closes, because Close waits for its
	// outstanding handlers and a parked handler would hang the test.
	t.Cleanup(g.server.Close)
	t.Cleanup(g.Release)
	return g
}

// Release lets the first turn finish. Idempotent: a test that already opened the
// gate and cleanup's call cannot close the channel twice.
func (g *gatedInbox) Release() {
	g.once.Do(func() { close(g.release) })
}

// newInboxProduct boots the desktop product against the slow server and opens a
// session. It returns the product pieces plus the PromptQueue half.
func newInboxProduct(t *testing.T) (*interactionProduct, contracts.PromptQueue, *gatedInbox) {
	t.Helper()
	gate := newGatedInbox(t)
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: gate.server.URL,
	}}
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	t.Cleanup(func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	})
	chat := requireChat(t, root)
	if _, err := chat.NewSession("inbox"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	queue, ok := chat.(contracts.PromptQueue)
	if !ok {
		t.Fatal("the assembled chat service does not implement contracts.PromptQueue")
	}
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	product := &interactionProduct{root: root, uiRoot: uiRoot, tester: tester, loader: loader, chat: chat}
	return product, queue, gate
}

// pumpFramesWait frames until condition or timeout.
func pumpFramesWait(t *testing.T, product *interactionProduct, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		product.tester.Frame()
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; texts = %v", what, product.tester.Texts())
}

func inboxSession(t *testing.T, product *interactionProduct) contracts.ChatSession {
	t.Helper()
	session, ok := product.chat.Session(product.chat.ActiveSession())
	if !ok {
		t.Fatal("active session read model is missing")
	}
	return session
}

func userTexts(session contracts.ChatSession) []string {
	var texts []string
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleUser {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

// TestPromptQueueRealBackendE2E covers the whole real path: queue through the
// composer while the first run streams, see the count chip and its panel, and
// let the backend drain the inbox after the run settles (the UI sends nothing
// itself). The first turn is gated, so "while the run streams" is a fact the
// test controls rather than a 600 ms window it has to win.
func TestPromptQueueRealBackendE2E(t *testing.T) {
	product, queue, gate := newInboxProduct(t)
	sessionID := product.chat.ActiveSession()
	if err := product.chat.Send(sessionID, "第一条运行"); err != nil {
		t.Fatalf("Send(first): %v", err)
	}
	// The first delta proves the reply is in flight; the gate keeps the turn from
	// settling until this test opens it, so the inbox cannot be drained under the
	// assertions below.
	pumpFramesWait(t, product, "the first turn to stream", 10*time.Second, func() bool {
		return product.tester.HasText("先处理第一条")
	})
	if session := inboxSession(t, product); !session.Running {
		t.Fatalf("the first turn is not running behind the gate: Running=%v", session.Running)
	}

	// Queue one prompt through the running composer.
	rect, ok := product.tester.Find("输入消息…")
	if !ok {
		t.Fatalf("composer input missing; texts = %v", product.tester.Texts())
	}
	product.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	product.tester.Frame()
	product.tester.Type("第二条排队")
	product.tester.Frame()
	if err := product.tester.Click("排队"); err != nil {
		t.Fatalf("click 排队: %v; texts = %v", err, product.tester.Texts())
	}
	// The chip is an asynchronous read-model update (the backend owns the queue),
	// so wait for it instead of reading the first frame after the click: that
	// read is exactly what used to race the drain on a slow machine.
	pumpFramesWait(t, product, "the queue count chip", 10*time.Second, func() bool {
		return product.tester.HasText("×1")
	})
	// The queue's size is the ×N chip beside the run controls (p2-inbox audit:
	// the count belongs next to the control that adds to the queue, so the status
	// bar reports only the run state). It sits immediately left of 排队 and opens
	// the queue panel; the chip's accessible name carries the sentence the old
	// badge carried.
	chip, okChip := product.tester.Find("×1")
	if !okChip {
		t.Fatalf("queue count chip ×1 missing; texts = %v", product.tester.Texts())
	}
	queueButton, okQueueButton := product.tester.Find("排队")
	if !okQueueButton {
		t.Fatalf("queue action 排队 missing; texts = %v", product.tester.Texts())
	}
	if chip.X >= queueButton.X {
		t.Fatalf("queue count chip (x=%.1f) is not left of 排队 (x=%.1f)", chip.X, queueButton.X)
	}
	if !product.tester.HasText("队列中的消息（1 条）") {
		t.Fatalf("queue chip lacks its accessible sentence; texts = %v", product.tester.Texts())
	}
	if queued := queue.QueuedPrompts(sessionID); len(queued) != 1 || queued[0].Text != "第二条排队" {
		t.Fatalf("backend inbox = %+v, want the typed prompt", queued)
	}
	// The chip is the queue panel's door: opening it shows the pending prompt
	// under the panel title.
	if err := product.tester.Click("队列中的消息（1 条）"); err != nil {
		t.Fatalf("open the queue panel from the chip: %v; texts = %v", err, product.tester.Texts())
	}
	product.tester.Frame()
	// 清空全部 exists only inside the panel, so it proves the panel opened (the
	// chip's own accessible sentence already contains 队列中的消息).
	if !product.tester.HasText("第二条排队") || !product.tester.HasText("清空全部") {
		t.Fatalf("queue panel did not open from the chip; texts = %v", product.tester.Texts())
	}
	// Close it again so the drain assertions below read the bare action row.
	product.tester.Key(0, ui.KeyEscape)
	product.tester.Frame()
	if product.tester.HasText("清空全部") {
		t.Fatalf("queue panel did not close on Escape; texts = %v", product.tester.Texts())
	}

	// Now let the first turn finish: the backend drains the inbox, the queued
	// prompt becomes its own user turn, and the chip disappears.
	gate.Release()
	pumpFramesWait(t, product, "the inbox drain", 20*time.Second, func() bool {
		session, ok := product.chat.Session(sessionID)
		if !ok || session.Running {
			return false
		}
		users := userTexts(session)
		return len(queue.QueuedPrompts(sessionID)) == 0 && len(users) >= 2 && users[1] == "第二条排队"
	})
	product.tester.Frame()
	if product.tester.HasText("×1") || product.tester.HasText("队列中的消息（") {
		t.Fatalf("queue count chip survived the backend drain; texts = %v", product.tester.Texts())
	}
	if !product.tester.HasText("排队消息已处理") {
		t.Fatalf("drained turn's answer missing; texts = %v", product.tester.Texts())
	}
}

// TestPromptQueueStopKeepsInboxE2E: a user stop cancels the run and the backend
// keeps the queued prompt; the count stays visible (the UI never clears it).
// The first turn is gated, so the queue cannot be drained before the stop.
func TestPromptQueueStopKeepsInboxE2E(t *testing.T) {
	product, queue, gate := newInboxProduct(t)
	sessionID := product.chat.ActiveSession()
	if err := product.chat.Send(sessionID, "运行中"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	pumpFramesWait(t, product, "the first turn to stream", 10*time.Second, func() bool {
		return product.tester.HasText("先处理第一条")
	})
	rect, ok := product.tester.Find("输入消息…")
	if !ok {
		t.Fatalf("composer input missing; texts = %v", product.tester.Texts())
	}
	product.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	product.tester.Frame()
	product.tester.Type("停止后保留")
	product.tester.Frame()
	if err := product.tester.Click("排队"); err != nil {
		t.Fatalf("click 排队: %v", err)
	}
	pumpFramesWait(t, product, "the queue count chip", 10*time.Second, func() bool {
		return product.tester.HasText("×1")
	})
	if queued := queue.QueuedPrompts(sessionID); len(queued) != 1 || queued[0].Text != "停止后保留" {
		t.Fatalf("inbox before the stop = %+v, want the typed prompt", queued)
	}
	if err := product.tester.Click("停止"); err != nil {
		t.Fatalf("click 停止: %v", err)
	}
	// The stop is asynchronous: wait for the read model to settle, then for the
	// paused frame. No wall-clock sleep decides this.
	pumpFramesWait(t, product, "the run to stop", 15*time.Second, func() bool {
		session, ok := product.chat.Session(sessionID)
		return ok && !session.Running
	})
	// After a user stop the queue is paused, not drained: the count stays visible
	// both ways — the composer's ×N chip and the status strip's queue badge,
	// whose sentence is 已排队 N 条 (the paused strip badge owns the resume/cancel
	// actions). The UI itself never clears the backend inbox.
	pumpFramesWait(t, product, "the paused queue count", 10*time.Second, func() bool {
		return product.tester.HasText("×1") && product.tester.HasText("已排队 1 条")
	})
	if queued := queue.QueuedPrompts(sessionID); len(queued) != 1 || queued[0].Text != "停止后保留" {
		t.Fatalf("stop changed the backend inbox: %+v", queued)
	}
	if users := userTexts(inboxSession(t, product)); len(users) < 1 || users[0] != "运行中" {
		t.Fatalf("stopped run's user turn changed: %v", users)
	}
	for _, queued := range queue.QueuedPrompts(sessionID) {
		if err := queue.RemoveQueuedPrompt(t.Context(), sessionID, queued.ID); err != nil {
			t.Fatalf("cleanup inbox: %v", err)
		}
	}
	gate.Release()
}
