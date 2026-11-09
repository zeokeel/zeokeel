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
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Presets page constants. Section "general" keeps it in the same settings
// section as the other utility pages; Order 98 places it after Network (97)
// and before the default About page.
const (
	PresetsPageID      = "presets"
	PresetsPageSection = "常用"
	PresetsPageOrder   = 30
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

	// confirmKind/confirmIndex name the destructive action waiting for the
	// native confirmation dialog ("", "delete-preset", "delete-agent",
	// "restore-builtins", "restore-agents").
	confirmKind  string
	confirmIndex int
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
// The page stays one page with instant-save rows; restoring the built-ins and
// deleting a preset overwrite/remove user data, so both ask for confirmation.
func buildPresetsPage(c *ui.Context, store contracts.SettingsService, st *presetsState, locale contracts.LocaleService) {
	t := design.Of(c)
	st.load(store, locale)
	st.loadAgentPresets(store)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, PresetsPageTitle, "Presets"), localized(locale,
			"提示词预设：可插入对话输入框的提示词/人设模板；修改后立即保存。",
			"Prompt presets: prompt/persona templates the conversation composer can insert; edits save immediately."))

		if st.parseErr != "" {
			uikit.ErrorLine(c, localized(locale,
				"配置解析失败：", "Failed to parse configuration: ")+st.parseErr)
		}

		uikit.SectionAction(c, localized(locale, "预设列表", "Presets"), func(c *ui.Context) {
			if design.Apply(ui.Button(c,
				localized(locale, "恢复内置默认", "Restore built-in defaults")),
				t.DangerButton()).Tooltip(localized(locale,
				"清空自定义列表，重新使用内置默认。",
				"Clear the custom list and use the built-in defaults again.")).Clicked() {
				st.confirmKind = "restore-builtins"
				c.Invalidate()
			}
		}, func(*ui.Context) {
			if st.usingLegacy {
				design.Apply(ui.Text(c, localized(locale,
					"内置默认（保存后自定义生效）", "Built-in defaults (save to customize)")),
					t.TextMuted(design.TypeXXS))
			}
			if len(st.presets) == 0 {
				uikit.EmptyState(c, localized(locale, "还没有预设", "No presets yet"))
				return
			}
			for index, preset := range st.presets {
				if index > 0 {
					ui.Divider(c)
				}
				buildPresetRow(c, t, store, st, locale, index, preset)
			}
		})

		uikit.Section(c, localized(locale, "新增预设", "Add preset"), func(*ui.Context) {
			ui.Field(c, localized(locale, "名称", "Name"), func() {
				name := ui.TextInput(c, &st.name).
					Label(inputLabel(locale, localized(locale, "预设名称", "Preset name"))).
					Placeholder(localized(locale, "例如：简洁回答", "e.g. Concise answers")).
					FillWidth()
				// Enter in the name adds the preset, as in a desktop form
				// (mygo text-input.md: Submitted is Enter in a text input).
				if name.Submitted() {
					st.addPreset(store, locale)
					c.Invalidate()
				}
			}).Description(localized(locale, "在对话输入框的预设菜单中显示的名称。",
				"The name shown in the conversation composer's preset menu."))
			ui.Field(c, localized(locale, "提示词", "Prompt"), func() {
				ui.TextArea(c, &st.prompt).
					Label(inputLabel(locale, localized(locale, "预设提示词", "Preset prompt"))).
					Placeholder(localized(locale, "输入要插入的提示词…", "Prompt text to insert…")).
					MinHeight(72).FillWidth()
			}).Description(localized(locale, "插入到对话输入框的文本。",
				"The text inserted into the conversation composer."))
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				uikit.DialogFooter(c, []uikit.DialogAction{{
					Label: localized(locale, "添加", "Add"), Primary: true,
					OnClick: func() {
						st.addPreset(store, locale)
						c.Invalidate()
					},
				}})
			})
			statusBadgesRow(c, t, st.status, st.statusErr)
		})

		// Agent presets: a section with a modal add form (the page is already
		// long; a modal keeps the controls reachable) and inline reorder rows.
		uikit.SectionAction(c, localized(locale, "代理预设", "Agent presets"), func(c *ui.Context) {
			if design.Apply(ui.Button(c,
				localized(locale, "恢复默认人设", "Restore default persona presets")),
				t.DangerButton()).Tooltip(localized(locale,
				"覆盖当前人设列表，恢复内置默认。",
				"Overwrite the current persona list with the built-in defaults.")).Clicked() {
				st.confirmKind = "restore-agents"
				c.Invalidate()
			}
		}, func(*ui.Context) {
			design.Apply(ui.Text(c, localized(locale,
				"人设会追加到系统提示末尾；工具允许列表为空表示不过滤；模型是会话默认值（会话覆盖优先）。",
				"Persona is appended to the system prompt; an empty tool allowlist keeps every tool; model is the session default (a session override wins).")),
				t.TextMuted(design.TypeXXS)).MaxLines(3)
			if len(st.agentPresets) == 0 {
				uikit.EmptyState(c, localized(locale, "还没有代理预设", "No agent presets yet"))
			}
			for index, preset := range st.agentPresets {
				if index > 0 {
					ui.Divider(c)
				}
				buildAgentPresetRow(c, t, store, st, locale, index, preset)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				uikit.DialogFooter(c, []uikit.DialogAction{{
					Label: localized(locale, "添加代理预设", "Add agent preset"), Primary: true,
					OnClick: func() {
						st.agentFormOpen = true
						st.agentName, st.agentPersona, st.agentPrompt, st.agentTools, st.agentModel = "", "", "", "", ""
						st.status = ""
						c.Invalidate()
					},
				}})
			})
		})

		buildAgentPresetDialog(c, t, store, st, locale)

		if st.confirmKind != "" {
			buildPresetConfirmDialog(c, t, store, st, locale)
		}
	})
}

