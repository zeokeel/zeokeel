package settings

// Model catalog editor: the native Modal behind each model row's 编辑 action.
// It edits the model's own provider connection (provider, Base URL, API key,
// context window), the "set as default model" choice, and — in the collapsed
// advanced group — its thinking family/default mode, custom thinking modes and
// generation options. Saving writes the whole modelcatalog.models row through
// writeCatalogModels so the modelcatalog backend (and the composer menus) see
// the change immediately; when the edited row is the default model its
// connection mirror (chat.model/chat.baseURL/chat.apiKey) is rewritten too.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// catalogEditDraft is the editor modal's UI-thread draft of one catalog model.
// The model ID is immutable: it is the row key the conversation menus store.
type catalogEditDraft struct {
	id   string
	name string
	note string
	// contextWindow is the numeric draft of the context-window input.
	contextWindow string
	// contextWindowNum mirrors the official ui.NumberInput (0 = unknown).
	contextWindowNum float64

	// baseURL/apiKey are the model's own provider connection. The API key input
	// always starts empty so the stored credential is never rendered;
	// storedAPIKey keeps the saved key for the documented "blank keeps it"
	// behavior, and clearKey is the explicit "clear the saved key" choice.
	baseURL      string
	apiKey       string
	storedAPIKey string
	clearKey     bool
	providerID   string

	// Numeric mirrors and "use the provider default" switches for the official
	// ui.NumberInputs: an empty string means "not set" (nil / 0), so the switch,
	// not an empty field, clears a value.
	temperatureNum  float64
	topPNum         float64
	maxTokensNum    float64
	tempDefault     bool
	topPDefault     bool
	tokensDefault   bool
	family          string
	defaultThinking string
	temperature     string
	topP            string
	maxTokens       string
	// priceInput/priceCached/priceOutput are the optional per-million USD
	// prices as typed; an empty field or 0 means "not configured".
	priceInput  string
	priceCached string
	priceOutput string
	modes       []*catalogModeDraft
	// setDefault mirrors the "set as default model" check box; wasDefault marks
	// the row that already is the default, whose box stays checked and disabled.
	setDefault bool
	wasDefault bool
	err        string
	// check is the dialog's inline connection-test state.
	check modelCheckState
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

// formatCatalogPrice is the editor's optional price field: 0 prints as an
// empty field (not configured), valid prices keep their natural precision.
func formatCatalogPrice(value float64) string {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return ""
	}
	return formatCatalogNumber(value)
}

// catalogPricingSummary renders a row's configured prices for the model list;
// empty when none are configured (an unknown price is never shown as $0).
func catalogPricingSummary(locale contracts.LocaleService, model contracts.ModelInfo) string {
	if !model.HasPricing() {
		return ""
	}
	parts := make([]string, 0, 3)
	if model.InputPricePerMillion > 0 {
		parts = append(parts, localized(locale, "输入 ", "input ")+"$"+formatCatalogNumber(model.InputPricePerMillion)+"/M")
	}
	if model.CachedInputPricePerMillion > 0 {
		parts = append(parts, localized(locale, "缓存 ", "cached ")+"$"+formatCatalogNumber(model.CachedInputPricePerMillion)+"/M")
	}
	if model.OutputPricePerMillion > 0 {
		parts = append(parts, localized(locale, "输出 ", "output ")+"$"+formatCatalogNumber(model.OutputPricePerMillion)+"/M")
	}
	return localized(locale, "价格：", "Pricing: ") + strings.Join(parts, " · ")
}

