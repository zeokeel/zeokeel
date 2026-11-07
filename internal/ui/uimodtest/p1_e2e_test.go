package uimodtest

// P1 end-to-end: durable sessions, approval dialog, tool cards and the
// trajectory view, driven through the real desktop assembly.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// loaderCloser is the part of the kernel module loader this test uses.
type loaderCloser interface{ Close() error }

func TestP1ApprovalToolsAndPersistence(t *testing.T) {
	dataDir := t.TempDir()
	server := scriptedToolServer(t)
	defer server.Close()
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}

	// --- first boot: send a prompt that requires tool approval -------------
	root, uiRoot, loader := bootDesktop(t, dataDir, settings)
	chat := requireChat(t, root)
	sessionID, err := chat.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := chat.Send(sessionID, "看看当前工作目录"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	approvals := requireApprovals(t, root)
	waitPending(t, approvals, 5*time.Second)
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if !tester.HasText("需要审批") {
		t.Fatalf("approval dialog missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("允许"); err != nil {
		t.Fatalf("click allow: %v; texts = %v", err, tester.Texts())
	}
	waitIdle(t, chat, sessionID, 10*time.Second)

	session, ok := chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionID)
	}
	if len(session.Messages) == 0 || session.Messages[len(session.Messages)-1].Text == "" {
		t.Fatalf("assistant did not finish: %+v", session.Messages)
	}
	foundTool := false
	for _, message := range session.Messages {
		for _, call := range message.Tools {
			if call.Name == "working_directory" && call.Status == "ok" {
				foundTool = true
			}
		}
	}
	if !foundTool {
		t.Fatalf("approved tool result missing from read model: %+v", session.Messages)
	}

	// Chat / trajectory frames show the tool timeline.
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester.Frame()
	if !tester.HasText("working_directory") {
		t.Fatalf("tool card text missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("轨迹"); err != nil {
		t.Fatalf("click trajectory tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"步骤 1", "working_directory", "完成"} {
		if !tester.HasText(want) {
			t.Fatalf("trajectory frame missing %q; texts = %v", want, tester.Texts())
		}
	}

	// --- restart: the session and its messages come back from SQLite ------
	title := session.Title
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("dispose root: %v", err)
	}

	root2, uiRoot2, loader2 := bootDesktop(t, dataDir, settings)
	defer func() {
		_ = loader2.Close()
		_ = root2.DisposeAll()
	}()
	chat2 := requireChat(t, root2)
	sessions := chat2.Sessions()
	if len(sessions) != 1 || sessions[0].ID != sessionID {
		t.Fatalf("restored sessions = %+v, want session %q", sessions, sessionID)
	}
	if sessions[0].Title != title {
		t.Fatalf("restored title = %q, want %q", sessions[0].Title, title)
	}
	restored, ok := chat2.Session(sessionID)
	if !ok || len(restored.Messages) == 0 {
		t.Fatalf("restored session messages = %+v (ok=%v)", restored, ok)
	}
	if !hasAssistantText(restored, "已读取工作目录") {
		t.Fatalf("restored assistant text missing: %+v", restored.Messages)
	}

	if err := uiRoot2.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session) after restart: %v", err)
	}
	tester2 := ui.NewTester(uiRoot2.View, 1280, 800)
	tester2.Frame()
	if !tester2.HasText("已读取工作目录") {
		t.Fatalf("restarted frame missing restored assistant text; texts = %v", tester2.Texts())
	}
}

// scriptedToolServer drives two model rounds: a working_directory tool call,
// then the final text after the tool result.
func scriptedToolServer(t *testing.T) *httptest.Server {
	t.Helper()
	var calls atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"working_directory\",\"arguments\":\"{}\"}}]},\"index\":0,\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"已读取工作目录\"},\"index\":0}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func bootDesktop(t *testing.T, dataDir string, settings *memSettings) (cordis.Context, *uiroot.Root, loaderCloser) {
	t.Helper()
	root := cordis.Root()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	provide(t, root, contracts.KeyDataDir, dataDir)
	provide(t, root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, root, contracts.KeyTheme, uiRoot.Theme())
	provide(t, root, contracts.KeyLocale, uiRoot.Locale())
	provide(t, root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, root, contracts.KeySettings, contracts.SettingsService(settings))
	provide(t, root, contracts.KeyLogger, contracts.Logger(discardLogger{}))
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		t.Fatalf("load desktop assembly: %v", err)
	}
	return root, uiRoot, loader
}

func requireChat(t *testing.T, root cordis.Context) contracts.ChatService {
	t.Helper()
	chat, err := root.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require chat: %v", err)
	}
	return chat
}

func requireApprovals(t *testing.T, root cordis.Context) contracts.Approvals {
	t.Helper()
	approvals, err := root.Require(contracts.KeyApprovals)
	if err != nil {
		t.Fatalf("require approvals: %v", err)
	}
	return approvals
}

func waitPending(t *testing.T, approvals contracts.Approvals, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pending := approvals.Pending(); len(pending) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no approval request appeared within %s", timeout)
}
