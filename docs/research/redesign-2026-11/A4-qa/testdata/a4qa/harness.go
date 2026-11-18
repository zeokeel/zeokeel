package main

// The A4 interaction-QA harness: the same hand-wired desktop boot the
// internal/devtools/ui-drive driver uses (assembly + uiroot + memory settings +
// a scripted OpenAI-compatible stream), plus the seeding a QA case needs
// (a workspace with files, two conversations, a slow running turn, a pending
// approval). Nothing here touches the product: it links the real desktop
// assembly, exactly as ui-drive does.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// memSettings is the in-memory settings service the desktop product reads.
type memSettings struct{ values map[string]string }

func (s *memSettings) Get(key, fallback string) string {
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}
func (s *memSettings) Set(key, value string) error { s.values[key] = value; return nil }
func (s *memSettings) All() map[string]string      { return s.values }

// discardLogger silences module logging.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// harness is one booted product under test.
type harness struct {
	t        *testing.T // nil outside a go test binary
	outDir   string
	root     cordis.Context
	uiRoot   *uiroot.Root
	loader   interface{ Close() error }
	server   *scriptServer
	dataDir  string
	wsRoot   string
	tester   *ui.Tester
	sessions []string // session ids, oldest first
	active   string
}

func provide[T any](root cordis.Context, key cordis.ServiceKey[T], value T) error {
	if _, err := root.Provide(key, value); err != nil {
		return fmt.Errorf("provide %s: %w", key.Name(), err)
	}
	return nil
}

// harnessOptions shape one boot.
type harnessOptions struct {
	// NoModel boots without a configured chat model, for the "no model" copy.
	NoModel bool
}

// newHarness boots the desktop product headlessly.
func newHarness(outDir string) (*harness, error) {
	return newHarnessWith(outDir, harnessOptions{})
}

// newHarnessWith boots the desktop product headlessly with options.
func newHarnessWith(outDir string, opts harnessOptions) (*harness, error) {
	dataDir, err := os.MkdirTemp("", "a4qa-data")
	if err != nil {
		return nil, fmt.Errorf("temp data dir: %w", err)
	}
	server := newScriptServer()
	root := cordis.Root()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	settings := &memSettings{values: map[string]string{}}
	if !opts.NoModel {
		settings.values[contracts.SettingsChatModel] = "qa-model"
		settings.values[contracts.SettingsChatBaseURL] = server.URL()
	}
	h := &harness{outDir: outDir, root: root, uiRoot: uiRoot, server: server, dataDir: dataDir}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"DataDir", func() error { return provide(root, contracts.KeyDataDir, dataDir) }},
		{"UIRuntime", func() error { return provide(root, contracts.KeyUIRuntime, uiRoot.Runtime()) }},
		{"ToastAnchor", func() error { return provide(root, contracts.KeyToastAnchor, uiRoot.ToastAnchor()) }},
		{"Navigator", func() error { return provide(root, contracts.KeyNavigator, uiRoot.Navigator()) }},
		{"Theme", func() error { return provide(root, contracts.KeyTheme, uiRoot.Theme()) }},
		{"Locale", func() error { return provide(root, contracts.KeyLocale, uiRoot.Locale()) }},
		{"ScreenLister", func() error { return provide(root, contracts.KeyScreenLister, uiRoot.ScreenLister()) }},
		{"FileDrop", func() error {
			return provide(root, contracts.KeyFileDrop, contracts.FileDropService(uiRoot.FileDrop()))
		}},
		{"UserInteraction", func() error {
			return provide(root, contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction()))
		}},
		{"ArtifactPresenter", func() error {
			return provide(root, contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter()))
		}},
		{"PlanReviewer", func() error {
			return provide(root, contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer()))
		}},
		{"Notifier", func() error { return provide(root, contracts.KeyNotifier, contracts.Notifier(uiRoot.Notifier())) }},
		{"Settings", func() error { return provide(root, contracts.KeySettings, contracts.SettingsService(settings)) }},
		{"Logger", func() error { return provide(root, contracts.KeyLogger, contracts.Logger(discardLogger{})) }},
	}
	for _, step := range steps {
		if err := step.fn(); err != nil {
			h.close()
			return nil, fmt.Errorf("boot %s: %w", step.name, err)
		}
	}
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		h.close()
		return nil, fmt.Errorf("load desktop assembly: %w", err)
	}
	h.loader = loader
	h.tester = ui.NewTester(uiRoot.View, 1280, 800)
	h.tester.Frame()
	return h, nil
}

// close tears the product down.
func (h *harness) close() {
	if h.loader != nil {
		_ = h.loader.Close()
	}
	if h.root != (cordis.Context{}) {
		_ = h.root.DisposeAll()
	}
	if h.server != nil {
		h.server.Close()
	}
	for _, dir := range []string{h.dataDir, h.wsRoot} {
		if dir != "" && strings.HasPrefix(filepath.Clean(dir), os.TempDir()) {
			_ = os.RemoveAll(dir)
		}
	}
}

