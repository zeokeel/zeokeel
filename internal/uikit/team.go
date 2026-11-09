package uikit

// The team surface entry component (AGENT-SURFACES.md S9): the inspector's one
// anatomy for the multi-agent team and the shared task board. Before Phase 3d
// the rightpanel module built every row, badge and detail itself; the team pane
// and the task board now render here, so the run-state vocabulary has one
// painter (StatusBadge) and the module keeps only data, cache and commands.
//
// Team pane (TeamPaneMembers):
//   - 主代理 (lead): the active conversation's run state and model. Its badge is
//     the same tinted StatusBadge as every teammate's; only a running session
//     paints the warning tint (F-1: the lead's idle badge used to be the old
//     plain BgHover badge, the pane's only badge with a different anatomy).
//   - 队友 (teammates): one native List; each row carries the truthful tracked
//     RunState badge, the current activity and the run meta. Availability
//     ("active") is never drawn as running: an unknown run state falls back to
//     the manager's availability (interrupted/idle), per DESIGN.md AP-08.
//   - 同步子代理 (synchronous delegations, read-only): the active session's
//     blocking subagent calls. Their status folds and labels with the tool
//     vocabulary; the row's "子代理" type marker and every other badge share the
//     same tinted anatomy.
//   - the selected row's detail sits BELOW its list in a design.Panel, never
//     inside the selected row (the accent selection background would make it
//     unreadable); the destructive close confirmation has no default action.
//   - the "new teammate" form is a ui.Form/Field Modal whose Create action is
//     never the default, so a stray Enter never spawns a teammate.
//
// Task pane (TeamPaneTasks):
//   - one native List with status Header rows and counts (action-first order:
//     in progress → pending → completed → unknown);
//   - inline owner PopoverMenuButton (the optional TaskAssigner), the
//     dependency/blocker line, the selected task's detail below the list and the
//     destructive delete confirmation with no default action.
//
// The component is a frame builder: it owns the immediate-mode interaction
// (list choice, expansion, dialogs) while the module passes its persistent view
// state by pointer and its data actions as callbacks. All copy is localized
// through Locale (nil falls back to the product's Chinese copy).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Legacy control metrics moved verbatim from the rightpanel module when the
// surface was extracted (Phase 3d): the pane's pre-existing grandfathered
// heights/weights, named here so the entry component has one local token for
// each. The design contract's control-height / heading-role tokens start in the
// design package (DESIGN.md §5.4 / D6); until one exists, a named constant beats
// a fresh bare literal in the component that now owns the anatomy.
const (
	// teamHeadingWeight is the heading weight of the pane's titles and row
	// names (600, the pre-Phase-3d value; DESIGN.md §5.3 allows it paired with
	// a type role until a heading role lands).
	teamHeadingWeight = 600
	// teamCompactActionHeight is the compact row/dialog action height (26).
	teamCompactActionHeight float32 = 26
	// teamSpawnPromptHeight is the spawn form's task textarea height (92).
	teamSpawnPromptHeight float32 = 92
	// teamTimelineClockWidth is the activity timeline's clock column (48).
	teamTimelineClockWidth float32 = 48
	// teamNoMinWidth lets a growing text shrink to an ellipsis inside a row
	// (0: no minimum besides the layout's own).
	teamNoMinWidth float32 = 0
)

// TeamPane selects which half of the team surface renders. The inspector's pane
// selector owns the choice; the entry point renders one pane per call.
type TeamPane int

const (
	// TeamPaneMembers is the team pane: the lead, teammates, read-only
	// synchronous subagents and the selected object's detail.
	TeamPaneMembers TeamPane = iota
	// TeamPaneTasks is the shared task board.
	TeamPaneTasks
)

// TeamLead is the lead-agent context row: whether the active conversation is
// running and its effective model.
type TeamLead struct {
	// Running reports the active conversation's run state (ChatSession.Running).
	// It is the only source of the lead's badge: the lead has no tracked
	// contracts.RunState and no manager availability.
	Running bool
	// Model is the effective model name; empty shows "未配置模型"/"No model
	// configured".
	Model string
}

// TeamSpawnForm is the persistent state and the actions of the native "new
// teammate" form. The pointers are required only when the form can open.
type TeamSpawnForm struct {
	// Open is the Modal's open flag (the module opens it from its own action).
	Open *bool
	// Name, Prompt and Model are the field buffers (Model is optional).
	Name   *string
	Prompt *string
	Model  *string
	// Error is the last failed spawn's message (inline ErrorLine).
	Error *string
	// Busy marks a spawn in flight: the footer shows a labelled spinner and
	// Create stays disabled.
	Busy *bool
	// OnSubmit is the explicit Create action; it reads the buffers above. The
	// component never submits on Enter (the form has no default action).
	OnSubmit func()
	// OnCancel dismisses the form (Escape, a backdrop click or Cancel). It also
	// cancels an in-flight spawn attempt.
	OnCancel func()
}

// TeamSurfaceOptions is one pane of the team surface: raw contracts data, the
// module's persistent view state (passed by pointer, the immediate-mode
// pattern), and the data actions. Every action callback is optional; a nil one
// leaves its control out or inert (e.g. a host without a TaskAssigner passes
// CanAssign=false and gets a read-only owner line).
type TeamSurfaceOptions struct {
	// Locale localizes the component's built-in copy; nil falls back to the
	// product's Chinese copy.
	Locale contracts.LocaleService
	// Pane selects the team or the task-board half.
	Pane TeamPane

	// Lead is the active conversation's lead context (members pane).
	Lead TeamLead
	// Members are the async teammates (contracts.TeamService.Members), in the
	// backend's order.
	Members []contracts.TeamMember
	// Subagents are the active session's synchronous delegations (read-only).
	Subagents []contracts.SubagentActivity
	// Tasks is the shared board snapshot (tasks pane).
	Tasks []contracts.BoardTask
	// TasksLoading marks an explicit off-thread board reload in flight: the
	// cached rows stay visible with a labelled spinner until the result lands.
	TasksLoading bool
	// CanAssign reports whether the board implements contracts.TaskAssigner.
	// Without it the owner stays a read-only line and no dead menu is drawn.
	CanAssign bool

	// MemberList and MemberSel own the teammate list's native state and chosen
	// row. The component writes the selection through SelectedMemberID.
	MemberList *ui.ListState
	MemberSel  *int
	// SubagentList and SubagentSel own the read-only delegation list.
	SubagentList *ui.ListState
	SubagentSel  *int
	// TaskList and TaskSel own the task list's native state and chosen row.
	TaskList *ui.ListState
	TaskSel  *int

	// SelectedMemberID / SelectedSubagentID are the pane's persistent selection
	// (empty means nothing selected). The component updates them when the user
	// chooses a row; the module also sets them from its commands and toasts.
	SelectedMemberID   *string
	SelectedSubagentID *string
	// ExpandedTask is the task whose detail panel is open below the list; Enter
	// on a task row (or the row menu) toggles it through OnOpenTask.
	ExpandedTask *string
	// TaskDraft is the new-task input buffer.
	TaskDraft *string
	// SteerDraft is the selected teammate's message buffer.
	SteerDraft *string
	// Status is the last successful action's one-line feedback (shown as a
	// status badge in the teammate detail); Err is the last failed action's
	// message (ErrorLine). The module owns the copy.
	Status *string
	Err    *string

	// CloseOpen/CloseID/CloseName are the destructive "close teammate"
	// confirmation state: open flag, member ID and display name. The component
	// clears them after the user cancels or confirms.
	CloseOpen *bool
	CloseID   *string
	CloseName *string
	// TaskDeleteOpen/TaskDeleteID/TaskDeleteSubject are the task deletion
	// confirmation state, same contract as the close confirmation.
	TaskDeleteOpen    *bool
	TaskDeleteID      *string
	TaskDeleteSubject *string

	// Spawn is the "new teammate" form state and its actions.
	Spawn TeamSpawnForm

	// OnOpenSpawn opens the "new teammate" form (the empty-state CTA and the
	// teammate section's action). nil hides the spawn entry points.
	OnOpenSpawn func()
	// OnSend steers the selected teammate with a non-empty message.
	OnSend func(memberID, message string)
	// OnInterrupt stops the selected teammate's running run.
	OnInterrupt func(memberID string)
	// OnClose closes the teammate after the user confirms the dialog. The
	// component has already cleared the confirmation state when this runs.
	OnClose func(memberID string)
	// OnAssign assigns a task's owner through the optional TaskAssigner.
	OnAssign func(taskID, owner string)
	// OnOpenTask toggles one task's detail panel (row Enter, the row menu's
	// "show details" and the detail's Collapse action all call it).
	OnOpenTask func(taskID string)
	// OnTaskCreate adds a task with the trimmed draft subject.
	OnTaskCreate func(subject string)
	// OnTaskStatus moves a task to the given contract status
	// (contracts.TaskStatusInProgress / Pending / Completed).
	OnTaskStatus func(taskID, status string)
	// OnTaskDelete deletes the task after the user confirms the dialog.
	OnTaskDelete func(taskID string)
	// OnRetryTasks reloads the board after a failed load (the inline Retry
	// action); the module clears the error and reloads.
	OnRetryTasks func()
	// Invalidate requests the next frame after a view-state change (list
	// selection, expansion, dialog open/close). The module passes
	// contracts.UIRuntime.Invalidate.
	Invalidate func()
}

