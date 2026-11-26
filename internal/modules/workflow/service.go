// Package workflow is the stage-based pipeline backend (M2).
//
// A workflow is a validated document: stages, each with a role (agent preset), a
// prompt and an acceptance line, and dependencies between them. A run turns the
// document into board tasks: one task per stage, dispatched to a session running
// as the stage's role, reviewed by the M1 audit loop. The run advances by
// reading the board — a stage is done when its task is accepted — so the
// workflow layer owns no second idea of completion that could disagree with the
// lease layer.
//
// What this package deliberately does not do:
//   - it does not decide who may author a workflow (the gateway's permission
//     table does);
//   - it does not review stages (the auditor role and the lease layer do);
//   - it does not retry a failed stage silently — a rejected task comes back
//     through the M1 rework loop, and a stage whose task never completes simply
//     stays pending, visibly.
package workflow

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// DefaultInterval is how often Advance runs when the assembly does not say.
// Ten seconds is deliberate: a stage boundary is a model turn (tens of seconds),
// so a tighter loop only adds store reads.
const DefaultInterval = 10 * time.Second

// Config is the module's typed assembly config.
type Config struct {
	// Enabled starts the advance loop (default true).
	Enabled *bool `json:"enabled,omitempty"`
	// Interval overrides DefaultInterval.
	Interval time.Duration `json:"interval,omitempty"`
	// Now overrides the clock (tests); nil means time.Now.
	Now func() time.Time `json:"-"`
}

func (c Config) enabled() bool { return c.Enabled == nil || *c.Enabled }

func (c Config) interval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return DefaultInterval
}

// Service implements contracts.WorkflowService.
// ToolBinder is the binding-write half of the lease layer (SetTools). A stage
// that names tools is refused when this is missing: the binding is a permission
// boundary, and "could not apply it" must not degrade into "no boundary".
type ToolBinder interface {
	SetTools(ctx context.Context, taskID string, tools []string) (contracts.TaskTools, error)
}

type Service struct {
	mu         sync.Mutex
	cfg        Config
	board      contracts.TaskBoard
	dispatcher contracts.WorkflowDispatcher
	binder     ToolBinder
	workflows  map[string]contracts.Workflow
	runs       map[string]*contracts.WorkflowRun
	// runOrder keeps Start's ordering stable across a restart (the document is
	// written in id order, and ids are monotonic per process).
	sequence int
	path     string
}

// New builds an in-memory service (unit tests and a data-dir-less product).
// WithToolBinder attaches the task tool binder and returns the service.
func (s *Service) WithToolBinder(binder ToolBinder) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.binder = binder
	return s
}

func New(board contracts.TaskBoard, dispatcher contracts.WorkflowDispatcher, cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{
		cfg:        cfg,
		board:      board,
		dispatcher: dispatcher,
		workflows:  make(map[string]contracts.Workflow),
		runs:       make(map[string]*contracts.WorkflowRun),
	}
}

var _ contracts.WorkflowService = (*Service)(nil)

// List returns the definitions sorted by id (deterministic for the UI and diff).
func (s *Service) List(ctx context.Context) ([]contracts.Workflow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.workflows))
	for id := range s.workflows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]contracts.Workflow, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.workflows[id])
	}
	return out, nil
}

// Get returns one definition; false means unknown.
func (s *Service) Get(ctx context.Context, id string) (contracts.Workflow, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Workflow{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, ok := s.workflows[trimID(id)]
	return wf, ok, nil
}

// Put validates and stores one definition (create or replace).
func (s *Service) Put(ctx context.Context, wf contracts.Workflow) (contracts.Workflow, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Workflow{}, err
	}
	normalized := contracts.NormalizeWorkflow(wf)
	if err := contracts.ValidateWorkflow(normalized); err != nil {
		return contracts.Workflow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Replacing a definition must not silently invalidate live runs: their stage
	// rows keep the ids they were started with, and Advance reports a stage that
	// no longer exists in the definition rather than dropping the run.
	s.workflows[normalized.ID] = normalized
	if err := s.persistLocked(); err != nil {
		return contracts.Workflow{}, err
	}
	return normalized, nil
}

// Delete removes one definition.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = trimID(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workflows[id]; !ok {
		return contracts.ErrWorkflowNotFound
	}
	delete(s.workflows, id)
	return s.persistLocked()
}

// Runs returns every run sorted by id.
func (s *Service) Runs(ctx context.Context) ([]contracts.WorkflowRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.runs))
	for id := range s.runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]contracts.WorkflowRun, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.runs[id])
	}
	return out, nil
}

