package settings

// Model settings page: the model list is the single source of truth for the
// models the composer offers. The page writes the modelcatalog.models settings
// row (which the modelcatalog backend reads on every call), so list edits apply
// immediately; the page footer Save only persists the prompt and the runtime
// options. Every row carries its own provider connection (BaseURL/APIKey), so a
// list can mix providers, and one row is the default model: the page mirrors it
// into chat.model/chat.baseURL/chat.apiKey for the consumers that predate the
// multi-model list. The mirror write is transactional (AUDIT-P3-15): a failed
// write rolls the already-written keys back, so the page can never leave
// chat.* pointing at a model the list does not contain.

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// catalogProviderCustomID is the custom provider preset's stable ID: a service
// that is not one of the built-in presets.
const catalogProviderCustomID = "custom"

// catalogState is the model section's UI-thread state: the list selection, the
// add/edit dialog drafts and the transient status line. The drafts live across
// frames; the persisted list itself is always re-read from the settings store.
type catalogState struct {
	// The 模型列表 collection is the official List: keyed by model ID, named by
	// the model name/ID and single-row selectable for the keyboard; the inline
	// edit/remove buttons stay the row actions.
	list ui.ListState
	sel  int

	// status/err are the section's last save result.
	status string
	err    bool

	// addOpen/add drive the add-model modal; add is nil while it is closed.
	addOpen bool
	add     *catalogAddDraft

	// editorOpen/edit drive the per-model editor modal; edit is nil while it is
	// closed.
	editorOpen bool
	edit       *catalogEditDraft

	// removeOpen is the removal confirmation's open flag (persistent for
	// uikit.ConfirmDanger); confirmRemove is the model ID being removed.
	removeOpen bool
	// confirmRemove is the model ID whose removal confirmation is open.
	confirmRemove string

	// editAdvanced expands the editor's advanced group (generation options and
	// custom thinking modes); it starts collapsed.
	editAdvanced bool
	// modeList is the official ui.List state of the custom thinking-mode rows
	// (identity follows the mode ID; empty IDs fall back to their index).
	modeList ui.ListState
}

// discardTransient throws the section's transient UI state away: an open
// dialog, a pending confirmation and the last status line. The host calls it
// when the settings surface is closed with "Discard".
func (st *catalogState) discardTransient() {
	st.addOpen = false
	st.add = nil
	st.editorOpen = false
	st.edit = nil
	st.confirmRemove = ""
	st.status = ""
	st.err = false
	st.editAdvanced = false
}

// catalogModels reads the user's model list from the settings row. The product
// ships no built-in models: an unset row is an empty list with ok=true.
// Malformed JSON returns an empty list and ok=false; the page reports it while
// the add action can still rewrite the row.
func catalogModels(store contracts.SettingsService) ([]contracts.ModelInfo, bool) {
	if store == nil {
		return nil, true
	}
	models, err := contracts.LoadModelCatalog(store)
	if err != nil {
		return nil, false
	}
	return contracts.SanitizeModelInfoList(models), true
}

// writeCatalogModels persists the whole list as the modelcatalog.models JSON
// row ("[]" is a real empty list, not "unset").
func writeCatalogModels(store contracts.SettingsService, models []contracts.ModelInfo) error {
	encoded, err := contracts.EncodeModelInfoList(contracts.SanitizeModelInfoList(models))
	if err != nil {
		return err
	}
	return store.Set(contracts.SettingModelCatalogModels, encoded)
}

// catalogDefaultMirror is the default row's connection as the legacy chat.*
// consumers read it.
type catalogDefaultMirror struct {
	ModelID string
	BaseURL string
	APIKey  string
}

// mirrorForModel builds the mirror of one model row; an empty ID clears the
// mirror (there is no default model).
func mirrorForModel(model contracts.ModelInfo) catalogDefaultMirror {
	return catalogDefaultMirror{
		ModelID: strings.TrimSpace(model.ID),
		BaseURL: strings.TrimRight(strings.TrimSpace(model.BaseURL), "/"),
		APIKey:  strings.TrimSpace(model.APIKey),
	}
}

// catalogSettingWrite is one settings key and the value to restore it to.
type catalogSettingWrite struct{ key, value string }

// mirrorWrites renders the three mirror rows in write order.
func mirrorWrites(mirror catalogDefaultMirror) []catalogSettingWrite {
	return []catalogSettingWrite{
		{contracts.SettingsChatModel, mirror.ModelID},
		{contracts.SettingsChatBaseURL, mirror.BaseURL},
		{contracts.SettingsChatAPIKey, mirror.APIKey},
	}
}

