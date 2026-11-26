package agent

// Cold resume of durable child sessions (DSH parity).
//
// A teammate or delegated subagent becomes a durable child-session record
// (child_sessions.go). After a restart its live Rig object is gone, so the
// record is presented as "historical" and used to be read-only. This file makes
// a historical child continuable again: a Lead/human message (SendChildAgent,
// TeamService.Send/SendAsync or the model's send_message) starts a normal agent
// run whose SessionID is the CHILD session, so
//
//   - the child's prior conversation is loaded from the durable store history
//     (first resume seeds it from the record's original prompt/answer);
//   - every message/tool call of the resumed run is appended to that same child
//     session, so the NEXT restart can resume again with the full history;
//   - the child-session record is updated Running → Completed/Failed/Interrupted
//     and stays visible through the child-agent/team read models.
//
// The resume never starts by itself (only an explicit send triggers it), never
// copies the parent history (the child's own session is the history), and keeps
// the recorded model, the parent session's permission policy and workspace.
//
// It deliberately does NOT go through the Rig team manager: the manager only
// owns live subagents and rebuilds them from a fresh in-memory history. Running
// the product's own agent against the durable child history is the "more direct
// way" the task allows and is the only path that persists the resumed turns.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel-rig/kit"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/hostbundle"
)

// ErrColdResumeUnavailable reports a historical child that cannot be resumed
// safely (no durable identity, no seedable history, or a backend restriction).
var ErrColdResumeUnavailable = errors.New("agent: this child session cannot be resumed")

// coldResumeState is the in-memory live overlay of one cold-resumed child: the
// read models use it while the run is in flight so a resumed child shows as
// running instead of as a stale historical row.
type coldResumeState struct {
	id         string
	name       string
	kind       string
	sessionID  string
	model      string
	status     string
	started    time.Time
	finished   time.Time
	output     string
	errText    string
	usage      rig.Usage
	iterations int
	done       chan struct{}
	// cancel stops the in-flight run during module teardown.
	cancel context.CancelFunc
}

// coldResumeSnapshot returns a copy of the live overlay for one child (nil when
// no cold resume is in flight).
func (s *service) coldResumeSnapshot(id string) *coldResumeState {
	s.coldMu.Lock()
	defer s.coldMu.Unlock()
	state, ok := s.coldResumes[strings.TrimSpace(id)]
	if !ok || state == nil {
		return nil
	}
	copied := *state
	return &copied
}

// coldResumeActive reports whether a cold resume run is in flight for the child.
func (s *service) coldResumeActive(id string) bool {
	return s.coldResumeSnapshot(id) != nil
}

// waitColdResume blocks until the current cold resume of id finishes (tests and
// the async send path). It returns false when no run is in flight.
func (s *service) waitColdResume(ctx context.Context, id string) bool {
	state := s.coldResumeSnapshot(id)
	if state == nil {
		return false
	}
	select {
	case <-state.done:
		return true
	case <-ctx.Done():
		return false
	}
}

// ColdResumeChild resumes one historical child session with a new human/Lead
// message and returns the child's answer. It is the shared implementation of
// SendChildAgent / TeamService.Send.
func (s *service) ColdResumeChild(ctx context.Context, childID, message string) (string, error) {
	record, ok := s.childRecord(childID)
	if !ok {
		return "", fmt.Errorf("agent: child agent %q not found", strings.TrimSpace(childID))
	}
	return s.coldResumeRecord(ctx, record, message, false)
}

// ColdResumeChildAsync starts a cold resume in the background (DSH send_message
// semantics: immediate ack, the answer arrives as a completion receipt). It
// returns the child record name for the ack.
func (s *service) ColdResumeChildAsync(childID, message string) (string, error) {
	record, ok := s.childRecord(childID)
	if !ok {
		return "", fmt.Errorf("agent: child agent %q not found", strings.TrimSpace(childID))
	}
	if err := s.validateColdResume(record, message); err != nil {
		return "", err
	}
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = record.ID
	}
	// Claim the child synchronously (so concurrent sends fail fast and the read
	// models show the resumed run immediately), then run it in the background.
	state, err := s.claimColdResume(record, message)
	if err != nil {
		return "", err
	}
	// The run is tracked (runWG) and cancellable on module teardown.
	runCtx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	s.startRunGoroutine(func() {
		defer cancel()
		_, _ = s.runClaimedColdResume(runCtx, record, message, true, state)
	})
	return name, nil
}

