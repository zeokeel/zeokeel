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

// buildPermissionSection is the one place the product explains the three
// permission presets (A5 §3.1 「权限」: one concept, one wording; the copies in
// the session composer and the sandbox note were removed so this is the only
// full statement). The presets are a bullet list under one summary line — a
// wall of one paragraph could not be scanned (A3 VD-19) — and a session's
// effective modes close the block in one metadata line, so a session-level
// "all permissions" override reads as approval-only while the OS sandbox
// follows the global mode (audit US-05/RG-02).
func buildPermissionSection(c *ui.Context, t *design.Tokens, locale contracts.LocaleService, chat contracts.ChatService) {
	uikit.Section(c, localized(locale, "权限模式", "Permission mode"), func(*ui.Context) {
		ui.Column(c).FillWidth().Gap(design.SpaceRow).MaxWidth(design.ProseMeasure).Children(func() {
			design.Apply(ui.Text(c, localized(locale,
				"在输入框的权限按钮里切换。三档决定哪些工具调用免审批：",
				"Switch in the composer's permission button. The presets decide which tool calls skip approval:")),
				t.TextMuted(design.TypeXS))
			ui.Column(c).FillWidth().Gap(design.SpaceInline).Children(func() {
				uikit.Bullet(c, localized(locale,
					"只读：仅只读与信息类工具免审批。",
					"Read-only: only read-only and informational tools skip approval."))
				uikit.Bullet(c, localized(locale,
					"工作区修改（默认）：再加上工作区内的文件读写。",
					"Workspace-write (default): plus file reads and writes inside the workspace."))
				uikit.Bullet(c, localized(locale,
					"所有权限：全部工具调用免审批。",
					"All permissions: every tool call skips approval."))
			})
		})
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
		scope := localized(locale, "跟随全局设置", "follows the global setting")
		if info.PermissionScope == contracts.PermissionScopeSession {
			scope = localized(locale, "本会话覆盖", "session override")
		}
		design.Apply(ui.Text(c, localized(locale, "本会话：审批 ", "This session: approval ")+
			permissionModeLabel(locale, info.EffectiveApprovalMode)+"（"+scope+"）"+
			localized(locale, " · 系统沙箱 ", " · OS sandbox ")+
			permissionModeLabel(locale, info.EffectiveSandboxMode)+
			localized(locale, "（全局）", " (global)")),
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
	ui.Column(c).FillWidth().Gap(design.SpaceScreen).Children(func() {
		uikit.PageHeader(c, localized(locale, "行为与限额", "Behavior & limits"), "")
		// The permission presets are documented here in one bulleted block; the
		// control itself lives on the composer's permission button (A5 §3.1,
		// the meta sentence "已移到输入框工具栏" is gone: it described a layout
		// that the next redesign invalidates).
		buildPermissionSection(c, t, locale, chat)
		// System sandbox: the always-on kernel-level file-effect enforcement
		// that lets the agent gate auto-allow shell tools. Its live status says
		// whether this host can enforce it — mode-aware, since full access
		// passes commands through unconfined (RG-02).
		buildSandboxSection(c, locale, sandboxService, store)
		// Work details: how much of a turn's tool-call steps the transcript shows
		// and when finished steps fold (DSH-aligned). This stays visible next to
		// the daily permission choice; it is a display preference, not a
		// low-frequency knob.
		search.region(c, "behavior.work-details", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "工作步骤", "Work details"), func(*ui.Context) {
				modeIndex := workStepsModeIndex(contracts.NormalizeWorkStepsMode(
					store.Get(contracts.SettingWorkStepsMode, "")))
				// The field label names the choice the control makes; repeating
				// the section title made a screen reader read the same name twice
				// (A5 F-22: 分节说范围，字段说取值).
				ui.Field(c, localized(locale, "展示密度", "Display density"), func() {
					if uikit.SegmentedControl(c, uikit.SegmentedOptions{
						Key:      "settings.behavior.workSteps",
						Label:    localized(locale, "展示密度", "Display density"),
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
			ui.Column(c).FillWidth().Gap(design.SpaceScreen).Children(func() {
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
							Description(localized(locale, "选择对话文字大小", "Choose the conversation text size"))
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
								Label(inputLabel(locale,
									localized(locale, "工作区指令文件", "Workspace instructions file"))).
								Placeholder("AGENTS.md").Width(controlWidth)
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
				"以下限额点「保存」后生效。",
				"These limits apply after you click Save.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
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
