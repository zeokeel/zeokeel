package shell

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/presets"
)

// FP-06/presets UI: Settings > Agent presets lists the registry (built-ins and
// user JSON files) with its validation state and the actions that manage it;
// the live session header gets a preset picker that writes the per-session
// selection. The backend registry lives in internal/presets and the controller;
// this file only renders it and surfaces its errors.

const presetExampleFile = "example-reviewer.json"

// presetExampleJSON is the "Create example preset" template. The id and file
// name are deliberately distinct from the documented "reviewer" example so a
// real reviewer.json never collides with the generated template.
const presetExampleJSON = `{
  "id": "example-reviewer",
  "name": "Reviewer",
  "description": "Read-only code review: no writes, focused on findings.",
  "tools": ["read_file", "list_dir", "grep", "terminal_open", "terminal_list", "terminal_read"],
  "persona": "You review code. Report findings, never modify files.",
  "prompt": "Prefer concrete file:line references and rank findings by severity.",
  "order": 10
}
`

// presetRegistry returns the controller's registry, or the built-ins when the
// shell is used without a live controller.
func (a *App) presetRegistry() *presets.Registry {
	if a.Ctrl != nil {
		if reg := a.Ctrl.Presets(); reg != nil {
			return reg
		}
	}
	reg, _ := presets.Load(presets.Options{})
	return reg
}

// presetsDir is the user preset directory inside the data directory.
func (a *App) presetsDir() string { return filepath.Join(a.pluginDataDir(), "presets") }

