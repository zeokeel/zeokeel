package audit_test

// The automatic audit loop (M1), tested through its seams: the queue says what
// is waiting, the review log says what was decided, the rework counter says how
// many rounds are left, and the chat service is where a session is started.
//
// The loop is a poller, so every test is "run a sweep, look at what changed".
// What must hold:
//   - one auditor per lease attempt, never two (a sweep is idempotent);
//   - a rejection dispatches the executor again, except at the ceiling, where
//     the human is told instead and told only once;
//   - an acceptance forgets the task;
//   - one broken task does not stop the others.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/audit"
)

type send struct{ session, prompt string }

type fakeChat struct {
	next     int
	sends    []send
	presets  map[string]string
	bindings map[string]string
	failOn   string // substring of a prompt that must fail Send
}

func (c *fakeChat) NewSession(title string) (string, error) {
	c.next++
	return "sess-" + title, nil
}

func (c *fakeChat) Send(sessionID, prompt string) error {
	if c.failOn != "" && strings.Contains(prompt, c.failOn) {
		return errors.New("send refused")
	}
	c.sends = append(c.sends, send{session: sessionID, prompt: prompt})
	return nil
}

func (c *fakeChat) BindSessionTask(_ context.Context, sessionID, taskID string) error {
	if c.bindings == nil {
		c.bindings = map[string]string{}
	}
	c.bindings[sessionID] = taskID
	return nil
}

func (c *fakeChat) SetSessionPreset(sessionID, presetID string) error {
	if c.presets == nil {
		c.presets = map[string]string{}
	}
	c.presets[sessionID] = presetID
	return nil
}

type fakeQueue struct{ leases []contracts.TaskLease }

func (q *fakeQueue) AwaitingReview(context.Context) ([]contracts.TaskLease, error) {
	return append([]contracts.TaskLease(nil), q.leases...), nil
}

type fakeLog struct {
	rows map[string][]contracts.TaskReview
}

func (l *fakeLog) Reviews(_ context.Context, taskID string) ([]contracts.TaskReview, error) {
	return l.rows[taskID], nil
}

type fakeRework struct {
	rows map[string]contracts.TaskRework
}

func (r *fakeRework) Rework(_ context.Context, taskID string) (contracts.TaskRework, error) {
	return r.rows[taskID], nil
}

type fakeBoard struct{ tasks []contracts.BoardTask }

func (b *fakeBoard) List(context.Context) ([]contracts.BoardTask, error) {
	return append([]contracts.BoardTask(nil), b.tasks...), nil
}

type fakeNotifier struct{ deliveries []string }

func (n *fakeNotifier) Deliver(_ context.Context, category, text string) (contracts.DispatchReceipt, error) {
	n.deliveries = append(n.deliveries, category+": "+text)
	return contracts.DispatchReceipt{}, nil
}

// harness bundles the fakes and the service under test.
type harness struct {
	chat     *fakeChat
	queue    *fakeQueue
	log      *fakeLog
	rework   *fakeRework
	board    *fakeBoard
	notifier *fakeNotifier
	service  *audit.Service
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		chat:     &fakeChat{},
		queue:    &fakeQueue{},
		log:      &fakeLog{rows: map[string][]contracts.TaskReview{}},
		rework:   &fakeRework{rows: map[string]contracts.TaskRework{}},
		board:    &fakeBoard{},
		notifier: &fakeNotifier{},
	}
	h.service = audit.New(audit.Deps{
		Chat:     h.chat,
		Queue:    h.queue,
		Reviews:  h.log,
		Rework:   h.rework,
		Board:    h.board,
		Notifier: h.notifier,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	return h
}

// awaiting puts one task in the review queue with a distinct lease attempt.
func (h *harness) awaiting(taskID string, until time.Time) {
	h.queue.leases = append(h.queue.leases, contracts.TaskLease{
		TaskID: taskID, Owner: "executor", State: contracts.LeaseStateReview, Until: until,
	})
}

func TestSweepStartsOneAuditorPerLeaseAttempt(t *testing.T) {
	h := newHarness(t)
	h.board.tasks = []contracts.BoardTask{{ID: "task-1", Subject: "修复登录", Description: "验收：单测通过"}}
	h.awaiting("task-1", time.Unix(1_700_000_100, 0))

	report, err := h.service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Queued != 1 || report.AuditorsStarted != 1 {
		t.Fatalf("report = %+v, want 1 queued and 1 auditor", report)
	}
	if len(h.chat.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(h.chat.sends))
	}
	got := h.chat.sends[0]
	if h.chat.presets[got.session] != contracts.PresetAuditorID {
		t.Fatalf("preset = %q, want auditor", h.chat.presets[got.session])
	}
	if h.chat.bindings[got.session] != "task-1" {
		t.Fatalf("binding = %q, want task-1", h.chat.bindings[got.session])
	}
	if !strings.Contains(got.prompt, "修复登录") || !strings.Contains(got.prompt, "单测通过") {
		t.Fatalf("prompt = %q, want the task subject and its acceptance line", got.prompt)
	}

	// The same lease attempt again: nothing new must start.
	if _, err := h.service.Sweep(context.Background()); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if len(h.chat.sends) != 1 {
		t.Fatalf("sends after the second sweep = %d, want still 1", len(h.chat.sends))
	}
}

