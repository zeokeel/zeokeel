package uikit_test

// Headless tests for the shared conversation transcript (uikit.SessionThread):
// the entry owns the official List (virtualization, message-ID row keys, the
// streaming follow), the empty hero, the floating "back to latest" action and
// the session-level error band; the message bodies are the caller's Row slot.
// The tests drive a real frame and read both the rendered labels and the
// accessibility tree (row names are role + ordinal, never the prose).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// sessionThreadView draws the transcript in a bounded page column.
func sessionThreadView(o uikit.SessionThreadOptions) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).Children(func() {
			uikit.SessionThread(c, o)
		})
	}
}

// threadMessage is one transcript message for the thread tests.
func threadMessage(index int) contracts.ChatMessage {
	role := contracts.ChatRoleUser
	if index%2 == 1 {
		role = contracts.ChatRoleAssistant
	}
	return contracts.ChatMessage{
		ID:   fmt.Sprintf("m%d", index),
		Role: role,
		Text: fmt.Sprintf("消息 %d：%s", index, strings.Repeat("内容 ", 40)),
	}
}

// TestSessionThreadVirtualizesLongConversation covers the List contract: rows
// are keyed by message ID and only the visible ones build, while the row labels
// stay role + ordinal (the prose never leaks into the accessibility tree).
func TestSessionThreadVirtualizesLongConversation(t *testing.T) {
	messages := make([]contracts.ChatMessage, 200)
	for index := range messages {
		messages[index] = threadMessage(index)
	}
	state := &ui.ListState{}
	follow, laidOut := true, false
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages:  messages,
		State:     state,
		FollowEnd: &follow,
		LaidOut:   &laidOut,
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			ui.Text(c, "row:"+message.ID)
		},
	}), 800, 400)
	tt.Frame()

	if _, ok := tt.AccessByRole(ui.RoleList, "对话消息"); !ok {
		t.Fatalf("transcript is not a named list; texts = %v", tt.Texts())
	}
	// The panel starts at the end of the conversation: the last row exists, the
	// first rows are virtualized away.
	if !tt.HasText("row:m199") {
		t.Fatalf("transcript does not show its end; texts = %v", tt.Texts())
	}
	if tt.HasText("row:m0") {
		t.Fatalf("transcript builds the first row while scrolled to the end; texts = %v", tt.Texts())
	}
	built := 0
	for index := range messages {
		message := messages[index]
		role := "用户"
		if message.Role == contracts.ChatRoleAssistant {
			role = "助手"
		}
		if _, ok := tt.AccessByRole(ui.RoleListItem, fmt.Sprintf("%s %d", role, index+1)); ok {
			built++
		}
	}
	if built == 0 || built >= len(messages) {
		t.Fatalf("built %d of %d rows, want only the visible ones", built, len(messages))
	}
}

// TestSessionThreadEmptyHero covers the empty state: the hero draws through the
// List's children slot while there are no rows.
func TestSessionThreadEmptyHero(t *testing.T) {
	state := &ui.ListState{}
	follow, laidOut := true, false
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		State:     state,
		FollowEnd: &follow,
		LaidOut:   &laidOut,
		Empty: func(c *ui.Context) {
			ui.Text(c, "开始新对话")
		},
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			ui.Text(c, "row:"+message.ID)
		},
	}), 800, 400)
	tt.Frame()
	if !tt.HasText("开始新对话") {
		t.Fatalf("empty transcript has no hero; texts = %v", tt.Texts())
	}
}

// TestSessionThreadRowSlotAndHoldPlace covers the Row slot and the one-shot fold
// flag: the thread hands each message to the caller's builder and consumes the
// HoldPlace request for the next layout.
func TestSessionThreadRowSlotAndHoldPlace(t *testing.T) {
	state := &ui.ListState{}
	holdPlace := true
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages:  []contracts.ChatMessage{threadMessage(0), threadMessage(1)},
		State:     state,
		HoldPlace: &holdPlace,
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			ui.Text(c, "slot:"+message.ID)
		},
	}), 800, 300)
	tt.Frame()
	if !tt.HasText("slot:m0") || !tt.HasText("slot:m1") {
		t.Fatalf("Row slot did not build the messages; texts = %v", tt.Texts())
	}
	if holdPlace {
		t.Fatal("the thread did not consume the HoldPlace request")
	}
	if _, ok := tt.AccessByRole(ui.RoleListItem, "用户 1"); !ok {
		t.Fatalf("row 1 is not named by role + ordinal; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleListItem, "助手 2"); !ok {
		t.Fatalf("row 2 is not named by role + ordinal; texts = %v", tt.Texts())
	}
}

// TestSessionThreadFollowAndBackToLatest covers the streaming follow: the
// transcript starts at its end, stops following when the reader scrolls up,
// shows the floating action and resumes the follow when it is clicked.
func TestSessionThreadFollowAndBackToLatest(t *testing.T) {
	messages := make([]contracts.ChatMessage, 60)
	for index := range messages {
		messages[index] = threadMessage(index)
	}
	state := &ui.ListState{}
	follow, laidOut := true, false
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages:  messages,
		State:     state,
		FollowEnd: &follow,
		LaidOut:   &laidOut,
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			ui.Text(c, "row:"+message.ID)
		},
	}), 800, 400)
	tt.Frame()
	if tt.HasText("回到最新") {
		t.Fatalf("at the end the back-to-latest action shows; texts = %v", tt.Texts())
	}

	tt.Scroll(400, 200, 0, -300)
	tt.Frame()
	if !tt.HasText("回到最新") {
		t.Fatalf("scrolled up the back-to-latest action is missing; texts = %v", tt.Texts())
	}
	if err := tt.Click("回到最新"); err != nil {
		t.Fatalf("click back to latest: %v", err)
	}
	tt.Frame()
	if tt.HasText("回到最新") {
		t.Fatalf("after scrolling to the end the action still shows; texts = %v", tt.Texts())
	}
	if !follow {
		t.Fatal("the back-to-latest action did not resume the follow")
	}
}