// childRecord looks up one durable child-session record.
func (s *service) childRecord(childID string) (childSessionRecord, bool) {
	if s == nil || s.childSessions == nil {
		return childSessionRecord{}, false
	}
	return s.childSessions.record(childID)
}

// childRecordByName finds a historical teammate record by its unique name.
func (s *service) childRecordByName(name string) (childSessionRecord, bool) {
	name = strings.TrimSpace(name)
	if s == nil || s.childSessions == nil || name == "" {
		return childSessionRecord{}, false
	}
	for _, record := range s.childSessions.records() {
		if strings.EqualFold(strings.TrimSpace(record.Name), name) {
			return record, true
		}
	}
	return childSessionRecord{}, false
}

// validateColdResume rejects a child that cannot be resumed safely.
func (s *service) validateColdResume(record childSessionRecord, message string) error {
	if strings.TrimSpace(record.ID) == "" {
		return fmt.Errorf("%w: the child has no durable session id", ErrColdResumeUnavailable)
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("%w: the message is empty", ErrColdResumeUnavailable)
	}
	if s.store == nil {
		return fmt.Errorf("%w: no durable store is wired", ErrColdResumeUnavailable)
	}
	if s.coldResumeActive(record.ID) {
		return fmt.Errorf("agent: child agent %q is already running", record.Name)
	}
	return nil
}

// claimColdResume atomically checks that no resume is in flight, installs the
// live overlay and marks the durable record running.
func (s *service) claimColdResume(record childSessionRecord, message string) (*coldResumeState, error) {
	if err := s.validateColdResume(record, message); err != nil {
		return nil, err
	}
	state := &coldResumeState{
		id:        record.ID,
		name:      record.Name,
		kind:      record.Kind,
		sessionID: record.ParentSessionID,
		model:     record.Model,
		status:    contracts.ChildAgentRunning,
		started:   time.Now(),
		done:      make(chan struct{}),
	}
	s.coldMu.Lock()
	if s.coldResumes == nil {
		s.coldResumes = make(map[string]*coldResumeState)
	}
	if s.coldResumes[record.ID] != nil {
		s.coldMu.Unlock()
		return nil, fmt.Errorf("agent: child agent %q is already running", record.Name)
	}
	s.coldResumes[record.ID] = state
	s.coldMu.Unlock()
	// The historical record is not "finished" while it runs again: mark it
	// running/unnotified so the read models and the receipt machinery see the
	// new run, and the old answer is not re-delivered as a completion.
	record.Status = contracts.ChildAgentRunning
	record.Error = ""
	record.Finished = time.Time{}
	record.Notified = true
	if s.childSessions != nil {
		s.childSessions.upsert(record)
	}
	return state, nil
}

// finishColdResume persists the run outcome and drops the live overlay.
func (s *service) finishColdResume(record childSessionRecord, state *coldResumeState, output string, runErr error, usage rig.Usage, iterations int, notified bool) {
	finished := time.Now()
	status := contracts.ChildAgentCompleted
	errText := ""
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			status = contracts.ChildAgentInterrupted
		} else {
			status = contracts.ChildAgentFailed
		}
		errText = runErr.Error()
	}
	record.Status = status
	record.Output = output
	record.Error = errText
	record.Finished = finished
	record.Notified = notified
	record.UsageTokens += usage.TotalTokens
	record.Iterations += iterations
	record.Updated = finished
	if !record.Started.IsZero() {
		// Keep the original spawn time; the run window is captured by Finished
		// and the activity rows.
	}
	if s.childSessions != nil {
		s.childSessions.upsert(record)
	}
	s.coldMu.Lock()
	delete(s.coldResumes, record.ID)
	s.coldMu.Unlock()
	if state != nil {
		if state.cancel != nil {
			state.cancel()
		}
		state.status = status
		state.output = output
		state.errText = errText
		state.finished = finished
		state.usage = usage
		state.iterations = iterations
		close(state.done)
	}
	s.notifyTeamChange()
}

