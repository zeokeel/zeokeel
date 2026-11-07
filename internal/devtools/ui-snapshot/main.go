// Command ui-snapshot renders Phase-1/Phase-2 product frames to PNG files for
// visual review. It is a headless development tool: no window, no real model;
// a scripted OpenAI-compatible server drives one tool call + approval + answer.
package main

import (
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
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
	a.render(outDir + "/p1-chat.png")
	if err := a.tester.Click("轨迹"); err != nil {
		fmt.Println("click trajectory:", err)
	}
	a.render(outDir + "/p1-trajectory.png")

	// P2: the window-level right inspector (durable session log pane).
	a.ui.SetRightPanelOpen(true)
	a.render(outDir + "/p2-rightpanel.png")
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
	if err := a.tester.Click("快捷键"); err != nil {
		fmt.Println("click shortcuts:", err)
	}
	a.render(outDir + "/p2-shortcuts.png")
	a.ui.Navigator().CloseSettings()
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
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"当前工作目录已经确认，可以开始下一步了。\"},\"index\":0}]}\n\n")
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
