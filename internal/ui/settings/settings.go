// Package settings contributes the "model" page to the settings surface: the
// chat model, base URL, API key and system prompt.
//
// It is a UI module like ui/about: a kernel Descriptor that requires the host UI
// runtime, the settings store and the chat read model, and registers one
// contracts.SettingsPageSpec on activation. The host owns the settings shell
// (native Sidebar + Modal); this module only builds native ui.TextInput,
// ui.TextArea, ui.Text and ui.PrimaryButton controls.
//
// The effective model configuration is read from ChatService.Config first (the
// agent backend resolves module config, settings, and environment into it) and
// from the settings store as a fallback. Saving writes the contracts.SettingsChat*
// keys; the API key is never displayed in clear text and is only written when
// the user types a new one (an empty API key field keeps the stored key).
package settings

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Settings page constants. ID and Section are stable contracts the host renders
// in its settings sidebar; Order places the page before About (90).
const (
	PageID      = "model"
	PageSection = "模型"
	PageOrder   = 10

	// AppearancePageID is the appearance page identity (theme + language).
	AppearancePageID = "appearance"
	// AppearancePageOrder places the page after the model page.
	AppearancePageOrder = 20
)

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

// buildAppearancePage builds the theme/language page with native controls.
func buildAppearancePage(c *ui.Context, state *appearanceState, store contracts.SettingsService,
	theme contracts.ThemeService, locale contracts.LocaleService, labels buildAppearanceLabels) {
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		uikit.PageHeader(c, labels.title, "主题与界面语言；保存后立即生效并写入产品设置。")

		uikit.Section(c, "主题", func(c *ui.Context) {
			toggle := ui.Toggle(c, &state.dark, labels.themeLabel)
			if toggle.Changed() {
				theme.SetDark(state.dark)
				_ = store.Set(contracts.SettingsUIThemeDark, fmt.Sprintf("%t", state.dark))
				state.status = "主题已切换"
				state.statusErr = false
			}
		})

		uikit.Section(c, "语言", func(c *ui.Context) {
			choice := languageChoice(state.language)
			selectControl := ui.Select(c, &choice, languageOptions).Label(labels.languageLabel)
			if selectControl.Changed() {
				state.language = languageCode(choice)
				locale.SetLanguage(state.language)
				_ = store.Set(contracts.SettingsUILanguage, state.language)
				state.status = "语言已切换"
				state.statusErr = false
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, labels.saveLabel).Clicked() {
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
				if state.statusErr {
					ui.Text(c, state.status).FontSize(12).TextColor(c.Theme().Danger)
				} else {
					ui.Badge(c, state.status)
				}
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

// Module registers the model settings page.
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
		appearanceTitle := firstNonEmpty(cfg.AppearanceTitle, "外观")
		themeLabel := firstNonEmpty(cfg.ThemeLabel, "深色模式")
		languageLabel := firstNonEmpty(cfg.LanguageLabel, "界面语言")
		saveAppearanceLabel := firstNonEmpty(cfg.SaveAppearanceLabel, "应用")

		state := newState(chat, store)
		appearance := &appearanceState{dark: theme.Dark(), language: locale.Language()}

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
		return own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(appearancePage) })
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyChat)).
		AddRequires(module.Requires(contracts.KeyTheme)).
		AddRequires(module.Requires(contracts.KeyLocale)).
		WithName("ZeoKeel settings pages")
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

// buildPage builds the model settings page with native controls only.
func buildPage(c *ui.Context, state *moduleState, chat contracts.ChatService, labels buildLabels, store contracts.SettingsService) {
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		uikit.PageHeader(c, labels.title, "配置对话使用的模型、API 地址与系统提示词。")

		uikit.Section(c, "快速预设", func(c *ui.Context) {
			type preset struct{ label, model, baseURL string }
			presets := []preset{
				{"DeepSeek", "deepseek-chat", "https://api.deepseek.com/v1"},
				{"OpenAI", "gpt-4o-mini", "https://api.openai.com/v1"},
				{"Ollama", "llama3.1", "http://localhost:11434/v1"},
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				for _, preset := range presets {
					preset := preset
					if ui.Button(c, preset.label).Clicked() {
						state.model = preset.model
						state.baseURL = preset.baseURL
						state.status = "已填入 " + preset.label + " 预设，保存后生效"
						state.statusErr = false
					}
				}
			})
		})

		uikit.Section(c, "连接", func(c *ui.Context) {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				inputField(c, labels.modelLabel, func() {
					ui.TextInput(c, &state.model).Label(labels.modelLabel + " 输入").FillWidth()
				})
				inputField(c, labels.baseURLLabel, func() {
					ui.TextInput(c, &state.baseURL).Label(labels.baseURLLabel + " 输入").FillWidth()
				})
				inputField(c, labels.apiKeyLabel, func() {
					if state.apiKeySet {
						ui.Text(c, "已设置（留空保持不变）").FontSize(12).TextColor(c.Theme().TextMuted)
					} else {
						ui.Text(c, "未设置").FontSize(12).TextColor(c.Theme().TextMuted)
					}
					ui.TextInput(c, &state.apiKey).Label(labels.apiKeyLabel + " 输入").
						Placeholder("输入新的 API key；留空保持不变").FillWidth()
				})
			})
		})

		uikit.Section(c, "提示词", func(c *ui.Context) {
			inputField(c, labels.systemPromptLabel, func() {
				ui.TextArea(c, &state.systemPrompt).Label(labels.systemPromptLabel + " 输入").
					FillWidth().Height(120)
			})
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, labels.saveLabel).Clicked() {
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
				if state.statusErr {
					ui.Text(c, state.status).FontSize(12).TextColor(c.Theme().Danger)
				} else {
					ui.Badge(c, state.status)
				}
			}
		})

		// Read-only context: the host owns navigation, so this is a plain
		// informational text, not a control.
		active := strings.TrimSpace(chat.ActiveSession())
		if active == "" {
			active = "无活动会话"
		}
		ui.Divider(c)
		ui.Textf(c, "当前会话: %s", active).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Textf(c, "当前模型: %s", firstNonEmpty(state.model, "未配置")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

// inputField draws one muted field label above its native control.
func inputField(c *ui.Context, label string, control func()) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
		control()
	})
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