// validateCatalogPrices parses the optional price fields. Empty/0 means "not
// configured"; a malformed, non-finite, negative or absurdly large value is an
// inline error (the draft is kept so the user can fix it). Valid values go
// through contracts.SanitizePrices, the same normalization the catalog module
// applies when it reads the row.
func validateCatalogPrices(draft *catalogEditDraft,
	locale contracts.LocaleService) (input, cached, output float64, errText string) {
	parse := func(raw, zhLabel, enLabel string) (float64, string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return 0, ""
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, localized(locale,
				zhLabel+"必须是数字", enLabel+" must be a number")
		}
		if value < 0 {
			return 0, localized(locale,
				zhLabel+"不能为负数", enLabel+" must not be negative")
		}
		if value > 1e9 {
			return 0, localized(locale,
				zhLabel+"过大（上限 1,000,000,000）",
				enLabel+" is too large (max 1,000,000,000)")
		}
		return value, ""
	}
	if value, errText := parse(draft.priceInput,
		localized(locale, "输入价格", "Input price"),
		localized(locale, "Input price", "Input price")); errText != "" {
		return 0, 0, 0, errText
	} else {
		input = value
	}
	if value, errText := parse(draft.priceCached,
		localized(locale, "缓存输入价格", "Cached input price"),
		localized(locale, "Cached input price", "Cached input price")); errText != "" {
		return 0, 0, 0, errText
	} else {
		cached = value
	}
	if value, errText := parse(draft.priceOutput,
		localized(locale, "输出价格", "Output price"),
		localized(locale, "Output price", "Output price")); errText != "" {
		return 0, 0, 0, errText
	} else {
		output = value
	}
	input, cached, output = contracts.SanitizePrices(input, cached, output)
	return input, cached, output, ""
}

// buildCatalogPricingFields renders the three optional per-million prices with
// the "estimate only, recomputed at current prices" explanation.
func buildCatalogPricingFields(c *ui.Context, t *design.Tokens, locale contracts.LocaleService,
	input, cached, output *string) {
	design.Apply(ui.Text(c, localized(locale, "价格（可选）", "Pricing (optional)")),
		t.Font(design.TypeS)).FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
	design.Apply(ui.Text(c, localized(locale,
		"每 1M tokens 的美元价格；留空或 0 表示未配置。价格只用于成本估算，按当前价格重算。",
		"USD per 1M tokens; blank or 0 means not configured. Prices are used for cost estimates only and are applied at current prices.")),
		t.TextMuted(design.TypeXS)).MaxLines(2)
	ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
		ui.Column(c).FillWidth().Grow(1).Children(func() {
			ui.Field(c, localized(locale, "输入价格 / 1M", "Input price / 1M"), func() {
				ui.TextInput(c.Key("catalog.edit.priceInput"), input).
					Label(inputLabel(locale, localized(locale, "输入价格 / 1M", "Input price / 1M"))).
					Placeholder("2.00").FillWidth()
			})
		})
		ui.Column(c).FillWidth().Grow(1).Children(func() {
			ui.Field(c, localized(locale, "缓存输入 / 1M", "Cached input / 1M"), func() {
				ui.TextInput(c.Key("catalog.edit.priceCached"), cached).
					Label(inputLabel(locale, localized(locale, "缓存输入 / 1M", "Cached input / 1M"))).
					Placeholder(localized(locale, "留空回退输入价", "blank falls back to input")).FillWidth()
			})
		})
		ui.Column(c).FillWidth().Grow(1).Children(func() {
			ui.Field(c, localized(locale, "输出价格 / 1M", "Output price / 1M"), func() {
				ui.TextInput(c.Key("catalog.edit.priceOutput"), output).
					Label(inputLabel(locale, localized(locale, "输出价格 / 1M", "Output price / 1M"))).
					Placeholder("8.00").FillWidth()
			})
		})
	})
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

