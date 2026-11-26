// Package audit is the automatic audit loop (M1): it watches the lease layer's
// review queue, opens an auditor session for every submitted task, and turns the
// verdict into the next step — another executor round, or the human.
//
// Why a poller and not a subscription: the lease layer is durable and the
// process may restart between a submit and its verdict. "Who is in the review
// state right now" is the only question that survives a crash, and the audit
// loop's job is exactly to answer it — repeatedly, without ever starting a
// second auditor for the same lease attempt.
//
// The loop owns no policy of its own:
//   - who may review is the lease layer's rule (auditor role, never the executor);
//   - how many rounds are left is the rework counter's rule;
//   - where the notification goes is the notification router's rule.
//
// It only decides the sequence: submitted → audit → rejected → rework → audit…
// until the counter says the task belongs to a person.
package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Stage values of one task's tracking row.
const (
	stageAuditing  = "auditing"
	stageReworking = "reworking"
	stageEscalated = "escalated"
)

// Chat is the slice of contracts.ChatService the loop needs: start a session and
// send it one prompt. Keeping it narrow is what lets the tests run a sweep with
// no agent, no model and no store.
type Chat interface {
	NewSession(title string) (string, error)
	Send(sessionID, prompt string) error
}

// Queue is contracts.TaskReviewQueue.
type Queue interface {
	AwaitingReview(ctx context.Context) ([]contracts.TaskLease, error)
}

// ReviewLog is contracts.TaskReviewLog.
type ReviewLog interface {
	Reviews(ctx context.Context, taskID string) ([]contracts.TaskReview, error)
}

// Rework is the rework counter half of the lease service.
type Rework interface {
	Rework(ctx context.Context, taskID string) (contracts.TaskRework, error)
}

// Board is the read half of the task board: the loop needs the subject and the
// acceptance line to write a prompt an auditor can act on.
type Board interface {
	List(ctx context.Context) ([]contracts.BoardTask, error)
}

// Notifier is the escalation path (channeldispatch's Deliver).
type Notifier interface {
	Deliver(ctx context.Context, category, text string) (contracts.DispatchReceipt, error)
}

// Optional capabilities the chat service may implement, exactly the shape the
// triage dispatcher uses (a backend without them still runs, just with less
// binding).
type taskBinder interface {
	BindSessionTask(ctx context.Context, sessionID, taskID string) error
}

type presetSetter interface {
	SetSessionPreset(sessionID, presetID string) error
}

// Deps is what the loop needs to run. Every field but the core four is
// optional: a missing board means prompts are thinner, a missing notifier means
// escalation is only reported in the sweep result.
type Deps struct {
	Chat     Chat
	Queue    Queue
	Reviews  ReviewLog
	Rework   Rework
	Board    Board
	Notifier Notifier
	Logger   contracts.Logger
	Now      func() time.Time
}

// Service implements contracts.AuditService.
type Service struct {
	deps Deps
	// tracks remembers what the loop already did per task: which lease attempt
	// has its auditor, which review row was already acted on, and whether the
	// task already escalated (so a 5s loop cannot become a pager).
	tracks map[string]*track
}

type track struct {
	until   time.Time // the lease attempt the current auditor belongs to
	stage   string
	actedAt time.Time // the review row already handled
}

// New builds the loop. A nil Now means time.Now; a nil logger is silent.
func New(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps, tracks: make(map[string]*track)}
}

