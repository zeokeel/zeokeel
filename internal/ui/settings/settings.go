// Package settings contributes the model, providers, appearance, skills and
// shortcuts pages to the settings surface, rebuilt with the ZeoKeel brand design
// system.
//
// It is a UI module like ui/about: a kernel Descriptor that requires the host UI
// runtime, the settings store, the chat read model, the theme and the locale, and
// registers two contracts.SettingsPageSpec values on activation. The host owns
// the settings shell (native Sidebar + Modal); this module only composes native
// mygo controls — ui.TextInput, ui.TextArea, ui.Toggle, ui.Select, ui.Button,
// ui.PrimaryButton, ui.Icon — and styles them with internal/design tokens
// (Panel cards, the brand type scale, brand spacing/radii and the semantic
// colors). Nothing is hand-drawn.
//
// The effective model configuration is read from ChatService.Config first (the
// agent backend resolves module config, settings, and environment into it) and
// from the settings store as a fallback. Saving writes the contracts.SettingsChat*
// keys; the API key is never displayed in clear text and is only written when
// the user types a new one (an empty API key field keeps the stored key).
package settings

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Settings page constants. ID and Section are stable contracts the host renders
// in its settings sidebar; Order places the model page before About (90).
const (
	PageID      = "model"
	PageSection = "模型"
	PageOrder   = 10

	// AppearancePageID is the appearance page identity (theme + language).
	AppearancePageID = "appearance"
	// AppearancePageOrder places the page after the model page.
	AppearancePageOrder = 20

	// ProvidersPageID is the provider catalogue page.
	ProvidersPageID = "providers"
	// ProvidersPageOrder places the page between model and appearance.
	ProvidersPageOrder = 15

	// ShortcutsPageID is the keyboard-shortcut reference page.
	ShortcutsPageID = "shortcuts"
	// ShortcutsPageOrder places the page after appearance.
	ShortcutsPageOrder = 30

	// DiagnosticsPageID is the read-only diagnostics page (about section).
	DiagnosticsPageID = "diagnostics"
	// DiagnosticsPageOrder places it after the About page.
	DiagnosticsPageOrder = 95

	// SkillsPageID is the skills discovery/preview page.
	SkillsPageID = "skills"
	// SkillsPageSection is the settings sidebar section the skills page lives
	// in. The host sorts pages by Section first, so it uses the same section as
	// the model page: a new section like "工具" would sort before "模型" and
	// become the default page, changing what opening Settings shows.
	SkillsPageSection = PageSection
	// SkillsPageOrder places the page between appearance and shortcuts.
	SkillsPageOrder = 25
	// SkillsPageTitle is the page title and heading.
	SkillsPageTitle = "技能"
)

// shortcutRow is one line of the shortcut reference.
type shortcutRow struct {
	Action string
	Keys   string
}

// shortcutRows is the product's keyboard reference (the old shell's shortcuts
// page): chrome toggles, composer keys and window dismissal.
var shortcutRows = []shortcutRow{
	{"折叠 / 展开侧边栏", "Cmd/Ctrl + B"},
	{"打开命令面板", "Cmd/Ctrl + K"},
	{"发送消息", "Enter"},
	{"换行", "Shift + Enter"},
	{"命令菜单", "输入 /"},
	{"关闭弹窗 / 菜单", "Esc"},
}

// buildShortcutsPage renders the shortcut reference with native rows.
func buildShortcutsPage(c *ui.Context, t *design.Tokens, title string) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(600).
			TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, "键盘快捷键参考（与老版本 shell 的快捷键页一致）。"),
			t.TextMuted(design.TypeXS))
		panel := ui.Column(c).FillWidth().Gap(0).Padding(design.Space3)
		design.Apply(panel, t.Panel())
		panel.Children(func() {
			for index, row := range shortcutRows {
				if index > 0 {
					ui.Divider(c)
				}
				ui.Row(c).FillWidth().Padding(design.Space2, 0, design.Space2, 0).
					AlignItems(ui.Center).Children(func() {
					design.Apply(ui.Text(c, row.Action), t.Text(design.TypeS)).Grow(1)
					design.Apply(ui.Badge(c, row.Keys), t.Badge())
				})
			}
		})
	})
}

