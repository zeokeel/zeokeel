package uiroot

// The close/quit guard's read model: is backend work still in flight? The
// desktop host asks Root.RunningWork from its close policy (internal/host) and
// confirms closing the window or quitting while it is true. The root reads the
// live services from the kernel context instead of the UI modules, so the host
// does not need to know which module owns a run.

import (
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// RunningWork reports whether quitting would interrupt backend work: a running
// chat/automation run, a session goal in its active phase, or a running
// background job. Finished, paused, blocked and complete work does not count.
// Missing services (a stripped product) are not running work.
//
// It is safe to call from any goroutine; the host calls it on the UI thread
// from the close listener.
func (r *Root) RunningWork() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	chat := r.chatService
	kernel := r.kernelContext
	r.mu.Unlock()
	if chat == nil && kernel != (cordis.Context{}) {
		if fromKernel, ok := kernel.Get(contracts.KeyChat); ok {
			chat = fromKernel
		}
	}
	var sessions []contracts.ChatSessionInfo
	if chat != nil {
		sessions = chat.Sessions()
		for _, session := range sessions {
			if session.Running {
				return true
			}
		}
	}
	if r.activeGoal(kernel, sessions) {
		return true
	}
	if kernel == (cordis.Context{}) {
		return false
	}
	if jobs, ok := kernel.Get(contracts.KeyJobs); ok && jobs != nil {
		for _, job := range jobs.Jobs() {
			if job.Running {
				return true
			}
		}
	}
	return false
}

// activeGoal reports whether a per-session goal in its active phase is driving
// work: the continuation driver advances such a goal, so quitting would
// interrupt it.
func (r *Root) activeGoal(kernel cordis.Context, sessions []contracts.ChatSessionInfo) bool {
	if kernel == (cordis.Context{}) {
		return false
	}
	goals, ok := kernel.Get(contracts.KeyGoal)
	if !ok || goals == nil {
		return false
	}
	for _, session := range sessions {
		if goal, ok := goals.Goal(session.ID); ok && goal.Phase == contracts.GoalPhaseActive {
			return true
		}
	}
	return false
}
