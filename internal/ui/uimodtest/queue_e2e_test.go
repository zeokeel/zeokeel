package uimodtest

// Real-backend prompt inbox E2E: the agent module's PromptQueue owns the
// per-session queue (the UI only reads/removes it). A prompt queued from the
// composer while a run streams shows in the badge/popover and is drained by the
// backend when the run settles; a user stop keeps the inbox.

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

// slowInboxServer streams the first answer slowly so a prompt can be queued
// while the run is in flight; later turns answer immediately.
func slowInboxServer(t *testing.T) *httptest.Server {
	t.Helper()
	var calls atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"先处理第一条\"}}]}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(600 * time.Millisecond)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"，完成。\"}}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"排队消息已处理\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// newInboxProduct boots the desktop product against the slow server and opens a
// session. It returns the product pieces plus the PromptQueue half.
func newInboxProduct(t *testing.T) (*interactionProduct, contracts.PromptQueue) {
	t.Helper()
	server := slowInboxServer(t)
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
	return product, queue
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
// composer while the first run streams, see the badge/popover, and let the
// backend drain the inbox after the run settles (the UI sends nothing itself).
func TestPromptQueueRealBackendE2E(t *testing.T) {
	product, queue := newInboxProduct(t)
	sessionID := product.chat.ActiveSession()
	if err := product.chat.Send(sessionID, "第一条运行"); err != nil {
		t.Fatalf("Send(first): %v", err)
	}
	pumpFramesWait(t, product, "the first run to start", 10*time.Second, func() bool {
		session, ok := product.chat.Session(sessionID)
		return ok && session.Running
	})

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
	product.tester.Frame()
	if !product.tester.HasText("已排队 1 条") {
		t.Fatalf("queue badge missing; texts = %v", product.tester.Texts())
	}
	if queued := queue.QueuedPrompts(sessionID); len(queued) != 1 || queued[0].Text != "第二条排队" {
		t.Fatalf("backend inbox = %+v, want the typed prompt", queued)
	}

	// The backend drains the inbox when the first run settles: the queued prompt
	// becomes its own user turn and the badge disappears.
	pumpFramesWait(t, product, "the inbox drain", 20*time.Second, func() bool {
		session, ok := product.chat.Session(sessionID)
		if !ok || session.Running {
			return false
		}
		users := userTexts(session)
		return len(queue.QueuedPrompts(sessionID)) == 0 && len(users) >= 2 && users[1] == "第二条排队"
	})
	product.tester.Frame()
	if product.tester.HasText("已排队") {
		t.Fatalf("queue badge survived the backend drain; texts = %v", product.tester.Texts())
	}
	if !product.tester.HasText("排队消息已处理") {
		t.Fatalf("drained turn's answer missing; texts = %v", product.tester.Texts())
	}
}

// TestPromptQueueStopKeepsInboxE2E: a user stop cancels the run and the backend
// keeps the queued prompt; the badge stays visible (the UI never clears it).
func TestPromptQueueStopKeepsInboxE2E(t *testing.T) {
	product, queue := newInboxProduct(t)
	sessionID := product.chat.ActiveSession()
	if err := product.chat.Send(sessionID, "运行中"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	pumpFramesWait(t, product, "the run to start", 10*time.Second, func() bool {
		session, ok := product.chat.Session(sessionID)
		return ok && session.Running
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
	product.tester.Frame()
	if err := product.tester.Click("停止"); err != nil {
		t.Fatalf("click 停止: %v", err)
	}
	product.tester.Frame()
	// The backend keeps the inbox: no automatic drain after a user stop.
	time.Sleep(700 * time.Millisecond)
	product.tester.Frame()
	if queued := queue.QueuedPrompts(sessionID); len(queued) != 1 || queued[0].Text != "停止后保留" {
		t.Fatalf("stop changed the backend inbox: %+v", queued)
	}
	if !product.tester.HasText("已排队 1 条") {
		t.Fatalf("queue badge disappeared after the stop; texts = %v", product.tester.Texts())
	}
	if users := userTexts(inboxSession(t, product)); len(users) < 1 || users[0] != "运行中" {
		t.Fatalf("stopped run's user turn changed: %v", users)
	}
	for _, queued := range queue.QueuedPrompts(sessionID) {
		if err := queue.RemoveQueuedPrompt(t.Context(), sessionID, queued.ID); err != nil {
			t.Fatalf("cleanup inbox: %v", err)
		}
	}
}
