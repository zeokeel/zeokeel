package rightpanel

// The Agents (团队) pane is the inspector's quick view of the team the lead is
// orchestrating: the lead's run state, the async teammates with their truthful
// run state and activity, the read-only synchronous delegations, the selected
// object's detail and the native spawn form. Phase 3d moved the whole rendering
// anatomy to uikit.TeamSurface (AGENT-SURFACES.md S9); this module keeps only
// the data (contracts.TeamService / SubagentActivityLister snapshots), the
// persistent pane state and the data actions, so the pane's badges and details
// have one painter (uikit.StatusBadge) instead of a module-local copy.
//
// The pane's empty state explains the team capability and offers the native
// spawn form; the "团队服务未启用" state stays here because it is the absent
// optional service, not the team semantics.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildAgentsPane renders the team inspector: the lead row, the teammate list,
// the read-only delegations, the selected object's detail and the native spawn
// form (all in uikit.TeamSurface).
func buildAgentsPane(c *ui.Context, t *design.Tokens, chat contracts.ChatService, team contracts.TeamService,
	activities contracts.SubagentActivityLister, runtime contracts.UIRuntime, st *state) {
	var members []contracts.TeamMember
	if team != nil {
		members = team.Members()
	}
	subagents := visibleSubagentActivities(activities, chat.ActiveSession())
	if team == nil && len(subagents) == 0 {
		// The host did not provide the optional team service: the pane explains
		// the missing capability instead of pretending an empty team.
		buildEmptyPane(c, uikit.T(st.locale, "团队服务未启用", "Team service not enabled"))
		return
	}

	// The spawn entry points exist only where the service can serve them: the
	// empty state's prominent CTA and the teammate section's quiet action.
	var openSpawn func()
	if team != nil {
		openSpawn = func() {
			st.openTeamSpawnDialog()
			runtime.Invalidate()
		}
	}

	session, _ := chat.Session(chat.ActiveSession())
	uikit.TeamSurface(c, uikit.TeamSurfaceOptions{
		Locale:    st.locale,
		Pane:      uikit.TeamPaneMembers,
		Lead:      uikit.TeamLead{Running: session.Running, Model: strings.TrimSpace(chat.Config().Model)},
		Members:   members,
		Subagents: subagents,

		MemberList:         &st.agentsList,
		MemberSel:          &st.agentsSel,
		SubagentList:       &st.subagentList,
		SubagentSel:        &st.subagentsSel,
		SelectedMemberID:   &st.selectedMemberID,
		SelectedSubagentID: &st.selectedSubagentID,
		SteerDraft:         &st.steerDraft,
		Status:             &st.teamStatus,
		Err:                &st.teamErr,

		CloseOpen: &st.memberCloseOpen,
		CloseID:   &st.memberCloseID,
		CloseName: &st.memberCloseName,

		Spawn: uikit.TeamSpawnForm{
			Open:     &st.teamSpawnOpen,
			Name:     &st.teamSpawnName,
			Prompt:   &st.teamSpawnPrompt,
			Model:    &st.teamSpawnModel,
			Error:    &st.teamSpawnErr,
			Busy:     &st.teamSpawnBusy,
			OnSubmit: func() { st.submitTeamSpawn(team, runtime) },
			OnCancel: st.cancelTeamSpawn,
		},

		OnOpenSpawn: openSpawn,
		OnSend: func(memberID, message string) {
			if team == nil {
				return
			}
			if err := team.SendAsync(memberID, message); err != nil {
				st.teamErr = st.tr("发送失败：", "Send failed: ") + err.Error()
				st.teamStatus = ""
			} else {
				st.teamErr = ""
				st.steerDraft = ""
				st.teamStatus = st.tr("已发送", "Sent")
			}
		},
		OnInterrupt: func(memberID string) {
			if team == nil {
				return
			}
			if err := team.Interrupt(memberID); err != nil {
				st.teamErr = st.tr("中断失败：", "Interrupt failed: ") + err.Error()
				st.teamStatus = ""
			} else {
				st.teamErr = ""
				st.teamStatus = st.tr("已中断", "Interrupted")
			}
		},
		OnClose: func(memberID string) {
			if team == nil {
				return
			}
			if err := team.Close(memberID); err != nil {
				st.teamErr = st.tr("关闭失败：", "Close failed: ") + err.Error()
				st.teamStatus = ""
			} else {
				st.teamErr = ""
				st.teamStatus = st.tr("已关闭", "Closed")
				st.selectedMemberID = ""
				st.selectedSubagentID = ""
				st.steerDraft = ""
			}
		},
		Invalidate: runtime.Invalidate,
	})
}

// visibleSubagentActivities returns the delegations the pane shows. The
// inspector describes the conversation it is attached to, so only the active
// session's delegations show; a background session's subagent run would be
// confusing there. With no active session there is no conversation to confuse
// and the whole (bounded) snapshot stays visible.
func visibleSubagentActivities(lister contracts.SubagentActivityLister, activeSession string) []contracts.SubagentActivity {
	if lister == nil {
		return nil
	}
	all := lister.SubagentActivities()
	if len(all) == 0 {
		return nil
	}
	active := strings.TrimSpace(activeSession)
	if active == "" {
		return all
	}
	out := make([]contracts.SubagentActivity, 0, len(all))
	for _, activity := range all {
		if strings.TrimSpace(activity.SessionID) == active {
			out = append(out, activity)
		}
	}
	return out
}
