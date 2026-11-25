package gateway

// Unit tests for the orchestration payload mapping and the dispatch presence.
// The end-to-end behaviour lives in the module tests (`inbox`, `triage`,
// `tasklease`, `approvals`); what this file protects is the seam: the methods
// are really dispatched, and the wire shape does not silently drop fields.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestOrchestrationMethodsAreDispatched(t *testing.T) {
	want := []string{
		"inbox.snapshot", "inbox.dispatch", "inbox.triage",
		"tasks.lease", "tasks.claim", "tasks.heartbeat", "tasks.submit", "tasks.review",
		"tasks.tools.set", "tasks.tools.get",
		"tasks.approval.set", "tasks.approval.get", "tasks.approve", "tasks.reject-start",
		"tasks.budget.set", "tasks.budget.get", "tasks.budget.consume", "tasks.send.pending", "tasks.requestedby.get", "tasks.send.approve",
		"approval.token", "approval.action",
		"notify.route.snapshot", "notify.route.upsert",
		"webhook.sources", "webhook.source.set", "webhook.source.remove",
		"lead.run",
		"credentials.list", "credentials.set", "credentials.delete",
		"heartbeat.status", "heartbeat.tick", "heartbeat.configure", "browser.status",
		"team.notes", "team.note.record",
		"team.suggestions", "team.suggest", "team.suggestion.promote", "team.suggestion.dismiss",
		"bots.snapshot", "bots.upsert",
		"users.snapshot", "users.upsert", "users.act", "bots.template.get", "bots.template.put",
		"skilldrafts.snapshot", "skilldrafts.propose", "skilldrafts.decide", "skilldrafts.install",
		"notify.plan", "notify.deliver", "team.note.review",
		"wechat.login.start", "wechat.login.state",
		// The task-binding seam: a session pointed at a task is what makes
		// tasks.tools.set an enforced policy instead of a recorded note.
		"session.bindtask",
	}
	methods := SupportedMethods()
	for _, name := range want {
		if !slices.Contains(methods, name) {
			t.Errorf("dispatch table is missing %q", name)
		}
	}
}

func TestInboxItemDTOMapping(t *testing.T) {
	received := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	item := contracts.InboxItem{
		ID: "inbox-1", Source: "wechat", Channel: "dm-1", Sender: "owner",
		DedupKey: "k", Priority: 3, Title: "查退款率", Body: "上周",
		Ref: "https://x", ReceivedAt: received, LastSeenAt: received.Add(time.Minute),
		Status: contracts.InboxStatusNew, Count: 2, TaskIDs: []string{"task-1"},
	}
	dto := inboxItemFromContract(item)
	if dto.ID != item.ID || dto.Source != item.Source || dto.Count != 2 || dto.Priority != 3 {
		t.Fatalf("dto = %+v, want the item's fields", dto)
	}
	if dto.ReceivedAt != "2026-11-25T09:00:00Z" {
		t.Fatalf("receivedAt = %q, want RFC3339 UTC", dto.ReceivedAt)
	}
	if len(dto.TaskIDs) != 1 || dto.TaskIDs[0] != "task-1" {
		t.Fatalf("taskIds = %v, want [task-1]", dto.TaskIDs)
	}
	// Defensive copy: mutating the item must not reach the DTO.
	item.TaskIDs[0] = "mutated"
	if dto.TaskIDs[0] != "task-1" {
		t.Fatal("dto shares the item's TaskIDs slice")
	}
}

func TestInboxItemDTOZeroTimes(t *testing.T) {
	dto := inboxItemFromContract(contracts.InboxItem{ID: "x"})
	if dto.ReceivedAt != "" || dto.LastSeenAt != "" || dto.ExpiresAt != "" {
		t.Fatalf("zero times = %q/%q/%q, want empty strings", dto.ReceivedAt, dto.LastSeenAt, dto.ExpiresAt)
	}
}