// presetFilePath returns the user file for id, or "" when the preset is a pure
// built-in.
func (a *App) presetFilePath(id string) string {
	if id == "" {
		return ""
	}
	path := filepath.Join(a.presetsDir(), id+".json")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// presetLoadError reports the last preset load/reload failure. The controller
// records it (fail closed: the previous registry keeps serving); when the
// controller has no record yet the shell loads the same directory itself so a
// file broken since startup is still explained. The page must say the list may
// be the last good one.
func (a *App) presetLoadError() string {
	if a.Ctrl == nil {
		return ""
	}
	if err := a.Ctrl.LastPresetLoadError(); err != nil {
		return err.Error()
	}
	_, err := presets.Load(presets.Options{UserDir: a.pluginDataDir()})
	if err == nil {
		return ""
	}
	return err.Error()
}

// presetsSettingsPage is Settings > Agent presets: the registry, its state and
// management actions.
func (a *App) presetsSettingsPage(c *ui.Context, t *design.Tokens) {
	reg := a.presetRegistry()
	list := reg.List()
	current := a.presetDefaultID()
	loadErr := a.presetLoadError()
	ui.Column(c).Key("agent-presets").Gap(design.Space3).Children(func() {
		ui.Column(c).Gap(design.Space1).Children(func() {
			settingsText(c, a.T("settings.agent_presets"), design.TypeM, t.LabelPrimary).
				FontWeight(600)
			settingsText(c, a.pluginLocalized(
				"Presets combine a tool allowlist, persona and extra prompt. User presets live in <data dir>/presets/*.json.",
				"预设组合了工具白名单、人设与额外提示词。用户预设位于 <数据目录>/presets/*.json。"),
				design.TypeXXS, t.LabelTertiary)
		})
		if loadErr != "" {
			// Fail-closed load: built-ins (or the last good registry) stay
			// active, but the user must know their preset files are ignored.
			ui.Row(c).Padding(design.Space2, design.Space3).Gap(design.Space2).
				Radius(design.RadiusSM).Background(t.Danger.Alpha(0.08)).
				Border(1, t.Danger.Alpha(0.35)).Children(func() {
				settingsTypeText(c, t, design.TypeXXS, t.DangerText,
					a.pluginLocalized("Preset files ignored: ", "预设文件被忽略：")+loadErr).MaxLines(3)
			})
		}
		a.settingsRow(c, t,
			a.pluginLocalized("Manage preset files", "管理预设文件"),
			a.pluginLocalized("Reload after editing the JSON files, open the folder, or create a template.",
				"编辑 JSON 后重新加载、打开目录，或创建模板。"),
			settingsRowStyle{}, func() {
				ui.Row(c).Gap(design.Space2).Children(func() {
					a.presetActionButton(c, t, a.pluginLocalized("Reload presets", "重新加载预设"), func() {
						a.reloadPresets(c)
					})
					a.presetActionButton(c, t, a.pluginLocalized("Open preset folder", "打开预设目录"), func() {
						if err := os.MkdirAll(a.presetsDir(), 0o755); err != nil {
							a.toastDeferred(c, a.pluginLocalized("Could not open the preset folder", "无法打开预设目录"))
							return
						}
						if err := a.pluginOpener()(a.presetsDir()); err != nil {
							a.toastDeferred(c, a.pluginLocalized("Could not open the preset folder", "无法打开预设目录"))
						}
					})
					a.presetActionButton(c, t, a.pluginLocalized("Create example preset", "创建示例预设"), func() {
						a.createExamplePreset(c)
					})
				})
			})
		if len(list) == 0 {
			settingsTypeText(c, t, design.TypeXS, t.LabelTertiary,
				a.pluginLocalized("No presets available.", "当前没有可用预设。"))
			return
		}
		for i, preset := range list {
			preset := preset
			last := i == len(list)-1
			var action func()
			if preset.ID != current {
				action = func() {
					a.presetActionButton(c, t, a.pluginLocalized("Set default "+preset.Name,
						"设为默认 "+preset.Name), func() {
						a.setPresetDefault(c, preset.ID)
					})
				}
			}
			a.presetRow(c, t, preset, current, last, loadErr != "", action)
		}
	})
}

// presetActionButton is one small native action on the preset page.
func (a *App) presetActionButton(c *ui.Context, t *design.Tokens, label string, fn func()) {
	button := ui.Button(c, label).Height(t.Theme().Space(8)).Label(label)
	if button.Clicked() {
		fn()
	}
}

// presetRow is one preset row: name and Default badge, description, and the
// allowlist/model/overrides metadata, with an optional action on the right.
func (a *App) presetRow(c *ui.Context, t *design.Tokens, preset presets.Preset,
	current string, last, lastGood bool, action func()) {
	th := t.Theme()
	row := ui.Column(c).FillWidth().Padding(th.Space(3), th.Space(3)).
		Gap(th.Space(1)).Radius(design.RadiusMD).Border(1, t.BorderL2)
	if preset.ID == current {
		row = row.Background(settingsFillColor(t)).BorderColor(settingsCardSelectedEdge)
	}
	row.Children(func() {
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
			settingsTypeText(c, t, design.TypeBody, t.LabelPrimary, preset.Name).FontWeight(500)
			if preset.ID == current {
				ui.Row(c).Height(18).PaddingX(design.Space2).Radius(design.RadiusMax).
					Background(t.Accent.Alpha(0.10)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.pluginLocalized("Default", "默认")).FontSize(10).
						TextColor(t.Accent)
				})
			}
			if len(preset.Tools) == 0 {
				ui.Row(c).Height(18).PaddingX(design.Space2).Radius(design.RadiusMax).
					Background(t.BgHover).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.pluginLocalized("all tools", "全部工具")).FontSize(10).
						TextColor(t.LabelTertiary)
				})
			}
			if lastGood {
				// Fail-closed load: the list is the previous registry, so a
				// user-file badge would claim fields the row is not showing.
				ui.Row(c).Height(18).PaddingX(design.Space2).Radius(design.RadiusMax).
					Background(t.Warning.Alpha(0.10)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.pluginLocalized("last good registry", "上一份可用列表")).
						FontSize(10).TextColor(t.WarningText)
				})
			} else if a.presetFilePath(preset.ID) != "" {
				badge := a.pluginLocalized("user file", "用户文件")
				if builtinPresetID(preset.ID) {
					badge = a.pluginLocalized("overrides built-in", "覆盖内置")
				}
				ui.Row(c).Height(18).PaddingX(design.Space2).Radius(design.RadiusMax).
					Background(t.Accent.Alpha(0.08)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, badge).FontSize(10).TextColor(t.Accent)
				})
			}
			ui.Row(c).Grow(1)
			if action != nil {
				action()
			}
		})
		if preset.Description != "" {
			settingsTypeText(c, t, design.TypeXXS, t.LabelSecondary, preset.Description).MaxLines(2)
		}
		meta := "ID: " + preset.ID
		if len(preset.Tools) > 0 {
			meta += " · " + a.pluginLocalized(pluralTools(len(preset.Tools)),
				"工具 "+itoa(len(preset.Tools))+" 个")
		} else {
			meta += " · " + a.pluginLocalized("all registered tools", "全部已注册工具")
		}
		if preset.Model != "" {
			meta += " · Model: " + preset.Model
		}
		if preset.Order != 0 {
			meta += " · order " + itoa(preset.Order)
		}
		settingsTypeText(c, t, design.TypeXXXS, t.LabelTertiary, meta).MaxLines(1)
	})
	_ = last
}

