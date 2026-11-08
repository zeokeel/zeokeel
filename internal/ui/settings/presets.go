package settings

// The Presets settings page: reusable prompt/persona templates that the
// composer's 预设 menu inserts into the draft. The stored custom list wins;
// empty/malformed settings fall back to the built-in LegacyPromptPresets().

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Presets page constants. Section "general" keeps it in the same settings
// section as the other utility pages; Order 98 places it after Network (97)
// and before the default About page.
const (
	PresetsPageID      = "presets"
	PresetsPageSection = "general"
	PresetsPageOrder   = 98
	PresetsPageTitle   = "预设"
)

// presetsState is the Presets page's effective list, add-form drafts and the
// last save/validation status. It is UI-thread state only.
type presetsState struct {
	loaded      bool
	presets     []contracts.PromptPreset
	usingLegacy bool
	parseErr    string

	name   string
	prompt string

	// Agent presets (persona/tools/model) live in the same page but a separate
	// settings row; their drafts use distinct fields.
	agentPresets  []contracts.AgentPreset
	agentName     string
	agentPersona  string
	agentPrompt   string
	agentTools    string
	agentModel    string
	agentFormOpen bool

	status    string
	statusErr bool
}

func newPresetsState() *presetsState { return &presetsState{} }

// load reads the settings row once. Empty/malformed values keep the built-in
// defaults visible; malformed JSON also records the parse error for DangerText.
func (st *presetsState) load(store contracts.SettingsService, locale contracts.LocaleService) {
	if st.loaded {
		return
	}
	st.loaded = true
	if store == nil {
		st.presets = contracts.LegacyPromptPresets()
		st.usingLegacy = true
		return
	}
	presets, err := contracts.ParsePromptPresets(store.Get(contracts.SettingPromptPresets, ""))
	if err != nil {
		st.parseErr = err.Error()
		st.presets = contracts.LegacyPromptPresets()
		st.usingLegacy = true
		return
	}
	if len(presets) == 0 {
		st.presets = contracts.LegacyPromptPresets()
		st.usingLegacy = true
		return
	}
	st.presets = presets
	st.usingLegacy = false
}

// loadAgentPresets reads the presets.agents row once (empty = no presets).
func (st *presetsState) loadAgentPresets(store contracts.SettingsService) {
	if store == nil {
		return
	}
	presets, err := contracts.ParseAgentPresets(store.Get(contracts.SettingAgentPresets, ""))
	if err != nil {
		st.parseErr = err.Error()
		return
	}
	st.agentPresets = presets
}

// persistAgentPresets writes the agent preset list, restoring the previous
// slice on failure.
func (st *presetsState) persistAgentPresets(store contracts.SettingsService,
	locale contracts.LocaleService, previous []contracts.AgentPreset) bool {
	for index := range st.agentPresets {
		st.agentPresets[index].Order = index
	}
	encoded, err := contracts.EncodeAgentPresets(st.agentPresets)
	if err == nil && store != nil {
		err = store.Set(contracts.SettingAgentPresets, encoded)
	}
	if err != nil {
		st.agentPresets = previous
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return false
	}
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
	return true
}

// addAgentPreset validates and appends one agent preset.
func (st *presetsState) addAgentPreset(store contracts.SettingsService, locale contracts.LocaleService) {
	name := strings.TrimSpace(st.agentName)
	if name == "" {
		st.status = localized(locale, "名称不能为空", "The name is required")
		st.statusErr = true
		return
	}
	for _, preset := range st.agentPresets {
		if strings.TrimSpace(preset.Name) == name {
			st.status = localized(locale, "该名称已存在", "That name already exists")
			st.statusErr = true
			return
		}
	}
	slug := agentPresetSlug(name)
	for index := 2; ; index++ {
		duplicate := false
		for _, preset := range st.agentPresets {
			if preset.ID == slug {
				duplicate = true
				break
			}
		}
		if !duplicate {
			break
		}
		slug = agentPresetSlug(name) + "-" + strconv.Itoa(index)
	}
	var tools []string
	for _, tool := range strings.Split(st.agentTools, ",") {
		if trimmed := strings.TrimSpace(tool); trimmed != "" {
			tools = append(tools, trimmed)
		}
	}
	previous := append([]contracts.AgentPreset(nil), st.agentPresets...)
	st.agentPresets = append(st.agentPresets, contracts.AgentPreset{
		ID: slug, Name: name, Persona: strings.TrimSpace(st.agentPersona),
		Prompt: strings.TrimSpace(st.agentPrompt), Tools: tools,
		Model: strings.TrimSpace(st.agentModel), Order: len(st.agentPresets),
	})
	if st.persistAgentPresets(store, locale, previous) {
		st.agentName, st.agentPersona, st.agentPrompt, st.agentTools, st.agentModel = "", "", "", "", ""
	}
}

