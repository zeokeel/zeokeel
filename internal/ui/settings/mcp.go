package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
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

	// The MCP server collection is the official List: keyed by server name,
	// named by it and single-row selectable; the inline 移除 button stays the
	// row action.
	list ui.ListState
	sel  int

	transport string // mcpTransportHTTP or mcpTransportStdio
	name      string
	url       string
	command   string
	args      string
	env       string

	status    string
	statusErr bool
	// envErr is the field-level error of the environment draft (one
	// KEY=VALUE per line); it clears when the user edits the field.
	envErr string

	// Workspace mcp.json opt-in state (loaded once per page visit).
	workspaceOptIn  bool
	workspaceLoaded bool
	workspaceRoot   string

	// confirmRemove is the index of the server row whose removal waits for the
	// native confirmation dialog (-1 when none is open).
	confirmRemove int
	removeOpen    bool // the removal confirmation's open flag (persistent for ConfirmDanger)

	// editIndex is the stored row the form is editing (-1 = add mode). The same
	// form serves both, so a typo is fixed in place instead of delete+re-add.
	editIndex int

	// testing is true while the async "test connection" action runs; testStatus
	// carries its localized result.
	testing    bool
	testStatus string
	testErr    bool

	// transient detects a new visit to the page, clearing the previous visit's
	// result copy (the form draft itself is guarded by Dirty).
	transient transientStatus
}