// buildPresetConfirmDialog renders the native confirmation for the destructive
// preset actions and dispatches the confirmed one.
func buildPresetConfirmDialog(c *ui.Context, t *design.Tokens, store contracts.SettingsService,
	st *presetsState, locale contracts.LocaleService) {
	title := localized(locale, "确认操作", "Confirm action")
	message := localized(locale, "此操作无法撤销。", "This cannot be undone.")
	cancel := localized(locale, "取消", "Cancel")
	confirm := localized(locale, "确认", "Confirm")
	kind, index := st.confirmKind, st.confirmIndex
	switch kind {
	case "delete-preset":
		title = localized(locale, "删除预设", "Delete preset")
		message = localized(locale, "确认删除该提示词预设？此操作无法撤销。",
			"Delete this prompt preset? This cannot be undone.")
		confirm = localized(locale, "确认删除", "Confirm delete")
	case "delete-agent":
		title = localized(locale, "删除代理预设", "Delete agent preset")
		message = localized(locale, "确认删除该代理预设？此操作无法撤销。",
			"Delete this agent preset? This cannot be undone.")
		confirm = localized(locale, "确认删除", "Confirm delete")
	case "restore-builtins":
		title = localized(locale, "恢复内置默认", "Restore built-in defaults")
		message = localized(locale, "恢复内置默认会覆盖当前的自定义预设列表。此操作无法撤销。",
			"Restoring the built-in defaults overwrites the current custom preset list. This cannot be undone.")
		confirm = localized(locale, "恢复默认", "Restore defaults")
	case "restore-agents":
		title = localized(locale, "恢复默认人设", "Restore default persona presets")
		message = localized(locale, "恢复默认人设会覆盖当前的代理预设列表。此操作无法撤销。",
			"Restoring the default persona presets overwrites the current agent preset list. This cannot be undone.")
		confirm = localized(locale, "恢复默认", "Restore defaults")
	}
	open := true
	switch ui.AlertDialog(c, &open, title, message, cancel, confirm) {
	case 1:
		switch kind {
		case "delete-preset":
			st.deletePreset(store, locale, index)
		case "delete-agent":
			st.deleteAgentPreset(store, locale, index)
		case "restore-builtins":
			st.restoreBuiltIns(store, locale)
		case "restore-agents":
			st.restoreAgentPresets(store, locale)
		}
		st.confirmKind = ""
		c.Invalidate()
	}
	if !open {
		st.confirmKind = ""
	}
}