// rollbackSettingsWrites restores keys in reverse write order (best effort: the
// original failure is what the caller reports).
func rollbackSettingsWrites(store contracts.SettingsService, writes []catalogSettingWrite) {
	for index := len(writes) - 1; index >= 0; index-- {
		_ = store.Set(writes[index].key, writes[index].value)
	}
}

// persistCatalogModels persists the whole list and, when mirror is non-nil, the
// default-model mirror. The mirror is written first so a failed list write can
// restore it; both halves roll back their own writes, so list and mirror never
// disagree about which model is the default.
func persistCatalogModels(store contracts.SettingsService, models []contracts.ModelInfo,
	mirror *catalogDefaultMirror) error {
	previous := make([]catalogSettingWrite, 0, 3)
	if mirror != nil {
		for _, write := range mirrorWrites(*mirror) {
			old := store.Get(write.key, "")
			if err := store.Set(write.key, write.value); err != nil {
				rollbackSettingsWrites(store, previous)
				return err
			}
			previous = append(previous, catalogSettingWrite{key: write.key, value: old})
		}
	}
	if err := writeCatalogModels(store, models); err != nil {
		rollbackSettingsWrites(store, previous)
		return err
	}
	return nil
}

// catalogKeepDefaultMirror returns the mirror to rewrite when the stored
// default (chat.model) is not one of the listed models anymore — a stale value
// from an older configuration or a removed row — or nil when the existing
// mirror is still valid. It normalizes the mirror on every mutation instead of
// leaving chat.model pointing at a model the list does not contain.
func catalogKeepDefaultMirror(store contracts.SettingsService, next []contracts.ModelInfo) *catalogDefaultMirror {
	stored := strings.TrimSpace(store.Get(contracts.SettingsChatModel, ""))
	if _, ok := contracts.FindModelInfo(next, stored); ok {
		return nil
	}
	if entry, ok := contracts.DefaultModelEntry(next, store); ok {
		mirror := mirrorForModel(entry)
		return &mirror
	}
	return &catalogDefaultMirror{}
}

// catalogIsDefault reports whether id is the effective default model row: the
// row chat.model points at, or the first row when chat.model is unset/unknown.
func catalogIsDefault(store contracts.SettingsService, models []contracts.ModelInfo, id string) bool {
	return strings.EqualFold(contracts.DefaultModelID(models, store), strings.TrimSpace(id))
}

// catalogHasModelID reports whether id already exists in the list
// (case-insensitive).
func catalogHasModelID(models []contracts.ModelInfo, id string) bool {
	_, ok := contracts.FindModelInfo(models, id)
	return ok
}

// normalizeCatalogURL normalizes one endpoint for provider matching and API-key
// inheritance (trailing slash and letter case ignored).
func normalizeCatalogURL(value string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
}

// inheritedCatalogAPIKey returns the API key of the first listed model with the
// same Base URL, so adding a second model under a provider the user already
// configured does not require re-typing the key. Empty means none was found.
func inheritedCatalogAPIKey(models []contracts.ModelInfo, baseURL string) string {
	target := normalizeCatalogURL(baseURL)
	if target == "" {
		return ""
	}
	for _, model := range models {
		if normalizeCatalogURL(model.BaseURL) != target {
			continue
		}
		if key := strings.TrimSpace(model.APIKey); key != "" {
			return key
		}
	}
	return ""
}

// catalogProviderName renders a row's provider: the preset whose endpoint
// matches, otherwise the custom-service label.
func catalogProviderName(locale contracts.LocaleService, model contracts.ModelInfo) string {
	if preset, ok := contracts.MatchProviderPreset(model.BaseURL); ok {
		return preset.DisplayName(catalogEnglish(locale))
	}
	return localized(locale, "自定义服务", "Custom service")
}

// catalogKeyState renders the row's credential state; the key itself is never
// displayed.
func catalogKeyState(locale contracts.LocaleService, model contracts.ModelInfo) string {
	if strings.TrimSpace(model.APIKey) != "" {
		return localized(locale, "密钥已设置", "Key set")
	}
	return localized(locale, "密钥未设置", "No key")
}

