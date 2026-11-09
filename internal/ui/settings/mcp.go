package settings

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// MCP page constants. Section "general" sits after 搜索 (93) and 导入 (94); the
// default page stays About.
const (
	MCPPageID      = "mcp"
	MCPPageSection = "集成"
	MCPPageOrder   = 10
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

	// Workspace mcp.json opt-in state (loaded once per page visit).
	workspaceOptIn  bool
	workspaceLoaded bool
	workspaceRoot   string

	// confirmRemove is the index of the server row whose removal waits for the
	// native confirmation dialog (-1 when none is open).
	confirmRemove int
}

func newMCPState() *mcpState {
	return &mcpState{transport: mcpTransportHTTP, confirmRemove: -1}
}

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

// mcpWorkspaceOptInEnabled normalizes the stored opt-in value: only an explicit
// true enables reading the workspace file; absent/malformed values stay off.
func mcpWorkspaceOptInEnabled(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
}

// loadMCPWorkspaceState reads the opt-in setting and the current workspace root
// once per page visit. The workspace service is optional.
func loadMCPWorkspaceState(st *mcpState, store contracts.SettingsService, kctx cordis.Context) {
	st.workspaceLoaded = true
	st.workspaceOptIn = mcpWorkspaceOptInEnabled(store.Get(contracts.SettingMCPWorkspaceOptIn, "false"))
	workspace, ok := kctx.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return
	}
	current, has, err := workspace.Current(context.Background())
	if err == nil && has {
		st.workspaceRoot = current.Root
	}
}

