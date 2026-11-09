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
	"github.com/zeokeel/zeokeel/internal/uikit"
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

// Bounds for the success detail: the result line stays one readable row even
// when the endpoint serves a large model catalogue.
const (
	maxModelNamesShown = 2
	maxModelNameRunes  = 24
)

// modelCheckState is the connection-check block's UI-local state.
type modelCheckState struct {
	running         bool
	result          string
	resultErr       bool
	modelCount      int
	modelNames      []string
	modelCountKnown bool
	elapsed         time.Duration
}

// parseModelList reports the non-empty model ids in an OpenAI-style
// `{"data":[...]}` response; known=false when the body is not that shape.
func parseModelList(body []byte) (names []string, known bool) {
	if len(body) == 0 {
		return nil, false
	}
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.Data == nil {
		return nil, false
	}
	names = make([]string, 0, len(decoded.Data))
	for _, model := range decoded.Data {
		if id := strings.TrimSpace(model.ID); id != "" {
			names = append(names, id)
		}
	}
	return names, true
}

// modelCountLabel renders the discovered model count in the current language.
func modelCountLabel(locale contracts.LocaleService, count int) string {
	if uikit.T(locale, "zh", "en") == "en" {
		if count == 1 {
			return "1 model"
		}
		return fmt.Sprintf("%d models", count)
	}
	return fmt.Sprintf("%d 个模型", count)
}

// modelCountDetail renders the count with a bounded id listing:
// "2 个模型：model-a、model-b" / "2 models: model-a, model-b".
func modelCountDetail(locale contracts.LocaleService, count int, names []string) string {
	label := modelCountLabel(locale, count)
	english := uikit.T(locale, "zh", "en") == "en"
	shown := make([]string, 0, maxModelNamesShown)
	for _, name := range names {
		if len(shown) == maxModelNamesShown {
			break
		}
		shown = append(shown, truncateModelName(name))
	}
	if len(shown) == 0 {
		return label
	}
	separator := "、"
	if english {
		separator = ", "
	}
	listing := strings.Join(shown, separator)
	if len(shown) < len(names) {
		listing += "…"
	}
	if english {
		return label + ": " + listing
	}
	return label + "：" + listing
}

// truncateModelName bounds one listed id to maxModelNameRunes runes.
func truncateModelName(name string) string {
	runes := []rune(name)
	if len(runes) <= maxModelNameRunes {
		return name
	}
	return string(runes[:maxModelNameRunes]) + "…"
}

// formatModelCheckDuration renders the measured check latency: milliseconds
// below a second, tenths of a second above it (the check is capped at 5s).
func formatModelCheckDuration(elapsed time.Duration) string {
	if elapsed < time.Second {
		return fmt.Sprintf("%d ms", elapsed.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", elapsed.Seconds())
}

// modelCheckDetailLabel renders the success detail shown next to the status
// badge: the discovered model list and the measured latency, bounded to one
// line ("2 个模型：model-a、model-b · 812 ms" / "2 models: a, b · 812 ms").
func modelCheckDetailLabel(locale contracts.LocaleService, count int, names []string, known bool, elapsed time.Duration) string {
	parts := make([]string, 0, 2)
	if known {
		parts = append(parts, modelCountDetail(locale, count, names))
	}
	parts = append(parts, formatModelCheckDuration(elapsed))
	return strings.Join(parts, " · ")
}

// startModelCheck launches one background check and posts its result to the UI.
// The running guard makes repeated clicks a no-op.
func startModelCheck(runtime contracts.UIRuntime, check *modelCheckState,
	baseURL, apiKey string, locale contracts.LocaleService) {
	baseURL = strings.TrimSpace(baseURL)
	if check.running || baseURL == "" {
		return
	}
	check.running = true
	check.result = ""
	check.resultErr = false
	check.modelCount = 0
	check.modelNames = nil
	check.modelCountKnown = false
	check.elapsed = 0
	go func() {
		started := time.Now()
		status, body, err := ModelListGetter(context.Background(), baseURL, apiKey)
		elapsed := time.Since(started)
		names, known := parseModelList(body)
		runtime.Post(func() {
			check.running = false
			check.elapsed = elapsed
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
				check.modelCount = len(names)
				check.modelNames = names
				check.modelCountKnown = known
			}
			runtime.Invalidate()
		})
	}()
}

// buildModelCheckPanel renders the "connection check" block of the model page.
// H2: it tests the drafts currently in the form (base URL/model as typed, with
// the stored API key kept while its field is blank), not the saved store; the
// action is a secondary button because the page's primary action is Save in the
// fixed footer.
func buildModelCheckPanel(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	check *modelCheckState, model, baseURL, apiKey string, locale contracts.LocaleService) {
	baseURL = strings.TrimSpace(baseURL)
	uikit.Section(c, localized(locale, "连接检查", "Connection check"), func(*ui.Context) {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "服务地址：", "Service URL: ")+
				firstNonEmpty(baseURL, localized(locale, "未填写", "not entered"))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
			design.Apply(ui.Text(c, localized(locale, "模型：", "Model: ")+
				firstNonEmpty(strings.TrimSpace(model), localized(locale, "未填写", "not entered"))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		})
		design.Apply(ui.Text(c, localized(locale,
			"测试会使用当前输入，不会保存配置。",
			"Tests the input above; nothing is saved.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		if baseURL == "" {
			uikit.ErrorLine(c, localized(locale, "尚未配置服务地址", "No service URL configured"))
		}
		label := localized(locale, "测试当前输入", "Test current input")
		busyLabel := localized(locale, "检查中…", "Checking…")
		disabled := baseURL == ""
		if check.running {
			label = busyLabel
			disabled = true
		}
		ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			// The busy state is the official spinner with an accessible
			// progress name; the button keeps its disabled logic.
			buildBusySpinner(c, check.running, busyLabel)
			button := design.Apply(ui.Button(c, label), t.SecondaryButton())
			button.Disabled(disabled)
			if button.Clicked() && !check.running && !disabled {
				startModelCheck(runtime, check, baseURL, apiKey, locale)
				c.Invalidate()
			}
		})
		if check.result != "" {
			if check.resultErr {
				uikit.ErrorLine(c, check.result)
			} else {
				// The badge stays the short state; the detail carries the
				// measured latency and the discovered models (ZK-14) as one
				// bounded metadata line.
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					statusBadge(c, t, check.result, false)
					detail := modelCheckDetailLabel(locale, check.modelCount, check.modelNames,
						check.modelCountKnown, check.elapsed)
					design.Apply(ui.Text(c, detail), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(1)
				})
			}
		}
	})
}