// catalogProviderOptions renders the provider selector's localized labels and
// their stable preset IDs, in the built-in display order (the custom entry is
// last).
func catalogProviderOptions(locale contracts.LocaleService) (labels, ids []string) {
	presets := contracts.ModelProviderPresets()
	labels = make([]string, 0, len(presets))
	ids = make([]string, 0, len(presets))
	for _, preset := range presets {
		labels = append(labels, preset.DisplayName(catalogEnglish(locale)))
		ids = append(ids, preset.ID)
	}
	return labels, ids
}

// catalogProviderLabel resolves one provider ID to its localized label.
func catalogProviderLabel(locale contracts.LocaleService, providerID string) string {
	if preset, ok := contracts.ProviderPresetByID(providerID); ok {
		return preset.DisplayName(catalogEnglish(locale))
	}
	if preset, ok := contracts.ProviderPresetByID(catalogProviderCustomID); ok {
		return preset.DisplayName(catalogEnglish(locale))
	}
	return providerID
}

// catalogProviderFromURL resolves the provider selector's value from the draft
// Base URL: a preset match wins, anything else is the custom entry.
func catalogProviderFromURL(baseURL string) string {
	if preset, ok := contracts.MatchProviderPreset(baseURL); ok {
		return preset.ID
	}
	return catalogProviderCustomID
}

// applyCatalogProviderPreset fills a dialog's connection fields from a chosen
// provider: the base URL is replaced and, for the add dialog only (modelID is
// nil in the editor, whose ID is immutable), an empty model ID gets the
// preset's example. Choosing the custom entry keeps the typed URL.
func applyCatalogProviderPreset(baseURL, modelID *string, providerID string) {
	preset, ok := contracts.ProviderPresetByID(providerID)
	if !ok || preset.IsCustom() {
		return
	}
	if baseURL != nil {
		*baseURL = preset.BaseURL
	}
	if modelID != nil && strings.TrimSpace(*modelID) == "" {
		*modelID = preset.ExampleModel
	}
}

// buildCatalogProviderField renders the shared provider selector of the add and
// edit dialogs: choosing a preset fills the endpoint (and the example model ID
// while the add dialog's ID is empty); typing an endpoint that matches a preset
// selects it again.
func buildCatalogProviderField(c *ui.Context, locale contracts.LocaleService, providerID,
	baseURL, modelID *string) {
	ui.Field(c, localized(locale, "服务商", "Provider"), func() {
		labels, ids := catalogProviderOptions(locale)
		choice := catalogProviderLabel(locale, *providerID)
		selector := ui.Select(c.Key("catalog.provider"), &choice, labels).
			Label(localized(locale, "服务商菜单", "Provider menu"))
		if !selector.Changed() {
			return
		}
		for index, label := range labels {
			if label != choice {
				continue
			}
			*providerID = ids[index]
			applyCatalogProviderPreset(baseURL, modelID, *providerID)
			c.Invalidate()
			return
		}
	})
}

// buildCatalogBaseURLField renders the shared Base URL input. Typing an
// endpoint that matches a provider preset selects that provider.
func buildCatalogBaseURLField(c *ui.Context, locale contracts.LocaleService, providerID,
	baseURL *string) {
	ui.Field(c, "Base URL", func() {
		input := ui.TextInput(c, baseURL).
			Label(inputLabel(locale, "Base URL")).
			Placeholder("https://api.deepseek.com/v1").
			FillWidth()
		if input.Changed() {
			*providerID = catalogProviderFromURL(*baseURL)
		}
	})
}

// buildCatalogContextWindowField renders the shared context-window number
// input; 0 means unknown.
func buildCatalogContextWindowField(c *ui.Context, locale contracts.LocaleService, key string,
	text *string, number *float64) {
	ui.Field(c, localized(locale, "上下文窗口", "Context window"), func() {
		// Official number input: typed values apply only in range, and 0 is the
		// documented "unknown" value (mygo number-input.md).
		input := ui.NumberInput(c.Key(key), number, 0, 10000000, 1000).
			Label(inputLabel(locale, localized(locale, "上下文窗口", "Context window")))
		if input.Changed() {
			*text = strconv.Itoa(int(*number))
		}
	}).Description(localized(locale,
		"模型可用的 token 数；0 表示未知。", "Tokens the model can take; 0 means unknown."))
}

