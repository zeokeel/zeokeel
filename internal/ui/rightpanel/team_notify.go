package rightpanel

// Team completion toasts. The team backend asks the chat subscribers for a
// frame whenever a run starts or finishes (agent/team_state.go), so the
// inspector sees run-state transitions here: one transition to done/failed
// raises one action toast ("查看" opens the team pane on that member), deduped
// by the run's finished stamp so a repaint storm never repeats it. The first
// observation of a member is a silent baseline: opening the panel must not toast
// its history.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// teamSeenState is the last observed run state of one member.
type teamSeenState struct {
	// run is the observed contracts.TeamRun* state.
	run string
	// finished is the run's Finished stamp in UnixNano (0 when unknown).
	finished int64
	// notified is the finished stamp already toasted (dedupe).
	notified int64
}

// observeTeamRunChanges reads the live team roster on the UI thread and raises
// one completion/failure toast per finished run. It runs from the chat
// subscription's posted callback, never from a build.
func (st *state) observeTeamRunChanges() {
	if st == nil {
		return
	}
	raw, ok := st.ctx.Get(contracts.KeyTeam)
	if !ok || raw == nil {
		return
	}
	team, ok := raw.(contracts.TeamService)
	if !ok || team == nil {
		return
	}
	members := team.Members()
	live := make(map[string]bool, len(members))
	for _, member := range members {
		live[member.ID] = true
		finished := int64(0)
		if !member.Finished.IsZero() {
			finished = member.Finished.UnixNano()
		}
		previous, seen := st.teamSeen[member.ID]
		if !seen {
			// Silent baseline: remember the current state as already handled.
			st.teamSeen[member.ID] = teamSeenState{run: member.RunState, finished: finished, notified: finished}
			continue
		}
		st.teamSeen[member.ID] = teamSeenState{run: member.RunState, finished: finished, notified: previous.notified}
		if !teamRunFinished(member.RunState) {
			continue
		}
		if finished != 0 && finished == previous.notified {
			continue
		}
		if previous.run != contracts.TeamRunRunning && previous.run != contracts.TeamRunIdle {
			// Only a transition out of a run raises a toast; a member first seen
			// finished was baselined above.
			continue
		}
		st.notifyTeamCompletion(member, finished)
		state := st.teamSeen[member.ID]
		state.notified = finished
		if finished == 0 {
			state.notified = -1
		}
		st.teamSeen[member.ID] = state
	}
	for id := range st.teamSeen {
		if !live[id] {
			delete(st.teamSeen, id)
		}
	}
}

// teamRunFinished reports whether the run state is a terminal outcome.
func teamRunFinished(runState string) bool {
	return runState == contracts.TeamRunDone || runState == contracts.TeamRunFailed
}

// notifyTeamCompletion raises the one completion/failure toast of a member.
func (st *state) notifyTeamCompletion(member contracts.TeamMember, finished int64) {
	if st.notifier == nil {
		return
	}
	name := strings.TrimSpace(member.Name)
	if name == "" {
		name = member.ID
	}
	level := contracts.NotifySuccess
	var text string
	if member.RunState == contracts.TeamRunFailed {
		level = contracts.NotifyError
		text = st.tr("队友运行失败：", "Teammate failed: ") + name
		if detail := strings.TrimSpace(member.Error); detail != "" {
			text += " · " + teamOutputPreview(detail, 80)
		}
	} else {
		text = st.tr("队友已完成：", "Teammate finished: ") + name
		if detail := strings.TrimSpace(member.Last); detail != "" {
			text += " · " + teamOutputPreview(detail, 80)
		}
	}
	memberID := member.ID
	action := func() {
		st.selectedMemberID = memberID
		st.selectedSubagentID = ""
		st.openPane(tabAgents)
	}
	if actionNotifier, ok := st.notifier.(contracts.ActionNotifier); ok {
		actionNotifier.NotifyAction(level, text, st.tr("查看", "View"), action)
		return
	}
	st.notifier.Notify(level, text)
}

// teamOutputPreview turns one teammate answer (or error) into a one-line toast
// / row preview: it drops code fences and their content (a final answer often
// ends in a fenced path dump), strips leading markdown markers (#, >, list
// bullets), removes inline backticks and collapses every run of whitespace, then
// bounds the result to limit runes. A markdown-heavy answer therefore reads as
// "脚本回复 工作目录已确认 可以继续下一步了" instead of raw fence noise.
func teamOutputPreview(text string, limit int) string {
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			// Drop the fence line and everything inside the fenced block: a
			// fenced path/code dump is noise in a toast.
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>")
		trimmed = strings.TrimSpace(trimmed)
		if bullet, ok := trimMarkdownBullet(trimmed); ok {
			trimmed = bullet
		}
		trimmed = strings.ReplaceAll(trimmed, "`", "")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	collapsed := strings.Join(strings.Fields(strings.Join(cleaned, " ")), " ")
	runes := []rune(collapsed)
	if len(runes) <= limit {
		return collapsed
	}
	return string(runes[:limit]) + "…"
}

// trimMarkdownBullet strips one leading list marker ("- ", "* ", "+ ", "1. ",
// "2) ") from line, reporting whether it found one.
func trimMarkdownBullet(line string) (string, bool) {
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker)), true
		}
	}
	index := 0
	for index < len(line) && line[index] >= '0' && line[index] <= '9' {
		index++
	}
	if index > 0 && index < len(line)-1 && (line[index] == '.' || line[index] == ')') && line[index+1] == ' ' {
		return strings.TrimSpace(line[index+2:]), true
	}
	return line, false
}
