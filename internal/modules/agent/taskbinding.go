package agent

// Session ⇄ task binding enforcement (M5).
//
// The task board already records an execution policy per task
// (contracts.TaskLeaseService.SetTools / Tools). Recording it was not enough:
// nothing refused a tool outside the policy, so the binding was a note the model
// could ignore. This file makes it a ceiling — a session bound to a task may only
// call the tools that task's binding names.
//
// The binding is one settings row (contracts.SessionBoundTaskSettingKey) holding
// the task id, so it survives a restart and disappears with its conversation
// like every other `session.<id>.*` row. It only ever *narrows*: an unbound
// session, a task with no binding, and an assembly without the lease module all
// take exactly the path they took before this file existed (see
// taskBindingDenial — it is written as "reasons to do nothing" first).
//
// Where the veto sits: the check in plan_mode.go's gate runs right after the
// tool name is resolved and before every branch that can allow a call — the
// plan/chat whitelists, the per-session permission override, the OS sandbox
// exemption, the interaction-tool exemption, the remembered "always allow" set
// and the approval dialog. Those branches can only widen, so a check placed
// after them would be bypassed by a remembered tool or a sandbox exemption; in
// front of them, a task's policy is a ceiling rather than a hint.

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var _ contracts.SessionTaskBinder = (*service)(nil)

// BindSessionTask implements contracts.SessionTaskBinder: it points sessionID at
// taskID, and an empty taskID clears the binding.
//
// The task is not required to exist yet — dispatch and binding are two calls, and
// a binding to a task the board no longer has is inert at the gate instead of an
// error here: a stale row must stay removable (and a conversation must not
// become unusable because a task was deleted).
func (s *service) BindSessionTask(_ context.Context, sessionID, taskID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.ErrSessionTaskBindingInvalid
	}
	normalized, err := normalizeBoundTaskID(taskID)
	if err != nil {
		return err
	}
	if s.settings == nil {
		return ErrSettingsUnavailable
	}
	return s.settings.Set(contracts.SessionBoundTaskSettingKey(sessionID), normalized)
}

// SessionTask implements contracts.SessionTaskBinder: the task the session is
// bound to, or "" when it is not bound.
func (s *service) SessionTask(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", contracts.ErrSessionTaskBindingInvalid
	}
	if s.settings == nil {
		return "", ErrSettingsUnavailable
	}
	return s.boundTaskRow(sessionID), nil
}

// boundTaskRow reads the raw binding row ("" when the session has none). A
// corrupt row cannot be told apart from "no binding"; both mean "nothing to
// enforce", never an error — a bad settings value must not break a run.
func (s *service) boundTaskRow(sessionID string) string {
	if s.settings == nil || sessionID == "" {
		return ""
	}
	return strings.TrimSpace(s.settings.Get(contracts.SessionBoundTaskSettingKey(sessionID), ""))
}

// normalizeBoundTaskID validates one task id for the settings row: "" clears, and
// a value with whitespace/control characters inside or over the contract's bound
// is refused (ErrSessionTaskBindingInvalid) instead of being silently sanitized
// into something the caller did not ask for — the same rule the task board's own
// tool binding follows.
func normalizeBoundTaskID(taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", nil
	}
	if utf8.RuneCountInString(taskID) > contracts.MaxSessionBoundTaskLen {
		return "", contracts.ErrSessionTaskBindingInvalid
	}
	for _, r := range taskID {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", contracts.ErrSessionTaskBindingInvalid
		}
	}
	return taskID, nil
}

// taskBindingDenial returns the model-visible reason a bound session may not call
// name, or "" when the call may proceed. It is intentionally written as "reasons
// to do nothing" first: everything that cannot be enforced is inert, because a
// binding that fails closed would turn a deleted task or a stripped assembly into
// a product that cannot run a tool at all.
func (s *service) taskBindingDenial(ctx context.Context, sessionID, name string) string {
	taskID := s.boundTaskRow(sessionID)
	if taskID == "" {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	leases := s.taskLease
	if leases == nil {
		// Inert, not a failure: this build cannot enforce the row. Logged once
		// per task (never per tool call).
		s.logTaskBindingOnce("no-lease:"+taskID,
			"[agent] session %s is bound to task %s but this build has no task lease service; its tool binding is not enforced",
			sessionID, taskID)
		return ""
	}
	binding, ok, err := leases.Tools(ctx, taskID)
	if err != nil {
		// A broken board read must not brick the conversation.
		s.logTaskBindingOnce("read:"+taskID,
			"[agent] read the tool binding of task %s (session %s): %v; the binding is not enforced",
			taskID, sessionID, err)
		return ""
	}
	if !ok || len(binding.Tools) == 0 {
		// The task is gone, or nobody bound tools to it: no policy to enforce.
		s.logTaskBindingOnce("empty:"+taskID,
			"[agent] task %s has no tool binding; session %s is unrestricted by it", taskID, sessionID)
		return ""
	}
	for _, allowed := range binding.Tools {
		if strings.TrimSpace(allowed) == name {
			return ""
		}
	}
	return fmt.Sprintf("任务 %s 只允许这些工具：%v；这次调用 %s 被拒绝", taskID, binding.Tools, name)
}

// logTaskBindingOnce logs one inert explanation per reason, at most once per
// service: the gate runs on every tool call, so an unenforceable binding must not
// produce a line per call. The lock is never held while a lease is read.
func (s *service) logTaskBindingOnce(key, format string, args ...any) {
	s.taskBindingLogMu.Lock()
	if s.taskBindingLogged == nil {
		s.taskBindingLogged = make(map[string]bool)
	}
	if s.taskBindingLogged[key] {
		s.taskBindingLogMu.Unlock()
		return
	}
	s.taskBindingLogged[key] = true
	s.taskBindingLogMu.Unlock()
	s.logger.Printf(format, args...)
}

// taskBindingGate attaches the model-visible denial reason to the service gate.
//
// rig.Gate's contract carries only the decision, so the readable
// "任务 task-1 只允许这些工具：[browser_open]；这次调用 read_file 被拒绝" travels
// through the SDK's optional DenialExplainer channel (a structurally matched
// method — neither side imports the other) which the agent renders into the tool
// error the model sees. A denial from any other branch keeps the SDK's historical
// text: ExplainDenial recomputes the binding and answers "" when that binding did
// not refuse the call.
type taskBindingGate struct {
	rig.Gate
	service   *service
	sessionID string
}

// ExplainDenial implements the SDK's optional denial-explanation channel.
func (g taskBindingGate) ExplainDenial(tool rig.Tool, call rig.ToolCall) string {
	name := call.Name
	if tool != nil && strings.TrimSpace(tool.Name()) != "" {
		name = tool.Name()
	}
	// This channel has no context and must not block: the lease read is an
	// in-memory lookup, and the goal here is one sentence for the model.
	return g.service.taskBindingDenial(context.Background(), g.sessionID, name)
}

// taskBindingGate wraps inner in the explainer above. The check itself lives in
// gate() (plan_mode.go) so it runs before every branch that can allow a call;
// this wrapper only adds the reason.
func (s *service) taskBindingGate(sessionID string, inner rig.Gate) rig.Gate {
	return taskBindingGate{Gate: inner, service: s, sessionID: sessionID}
}