// buildCatalogSetDefaultField renders the "set as default model" check box of
// the add/edit dialogs. A disabled box means the choice is fixed (the first
// model, or the row that already is the default).
func buildCatalogSetDefaultField(c *ui.Context, t *design.Tokens, locale contracts.LocaleService,
	value *bool, disabled bool, hint string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		box := ui.Checkbox(c, value, localized(locale, "设为默认模型", "Set as default model"))
		box.Disabled(disabled)
		design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(1)
	})
}

// buildModelCatalogSection renders the model list inside the merged 模型 page.
// The list is the control surface itself: add/edit/remove apply at once. The
// page footer Save owns only the prompt and runtime drafts.
func buildModelCatalogSection(c *ui.Context, runtime contracts.UIRuntime, store contracts.SettingsService,
	st *catalogState, locale contracts.LocaleService, fallbackBaseURL, fallbackAPIKey, effectiveModel string) {
	t := design.Of(c)
	models, ok := catalogModels(store)
	ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
		uikit.SectionAction(c, localized(locale, "模型列表（即时生效）", "Models (apply immediately)"),
			func(*ui.Context) {
				// Adding a model is a section action, not a page primary: the page's
				// one filled accent button is the footer's Save (R12).
				if design.Apply(ui.Button(c, localized(locale, "添加模型", "Add model")),
					t.SecondaryButton()).Clicked() {
					st.openCatalogAdd(models)
					c.Invalidate()
				}
			}, func(*ui.Context) {
				if !ok {
					uikit.ErrorLine(c, localized(locale,
						"模型配置无法解析；添加模型会重写该配置。",
						"The model-list setting is invalid; adding a model rewrites it."))
				}
				if len(models) == 0 {
					if ok {
						uikit.EmptyState(c, localized(locale, "还没有模型", "No models yet"))
					}
				} else {
					// The models are a collection: the official List keys the rows
					// by model ID, names them and owns the keyboard selection; the
					// inline edit/remove buttons keep their click semantics.
					bindSettingsList(&st.list, &st.sel, len(models), func(index int) any {
						if index < 0 || index >= len(models) {
							return nil
						}
						return models[index].ID
					}, func(index int) string {
						if index < 0 || index >= len(models) {
							return ""
						}
						return firstNonEmpty(strings.TrimSpace(models[index].Name), models[index].ID)
					})
					ui.List(c, &st.list, len(models)).
						Label(localized(locale, "模型列表", "Model list")).
						Gap(design.Space1).
						Rows(func(row ui.ListRow) {
							buildModelCatalogRow(row.Context, t, store, st, models, models[row.Index], locale, row.Selected())
						})
				}
				if st.status != "" {
					statusBadgesRow(c, t, st.status, st.err)
				}
				if st.confirmRemove != "" {
					buildCatalogRemoveDialog(c, store, st, models, locale)
				}
			})
		if st.addOpen {
			buildCatalogAddDialog(c, t, runtime, store, models, st, locale, fallbackBaseURL, fallbackAPIKey)
		}
		if st.editorOpen {
			buildCatalogEditDialog(c, t, runtime, store, models, st, locale, fallbackBaseURL, fallbackAPIKey)
		}
	})
}

