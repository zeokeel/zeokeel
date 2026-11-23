package session_test

// Frame tests for the Todo dock above the composer: the agent's todo_write list
// with its title/count, per-item status marker, in-progress highlight, stable
// status grouping, collapse, empty-state hiding and the English/narrow-window
// forms. The dock only reads (no editing or dragging): the agent replaces the
// whole list with todo_write.

import (
	"image/color"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeTodoService is a mutable contracts.TodoService: set replaces the list the
// way todo_write does.
type fakeTodoService struct {
	mu    sync.Mutex
	todos []contracts.TodoItem
}

func (f *fakeTodoService) Todos() []contracts.TodoItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TodoItem(nil), f.todos...)
}

func (f *fakeTodoService) set(todos ...contracts.TodoItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.todos = append([]contracts.TodoItem(nil), todos...)
}

// newTodoHarness builds the conversation screen with the optional todo service
// provided (nil leaves it out, like a host without the toolkit).
func newTodoHarness(t *testing.T, chat *fakeChat, todo contracts.TodoService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, uinative.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if todo != nil {
		provide[contracts.TodoService](t, ctx, contracts.KeyTodo, todo)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// compositeOver blends a translucent fill over an opaque base, as the painter
// draws a selected row.
func compositeOver(fill, base ui.Color) color.RGBA {
	alpha := float32(fill.A) / 255
	blend := func(over, under uint8) uint8 {
		return uint8(float32(over)*alpha + float32(under)*(1-alpha))
	}
	return color.RGBA{
		R: blend(fill.R, base.R), G: blend(fill.G, base.G), B: blend(fill.B, base.B), A: 255,
	}
}

// nearColor reports whether two opaque colors are within tolerance per channel.
func nearColor(a, b color.RGBA, tolerance int) bool {
	diff := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return diff(a.R, b.R) <= tolerance && diff(a.G, b.G) <= tolerance && diff(a.B, b.B) <= tolerance
}

// todoChat is one idle conversation, so the dock has the composer to sit above.
func todoChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "发布会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "继续发布"}},
	}, true)
	return chat
}

// todoFixture is a mixed list in a deliberately unsorted order: the dock groups
// in-progress first, then pending, then completed (each group in list order).
func todoFixture() *fakeTodoService {
	todo := &fakeTodoService{}
	todo.set(
		contracts.TodoItem{ID: "t1", Content: "写测试", Status: contracts.TodoStatusPending},
		contracts.TodoItem{ID: "t2", Content: "合并发布", Status: contracts.TodoStatusCompleted},
		contracts.TodoItem{ID: "t3", Content: "修复构建", Status: contracts.TodoStatusInProgress},
	)
	return todo
}

// TestTodoDockShowsListAndCounts covers the resting dock: title, completed/total
// count, every item with its status name, and the in-progress item highlighted
// above the composer.
func TestTodoDockShowsListAndCounts(t *testing.T) {
	h := newTodoHarness(t, todoChat(), todoFixture())
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "待办", "1/3", "修复构建", "写测试", "合并发布")
	// The rows carry their status in the accessible name (color/shape are never
	// the only carrier).
	for _, label := range []string{"进行中 · 修复构建", "待处理 · 写测试", "已完成 · 合并发布"} {
		if _, ok := tester.AccessByRole(ui.RoleListItem, label); !ok {
			t.Fatalf("todo row %q missing; texts = %v", label, tester.Texts())
		}
	}
	// The in-progress row is highlighted: the translucent BgSelected accent wash
	// composited over the page base, unlike the plain rows.
	light := design.Light()
	selected, ok := tester.AccessByRole(ui.RoleListItem, "进行中 · 修复构建")
	if !ok {
		t.Fatalf("in-progress row missing; texts = %v", tester.Texts())
	}
	got := tester.Image().RGBAAt(int(selected.Bounds.X)+2, int(selected.Bounds.Y+selected.Bounds.H/2))
	want := compositeOver(light.BgSelected, light.BgBase)
	if !nearColor(got, want, 2) {
		t.Fatalf("in-progress row fill = %v, want the highlighted %v", got, want)
	}
	if nearColor(got, color.RGBA{R: light.BgBase.R, G: light.BgBase.G, B: light.BgBase.B, A: light.BgBase.A}, 2) {
		t.Fatalf("in-progress row fill = %v, want the highlight, not the page base", got)
	}
	// It sits above the composer input. The geometry is read after the frame has
	// settled: a single pass could measure a layout still in progress on a loaded
	// runner (this assertion flaked twice in other people's full-package runs).
	// waitFor pumps frames until both rects resolve, so the check is a property of
	// the settled frame rather than of the first one.
	waitFor(t, tester, func() bool {
		_, rowOK := tester.Find("修复构建")
		_, inputOK := tester.Find("输入消息…")
		return rowOK && inputOK
	}, "the todo row and the composer input")
	input, ok := tester.Find("输入消息…")
	if !ok {
		t.Fatalf("composer input missing; texts = %v", tester.Texts())
	}
	row, ok := tester.Find("修复构建")
	if !ok {
		t.Fatalf("todo row missing; texts = %v", tester.Texts())
	}
	if row.Y >= input.Y {
		t.Fatalf("todo dock at Y=%.1f is not above the composer at Y=%.1f", row.Y, input.Y)
	}
}

