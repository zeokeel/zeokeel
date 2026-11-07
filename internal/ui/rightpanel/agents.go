package rightpanel

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildAgentsPane renders the team inspector: the live teammate rows (name, id,
// status badge and latest answer) and, once a member is selected, the steer
// input with the Send/Interrupt/Close actions. The team backend is optional:
// without it (or without members) the pane shows an explicit empty state.
func buildAgentsPane(c *ui.Context, t *design.Tokens, team contracts.TeamService, runtime contracts.UIRuntime, st *state) {
	if team == nil {
		buildEmptyPane(c, t, icons.Data(), uikit.T(st.locale, "团队服务未启用", "Team service not enabled"))
		return
	}
	members := team.Members()
	if len(members) == 0 {
		buildEmptyPane(c, t, icons.Data(), uikit.T(st.locale, "还没有队友", "No teammates yet"))
		return
	}

	list := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space1)
	list.Children(func() {
		for _, member := range members {
			member := member
			buildAgentRow(c, t, member, runtime, st)
		}
	})

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

// buildAgentRow is one teammate card: selectable row with name, id, status and
// the latest answer summary.
func buildAgentRow(c *ui.Context, t *design.Tokens, member contracts.TeamMember, runtime contracts.UIRuntime, st *state) {
	name := firstNonEmpty(member.Name, member.ID)
	row := ui.ButtonBase(c).FillWidth().Padding(design.Space2).Radius(design.RadiusSM).Label(name)
	if member.ID == st.selectedMemberID {
		row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, name), t.Font(design.TypeS)).FontWeight(600).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				label := strings.TrimSpace(member.Status)
				color := t.LabelSecondary
				switch strings.ToLower(label) {
				case "active":
					label = uikit.T(st.locale, "运行中", "Running")
					color = t.Success
				case "interrupted":
					label = uikit.T(st.locale, "已中断", "Interrupted")
					color = t.WarningText
				}
				if label == "" {
					label = uikit.T(st.locale, "未知", "Unknown")
				}
				design.Apply(ui.Badge(c, label), t.Badge()).TextColor(color)
			})
			design.Apply(ui.Text(c, member.ID), t.MonoFamily()).FontSize(10).
				TextColor(t.LabelTertiary)
			last := strings.TrimSpace(member.Last)
			if last == "" {
				last = uikit.T(st.locale, "还没有结果", "No result yet")
			}
			design.Apply(ui.Text(c, last), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
		})
	})
	row.OnClick(func() {
		st.selectedMemberID = member.ID
		st.teamStatus = ""
		st.teamErr = ""
		runtime.Invalidate()
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
			if ui.Button(c, uikit.T(st.locale, "关闭", "Close")).Clicked() {
				if err := team.Close(member.ID); err != nil {
					st.teamErr = uikit.T(st.locale, "关闭失败：", "Close failed: ") + err.Error()
					st.teamStatus = ""
				} else {
					st.teamErr = ""
					st.teamStatus = uikit.T(st.locale, "已关闭", "Closed")
					st.selectedMemberID = ""
					st.steerDraft = ""
				}
				runtime.Invalidate()
			}
		})
		if st.teamStatus != "" {
			design.Apply(ui.Badge(c, st.teamStatus), t.Badge()).TextColor(t.Success)
		}
		if st.teamErr != "" {
			buildErrorLine(c, t, st.teamErr)
		}
	})
}
