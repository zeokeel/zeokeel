package triage

// 派单唤醒（dispatch wake-up）。
//
// 之前"派发"只写一个 owner 字符串：任务板上多了一行署名，没有任何智能体被
// 唤醒，用户以为有人在干活，其实没有（bot 模块自己也在注释里承认"不派活"）。
// 这个连接器补上那一步：任务被派给 **lead 角色** 时，创建一个会话、绑定任务、
// 把会话设为 lead 角色，然后发出目标——lead 就在真正运行了，它会拆解、派给执行者、
// 再调审计角色验收。
//
// 为什么不做成"每个任务都自动起 run"：派单一旦上线就等于"写一行 owner 就烧 token"，
// 用户没有预期。因此范围是**严格的**：
//
//   - 只有 assignee 恰好是 `lead` 角色（contracts.PresetLeadID）才唤醒。其它
//     assignee（人名、bot slug、空）保持原行为：只写 owner，不起 run。
//   - 连接器必须被装配（有 chat 服务）才生效；没装配就什么都不做。
//   - 唤醒失败**不**让 triage 失败：任务已经建好并已入库，回滚它才是真正的数据
//     丢失。失败写进 TriageResult.Dispatch.Error 与日志，调用方能看到。
//
// 顺序也重要：先建任务、指派、把 inbox 标成 task，最后才唤醒。任何一步失败都
// 留下"任务在板上、状态与 inbox 一致"的可重试状态。
//
// 预算与并发（2026-11-26 审计，中高）：唤醒是**花钱**的动作，所以它现在有两道闸：
//
//   - **预算**：任务必须有预算行（不存在就按默认上限建一条），且未耗尽。没有租约
//     服务 = 无法计量 → 不唤醒。审计原文"无预算则不唤醒"由此成立，同时不必要求用户
//     先手工给一个还没被派发的任务设预算（那在流程上根本做不到）。
//   - **并发上限**：同时"在派单中"的 run 数量受 `agent.subagentMaxConcurrent`
//     （1–8，缺省 2）约束。一个槽位在唤醒后占用一个 lease TTL（`DefaultLeaseTTL`）：
//     triage 拿不到 run 结束的信号，所以这条闸门限制的是**唤醒速率/在飞数量**，不是
//     精确的在跑模型调用数——这一点必须如实写出来，不能假装它是精确信号量。没有它，
//     "每趟 20 条 × 多趟"会把派单累积成一场 token 风暴。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// dispatchPromptMaxDetail bounds the task detail copied into the lead's first
// message. A pasted transcript must not become an unbounded prompt.
const dispatchPromptMaxDetail = 8000

// Default budget of a dispatched run. It is applied when the task has no budget
// row yet: a dispatch happens immediately after the task is created, so "the user
// sets a ceiling first" is not a state that can happen. Zero on either axis means
// unlimited (contracts.TaskBudget), which is why these are positive.
const (
	// defaultDispatchMaxTokens is the token ceiling of one dispatched lead run.
	defaultDispatchMaxTokens = 200_000
	// defaultDispatchMaxMinutes is its wall-clock ceiling.
	defaultDispatchMaxMinutes = 30
	// defaultDispatchConcurrency is how many dispatched runs may be in flight at
	// once when the settings row is absent (the same 1–8 range the delegation
	// knobs use).
	defaultDispatchConcurrency = 2
	// dispatchSlotTTL is how long one dispatch holds its concurrency slot. triage
	// has no completion signal, so the slot expires with the longest a lease may
	// live without a heartbeat; the ceiling therefore bounds concurrent wakes.
	dispatchSlotTTL = contracts.DefaultLeaseTTL
)

// Dispatcher is the wake-up half of triage: it owns the chat service (and the
// settings row that records the session's origin). Both are optional together —
// without a chat service there is nothing to wake.
type Dispatcher struct {
	chat     contracts.ChatService
	settings contracts.SettingsService
	logger   contracts.Logger
	// leases is the task lease service: the budget gate reads it and the default
	// ceiling is written through it. Nil means "this assembly cannot meter a run",
	// which refuses every wake-up instead of spending unmetered (fail-closed).
	leases contracts.TaskLeaseService
	// now is the clock the concurrency slots expire against (tests freeze it).
	now func() time.Time
	// mu guards live: the slots of the dispatches this process started.
	mu sync.Mutex
	// live maps a dispatched task to when its slot expires.
	live map[string]time.Time
}