// appearanceState is the appearance page's UI-local state.
type appearanceState struct {
	dark      bool
	language  string
	status    string
	statusErr bool
}

// buildAppearanceLabels is the resolved copy of the appearance page.
type buildAppearanceLabels struct {
	title         string
	themeLabel    string
	languageLabel string
	saveLabel     string
}

// languageOptions maps the selector's display strings to locale codes.
var languageOptions = []string{"中文", "English"}

func languageCode(choice string) string {
	if choice == "English" {
		return "en"
	}
	return "zh"
}

func languageChoice(code string) string {
	if code == "en" {
		return "English"
	}
	return "中文"
}

// Config is the module's typed assembly config.
type Config struct {
	// Title is the settings page title (default "模型").
	Title string `json:"title"`
	// ModelLabel labels the model input (default "模型").
	ModelLabel string `json:"modelLabel"`
	// BaseURLLabel labels the base URL input (default "Base URL").
	BaseURLLabel string `json:"baseURLLabel"`
	// APIKeyLabel labels the API key input (default "API Key").
	APIKeyLabel string `json:"apiKeyLabel"`
	// SystemPromptLabel labels the system prompt text area (default "系统提示词").
	SystemPromptLabel string `json:"systemPromptLabel"`
	// SaveLabel labels the save button (default "保存").
	SaveLabel string `json:"saveLabel"`
	// AppearanceTitle is the appearance page title (default "外观").
	AppearanceTitle string `json:"appearanceTitle"`
	// ThemeLabel labels the dark-mode toggle (default "深色模式").
	ThemeLabel string `json:"themeLabel"`
	// LanguageLabel labels the language selector (default "界面语言").
	LanguageLabel string `json:"languageLabel"`
	// SaveAppearanceLabel labels the appearance save button (default "应用").
	SaveAppearanceLabel string `json:"saveAppearanceLabel"`
}

// moduleState is the page's UI-local state. Drafts live across frames; the API
// key draft starts empty because the stored key is never rendered.
type moduleState struct {
	model        string
	baseURL      string
	apiKey       string
	systemPrompt string

	apiKeySet bool
	status    string
	statusErr bool
}

// buildLabels is the resolved copy of one page build.
type buildLabels struct {
	title             string
	modelLabel        string
	baseURLLabel      string
	apiKeyLabel       string
	systemPromptLabel string
	saveLabel         string
}

// providerPreset is one built-in OpenAI-compatible provider.
type providerPreset struct {
	Name    string
	Model   string
	BaseURL string
}

// providerPresets is the product's provider catalogue (old models_ui page):
// the endpoint/model pairs a first-run user can select with one click.
var providerPresets = []providerPreset{
	{"DeepSeek", "deepseek-chat", "https://api.deepseek.com/v1"},
	{"OpenAI", "gpt-4o-mini", "https://api.openai.com/v1"},
	{"Ollama", "llama3.1", "http://localhost:11434/v1"},
}

// buildProvidersPage renders the provider catalogue as native brand cards; the
// action writes the chat settings keys (the agent reads them on next start).
func buildProvidersPage(c *ui.Context, t *design.Tokens, store contracts.SettingsService) {
	ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
		design.Apply(ui.Text(c, "服务商"), t.Font(design.TypeL)).FontWeight(600).
			TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, "选择内置的 OpenAI 兼容服务商；保存后下次启动生效。"),
			t.TextMuted(design.TypeXS))
		for _, preset := range providerPresets {
			preset := preset
			card := ui.Column(c).FillWidth().Gap(2).Padding(design.Space3)
			design.Apply(card, t.Panel())
			card.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, preset.Name), t.Font(design.TypeS)).FontWeight(600).
						TextColor(t.LabelPrimary)
					design.Apply(ui.Badge(c, preset.Model), t.Badge())
					ui.Spacer(c).Grow(1)
					if ui.Button(c, "使用").Clicked() {
						_ = store.Set(contracts.SettingsChatModel, preset.Model)
						_ = store.Set(contracts.SettingsChatBaseURL, preset.BaseURL)
					}
				})
				design.Apply(ui.Text(c, preset.BaseURL), t.TextMuted(design.TypeXXS))
			})
		}
	})
}

