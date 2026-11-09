package settings

// Model catalog editor: the native Modal behind each catalog row's 编辑 action.
// It edits a model's family (which picks the built-in thinking modes), its
// default thinking mode, its custom thinking modes and its generation options
// (temperature, top_p, max tokens), then writes the whole list through
// writeCatalogModels so the catalog module (and the composer menus) see the
// change immediately.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// catalogEditDraft is the editor modal's UI-thread draft of one catalog model.
// The model ID is immutable: it is the row key the conversation menus store.
type catalogEditDraft struct {
	id              string
	name            string
	note            string
	contextWindow   string
	family          string
	defaultThinking string
	temperature     string
	topP            string
	maxTokens       string
	modes           []*catalogModeDraft
	err             string
}

// catalogModeDraft is one custom thinking mode being edited.
type catalogModeDraft struct {
	id     string
	name   string
	params string // JSON object text; "{}" (or empty) sends no fields
}

// errCatalogParamsNotObject reports parameters that are valid JSON but not an
// object (a list, string, number, boolean or null).
var errCatalogParamsNotObject = errors.New("parameters must be a JSON object")

// catalogEnglish reports whether mode names should render in English.
func catalogEnglish(locale contracts.LocaleService) bool {
	return localized(locale, "zh", "en") == "en"
}

// catalogFamilyLabel returns the localized label of one model family.
func catalogFamilyLabel(locale contracts.LocaleService, family string) string {
	switch strings.TrimSpace(family) {
	case contracts.ModelFamilyQwen:
		return localized(locale, "千问", "Qwen")
	case contracts.ModelFamilyDeepSeek:
		return "DeepSeek"
	case contracts.ModelFamilyCustom:
		return localized(locale, "自定义", "Custom")
	default:
		return localized(locale, "其他", "Other")
	}
}

// catalogFamilyOptions renders the four selectable families in display order.
func catalogFamilyOptions(locale contracts.LocaleService) []string {
	families := contracts.ThinkingFamilies()
	options := make([]string, 0, len(families))
	for _, family := range families {
		options = append(options, catalogFamilyLabel(locale, family))
	}
	return options
}

// catalogFamilyValue maps a family select label back to its family constant
// ("" when the label belongs to another language's options).
func catalogFamilyValue(locale contracts.LocaleService, label string) string {
	for _, family := range contracts.ThinkingFamilies() {
		if catalogFamilyLabel(locale, family) == label {
			return family
		}
	}
	return ""
}

// catalogAddFamily is the add form's family: the user's explicit pick once they
// choose one, otherwise inferred from the typed model ID.
func catalogAddFamily(st *catalogState) string {
	if st.familyTouched {
		if family := strings.TrimSpace(st.addFamily); contracts.ValidThinkingFamily(family) && family != "" {
			return family
		}
	}
	return contracts.InferThinkingFamily(st.id)
}

// catalogModeDisplayName localizes one thinking mode's display label.
func catalogModeDisplayName(locale contracts.LocaleService, mode contracts.ThinkingMode) string {
	if name := strings.TrimSpace(mode.DisplayName(catalogEnglish(locale))); name != "" {
		return name
	}
	return mode.ID
}

// catalogModelDefaultThinkingLabel renders a model's configured default thinking
// mode; an empty value follows the family default.
func catalogModelDefaultThinkingLabel(locale contracts.LocaleService, model contracts.ModelInfo) string {
	id := strings.TrimSpace(model.DefaultThinking)
	if id == "" {
		return localized(locale, "跟随系列默认", "Family default")
	}
	if mode, ok := contracts.ThinkingModeByID(contracts.ThinkingModesForModel(model), id); ok {
		return catalogModeDisplayName(locale, mode)
	}
	return id
}

// catalogThinkingSummary is the catalog row's muted metadata line: effective
// family, default thinking mode and custom-mode count.
func catalogThinkingSummary(locale contracts.LocaleService, model contracts.ModelInfo) string {
	parts := []string{
		localized(locale, "系列：", "Family: ") +
			catalogFamilyLabel(locale, contracts.EffectiveThinkingFamily(model)),
		localized(locale, "思考：", "Thinking: ") + catalogModelDefaultThinkingLabel(locale, model),
	}
	if count := len(contracts.SanitizeThinkingModes(model.ThinkingModes)); count == 1 {
		parts = append(parts, localized(locale, "自定义模式 1", "1 custom mode"))
	} else if count > 1 {
		parts = append(parts, localized(locale,
			fmt.Sprintf("自定义模式 %d", count), fmt.Sprintf("%d custom modes", count)))
	}
	return strings.Join(parts, " · ")
}

