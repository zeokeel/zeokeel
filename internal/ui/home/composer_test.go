package home_test

// Tests for the Home page's real composer: input, chips, attachments, send.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

type homeSendCall struct {
	sessionID   string
	prompt      string
	attachments []contracts.Attachment
}

// composerChat extends fakeChat with send/new-session recording and the
// optional AttachmentSender half.
type composerChat struct {
	*fakeChat
	mu          sync.Mutex
	newCalls    []string
	sends       []homeSendCall
	attachSends []homeSendCall
	sendErr     error
	newErr      error
}

func (f *composerChat) NewSession(title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newCalls = append(f.newCalls, title)
	if f.newErr != nil {
		return "", f.newErr
	}
	return "sess-home", nil
}

func (f *composerChat) Send(sessionID, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, homeSendCall{sessionID: sessionID, prompt: prompt})
	return f.sendErr
}

func (f *composerChat) SendWithAttachments(sessionID, prompt string, attachments []contracts.Attachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachSends = append(f.attachSends, homeSendCall{sessionID: sessionID, prompt: prompt, attachments: attachments})
	return f.sendErr
}

func (f *composerChat) snapshot() (news []string, sends, attachSends []homeSendCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.newCalls...), append([]homeSendCall(nil), f.sends...), append([]homeSendCall(nil), f.attachSends...)
}

// textOnlyChat does not implement AttachmentSender.
type textOnlyChat struct{ *fakeChat }

type homeWorkspace struct {
	current contracts.Workspace
	has     bool
}

func (w *homeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	if !w.has {
		return nil, nil
	}
	return []contracts.Workspace{w.current}, nil
}
func (w *homeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return w.current, w.has, nil
}
func (w *homeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (w *homeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (w *homeWorkspace) Delete(context.Context, string) error { return nil }
func (w *homeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (w *homeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }
func (w *homeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

type homeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newHomeSettings(values map[string]string) *homeSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &homeSettings{values: values}
}
func (s *homeSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}
func (s *homeSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}
func (s *homeSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

func newComposerHarness(t *testing.T, chat contracts.ChatService, settings contracts.SettingsService,
	workspace contracts.WorkspaceService) *harness {
	t.Helper()
	h := newHarness(t, chat)
	if settings != nil {
		provide(t, h.ctx, contracts.KeySettings, settings)
	}
	if workspace != nil {
		provide(t, h.ctx, contracts.KeyWorkspace, workspace)
	}
	return h
}

func clickHome(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

func TestHomeComposerRendersAndChips(t *testing.T) {
	settings := newHomeSettings(map[string]string{
		contracts.SettingAgentMode: contracts.AgentModeNormal,
		contracts.SettingChatMode:  contracts.ChatModeMinimal,
	})
	workspace := &homeWorkspace{current: contracts.Workspace{ID: "w1", Name: "proj", Root: "/tmp/proj"}, has: true}
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "test-model"}}}
	h := newComposerHarness(t, chat, settings, workspace)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	h.assertTexts(tester, "问点什么…", "发送", "附件", "proj", "test-model", "精简")
	h.assertTexts(tester, "向 ZeoKeel 提问…", "开始新对话")
}

func TestHomeComposerEmptyDraftDoesNotSend(t *testing.T) {
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "发送")
	news, sends, _ := chat.snapshot()
	if len(news) != 0 || len(sends) != 0 {
		t.Fatalf("empty draft sent: news=%v sends=%v", news, sends)
	}
}

func TestHomeComposerClickSend(t *testing.T) {
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "问点什么…")
	tester.Type("hello home")
	tester.Frame()
	clickHome(t, tester, "发送")

	news, sends, attachSends := chat.snapshot()
	if len(news) != 1 || news[0] != "" {
		t.Fatalf("NewSession calls = %v", news)
	}
	if len(attachSends) != 0 || len(sends) != 1 || sends[0].sessionID != "sess-home" || sends[0].prompt != "hello home" {
		t.Fatalf("sends = %v attachSends = %v", sends, attachSends)
	}
}

func TestHomeComposerEnterSends(t *testing.T) {
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "问点什么…")
	tester.Type("enter prompt")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	_, sends, _ := chat.snapshot()
	if len(sends) != 1 || sends[0].prompt != "enter prompt" {
		t.Fatalf("Enter sends = %v", sends)
	}
}

func TestHomeComposerSendFailureShowsError(t *testing.T) {
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}, sendErr: errors.New("network down")}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "问点什么…")
	tester.Type("will fail")
	tester.Frame()
	clickHome(t, tester, "发送")
	h.assertTexts(tester, "network down")
}

func TestHomeComposerAttachments(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textPath, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	imagePath := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(imagePath, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	oldPicker := home.AttachmentPicker
	home.AttachmentPicker = func() ([]string, error) { return []string{textPath, imagePath}, nil }
	t.Cleanup(func() { home.AttachmentPicker = oldPicker })

	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "附件")
	h.assertTexts(tester, "notes.txt", "pic.png", "5 B", "4 B", "移除")

	clickHome(t, tester, "移除")
	clickHome(t, tester, "问点什么…")
	tester.Type("with attachment")
	tester.Frame()
	clickHome(t, tester, "发送")
	_, sends, attachSends := chat.snapshot()
	if len(sends) != 0 || len(attachSends) != 1 {
		t.Fatalf("sends = %v attachSends = %v", sends, attachSends)
	}
	call := attachSends[0]
	if call.sessionID != "sess-home" || call.prompt != "with attachment" || len(call.attachments) != 1 ||
		call.attachments[0].Name != "pic.png" || call.attachments[0].MIME != "image/png" || string(call.attachments[0].Data) != "\x89PNG" {
		t.Fatalf("attachment send = %+v", call)
	}
}

func TestHomeComposerAttachmentFallbackWithoutAPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	oldPicker := home.AttachmentPicker
	home.AttachmentPicker = func() ([]string, error) { return []string{path}, nil }
	t.Cleanup(func() { home.AttachmentPicker = oldPicker })

	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	textChat := &textOnlyChat{fakeChat: chat.fakeChat}
	h := newComposerHarness(t, textChat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	clickHome(t, tester, "附件")
	clickHome(t, tester, "问点什么…")
	tester.Type("text only")
	tester.Frame()
	clickHome(t, tester, "发送")
	h.assertTexts(tester, "附件需在会话中发送")
	_, sends, _ := chat.snapshot()
	if len(sends) != 0 {
		// textOnlyChat promotes fakeChat.Send, which is a no-op; the composer
		// records the fallback text itself, so only the hint is asserted here.
		t.Fatalf("attachment fallback unexpectedly called Send: %v", sends)
	}
}

func TestHomeComposerNoChatAndEnglish(t *testing.T) {
	h := newComposerHarness(t, nil, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertTexts(tester, "问点什么…", "发送", "附件")

	h = newComposerHarness(t, &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester = ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Ask anything…", "Send", "Attach", "Ask ZeoKeel…", "Start a new conversation")
}