// seedWorkspace creates a workspace with three files, so the inspector tree and
// the composer's drop path have real content.
func (h *harness) seedWorkspace() error {
	workspace, err := h.root.Require(contracts.KeyWorkspace)
	if err != nil {
		return fmt.Errorf("workspace service: %w", err)
	}
	root, err := os.MkdirTemp("", "a4qa-ws")
	if err != nil {
		return fmt.Errorf("temp workspace: %w", err)
	}
	h.wsRoot = root
	// Distinct bodies so a preview can be asserted by content, not by the file
	// name that the tree and the tab also show.
	bodies := map[string]string{
		"README.md": "# README.md\n\nQA 预览标记 alpha\n",
		"notes.txt": "QA 预览标记 beta\n",
		"main.go":   "package main\n\n// QA 预览标记 gamma\n",
	}
	for _, name := range []string{"README.md", "notes.txt", "main.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(bodies[name]), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	if _, err := workspace.Create(context.Background(), "qa-ws", root); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	return nil
}

// newSession creates one conversation and makes it active.
func (h *harness) newSession(title string) (string, error) {
	chat, err := h.root.Require(contracts.KeyChat)
	if err != nil {
		return "", fmt.Errorf("chat service: %w", err)
	}
	id, err := chat.NewSession(title)
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	h.sessions = append(h.sessions, id)
	h.active = id
	if err := h.uiRoot.Navigator().Push("session"); err != nil {
		return "", fmt.Errorf("push session: %w", err)
	}
	h.frame()
	return id, nil
}

func (h *harness) chat() (contracts.ChatService, error) {
	return h.root.Require(contracts.KeyChat)
}

func (h *harness) frame() {
	h.tester.Frame()
}

func (h *harness) drain(frames int) {
	for i := 0; i < frames; i++ {
		h.tester.Frame()
	}
}

// waitText pumps frames until the frame shows text (or times out). The scripted
// server runs on real time, so this also sleeps a little.
func (h *harness) waitText(text string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.tester.Frame()
		if h.tester.HasText(text) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// waitRunning pumps frames until the active conversation reports the state.
func (h *harness) waitRunning(running bool, timeout time.Duration) bool {
	chat, err := h.chat()
	if err != nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.tester.Frame()
		if session, ok := chat.Session(h.active); ok && session.Running == running {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func (h *harness) running() bool {
	chat, err := h.chat()
	if err != nil {
		return false
	}
	session, ok := chat.Session(h.active)
	return ok && session.Running
}

func (h *harness) sessionCount() int {
	chat, err := h.chat()
	if err != nil {
		return -1
	}
	return len(chat.Sessions())
}

func (h *harness) pendingApprovals() int {
	approvals, ok := h.root.Get(contracts.KeyApprovals)
	if !ok || approvals == nil {
		return -1
	}
	return len(approvals.Pending())
}

// startSlowTurn types one prompt and sends it while the scripted server streams
// a slow answer, so the conversation is Running when the case takes over.
func (h *harness) startSlowTurn(prompt string) error {
	h.server.SetMode(scriptSlowAnswer)
	if !h.tester.HasText("输入消息…") {
		return fmt.Errorf("the composer is not on screen (texts=%v)", h.tester.Texts())
	}
	rect, ok := h.tester.Find("输入消息…")
	if !ok {
		return fmt.Errorf("the composer input is missing")
	}
	h.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	h.frame()
	h.tester.Type(prompt)
	h.frame()
	if err := h.tester.Click("发送"); err != nil {
		return fmt.Errorf("click send: %w", err)
	}
	if !h.waitRunning(true, 5*time.Second) {
		return fmt.Errorf("the turn never started running")
	}
	return nil
}

// requestApproval parks one real approval request on the approval service from
// a goroutine (the backend's own registration path) without needing a model.
func (h *harness) requestApproval(tool, summary string) error {
	approvals, ok := h.root.Get(contracts.KeyApprovals)
	if !ok || approvals == nil {
		return fmt.Errorf("no approval service")
	}
	id := fmt.Sprintf("qa-ap-%d", time.Now().UnixNano())
	request := contracts.ApprovalRequest{ID: id, Tool: tool, SessionID: h.active, Summary: summary}
	go func() { _, _ = approvals.Request(context.Background(), request) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.tester.Frame()
		if len(approvals.Pending()) > 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("the approval never became pending")
}

// focusedName returns the accessible name of the focused node.
func (h *harness) focusedName() string {
	for _, info := range h.tester.AccessAll() {
		if info.Focused {
			return info.Name
		}
	}
	return ""
}

// tabTo presses Tab until the focus lands on the node with this name.
func (h *harness) tabTo(name string) bool {
	for i := 0; i < 40; i++ {
		if h.focusedName() == name {
			return true
		}
		h.tester.Key(0, ui.KeyTab)
		h.frame()
	}
	return h.focusedName() == name
}

// tabSequence records the focused names of one full Tab pass.
func (h *harness) tabSequence(limit int) []string {
	var names []string
	for i := 0; i < limit; i++ {
		h.tester.Key(0, ui.KeyTab)
		h.frame()
		name := h.focusedName()
		if name == "" {
			name = "<none>"
		}
		names = append(names, name)
	}
	return names
}

// findRect returns the first text's box.
func (h *harness) findRect(label string) (ui.Rect, bool) { return h.tester.Find(label) }

// snapshot writes the frame to outDir/name.png.
func (h *harness) snapshot(name string) (string, error) {
	img := h.tester.Image()
	if img == nil {
		return "", fmt.Errorf("no frame image")
	}
	if err := os.MkdirAll(h.outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(h.outDir, name+".png")
	if err := writePNG(path, img); err != nil {
		return "", err
	}
	return path, nil
}

// goos names the platform in the report.
func goos() string { return runtime.GOOS }
