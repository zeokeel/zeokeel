package audit_test

// The loop end to end, without a model: the REAL lease service (claim, evidence,
// review log, rework counter) plus the REAL audit loop, with a fake chat that
// plays the auditor and the executor.
//
// What this pins that the unit tests cannot: the two halves agree on identity.
// The audit loop starts a session bound to a task; the (simulated) auditor then
// calls Accept/Reject with the auditor role against the same lease the loop is
// reading. If those two ever disagree — different task id, a stale lease, a
// counter that resets — the loop would look healthy in isolation and dead in
// production.
//
// The scenario is the product's whole promise: submit → audit → reject →
// rework → submit → audit → accept.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/audit"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

// integrationBoard is a minimal contracts.TaskBoard over one map.
type integrationBoard struct {
	rows  map[string]contracts.BoardTask
	order []string
}

func newIntegrationBoard(tasks ...contracts.BoardTask) *integrationBoard {
	board := &integrationBoard{rows: map[string]contracts.BoardTask{}}
	for _, task := range tasks {
		if task.Status == "" {
			task.Status = contracts.TaskStatusPending
		}
		board.rows[task.ID] = task
		board.order = append(board.order, task.ID)
	}
	return board
}

func (b *integrationBoard) List(context.Context) ([]contracts.BoardTask, error) {
	out := make([]contracts.BoardTask, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, b.rows[id])
	}
	return out, nil
}

func (b *integrationBoard) Create(_ context.Context, subject, description string) (contracts.BoardTask, error) {
	id := "task-" + subject
	task := contracts.BoardTask{ID: id, Subject: subject, Description: description, Status: contracts.TaskStatusPending}
	b.rows[id] = task
	b.order = append(b.order, id)
	return task, nil
}

func (b *integrationBoard) SetStatus(_ context.Context, id, status string) (contracts.BoardTask, error) {
	task, ok := b.rows[id]
	if !ok {
		return contracts.BoardTask{}, errors.New("unknown task")
	}
	task.Status = status
	b.rows[id] = task
	return task, nil
}

func (b *integrationBoard) Delete(_ context.Context, id string) error {
	delete(b.rows, id)
	return nil
}

