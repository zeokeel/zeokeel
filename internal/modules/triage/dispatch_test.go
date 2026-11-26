package triage_test

// 派单唤醒（dispatch.go）的测试：范围必须是"只对 lead 角色"，唤醒失败必须不
// 影响已经建好的任务，成功时四步（建会话 → 记录 provenance → 绑定任务 → 选角色 →
// 发 prompt）都要真的发生。
//
// 断言的写法照 product 的规则来：**不检查"有没有调用"这种实现细节之外的宽松断言**，
// 而是检查任务板上仍然只有一个任务、inbox 仍是 task 状态、会话拿到了 dispatch
// provenance + lead 角色 + 该任务的绑定——这三件是用户/审计能看到的事实。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
	"github.com/zeokeel/zeokeel/internal/modules/triage"
)

// dispatchChat is a chat backend with both optional halves the connector needs.
type dispatchChat struct {
	mu sync.Mutex
	// newSessionErr/sendErr inject the two failures that matter: the run could
	// not be created, or it could not be started.
	newSessionErr error
	bindErr       error
	presetErr     error
	sendErr       error
	sessions      []string
	titles        []string
	bindings      map[string]string
	presets       map[string]string
	prompts       map[string]string
}

func newDispatchChat() *dispatchChat {
	return &dispatchChat{
		bindings: map[string]string{},
		presets:  map[string]string{},
		prompts:  map[string]string{},
	}
}

func (c *dispatchChat) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (c *dispatchChat) Sessions() []contracts.ChatSessionInfo { return nil }
func (c *dispatchChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (c *dispatchChat) ActiveSession() string         { return "" }
func (c *dispatchChat) SetActiveSession(string) error { return nil }
func (c *dispatchChat) DeleteSession(string) error    { return nil }
func (c *dispatchChat) Cancel(string) error           { return nil }
func (c *dispatchChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

func (c *dispatchChat) NewSession(title string) (string, error) {
	if c.newSessionErr != nil {
		return "", c.newSessionErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := "sess-" + string(rune('1'+len(c.sessions)))
	c.sessions = append(c.sessions, id)
	c.titles = append(c.titles, title)
	return id, nil
}

func (c *dispatchChat) Send(sessionID, prompt string) error {
	if c.sendErr != nil {
		return c.sendErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompts[sessionID] = prompt
	return nil
}

func (c *dispatchChat) BindSessionTask(_ context.Context, sessionID, taskID string) error {
	if c.bindErr != nil {
		return c.bindErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings[sessionID] = taskID
	return nil
}

func (c *dispatchChat) SessionTask(sessionID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bindings[sessionID], nil
}

func (c *dispatchChat) SetSessionPreset(sessionID, presetID string) error {
	if c.presetErr != nil {
		return c.presetErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.presets[sessionID] = presetID
	return nil
}

func (c *dispatchChat) SessionPreset(sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.presets[sessionID]
}

// fakeSettings is the minimum the origin row needs.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (f *fakeSettings) Get(key, fallback string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.values[key]; ok {
		return value
	}
	return fallback
}

func (f *fakeSettings) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
	return nil
}

func (f *fakeSettings) All() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.values))
	for key, value := range f.values {
		out[key] = value
	}
	return out
}

func newDispatchServices(t *testing.T, chat contracts.ChatService, settings contracts.SettingsService) (*triage.Service, contracts.InboxService, *fakeBoard, *tasklease.Service) {
	t.Helper()
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now})
	board := newFakeBoard()
	// The budget gate needs a real lease service: a dispatched run must start
	// under a ceiling (2026-11-26 audit, 中高), so the connector is wired with the
	// same service the product assembles.
	leases := tasklease.NewService(board, tasklease.Config{})
	svc := triage.NewService(inboxSvc, board).
		WithDispatcher(triage.NewDispatcher(chat, settings, nil).WithLeases(leases))
	return svc, inboxSvc, board, leases
}