// newCatalogEditDraft prefills the editor from one catalog row. isDefault marks
// the row as the current default model, whose "set as default" box is fixed.
func newCatalogEditDraft(model contracts.ModelInfo, isDefault bool) *catalogEditDraft {
	draft := &catalogEditDraft{
		id:              model.ID,
		name:            model.Name,
		note:            model.Note,
		baseURL:         strings.TrimRight(strings.TrimSpace(model.BaseURL), "/"),
		storedAPIKey:    strings.TrimSpace(model.APIKey),
		providerID:      catalogProviderFromURL(model.BaseURL),
		family:          contracts.EffectiveThinkingFamily(model),
		defaultThinking: strings.TrimSpace(model.DefaultThinking),
		setDefault:      isDefault,
		wasDefault:      isDefault,
	}
	if model.ContextWindow > 0 {
		draft.contextWindow = strconv.Itoa(model.ContextWindow)
		draft.contextWindowNum = float64(model.ContextWindow)
	}
	draft.tempDefault = model.Temperature == nil
	if model.Temperature != nil {
		draft.temperature = formatCatalogNumber(*model.Temperature)
		draft.temperatureNum = *model.Temperature
	}
	draft.topPDefault = model.TopP == nil
	if model.TopP != nil {
		draft.topP = formatCatalogNumber(*model.TopP)
		draft.topPNum = *model.TopP
	}
	draft.tokensDefault = model.MaxTokens <= 0
	if model.MaxTokens > 0 {
		draft.maxTokens = strconv.Itoa(model.MaxTokens)
		draft.maxTokensNum = float64(model.MaxTokens)
	}
	draft.priceInput = formatCatalogPrice(model.InputPricePerMillion)
	draft.priceCached = formatCatalogPrice(model.CachedInputPricePerMillion)
	draft.priceOutput = formatCatalogPrice(model.OutputPricePerMillion)
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
// Labels are disambiguated by ID when two modes share a display name. The
// "default" mode (no thinking fields) is dropped: it is exactly what the empty
// 跟随系列默认 override already does, so offering both is a near-synonymous
// duplicate (2026-11 audit US-07).
func catalogDefaultThinkingOptions(locale contracts.LocaleService,
	modes []contracts.ThinkingMode) (labels, ids []string) {
	labels = []string{localized(locale, "跟随系列默认", "Family default")}
	ids = []string{""}
	seen := make(map[string]bool, len(modes))
	for _, mode := range modes {
		if mode.ID == contracts.ThinkingModeDefault {
			continue
		}
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
	pricing := make([]string, 0, 3)
	if value, ok := catalogDraftFloat(draft.priceInput, 0, 1e9); ok && value > 0 {
		pricing = append(pricing, localized(locale, "输入 ", "input ")+"$"+formatCatalogNumber(value)+"/M")
	}
	if value, ok := catalogDraftFloat(draft.priceCached, 0, 1e9); ok && value > 0 {
		pricing = append(pricing, localized(locale, "缓存 ", "cached ")+"$"+formatCatalogNumber(value)+"/M")
	}
	if value, ok := catalogDraftFloat(draft.priceOutput, 0, 1e9); ok && value > 0 {
		pricing = append(pricing, localized(locale, "输出 ", "output ")+"$"+formatCatalogNumber(value)+"/M")
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
	if len(pricing) > 0 {
		if line != "" {
			line += " · "
		}
		line += localized(locale, "价格：", "Pricing: ") + strings.Join(pricing, " · ")
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
		return localized(locale, "未配置生成参数、价格或自定义模式",
			"No generation options, pricing or custom modes")
	}
	return line
}

// buildCatalogEditDialog renders the editor modal: the model's provider
// connection and default-model choice on top, its thinking/generation
// configuration in the collapsed advanced group below. Saving writes the whole
// list and the default-model mirror in one transactional step.
func buildCatalogEditDialog(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime,
	store contracts.SettingsService, models []contracts.ModelInfo, st *catalogState,
	locale contracts.LocaleService, fallbackBaseURL, fallbackAPIKey string) {
	draft := st.edit
	if draft == nil {
		return
	}
	submit := func() { saveCatalogEdit(c, st, store, models, locale) }
	ui.Modal(c, &st.editorOpen, func() {
		// Same dialog shell as the connector form: a fixed header (icon, title,
		// subtitle, close), ONE scrolling body, a fixed status row and a fixed
		// action row. The old shape put a bare title above a *fixed-height*
		// Scroll, so the panel outgrew the window and the title ended up on top of
		// the scrolled fields (2026-11-18 review); the subtitle names the model
		// being edited so the context never scrolls away.
		design.Apply(ui.Column(c).Key("catalog.edit"), t.Panel()).
			Width(design.DialogWidthXL).Padding(design.Space4).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key:      "catalog.edit",
				Title:    localized(locale, "编辑模型", "Edit model"),
				Subtitle: catalogEditorSubtitle(locale, draft),
				OnClose: func() {
					st.editorOpen = false
					st.edit = nil
					c.Invalidate()
				},
			})
			ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).Children(func() {
				ui.Column(c).FillWidth().
					Padding(design.Space0, design.Space0, design.Space4, design.Space0).
					Gap(design.Space3).Children(func() {
					ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							ui.Field(c, localized(locale, "模型名称", "Model name"), func() {
								ui.TextInput(c, &draft.name).
									Label(inputLabel(locale, localized(locale, "模型名称", "Model name"))).
									Placeholder(localized(locale, "可选；留空时使用模型 ID",
										"optional; falls back to the model ID")).
									FillWidth().AutoFocus().OnTextKey(uikit.EnterTo(submit))
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
							buildCatalogProviderField(c, locale, &draft.providerID, &draft.baseURL, nil)
						})
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							buildCatalogBaseURLField(c, locale, &draft.providerID, &draft.baseURL)
						})
					})
					ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							description := localized(locale, "未设置；密钥只保存在本机。",
								"Not configured; the key is stored locally only.")
							if draft.storedAPIKey != "" {
								description = localized(locale, "已设置（留空保持不变）。",
									"Configured (leave blank to keep it).")
							}
							if draft.clearKey {
								description = localized(locale, "保存后删除该模型的密钥。",
									"Saves the model without a key.")
							}
							ui.Field(c, "API Key", func() {
								input := ui.TextInput(c, &draft.apiKey).
									Label(inputLabel(locale, "API Key")).
									Password().
									Placeholder(localized(locale, "输入新的 API Key；留空保持不变",
										"Enter a new API key; leave blank to keep")).
									FillWidth()
								if input.Changed() {
									// A typed key wins over a pending clear.
									draft.clearKey = false
									c.Invalidate()
								}
								if draft.storedAPIKey != "" {
									ui.Checkbox(c, &draft.clearKey,
										localized(locale, "清除已保存密钥", "Clear the saved key"))
								}
							}).Description(description)
						})
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							buildCatalogContextWindowField(c, locale, "catalog.edit.contextWindow",
								&draft.contextWindow, &draft.contextWindowNum)
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

					hint := localized(locale, "对话输入框默认使用该模型。",
						"The composer uses it as its default model.")
					if draft.wasDefault {
						hint = localized(locale, "它是当前默认模型。", "It is the current default model.")
					}
					buildCatalogSetDefaultField(c, t, locale, &draft.setDefault, draft.wasDefault, hint)

					// Progressive disclosure: generation options and custom thinking
					// modes are advanced settings, collapsed by default behind a one-line
					// summary so the modal's common fields stay short.
					ui.Collapsible(c.Key("catalog.editAdvanced"),
						localized(locale, "生成、价格与自定义模式", "Generation, pricing & custom modes"), &st.editAdvanced, func() {
							// Generation options: optional numeric fields; every empty
							// field means "provider default".
							design.Apply(ui.Text(c, localized(locale, "生成参数", "Generation")),
								t.Font(design.TypeS)).FontWeight(600).TextColor(t.LabelPrimary)
							ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
								ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
									ui.Field(c, localized(locale, "温度 0–2", "Temperature 0–2"), func() {
										ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
											if switchInline(c, localized(locale, "使用默认温度", "Use default temperature"), &draft.tempDefault) {
												if draft.tempDefault {
													draft.temperature = ""
												} else {
													if draft.temperatureNum == 0 {
														draft.temperatureNum = 0.7
													}
													draft.temperature = formatCatalogNumber(draft.temperatureNum)
												}
											}
										})
										if !draft.tempDefault {
											input := ui.NumberInput(c.Key("catalog.edit.temperature"),
												&draft.temperatureNum, 0, 2, 0.1).
												Label(inputLabel(locale, localized(locale, "温度", "Temperature")))
											if input.Changed() {
												draft.temperature = formatCatalogNumber(draft.temperatureNum)
											}
										}
									})
								})
								ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
									ui.Field(c, "Top P 0–1", func() {
										ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
											if switchInline(c, localized(locale, "使用默认 Top P", "Use default Top P"), &draft.topPDefault) {
												if draft.topPDefault {
													draft.topP = ""
												} else {
													if draft.topPNum == 0 {
														draft.topPNum = 0.9
													}
													draft.topP = formatCatalogNumber(draft.topPNum)
												}
											}
										})
										if !draft.topPDefault {
											input := ui.NumberInput(c.Key("catalog.edit.topP"),
												&draft.topPNum, 0, 1, 0.05).
												Label(inputLabel(locale, "Top P"))
											if input.Changed() {
												draft.topP = formatCatalogNumber(draft.topPNum)
											}
										}
									})
								})
								ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
									ui.Field(c, localized(locale, "最大输出 tokens", "Max output tokens"), func() {
										ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
											if switchInline(c, localized(locale, "使用默认 max tokens", "Use default max tokens"), &draft.tokensDefault) {
												if draft.tokensDefault {
													draft.maxTokens = ""
												} else {
													if draft.maxTokensNum <= 0 {
														draft.maxTokensNum = 2048
													}
													draft.maxTokens = strconv.Itoa(int(draft.maxTokensNum))
												}
											}
										})
										if !draft.tokensDefault {
											input := ui.NumberInput(c.Key("catalog.edit.maxTokens"),
												&draft.maxTokensNum, 1, 1000000, 1).
												Label(inputLabel(locale, localized(locale, "最大输出 tokens", "Max output tokens")))
											if input.Changed() {
												draft.maxTokens = strconv.Itoa(int(draft.maxTokensNum))
											}
										}
									})
								})
							})

							ui.Divider(c)
							buildCatalogPricingFields(c, t, locale,
								&draft.priceInput, &draft.priceCached, &draft.priceOutput)

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
								// The rows are a native ui.List: only the visible modes are
								// built, the identity follows the mode ID (or its index while
								// a new row has no ID yet) and the keyboard scrolls it.
								st.modeList.Key = func(index int) any {
									if id := strings.TrimSpace(draft.modes[index].id); id != "" {
										return id
									}
									return index
								}
								st.modeList.Label = func(index int) string {
									if name := strings.TrimSpace(draft.modes[index].name); name != "" {
										return name
									}
									return draft.modes[index].id
								}
								ui.List(c, &st.modeList, len(draft.modes)).
									Label(localized(locale, "自定义思考模式", "Custom thinking modes")).
									Gap(design.Space1).Height(130).FillWidth().
									Rows(func(row ui.ListRow) {
										index := row.Index
										mode := draft.modes[index]
										ui.Row(row.Context).FillWidth().Gap(design.Space2).
											AlignItems(ui.Start).PaddingY(design.Space1).Children(func() {
											ui.Column(row.Context).Width(150).Grow(0).Shrink(0).Children(func() {
												ui.TextInput(row.Context, &mode.id).
													Label(inputLabel(locale, localized(locale, "模式 ", "Mode ")+
														strconv.Itoa(index+1)+" ID")).
													Placeholder("thinking-low").FillWidth()
											})
											ui.Column(row.Context).Width(150).Grow(0).Shrink(0).Children(func() {
												ui.TextInput(row.Context, &mode.name).
													Label(inputLabel(locale, localized(locale, "模式 ", "Mode ")+
														strconv.Itoa(index+1)+localized(locale, " 名称", " name"))).
													Placeholder(localized(locale, "低强度", "Low")).FillWidth()
											})
											ui.Column(row.Context).FillWidth().Grow(1).Children(func() {
												ui.TextArea(row.Context, &mode.params).
													Label(inputLabel(locale, localized(locale, "模式 ", "Mode ")+
														strconv.Itoa(index+1)+localized(locale, " 参数", " params"))).
													Placeholder(`{"reasoning_effort": "low"}`).
													FillWidth().MinHeight(design.Space4 * 2)
											})
											if design.Apply(ui.Button(row.Context, localized(locale, "移除", "Remove")),
												t.DangerButton()).
												Label(localized(locale, "移除思考模式 ", "Remove thinking mode ") + strconv.Itoa(index+1)).
												Description(localized(locale, "删除这个思考模式", "Remove this thinking mode")).
												Clicked() {
												removed = index
											}
										})
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

				})
			})

			testAPIKey := strings.TrimSpace(draft.apiKey)
			if testAPIKey == "" && !draft.clearKey {
				testAPIKey = draft.storedAPIKey
			}
			testBaseURL := textutil.FirstNonEmpty(strings.TrimRight(strings.TrimSpace(draft.baseURL), "/"), fallbackBaseURL)
			if testAPIKey == "" {
				testAPIKey = fallbackAPIKey
			}
			// The status row is reserved and lives outside the scrolling body (an
			// in-body line was sliced at the clip), and the connection test is an
			// action on the action row, never a second action line.
			ui.Column(c).FillWidth().MinHeight(design.Space6).Justify(ui.Center).Children(func() {
				if draft.err != "" {
					uikit.ErrorLine(c, draft.err)
					return
				}
				buildCatalogCheckStatus(c, t, &draft.check, testBaseURL, locale)
			})
			uikit.DialogFooter(c, []uikit.DialogAction{
				catalogTestAction(c, t, runtime, &draft.check, testBaseURL, testAPIKey, locale),
				{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
					st.editorOpen = false
					st.edit = nil
					c.Invalidate()
				}},
				{Label: localized(locale, "保存模型", "Save model"), Primary: true, OnClick: func() {
					submit()
					c.Invalidate()
				}},
			})
		})
	})
	if !st.editorOpen {
		// Escape or a backdrop click dismissed the modal without saving.
		st.edit = nil
	}
}