// TeamSurface renders one pane of the inspector's team surface. It is a frame
// builder; the module passes its state and actions, and the component owns the
// anatomy.
func TeamSurface(c *ui.Context, o TeamSurfaceOptions) {
	if o.Pane == TeamPaneTasks {
		teamTasksPane(c, o)
		return
	}
	teamMembersPane(c, o)
}

// invalidate requests the next frame when the module provided an invalidator.
func (o *TeamSurfaceOptions) invalidate() {
	if o.Invalidate != nil {
		o.Invalidate()
	}
}

// tr localizes the surface's copy for the current frame.
func (o *TeamSurfaceOptions) tr(zh, en string) string { return T(o.Locale, zh, en) }

// --- local state fallbacks ---------------------------------------------------
// A bare host may omit the persistent list/selection pointers; rendering must
// never panic for that. The fallbacks live for one frame only (no cross-frame
// state), which is documented on the options' fields instead of being hidden.

func (o *TeamSurfaceOptions) memberList() (*ui.ListState, *int) {
	var list *ui.ListState
	var selected *int
	if o.MemberList != nil {
		list = o.MemberList
	} else {
		list = &ui.ListState{}
	}
	if o.MemberSel != nil {
		selected = o.MemberSel
	} else {
		negative := -1
		selected = &negative
	}
	return list, selected
}

func (o *TeamSurfaceOptions) subagentList() (*ui.ListState, *int) {
	var list *ui.ListState
	if o.SubagentList != nil {
		list = o.SubagentList
	} else {
		list = &ui.ListState{}
	}
	var selected *int
	if o.SubagentSel != nil {
		selected = o.SubagentSel
	} else {
		negative := -1
		selected = &negative
	}
	return list, selected
}

func (o *TeamSurfaceOptions) taskList() (*ui.ListState, *int) {
	var list *ui.ListState
	if o.TaskList != nil {
		list = o.TaskList
	} else {
		list = &ui.ListState{}
	}
	var selected *int
	if o.TaskSel != nil {
		selected = o.TaskSel
	} else {
		negative := -1
		selected = &negative
	}
	return list, selected
}

// --- team pane ---------------------------------------------------------------

// teamMembersPane renders the team pane: the lead context row, the teammate
// section, the read-only delegation section, the selected object's detail below
// the lists and the spawn form.
func teamMembersPane(c *ui.Context, o TeamSurfaceOptions) {
	t := design.Of(c)
	if len(o.Members) == 0 && len(o.Subagents) == 0 {
		teamEmptyState(c, t, o)
		teamSpawnDialog(c, t, o)
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
		teamLeadSection(c, t, o)
		if len(o.Members) > 0 {
			teamMemberSection(c, t, o)
		}
		if len(o.Subagents) > 0 {
			teamSubagentSection(c, t, o)
		}
		teamSelectedDetail(c, t, o)
	})
	teamSpawnDialog(c, t, o)
}

// teamEmptyState is the pane's empty state: it explains what the team is and
// offers the native "new teammate" action instead of a dead end.
func teamEmptyState(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	EmptyStateBlock(c,
		o.tr("还没有队友", "No teammates yet"),
		o.tr("团队让主代理把任务交给并行工作的队友：这里能看到谁在做什么、消息、活动和任务。"+
			"也可以让主代理用 spawn_teammate 创建。",
			"Teams let the lead hand work to parallel teammates: see who is doing what, "+
				"their messages, activity and tasks here. You can also let the lead create one with spawn_teammate."),
		func() {
			if o.OnOpenSpawn == nil {
				return
			}
			create := design.Apply(ui.PrimaryButton(c, o.tr("新建队友", "New teammate")), t.PrimaryButton())
			if create.Clicked() {
				o.OnOpenSpawn()
				o.invalidate()
			}
		})
}

// leadBadge maps the active conversation's run state to the lead row's badge:
// the product's one status vocabulary, so the lead's badge has exactly the same
// anatomy as a teammate's (F-1). The conversation is either running now or
// idle; an inactive conversation is never drawn as running.
func leadBadge(o *TeamSurfaceOptions) (string, StatusKind) {
	if o.Lead.Running {
		return o.tr("运行中", "Running"), StatusRunning
	}
	return o.tr("空闲", "Idle"), StatusIdle
}

// teamLeadSection is the "主代理" group: the active conversation's lead, its
// run state and its effective model. It is context, not a list row.
func teamLeadSection(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	label, kind := leadBadge(&o)
	model := strings.TrimSpace(o.Lead.Model)
	if model == "" {
		model = o.tr("未配置模型", "No model configured")
	}
	teamSectionHeading(c, t, o.tr("主代理", "Lead"), nil)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, o.tr("ZeoKeel 主代理", "ZeoKeel lead")), t.Font(design.TypeS)).
			FontWeight(teamHeadingWeight).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		StatusBadge(c, t, kind, label)
	})
	design.Apply(ui.Text(c, model), t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
	ui.Divider(c)
}

// teamMemberSection is the "队友" group: the spawn action at the heading, then
// one native list of teammates.
func teamMemberSection(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	teamSectionHeading(c, t,
		fmt.Sprintf(o.tr("队友 · %d", "Teammates · %d"), len(o.Members)),
		func() {
			// The pane-level spawn entry stays a quiet text button at the
			// section's right edge (the empty state has the prominent CTA).
			if o.OnOpenSpawn == nil {
				return
			}
			create := ui.Button(c, o.tr("新建队友", "New teammate"))
			create = design.Apply(create, t.SecondaryButton()).Height(teamCompactActionHeight)
			if create.Clicked() {
				o.OnOpenSpawn()
				o.invalidate()
			}
		})

	list, selected := o.memberList()
	chosen := -1
	for index, member := range o.Members {
		if member.ID == derefString(o.SelectedMemberID) {
			chosen = index
			break
		}
	}
	*selected = chosen
	list.Key = func(row int) any { return o.Members[row].ID }
	list.Label = func(row int) string { return teamMemberDisplayName(o.Members[row]) }
	list.Selected = selected
	view := ui.List(c, list, len(o.Members)).
		Label(o.tr("队友列表", "Teammates")).
		Gap(design.Space1).
		Rows(func(row ui.ListRow) {
			teamMemberRow(row, t, o, o.Members[row.Index])
		})
	if view.Changed() && *selected >= 0 && *selected < len(o.Members) {
		if o.SelectedMemberID != nil {
			*o.SelectedMemberID = o.Members[*selected].ID
		}
		if o.SelectedSubagentID != nil {
			*o.SelectedSubagentID = ""
		}
		o.clearActionFeedback()
		o.invalidate()
	}
}

// clearActionFeedback drops the previous action's status/error when the
// selection changes: the feedback belongs to the object it was raised for.
func (o *TeamSurfaceOptions) clearActionFeedback() {
	if o.Status != nil {
		*o.Status = ""
	}
	if o.Err != nil {
		*o.Err = ""
	}
}

// teamMemberRow is one teammate row: name with the truthful run badge, the
// current activity and the run meta.
func teamMemberRow(row ui.ListRow, t *design.Tokens, o TeamSurfaceOptions, member contracts.TeamMember) {
	c := row.Context
	name := teamMemberDisplayName(member)
	label, kind := teamTeammateRunBadge(&o, member)
	activity, activityColor := teamTeammateActivityLine(t, &o, member)
	ui.Column(c).FillWidth().Padding(design.Space2, design.Space2).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeS)).FontWeight(teamHeadingWeight).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			StatusBadge(c, t, kind, label)
		})
		design.Apply(ui.Text(c, activity), t.Font(design.TypeXXS)).
			TextColor(activityColor).MaxLines(2).Ellipsis("…")
		design.Apply(ui.Text(c, teamTeammateMeta(&o, member)), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
	})
}