// Sweep runs one bounded pass over the review queue.
func (s *Service) Sweep(ctx context.Context) (contracts.AuditReport, error) {
	report := contracts.AuditReport{}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	awaiting, err := s.deps.Queue.AwaitingReview(ctx)
	if err != nil {
		return report, fmt.Errorf("audit: read review queue: %w", err)
	}
	report.Queued = len(awaiting)
	tasks := s.boardIndex(ctx)

	// 1. Everything waiting for a verdict gets exactly one auditor per attempt.
	inQueue := make(map[string]bool, len(awaiting))
	for _, lease := range awaiting {
		inQueue[lease.TaskID] = true
		row := s.tracks[lease.TaskID]
		if row != nil && row.stage == stageAuditing && row.until.Equal(lease.Until) {
			continue
		}
		if err := s.startAuditor(ctx, lease, tasks[lease.TaskID]); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: start auditor: %v", lease.TaskID, err))
			continue
		}
		next := &track{until: lease.Until, stage: stageAuditing}
		if row != nil {
			next.actedAt = row.actedAt
		}
		s.tracks[lease.TaskID] = next
		report.AuditorsStarted++
	}

	// 2. Tasks that left the queue were decided: accept forgets them, reject
	//    dispatches again or escalates. Deterministic order keeps reports stable.
	left := make([]string, 0, len(s.tracks))
	for taskID := range s.tracks {
		if !inQueue[taskID] {
			left = append(left, taskID)
		}
	}
	sort.Strings(left)
	for _, taskID := range left {
		if err := s.settle(ctx, taskID, &report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", taskID, err))
		}
	}
	return report, nil
}

// settle handles one task that is no longer waiting for a verdict.
func (s *Service) settle(ctx context.Context, taskID string, report *contracts.AuditReport) error {
	row := s.tracks[taskID]
	reviews, err := s.deps.Reviews.Reviews(ctx, taskID)
	if err != nil {
		return fmt.Errorf("read review log: %w", err)
	}
	last := lastReview(reviews)
	if last == nil {
		// Vanished without a decision (a release, an expiry): nothing to drive.
		delete(s.tracks, taskID)
		return nil
	}
	if last.Decision == contracts.TaskReviewAccept {
		delete(s.tracks, taskID)
		return nil
	}
	if !row.actedAt.IsZero() && !last.At.After(row.actedAt) {
		// This rejection was already handled; the queue simply has not changed.
		return nil
	}
	row.actedAt = last.At
	rework, err := s.deps.Rework.Rework(ctx, taskID)
	if err != nil {
		return fmt.Errorf("read rework: %w", err)
	}
	if rework.Exhausted {
		if row.stage != stageEscalated {
			s.escalate(ctx, taskID, last.Findings, rework)
			row.stage = stageEscalated
			row.until = time.Time{}
			report.Escalated++
		}
		return nil
	}
	if err := s.startExecutorRedo(ctx, taskID, last.Findings, rework); err != nil {
		return fmt.Errorf("dispatch rework: %w", err)
	}
	row.stage = stageReworking
	report.Redispatches++
	return nil
}

// startAuditor opens the auditor session for one lease attempt.
func (s *Service) startAuditor(ctx context.Context, lease contracts.TaskLease, task contracts.BoardTask) error {
	sessionID, err := s.deps.Chat.NewSession("审计：" + taskTitle(task))
	if err != nil {
		return err
	}
	if binder, ok := s.deps.Chat.(taskBinder); ok {
		if err := binder.BindSessionTask(ctx, sessionID, lease.TaskID); err != nil {
			return err
		}
	}
	if setter, ok := s.deps.Chat.(presetSetter); ok {
		if err := setter.SetSessionPreset(sessionID, contracts.PresetAuditorID); err != nil {
			return err
		}
	} else if s.deps.Logger != nil {
		s.deps.Logger.Printf("[audit] task %s: chat service has no preset setter; the auditor session runs with the default role", lease.TaskID)
	}
	return s.deps.Chat.Send(sessionID, auditorPrompt(task, lease))
}