// diagnosticRow is one label/value line of the diagnostics page.
type diagnosticRow struct{ Label, Value string }

// buildDiagnosticsPage renders the read-only runtime/storage summary (the old
// shell's diagnostics view) with a native copy action.
func buildDiagnosticsPage(c *ui.Context, t *design.Tokens, rows []diagnosticRow) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		design.Apply(ui.Text(c, "诊断"), t.Font(design.TypeL)).FontWeight(600).
			TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, "运行时与存储信息（只读）；可复制后附在问题报告里。"),
			t.TextMuted(design.TypeXS))
		panel := ui.Column(c).FillWidth().Gap(0).Padding(design.Space3)
		design.Apply(panel, t.Panel())
		panel.Children(func() {
			for index, row := range rows {
				if index > 0 {
					ui.Divider(c)
				}
				ui.Row(c).FillWidth().Padding(design.Space2, 0, design.Space2, 0).
					AlignItems(ui.Center).Gap(design.Space3).Children(func() {
					design.Apply(ui.Text(c, row.Label), t.Font(design.TypeXS)).
						TextColor(t.LabelSecondary).Width(160).Shrink(0)
					design.Apply(ui.Text(c, row.Value), t.MonoFamily()).FontSize(12).
						TextColor(t.LabelPrimary).Grow(1).MaxLines(2).Ellipsis("…")
				})
			}
		})
		ui.Row(c).FillWidth().Children(func() {
			if ui.Button(c, "复制诊断信息").Clicked() {
				var builder strings.Builder
				for _, row := range rows {
					fmt.Fprintf(&builder, "%s: %s\n", row.Label, row.Value)
				}
				c.WriteClipboard(strings.TrimRight(builder.String(), "\n"))
			}
		})
	})
}