// teamSubagentSection is the read-only "同步子代理" group of the active
// session's blocking delegations.
func teamSubagentSection(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	teamSectionHeading(c, t,
		fmt.Sprintf(o.tr("同步子代理 · %d（只读）", "Subagents · %d (read-only)"), len(o.Subagents)), nil)
	list, selected := o.subagentList()
	chosen := -1
	for index, activity := range o.Subagents {
		if activity.ID == derefString(o.SelectedSubagentID) {
			chosen = index
			break
		}
	}
	*selected = chosen
	list.Key = func(row int) any { return o.Subagents[row].ID }
	list.Label = func(row int) string { return teamSubagentDisplayName(o.Subagents[row]) }
	list.Selected = selected
	view := ui.List(c, list, len(o.Subagents)).
		Label(o.tr("同步子代理列表", "Synchronous subagents")).
		Gap(design.Space1).
		Rows(func(row ui.ListRow) {
			teamSubagentRow(row, t, o, o.Subagents[row.Index])
		})
	if view.Changed() && *selected >= 0 && *selected < len(o.Subagents) {
		if o.SelectedSubagentID != nil {
			*o.SelectedSubagentID = o.Subagents[*selected].ID
		}
		if o.SelectedMemberID != nil {
			*o.SelectedMemberID = ""
		}
		o.clearActionFeedback()
		o.invalidate()
	}
}

// teamSelectedDetail renders the selected object's detail below its list, or
// the quiet hint that explains how to open one.
func teamSelectedDetail(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	if member := selectedTeamMember(o.Members, derefString(o.SelectedMemberID)); member != nil {
		teamTeammateDetail(c, t, o, *member)
		return
	}
	if activity := selectedTeamSubagent(o.Subagents, derefString(o.SelectedSubagentID)); activity != nil {
		teamSubagentDetail(c, t, o, *activity)
		return
	}
	hint := o.tr("选择一个队友以发送消息、中断或查看活动",
		"Select a teammate to steer, interrupt or inspect its activity")
	if len(o.Subagents) > 0 {
		hint = o.tr("选择一行：队友可以发送消息或中断，同步子代理只读",
			"Select a row: teammates can be steered; synchronous subagents are read-only")
	}
	design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXXS)).MaxLines(2)
}

// teamTeammateDetail is the selected teammate's surface: identity, meta, the
// current activity, the latest output with Copy, the steer/interrupt/close
// controls and the bounded activity timeline.
func teamTeammateDetail(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions, member contracts.TeamMember) {
	name := teamMemberDisplayName(member)
	label, kind := teamTeammateRunBadge(&o, member)
	panel := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space3)
	panel.Children(func() {
		// Identity.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(teamHeadingWeight).
				TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
			StatusBadge(c, t, kind, label)
		})
		design.Apply(ui.Text(c, teamTeammateDetailMeta(&o, member)), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(2)
		if member.Error != "" {
			ErrorLine(c, member.Error)
		}

		// Current activity.
		if activity := teamTeammateLatestActivity(member); activity != "" {
			ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
				teamDetailHeading(c, t, o, "活动", "Activity")
				design.Apply(ui.Text(c, activity), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).MaxLines(3).Selectable()
			})
		}

		// Latest output with Copy.
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				teamDetailHeading(c, t, o, "最新产出", "Latest output")
				ui.Spacer(c).Grow(1)
				if output := strings.TrimSpace(member.Last); output != "" {
					copyButton := ui.Button(c, o.tr("复制", "Copy"))
					copyButton = design.Apply(copyButton, t.SecondaryButton()).Height(design.Space6)
					if copyButton.Clicked() {
						c.WriteClipboard(output)
						if o.Status != nil {
							*o.Status = o.tr("已复制产出", "Output copied")
						}
						o.invalidate()
					}
				}
			})
			if output := strings.TrimSpace(member.Last); output != "" {
				// The teammate's answer is Markdown (headings/lists/code): render
				// it as the official Markdown view in a bounded scroll instead of
				// leaking raw markers into the inspector.
				ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 8).Children(func() {
					markdown.View(c, teamTruncateDetail(output), nil)
				})
			} else {
				design.Apply(ui.Text(c, o.tr("还没有最终产出。", "No final output yet.")),
					t.TextMuted(design.TypeXS))
			}
		})

		// Controls: steer always, interrupt only while running, close confirmed.
		if o.SteerDraft != nil {
			ui.TextInput(c, o.SteerDraft).Label(o.tr("发送消息", "Message")).
				Placeholder(o.tr("发给选中队友的消息", "Message to the selected teammate")).
				FillWidth()
			draft := strings.TrimSpace(*o.SteerDraft)
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				send := design.Apply(ui.PrimaryButton(c, o.tr("发送", "Send")), t.PrimaryButton()).Disabled(draft == "")
				if send.Clicked() && draft != "" && o.OnSend != nil {
					o.OnSend(member.ID, draft)
					o.invalidate()
				}
				interrupt := ui.Button(c, o.tr("中断", "Interrupt")).
					Disabled(member.RunState != contracts.TeamRunRunning)
				if design.Apply(interrupt, t.SecondaryButton()).Clicked() && o.OnInterrupt != nil {
					o.OnInterrupt(member.ID)
					o.invalidate()
				}
				if design.Apply(ui.Button(c, o.tr("关闭", "Close")), t.DangerButton()).Clicked() {
					teamOpenCloseConfirmation(&o, member.ID, name)
					o.invalidate()
				}
			})
		}
		if status := derefString(o.Status); status != "" {
			StatusBadge(c, t, StatusSuccess, status)
		}
		if message := derefString(o.Err); message != "" {
			ErrorLine(c, message)
		}

		// Activity timeline (bounded by the backend; newest last).
		if timeline := member.Activity; len(timeline) > 0 {
			teamDetailHeading(c, t, o, "活动时间线", "Activity timeline")
			start := 0
			if len(timeline) > 6 {
				start = len(timeline) - 6
			}
			for _, entry := range timeline[start:] {
				entry := entry
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
					clock := ""
					if !entry.Time.IsZero() {
						clock = FormatClock(o.Locale, entry.Time)
					}
					design.Apply(ui.Text(c, clock), t.Font(design.TypeXXXS)).
						TextColor(t.LabelTertiary).Width(teamTimelineClockWidth)
					textColor := t.LabelSecondary
					if entry.Error {
						textColor = t.DangerText
					}
					design.Apply(ui.Text(c, entry.Text), t.Font(design.TypeXXS)).
						TextColor(textColor).Grow(1).MaxLines(2).Ellipsis("…")
				})
			}
		}
	})
	teamCloseDialog(c, t, o)
}

// teamOpenCloseConfirmation opens the destructive close confirmation for one
// teammate.
func teamOpenCloseConfirmation(o *TeamSurfaceOptions, id, name string) {
	if o.CloseOpen != nil {
		*o.CloseOpen = true
	}
	if o.CloseID != nil {
		*o.CloseID = id
	}
	if o.CloseName != nil {
		*o.CloseName = name
	}
}

// teamCloseDialog is the native confirmation of the destructive "close
// teammate" action: a Modal with no default action, so Enter can never close a
// teammate by accident (the approval dialog's policy).
func teamCloseDialog(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	if o.CloseOpen == nil || !*o.CloseOpen {
		return
	}
	name := teamFirstNonEmpty(derefString(o.CloseName), derefString(o.CloseID))
	confirm := func() {
		id := derefString(o.CloseID)
		teamCloseConfirmationState(&o)
		if o.OnClose != nil {
			o.OnClose(id)
		}
		o.invalidate()
	}
	dismiss := func() {
		teamCloseConfirmationState(&o)
		o.invalidate()
	}
	ui.Modal(c, o.CloseOpen, func() {
		ui.Column(c).Width(design.DialogWidthS).Gap(design.Space3).Padding(design.Space4).Children(func() {
			design.Apply(ui.Text(c, o.tr("关闭队友", "Close teammate")), t.Font(design.TypeM)).
				FontWeight(teamHeadingWeight).TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(2)
			design.Apply(ui.Text(c, o.tr(
				"「"+name+"」将被停止并移除，此操作无法撤销。",
				"\""+name+"\" will be stopped and removed. This cannot be undone.")),
				t.TextMuted(design.TypeXS)).MaxLines(3)
			// The product's one dialog action bar (DESIGN.md §2.3/§6.2):
			// destructive action first (danger, left), the safe Cancel rightmost.
			// The Modal takes the keyboard on its first focusable control, so the
			// safe action is the Default and the danger action never is: a stray
			// Enter cancels, never closes a teammate (the same contract as the
			// product's sidebar danger confirmation, internal/ui/sidebar/
			// danger_confirm.go; AP-12 forbids the destructive action as the
			// default focus).
			DialogFooter(c, []DialogAction{
				{Label: o.tr("确认关闭", "Close teammate"), Danger: true, OnClick: confirm},
				{Label: o.tr("取消", "Cancel"), Default: true, OnClick: dismiss},
			})
		})
	})
	if !*o.CloseOpen {
		teamCloseConfirmationState(&o)
		o.invalidate()
	}
}