// startExecutorRedo dispatches the next attempt after a rejection.
func (s *Service) startExecutorRedo(ctx context.Context, taskID, note string, rework contracts.TaskRework) error {
	sessionID, err := s.deps.Chat.NewSession("返工：" + taskID)
	if err != nil {
		return err
	}
	if binder, ok := s.deps.Chat.(taskBinder); ok {
		if err := binder.BindSessionTask(ctx, sessionID, taskID); err != nil {
			return err
		}
	}
	if setter, ok := s.deps.Chat.(presetSetter); ok {
		if err := setter.SetSessionPreset(sessionID, contracts.PresetExecutorID); err != nil {
			return err
		}
	}
	return s.deps.Chat.Send(sessionID, reworkPrompt(taskID, note, rework))
}

// escalate tells the human, once per task, that the loop has stopped.
func (s *Service) escalate(ctx context.Context, taskID, note string, rework contracts.TaskRework) {
	text := fmt.Sprintf("任务 %s 已用尽 %d 轮返工，已停止自动流转，需要你决定（改写任务或接手）。", taskID, rework.Rounds)
	if strings.TrimSpace(note) != "" {
		text += "\n最后一条审计意见：" + note
	}
	if s.deps.Notifier == nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("[audit] task %s escalated (no notifier): %s", taskID, text)
		}
		return
	}
	if _, err := s.deps.Notifier.Deliver(ctx, contracts.NotifyCategoryNeedsDecision, text); err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("[audit] task %s: escalate: %v", taskID, err)
		}
	}
}

// boardIndex reads the board once per sweep, best effort: a missing or broken
// board thins the prompts, it does not stop the loop.
func (s *Service) boardIndex(ctx context.Context) map[string]contracts.BoardTask {
	out := map[string]contracts.BoardTask{}
	if s.deps.Board == nil {
		return out
	}
	tasks, err := s.deps.Board.List(ctx)
	if err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("[audit] read task board: %v", err)
		}
		return out
	}
	for _, task := range tasks {
		out[task.ID] = task
	}
	return out
}

// lastReview is the newest recorded decision, or nil.
func lastReview(rows []contracts.TaskReview) *contracts.TaskReview {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

// taskTitle is what a session is named after: the subject when there is one.
func taskTitle(task contracts.BoardTask) string {
	if subject := strings.TrimSpace(task.Subject); subject != "" {
		return subject
	}
	return task.ID
}

// auditorPrompt asks for a read-only review that ends in a machine-readable
// verdict (the task_review tool), with the evidence the executor submitted.
func auditorPrompt(task contracts.BoardTask, lease contracts.TaskLease) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是审计者（auditor）。任务 %s 已提交，等待你的验收。\n", task.ID)
	if subject := strings.TrimSpace(task.Subject); subject != "" {
		fmt.Fprintf(&b, "任务目标：%s\n", subject)
	}
	if description := strings.TrimSpace(task.Description); description != "" {
		fmt.Fprintf(&b, "任务说明与验收标准：%s\n", description)
	}
	if len(lease.Evidence) > 0 {
		b.WriteString("提交的证据：\n")
		for _, evidence := range lease.Evidence {
			fmt.Fprintf(&b, "- %s: %s", evidence.Kind, evidence.Ref)
			if note := strings.TrimSpace(evidence.Note); note != "" {
				fmt.Fprintf(&b, "（%s）", note)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("要求：只读复核，逐条核对验收标准；用 task_review 工具给出 accept 或 reject（reject 必须写明问题位置与复现方式），不要自己修改代码。\n")
	return b.String()
}

// reworkPrompt hands the rejection back to an executor, with the counter so the
// next attempt knows how many rounds are left.
func reworkPrompt(taskID, note string, rework contracts.TaskRework) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你上一轮提交的任务 %s 被打回（第 %d/%d 轮返工）。\n", taskID, rework.Rounds, rework.Max)
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		fmt.Fprintf(&b, "审计意见：%s\n", trimmed)
	}
	b.WriteString("请按审计意见返工，完成后用 task_submit 提交证据（命令输出、测试名、改动文件）。\n")
	return b.String()
}

var _ contracts.AuditService = (*Service)(nil)