// formatCatalogNumber renders a sampling value without trailing zeros: 0.7,
// 0.3, 2, 0.
func formatCatalogNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// catalogGenerationSummary renders a row's configured generation options,
// clamped to the valid ranges for display; empty when none are configured.
func catalogGenerationSummary(locale contracts.LocaleService, model contracts.ModelInfo) string {
	temperature, topP, maxTokens := contracts.SanitizeGeneration(
		model.Temperature, model.TopP, model.MaxTokens)
	parts := make([]string, 0, 3)
	if temperature != nil {
		parts = append(parts, localized(locale, "温度 ", "temperature ")+formatCatalogNumber(*temperature))
	}
	if topP != nil {
		parts = append(parts, "Top P "+formatCatalogNumber(*topP))
	}
	if maxTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", maxTokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return localized(locale, "生成：", "Generation: ") + strings.Join(parts, " · ")
}

// newCatalogEditDraft prefills the editor from one catalog row.
func newCatalogEditDraft(model contracts.ModelInfo) *catalogEditDraft {
	draft := &catalogEditDraft{
		id:              model.ID,
		name:            model.Name,
		note:            model.Note,
		family:          contracts.EffectiveThinkingFamily(model),
		defaultThinking: strings.TrimSpace(model.DefaultThinking),
	}
	if model.ContextWindow > 0 {
		draft.contextWindow = strconv.Itoa(model.ContextWindow)
	}
	if model.Temperature != nil {
		draft.temperature = formatCatalogNumber(*model.Temperature)
	}
	if model.TopP != nil {
		draft.topP = formatCatalogNumber(*model.TopP)
	}
	if model.MaxTokens > 0 {
		draft.maxTokens = strconv.Itoa(model.MaxTokens)
	}
	for _, mode := range contracts.SanitizeThinkingModes(model.ThinkingModes) {
		draft.modes = append(draft.modes, &catalogModeDraft{
			id:     mode.ID,
			name:   mode.DisplayName(false),
			params: encodeCatalogModeParams(mode.Params),
		})
	}
	return draft
}

// catalogDraftModelInfo builds the model entry the thinking helpers resolve
// against while the editor is open (valid IDs only; names are a preview).
func catalogDraftModelInfo(draft *catalogEditDraft) contracts.ModelInfo {
	modes := make([]contracts.ThinkingMode, 0, len(draft.modes))
	for _, mode := range draft.modes {
		id := strings.TrimSpace(mode.id)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(mode.name)
		modes = append(modes, contracts.ThinkingMode{ID: id, NameZH: name, NameEN: name})
	}
	return contracts.ModelInfo{
		ID:              draft.id,
		Family:          draft.family,
		DefaultThinking: draft.defaultThinking,
		ThinkingModes:   contracts.SanitizeThinkingModes(modes),
	}
}

// catalogDefaultThinkingOptions lists the default-thinking select: the empty
// "follow the family default" option first, then the model's effective modes.
// Labels are disambiguated by ID when two modes share a display name.
func catalogDefaultThinkingOptions(locale contracts.LocaleService,
	modes []contracts.ThinkingMode) (labels, ids []string) {
	labels = []string{localized(locale, "跟随系列默认", "Family default")}
	ids = []string{""}
	seen := make(map[string]bool, len(modes))
	for _, mode := range modes {
		label := catalogModeDisplayName(locale, mode)
		if seen[label] {
			label += " (" + mode.ID + ")"
		}
		seen[label] = true
		labels = append(labels, label)
		ids = append(ids, mode.ID)
	}
	return labels, ids
}

// encodeCatalogModeParams renders mode parameters as the textarea's JSON text.
func encodeCatalogModeParams(params map[string]any) string {
	if len(params) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// parseCatalogModeParams decodes the textarea's JSON: empty text means no
// parameters, otherwise the value must be a JSON object.
func parseCatalogModeParams(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errCatalogParamsNotObject
	}
	if len(object) == 0 {
		return nil, nil
	}
	return object, nil
}