func TestTaskLeaseDTOMapping(t *testing.T) {
	until := time.Date(2026, 11, 25, 9, 10, 0, 0, time.UTC)
	lease := contracts.TaskLease{
		TaskID: "task-1", Owner: "bot-1", Until: until,
		Heartbeat: until.Add(-time.Minute), State: contracts.LeaseStateReview,
		Evidence:   []contracts.Evidence{{Kind: "link", Ref: "https://x", Note: "src"}},
		ReviewNote: "",
	}
	dto := taskLeaseFromContract(lease)
	if dto.TaskID != "task-1" || dto.Owner != "bot-1" || dto.State != "review" {
		t.Fatalf("dto = %+v", dto)
	}
	if dto.Until != "2026-11-25T09:10:00Z" {
		t.Fatalf("until = %q", dto.Until)
	}
	if len(dto.Evidence) != 1 || dto.Evidence[0].Kind != "link" {
		t.Fatalf("evidence = %+v", dto.Evidence)
	}
}

func TestEvidenceRoundTrip(t *testing.T) {
	in := []evidenceDTO{{Kind: "file", Ref: "a.xlsx", Note: "结果"}}
	out := evidenceFromDTO(in)
	if len(out) != 1 || out[0].Kind != "file" || out[0].Ref != "a.xlsx" {
		t.Fatalf("round trip = %+v", out)
	}
	if evidenceFromDTO(nil) != nil {
		t.Fatal("nil evidence should stay nil (Submit rejects it with a typed error)")
	}
}

func TestOrchestrationErrorMapsTypedErrorsToClientErrors(t *testing.T) {
	for _, err := range []error{
		contracts.ErrLeaseHeld, contracts.ErrLeaseNotHeld, contracts.ErrLeaseEvidenceRequired,
		contracts.ErrInboxUnknownItem, contracts.ErrApprovalTokenExpired,
	} {
		mapped := orchestrationError(err)
		if mapped == nil || mapped.Code != codeInvalidRequest {
			t.Errorf("orchestrationError(%v) = %+v, want codeInvalidRequest", err, mapped)
		}
		if !strings.Contains(mapped.Message, "lease") && !strings.Contains(mapped.Message, "inbox") &&
			!strings.Contains(mapped.Message, "approval") {
			t.Errorf("mapped message = %q, want the original typed text", mapped.Message)
		}
	}
}

func TestNotifyRouteDTOMapping(t *testing.T) {
	route := contracts.NotifyRoute{
		Category: contracts.NotifyCategoryApproval, Primary: contracts.NotifyChannelWeChat,
		Fallback: []string{contracts.NotifyChannelDingTalk}, QuietHours: "22:30-08:00",
		Escalation: 15 * time.Minute, Enabled: true,
	}
	dto := notifyRouteFromContract(route)
	if dto.Category != route.Category || dto.Primary != route.Primary || !dto.Enabled {
		t.Fatalf("dto = %+v", dto)
	}
	if dto.EscalationMS != (15 * time.Minute).Milliseconds() {
		t.Fatalf("escalationMs = %d, want %d", dto.EscalationMS, (15 * time.Minute).Milliseconds())
	}
	if len(dto.Fallback) != 1 || dto.Fallback[0] != contracts.NotifyChannelDingTalk {
		t.Fatalf("fallback = %v", dto.Fallback)
	}
	route.Fallback[0] = "mutated"
	if dto.Fallback[0] != contracts.NotifyChannelDingTalk {
		t.Fatal("dto shares the route's fallback slice")
	}
}

func TestTaskToolsDTOMapping(t *testing.T) {
	updated := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	binding := contracts.TaskTools{
		TaskID: "task-1", Tools: []string{"browser_open", "mcp__github__search"}, UpdatedAt: updated,
	}
	dto := taskToolsFromContract(binding)
	if dto.TaskID != "task-1" || len(dto.Tools) != 2 || dto.Tools[1] != "mcp__github__search" {
		t.Fatalf("dto = %+v", dto)
	}
	if dto.UpdatedAt != "2026-11-25T09:00:00Z" {
		t.Fatalf("updatedAt = %q, want RFC3339 UTC", dto.UpdatedAt)
	}
	// Defensive copy: the policy on the wire must not be mutated by a caller.
	binding.Tools[0] = "mutated"
	if dto.Tools[0] != "browser_open" {
		t.Fatal("dto shares the binding's Tools slice")
	}
	// No binding is null on the wire, never []: [] would say "may run nothing",
	// which is a different policy from "no binding at all".
	if empty := taskToolsFromContract(contracts.TaskTools{TaskID: "task-2"}); empty.Tools != nil {
		t.Fatalf("empty binding tools = %v, want nil", empty.Tools)
	}
}

