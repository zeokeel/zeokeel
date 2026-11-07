package settings

import (
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

	mcpTransportHTTP  = "http"
	mcpTransportStdio = "stdio"
)

// mcpState is the MCP page's parsed server list, add-form drafts and status.
// The rows are the shared contracts.MCPServerConfig shape: URL rows use
// Streamable HTTP, command rows start a stdio child process.
type mcpState struct {
	loaded   bool
	servers  []contracts.MCPServerConfig
	parseErr string

	transport string // mcpTransportHTTP or mcpTransportStdio
	name      string
	url       string
	command   string
	args      string
	env       string

	status    string
	statusErr bool
}

func newMCPState() *mcpState { return &mcpState{transport: mcpTransportHTTP} }

// mcpRowTransport reports the effective transport of one stored row: rows with
// a command (or an explicit stdio transport) run a child process, everything
// else uses Streamable HTTP (legacy {name,url} rows included).
func mcpRowTransport(row contracts.MCPServerConfig) string {
	if strings.EqualFold(strings.TrimSpace(row.Transport), mcpTransportStdio) ||
		strings.TrimSpace(row.Command) != "" {
		return mcpTransportStdio
	}
	return mcpTransportHTTP
}

// mcpRowAddress is the display address of one server row: the URL for HTTP
// rows, or "command args..." for stdio rows.
func mcpRowAddress(row contracts.MCPServerConfig) string {
	if mcpRowTransport(row) == mcpTransportStdio {
		return strings.TrimSpace(strings.TrimSpace(row.Command) + " " + strings.Join(row.Args, " "))
	}
	return row.URL
}

// buildMCPPage renders the MCP server CRUD page: the stored server rows with
// their transport badge and native remove actions, the transport-aware add form
// and the serialized write-back status. Changes apply on the next app start
// (stdio subprocesses are started from the stored rows on launch).
func buildMCPPage(c *ui.Context, kctx cordis.Context, st *mcpState, locale contracts.LocaleService) {
	t := design.Of(c)
	store, hasStore := kctx.Get(contracts.KeySettings)

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, MCPPageTitle, localized(locale,
			"通过 Streamable HTTP 或 stdio 连接 MCP 服务器；修改后重启生效（stdio 子进程在启动时拉起）。",
			"Connect MCP servers over Streamable HTTP or stdio; changes apply after a restart (stdio subprocesses start on launch)."))
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
					transport := mcpRowTransport(server)
					badge := design.Apply(ui.Badge(c, transportBadgeLabel(transport)), t.Badge())
					if transport == mcpTransportStdio {
						badge.TextColor(t.Accent)
					}
					design.Apply(ui.Text(c, mcpRowAddress(server)), t.MonoFamily()).FontSize(12).
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
			stdio := st.transport == mcpTransportStdio
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.End).Children(func() {
				ui.Column(c).Width(150).Grow(0).Shrink(0).Gap(design.Space1).Children(func() {
					fieldLabel(c, t, localized(locale, "传输", "Transport"))
					choice := transportDisplay(st.transport)
					selector := ui.Select(c, &choice, []string{"HTTP", "STDIO"}).
						Label(localized(locale, "传输", "Transport"))
					if selector.Changed() {
						st.transport = transportValue(choice)
						stdio = st.transport == mcpTransportStdio
					}
				})
				ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
					fieldLabel(c, t, localized(locale, "名称", "Name"))
					ui.TextInput(c, &st.name).Label(inputLabel(locale, localized(locale, "名称", "Name"))).
						Placeholder(localized(locale, "例如：files", "For example: files")).FillWidth()
				})
			})
			if stdio {
				ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
					ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).Children(func() {
						fieldLabel(c, t, localized(locale, "命令", "Command"))
						ui.TextInput(c, &st.command).Label(inputLabel(locale, localized(locale, "命令", "Command"))).
							Placeholder(localized(locale, "例如：npx", "For example: npx")).FillWidth()
					})
					ui.Column(c).FillWidth().Grow(2).Gap(design.Space1).Children(func() {
						fieldLabel(c, t, localized(locale, "参数", "Arguments"))
						ui.TextInput(c, &st.args).Label(inputLabel(locale, localized(locale, "参数", "Arguments"))).
							Placeholder(localized(locale,
								"空格分隔，例如：-y server-github",
								"Space-separated, e.g. -y server-github")).FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
					fieldLabel(c, t, localized(locale, "环境变量", "Environment"))
					ui.TextInput(c, &st.env).Label(inputLabel(locale, localized(locale, "环境变量", "Environment"))).
						Placeholder(localized(locale,
							"每行 KEY=VALUE，例如：TOKEN=abc",
							"One KEY=VALUE per line, e.g. TOKEN=abc")).FillWidth()
				})
			} else {
				ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
					fieldLabel(c, t, "URL")
					ui.TextInput(c, &st.url).Label(inputLabel(locale, "URL")).
						Placeholder("https://mcp.example.com/mcp").FillWidth()
				})
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if design.Apply(ui.PrimaryButton(c, localized(locale, "添加", "Add")),
					t.PrimaryButton()).Clicked() {
					addMCPServer(st, store, locale)
				}
				ui.Spacer(c).Grow(1)
				if st.status != "" {
					statusBadge(c, t, st.status, st.statusErr)
				}
			})
		})
	})
}

