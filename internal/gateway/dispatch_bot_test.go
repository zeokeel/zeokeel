package gateway

// Dispatching to a bot is how a grant stops being a note: the task is owned by the
// bot and the bot's allowed tools become the task's binding. An unknown bot must
// fail the dispatch instead of creating an unowned task.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeBots struct {
	instances map[string]contracts.BotInstance
}

func (f *fakeBots) Templates(context.Context) ([]contracts.BotTemplate, error) {
	return []contracts.BotTemplate{{ID: "lead", Name: "Lead"}}, nil
}
func (f *fakeBots) PutTemplate(_ context.Context, template contracts.BotTemplate) (contracts.BotTemplate, error) {
	return template, nil
}
func (f *fakeBots) Template(_ context.Context, id string) (contracts.BotTemplate, bool, error) {
	if id == "lead" {
		return contracts.BotTemplate{ID: "lead", Name: "Lead"}, true, nil
	}
	return contracts.BotTemplate{}, false, nil
}
func (f *fakeBots) Instances(context.Context) ([]contracts.BotInstance, error) { return nil, nil }
func (f *fakeBots) Create(_ context.Context, instance contracts.BotInstance) (contracts.BotInstance, error) {
	return instance, nil
}
func (f *fakeBots) Update(_ context.Context, instance contracts.BotInstance) (contracts.BotInstance, error) {
	f.instances[instance.ID] = instance
	return instance, nil
}
func (f *fakeBots) Get(_ context.Context, id string) (contracts.BotInstance, error) {
	instance, ok := f.instances[id]
	if !ok {
		return contracts.BotInstance{}, contracts.ErrBotUnknown
	}
	return instance, nil
}

type fakeTriageRecorder struct {
	mu     sync.Mutex
	seen   []contracts.NewTask
	nextID int
}

func (f *fakeTriageRecorder) Apply(_ context.Context, inboxID string, decision contracts.TriageDecision) (contracts.TriageResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	if decision.Task != nil {
		f.seen = append(f.seen, *decision.Task)
	}
	taskID := "task-bot"
	return contracts.TriageResult{
		Inbox:   contracts.InboxItem{ID: inboxID, Status: contracts.InboxStatusTask, TaskIDs: []string{taskID}},
		TaskIDs: []string{taskID}, Created: true,
	}, nil
}

type fakeLeaseTools struct {
	mu    sync.Mutex
	tools map[string][]string
	err   error
}

