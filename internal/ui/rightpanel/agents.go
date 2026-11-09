package rightpanel

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildAgentsPane renders the team inspector: the live teammate rows (name, id,
// status badge and latest answer) and, once a member is selected, the steer
// input with the Send/Interrupt/Close actions. The team backend is optional:
// without it (or without members) the pane shows an explicit empty state.
func buildAgentsPane(c *ui.Context, t *design.Tokens, team contracts.TeamService, runtime contracts.UIRuntime, st *state) {
	if team == nil {
		buildEmptyPane(c, uikit.T(st.locale, "团队服务未启用", "Team service not enabled"))
		return
	}
	members := team.Members()
	if len(members) == 0 {
		buildEmptyPane(c, uikit.T(st.locale, "还没有队友", "No teammates yet"))
		return
	}

	// The native list owns the rows' hover, choice and keyboard: Up/Down choose
	// a teammate, which opens its steer pane (docs/ui/list.md).
	chosen := -1
	for index := range members {
		if members[index].ID == st.selectedMemberID {
			chosen = index
			break
		}
	}
	st.agentsList.Key = func(row int) any { return members[row].ID }
	st.agentsList.Label = func(row int) string {
		return firstNonEmpty(members[row].Name, members[row].ID)
	}
	st.agentsSel = chosen
	st.agentsList.Selected = &st.agentsSel
	list := ui.List(c, &st.agentsList, len(members)).
		Label(uikit.T(st.locale, "队友列表", "Teammates")).
		Rows(func(row ui.ListRow) {
			buildAgentRow(row, t, members[row.Index], st)
		})
	if list.Changed() && st.agentsSel >= 0 && st.agentsSel < len(members) {
		st.selectedMemberID = members[st.agentsSel].ID
		st.teamStatus = ""
		st.teamErr = ""
		runtime.Invalidate()
	}

	selected := selectedAgent(members, st.selectedMemberID)
	if selected == nil {
		design.Apply(ui.Text(c, uikit.T(st.locale,
			"选择一个队友以发送消息或中断", "Select a teammate to steer or interrupt")),
			t.TextMuted(design.TypeXXS))
		return
	}
	buildAgentControls(c, t, team, runtime, st, *selected)
}

// selectedAgent returns the member with id, or nil while nothing is selected
// (or the selected teammate disappeared).
func selectedAgent(members []contracts.TeamMember, id string) *contracts.TeamMember {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	for _, member := range members {
		if member.ID == id {
			member := member
			return &member
		}
	}
	return nil
}

// buildAgentRow is one teammate row of the native list: name, id, status and
// the latest answer summary.
func buildAgentRow(row ui.ListRow, t *design.Tokens, member contracts.TeamMember, st *state) {
	c := row.Context
	name := firstNonEmpty(member.Name, member.ID)
	ui.Column(c).FillWidth().Padding(design.Space2, design.Space2).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeS)).FontWeight(600).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			label := strings.TrimSpace(member.Status)
			color := t.LabelSecondary
			switch strings.ToLower(label) {
			case "active":
				label = uikit.T(st.locale, "运行中", "Running")
				color = t.SuccessText
			case "interrupted":
				label = uikit.T(st.locale, "已中断", "Interrupted")
				color = t.WarningText
			}
			if label == "" {
				label = uikit.T(st.locale, "未知", "Unknown")
			}
			design.Apply(ui.Badge(c, label), t.Badge()).TextColor(color)
		})
		design.Apply(ui.Text(c, member.ID), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary)
		last := strings.TrimSpace(member.Last)
		if last == "" {
			last = uikit.T(st.locale, "还没有结果", "No result yet")
		}
		design.Apply(ui.Text(c, last), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
	})
}

// buildAgentControls is the steer panel for the selected teammate.
func buildAgentControls(c *ui.Context, t *design.Tokens, team contracts.TeamService,
	runtime contracts.UIRuntime, st *state, member contracts.TeamMember) {
	name := firstNonEmpty(member.Name, member.ID)
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(600).
			TextColor(t.LabelSecondary)
		ui.TextInput(c, &st.steerDraft).Label(uikit.T(st.locale, "发送消息", "Message")).
			Placeholder(uikit.T(st.locale, "发给选中队友的消息", "Message to the selected teammate")).
			FillWidth()
		draft := strings.TrimSpace(st.steerDraft)
		ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
			send := design.Apply(ui.PrimaryButton(c, uikit.T(st.locale, "发送", "Send")),
				t.PrimaryButton()).Disabled(draft == "")
			if send.Clicked() && draft != "" {
				if err := team.SendAsync(member.ID, draft); err != nil {
					st.teamErr = uikit.T(st.locale, "发送失败：", "Send failed: ") + err.Error()
					st.teamStatus = ""
				} else {
					st.teamErr = ""
					st.steerDraft = ""
					st.teamStatus = uikit.T(st.locale, "已发送", "Sent")
				}
				runtime.Invalidate()
			}
			if ui.Button(c, uikit.T(st.locale, "中断", "Interrupt")).Clicked() {
				if err := team.Interrupt(member.ID); err != nil {
					st.teamErr = uikit.T(st.locale, "中断失败：", "Interrupt failed: ") + err.Error()
					st.teamStatus = ""
				} else {
					st.teamErr = ""
					st.teamStatus = uikit.T(st.locale, "已中断", "Interrupted")
				}
				runtime.Invalidate()
			}
			if design.Apply(ui.Button(c, uikit.T(st.locale, "关闭", "Close")), t.DangerButton()).Clicked() {
				st.memberCloseOpen = true
				st.memberCloseID = member.ID
				st.memberCloseName = name
				runtime.Invalidate()
			}
		})
		if st.teamStatus != "" {
			design.Apply(ui.Badge(c, st.teamStatus), t.Badge()).TextColor(t.SuccessText)
		}
		if st.teamErr != "" {
			uikit.ErrorLine(c, st.teamErr)
		}
	})
	buildMemberCloseDialog(c, team, runtime, st)
}

// buildMemberCloseDialog is the native confirmation of the destructive "close
// teammate" action: the member row stays, the action is not one click away.
func buildMemberCloseDialog(c *ui.Context, team contracts.TeamService, runtime contracts.UIRuntime, st *state) {
	if !st.memberCloseOpen {
		return
	}
	name := firstNonEmpty(st.memberCloseName, st.memberCloseID)
	message := st.tr(
		fmt.Sprintf("“%s”将被停止并移除，此操作无法撤销。", name),
		fmt.Sprintf("%q will be stopped and removed. This cannot be undone.", name))
	switch ui.AlertDialog(c, &st.memberCloseOpen, st.tr("关闭队友", "Close teammate"),
		message, st.tr("取消", "Cancel"), st.tr("确认关闭", "Close teammate")) {
	case 1:
		if err := team.Close(st.memberCloseID); err != nil {
			st.teamErr = st.tr("关闭失败：", "Close failed: ") + err.Error()
			st.teamStatus = ""
		} else {
			st.teamErr = ""
			st.teamStatus = st.tr("已关闭", "Closed")
			st.selectedMemberID = ""
			st.steerDraft = ""
		}
		st.memberCloseID = ""
		st.memberCloseName = ""
		runtime.Invalidate()
	case 0, ui.AlertDismissed:
		st.memberCloseID = ""
		st.memberCloseName = ""
		runtime.Invalidate()
	}
}