// transportDisplay maps the stored transport to the selector's display text.
func transportDisplay(transport string) string {
	if transport == mcpTransportStdio {
		return "STDIO"
	}
	return "HTTP"
}

// transportValue maps the selector's display text back to the stored value.
func transportValue(display string) string {
	if display == "STDIO" {
		return mcpTransportStdio
	}
	return mcpTransportHTTP
}

// transportBadgeLabel is the compact badge text of one transport.
func transportBadgeLabel(transport string) string {
	if transport == mcpTransportStdio {
		return "stdio"
	}
	return "HTTP"
}

// loadMCPServers parses the stored JSON array through the shared contract
// parser; a malformed value is reported and treated as an empty list (the
// stored bytes are left untouched).
func loadMCPServers(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) {
	st.loaded = true
	raw := store.Get(contracts.SettingMCPServers, "")
	rows, err := contracts.ParseMCPServersJSON(raw)
	if err != nil {
		st.parseErr = localized(locale, "配置解析失败：", "Configuration parse failed: ") + err.Error()
		st.servers = nil
		return
	}
	st.servers = rows
}

// splitMCPFields splits the space-separated args/env draft into fields (simple
// Fields splitting, which matches the placeholder contract).
func splitMCPFields(value string) []string {
	return strings.Fields(value)
}

// parseMCPEnv turns the KEY=VALUE draft into the stored env slice.
func parseMCPEnv(value string) []string {
	fields := splitMCPFields(value)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// addMCPServer validates the drafts and appends the server, writing the JSON
// array back to the settings store. Invalid input never touches the store.
func addMCPServer(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) {
	name := strings.TrimSpace(st.name)
	if name == "" {
		st.status = localized(locale, "名称不能为空", "The name is required")
		st.statusErr = true
		return
	}
	row := contracts.MCPServerConfig{Name: name}
	if st.transport == mcpTransportStdio {
		command := strings.TrimSpace(st.command)
		switch {
		case command == "":
			st.status = localized(locale, "命令不能为空", "The command is required")
			st.statusErr = true
			return
		case strings.ContainsRune(command, '\x00'):
			st.status = localized(locale, "命令不能包含 NUL", "The command must not contain NUL")
			st.statusErr = true
			return
		}
		args := splitMCPFields(st.args)
		for _, existing := range st.servers {
			if mcpRowTransport(existing) == mcpTransportStdio &&
				strings.TrimSpace(existing.Command) == command &&
				strings.Join(existing.Args, "\x00") == strings.Join(args, "\x00") {
				st.status = localized(locale, "该服务器已存在", "That server already exists")
				st.statusErr = true
				return
			}
		}
		row.Transport = mcpTransportStdio
		row.Command = command
		row.Args = args
		row.Env = parseMCPEnv(st.env)
	} else {
		url := strings.TrimSpace(st.url)
		switch {
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
		for _, existing := range st.servers {
			if mcpRowTransport(existing) == mcpTransportHTTP &&
				strings.EqualFold(strings.TrimSpace(existing.URL), url) {
				st.status = localized(locale, "该 URL 已存在", "That URL already exists")
				st.statusErr = true
				return
			}
		}
		// HTTP rows keep the legacy {name,url} shape (no transport field).
		row.URL = url
	}

	previous := append([]contracts.MCPServerConfig(nil), st.servers...)
	st.servers = append(st.servers, row)
	if !saveMCPServers(st, store, locale) {
		st.servers = previous
		return
	}
	st.name = ""
	st.url = ""
	st.command = ""
	st.args = ""
	st.env = ""
}

// removeMCPServer drops one row and writes the remaining servers back.
func removeMCPServer(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService, index int) {
	if index < 0 || index >= len(st.servers) {
		return
	}
	previous := append([]contracts.MCPServerConfig(nil), st.servers...)
	kept := make([]contracts.MCPServerConfig, 0, len(st.servers)-1)
	kept = append(kept, st.servers[:index]...)
	kept = append(kept, st.servers[index+1:]...)
	st.servers = kept
	if !saveMCPServers(st, store, locale) {
		st.servers = previous
	}
}

// saveMCPServers serializes the list through the shared contract encoder and
// writes it back; failures revert the caller's in-memory list and surface the
// error in the status badge.
func saveMCPServers(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) bool {
	encoded, err := contracts.EncodeMCPServersJSON(st.servers)
	if err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return false
	}
	if err := store.Set(contracts.SettingMCPServers, encoded); err != nil {
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
