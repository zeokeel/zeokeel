package session

// /help modal: the registered slash commands and the product's actual keyboard
// behavior, rendered with native controls and closed from the same footer
// position every overlay uses.

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// helpShortcut is one keyboard shortcut row of the help modal.
type helpShortcut struct{ keys, zh, en string }

// shortcutChordLabel renders a canonical host chord ("cmd+shift+b") as the
// product's display label ("Cmd/Ctrl + Shift + B"), matching the settings
// shortcut reference.
func shortcutChordLabel(chord string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(chord)), "+")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		switch strings.TrimSpace(part) {
		case "":
			continue
		case "cmd", "command", "cmd/ctrl":
			labels = append(labels, "Cmd/Ctrl")
		case "ctrl", "control":
			labels = append(labels, "Ctrl")
		case "shift":
			labels = append(labels, "Shift")
		case "alt", "option":
			labels = append(labels, "Alt")
		default:
			labels = append(labels, strings.ToUpper(strings.TrimSpace(part)))
		}
	}
	if len(labels) == 0 {
		return chord
	}
	return strings.Join(labels, " + ")
}

// shortcutSetting reads one configurable chrome chord from the settings store,
// falling back to the documented default for a missing, empty or malformed row.
func (st *state) shortcutSetting(key, fallback string) string {
	if st.settings == nil {
		return fallback
	}
	value := strings.TrimSpace(st.settings.Get(key, fallback))
	if value == "" {
		return fallback
	}
	return value
}

// helpShortcuts returns the keys the product really uses. The two configurable
// desktop chords (sidebar toggle and command palette) are rendered from the
// live settings values with the documented defaults as fallback, so the help
// reference can never drift from the host bindings; the fixed chords are the
// ones the host and the composer register.
func (st *state) helpShortcuts() []helpShortcut {
	palette := shortcutChordLabel(st.shortcutSetting(contracts.SettingsUIShortcutCommandPalette,
		contracts.DefaultShortcutCommandPalette))
	sidebar := shortcutChordLabel(st.shortcutSetting(contracts.SettingsUIShortcutToggleSidebar,
		contracts.DefaultShortcutToggleSidebar))
	return []helpShortcut{
		{keys: "Cmd/Ctrl + N", zh: "新会话", en: "New conversation"},
		{keys: "Cmd/Ctrl + ,", zh: "设置", en: "Settings"},
		{keys: palette, zh: "打开命令面板", en: "Open the command palette"},
		{keys: sidebar, zh: "折叠 / 展开侧边栏", en: "Collapse / expand sidebar"},
		{keys: "Cmd/Ctrl + Alt + I", zh: "切换右侧栏", en: "Toggle the inspector"},
		{keys: "Enter", zh: "发送消息", en: "Send the message"},
		{keys: "Shift + Enter", zh: "换行", en: "New line"},
		{keys: "Esc", zh: "关闭菜单 / 浮层；审批中为拒绝", en: "Close a menu or overlay; deny in approvals"},
		{keys: "/", zh: "打开斜杠命令菜单", en: "Open the slash command menu"},
		{keys: "@", zh: "引用工作区文件或会话", en: "Reference a workspace file or chat"},
		{keys: "↑ / ↓", zh: "在建议菜单中移动", en: "Move in the suggestion menu"},
		{keys: "Tab", zh: "接受高亮的建议", en: "Accept the highlighted suggestion"},
	}
}

// buildHelpDialog renders the /help modal: every registered command with its
// hint, then the real keyboard map.
func buildHelpDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.helpOpen {
		return
	}
	commands := append([]contracts.CommandSpec(nil), runtime.Commands()...)
	sort.SliceStable(commands, func(i, j int) bool {
		if commands[i].Order != commands[j].Order {
			return commands[i].Order < commands[j].Order
		}
		return commands[i].Title < commands[j].Title
	})
	close := func() { st.helpOpen = false }
	ui.Modal(c, &st.helpOpen, func() {
		dialogEnter(c, close)
		design.Apply(ui.Column(c).Width(560).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("帮助", "Help")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"斜杠命令与键盘快捷键。输入 / 可打开命令菜单。",
				"Slash commands and keyboard shortcuts. Type / to open the command menu.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)

			design.Apply(ui.Text(c, st.tr("斜杠命令", "Slash commands")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 8).Children(func() {
				ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
					for _, command := range commands {
						command := command
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
							design.Apply(ui.Text(c, command.Title), t.Font(design.TypeXS)).
								FontWeight(600).TextColor(t.LabelPrimary).Width(120).Shrink(0)
							design.Apply(ui.Text(c, command.Hint), t.TextMuted(design.TypeXS)).
								Grow(1).MaxLines(1).Ellipsis("…")
						})
					}
				})
			})

			design.Apply(ui.Text(c, st.tr("快捷键", "Keyboard shortcuts")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
				for _, shortcut := range st.helpShortcuts() {
					shortcut := shortcut
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						design.Apply(ui.Text(c, shortcut.keys), t.MonoFamily(), t.Font(design.TypeXXS)).
							TextColor(t.LabelSecondary).Width(150).Shrink(0)
						design.Apply(ui.Text(c, st.tr(shortcut.zh, shortcut.en)), t.Font(design.TypeXS)).
							TextColor(t.LabelTertiary).Grow(1)
					})
				}
			})
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("关闭", "Close"), Primary: true, OnClick: close},
			})
		})
	})
}
