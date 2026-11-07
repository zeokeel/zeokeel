// Command ui-snapshot renders Phase-1/Phase-2 product frames to PNG files for
// visual review. It is a headless development tool: no window, no real model;
// a scripted OpenAI-compatible server drives one tool call + approval + answer.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type settings struct{ values map[string]string }

func (s *settings) Get(key, fallback string) string {
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}
func (s *settings) Set(key, value string) error { s.values[key] = value; return nil }
func (s *settings) All() map[string]string      { return s.values }

type logger struct{}

func (logger) Printf(string, ...any) {}

type app struct {
	ui      *uiroot.Root
	tester  *ui.Tester
	loader  *module.Loader
	root    cordis.Context
	cleanup func()
}

func main() {
	outDir := "/tmp"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	dataDir, err := os.MkdirTemp("", "zeokeel-snapshot")
	if err != nil {
		fail("temp dir", err)
	}
	defer os.RemoveAll(dataDir)

	server := scriptedServer()
	defer server.Close()

	a := boot(server.URL, dataDir)
	defer a.cleanup()

	chat, err := a.root.Require(contracts.KeyChat)
	if err != nil {
		fail("chat", err)
	}
	sessionID, err := chat.NewSession("")
	if err != nil {
		fail("new session", err)
	}
	if err := chat.Send(sessionID, "看看当前工作目录，然后告诉我"); err != nil {
		fail("send", err)
	}
	approvals, err := a.root.Require(contracts.KeyApprovals)
	if err != nil {
		fail("approvals", err)
	}
	waitPending(approvals, 5*time.Second)
	a.render(outDir + "/p1-approval.png")

	for _, pending := range approvals.Pending() {
		if err := approvals.Resolve(pending.ID, contracts.ApprovalAllow); err != nil {
			fail("resolve", err)
		}
	}
	waitIdle(chat, sessionID, 10*time.Second)
	if err := a.ui.Navigator().Push("session"); err != nil {
		fail("push", err)
	}
	// Send an image attachment through the optional attachment API so the chat
	// frame shows the staged-message chips and the provider sees an image part.
	if sender, ok := chat.(contracts.AttachmentSender); ok {
		if pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="); err == nil {
			_ = sender.SendWithAttachments(sessionID, "看看这张图", []contracts.Attachment{{
				ID: "att-1", Name: "zeokeel.png", MIME: "image/png", Data: pngBytes,
			}})
			waitIdle(chat, sessionID, 10*time.Second)
		}
	}

	// Seed one persisted automation task for the Automation screen.
	if automation, err := a.root.Require(contracts.KeyAutomation); err == nil {
		_, _ = automation.Create(context.Background(), contracts.AutomationTask{
			Name: "每日构建检查", Prompt: "检查构建是否通过并总结", Schedule: "*/30 * * * *", Enabled: true,
		})
	}

	// A native window drop stages a composer attachment (same path as the
	// picker): dispatch two paths and render the staged chips.
	if drop, err := a.root.Require(contracts.KeyFileDrop); err == nil {
		dropDir, dirErr := os.MkdirTemp("", "zeokeel-drop")
		if dirErr == nil {
			notePath := filepath.Join(dropDir, "dropped-note.md")
			_ = os.WriteFile(notePath, []byte("# 拖放进来的笔记\n\n来自窗口拖放。\n"), 0o600)
			drop.Dispatch([]string{notePath})
			a.tester.Frame()
			a.tester.Frame()
			a.render(outDir + "/p2-drop.png")
		}
	}

	// @session references: the composer @ menu also lists other conversations.
	if chat, chatErr := a.root.Require(contracts.KeyChat); chatErr == nil {
		activeBefore := chat.ActiveSession()
		if _, err := chat.NewSession("参考会话"); err == nil && activeBefore != "" {
			_ = chat.SetActiveSession(activeBefore)
		}
		a.tester.Frame()
	}
	if sendRect, ok := a.tester.Find("发送"); ok {
		a.tester.ClickAt(sendRect.X-120, sendRect.Y-50)
		a.tester.Frame()
		a.tester.Type("@")
		a.tester.Frame()
		a.render(outDir + "/p2-sessionrefs.png")
		a.tester.Key(0, ui.KeyEscape)
		if rect, ok := a.tester.Find("发送"); ok {
			a.tester.ClickAt(rect.X-120, rect.Y-50)
			a.tester.Frame()
		}
		a.tester.Key(ui.Cmd, ui.KeyA)
		a.tester.Key(0, ui.KeyBackspace)
		a.tester.Frame()
	}

	// English UI (live locale switch): the session screen + sidebar re-render.
	if locale, err := a.root.Require(contracts.KeyLocale); err == nil {
		locale.SetLanguage("en")
		a.tester.Frame()
		a.render(outDir + "/p2-english.png")
		a.ui.Navigator().ToggleSettings()
		a.tester.Frame()
		a.render(outDir + "/p2-english-settings.png")
		a.ui.Navigator().CloseSettings()
		if err := a.ui.Navigator().Push("automation"); err == nil {
			a.render(outDir + "/p2-english-automation.png")
		}
		if err := a.ui.Navigator().Push("session"); err != nil {
			fmt.Println("push session after english:", err)
		}
		locale.SetLanguage("zh")
		a.tester.Frame()
	}

	// Fork the finished conversation (agent SessionForker): the sidebar shows
	// the copy and it becomes the active session.
	if forker, ok := chat.(contracts.SessionForker); ok {
		if forkID, forkErr := forker.ForkSession(sessionID); forkErr == nil {
			_ = forkID
			a.render(outDir + "/p2-fork.png")
		}
	}

	// Seed the durable memory backend and render the Memory tab.
	if memory, err := a.root.Require(contracts.KeyMemory); err == nil {
		_, _ = memory.Add(context.Background(), contracts.Memory{Text: "偏好：界面使用中文，回答尽量简洁", Scope: "global"})
		_, _ = memory.Add(context.Background(), contracts.Memory{Text: "项目根目录是 /data/mygo", Scope: "workspace"})
	}
	a.render(outDir + "/p1-chat.png")
	a.render(outDir + "/p2-attachments.png")
	if err := a.tester.Click("记忆"); err != nil {
		fmt.Println("click memory tab:", err)
	}
	a.render(outDir + "/p2-memory.png")
	if err := a.tester.Click("对话"); err != nil {
		fmt.Println("click chat tab:", err)
	}
	a.render(outDir + "/p1-chat.png")

	// Composer slash commands (the old shell's command menu).
	if sendRect, ok := a.tester.Find("发送"); ok {
		a.tester.ClickAt(sendRect.X-120, sendRect.Y-50)
		a.tester.Frame()
		a.tester.Type("/")
		a.tester.Frame()
		a.render(outDir + "/p2-slash.png")
		a.tester.Key(ui.Cmd, ui.KeyA)
		a.tester.Key(0, ui.KeyBackspace)
		a.tester.Frame()
	}
	// ask_user: the host bridge + native overlay dialog (the tool parks).
	if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil {
		go func() {
			_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
				ID: "ask-snapshot", Name: "ask_user",
				Arguments: `{"question":"要使用哪个数据库？","options":["SQLite（本地文件）","PostgreSQL（服务端）"]}`,
			})
		}()
		for attempts := 0; attempts < 50; attempts++ {
			a.tester.Frame()
			if a.tester.HasText("要使用哪个数据库？") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		a.render(outDir + "/p2-ask.png")
		if err := a.tester.Click("SQLite（本地文件）"); err != nil {
			fmt.Println("answer ask dialog:", err)
		}
		a.tester.Frame()
	}

	// present: artifact preview overlay (markdown body + 关闭).
	if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil {
		go func() {
			_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
				ID: "present-snapshot", Name: "present",
				Arguments: `{"title":"检查报告","content":"# 构建结果\n\n- 30 个测试包通过\n- 0 失败\n\n![badge](https://example.com/badge.svg)"}`,
			})
		}()
		for attempts := 0; attempts < 50; attempts++ {
			a.tester.Frame()
			if a.tester.HasText("检查报告") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		a.render(outDir + "/p2-artifact.png")
		if err := a.tester.Click("关闭"); err != nil {
			fmt.Println("close artifact:", err)
		}
		a.tester.Frame()
	}

	// Artifact history in the Files inspector (preview from the history list).
	a.ui.SetRightPanelOpen(true)
	a.tester.Frame()
	if err := a.tester.Click("文件"); err != nil {
		fmt.Println("open files tab:", err)
	}
	a.render(outDir + "/p2-artifact-history.png")
	a.ui.SetRightPanelOpen(false)
	a.tester.Frame()

	// plan_review: plan overlay with approve/cancel.
	if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil {
		go func() {
			_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
				ID: "plan-snapshot", Name: "plan_review",
				Arguments: `{"plan":"# 发布计划\n\n1. 运行全量测试\n2. 更新文档与截图\n3. 打 tag 并发布"}`,
			})
		}()
		for attempts := 0; attempts < 50; attempts++ {
			a.tester.Frame()
			if a.tester.HasText("计划待审阅") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		a.render(outDir + "/p2-plan.png")
		if err := a.tester.Click("批准"); err != nil {
			fmt.Println("approve plan:", err)
		}
		a.tester.Frame()
	}

	// goal: the agent goal tool shows in the session header.
	if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil {
		_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
			ID: "goal-snapshot", Name: "goal", Arguments: `{"action":"set","text":"完成 Q4 发布清单并更新文档"}`,
		})
		a.tester.Frame()
		a.render(outDir + "/p2-goal.png")
	}

	// Team orchestration: a real teammate + the Agents inspector.
	if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil {
		_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
			ID: "spawn-snapshot", Name: "spawn_teammate",
			Arguments: `{"name":"研究员","prompt":"研究一下当前工作目录并给出摘要"}`,
		})
		if team, teamErr := a.root.Require(contracts.KeyTeam); teamErr == nil {
			for attempts := 0; attempts < 80; attempts++ {
				ready := false
				for _, member := range team.Members() {
					if member.Last != "" {
						ready = true
					}
				}
				if ready {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			a.ui.SetRightPanelOpen(true)
			a.tester.Frame()
			if err := a.tester.Click("团队"); err != nil {
				fmt.Println("open agents tab:", err)
			}
			a.render(outDir + "/p2-team.png")
			a.ui.SetRightPanelOpen(false)
			a.tester.Frame()
		}
	}

	// Plan mode: the header badge and composer mode chip.
	if store, ok := a.root.Get(contracts.KeySettings); ok {
		_ = store.Set(contracts.SettingAgentMode, contracts.AgentModePlan)
		a.tester.Frame()
		a.render(outDir + "/p2-plan-mode.png")
		_ = store.Set(contracts.SettingAgentMode, contracts.AgentModeNormal)
		a.tester.Frame()
	}

	// Notification toast overlay.
	if notifier, notifierErr := a.root.Require(contracts.KeyNotifier); notifierErr == nil {
		notifier.Notify(contracts.NotifySuccess, "对话已导出")
		notifier.Notify(contracts.NotifyInfo, "更新已下载到暂存目录")
		a.tester.Frame()
		a.render(outDir + "/p2-toast.png")
	}

	// Composer prompt presets menu (inserts prompt/persona templates).
	if presetRect, ok := a.tester.Find("预设"); ok {
		a.tester.ClickAt(presetRect.X+presetRect.W/2, presetRect.Y+presetRect.H/2)
		a.render(outDir + "/p2-presets.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
	}

	// Permission mode chip (ask → workspace → full, the slash command cycles it).
	if store, ok := a.root.Get(contracts.KeySettings); ok {
		_ = store.Set(contracts.SettingAgentPermission, contracts.PermissionFull)
		a.tester.Frame()
		a.render(outDir + "/p2-permission.png")
		_ = store.Set(contracts.SettingAgentPermission, contracts.PermissionAsk)
		a.tester.Frame()
	}

	// Composer model menu (catalog presets + per-session switch).
	if err := a.tester.Click("snapshot-model"); err == nil {
		a.tester.Frame()
		a.render(outDir + "/p2-modelmenu.png")
		if err := a.tester.Click("DeepSeek Reasoner · 深度推理"); err == nil {
			a.tester.Frame()
			a.render(outDir + "/p2-model-switched.png")
		}
	}

	if err := a.tester.Click("轨迹"); err != nil {
		fmt.Println("click trajectory:", err)
	}
	a.render(outDir + "/p1-trajectory.png")

	// P2: the Files inspector reads the workspace backend.
	workspaceRoot, err := os.MkdirTemp("", "zeokeel-snapshot-workspace")
	if err != nil {
		fail("workspace dir", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("# ZeoKeel 工作区\n\n这是右侧 Files 检查器的示例文件。\n\n- 目录浏览\n- 文本与 Markdown 预览\n"), 0o600); err != nil {
		fail("write README", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "internal"), 0o700); err != nil {
		fail("mkdir internal", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "internal", "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		fail("write main.go", err)
	}
	skillDir := filepath.Join(workspaceRoot, "skills", "release-checklist")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		fail("mkdir skill", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: release-checklist\ndescription: 发布前检查清单\n---\n# 发布检查\n\n- 跑测试\n- 更新 changelog\n"), 0o600); err != nil {
		fail("write SKILL.md", err)
	}
	// Make the workspace a git repository with one uncommitted edit and one
	// untracked file so the 变更 tab has content.
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=ZeoKeel", "GIT_AUTHOR_EMAIL=test@zeokeel.local",
		"GIT_COMMITTER_NAME=ZeoKeel", "GIT_COMMITTER_EMAIL=test@zeokeel.local")
	runGit := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", workspaceRoot}, args...)...)
		command.Env = gitEnv
		_ = command.Run()
	}
	runGit("init")
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	_ = os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("# ZeoKeel 工作区\n\n这是右侧 Files 检查器的示例文件。\n\n- 目录浏览\n- 文本与 Markdown 预览\n- 工作区变更 diff\n"), 0o600)
	_ = os.WriteFile(filepath.Join(workspaceRoot, "notes.txt"), []byte("临时笔记\n"), 0o600)
	if workspaces, err := a.root.Require(contracts.KeyWorkspace); err == nil {
		if workspace, createErr := workspaces.Create(context.Background(), "mygo", workspaceRoot); createErr == nil {
			_ = workspace
			if _, sessionErr := chat.NewSession("工作区会话"); sessionErr == nil {
				a.render(outDir + "/p2-workspace-tree.png")
			}
		}
	}

	// P2: the window-level right inspector (durable session log pane).
	a.ui.SetRightPanelOpen(true)
	a.render(outDir + "/p2-rightpanel.png")
	if err := a.tester.Click("文件"); err != nil {
		fmt.Println("click files tab:", err)
	}
	a.render(outDir + "/p2-files.png")
	if err := a.tester.Click("README.md"); err != nil {
		fmt.Println("click README:", err)
	}
	a.render(outDir + "/p2-files-preview.png")
	if err := a.tester.Click("变更"); err != nil {
		fmt.Println("click changes tab:", err)
	}
	a.render(outDir + "/p2-changes.png")
	if err := a.tester.Click("查看 diff"); err != nil {
		fmt.Println("expand diff:", err)
	}
	a.tester.Advance(200 * time.Millisecond)
	a.render(outDir + "/p2-changes-diff.png")
	// Terminal tab: run a real command in the workspace root.
	if err := a.tester.Click("终端"); err != nil {
		fmt.Println("click terminal tab:", err)
	}
	a.tester.Frame()
	if runRect, ok := a.tester.Find("运行"); ok {
		a.tester.ClickAt(runRect.X-140, runRect.Y+runRect.H/2)
		a.tester.Frame()
		a.tester.Type("echo ZeoKeel terminal")
		if err := a.tester.Click("运行"); err != nil {
			fmt.Println("run terminal:", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			a.tester.Frame()
			if a.tester.HasText("ZeoKeel terminal") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	a.render(outDir + "/p2-terminal.png")

	// Shared task board (todo panel): create two tasks and render the Tasks tab.
	if board, boardErr := a.root.Require(contracts.KeyTaskBoard); boardErr == nil {
		_, _ = board.Create(context.Background(), "发布前检查", "确认文档与截图")
		pending, _ := board.Create(context.Background(), "编写发布说明", "")
		_, _ = board.SetStatus(context.Background(), pending.ID, contracts.TaskStatusInProgress)
		if err := a.tester.Click("任务"); err != nil {
			fmt.Println("open tasks tab:", err)
		}
		a.render(outDir + "/p2-tasks.png")
	}

	// Composer @-mention suggestions over the workspace.
	if sendRect, ok := a.tester.Find("发送"); ok {
		a.tester.ClickAt(sendRect.X-120, sendRect.Y-50)
		a.tester.Frame()
		a.tester.Type("@REA")
		a.render(outDir + "/p2-mentions.png")
		a.tester.Key(ui.Cmd, ui.KeyA)
		a.tester.Key(0, ui.KeyBackspace)
		a.render(outDir + "/p2-mentions.png")
	}
	if err := a.tester.Click("日志"); err != nil {
		fmt.Println("click log tab:", err)
	}
	a.render(outDir + "/p2-log.png")
	a.ui.SetRightPanelOpen(false)

	// P2: the collapsed 64 DIP sidebar rail (Cmd/Ctrl+B).
	a.ui.SetSidebarCollapsed(true)
	a.render(outDir + "/p2-sidebar-collapsed.png")
	a.ui.SetSidebarCollapsed(false)

	// P2: Home recent sessions and the appearance settings page.
	if err := a.ui.Navigator().Push("home"); err != nil {
		fmt.Println("push home:", err)
	}
	a.render(outDir + "/p2-home.png")
	a.ui.Navigator().ToggleSettings()
	a.tester.Frame()
	if err := a.tester.Click("外观"); err != nil {
		fmt.Println("click appearance:", err)
	}
	if err := a.tester.Click("服务商"); err != nil {
		fmt.Println("click providers:", err)
	}
	a.render(outDir + "/p2-providers.png")
	if err := a.tester.Click("外观"); err != nil {
		fmt.Println("click appearance:", err)
	}
	a.render(outDir + "/p2-appearance.png")
	if err := a.tester.Click("技能"); err != nil {
		fmt.Println("click skills:", err)
	}
	a.render(outDir + "/p2-skills.png")
	if err := a.tester.Click("查看内容"); err != nil {
		fmt.Println("expand skill:", err)
	}
	a.tester.Advance(200 * time.Millisecond)
	a.render(outDir + "/p2-skills-content.png")
	if err := a.tester.Click("快捷键"); err != nil {
		fmt.Println("click shortcuts:", err)
	}
	a.render(outDir + "/p2-shortcuts.png")
	if err := a.tester.Click("诊断"); err != nil {
		fmt.Println("click diagnostics:", err)
	}
	a.render(outDir + "/p2-diagnostics.png")
	if err := a.tester.Click("更新"); err != nil {
		fmt.Println("click update:", err)
	}
	a.render(outDir + "/p2-update.png")
	if err := a.tester.Click("导入"); err != nil {
		fmt.Println("click import:", err)
	}
	a.render(outDir + "/p2-import.png")
	if err := a.tester.Click("搜索"); err != nil {
		fmt.Println("click search settings:", err)
	}
	a.render(outDir + "/p2-search-settings.png")
	if err := a.tester.Click("MCP"); err != nil {
		fmt.Println("click MCP settings:", err)
	}
	a.render(outDir + "/p2-mcp-settings.png")
	if err := a.tester.Click("网络"); err != nil {
		fmt.Println("click network settings:", err)
	}
	a.render(outDir + "/p2-network-settings.png")
	if err := a.tester.Click("预设"); err != nil {
		fmt.Println("click presets settings:", err)
	}
	a.render(outDir + "/p2-presets-settings.png")
	if err := a.tester.Click("模型目录"); err != nil {
		fmt.Println("click model catalog settings:", err)
	}
	a.render(outDir + "/p2-modelcatalog-settings.png")
	a.ui.Navigator().CloseSettings()
	if err := a.ui.Navigator().Push("automation"); err != nil {
		fmt.Println("push automation:", err)
	}
	a.render(outDir + "/p2-automation.png")
	if err := a.ui.Navigator().Push("plugins"); err != nil {
		fmt.Println("push plugins:", err)
	}
	a.render(outDir + "/p2-plugins.png")
	if rect, ok := a.tester.Find("搜索工具"); ok {
		a.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		a.tester.Frame()
		a.tester.Type("memory")
		a.render(outDir + "/p2-plugins-search.png")
	}

	// Sidebar full-text message search over the stored sessions.
	if rect, ok := a.tester.Find("搜索会话"); ok {
		a.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		a.tester.Frame()
		a.tester.Type("工作目录")
		a.render(outDir + "/p2-search.png")
	}

	// Clear the sidebar search, then render the host command palette.
	if rect, ok := a.tester.Find("工作目录"); ok {
		a.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	}
	a.tester.Key(ui.Cmd, ui.KeyA)
	a.tester.Key(0, ui.KeyBackspace)
	a.tester.Frame()
	a.tester.Key(ui.Cmd, ui.KeyK)
	a.render(outDir + "/p2-palette.png")
	a.tester.Key(0, ui.KeyEscape)
	a.tester.Frame()

	// Pinned session in the sidebar (agent SessionPinner + settings pin flag).
	if pinner, ok := chat.(contracts.SessionPinner); ok {
		for _, info := range chat.Sessions() {
			if info.Title == "工作区会话" {
				_ = pinner.SetSessionPinned(info.ID, true)
			}
		}
		a.render(outDir + "/p2-pinned.png")
	}

	// Archived sessions (agent SessionArchiver + sidebar view option).
	if archiver, ok := chat.(contracts.SessionArchiver); ok {
		for _, info := range chat.Sessions() {
			if strings.HasPrefix(info.Title, "看看当前工作目录") {
				_ = archiver.SetSessionArchived(info.ID, true)
			}
		}
		if settingsStore, ok := a.root.Get(contracts.KeySettings); ok {
			_ = settingsStore.Set(contracts.SettingSessionsShowArchived, "true")
		}
		a.render(outDir + "/p2-archived.png")
	}
}

func boot(baseURL, dataDir string) *app {
	root := cordis.Root()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	provide(root, contracts.KeyDataDir, dataDir)
	provide(root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(root, contracts.KeyTheme, uiRoot.Theme())
	provide(root, contracts.KeyLocale, uiRoot.Locale())
	provide(root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(root, contracts.KeyFileDrop, contracts.FileDropService(uiRoot.FileDrop()))
	provide(root, contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction()))
	provide(root, contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter()))
	provide(root, contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer()))
	provide(root, contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker()))
	provide(root, contracts.KeyNotifier, contracts.Notifier(uiRoot.Notifier()))
	provide(root, contracts.KeySettings, contracts.SettingsService(&settings{values: map[string]string{
		contracts.SettingsChatModel:   "snapshot-model",
		contracts.SettingsChatBaseURL: baseURL,
	}}))
	provide(root, contracts.KeyLogger, contracts.Logger(logger{}))
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		fail("load", err)
	}
	a := &app{ui: uiRoot, tester: ui.NewTester(uiRoot.View, 1280, 800), loader: loader, root: root}
	a.cleanup = func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}
	return a
}