// Start opens a run and dispatches its root stages.
func (s *Service) Start(ctx context.Context, workflowID string) (contracts.WorkflowRun, error) {
	workflowID = trimID(workflowID)
	s.mu.Lock()
	wf, ok := s.workflows[workflowID]
	if !ok {
		s.mu.Unlock()
		return contracts.WorkflowRun{}, contracts.ErrWorkflowNotFound
	}
	now := s.cfg.Now()
	s.sequence++
	run := &contracts.WorkflowRun{
		ID:         fmt.Sprintf("run-%d", s.sequence),
		WorkflowID: wf.ID,
		Status:     contracts.WorkflowRunRunning,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	for _, stage := range wf.Stages {
		run.Stages = append(run.Stages, contracts.WorkflowStageState{StageID: stage.ID, Status: contracts.WorkflowStagePending})
	}
	s.runs[run.ID] = run
	persistErr := s.persistLocked()
	s.mu.Unlock()
	if persistErr != nil {
		return contracts.WorkflowRun{}, persistErr
	}
	// Dispatch the roots with the same pass Advance uses, so Start cannot have a
	// different notion of "ready" than a later tick.
	if _, err := s.Advance(ctx); err != nil {
		return *run, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.runs[run.ID], nil
}

// Advance is one pass: settle dispatched stages against the board, dispatch the
// stages whose dependencies are met, finish completed runs.
func (s *Service) Advance(ctx context.Context) (contracts.WorkflowReport, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return contracts.WorkflowReport{}, err
	}
	report := contracts.WorkflowReport{}
	statuses, err := s.boardStatuses(ctx)
	if err != nil {
		return report, fmt.Errorf("workflow: read task board: %w", err)
	}

	s.mu.Lock()
	runIDs := make([]string, 0, len(s.runs))
	for id, run := range s.runs {
		if run.Status == contracts.WorkflowRunRunning {
			runIDs = append(runIDs, id)
		}
	}
	sort.Strings(runIDs)
	report.Runs = len(runIDs)

	var plans []advancePlan
	for _, runID := range runIDs {
		run := s.runs[runID]
		wf, ok := s.workflows[run.WorkflowID]
		if !ok {
			report.Errors = append(report.Errors, fmt.Sprintf("run %s: workflow %q no longer exists", run.ID, run.WorkflowID))
			continue
		}
		if s.settleStages(run, statuses) {
			run.Status = contracts.WorkflowRunDone
			run.UpdatedAt = s.cfg.Now()
			report.Completed++
			continue
		}
		// A stage row whose id no longer exists in the definition can never be
		// dispatched or settled: the run is stuck, and saying so once per pass is
		// better than a run that looks busy forever.
		if missing := vanishedStage(wf, run); missing != "" {
			report.Errors = append(report.Errors, fmt.Sprintf("run %s: stage %q no longer exists in workflow %q", run.ID, missing, wf.ID))
			continue
		}
		done := doneStages(run)
		for _, stage := range contracts.ReadyStages(wf, done) {
			// ReadyStages answers "dependencies met", which is also true of a
			// stage that is already running. Only a still-pending stage is a
			// dispatch: re-dispatching a running one would start the same work
			// twice.
			if stageStatus(run, stage.ID) != contracts.WorkflowStagePending {
				continue
			}
			plans = append(plans, advancePlan{runID: run.ID, workflow: wf, stage: stage})
		}
	}
	s.mu.Unlock()

	// Dispatch outside the lock: a task create and a session start are slow.
	type dispatched struct {
		plan      advancePlan
		taskID    string
		sessionID string
		err       error
	}
	results := make([]dispatched, 0, len(plans))
	for _, plan := range plans {
		taskID, sessionID, err := s.dispatchStage(ctx, plan, len(results))
		results = append(results, dispatched{plan: plan, taskID: taskID, sessionID: sessionID, err: err})
	}

	s.mu.Lock()
	for _, result := range results {
		run, ok := s.runs[result.plan.runID]
		if !ok {
			continue
		}
		if result.err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("run %s stage %s: %v", run.ID, result.plan.stage.ID, result.err))
			continue
		}
		for i := range run.Stages {
			if run.Stages[i].StageID != result.plan.stage.ID {
				continue
			}
			run.Stages[i].Status = contracts.WorkflowStageDispatched
			run.Stages[i].TaskID = result.taskID
			run.Stages[i].SessionID = result.sessionID
			break
		}
		run.UpdatedAt = s.cfg.Now()
		report.Dispatched++
	}
	if len(results) > 0 {
		if err := s.persistLocked(); err != nil {
			report.Errors = append(report.Errors, err.Error())
		}
	}
	s.mu.Unlock()
	return report, nil
}

// settleStages marks every dispatched stage whose task is completed as done.
// It reports whether the whole run is complete. The caller holds s.mu.
func (s *Service) settleStages(run *contracts.WorkflowRun, statuses map[string]string) bool {
	allDone := true
	for i := range run.Stages {
		stage := &run.Stages[i]
		switch stage.Status {
		case contracts.WorkflowStageDone:
			continue
		case contracts.WorkflowStageDispatched:
			if statuses[stage.TaskID] == contracts.TaskStatusCompleted {
				stage.Status = contracts.WorkflowStageDone
				run.UpdatedAt = s.cfg.Now()
				continue
			}
		}
		allDone = false
	}
	return allDone
}