// teamCloseConfirmationState clears the close confirmation's persistent state.
func teamCloseConfirmationState(o *TeamSurfaceOptions) {
	if o.CloseOpen != nil {
		*o.CloseOpen = false
	}
	if o.CloseID != nil {
		*o.CloseID = ""
	}
	if o.CloseName != nil {
		*o.CloseName = ""
	}
}

// teamSubagentRow is one synchronous delegation row: the official "子代理"
// marker, the status badge, the model/mode line and the task/output summary.
func teamSubagentRow(row ui.ListRow, t *design.Tokens, o TeamSurfaceOptions, activity contracts.SubagentActivity) {
	c := row.Context
	name := teamSubagentDisplayName(activity)
	ui.Column(c).FillWidth().Padding(design.Space2, design.Space2).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeS)).FontWeight(teamHeadingWeight).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			// The type marker is not a status, but it shares the pane's one
			// tinted badge anatomy (F-1): a plain BgHover badge here used to be
			// the odd one out beside the status badges.
			StatusBadge(c, t, StatusUnknown, o.tr("子代理", "Subagent"))
			// A synchronous delegation reports the tool-status vocabulary
			// (running/done/error/cancelled), so it folds and labels with it.
			StatusBadge(c, t, ParseToolStatus(activity.Status).Kind(),
				ToolStatusLabel(o.Locale, activity.Status))
		})
		design.Apply(ui.Text(c, teamSubagentRowMeta(o.Locale, activity)), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		summary := strings.TrimSpace(activity.Detail)
		if summary == "" {
			summary = strings.TrimSpace(activity.Prompt)
		}
		if summary == "" {
			summary = o.tr("运行中，等待结果…", "Running — waiting for the result…")
		}
		design.Apply(ui.Text(c, summary), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
	})
}

// teamSubagentDetail is the read-only surface of a selected synchronous
// delegation: status, model, mode, timing, task and latest output (Copy) with
// the explicit note that a blocking call cannot be steered or interrupted.
func teamSubagentDetail(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions, activity contracts.SubagentActivity) {
	name := teamSubagentDisplayName(activity)
	design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(teamHeadingWeight).
				TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
			StatusBadge(c, t, StatusUnknown, o.tr("子代理", "Subagent"))
			StatusBadge(c, t, ParseToolStatus(activity.Status).Kind(),
				ToolStatusLabel(o.Locale, activity.Status))
		})
		rows := []KeyValue{{
			Key:   o.tr("模型", "Model"),
			Value: teamFirstNonEmpty(strings.TrimSpace(activity.Model), o.tr("默认", "Default")),
		}}
		if !activity.Started.IsZero() {
			rows = append(rows, KeyValue{
				Key:   o.tr("开始", "Started"),
				Value: FormatDateTime(o.Locale, activity.Started),
			})
		}
		if duration := teamSubagentDuration(activity.Started, activity.Finished); duration != "" {
			rows = append(rows, KeyValue{
				Key: o.tr("耗时", "Duration"), Value: duration,
			})
		}
		rows = append(rows, KeyValue{
			Key:   o.tr("方式", "Mode"),
			Value: teamSubagentModeLabel(o.Locale, activity.Fork),
		})
		KeyValueRows(c, rows)

		if prompt := strings.TrimSpace(activity.Prompt); prompt != "" {
			teamDetailHeading(c, t, o, "任务", "Task")
			design.Apply(ui.Text(c, prompt), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary).Selectable().MaxLines(4)
		}
		detail := strings.TrimSpace(activity.Detail)
		switch strings.TrimSpace(activity.Status) {
		case contracts.SubagentStatusError:
			if detail == "" {
				detail = o.tr("运行失败", "Run failed")
			}
			ErrorLine(c, detail)
		case contracts.SubagentStatusCancelled:
			teamDetailHeading(c, t, o, "最新输出", "Latest output")
			design.Apply(ui.Text(c, o.tr("已取消，没有最终输出。", "Cancelled — no final output.")),
				t.TextMuted(design.TypeXS))
		default:
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				teamDetailHeading(c, t, o, "最新输出", "Latest output")
				ui.Spacer(c).Grow(1)
				if detail != "" {
					copyButton := design.Apply(ui.Button(c, o.tr("复制", "Copy")), t.SecondaryButton()).Height(design.Space6)
					if copyButton.Clicked() {
						c.WriteClipboard(detail)
						if o.Status != nil {
							*o.Status = o.tr("已复制产出", "Output copied")
						}
						o.invalidate()
					}
				}
			})
			if detail == "" {
				design.Apply(ui.Text(c, o.tr("运行中，等待结果…", "Running — waiting for the result…")),
					t.TextMuted(design.TypeXS))
			} else {
				ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 8).Children(func() {
					markdown.View(c, teamTruncateDetail(detail), nil)
				})
			}
		}
		design.Apply(ui.Text(c, o.tr(
			"同步委派只读：它是一次阻塞调用，不能发送消息或中断。",
			"Synchronous delegation is read-only: it is one blocking call, so it cannot be steered or interrupted.")),
			t.TextMuted(design.TypeXXXS)).MaxLines(3)
	})
}

// teamSpawnDialog renders the native "new teammate" form when it is open.
func teamSpawnDialog(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	if o.Spawn.Open == nil || !*o.Spawn.Open {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(design.DialogWidthM).Gap(design.Space3).Padding(design.Space4).Children(func() {
			ui.Column(c).Gap(design.Space1).Children(func() {
				design.Apply(ui.Text(c, o.tr("新建队友", "New teammate")), t.Font(design.TypeM)).
					FontWeight(teamHeadingWeight).TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(2)
				design.Apply(ui.Text(c, o.tr(
					"队友在后台运行；完成或失败时会通知你，并可在团队面板查看活动。",
					"The teammate runs in the background; a toast reports its completion and the team panel keeps its activity.")),
					t.TextMuted(design.TypeXS)).MaxLines(3)
			})
			// A form gives every field its visible label above the control
			// (docs/ui/form.md): a placeholder alone would vanish as soon as
			// the user types.
			ui.Form(c, func() {
				ui.Field(c, o.tr("名称", "Name"), func() {
					ui.TextInput(c, o.Spawn.Name).
						Placeholder(o.tr("例如：研究员", "e.g. Researcher")).
						FillWidth().AutoFocus()
				})
				ui.Field(c, o.tr("任务", "Task"), func() {
					ui.TextArea(c, o.Spawn.Prompt).
						Placeholder(o.tr("描述队友要完成的工作（自包含的任务）", "Describe a self-contained task for the teammate")).
						FillWidth().
						Height(teamSpawnPromptHeight)
				})
				ui.Field(c, o.tr("模型（可选）", "Model (optional)"), func() {
					ui.TextInput(c, o.Spawn.Model).
						Placeholder(o.tr("留空继承当前会话模型", "Empty inherits the session model")).
						FillWidth()
				})
			})
			if message := derefString(o.Spawn.Error); message != "" {
				ErrorLine(c, message)
			}
			if o.Spawn.Busy != nil && *o.Spawn.Busy {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Spinner(c)
					design.Apply(ui.Text(c, o.tr("正在创建队友…", "Creating teammate…")),
						t.TextMuted(design.TypeXS))
				})
			}
			// No default action: the destructive-free form still must not turn a
			// stray Enter into a spawn.
			DialogFooter(c, []DialogAction{
				{Label: o.tr("取消", "Cancel"), OnClick: teamCancelSpawn(&o)},
				{
					Label:    o.tr("创建", "Create"),
					Primary:  true,
					Disabled: teamSpawnDisabled(o),
					OnClick: func() {
						if o.Spawn.OnSubmit != nil {
							o.Spawn.OnSubmit()
						}
						o.invalidate()
					},
				},
			})
		})
	})
	if !open {
		teamCancelSpawn(&o)()
	}
}

