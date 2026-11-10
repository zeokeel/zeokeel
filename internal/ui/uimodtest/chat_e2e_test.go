package uimodtest

// End-to-end proof of the Phase-1 chat stack: the real desktop assembly (agent
// backend module + session/sidebar/settings UI modules + toolkit) streamed by a
// fake OpenAI-compatible server, rendered in the real single mygo frame.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func TestDesktopChatPipelineEndToEnd(t *testing.T) {
	server := fakeChatServer(t)
	defer server.Close()

	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	provide(t, root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, root, contracts.KeyTheme, uiRoot.Theme())
	provide(t, root, contracts.KeyLocale, uiRoot.Locale())
	provide(t, root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	provide(t, root, contracts.KeySettings, contracts.SettingsService(&memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}))
	provide(t, root, contracts.KeyLogger, contracts.Logger(discardLogger{}))

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(desktop): %v", err)
	}
	defer func() { _ = loader.Close() }()

	chat, err := root.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require chat: %v", err)
	}
	sessionID, err := chat.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := chat.Send(sessionID, "你好"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, chat, sessionID, 5*time.Second)

	session, ok := chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionID)
	}
	if !hasAssistantText(session, "我是 ZeoKeel") {
		t.Fatalf("assistant text missing; messages = %+v", session.Messages)
	}

	// The product lands on the session screen (the DSH shell): the active
	// conversation's streamed reply is rendered and the session composer is
	// the input the user gets, above the brand content header.
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	for _, want := range []string{"ZeoKeel", "智能体团队", "对话", "轨迹", "记忆", "输入消息…", "发送"} {
		if !tester.HasText(want) {
			t.Fatalf("session landing frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("问点什么…") {
		t.Fatalf("the old home composer is still the landing; texts = %v", tester.Texts())
	}
	if !frameUntil(tester, "我是 ZeoKeel", 3*time.Second) {
		t.Fatalf("landing frame does not render the streamed reply; texts = %v", tester.Texts())
	}

	// The real product frame: side bar + chat screen + settings page.
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester.Frame()
	for _, want := range []string{"对话", "轨迹", "记忆", "我是 ZeoKeel", "发送"} {
		if !tester.HasText(want) {
			t.Fatalf("session frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("还没有消息，开始一段新对话吧。") {
		t.Fatalf("session frame still shows the empty state after a message")
	}

	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	if err := tester.Click("模型"); err != nil {
		t.Fatalf("click model settings page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"模型列表", "添加模型", "系统提示词", "保存"} {
		if !tester.HasText(want) {
			t.Fatalf("model settings page does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("快速预设") || tester.HasText("连接检查") {
		t.Fatalf("model settings page still shows the removed preset/connection sections")
	}
	if err := tester.Click("外观"); err != nil {
		t.Fatalf("click appearance page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"外观模式", "浅色", "深色", "跟随系统", "界面语言", "应用"} {
		if !tester.HasText(want) {
			t.Fatalf("appearance page does not show %q; texts = %v", want, tester.Texts())
		}
	}
	uiRoot.Navigator().CloseSettings()
}

func fakeChatServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"，我是 ZeoKeel。\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func waitIdle(t *testing.T, chat contracts.ChatService, sessionID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		session, ok := chat.Session(sessionID)
		if ok && !session.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := chat.Session(sessionID)
	t.Fatalf("session %q did not finish within %s: %+v", sessionID, timeout, session)
}

func hasAssistantText(session contracts.ChatSession, want string) bool {
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleAssistant && contains(message.Text, want) {
			return true
		}
	}
	return false
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