// buildModelCatalogRow renders one model-list row: the name with the default
// badge, the model ID and its provider connection, the credential/context/
// thinking metadata, and the inline edit/remove actions. The official List owns
// the row paint and the choice, so the selected row's copy uses the accent's
// on-color.
func buildModelCatalogRow(c *ui.Context, t *design.Tokens, store contracts.SettingsService,
	st *catalogState, models []contracts.ModelInfo, model contracts.ModelInfo,
	locale contracts.LocaleService, selected bool) {
	primary, secondary, tertiary := listRowTextColors(t, selected)
	id := strings.TrimSpace(model.ID)
	baseURL := strings.TrimRight(strings.TrimSpace(model.BaseURL), "/")
	ui.Row(c).FillWidth().Padding(design.Space1, design.Space2).
		AlignItems(ui.Start).Gap(design.Space2).Children(func() {
		ui.Column(c).Gap(design.Space1).Grow(1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, firstNonEmpty(strings.TrimSpace(model.Name), id)),
					t.Font(design.TypeS)).FontWeight(design.WeightSemibold).TextColor(primary)
				if catalogIsDefault(store, models, id) {
					label := localized(locale, "默认", "Default")
					if selected {
						uikit.OnAccentBadge(c, label, t)
					} else {
						design.Apply(ui.Badge(c, label), t.Badge())
					}
				}
			})
			// Row metadata: the model ID, its provider and the endpoint.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, id), t.Font(design.TypeXS), t.MonoFamily()).
					TextColor(secondary)
				design.Apply(ui.Text(c, catalogProviderName(locale, model)),
					t.Font(design.TypeXXS)).TextColor(secondary)
				design.Apply(ui.Text(c, firstNonEmpty(baseURL,
					localized(locale, "未设置地址", "no base URL"))),
					t.Font(design.TypeXXS), t.MonoFamily()).TextColor(tertiary).
					MaxLines(1).Grow(1)
			})
			// Credential, context window and thinking configuration: metadata
			// only, the key itself is never displayed.
			meta := []string{catalogKeyState(locale, model)}
			if model.ContextWindow > 0 {
				size := formatContextWindow(model.ContextWindow)
				meta = append(meta, localized(locale, "上下文 "+size, size+" context"))
			}
			meta = append(meta, catalogThinkingSummary(locale, model))
			design.Apply(ui.Text(c, strings.Join(meta, " · ")),
				t.Font(design.TypeXXS)).TextColor(tertiary).MaxLines(2)
			if generation := catalogGenerationSummary(locale, model); generation != "" {
				design.Apply(ui.Text(c, generation),
					t.Font(design.TypeXXS)).TextColor(tertiary).MaxLines(1)
			}
		})
		if design.Apply(ui.Button(c, localized(locale, "编辑", "Edit")),
			t.SecondaryButton()).
			Label(localized(locale, "编辑模型 ", "Edit model ") + id).
			Tooltip(localized(locale, "编辑模型与它的服务商连接",
				"Edit the model and its provider connection")).Clicked() {
			st.edit = newCatalogEditDraft(model, catalogIsDefault(store, models, id))
			st.openCatalogEdit()
			c.Invalidate()
		}
		// The destructive remove lives behind the row's ⋯ menu (danger item), so
		// a list of models never paints one red button per row (DESIGN.md §5.2
		// danger budget; the shared ListRow keeps one trailing menu).
		label := localized(locale, "模型操作", "Model actions")
		menu := uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "model-actions/" + id,
			Label:   label + " " + id,
			Tooltip: label,
			Icon:    icons.More(),
			Size:    design.IconSizeS,
		})
		menu.PopoverMenuButton(func(m *ui.Menu) {
			remove := m.Item(localized(locale, "删除模型", "Remove model"))
			remove.Danger()
			if remove.Chosen() {
				st.confirmRemove = id
				st.removeOpen = true
				c.Invalidate()
			}
		})
	})
}

// formatContextWindow renders a token count for the list: exact thousands as
// "128k", other positive values with one decimal.
func formatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens%1000 == 0 {
		return strconv.Itoa(tokens/1000) + "k"
	}
	return strconv.FormatFloat(float64(tokens)/1000, 'f', 1, 64) + "k"
}

