package rightpanel_test

// Frame tests for the inspector fix round: the on-accent foreground of a
// selected team/task row, the Changes cache invalidation (Refresh and workspace
// switch), the Files list's choose-vs-open split, the task add row's Enter path,
// the Files refresh busy state, the Log pane's header refresh + throttle-aware
// background read, and the activity timeline's no-wrap clock.

import (
	"context"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// pumpFrames runs frames until condition reports true or the timeout passes;
// the off-thread loads land through UIRuntime.Post, so a frame per tick.
func pumpFrames(tester *ui.Tester, condition func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		tester.Frame()
		tester.Advance(20 * time.Millisecond)
		if condition() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// nearPixelCount counts the pixels of one region within tolerance of the target
// color: the selected-row foreground is a token color the canvas paints, and
// the tester has no color getter for a Text element.
func nearPixelCount(tester *ui.Tester, region ui.Rect, target ui.Color, tolerance uint8) int {
	image := tester.Image()
	left := int(region.X)
	top := int(region.Y)
	right := int(region.X + region.W)
	bottom := int(region.Y + region.H)
	count := 0
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			got := image.RGBAAt(x, y)
			if within(got.R, target.R, tolerance) && within(got.G, target.G, tolerance) &&
				within(got.B, target.B, tolerance) {
				count++
			}
		}
	}
	return count
}

// colorOf converts a ui.Color token to the image color the canvas paints.
func colorOf(value ui.Color) color.RGBA {
	return color.RGBA{R: value.R, G: value.G, B: value.B, A: value.A}
}

func within(got, want, tolerance uint8) bool {
	if got > want {
		return got-want <= tolerance
	}
	return want-got <= tolerance
}

// rowRegion returns a sub-rectangle of one list row.
func rowRegion(row ui.Rect, xFraction, widthFraction, yFraction, heightFraction float32) ui.Rect {
	return ui.Rect{
		X: row.X + row.W*xFraction,
		Y: row.Y + row.H*yFraction,
		W: row.W * widthFraction,
		H: row.H * heightFraction,
	}
}

// requireListItem returns the chosen row's accessible box.
func requireListItem(t *testing.T, tester *ui.Tester, name string) ui.Rect {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleListItem, name)
	if !ok {
		t.Fatalf("list row %q is not exposed; texts = %v", name, tester.Texts())
	}
	return info.Bounds
}

// TestTeamSelectedRowUsesOnAccentForeground pins PN-01: the native List paints
// the chosen row in the accent fill, so the row's name/activity/meta must be
// painted with the on-accent foreground (Tokens.AccentText), not the resting
// label colors. The tester has no color getter for a Text element, so the frame
// is sampled: counting pure-white glyph pixels in the row's text band separates
// the two states.
func TestTeamSelectedRowUsesOnAccentForeground(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")

	row := requireListItem(t, tester, "研究员")
	// The row is painted in the accent fill (the List's chosen row)...
	if got := tester.Image().RGBAAt(int(row.X+4), int(row.Y+4)); got != colorOf(design.Light().Accent) {
		t.Fatalf("chosen row background = %v, want the accent fill %v", got, design.Light().Accent)
	}
	// ...and its content must switch away from the resting label ink: no
	// near-LabelPrimary glyphs remain anywhere in the row, while the on-accent
	// foreground (plus the inverted badge) paints.
	dark := nearPixelCount(tester, row, design.Light().LabelPrimary, 24)
	light := nearPixelCount(tester, row, design.Light().AccentText, 12)
	if dark > 20 {
		t.Fatalf("selected teammate row still paints %d resting-ink glyph pixels, want the AccentText foreground",
			dark)
	}
	if light < 40 {
		t.Fatalf("selected teammate row paints only %d on-accent pixels, want the inverted foreground", light)
	}

	// The status badge inverts on the selected row (uikit.OnAccentBadge): its
	// label is still present and readable.
	h.assertTexts(tester, "已完成")
}