// builtinPresetID reports whether id belongs to the fixed built-in catalog, so
// the page can label a user file as an override rather than a new preset.
func builtinPresetID(id string) bool {
	for _, builtin := range presets.Builtins() {
		if builtin.ID == id {
			return true
		}
	}
	return false
}

// pluralTools renders the allowlist size.
func pluralTools(n int) string {
	if n == 1 {
		return "1 tool"
	}
	return itoa(n) + " tools"
}

// itoa is a small int formatter for the preset metadata line.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// presetDefaultID is the default preset new sessions start with
// (agent.preset), canonicalized to a catalog ID.
func (a *App) presetDefaultID() string {
	if a.Ctrl == nil {
		return ""
	}
	if a.Ctrl.Store() == nil {
		return ""
	}
	raw, err := a.Ctrl.Store().GetSetting("agent.preset")
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(raw)
	if id == "" {
		return ""
	}
	if _, ok := a.presetRegistry().Get(id); !ok {
		return ""
	}
	return id
}

// setPresetDefault persists the default preset for new sessions.
func (a *App) setPresetDefault(c *ui.Context, id string) {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return
	}
	if err := a.Ctrl.Store().SetSetting("agent.preset", id); err != nil {
		a.toastDeferred(c, a.pluginLocalized("Could not set the default preset",
			"设置默认预设失败"))
		return
	}
	a.toastDeferred(c, a.pluginLocalized("Default preset: "+id, "默认预设："+id))
}

// reloadPresets re-reads the user files through the controller.
func (a *App) reloadPresets(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	_, err := a.Ctrl.ReloadPresets()
	if err != nil {
		a.toastDeferred(c, a.pluginLocalized("Preset reload failed: ", "预设重新加载失败：")+err.Error())
		return
	}
	a.toastDeferred(c, a.pluginLocalized("Presets reloaded", "预设已重新加载"))
}

// createExamplePreset writes the template when it does not exist yet.
func (a *App) createExamplePreset(c *ui.Context) {
	dir := a.presetsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.toastDeferred(c, a.pluginLocalized("Could not create the preset folder",
			"无法创建预设目录"))
		return
	}
	path := filepath.Join(dir, presetExampleFile)
	if _, err := os.Stat(path); err == nil {
		a.toastDeferred(c, a.pluginLocalized("The example preset already exists",
			"示例预设已存在"))
		return
	}
	if err := os.WriteFile(path, []byte(presetExampleJSON), 0o644); err != nil {
		a.toastDeferred(c, a.pluginLocalized("Could not write the example preset",
			"无法写入示例预设"))
		return
	}
	if a.Ctrl != nil {
		_, _ = a.Ctrl.ReloadPresets()
	}
	a.toastDeferred(c, a.pluginLocalized("Example preset created; edit it and reload",
		"示例预设已创建，可编辑后重新加载"))
}

// currentSessionPreset resolves the live session's preset and its warnings.
func (a *App) currentSessionPreset() (presets.Preset, []string) {
	if a.Ctrl == nil {
		reg := a.presetRegistry()
		preset, _ := reg.Get(presets.PresetFallbackID)
		return preset, nil
	}
	id := a.Ctrl.CurrentSessionID()
	if id == "" {
		preset, _ := a.presetRegistry().Get(presets.PresetFallbackID)
		return preset, nil
	}
	return a.Ctrl.ResolvePreset(id)
}

