package settings

// Behavior page: the default agent permission mode (daily), the transcript's
// work-details presentation (DSH-aligned), and the page's lower-frequency
// conversation options behind one collapsed disclosure. Every control applies
// immediately (the page has no save button): the permission menu writes the
// shared agent.permission row the approvals gate reads dynamically, the work
// details segmented controls write ui.workSteps.*, the switches persist on change
// and the workspace instruction file saves as it is edited.

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// BehaviorPageID is the behavior page identity.
	BehaviorPageID = "behavior"
	// BehaviorPageSection places it in the general group.
	BehaviorPageSection = "常用"
	// BehaviorPageOrder makes this the first page of the general group and thus
	// the page the settings surface opens on (the host shows the group's lowest
	// Order as the default; About moved to the last group).
	BehaviorPageOrder = 20
)

// behaviorPermission is one default-permission choice.
type behaviorPermission struct {
	value     string
	zh        string
	en        string
	meaningZh string
	meaningEn string
}

// behaviorPermissions lists the three choices in menu order: 只读 / 工作区可写 /
// 所有权限 (DSH's read-only / workspace-write / danger-full-access presets).
var behaviorPermissions = []behaviorPermission{
	{
		value: contracts.PermissionReadOnly, zh: "只读", en: "Read-only",
		meaningZh: "只自动允许只读与信息类工具，其他操作会询问",
		meaningEn: "Auto-allow read-only and informational tools; ask for the rest",
	},
	{
		value: contracts.PermissionWorkspace, zh: "工作区可写", en: "Workspace-write",
		meaningZh: "工作区内的文件读写自动允许，其他操作询问",
		meaningEn: "Auto-allow workspace file reads and writes; ask for the rest",
	},
	{
		value: contracts.PermissionFull, zh: "所有权限", en: "All permissions",
		meaningZh: "全部自动允许，不再询问", meaningEn: "Allow everything without asking",
	},
}

// workStepsModeChoice is one work-details presentation level (DSH-aligned).
type workStepsModeChoice struct {
	value     string
	zh        string
	en        string
	meaningZh string
	meaningEn string
}

// workStepsModeChoices lists the four display levels in segmented order:
// compact / standard (default) / detailed / verbose.
var workStepsModeChoices = []workStepsModeChoice{
	{
		value: contracts.WorkStepsCompact, zh: "简洁", en: "Compact",
		meaningZh: "一行状态，不带工具细节",
		meaningEn: "One status line, no tool details",
	},
	{
		value: contracts.WorkStepsStandard, zh: "标准", en: "Standard",
		meaningZh: "一行状态 + 实时细节，回答结束后折叠",
		meaningEn: "One status line + live details, folded after the answer",
	},
	{
		value: contracts.WorkStepsDetailed, zh: "详细", en: "Detailed",
		meaningZh: "运行中逐步显示工具调用",
		meaningEn: "Tool calls shown step by step while running",
	},
	{
		value: contracts.WorkStepsVerbose, zh: "完全展开", en: "Verbose",
		meaningZh: "不折叠不分组，全部展开",
		meaningEn: "Everything expanded, never folded or grouped",
	},
}

// workStepsCollapseChoice is one work-details collapse timing.
type workStepsCollapseChoice struct {
	value string
	zh    string
	en    string
}

// workStepsCollapseChoices lists the two collapse timings in segmented order:
// completion (default) / next-input.
var workStepsCollapseChoices = []workStepsCollapseChoice{
	{
		value: contracts.WorkStepsCollapseCompletion, zh: "回答结束后", en: "After the answer",
	},
	{
		value: contracts.WorkStepsCollapseNextInput, zh: "下次有新消息时", en: "On the next message",
	},
}

// workStepsModeIndex maps a normalized mode to its segmented index.
func workStepsModeIndex(mode string) int {
	for index, choice := range workStepsModeChoices {
		if choice.value == mode {
			return index
		}
	}
	return 1 // standard
}