// Module registers the model and appearance settings pages.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/settings", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		store, err := ctx.Require(contracts.KeySettings)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}
		theme, err := ctx.Require(contracts.KeyTheme)
		if err != nil {
			return err
		}
		locale, err := ctx.Require(contracts.KeyLocale)
		if err != nil {
			return err
		}

		title := firstNonEmpty(cfg.Title, "模型")
		modelLabel := firstNonEmpty(cfg.ModelLabel, "模型")
		baseURLLabel := firstNonEmpty(cfg.BaseURLLabel, "Base URL")
		apiKeyLabel := firstNonEmpty(cfg.APIKeyLabel, "API Key")
		systemPromptLabel := firstNonEmpty(cfg.SystemPromptLabel, "系统提示词")
		saveLabel := firstNonEmpty(cfg.SaveLabel, "保存")
		providersTitle := "服务商"
		appearanceTitle := firstNonEmpty(cfg.AppearanceTitle, "外观")
		themeLabel := firstNonEmpty(cfg.ThemeLabel, "深色模式")
		languageLabel := firstNonEmpty(cfg.LanguageLabel, "界面语言")
		saveAppearanceLabel := firstNonEmpty(cfg.SaveAppearanceLabel, "应用")

		state := newState(chat, store)
		appearance := &appearanceState{dark: theme.Dark(), language: locale.Language()}
		skills := newSkillsState()

		page := contracts.SettingsPageSpec{
			ID:      PageID,
			Section: PageSection,
			Title:   title,
			Order:   PageOrder,
			Build: func(c *ui.Context) {
				buildPage(c, state, chat, buildLabels{
					title:             title,
					modelLabel:        modelLabel,
					baseURLLabel:      baseURLLabel,
					apiKeyLabel:       apiKeyLabel,
					systemPromptLabel: systemPromptLabel,
					saveLabel:         saveLabel,
				}, store)
			},
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(page) }); err != nil {
			return err
		}
		providersPage := contracts.SettingsPageSpec{
			ID:      ProvidersPageID,
			Section: PageSection,
			Title:   providersTitle,
			Order:   ProvidersPageOrder,
			Build: func(c *ui.Context) {
				buildProvidersPage(c, design.Of(c), store)
			},
		}
		// Optional host services for the read-only diagnostics page.
		info, _ := ctx.Get(contracts.KeyAppInfo)
		dataDir, _ := ctx.Get(contracts.KeyDataDir)
		toolkit, _ := ctx.Get(contracts.KeyToolkit)
		diagnosticsPage := contracts.SettingsPageSpec{
			ID: DiagnosticsPageID,
			// "高级" sorts after the 模型/键盘 sections, so diagnostics does not
			// become the default settings page.
			Section: "高级",
			Title:   "诊断",
			Order:   DiagnosticsPageOrder,
			Build: func(c *ui.Context) {
				rows := diagnosticsRows(info, dataDir, toolkit, chat, store)
				buildDiagnosticsPage(c, design.Of(c), rows)
			},
		}
		shortcutsPage := contracts.SettingsPageSpec{
			ID:      ShortcutsPageID,
			Section: "键盘",
			Title:   "快捷键",
			Order:   ShortcutsPageOrder,
			Build: func(c *ui.Context) {
				buildShortcutsPage(c, design.Of(c), "快捷键")
			},
		}
		appearancePage := contracts.SettingsPageSpec{
			ID: AppearancePageID,
			// Same sidebar section as the model page so the section heading does
			// not reorder the pages ahead of the model page; Order keeps the
			// model page first.
			Section: PageSection,
			Title:   appearanceTitle,
			Order:   AppearancePageOrder,
			Build: func(c *ui.Context) {
				buildAppearancePage(c, appearance, store, theme, locale, buildAppearanceLabels{
					title:         appearanceTitle,
					themeLabel:    themeLabel,
					languageLabel: languageLabel,
					saveLabel:     saveAppearanceLabel,
				})
			},
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(appearancePage) }); err != nil {
			return err
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(providersPage) }); err != nil {
			return err
		}
		skillsPage := contracts.SettingsPageSpec{
			ID:      SkillsPageID,
			Section: SkillsPageSection,
			Title:   SkillsPageTitle,
			Order:   SkillsPageOrder,
			Build: func(c *ui.Context) {
				// The skills backend is optional: it is looked up lazily so the
				// settings surface still renders (with an empty state) when no
				// skills module is part of the assembly.
				buildSkillsPage(c, design.Of(c), ctx, skills)
			},
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(skillsPage) }); err != nil {
			return err
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(shortcutsPage) }); err != nil {
			return err
		}
		return own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(diagnosticsPage) })
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyChat)).
		AddRequires(module.Requires(contracts.KeyTheme)).
		AddRequires(module.Requires(contracts.KeyLocale)).
		WithName("ZeoKeel settings pages")
}