// sessionPresetPicker is the live session header's preset chip and popover.
func (a *App) sessionPresetPicker(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	// At the 900px minimum width the header is already dense (title, workspace,
	// mode, phase, two actions); the chip is hidden there so the right panel
	// keeps its readable width. The preset is still selectable in Settings.
	if w, _ := c.Size(); w > 0 && w < 1000 {
		return
	}
	preset, warnings := a.currentSessionPreset()
	// The controller also records build-time warnings per session (for example
	// a preset allowlist that matched no registered tool); surface both.
	if a.Ctrl != nil {
		warnings = append(warnings, a.Ctrl.PresetWarnings(a.Ctrl.CurrentSessionID())...)
	}
	st := a.shellUI()
	th := t.Theme()
	chip := ui.Row(c).Height(th.Space(7)).PaddingX(design.Space2).
		Radius(design.RadiusSM).Border(1, t.BorderL2).
		AlignItems(ui.Center).Gap(design.Space1).
		Label(a.pluginLocalized("Session preset, current: ", "会话预设，当前：") + preset.Name).
		Role(ui.RolePopUpButton).Focusable()
	if chip.Hovered() {
		chip = chip.Background(t.BgHover)
	}
	chip.Children(func() {
		ui.Text(c, preset.Name).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelSecondary).NoWrap()
	})
	if chip.Clicked() {
		st.presetPickerOpen = !st.presetPickerOpen
	}
	if len(warnings) > 0 {
		// A stale selection (deleted user preset) or a preset the build could
		// not apply fully falls back with a visible warning instead of
		// pretending the selection still applies.
		message := a.pluginLocalized("Preset unavailable, using Standard",
			"预设不可用，已回退到标准模式")
		if preset.ID == presets.PresetFallbackID {
			message = a.pluginLocalized("Preset warning: ", "预设警告：") + warnings[0]
		}
		ui.Text(c, message).FontSize(10).TextColor(t.WarningText).MaxLines(1).
			Ellipsis("…")
	}
	ui.PopoverBase(c, chip, &st.presetPickerOpen, func(panel ui.Element) {
		design.Apply(panel, t.Panel()).Width(th.Space(80)).Padding(design.Space2).
			Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		ui.Column(c).Gap(design.Space1).Children(func() {
			settingsTypeText(c, t, design.TypeXXS, t.LabelTertiary,
				a.pluginLocalized("Session preset", "会话预设"))
			if summary := presetPromptSummary(preset); summary != "" {
				settingsTypeText(c, t, design.TypeXXXS, t.LabelSecondary, summary).
					MaxLines(2).Ellipsis("…")
			}
			for _, option := range a.presetRegistry().List() {
				option := option
				selected := option.ID == preset.ID
				row := ui.Row(c).Height(th.Space(9)).Padding(design.Space1, design.Space2).
					Radius(design.RadiusSM).AlignItems(ui.Center).Gap(design.Space2).
					Label(option.Name).Role(ui.RoleMenuItem).Focusable()
				if row.Hovered() {
					row = row.Background(t.BgHover)
				}
				row.Children(func() {
					ui.Column(c).Grow(1).Gap(0).Children(func() {
						ui.Text(c, option.Name).FontSize(12).FixedLineHeight(17).
							TextColor(t.LabelPrimary).NoWrap()
						desc := option.Description
						if desc == "" {
							desc = option.ID
						}
						ui.Text(c, desc).FontSize(10).FixedLineHeight(15).
							TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
					})
					if option.Persona != "" {
						ui.Text(c, a.pluginLocalized("persona", "人设")).
							FontSize(9).TextColor(t.LabelTertiary)
					}
					if selected {
						ui.Text(c, "✓").FontSize(11).TextColor(t.Accent)
					}
				})
				row.OnClick(func() {
					st.presetPickerOpen = false
					a.applySessionPreset(c, option.ID)
				})
			}
		})
	})
}

// presetPromptSummary is the persona/prompt excerpt the picker shows for the
// effective preset (ResolvePreset's preset, not the raw stored id).
func presetPromptSummary(preset presets.Preset) string {
	if text := strings.TrimSpace(preset.Persona); text != "" {
		return "Persona: " + text
	}
	if text := strings.TrimSpace(preset.Prompt); text != "" {
		return "Prompt: " + text
	}
	return ""
}

// applySessionPreset writes the session's selection; an unknown id (a race with
// a reload) is reported inline instead of silently ignored.
func (a *App) applySessionPreset(c *ui.Context, id string) {
	if a.Ctrl == nil {
		return
	}
	sessionID := a.Ctrl.CurrentSessionID()
	if sessionID == "" {
		return
	}
	if err := a.Ctrl.SetSessionPreset(sessionID, id); err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			a.toastDeferred(c, a.pluginLocalized("Preset not found: ", "未找到预设：")+id)
			return
		}
		a.toastDeferred(c, a.pluginLocalized("Could not set the session preset: ",
			"设置会话预设失败：")+err.Error())
		return
	}
	if a.Ctrl.Store() != nil {
		// A chosen preset replaces the legacy per-session mode selection so the
		// two settings cannot disagree (built-in preset ids mirror mode ids).
		_ = a.Ctrl.Store().SetSetting(sessionModeKey(sessionID), id)
	}
	a.toastDeferred(c, a.pluginLocalized("Preset set; applies from the next turn",
		"预设已设置，下一回合生效"))
}

// sessionModeKey is the legacy per-session mode setting key (kept in sync by
// applySessionPreset).
func sessionModeKey(sessionID string) string {
	return "session.mode." + sessionID
}

// sessionPresetForDisplay is the picker label for snapshots rendered without a
// controller (static tests).
func (a *App) sessionPresetName() string {
	preset, _ := a.currentSessionPreset()
	return preset.Name
}