// toolsFakeLease is the lease service seen by the two new handlers: a map of
// bindings plus one injectable typed error.
type toolsFakeLease struct {
	fakeLease
	bound map[string][]string
	err   error
}

func (f *toolsFakeLease) SetTools(_ context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	if f.err != nil {
		return contracts.TaskTools{}, f.err
	}
	if len(tools) == 0 {
		delete(f.bound, taskID)
		return contracts.TaskTools{TaskID: taskID, UpdatedAt: time.Now()}, nil
	}
	f.bound[taskID] = append([]string(nil), tools...)
	return contracts.TaskTools{TaskID: taskID, Tools: tools, UpdatedAt: time.Now()}, nil
}

func (f *toolsFakeLease) Tools(_ context.Context, taskID string) (contracts.TaskTools, bool, error) {
	if f.err != nil {
		return contracts.TaskTools{}, false, f.err
	}
	tools, ok := f.bound[taskID]
	if !ok {
		return contracts.TaskTools{}, false, nil
	}
	return contracts.TaskTools{TaskID: taskID, Tools: tools, UpdatedAt: time.Now()}, true, nil
}

func TestTaskToolsHandlersRoundTrip(t *testing.T) {
	lease := &toolsFakeLease{bound: map[string][]string{}}
	gw := newReviewGateway(t, lease, nil)
	ctx := context.Background()
	marshal := func(params map[string]any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal %v: %v", params, err)
		}
		return raw
	}

	// set → TaskTools
	out, problem := gw.taskToolsSet(ctx, marshal(map[string]any{
		"id": "task-1", "tools": []string{"browser_open", "mcp__github__search"},
	}))
	if problem != nil {
		t.Fatalf("tasks.tools.set: %+v", problem)
	}
	set, ok := out.(taskToolsDTO)
	if !ok {
		t.Fatalf("tasks.tools.set result = %T, want taskToolsDTO", out)
	}
	if set.TaskID != "task-1" || len(set.Tools) != 2 || set.Tools[0] != "browser_open" {
		t.Fatalf("set = %+v", set)
	}

	// get → the same binding
	out, problem = gw.taskToolsGet(ctx, marshal(map[string]any{"id": "task-1"}))
	if problem != nil {
		t.Fatalf("tasks.tools.get: %+v", problem)
	}
	got, ok := out.(taskToolsDTO)
	if !ok || got.TaskID != "task-1" || len(got.Tools) != 2 {
		t.Fatalf("get = (%T) %+v", out, out)
	}

	// get on a task without a binding → {"tools":null}
	out, problem = gw.taskToolsGet(ctx, marshal(map[string]any{"id": "task-2"}))
	if problem != nil {
		t.Fatalf("tasks.tools.get(no binding): %+v", problem)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal absent result: %v", err)
	}
	if string(encoded) != `{"tools":null}` {
		t.Fatalf("absent result = %s, want {\"tools\":null}", encoded)
	}

	// clear → the binding is gone (an empty list, not a missing one)
	if _, problem := gw.taskToolsSet(ctx, marshal(map[string]any{"id": "task-1", "tools": []string{}})); problem != nil {
		t.Fatalf("tasks.tools.set(clear): %+v", problem)
	}
	if len(lease.bound["task-1"]) != 0 {
		t.Fatalf("binding after clear = %v, want none", lease.bound["task-1"])
	}

	// A missing tools field is a malformed request, never a silent clear: the
	// binding set above must still be there for task-1... which it is not, so
	// rebind first and then send the malformed request.
	if _, problem := gw.taskToolsSet(ctx, marshal(map[string]any{"id": "task-1", "tools": []string{"fs.read"}})); problem != nil {
		t.Fatalf("rebind: %+v", problem)
	}
	if _, problem := gw.taskToolsSet(ctx, marshal(map[string]any{"id": "task-1"})); problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("missing tools problem = %+v, want codeInvalidParams", problem)
	}
	if len(lease.bound["task-1"]) != 1 {
		t.Fatalf("a malformed request changed the binding: %v", lease.bound["task-1"])
	}
}