// TestSessionThreadErrorBand covers the session-level error line and its optional
// retry action (the product renders its own per-turn retry and passes nil).
func TestSessionThreadErrorBand(t *testing.T) {
	calls := 0
	state := &ui.ListState{}
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages: []contracts.ChatMessage{threadMessage(0)},
		State:    state,
		Error:    "会话失败：超时",
		OnRetry:  func() { calls++ },
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			ui.Text(c, "row:"+message.ID)
		},
	}), 800, 300)
	tt.Frame()
	if !tt.HasText("会话失败：超时") {
		t.Fatalf("session error line missing; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试"); !ok {
		t.Fatalf("thread error band has no retry action; texts = %v", tt.Texts())
	}
	if err := tt.Click("重试"); err != nil {
		t.Fatalf("click retry: %v", err)
	}
	tt.Frame()
	if calls != 1 {
		t.Fatalf("session retry calls = %d, want 1", calls)
	}

	// Without an OnRetry the band says what failed without inventing an action.
	plain := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages: []contracts.ChatMessage{threadMessage(0)},
		State:    &ui.ListState{},
		Error:    "会话失败：超时",
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			ui.Text(c, "row:"+message.ID)
		},
	}), 800, 300)
	plain.Frame()
	if plain.HasText("重试") {
		t.Fatalf("error band without OnRetry shows a retry action; texts = %v", plain.Texts())
	}
}

// TestSessionThreadDefaultRows covers the entry's fallback body used by a host
// without its own Row slot: user text, tool/system badges, the reply Markdown
// and the generating indicator while the turn runs.
func TestSessionThreadDefaultRows(t *testing.T) {
	state := &ui.ListState{}
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "t1", Role: contracts.ChatRoleTool, Text: "工具输出"},
			{ID: "s1", Role: contracts.ChatRoleSystem, Text: "系统提示"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "**回复正文**"},
			{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "正在写…", Streaming: true},
		},
		Running: true,
		State:   state,
	}), 800, 400)
	tt.Frame()
	for _, want := range []string{"你好", "工具", "工具输出", "系统提示", "回复正文", "正在", "生成中"} {
		if !tt.HasText(want) {
			t.Fatalf("default transcript body does not show %q; texts = %v", want, tt.Texts())
		}
	}
}

// TestSessionThreadGroupsConsecutiveRuns covers the grouping hooks: a run of
// consecutive messages SameTurn accepts shares one List row (one accessibility
// label range) and builds through GroupRow, while single-message runs still use
// Row.
func TestSessionThreadGroupsConsecutiveRuns(t *testing.T) {
	messages := []contracts.ChatMessage{
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一步"},
		{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "两步"},
		{ID: "a3", Role: contracts.ChatRoleAssistant, Text: "三步"},
		{ID: "u4", Role: contracts.ChatRoleUser, Text: "问"},
		{ID: "a5", Role: contracts.ChatRoleAssistant, Text: "答"},
	}
	var groups [][]string
	seenSingles := map[string]bool{}
	tt := ui.NewTester(sessionThreadView(uikit.SessionThreadOptions{
		Messages: messages,
		State:    &ui.ListState{},
		SameTurn: func(prev, next contracts.ChatMessage) bool {
			return prev.Role == contracts.ChatRoleAssistant && next.Role == contracts.ChatRoleAssistant
		},
		Row: func(c *ui.Context, message contracts.ChatMessage) {
			seenSingles[message.ID] = true
			ui.Text(c, "row:"+message.ID)
		},
		GroupRow: func(c *ui.Context, group []contracts.ChatMessage) {
			ids := make([]string, 0, len(group))
			for _, message := range group {
				ids = append(ids, message.ID)
			}
			groups = append(groups, ids)
			ui.Text(c, "group:"+strings.Join(ids, ","))
		},
	}), 800, 400)
	tt.Frame()
	if len(groups) == 0 || strings.Join(groups[0], ",") != "a1,a2,a3" {
		t.Fatalf("grouped rows = %v, want an a1,a2,a3 run", groups)
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if seenSingles[id] {
			t.Fatalf("message %s built as a single row inside a grouped run", id)
		}
	}
	if !seenSingles["u4"] || !seenSingles["a5"] {
		t.Fatalf("single rows = %v, want u4 and a5 built by Row", seenSingles)
	}
	if !tt.HasText("group:a1,a2,a3") || tt.HasText("row:a1") {
		t.Fatalf("grouped run not rendered through GroupRow; texts = %v", tt.Texts())
	}
	for _, label := range []string{"助手 1–3", "用户 4", "助手 5"} {
		if _, ok := tt.AccessByRole(ui.RoleListItem, label); !ok {
			t.Fatalf("list row %q missing; row labels = %v", label, tt.Texts())
		}
	}
}
