package settings

// Model connection check: a small block on the model settings page that asks an
// OpenAI-compatible /models endpoint whether the configured base URL and API key
// work. The request runs off the UI thread and posts its result back.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// modelCheckTimeout bounds one connection check (5s, as the page documents).
const modelCheckTimeout = 5 * time.Second

// ModelListGetter performs the OpenAI-compatible `GET {baseURL}/models` request
// and returns the HTTP status and body. It is a seam so tests can script the
// outcome (and products can inject a custom transport).
var ModelListGetter = func(ctx context.Context, baseURL, apiKey string) (int, []byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, modelCheckTimeout)
	defer cancel()
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/models"
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, nil, err
	}
	return response.StatusCode, body, nil
}

// modelCheckState is the connection-check block's UI-local state.
type modelCheckState struct {
	running         bool
	result          string
	resultErr       bool
	modelCount      int
	modelCountKnown bool
}

// modelCheckConfig resolves the effective check configuration: the saved
// settings rows first, then the chat backend's own config as fallback.
func modelCheckConfig(chat contracts.ChatService, store contracts.SettingsService) (baseURL, model, apiKey string) {
	var effective contracts.ChatConfig
	if chat != nil {
		effective = chat.Config()
	}
	saved := func(key string) string {
		if store == nil {
			return ""
		}
		return strings.TrimSpace(store.Get(key, ""))
	}
	return firstNonEmpty(saved(contracts.SettingsChatBaseURL), strings.TrimSpace(effective.BaseURL)),
		firstNonEmpty(saved(contracts.SettingsChatModel), strings.TrimSpace(effective.Model)),
		firstNonEmpty(saved(contracts.SettingsChatAPIKey), strings.TrimSpace(effective.APIKey))
}

// parseModelListCount counts the models in an OpenAI-style `{"data":[...]}`
// response; known=false when the body is not that shape.
func parseModelListCount(body []byte) (count int, known bool) {
	if len(body) == 0 {
		return 0, false
	}
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.Data == nil {
		return 0, false
	}
	return len(decoded.Data), true
}

// modelCountLabel renders the discovered model count, if known.
func modelCountLabel(locale contracts.LocaleService, count int, known bool) string {
	if !known {
		return ""
	}
	if localized(locale, "zh", "en") == "en" {
		if count == 1 {
			return "1 model"
		}
		return fmt.Sprintf("%d models", count)
	}
	return fmt.Sprintf("%d 个模型", count)
}

// startModelCheck launches one background check and posts its result to the UI.
// The running guard makes repeated clicks a no-op.
func startModelCheck(runtime contracts.UIRuntime, check *modelCheckState,
	chat contracts.ChatService, store contracts.SettingsService, locale contracts.LocaleService) {
	baseURL, _, apiKey := modelCheckConfig(chat, store)
	if check.running || strings.TrimSpace(baseURL) == "" {
		return
	}
	check.running = true
	check.result = ""
	check.resultErr = false
	check.modelCountKnown = false
	go func() {
		status, body, err := ModelListGetter(context.Background(), baseURL, apiKey)
		count, known := parseModelListCount(body)
		runtime.Post(func() {
			check.running = false
			switch {
			case err != nil:
				check.result = localized(locale, "连接失败：", "Connection failed: ") + err.Error()
				check.resultErr = true
			case status < 200 || status >= 300:
				check.result = localized(locale,
					fmt.Sprintf("连接失败：HTTP %d", status),
					fmt.Sprintf("Connection failed: HTTP %d", status))
				check.resultErr = true
			default:
				check.result = localized(locale, "连接正常", "Connection OK")
				check.resultErr = false
				check.modelCount = count
				check.modelCountKnown = known
			}
			runtime.Invalidate()
		})
	}()
}

// buildModelCheckPanel renders the "connection check" block of the model page.
func buildModelCheckPanel(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	chat contracts.ChatService, store contracts.SettingsService, locale contracts.LocaleService,
	check *modelCheckState) {
	baseURL, model, _ := modelCheckConfig(chat, store)
	panel(c, t, func() {
		sectionTitle(c, t, localized(locale, "连接检查", "Connection check"))
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "服务地址：", "Service URL: ")+
				firstNonEmpty(baseURL, localized(locale, "未配置", "not configured"))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
			design.Apply(ui.Text(c, localized(locale, "模型：", "Model: ")+
				firstNonEmpty(model, localized(locale, "未配置", "not configured"))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		})
		if strings.TrimSpace(baseURL) == "" {
			buildMemoryPageError(c, t, localized(locale, "尚未配置服务地址", "No service URL configured"))
		}
		label := localized(locale, "测试连接", "Test connection")
		disabled := strings.TrimSpace(baseURL) == ""
		if check.running {
			label = localized(locale, "检查中…", "Checking…")
			disabled = true
		}
		button := design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton()).Disabled(disabled)
		if button.Clicked() && !check.running && !disabled {
			startModelCheck(runtime, check, chat, store, locale)
		}
		if check.result != "" {
			if check.resultErr {
				buildMemoryPageError(c, t, check.result)
			} else {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					statusBadge(c, t, check.result, false)
					if countLabel := modelCountLabel(locale, check.modelCount, check.modelCountKnown); countLabel != "" {
						design.Apply(ui.Text(c, countLabel), t.Font(design.TypeXXS)).
							TextColor(t.LabelTertiary)
					}
				})
			}
		}
	})
}