// teamCancelSpawn returns the cancel action: it closes the form through the
// module (which also cancels an in-flight spawn attempt).
func teamCancelSpawn(o *TeamSurfaceOptions) func() {
	return func() {
		if o.Spawn.OnCancel != nil {
			o.Spawn.OnCancel()
		}
		o.invalidate()
	}
}

// teamSpawnBusy reports whether a spawn is in flight.
func teamSpawnBusy(o TeamSurfaceOptions) bool {
	return o.Spawn.Busy != nil && *o.Spawn.Busy
}

// teamSpawnDisabled reports whether the form cannot submit yet.
func teamSpawnDisabled(o TeamSurfaceOptions) bool {
	return teamSpawnBusy(o) ||
		strings.TrimSpace(derefString(o.Spawn.Name)) == "" ||
		strings.TrimSpace(derefString(o.Spawn.Prompt)) == ""
}

// teamSectionHeading draws one group heading with an optional right action.
func teamSectionHeading(c *ui.Context, t *design.Tokens, title string, action func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeXXXS)).FontWeight(teamHeadingWeight).
			TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
		if action != nil {
			action()
		}
	})
}

// teamDetailHeading labels one section inside a detail surface.
func teamDetailHeading(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions, zh, en string) {
	design.Apply(ui.Text(c, o.tr(zh, en)), t.Font(design.TypeXXS)).
		FontWeight(teamHeadingWeight).TextColor(t.LabelSecondary)
}

// teamMemberDisplayName returns the member's display name (ID fallback).
func teamMemberDisplayName(member contracts.TeamMember) string {
	return teamFirstNonEmpty(member.Name, member.ID)
}

// teamSubagentDisplayName returns the delegation's display name (ID fallback).
func teamSubagentDisplayName(activity contracts.SubagentActivity) string {
	return teamFirstNonEmpty(activity.Name, activity.ID)
}

// teamTeammateRunBadge maps the tracked run state to the inspector badge: its
// label and its place in the shared status vocabulary. It never draws
// availability "active" as running: availability and run state are different
// facts, so "active" stays unknown and falls back to the availability the
// manager reported.
func teamTeammateRunBadge(o *TeamSurfaceOptions, member contracts.TeamMember) (string, StatusKind) {
	running := ParseRunState(member.RunState)
	if running != RunStateUnknown {
		return RunStateLabel(o.Locale, member.RunState), running.Kind()
	}
	// Unknown run state (an older backend): fall back to availability.
	if strings.EqualFold(strings.TrimSpace(member.Status), "interrupted") {
		return o.tr("已中断", "Interrupted"), StatusInterrupted
	}
	return o.tr("空闲", "Idle"), StatusIdle
}

// teamTeammateActivityLine renders the row's second line: what the teammate is
// doing now (running) or the outcome it reached.
func teamTeammateActivityLine(t *design.Tokens, o *TeamSurfaceOptions, member contracts.TeamMember) (string, ui.Color) {
	switch member.RunState {
	case contracts.TeamRunRunning:
		if activity := teamTeammateLatestActivity(member); activity != "" {
			return activity, t.LabelSecondary
		}
		return o.tr("正在处理…", "Working…"), t.LabelSecondary
	case contracts.TeamRunDone:
		if output := strings.TrimSpace(member.Last); output != "" {
			return o.tr("已完成：", "Done: ") + teamOutputPreview(output, 64), t.LabelTertiary
		}
		return o.tr("已完成", "Done"), t.LabelTertiary
	case contracts.TeamRunFailed:
		if detail := strings.TrimSpace(member.Error); detail != "" {
			return o.tr("失败：", "Failed: ") + teamOutputPreview(detail, 64), t.DangerText
		}
		return o.tr("失败", "Failed"), t.DangerText
	case contracts.TeamRunInterrupted:
		// An interruption is its own neutral state, like a user cancellation:
		// the teammate stays available, so it must not read as a failure.
		return o.tr("运行已中断，等待下一条消息", "Run interrupted — waiting for a message"),
			StatusColor(t, StatusInterrupted)
	default:
		return o.tr("空闲，等待消息", "Idle, waiting for a message"), t.LabelTertiary
	}
}

// teamTeammateLatestActivity returns the newest activity text of one teammate.
func teamTeammateLatestActivity(member contracts.TeamMember) string {
	if len(member.Activity) == 0 {
		return ""
	}
	return strings.TrimSpace(member.Activity[len(member.Activity)-1].Text)
}