// deleteAgentPreset removes one row and persists the remainder.
func (st *presetsState) deleteAgentPreset(store contracts.SettingsService, locale contracts.LocaleService, index int) {
	if index < 0 || index >= len(st.agentPresets) {
		return
	}
	previous := append([]contracts.AgentPreset(nil), st.agentPresets...)
	st.agentPresets = append(append([]contracts.AgentPreset(nil), st.agentPresets[:index]...), st.agentPresets[index+1:]...)
	st.persistAgentPresets(store, locale, previous)
}

// moveAgentPreset swaps one row with its neighbor.
func (st *presetsState) moveAgentPreset(store contracts.SettingsService, locale contracts.LocaleService, index, delta int) {
	target := index + delta
	if index < 0 || index >= len(st.agentPresets) || target < 0 || target >= len(st.agentPresets) {
		return
	}
	previous := append([]contracts.AgentPreset(nil), st.agentPresets...)
	st.agentPresets[index], st.agentPresets[target] = st.agentPresets[target], st.agentPresets[index]
	st.persistAgentPresets(store, locale, previous)
}

// restoreAgentPresets clears the agent preset list (explicit empty array).
func (st *presetsState) restoreAgentPresets(store contracts.SettingsService, locale contracts.LocaleService) {
	previous := append([]contracts.AgentPreset(nil), st.agentPresets...)
	st.agentPresets = nil
	if st.persistAgentPresets(store, locale, previous) {
		st.status = localized(locale, "已恢复默认（已保存）", "Defaults restored (saved)")
	}
}

// agentPresetSlug derives a stable ASCII-ish ID from a display name.
func agentPresetSlug(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char >= 0x4e00 && char <= 0x9fff:
			builder.WriteRune(char)
			lastDash = false
		case char == '-' || char == '_':
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "preset"
	}
	return slug
}

// persist encodes and writes the current list, restoring the supplied snapshot
// when the write fails.
func (st *presetsState) persist(store contracts.SettingsService, locale contracts.LocaleService,
	previous []contracts.PromptPreset, previousLegacy bool) bool {
	encoded, err := contracts.EncodePromptPresets(st.presets)
	if err == nil && store != nil {
		err = store.Set(contracts.SettingPromptPresets, encoded)
	}
	if err != nil {
		st.presets = previous
		st.usingLegacy = previousLegacy
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return false
	}
	st.parseErr = ""
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
	if len(st.presets) == 0 {
		// An explicit empty list means "use the built-in defaults" again.
		st.presets = contracts.LegacyPromptPresets()
		st.usingLegacy = true
	} else {
		st.usingLegacy = false
	}
	return true
}

// addPreset validates the draft and appends it to the effective list.
func (st *presetsState) addPreset(store contracts.SettingsService, locale contracts.LocaleService) {
	name := strings.TrimSpace(st.name)
	prompt := strings.TrimSpace(st.prompt)
	switch {
	case name == "":
		st.status = localized(locale, "名称不能为空", "The name is required")
		st.statusErr = true
		return
	case prompt == "":
		st.status = localized(locale, "提示词不能为空", "The prompt is required")
		st.statusErr = true
		return
	}
	for _, preset := range st.presets {
		if strings.TrimSpace(preset.Name) == name {
			st.status = localized(locale, "该名称已存在", "That name already exists")
			st.statusErr = true
			return
		}
	}
	previous := append([]contracts.PromptPreset(nil), st.presets...)
	previousLegacy := st.usingLegacy
	st.presets = append(append([]contracts.PromptPreset(nil), st.presets...), contracts.PromptPreset{Name: name, Prompt: prompt})
	if st.persist(store, locale, previous, previousLegacy) {
		st.name = ""
		st.prompt = ""
	}
}