// parseContextWindow parses the optional context-window draft: a malformed,
// empty or non-positive value becomes 0 (not displayed and not budgeted).
func parseContextWindow(raw string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

// openCatalogAdd opens the add-model dialog. Adding to an empty list always
// creates the default model, so the default box starts checked.
func (st *catalogState) openCatalogAdd(models []contracts.ModelInfo) {
	st.add = &catalogAddDraft{
		providerID: catalogProviderCustomID,
		setDefault: len(models) == 0,
	}
	st.addOpen = true
}

// openCatalogEdit opens the per-model editor; the advanced group starts
// collapsed.
func (st *catalogState) openCatalogEdit() {
	st.editorOpen = true
	st.editAdvanced = false
}

// catalogAddDraft is the add-model dialog's UI-thread draft.
type catalogAddDraft struct {
	id            string
	name          string
	baseURL       string
	apiKey        string
	contextWindow string
	// contextWindowNum mirrors the official ui.NumberInput (0 = unknown).
	contextWindowNum float64
	providerID       string
	// family is the thinking family the added model gets; it is inferred from
	// the typed model ID until the user picks one explicitly (familyTouched).
	family        string
	familyTouched bool
	// setDefault mirrors the "set as default model" check box.
	setDefault bool
	err        string
	// check is the dialog's inline connection-test state.
	check modelCheckState
}

// catalogAddFamily is the add form's family: the user's explicit pick once they
// choose one, otherwise inferred from the typed model ID.
func catalogAddFamily(draft *catalogAddDraft) string {
	if draft.familyTouched {
		if family := strings.TrimSpace(draft.family); contracts.ValidThinkingFamily(family) && family != "" {
			return family
		}
	}
	return contracts.InferThinkingFamily(draft.id)
}

// buildCatalogAddDialog renders the add-model modal: the provider selector
// prefills the endpoint and an example model ID, the endpoint and key are the
// model's own connection, and the dialog tests the current draft before adding.
func buildCatalogAddDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	store contracts.SettingsService, models []contracts.ModelInfo, st *catalogState,
	locale contracts.LocaleService, fallbackBaseURL, fallbackAPIKey string) {
	draft := st.add
	if draft == nil {
		return
	}
	submit := func() { addCatalogModel(st, store, models, locale) }
	ui.Modal(c, &st.addOpen, func() {
		design.Apply(ui.Column(c).Width(design.DialogWidthXL).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "添加模型", "Add model")),
				t.Font(design.TypeL)).FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)

			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					buildCatalogProviderField(c, locale, &draft.providerID, &draft.baseURL, &draft.id)
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型 ID", "Model ID"), func() {
						input := ui.TextInput(c, &draft.id).
							Label(inputLabel(locale, localized(locale, "模型 ID", "Model ID"))).
							Placeholder("deepseek-chat").FillWidth().
							OnTextKey(uikit.EnterTo(submit))
						if input.Changed() {
							c.Invalidate()
						}
					})
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "名称", "Name"), func() {
						ui.TextInput(c, &draft.name).
							Label(inputLabel(locale, localized(locale, "名称", "Name"))).
							Placeholder(localized(locale, "可选；留空时使用模型 ID",
								"optional; falls back to the model ID")).FillWidth().
							OnTextKey(uikit.EnterTo(submit))
					})
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					buildCatalogBaseURLField(c, locale, &draft.providerID, &draft.baseURL)
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, "API Key", func() {
						ui.TextInput(c, &draft.apiKey).
							Label(inputLabel(locale, "API Key")).
							Password().
							Placeholder(localized(locale, "可留空", "optional")).FillWidth().
							OnTextKey(uikit.EnterTo(submit))
					}).Description(localized(locale,
						"留空时继承同一 Base URL 已有模型的密钥；密钥只保存在本机。",
						"Blank inherits the key of an existing model with the same base URL; the key is stored locally only."))
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					buildCatalogContextWindowField(c, locale, "catalog.add.contextWindow",
						&draft.contextWindow, &draft.contextWindowNum)
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型系列", "Model family"), func() {
						choice := catalogFamilyLabel(locale, catalogAddFamily(draft))
						selector := ui.Select(c.Key("catalog.addFamily"), &choice,
							catalogFamilyOptions(locale)).
							Label(localized(locale, "模型系列菜单", "Model family menu"))
						if selector.Changed() {
							if family := catalogFamilyValue(locale, choice); family != "" {
								draft.family, draft.familyTouched = family, true
							}
							c.Invalidate()
						}
					}).Description(localized(locale,
						"决定内置的思考模式。",
						"Selects the built-in thinking modes."))
				})
				ui.Column(c).FillWidth().Grow(1).Justify(ui.End).Children(func() {
					hint := localized(locale, "对话输入框默认使用该模型。",
						"The composer uses it as its default model.")
					disabled := false
					if len(models) == 0 {
						hint = localized(locale, "第一个模型会自动设为默认。",
							"The first model becomes the default.")
						disabled = true
					}
					buildCatalogSetDefaultField(c, t, locale, &draft.setDefault, disabled, hint)
				})
			})

			testAPIKey := strings.TrimSpace(draft.apiKey)
			testBaseURL := firstNonEmpty(strings.TrimRight(strings.TrimSpace(draft.baseURL), "/"), fallbackBaseURL)
			if testAPIKey == "" {
				testAPIKey = firstNonEmpty(inheritedCatalogAPIKey(models, draft.baseURL), fallbackAPIKey)
			}
			buildCatalogCheckRow(c, t, runtime, &draft.check, testBaseURL, testAPIKey, locale)
			if draft.err != "" {
				uikit.ErrorLine(c, draft.err)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
					st.addOpen = false
					st.add = nil
					c.Invalidate()
				}},
				{Label: localized(locale, "添加", "Add"), Primary: true, OnClick: func() {
					submit()
					c.Invalidate()
				}},
			})
		})
	})
	if !st.addOpen {
		// Escape or a backdrop click dismissed the modal without adding.
		st.add = nil
	}
}

