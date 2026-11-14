package uikit_test

// Headless tests for the shared composer shell (uikit.ComposerShell): both the
// conversation screen and Home use it, so the tests pin the unified card, the
// width policy, the input's key semantics (Enter/Cmd+Enter send, Shift+Enter
// newline, Esc interrupts after the surface's menus had their chance), the
// send/queue/stop position and the slots.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// composerTestState is the caller-side state the shell options point at.
type composerTestState struct {
	draft   string
	sent    int
	queued  int
	stopped int
	focus   bool
	menu    bool // a surface-owned menu that owns the first Esc
	err     string
}

// composerTestView draws one shell on a page background, with the surface
// callbacks recording into st. customize may add slots/callbacks.
func composerTestView(st *composerTestState, customize func(*uikit.ComposerShellOptions)) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		opts := uikit.ComposerShellOptions{
			Key:         "test.composer",
			Draft:       &st.draft,
			Placeholder: "输入消息…",
			SendEnabled: strings.TrimSpace(st.draft) != "",
			Send: func() bool {
				if strings.TrimSpace(st.draft) == "" {
					return false
				}
				st.sent++
				return true
			},
			Focus: &st.focus,
			Error: st.err,
		}
		if customize != nil {
			customize(&opts)
		}
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).Children(func() {
			uikit.ComposerShell(c, opts)
		})
	}
}

// TestComposerEnterSends covers the primary key semantics: Enter sends the draft
// and the input is cleared; Shift+Enter reaches the input as a newline and
// Cmd/Ctrl+Enter sends like Enter (the DSH/Codex habit).
func TestComposerEnterSends(t *testing.T) {
	st := &composerTestState{focus: true}
	tt := ui.NewTester(composerTestView(st, nil), 900, 400)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleTextField, "输入消息…"); !ok {
		t.Fatalf("composer input has no accessible label; texts = %v", tt.Texts())
	}
	if err := tt.Click("输入消息…"); err != nil {
		t.Fatalf("focus input: %v", err)
	}
	tt.Type("hello")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if st.sent != 1 {
		t.Fatalf("Enter sent %d times, want 1", st.sent)
	}
	if st.draft != "" {
		t.Fatalf("Enter left the draft %q, want empty", st.draft)
	}

	// Shift+Enter is a newline: no send.
	tt.Type("line one")
	tt.Frame()
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Frame()
	if st.sent != 1 {
		t.Fatalf("Shift+Enter sent the draft (sent=%d)", st.sent)
	}
	if !strings.Contains(st.draft, "line one") {
		t.Fatalf("Shift+Enter lost the draft: %q", st.draft)
	}

	// Cmd+Enter sends.
	tt.Key(ui.Cmd, ui.KeyEnter)
	tt.Frame()
	if st.sent != 2 {
		t.Fatalf("Cmd+Enter sent %d times, want 2", st.sent)
	}
}

