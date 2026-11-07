package uimodtest

// P1 end-to-end: durable sessions, approval dialog, tool cards and the
// trajectory view, driven through the real desktop assembly.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	rig "github.com/zeokeel/zeokeel-rig"
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

	// The window-level right inspector opens from the content header: the Log
	// pane reads the durable session events the agent persisted.
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	for _, want := range []string{"概览", "工具", "模型", "日志"} {
		if !tester.HasText(want) {
			t.Fatalf("right panel missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("日志"); err != nil {
		t.Fatalf("click log tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"会话日志", "user_message", "run_end"} {
		if !tester.HasText(want) {
			t.Fatalf("session log missing %q; texts = %v", want, tester.Texts())
		}
	}
	uiRoot.SetRightPanelOpen(false)

	// The workspace backend is wired too: register a temp folder and inspect it
	// through the docked Files tab (directory listing + text preview).
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte("# ZeoKeel 工作区\n\n示例文件。"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o700); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	skillDir := filepath.Join(workspaceRoot, "skills", "release-checklist")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	skillBody := "---\nname: release-checklist\ndescription: 发布前检查清单\n---\n# 发布检查\n\n- 跑测试\n- 更新 changelog\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	workspaces, err := root.Require(contracts.KeyWorkspace)
	if err != nil {
		t.Fatalf("require workspace: %v", err)
	}
	workspace, err := workspaces.Create(context.Background(), "demo", workspaceRoot)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// A session created while the workspace is current carries the association
	// and appears nested under the workspace in the sidebar tree.
	workspaceSessionID, err := chat.NewSession("工作区会话")
	if err != nil {
		t.Fatalf("NewSession in workspace: %v", err)
	}
	associated := false
	for _, info := range chat.Sessions() {
		if info.ID == workspaceSessionID && info.WorkspaceID == workspace.ID {
			associated = true
		}
	}
	if !associated {
		t.Fatalf("workspace session is not associated: %+v", chat.Sessions())
	}
	tester.Frame()
	for _, want := range []string{"工作区", "demo", "工作区会话"} {
		if !tester.HasText(want) {
			t.Fatalf("workspace tree missing %q; texts = %v", want, tester.Texts())
		}
	}
	// Keep the original conversation active for the remaining assertions.
	if err := chat.SetActiveSession(sessionID); err != nil {
		t.Fatalf("reactivate original session: %v", err)
	}
	tester.Frame()
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	if err := tester.Click("文件"); err != nil {
		t.Fatalf("click files tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"README.md", "src"} {
		if !tester.HasText(want) {
			t.Fatalf("files tab missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("README.md"); err != nil {
		t.Fatalf("click README: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("示例文件") {
		t.Fatalf("file preview missing; texts = %v", tester.Texts())
	}

	// The terminal tab runs real commands in the workspace root.
	if err := tester.Click("终端"); err != nil {
		t.Fatalf("click terminal tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if runRect, ok := tester.Find("运行"); ok {
		tester.ClickAt(runRect.X-140, runRect.Y+runRect.H/2)
		tester.Frame()
		tester.Type("echo zeokeel-terminal")
		if err := tester.Click("运行"); err != nil {
			t.Fatalf("run terminal command: %v; texts = %v", err, tester.Texts())
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			tester.Frame()
			if tester.HasText("zeokeel-terminal") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !tester.HasText("zeokeel-terminal") {
			t.Fatalf("terminal output missing; texts = %v", tester.Texts())
		}
	}
	uiRoot.SetRightPanelOpen(false)
	if err := tester.Click("对话"); err != nil {
		t.Fatalf("click chat tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()

	// The composer's @-mention menu lists workspace files; choosing one writes
	// the reference into the draft and the prompt carries it to the agent.
	if sendRect, ok := tester.Find("发送"); ok {
		tester.ClickAt(sendRect.X-120, sendRect.Y-50)
		tester.Frame()
		tester.Type("@REA")
		tester.Frame()
		if !tester.HasText("/README.md") {
			t.Fatalf("mention menu missing workspace file; texts = %v", tester.Texts())
		}
		if err := tester.Click("/README.md"); err != nil {
			t.Fatalf("click mention: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if err := tester.Click("发送"); err != nil {
			t.Fatalf("send mention: %v; texts = %v", err, tester.Texts())
		}
		waitIdle(t, chat, sessionID, 10*time.Second)
		tester.Frame()
		if !tester.HasText("@README.md") {
			t.Fatalf("sent prompt does not show the mention; texts = %v", tester.Texts())
		}
	}

	// Skills are discovered from the workspace and exposed both to the settings
	// skills page and to the agent's skill tool through the toolkit.
	skillsService, err := root.Require(contracts.KeySkills)
	if err != nil {
		t.Fatalf("require skills: %v", err)
	}
	skills, err := skillsService.List(context.Background())
	if err != nil || len(skills) != 1 || skills[0].Name != "release-checklist" {
		t.Fatalf("skills = %+v (err %v), want release-checklist", skills, err)
	}
	registry, ok := root.Get(contracts.KeyToolkit)
	if !ok {
		t.Fatal("toolkit registry missing")
	}
	result, err := registry.Invoke(context.Background(), rig.ToolCall{
		Name: "skill", Arguments: `{"action":"list"}`,
	})
	if err != nil {
		t.Fatalf("skill tool list: %v", err)
	}
	skillList := result.Content
	if !containsText(skillList, "release-checklist") {
		t.Fatalf("skill tool list = %q, want release-checklist", skillList)
	}
	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	if err := tester.Click("技能"); err != nil {
		t.Fatalf("click skills page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"release-checklist", "发布前检查清单"} {
		if !tester.HasText(want) {
			t.Fatalf("skills page missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("查看内容"); err != nil {
		t.Fatalf("expand skill: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("发布检查") {
		t.Fatalf("skill body missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("诊断"); err != nil {
		t.Fatalf("click diagnostics page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"诊断", "工具数", "会话数", "zeokeel.db"} {
		if !tester.HasText(want) {
			t.Fatalf("diagnostics page missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("更新"); err != nil {
		t.Fatalf("click update page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("检查更新") {
		t.Fatalf("update page missing the check button; texts = %v", tester.Texts())
	}
	if err := tester.Click("检查更新"); err != nil {
		t.Fatalf("click check update: %v; texts = %v", err, tester.Texts())
	}
	checkDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(checkDeadline) {
		tester.Frame()
		if tester.HasText("更新源未配置") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !tester.HasText("更新源未配置") {
		t.Fatalf("update check error missing; texts = %v", tester.Texts())
	}
	uiRoot.Navigator().CloseSettings()
	tester.Frame()

	// The durable memory backend is wired into the product: entries persist and
	// the session Memory tab renders them.
	memory, err := root.Require(contracts.KeyMemory)
	if err != nil {
		t.Fatalf("require memory: %v", err)
	}
	if _, err := memory.Add(context.Background(), contracts.Memory{Text: "记住：用中文回复", Scope: "global"}); err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if err := tester.Click("记忆"); err != nil {
		t.Fatalf("click memory tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("记住：用中文回复") {
		t.Fatalf("memory tab does not show the entry; texts = %v", tester.Texts())
	}

	// The chrome shortcuts are configurable through the host settings: an
	// override applies on the next frame, and restoring the default switches
	// the binding back.
	if err := settings.Set(contracts.SettingsUIShortcutToggleSidebar, "alt+b"); err != nil {
		t.Fatalf("set shortcut setting: %v", err)
	}
	tester.Frame()
	tester.Key(ui.Alt, ui.KeyB)
	tester.Frame()
	if !uiRoot.SidebarCollapsed() {
		t.Fatal("Alt+B did not collapse the sidebar after the override")
	}
	if err := settings.Set(contracts.SettingsUIShortcutToggleSidebar, contracts.DefaultShortcutToggleSidebar); err != nil {
		t.Fatalf("reset shortcut setting: %v", err)
	}
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyB)
	tester.Frame()
	if uiRoot.SidebarCollapsed() {
		t.Fatal("Cmd+B did not expand the sidebar after restoring the default")
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
	var restoredInfo *contracts.ChatSessionInfo
	for index := range sessions {
		if sessions[index].ID == sessionID {
			restoredInfo = &sessions[index]
		}
	}
	if restoredInfo == nil {
		t.Fatalf("restored sessions = %+v, want session %q", sessions, sessionID)
	}
	if restoredInfo.Title != title {
		t.Fatalf("restored title = %q, want %q", restoredInfo.Title, title)
	}
	if err := chat2.SetActiveSession(sessionID); err != nil {
		t.Fatalf("reactivate restored session: %v", err)
	}
	restored, ok := chat2.Session(sessionID)
	if !ok || len(restored.Messages) == 0 {
		t.Fatalf("restored session messages = %+v (ok=%v)", restored, ok)
	}
	if !hasAssistantText(restored, "已读取工作目录") {
		t.Fatalf("restored assistant text missing: %+v", restored.Messages)
	}
	memory2, err := root2.Require(contracts.KeyMemory)
	if err != nil {
		t.Fatalf("require memory after restart: %v", err)
	}
	entries, err := memory2.List(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Text != "记住：用中文回复" {
		t.Fatalf("restored memories = %+v (err %v)", entries, err)
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
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Settings: settings})
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