// workStepsModeValue maps a segmented index back to the stored mode.
func workStepsModeValue(index int) string {
	if index < 0 || index >= len(workStepsModeChoices) {
		return contracts.WorkStepsStandard
	}
	return workStepsModeChoices[index].value
}

// workStepsCollapseIndex maps a normalized collapse timing to its segmented
// index.
func workStepsCollapseIndex(value string) int {
	for index, choice := range workStepsCollapseChoices {
		if choice.value == value {
			return index
		}
	}
	return 0 // completion
}

// workStepsCollapseValue maps a segmented index back to the stored timing.
func workStepsCollapseValue(index int) string {
	if index < 0 || index >= len(workStepsCollapseChoices) {
		return contracts.WorkStepsCollapseCompletion
	}
	return workStepsCollapseChoices[index].value
}

// workStepsModeLabels renders the four segmented labels for the current locale.
func workStepsModeLabels(locale contracts.LocaleService) []string {
	labels := make([]string, 0, len(workStepsModeChoices))
	for _, choice := range workStepsModeChoices {
		labels = append(labels, localized(locale, choice.zh, choice.en))
	}
	return labels
}

// workStepsCollapseLabels renders the two collapse-timing labels.
func workStepsCollapseLabels(locale contracts.LocaleService) []string {
	labels := make([]string, 0, len(workStepsCollapseChoices))
	for _, choice := range workStepsCollapseChoices {
		labels = append(labels, localized(locale, choice.zh, choice.en))
	}
	return labels
}

// behaviorState is the page's status line (UI thread only).
type behaviorState struct {
	status    string
	statusErr bool

	// moreOpen is the 更多对话设置 disclosure state; native collapsibles need a
	// persistent *bool so the open/closed animation survives frame rebuilds.
	moreOpen bool

	// agentsFile is the workspace instruction file name draft; loaded once from
	// settings so an edit is not overwritten by a rebuild.
	agentsFile       string
	agentsFileLoaded bool
}

// agentPermissionMode normalizes the stored agent.permission row to one of the
// three presets. Empty/unknown values keep the workspace default; the retired
// "ask"/"never" rows normalize to read-only (see
// contracts.NormalizePermissionMode) instead of being offered as choices.
func agentPermissionMode(raw string) string {
	return contracts.NormalizePermissionMode(raw)
}

// behaviorPermissionLabel returns the localized short label of one mode.
func behaviorPermissionLabel(locale contracts.LocaleService, mode string) string {
	for _, choice := range behaviorPermissions {
		if choice.value == mode {
			return localized(locale, choice.zh, choice.en)
		}
	}
	return localized(locale, "工作区可写", "Workspace-write")
}

// saveBehaviorSetting persists one immediate-apply settings row and reports the
// result in the page's shared status line; the behavior page has no save button.
func saveBehaviorSetting(st *behaviorState, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, key, value string) {
	if err := store.Set(key, value); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
	} else {
		st.status = localized(locale, "已保存", "Saved")
		st.statusErr = false
	}
	runtime.Invalidate()
}