// TestComposerEscapeInterruptsAfterMenus covers the menu-first interrupt: the
// surface's OnKey owns an open menu, and only an Esc that the menu did not
// consume stops the running turn.
func TestComposerEscapeInterruptsAfterMenus(t *testing.T) {
	st := &composerTestState{focus: true}
	tt := ui.NewTester(composerTestView(st, func(o *uikit.ComposerShellOptions) {
		o.Running = true
		o.Stop = func() { st.stopped++ }
		o.QueueSend = func() bool { st.queued++; return true }
		o.QueueEnabled = true
		o.OnKey = func(k ui.TextKey) bool {
			if k.Key == ui.KeyEscape && k.Mods == 0 && st.menu {
				st.menu = false
				return true
			}
			return false
		}
	}), 900, 400)
	tt.Frame()
	if err := tt.Click("输入消息…"); err != nil {
		t.Fatalf("focus input: %v", err)
	}
	st.menu = true
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if st.stopped != 0 {
		t.Fatalf("Esc closed the menu but also stopped the turn")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if st.stopped != 1 {
		t.Fatalf("Esc did not stop the running turn (stopped=%d)", st.stopped)
	}
}

// TestComposerQueueDuringRun covers the running composer: the send position
// becomes 排队 + 停止, Enter queues the draft (never sends it), and the stop
// control interrupts the turn.
func TestComposerQueueDuringRun(t *testing.T) {
	st := &composerTestState{focus: true, draft: "next question"}
	tt := ui.NewTester(composerTestView(st, func(o *uikit.ComposerShellOptions) {
		o.Running = true
		o.QueueSend = func() bool { st.queued++; st.draft = ""; return true }
		o.QueueEnabled = true
		o.Stop = func() { st.stopped++ }
	}), 900, 400)
	tt.Frame()
	if st.sent != 0 {
		t.Fatalf("a running composer sent the draft instead of queueing it")
	}
	if err := tt.Click("输入消息…"); err != nil {
		t.Fatalf("focus input: %v", err)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "停止"); !ok {
		t.Fatalf("running composer has no stop control; texts = %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if st.queued != 1 || st.sent != 0 {
		t.Fatalf("Enter queued=%d sent=%d, want 1/0", st.queued, st.sent)
	}
	if err := tt.Click("停止"); err != nil {
		t.Fatalf("click stop: %v", err)
	}
	tt.Frame()
	if st.stopped != 1 {
		t.Fatalf("stop control did not interrupt the turn (stopped=%d)", st.stopped)
	}
}

// TestComposerSlotsAndWidth covers the slots and the unified width policy: the
// card is design.TranscriptWidth on a wide window and capped at 92% on a narrow
// one, the toolbar/status/attachments/error slots build, and the hint is the
// surface's ("chip folding" is the surface deciding via OnWidth).
func TestComposerSlotsAndWidth(t *testing.T) {
	st := &composerTestState{focus: true, err: "发送失败：网络"}
	queueOpen := false
	var width float32
	view := composerTestView(st, func(o *uikit.ComposerShellOptions) {
		o.OnWidth = func(w float32) { width = w }
		o.Attachments = func(c *ui.Context) { ui.Text(c, "附件：a.txt") }
		o.Toolbar = func(c *ui.Context) {
			ui.Button(c, "附件").Label("附件")
			ui.Button(c, "模型").Label("模型")
		}
		o.Queue = []string{"排队中的消息"}
		o.QueueOpen = &queueOpen
		o.Errors = []string{"第二条错误"}
		o.Hint = func(*ui.Context) string {
			if width > 0 && width < design.TranscriptWidth {
				return "" // a narrow composer folds the hint into the send tooltip
			}
			return "Enter 发送"
		}
	})
	wide := ui.NewTester(view, 1200, 400)
	wide.Frame()
	if width != design.TranscriptWidth {
		t.Fatalf("wide composer width = %v, want the transcript width %v", width, design.TranscriptWidth)
	}
	for _, want := range []string{"附件：a.txt", "模型", "已排队 1 条", "发送失败：网络", "第二条错误", "Enter 发送"} {
		if !wide.HasText(want) {
			t.Fatalf("composer slot %q missing; texts = %v", want, wide.Texts())
		}
	}
	if _, ok := wide.AccessByRole(ui.RoleAlert, ""); !ok {
		t.Fatalf("composer error row is not an alert; texts = %v", wide.Texts())
	}

	narrow := ui.NewTester(view, 700, 400)
	narrow.Frame()
	if width >= design.TranscriptWidth || width <= 0 {
		t.Fatalf("narrow composer width = %v, want below the transcript width", width)
	}
	if narrow.HasText("Enter 发送") {
		t.Fatalf("narrow composer still shows the wide hint; texts = %v", narrow.Texts())
	}
}

// TestComposerQueueStatus covers the shell's queue area: the shared queue badge
// (RunStatus) with its popover and per-row cancel, plus the delivery error row.
func TestComposerQueueStatus(t *testing.T) {
	st := &composerTestState{focus: true}
	queueOpen := false
	cancelled := 0
	tt := ui.NewTester(composerTestView(st, func(o *uikit.ComposerShellOptions) {
		o.Running = true
		o.Queue = []string{"第一条排队消息", "第二条排队消息"}
		o.QueueOpen = &queueOpen
		o.QueueList = &ui.ListState{}
		o.QueueError = "排队发送失败：超时"
		o.OnQueueCancel = func(index int) { cancelled += index + 1 }
		o.OnQueueClear = func() {}
		o.Stop = func() {}
	}), 900, 500)
	tt.Frame()
	if !tt.HasText("已排队 2 条") {
		t.Fatalf("composer queue badge missing; texts = %v", tt.Texts())
	}
	if !tt.HasText("排队发送失败：超时") {
		t.Fatalf("composer queue error missing; texts = %v", tt.Texts())
	}
	if tt.HasText("第一条排队消息") {
		t.Fatalf("queue rows leak before the badge opens; texts = %v", tt.Texts())
	}
	if err := tt.Click("已排队 2 条"); err != nil {
		t.Fatalf("open queue popover: %v", err)
	}
	tt.Frame()
	if !queueOpen || !tt.HasText("第一条排队消息") {
		t.Fatalf("queue popover did not open; texts = %v", tt.Texts())
	}
	if err := tt.Click("取消排队"); err != nil {
		t.Fatalf("cancel queued prompt: %v", err)
	}
	tt.Frame()
	if cancelled != 1 {
		t.Fatalf("queue cancel calls = %d, want 1", cancelled)
	}
}

// composerInContainer draws the shell inside a fixed-width container, capturing
// the card's laid-out width, so the two width policies can be compared.
func composerInContainer(containerWidth float32, fullWidth bool, capture *float32) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		draft := ""
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).Children(func() {
			ui.Row(c).Width(containerWidth).Children(func() {
				uikit.ComposerShell(c, uikit.ComposerShellOptions{
					Key:         "test.fullwidth",
					Draft:       &draft,
					Placeholder: "输入消息…",
					FullWidth:   fullWidth,
					OnWidth:     func(width float32) { *capture = width },
				})
			})
		})
	}
}

