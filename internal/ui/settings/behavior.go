package settings

// Behavior page: the default agent permission mode (daily) plus the page's
// lower-frequency conversation options behind one collapsed disclosure. Every
// control applies immediately (the page has no save button): the permission menu
// writes the shared agent.permission row the approvals gate reads dynamically,
// the switches persist on change and the workspace instruction file saves as it
// is edited.

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
	// permissionNever denies every tool call (fail-closed); the approvals gate
	// owns the enforcement while this page only persists the choice.
	permissionNever = "never"
)

// behaviorPermission is one default-permission choice.
type behaviorPermission struct {
	value     string
	zh        string
	en        string
	meaningZh string
	meaningEn string
}

// behaviorPermissions lists the four choices in menu order.
var behaviorPermissions = []behaviorPermission{
	{
		value: contracts.PermissionAsk, zh: "询问", en: "Ask",
		meaningZh: "每次危险操作都会询问", meaningEn: "Ask for every dangerous operation",
	},
	{
		value: contracts.PermissionWorkspace, zh: "工作区", en: "Workspace",
		meaningZh: "工作区内的只读工具自动允许，其他操作询问",
		meaningEn: "Auto-allow read-only workspace tools; ask for the rest",
	},
	{
		value: contracts.PermissionFull, zh: "完全访问", en: "Full access",
		meaningZh: "全部自动允许，不再询问", meaningEn: "Allow everything without asking",
	},
	{
		value: permissionNever, zh: "从不", en: "Never",
		meaningZh: "全部拒绝，不执行任何工具", meaningEn: "Deny every tool call",
	},
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

// agentPermissionMode normalizes the stored agent.permission row: unknown and
// empty values default to ask.
func agentPermissionMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case contracts.PermissionWorkspace:
		return contracts.PermissionWorkspace
	case contracts.PermissionFull:
		return contracts.PermissionFull
	case permissionNever:
		return permissionNever
	default:
		return contracts.PermissionAsk
	}
}

// behaviorPermissionLabel returns the localized short label of one mode.
func behaviorPermissionLabel(locale contracts.LocaleService, mode string) string {
	for _, choice := range behaviorPermissions {
		if choice.value == mode {
			return localized(locale, choice.zh, choice.en)
		}
	}
	return localized(locale, "询问", "Ask")
}

// buildBehaviorPage renders the default-permission settings. Every control on
// the page applies at once (the page has no save button): the permission menu
// writes the shared agent row, the switches persist on change and the workspace
// instruction file saves as it is edited.
func buildBehaviorPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *behaviorState) {
	t := design.Of(c)
	applyBool := func(key string, value bool) {
		if err := store.Set(key, strconv.FormatBool(value)); err != nil {
			st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
			st.statusErr = true
		} else {
			st.status = localized(locale, "已保存", "Saved")
			st.statusErr = false
		}
		runtime.Invalidate()
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
		// Progressive disclosure (settings-complexity audit P4): only the daily
		// permission choice stays visible; the page's other conversation options
		// live behind one native disclosure, collapsed by default, so the page
		// reads as one clear setting. A muted summary keeps the collapsed state
		// informative without repeating the controls.
		ui.Collapsible(c, localized(locale, "更多对话设置", "More conversation settings"), &st.moreOpen, func() {
			ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
				uikit.Section(c, localized(locale, "运行中发送", "Sending while running"), func(*ui.Context) {
					ui.Form(c, func() {
						enabled := sendWhileRunningEnabled(store)
						if switchField(c,
							localized(locale, "运行中允许排队发送", "Queue prompts while running"),
							localized(locale, "发送的提示会在当前回合结束后依次发出。",
								"Queued prompts are sent one by one after the current turn finishes."),
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
					}).Tooltip(localized(locale, "选择对话文字大小", "Choose the conversation text size"))
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

// sendWhileRunningEnabled normalizes the stored ui.sendWhileRunning row: only
// "true" enables queueing; missing, empty and malformed values are off.
func sendWhileRunningEnabled(store contracts.SettingsService) bool {
	if store == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(store.Get(contracts.SettingSendWhileRunning, "")), "true")
}

// goalAutoContinueEnabled normalizes goal.autoContinue: only "true" enables it.
func goalAutoContinueEnabled(store contracts.SettingsService) bool {
	if store == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(store.Get(contracts.SettingGoalAutoContinue, "")), "true")
}
