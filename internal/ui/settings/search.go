package settings

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Search page constants. Section "general" matches About (90) and sits between
// 更新 (92) and 导入 (94); the default page stays About.
const (
	SearchPageID      = "search"
	SearchPageSection = "general"
	SearchPageOrder   = 93
	SearchPageTitle   = "搜索"
)

// searchState is the Search page's draft/status state. The endpoint echoes the
// stored value; the API key is write-only (blank keeps the stored key), like
// the model page's API key field.
type searchState struct {
	loaded   bool
	endpoint string
	apiKey   string
	status   string
	// statusErr marks the last save/validation message as a failure.
	statusErr bool
}

func newSearchState() *searchState { return &searchState{} }

// buildSearchPage renders the web-search backend settings: endpoint (an
// http/https URL; empty disables web_search), the Bearer API key and the save
// action with its status. The settings store is looked up lazily so the page
// degrades to an explicit empty state without one.
func buildSearchPage(c *ui.Context, kctx cordis.Context, st *searchState, locale contracts.LocaleService) {
	t := design.Of(c)
	store, hasStore := kctx.Get(contracts.KeySettings)

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, localized(locale, SearchPageTitle, "Search"), localized(locale,
			"web_search 工具调用的搜索后端；留空则禁用 web_search。",
			"Search backend used by the web_search tool; leave the endpoint blank to disable web_search."))
		if !hasStore || store == nil {
			panel(c, t, func() {
				sectionTitle(c, t, localized(locale, "设置存储未启用", "Settings store not enabled"))
				design.Apply(ui.Text(c, localized(locale,
					"没有可用的设置存储，无法保存搜索配置。",
					"No settings store is available, so the search configuration cannot be saved.")),
					t.TextMuted(design.TypeXXS))
			})
			return
		}

		if !st.loaded {
			st.endpoint = store.Get(contracts.SettingWebSearchEndpoint, "")
			st.loaded = true
		}
		apiKeySet := firstNonEmpty(store.Get(contracts.SettingWebSearchAPIKey, ""), st.apiKey) != ""
		configured := strings.TrimSpace(st.endpoint) != ""

		panel(c, t, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				sectionTitle(c, t, localized(locale, "搜索后端", "Search backend"))
				ui.Spacer(c).Grow(1)
				if configured {
					design.Apply(ui.Badge(c, localized(locale, "已配置", "Configured")), t.Badge()).
						TextColor(t.Success)
				} else {
					design.Apply(ui.Badge(c, localized(locale, "未配置", "Not configured")), t.Badge())
				}
				if apiKeySet {
					design.Apply(ui.Badge(c, localized(locale, "API Key 已设置", "API key set")), t.Badge())
				}
			})

			field(c, t, localized(locale, "搜索端点", "Search endpoint"), func() {
				ui.TextInput(c, &st.endpoint).Label(inputLabel(locale, localized(locale, "搜索端点", "Search endpoint"))).
					Placeholder("https://search.example.com/search").FillWidth()
			})
			field(c, t, "API Key", func() {
				ui.TextInput(c, &st.apiKey).Label(inputLabel(locale, "API Key")).
					Placeholder(localized(locale,
						"API Key 以 Authorization: Bearer 发送；留空保持不变",
						"The API key is sent as Authorization: Bearer; leave blank to keep it")).FillWidth()
			})
			design.Apply(ui.Text(c, localized(locale,
				"修改后下一次 web_search 调用生效，无需重启。",
				"Changes apply to the next web_search call; no restart needed.")),
				t.TextMuted(design.TypeXXS))
		})

		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			if brandPrimaryButton(c, t, icons.Search(), localized(locale, "保存", "Save")).Clicked() {
				saveSearchSettings(st, store, locale)
			}
			ui.Spacer(c).Grow(1)
			if st.status != "" {
				statusBadge(c, t, st.status, st.statusErr)
			}
		})
	})
}

// saveSearchSettings validates the endpoint and writes the two settings rows.
// The API key is only written when the user typed a new one (an empty draft
// keeps the stored key).
func saveSearchSettings(st *searchState, store contracts.SettingsService, locale contracts.LocaleService) {
	endpoint := strings.TrimSpace(st.endpoint)
	if endpoint != "" && !validSearchEndpoint(endpoint) {
		st.status = localized(locale, "端点无效：请输入 http(s) URL",
			"Invalid endpoint: enter an http(s) URL")
		st.statusErr = true
		return
	}
	if err := store.Set(contracts.SettingWebSearchEndpoint, endpoint); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	if key := strings.TrimSpace(st.apiKey); key != "" {
		if err := store.Set(contracts.SettingWebSearchAPIKey, key); err != nil {
			st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
			st.statusErr = true
			return
		}
	}
	st.apiKey = ""
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
}

// validSearchEndpoint reports whether value is an absolute http(s) URL.
func validSearchEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return parsed.Host != ""
}