func (f *fakeLeaseTools) Claim(context.Context, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLeaseTools) Heartbeat(context.Context, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLeaseTools) Release(context.Context, string, string) error { return nil }
func (f *fakeLeaseTools) Submit(context.Context, string, string, []contracts.Evidence) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLeaseTools) Accept(context.Context, string, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLeaseTools) Reject(context.Context, string, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLeaseTools) Lease(context.Context, string) (contracts.TaskLease, bool, error) {
	return contracts.TaskLease{}, false, nil
}
func (f *fakeLeaseTools) Expire(context.Context) ([]string, error) { return nil, nil }
func (f *fakeLeaseTools) SetTools(_ context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	if f.err != nil {
		return contracts.TaskTools{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tools == nil {
		f.tools = map[string][]string{}
	}
	f.tools[taskID] = tools
	return contracts.TaskTools{TaskID: taskID, Tools: tools, UpdatedAt: time.Now()}, nil
}
func (f *fakeLeaseTools) Tools(_ context.Context, taskID string) (contracts.TaskTools, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tools, ok := f.tools[taskID]
	return contracts.TaskTools{TaskID: taskID, Tools: tools}, ok, nil
}
func (f *fakeLeaseTools) SetApproval(context.Context, string, string) (string, error) { return "", nil }
func (f *fakeLeaseTools) Approval(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeLeaseTools) Approve(context.Context, string, string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, nil
}
func (f *fakeLeaseTools) RejectStart(context.Context, string, string, string) error { return nil }
func (f *fakeLeaseTools) SetBudget(context.Context, string, contracts.TaskBudget) (contracts.TaskBudget, error) {
	return contracts.TaskBudget{}, nil
}
func (f *fakeLeaseTools) Budget(context.Context, string) (contracts.TaskBudget, bool, error) {
	return contracts.TaskBudget{}, false, nil
}
func (f *fakeLeaseTools) Consume(context.Context, string, int) (contracts.TaskBudget, error) {
	return contracts.TaskBudget{}, nil
}

func newDispatchGateway(t *testing.T, bots *fakeBots, triage *fakeTriageRecorder, leases *fakeLeaseTools) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyBots, contracts.BotService(bots)); err != nil {
		t.Fatalf("provide bots: %v", err)
	}
	if _, err := root.Provide(contracts.KeyTriage, contracts.TriageService(triage)); err != nil {
		t.Fatalf("provide triage: %v", err)
	}
	if leases != nil {
		if _, err := root.Provide(contracts.KeyTaskLease, contracts.TaskLeaseService(leases)); err != nil {
			t.Fatalf("provide lease: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

func TestDispatchToABotOwnsTheTaskAndBindsItsTools(t *testing.T) {
	bots := &fakeBots{instances: map[string]contracts.BotInstance{
		"research": {ID: "research", TemplateID: "lead", Name: "Research", AllowedTools: []string{"browser_open"}},
	}}
	triage := &fakeTriageRecorder{}
	leases := &fakeLeaseTools{}
	gw := newDispatchGateway(t, bots, triage, leases)

	raw, _ := json.Marshal(map[string]any{"id": "i1", "subject": "查一下", "botId": "research"})
	if _, problem := gw.inboxDispatch(context.Background(), raw); problem != nil {
		t.Fatalf("dispatch: %+v", problem)
	}
	if len(triage.seen) != 1 || triage.seen[0].Assignee != "research" {
		t.Fatalf("task = %+v, want the bot as owner", triage.seen)
	}
	leases.mu.Lock()
	defer leases.mu.Unlock()
	if len(leases.tools["task-bot"]) != 1 || leases.tools["task-bot"][0] != "browser_open" {
		t.Fatalf("bound tools = %v, want the bot's grant", leases.tools)
	}
}

func TestDispatchToAnUnknownBotFails(t *testing.T) {
	bots := &fakeBots{instances: map[string]contracts.BotInstance{}}
	triage := &fakeTriageRecorder{}
	gw := newDispatchGateway(t, bots, triage, &fakeLeaseTools{})

	raw, _ := json.Marshal(map[string]any{"id": "i1", "subject": "查一下", "botId": "ghost"})
	if _, problem := gw.inboxDispatch(context.Background(), raw); problem == nil {
		t.Fatal("dispatch to an unknown bot created a task")
	}
	if len(triage.seen) != 0 {
		t.Fatalf("tasks created = %+v, want none", triage.seen)
	}
}

func TestDispatchWithoutABotBehavesAsBefore(t *testing.T) {
	bots := &fakeBots{instances: map[string]contracts.BotInstance{}}
	triage := &fakeTriageRecorder{}
	gw := newDispatchGateway(t, bots, triage, &fakeLeaseTools{})

	raw, _ := json.Marshal(map[string]any{"id": "i1", "subject": "查一下", "assignee": "ann"})
	if _, problem := gw.inboxDispatch(context.Background(), raw); problem != nil {
		t.Fatalf("dispatch: %+v", problem)
	}
	if len(triage.seen) != 1 || triage.seen[0].Assignee != "ann" {
		t.Fatalf("task = %+v", triage.seen)
	}
}

var _ = errors.Is

func TestDispatchTouchesTheBot(t *testing.T) {
	bots := &fakeBots{instances: map[string]contracts.BotInstance{
		"research": {ID: "research", TemplateID: "lead", Name: "Research"},
	}}
	triage := &fakeTriageRecorder{}
	gw := newDispatchGateway(t, bots, triage, &fakeLeaseTools{})

	raw, _ := json.Marshal(map[string]any{"id": "i1", "subject": "查一下", "botId": "research"})
	if _, problem := gw.inboxDispatch(context.Background(), raw); problem != nil {
		t.Fatalf("dispatch: %+v", problem)
	}
	if bots.instances["research"].LastSeenAt.IsZero() {
		t.Fatal("the dispatched bot was not marked as seen")
	}
}

func TestABotTakesOneJobAtATime(t *testing.T) {
	bots := &fakeBots{instances: map[string]contracts.BotInstance{
		"research": {ID: "research", TemplateID: "lead", Name: "Research"},
	}}
	triage := &fakeTriageRecorder{}
	leases := &busyAwareLease{held: map[string]contracts.TaskLease{
		"task-live": {TaskID: "task-live", Owner: "research", State: contracts.LeaseStateClaimed},
	}}
	gateway := newDispatchGatewayWithBoard(t, bots, triage, leases,
		[]contracts.BoardTask{{ID: "task-live", Owner: "research"}})

	raw, _ := json.Marshal(map[string]any{"id": "i1", "subject": "再来一个", "botId": "research"})
	_, problem := gateway.inboxDispatch(context.Background(), raw)
	if problem == nil {
		t.Fatal("a busy bot was handed a second job")
	}
	if problem.Code != codeInvalidRequest {
		t.Fatalf("code = %d, want %d", problem.Code, codeInvalidRequest)
	}
	if !strings.Contains(problem.Message, "task-live") {
		t.Fatalf("message = %q, want the task in flight named", problem.Message)
	}
	if len(triage.seen) != 0 {
		t.Fatalf("a task was created anyway: %+v", triage.seen)
	}
}

func TestAnIdleBotTakesTheJob(t *testing.T) {
	bots := &fakeBots{instances: map[string]contracts.BotInstance{
		"research": {ID: "research", TemplateID: "lead", Name: "Research"},
	}}
	triage := &fakeTriageRecorder{}
	leases := &busyAwareLease{held: map[string]contracts.TaskLease{
		"task-done": {TaskID: "task-done", Owner: "research", State: contracts.LeaseStateReview},
	}}
	gateway := newDispatchGatewayWithBoard(t, bots, triage, leases,
		[]contracts.BoardTask{{ID: "task-done", Owner: "research"}})

	raw, _ := json.Marshal(map[string]any{"id": "i1", "subject": "新的", "botId": "research"})
	if _, problem := gateway.inboxDispatch(context.Background(), raw); problem != nil {
		t.Fatalf("dispatch: %+v", problem)
	}
	if len(triage.seen) != 1 {
		t.Fatalf("tasks created = %d, want one", len(triage.seen))
	}
}

// busyAwareLease is the fake lease set that can report a live lease per task.
type busyAwareLease struct {
	fakeLeaseTools
	held map[string]contracts.TaskLease
}

func (f *busyAwareLease) Lease(_ context.Context, taskID string) (contracts.TaskLease, bool, error) {
	lease, ok := f.held[taskID]
	return lease, ok, nil
}

type staticBoard struct {
	tasks []contracts.BoardTask
}

func (b *staticBoard) List(context.Context) ([]contracts.BoardTask, error) { return b.tasks, nil }
func (b *staticBoard) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}
func (b *staticBoard) SetStatus(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}
func (b *staticBoard) Delete(context.Context, string) error { return nil }

func newDispatchGatewayWithBoard(t *testing.T, bots *fakeBots, triage *fakeTriageRecorder, leases contracts.TaskLeaseService, tasks []contracts.BoardTask) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyBots, contracts.BotService(bots)); err != nil {
		t.Fatalf("provide bots: %v", err)
	}
	if _, err := root.Provide(contracts.KeyTriage, contracts.TriageService(triage)); err != nil {
		t.Fatalf("provide triage: %v", err)
	}
	if _, err := root.Provide(contracts.KeyTaskLease, leases); err != nil {
		t.Fatalf("provide lease: %v", err)
	}
	if _, err := root.Provide(contracts.KeyTaskBoard, contracts.TaskBoard(&staticBoard{tasks: tasks})); err != nil {
		t.Fatalf("provide board: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}