// teamTeammateMeta is the row's third line: identity, elapsed, steps and tokens.
func teamTeammateMeta(o *TeamSurfaceOptions, member contracts.TeamMember) string {
	parts := make([]string, 0, 4)
	if id := strings.TrimSpace(member.ID); id != "" {
		parts = append(parts, id)
	}
	if elapsed := teamRunElapsed(member, time.Now()); elapsed != "" {
		parts = append(parts, elapsed)
	}
	if member.Iterations > 0 {
		parts = append(parts, fmt.Sprintf(o.tr("%d 步", "%d steps"), member.Iterations))
	}
	if member.UsageTokens > 0 {
		parts = append(parts, FormatCompactCount(member.UsageTokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}

// teamTeammateDetailMeta is the detail's meta line: model, elapsed, steps,
// tokens.
func teamTeammateDetailMeta(o *TeamSurfaceOptions, member contracts.TeamMember) string {
	parts := make([]string, 0, 5)
	if model := strings.TrimSpace(member.Model); model != "" {
		parts = append(parts, model)
	}
	if elapsed := teamRunElapsed(member, time.Now()); elapsed != "" {
		parts = append(parts, o.tr("耗时 ", "elapsed ")+elapsed)
	}
	if member.Iterations > 0 {
		parts = append(parts, fmt.Sprintf(o.tr("%d 步", "%d steps"), member.Iterations))
	}
	if member.UsageTokens > 0 {
		parts = append(parts, FormatCompactCount(member.UsageTokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}

// selectedTeamMember returns the member with id, or nil while nothing is
// selected (or the selected teammate disappeared).
func selectedTeamMember(members []contracts.TeamMember, id string) *contracts.TeamMember {
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

// selectedTeamSubagent returns the delegation with id, or nil while nothing is
// selected (or the selected row was pruned or belongs to another session).
func selectedTeamSubagent(activities []contracts.SubagentActivity, id string) *contracts.SubagentActivity {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	for _, activity := range activities {
		if activity.ID == id {
			activity := activity
			return &activity
		}
	}
	return nil
}

// teamSubagentRowMeta is the secondary line of a delegation row: the child
// model, the mode (fork/fresh) and the start clock.
func teamSubagentRowMeta(locale contracts.LocaleService, activity contracts.SubagentActivity) string {
	parts := make([]string, 0, 3)
	if model := strings.TrimSpace(activity.Model); model != "" {
		parts = append(parts, model)
	}
	parts = append(parts, teamSubagentModeLabel(locale, activity.Fork))
	if !activity.Started.IsZero() {
		parts = append(parts, FormatClock(locale, activity.Started))
	}
	return strings.Join(parts, " · ")
}

// teamSubagentModeLabel names how the child got its context.
func teamSubagentModeLabel(locale contracts.LocaleService, fork bool) string {
	if fork {
		return T(locale, "分叉上下文", "Forked context")
	}
	return T(locale, "全新上下文", "Fresh context")
}

// teamSubagentDuration renders the wall time of a finished delegation, "" while
// it is still running.
func teamSubagentDuration(started, finished time.Time) string {
	if started.IsZero() || finished.IsZero() || finished.Before(started) {
		return ""
	}
	return finished.Sub(started).Round(100 * time.Millisecond).String()
}

// teamTruncateDetail bounds the rendered detail text (UTF-8 safe). The registry
// already bounds its stored detail; a scripted lister may not.
func teamTruncateDetail(detail string) string {
	const max = 1200
	if len(detail) <= max {
		return detail
	}
	cut := max
	for cut > 0 && (detail[cut]&0xC0) == 0x80 {
		cut--
	}
	return detail[:cut] + "…"
}

// teamRunElapsed renders the wall time of the current or latest run in a compact
// desktop form: "42s", "4m12s", "1h04m". A run that is still in flight measures
// against now.
func teamRunElapsed(member contracts.TeamMember, now time.Time) string {
	started := member.Started
	if started.IsZero() {
		// Backends that do not track the run start fall back to the creation
		// time, so the row still answers "how long has this been alive".
		started = member.Created
	}
	if started.IsZero() {
		return ""
	}
	end := member.Finished
	if end.IsZero() || member.RunState == contracts.TeamRunRunning {
		end = now
	}
	duration := end.Sub(started)
	if duration < 0 {
		duration = 0
	}
	round := duration.Round(time.Second)
	if round < time.Minute {
		return round.String()
	}
	if round < time.Hour {
		minutes := int(round / time.Minute)
		seconds := int(round/time.Second) % 60
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := int(round / time.Hour)
	minutes := int(round/time.Minute) % 60
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

// teamOutputPreview turns one teammate answer (or error) into a one-line row
// preview: it drops code fences and their content (a final answer often ends in
// a fenced path dump), strips leading markdown markers (#, >, list bullets),
// removes inline backticks and collapses every run of whitespace, then bounds
// the result to limit runes. The completion toast in the rightpanel module
// applies the same cleaning to its copy; this surface cannot import a module,
// so the read-only renderer keeps its own copy of the rule.
func teamOutputPreview(text string, limit int) string {
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			// Drop the fence line and everything inside the fenced block: a
			// fenced path/code dump is noise in a one-line preview.
			inFence = !inFence
			continue
		}
		if inFence || trimmed == "" {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>")
		trimmed = strings.TrimSpace(trimmed)
		if bullet, ok := teamTrimMarkdownBullet(trimmed); ok {
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

// teamTrimMarkdownBullet strips one leading list marker ("- ", "* ", "+ ",
// "1. ", "2) ") from line, reporting whether it found one.
func teamTrimMarkdownBullet(line string) (string, bool) {
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

// --- task board pane ---------------------------------------------------------

// teamTaskRow is one flattened row of the task list: a status section header or
// one board task.
type teamTaskRow struct {
	header bool
	status string
	task   contracts.BoardTask
}

// teamTasksPane renders the task board: the add row, the error/retry row, the
// grouped native list with counts and the selected task's detail below it, and
// the delete confirmation.
func teamTasksPane(c *ui.Context, o TeamSurfaceOptions) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		teamTaskAddRow(c, t, o)
		if message := derefString(o.Err); message != "" {
			ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
				ErrorLine(c, message)
				retry := design.Apply(ui.Button(c, o.tr("重试", "Retry")), t.SecondaryButton()).Height(teamCompactActionHeight)
				if retry.Clicked() && o.OnRetryTasks != nil {
					o.OnRetryTasks()
					o.invalidate()
				}
			})
		}
		if len(o.Tasks) == 0 {
			EmptyStateBlock(c, o.tr("还没有任务", "No tasks yet"), "", nil)
			design.Apply(ui.Text(c, o.tr(
				"任务板由主代理与队友共享；主代理可以用 team_task_create 添加任务。",
				"The task board is shared with the lead and the teammates; the lead can add tasks with team_task_create.")),
				t.TextMuted(design.TypeXXXS)).MaxLines(3)
			return
		}
		teamTaskCountLine(c, t, o)
		teamTaskList(c, t, o)
		teamExpandedTaskDetail(c, t, o)
	})
	teamTaskDeleteDialog(c, t, o)
}

// teamTaskCountLine is the board summary: total and per-status counts.
func teamTaskCountLine(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	counts := teamTaskStatusCounts(o.Tasks)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, fmt.Sprintf(
			o.tr("%d 个任务 · 进行中 %d · 待处理 %d · 已完成 %d",
				"%d tasks · %d in progress · %d pending · %d done"),
			len(o.Tasks), counts[contracts.TaskStatusInProgress],
			counts[contracts.TaskStatusPending], counts[contracts.TaskStatusCompleted])),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1).MaxLines(2).Ellipsis("…")
		if o.TasksLoading {
			ui.Spinner(c)
			design.Apply(ui.Text(c, o.tr("刷新中…", "Refreshing…")), t.TextMuted(design.TypeXXXS))
		}
	})
}

// teamTaskList renders the flattened group-header + task rows in the official
// native list. Up/Down choose a row, Enter expands/collapses the task detail.
func teamTaskList(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	rows := teamFlattenBoardTasks(o.Tasks)
	list, selected := o.taskList()
	if *selected >= len(rows) {
		*selected = len(rows) - 1
	}
	if *selected < 0 {
		*selected = -1
	}
	list.Key = func(row int) any {
		if rows[row].header {
			return "header:" + rows[row].status
		}
		return rows[row].task.ID
	}
	list.Label = func(row int) string {
		if rows[row].header {
			return ""
		}
		return teamFirstNonEmpty(rows[row].task.Subject, rows[row].task.ID)
	}
	list.Header = func(row int) bool { return rows[row].header }
	list.Selected = selected
	view := ui.List(c, list, len(rows)).
		Label(o.tr("任务列表", "Tasks")).
		Gap(design.Space1).
		Rows(func(row ui.ListRow) {
			entry := rows[row.Index]
			if entry.header {
				teamTaskGroupHeader(row, t, o, entry.status)
				return
			}
			teamTaskRowView(row, t, o, entry.task)
		})
	if view.Submitted() && *selected >= 0 && *selected < len(rows) && !rows[*selected].header {
		if o.OnOpenTask != nil {
			o.OnOpenTask(rows[*selected].task.ID)
		}
		o.invalidate()
	}
}

// teamFlattenBoardTasks flattens the board into section headers + task rows, in
// the action-first order in progress → pending → completed, created ascending
// inside a section.
func teamFlattenBoardTasks(tasks []contracts.BoardTask) []teamTaskRow {
	groups := []string{
		contracts.TaskStatusInProgress,
		contracts.TaskStatusPending,
		contracts.TaskStatusCompleted,
	}
	rows := make([]teamTaskRow, 0, len(tasks)+len(groups))
	for _, status := range groups {
		group := make([]contracts.BoardTask, 0, 4)
		for _, task := range tasks {
			if teamNormalizeTaskStatus(task.Status) == status {
				group = append(group, task)
			}
		}
		if len(group) == 0 {
			continue
		}
		sort.SliceStable(group, func(left, right int) bool {
			if !group[left].Created.Equal(group[right].Created) {
				return group[left].Created.Before(group[right].Created)
			}
			return group[left].ID < group[right].ID
		})
		rows = append(rows, teamTaskRow{header: true, status: status})
		for _, task := range group {
			rows = append(rows, teamTaskRow{task: task})
		}
	}
	// Unknown statuses (a future backend) stay visible in a trailing section so
	// the pane never silently drops a task.
	unknown := make([]contracts.BoardTask, 0)
	for _, task := range tasks {
		switch teamNormalizeTaskStatus(task.Status) {
		case contracts.TaskStatusInProgress, contracts.TaskStatusPending, contracts.TaskStatusCompleted:
		default:
			unknown = append(unknown, task)
		}
	}
	if len(unknown) > 0 {
		rows = append(rows, teamTaskRow{header: true, status: "unknown"})
		for _, task := range unknown {
			rows = append(rows, teamTaskRow{task: task})
		}
	}
	return rows
}

// teamTaskGroupHeader is one status section heading with its count. It is a
// list header row: it stays pinned while the section scrolls under it.
func teamTaskGroupHeader(row ui.ListRow, t *design.Tokens, o TeamSurfaceOptions, status string) {
	c := row.Context
	label := TaskStatusLabel(o.Locale, status)
	if status == "unknown" {
		label = o.tr("其他", "Other")
	}
	// The trailing section has no contract status: count every task whose
	// normalized status is none of the board's three known ones (a future
	// backend's word stays visible and its header stays truthful).
	count := 0
	for _, task := range o.Tasks {
		normalized := teamNormalizeTaskStatus(task.Status)
		if status == "unknown" {
			switch normalized {
			case contracts.TaskStatusInProgress, contracts.TaskStatusPending, contracts.TaskStatusCompleted:
			default:
				count++
			}
			continue
		}
		if normalized == status {
			count++
		}
	}
	design.Apply(ui.Text(c, fmt.Sprintf("%s · %d", label, count)), t.Font(design.TypeXXXS)).
		FontWeight(teamHeadingWeight).TextColor(t.LabelTertiary).
		FillWidth().Background(t.BgBase).PaddingY(design.Space2)
}

// teamTaskRowView is one task row of the native list: subject, status badge,
// owner control and the row's overflow menu, the dependency/blocker line and
// (while expanded) the task detail with the inline transitions.
func teamTaskRowView(row ui.ListRow, t *design.Tokens, o TeamSurfaceOptions, task contracts.BoardTask) {
	c := row.Context
	subject := teamFirstNonEmpty(task.Subject, task.ID)
	// The full task menu is shared by the ⋯ button and a right-click on the row
	// (mygo context-menu.md: one build function fills both menus).
	taskMenu := func(m *ui.Menu) {
		status := teamNormalizeTaskStatus(task.Status)
		if status == contracts.TaskStatusPending {
			if m.Item(o.tr("开始", "Start")).Chosen() {
				teamUpdateTaskStatus(o, task.ID, contracts.TaskStatusInProgress)
			}
		}
		if status != contracts.TaskStatusCompleted {
			if m.Item(o.tr("完成", "Complete")).Chosen() {
				teamUpdateTaskStatus(o, task.ID, contracts.TaskStatusCompleted)
			}
		}
		if status == contracts.TaskStatusCompleted {
			if m.Item(o.tr("重新打开", "Reopen")).Chosen() {
				teamUpdateTaskStatus(o, task.ID, contracts.TaskStatusPending)
			}
		}
		m.Separator()
		if m.Item(o.tr("展开详情", "Show details")).Chosen() &&
			derefString(o.ExpandedTask) != task.ID && o.OnOpenTask != nil {
			o.OnOpenTask(task.ID)
			o.invalidate()
		}
		if m.Item(o.tr("删除", "Delete")).Chosen() {
			teamOpenTaskDelete(&o, task.ID, subject)
			o.invalidate()
		}
	}
	element := ui.Column(c).FillWidth().PaddingX(design.Space2).Gap(design.Space1)
	element.ContextMenu(taskMenu)
	element.Children(func() {
		// First line keeps the subject: at the 320 DIP inspector width the owner
		// button and the ⋯ control used to squeeze the subject to one visible
		// character. Status badge + subject + actions, then a secondary line with
		// the owner and the dependency/readiness state.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			StatusBadge(c, t, ParseTaskStatus(task.Status).Kind(),
				TaskStatusLabel(o.Locale, task.Status))
			design.Apply(ui.Text(c, subject), t.Font(design.TypeS)).FontWeight(teamHeadingWeight).
				TextColor(t.LabelPrimary).Grow(1).MinWidth(teamNoMinWidth).MaxLines(1).Ellipsis("…")
			menuLabel := o.tr("任务操作：", "Task actions: ") + subject
			more := IconButton(c, IconButtonOptions{
				Key:     "uikit.team.task.more",
				Label:   menuLabel,
				Tooltip: menuLabel,
				Icon:    icons.More(),
				Size:    design.IconSizeS,
			})
			more.PopoverMenuButton(taskMenu)
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			teamTaskOwnerControl(c, t, o, task)
			// The dependency line is suppressed while the detail panel is open
			// below the list (the panel repeats it readably; on the accent
			// selection background the warning copy is hard to read).
			if derefString(o.ExpandedTask) != task.ID {
				if label, color := teamTaskDependencyStatus(o, t, task); label != "" {
					design.Apply(ui.Text(c, label), t.Font(design.TypeXXXS)).
						TextColor(color).Grow(1).MinWidth(teamNoMinWidth).MaxLines(1).Ellipsis("…")
				}
			}
		})
		ui.Divider(c)
	})
}