// deletePreset removes one row; deleting the last custom row writes "[]" and
// returns to the built-in defaults.
func (st *presetsState) deletePreset(store contracts.SettingsService, locale contracts.LocaleService, index int) {
	if index < 0 || index >= len(st.presets) {
		return
	}
	previous := append([]contracts.PromptPreset(nil), st.presets...)
	previousLegacy := st.usingLegacy
	kept := make([]contracts.PromptPreset, 0, len(st.presets)-1)
	kept = append(kept, st.presets[:index]...)
	kept = append(kept, st.presets[index+1:]...)
	st.presets = kept
	st.persist(store, locale, previous, previousLegacy)
}

// movePreset swaps one row with its neighbor (delta -1 or +1) and persists the
// new order.
func (st *presetsState) movePreset(store contracts.SettingsService, locale contracts.LocaleService, index, delta int) {
	target := index + delta
	if index < 0 || index >= len(st.presets) || target < 0 || target >= len(st.presets) {
		return
	}
	previous := append([]contracts.PromptPreset(nil), st.presets...)
	previousLegacy := st.usingLegacy
	st.presets[index], st.presets[target] = st.presets[target], st.presets[index]
	st.persist(store, locale, previous, previousLegacy)
}

// restoreBuiltIns writes an explicit empty list, which selects the built-in
// defaults on this and every later load.
func (st *presetsState) restoreBuiltIns(store contracts.SettingsService, locale contracts.LocaleService) {
	encoded, err := contracts.EncodePromptPresets(nil)
	if err == nil && store != nil {
		err = store.Set(contracts.SettingPromptPresets, encoded)
	}
	if err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	st.presets = contracts.LegacyPromptPresets()
	st.usingLegacy = true
	st.parseErr = ""
	st.status = localized(locale, "已恢复内置默认（已保存）", "Built-in defaults restored (saved)")
	st.statusErr = false
}