func (a *app) render(path string) {
	a.tester.Frame()
	img := a.tester.Image()
	file, err := os.Create(path)
	if err != nil {
		fail("create "+path, err)
	}
	if err := png.Encode(file, img); err != nil {
		fail("encode "+path, err)
	}
	_ = file.Close()
	fmt.Println("wrote", path, img.Bounds().Size())
}

func scriptedServer() *httptest.Server {
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
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"## 工作目录已确认\\n\\n- 路径：当前工作目录\\n- 可以开始下一步了\\n\\n```text\\n/data/mygo\\n```\"},\"index\":0}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":340,\"total_tokens\":460,\"prompt_cache_hit_tokens\":20}}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func waitPending(approvals contracts.Approvals, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(approvals.Pending()) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fail("approvals", fmt.Errorf("no pending request within %s", timeout))
}

func waitIdle(chat contracts.ChatService, sessionID string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if session, ok := chat.Session(sessionID); ok && !session.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fail("wait idle", fmt.Errorf("session %s still running", sessionID))
}

func provide[T any](root cordis.Context, key cordis.ServiceKey[T], value T) {
	if _, err := root.Provide(key, value); err != nil {
		fail("provide "+key.Name(), err)
	}
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "ui-snapshot: %s: %v\n", step, err)
	os.Exit(1)
}
