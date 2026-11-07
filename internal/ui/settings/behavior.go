package settings

// Behavior page: the default agent permission mode. The page writes the same
// agent.permission row the approvals gate reads dynamically, so a selection
// applies immediately (the session composer's permission chip shares the key).

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

const (
	// BehaviorPageID is the behavior page identity.
	BehaviorPageID = "behavior"
	// BehaviorPageSection places it in the general group.
	BehaviorPageSection = "general"
	// BehaviorPageOrder places it next to Diagnostics (95) and Notifications (99).
	BehaviorPageOrder = 96
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
		meaningZh: "全部拒绝（fail-closed）", meaningEn: "Deny everything (fail-closed)",
	},
}

// behaviorState is the page's status line (UI thread only).
type behaviorState struct {
	status    string
	statusErr bool
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

// buildBehaviorPage renders the default-permission settings.
func buildBehaviorPage(c *ui.Context, runtime contracts.UIRuntime,
	store contracts.SettingsService, locale contracts.LocaleService, st *behaviorState) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, localized(locale, "行为", "Behavior"), localized(locale,
			"代理的默认权限与工具行为。",
			"Default agent permissions and tool behavior."))
		panel(c, t, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				sectionTitle(c, t, localized(locale, "默认代理权限", "Default agent permission"))
				ui.Spacer(c).Grow(1)
				if st.status != "" {
					statusBadge(c, t, st.status, st.statusErr)
				}
			})
			current := agentPermissionMode(store.Get(contracts.SettingAgentPermission, ""))
			design.Apply(ui.Text(c, localized(locale, "当前：", "Current: ")+
				behaviorPermissionLabel(locale, current)), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary)
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
					design.Apply(ui.Text(c,
						localized(locale, choice.zh, choice.en)+localized(locale, "：", ": ")+
							localized(locale, choice.meaningZh, choice.meaningEn)),
						t.TextMuted(design.TypeXXS)).Grow(1)
				}
			})
			design.Apply(ui.Text(c, localized(locale,
				"修改立即生效（approvals 动态读取该设置，与会话工具栏的权限模式共用）。",
				"Changes apply immediately (approvals read it dynamically and it is shared with the session toolbar).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})
		panel(c, t, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				sectionTitle(c, t, localized(locale, "运行中发送", "Sending while running"))
				ui.Spacer(c).Grow(1)
				if st.status != "" {
					statusBadge(c, t, st.status, st.statusErr)
				}
			})
			enabled := sendWhileRunningEnabled(store)
			toggle := ui.Toggle(c, &enabled, localized(locale,
				"运行中允许排队发送", "Queue prompts while running"))
			if toggle.Changed() {
				if err := store.Set(contracts.SettingSendWhileRunning, strconv.FormatBool(enabled)); err != nil {
					st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
					st.statusErr = true
				} else {
					st.status = localized(locale, "已保存", "Saved")
					st.statusErr = false
				}
				runtime.Invalidate()
			}
			design.Apply(ui.Text(c, localized(locale,
				"发送的提示会在当前回合结束后依次发出。",
				"Queued prompts are sent one by one after the current turn finishes.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})
	})
}

// sendWhileRunningEnabled normalizes the stored ui.sendWhileRunning row: only
// "true" enables queueing; missing, empty and malformed values are off.
func sendWhileRunningEnabled(store contracts.SettingsService) bool {
	if store == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(store.Get(contracts.SettingSendWhileRunning, "")), "true")
}
