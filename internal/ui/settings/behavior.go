package settings

// Behavior page: the default agent permission mode (daily), the transcript's
// work-details presentation (DSH-aligned), the page's lower-frequency
// conversation options behind one collapsed disclosure, and the run-limits
// form. The page has TWO submit models, partitioned explicitly (audit US-13):
// the display preferences apply immediately — the permission pointer reflects
// the shared agent.permission row the approvals gate reads dynamically, the
// work-details segmented controls write ui.workSteps.*, and the workspace
// instruction file saves as it is edited — while the run-limits form is an
// explicit-save draft persisted by the page footer's Save button (VE-01: the
// old "the page has no save button" comment drifted from that footer).

import (
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

// workStepsModeMeaning renders one display level's plain-language meaning for
// the current locale (the meaningZh/En fields, previously defined but never
// shown — 2026-11 audit US-06). It is the selected level's description, so the
// four abstract levels teach what they do instead of leaving the user to guess.
func workStepsModeMeaning(locale contracts.LocaleService, mode string) string {
	for _, choice := range workStepsModeChoices {
		if choice.value == mode {
			return localized(locale, choice.meaningZh, choice.meaningEn)
		}
	}
	return localized(locale, workStepsModeChoices[1].meaningZh, workStepsModeChoices[1].meaningEn)
}

// permissionModeLabel names one permission preset in plain language.
func permissionModeLabel(locale contracts.LocaleService, mode string) string {
	switch contracts.NormalizePermissionMode(mode) {
	case contracts.PermissionReadOnly:
		return localized(locale, "只读", "Read-only")
	case contracts.PermissionFull:
		return localized(locale, "所有权限", "All permissions")
	default:
		return localized(locale, "工作区修改", "Workspace-write")
	}
}

// buildPermissionExplanation is the visible permission teaching block (audit
// US-05): the three states explained accurately, the two-layer model (approval
// gate × OS sandbox) disclosed, the shell auto-allow fact stated correctly (no
// "仍会询问" contradiction), and — when an active session carries the wave-A read
// model — its effective approval mode, OS sandbox mode and permission scope, so
// a session-level "all permissions" override is visibly approval-only while the
// OS sandbox follows the global mode.
func buildPermissionExplanation(c *ui.Context, t *design.Tokens, locale contracts.LocaleService, chat contracts.ChatService) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		design.Apply(ui.Text(c, localized(locale,
			"权限三态只决定「哪些工具调用免审批」：只读＝仅只读/信息类工具；工作区修改（默认）＝再加工作区内文件读写；所有权限＝全部免审批。",
			"The three permission states only decide which tool calls skip approval: Read-only = read-only/informational tools; Workspace-write (default) = plus file reads/writes inside the workspace; All permissions = everything skips approval.")),
			t.TextMuted(design.TypeXXS)).MaxLines(3)
		design.Apply(ui.Text(c, localized(locale,
			"审批与 OS 沙箱是两层：OS 沙箱始终跟随全局权限，会话/自动化级「所有权限」只放宽审批、不解除沙箱；沙箱可用时 shell 类工具自动放行，不可用时命令失败。",
			"Approval and the OS sandbox are two layers: the OS sandbox always follows the global permission; a session/automation-level All permissions only relaxes approval, never lifts the sandbox. Shell tools are auto-allowed when the sandbox is available and fail closed when not.")),
			t.TextMuted(design.TypeXXS)).MaxLines(3)
		// The active session's effective modes make the distinction concrete.
		if chat == nil {
			return
		}
		active := chat.ActiveSession()
		if active == "" {
			return
		}
		var info contracts.ChatSessionInfo
		found := false
		for _, session := range chat.Sessions() {
			if session.ID == active {
				info, found = session, true
				break
			}
		}
		if !found || info.EffectiveApprovalMode == "" {
			return
		}
		scope := localized(locale, "全局跟随", "follows global")
		if info.PermissionScope == contracts.PermissionScopeSession {
			scope = localized(locale, "会话级覆盖", "session override")
		}
		design.Apply(ui.Text(c, localized(locale, "当前会话：审批＝", "Current session: approval = ")+
			permissionModeLabel(locale, info.EffectiveApprovalMode)+"（"+scope+"）"+
			localized(locale, " · OS 沙箱＝", " · OS sandbox = ")+
			permissionModeLabel(locale, info.EffectiveSandboxMode)+
			localized(locale, "（跟随全局）", " (follows global)")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
	})
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

// saveBehaviorSetting persists one immediate-apply settings row and reports the
// result in the page's shared status line. These display preferences apply at
// once; only the run-limits form below uses the page footer's Save button.
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

// buildBehaviorPage renders the behavior & limits page. The display preferences
// apply at once (the permission pointer reflects the shared agent row, the
// work-details segmented controls write ui.workSteps.*, the workspace
// instruction file saves as it is edited); the run-limits form is the page's
// explicit-save half, persisted by the footer Save button (US-13/VE-01).
func buildBehaviorPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *behaviorState,
	sandboxService contracts.SandboxService, chat contracts.ChatService, capabilities *capabilitiesState, search *pageSearch) {
	t := design.Of(c)
	// The message-size and workspace-instruction settings live inside the
	// collapsed 更多对话设置 disclosure: a search hit on either opens it before
	// they build, so the reveal can scroll the found region into view in this
	// frame (contracts.SettingsPageSpec.Reveal runs before Build).
	if search.targetedAt("behavior.message-size") || search.targetedAt("behavior.workspace-instructions") {
		st.moreOpen = true
	}
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "行为与限额", "Behavior & limits"), "")
		// Permission is edited from the composer's permission button now; keep
		// a quiet pointer here so the setting stays discoverable.
		design.Apply(ui.Text(c, localized(locale,
			"权限模式已移到输入框工具栏的权限按钮：工作区修改 / 所有权限 / 只读。",
			"Permission mode lives on the composer toolbar's permission button: Workspace-write / All permissions / Read-only.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		// The three states get a visible plain-language explanation here, not a
		// tooltip buried on a disabled menu header (audit US-05): what each state
		// auto-allows, the two-layer model (approval gate × OS sandbox), and the
		// corrected fact that shell tools are auto-allowed when the sandbox is
		// available. The active session's effective modes + scope make the
		// session-vs-global distinction concrete.
		buildPermissionExplanation(c, t, locale, chat)
		// OS sandbox: the always-on kernel-level file-effect enforcement that
		// lets the agent gate auto-allow shell tools. It sits next to the
		// permission mode it mirrors, and its live status says whether this
		// host can enforce it — mode-aware, since full access passes commands
		// through unconfined (RG-02).
		buildSandboxSection(c, locale, sandboxService, store)
		// Work details: how much of a turn's tool-call steps the transcript shows
		// and when finished steps fold (DSH-aligned). This stays visible next to
		// the daily permission choice; it is a display preference, not a
		// low-frequency knob.
		search.region(c, "behavior.work-details", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "工作步骤", "Work details"), func(*ui.Context) {
				modeIndex := workStepsModeIndex(contracts.NormalizeWorkStepsMode(
					store.Get(contracts.SettingWorkStepsMode, "")))
				ui.Field(c, localized(locale, "工作步骤展示", "Work details"), func() {
					if uikit.SegmentedControl(c, uikit.SegmentedOptions{
						Key:      "settings.behavior.workSteps",
						Label:    localized(locale, "工作步骤展示", "Work details"),
						Labels:   workStepsModeLabels(locale),
						Selected: &modeIndex,
					}) {
						saveBehaviorSetting(st, runtime, store, locale,
							contracts.SettingWorkStepsMode, workStepsModeValue(modeIndex))
					}
				}).Description(workStepsModeMeaning(locale, workStepsModeValue(modeIndex)))
				collapseIndex := workStepsCollapseIndex(contracts.NormalizeWorkStepsCollapse(
					store.Get(contracts.SettingWorkStepsCollapse, "")))
				ui.Field(c, localized(locale, "工作步骤收起时机", "When to collapse work details"),
					func() {
						if uikit.SegmentedControl(c, uikit.SegmentedOptions{
							Key:      "settings.behavior.workStepsCollapse",
							Label:    localized(locale, "工作步骤收起时机", "When to collapse work details"),
							Labels:   workStepsCollapseLabels(locale),
							Selected: &collapseIndex,
						}) {
							saveBehaviorSetting(st, runtime, store, locale,
								contracts.SettingWorkStepsCollapse, workStepsCollapseValue(collapseIndex))
						}
					})
			})
		})
		// Progressive disclosure (settings-complexity audit P4): only the daily
		// permission choice and the work-details preference stay visible; the
		// page's other conversation options live behind one native disclosure,
		// collapsed by default, so the page reads as one clear setting. A muted
		// summary keeps the collapsed state informative without repeating the
		// controls.
		ui.Collapsible(c, localized(locale, "更多对话设置", "More conversation settings"), &st.moreOpen, func() {
			ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
				search.region(c, "behavior.message-size", func(c *ui.Context) {
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
				})
				search.region(c, "behavior.workspace-instructions", func(c *ui.Context) {
					uikit.Section(c, localized(locale, "工作区指令", "Workspace instructions"), func(*ui.Context) {
						if !st.agentsFileLoaded {
							st.agentsFileLoaded = true
							st.agentsFile = store.Get(contracts.SettingAgentsFile, "AGENTS.md")
						}
						ui.Field(c, localized(locale, "工作区指令文件", "Workspace instructions file"), func() {
							input := ui.TextInput(c.Key("settings.behavior.agents-file"), &st.agentsFile).
								Label(localized(locale, "工作区指令文件", "Workspace instructions file")).
								Placeholder("AGENTS.md").FillWidth()
							if input.Changed() {
								saveAgentsFile(st, store, locale)
							}
						}).Description(localized(locale,
							"留空关闭；下次运行生效。",
							"Empty disables it; applies on the next run."))
					})
				})
			})
		})
		if !st.moreOpen {
			design.Apply(ui.Text(c, behaviorMoreSummary(locale, store)), t.TextMuted(design.TypeXXS)).MaxLines(2)
		}
		// Keep the transient save line below the editable controls: inserting it
		// above the workspace-instructions input would shift the focused field
		// and return focus to the navigation search.
		if st.status != "" {
			statusBadgesRow(c, t, st.status, st.statusErr)
		}

		// Delegation/shell/parallel limits used to be their own settings page;
		// they are the "limits" half of this page now. Unlike the display
		// preferences above (which apply immediately), this form is saved by the
		// page footer's Save button — the section says so, so the two submit
		// models are not confused (2026-11 audit US-13).
		uikit.Section(c, localized(locale, "运行限额", "Run limits"), func(body *ui.Context) {
			design.Apply(ui.Text(body, localized(locale,
				"以下限额需点页脚「保存」后生效；上方的显示偏好即时生效。",
				"These limits apply after the footer Save; the display preferences above apply immediately.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			buildCapabilitiesPage(body, store, capabilities, locale, false)
		})
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
// a compact digest of the two settings it holds.
func behaviorMoreSummary(locale contracts.LocaleService, store contracts.SettingsService) string {
	agentsFile := strings.TrimSpace(store.Get(contracts.SettingAgentsFile, "AGENTS.md"))
	if agentsFile == "" {
		agentsFile = localized(locale, "已关闭", "Off")
	}
	return localized(locale, "消息字号：", "Text size: ") +
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