// TestDispatchToLeadWakesARun is the whole point of the connector: assigning a
// task to the lead role starts a lead session that is bound to the task.
func TestDispatchToLeadWakesARun(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{}}
	svc, inboxSvc, board, leases := newDispatchServices(t, chat, settings)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject: "查退款率", Detail: "上周", Assignee: contracts.PresetLeadID,
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Dispatch == nil || result.Dispatch.Error != "" {
		t.Fatalf("dispatch = %+v, want a started run", result.Dispatch)
	}
	if len(chat.sessions) != 1 {
		t.Fatalf("sessions = %v, want exactly one", chat.sessions)
	}
	sessionID := chat.sessions[0]
	if result.Dispatch.SessionID != sessionID {
		t.Fatalf("dispatch session = %q, want %q", result.Dispatch.SessionID, sessionID)
	}
	// The three facts an audit checks: role, binding, provenance.
	if got := chat.presets[sessionID]; got != contracts.PresetLeadID {
		t.Fatalf("session role = %q, want %q", got, contracts.PresetLeadID)
	}
	if got := chat.bindings[sessionID]; got != result.TaskIDs[0] {
		t.Fatalf("session binding = %q, want %q", got, result.TaskIDs[0])
	}
	if got := settings.values[contracts.SessionOriginSettingKey(sessionID)]; got != contracts.SessionOriginDispatch {
		t.Fatalf("session origin = %q, want %q", got, contracts.SessionOriginDispatch)
	}
	// The prompt carries the task id, the goal and the orchestration discipline.
	prompt := chat.prompts[sessionID]
	for _, want := range []string{result.TaskIDs[0], "查退款率", "上周", "审计角色"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q is missing %q", prompt, want)
		}
	}
	// And the board/inbox are unchanged by the wake-up.
	if board.count() != 1 || result.Inbox.Status != contracts.InboxStatusTask {
		t.Fatalf("board=%d inbox=%q, want 1 task and task status", board.count(), result.Inbox.Status)
	}
	// The run started under a ceiling: a dispatch nobody budgeted would be a bill
	// nobody agreed to (2026-11-26 audit, 中高).
	budget, ok, err := leases.Budget(ctx, result.TaskIDs[0])
	if err != nil || !ok {
		t.Fatalf("budget after dispatch = %+v/%v/%v, want a default row", budget, ok, err)
	}
	if budget.MaxTokens <= 0 || budget.MaxMinutes <= 0 {
		t.Fatalf("default dispatch budget = %+v, want positive limits on both axes", budget)
	}
}

// TestDispatchOnlyForTheLeadRole pins the scope restriction: any other assignee
// keeps the historical behaviour (owner recorded, nothing runs). This is what
// keeps "dispatch" from spending tokens on every task.
func TestDispatchOnlyForTheLeadRole(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{}}
	svc, inboxSvc, board, _ := newDispatchServices(t, chat, settings)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x", Assignee: "research-bot"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Dispatch != nil {
		t.Fatalf("dispatch = %+v for a non-lead assignee, want none", result.Dispatch)
	}
	if len(chat.sessions) != 0 {
		t.Fatalf("sessions = %v, want none", chat.sessions)
	}
	if board.assigned[result.TaskIDs[0]] != "research-bot" {
		t.Fatalf("assignee = %q, want research-bot", board.assigned[result.TaskIDs[0]])
	}
}

// TestDispatchFailureKeepsTheTask: a wake-up that fails must report why without
// undoing the task. Otherwise "the request is on the board" would be a lie.
func TestDispatchFailureKeepsTheTask(t *testing.T) {
	chat := newDispatchChat()
	chat.newSessionErr = errors.New("chat unavailable")
	settings := &fakeSettings{values: map[string]string{}}
	svc, inboxSvc, board, _ := newDispatchServices(t, chat, settings)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x", Assignee: contracts.PresetLeadID},
	})
	if err != nil {
		t.Fatalf("Apply must not fail when only the wake-up failed: %v", err)
	}
	if result.Dispatch == nil || !strings.Contains(result.Dispatch.Error, "chat unavailable") {
		t.Fatalf("dispatch = %+v, want the failure recorded", result.Dispatch)
	}
	if result.Dispatch.SessionID != "" {
		t.Fatalf("dispatch session = %q, want empty", result.Dispatch.SessionID)
	}
	if board.count() != 1 || result.Inbox.Status != contracts.InboxStatusTask {
		t.Fatalf("board=%d inbox=%q, want the task to survive", board.count(), result.Inbox.Status)
	}
}