func newMCPState() *mcpState {
	return &mcpState{transport: mcpTransportHTTP, confirmRemove: -1, editIndex: -1, sel: -1}
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

// mcpServerRowKey is the official List key of one MCP server row: the name
// when it is unique among the rows, else the name plus the address, so two
// servers with the same name still have stable distinct keys.
func mcpServerRowKey(servers []contracts.MCPServerConfig, index int) any {
	if index < 0 || index >= len(servers) {
		return nil
	}
	name := strings.TrimSpace(servers[index].Name)
	duplicate := false
	for other := range servers {
		if other != index && strings.TrimSpace(servers[other].Name) == name {
			duplicate = true
			break
		}
	}
	if !duplicate {
		return name
	}
	return name + "\x00" + mcpRowAddress(servers[index])
}

// buildMCPServerRow renders one stored MCP server: its name, transport badge,
// address and the inline remove action. The official List owns the row paint
// and the choice, so the selected row's copy uses the accent's on-color.
func buildMCPServerRow(c *ui.Context, t *design.Tokens, st *mcpState, index int,
	locale contracts.LocaleService, selected bool) {
	server := st.servers[index]
	primary, _, tertiary := listRowTextColors(t, selected)
	ui.Row(c).FillWidth().Padding(design.Space1, design.Space2).
		AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, server.Name), t.Font(design.TypeS)).
			FontWeight(600).TextColor(primary).MaxLines(1).Ellipsis("…")
		// A transport is a type, not a status: neutral badge, no accent metadata
		// (DESIGN.md §5.2: metadata never uses the accent).
		transport := mcpRowTransport(server)
		design.Apply(ui.Badge(c, transportBadgeLabel(transport)), t.Badge())
		design.Apply(ui.Text(c, mcpRowAddress(server)),
			t.Font(design.TypeXS), t.MonoFamily()).
			TextColor(tertiary).Grow(1).MaxLines(1).Ellipsis("…")
		// The row's trailing ⋯ menu owns edit and the destructive remove, so a
		// list of servers paints no red button per row.
		label := localized(locale, "服务器操作", "Server actions")
		menu := uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "mcp-actions/" + server.Name,
			Label:   label,
			Tooltip: label,
			Icon:    icons.More(),
			Size:    design.IconSizeS,
		})
		menu.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(localized(locale, "编辑", "Edit")).Chosen() {
				st.fillMCPForm(index)
				c.Invalidate()
			}
			remove := m.Item(localized(locale, "移除", "Remove"))
			remove.Danger()
			if remove.Chosen() {
				st.confirmRemove = index
				st.removeOpen = true
				c.Invalidate()
			}
		})
	})
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

		// Stored server rows: the official List keeps the hairline rows, names
		// them for assistive technology and owns the keyboard selection; the
		// inline remove action keeps its confirmation semantics.
		uikit.Section(c, localized(locale, "MCP 服务器", "MCP servers"), func(*ui.Context) {
			if len(st.servers) == 0 {
				uikit.EmptyState(c, localized(locale,
					"还没有配置 MCP 服务器", "No MCP servers configured yet"))
				return
			}
			bindSettingsList(&st.list, &st.sel, len(st.servers), func(index int) any {
				if index < 0 || index >= len(st.servers) {
					return nil
				}
				return mcpServerRowKey(st.servers, index)
			}, func(index int) string {
				if index < 0 || index >= len(st.servers) {
					return ""
				}
				return firstNonEmpty(strings.TrimSpace(st.servers[index].Name),
					mcpRowAddress(st.servers[index]),
					localized(locale, "(未命名服务器)", "(Untitled server)"))
			})
			ui.List(c, &st.list, len(st.servers)).
				Label(localized(locale, "MCP 服务器列表", "MCP servers list")).
				Gap(design.Space1).
				Rows(func(row ui.ListRow) {
					buildMCPServerRow(row.Context, t, st, row.Index, locale, row.Selected())
				})
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

		// Server form: one native form serves add and edit. The fields come
		// first, then the form's status line, then one action row at the end
		// (Test connection beside Cancel/Add): the actions follow the input they
		// operate on instead of preceding it.
		sectionTitle := localized(locale, "添加服务器", "Add server")
		if st.editIndex >= 0 {
			sectionTitle = localized(locale, "编辑服务器", "Edit server")
		}
		runtime, hasRuntime := kctx.Get(contracts.KeyUIRuntime)
		uikit.Section(c, sectionTitle, func(*ui.Context) {
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
						// A text area, not a password input: one KEY=VALUE per line
						// needs real newlines (and values may contain spaces), and
						// mygo text areas have no password mode by design. The
						// Plugins page masks these values in its read-only details.
						envInput := ui.TextArea(c, &st.env).
							Label(inputLabel(locale, localized(locale, "环境变量", "Environment"))).
							Placeholder(localized(locale,
								"每行 KEY=VALUE，例如：GITHUB_TOKEN=abc",
								"One KEY=VALUE per line, e.g. GITHUB_TOKEN=abc")).
							MinHeight(design.Space6 * 2).FillWidth()
						if envInput.Changed() {
							st.envErr = ""
						}
					}).Description(localized(locale, "传给子进程的环境变量；每行一个 KEY=VALUE，值可以包含空格。",
						"Environment variables passed to the subprocess; one KEY=VALUE per line, values may contain spaces.")).
						Error(st.envErr)
				})
			} else {
				ui.Field(c, "URL", func() {
					ui.TextInput(c, &st.url).Label(inputLabel(locale, "URL")).
						Placeholder("https://mcp.example.com/mcp").FillWidth()
				}).Description(localized(locale, "Streamable HTTP 端点。",
					"The Streamable HTTP endpoint."))
			}
			// The form's own result line sits between the fields and the actions.
			statusBadgesRow(c, t, st.status, st.statusErr)
			if st.testStatus != "" {
				if st.testErr {
					uikit.ErrorLine(c, st.testStatus)
				} else {
					design.Apply(ui.Text(c, st.testStatus), t.Font(design.TypeXS)).
						TextColor(t.SuccessText).MaxLines(2)
				}
			}
			// One action row at the form's end: the secondary connection test
			// (it checks the draft without saving) on the left, Cancel/Add on the
			// right.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if st.testing {
					ui.Spinner(c).Label(localized(locale, "正在测试…", "Testing…")).
						Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
				}
				testLabel := localized(locale, "测试连接", "Test connection")
				if st.testing {
					testLabel = localized(locale, "正在测试…", "Testing…")
				}
				test := design.Apply(ui.Button(c, testLabel), t.SecondaryButton()).
					Disabled(st.testing).
					Tooltip(localized(locale,
						"用当前填写的内容测试连接，不会保存",
						"Test the connection with the current draft; nothing is saved"))
				if test.Clicked() && !st.testing && hasRuntime && runtime != nil {
					st.startMCPTest(runtime, locale)
					c.Invalidate()
				}
				ui.Spacer(c).Grow(1)
				if st.editIndex >= 0 {
					if design.Apply(ui.Button(c, localized(locale, "取消编辑", "Cancel edit")),
						t.SecondaryButton()).Clicked() {
						st.resetMCPForm()
						c.Invalidate()
					}
				}
				submitLabel := localized(locale, "添加", "Add")
				if st.editIndex >= 0 {
					submitLabel = localized(locale, "保存", "Save")
				}
				if design.Apply(ui.PrimaryButton(c, submitLabel), t.PrimaryButton()).Clicked() {
					submitMCPServer(st, store, locale)
					c.Invalidate()
				}
			})
		})

		// Removing a server is destructive: confirm it with the native dialog.
		if st.confirmRemove >= 0 && st.confirmRemove < len(st.servers) {
			index := st.confirmRemove
			name := st.servers[index].Name
			uikit.ConfirmDanger(c, &st.removeOpen, uikit.ConfirmDangerOptions{
				Key:   "mcp.remove",
				Title: localized(locale, "移除 MCP 服务器", "Remove MCP server"),
				Message: localized(locale, "确认移除「", "Remove ") + name +
					localized(locale, "」？此操作无法撤销。", "? This cannot be undone."),
				Confirm: localized(locale, "移除服务器", "Remove server"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					removeMCPServer(st, store, locale, index)
					if st.editIndex == index {
						st.resetMCPForm()
					} else if st.editIndex > index {
						st.editIndex--
					}
					st.confirmRemove = -1
					c.Invalidate()
				},
			})
			if !st.removeOpen {
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

// splitMCPEnvLines splits the environment draft on newlines (never on spaces):
// the hint promises one KEY=VALUE per line, so values such as
// "TOKEN=a b" stay whole. The old strings.Fields split tore them apart.
func splitMCPEnvLines(value string) []string {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parseMCPEnv turns the KEY=VALUE draft into the stored env slice and reports
// the first malformed line: every non-empty line needs a non-empty KEY before
// the "=" so the child process cannot receive a meaningless entry.
func parseMCPEnv(value string, locale contracts.LocaleService) ([]string, string) {
	lines := splitMCPEnvLines(value)
	if len(lines) == 0 {
		return nil, ""
	}
	for index, line := range lines {
		key, _, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, localized(locale,
				fmt.Sprintf("环境变量格式无效（第 %d 行）：每行应为 KEY=VALUE", index+1),
				fmt.Sprintf("Invalid environment row %d: each line must be KEY=VALUE", index+1))
		}
	}
	return lines, ""
}

// buildMCPDraft validates the drafts and returns the settings row. Problems are
// reported in st.status/st.statusErr (or the environment field's st.envErr);
// false means the draft may not be submitted/tested.
func buildMCPDraft(st *mcpState, locale contracts.LocaleService, checkDuplicates bool) (contracts.MCPServerConfig, bool) {
	name := strings.TrimSpace(st.name)
	if name == "" {
		st.status = localized(locale, "名称不能为空", "The name is required")
		st.statusErr = true
		return contracts.MCPServerConfig{}, false
	}
	row := contracts.MCPServerConfig{Name: name}
	if st.transport == mcpTransportStdio {
		command := strings.TrimSpace(st.command)
		switch {
		case command == "":
			st.status = localized(locale, "命令不能为空", "The command is required")
			st.statusErr = true
			return contracts.MCPServerConfig{}, false
		case strings.ContainsRune(command, '\x00'):
			st.status = localized(locale, "命令不能包含 NUL", "The command must not contain NUL")
			st.statusErr = true
			return contracts.MCPServerConfig{}, false
		}
		args := splitMCPFields(st.args)
		if checkDuplicates {
			for index, existing := range st.servers {
				if index == st.editIndex {
					continue
				}
				if mcpRowTransport(existing) == mcpTransportStdio &&
					strings.TrimSpace(existing.Command) == command &&
					strings.Join(existing.Args, "\x00") == strings.Join(args, "\x00") {
					st.status = localized(locale, "该服务器已存在", "That server already exists")
					st.statusErr = true
					return contracts.MCPServerConfig{}, false
				}
			}
		}
		env, envErr := parseMCPEnv(st.env, locale)
		if envErr != "" {
			st.envErr = envErr
			st.status = ""
			st.statusErr = false
			return contracts.MCPServerConfig{}, false
		}
		st.envErr = ""
		row.Transport = mcpTransportStdio
		row.Command = command
		row.Args = args
		row.Env = env
		return row, true
	}
	url := strings.TrimSpace(st.url)
	switch {
	case url == "":
		st.status = localized(locale, "URL 不能为空", "The URL is required")
		st.statusErr = true
		return contracts.MCPServerConfig{}, false
	case !validSearchEndpoint(url):
		st.status = localized(locale, "URL 无效：请输入 http(s) URL",
			"Invalid URL: enter an http(s) URL")
		st.statusErr = true
		return contracts.MCPServerConfig{}, false
	}
	if checkDuplicates {
		for index, existing := range st.servers {
			if index == st.editIndex {
				continue
			}
			if mcpRowTransport(existing) == mcpTransportHTTP &&
				strings.EqualFold(strings.TrimSpace(existing.URL), url) {
				st.status = localized(locale, "该 URL 已存在", "That URL already exists")
				st.statusErr = true
				return contracts.MCPServerConfig{}, false
			}
		}
	}
	// HTTP rows keep the legacy {name,url} shape (no transport field).
	row.URL = url
	return row, true
}

// submitMCPServer validates the drafts and writes the add/edit result back to
// the settings store. Editing replaces the stored row in place, so a typo is
// fixed without deleting and re-adding the server.
func submitMCPServer(st *mcpState, store contracts.SettingsService, locale contracts.LocaleService) {
	row, ok := buildMCPDraft(st, locale, true)
	if !ok {
		return
	}
	index := st.editIndex
	previous := append([]contracts.MCPServerConfig(nil), st.servers...)
	if index >= 0 && index < len(st.servers) {
		st.servers[index] = row
	} else {
		st.servers = append(st.servers, row)
	}
	if !saveMCPServers(st, store, locale) {
		st.servers = previous
		return
	}
	st.resetMCPForm()
}

// fillMCPForm loads one stored row into the form for editing.
func (st *mcpState) fillMCPForm(index int) {
	if index < 0 || index >= len(st.servers) {
		return
	}
	server := st.servers[index]
	st.editIndex = index
	st.transport = mcpRowTransport(server)
	st.name = strings.TrimSpace(server.Name)
	st.url = strings.TrimSpace(server.URL)
	st.command = strings.TrimSpace(server.Command)
	st.args = strings.Join(server.Args, " ")
	st.env = strings.Join(server.Env, "\n")
	st.envErr = ""
	st.testStatus = ""
	st.testErr = false
}

// mcpDraftDirty reports whether the add/edit form holds an unsaved draft. An
// edit form that still matches the stored row is not a draft, so only entering
// edit mode and closing the settings surface does not trigger the guard.
func mcpDraftDirty(st *mcpState) bool {
	if st.editIndex >= 0 {
		if st.editIndex >= len(st.servers) {
			return false
		}
		server := st.servers[st.editIndex]
		return st.transport != mcpRowTransport(server) ||
			strings.TrimSpace(st.name) != strings.TrimSpace(server.Name) ||
			strings.TrimSpace(st.url) != strings.TrimSpace(server.URL) ||
			strings.TrimSpace(st.command) != strings.TrimSpace(server.Command) ||
			strings.TrimSpace(st.args) != strings.Join(server.Args, " ") ||
			strings.TrimSpace(st.env) != strings.Join(server.Env, "\n")
	}
	return st.transport != mcpTransportHTTP ||
		strings.TrimSpace(st.name) != "" || strings.TrimSpace(st.url) != "" ||
		strings.TrimSpace(st.command) != "" || strings.TrimSpace(st.args) != "" ||
		strings.TrimSpace(st.env) != ""
}

// discardMCPDraft throws the form draft and its transient status away, so the
// host's Discard action reopens a clean MCP page.
func discardMCPDraft(st *mcpState) {
	st.resetMCPForm()
	st.status = ""
	st.statusErr = false
	st.testStatus = ""
	st.testErr = false
}

// resetMCPForm clears the form and returns it to add mode.
func (st *mcpState) resetMCPForm() {
	st.editIndex = -1
	st.name = ""
	st.url = ""
	st.command = ""
	st.args = ""
	st.env = ""
	st.envErr = ""
	st.testStatus = ""
	st.testErr = false
}

// MCPConnectionTester is the seam behind the async "test connection" action:
// the default probes the configured endpoint/executable, tests replace it.
var MCPConnectionTester = defaultMCPConnectionTest

// defaultMCPConnectionTest probes one draft server: a stdio command must exist
// on PATH, an HTTP endpoint must answer a JSON-RPC initialize request. Any HTTP
// answer below 500 counts as reachable (401/403 are reported as auth problems).
func defaultMCPConnectionTest(ctx context.Context, cfg contracts.MCPServerConfig) error {
	if mcpRowTransport(cfg) == mcpTransportStdio {
		command := strings.TrimSpace(cfg.Command)
		if command == "" {
			return errors.New("command is required")
		}
		if _, err := exec.LookPath(command); err != nil {
			return fmt.Errorf("executable not found: %w", err)
		}
		return nil
	}
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		return errors.New("url is required")
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"ZeoKeel","version":"probe"}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("server returned status %d", response.StatusCode)
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("server returned status %d", response.StatusCode)
	}
	return nil
}

