package plugins

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// The capability tabs' card grid measures. A card is an independent object (a
// skill, a connector), so cards are allowed here; the grid is the official
// ui.GridView, which virtualizes the collection and gives every card an
// accessible item identity. Fixed card heights keep a row aligned. Every
// measure derives from the 4 DIP token scale (DESIGN.md §5.4).
const (
	// cardGridMinWidth is the narrowest card; the grid fits as many columns as
	// the content column allows (three at the standard window width).
	cardGridMinWidth float32 = design.Space8*9 + design.Space3
	// cardGridHeight is the skills card's fixed height (header + two-line
	// description + meta row fit without dead space).
	cardGridHeight float32 = design.Space8 * 4
	// connectorCardHeight is the connectors card's fixed height; a failed card
	// adds a red summary line and the detail/retry actions, so it is one step
	// taller than a skill card. The extra Space4 keeps the trailing enable
	// switch row inside the card's bottom padding: at the old height the
	// failed card's content overflowed the inner box and pressed the switch
	// against the card's bottom border.
	connectorCardHeight float32 = design.Space8*5 + design.Space2*2 + design.Space4
	// cardGridViewportThreeRows/twoRows bound each grid's own scroll area before
	// it scrolls instead of pushing the page footer away.
	cardGridViewportThreeRows float32 = cardGridHeight*3 + design.Space2*3
	cardGridViewportTwoRows   float32 = connectorCardHeight*2 + design.Space2*2
)

// buildScreen renders one frame of the Capabilities screen: the page skeleton
// (tab row + shared diagnostics), with the chosen tab rendering its own page.
func buildScreen(c *ui.Context, cfg Config, st *state) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Grow(1).Gap(design.Space4).Children(func() {
		if st.status != "" {
			if st.statusErr {
				uikit.ErrorLine(c, st.status)
			} else {
				design.Apply(ui.Text(c, st.status), t.Font(design.TypeXXS)).TextColor(t.SuccessText)
			}
		}
		buildCapabilityPage(c, cfg, st)
	})
}

// buildScreenHeaderSlot is the screen's SlotScreenHeader contribution: the
// window-band title on the TypeM step, in proportion with the host's 13 DIP
// tabs and 28 DIP band controls. The page description stays a tooltip on the
// title instead of a second in-band line (the band is window chrome).
func buildScreenHeaderSlot(c *ui.Context, t *design.Tokens, cfg Config, st *state) {
	title := st.localized(cfg.Title, "能力", "Capabilities")
	description := st.localized(cfg.Description,
		"管理技能与 MCP 连接器，扩展 ZeoKeel 的能力。",
		"Manage skills and MCP connectors that extend ZeoKeel.")
	design.Apply(ui.Text(c, title), t.Font(design.TypeM)).
		TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Tooltip(description)
}

// mcpServerKey identifies one MCP server across frames and refreshes. It is
// shared by the connectors tab for the details modal and the cards.
func mcpServerKey(server contracts.MCPPlugin, index int) string {
	switch {
	case strings.TrimSpace(server.Name) != "":
		return "name:" + strings.TrimSpace(server.Name)
	case strings.TrimSpace(server.ServerName) != "":
		return "server:" + strings.TrimSpace(server.ServerName)
	case strings.TrimSpace(server.URL) != "":
		return "url:" + strings.TrimSpace(server.URL)
	default:
		return fmt.Sprintf("index:%d", index)
	}
}

// pluginStatusFailed reports whether an MCP connection state is a failure the
// card should offer a retry for.
func pluginStatusFailed(status string) bool {
	lower := strings.ToLower(strings.TrimSpace(status))
	return strings.Contains(lower, "失败") || strings.Contains(lower, "fail") ||
		strings.Contains(lower, "error") || strings.Contains(lower, "不可用")
}

// mcpFailureSummary classifies a failed MCP status into a product-language
// cause. The raw handshake/network error stays available as the card tooltip
// and in the details modal; the collapsed card never shows raw English.
func mcpFailureSummary(st *state, status string) string {
	lower := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"),
		strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "超时"):
		return st.tr("连接超时（服务器未及时响应）", "Connection timed out (the server did not answer in time)")
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"),
		strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "authentication"), strings.Contains(lower, "鉴权"):
		return st.tr("鉴权失败（请检查访问令牌）", "Authentication failed (check the access token)")
	case strings.Contains(lower, "initialize"), strings.Contains(lower, "handshake"),
		strings.Contains(lower, "json-rpc"), strings.Contains(lower, "unmarshal"),
		strings.Contains(lower, "protocol"), strings.Contains(lower, "握手"):
		return st.tr("服务器未完成 MCP 初始化",
			"The server did not finish MCP initialization")
	case strings.Contains(lower, "refused"), strings.Contains(lower, "no such host"),
		strings.Contains(lower, "dial tcp"), strings.Contains(lower, "connect:"),
		strings.Contains(lower, "无法连接"):
		return st.tr("无法连接到服务器", "Could not reach the server")
	default:
		return st.tr("连接失败", "Connection failed")
	}
}

// startMCPRefresh reconnects the MCP servers off the UI thread: Refresh pings
// every client and can block on a hung server, so it must never run inside the
// frame's click handler. The frame shows a spinner until the posted result lands.
func (st *state) startMCPRefresh(service contracts.PluginsService) {
	if service == nil || st.refreshing || st.runtime == nil {
		return
	}
	st.refreshing = true
	st.status = ""
	st.statusErr = false
	locale := st.locale
	go func() {
		servers, err := refreshMCPsafely(service)
		st.runtime.Post(func() {
			st.refreshing = false
			if err != nil {
				st.status = uikit.T(locale, "刷新 MCP 连接器失败：", "Failed to refresh MCP connectors: ") + err.Error()
				st.statusErr = true
			} else {
				st.status = uikit.T(locale,
					fmt.Sprintf("已刷新 %d 个 MCP 连接器", len(servers)),
					fmt.Sprintf("Refreshed %d MCP connectors", len(servers)))
				st.statusErr = false
			}
			st.runtime.Invalidate()
		})
	}()
}

// refreshMCPsafely contains a panic in the backend's Refresh call so a broken
// MCP client cannot cross the goroutine boundary and take the process down.
func refreshMCPsafely(service contracts.PluginsService) (servers []contracts.MCPPlugin, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return service.Refresh(context.Background()), nil
}

// mcpDetailValue renders one detail value, with an em dash when empty.
func mcpDetailValue(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "—"
}

// pluginStatusKind maps an MCP connection state onto the shared status
// vocabulary, so the badge uses the same tint anatomy as every other badge.
func pluginStatusKind(status string) uikit.StatusKind {
	lower := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(lower, "需要授权"), strings.Contains(lower, "authorization needed"),
		strings.Contains(lower, "授权已失效"), strings.Contains(lower, "authorization expired"),
		strings.Contains(lower, "授权中"), strings.Contains(lower, "authorizing"):
		return uikit.StatusNeedsAction
	case strings.Contains(lower, "已连接"), strings.Contains(lower, "connected"),
		strings.Contains(lower, "ok"), strings.Contains(lower, "成功"):
		return uikit.StatusSuccess
	case strings.Contains(lower, "失败"), strings.Contains(lower, "fail"),
		strings.Contains(lower, "error"), strings.Contains(lower, "不可用"):
		return uikit.StatusError
	default:
		return uikit.StatusIdle
	}
}