// TestTaskSelectedRowUsesOnAccentForeground is the same contract for the task
// board rows (PN-01's task half): subject, owner line and the ⋯ glyph action
// switch to the on-accent foreground on the chosen row.
func TestTaskSelectedRowUsesOnAccentForeground(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "发布前检查", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	})
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	h.assertTexts(tester, "发布前检查")
	if err := tester.Click("发布前检查"); err != nil {
		t.Fatalf("choose task row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()

	row := requireListItem(t, tester, "发布前检查")
	if got := tester.Image().RGBAAt(int(row.X+4), int(row.Y+4)); got != colorOf(design.Light().Accent) {
		t.Fatalf("chosen task row background = %v, want the accent fill %v", got, design.Light().Accent)
	}
	dark := nearPixelCount(tester, row, design.Light().LabelPrimary, 24)
	light := nearPixelCount(tester, row, design.Light().AccentText, 12)
	if dark > 20 {
		t.Fatalf("selected task row still paints %d resting-ink glyph pixels, want the AccentText foreground", dark)
	}
	if light < 40 {
		t.Fatalf("selected task row paints only %d on-accent pixels, want the inverted foreground", light)
	}
}

// diffPatchOf returns a parsable unified patch whose added line carries tag, so
// a changed workspace file produces a distinguishable viewer text.
func diffPatchOf(tag string) string {
	return "diff --git a/a.txt b/a.txt\n" +
		"--- a/a.txt\n" +
		"+++ b/a.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-old-" + tag + "\n" +
		"+new-" + tag + "\n" +
		" context line\n"
}

// TestChangesRefreshReloadsDiff pins PN-02: the pane header's Refresh must drop
// the cached official diff viewers, or the refreshed rows keep showing the diff
// content they cached before the refresh.
func TestChangesRefreshReloadsDiff(t *testing.T) {
	workspace := newDifferWorkspace(contracts.FileChange{
		Path: "a.txt", Status: "M", Diff: diffPatchOf("A"),
	})
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "变更")
	if !pumpFrames(tester, func() bool { return tester.HasText("查看 diff") }, 3*time.Second) {
		t.Fatalf("changes did not load; texts = %v", tester.Texts())
	}
	if err := tester.Click("查看 diff"); err != nil {
		t.Fatalf("expand diff: %v", err)
	}
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	before, ok := tester.Access("New text")
	if !ok || !strings.Contains(before.Value, "new-A") {
		t.Fatalf("initial diff viewer = %+v found %v, want new-A", before, ok)
	}

	// The workspace content changes; the user presses Refresh.
	workspace.setChanges(contracts.FileChange{Path: "a.txt", Status: "M", Diff: diffPatchOf("B")})
	h.click(tester, "刷新")
	if !pumpFrames(tester, func() bool { return !tester.HasText("正在读取工作区变更…") }, 3*time.Second) {
		t.Fatalf("changes refresh did not finish; texts = %v", tester.Texts())
	}
	// The refresh dropped the fold state, so the refreshed viewer is expanded
	// again and must show the new content.
	if err := tester.Click("查看 diff"); err != nil {
		t.Fatalf("expand refreshed diff: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Advance(300 * time.Millisecond)
	tester.Frame()
	after, ok := tester.Access("New text")
	if !ok || !strings.Contains(after.Value, "new-B") {
		t.Fatalf("diff after Refresh = %+v found %v, want the reloaded new-B", after, ok)
	}
}

// TestChangesWorkspaceSwitchReloads pins PN-03: the Changes cache is keyed by
// the workspace (ID + root), as the Files listing is, so switching workspaces
// shows the new workspace's changes without a manual Refresh.
func TestChangesWorkspaceSwitchReloads(t *testing.T) {
	workspace := newDifferWorkspace(contracts.FileChange{
		Path: "only-in-ws1.txt", Status: "M", Diff: diffPatchOf("A"),
	})
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "变更")
	if !pumpFrames(tester, func() bool { return tester.HasText("only-in-ws1.txt") }, 3*time.Second) {
		t.Fatalf("ws1 changes did not load; texts = %v", tester.Texts())
	}

	workspace.fakeWorkspace.setCurrent(contracts.Workspace{ID: "ws-2", Name: "other", Root: "/work/other"})
	workspace.setChanges(contracts.FileChange{Path: "only-in-ws2.txt", Status: "M", Diff: diffPatchOf("B")})
	if !pumpFrames(tester, func() bool { return tester.HasText("only-in-ws2.txt") }, 3*time.Second) {
		t.Fatalf("the pane did not follow the workspace switch; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "only-in-ws1.txt")
}

// TestFilesChooseDoesNotEnterDirectory pins PN-04: the List's choice changes on
// a click as well as on Up/Down/Home/End (docs/ui/list.md), so a directory must
// only become chosen; entering it is the Submitted action (Enter/double click).
func TestFilesChooseDoesNotEnterDirectory(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent(contracts.Workspace{ID: "ws-nav", Name: "nav", Root: "/work/nav"})
	workspace.setDir("",
		contracts.FileEntry{Name: "docs", Path: "docs", Dir: true},
		contracts.FileEntry{Name: "main.go", Path: "main.go", Size: 12},
	)
	workspace.setDir("docs", contracts.FileEntry{Name: "readme.md", Path: "docs/readme.md", Size: 4})
	workspace.setFile("main.go", []byte("package main"))
	workspace.setFile("docs/readme.md", []byte("readme"))

	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	if !pumpFrames(tester, func() bool { return tester.HasText("main.go") }, 3*time.Second) {
		t.Fatalf("listing did not load; texts = %v", tester.Texts())
	}
	// Choose the file with the pointer, then walk up to the directory row with
	// the keyboard: the choice follows without entering the folder.
	if err := tester.Click("main.go"); err != nil {
		t.Fatalf("choose main.go: %v", err)
	}
	tester.Frame()
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	h.assertTexts(tester, "main.go", "docs")
	h.assertNoTexts(tester, "readme.md")

	// Enter on the chosen directory opens it.
	tester.Key(0, ui.KeyEnter)
	if !pumpFrames(tester, func() bool { return tester.HasText("readme.md") }, 3*time.Second) {
		t.Fatalf("Enter on the chosen folder did not open it; texts = %v", tester.Texts())
	}
}

// TestTaskAddRowEnterSubmits pins PN-06: the single-line subject field submits
// on Enter through the same path as the Add button.
func TestTaskAddRowEnterSubmits(t *testing.T) {
	board := newFakeTaskBoard()
	h := newTasksHarness(t, "zh", newFakeChat(), rightpanel.Config{}, board)
	tester := h.tester()
	openTasks(t, h, tester)
	typeTask(t, tester, "回车创建任务")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Frame()
	if len(board.creates) != 1 || board.creates[0].subject != "回车创建任务" {
		t.Fatalf("Create calls = %+v, want one 回车创建任务", board.creates)
	}
	h.assertTexts(tester, "回车创建任务")
}

// blockingListWorkspace makes one Files listing block, so the test observes the
// pane's loading state on a cached listing.
type blockingListWorkspace struct {
	*fakeWorkspace
	release chan struct{}
	block   bool
}

func (w *blockingListWorkspace) Files(ctx context.Context, dir string) ([]contracts.FileEntry, error) {
	w.mu.Lock()
	block := w.block
	release := w.release
	w.mu.Unlock()
	if block {
		<-release
	}
	return w.fakeWorkspace.Files(ctx, dir)
}

// TestFilesRefreshShowsBusyFeedback pins PN-07: a Refresh with cached rows must
// show the spinner and disable the action while the off-thread read runs (the
// Tasks pane's contract), instead of looking like a no-op.
func TestFilesRefreshShowsBusyFeedback(t *testing.T) {
	base := newFakeWorkspace()
	base.setCurrent(contracts.Workspace{ID: "ws-load", Name: "load", Root: "/work/load"})
	base.setDir("", contracts.FileEntry{Name: "README.md", Path: "README.md", Size: 4})
	base.setFile("README.md", []byte("# hi"))
	workspace := &blockingListWorkspace{fakeWorkspace: base, release: make(chan struct{})}
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	if !pumpFrames(tester, func() bool { return tester.HasText("README.md") }, 3*time.Second) {
		t.Fatalf("listing did not load; texts = %v", tester.Texts())
	}

	workspace.mu.Lock()
	workspace.block = true
	workspace.mu.Unlock()
	h.click(tester, "刷新")
	tester.Frame()
	info, ok := tester.AccessByRole(ui.RoleButton, "刷新")
	if !ok {
		t.Fatalf("refresh action is missing; texts = %v", tester.Texts())
	}
	if !info.Disabled {
		t.Fatalf("refresh is still enabled while the listing read is in flight: %+v", info)
	}

	workspace.mu.Lock()
	workspace.block = false
	workspace.mu.Unlock()
	close(workspace.release)
	if !pumpFrames(tester, func() bool {
		info, ok := tester.AccessByRole(ui.RoleButton, "刷新")
		return ok && !info.Disabled
	}, 3*time.Second) {
		t.Fatal("refresh stayed disabled after the listing landed")
	}
}

// logStore is the scripted durable session store for the Log pane tests: only
// SessionLog.Last is served; the rest of contracts.Store is unused.
type logStore struct {
	rig.SessionLog
	mu     sync.Mutex
	calls  int
	events []rig.SessionEvent
}

func (s *logStore) Last(_ context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	out := make([]rig.SessionEvent, 0, len(s.events))
	for _, event := range s.events {
		event.SessionID = sessionID
		out = append(out, event)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *logStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *logStore) History() rig.History                 { return nil }
func (s *logStore) CheckpointStore() rig.CheckpointStore { return nil }
func (s *logStore) ReplaceHistory(context.Context, string, []rig.Message) error {
	return nil
}
func (s *logStore) CreateSession(context.Context, string, string) error { return nil }
func (s *logStore) DeleteSession(context.Context, string) error         { return nil }
func (s *logStore) RenameSession(context.Context, string, string) error { return nil }
func (s *logStore) Sessions(context.Context) ([]contracts.SessionMeta, error) {
	return nil, nil
}
func (s *logStore) GetSessionMeta(context.Context, string) (contracts.SessionMeta, bool, error) {
	return contracts.SessionMeta{}, false, nil
}
func (s *logStore) AppendEvent(context.Context, rig.SessionEvent) error { return nil }

// newLogHarness builds the product frame with the optional durable store.
func newLogHarness(t *testing.T, chat *fakeChat, store contracts.Store) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.Store](t, ctx, contracts.KeyStore, store)
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {
			uiRoot.Runtime().RenderSlot(c, contracts.SlotSessionPanelRight)
		},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat}
	h.fiber = activate(t, ctx, rightpanel.Module(), rightpanel.Config{})
	return h
}

// TestLogRefreshInPaneHeader pins PN-09: the Log pane's explicit refresh is the
// same pane-header action every other pane uses, not an icon hidden in the
// filter row.
func TestLogRefreshInPaneHeader(t *testing.T) {
	chat := newFakeChat()
	if _, err := chat.NewSession("probe"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	store := &logStore{events: []rig.SessionEvent{
		{Seq: 1, Type: "session_started", Time: time.Unix(1700000000, 0)},
		{Seq: 2, Type: "user_message", Time: time.Unix(1700000001, 0), Payload: []byte(`{"text":"hi"}`)},
	}}
	h := newLogHarness(t, chat, store)
	tester := h.tester()
	h.selectPane(tester, "日志")
	if !pumpFrames(tester, func() bool { return tester.HasText("user_message") }, 3*time.Second) {
		t.Fatalf("log did not load; texts = %v", tester.Texts())
	}
	before := store.callCount()
	// The pane header carries the Refresh action.
	if _, ok := tester.AccessByRole(ui.RoleButton, "刷新"); !ok {
		t.Fatalf("the Log pane header has no Refresh action; texts = %v", tester.Texts())
	}
	h.click(tester, "刷新")
	if !pumpFrames(tester, func() bool { return store.callCount() > before }, 3*time.Second) {
		t.Fatalf("Refresh did not re-read the store (reads %d -> %d)", before, store.callCount())
	}
}

// TestTeamTimelineClockDoesNotWrap pins L-01: the activity timeline's clock
// column is wide enough for the English "3:04:05 PM" and never wraps to a
// second line (English used to wrap the clock into the event text's lines).
func TestTeamTimelineClockDoesNotWrap(t *testing.T) {
	team := newFakeTeam()
	when := team.members[0].Activity[0].Time.Local()
	clock := when.Format("3:04:05 PM")
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "en")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "Agents")
	selectAgent(t, h, tester, "研究员")
	tester.Frame()

	rect, ok := tester.Find(clock)
	if !ok {
		t.Fatalf("English timeline clock %q not rendered; texts = %v", clock, tester.Texts())
	}
	if rect.H > 22 {
		t.Fatalf("timeline clock %q height = %.1f DIP, want one line (no wrap)", clock, rect.H)
	}
}