func TestTaskToolsHandlersMapTypedErrors(t *testing.T) {
	lease := &toolsFakeLease{bound: map[string][]string{}}
	gw := newReviewGateway(t, lease, nil)
	ctx := context.Background()
	raw, err := json.Marshal(map[string]any{"id": "task-1", "tools": []string{"browser_open"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	lease.err = contracts.ErrTaskToolsInvalid
	if _, problem := gw.taskToolsSet(ctx, raw); problem == nil || problem.Code != codeInvalidRequest ||
		!strings.Contains(problem.Message, "tool binding") {
		t.Fatalf("invalid binding problem = %+v, want codeInvalidRequest with the typed text", problem)
	}
	lease.err = contracts.ErrTaskToolsUnknownTask
	if _, problem := gw.taskToolsGet(ctx, raw); problem == nil || problem.Code != codeInvalidRequest ||
		!strings.Contains(problem.Message, "unknown task") {
		t.Fatalf("unknown task problem = %+v, want codeInvalidRequest with the typed text", problem)
	}
}

// --- pre-start approval (G2) -----------------------------------------------

// approvalFakeLease is the lease service seen by the four approval handlers: a
// policy map, one parked claim per task, and one injectable typed error.
type approvalFakeLease struct {
	fakeLease
	policies map[string]string
	parked   map[string]string // task id -> owner of the parked claim
	err      error
}

func (f *approvalFakeLease) SetApproval(_ context.Context, taskID, policy string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if policy != contracts.TaskApprovalNone && policy != contracts.TaskApprovalBeforeStart {
		return "", contracts.ErrLeaseInvalid
	}
	f.policies[taskID] = policy
	return policy, nil
}

func (f *approvalFakeLease) Approval(_ context.Context, taskID string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	policy, ok := f.policies[taskID]
	return policy, ok, nil
}

// Submit honours the injected error too: the gate refuses a submit, which is
// the exact call a teammate makes and the one the mapping must cover.
func (f *approvalFakeLease) Submit(context.Context, string, string, []contracts.Evidence) (contracts.TaskLease, error) {
	if f.err != nil {
		return contracts.TaskLease{}, f.err
	}
	return contracts.TaskLease{}, nil
}

func (f *approvalFakeLease) Approve(_ context.Context, taskID, approver string) (contracts.TaskLease, error) {
	if f.err != nil {
		return contracts.TaskLease{}, f.err
	}
	owner, ok := f.parked[taskID]
	if !ok {
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	delete(f.parked, taskID)
	return contracts.TaskLease{TaskID: taskID, Owner: owner, State: contracts.LeaseStateClaimed}, nil
}

func (f *approvalFakeLease) RejectStart(_ context.Context, taskID, approver, note string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.parked[taskID]; !ok {
		return contracts.ErrLeaseNotHeld
	}
	delete(f.parked, taskID)
	f.applied = append(f.applied, "reject-start:"+taskID+":"+approver+":"+note)
	return nil
}

func TestTaskApprovalHandlersRoundTrip(t *testing.T) {
	lease := &approvalFakeLease{policies: map[string]string{}, parked: map[string]string{"task-1": "bot-1"}}
	gw := newReviewGateway(t, lease, nil)
	ctx := context.Background()
	marshal := func(params map[string]any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal %v: %v", params, err)
		}
		return raw
	}

	// set → the stored policy
	out, problem := gw.taskApprovalSet(ctx, marshal(map[string]any{"id": "task-1", "policy": "before_start"}))
	if problem != nil {
		t.Fatalf("tasks.approval.set: %+v", problem)
	}
	if set, ok := out.(taskApprovalSetResult); !ok || set.Policy != contracts.TaskApprovalBeforeStart {
		t.Fatalf("tasks.approval.set result = (%T) %+v, want before_start", out, out)
	}

	// get → {policy,set}
	out, problem = gw.taskApprovalGet(ctx, marshal(map[string]any{"id": "task-1"}))
	if problem != nil {
		t.Fatalf("tasks.approval.get: %+v", problem)
	}
	if got, ok := out.(taskApprovalGetResult); !ok || got.Policy != contracts.TaskApprovalBeforeStart || !got.Set {
		t.Fatalf("tasks.approval.get result = (%T) %+v, want (before_start, true)", out, out)
	}
	// A task with no policy answers the default, never an empty string.
	out, problem = gw.taskApprovalGet(ctx, marshal(map[string]any{"id": "task-2"}))
	if problem != nil {
		t.Fatalf("tasks.approval.get(no policy): %+v", problem)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal absent result: %v", err)
	}
	if string(encoded) != `{"policy":"none","set":false}` {
		t.Fatalf("absent result = %s, want {\"policy\":\"none\",\"set\":false}", encoded)
	}

	// approve → the lease, now claimed, same owner
	out, problem = gw.taskApprove(ctx, marshal(map[string]any{"id": "task-1", "approver": "lead"}))
	if problem != nil {
		t.Fatalf("tasks.approve: %+v", problem)
	}
	if approved, ok := out.(taskLeaseDTO); !ok || approved.State != contracts.LeaseStateClaimed || approved.Owner != "bot-1" {
		t.Fatalf("tasks.approve result = (%T) %+v, want claimed by bot-1", out, out)
	}

	// reject-start → {ok:true}, and the parked claim is released
	lease.parked["task-2"] = "bot-2"
	out, problem = gw.taskRejectStart(ctx, marshal(map[string]any{
		"id": "task-2", "approver": "lead", "note": "先别动生产库",
	}))
	if problem != nil {
		t.Fatalf("tasks.reject-start: %+v", problem)
	}
	if ok, isMap := out.(map[string]any); !isMap || ok["ok"] != true {
		t.Fatalf("tasks.reject-start result = (%T) %+v, want {ok:true}", out, out)
	}
	if _, still := lease.parked["task-2"]; still {
		t.Fatal("reject-start left the claim parked")
	}
	if len(lease.applied) != 1 || !strings.Contains(lease.applied[0], "reject-start:task-2:lead:") {
		t.Fatalf("applied = %v, want the recorded refusal", lease.applied)
	}

	// A missing policy is a malformed request, never a silent "none".
	if _, problem := gw.taskApprovalSet(ctx, marshal(map[string]any{"id": "task-1"})); problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("missing policy problem = %+v, want codeInvalidParams", problem)
	}
	if lease.policies["task-1"] != contracts.TaskApprovalBeforeStart {
		t.Fatalf("a malformed request changed the policy: %q", lease.policies["task-1"])
	}
}

// TestTaskApprovalHandlersMapTypedErrors pins the two refusals a client must be
// able to tell apart: a bad value (-32600, the domain said no) and the gate
// itself (also -32600, but with the gate's own text so the UI can say why).
func TestTaskApprovalHandlersMapTypedErrors(t *testing.T) {
	lease := &approvalFakeLease{policies: map[string]string{}, parked: map[string]string{}}
	gw := newReviewGateway(t, lease, nil)
	ctx := context.Background()
	marshal := func(params map[string]any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal %v: %v", params, err)
		}
		return raw
	}

	if _, problem := gw.taskApprovalSet(ctx, marshal(map[string]any{"id": "task-1", "policy": "before_send"})); problem == nil ||
		problem.Code != codeInvalidRequest || !strings.Contains(problem.Message, "invalid") {
		t.Fatalf("invalid policy problem = %+v, want codeInvalidRequest with the typed text", problem)
	}

	// The gate refusing a submit is the case a teammate actually hits.
	lease.err = contracts.ErrLeaseApprovalRequired
	submit := marshal(map[string]any{
		"id": "task-1", "owner": "bot-1",
		"evidence": []map[string]any{{"kind": "link", "ref": "https://x"}},
	})
	if _, problem := gw.taskSubmit(ctx, submit); problem == nil || problem.Code != codeInvalidRequest ||
		!strings.Contains(problem.Message, "approval required") {
		t.Fatalf("submit before approval problem = %+v, want codeInvalidRequest with the gate's text", problem)
	}
	if _, problem := gw.taskApprove(ctx, marshal(map[string]any{"id": "task-1", "approver": "lead"})); problem == nil ||
		problem.Code != codeInvalidRequest {
		t.Fatalf("approve error problem = %+v, want codeInvalidRequest", problem)
	}
	if _, problem := gw.taskRejectStart(ctx, marshal(map[string]any{"id": "task-1", "approver": "lead", "note": "no"})); problem == nil ||
		problem.Code != codeInvalidRequest {
		t.Fatalf("reject-start error problem = %+v, want codeInvalidRequest", problem)
	}
}

// --- per-task budget (G11) -------------------------------------------------

// budgetFakeLease is the lease service seen by the three budget handlers: one
// row per task and one injectable typed error. It deliberately does not
// re-implement the ceiling arithmetic (the module tests own that) — what the
// gateway must get right is the wire shape and the mapping.
type budgetFakeLease struct {
	fakeLease
	rows map[string]contracts.TaskBudget
	err  error
}

func (f *budgetFakeLease) SetBudget(_ context.Context, taskID string, budget contracts.TaskBudget) (contracts.TaskBudget, error) {
	if f.err != nil {
		return contracts.TaskBudget{}, f.err
	}
	if budget.MaxTokens < 0 || budget.MaxMinutes < 0 {
		return contracts.TaskBudget{}, contracts.ErrLeaseInvalid
	}
	stored := contracts.TaskBudget{TaskID: taskID, MaxTokens: budget.MaxTokens, MaxMinutes: budget.MaxMinutes}
	if previous, ok := f.rows[taskID]; ok {
		stored.UsedTokens = previous.UsedTokens
		stored.StartedAt = previous.StartedAt
	}
	f.rows[taskID] = stored
	return stored, nil
}

func (f *budgetFakeLease) Budget(_ context.Context, taskID string) (contracts.TaskBudget, bool, error) {
	if f.err != nil {
		return contracts.TaskBudget{}, false, f.err
	}
	row, ok := f.rows[taskID]
	return row, ok, nil
}

func (f *budgetFakeLease) Consume(_ context.Context, taskID string, tokens int) (contracts.TaskBudget, error) {
	if f.err != nil {
		return contracts.TaskBudget{}, f.err
	}
	row := f.rows[taskID]
	row.TaskID = taskID
	row.UsedTokens += tokens
	if row.StartedAt.IsZero() {
		row.StartedAt = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	}
	if row.MaxTokens > 0 && row.UsedTokens > row.MaxTokens {
		row.Exhausted = true
		f.rows[taskID] = row
		return row, contracts.ErrLeaseBudgetExhausted
	}
	f.rows[taskID] = row
	return row, nil
}

func TestTaskBudgetHandlersRoundTrip(t *testing.T) {
	lease := &budgetFakeLease{rows: map[string]contracts.TaskBudget{}}
	gw := newReviewGateway(t, lease, nil)
	ctx := context.Background()
	marshal := func(params map[string]any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal %v: %v", params, err)
		}
		return raw
	}

	// set → the stored ceilings, on the wire as maxTokens/maxMinutes.
	out, problem := gw.taskBudgetSet(ctx, marshal(map[string]any{
		"id": "task-1", "maxTokens": 1000, "maxMinutes": 30,
	}))
	if problem != nil {
		t.Fatalf("tasks.budget.set: %+v", problem)
	}
	budget, ok := out.(taskBudgetDTO)
	if !ok || budget.TaskID != "task-1" || budget.MaxTokens != 1000 || budget.MaxMinutes != 30 {
		t.Fatalf("tasks.budget.set result = (%T) %+v, want task-1 1000/30", out, out)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	// The limits are always present (0 means unlimited) and exhausted always
	// present: the client must be able to tell "no ceiling" from "absent".
	for _, want := range []string{
		`"taskId":"task-1"`, `"maxTokens":1000`, `"maxMinutes":30`,
		`"usedTokens":0`, `"exhausted":false`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("DTO = %s, missing %s", encoded, want)
		}
	}

	// get → the same row.
	out, problem = gw.taskBudgetGet(ctx, marshal(map[string]any{"id": "task-1"}))
	if problem != nil {
		t.Fatalf("tasks.budget.get: %+v", problem)
	}
	if got, ok := out.(taskBudgetDTO); !ok || got.MaxTokens != 1000 {
		t.Fatalf("tasks.budget.get result = (%T) %+v, want the stored ceiling", out, out)
	}

	// get on a task with no budget → {budget:null}, the same absent shape as
	// tasks.lease.
	out, problem = gw.taskBudgetGet(ctx, marshal(map[string]any{"id": "task-2"}))
	if problem != nil {
		t.Fatalf("tasks.budget.get(absent): %+v", problem)
	}
	encoded, err = json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal absent result: %v", err)
	}
	if string(encoded) != `{"budget":null}` {
		t.Fatalf("absent result = %s, want {\"budget\":null}", encoded)
	}

	// consume → the accumulated row.
	out, problem = gw.taskBudgetConsume(ctx, marshal(map[string]any{"id": "task-1", "tokens": 120}))
	if problem != nil {
		t.Fatalf("tasks.budget.consume: %+v", problem)
	}
	if spent, ok := out.(taskBudgetDTO); !ok || spent.UsedTokens != 120 || spent.Exhausted {
		t.Fatalf("tasks.budget.consume result = (%T) %+v, want 120 used", out, out)
	}

	// A negative ceiling is a domain refusal (-32600), not a clamped value.
	if _, problem := gw.taskBudgetSet(ctx, marshal(map[string]any{"id": "task-1", "maxTokens": -1})); problem == nil ||
		problem.Code != codeInvalidRequest {
		t.Fatalf("negative maxTokens problem = %+v, want codeInvalidRequest", problem)
	}
	if lease.rows["task-1"].MaxTokens != 1000 {
		t.Fatalf("a rejected set changed the ceiling: %+v", lease.rows["task-1"])
	}
}