// validateCatalogModeDrafts validates the custom-mode rows and returns the
// sanitized mode list. Rows left entirely empty are ignored (the editor starts
// one blank row); partially filled rows with a missing ID, duplicate IDs and
// non-object parameters return an inline error message instead.
func validateCatalogModeDrafts(modes []*catalogModeDraft,
	locale contracts.LocaleService) ([]contracts.ThinkingMode, string) {
	out := make([]contracts.ThinkingMode, 0, len(modes))
	seen := make(map[string]bool, len(modes))
	for index, mode := range modes {
		id := strings.ToLower(strings.TrimSpace(mode.id))
		name := strings.TrimSpace(mode.name)
		paramsText := strings.TrimSpace(mode.params)
		if id == "" {
			if name == "" && (paramsText == "" || paramsText == "{}") {
				continue
			}
			return nil, localized(locale,
				fmt.Sprintf("第 %d 个思考模式的 ID 不能为空", index+1),
				fmt.Sprintf("Thinking mode %d needs an ID", index+1))
		}
		if seen[id] {
			return nil, localized(locale,
				fmt.Sprintf("思考模式 ID 重复：%s", id),
				fmt.Sprintf("Duplicate thinking mode ID: %s", id))
		}
		seen[id] = true
		params, err := parseCatalogModeParams(paramsText)
		if err != nil {
			if errors.Is(err, errCatalogParamsNotObject) {
				return nil, localized(locale,
					fmt.Sprintf("思考模式 %s 的参数必须是 JSON 对象", id),
					fmt.Sprintf("Thinking mode %s parameters must be a JSON object", id))
			}
			return nil, localized(locale,
				fmt.Sprintf("思考模式 %s 的参数不是有效 JSON：%v", id, err),
				fmt.Sprintf("Thinking mode %s parameters are not valid JSON: %v", id, err))
		}
		out = append(out, contracts.ThinkingMode{ID: id, NameZH: name, NameEN: name, Params: params})
	}
	return contracts.SanitizeThinkingModes(out), ""
}

// validateCatalogGeneration parses the optional generation drafts. Empty fields
// mean "provider default"; a malformed or out-of-range value returns an inline
// error message (the draft itself is kept so the user can fix it). The result
// goes through contracts.SanitizeGeneration, the same clamping the catalog
// module applies when it reads the row.
func validateCatalogGeneration(draft *catalogEditDraft,
	locale contracts.LocaleService) (temperature, topP *float64, maxTokens int, errText string) {
	if raw := strings.TrimSpace(draft.temperature); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, nil, 0, localized(locale,
				"温度必须是数字", "Temperature must be a number")
		}
		if value < 0 || value > 2 {
			return nil, nil, 0, localized(locale,
				"温度必须在 0–2 之间", "Temperature must be between 0 and 2")
		}
		temperature = &value
	}
	if raw := strings.TrimSpace(draft.topP); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, nil, 0, localized(locale,
				"Top P 必须是数字", "Top P must be a number")
		}
		if value < 0 || value > 1 {
			return nil, nil, 0, localized(locale,
				"Top P 必须在 0–1 之间", "Top P must be between 0 and 1")
		}
		topP = &value
	}
	if raw := strings.TrimSpace(draft.maxTokens); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, nil, 0, localized(locale,
				"最大输出 tokens 必须是整数", "Max output tokens must be an integer")
		}
		if value <= 0 {
			return nil, nil, 0, localized(locale,
				"最大输出 tokens 必须大于 0", "Max output tokens must be greater than 0")
		}
		maxTokens = value
	}
	temperature, topP, maxTokens = contracts.SanitizeGeneration(temperature, topP, maxTokens)
	return temperature, topP, maxTokens, ""
}

// catalogDraftFloat parses one optional numeric draft, only when it is valid
// and inside [min,max]; used by the collapsed advanced summary.
func catalogDraftFloat(raw string, min, max float64) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < min || value > max {
		return 0, false
	}
	return value, true
}