// TestDispatchWithoutChatServiceIsInert: a product assembled without chat still
// triages, it just never runs anything by itself.
func TestDispatchWithoutChatServiceIsInert(t *testing.T) {
	svc, inboxSvc, board, _ := newDispatchServices(t, nil, nil)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x", Assignee: contracts.PresetLeadID},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Dispatch != nil {
		t.Fatalf("dispatch = %+v without a chat service, want none", result.Dispatch)
	}
	if board.count() != 1 {
		t.Fatalf("board tasks = %d, want 1", board.count())
	}
}

// --- spend gates and assignee validation (2026-11-26 audit, 中高) ----------

// TestDispatchWithoutALeaseServiceIsSkipped: no meter, no run. A dispatched run is
// a bill, so an assembly that cannot bound the spend does not start one — and the
// decision is recorded instead of being silently dropped.
func TestDispatchWithoutALeaseServiceIsSkipped(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{}}
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now})
	board := newFakeBoard()
	// Deliberately no WithLeases: this is the "cannot meter" assembly.
	svc := triage.NewService(inboxSvc, board).
		WithDispatcher(triage.NewDispatcher(chat, settings, nil))
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x", Assignee: contracts.PresetLeadID},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(chat.sessions) != 0 {
		t.Fatalf("sessions = %v, want none (an unmetered run must not start)", chat.sessions)
	}
	if result.Dispatch == nil || !strings.Contains(result.Dispatch.Skipped, "预算") {
		t.Fatalf("dispatch = %+v, want a recorded skip with the budget reason", result.Dispatch)
	}
	if result.Dispatch.Error != "" {
		t.Fatalf("dispatch error = %q, want a skip (a policy decision), not a failure", result.Dispatch.Error)
	}
	// The task still exists: the request is not lost because its run was refused.
	if board.count() != 1 || result.Inbox.Status != contracts.InboxStatusTask {
		t.Fatalf("board=%d inbox=%q, want the task to survive", board.count(), result.Inbox.Status)
	}
}

// TestDispatchSkipsAnExhaustedBudget: the stop is sticky (contracts.TaskBudget), and
// re-dispatching is not a re-budget.
func TestDispatchSkipsAnExhaustedBudget(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{}}
	_, _, board, leases := newDispatchServices(t, chat, settings)
	ctx := context.Background()

	// A task created outside triage is fine for this gate: the dispatcher is asked
	// about the task id, and the ceiling lives on that id.
	task, err := board.Create(ctx, "已超预算", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := leases.SetBudget(ctx, task.ID, contracts.TaskBudget{MaxTokens: 10}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if _, err := leases.Consume(ctx, task.ID, 11); !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Consume over the ceiling = %v, want ErrLeaseBudgetExhausted", err)
	}

	// Triage creates its own task, so the dispatcher is asked about THAT id; the
	// gate must read the real row. Drive the connector directly with the stopped
	// task to prove the gate reads it.
	dispatcher := triage.NewDispatcher(chat, settings, nil).WithLeases(leases)
	record := dispatcher.Wake(ctx, task, "已超预算", "")
	if record == nil || !strings.Contains(record.Skipped, "耗尽") {
		t.Fatalf("dispatch of an exhausted task = %+v, want a skip naming the exhausted budget", record)
	}
	if len(chat.sessions) != 0 {
		t.Fatalf("sessions = %v, want none", chat.sessions)
	}
}

// TestDispatchRespectsTheConcurrencyCeiling pins the second gate: a burst of
// passes must not accumulate runs. The ceiling is agent.subagentMaxConcurrent.
func TestDispatchRespectsTheConcurrencyCeiling(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{contracts.SettingAgentSubagentMaxConcurrent: "1"}}
	svc, inboxSvc, board, _ := newDispatchServices(t, chat, settings)
	ctx := context.Background()

	first := arrive(t, inboxSvc)
	second, _, err := inboxSvc.Add(ctx, contracts.InboxItem{
		Source: "wechat", Channel: "dm", Sender: "owner", DedupKey: "k2", Title: "第二条",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	decision := contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x", Assignee: contracts.PresetLeadID},
	}
	if result, err := svc.Apply(ctx, first.ID, decision); err != nil || result.Dispatch.Skipped != "" {
		t.Fatalf("first dispatch = %+v/%v, want it started", result.Dispatch, err)
	}
	second2 := decision
	second2.Task = &contracts.NewTask{Subject: "y", Assignee: contracts.PresetLeadID}
	result, err := svc.Apply(ctx, second.ID, second2)
	if err != nil {
		t.Fatalf("Apply second: %v", err)
	}
	if len(chat.sessions) != 1 {
		t.Fatalf("sessions = %v, want exactly one run at the ceiling of 1", chat.sessions)
	}
	if result.Dispatch == nil || !strings.Contains(result.Dispatch.Skipped, "并发上限") {
		t.Fatalf("second dispatch = %+v, want a recorded concurrency skip", result.Dispatch)
	}
	// Both tasks are on the board: the ceiling stops runs, not work.
	if board.count() != 2 {
		t.Fatalf("board tasks = %d, want 2", board.count())
	}
}

