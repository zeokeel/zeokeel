package session_test

// Dropped window files stage as composer attachments (the same path as the
// native picker).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type fakeFileDrop struct{ handler func(paths []string) }

func (f *fakeFileDrop) SetHandler(fn func(paths []string)) { f.handler = fn }
func (f *fakeFileDrop) Dispatch(paths []string) {
	if f.handler != nil {
		f.handler(paths)
	}
}

func TestDroppedFileStagesAttachment(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "拖放"}, true)
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	drop := &fakeFileDrop{}
	provide(t, ctx, contracts.KeyFileDrop, contracts.FileDropService(drop))
	activate(t, ctx, session.Module(), session.Config{})

	path := filepath.Join(t.TempDir(), "dropped.txt")
	if err := os.WriteFile(path, []byte("hello drop"), 0o600); err != nil {
		t.Fatalf("write dropped file: %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	drop.Dispatch([]string{path})
	tester.Frame()
	tester.Frame()
	if !tester.HasText("dropped.txt") {
		t.Fatalf("dropped attachment chip missing; texts = %v", tester.Texts())
	}
}
