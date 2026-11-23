package home_test

// Tests for the Home page's real composer: input, chips, attachments, send.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
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
	activeIDs   []string
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

func (f *composerChat) SetActiveSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeIDs = append(f.activeIDs, sessionID)
	return nil
}

func (f *composerChat) activeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.activeIDs...)
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

// clickHomeAttachment opens the composer's attach control, which starts the
// native picker directly (P2-12 removed the 选择文件…/手动输入路径… menu); the
// picker runs off the UI thread, so callers drive frames for the posted result.
func clickHomeAttachment(t *testing.T, tester *ui.Tester) {
	t.Helper()
	clickHome(t, tester, "附件")
}

// chooseHomeAttachmentFiles clicks 附件; the native picker then runs off the UI
// thread, so callers drive frames for the posted result.
func chooseHomeAttachmentFiles(t *testing.T, tester *ui.Tester) {
	t.Helper()
	clickHomeAttachment(t, tester)
}

// assertHomeBottomRow checks the redesigned single action row: the 附件 menu, the
// model and mode chips and Send share one row below the input, so the removed
// separate context row above the input cannot quietly come back.
func assertHomeBottomRow(t *testing.T, tester *ui.Tester) {
	t.Helper()
	input, ok := tester.Find("问点什么…")
	if !ok {
		t.Fatalf("composer input missing; texts = %v", tester.Texts())
	}
	row, ok := tester.Find("附件")
	if !ok {
		t.Fatalf("attach menu missing; texts = %v", tester.Texts())
	}
	if row.Y <= input.Y {
		t.Fatalf("attach menu y=%.1f is not below the input y=%.1f", row.Y, input.Y)
	}
	for _, label := range []string{"test-model", "精简", "发送", "proj"} {
		element, ok := tester.Find(label)
		if !ok {
			t.Fatalf("%q missing from the bottom row; texts = %v", label, tester.Texts())
		}
		// The action row is center-aligned: the shared send control (icon plus
		// label) is taller than the quiet menu chips, so its top edge sits a few
		// DIP above theirs while the row centers still match. Before the shared
		// ComposerShell the send control had no glyph and its top lined up within
		// 8 DIP; the unified control keeps the glyph, so allow the half-height
		// difference of the two targets instead.
		if diff := element.Y - row.Y; diff < -12 || diff > 12 {
			t.Fatalf("%q y=%.1f is not on the action row (y=%.1f); texts = %v",
				label, element.Y, row.Y, tester.Texts())
		}
	}
}

// waitHome drives frames until every wanted text is visible. The native picker
// runs off the UI thread and publishes through UIRuntime.Post, so its result
// lands on a later frame than the click.
func waitHome(t *testing.T, tester *ui.Tester, wanted ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		missing := ""
		for _, want := range wanted {
			if !tester.HasText(want) {
				missing = want
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("frame does not show %q (wanted %v); texts = %v", missing, wanted, tester.Texts())
		}
		time.Sleep(2 * time.Millisecond)
		tester.Frame()
	}
}

// settleHome drives frames for a short while so a no-op background picker has
// had its chance to publish before an absence is asserted.
func settleHome(tester *ui.Tester) {
	for i := 0; i < 25; i++ {
		time.Sleep(2 * time.Millisecond)
		tester.Frame()
	}
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

	h.assertTexts(tester, "问点什么…", "发送", "附件", "test-model", "精简", "proj")
	// The single bottom action row carries the workspace context chip next to
	// the model/mode pickers; the old top context row, the Enter hint and the
	// standalone 路径 button stay gone.
	assertHomeBottomRow(t, tester)
	h.assertNoTexts(tester, "Enter 发送", "Shift+Enter", "路径", "开始新对话", "新对话")
	// The removed attach menu and manual path dialog never render: the attach
	// control opens the picker directly.
	h.assertNoTexts(tester, "选择文件…", "手动输入路径…", "手动添加附件", "文件路径")
	// With a model configured and no conversations, the empty state offers the
	// first conversation.
	h.assertTexts(tester, "新建会话")
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
	// One click on 附件 opens the native picker; the picked files stage as chips.
	h.assertNoTexts(tester, "选择文件…", "手动输入路径…", "手动添加附件")
	clickHomeAttachment(t, tester)
	waitHome(t, tester, "notes.txt", "pic.png", "5 B", "4 B", "移除附件 notes.txt")

	clickHome(t, tester, "移除附件 notes.txt")
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
	chooseHomeAttachmentFiles(t, tester)
	waitHome(t, tester, "a.txt")
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
	h.assertTexts(tester, "Ask anything…", "Send", "Attach")
	// The attach control opens the picker directly: the removed menu items never
	// render in either language.
	h.assertNoTexts(tester, "Choose files…", "Enter a path manually…", "Add attachments by path")
}