// buildPresetRow renders one preset with its two-line prompt summary and the
// native move/delete actions.
func buildPresetRow(c *ui.Context, t *design.Tokens, store contracts.SettingsService, st *presetsState,
	locale contracts.LocaleService, index int, preset contracts.PromptPreset) {
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).
		PaddingY(design.Space1).Children(func() {
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(strings.TrimSpace(preset.Name),
				localized(locale, "(未命名预设)", "(Untitled preset)"))),
				t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
			summary := strings.TrimSpace(preset.Prompt)
			if summary == "" {
				summary = localized(locale, "(空提示词)", "(Empty prompt)")
			}
			design.Apply(ui.Text(c, summary), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
		})
		if design.Apply(ui.Button(c, localized(locale, "上移", "Up")),
			t.SecondaryButton()).Tooltip(localized(locale, "上移一位", "Move up")).Clicked() {
			st.movePreset(store, locale, index, -1)
			c.Invalidate()
		}
		if design.Apply(ui.Button(c, localized(locale, "下移", "Down")),
			t.SecondaryButton()).Tooltip(localized(locale, "下移一位", "Move down")).Clicked() {
			st.movePreset(store, locale, index, 1)
			c.Invalidate()
		}
		if design.Apply(ui.Button(c, localized(locale, "删除", "Delete")),
			t.DangerButton()).Tooltip(localized(locale,
			"删除该预设", "Delete this preset")).Clicked() {
			st.confirmKind, st.confirmIndex = "delete-preset", index
			c.Invalidate()
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
		design.Apply(ui.Column(c).Width(design.DialogWidthL).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "新增代理预设", "New agent preset")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
			submitted := false
			ui.Field(c, localized(locale, "人设名称", "Persona name"), func() {
				name := ui.TextInput(c, &st.agentName).
					Label(inputLabel(locale, localized(locale, "人设名称", "Persona name"))).FillWidth()
				if name.Submitted() {
					submitted = true
				}
			}).Description(localized(locale, "在代理预设列表中显示的名称。",
				"The name shown in the agent preset list."))
			ui.Field(c, localized(locale, "人设", "Persona"), func() {
				ui.TextArea(c, &st.agentPersona).
					Label(inputLabel(locale, localized(locale, "人设", "Persona"))).
					Placeholder(localized(locale, "追加到系统提示的人设前缀",
						"Persona prefix appended to the system prompt")).
					MinHeight(design.Space6 * 2).FillWidth()
			}).Description(localized(locale, "追加到系统提示末尾的一段文字。",
				"Text appended to the end of the system prompt."))
			ui.Field(c, localized(locale, "人设提示词", "Persona prompt"), func() {
				ui.TextArea(c, &st.agentPrompt).
					Label(inputLabel(locale, localized(locale, "人设提示词", "Persona prompt"))).
					MinHeight(design.Space6 * 2).FillWidth()
			}).Description(localized(locale, "该人设默认使用的系统提示词。",
				"The system prompt this persona uses by default."))
			ui.Field(c, localized(locale, "允许工具（逗号分隔）", "Allowed tools (comma separated)"), func() {
				tools := ui.TextInput(c, &st.agentTools).
					Label(inputLabel(locale, localized(locale, "允许工具（逗号分隔）",
						"Allowed tools (comma separated)"))).
					Placeholder("read_file, bash").FillWidth()
				if tools.Submitted() {
					submitted = true
				}
			}).Description(localized(locale, "留空表示不过滤工具。", "Empty keeps every tool."))
			ui.Field(c, localized(locale, "默认模型", "Default model"), func() {
				model := ui.TextInput(c, &st.agentModel).
					Label(inputLabel(locale, localized(locale, "默认模型", "Default model"))).FillWidth()
				if model.Submitted() {
					submitted = true
				}
			}).Description(localized(locale, "留空沿用当前模型。", "Empty keeps the current model."))
			if submitted {
				st.addAgentPreset(store, locale)
				if !st.statusErr && st.status != "" {
					st.agentFormOpen = false
				}
				c.Invalidate()
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
					st.agentFormOpen = false
					st.status = ""
					c.Invalidate()
				}},
				{Label: localized(locale, "保存人设", "Save persona"), Primary: true, OnClick: func() {
					st.addAgentPreset(store, locale)
					if !st.statusErr && st.status != "" {
						st.agentFormOpen = false
					}
					c.Invalidate()
				}},
			})
		})
	})
}

// buildAgentPresetRow renders one configured agent preset with its summary,
// tool/model badges and move/delete actions.
func buildAgentPresetRow(c *ui.Context, t *design.Tokens, store contracts.SettingsService, st *presetsState,
	locale contracts.LocaleService, index int, preset contracts.AgentPreset) {
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).
		PaddingY(design.Space1).Children(func() {
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(strings.TrimSpace(preset.Name),
				localized(locale, "(未命名人设)", "(Untitled persona)"))),
				t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
			summary := strings.TrimSpace(preset.Persona)
			if summary == "" {
				summary = strings.TrimSpace(preset.Prompt)
			}
			if summary != "" {
				design.Apply(ui.Text(c, presetSummary(summary)), t.TextMuted(design.TypeXXS)).MaxLines(1)
			}
			meta := make([]string, 0, 2)
			if len(preset.Tools) > 0 {
				meta = append(meta, localized(locale, "工具：", "Tools: ")+strings.Join(preset.Tools, ", "))
			}
			if model := strings.TrimSpace(preset.Model); model != "" {
				meta = append(meta, localized(locale, "模型：", "Model: ")+model)
			}
			if len(meta) > 0 {
				design.Apply(ui.Text(c, strings.Join(meta, " · ")),
					t.TextMuted(design.TypeXXS)).MaxLines(1)
			}
		})
		if design.Apply(ui.Button(c, localized(locale, "上移", "Up")),
			t.SecondaryButton()).Clicked() {
			st.moveAgentPreset(store, locale, index, -1)
			c.Invalidate()
		}
		if design.Apply(ui.Button(c, localized(locale, "下移", "Down")),
			t.SecondaryButton()).Clicked() {
			st.moveAgentPreset(store, locale, index, 1)
			c.Invalidate()
		}
		if design.Apply(ui.Button(c, localized(locale, "删除", "Delete")),
			t.DangerButton()).Clicked() {
			st.confirmKind, st.confirmIndex = "delete-agent", index
			c.Invalidate()
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