// TestDispatchSlotsExpire releases a slot after the dispatch TTL, so a long-lived
// process does not stop dispatching forever after one wake-up.
func TestDispatchSlotsExpire(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{contracts.SettingAgentSubagentMaxConcurrent: "1"}}
	board := newFakeBoard()
	leases := tasklease.NewService(board, tasklease.Config{})
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	dispatcher := triage.NewDispatcher(chat, settings, nil).WithLeases(leases).
		WithClock(func() time.Time { return now })
	ctx := context.Background()

	task, _ := board.Create(ctx, "先派一个", "")
	if record := dispatcher.Wake(ctx, task, "", ""); record == nil || record.Skipped != "" {
		t.Fatalf("first wake = %+v, want it started", record)
	}
	second, _ := board.Create(ctx, "第二个", "")
	if record := dispatcher.Wake(ctx, second, "", ""); record == nil || !strings.Contains(record.Skipped, "并发上限") {
		t.Fatalf("second wake = %+v, want the ceiling", record)
	}
	// A lease TTL later the slot is free again.
	now = now.Add(contracts.DefaultLeaseTTL + time.Second)
	third, _ := board.Create(ctx, "第三个", "")
	if record := dispatcher.Wake(ctx, third, "", ""); record == nil || record.Skipped != "" {
		t.Fatalf("third wake after the TTL = %+v, want it started", record)
	}
}

// TestUnknownAssigneeIsNotWrittenOrDispatched is requirement ①: when the caller
// declares its teammates (NewTask.KnownAssignees), an assignee outside that set is
// not written to the board as an owner and never starts a run — the skip names the
// names it could have used.
func TestUnknownAssigneeIsNotWrittenOrDispatched(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{}}
	svc, inboxSvc, board, _ := newDispatchServices(t, chat, settings)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject: "x", Assignee: "bob",
			KnownAssignees: []string{contracts.PresetLeadID, "bot-1"},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := board.assigned[result.TaskIDs[0]]; got != "" {
		t.Fatalf("owner = %q, want no owner (an invented teammate is not an owner)", got)
	}
	if len(chat.sessions) != 0 {
		t.Fatalf("sessions = %v, want none", chat.sessions)
	}
	if result.Dispatch == nil || !strings.Contains(result.Dispatch.Skipped, "bob") ||
		!strings.Contains(result.Dispatch.Skipped, "bot-1") {
		t.Fatalf("dispatch = %+v, want a skip naming the rejected name and the known set", result.Dispatch)
	}
	// The task itself is created and the inbox is marked: the request is not lost.
	if board.count() != 1 || result.Inbox.Status != contracts.InboxStatusTask {
		t.Fatalf("board=%d inbox=%q, want the task to survive", board.count(), result.Inbox.Status)
	}
}

// TestEnvelopeAssigneeStillBecomesTheOwner: the envelope is an allowlist, not a
// role restriction — a rule-derived teammate name inside it is still written as the
// owner (and does not start a run, because only the lead role does).
func TestEnvelopeAssigneeStillBecomesTheOwner(t *testing.T) {
	chat := newDispatchChat()
	settings := &fakeSettings{values: map[string]string{}}
	svc, inboxSvc, board, _ := newDispatchServices(t, chat, settings)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject: "x", Assignee: "bot-1",
			KnownAssignees: []string{"bot-1"},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := board.assigned[result.TaskIDs[0]]; got != "bot-1" {
		t.Fatalf("owner = %q, want bot-1", got)
	}
	if result.Dispatch != nil {
		t.Fatalf("dispatch = %+v for a non-lead assignee, want none", result.Dispatch)
	}
	if len(chat.sessions) != 0 {
		t.Fatalf("sessions = %v, want none", chat.sessions)
	}
}