// buildPresetsPage renders the effective list (custom first, built-in defaults
// when unset) with reorder/delete controls, the add form and the restore action.
func buildPresetsPage(c *ui.Context, store contracts.SettingsService, st *presetsState, locale contracts.LocaleService) {
	t := design.Of(c)
	st.load(store, locale)
	st.loadAgentPresets(store)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, localized(locale, PresetsPageTitle, "Presets"), localized(locale,
			"提示词预设：可插入 composer 的提示词/人设模板；修改后立即保存。",
			"Prompt presets: prompt/persona templates the composer can insert; edits save immediately."))

		if st.parseErr != "" {
			design.Apply(ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start), func(el ui.Element) ui.Element {
				return el
			}).Children(func() {
				th := t.Theme()
				ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).TextColor(t.Danger)
				design.Apply(ui.Text(c, localized(locale, "配置解析失败：", "Failed to parse configuration: ")+st.parseErr),
					t.Font(design.TypeXS)).TextColor(t.DangerText)
			})
		}

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "预设列表", "Presets"))
			if st.usingLegacy {
				design.Apply(ui.Text(c, localized(locale,
					"内置默认（保存后自定义生效）", "Built-in defaults (save to customize)")),
					t.TextMuted(design.TypeXXS))
			}
			if len(st.presets) == 0 {
				design.Apply(ui.Text(c, localized(locale, "还没有预设", "No presets yet")),
					t.TextMuted(design.TypeXS))
				return
			}
			for index, preset := range st.presets {
				buildPresetRow(c, t, store, st, locale, index, preset)
			}
		})

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "新增预设", "Add preset"))
			field(c, t, localized(locale, "名称", "Name"), func() {
				ui.TextInput(c, &st.name).
					Label(inputLabel(locale, localized(locale, "预设名称", "Preset name"))).
					Placeholder(localized(locale, "例如：简洁回答", "e.g. Concise answers")).
					FillWidth()
			})
			field(c, t, localized(locale, "提示词", "Prompt"), func() {
				ui.TextArea(c, &st.prompt).
					Label(inputLabel(locale, localized(locale, "预设提示词", "Preset prompt"))).
					Placeholder(localized(locale, "输入要插入的提示词…", "Prompt text to insert…")).
					MinHeight(72).FillWidth()
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if brandPrimaryButton(c, t, icons.Plus(), localized(locale, "添加", "Add")).Clicked() {
					st.addPreset(store, locale)
				}
				restore := ui.Button(c, localized(locale, "恢复内置默认", "Restore built-in defaults"))
				design.Apply(restore, secondaryButtonStyle(t)).Tooltip(localized(locale,
					"清空自定义列表，重新使用内置默认。", "Clear the custom list and use the built-in defaults again."))
				if restore.Clicked() {
					st.restoreBuiltIns(store, locale)
				}
				ui.Spacer(c).Grow(1)
				if st.status != "" {
					statusBadge(c, t, st.status, st.statusErr)
				}
			})
		})

		// Agent presets: a compact section with a modal add/edit form (the page
		// is already long; a modal keeps the controls reachable) and inline
		// reorder/delete rows.
		panel(c, t, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				sectionTitle(c, t, localized(locale, "代理预设", "Agent presets"))
				ui.Spacer(c).Grow(1)
				if brandPrimaryButton(c, t, icons.Plus(), localized(locale, "添加代理预设", "Add agent preset")).Clicked() {
					st.agentFormOpen = true
					st.agentName, st.agentPersona, st.agentPrompt, st.agentTools, st.agentModel = "", "", "", "", ""
					st.status = ""
				}
				if ui.Button(c, localized(locale, "恢复默认人设", "Restore default persona presets")).Clicked() {
					st.restoreAgentPresets(store, locale)
				}
				if st.status != "" {
					statusBadge(c, t, st.status, st.statusErr)
				}
			})
			design.Apply(ui.Text(c, localized(locale,
				"人设会追加到系统提示末尾；工具允许列表为空表示不过滤；模型是会话默认值（会话覆盖优先）。",
				"Persona is appended to the system prompt; an empty tool allowlist keeps every tool; model is the session default (a session override wins).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			if len(st.agentPresets) == 0 {
				design.Apply(ui.Text(c, localized(locale, "还没有代理预设", "No agent presets yet")),
					t.TextMuted(design.TypeXS))
				return
			}
			for index, preset := range st.agentPresets {
				buildAgentPresetRow(c, t, store, st, locale, index, preset)
			}
		})
		buildAgentPresetDialog(c, t, store, st, locale)
	})
}

// buildPresetRow renders one preset with its two-line prompt summary and the
// native move/delete actions.
func buildPresetRow(c *ui.Context, t *design.Tokens, store contracts.SettingsService, st *presetsState,
	locale contracts.LocaleService, index int, preset contracts.PromptPreset) {
	design.Apply(ui.Row(c), func(el ui.Element) ui.Element {
		return el.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}).FillWidth().Gap(design.Space2).AlignItems(ui.Center).
		Padding(design.Space2, design.Space1).Children(func() {
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(strings.TrimSpace(preset.Name), localized(locale, "(未命名预设)", "(Untitled preset)"))),
				t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
			summary := strings.TrimSpace(preset.Prompt)
			if summary == "" {
				summary = localized(locale, "(空提示词)", "(Empty prompt)")
			}
			design.Apply(ui.Text(c, summary), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
		})
		up := ui.Button(c, localized(locale, "上移", "Up"))
		design.Apply(up, secondaryButtonStyle(t)).Tooltip(localized(locale, "上移一位", "Move up"))
		if up.Clicked() {
			st.movePreset(store, locale, index, -1)
		}
		down := ui.Button(c, localized(locale, "下移", "Down"))
		design.Apply(down, secondaryButtonStyle(t)).Tooltip(localized(locale, "下移一位", "Move down"))
		if down.Clicked() {
			st.movePreset(store, locale, index, 1)
		}
		remove := ui.Button(c, localized(locale, "删除", "Delete"))
		design.Apply(remove, secondaryButtonStyle(t)).Tooltip(localized(locale, "删除该预设", "Delete this preset"))
		if remove.Clicked() {
			st.deletePreset(store, locale, index)
		}
	})
}

