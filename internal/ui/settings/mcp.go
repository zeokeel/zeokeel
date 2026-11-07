package settings

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// MCP page constants. Section "general" sits after 搜索 (93) and 导入 (94); the
// default page stays About.
const (
	MCPPageID      = "mcp"
	MCPPageSection = "general"
	MCPPageOrder   = 96
	MCPPageTitle   = "MCP"
)

// mcpServerEntry is one stored MCP server row (the contracts JSON shape).
type mcpServerEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// mcpState is the MCP page's parsed server list, add-form drafts and status.
type mcpState struct {
	loaded   bool
	servers  []mcpServerEntry
	parseErr string

	name string
	url  string

	status    string
	statusErr bool
}

func newMCPState() *mcpState { return &mcpState{} }

// buildMCPPage renders the MCP server CRUD page: the stored server rows with
// their native remove actions, the add form with validation and the
// serialized write-back status. Changes apply on the next app start (the
// toolkit builds its clients from the stored rows on startup).
func buildMCPPage(c *ui.Context, kctx cordis.Context, st *mcpState, locale contracts.LocaleService) {
	t := design.Of(c)
	store, hasStore := kctx.Get(contracts.KeySettings)

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, MCPPageTitle, localized(locale,
			"通过 Streamable HTTP 连接 MCP 服务器；修改后重启生效。",
			"Connect MCP servers over Streamable HTTP; changes apply after a restart."))
		if !hasStore || store == nil {
			panel(c, t, func() {
				sectionTitle(c, t, localized(locale, "设置存储未启用", "Settings store not enabled"))
				design.Apply(ui.Text(c, localized(locale,
					"没有可用的设置存储，无法保存 MCP 服务器配置。",
					"No settings store is available, so the MCP servers cannot be saved.")),
					t.TextMuted(design.TypeXXS))
			})
			return
		}
		if !st.loaded {
			loadMCPServers(st, store, locale)
		}
		if st.parseErr != "" {
			buildMCPServerError(c, t, st.parseErr)
		}

		// Stored server rows.
		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "MCP 服务器", "MCP servers"))
			if len(st.servers) == 0 {
				design.Apply(ui.Text(c, localized(locale,
					"还没有配置 MCP 服务器", "No MCP servers configured yet")), t.TextMuted(design.TypeXXS))
				return
			}
			for index, server := range st.servers {
				index, server := index, server
				ui.Divider(c)
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, server.Name), t.Font(design.TypeS)).
						FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
					design.Apply(ui.Text(c, server.URL), t.MonoFamily()).FontSize(12).
						TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
					if design.Apply(ui.Button(c, localized(locale, "移除", "Remove")),
						secondaryButtonStyle(t)).Tooltip(localized(locale, "移除该服务器", "Remove this server")).Clicked() {
						removeMCPServer(st, store, locale, index)
					}
				})
			}
		})

		// Add form.
		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "添加服务器", "Add server"))
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.End).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
					fieldLabel(c, t, localized(locale, "名称", "Name"))
					ui.TextInput(c, &st.name).Label(inputLabel(locale, localized(locale, "名称", "Name"))).
						Placeholder(localized(locale, "例如：files", "For example: files")).FillWidth()
				})
				ui.Column(c).FillWidth().Grow(2).Gap(design.Space1).Children(func() {
					fieldLabel(c, t, "URL")
					ui.TextInput(c, &st.url).Label(inputLabel(locale, "URL")).
						Placeholder("https://mcp.example.com/mcp").FillWidth()
				})
				if design.Apply(ui.PrimaryButton(c, localized(locale, "添加", "Add")),
					t.PrimaryButton()).Clicked() {
					addMCPServer(st, store, locale)
				}
			})
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spacer(c).Grow(1)
			if st.status != "" {
				statusBadge(c, t, st.status, st.statusErr)
			}
		})
	})
}

// loadMCPServers parses the stored JSON array; a malformed value is reported
// and treated as an empty list (the stored bytes are left untouched).
func loadMCPServers(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) {
	st.loaded = true
	raw := strings.TrimSpace(store.Get(contracts.SettingMCPServers, "[]"))
	if raw == "" {
		st.servers = nil
		return
	}
	var servers []mcpServerEntry
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		st.parseErr = localized(locale, "配置解析失败：", "Configuration parse failed: ") + err.Error()
		st.servers = nil
		return
	}
	st.servers = servers
}

// addMCPServer validates the drafts and appends the server, writing the JSON
// array back to the settings store. Invalid input never touches the store.
func addMCPServer(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) {
	name := strings.TrimSpace(st.name)
	url := strings.TrimSpace(st.url)
	switch {
	case name == "":
		st.status = localized(locale, "名称不能为空", "The name is required")
		st.statusErr = true
		return
	case url == "":
		st.status = localized(locale, "URL 不能为空", "The URL is required")
		st.statusErr = true
		return
	case !validSearchEndpoint(url):
		st.status = localized(locale, "URL 无效：请输入 http(s) URL",
			"Invalid URL: enter an http(s) URL")
		st.statusErr = true
		return
	}
	for _, server := range st.servers {
		if strings.EqualFold(strings.TrimSpace(server.URL), url) {
			st.status = localized(locale, "该 URL 已存在", "That URL already exists")
			st.statusErr = true
			return
		}
	}
	previous := append([]mcpServerEntry(nil), st.servers...)
	st.servers = append(st.servers, mcpServerEntry{Name: name, URL: url})
	if !saveMCPServers(st, store, locale) {
		st.servers = previous
		return
	}
	st.name = ""
	st.url = ""
}

// removeMCPServer drops one row and writes the remaining servers back.
func removeMCPServer(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService, index int) {
	if index < 0 || index >= len(st.servers) {
		return
	}
	previous := append([]mcpServerEntry(nil), st.servers...)
	kept := make([]mcpServerEntry, 0, len(st.servers)-1)
	kept = append(kept, st.servers[:index]...)
	kept = append(kept, st.servers[index+1:]...)
	st.servers = kept
	if !saveMCPServers(st, store, locale) {
		st.servers = previous
	}
}

// saveMCPServers serializes the list and writes it back; failures revert the
// caller's in-memory list and surface the error in the status badge.
func saveMCPServers(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) bool {
	data := []byte("[]")
	if len(st.servers) > 0 {
		encoded, err := json.Marshal(st.servers)
		if err != nil {
			st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
			st.statusErr = true
			return false
		}
		data = encoded
	}
	if err := store.Set(contracts.SettingMCPServers, string(data)); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return false
	}
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
	return true
}

// buildMCPServerError is the page's danger row.
func buildMCPServerError(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText)
	})
}