// TestHomeContextControlsSwitchModelAndMode covers H13's interactive context
// line: the model picker writes the default chat model and the mode picker
// writes the agent/chat mode pair, without leaving Home.
func TestHomeContextControlsSwitchModelAndMode(t *testing.T) {
	settings := newHomeSettings(map[string]string{
		contracts.SettingAgentMode: contracts.AgentModeNormal,
		contracts.SettingChatMode:  contracts.ChatModeStandard,
	})
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "test-model"}}}
	h := newComposerHarness(t, chat, settings, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	// Without a model catalog the model control deep-links to the model
	// settings page.
	if err := tester.Click("test-model"); err != nil {
		t.Fatalf("open model menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "打开模型设置…")
	clickHome(t, tester, "打开模型设置…")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("model control did not open the settings surface")
	}
	h.ui.Navigator().CloseSettings()
	tester.Frame()

	// The mode picker switches between the chat-mode presets.
	if err := tester.Click("普通"); err != nil {
		t.Fatalf("open mode menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "计划", "精简", "创作")
	clickHome(t, tester, "精简")
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeMinimal {
		t.Fatalf("chat mode = %q, want minimal", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent mode = %q, want normal", got)
	}
	h.assertTexts(tester, "精简")
}

// TestHomeContextModelMenuFromCatalog covers the model catalog path: the menu
// lists presets and choosing one persists the default model.
func TestHomeContextModelMenuFromCatalog(t *testing.T) {
	settings := newHomeSettings(nil)
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "test-model"}}}
	h := newComposerHarness(t, chat, settings, nil)
	provide(t, h.ctx, contracts.KeyModelCatalog, contracts.ModelCatalogService(fakeModelCatalog{}))
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	if err := tester.Click("test-model"); err != nil {
		t.Fatalf("open model menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "模型")
	clickHome(t, tester, "模型")
	h.assertTexts(tester, "自定义服务", "DeepSeek Chat", "Qwen Coder")
	clickHome(t, tester, "Qwen Coder")
	if got := settings.Get(contracts.SettingsChatModel, ""); got != "qwen2.5-coder" {
		t.Fatalf("selected model = %q, want qwen2.5-coder", got)
	}
}

// fakeModelCatalog is the two-preset model catalog for the Home model menu test.
type fakeModelCatalog struct{}

func (fakeModelCatalog) Models() []contracts.ModelInfo {
	return []contracts.ModelInfo{
		{ID: "deepseek-chat", Name: "DeepSeek Chat"},
		{ID: "qwen2.5-coder", Name: "Qwen Coder"},
	}
}

// TestHomeRecentSessionsExcludeArchivedAndOpen covers H13's recent list: up to
// five newest visible conversations, archived ones excluded, clicking one
// activates and opens it.
func TestHomeRecentSessionsExcludeArchivedAndOpen(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := &composerChat{fakeChat: &fakeChat{
		config: contracts.ChatConfig{Model: "test-model"},
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "最近会话一", UpdatedAt: base.Add(3 * time.Hour)},
			{ID: "s2", Title: "第二会话", UpdatedAt: base.Add(2 * time.Hour)},
			{ID: "s3", Title: "归档会话", UpdatedAt: base.Add(time.Hour), Archived: true},
		},
	}}
	h := newComposerHarness(t, chat, nil, nil)
	if _, err := h.ui.Runtime().RegisterScreen(uinative.ScreenSpec{
		ID: "session", Title: "会话", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "会话屏幕") },
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	activate(t, h, home.Module(), home.Config{})
	// The product now lands on the conversation surface; Home is an explicit
	// destination for the embeddable screen, so open it before asserting.
	if err := h.ui.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	h.assertTexts(tester, "最近会话", "最近会话一", "第二会话")
	h.assertNoTexts(tester, "归档会话")
	clickHome(t, tester, "最近会话一")
	if got := chat.activeCalls(); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("SetActiveSession calls = %v, want [s1]", got)
	}
	h.assertTexts(tester, "会话屏幕")
}