// diagnosticsRows collects the read-only diagnostics values. Optional services
// degrade to explicit "未启用" values instead of hiding the row.
func diagnosticsRows(info contracts.AppInfo, dataDir string, toolkit *rigtools.Registry,
	chat contracts.ChatService, store contracts.SettingsService) []diagnosticRow {
	name := info.Name
	if name == "" {
		name = "ZeoKeel"
	}
	version := info.Version
	if version == "" {
		version = "dev"
	}
	rows := []diagnosticRow{
		{"应用", name + " v" + version},
		{"Commit", firstNonEmpty(info.Commit, "unknown")},
		{"构建时间", firstNonEmpty(info.BuildTime, "unknown")},
		{"Go 版本", firstNonEmpty(info.GoVersion, runtime.Version())},
		{"平台", runtime.GOOS + "/" + runtime.GOARCH},
		{"数据目录", firstNonEmpty(dataDir, "未设置")},
		{"会话数据库", "zeokeel.db（数据目录）"},
	}
	if dataDir != "" {
		rows[6].Value = filepath.Join(dataDir, "zeokeel.db")
	}
	toolCount := 0
	if toolkit != nil {
		toolCount = len(toolkit.Specs())
	}
	rows = append(rows,
		diagnosticRow{"工具数", fmt.Sprintf("%d", toolCount)},
		diagnosticRow{"会话数", fmt.Sprintf("%d", len(chat.Sessions()))},
	)
	keys := make([]string, 0)
	if all := store.All(); len(all) > 0 {
		for key := range all {
			if key == contracts.SettingsChatAPIKey {
				keys = append(keys, key+"=已设置(隐藏)")
				continue
			}
			keys = append(keys, key+"="+all[key])
		}
		sort.Strings(keys)
	}
	rows = append(rows, diagnosticRow{"设置键", firstNonEmpty(strings.Join(keys, " · "), "（空）")})
	return rows
}

// newState seeds the drafts from the effective chat config, falling back to the
// settings store for values the agent backend did not resolve.
func newState(chat contracts.ChatService, store contracts.SettingsService) *moduleState {
	effective := chat.Config()
	state := &moduleState{
		model:        firstNonEmpty(effective.Model, store.Get(contracts.SettingsChatModel, "")),
		baseURL:      firstNonEmpty(effective.BaseURL, store.Get(contracts.SettingsChatBaseURL, "")),
		systemPrompt: firstNonEmpty(effective.SystemPrompt, store.Get(contracts.SettingsChatSystemPrompt, "")),
	}
	state.apiKeySet = firstNonEmpty(effective.APIKey, store.Get(contracts.SettingsChatAPIKey, "")) != ""
	return state
}

// --- brand building blocks -------------------------------------------------

// pageHeader draws the brand page header: type L title and a muted description.
func pageHeader(c *ui.Context, t *design.Tokens, title, description string) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(500).
			TextColor(t.LabelPrimary)
		if description != "" {
			design.Apply(ui.Text(c, description), t.TextMuted(design.TypeXXS))
		}
	})
}

// panel draws one brand panel card (raised surface, border, radius) with its
// content padding on the 4 DIP grid. It is used for every settings block.
func panel(c *ui.Context, t *design.Tokens, body func()) {
	design.Apply(ui.Column(c).FillWidth().Gap(design.Space2), t.Panel()).
		Padding(design.Space3).Children(body)
}

// sectionTitle draws one panel section title: type S, weight 600, primary text.
func sectionTitle(c *ui.Context, t *design.Tokens, title string) {
	design.Apply(ui.Text(c, title), t.Font(design.TypeS)).FontWeight(600).
		TextColor(t.LabelPrimary)
}

// fieldLabel draws the muted label above one input.
func fieldLabel(c *ui.Context, t *design.Tokens, label string) {
	design.Apply(ui.Text(c, label), t.TextMuted(design.TypeXS))
}

// field stacks one label and its native control in a panel.
func field(c *ui.Context, t *design.Tokens, label string, control func()) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		fieldLabel(c, t, label)
		control()
	})
}

// secondaryButtonStyle is the brand's secondary button: raised fill, regular
// border, control radius and 36 DIP height.
func secondaryButtonStyle(t *design.Tokens) design.Style {
	return func(el ui.Element) ui.Element {
		return el.Height(32).PaddingX(design.Space3).Radius(design.RadiusMD).
			Background(t.BgRaised).Border(1, t.BorderL2).TextColor(t.LabelPrimary)
	}
}