// TestTodoDockKeepsAgentOrder covers US-11's ordinal contract: the agent writes
// the list as a numbered plan, so the dock renders it in the agent's order —
// status grouping must not shuffle 第一步/第二步/第三步 (the old grouped order
// read 2→3→1). Status stays visible through each row's marker and name.
func TestTodoDockKeepsAgentOrder(t *testing.T) {
	todo := &fakeTodoService{}
	todo.set(
		contracts.TodoItem{ID: "t1", Content: "第一步：写测试", Status: contracts.TodoStatusPending},
		contracts.TodoItem{ID: "t2", Content: "第二步：合并发布", Status: contracts.TodoStatusCompleted},
		contracts.TodoItem{ID: "t3", Content: "第三步：修复构建", Status: contracts.TodoStatusInProgress},
	)
	h := newTodoHarness(t, todoChat(), todo)
	tester := h.tester()
	tester.Frame()
	ordered := []string{"待处理 · 第一步：写测试", "已完成 · 第二步：合并发布", "进行中 · 第三步：修复构建"}
	var previous float32
	for _, label := range ordered {
		info, ok := tester.AccessByRole(ui.RoleListItem, label)
		if !ok {
			t.Fatalf("todo row %q missing; texts = %v", label, tester.Texts())
		}
		if previous > 0 && info.Bounds.Y <= previous {
			t.Fatalf("US-11: row %q at Y=%.1f broke the agent's step order (previous Y=%.1f)",
				label, info.Bounds.Y, previous)
		}
		previous = info.Bounds.Y
	}
}

// TestTodoDockDisclosesGlobalScope covers US-11's disclosure: the dock says the
// list is shared across the whole app, not scoped to the conversation on screen.
func TestTodoDockDisclosesGlobalScope(t *testing.T) {
	h := newTodoHarness(t, todoChat(), todoFixture())
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "待办为全应用共享")
}

// TestTodoDockCollapses covers the collapse: the header toggles the rows, the
// count stays, and a second press restores them.
func TestTodoDockCollapses(t *testing.T) {
	h := newTodoHarness(t, todoChat(), todoFixture())
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "修复构建", "1/3")

	h.click(tester, "待办")
	h.assertTexts(tester, "1/3")
	h.assertNoTexts(tester, "修复构建", "写测试", "合并发布")
	// The trigger takes the keyboard focus; Enter reopens the rows.
	if !tester.Focused("待办清单，已完成 1/3") {
		t.Fatalf("collapsed todo trigger does not hold the focus; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "修复构建", "写测试", "合并发布")
	h.click(tester, "待办")
	h.assertNoTexts(tester, "修复构建")
}

// TestTodoDockHidesWhenEmpty covers the empty state: no dock, no title, no
// collapse chrome.
func TestTodoDockHidesWhenEmpty(t *testing.T) {
	empty := &fakeTodoService{}
	h := newTodoHarness(t, todoChat(), empty)
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "待办", "待办清单")
	// A clear while visible hides it again.
	todo := todoFixture()
	h2 := newTodoHarness(t, todoChat(), todo)
	tester2 := h2.tester()
	tester2.Frame()
	h2.assertTexts(tester2, "修复构建")
	todo.set()
	tester2.Frame()
	h2.assertNoTexts(tester2, "待办", "修复构建")
}

// TestTodoDockRefreshShowsTheNewList covers the live update: the dock reads the
// lightweight snapshot each frame, so a replaced list shows at once.
func TestTodoDockRefreshShowsTheNewList(t *testing.T) {
	todo := todoFixture()
	h := newTodoHarness(t, todoChat(), todo)
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "文档截图")

	todo.set(contracts.TodoItem{ID: "n1", Content: "文档截图", Status: contracts.TodoStatusInProgress})
	tester.Frame()
	h.assertTexts(tester, "文档截图", "0/1")
	h.assertNoTexts(tester, "修复构建")
}

// TestTodoDockEnglish covers the English form.
func TestTodoDockEnglish(t *testing.T) {
	todo := &fakeTodoService{}
	todo.set(
		contracts.TodoItem{ID: "t1", Content: "Write tests", Status: contracts.TodoStatusPending},
		contracts.TodoItem{ID: "t2", Content: "Ship the release", Status: contracts.TodoStatusCompleted},
		contracts.TodoItem{ID: "t3", Content: "Fix the build", Status: contracts.TodoStatusInProgress},
	)
	h := newTodoHarness(t, todoChat(), todo)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "To-do", "1/3", "Fix the build")
	for _, label := range []string{"In progress · Fix the build", "Pending · Write tests", "Completed · Ship the release"} {
		if _, ok := tester.AccessByRole(ui.RoleListItem, label); !ok {
			t.Fatalf("English todo row %q missing; texts = %v", label, tester.Texts())
		}
	}
}

// TestTodoDockNarrowWindow covers the 900x600 minimum: the dock stays legible
// (title, count and rows inside the window).
func TestTodoDockNarrowWindow(t *testing.T) {
	h := newTodoHarness(t, todoChat(), todoFixture())
	tester := h.tester()
	tester.SetSize(900, 600)
	tester.Frame()
	h.assertTexts(tester, "待办", "1/3", "修复构建", "写测试", "合并发布")
	row, ok := tester.AccessByRole(ui.RoleListItem, "进行中 · 修复构建")
	if !ok {
		t.Fatalf("narrow todo row missing; texts = %v", tester.Texts())
	}
	if row.Bounds.X < 0 || row.Bounds.X+row.Bounds.W > 900 {
		t.Fatalf("narrow todo row = %+v, outside the 900 DIP window", row.Bounds)
	}
	// The dock keeps the collapse affordance at this width too.
	h.click(tester, "待办")
	h.assertNoTexts(tester, "修复构建")
}

// TestTodoDockWithoutService covers a host without the optional service: no
// dock and no crash.
func TestTodoDockWithoutService(t *testing.T) {
	h := newTodoHarness(t, todoChat(), nil)
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "待办", "待办清单")
}