// coldResumeRecord runs the actual resume synchronously (SendChildAgent and
// TeamService.Send).
func (s *service) coldResumeRecord(ctx context.Context, record childSessionRecord, message string, async bool) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := s.claimColdResume(record, message)
	if err != nil {
		return "", err
	}
	return s.runClaimedColdResume(ctx, record, message, async, state)
}

// runClaimedColdResume executes an already-claimed resume.
func (s *service) runClaimedColdResume(ctx context.Context, record childSessionRecord, message string, async bool, state *coldResumeState) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Seed the child session history on the first resume: the durable record
	// already stores the original task and answer, so a child whose live Rig
	// history never reached the store still continues from its own story.
	if err := s.seedColdResumeHistory(ctx, record); err != nil {
		s.finishColdResume(record, state, "", err, rig.Usage{}, 0, true)
		return "", err
	}
	// Record the incoming message as an activity row immediately (visible while
	// the run is in flight).
	record.Activity = appendChildActivity(record.Activity, contracts.TeamActivitySend,
		"冷恢复消息："+truncateColdResumeText(message, 240), false)
	if s.childSessions != nil {
		s.childSessions.upsert(record)
	}
	result, runErr := s.runColdResumeAgent(ctx, record, message)
	output := ""
	if runErr == nil {
		output = lastAssistantOutput(result.Messages)
	}
	if runErr == nil && strings.TrimSpace(output) == "" {
		runErr = errors.New("agent: the resumed child returned an empty answer")
	}
	kind := contracts.TeamActivityFinish
	text := "冷恢复完成：" + truncateColdResumeText(output, 400)
	if runErr != nil {
		kind = contracts.TeamActivityError
		text = "冷恢复失败：" + truncateColdResumeText(runErr.Error(), 240)
	}
	record.Activity = appendChildActivity(record.Activity, kind, text, runErr != nil)
	notified := !async
	s.finishColdResume(record, state, output, runErr, result.Usage, result.Iterations, notified)
	if runErr != nil {
		return "", runErr
	}
	return output, nil
}

// seedColdResumeHistory makes sure the child session has a durable history to
// load: on the first cold resume it is reconstructed from the record's original
// task and last answer. A child with neither a prompt nor a stored history is
// refused instead of running with an invented context.
func (s *service) seedColdResumeHistory(ctx context.Context, record childSessionRecord) error {
	if s.store == nil {
		return fmt.Errorf("%w: no durable store is wired", ErrColdResumeUnavailable)
	}
	history := s.store.History()
	existing, err := history.Load(ctx, record.ID)
	if err != nil {
		return fmt.Errorf("agent: load child session %q history: %w", record.ID, err)
	}
	if len(existing) > 0 {
		return nil
	}
	seed := make([]rig.Message, 0, 2)
	if prompt := strings.TrimSpace(record.Prompt); prompt != "" {
		seed = append(seed, rig.NewMessage(rig.RoleUser, prompt))
	}
	if output := strings.TrimSpace(record.Output); output != "" {
		seed = append(seed, rig.NewMessage(rig.RoleAssistant, output))
	}
	if len(seed) == 0 {
		return fmt.Errorf("%w: child %q has no prompt or stored answer to resume from", ErrColdResumeUnavailable, record.ID)
	}
	if err := history.Append(ctx, record.ID, seed...); err != nil {
		return fmt.Errorf("agent: seed child session %q history: %w", record.ID, err)
	}
	return nil
}