// brandButton builds a secondary button with a leading icon and label using
// native controls only (ui.Button + ui.Icon + ui.Text).
func brandButton(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string) ui.Element {
	button := ui.Button(c, "").Children(func() {
		ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, label), t.Font(design.TypeBody))
	})
	return design.Apply(button, secondaryButtonStyle(t))
}

// brandPrimaryButton builds the brand primary action: native PrimaryButton with
// a leading icon and label, styled with the design system's primary button.
func brandPrimaryButton(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string) ui.Element {
	button := ui.PrimaryButton(c, "").Children(func() {
		ui.Icon(c, icon).Size(design.Space4, design.Space4)
		design.Apply(ui.Text(c, label), t.Font(design.TypeBody))
	})
	return design.Apply(button, t.PrimaryButton())
}

// statusBadge renders the save status as a brand badge, with danger text when
// the save failed.
func statusBadge(c *ui.Context, t *design.Tokens, text string, failed bool) {
	badge := design.Apply(ui.Badge(c, text), t.Badge())
	if failed {
		badge.TextColor(t.DangerText)
	}
}

// --- model page ------------------------------------------------------------

// preset is one quick model preset.
type preset struct {
	label   string
	model   string
	baseURL string
	icon    *ui.SVG
}

// modelPresets are the brand quick presets (filled into the drafts only).
func modelPresets() []preset {
	return []preset{
		{label: "DeepSeek", model: "deepseek-chat", baseURL: "https://api.deepseek.com/v1", icon: icons.Sparkles()},
		{label: "OpenAI", model: "gpt-4o-mini", baseURL: "https://api.openai.com/v1", icon: icons.Globe()},
		{label: "Ollama", model: "llama3.1", baseURL: "http://localhost:11434/v1", icon: icons.Terminal()},
	}
}

// buildPage builds the model settings page with branded native controls.
func buildPage(c *ui.Context, state *moduleState, chat contracts.ChatService, labels buildLabels, store contracts.SettingsService) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, labels.title, "配置对话使用的模型、API 地址与系统提示词。")

		panel(c, t, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				sectionTitle(c, t, "快速预设")
				ui.Spacer(c).Grow(1)
				for _, option := range modelPresets() {
					option := option
					if brandButton(c, t, option.icon, option.label).Clicked() {
						state.model = option.model
						state.baseURL = option.baseURL
						state.status = "已填入 " + option.label + " 预设，保存后生效"
						state.statusErr = false
					}
				}
			})
		})

		panel(c, t, func() {
			sectionTitle(c, t, "连接")
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
					fieldLabel(c, t, labels.modelLabel)
					ui.TextInput(c, &state.model).Label(labels.modelLabel + " 输入").FillWidth()
				})
				ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
					fieldLabel(c, t, labels.baseURLLabel)
					ui.TextInput(c, &state.baseURL).Label(labels.baseURLLabel + " 输入").FillWidth()
				})
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				fieldLabel(c, t, labels.apiKeyLabel)
				ui.Spacer(c).Grow(1)
				if state.apiKeySet {
					design.Apply(ui.Text(c, "已设置（留空保持不变）"), t.TextMuted(design.TypeXXS))
				} else {
					design.Apply(ui.Text(c, "未设置"), t.TextMuted(design.TypeXXS))
				}
			})
			ui.TextInput(c, &state.apiKey).Label(labels.apiKeyLabel + " 输入").
				Placeholder("输入新的 API key；留空保持不变").FillWidth()
		})

		panel(c, t, func() {
			sectionTitle(c, t, "提示词")
			fieldLabel(c, t, labels.systemPromptLabel)
			ui.TextArea(c, &state.systemPrompt).Label(labels.systemPromptLabel + " 输入").
				FillWidth().Height(56)
		})

		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			if brandPrimaryButton(c, t, icons.Sliders(), labels.saveLabel).Clicked() {
				if err := save(state, store); err != nil {
					state.status = "保存失败：" + err.Error()
					state.statusErr = true
				} else {
					state.status = "已保存"
					state.statusErr = false
				}
			}
			ui.Spacer(c).Grow(1)
			if state.status != "" {
				statusBadge(c, t, state.status, state.statusErr)
			}
		})

		// Read-only context: the host owns navigation, so this is a plain
		// informational text, not a control.
		active := strings.TrimSpace(chat.ActiveSession())
		if active == "" {
			active = "无活动会话"
		}
		ui.Divider(c)
		design.Apply(ui.Textf(c, "当前会话: %s · 当前模型: %s", active, firstNonEmpty(state.model, "未配置")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
	})
}