// catalogAdvancedSummary renders the one-line summary shown while the advanced
// group is collapsed: the configured generation values plus the custom-mode
// count, or a hint that nothing is configured.
func catalogAdvancedSummary(locale contracts.LocaleService, draft *catalogEditDraft) string {
	generation := make([]string, 0, 3)
	if value, ok := catalogDraftFloat(draft.temperature, 0, 2); ok {
		generation = append(generation,
			localized(locale, "温度 ", "temperature ")+formatCatalogNumber(value))
	}
	if value, ok := catalogDraftFloat(draft.topP, 0, 1); ok {
		generation = append(generation, "Top P "+formatCatalogNumber(value))
	}
	if raw := strings.TrimSpace(draft.maxTokens); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			generation = append(generation, fmt.Sprintf("%d tokens", value))
		}
	}
	custom := 0
	for _, mode := range draft.modes {
		if strings.TrimSpace(mode.id) != "" {
			custom++
		}
	}
	line := ""
	if len(generation) > 0 {
		line = localized(locale, "生成：", "Generation: ") + strings.Join(generation, " · ")
	}
	if custom == 1 {
		if line != "" {
			line += " · "
		}
		line += localized(locale, "自定义模式 1", "1 custom mode")
	} else if custom > 1 {
		if line != "" {
			line += " · "
		}
		line += localized(locale,
			fmt.Sprintf("自定义模式 %d", custom), fmt.Sprintf("%d custom modes", custom))
	}
	if line == "" {
		return localized(locale, "未配置生成参数或自定义模式",
			"No generation options or custom modes")
	}
	return line
}

