package uimodtest

// Real-backend Context tab E2E: after one real scripted turn the agent's
// ContextInspector has a snapshot and a model call folded; the new tab renders
// them, and a conversation with no recorded data shows the honest empty state.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// contextUsageServer streams one answer with provider usage, so the agent folds
// a model_call event (and therefore a request row with real token counts).
func contextUsageServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"发布检查完成\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1200,\"completion_tokens\":30,\"total_tokens\":1230,\"prompt_cache_hit_tokens\":100}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestContextTabRealBackendE2E(t *testing.T) {
	server := contextUsageServer(t)
	t.Cleanup(server.Close)
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:         "test-model",
		contracts.SettingsChatBaseURL:       server.URL,
		contracts.SettingModelCatalogModels: `[{"id":"test-model","name":"test-model","baseURL":"` + server.URL + `","contextWindow":128000}]`,
	}}
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	t.Cleanup(func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	})
	chat := requireChat(t, root)
	inspector := requireService(t, root, contracts.KeyContext)
	sessionID, err := chat.NewSession("上下文会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := chat.Send(sessionID, "跑一下发布检查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, chat, sessionID, 10*time.Second)

	// The backend folds the run's snapshot/model-call events for the session.
	deadline := time.Now().Add(5 * time.Second)
	var data contracts.SessionContext
	ok := false
	for time.Now().Before(deadline) {
		if data, ok = inspector.SessionContext(sessionID); ok &&
			!data.Snapshot.At.IsZero() && len(data.Requests) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || data.Snapshot.At.IsZero() || len(data.Requests) == 0 {
		t.Fatalf("inspector data after one run = %+v (ok %v), want snapshot + requests", data, ok)
	}
	if data.Snapshot.Window <= 0 {
		t.Fatalf("snapshot window = %d, want the catalog context window", data.Snapshot.Window)
	}

	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleTab, "上下文"); !ok {
		t.Fatalf("Context tab missing with the real inspector; texts = %v", tester.Texts())
	}
	if err := tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"概览", "上下文用量", "最近一次请求",
		"系统提示分段", "身份", "分段合计", "模型调用", "轮次"} {
		if !tester.HasText(want) {
			t.Fatalf("context pane missing %q; texts = %v", want, tester.Texts())
		}
	}
	// This product has no model pricing configured, so the cost total must be
	// hidden instead of rendered as $0.
	if tester.HasText("累计成本") {
		t.Fatalf("unpriced session rendered a cost total; texts = %v", tester.Texts())
	}

	// A fresh conversation with no recorded context data shows the empty state.
	emptyID, err := chat.NewSession("空会话")
	if err != nil {
		t.Fatalf("NewSession(empty): %v", err)
	}
	if err := chat.SetActiveSession(emptyID); err != nil {
		t.Fatalf("activate empty session: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("本会话还没有上下文数据") {
		t.Fatalf("empty session did not show the context empty state; texts = %v", tester.Texts())
	}
}