// teamTaskOwnerControl is the row's owner: the optional assigner turns it into
// a menu (未认领 / 主代理 / each live teammate); without it the owner stays a
// read-only line.
func teamTaskOwnerControl(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions, task contracts.BoardTask) {
	label, color := teamTaskOwnerDisplay(t, &o, task.Owner)
	if !o.CanAssign || o.OnAssign == nil {
		design.Apply(ui.Text(c, o.tr("负责人：", "Owner: ")+label), t.TextMuted(design.TypeXXS)).MaxLines(1)
		return
	}
	menu := func(m *ui.Menu) {
		owner := strings.TrimSpace(task.Owner)
		if m.Item(o.tr("未认领", "Unclaimed")).Checked(owner == "").Chosen() {
			o.OnAssign(task.ID, "")
			o.invalidate()
		}
		if m.Item(o.tr("主代理", "Lead")).Checked(owner == contracts.TaskOwnerUnassigned).Chosen() {
			o.OnAssign(task.ID, contracts.TaskOwnerUnassigned)
			o.invalidate()
		}
		for _, member := range o.Members {
			member := member
			name := teamFirstNonEmpty(member.Name, member.ID)
			if m.Item(name).Checked(owner == name).Chosen() {
				o.OnAssign(task.ID, name)
				o.invalidate()
			}
		}
	}
	button := ui.PopoverMenuButton(c, o.tr("负责人：", "Owner: ")+label, menu)
	button = design.Apply(button, t.SecondaryButton()).Height(design.Space6)
	button.TextColor(color)
	button.Tooltip(o.tr("负责人：", "Owner: ") + label)
}

// teamUpdateTaskStatus drives one row-menu status transition.
func teamUpdateTaskStatus(o TeamSurfaceOptions, id, status string) {
	if o.OnTaskStatus != nil {
		o.OnTaskStatus(id, status)
	}
	o.invalidate()
}

// teamTaskDependencyStatus is the row's secondary-line dependency text: the
// blocked/waiting state for a task with dependencies, or 可开始 for a ready
// pending task. It replaces the old invisible dependency: the rig board always
// carried BlockedBy, the product contract used to drop it.
func teamTaskDependencyStatus(o TeamSurfaceOptions, t *design.Tokens, task contracts.BoardTask) (string, ui.Color) {
	if len(task.BlockedBy) == 0 {
		if teamNormalizeTaskStatus(task.Status) == contracts.TaskStatusPending {
			return o.tr("可开始", "Ready"), t.LabelTertiary
		}
		return "", t.LabelTertiary
	}
	waiting := teamWaitingDependencies(o.Tasks, task)
	if len(waiting) == 0 {
		return o.tr("依赖已完成 · 可开始", "Dependencies done · ready"), t.LabelTertiary
	}
	labels := make([]string, 0, len(waiting))
	for _, dependency := range waiting {
		labels = append(labels, teamDependencyLabel(o, dependency))
	}
	return T(o.Locale,
		"等待 "+strings.Join(labels, "、")+"完成",
		"Waiting for "+strings.Join(labels, ", ")), t.WarningText
}

// teamTaskDetail is the expanded task detail: description, dependency states,
// write scopes, the run meta and the inline transitions.
func teamTaskDetail(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions, task contracts.BoardTask) {
	ui.Column(c).FillWidth().Gap(design.Space1).PaddingY(design.Space1).Children(func() {
		if description := strings.TrimSpace(task.Description); description != "" {
			design.Apply(ui.Text(c, description), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).Selectable().MaxLines(4)
		}
		if len(task.BlockedBy) > 0 {
			for _, id := range task.BlockedBy {
				state := o.tr("未知任务", "unknown task")
				status := t.LabelTertiary
				for _, candidate := range o.Tasks {
					if candidate.ID == id {
						state = fmt.Sprintf("%s「%s」· %s", id,
							teamFirstNonEmpty(candidate.Subject, candidate.ID),
							TaskStatusLabel(o.Locale, candidate.Status))
						if teamNormalizeTaskStatus(candidate.Status) != contracts.TaskStatusCompleted {
							status = t.WarningText
						}
						break
					}
				}
				design.Apply(ui.Text(c, o.tr("依赖：", "Depends on: ")+state), t.Font(design.TypeXXXS)).
					TextColor(status).MaxLines(2).Ellipsis("…")
			}
		}
		if len(task.WriteScopes) > 0 {
			design.Apply(ui.Text(c, o.tr("写入范围：", "Write scopes: ")+strings.Join(task.WriteScopes, ", ")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
		}
		design.Apply(ui.Text(c, fmt.Sprintf(o.tr("%s · 更新于 %s", "%s · updated %s"), task.ID,
			FormatDateTime(o.Locale, task.Updated))), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).Selectable()
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			switch teamNormalizeTaskStatus(task.Status) {
			case contracts.TaskStatusPending:
				if start := design.Apply(ui.Button(c, o.tr("开始任务", "Start task")), t.SecondaryButton()).Height(teamCompactActionHeight); start.Clicked() {
					teamUpdateTaskStatus(o, task.ID, contracts.TaskStatusInProgress)
				}
			case contracts.TaskStatusInProgress:
				if done := design.Apply(ui.Button(c, o.tr("完成任务", "Complete task")), t.SecondaryButton()).Height(teamCompactActionHeight); done.Clicked() {
					teamUpdateTaskStatus(o, task.ID, contracts.TaskStatusCompleted)
				}
			case contracts.TaskStatusCompleted:
				if reopen := design.Apply(ui.Button(c, o.tr("重新打开", "Reopen")), t.SecondaryButton()).Height(teamCompactActionHeight); reopen.Clicked() {
					teamUpdateTaskStatus(o, task.ID, contracts.TaskStatusPending)
				}
			}
			ui.Spacer(c).Grow(1)
			if remove := design.Apply(ui.Button(c, o.tr("删除", "Delete")), t.DangerButton()).Height(teamCompactActionHeight); remove.Clicked() {
				teamOpenTaskDelete(&o, task.ID, teamFirstNonEmpty(task.Subject, task.ID))
				o.invalidate()
			}
		})
	})
}

