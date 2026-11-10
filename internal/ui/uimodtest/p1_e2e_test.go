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
	"strings"
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
		// The product ships no built-in model list: seed the two models this
		// scenario switches between (test-model is the default row).
		contracts.SettingModelCatalogModels: `[` +
			`{"id":"test-model","name":"test-model","baseURL":"` + server.URL + `"},` +
			`{"id":"deepseek-chat","name":"DeepSeek Chat","note":"通用对话","baseURL":"` + server.URL + `"}]`,
		// This scenario covers the tool-approval handshake end to end: the
		// read-only preset auto-allows read-only/informational tools but still
		// sends mutating tools (here a shared task-board write) through the
		// approval dialog.
		contracts.SettingAgentPermission: contracts.PermissionReadOnly,
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
			if call.Name == "team_task_create" && call.Status == "ok" {
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
	// The default (standard) work-steps mode folds a finished turn's process
	// into one summary row: expand it to read the tool row.
	if !tester.HasText("团队任务") {
		for _, label := range tester.Texts() {
			if strings.HasPrefix(label, "过程 ·") {
				if err := tester.Click(label); err != nil {
					t.Fatalf("expand process summary %q: %v; texts = %v", label, err, tester.Texts())
				}
				tester.Frame()
				break
			}
		}
	}
	// The transcript shows the friendly localized tool name; the raw protocol id
	// stays in the row's tooltip/accessible description.
	if !tester.HasText("团队任务") {
		t.Fatalf("tool row text missing; texts = %v", tester.Texts())
	}
	if tester.HasText("team_task_create") {
		t.Fatalf("raw tool id leaked into the transcript; texts = %v", tester.Texts())
	}
	if err := tester.Click("轨迹"); err != nil {
		t.Fatalf("click trajectory tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"步骤 1", "团队任务", "完成"} {
		if !tester.HasText(want) {
			t.Fatalf("trajectory frame missing %q; texts = %v", want, tester.Texts())
		}
	}

	// The window-level right sidebar opens from the content header: it lands
	// on the 开始 guide (the retired overview/log panes are gone). The durable
	// session events the old Log pane read are still persisted; assert them
	// through the same store the pane read.
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	requireRightSidebarGuide(t, tester, "right sidebar")
	store, err := root.Require(contracts.KeyStore)
	if err != nil {
		t.Fatalf("require store: %v", err)
	}
	events, err := store.Last(context.Background(), sessionID, 50)
	if err != nil {
		t.Fatalf("store.Last(%q): %v", sessionID, err)
	}
	eventTypes := make(map[string]bool, len(events))
	for _, event := range events {
		eventTypes[event.Type] = true
	}
	for _, want := range []string{"user_message", "run_end"} {
		if !eventTypes[want] {
			t.Fatalf("durable session log missing %q; events = %+v", want, events)
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
	clickRightSidebarPage(t, tester, "文件")
	// The tree reads the workspace off the UI thread: wait for the listing.
	filesDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(filesDeadline) && !tester.HasText("README.md") {
		tester.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"README.md", "src"} {
		if !tester.HasText(want) {
			t.Fatalf("files page missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("README.md"); err != nil {
		t.Fatalf("click README: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	// The file preview reads the file off the UI thread.
	if !frameUntil(tester, "示例文件", 3*time.Second) {
		t.Fatalf("file preview missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("关闭 README.md") {
		t.Fatalf("opened file is not its own tab; texts = %v", tester.Texts())
	}

	// The 终端 page runs real commands in the workspace root.
	clickRightSidebarPage(t, tester, "命令")
	if !frameUntil(tester, "运行", 3*time.Second) {
		t.Fatalf("terminal page missing its run control; texts = %v", tester.Texts())
	}
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
	// The official drawer is a modal overlay: the frame must rebuild after
	// closing it before the next click can reach the content behind it.
	tester.Frame()
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
		if !frameUntil(tester, "/README.md", 2*time.Second) {
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
	if err := uiRoot.OpenSettingsPage("skills"); err != nil {
		t.Fatalf("open skills page: %v", err)
	}
	tester.Frame()
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
	if err := uiRoot.OpenSettingsPage("diagnostics"); err != nil {
		t.Fatalf("open diagnostics page: %v", err)
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"诊断", "工具数", "会话数", "zeokeel.db"} {
		if !tester.HasText(want) {
			t.Fatalf("diagnostics page missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := uiRoot.OpenSettingsPage("update"); err != nil {
		t.Fatalf("open update page: %v", err)
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"检查更新", "当前版本"} {
		if !tester.HasText(want) {
			t.Fatalf("update page missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The desktop assembly configures the GitHub Releases feed, so the page
	// offers the check action and never claims updates are disabled. The
	// network call itself belongs to the update module's tests; the page smoke
	// here stays offline and deterministic.
	if tester.HasText("更新服务未启用") {
		t.Fatalf("update page reports a disabled service although the assembly configures GitHub Releases; texts = %v", tester.Texts())
	}
	if err := uiRoot.OpenSettingsPage("search"); err != nil {
		t.Fatalf("open search settings: %v", err)
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"搜索", "搜索端点", "API Key", "保存"} {
		if !tester.HasText(want) {
			t.Fatalf("search settings missing %q; texts = %v", want, tester.Texts())
		}
	}
	if rect, ok := tester.Find("https://search.example.com/search"); ok {
		tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
		tester.Type("https://search.example.com/search")
		tester.Frame()
		if err := tester.Click("保存"); err != nil {
			t.Fatalf("save search settings: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if got := settings.Get(contracts.SettingWebSearchEndpoint, ""); got != "https://search.example.com/search" {
			t.Fatalf("search endpoint = %q", got)
		}
	}
	if err := uiRoot.OpenSettingsPage("mcp"); err != nil {
		t.Fatalf("open MCP settings: %v", err)
	}
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"MCP", "添加", "还没有配置 MCP 服务器"} {
		if !tester.HasText(want) {
			t.Fatalf("MCP settings page missing %q; texts = %v", want, tester.Texts())
		}
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
	// H25: the tab label is the only 「记忆」 title; the body no longer repeats
	// it as a big heading.
	memoryLabels := 0
	for _, text := range tester.Texts() {
		if text == "记忆" {
			memoryLabels++
		}
	}
	if memoryLabels > 2 {
		t.Fatalf("memory tab repeats its body title (%d exact 「记忆」labels); texts = %v",
			memoryLabels, tester.Texts())
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

	// A native window drop stages the same composer attachments as the picker.
	dropPath := filepath.Join(t.TempDir(), "dropped-note.txt")
	if err := os.WriteFile(dropPath, []byte("dropped content"), 0o600); err != nil {
		t.Fatalf("write drop file: %v", err)
	}
	drop, err := root.Require(contracts.KeyFileDrop)
	if err != nil {
		t.Fatalf("require file drop: %v", err)
	}
	drop.Dispatch([]string{dropPath})
	tester.Frame()
	tester.Frame()
	if !tester.HasText("dropped-note.txt") {
		t.Fatalf("dropped attachment chip missing; texts = %v", tester.Texts())
	}

	// Slash commands: the composer menus list the session command surface and
	// choosing one runs it (打开设置 opens the settings overlay).
	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(sendRect.X-120, sendRect.Y-50)
	tester.Frame()
	tester.Type("/")
	tester.Frame()
	for _, want := range []string{"新会话", "创建副本", "导出对话", "打开设置"} {
		if !tester.HasText(want) {
			t.Fatalf("slash menu missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("打开设置"); err != nil {
		t.Fatalf("run slash command: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("模型") && !tester.HasText("Model") {
		t.Fatalf("slash command did not open settings; texts = %v", tester.Texts())
	}
	uiRoot.Navigator().CloseSettings()
	tester.Frame()

	// Composer model menu: the model list lives in the compact chip menu's
	// 模型 submenu (the first level only has the 模型/推理等级 entries).
	if err := tester.Click("test-model"); err != nil {
		t.Fatalf("open model menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("模型"); err != nil {
		t.Fatalf("open model submenu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("DeepSeek Chat") || !tester.HasText("通用对话") {
		t.Fatalf("model catalog missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("DeepSeek Chat"); err != nil {
		t.Fatalf("choose catalog model: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	activeID := chat.ActiveSession()
	modelSession, ok := chat.Session(activeID)
	if !ok || modelSession.Model != "deepseek-chat" {
		t.Fatalf("session model = %q (ok %v), want deepseek-chat", modelSession.Model, ok)
	}

	// Language switching is live: the session screen and the sidebar re-render
	// in English on the next frame and back to Chinese.
	locale, err := root.Require(contracts.KeyLocale)
	if err != nil {
		t.Fatalf("require locale: %v", err)
	}
	locale.SetLanguage("en")
	tester.Frame()
	for _, want := range []string{"New chat", "Search chats and messages", "Settings", "Chat", "Ask ZeoKeel…", "Send"} {
		if !tester.HasText(want) {
			t.Fatalf("english UI missing %q; texts = %v", want, tester.Texts())
		}
	}
	// Host chrome (command palette) follows the locale too.
	tester.Key(ui.Cmd, ui.KeyK)
	tester.Frame()
	for _, want := range []string{"Command palette", "Search commands", "Esc to close"} {
		if !tester.HasText(want) {
			t.Fatalf("english command palette missing %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	// Settings pages follow the locale too. The grouped navigation is longer
	// than the dialog, so the about page is opened through the host's deep
	// link instead of clicking a nav item that may sit below the fold.
	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	for _, want := range []string{"Settings", "Model", "Appearance", "Shortcuts"} {
		if !tester.HasText(want) {
			t.Fatalf("english settings missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The low-frequency group is folded while a daily page shows; open it before
	// looking for its pages (the group follows the selected page's section).
	if err := tester.Click("Advanced"); err != nil {
		t.Fatalf("expand Advanced: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"Diagnostics", "Update", "Import"} {
		if !tester.HasText(want) {
			t.Fatalf("english settings advanced pages missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := uiRoot.OpenSettingsPage("about"); err != nil {
		t.Fatalf("open english about page: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("About ZeoKeel") || !tester.HasText("Build information") {
		t.Fatalf("english about page missing; texts = %v", tester.Texts())
	}
	uiRoot.Navigator().CloseSettings()
	if err := uiRoot.Navigator().Push("automation"); err != nil {
		t.Fatalf("Push(automation): %v", err)
	}
	tester.Frame()
	for _, want := range []string{"Automation", "New task", "No automation tasks yet"} {
		if !tester.HasText(want) {
			t.Fatalf("english automation page missing %q; texts = %v", want, tester.Texts())
		}
	}
	locale.SetLanguage("zh")
	tester.Frame()
	if !tester.HasText("新对话") {
		t.Fatalf("chinese UI did not come back; texts = %v", tester.Texts())
	}

	// ask_user reaches the real native dialog through the host bridge: the tool
	// parks until the user picks an option, then returns the answer.
	toolkit, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	answerCh := make(chan rig.ToolResult, 1)
	go func() {
		result, invokeErr := toolkit.Invoke(context.Background(), rig.ToolCall{
			ID: "ask-1", Name: "ask_user",
			Arguments: `{"question":"选择数据库","options":["sqlite","postgres"]}`,
		})
		if invokeErr != nil {
			result = rig.ToolResult{IsError: true, Content: invokeErr.Error()}
		}
		answerCh <- result
	}()
	askDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(askDeadline) {
		tester.Frame()
		if tester.HasText("选择数据库") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !tester.HasText("选择数据库") {
		t.Fatalf("ask_user dialog missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("sqlite"); err != nil {
		t.Fatalf("answer ask_user: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	select {
	case result := <-answerCh:
		if result.IsError || result.Content != "user answered: sqlite" {
			t.Fatalf("ask_user result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask_user tool did not return")
	}

	// present: the artifact preview overlay shows the agent's artifact.
	presentCh := make(chan rig.ToolResult, 1)
	go func() {
		result, invokeErr := toolkit.Invoke(context.Background(), rig.ToolCall{
			ID: "present-1", Name: "present",
			Arguments: `{"title":"检查报告","content":"# 结果\n\n全部通过。"}`,
		})
		if invokeErr != nil {
			result = rig.ToolResult{IsError: true, Content: invokeErr.Error()}
		}
		presentCh <- result
	}()
	presentDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(presentDeadline) {
		tester.Frame()
		if tester.HasText("检查报告") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !tester.HasText("检查报告") {
		t.Fatalf("artifact overlay missing; texts = %v", tester.Texts())
	}
	select {
	case result := <-presentCh:
		if result.IsError || !strings.Contains(result.Content, "artifact-") {
			t.Fatalf("present result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("present tool did not return")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close artifact overlay: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()

	// plan_review: the plan overlay approves through the host plan bridge.
	planCh := make(chan rig.ToolResult, 1)
	go func() {
		result, invokeErr := toolkit.Invoke(context.Background(), rig.ToolCall{
			ID: "plan-1", Name: "plan_review",
			Arguments: `{"plan":"# 发布计划\n1. 测试\n2. 发布"}`,
		})
		if invokeErr != nil {
			result = rig.ToolResult{IsError: true, Content: invokeErr.Error()}
		}
		planCh <- result
	}()
	planDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(planDeadline) {
		tester.Frame()
		if tester.HasText("计划待审阅") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !tester.HasText("计划待审阅") {
		t.Fatalf("plan overlay missing; texts = %v", tester.Texts())
	}
	<-planCh
	if err := tester.Click("批准"); err != nil {
		t.Fatalf("approve plan: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	planReviewer, err := root.Require(contracts.KeyPlanReviewer)
	if err != nil {
		t.Fatalf("require plan reviewer: %v", err)
	}
	if state := planReviewer.State(); state != "approved" {
		t.Fatalf("plan state = %q, want approved", state)
	}

	// goal: the agent goal tool surfaces as one chip in the session header
	// (the old flat row of four actions is gone); the destructive clear runs
	// through the chip's popover and a native confirmation dialog.
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session) before goal: %v", err)
	}
	tester.Frame()
	if _, err := toolkit.Invoke(context.Background(), rig.ToolCall{
		ID: "goal-1", Name: "goal", Arguments: `{"action":"set","text":"完成 Q4 发布清单"}`,
	}); err != nil {
		t.Fatalf("set goal: %v", err)
	}
	tester.Frame()
	goalTracker, err := root.Require(contracts.KeyGoalTracker)
	if err != nil {
		t.Fatalf("require goal tracker: %v", err)
	}
	if _, text, _, ok := goalTracker.Get(); !ok || text != "完成 Q4 发布清单" {
		t.Fatalf("goal tracker = %q (ok %v), want the set goal", text, ok)
	}
	if !tester.HasText("目标") {
		t.Fatalf("goal chip missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("目标操作"); err != nil {
		t.Fatalf("open goal chip menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("删除目标记录") {
		t.Fatalf("goal chip menu lacks the delete-record action; texts = %v", tester.Texts())
	}
	if err := tester.Click("删除目标记录"); err != nil {
		t.Fatalf("choose delete goal record: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("确认删除") {
		t.Fatalf("delete-goal-record confirmation missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatalf("confirm delete goal record: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if _, _, _, ok := goalTracker.Get(); ok {
		t.Fatalf("goal tracker still set after clear")
	}

	// subagent: the agent module registers the synchronous delegation tools and
	// a real nested rig runner produces the answer.
	subResult, subErr := toolkit.Invoke(context.Background(), rig.ToolCall{
		ID: "sub-1", Name: "subagent", Arguments: `{"prompt":"总结当前工作目录"}`,
	})
	if subErr != nil || subResult.IsError {
		t.Fatalf("subagent = %+v (err %v)", subResult, subErr)
	}
	if !strings.Contains(subResult.Content, "已读取工作目录") {
		t.Fatalf("subagent content = %q", subResult.Content)
	}

	// permission modes: full permission auto-allows tool calls without asking;
	// restoring read-only mode queues a shell request again.
	approvalsSvc := requireApprovals(t, root)
	if err := settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull); err != nil {
		t.Fatalf("set permission full: %v", err)
	}
	if mode := approvalsSvc.Mode(); mode != "auto" {
		t.Fatalf("mode with full permission = %q, want auto", mode)
	}
	permDecision, permErr := approvalsSvc.Request(context.Background(), contracts.ApprovalRequest{
		Tool: "bash", Summary: "permission e2e",
	})
	if permErr != nil || permDecision != contracts.ApprovalAllow {
		t.Fatalf("full permission decision = %v (err %v)", permDecision, permErr)
	}
	if pending := approvalsSvc.Pending(); len(pending) != 0 {
		t.Fatalf("full permission queued approvals: %+v", pending)
	}
	if err := settings.Set(contracts.SettingAgentPermission, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("restore read-only permission: %v", err)
	}

	// team orchestration: spawn_teammate creates a real in-process teammate and
	// the session header's 智能体团队 overlay lists it with its truthful run
	// state (the old docked Agents pane moved into the header overlay).
	teamSvc, err := root.Require(contracts.KeyTeam)
	if err != nil {
		t.Fatalf("require team service: %v", err)
	}
	spawnResult, spawnErr := toolkit.Invoke(context.Background(), rig.ToolCall{
		ID: "spawn-1", Name: "spawn_teammate",
		Arguments: `{"name":"研究员","prompt":"研究一下当前工作目录"}`,
	})
	if spawnErr != nil || spawnResult.IsError {
		t.Fatalf("spawn_teammate = %+v (err %v)", spawnResult, spawnErr)
	}
	var member contracts.TeamMember
	teamDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(teamDeadline) {
		for _, candidate := range teamSvc.Members() {
			if candidate.Name == "研究员" && candidate.Last != "" {
				member = candidate
			}
		}
		if member.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if member.ID == "" {
		t.Fatalf("teammate never produced a result: %+v", teamSvc.Members())
	}
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session) before team overlay: %v", err)
	}
	uiRoot.SetRightPanelOpen(false)
	tester.Frame()
	if err := tester.Click("智能体团队"); err != nil {
		t.Fatalf("open team overlay: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	// The run finished (the wait loop above required a result), so the member
	// card's truthful run state is 已完成, not the old availability-derived 运行中.
	for _, want := range []string{"研究员", "已完成", "成员", "共享任务"} {
		if !tester.HasText(want) {
			t.Fatalf("team overlay missing %q; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	// @session references + reasoning effort: the reference token persists in
	// the transcript while the model input expands the referenced transcript.
	refSource := chat.ActiveSession()
	refSession, err := chat.NewSession("引用测试")
	if err != nil {
		t.Fatalf("NewSession reference: %v", err)
	}
	refPrompt := "@session:" + refSource + " 请总结上面的会话"
	if err := chat.Send(refSession, refPrompt); err != nil {
		t.Fatalf("Send with session reference: %v", err)
	}
	waitIdle(t, chat, refSession, 5*time.Second)
	refRead, ok := chat.Session(refSession)
	if !ok || len(refRead.Messages) == 0 {
		t.Fatalf("reference session read model = %+v (ok %v)", refRead, ok)
	}
	lastUser := ""
	for _, message := range refRead.Messages {
		if message.Role == contracts.ChatRoleUser {
			lastUser = message.Text
		}
	}
	if lastUser != refPrompt {
		t.Fatalf("persisted user prompt = %q, want the raw reference token", lastUser)
	}
	if err := settings.Set(contracts.SessionEffortSettingKey(refSession), contracts.SessionEffortHigh); err != nil {
		t.Fatalf("set session effort: %v", err)
	}
	if err := chat.Send(refSession, "再总结一次"); err != nil {
		t.Fatalf("Send with high effort: %v", err)
	}
	waitIdle(t, chat, refSession, 5*time.Second)
	if refRead, ok := chat.Session(refSession); !ok || refRead.Running {
		t.Fatalf("high effort run did not finish: %+v", refRead)
	}

	// shared task board: creating a task through the service shows up in the
	// session header's 智能体团队 overlay (its 共享任务 section); the board
	// mutation itself goes through the TaskBoard service (the docked task pane
	// was removed with the 8-pane inspector) and the overlay re-reads it.
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	task, err := board.Create(context.Background(), "发布前检查", "确认文档与截图")
	if err != nil {
		t.Fatalf("TaskBoard.Create: %v", err)
	}
	tester.Frame()
	if err := tester.Click("智能体团队"); err != nil {
		t.Fatalf("open team overlay: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !frameUntil(tester, "发布前检查", 3*time.Second) {
		t.Fatalf("shared task missing from the team overlay; texts = %v", tester.Texts())
	}
	if !tester.HasText("待处理") {
		t.Fatalf("shared task status missing from the team overlay; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if _, err := board.SetStatus(context.Background(), task.ID, contracts.TaskStatusCompleted); err != nil {
		t.Fatalf("complete task through the board: %v", err)
	}
	if err := tester.Click("智能体团队"); err != nil {
		t.Fatalf("reopen team overlay: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !frameUntil(tester, "已完成", 3*time.Second) {
		t.Fatalf("completed task status missing from the team overlay; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tasks, err := board.List(context.Background())
	if err != nil {
		t.Fatalf("TaskBoard.List: %v", err)
	}
	completed := false
	for _, item := range tasks {
		if item.ID == task.ID && item.Status == contracts.TaskStatusCompleted {
			completed = true
		}
	}
	if !completed {
		t.Fatalf("task not completed: %+v", tasks)
	}

	// artifact history: the presented artifact is listed in the Files page's
	// 交付区 section (the old 产物 pane is gone) and can be previewed again
	// from there.
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	clickRightSidebarPage(t, tester, "文件")
	if !frameUntil(tester, "检查报告", 3*time.Second) {
		t.Fatalf("artifact history missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("交付区") {
		t.Fatalf("deliverables section heading missing; texts = %v", tester.Texts())
	}
	// A top-right notification can overlay the inspector's actions; dismiss it
	// first (the toast's own close control) so the click reaches the artifact.
	if tester.HasText("关闭通知") {
		_ = tester.Click("关闭通知")
		tester.Frame()
	}
	if err := tester.Click("预览"); err != nil {
		t.Fatalf("preview artifact: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("全部通过") {
		t.Fatalf("artifact preview body missing; texts = %v", tester.Texts())
	}
	uiRoot.SetRightPanelOpen(false)
	tester.Frame()

	// notifications: a toast raised through the host bridge renders in the
	// overlay.toasts slot and can be dismissed with its close button.
	notifier, err := root.Require(contracts.KeyNotifier)
	if err != nil {
		t.Fatalf("require notifier: %v", err)
	}
	notifier.Notify(contracts.NotifySuccess, "E2E 通知")
	tester.Frame()
	if !tester.HasText("E2E 通知") {
		t.Fatalf("toast missing; texts = %v", tester.Texts())
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

// scriptedToolServer drives two model rounds: a team_task_create tool call (the
// read-only preset still asks for mutations), then the final text after the
// tool result.
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
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"team_task_create\",\"arguments\":\"{\\\"subject\\\":\\\"审批握手验证\\\"}\"}}]},\"index\":0,\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"已读取工作目录\"},\"index\":0}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":34,\"total_tokens\":46,\"prompt_cache_hit_tokens\":5}}\n\n")
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
	provide(t, root, contracts.KeyFileDrop, contracts.FileDropService(uiRoot.FileDrop()))
	provide(t, root, contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction()))
	provide(t, root, contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter()))
	provide(t, root, contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer()))
	provide(t, root, contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker()))
	provide(t, root, contracts.KeyNotifier, contracts.Notifier(uiRoot.Notifier()))
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