// buildMCPPage renders the MCP server CRUD page: the stored server rows with
// their transport badge and native remove actions (confirmed with the native
// alert dialog), the workspace opt-in switch and the transport-aware add form.
// Changes apply on the next app start (stdio subprocesses are started from the
// stored rows on launch).
func buildMCPPage(c *ui.Context, kctx cordis.Context, st *mcpState, locale contracts.LocaleService) {
	t := design.Of(c)
	store, hasStore := kctx.Get(contracts.KeySettings)

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, MCPPageTitle, localized(locale,
			"通过 Streamable HTTP 或 stdio 连接 MCP 服务器；修改后重启生效（stdio 子进程在启动时拉起）。",
			"Connect MCP servers over Streamable HTTP or stdio; changes apply after a restart (stdio subprocesses start on launch)."))
		if !hasStore || store == nil {
			uikit.Section(c, localized(locale, "设置存储未启用", "Settings store not enabled"), func(*ui.Context) {
				uikit.EmptyState(c, localized(locale,
					"没有可用的设置存储，无法保存 MCP 服务器配置。",
					"No settings store is available, so the MCP servers cannot be saved."))
			})
			return
		}
		if !st.loaded {
			loadMCPServers(st, store, locale)
		}
		if !st.workspaceLoaded {
			loadMCPWorkspaceState(st, store, kctx)
		}
		if st.parseErr != "" {
			uikit.ErrorLine(c, st.parseErr)
		}

		// Stored server rows: rows separated by hairlines, not cards.
		uikit.Section(c, localized(locale, "MCP 服务器", "MCP servers"), func(*ui.Context) {
			if len(st.servers) == 0 {
				uikit.EmptyState(c, localized(locale,
					"还没有配置 MCP 服务器", "No MCP servers configured yet"))
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
					design.Apply(ui.Text(c, mcpRowAddress(server)),
						t.Font(design.TypeXS), t.MonoFamily()).
						TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
					if design.Apply(ui.Button(c, localized(locale, "移除", "Remove")),
						t.DangerButton()).Tooltip(localized(locale,
						"移除该服务器", "Remove this server")).Clicked() {
						st.confirmRemove = index
						c.Invalidate()
					}
				})
			}
		})

		// Workspace mcp.json opt-in: applied on the next app start.
		uikit.Section(c, localized(locale, "工作区 MCP", "Workspace MCP"), func(*ui.Context) {
			ui.Form(c, func() {
				if switchField(c, localized(locale, "读取工作区 mcp.json", "Read workspace mcp.json"),
					mcpWorkspaceHint(st, locale), &st.workspaceOptIn) {
					if err := store.Set(contracts.SettingMCPWorkspaceOptIn,
						fmt.Sprintf("%t", st.workspaceOptIn)); err != nil {
						st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
						st.statusErr = true
					} else {
						st.status = localized(locale, "已保存", "Saved")
						st.statusErr = false
					}
					c.Invalidate()
				}
			})
		})

		// Add form: every input has a visible field label and a description.
		uikit.Section(c, localized(locale, "添加服务器", "Add server"), func(*ui.Context) {
			stdio := st.transport == mcpTransportStdio
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(150).Grow(0).Shrink(0).Children(func() {
					ui.Field(c, localized(locale, "传输", "Transport"), func() {
						choice := transportDisplay(st.transport)
						selector := ui.Select(c, &choice, []string{"HTTP", "STDIO"}).
							Label(localized(locale, "传输", "Transport"))
						if selector.Changed() {
							st.transport = transportValue(choice)
							stdio = st.transport == mcpTransportStdio
						}
					}).Description(localized(locale, "HTTP 用 URL 连接；STDIO 启动本地子进程。",
						"HTTP connects by URL; STDIO starts a local subprocess."))
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "名称", "Name"), func() {
						ui.TextInput(c, &st.name).
							Label(inputLabel(locale, localized(locale, "名称", "Name"))).
							Placeholder(localized(locale, "例如：files", "For example: files")).FillWidth()
					}).Description(localized(locale, "在服务器列表中显示的名称。",
						"The name shown in the server list."))
				})
			})
			if stdio {
				// The subprocess fields form one labelled group: assistive
				// technology reads the legend with every field inside (mygo
				// form.md "Fieldsets"), instead of three unrelated rows.
				ui.Fieldset(c, localized(locale, "STDIO 进程", "STDIO process"), func() {
					ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
						ui.Column(c).FillWidth().Grow(1).Children(func() {
							ui.Field(c, localized(locale, "命令", "Command"), func() {
								ui.TextInput(c, &st.command).
									Label(inputLabel(locale, localized(locale, "命令", "Command"))).
									Placeholder(localized(locale, "例如：npx", "For example: npx")).FillWidth()
							}).Description(localized(locale, "启动子进程的可执行文件。",
								"The executable that starts the subprocess."))
						})
						ui.Column(c).FillWidth().Grow(2).Children(func() {
							ui.Field(c, localized(locale, "参数", "Arguments"), func() {
								ui.TextInput(c, &st.args).
									Label(inputLabel(locale, localized(locale, "参数", "Arguments"))).
									Placeholder(localized(locale,
										"空格分隔，例如：-y server-github",
										"Space-separated, e.g. -y server-github")).FillWidth()
							}).Description(localized(locale, "按空格拆分为参数列表。",
								"Split on spaces into the argument list."))
						})
					})
					ui.Field(c, localized(locale, "环境变量", "Environment"), func() {
						ui.TextInput(c, &st.env).
							Label(inputLabel(locale, localized(locale, "环境变量", "Environment"))).
							Placeholder(localized(locale,
								"每行 KEY=VALUE，例如：TOKEN=abc",
								"One KEY=VALUE per line, e.g. TOKEN=abc")).FillWidth()
					}).Description(localized(locale, "传给子进程的环境变量。",
						"Environment variables passed to the subprocess."))
				})
			} else {
				ui.Field(c, "URL", func() {
					ui.TextInput(c, &st.url).Label(inputLabel(locale, "URL")).
						Placeholder("https://mcp.example.com/mcp").FillWidth()
				}).Description(localized(locale, "Streamable HTTP 端点。",
					"The Streamable HTTP endpoint."))
			}
			statusBadgesRow(c, t, st.status, st.statusErr)
			uikit.DialogFooter(c, []uikit.DialogAction{{
				Label: localized(locale, "添加", "Add"), Primary: true,
				OnClick: func() {
					addMCPServer(st, store, locale)
					c.Invalidate()
				},
			}})
		})

		// Removing a server is destructive: confirm it with the native dialog.
		if st.confirmRemove >= 0 && st.confirmRemove < len(st.servers) {
			index := st.confirmRemove
			open := true
			switch ui.AlertDialog(c, &open,
				localized(locale, "移除 MCP 服务器", "Remove MCP server"),
				localized(locale, "确认移除「", "Remove ")+st.servers[index].Name+
					localized(locale, "」？此操作无法撤销。", "? This cannot be undone."),
				localized(locale, "取消", "Cancel"),
				localized(locale, "移除服务器", "Remove server")) {
			case 1:
				removeMCPServer(st, store, locale, index)
				st.confirmRemove = -1
				c.Invalidate()
			}
			if !open {
				st.confirmRemove = -1
			}
		}
	})
}

// mcpWorkspaceHint describes the workspace file the opt-in reads.
func mcpWorkspaceHint(st *mcpState, locale contracts.LocaleService) string {
	if root := strings.TrimSpace(st.workspaceRoot); root != "" {
		path := filepath.Join(root, "mcp.json")
		return localized(locale,
			"当前工作区："+root+"；启动时读取 "+path+" 中的额外服务器，修改后重启生效。",
			"Current workspace: "+root+"; extra servers are read from "+path+" on launch, after a restart.")
	}
	return localized(locale,
		"当前没有打开工作区；打开工作区并重启后读取其 mcp.json。",
		"No workspace is open; open one and restart to read its mcp.json.")
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
