// Command ui-snapshot renders Phase-1/Phase-2 product frames to PNG files for
// visual review. It is a headless development tool: no window, no real model;
// a scripted OpenAI-compatible server drives one tool call + approval + answer.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	// The framework's own zh-Hans catalog: the snapshot/driver mirrors
	// the product's language into mygo (host does the same import), so link
	// the same translations or framework chrome would stay English.
	_ "github.com/egoist/mygo/locales/zh-Hans"
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
	// pane is the inspector pane whose label the pane selector currently
	// shows, so the snapshot can open the selector and choose another pane.
	pane string
	// localeTag is the framework (mygo) locale last pushed into the Tester.
	// The product mirrors its own language into mygo.App.SetLocale; the
	// headless snapshot must do the same or framework chrome (markdown Copy,
	// divider names, ...) would keep the host language instead of the app's.
	localeTag string
}

func main() {
	outDir := "/tmp"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail("output dir", err)
	}
	if err := os.MkdirAll(outDir+"/narrow", 0o755); err != nil {
		fail("narrow dir", err)
	}
	dataDir, err := os.MkdirTemp("", "zeokeel-snapshot")
	if err != nil {
		fail("temp dir", err)
	}
	defer os.RemoveAll(dataDir)

	server := newScriptedServer()
	defer server.Close()

	a := boot(server.URL(), dataDir)
	defer a.cleanup()

	chat, err := a.root.Require(contracts.KeyChat)
	if err != nil {
		fail("chat", err)
	}
	sessionID, err := chat.NewSession("")
	if err != nil {
		fail("new session", err)
	}
	if err := chat.Send(sessionID, "帮我建一个发布检查任务，然后继续"); err != nil {
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
	// The composer must stay visible and editable while a turn streams (user
	// report #7), and the DSH-style status strip belongs right below it. Park
	// the scripted reply after its first delta, type a draft while the turn
	// runs and capture the frame, then let the turn finish and capture the
	// settled strip.
	server.SetSlow()
	if err := chat.Send(sessionID, "慢慢检查当前工作目录，然后告诉我"); err != nil {
		fail("slow send", err)
	}
	for attempts := 0; attempts < 400; attempts++ {
		a.tester.Frame()
		if session, ok := chat.Session(sessionID); ok && session.Running {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rect, ok := a.tester.Find("输入消息…"); ok {
		a.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		a.tester.Frame()
	}
	a.tester.Type("运行中继续写的草稿")
	a.tester.Frame()
	a.render(outDir + "/p2-running-composer.png")
	a.tester.Key(ui.Cmd, ui.KeyA)
	a.tester.Key(0, ui.KeyBackspace)
	a.tester.Frame()
	server.ReleaseSlow()
	waitIdle(chat, sessionID, 10*time.Second)
	// The finished turn's reasoning disclosure auto-collapses; advance past the
	// 200 ms animation so the settled frame is captured, not a half-open panel.
	a.tester.Frame()
	a.tester.Advance(300 * time.Millisecond)
	a.render(outDir + "/p2-status-strip.png")

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

	// Seed one archived session for the settings Archived page (H series).
	if archiver, ok := chat.(contracts.SessionArchiver); ok {
		if archivedID, err := chat.NewSession("归档的发布计划"); err == nil {
			_ = chat.Send(archivedID, "整理发布前的检查清单")
			waitIdle(chat, archivedID, 10*time.Second)
			_ = archiver.SetSessionArchived(archivedID, true)
		}
		_ = chat.SetActiveSession(sessionID)
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
		// Duplicates for the Memory tab's tidy action screenshot.
		_, _ = memory.Add(context.Background(), contracts.Memory{Text: "  项目根目录是   /data/mygo ", Scope: "workspace"})
		_, _ = memory.Add(context.Background(), contracts.Memory{Text: "PREFER: concise answers", Scope: "global"})
		_, _ = memory.Add(context.Background(), contracts.Memory{Text: "prefer: concise answers", Scope: "global"})
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

	// Density pass evidence (A+B): a fresh single-turn conversation (so the
	// frames are deterministic) shows the folded activity band, the message
	// menu and the compact usage details.
	if chatSvc, chatErr := a.root.Require(contracts.KeyChat); chatErr == nil {
		previousSession := chatSvc.ActiveSession()
		if evidenceID, newErr := chatSvc.NewSession("过程与动作区"); newErr == nil && evidenceID != "" {
			server.Reset()
			if sendErr := chatSvc.Send(evidenceID, "帮我建一个发布检查任务，然后继续"); sendErr == nil {
				if approvalsSvc, approveErr := a.root.Require(contracts.KeyApprovals); approveErr == nil {
					waitPending(approvalsSvc, 5*time.Second)
					for _, pending := range approvalsSvc.Pending() {
						_ = approvalsSvc.Resolve(pending.ID, contracts.ApprovalAllow)
					}
				}
				waitIdle(chatSvc, evidenceID, 10*time.Second)
				if err := a.ui.Navigator().Push("session"); err == nil {
					a.tester.Frame()
					a.render(outDir + "/p2-process-fold.png")
					// The summary's accessible name carries the live counts, so
					// click it by its stable prefix.
					clickProcess := func() bool {
						for _, text := range a.tester.Texts() {
							if strings.HasPrefix(text, "过程 ·") {
								if err := a.tester.Click(text); err == nil {
									a.tester.Frame()
									return true
								}
							}
						}
						return false
					}
					if clickProcess() {
						a.render(outDir + "/p2-process-expanded.png")
						clickProcess()
					}
					if err := a.tester.Click("消息操作"); err == nil {
						a.tester.Frame()
						a.render(outDir + "/p2-message-actions.png")
						if err := a.tester.Click("用量明细"); err == nil {
							a.tester.Frame()
							a.render(outDir + "/p2-message-usage.png")
							a.tester.Key(0, ui.KeyEscape)
							a.tester.Frame()
						}
						a.tester.Key(0, ui.KeyEscape)
						a.tester.Frame()
					}
				}
				_ = chatSvc.SetActiveSession(previousSession)
				_ = chatSvc.DeleteSession(evidenceID)
			}
		}
	}

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
	if err := a.openPane("文件"); err != nil {
		fail("artifact history pane", err)
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
		if a.ui.GoalTracker().Pause() {
			a.tester.Frame()
			a.render(outDir + "/p2-goal-paused.png")
			a.ui.GoalTracker().Resume()
			a.tester.Frame()
		}
	}

	// Team orchestration: a real teammate + the Agents inspector.
	if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil {
		_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
			ID: "spawn-snapshot", Name: "spawn_teammate",
			Arguments: `{"name":"研究员","prompt":"研究一下当前工作目录并给出摘要"}`,
		})
		if team, teamErr := a.root.Require(contracts.KeyTeam); teamErr == nil {
			// The scripted answer takes ~2.5s: wait long enough for the teammate
			// to finish so the frame shows the truthful 已完成 state and output.
			for attempts := 0; attempts < 600; attempts++ {
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
			// The header team chip is the one-step entry the audit asked for:
			// it shows the roster size and opens the inspector's team pane.
			a.render(outDir + "/p2-team-chip.png")
			a.ui.SetRightPanelOpen(true)
			a.tester.Frame()
			if err := a.openPane("团队"); err != nil {
				fmt.Println("open agents pane:", err)
			}
			a.render(outDir + "/p2-team.png")
			// A teammate row opens the activity timeline (spawn → finished).
			if members := team.Members(); len(members) > 0 {
				if err := a.tester.Click(members[0].Name); err != nil {
					// The 320 DIP pane can clip the secondary line, so fall back to
					// the row's screen position (header + sections are fixed above
					// the first teammate row).
					fmt.Println("select teammate:", err)
					a.tester.ClickAt(1120, 222)
				}
				a.tester.Frame()
				a.render(outDir + "/p2-team-detail.png")
				// The pane's own spawn form: name, self-contained task, model.
				if err := a.tester.Click("新建队友"); err == nil {
					a.tester.Frame()
					a.render(outDir + "/p2-team-spawn.png")
					a.tester.Key(0, ui.KeyEscape)
					a.tester.Frame()
				}
			}
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

	// Chat modes menu (普通/计划/精简/创作).
	if modeRect, ok := a.tester.Find("普通"); ok {
		a.tester.ClickAt(modeRect.X+modeRect.W/2, modeRect.Y+modeRect.H/2)
		a.tester.Frame()
		a.render(outDir + "/p2-chat-modes.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
	}

	// Notification toast overlay.
	if notifier, notifierErr := a.root.Require(contracts.KeyNotifier); notifierErr == nil {
		first := notifier.Notify(contracts.NotifySuccess, "对话已导出")
		second := notifier.Notify(contracts.NotifyInfo, "更新已下载到暂存目录")
		// Native toasts are added to the window by the frame that drains the
		// queue and paint their fade-in on the next pass; advance a little so
		// the screenshot shows them, not an empty viewport.
		a.tester.Frame()
		a.tester.Advance(300 * time.Millisecond)
		a.render(outDir + "/p2-toast.png")
		// Clear the toast stack so later screenshots are not covered by it.
		notifier.Dismiss(first.ID)
		notifier.Dismiss(second.ID)
		a.tester.Frame()
		a.tester.Advance(5 * time.Second)
		a.tester.Frame()
	}

	// Composer prompt presets menu (inserts prompt/persona templates).
	if presetRect, ok := a.tester.Find("预设"); ok {
		a.tester.ClickAt(presetRect.X+presetRect.W/2, presetRect.Y+presetRect.H/2)
		a.render(outDir + "/p2-presets.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
	}

	// Permission mode state (read-only → workspace-write → all permissions, the
	// slash command cycles it).
	if store, ok := a.root.Get(contracts.KeySettings); ok {
		_ = store.Set(contracts.SettingAgentPermission, contracts.PermissionFull)
		a.tester.Frame()
		a.render(outDir + "/p2-permission.png")
		_ = store.Set(contracts.SettingAgentPermission, contracts.PermissionReadOnly)
		a.tester.Frame()
	}

	// Composer model menu: the chip opens two compact entries (模型 / 推理等级)
	// and the real choices live one level down (models grouped by provider,
	// reasoning levels per model).
	if err := a.tester.Click("snapshot-model"); err == nil {
		a.tester.Frame()
		a.render(outDir + "/p2-modelmenu.png")
		// The first level starts on the 模型 row: Down/Right opens its submenu
		// without choosing anything yet.
		a.tester.Key(0, ui.KeyDown)
		a.tester.Key(0, ui.KeyRight)
		a.render(outDir + "/p2-modelmenu-models.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
		// Move to the 推理等级 row and open its submenu.
		a.tester.Key(0, ui.KeyDown)
		a.tester.Key(0, ui.KeyRight)
		a.render(outDir + "/p2-modelmenu-thinking.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
		// Switch the session model through the grouped 模型 submenu.
		if err := a.tester.Click("snapshot-model"); err == nil {
			a.tester.Frame()
			if err := a.tester.ChooseMenuItem("模型", "DeepSeek Reasoner"); err == nil {
				a.tester.Frame()
				a.render(outDir + "/p2-model-switched.png")
			} else {
				fmt.Println("choose model:", err)
			}
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
		mygo, createErr := workspaces.Create(context.Background(), "mygo", workspaceRoot)
		if createErr != nil {
			fail("workspace", createErr)
		}
		if _, sessionErr := chat.NewSession("工作区会话"); sessionErr != nil {
			fail("workspace session", sessionErr)
		}
		// A second, freshly added workspace with no sessions: the tree must show
		// its own per-workspace ⊕ action (revealed for a hovered row) next to
		// the session-bearing group, and the two groups are ordered by recent
		// activity (the empty one by its CreatedAt). The backend derives the
		// name from the folder, like the direct-picker add flow.
		emptyRoot := filepath.Join(dataDir, "zeokeel-docs")
		if err := os.MkdirAll(emptyRoot, 0o700); err != nil {
			fail("empty workspace dir", err)
		}
		docs, docsErr := workspaces.Create(context.Background(), "", emptyRoot)
		if docsErr != nil {
			fail("empty workspace", docsErr)
		}
		// Keep the session-bearing workspace current for the shot (the header
		// names it and its ⊕ stays visible), then hover the empty group row so
		// its ⊕ is revealed as it is for a pointer.
		if _, openErr := workspaces.Open(context.Background(), mygo.ID); openErr != nil {
			fail("open mygo workspace", openErr)
		}
		a.tester.Frame()
		docsName := strings.TrimSpace(docs.Name)
		if docsName == "" {
			docsName = "zeokeel-docs"
		}
		if rect, ok := a.tester.Find(docsName); ok {
			a.tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
		}
		a.render(outDir + "/p2-workspace-tree.png")
		a.tester.Move(10, 10)
		a.tester.Frame()
	}

	// P2: the window-level right inspector (durable session log pane).
	a.ui.SetRightPanelOpen(true)
	a.tester.Frame()
	// The pane selection is module state that survived the earlier inspector
	// steps (the team pane); reset it so the overview screenshot shows the
	// overview pane.
	if err := a.openPane("概览"); err != nil {
		fail("reset inspector pane", err)
	}
	a.render(outDir + "/p2-rightpanel.png")
	if err := a.openPane("文件"); err != nil {
		fail("files pane", err)
	}
	// The listing (and the preview below it) loads off the UI thread now.
	a.waitText("README.md", 5*time.Second)
	a.render(outDir + "/p2-files.png")
	if err := a.tester.Click("README.md"); err != nil {
		fail("open README.md", err)
	}
	a.waitText("ZeoKeel 工作区", 5*time.Second)
	a.render(outDir + "/p2-files-preview.png")
	if err := a.openPane("变更"); err != nil {
		fail("changes pane", err)
	}
	a.waitText("查看 diff", 5*time.Second)
	a.render(outDir + "/p2-changes.png")
	if err := a.tester.Click("查看 diff"); err != nil {
		fail("expand diff", err)
	}
	a.tester.Advance(200 * time.Millisecond)
	a.render(outDir + "/p2-changes-diff.png")
	// Terminal pane: run a real command in the workspace root.
	if err := a.openPane("终端"); err != nil {
		fail("terminal pane", err)
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

	// Shared task board: status groups, owners, dependencies and the expanded
	// task detail (the board is also the agent's todo list).
	if board, boardErr := a.root.Require(contracts.KeyTaskBoard); boardErr == nil {
		first, _ := board.Create(context.Background(), "发布前检查", "确认文档与截图")
		pending, _ := board.Create(context.Background(), "编写发布说明", "")
		_, _ = board.SetStatus(context.Background(), pending.ID, contracts.TaskStatusInProgress)
		if toolkit, tkErr := a.root.Require(contracts.KeyToolkit); tkErr == nil && first.ID != "" {
			_, _ = toolkit.Invoke(context.Background(), rig.ToolCall{
				ID: "task-snapshot", Name: "team_task_create",
				Arguments: `{"subject":"更新发布截图","description":"替换三张旧截图并复核","blocked_by":["` + first.ID + `"]}`,
			})
		}
		// Assign the dependent task to the live teammate: the owner menu and the
		// task ↔ member mapping are part of the team depth.
		if tasks, listErr := board.List(context.Background()); listErr == nil {
			if assigner, ok := any(board).(contracts.TaskAssigner); ok {
				owner := ""
				if team, teamErr := a.root.Require(contracts.KeyTeam); teamErr == nil {
					if members := team.Members(); len(members) > 0 {
						owner = members[0].Name
					}
				}
				for _, task := range tasks {
					if task.Subject == "更新发布截图" && owner != "" {
						_, _ = assigner.Assign(context.Background(), task.ID, owner)
					}
				}
			}
		}
		if err := a.openPane("任务"); err != nil {
			fail("tasks pane", err)
		}
		a.render(outDir + "/p2-tasks.png")
		// Enter expands one task's detail: description, dependency state, owner
		// and write scopes.
		if rect, ok := a.tester.Find("更新发布截图"); ok {
			a.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
			a.tester.Frame()
			a.tester.Key(0, ui.KeyEnter)
			a.tester.Frame()
			a.render(outDir + "/p2-tasks-expanded.png")
		}
	}

	// The command palette finds the team entries by their stable command IDs.
	a.tester.Key(ui.Cmd, ui.KeyK)
	a.tester.Frame()
	a.tester.Type("团队")
	a.tester.Frame()
	a.render(outDir + "/p2-palette-team.png")
	a.tester.Key(0, ui.KeyEscape)
	a.tester.Frame()
	a.tester.Key(0, ui.KeyEscape)
	a.tester.Frame()

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
	if err := a.openPane("日志"); err != nil {
		fail("log pane", err)
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
	if err := a.clickSettingsItem("模型"); err != nil {
		fmt.Println("click model settings:", err)
	}
	a.render(outDir + "/p2-model-settings.png")
	// The model list is the only model source: the add/edit dialogs carry the
	// per-model provider connection (provider preset, Base URL, API key,
	// connection test) and the default-model switch.
	if err := a.tester.Click("添加模型"); err == nil {
		a.render(outDir + "/p2-model-add.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
	}
	if err := a.tester.Click("编辑模型 snapshot-model"); err == nil {
		a.render(outDir + "/p2-model-edit.png")
		a.tester.Key(0, ui.KeyEscape)
		a.tester.Frame()
	}
	// Empty list: there are no built-in default models any more, so a cleared
	// list is a supported first-run state with an explicit add entry. Clearing
	// the list also clears the default-model mirror, exactly like removing the
	// last model on the page does.
	if store, ok := a.root.Get(contracts.KeySettings); ok && store != nil {
		_ = store.Set(contracts.SettingModelCatalogModels, "[]")
		for _, key := range []string{
			contracts.SettingsChatModel,
			contracts.SettingsChatBaseURL,
			contracts.SettingsChatAPIKey,
		} {
			_ = store.Set(key, "")
		}
		a.ui.Runtime().Invalidate()
		a.render(outDir + "/p2-model-empty.png")
		_ = store.Set(contracts.SettingModelCatalogModels, snapshotModelCatalog(server.URL()))
		for key, value := range map[string]string{
			contracts.SettingsChatModel:   "snapshot-model",
			contracts.SettingsChatBaseURL: server.URL(),
			contracts.SettingsChatAPIKey:  "",
		} {
			_ = store.Set(key, value)
		}
		a.ui.Runtime().Invalidate()
		a.tester.Frame()
	}
	if err := a.clickSettingsItem("外观"); err != nil {
		fmt.Println("click appearance:", err)
	}
	a.render(outDir + "/p2-appearance.png")
	// The hotkey recorder (H6): press Record and show the capture field.
	a.tester.Scroll(740, 500, 0, 260)
	a.tester.Frame()
	if err := a.tester.Click("录入"); err != nil {
		fmt.Println("click hotkey record:", err)
	}
	a.tester.Frame()
	a.render(outDir + "/p2-hotkey-record.png")
	a.tester.Key(0, ui.KeyEscape)
	a.tester.Frame()
	a.tester.Scroll(740, 500, 0, -260)
	a.tester.Frame()
	a.ui.Locale().SetLanguage("en")
	a.tester.Frame()
	a.render(outDir + "/p2-english-appearance.png")
	a.ui.Locale().SetLanguage("zh")
	a.tester.Frame()
	a.tester.Scroll(740, 500, 0, 240)
	a.tester.Frame()
	a.render(outDir + "/p2-appearance-actions.png")
	a.tester.Scroll(740, 500, 0, -240)
	a.tester.Frame()
	// A data-directory skill makes the destructive delete confirmation
	// reachable in the snapshot (workspace skills are read-only).
	if editor, err := a.root.Require(contracts.KeySkillsEditor); err == nil && editor != nil {
		_, _ = editor.CreateSkill(context.Background(), contracts.SkillDraft{
			Name:        "release-check",
			Description: "发布前检查清单",
			Body:        "# 发布前检查\n\n- 跑测试\n- 更新版本号",
		})
	}
	if err := a.clickSettingsItem("技能"); err != nil {
		fmt.Println("click skills:", err)
	}
	a.render(outDir + "/p2-skills.png")
	if err := a.tester.Click("删除"); err == nil {
		a.tester.Frame()
		a.render(outDir + "/p2-skills-delete.png")
		if cancel, ok := a.tester.Find("取消"); ok {
			a.tester.ClickAt(cancel.X+cancel.W/2, cancel.Y+cancel.H/2)
			a.tester.Frame()
		}
	} else {
		fmt.Println("skills delete:", err)
	}
	if err := a.tester.Click("新建技能"); err == nil {
		a.tester.Frame()
		a.render(outDir + "/p2-skills-new.png")
		if ok := a.tester.HasText("创建"); ok {
			if cancel, found := a.tester.Find("取消"); found {
				a.tester.ClickAt(cancel.X+cancel.W/2, cancel.Y+cancel.H/2)
				a.tester.Frame()
			}
		}
	}
	if err := a.tester.Click("查看内容"); err != nil {
		fmt.Println("expand skill:", err)
	}
	a.tester.Advance(200 * time.Millisecond)
	a.render(outDir + "/p2-skills-content.png")
	if err := a.clickSettingsItem("快捷键"); err != nil {
		fmt.Println("click shortcuts:", err)
	}
	a.render(outDir + "/p2-shortcuts.png")
	if err := a.clickSettingsItem("诊断"); err != nil {
		fmt.Println("click diagnostics:", err)
	}
	a.render(outDir + "/p2-diagnostics.png")
	if err := a.clickSettingsItem("更新"); err != nil {
		fmt.Println("click update:", err)
	}
	a.render(outDir + "/p2-update.png")
	if err := a.clickSettingsItem("导入"); err != nil {
		fmt.Println("click import:", err)
	}
	a.render(outDir + "/p2-import.png")
	// With a directory typed, Scan is the section's accent primary action (H24).
	if rect, ok := a.tester.Find("DSH 数据目录"); ok {
		a.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		a.tester.Type("/tmp/dsh-data")
		a.tester.Frame()
		a.render(outDir + "/p2-import-selected.png")
	}
	if err := a.clickSettingsItem("搜索"); err != nil {
		fmt.Println("click search settings:", err)
	}
	a.render(outDir + "/p2-search-settings.png")
	if err := a.clickSettingsItem("MCP"); err != nil {
		fmt.Println("click MCP settings:", err)
	}
	a.render(outDir + "/p2-mcp-settings.png")
	if err := a.clickSettingsItem("网络"); err != nil {
		fmt.Println("click network settings:", err)
	}
	a.render(outDir + "/p2-network-settings.png")
	if err := a.clickSettingsItem("预设"); err != nil {
		fmt.Println("click presets settings:", err)
	}
	a.render(outDir + "/p2-presets-settings.png")
	a.tester.Scroll(740, 500, 0, 400)
	a.tester.Frame()
	a.render(outDir + "/p2-presets-agent-meta.png")
	a.tester.Scroll(740, 500, 0, -400)
	a.tester.Frame()
	if err := a.clickSettingsItem("LSP"); err != nil {
		fmt.Println("click LSP settings:", err)
	}
	a.render(outDir + "/p2-lsp-settings.png")
	if err := a.clickSettingsItem("数据与隐私"); err != nil {
		fmt.Println("click data settings:", err)
	}
	a.render(outDir + "/p2-data-privacy.png")
	if err := a.clickSettingsItem("通知"); err != nil {
		fmt.Println("click notification settings:", err)
	}
	a.render(outDir + "/p2-notifications.png")
	if err := a.clickSettingsItem("行为"); err != nil {
		fmt.Println("click behavior settings:", err)
	}
	a.render(outDir + "/p2-behavior.png")
	if err := a.clickSettingsItem("归档"); err != nil {
		fmt.Println("click archived settings:", err)
	}
	a.render(outDir + "/p2-archive-settings.png")
	a.ui.Locale().SetLanguage("en")
	a.tester.Frame()
	a.render(outDir + "/p2-english-archive-settings.png")
	a.ui.Locale().SetLanguage("zh")
	a.tester.Frame()
	// The archived page in the dark theme (H series asks for both themes).
	a.ui.Theme().SetDark(true)
	a.tester.Frame()
	a.render(outDir + "/p2-archive-settings-dark.png")
	a.ui.Theme().SetDark(false)
	a.tester.Frame()
	// The last navigation item sits below the grouped nav's visible area; open
	// the page through the host's deep link instead of scrolling the sidebar.
	if err := a.ui.OpenSettingsPage("memory"); err != nil {
		fmt.Println("open memory settings:", err)
	}
	a.tester.Frame()
	a.render(outDir + "/p2-memory-settings.png")
	// The memory timestamps must follow the interface language (FormatDateTime).
	a.ui.Locale().SetLanguage("en")
	a.tester.Frame()
	a.render(outDir + "/p2-english-memory-settings.png")
	a.ui.Locale().SetLanguage("zh")
	a.tester.Frame()
	if err := a.ui.OpenSettingsPage("about"); err != nil {
		fmt.Println("open about settings:", err)
	}
	a.tester.Frame()
	a.render(outDir + "/p2-about-settings.png")
	a.ui.Navigator().CloseSettings()
	// Automation with a finished run so the history expansion is visible.
	if automation, err := a.root.Require(contracts.KeyAutomation); err == nil {
		if task, createErr := automation.Create(context.Background(), contracts.AutomationTask{
			Name: "每日构建检查", Prompt: "检查构建状态并汇总", Schedule: "every 24h",
			Enabled: false, Model: "snapshot-model",
		}); createErr == nil {
			_ = automation.RunNow(context.Background(), task.ID)
		}
	}
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
	if rect, ok := a.tester.Find("搜索会话与消息"); ok {
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

	// Archived sessions moved into the settings surface (the sidebar always
	// hides them now): capture the Archive page and its restore action.
	if archiver, ok := chat.(contracts.SessionArchiver); ok {
		for _, info := range chat.Sessions() {
			if strings.HasPrefix(info.Title, "看看当前工作目录") {
				_ = archiver.SetSessionArchived(info.ID, true)
			}
		}
		if err := a.ui.OpenSettingsPage("archive"); err != nil {
			fail("open archive settings page", err)
		}
		a.tester.Frame()
		if err := a.tester.Click("恢复"); err != nil {
			fail("restore archived session", err)
		}
		a.tester.Frame()
		a.render(outDir + "/p2-archive-restored.png")
		a.ui.Navigator().CloseSettings()
		a.tester.Frame()
	}
	// Narrow-window and dark-theme evidence: the responsive shell and the dark
	// theme are part of the design review, so each snapshot run renders them
	// too (the desktop default window is 1512x944).
	if err := a.ui.Navigator().Push("session"); err != nil {
		fmt.Println("push session for narrow:", err)
	}
	a.tester.SetSize(1024, 720)
	a.tester.Frame()
	a.render(outDir + "/narrow/narrow-session.png")
	a.ui.SetRightPanelOpen(true)
	a.tester.Frame()
	a.render(outDir + "/narrow/narrow-session-inspector.png")
	a.ui.SetRightPanelOpen(false)
	a.tester.Frame()
	if err := a.ui.Navigator().Push("home"); err == nil {
		a.render(outDir + "/narrow/narrow-home.png")
	}
	a.ui.Navigator().ToggleSettings()
	a.tester.Frame()
	a.render(outDir + "/narrow/narrow-settings.png")
	a.ui.Navigator().CloseSettings()
	a.tester.SetSize(1512, 944)
	a.tester.Frame()
	a.ui.Theme().SetDark(true)
	a.tester.Frame()
	if err := a.ui.Navigator().Push("home"); err == nil {
		a.render(outDir + "/narrow/dark-home.png")
	}
	if err := a.ui.Navigator().Push("session"); err == nil {
		a.render(outDir + "/narrow/dark-session.png")
	}
	a.ui.Navigator().ToggleSettings()
	a.tester.Frame()
	a.render(outDir + "/narrow/dark-settings.png")
	a.ui.Navigator().CloseSettings()
	a.ui.Theme().SetDark(false)
	a.tester.Frame()
	writeManifest(outDir)
}

// openPane opens one inspector pane through the pane selector: the module shows
// the panes as one menu button naming the current pane (eight tabs do not fit a
// 320 DIP inspector), so a snapshot clicks the selector and then the pane.
func (a *app) openPane(name string) error {
	current := a.pane
	if current == "" {
		current = "概览"
	}
	if err := a.tester.Click(current); err != nil {
		return fmt.Errorf("open pane selector %q: %w", current, err)
	}
	a.tester.Frame()
	if err := a.tester.Click(name); err != nil {
		return fmt.Errorf("choose pane %q: %w", name, err)
	}
	a.pane = name
	a.tester.Frame()
	return nil
}

// writeManifest records what produced a snapshot run, so a reviewer can tell
// whether the PNGs match the working tree they are looking at.
func writeManifest(outDir string) {
	var b strings.Builder
	fmt.Fprintf(&b, "zeokeel ui-snapshot\n")
	fmt.Fprintf(&b, "generated: %s\n", time.Now().Format(time.RFC3339))
	if out, err := exec.Command("git", "-C", ".", "rev-parse", "HEAD").Output(); err == nil {
		fmt.Fprintf(&b, "head: %s", out)
	}
	if out, err := exec.Command("git", "-C", ".", "status", "--porcelain").Output(); err == nil {
		dirty := strings.Split(strings.TrimSpace(string(out)), "\n")
		count := 0
		if len(dirty) == 1 && dirty[0] == "" {
			count = 0
		} else {
			count = len(dirty)
		}
		fmt.Fprintf(&b, "changed files: %d\n", count)
	}
	var files []string
	_ = filepath.WalkDir(outDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".png") {
			return nil
		}
		rel, relErr := filepath.Rel(outDir, path)
		if relErr == nil {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	fmt.Fprintf(&b, "screenshots: %d\n", len(files))
	for _, file := range files {
		fmt.Fprintf(&b, "  %s\n", file)
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.txt"), []byte(b.String()), 0o644); err != nil {
		fmt.Println("write manifest:", err)
	}
}

// snapshotModelCatalog is the seeded model list the snapshot app boots with:
// three rows covering the default model, a shared provider connection and a
// per-model API key. The first row is the default model (chat.model).
func snapshotModelCatalog(baseURL string) string {
	return `[{"id":"snapshot-model","name":"snapshot-model","note":"快照脚本模型","contextWindow":128000,"baseURL":"` + baseURL + `"},` +
		`{"id":"deepseek-chat","name":"DeepSeek Chat","note":"通用对话","contextWindow":128000,"baseURL":"https://api.deepseek.com/v1","apiKey":"sk-snapshot-demo"},` +
		`{"id":"deepseek-reasoner","name":"DeepSeek Reasoner","note":"深度推理","contextWindow":128000,"baseURL":"https://api.deepseek.com/v1"}]`
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
		contracts.SettingsChatModel:         "snapshot-model",
		contracts.SettingsChatBaseURL:       baseURL,
		contracts.SettingModelCatalogModels: snapshotModelCatalog(baseURL),
		// A demo MCP server whose connection fails fast, so the plugins page
		// shows a diagnostic entry whose details can be expanded.
		contracts.SettingMCPServers: `[{"name":"demo-mcp","url":"http://127.0.0.1:1/mcp"}]`,
	}}))
	provide(root, contracts.KeyLogger, contracts.Logger(logger{}))
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		fail("load", err)
	}
	a := &app{ui: uiRoot, tester: ui.NewTester(uiRoot.View, 1512, 944), loader: loader, root: root}
	a.cleanup = func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}
	return a
}

// settingsPageIDs maps the page titles the snapshot clicks to the page
// identifiers the host deep link (and the navigation row) address.
var settingsPageIDs = map[string]string{
	"模型":    "model",
	"外观":    "appearance",
	"技能":    "skills",
	"快捷键":   "shortcuts",
	"诊断":    "diagnostics",
	"更新":    "update",
	"导入":    "import",
	"搜索":    "search",
	"MCP":   "mcp",
	"网络":    "network",
	"预设":    "presets",
	"LSP":   "lsp",
	"数据与隐私": "data-privacy",
	"通知":    "notifications",
	"行为":    "behavior",
	"归档":    "archive",
}

// clickSettingsItem opens one settings page through the host's deep link, the
// same path the product's own "open model settings" links use. The grouped
// navigation scrolls, so clicking a row below the fold would silently miss;
// the deep link also verifies the page actually rendered.
func (a *app) clickSettingsItem(label string) error {
	id, ok := settingsPageIDs[label]
	if !ok {
		return fmt.Errorf("no settings page id for %q", label)
	}
	if err := a.ui.OpenSettingsPage(id); err != nil {
		return fmt.Errorf("open settings page %q: %w", label, err)
	}
	a.tester.Frame()
	if !a.tester.HasText(label) {
		return fmt.Errorf("settings page %q did not render", label)
	}
	return nil
}

func (a *app) render(path string) {
	a.syncFrameworkLocale()
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

// syncFrameworkLocale mirrors the app language into mygo's own locale, as the
// process host does for the real window (host.frameworkLocale). It is called
// before every snapshot render so the framework's built-in chrome (markdown
// Copy button, accessibility names) follows the language of the product.
func (a *app) syncFrameworkLocale() {
	tag := frameworkLocaleTag(a.ui.Locale().Language())
	if tag == a.localeTag {
		return
	}
	a.localeTag = tag
	a.tester.SetLocale(tag)
}

// frameworkLocaleTag maps the product language to the mygo locale tag linked by
// the host (zh-Hans translation catalog; English is the framework's source).
func frameworkLocaleTag(lang string) string {
	if strings.HasPrefix(strings.ToLower(lang), "zh") {
		return "zh-Hans"
	}
	return "en"
}

// waitText advances frames (letting off-UI-thread reads land) until the frame
// shows text; the inspector's Files/Changes panes now load asynchronously, so
// every step that clicks their content waits for it first.
func (a *app) waitText(text string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.tester.Frame()
		if a.tester.HasText(text) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return a.tester.HasText(text)
}

// snapshotUsage is the provider usage every scripted turn reports: realistic
// sizes so the compact (K/M) formatting and the context-window strip items are
// visible in the frames (the old 460-token numbers made the compact formatter
// invisible). Completion is small because the scripted answer is short; the
// answer delay below makes the derived tok/s land in a believable range.
const snapshotUsage = `{"prompt_tokens":42000,"completion_tokens":120,"total_tokens":42120,"prompt_cache_hit_tokens":36000}`

// scriptedAnswerDelay makes every scripted turn take a realistic wall time, so
// the message footer's 用时 and the status strip's tok/s are believable in the
// screenshots instead of the hundreds-of-thousands a zero-latency server gives.
const scriptedAnswerDelay = 2500 * time.Millisecond

// snapshotAnswer is the markdown body every scripted turn ends with; it names
// the gated team-task call the first scripted turn makes, so the approval frame
// and the transcript read as one coherent story.
const snapshotAnswer = "## 发布检查清单已创建\n\n- 任务：检查发布清单\n- 可以开始下一步了\n\n```text\n/data/mygo\n```"

// scriptedServer is the snapshot's OpenAI-compatible SSE server. The first
// model request asks for a shell tool call that the default workspace
// permission still gates (so p1-approval renders; working_directory is now
// auto-allowed), every later request answers. SetSlow parks the next reply
// after one delta so the composer-while-running frame can be captured.
type scriptedServer struct {
	srv     *httptest.Server
	calls   atomic.Int64
	slow    atomic.Bool
	mu      sync.Mutex
	release chan struct{}
}

func newScriptedServer() *scriptedServer {
	s := &scriptedServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *scriptedServer) URL() string { return s.srv.URL }
func (s *scriptedServer) Close()      { s.srv.Close() }

// Reset makes the next model request the scripted approval turn again, so a
// snapshot scenario can seed a deterministic tool-call conversation.
func (s *scriptedServer) Reset() { s.calls.Store(0) }

// SetSlow makes the next (non-approval) scripted reply stream one delta and
// then wait for ReleaseSlow.
func (s *scriptedServer) SetSlow() {
	s.mu.Lock()
	s.release = make(chan struct{})
	s.mu.Unlock()
	s.slow.Store(true)
}

// ReleaseSlow lets a parked reply finish.
func (s *scriptedServer) ReleaseSlow() {
	s.slow.Store(false)
	s.mu.Lock()
	if s.release != nil {
		close(s.release)
		s.release = nil
	}
	s.mu.Unlock()
}

func (s *scriptedServer) slowRelease() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.release
}

func (s *scriptedServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	// The approval run is the first model request; a slow run always follows it.
	if s.slow.Load() && s.calls.Load() > 0 {
		s.calls.Add(1)
		// The parked turn shows the streaming thinking block (open, spinner)
		// before its answer, as a reasoning model streams it.
		emitScriptedReasoning(w, slowReasoning)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"## 正在慢慢检查\\n\\n\"},\"index\":0}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-s.slowRelease():
		case <-time.After(30 * time.Second):
		}
		emitScriptedAnswer(w)
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	if s.calls.Add(1) == 1 {
		emitScriptedReasoning(w, scriptedReasoning)
		fmt.Fprint(w, approvalToolCallSSE())
	} else {
		time.Sleep(scriptedAnswerDelay)
		emitScriptedReasoning(w, scriptedReasoning)
		emitScriptedAnswer(w)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// approvalToolCallSSE is the first scripted model request: a team-task write,
// which the default workspace permission still gates (read-only/informational
// tools such as working_directory are auto-allowed now), works without a
// workspace root and succeeds after approval, so the frames show a normal tool
// card instead of a sandbox error.
func approvalToolCallSSE() string {
	const tool, arguments = "team_task_create", `{"subject":"检查发布清单","description":"确认文档与截图已更新"}`
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":%s,\"arguments\":%s}}]},\"index\":0,\"finish_reason\":\"tool_calls\"}]}\n\n",
		jsonString(tool), jsonString(arguments))
}

// scriptedReasoning is the thinking text every scripted turn streams first: the
// transcript frames then show the reasoning disclosure (open with the spinner
// while streaming, folded once the turn ends) instead of an empty thinking area.
const scriptedReasoning = "先确认发布检查清单需要哪些字段，再创建任务并核对结果。"

// slowReasoning is the thinking of the parked turn, so the running frame shows
// the open thinking panel and the generation spinner.
const slowReasoning = "先确认当前工作目录，再检查构建与文档状态，最后给出结论。"

// emitScriptedReasoning streams one reasoning_content delta, as a reasoning
// model does before its answer.
func emitScriptedReasoning(w http.ResponseWriter, text string) {
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%s},\"index\":0}]}\n\n", jsonString(text))
}

// emitScriptedAnswer writes the scripted markdown answer plus provider usage.
func emitScriptedAnswer(w http.ResponseWriter) {
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"index\":0}]}\n\n", jsonString(snapshotAnswer))
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":%s}\n\n", snapshotUsage)
}

// jsonString encodes one Go string as a JSON string literal.
func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
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