// teamTaskAddRow is the native add row: one subject input and a primary Add
// button that is disabled while the draft is empty.
func teamTaskAddRow(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	if o.TaskDraft == nil {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.TextInput(c, o.TaskDraft).
			Label(o.tr("新任务", "New task")).
			Placeholder(o.tr("输入任务标题", "Task subject")).
			FillWidth()
		subject := strings.TrimSpace(*o.TaskDraft)
		add := design.Apply(ui.PrimaryButton(c, o.tr("添加", "Add")), t.PrimaryButton()).
			Disabled(subject == "")
		if add.Clicked() && subject != "" {
			if o.OnTaskCreate != nil {
				o.OnTaskCreate(subject)
			}
			o.invalidate()
		}
	})
}

// teamOpenTaskDelete opens the destructive delete confirmation for one task.
func teamOpenTaskDelete(o *TeamSurfaceOptions, id, subject string) {
	if o.TaskDeleteOpen != nil {
		*o.TaskDeleteOpen = true
	}
	if o.TaskDeleteID != nil {
		*o.TaskDeleteID = id
	}
	if o.TaskDeleteSubject != nil {
		*o.TaskDeleteSubject = subject
	}
}

// teamTaskDeleteDialog is the native confirmation of the row menu's Delete. It
// is a Modal with no default action: Enter can never delete a task by accident
// (the approval dialog's no-default policy), unlike the AlertDialog whose last
// button is the platform default.
func teamTaskDeleteDialog(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	if o.TaskDeleteOpen == nil || !*o.TaskDeleteOpen {
		return
	}
	subject := teamFirstNonEmpty(derefString(o.TaskDeleteSubject), derefString(o.TaskDeleteID))
	confirm := func() {
		id := derefString(o.TaskDeleteID)
		teamTaskDeleteState(&o)
		if o.OnTaskDelete != nil {
			o.OnTaskDelete(id)
		}
		o.invalidate()
	}
	dismiss := func() {
		teamTaskDeleteState(&o)
		o.invalidate()
	}
	ui.Modal(c, o.TaskDeleteOpen, func() {
		ui.Column(c).Width(design.DialogWidthS).Gap(design.Space3).Padding(design.Space4).Children(func() {
			design.Apply(ui.Text(c, o.tr("删除任务", "Delete task")), t.Font(design.TypeM)).
				FontWeight(teamHeadingWeight).TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(2)
			design.Apply(ui.Text(c, o.tr(
				"「"+subject+"」将被删除，此操作无法撤销。",
				"\""+subject+"\" will be deleted. This cannot be undone.")),
				t.TextMuted(design.TypeXS)).MaxLines(3)
			// The product's one dialog action bar (DESIGN.md §2.3/§6.2):
			// destructive action first (danger, left), the safe Cancel rightmost.
			// The Modal takes the keyboard on its first focusable control, so the
			// safe action is the Default and the danger action never is: a stray
			// Enter cancels, never deletes a task (the same contract as the
			// product's sidebar danger confirmation; AP-12 forbids the
			// destructive action as the default focus).
			DialogFooter(c, []DialogAction{
				{Label: o.tr("删除", "Delete"), Danger: true, OnClick: confirm},
				{Label: o.tr("取消", "Cancel"), Default: true, OnClick: dismiss},
			})
		})
	})
	if !*o.TaskDeleteOpen {
		teamTaskDeleteState(&o)
		o.invalidate()
	}
}

// teamTaskDeleteState clears the delete confirmation's persistent state.
func teamTaskDeleteState(o *TeamSurfaceOptions) {
	if o.TaskDeleteOpen != nil {
		*o.TaskDeleteOpen = false
	}
	if o.TaskDeleteID != nil {
		*o.TaskDeleteID = ""
	}
	if o.TaskDeleteSubject != nil {
		*o.TaskDeleteSubject = ""
	}
}

// teamExpandedTaskDetail renders the selected task's detail below the list (the
// native List selection stays readable; putting the detail inside the chosen row
// would paint it on the accent selection background).
func teamExpandedTaskDetail(c *ui.Context, t *design.Tokens, o TeamSurfaceOptions) {
	if o.ExpandedTask == nil {
		return
	}
	if strings.TrimSpace(*o.ExpandedTask) == "" {
		return
	}
	var task contracts.BoardTask
	found := false
	for _, candidate := range o.Tasks {
		if candidate.ID == *o.ExpandedTask {
			task, found = candidate, true
			break
		}
	}
	if !found {
		*o.ExpandedTask = ""
		return
	}
	design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, teamFirstNonEmpty(task.Subject, task.ID)), t.Font(design.TypeS)).
				FontWeight(teamHeadingWeight).TextColor(t.LabelPrimary).Grow(1).MaxLines(2).Ellipsis("…")
			StatusBadge(c, t, ParseTaskStatus(task.Status).Kind(),
				TaskStatusLabel(o.Locale, task.Status))
			if collapse := design.Apply(ui.Button(c, o.tr("收起", "Collapse")), t.SecondaryButton()).Height(teamCompactActionHeight); collapse.Clicked() {
				if o.OnOpenTask != nil {
					o.OnOpenTask(task.ID)
				}
				o.invalidate()
			}
		})
		teamTaskDetail(c, t, o, task)
	})
}

// teamTaskOwnerDisplay maps the board owner to its row label: the empty owner is
// 未认领 and the board API's pseudo owner is the lead agent.
func teamTaskOwnerDisplay(t *design.Tokens, o *TeamSurfaceOptions, owner string) (string, ui.Color) {
	owner = strings.TrimSpace(owner)
	switch owner {
	case "":
		return o.tr("未认领", "Unclaimed"), t.LabelTertiary
	case contracts.TaskOwnerUnassigned:
		return o.tr("主代理", "Lead"), t.LabelSecondary
	default:
		return owner, t.LabelSecondary
	}
}

// teamTaskStatusCounts counts the board's tasks per normalized status.
func teamTaskStatusCounts(tasks []contracts.BoardTask) map[string]int {
	counts := make(map[string]int, 3)
	for _, task := range tasks {
		counts[teamNormalizeTaskStatus(task.Status)]++
	}
	return counts
}

// teamWaitingDependencies returns the dependencies that are not completed yet.
func teamWaitingDependencies(tasks []contracts.BoardTask, task contracts.BoardTask) []contracts.BoardTask {
	byID := make(map[string]contracts.BoardTask, len(tasks))
	for _, candidate := range tasks {
		byID[candidate.ID] = candidate
	}
	waiting := make([]contracts.BoardTask, 0, len(task.BlockedBy))
	for _, id := range task.BlockedBy {
		dependency, ok := byID[id]
		if !ok {
			waiting = append(waiting, contracts.BoardTask{ID: id})
			continue
		}
		if teamNormalizeTaskStatus(dependency.Status) != contracts.TaskStatusCompleted {
			waiting = append(waiting, dependency)
		}
	}
	return waiting
}

// teamDependencyLabel renders one waiting dependency for the row line.
func teamDependencyLabel(o TeamSurfaceOptions, dependency contracts.BoardTask) string {
	if strings.TrimSpace(dependency.Subject) == "" {
		return o.tr("任务 ", "task ") + dependency.ID
	}
	return fmt.Sprintf("%s「%s」", dependency.ID, dependency.Subject)
}

// teamNormalizeTaskStatus folds common status spellings onto the contract values
// for the board's grouping, counts and transitions. The badge label and color
// come from the uikit status vocabulary; this stays data-only so the grouping
// keys are the contract strings the board API itself returns.
func teamNormalizeTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "todo", "open", "waiting":
		return contracts.TaskStatusPending
	case "in_progress", "in-progress", "running", "active", "doing", "started":
		return contracts.TaskStatusInProgress
	case "completed", "complete", "done", "finished":
		return contracts.TaskStatusCompleted
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

// --- small shared helpers ----------------------------------------------------

// teamFirstNonEmpty returns the first non-empty string.
func teamFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// derefString reads an optional string pointer ("" when nil).
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