func TestHomeComposerAttachmentPickerOffThreadBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	release := make(chan struct{})
	oldPicker := home.AttachmentPicker
	home.AttachmentPicker = func() ([]string, error) {
		<-release
		return []string{path}, nil
	}
	t.Cleanup(func() { home.AttachmentPicker = oldPicker })

	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	chooseHomeAttachmentFiles(t, tester)
	// The picker is still blocked in its goroutine: the frame must show the
	// busy label on the 附件 button instead of freezing (a synchronous picker
	// would hang).
	h.assertTexts(tester, "选择中…")
	if info, ok := tester.AccessByRole(ui.RoleProgress, "选择中…"); !ok || info.Role != ui.RoleProgress {
		t.Fatalf("home picker busy state is not a progress indicator: %+v found %v", info, ok)
	}
	h.assertNoTexts(tester, "选择文件…")
	h.assertNoTexts(tester, "notes.txt") // the result is still pending
	close(release)
	waitHome(t, tester, "notes.txt")
	h.assertNoTexts(tester, "选择中…")
}

// TestHomeComposerAttachButtonOpensPickerDirectly covers the P2-12 entry: one
// click on 附件 starts the native picker itself, with no 选择文件…/手动输入路径…
// menu and no manual path dialog, and the picked file stages as a chip.
func TestHomeComposerAttachButtonOpensPickerDirectly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	calls := 0
	oldPicker := home.AttachmentPicker
	home.AttachmentPicker = func() ([]string, error) {
		calls++
		return []string{path}, nil
	}
	t.Cleanup(func() { home.AttachmentPicker = oldPicker })

	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	for _, gone := range []string{"选择文件…", "手动输入路径…", "手动添加附件", "文件路径"} {
		if tester.HasText(gone) {
			t.Fatalf("frame still shows %q; texts = %v", gone, tester.Texts())
		}
	}
	clickHomeAttachment(t, tester)
	waitHome(t, tester, "direct.txt")
	if calls != 1 {
		t.Fatalf("picker calls = %d, want 1", calls)
	}
	h.assertNoTexts(tester, "选择文件…", "手动输入路径…", "手动添加附件")
}

// TestHomeComposerAttachPickerUnavailableExplains covers a picker that cannot
// open: Home reports the reason and the drag/paste paths instead of asking for a
// typed path, and the next click retries the picker.
func TestHomeComposerAttachPickerUnavailableExplains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retry.txt")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	oldPicker := home.AttachmentPicker
	home.AttachmentPicker = func() ([]string, error) { return nil, errors.New("no file dialog") }
	t.Cleanup(func() { home.AttachmentPicker = oldPicker })

	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	clickHomeAttachment(t, tester)
	waitHome(t, tester, "无法打开文件选择器：no file dialog", "请拖入文件或粘贴图片")
	h.assertNoTexts(tester, "手动输入路径…", "手动添加附件", "文件路径")
	h.assertTexts(tester, "附件")

	home.AttachmentPicker = func() ([]string, error) { return []string{path}, nil }
	clickHomeAttachment(t, tester)
	waitHome(t, tester, "retry.txt")
	h.assertNoTexts(tester, "无法打开文件选择器")
}

// TestHomeComposerNarrowKeepsSend covers the narrow windows (900/1024): the
// toolbar fits its controls inside the composer card and sending still works
// with a typed draft.
func TestHomeComposerNarrowKeepsSend(t *testing.T) {
	for _, width := range []int{900, 1024} {
		chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
		h := newComposerHarness(t, chat, nil, nil)
		activate(t, h, home.Module(), home.Config{})
		tester := ui.NewTester(h.ui.View, width, 800)
		tester.Frame()
		tester.Frame()

		bar, ok := tester.AccessByRole(ui.RoleToolbar, "输入框工具栏")
		if !ok {
			t.Fatalf("%d: Home composer toolbar missing; texts = %v", width, tester.Texts())
		}
		if bar.Bounds.X < 0 || bar.Bounds.X+bar.Bounds.W > float32(width) {
			t.Fatalf("%d: toolbar at %.1f..%.1f escaped the window", width, bar.Bounds.X, bar.Bounds.X+bar.Bounds.W)
		}
		send, ok := tester.AccessByRole(ui.RoleButton, "发送")
		if !ok {
			t.Fatalf("%d: send control missing; texts = %v", width, tester.Texts())
		}
		if send.Bounds.X < 0 || send.Bounds.X+send.Bounds.W > float32(width) {
			t.Fatalf("%d: send at %.1f..%.1f escaped the window", width, send.Bounds.X, send.Bounds.X+send.Bounds.W)
		}
		clickHome(t, tester, "发送")
		if _, sends, _ := chat.snapshot(); len(sends) != 0 {
			t.Fatalf("%d: an empty draft sent %v", width, sends)
		}

		clickHome(t, tester, "问点什么…")
		tester.Type("narrow home")
		tester.Frame()
		clickHome(t, tester, "发送")
		if _, sends, _ := chat.snapshot(); len(sends) != 1 || sends[0].prompt != "narrow home" {
			t.Fatalf("%d: send calls = %v, want the typed draft", width, sends)
		}
	}
}