// addCatalogModel validates the add draft, persists it together with the
// default-model mirror it requires, and closes the dialog on success. A failure
// keeps the modal open with an inline danger line.
func addCatalogModel(st *catalogState, store contracts.SettingsService,
	models []contracts.ModelInfo, locale contracts.LocaleService) {
	draft := st.add
	if draft == nil {
		return
	}
	id := strings.TrimSpace(draft.id)
	if id == "" {
		draft.err = localized(locale, "模型 ID 不能为空", "Model ID is required")
		return
	}
	if catalogHasModelID(models, id) {
		draft.err = localized(locale, "模型 ID 已存在", "Model ID already exists")
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(draft.baseURL), "/")
	if baseURL == "" {
		draft.err = localized(locale, "Base URL 不能为空", "Base URL is required")
		return
	}
	apiKey := strings.TrimSpace(draft.apiKey)
	if apiKey == "" {
		// A second model on an already-configured provider inherits its key.
		apiKey = inheritedCatalogAPIKey(models, baseURL)
	}
	model := contracts.ModelInfo{
		ID:            id,
		Name:          strings.TrimSpace(draft.name),
		ContextWindow: parseContextWindow(draft.contextWindow),
		BaseURL:       baseURL,
		APIKey:        apiKey,
		Family:        catalogAddFamily(draft),
	}
	next := append(append([]contracts.ModelInfo{}, models...), model)
	mirror := catalogKeepDefaultMirror(store, next)
	if draft.setDefault {
		selected := mirrorForModel(model)
		mirror = &selected
	}
	if err := persistCatalogModels(store, next, mirror); err != nil {
		draft.err = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		return
	}
	st.addOpen, st.add = false, nil
	st.status, st.err = localized(locale, "已添加模型", "Model added"), false
}

// buildCatalogRemoveDialog renders the removal confirmation with the native
// alert dialog. Removing the default row promotes the first remaining row (or
// clears the mirror when the list becomes empty), so the default model mirror
// never points at a removed row.
func buildCatalogRemoveDialog(c *ui.Context, store contracts.SettingsService,
	st *catalogState, models []contracts.ModelInfo, locale contracts.LocaleService) {
	id := st.confirmRemove
	uikit.ConfirmDanger(c, &st.removeOpen, uikit.ConfirmDangerOptions{
		Key:     "model.remove",
		Title:   localized(locale, "删除模型", "Remove model"),
		Message: localized(locale, "确认从模型列表删除「", "Remove ") + id + localized(locale, "」？", " from the model list?"),
		Confirm: localized(locale, "确认删除", "Confirm remove"),
		Cancel:  localized(locale, "取消", "Cancel"),
		OnConfirm: func() {
			removeCatalogModel(st, store, models, id, locale)
			c.Invalidate()
		},
	})
	if !st.removeOpen {
		st.confirmRemove = ""
	}
}

// removeCatalogModel drops one row and rewrites the list; a removed default row
// promotes the first remaining row, and an emptied list clears the mirror.
func removeCatalogModel(st *catalogState, store contracts.SettingsService,
	models []contracts.ModelInfo, id string, locale contracts.LocaleService) {
	remaining := make([]contracts.ModelInfo, 0, len(models))
	removed := false
	for _, model := range models {
		if !removed && strings.EqualFold(strings.TrimSpace(model.ID), strings.TrimSpace(id)) {
			removed = true
			continue
		}
		remaining = append(remaining, model)
	}
	if !removed {
		st.confirmRemove = ""
		st.status, st.err = localized(locale, "模型已不在列表中",
			"The model is no longer in the list"), true
		return
	}
	wasDefault := catalogIsDefault(store, models, id)
	mirror := catalogKeepDefaultMirror(store, remaining)
	if wasDefault {
		if entry, ok := contracts.DefaultModelEntry(remaining, store); ok {
			selected := mirrorForModel(entry)
			mirror = &selected
		} else {
			mirror = &catalogDefaultMirror{}
		}
	}
	if err := persistCatalogModels(store, remaining, mirror); err != nil {
		st.status, st.err = localized(locale, "保存失败：", "Save failed: ")+err.Error(), true
		st.confirmRemove = ""
		return
	}
	st.confirmRemove = ""
	st.status, st.err = localized(locale, "已删除模型", "Model removed"), false
}
