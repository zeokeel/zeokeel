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
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

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
	a.ui.Navigator().CloseSettings()
	if err := a.ui.Navigator().Push("automation"); err != nil {
		fmt.Println("push automation:", err)
	}
	a.render(outDir + "/p2-automation.png")
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
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}]}\n\n")
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