// runColdResumeAgent runs one product-agent turn whose history is the child
// session itself: Rig loads the child's prior messages and appends this turn
// (including tool calls/results) back to the same durable session.
func (s *service) runColdResumeAgent(ctx context.Context, record childSessionRecord, message string) (rig.RunResult, error) {
	parentID := strings.TrimSpace(record.ParentSessionID)
	cfg := s.configLocked()
	if parentID != "" {
		cfg = s.configForSessionLocked(parentID)
	}
	// The recorded model wins over the parent's current model selection; its
	// provider connection comes from the same model catalog row.
	if model := strings.TrimSpace(record.Model); model != "" {
		cfg = s.configForModelOverride(cfg, model)
	}
	// A resumed child is still the child of its parent: an orchestrator parent
	// hands it the executor role (surface + persona), exactly like a live
	// delegation, so a resumed worker does not come back as a reader.
	childTools := withoutToolNames(s.delegatedToolsForSession(parentID), subagentExcludedTools())
	workspaceRoot := s.sessionWorkspaceRoot(parentID)
	cfg.SystemPrompt = s.delegatedSystemPrompt(ctx, parentID, cfg, toolNameSet(childTools), workspaceRoot)
	provider, err := s.newProvider(cfg, s.thinkingParamsForSession(parentID, cfg.Model))
	if err != nil {
		return rig.RunResult{}, fmt.Errorf("agent: cold resume %q: create provider: %w", record.ID, err)
	}
	// The cold-resume turn is a kit.Config: the child session is the history.
	runConfig := kit.Config{
		Provider:      provider,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxIterations: delegatedIterationBudget(cfg),
		Tools:         withWorkspaceRoot(withAgentScope(childTools, parentID, contracts.AgentRunOriginSubagent), workspaceRoot),
		Logger:        s.logger,
		// No discovery here: the child excludes tool_search, so InitialActiveTools
		// stays nil (all registered tools active).
	}
	s.withKitContextBudget(&runConfig, cfg, provider)
	hostModule := hostbundle.New(hostbundle.Options{
		History: s.store.History(),
		// Same role rule as a live delegation: the resumed child executes as its
		// parent's delegated role, so the gate must judge it that way.
		Gate:  s.childGate(parentID),
		Spill: s.spillPolicy(ctx, workspaceRoot),
	})
	runner, err := kit.BuildConfig(ctx, runConfig, hostModule)
	if err != nil {
		return rig.RunResult{}, fmt.Errorf("agent: cold resume %q: create agent: %w", record.ID, err)
	}
	result, runErr := runner.Run(ctx, rig.RunRequest{
		SessionID: record.ID, // the child session is the history
		Input:     []rig.Message{rig.NewMessage(rig.RoleUser, message)},
	})
	if runErr != nil {
		return result, fmt.Errorf("agent: cold resume %q: %w", record.Name, runErr)
	}
	return result, nil
}

// applyColdResumeOverlay replaces a historical row with the live in-flight
// resume state (running, not read-only). A finished run drops the overlay, so
// the durable row (completed/failed/interrupted) is shown again.
func (s *service) applyColdResumeOverlay(agent contracts.ChildAgent) contracts.ChildAgent {
	if s == nil {
		return agent
	}
	state := s.coldResumeSnapshot(agent.ID)
	if state == nil {
		return agent
	}
	agent.Historical = false
	agent.Resumable = false
	agent.Status = state.status
	agent.Started = state.started
	agent.Finished = state.finished
	agent.Output = state.output
	agent.Error = state.errText
	agent.UsageTokens += state.usage.TotalTokens
	agent.Iterations += state.iterations
	if agent.Model == "" {
		agent.Model = state.model
	}
	return agent
}

// lastAssistantOutput returns the last assistant text of one run's messages.
func lastAssistantOutput(messages []rig.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == rig.RoleAssistant {
			if text := strings.TrimSpace(messageText(messages[index])); text != "" {
				return text
			}
		}
	}
	return ""
}

// appendChildActivity appends one bounded activity row to a record.
func appendChildActivity(activity []childSessionActivity, kind, text string, failed bool) []childSessionActivity {
	out := append(append([]childSessionActivity(nil), activity...), childSessionActivity{
		Kind: kind, Text: text, Time: time.Now(), Error: failed,
	})
	// Keep the recent window bounded (the session store normalizes per-row
	// text; this bounds the row count).
	const maxChildResumeActivity = 32
	if len(out) > maxChildResumeActivity {
		out = out[len(out)-maxChildResumeActivity:]
	}
	return out
}

// truncateColdResumeText bounds one activity text.
func truncateColdResumeText(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