// buildBehaviorPage renders the default-permission settings. Every control on
// the page applies at once (the page has no save button): the permission menu
// writes the shared agent row, the work-details segmented controls write
// ui.workSteps.*, the switches persist on change and the workspace instruction
// file saves as it is edited.
func buildBehaviorPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *behaviorState) {
	t := design.Of(c)
	applyBool := func(key string, value bool) {
		saveBehaviorSetting(st, runtime, store, locale, key, strconv.FormatBool(value))
	}
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "行为", "Behavior"), localized(locale,
			"代理的默认权限与工具行为；修改立即生效。",
			"Default agent permissions and tool behavior; changes apply immediately."))
		uikit.Section(c, localized(locale, "默认代理权限", "Default agent permission"), func(*ui.Context) {
			current := agentPermissionMode(store.Get(contracts.SettingAgentPermission, ""))
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "当前：", "Current: ")+
					behaviorPermissionLabel(locale, current)), t.Font(design.TypeXS)).
					TextColor(t.LabelSecondary)
				ui.Spacer(c).Grow(1)
				if st.status != "" {
					statusBadge(c, t, st.status, st.statusErr)
				}
			})
			// A menu is a chip, not a form field: a row keeps it at its natural
			// width instead of stretching it across the content column.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.PopoverMenuButton(c, behaviorPermissionLabel(locale, current), func(m *ui.Menu) {
					for _, choice := range behaviorPermissions {
						choice := choice
						item := m.Item(localized(locale, choice.zh, choice.en))
						item.Checked(choice.value == current)
						if item.Chosen() && choice.value != current {
							if err := store.Set(contracts.SettingAgentPermission, choice.value); err != nil {
								st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
								st.statusErr = true
							} else {
								st.status = localized(locale, "已保存", "Saved")
								st.statusErr = false
							}
							runtime.Invalidate()
						}
					}
				}).Tooltip(localized(locale, "选择默认权限模式", "Choose the default permission mode"))
			})
			ui.Column(c).FillWidth().Gap(design.Space1).PaddingY(design.Space1).Children(func() {
				for _, choice := range behaviorPermissions {
					uikit.Bullet(c, localized(locale, choice.zh, choice.en)+localized(locale, "：", ": ")+
						localized(locale, choice.meaningZh, choice.meaningEn))
				}
			})
			design.Apply(ui.Text(c, localized(locale,
				"修改立即生效（与会话「会话设置」菜单的权限模式共用）。",
				"Changes apply immediately (shared with the session toolbar).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})
		// Work details: how much of a turn's tool-call steps the transcript shows
		// and when finished steps fold (DSH-aligned). This stays visible next to
		// the daily permission choice; it is a display preference, not a
		// low-frequency knob.
		uikit.Section(c, localized(locale, "工作步骤", "Work details"), func(*ui.Context) {
			design.Apply(ui.Text(c, localized(locale,
				"选择希望看到多少工具调用细节。默认：标准。",
				"Choose how much tool-call detail to see. Default: standard.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			modeIndex := workStepsModeIndex(contracts.NormalizeWorkStepsMode(
				store.Get(contracts.SettingWorkStepsMode, "")))
			ui.Field(c, localized(locale, "工作步骤展示", "Work details"), func() {
				segmented := ui.Segmented(c, &modeIndex, workStepsModeLabels(locale)...).
					Label(localized(locale, "工作步骤展示", "Work details"))
				if segmented.Changed() {
					saveBehaviorSetting(st, runtime, store, locale,
						contracts.SettingWorkStepsMode, workStepsModeValue(modeIndex))
				}
			})
			ui.Column(c).FillWidth().Gap(design.Space1).PaddingY(design.Space1).Children(func() {
				for _, choice := range workStepsModeChoices {
					uikit.Bullet(c, localized(locale, choice.zh, choice.en)+
						localized(locale, "：", ": ")+localized(locale, choice.meaningZh, choice.meaningEn))
				}
			})
			collapseIndex := workStepsCollapseIndex(contracts.NormalizeWorkStepsCollapse(
				store.Get(contracts.SettingWorkStepsCollapse, "")))
			ui.Field(c, localized(locale, "工作步骤收起时机", "When to collapse work details"),
				func() {
					segmented := ui.Segmented(c, &collapseIndex,
						workStepsCollapseLabels(locale)...).
						Label(localized(locale, "工作步骤收起时机", "When to collapse work details"))
					if segmented.Changed() {
						saveBehaviorSetting(st, runtime, store, locale,
							contracts.SettingWorkStepsCollapse, workStepsCollapseValue(collapseIndex))
					}
				}).Description(localized(locale,
				"「回答结束后」在本次回答完成时收起；「下次有新消息时」保留到新消息出现。",
				"\"After the answer\" folds when this answer completes; \"On the next message\" keeps them open until the next message arrives."))
		})
		// Progressive disclosure (settings-complexity audit P4): only the daily
		// permission choice and the work-details preference stay visible; the
		// page's other conversation options live behind one native disclosure,
		// collapsed by default, so the page reads as one clear setting. A muted
		// summary keeps the collapsed state informative without repeating the
		// controls.
		ui.Collapsible(c, localized(locale, "更多对话设置", "More conversation settings"), &st.moreOpen, func() {
			ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
				uikit.Section(c, localized(locale, "运行中发送", "Sending while running"), func(*ui.Context) {
					ui.Form(c, func() {
						enabled := sendWhileRunningEnabled(store)
						if switchField(c,
							localized(locale, "运行中允许排队发送", "Queue prompts while running"),
							localized(locale, "默认开启。运行中发送的提示会排队，在本轮结束后依次发出；关闭后回复生成期间 Enter 不可用，草稿会保留到本轮结束。",
								"On by default. Prompts sent while running are queued and sent after the current turn; when off, Enter is unavailable while a reply streams and the draft is kept until the turn ends."),
							&enabled) {
							applyBool(contracts.SettingSendWhileRunning, enabled)
						}
					})
				})
				uikit.Section(c, localized(locale, "目标", "Goals"), func(*ui.Context) {
					ui.Form(c, func() {
						goalEnabled := goalAutoContinueEnabled(store)
						if switchField(c,
							localized(locale, "目标自动续跑", "Auto-continue goal"),
							localized(locale,
								"回合结束后若目标仍激活，自动发送继续推进提示（每个目标最多 5 次）。",
								"After a turn ends while the goal is still active, automatically send a continuation prompt (at most 5 times per goal)."),
							&goalEnabled) {
							applyBool(contracts.SettingGoalAutoContinue, goalEnabled)
						}
					})
				})
				uikit.Section(c, localized(locale, "消息字号", "Message text size"), func(*ui.Context) {
					current := chatFontSizeSetting(store)
					ui.PopoverMenuButton(c, chatFontSizeLabel(locale, current), func(m *ui.Menu) {
						for _, choice := range chatFontSizes {
							choice := choice
							item := m.Item(localized(locale, choice.zh, choice.en))
							item.Checked(choice.value == current)
							if item.Chosen() && choice.value != current {
								if err := store.Set(contracts.SettingChatFontSize, choice.value); err != nil {
									st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
									st.statusErr = true
								} else {
									st.status = localized(locale, "已保存", "Saved")
									st.statusErr = false
								}
								runtime.Invalidate()
							}
						}
					}).Label(localized(locale, "消息字号：", "Text size: ") +
						chatFontSizeLabel(locale, current)).
						Tooltip(localized(locale, "选择对话文字大小", "Choose the conversation text size"))
				})
				uikit.Section(c, localized(locale, "工作区指令", "Workspace instructions"), func(*ui.Context) {
					if !st.agentsFileLoaded {
						st.agentsFileLoaded = true
						st.agentsFile = store.Get(contracts.SettingAgentsFile, "AGENTS.md")
					}
					ui.Field(c, localized(locale, "工作区指令文件", "Workspace instructions file"), func() {
						input := ui.TextInput(c, &st.agentsFile).
							Label(localized(locale, "工作区指令文件", "Workspace instructions file")).
							Placeholder("AGENTS.md").FillWidth()
						if input.Changed() {
							saveAgentsFile(st, store, locale)
						}
					}).Description(localized(locale,
						"运行时从当前工作区读取并注入系统提示；重启/下次运行生效；留空关闭。",
						"Read from the current workspace at run time and appended to the system prompt; takes effect on the next run; empty disables it."))
				})
			})
		})
		if !st.moreOpen {
			design.Apply(ui.Text(c, behaviorMoreSummary(locale, store)), t.TextMuted(design.TypeXXS)).MaxLines(2)
		}
	})
}