// --- appearance page -------------------------------------------------------

// buildAppearancePage builds the theme/language page with branded native
// controls.
func buildAppearancePage(c *ui.Context, state *appearanceState, store contracts.SettingsService,
	theme contracts.ThemeService, locale contracts.LocaleService, labels buildAppearanceLabels) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, labels.title, "主题与界面语言；保存后立即生效并写入产品设置。")

		panel(c, t, func() {
			sectionTitle(c, t, "主题")
			toggle := ui.Toggle(c, &state.dark, labels.themeLabel)
			if toggle.Changed() {
				theme.SetDark(state.dark)
				_ = store.Set(contracts.SettingsUIThemeDark, fmt.Sprintf("%t", state.dark))
				state.status = "主题已切换"
				state.statusErr = false
			}
			design.Apply(ui.Text(c, "跟随系统或固定浅色/深色外观，切换立即预览。"), t.TextMuted(design.TypeXXS))
		})

		panel(c, t, func() {
			sectionTitle(c, t, "语言")
			choice := languageChoice(state.language)
			selectControl := ui.Select(c, &choice, languageOptions).Label(labels.languageLabel)
			if selectControl.Changed() {
				state.language = languageCode(choice)
				locale.SetLanguage(state.language)
				_ = store.Set(contracts.SettingsUILanguage, state.language)
				state.status = "语言已切换"
				state.statusErr = false
			}
			design.Apply(ui.Text(c, "界面文案与日期格式随语言切换。"), t.TextMuted(design.TypeXXS))
		})

		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			if brandPrimaryButton(c, t, icons.Globe(), labels.saveLabel).Clicked() {
				theme.SetDark(state.dark)
				locale.SetLanguage(state.language)
				if err := saveAppearance(state, store); err != nil {
					state.status = "保存失败：" + err.Error()
					state.statusErr = true
				} else {
					state.status = "已保存"
					state.statusErr = false
				}
			}
			ui.Spacer(c).Grow(1)
			if state.status != "" {
				statusBadge(c, t, state.status, state.statusErr)
			}
		})
	})
}

func saveAppearance(state *appearanceState, store contracts.SettingsService) error {
	if err := store.Set(contracts.SettingsUIThemeDark, fmt.Sprintf("%t", state.dark)); err != nil {
		return err
	}
	return store.Set(contracts.SettingsUILanguage, state.language)
}

// save writes the draft to the settings store. The API key is only written when
// the user typed one, and the plaintext draft is cleared afterwards so it never
// lingers in UI state.
func save(state *moduleState, store contracts.SettingsService) error {
	if err := store.Set(contracts.SettingsChatModel, state.model); err != nil {
		return fmt.Errorf("model: %w", err)
	}
	if err := store.Set(contracts.SettingsChatBaseURL, state.baseURL); err != nil {
		return fmt.Errorf("base URL: %w", err)
	}
	if err := store.Set(contracts.SettingsChatSystemPrompt, state.systemPrompt); err != nil {
		return fmt.Errorf("system prompt: %w", err)
	}
	if key := strings.TrimSpace(state.apiKey); key != "" {
		if err := store.Set(contracts.SettingsChatAPIKey, key); err != nil {
			return fmt.Errorf("API key: %w", err)
		}
		state.apiKeySet = true
	}
	state.apiKey = ""
	return nil
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