func TestRejectionDispatchesExecutorAgain(t *testing.T) {
	h := newHarness(t)
	h.awaiting("task-1", time.Unix(1_700_000_100, 0))
	if _, err := h.service.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	// The auditor rejected: the lease left the queue and the log has the reason.
	h.queue.leases = nil
	h.log.rows["task-1"] = []contracts.TaskReview{{TaskID: "task-1", Reviewer: "auditor", Decision: contracts.TaskReviewReject, Findings: "缺来源"}}
	h.rework.rows["task-1"] = contracts.TaskRework{TaskID: "task-1", Rounds: 1, Max: 3}

	report, err := h.service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep after reject: %v", err)
	}
	if report.Redispatches != 1 {
		t.Fatalf("report = %+v, want one redispatch", report)
	}
	if len(h.chat.sends) != 2 {
		t.Fatalf("sends = %d, want 2 (auditor + executor)", len(h.chat.sends))
	}
	redo := h.chat.sends[1]
	if h.chat.presets[redo.session] != contracts.PresetExecutorID {
		t.Fatalf("preset = %q, want executor", h.chat.presets[redo.session])
	}
	if !strings.Contains(redo.prompt, "缺来源") {
		t.Fatalf("redo prompt = %q, want the review note", redo.prompt)
	}

	// The executor submits again: a new lease attempt gets a fresh auditor.
	h.queue.leases = []contracts.TaskLease{{TaskID: "task-1", Owner: "executor", State: contracts.LeaseStateReview, Until: time.Unix(1_700_000_400, 0)}}
	report, err = h.service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep after resubmit: %v", err)
	}
	if report.AuditorsStarted != 1 {
		t.Fatalf("report = %+v, want a second auditor for the new attempt", report)
	}
}

func TestCeilingEscalatesToTheHumanOnce(t *testing.T) {
	h := newHarness(t)
	h.awaiting("task-1", time.Unix(1_700_000_100, 0))
	if _, err := h.service.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	h.queue.leases = nil
	h.log.rows["task-1"] = []contracts.TaskReview{{TaskID: "task-1", Reviewer: "auditor", Decision: contracts.TaskReviewReject, Findings: "还是不行"}}
	h.rework.rows["task-1"] = contracts.TaskRework{TaskID: "task-1", Rounds: 3, Max: 3, Exhausted: true}

	report, err := h.service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep at the ceiling: %v", err)
	}
	if report.Escalated != 1 || report.Redispatches != 0 {
		t.Fatalf("report = %+v, want one escalation and no redispatch", report)
	}
	if len(h.chat.sends) != 1 {
		t.Fatalf("sends = %d, want no executor session at the ceiling", len(h.chat.sends))
	}
	if len(h.notifier.deliveries) != 1 {
		t.Fatalf("deliveries = %v, want one human notification", h.notifier.deliveries)
	}

	// A loop that escalates every 5s is a pager, not a product.
	if _, err := h.service.Sweep(context.Background()); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if len(h.notifier.deliveries) != 1 {
		t.Fatalf("deliveries after the second sweep = %v, want still one", h.notifier.deliveries)
	}
}

func TestAcceptanceForgetsTheTask(t *testing.T) {
	h := newHarness(t)
	h.awaiting("task-1", time.Unix(1_700_000_100, 0))
	if _, err := h.service.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	h.queue.leases = nil
	h.log.rows["task-1"] = []contracts.TaskReview{{TaskID: "task-1", Reviewer: "auditor", Decision: contracts.TaskReviewAccept}}
	if _, err := h.service.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep after accept: %v", err)
	}
	report, err := h.service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep after forgetting: %v", err)
	}
	if report.Redispatches != 0 || report.Escalated != 0 || len(h.chat.sends) != 1 {
		t.Fatalf("after acceptance: report=%+v sends=%d, want a quiet loop", report, len(h.chat.sends))
	}
}

func TestOneBrokenTaskDoesNotStopTheSweep(t *testing.T) {
	h := newHarness(t)
	h.board.tasks = []contracts.BoardTask{
		{ID: "task-1", Subject: "坏的"},
		{ID: "task-2", Subject: "好的"},
	}
	h.chat.failOn = "坏的"
	h.awaiting("task-1", time.Unix(1_700_000_100, 0))
	h.queue.leases = append(h.queue.leases, contracts.TaskLease{TaskID: "task-2", Owner: "executor", State: contracts.LeaseStateReview, Until: time.Unix(1_700_000_100, 0)})

	report, err := h.service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(report.Errors) != 1 || report.AuditorsStarted != 1 {
		t.Fatalf("report = %+v, want one error and one started auditor", report)
	}
	if h.chat.bindings["sess-审计：好的"] != "task-2" {
		t.Fatalf("bindings = %v, want the healthy task started", h.chat.bindings)
	}
}
