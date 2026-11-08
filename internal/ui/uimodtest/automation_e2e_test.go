package uimodtest

// Automation end-to-end: a persisted scheduled task runs immediately through
// RunNow (creating a new chat session), and the toolkit's schedule_* tools use
// the same persistent scheduler.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestAutomationTasksEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"自动化回答\"},\"index\":0}]}\n\n")
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
	automation, err := root.Require(contracts.KeyAutomation)
	if err != nil {
		t.Fatalf("require automation: %v", err)
	}
	task, err := automation.Create(context.Background(), contracts.AutomationTask{
		Name: "每日摘要", Prompt: "总结今天的变更", Schedule: "every 60s", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.ID == "" || task.NextRun.IsZero() {
		t.Fatalf("created task = %+v, want ID and NextRun", task)
	}
	if err := automation.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	// The run creates a new chat session in the background.
	var sessionID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, info := range chat.Sessions() {
			if strings.HasPrefix(info.Title, "自动化: ") {
				sessionID = info.ID
			}
		}
		if sessionID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sessionID == "" {
		t.Fatalf("automation run did not create a session: %+v", chat.Sessions())
	}
	waitIdle(t, chat, sessionID, 10*time.Second)
	session, ok := chat.Session(sessionID)
	if !ok || len(session.Messages) < 2 || !hasAssistantText(session, "自动化回答") {
		t.Fatalf("automation session = %+v (ok=%v)", session, ok)
	}

	// The toolkit's schedule_* tools are backed by the persistent scheduler.
	registry, ok := root.Get(contracts.KeyToolkit)
	if !ok {
		t.Fatal("toolkit registry missing")
	}
	if _, err := registry.Invoke(context.Background(), rig.ToolCall{
		Name: "schedule_create", Arguments: `{"name":"工具任务","prompt":"工具提示词","every_seconds":60}`,
	}); err != nil {
		t.Fatalf("schedule_create: %v", err)
	}
	tasks, err := automation.List(context.Background())
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks after schedule_create = %+v (err %v)", tasks, err)
	}
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "schedule_list", Arguments: `{}`})
	if err != nil {
		t.Fatalf("schedule_list: %v", err)
	}
	if !strings.Contains(result.Content, "工具任务") {
		t.Fatalf("schedule_list = %q, want the toolkit task", result.Content)
	}

	// The Automation screen renders the tasks and navigation item.
	if err := uiRoot.Navigator().Push("automation"); err != nil {
		t.Fatalf("Push(automation): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	// H11: schedules render human-readable, and cards expose Edit.
	for _, want := range []string{"自动化", "每日摘要", "工具任务", "每 1 分钟", "立即运行", "编辑", "删除"} {
		if !tester.HasText(want) {
			t.Fatalf("automation frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("every 60s") {
		t.Fatalf("automation frame still shows the raw schedule; texts = %v", tester.Texts())
	}
}
