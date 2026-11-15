package uiroot

// Frame tests for the collapsed rail's conversation entry (P2-8): the 64 DIP
// rail has no conversation list (the expanded sidebar's tree lives in a module
// slot the rail does not render), so it opens a native popover with the recent
// conversations, their running/unread state, a search field and the
// "expand sidebar" action. Choosing a conversation opens it without leaving the
// rail; no new screen is registered.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// railChat is a minimal ChatService with a scripted conversation list; it
// records the active-session switches the popover makes.
type railChat struct {
	sessions []contracts.ChatSessionInfo
	setCalls []string
	active   string
}

func (c *railChat) Config() contracts.ChatConfig { return contracts.ChatConfig{} }
func (c *railChat) Sessions() []contracts.ChatSessionInfo {
	return append([]contracts.ChatSessionInfo(nil), c.sessions...)
}
func (c *railChat) Session(sessionID string) (contracts.ChatSession, bool) {
	for _, session := range c.sessions {
		if session.ID == sessionID {
			return contracts.ChatSession{ID: sessionID, Title: session.Title}, true
		}
	}
	return contracts.ChatSession{}, false
}
func (c *railChat) ActiveSession() string { return c.active }
func (c *railChat) SetActiveSession(sessionID string) error {
	c.setCalls = append(c.setCalls, sessionID)
	c.active = sessionID
	return nil
}
func (c *railChat) NewSession(string) (string, error) { return "", nil }
func (c *railChat) DeleteSession(string) error        { return nil }
func (c *railChat) Send(string, string) error         { return nil }
func (c *railChat) Cancel(string) error               { return nil }
func (c *railChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

// newRailTestRoot builds a root with the conversation screen registered and the
// given conversation list.
func newRailTestRoot(t *testing.T, chat contracts.ChatService) *Root {
	t.Helper()
	r := New(Config{AppName: "RailTest", Chat: chat})
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "session-body", 20))
	return r
}

// openRailSessions collapses the sidebar and opens the conversation popover.
func openRailSessions(t *testing.T, r *Root, tt *ui.Tester) {
	t.Helper()
	r.SetSidebarCollapsed(true)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleToggleButton, "会话")
	if !ok {
		t.Fatalf("rail conversation entry missing; texts = %v", tt.Texts())
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
}

// clickRailSession clicks one conversation row of the popover by its
// accessible name (the title plus its running/unread state).
func clickRailSession(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleButton, name)
	if !ok {
		t.Fatalf("conversation row %q missing; texts = %v", name, tt.Texts())
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
}

// TestRailSessionsPopoverListsAndOpensConversations covers the entry: the rail
// button opens the popover, the recent user conversations are listed with their
// running/unread state, and choosing one opens it while the sidebar stays in its
// rail form. Archived and non-user conversations stay out, as in the sidebar.
func TestRailSessionsPopoverListsAndOpensConversations(t *testing.T) {
	chat := &railChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "写测试"},
		{ID: "s2", Title: "跑发布", Running: true},
		{ID: "s3", Title: "看日志", Unread: true},
		{ID: "s4", Title: "旧归档", Archived: true},
		{ID: "s5", Title: "自动化后台", Origin: contracts.SessionOriginAutomation},
	}}
	r := newRailTestRoot(t, chat)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	// The expanded sidebar keeps its own conversation tree; the rail entry is
	// the rail's form of it.
	if _, ok := tt.AccessByRole(ui.RoleToggleButton, "会话"); ok {
		t.Fatalf("conversation entry rendered in the expanded sidebar; texts = %v", tt.Texts())
	}

	openRailSessions(t, r, tt)
	for _, want := range []string{"写测试", "跑发布", "看日志", "展开侧栏"} {
		requireHasText(t, tt, want)
	}
	requireNoText(t, tt, "旧归档")
	requireNoText(t, tt, "自动化后台")
	// A running or unread conversation carries its state to assistive
	// technology; the visual dot alone is not enough.
	if _, ok := tt.AccessByRole(ui.RoleButton, "跑发布 · 运行中"); !ok {
		t.Fatalf("running conversation row missing its state; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "看日志 · 未读"); !ok {
		t.Fatalf("unread conversation row missing its state; texts = %v", tt.Texts())
	}

	clickRailSession(t, tt, "跑发布 · 运行中")
	if len(chat.setCalls) != 1 || chat.setCalls[0] != "s2" {
		t.Fatalf("SetActiveSession calls = %v, want [s2]", chat.setCalls)
	}
	if got := r.Navigator().Current(); got != sessionScreenID {
		t.Fatalf("navigator current = %q after choosing a conversation, want %q", got, sessionScreenID)
	}
	if !r.SidebarCollapsed() {
		t.Fatal("choosing a conversation expanded the rail")
	}
	requireHasText(t, tt, "session-body")
	requireNoText(t, tt, "展开侧栏") // the popover closed with the choice
}

// TestRailSessionsPopoverSearches covers the popover's search input: the query
// filters the recent conversations by title.
func TestRailSessionsPopoverSearches(t *testing.T) {
	chat := &railChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "写测试"},
		{ID: "s2", Title: "跑发布"},
	}}
	r := newRailTestRoot(t, chat)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	openRailSessions(t, r, tt)

	if err := tt.Click("搜索会话"); err != nil {
		t.Fatalf("focus the conversation search: %v; texts = %v", err, tt.Texts())
	}
	tt.Frame()
	tt.Type("发布")
	tt.Frame()
	requireHasText(t, tt, "跑发布")
	requireNoText(t, tt, "写测试")
}

// TestRailSessionsPopoverExpandsSidebar covers the popover's explicit expand
// action: it is the way back to the conversation tree without leaving the rail
// popover (the rail's own expand buttons stay).
func TestRailSessionsPopoverExpandsSidebar(t *testing.T) {
	chat := &railChat{sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "写测试"}}}
	r := newRailTestRoot(t, chat)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	openRailSessions(t, r, tt)

	if err := tt.Click("展开侧栏"); err != nil {
		t.Fatalf("expand the sidebar from the popover: %v; texts = %v", err, tt.Texts())
	}
	tt.Frame()
	if r.SidebarCollapsed() {
		t.Fatal("the popover's expand action did not expand the sidebar")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "折叠侧边栏"); !ok {
		t.Fatalf("expanded sidebar is missing; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleToggleButton, "会话"); ok {
		t.Fatalf("the rail conversation entry lingered after expanding; texts = %v", tt.Texts())
	}
}