// TestTaskBudgetHandlersMapTypedErrors pins the stop on the wire: crossing a
// ceiling is the domain saying no (-32600, with used/limit in the text), not an
// internal failure (-32603). The write did happen, so the client re-reads.
func TestTaskBudgetHandlersMapTypedErrors(t *testing.T) {
	lease := &budgetFakeLease{rows: map[string]contracts.TaskBudget{}}
	gw := newReviewGateway(t, lease, nil)
	ctx := context.Background()
	marshal := func(params map[string]any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal %v: %v", params, err)
		}
		return raw
	}

	lease.rows["task-1"] = contracts.TaskBudget{TaskID: "task-1", MaxTokens: 10}
	if _, problem := gw.taskBudgetConsume(ctx, marshal(map[string]any{"id": "task-1", "tokens": 11})); problem == nil ||
		problem.Code != codeInvalidRequest || !strings.Contains(problem.Message, "budget exhausted") {
		t.Fatalf("exhausted consume problem = %+v, want codeInvalidRequest with the typed text", problem)
	}

	lease.err = contracts.ErrLeaseBudgetExhausted
	if _, problem := gw.taskBudgetConsume(ctx, marshal(map[string]any{"id": "task-2", "tokens": 1})); problem == nil ||
		problem.Code != codeInvalidRequest {
		t.Fatalf("typed consume error problem = %+v, want codeInvalidRequest", problem)
	}
	if _, problem := gw.taskBudgetGet(ctx, marshal(map[string]any{"id": "task-1"})); problem == nil ||
		problem.Code != codeInvalidRequest {
		t.Fatalf("typed get error problem = %+v, want codeInvalidRequest", problem)
	}
	if _, problem := gw.taskBudgetSet(ctx, marshal(map[string]any{"id": "task-1", "maxTokens": 5})); problem == nil ||
		problem.Code != codeInvalidRequest {
		t.Fatalf("typed set error problem = %+v, want codeInvalidRequest", problem)
	}
}