// NewDispatcher wires the connector. A nil chat or a product without settings
// simply never wakes anything (the connector reports nothing to do).
func NewDispatcher(chat contracts.ChatService, settings contracts.SettingsService, logger contracts.Logger) *Dispatcher {
	if chat == nil {
		return nil
	}
	return &Dispatcher{chat: chat, settings: settings, logger: logger, now: time.Now,
		live: make(map[string]time.Time)}
}

// WithLeases attaches the budget gate and returns the dispatcher (chain style, so
// wiring stays one expression). Without it every wake-up is skipped with a
// readable reason: an unmetered run is exactly what the budget gate exists to
// stop.
func (d *Dispatcher) WithLeases(leases contracts.TaskLeaseService) *Dispatcher {
	if d == nil {
		return nil
	}
	d.leases = leases
	return d
}

// WithClock replaces the clock the concurrency slots expire against (tests).
func (d *Dispatcher) WithClock(now func() time.Time) *Dispatcher {
	if d == nil || now == nil {
		return d
	}
	d.now = now
	return d
}

// concurrencyLimit resolves how many dispatched runs may be in flight at once.
func (d *Dispatcher) concurrencyLimit() int {
	limit := defaultDispatchConcurrency
	if d != nil && d.settings != nil {
		limit = contracts.NormalizeCapabilityConcurrency(
			d.settings.Get(contracts.SettingAgentSubagentMaxConcurrent, ""), limit)
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// acquireSlot reserves the concurrency slot of one dispatch. It returns a
// readable reason when the ceiling is reached.
func (d *Dispatcher) acquireSlot(taskID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.live == nil {
		d.live = make(map[string]time.Time)
	}
	for id, expiry := range d.live {
		if !expiry.After(now) {
			delete(d.live, id)
		}
	}
	if _, held := d.live[taskID]; held {
		return ""
	}
	limit := d.concurrencyLimit()
	if len(d.live) >= limit {
		return fmt.Sprintf("已有 %d 个派单在飞，达到并发上限 %d（%s）",
			len(d.live), limit, contracts.SettingAgentSubagentMaxConcurrent)
	}
	d.live[taskID] = now.Add(dispatchSlotTTL)
	return ""
}

// releaseSlot gives the slot back when the wake-up failed after reserving it.
func (d *Dispatcher) releaseSlot(taskID string) {
	d.mu.Lock()
	delete(d.live, taskID)
	d.mu.Unlock()
}

// ensureBudget makes sure the run starts under a ceiling, and returns a readable
// reason when it must not start. A task that already has a budget keeps it (the
// meter and the ceiling are the user's decision); an exhausted one is refused —
// the stop is sticky until somebody re-budgets it (contracts.TaskBudget), and
// re-dispatching is not a re-budget.
func (d *Dispatcher) ensureBudget(ctx context.Context, taskID string) string {
	if d.leases == nil {
		return "没有任务租约服务：无法给这次派单定预算，因此不唤醒"
	}
	row, ok, err := d.leases.Budget(ctx, taskID)
	if err != nil {
		return fmt.Sprintf("读取任务预算失败: %v", err)
	}
	if ok {
		if row.Exhausted {
			return fmt.Sprintf("任务预算已耗尽（%d tokens / %d 分钟上限），不唤醒；需要先追加预算",
				row.MaxTokens, row.MaxMinutes)
		}
		return ""
	}
	if _, err := d.leases.SetBudget(ctx, taskID, contracts.TaskBudget{
		MaxTokens: defaultDispatchMaxTokens, MaxMinutes: defaultDispatchMaxMinutes,
	}); err != nil {
		return fmt.Sprintf("无法为派单设置默认预算: %v", err)
	}
	return ""
}

// Wants reports whether this dispatch should wake a run: only a task assigned
// to the lead role does. The rule is one line on purpose — "dispatch starts a
// run" must stay narrow enough to read.
func (d *Dispatcher) Wants(assignee string) bool {
	return d != nil && d.chat != nil &&
		strings.EqualFold(strings.TrimSpace(assignee), contracts.PresetLeadID)
}

// Wake opens a lead session for the task and starts the run. It returns the
// dispatch record (never an error): the task exists either way, so a failure is
// reported in the record instead of being turned into a triage failure.
func (d *Dispatcher) Wake(ctx context.Context, task contracts.BoardTask, subject, detail string) *contracts.TriageDispatch {
	if d == nil || d.chat == nil {
		return nil
	}
	record := &contracts.TriageDispatch{TaskID: task.ID, PresetID: contracts.PresetLeadID}
	fail := func(stage string, err error) *contracts.TriageDispatch {
		record.Error = fmt.Sprintf("%s: %v", stage, err)
		if d.logger != nil {
			d.logger.Printf("[triage] dispatch task %s: %s: %v", task.ID, stage, err)
		}
		return record
	}
	// The two spend gates run BEFORE anything is created: a dispatched run is a
	// bill, so "no budget" and "already at the ceiling" are decisions, not
	// failures, and they are recorded as such.
	skip := func(reason string) *contracts.TriageDispatch {
		record.Skipped = reason
		if d.logger != nil {
			d.logger.Printf("[triage] dispatch task %s: not woken: %s", task.ID, reason)
		}
		return record
	}
	if reason := d.ensureBudget(ctx, task.ID); reason != "" {
		return skip(reason)
	}
	if reason := d.acquireSlot(task.ID); reason != "" {
		return skip(reason)
	}
	started := false
	defer func() {
		if !started {
			// The slot belongs to a live run; a wake-up that failed must not keep
			// occupying one, or a few failures would starve every later dispatch.
			d.releaseSlot(task.ID)
		}
	}()

	title := strings.TrimSpace(subject)
	if title == "" {
		title = strings.TrimSpace(task.Subject)
	}
	if title == "" {
		title = "任务 " + task.ID
	}
	sessionID, err := d.chat.NewSession("派单: " + truncateRunes(title, 40))
	if err != nil {
		return fail("create chat session", err)
	}
	record.SessionID = sessionID
	// Record the provenance before anything else can fail: the session exists
	// because a task was dispatched, and the sidebar/audit reads that from the
	// origin row, not from the title.
	if d.settings != nil {
		if err := d.settings.Set(contracts.SessionOriginSettingKey(sessionID), contracts.SessionOriginDispatch); err != nil {
			if d.logger != nil {
				d.logger.Printf("[triage] dispatch task %s: record session origin %s: %v", task.ID, sessionID, err)
			}
		}
	}
	// Bind the session to the task: while bound, the agent's gate may only call
	// the tools the task's binding names, and the run's writes belong to this
	// task (session.bindtask).
	if binder, ok := d.chat.(contracts.SessionTaskBinder); ok {
		if err := binder.BindSessionTask(ctx, sessionID, task.ID); err != nil {
			return fail("bind session to task", err)
		}
	}
	// The role is what makes it a lead run rather than a generic coding run;
	// without it the session would start with the default agent and no
	// orchestration discipline.
	if setter, ok := d.chat.(contracts.SessionPresetSetter); ok {
		if err := setter.SetSessionPreset(sessionID, contracts.PresetLeadID); err != nil {
			return fail("select lead role", err)
		}
	}
	if err := d.chat.Send(sessionID, dispatchPrompt(task, subject, detail)); err != nil {
		return fail("start run", err)
	}
	started = true
	if d.logger != nil {
		d.logger.Printf("[triage] dispatched task %s to a lead run in chat session %s", task.ID, sessionID)
	}
	return record
}

// dispatchPrompt is the lead's first message: the task, its goal and the
// instruction that matches the role it runs as. It never restates the whole
// task board — the session is bound to the task, and the lead's tools read it.
func dispatchPrompt(task contracts.BoardTask, subject, detail string) string {
	title := strings.TrimSpace(subject)
	if title == "" {
		title = strings.TrimSpace(task.Subject)
	}
	body := strings.TrimSpace(detail)
	if body == "" {
		body = strings.TrimSpace(task.Description)
	}
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "任务 %s 已派给你：%s\n", task.ID, title)
	if body != "" {
		prompt.WriteString("\n")
		prompt.WriteString(truncateRunes(body, dispatchPromptMaxDetail))
		prompt.WriteString("\n")
	}
	prompt.WriteString("\n请按编排纪律处理：整理上下文 → 拆成可委派的子任务 → 派给执行者 → " +
		"用审计角色（不是执行者自己）验收。本会话已绑定该任务，工具范围受任务绑定与 lead 角色约束。")
	return prompt.String()
}

// truncateRunes bounds s to limit runes (never splitting a multi-byte rune).
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
