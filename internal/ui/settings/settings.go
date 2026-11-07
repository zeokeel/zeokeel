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
)

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

		title := firstNonEmpty(cfg.Title, "模型")
		modelLabel := firstNonEmpty(cfg.ModelLabel, "模型")
		baseURLLabel := firstNonEmpty(cfg.BaseURLLabel, "Base URL")
		apiKeyLabel := firstNonEmpty(cfg.APIKeyLabel, "API Key")
		systemPromptLabel := firstNonEmpty(cfg.SystemPromptLabel, "系统提示词")
		saveLabel := firstNonEmpty(cfg.SaveLabel, "保存")

		state := newState(chat, store)

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
		return own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(page) })
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel model settings page")
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
