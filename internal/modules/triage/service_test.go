package triage_test

// TDD for the inbox → task board seam. The two facts ("something arrived" and
// "someone is working on it") must never disagree: a failed board write leaves
// the inbox untouched, and applying the same decision twice creates one task.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/triage"
)

// --- fake task board -------------------------------------------------------

type fakeBoard struct {
	mu        sync.Mutex
	tasks     map[string]contracts.BoardTask
	assigned  map[string]string
	seq       int
	createErr error
}

func newFakeBoard() *fakeBoard {
	return &fakeBoard{tasks: map[string]contracts.BoardTask{}, assigned: map[string]string{}}
}

func (b *fakeBoard) List(context.Context) ([]contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]contracts.BoardTask, 0, len(b.tasks))
	for _, task := range b.tasks {
		out = append(out, task)
	}
	return out, nil
}

func (b *fakeBoard) Create(_ context.Context, subject, description string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.createErr != nil {
		return contracts.BoardTask{}, b.createErr
	}
	b.seq++
	task := contracts.BoardTask{
		ID: "task-" + string(rune('0'+b.seq)), Subject: subject, Description: description,
		Status: contracts.TaskStatusPending, Created: time.Now(), Updated: time.Now(),
	}
	b.tasks[task.ID] = task
	return task, nil
}

func (b *fakeBoard) SetStatus(_ context.Context, id, status string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return contracts.BoardTask{}, errors.New("fake board: unknown task")
	}
	task.Status = status
	b.tasks[id] = task
	return task, nil
}

func (b *fakeBoard) Delete(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.tasks, id)
	return nil
}

func (b *fakeBoard) Assign(_ context.Context, id, owner string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assigned[id] = owner
	task := b.tasks[id]
	task.Owner = owner
	b.tasks[id] = task
	return task, nil
}

func (b *fakeBoard) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.tasks)
}

// --- helpers ---------------------------------------------------------------

func newServices(t *testing.T) (*triage.Service, contracts.InboxService, *fakeBoard) {
	t.Helper()
	svc := inbox.NewService(inbox.Config{Now: time.Now})
	board := newFakeBoard()
	return triage.NewService(svc, board), svc, board
}

func arrive(t *testing.T, svc contracts.InboxService) contracts.InboxItem {
	t.Helper()
	stored, _, err := svc.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", Channel: "dm", Sender: "owner", DedupKey: "k1", Title: "查退款率",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return stored
}

func TestApplyTaskCreatesAssignsAndMarks(t *testing.T) {
	svc, inboxSvc, board := newServices(t)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(ctx, stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject: "查退款率", Detail: "上周", WriteScopes: []string{"reports/"}, Assignee: "research-bot",
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Created {
		t.Fatal("first decision reported not created")
	}
	if len(result.TaskIDs) != 1 {
		t.Fatalf("task ids = %v, want one", result.TaskIDs)
	}
	if board.count() != 1 {
		t.Fatalf("board tasks = %d, want 1", board.count())
	}
	if got := board.assigned[result.TaskIDs[0]]; got != "research-bot" {
		t.Fatalf("assignee = %q, want research-bot", got)
	}
	if result.Inbox.Status != contracts.InboxStatusTask {
		t.Fatalf("inbox status = %q, want task", result.Inbox.Status)
	}
	if len(result.Inbox.TaskIDs) != 1 || result.Inbox.TaskIDs[0] != result.TaskIDs[0] {
		t.Fatalf("inbox task ids = %v, want %v", result.Inbox.TaskIDs, result.TaskIDs)
	}
}

func TestApplyTaskIsIdempotent(t *testing.T) {
	svc, inboxSvc, board := newServices(t)
	ctx := context.Background()
	stored := arrive(t, inboxSvc)
	decision := contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "查退款率"},
	}
	first, err := svc.Apply(ctx, stored.ID, decision)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := svc.Apply(ctx, stored.ID, decision)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second.Created {
		t.Fatal("second apply created a second task")
	}
	if board.count() != 1 {
		t.Fatalf("board tasks = %d, want 1", board.count())
	}
	if len(second.TaskIDs) != 1 || second.TaskIDs[0] != first.TaskIDs[0] {
		t.Fatalf("second ids = %v, want %v", second.TaskIDs, first.TaskIDs)
	}
}

func TestApplyRejectsUnknownInboxItem(t *testing.T) {
	svc, _, _ := newServices(t)
	if _, err := svc.Apply(context.Background(), "missing", contracts.TriageDecision{
		Action: contracts.InboxActionIgnored,
	}); !errors.Is(err, contracts.ErrInboxUnknownItem) {
		t.Fatalf("err = %v, want ErrInboxUnknownItem", err)
	}
}

func TestApplyRejectsTaskActionWithoutTask(t *testing.T) {
	svc, inboxSvc, _ := newServices(t)
	stored := arrive(t, inboxSvc)
	if _, err := svc.Apply(context.Background(), stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
	}); !errors.Is(err, contracts.ErrTriageInvalid) {
		t.Fatalf("err = %v, want ErrTriageInvalid", err)
	}
}

func TestApplyBoardFailureLeavesInboxUntouched(t *testing.T) {
	svc, inboxSvc, board := newServices(t)
	board.createErr = errors.New("board down")
	stored := arrive(t, inboxSvc)

	if _, err := svc.Apply(context.Background(), stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x"},
	}); err == nil {
		t.Fatal("expected the board failure to surface")
	}
	got, err := inboxSvc.Get(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != contracts.InboxStatusNew {
		t.Fatalf("inbox status = %q after a failed board write, want new", got.Status)
	}
	if len(got.TaskIDs) != 0 {
		t.Fatalf("inbox task ids = %v after a failed board write, want none", got.TaskIDs)
	}
}

func TestApplyIgnoreDoesNotTouchTheBoard(t *testing.T) {
	svc, inboxSvc, board := newServices(t)
	stored := arrive(t, inboxSvc)

	result, err := svc.Apply(context.Background(), stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionIgnored, Note: "噪音",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Inbox.Status != contracts.InboxStatusIgnored {
		t.Fatalf("status = %q, want ignored", result.Inbox.Status)
	}
	if board.count() != 0 {
		t.Fatalf("board tasks = %d, want 0", board.count())
	}
}

func TestApplyTaskWithoutAssigneeSkipsAssign(t *testing.T) {
	svc, inboxSvc, board := newServices(t)
	stored := arrive(t, inboxSvc)
	result, err := svc.Apply(context.Background(), stored.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "x"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, ok := board.assigned[result.TaskIDs[0]]; ok && got != "" {
		t.Fatalf("assignee = %q, want none", got)
	}
}