// TestComposerFullWidthFillsContainer covers the width policy: FullWidth fills
// the caller's container up to the reading width (Home's 720 column), while the
// default caps the card at 92% of the container (the conversation composer's
// narrow-window margin) and never exceeds the reading width.
func TestComposerFullWidthFillsContainer(t *testing.T) {
	const container float32 = 500
	var fullWidthCard float32
	full := ui.NewTester(composerInContainer(container, true, &fullWidthCard), 900, 400)
	full.Frame()
	if diff := fullWidthCard - container; diff < -1 || diff > 1 {
		t.Fatalf("FullWidth card = %v, want the container width %v", fullWidthCard, container)
	}
	if fullWidthCard > design.TranscriptWidth+1 {
		t.Fatalf("FullWidth card = %v, over the reading width %v", fullWidthCard, design.TranscriptWidth)
	}

	var cappedCard float32
	capped := ui.NewTester(composerInContainer(container, false, &cappedCard), 900, 400)
	capped.Frame()
	wantCapped := container * 0.92
	if diff := cappedCard - wantCapped; diff < -1 || diff > 1 {
		t.Fatalf("default card = %v, want 92%% of the container %v", cappedCard, wantCapped)
	}
	if cappedCard >= fullWidthCard {
		t.Fatalf("default card %v is not narrower than the FullWidth card %v", cappedCard, fullWidthCard)
	}
}

// TestComposerFocusOption covers the one-shot Focus flag: the caller asks for the
// keyboard and the shell focuses the input and clears the flag.
func TestComposerFocusOption(t *testing.T) {
	st := &composerTestState{focus: true}
	tt := ui.NewTester(composerTestView(st, nil), 900, 400)
	tt.Frame()
	if _, focused := tt.AccessByRole(ui.RoleTextField, "输入消息…"); !focused {
		t.Fatalf("input not found; texts = %v", tt.Texts())
	}
	info, _ := tt.AccessByRole(ui.RoleTextField, "输入消息…")
	if !info.Focused {
		t.Fatalf("Focus option did not focus the input: %+v", info)
	}
	if st.focus {
		t.Fatal("Focus flag was not cleared after being honored")
	}
}

// TestComposerRefocusKeepsScroll covers the v0.9.0 Focus reveal behavior: Focus
// now scrolls the element's containers as little as shows it (mygo ui/input.go).
// The shell's one-shot Focus flag hands the keyboard back to the input after an
// accepted send; when the composer is already in view, that programmatic focus
// must not move the view the user has scrolled.
func TestComposerRefocusKeepsScroll(t *testing.T) {
	st := &composerTestState{draft: "hello"}
	tt := ui.NewTester(func(c *ui.Context) {
		tokens := design.Of(c)
		ui.Scroll(c).FillWidth().FillHeight().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for i := 0; i < 40; i++ {
					design.Apply(ui.Text(c, "filler-"+strconv.Itoa(i)),
						tokens.TextMuted(design.TypeXS)).Height(20)
				}
				uikit.ComposerShell(c, uikit.ComposerShellOptions{
					Key:         "test.composer",
					Draft:       &st.draft,
					Placeholder: "输入消息…",
					SendEnabled: true,
					Send: func() bool {
						st.sent++
						return true
					},
					Focus: &st.focus,
				})
			})
		})
	}, 900, 400)
	tt.Frame()
	// The composer sits below the tall filler; scroll so it is in view.
	tt.Scroll(450, 300, 0, 500)
	tt.Frame()
	before, ok := tt.AccessByRole(ui.RoleTextField, "输入消息…")
	if !ok {
		t.Fatalf("composer input missing; texts = %v", tt.Texts())
	}
	if bottom := before.Bounds.Y + before.Bounds.H; bottom > 400 {
		t.Fatalf("composer input is not in view after scrolling: bottom = %v (bounds %+v)", bottom, before.Bounds)
	}

	// Clicking send moves the focus to the control; the shell then hands it back
	// to the input on the next frame.
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("click send: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if st.sent != 1 {
		t.Fatalf("send calls = %d, want 1", st.sent)
	}
	after, _ := tt.AccessByRole(ui.RoleTextField, "输入消息…")
	if !after.Focused {
		t.Fatalf("focus did not return to the composer input: %+v", after)
	}
	if after.Bounds != before.Bounds {
		t.Fatalf("refocusing the composer moved the view: input at %+v, before %+v", after.Bounds, before.Bounds)
	}
}