// behaviorDraftDirty reports whether the workspace instruction file input holds
// a value that differs from the stored row. The other controls write at once,
// so the agents-file field is the page's only possible draft.
func behaviorDraftDirty(st *behaviorState, store contracts.SettingsService) bool {
	// Before the page is first shown there is no draft at all: the field is
	// seeded lazily on its first build.
	if store == nil || !st.agentsFileLoaded {
		return false
	}
	return strings.TrimSpace(st.agentsFile) != strings.TrimSpace(store.Get(contracts.SettingAgentsFile, "AGENTS.md"))
}

// discardBehaviorDraft re-reads the stored instruction file and clears the
// page status, so the host's Discard action reopens a clean behavior page.
func discardBehaviorDraft(st *behaviorState, store contracts.SettingsService) {
	if store != nil {
		st.agentsFile = store.Get(contracts.SettingAgentsFile, "AGENTS.md")
	}
	st.agentsFileLoaded = true
	st.status = ""
	st.statusErr = false
}

// behaviorMoreSummary is the one muted line shown while 更多对话设置 is collapsed:
// a compact digest of the four settings it holds.
func behaviorMoreSummary(locale contracts.LocaleService, store contracts.SettingsService) string {
	onOff := func(value bool) string {
		if value {
			return localized(locale, "开", "On")
		}
		return localized(locale, "关", "Off")
	}
	agentsFile := strings.TrimSpace(store.Get(contracts.SettingAgentsFile, "AGENTS.md"))
	if agentsFile == "" {
		agentsFile = localized(locale, "已关闭", "Off")
	}
	return localized(locale, "排队发送：", "Queue: ") + onOff(sendWhileRunningEnabled(store)) +
		" · " + localized(locale, "目标续跑：", "Goal auto-continue: ") +
		onOff(goalAutoContinueEnabled(store)) +
		" · " + localized(locale, "消息字号：", "Text size: ") +
		chatFontSizeLabel(locale, chatFontSizeSetting(store)) +
		" · " + localized(locale, "指令文件：", "Instructions: ") + agentsFile
}