// buildAgentPresetDialog renders the modal add form for one agent preset.
func buildAgentPresetDialog(c *ui.Context, t *design.Tokens, store contracts.SettingsService, st *presetsState,
	locale contracts.LocaleService) {
	if !st.agentFormOpen {
		return
	}
	ui.Modal(c, &st.agentFormOpen, func() {
		design.Apply(ui.Column(c).Width(620).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "新增代理预设", "New agent preset")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
			ui.TextInput(c, &st.agentName).
				Label(inputLabel(locale, localized(locale, "人设名称", "Persona name"))).FillWidth()
			ui.TextArea(c, &st.agentPersona).
				Label(inputLabel(locale, localized(locale, "人设", "Persona"))).
				Placeholder(localized(locale, "追加到系统提示的人设前缀", "Persona prefix appended to the system prompt")).
				MinHeight(design.Space6 * 2).FillWidth()
			ui.TextArea(c, &st.agentPrompt).
				Label(inputLabel(locale, localized(locale, "人设提示词", "Persona prompt"))).
				MinHeight(design.Space6 * 2).FillWidth()
			ui.TextInput(c, &st.agentTools).
				Label(inputLabel(locale, localized(locale, "允许工具（逗号分隔）", "Allowed tools (comma separated)"))).
				Placeholder("read_file, bash").FillWidth()
			ui.TextInput(c, &st.agentModel).
				Label(inputLabel(locale, localized(locale, "默认模型", "Default model"))).FillWidth()
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				if brandPrimaryButton(c, t, icons.Plus(), localized(locale, "保存人设", "Save persona")).Clicked() {
					st.addAgentPreset(store, locale)
					if st.statusErr == false && st.status != "" {
						st.agentFormOpen = false
					}
				}
				if ui.Button(c, localized(locale, "取消", "Cancel")).Clicked() {
					st.agentFormOpen = false
					st.status = ""
				}
			})
		})
	})
}

// buildAgentPresetRow renders one configured agent preset with its summary,
// tool/model badges and move/delete actions.
func buildAgentPresetRow(c *ui.Context, t *design.Tokens, store contracts.SettingsService, st *presetsState,
	locale contracts.LocaleService, index int, preset contracts.AgentPreset) {
	design.Apply(ui.Row(c), func(el ui.Element) ui.Element {
		return el.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}).FillWidth().Gap(design.Space2).AlignItems(ui.Center).
		Padding(design.Space2, design.Space1).Children(func() {
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(strings.TrimSpace(preset.Name), localized(locale, "(未命名人设)", "(Untitled persona)"))),
				t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
			summary := strings.TrimSpace(preset.Persona)
			if summary == "" {
				summary = strings.TrimSpace(preset.Prompt)
			}
			if summary != "" {
				design.Apply(ui.Text(c, presetSummary(summary)), t.TextMuted(design.TypeXXS)).MaxLines(1)
			}
			ui.Row(c).Gap(design.Space2).Children(func() {
				if len(preset.Tools) > 0 {
					design.Apply(ui.Badge(c, localized(locale, "工具：", "Tools: ")+strings.Join(preset.Tools, ", ")),
						t.Badge()).TextColor(t.LabelSecondary)
				}
				if model := strings.TrimSpace(preset.Model); model != "" {
					design.Apply(ui.Badge(c, localized(locale, "模型：", "Model: ")+model),
						t.Badge()).TextColor(t.LabelSecondary)
				}
			})
		})
		up := ui.Button(c, localized(locale, "上移", "Up"))
		if up.Clicked() {
			st.moveAgentPreset(store, locale, index, -1)
		}
		down := ui.Button(c, localized(locale, "下移", "Down"))
		if down.Clicked() {
			st.moveAgentPreset(store, locale, index, 1)
		}
		remove := ui.Button(c, localized(locale, "删除", "Delete"))
		if remove.Clicked() {
			st.deleteAgentPreset(store, locale, index)
		}
	})
}

// presetSummary truncates a preset persona/prompt for one summary line.
func presetSummary(text string) string {
	runes := []rune(strings.TrimSpace(text))
	const limit = 80
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
