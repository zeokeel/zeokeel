package uimodtest

// End-to-end proof for P2-4: a message hit in the sidebar's full-text search
// opens its conversation at the matched message. The store emits the message's
// durable locator, the sidebar hands it to the session module's optional
// contracts.SessionFocus, and the conversation transcript scrolls to that
// message's row instead of the conversation's followed end.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMessageSearchHitOpensAtMatchedMessageEndToEnd(t *testing.T) {
	var rounds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		round := rounds.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"第 %d 轮回答\"},\"index\":0}]}\n\n", round)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	defer func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}()
	chat := requireChat(t, root)
	sessionID, err := chat.NewSession("搜索定位会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Enough rounds that the transcript overflows its viewport: opening the
	// conversation at the followed end hides the first message, which is the
	// message the search hit points at.
	const turns = 14
	for index := 1; index <= turns; index++ {
		prompt := fmt.Sprintf("第 %d 轮问题", index)
		if index == 1 {
			prompt = "定位锚点开头的问题"
		}
		if err := chat.Send(sessionID, prompt); err != nil {
			t.Fatalf("Send turn %d: %v", index, err)
		}
		waitIdle(t, chat, sessionID, 10*time.Second)
	}

	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if !tester.HasText(fmt.Sprintf("第 %d 轮回答", turns)) {
		t.Fatalf("the transcript does not follow its end before the search; texts = %v", tester.Texts())
	}
	if tester.HasText("第 1 轮回答") {
		t.Fatalf("the first turn is already in view; the transcript viewport is not overflowing")
	}
	field, ok := tester.Find("搜索会话与消息")
	if !ok {
		t.Fatalf("sidebar search field missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(field.X+field.W/2, field.Y+field.H/2)
	tester.Frame()
	tester.Type("定位锚点开头")
	tester.Frame()
	waitSearchTexts(t, tester, 5*time.Second, "消息搜索", "定位锚点开头的问题")
	if err := tester.Click("定位锚点开头的问题"); err != nil {
		t.Fatalf("click search hit: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()

	// The transcript scrolled to the matched first message: its turn's answer is
	// in view while the followed end (the last answer) is not. The sidebar hit
	// snippet still shows the query, so the proof uses transcript-only copy.
	if !tester.HasText("第 1 轮回答") {
		t.Fatalf("the opened conversation did not scroll to the matched first message; texts = %v", tester.Texts())
	}
	if tester.HasText(fmt.Sprintf("第 %d 轮回答", turns)) {
		t.Fatalf("the conversation opened at its followed end instead of the matched message; texts = %v", tester.Texts())
	}
}
