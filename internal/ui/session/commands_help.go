package session

// /help modal: the registered slash commands and the product's window
// shortcuts, rendered with native controls.

import (
	"sort"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// helpShortcut is one keyboard shortcut row of the help modal.
type helpShortcut struct{ keys, zh, en string }

// helpShortcuts lists the window shortcuts the old shell advertised.
var helpShortcuts = []helpShortcut{
	{keys: "Cmd/Ctrl + K", zh: "打开命令面板", en: "Open the command palette"},
	{keys: "Cmd/Ctrl + B", zh: "折叠 / 展开侧边栏", en: "Collapse / expand sidebar"},
	{keys: "Cmd/Ctrl + Enter", zh: "发送消息", en: "Send the message"},
}

// buildHelpDialog renders the /help modal: every registered command with its
// hint, then the keyboard shortcuts and the Esc hint.
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
	ui.Modal(c, &st.helpOpen, func() {
		design.Apply(ui.Column(c).Width(520).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("帮助", "Help")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"斜杠命令与窗口快捷键。输入 / 可打开命令菜单。",
				"Slash commands and window shortcuts. Type / to open the command menu.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)

			design.Apply(ui.Text(c, st.tr("斜杠命令", "Slash commands")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 8).Children(func() {
				ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
					for _, command := range commands {
						command := command
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
							design.Apply(ui.Text(c, command.Title), t.Font(design.TypeXS)).
								FontWeight(600).TextColor(t.LabelPrimary).Width(96).Shrink(0)
							design.Apply(ui.Text(c, command.Hint), t.TextMuted(design.TypeXS)).
								Grow(1).MaxLines(1).Ellipsis("…")
						})
					}
				})
			})

			design.Apply(ui.Text(c, st.tr("快捷键", "Keyboard shortcuts")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
				for _, shortcut := range helpShortcuts {
					shortcut := shortcut
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						design.Apply(ui.Text(c, shortcut.keys), t.MonoFamily()).FontSize(11).
							TextColor(t.LabelSecondary).Width(160).Shrink(0)
						design.Apply(ui.Text(c, st.tr(shortcut.zh, shortcut.en)), t.Font(design.TypeXS)).
							TextColor(t.LabelTertiary).Grow(1)
					})
				}
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, st.tr("Esc 关闭", "Esc to close")), t.TextMuted(design.TypeXXS))
				ui.Spacer(c).Grow(1)
				if ui.Button(c, st.tr("关闭", "Close")).Clicked() {
					st.helpOpen = false
				}
			})
		})
	})
}