// buildCatalogEditDialog renders the editor modal. The model ID stays read-only;
// family, default mode and custom modes are edited as drafts and saved together
// so a partially edited row never reaches the catalog.
func buildCatalogEditDialog(c *ui.Context, t *design.Tokens, store contracts.SettingsService,
	models []contracts.ModelInfo, st *catalogState, locale contracts.LocaleService) {
	draft := st.edit
	if draft == nil {
		return
	}
	ui.Modal(c, &st.editorOpen, func() {
		design.Apply(ui.Column(c).Width(680).Gap(design.Space2), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "编辑模型", "Edit model")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, localized(locale,
				"保存后立即生效。", "Changes apply immediately.")),
				t.TextMuted(design.TypeXS)).MaxLines(1)

			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型名称", "Model name"), func() {
						ui.TextInput(c, &draft.name).
							Label(inputLabel(locale, localized(locale, "模型名称", "Model name"))).
							Placeholder(localized(locale, "可选；留空时使用模型 ID",
								"optional; falls back to the model ID")).FillWidth()
					})
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型 ID", "Model ID"), func() {
						ui.Row(c).Gap(design.Space2).Children(func() {
							design.Apply(ui.Text(c, draft.id), t.Font(design.TypeXS), t.MonoFamily()).
								TextColor(t.LabelSecondary)
							design.Apply(ui.Text(c, localized(locale, "不可修改", "read-only")),
								t.TextMuted(design.TypeXXS))
						})
					})
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型备注", "Model note"), func() {
						ui.TextInput(c, &draft.note).
							Label(inputLabel(locale, localized(locale, "模型备注", "Model note"))).
							Placeholder(localized(locale, "可选", "optional")).FillWidth()
					})
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型上下文窗口", "Model context window"), func() {
						ui.TextInput(c, &draft.contextWindow).
							Label(inputLabel(locale, localized(locale,
								"模型上下文窗口", "Model context window"))).
							Placeholder("128000 tokens").FillWidth()
					})
				})
			})

			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "思考系列", "Thinking family"), func() {
						choice := catalogFamilyLabel(locale, draft.family)
						selector := ui.Select(c.Key("catalog.editFamily"), &choice,
							catalogFamilyOptions(locale)).
							Label(localized(locale, "思考系列菜单", "Thinking family menu"))
						if selector.Changed() {
							if family := catalogFamilyValue(locale, choice); family != "" && family != draft.family {
								draft.family = family
								// A default mode the new family no longer offers
								// goes back to "follow the family default".
								if draft.defaultThinking != "" {
									if _, ok := contracts.ThinkingModeByID(
										contracts.ThinkingModesForModel(catalogDraftModelInfo(draft)),
										draft.defaultThinking); !ok {
										draft.defaultThinking = ""
									}
								}
								c.Invalidate()
							}
						}
					})
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "默认思考", "Default thinking"), func() {
						labels, ids := catalogDefaultThinkingOptions(locale,
							contracts.ThinkingModesForModel(catalogDraftModelInfo(draft)))
						current := 0
						for index, id := range ids {
							if id != "" && strings.EqualFold(strings.TrimSpace(draft.defaultThinking), id) {
								current = index
								break
							}
						}
						choice := labels[current]
						selector := ui.Select(c.Key("catalog.editDefault"), &choice, labels).
							Label(localized(locale, "默认思考菜单", "Default thinking menu"))
						if selector.Changed() {
							for index, label := range labels {
								if label == choice {
									draft.defaultThinking = ids[index]
									break
								}
							}
							c.Invalidate()
						}
					})
				})
			})
			design.Apply(ui.Text(c, localized(locale,
				"系列决定内置思考模式；「跟随系列默认」跟随系列。",
				"The family selects the built-in modes; Family default follows it.")),
				t.TextMuted(design.TypeXS)).MaxLines(1)

			// Progressive disclosure: generation options and custom thinking
			// modes are advanced settings, collapsed by default behind a one-line
			// summary so the modal's common fields stay short.
			ui.Collapsible(c.Key("catalog.editAdvanced"),
				localized(locale, "生成与自定义模式", "Generation & custom modes"), &st.editAdvanced, func() {
					// Generation options: optional numeric fields; every empty
					// field means "provider default".
					design.Apply(ui.Text(c, localized(locale, "生成参数", "Generation")),
						t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
					ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							ui.Field(c, localized(locale, "温度 0–2", "Temperature 0–2"), func() {
								ui.TextInput(c, &draft.temperature).
									Label(inputLabel(locale, localized(locale, "温度", "Temperature"))).
									Placeholder(localized(locale, "服务商默认", "provider default")).FillWidth()
							})
						})
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							ui.Field(c, "Top P 0–1", func() {
								ui.TextInput(c, &draft.topP).
									Label(inputLabel(locale, "Top P")).
									Placeholder(localized(locale, "服务商默认", "provider default")).FillWidth()
							})
						})
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							ui.Field(c, localized(locale, "最大输出 tokens", "Max output tokens"), func() {
								ui.TextInput(c, &draft.maxTokens).
									Label(inputLabel(locale, localized(locale, "最大输出 tokens", "Max output tokens"))).
									Placeholder(localized(locale, "服务商默认", "provider default")).FillWidth()
							})
						})
					})

					ui.Divider(c)
					design.Apply(ui.Text(c, localized(locale, "自定义思考模式", "Custom thinking modes")),
						t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
					design.Apply(ui.Text(c, localized(locale,
						"每行一个模式；ID 与内置模式同名时覆盖内置模式。",
						"One mode per row; matching a built-in ID overrides it.")),
						t.TextMuted(design.TypeXS)).MaxLines(1)

					if len(draft.modes) == 0 {
						design.Apply(ui.Text(c, localized(locale, "还没有自定义模式。",
							"No custom thinking modes yet.")), t.TextMuted(design.TypeXS))
					} else {
						removed := -1
						scroll := ui.Scroll(c).Height(130).FillWidth()
						scroll.Children(func() {
							for index, mode := range draft.modes {
								index, mode := index, mode
								if index > 0 {
									ui.Divider(c)
								}
								ui.Row(c).FillWidth().Gap(design.Space2).
									AlignItems(ui.Start).PaddingY(design.Space1).Children(func() {
									ui.Column(c).Width(150).Grow(0).Shrink(0).Children(func() {
										ui.TextInput(c.Key("catalog.mode."+strconv.Itoa(index)+".id"), &mode.id).
											Label(localized(locale, "模式 ", "Mode ") + strconv.Itoa(index+1) + " ID").
											Placeholder("thinking-low").FillWidth()
									})
									ui.Column(c).Width(150).Grow(0).Shrink(0).Children(func() {
										ui.TextInput(c.Key("catalog.mode."+strconv.Itoa(index)+".name"), &mode.name).
											Label(localized(locale, "模式 ", "Mode ") + strconv.Itoa(index+1) +
												localized(locale, " 名称", " name")).
											Placeholder(localized(locale, "低强度", "Low")).FillWidth()
									})
									ui.Column(c).FillWidth().Grow(1).Children(func() {
										ui.TextArea(c.Key("catalog.mode."+strconv.Itoa(index)+".params"), &mode.params).
											Label(localized(locale, "模式 ", "Mode ") + strconv.Itoa(index+1) +
												localized(locale, " 参数", " params")).
											Placeholder(`{"reasoning_effort": "low"}`).
											FillWidth().MinHeight(design.Space4 * 2)
									})
									if design.Apply(ui.Button(c, localized(locale, "删除", "Remove")),
										t.DangerButton()).
										Label(localized(locale, "删除思考模式 ", "Remove thinking mode ") + strconv.Itoa(index+1)).
										Tooltip(localized(locale, "删除这个思考模式", "Remove this thinking mode")).
										Clicked() {
										removed = index
									}
								})
							}
						})
						if removed >= 0 {
							removedID := strings.ToLower(strings.TrimSpace(draft.modes[removed].id))
							draft.modes = append(draft.modes[:removed], draft.modes[removed+1:]...)
							if removedID != "" && strings.EqualFold(
								strings.TrimSpace(draft.defaultThinking), removedID) {
								draft.defaultThinking = ""
							}
							c.Invalidate()
						}
					}
					ui.Row(c).FillWidth().Children(func() {
						if design.Apply(ui.Button(c, localized(locale, "添加思考模式", "Add thinking mode")),
							t.SecondaryButton()).Clicked() {
							draft.modes = append(draft.modes, &catalogModeDraft{params: "{}"})
							c.Invalidate()
						}
					})
				})
			if !st.editAdvanced {
				design.Apply(ui.Text(c, catalogAdvancedSummary(locale, draft)),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(1)
			}
			if draft.err != "" {
				uikit.ErrorLine(c, draft.err)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
					st.editorOpen = false
					st.edit = nil
					c.Invalidate()
				}},
				{Label: localized(locale, "保存模型", "Save model"), Primary: true, OnClick: func() {
					saveCatalogEdit(c, st, store, models, locale)
				}},
			})
		})
	})
	if !st.editorOpen {
		// Escape or a backdrop click dismissed the modal without saving.
		st.edit = nil
	}
}

