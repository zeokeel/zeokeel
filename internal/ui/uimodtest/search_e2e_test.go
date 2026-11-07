package uimodtest

// Full-text search end-to-end: a message sent through the agent is found by
// the sidebar's message search and the result navigates to its session.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMessageSearchEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"搜索回答内容\"},\"index\":0}]}\n\n")
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
	sessionID, err := chat.NewSession("搜索会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := chat.Send(sessionID, "搜索目标短语"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, chat, sessionID, 10*time.Second)

	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	field, ok := tester.Find("搜索会话")
	if !ok {
		t.Fatalf("sidebar search field missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(field.X+field.W/2, field.Y+field.H/2)
	tester.Frame()
	tester.Type("目标短语")
	tester.Frame()
	for _, want := range []string{"消息搜索", "搜索目标短语", "用户"} {
		if !tester.HasText(want) {
			t.Fatalf("search frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("搜索目标短语"); err != nil {
		t.Fatalf("click search hit: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("搜索回答内容") {
		t.Fatalf("search navigation did not open the session; texts = %v", tester.Texts())
	}
}
