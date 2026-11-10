package settings

// The Network settings page: the outbound HTTP(S) proxy and NO_PROXY list. The
// process host applies the two settings (net.proxy/net.noProxy) to the process
// environment at startup, so the page explains that a restart is needed; it only
// edits/persists the stored values and shows what is currently applied.

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Network page constants. Section "general" keeps it in the same settings
// section as About/Update/Import; Order 97 places it after them.
const (
	NetworkPageID      = "network"
	NetworkPageSection = "集成"
	NetworkPageOrder   = 30
	NetworkPageTitle   = "网络"
)

// networkState is the Network page's UI-local draft/status state.
type networkState struct {
	loaded    bool
	proxy     string
	noProxy   string
	status    string
	statusErr bool
}

func newNetworkState() *networkState { return &networkState{} }

// buildNetworkPage renders the current effective proxy values and the two
// native inputs with the save action. Changes are stored now and applied by the
// host at the next start.
func buildNetworkPage(c *ui.Context, store contracts.SettingsService, st *networkState, locale contracts.LocaleService) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, NetworkPageTitle, "Network"), localized(locale,
			"出站 HTTP(S) 代理；保存后重启应用生效。",
			"Outbound HTTP(S) proxy; restart the app to apply changes."))

		if !st.loaded {
			st.loaded = true
			st.proxy = store.Get(contracts.SettingNetProxy, "")
			st.noProxy = store.Get(contracts.SettingNetNoProxy, "")
		}
		effectiveProxy := strings.TrimSpace(store.Get(contracts.SettingNetProxy, ""))
		effectiveNoProxy := strings.TrimSpace(store.Get(contracts.SettingNetNoProxy, ""))

		uikit.Section(c, localized(locale, "当前生效", "Currently applied"), func(*ui.Context) {
			buildPropertySheet(c, t, []uikit.KeyValue{
				{Key: localized(locale, "代理地址", "Proxy URL"),
					Value: firstNonEmpty(effectiveProxy, localized(locale, "直连", "Direct"))},
				{Key: "NO_PROXY",
					Value: firstNonEmpty(effectiveNoProxy, localized(locale, "未设置", "Not set"))},
			})
		})

		uikit.Section(c, localized(locale, "代理配置", "Proxy configuration"), func(*ui.Context) {
			ui.Field(c, localized(locale, "代理地址", "Proxy URL"), func() {
				ui.TextInput(c, &st.proxy).
					Label(inputLabel(locale, localized(locale, "代理地址", "Proxy URL"))).
					Placeholder("http://127.0.0.1:7890").FillWidth()
			}).Description(localized(locale, "留空表示直连。", "Leave empty to connect directly."))
			ui.Field(c, "NO_PROXY", func() {
				ui.TextInput(c, &st.noProxy).
					Label(inputLabel(locale, "NO_PROXY")).
					Placeholder("localhost,127.0.0.1,::1").FillWidth()
			}).Description(localized(locale, "逗号分隔的直连主机列表。",
				"Comma-separated hosts that connect directly."))
			design.Apply(ui.Text(c, localized(locale,
				"代理设置由进程启动时应用；修改后需要重启应用生效。",
				"The proxy is applied when the process starts; restart the app to apply changes.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})

		// H1: the save action lives in the host's fixed footer; the body keeps
		// the status line.
		statusBadgesRow(c, t, st.status, st.statusErr)
	})
}

// discardDrafts re-reads the persisted proxy rows into the drafts and clears
// the page status, so reopening after "Discard" shows the saved values.
func (st *networkState) discardDrafts(store contracts.SettingsService) {
	st.loaded = true
	st.proxy = ""
	st.noProxy = ""
	if store != nil {
		st.proxy = store.Get(contracts.SettingNetProxy, "")
		st.noProxy = store.Get(contracts.SettingNetNoProxy, "")
	}
	st.status = ""
	st.statusErr = false
}

// dirty reports whether the proxy drafts differ from the persisted rows.
func (st *networkState) dirty(store contracts.SettingsService) bool {
	if store == nil {
		return false
	}
	return strings.TrimSpace(st.proxy) != strings.TrimSpace(store.Get(contracts.SettingNetProxy, "")) ||
		strings.TrimSpace(st.noProxy) != strings.TrimSpace(store.Get(contracts.SettingNetNoProxy, ""))
}

// saveNetworkSettings validates the proxy URL (empty means direct) and writes
// both net.* keys.
func saveNetworkSettings(st *networkState, store contracts.SettingsService, locale contracts.LocaleService) {
	proxy := strings.TrimSpace(st.proxy)
	noProxy := strings.TrimSpace(st.noProxy)
	if proxy != "" && !validNetworkProxyURL(proxy) {
		st.status = localized(locale,
			"代理地址无效：请输入 http(s) URL",
			"Invalid proxy URL: enter an http(s) URL")
		st.statusErr = true
		return
	}
	if err := store.Set(contracts.SettingNetProxy, proxy); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	if err := store.Set(contracts.SettingNetNoProxy, noProxy); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	st.proxy, st.noProxy = proxy, noProxy
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
}

// validNetworkProxyURL reports whether raw is an http(s) URL with a host.
func validNetworkProxyURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return false
	}
	return parsed.Hostname() != ""
}
