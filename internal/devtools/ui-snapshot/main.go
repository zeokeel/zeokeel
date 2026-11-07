// Command ui-snapshot renders the Phase-1 product frames to PNG files for
// visual review. It is a development tool: headless, no window, no real model.
package main

import (
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

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

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好！我是 ZeoKeel，有什么可以帮你？\"},\"index\":0}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	provide(root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(root, contracts.KeyTheme, uiRoot.Theme())
	provide(root, contracts.KeyLocale, uiRoot.Locale())
	provide(root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(root, contracts.KeySettings, contracts.SettingsService(&settings{values: map[string]string{
		contracts.SettingsChatModel:   "snapshot-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}))
	provide(root, contracts.KeyLogger, contracts.Logger(logger{}))

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		fail("load", err)
	}
	defer func() { _ = loader.Close() }()

	tester := ui.NewTester(uiRoot.View, 1280, 800)
	render(tester, "/tmp/zeokeel-home.png", nil)
	ui := uiRoot
	_ = ui

	chat, err := root.Require(contracts.KeyChat)
	if err != nil {
		fail("chat", err)
	}
	sessionID, err := chat.NewSession("")
	if err != nil {
		fail("new session", err)
	}
	if err := chat.Send(sessionID, "你好，帮我介绍一下 ZeoKeel"); err != nil {
		fail("send", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := chat.Session(sessionID)
		if ok && !session.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := uiRoot.Navigator().Push("session"); err != nil {
		fail("push", err)
	}
	render(tester, "/tmp/zeokeel-session.png", nil)

	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	if err := tester.Click("模型"); err != nil {
		// The settings modal may already show the model page.
		fmt.Println("click model:", err)
	}
	render(tester, "/tmp/zeokeel-settings.png", nil)
}

func render(tester *ui.Tester, path string, edit func()) {
	tester.Frame()
	if edit != nil {
		edit()
		tester.Frame()
	}
	img := tester.Image()
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

func provide[T any](root cordis.Context, key cordis.ServiceKey[T], value T) {
	if _, err := root.Provide(key, value); err != nil {
		fail("provide "+key.Name(), err)
	}
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "ui-snapshot: %s: %v\n", step, err)
	os.Exit(1)
}