// catalogEditorSubtitle is the edit dialog's one context line: the model being
// edited (its display name, or the id when the name is blank). It stays in the
// fixed header so scrolling the fields never loses "which model is this".
func catalogEditorSubtitle(locale contracts.LocaleService, draft *catalogEditDraft) string {
	if draft == nil {
		return ""
	}
	name := textutil.FirstNonEmptyTrimmed(draft.name, draft.id)
	if name == "" {
		return localized(locale, "未命名模型", "Untitled model")
	}
	return name
}

// saveCatalogEdit validates the draft and replaces the row in the catalog. The
// write goes through persistCatalogModels, which the modelcatalog backend reads
// on every call, so the change applies immediately; when the row is the default
// model its connection mirror is rewritten in the same transaction. Failures
// keep the modal open with an inline danger line.
func saveCatalogEdit(c *ui.Context, st *catalogState, store contracts.SettingsService,
	models []contracts.ModelInfo, locale contracts.LocaleService) {
	draft := st.edit
	if draft == nil {
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(draft.baseURL), "/")
	if baseURL == "" {
		draft.err = localized(locale, "Base URL 不能为空", "Base URL is required")
		c.Invalidate()
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
	inputPrice, cachedPrice, outputPrice, priceErr := validateCatalogPrices(draft, locale)
	if priceErr != "" {
		draft.err = priceErr
		c.Invalidate()
		return
	}
	// The API key input starts empty; blank keeps the stored key unless the
	// user explicitly clears it.
	apiKey := strings.TrimSpace(draft.apiKey)
	switch {
	case draft.clearKey:
		apiKey = ""
	case apiKey == "":
		apiKey = draft.storedAPIKey
	}
	updated := contracts.ModelInfo{
		ID:              draft.id,
		Name:            strings.TrimSpace(draft.name),
		Note:            strings.TrimSpace(draft.note),
		ContextWindow:   parseContextWindow(draft.contextWindow),
		BaseURL:         baseURL,
		APIKey:          apiKey,
		Family:          family,
		DefaultThinking: strings.TrimSpace(draft.defaultThinking),
		ThinkingModes:   thinkingModes,
		Temperature:     temperature,
		TopP:            topP,
		MaxTokens:       maxTokens,

		InputPricePerMillion:       inputPrice,
		CachedInputPricePerMillion: cachedPrice,
		OutputPricePerMillion:      outputPrice,
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
		draft.err = localized(locale, "模型已不在列表中，请重新打开编辑",
			"The model is no longer in the list; reopen the editor")
		c.Invalidate()
		return
	}
	wasDefault := catalogIsDefault(store, models, draft.id)
	mirror := catalogKeepDefaultMirror(store, next)
	if wasDefault || draft.setDefault {
		selected := mirrorForModel(updated)
		mirror = &selected
	}
	if err := persistCatalogModels(store, next, mirror); err != nil {
		draft.err = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		c.Invalidate()
		return
	}
	st.editorOpen = false
	st.edit = nil
	st.status, st.err = localized(locale, "已保存", "Saved"), false
	c.Invalidate()
}