func TestAuditLoopEndToEnd(t *testing.T) {
	ctx := context.Background()
	board := newIntegrationBoard(contracts.BoardTask{
		ID: "task-1", Subject: "修复登录", Description: "验收：单测通过",
	})
	clock := time.Unix(1_700_000_000, 0)
	lease := tasklease.NewService(board, tasklease.Config{
		Now:       func() time.Time { return clock },
		TTL:       10 * time.Minute,
		MaxRework: 2,
	})
	chat := &fakeChat{}
	notifier := &fakeNotifier{}
	loop := audit.New(audit.Deps{
		Chat:     chat,
		Queue:    lease,
		Reviews:  lease,
		Rework:   lease,
		Board:    board,
		Notifier: notifier,
		Now:      func() time.Time { return clock },
	})

	// 1. The executor claims, works and submits.
	if _, err := lease.Claim(ctx, "task-1", contracts.PresetExecutorID); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := lease.Submit(ctx, "task-1", contracts.PresetExecutorID, []contracts.Evidence{{Kind: "output", Ref: "go test ./..."}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// 2. The loop starts one auditor for the submitted task.
	report, err := loop.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.AuditorsStarted != 1 || len(chat.sends) != 1 {
		t.Fatalf("report=%+v sends=%d, want one auditor", report, len(chat.sends))
	}
	auditorSession := chat.sends[0].session
	if chat.presets[auditorSession] != contracts.PresetAuditorID || chat.bindings[auditorSession] != "task-1" {
		t.Fatalf("auditor session = %q preset=%q binding=%q", auditorSession, chat.presets[auditorSession], chat.bindings[auditorSession])
	}

	// 3. The auditor rejects (this is what task_review does).
	if _, err := lease.Reject(ctx, "task-1", contracts.PresetAuditorID, "缺来源"); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	// 4. The loop dispatches the executor again, with the note.
	report, err = loop.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep after reject: %v", err)
	}
	if report.Redispatches != 1 {
		t.Fatalf("report = %+v, want one redispatch", report)
	}
	redo := chat.sends[len(chat.sends)-1]
	if chat.presets[redo.session] != contracts.PresetExecutorID || chat.bindings[redo.session] != "task-1" {
		t.Fatalf("rework session = %q preset=%q binding=%q", redo.session, chat.presets[redo.session], chat.bindings[redo.session])
	}
	if rework, err := lease.Rework(ctx, "task-1"); err != nil || rework.Rounds != 1 || rework.Max != 2 {
		t.Fatalf("rework = (%+v, %v), want 1/2", rework, err)
	}

	// 5. The executor submits again; the loop starts a second auditor.
	if _, err := lease.Claim(ctx, "task-1", contracts.PresetExecutorID); err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if _, err := lease.Submit(ctx, "task-1", contracts.PresetExecutorID, []contracts.Evidence{{Kind: "link", Ref: "https://ci/run/1"}}); err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	report, err = loop.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep after resubmit: %v", err)
	}
	if report.AuditorsStarted != 1 {
		t.Fatalf("report = %+v, want a second auditor", report)
	}

	// 6. The auditor accepts. The loop forgets the task and clears the counter.
	if _, err := lease.Accept(ctx, "task-1", contracts.PresetAuditorID, "这次可以"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := loop.Sweep(ctx); err != nil {
		t.Fatalf("Sweep after accept: %v", err)
	}
	if rework, err := lease.Rework(ctx, "task-1"); err != nil || rework.Rounds != 0 || rework.Exhausted {
		t.Fatalf("rework after accept = (%+v, %v), want 0", rework, err)
	}
	if len(notifier.deliveries) != 0 {
		t.Fatalf("deliveries = %v, want no escalation in a converging loop", notifier.deliveries)
	}

	// 7. Nothing is left running: a quiet sweep starts nothing.
	before := len(chat.sends)
	report, err = loop.Sweep(ctx)
	if err != nil {
		t.Fatalf("final Sweep: %v", err)
	}
	if len(chat.sends) != before || report.AuditorsStarted != 0 || report.Redispatches != 0 || report.Escalated != 0 {
		t.Fatalf("final sweep = %+v sends=%d, want a quiet loop", report, len(chat.sends))
	}
}

// The same wiring at the ceiling: the third rejection stops the loop and tells
// the human once, and the lease layer refuses further dispatch until someone
// resets it.
func TestAuditLoopEscalatesAtTheCeiling(t *testing.T) {
	ctx := context.Background()
	board := newIntegrationBoard(contracts.BoardTask{ID: "task-1", Subject: "修复登录"})
	clock := time.Unix(1_700_000_000, 0)
	lease := tasklease.NewService(board, tasklease.Config{
		Now:       func() time.Time { return clock },
		TTL:       10 * time.Minute,
		MaxRework: 1,
	})
	chat := &fakeChat{}
	notifier := &fakeNotifier{}
	loop := audit.New(audit.Deps{Chat: chat, Queue: lease, Reviews: lease, Rework: lease, Board: board, Notifier: notifier})

	if _, err := lease.Claim(ctx, "task-1", contracts.PresetExecutorID); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := lease.Submit(ctx, "task-1", contracts.PresetExecutorID, []contracts.Evidence{{Kind: "file", Ref: "a.go"}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := loop.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := lease.Reject(ctx, "task-1", contracts.PresetAuditorID, "还是不行"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	report, err := loop.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep at the ceiling: %v", err)
	}
	if report.Escalated != 1 || report.Redispatches != 0 {
		t.Fatalf("report = %+v, want escalation and no redispatch", report)
	}
	if len(notifier.deliveries) != 1 {
		t.Fatalf("deliveries = %v, want one human notification", notifier.deliveries)
	}
	// The lease layer is the backstop: no dispatch, no submission, until reset.
	if _, err := lease.Claim(ctx, "task-1", contracts.PresetExecutorID); !errors.Is(err, contracts.ErrLeaseReworkExhausted) {
		t.Fatalf("Claim after escalation err = %v, want ErrLeaseReworkExhausted", err)
	}
	if err := lease.ResetRework(ctx, "task-1"); err != nil {
		t.Fatalf("ResetRework: %v", err)
	}
	if _, err := lease.Claim(ctx, "task-1", contracts.PresetExecutorID); err != nil {
		t.Fatalf("Claim after reset: %v", err)
	}
}