// saveAgentsFile persists the trimmed workspace instruction file name; an empty
// value disables the injection.
func saveAgentsFile(st *behaviorState, store contracts.SettingsService, locale contracts.LocaleService) {
	name := strings.TrimSpace(st.agentsFile)
	if err := store.Set(contracts.SettingAgentsFile, name); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	st.agentsFile = name
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
}

// chatFontSizeChoice is one conversation font-size choice.
type chatFontSizeChoice struct {
	value string
	zh    string
	en    string
}

var chatFontSizes = []chatFontSizeChoice{
	{value: "small", zh: "小", en: "Small"},
	{value: "standard", zh: "标准", en: "Standard"},
	{value: "large", zh: "大", en: "Large"},
}

// chatFontSizeSetting normalizes ui.chatFontSize: small/standard/large, with
// missing and malformed values falling back to standard.
func chatFontSizeSetting(store contracts.SettingsService) string {
	if store == nil {
		return "standard"
	}
	switch strings.ToLower(strings.TrimSpace(store.Get(contracts.SettingChatFontSize, ""))) {
	case "small":
		return "small"
	case "large":
		return "large"
	default:
		return "standard"
	}
}

// chatFontSizeLabel returns the localized current choice label.
func chatFontSizeLabel(locale contracts.LocaleService, value string) string {
	for _, choice := range chatFontSizes {
		if choice.value == value {
			return localized(locale, choice.zh, choice.en)
		}
	}
	return localized(locale, "标准", "Standard")
}

// sendWhileRunningEnabled normalizes the stored ui.sendWhileRunning row:
// queueing is on unless the value is explicitly "false"; missing, empty and
// malformed values keep the default (on) so the composer stays usable while a
// reply streams.
func sendWhileRunningEnabled(store contracts.SettingsService) bool {
	if store == nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(store.Get(contracts.SettingSendWhileRunning, "")), "false")
}

// goalAutoContinueEnabled normalizes goal.autoContinue: only "true" enables it.
func goalAutoContinueEnabled(store contracts.SettingsService) bool {
	if store == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(store.Get(contracts.SettingGoalAutoContinue, "")), "true")
}