// saveCatalogEdit validates the draft and replaces the row in the catalog. The
// write goes through writeCatalogModels, which the modelcatalog module reads on
// every call, so the change applies immediately. Failures keep the modal open
// with an inline danger line.
func saveCatalogEdit(c *ui.Context, st *catalogState, store contracts.SettingsService,
	models []contracts.ModelInfo, locale contracts.LocaleService) {
	draft := st.edit
	if draft == nil {
		return
	}
	family := strings.TrimSpace(draft.family)
	if family == "" || !contracts.ValidThinkingFamily(family) {
		draft.err = localized(locale, "请选择有效的模型系列", "Select a valid model family")
		c.Invalidate()
		return
	}
	thinkingModes, modeErr := validateCatalogModeDrafts(draft.modes, locale)
	if modeErr != "" {
		draft.err = modeErr
		c.Invalidate()
		return
	}
	temperature, topP, maxTokens, generationErr := validateCatalogGeneration(draft, locale)
	if generationErr != "" {
		draft.err = generationErr
		c.Invalidate()
		return
	}
	updated := contracts.ModelInfo{
		ID:              draft.id,
		Name:            strings.TrimSpace(draft.name),
		Note:            strings.TrimSpace(draft.note),
		ContextWindow:   parseContextWindow(draft.contextWindow),
		Family:          family,
		DefaultThinking: strings.TrimSpace(draft.defaultThinking),
		ThinkingModes:   thinkingModes,
		Temperature:     temperature,
		TopP:            topP,
		MaxTokens:       maxTokens,
	}
	if updated.DefaultThinking != "" {
		if _, ok := contracts.ThinkingModeByID(
			contracts.ThinkingModesForModel(updated), updated.DefaultThinking); !ok {
			// The selected mode was removed or renamed while editing; the picker
			// already shows the family default, so save that state.
			updated.DefaultThinking = ""
		}
	}
	// Replace in place so the row keeps its position in the composer menu.
	next := make([]contracts.ModelInfo, 0, len(models))
	replaced := false
	for _, model := range models {
		if !replaced && strings.EqualFold(strings.TrimSpace(model.ID), strings.TrimSpace(draft.id)) {
			next = append(next, updated)
			replaced = true
			continue
		}
		next = append(next, model)
	}
	if !replaced {
		draft.err = localized(locale, "模型已不在目录中，请重新打开编辑",
			"The model is no longer in the catalog; reopen the editor")
		c.Invalidate()
		return
	}
	if err := writeCatalogModels(store, next); err != nil {
		draft.err = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		c.Invalidate()
		return
	}
	st.editorOpen = false
	st.edit = nil
	st.status, st.err = localized(locale, "已保存", "Saved"), false
	c.Invalidate()
}