// startMCPTest runs the draft's connection test off the UI thread and publishes
// the localized result through UIRuntime.Post.
func (st *mcpState) startMCPTest(runtime contracts.UIRuntime, locale contracts.LocaleService) {
	if st.testing || runtime == nil {
		return
	}
	st.testStatus = ""
	st.testErr = false
	st.status = ""
	st.statusErr = false
	row, ok := buildMCPDraft(st, locale, false)
	if !ok {
		st.testStatus = firstNonEmpty(st.envErr, st.status, localized(locale, "请先填写完整配置", "Fill in the configuration first"))
		st.testErr = true
		st.envErr = ""
		st.status = ""
		st.statusErr = false
		return
	}
	st.testing = true
	go func() {
		err := MCPConnectionTester(context.Background(), row)
		runtime.Post(func() {
			st.testing = false
			if err != nil {
				st.testStatus = mcpTestStatusText(locale, err)
				st.testErr = true
			} else {
				st.testStatus = localized(locale, "连接测试通过", "Connection test passed")
				st.testErr = false
			}
			runtime.Invalidate()
		})
	}()
}

// mcpTestStatusText localizes a connection-test failure: a known cause gets
// product language, anything else keeps the bounded raw detail.
func mcpTestStatusText(locale contracts.LocaleService, err error) string {
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "executable not found"):
		return localized(locale, "找不到命令：请确认可执行文件在 PATH 中",
			"The command was not found: make sure the executable is on PATH")
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"),
		strings.Contains(lower, "timed out"):
		return localized(locale, "连接超时（服务器未及时响应）",
			"Connection timed out (the server did not answer in time)")
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"),
		strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"):
		return localized(locale, "鉴权失败（请检查访问令牌）",
			"Authentication failed (check the access token)")
	case strings.Contains(lower, "refused"), strings.Contains(lower, "no such host"),
		strings.Contains(lower, "dial tcp"), strings.Contains(lower, "connect:"):
		return localized(locale, "无法连接到服务器", "Could not reach the server")
	default:
		bounded := message
		if runes := []rune(bounded); len(runes) > 160 {
			bounded = string(runes[:160]) + "…"
		}
		return localized(locale, "连接测试失败：", "Connection test failed: ") + bounded
	}
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
	st.status = localized(locale, "已保存，重启生效", "Saved; applies after a restart")
	st.statusErr = false
	return true
}