// dispatchStage creates the stage's task and starts its session as the stage's
// role, in the order the rest of the product uses (task first, then session,
// then binding and preset, then the prompt).
func (s *Service) dispatchStage(ctx context.Context, plan advancePlan, index int) (string, string, error) {
	if s.board == nil || s.dispatcher == nil {
		return "", "", fmt.Errorf("no task board or chat service is assembled")
	}
	subject := fmt.Sprintf("%s/%s", plan.workflow.ID, plan.stage.ID)
	if index > 0 {
		// Two runs of one workflow would otherwise create the same subject; the
		// id suffix keeps the board readable and unique.
		subject = fmt.Sprintf("%s#%d", subject, index+1)
	}
	task, err := s.board.Create(ctx, subject, stageDescription(plan.stage))
	if err != nil {
		return "", "", fmt.Errorf("create stage task: %w", err)
	}
	sessionID, err := s.dispatcher.NewSession("流程：" + plan.workflow.Name + " · " + plan.stage.Name)
	if err != nil {
		return task.ID, "", fmt.Errorf("start stage session: %w", err)
	}
	if err := s.dispatcher.BindSessionTask(ctx, sessionID, task.ID); err != nil {
		return task.ID, sessionID, fmt.Errorf("bind stage task: %w", err)
	}
	// The stage's tool binding (M3b): applied before the prompt so the executor's
	// first call already sees the ceiling, and refused loudly when the lease layer
	// cannot record it.
	if len(plan.stage.Tools) > 0 {
		s.mu.Lock()
		binder := s.binder
		s.mu.Unlock()
		if binder == nil {
			return task.ID, sessionID, fmt.Errorf("stage %s binds tools but this product has no task lease layer", plan.stage.ID)
		}
		if _, err := binder.SetTools(ctx, task.ID, plan.stage.Tools); err != nil {
			return task.ID, sessionID, fmt.Errorf("bind stage tools: %w", err)
		}
	}
	if err := s.dispatcher.SetSessionPreset(sessionID, plan.stage.Role); err != nil {
		return task.ID, sessionID, fmt.Errorf("select stage role: %w", err)
	}
	if err := s.dispatcher.Send(sessionID, stagePrompt(plan.workflow, plan.stage, len(plan.workflow.Stages))); err != nil {
		return task.ID, sessionID, fmt.Errorf("start stage run: %w", err)
	}
	return task.ID, sessionID, nil
}

// boardStatuses reads the board once per pass.
func (s *Service) boardStatuses(ctx context.Context) (map[string]string, error) {
	if s.board == nil {
		return map[string]string{}, nil
	}
	tasks, err := s.board.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(tasks))
	for _, task := range tasks {
		out[task.ID] = task.Status
	}
	return out, nil
}

// vanishedStage reports the first unfinished run stage whose definition is gone
// ("" when every unfinished stage still exists).
func vanishedStage(wf contracts.Workflow, run *contracts.WorkflowRun) string {
	known := make(map[string]bool, len(wf.Stages))
	for _, stage := range wf.Stages {
		known[stage.ID] = true
	}
	for _, stage := range run.Stages {
		if stage.Status == contracts.WorkflowStageDone {
			continue
		}
		if !known[stage.StageID] {
			return stage.StageID
		}
	}
	return ""
}

// stageStatus returns one stage's status inside a run ("" when unknown).
func stageStatus(run *contracts.WorkflowRun, stageID string) string {
	for _, stage := range run.Stages {
		if stage.StageID == stageID {
			return stage.Status
		}
	}
	return ""
}

// doneStages is the input ReadyStages wants.
func doneStages(run *contracts.WorkflowRun) map[string]bool {
	done := make(map[string]bool, len(run.Stages))
	for _, stage := range run.Stages {
		if stage.Status == contracts.WorkflowStageDone {
			done[stage.StageID] = true
		}
	}
	return done
}

// stageDescription is what the board task carries: the goal and the acceptance
// line, because the auditor reads the task, not the workflow.
func stageDescription(stage contracts.WorkflowStage) string {
	out := "阶段 " + stage.Name
	if stage.Prompt != "" {
		out += "\n目标：" + stage.Prompt
	}
	if stage.Acceptance != "" {
		out += "\n验收标准：" + stage.Acceptance
	}
	return out
}

// stagePrompt is what the stage's session receives.
func stagePrompt(wf contracts.Workflow, stage contracts.WorkflowStage, total int) string {
	out := fmt.Sprintf("工作流「%s」的阶段 %s（共 %d 个阶段）已派给你。\n", wf.Name, stage.Name, total)
	if stage.Prompt != "" {
		out += "任务目标：" + stage.Prompt + "\n"
	}
	if stage.Acceptance != "" {
		out += "验收标准：" + stage.Acceptance + "\n"
	}
	out += "完成后用 task_submit 提交证据；验收由 auditor 角色完成，不要自审。"
	return out
}

func trimID(id string) string {
	out := make([]rune, 0, len(id))
	for _, r := range id {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
