package settings

// LSP page: manage the multi-server language-server configuration the toolkit
// reads at activation (lsp.servers + lsp.active). Only the active server is
// started; the legacy single-server lsp.server row is still read as a fallback
// but saving the list migrates to the multi-server rows and clears it.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// LSPPageID is the optional language-server page identity.
	LSPPageID = "lsp"
	// LSPPageSection places it in the 高级 group with diagnostics/update.
	LSPPageSection = "高级"
	// LSPPageOrder places the page after diagnostics (95) and shortcuts.
	LSPPageOrder = 70
)

// lspState is the page's list, active-selection and add-form state (UI thread
// only). The rows are loaded once per module visit; malformed stored JSON is
// flagged instead of pre-filling the form.
type lspState struct {
	loaded  bool
	listErr string

	servers      []contracts.LSPServerConfig
	active       string
	activeStored string

	// The language-server collection is the official List: keyed by server
	// name, named by it and single-row selectable; the inline 设为活动/移除
	// actions stay the row actions.
	list ui.ListState
	sel  int

	name       string
	command    string
	args       string
	dir        string
	languageID string

	status    string
	statusErr bool

	// confirmRemove names the server whose removal waits for the native
	// confirmation dialog ("" when none is open).
	confirmRemove string
	removeOpen    bool // the removal confirmation's open flag (persistent for ConfirmDanger)
}

// loadLSPState reads the stored rows once (multi-server list first, then the
// legacy single row as a "default" entry).
func loadLSPState(st *lspState, store contracts.SettingsService) {
	if st.loaded || store == nil {
		return
	}
	st.loaded = true
	servers, err := contracts.ResolveLSPServerConfigs(
		store.Get(contracts.SettingLSPServers, ""),
		store.Get(contracts.SettingLSPServer, ""),
	)
	if err != nil {
		st.listErr = err.Error()
		st.servers = nil
	} else {
		st.servers = servers
	}
	st.activeStored = strings.TrimSpace(store.Get(contracts.SettingLSPActive, ""))
	st.refreshActive()
}

// lspDraftDirty reports whether the add form holds an unsaved draft.
func lspDraftDirty(st *lspState) bool {
	return strings.TrimSpace(st.name) != "" || strings.TrimSpace(st.command) != "" ||
		strings.TrimSpace(st.args) != "" || strings.TrimSpace(st.dir) != "" ||
		strings.TrimSpace(st.languageID) != ""
}

// discardLSPDraft clears the add form and its transient status, so the host's
// Discard action reopens a clean LSP page.
func discardLSPDraft(st *lspState) {
	st.name = ""
	st.command = ""
	st.args = ""
	st.dir = ""
	st.languageID = ""
	st.status = ""
	st.statusErr = false
	st.confirmRemove = ""
}

// refreshActive resolves the effective active server name (first when unset or
// unknown).
func (st *lspState) refreshActive() {
	st.active = ""
	if cfg, ok := contracts.ActiveLSPServer(st.servers, st.activeStored); ok {
		st.active = cfg.Name
	}
}

// writeLSPServers persists the list and clears the legacy single-server row so
// there is a single source of truth.
func writeLSPServers(st *lspState, store contracts.SettingsService,
	locale contracts.LocaleService, success string) bool {
	encoded, err := contracts.EncodeLSPServerConfigList(st.servers)
	if err == nil {
		err = store.Set(contracts.SettingLSPServers, encoded)
	}
	if err == nil {
		err = store.Set(contracts.SettingLSPServer, "")
	}
	if err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return false
	}
	st.listErr = ""
	st.status = success
	st.statusErr = false
	return true
}

// addLSPServer validates the add form and appends the server.
func addLSPServer(st *lspState, store contracts.SettingsService, locale contracts.LocaleService) {
	name := strings.TrimSpace(st.name)
	command := strings.TrimSpace(st.command)
	if name == "" || command == "" {
		st.status = localized(locale, "名称和命令不能为空", "Name and command are required")
		st.statusErr = true
		return
	}
	for _, server := range st.servers {
		if strings.EqualFold(strings.TrimSpace(server.Name), name) {
			st.status = localized(locale, "该名称已存在", "That name already exists")
			st.statusErr = true
			return
		}
	}
	st.servers = append(st.servers, contracts.LSPServerConfig{
		Name:       name,
		Command:    command,
		Args:       strings.Fields(st.args),
		Dir:        strings.TrimSpace(st.dir),
		LanguageID: strings.TrimSpace(st.languageID),
	})
	previous := append([]contracts.LSPServerConfig(nil), st.servers[:len(st.servers)-1]...)
	if !writeLSPServers(st, store, locale, localized(locale, "已添加（重启后生效）", "Added (applies after restart)")) {
		st.servers = previous
		return
	}
	st.name = ""
	st.command = ""
	st.args = ""
	st.dir = ""
	st.languageID = ""
	st.refreshActive()
}

// removeLSPServer removes one configured server.
func removeLSPServer(st *lspState, store contracts.SettingsService,
	name string, locale contracts.LocaleService) {
	previous := append([]contracts.LSPServerConfig(nil), st.servers...)
	kept := make([]contracts.LSPServerConfig, 0, len(st.servers))
	for _, server := range st.servers {
		if server.Name != name {
			kept = append(kept, server)
		}
	}
	if len(kept) == len(st.servers) {
		return
	}
	st.servers = kept
	if !writeLSPServers(st, store, locale, localized(locale, "已移除（重启后生效）", "Removed (applies after restart)")) {
		st.servers = previous
		return
	}
	if strings.EqualFold(st.activeStored, name) || st.active == name {
		st.activeStored = ""
		_ = store.Set(contracts.SettingLSPActive, "")
	}
	st.refreshActive()
}

// buildLSPServerRow renders one language server: its name, command summary, the
// active badge or set-active action, and remove. The official List owns the row
// paint and the choice, so the selected row's copy uses the accent's on-color.
func buildLSPServerRow(c *ui.Context, t *design.Tokens, store contracts.SettingsService,
	st *lspState, index int, locale contracts.LocaleService, selected bool) {
	server := st.servers[index]
	primary, _, tertiary := listRowTextColors(t, selected)
	active := st.active != "" && server.Name == st.active
	ui.Row(c).FillWidth().Padding(design.Space1, design.Space2).
		AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, server.Name), t.Font(design.TypeS)).
			FontWeight(600).TextColor(primary)
		summary := server.Command
		if args := strings.Join(server.Args, " "); args != "" {
			summary += " " + args
		}
		design.Apply(ui.Text(c, summary), t.Font(design.TypeXS), t.MonoFamily()).
			TextColor(tertiary).Grow(1).MaxLines(1).Ellipsis("…")
		if active {
			uikit.StatusBadge(c, t, uikit.StatusSuccess, localized(locale, "活动中", "Active"))
		} else {
			if design.Apply(ui.Button(c, localized(locale, "设为活动", "Set active")),
				t.SecondaryButton()).Clicked() {
				setActiveLSPServer(st, store, server.Name, locale)
				c.Invalidate()
			}
		}
		// The destructive remove is the row's ⋯ danger item.
		label := localized(locale, "服务器操作", "Server actions")
		menu := uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "lsp-actions/" + server.Name,
			Label:   label,
			Tooltip: label,
			Icon:    icons.More(),
			Size:    design.IconSizeS,
		})
		menu.PopoverMenuButton(func(m *ui.Menu) {
			remove := m.Item(localized(locale, "移除", "Remove"))
			remove.Danger()
			if remove.Chosen() {
				st.confirmRemove = server.Name
				st.removeOpen = true
				c.Invalidate()
			}
		})
	})
}

// setActiveLSPServer stores the active server choice.
func setActiveLSPServer(st *lspState, store contracts.SettingsService,
	name string, locale contracts.LocaleService) {
	if err := store.Set(contracts.SettingLSPActive, name); err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	st.activeStored = name
	st.status = localized(locale, "已设为活动（重启后生效）", "Set as active (applies after restart)")
	st.statusErr = false
	st.refreshActive()
}

// lspWorkspaceOptIn reports whether the workspace config file is enabled: only
// the exact value "allow" turns it on (everything else, including malformed
// values, is off).
func lspWorkspaceOptIn(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "allow")
}

// buildLSPPage renders the multi-server language-server configuration. The
// list, add and remove actions write the store at once (there is no separate
// save button: a second save would only rewrite the same rows); removal asks
// for confirmation with the native alert dialog.
func buildLSPPage(c *ui.Context, store contracts.SettingsService, st *lspState, locale contracts.LocaleService) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, "LSP", localized(locale,
			"可选的语言服务器（可配置多个）；仅活动服务器在启动时拉起，重启生效，其能力以 lsp_* 工具提供。",
			"Optional language servers (multiple allowed); only the active server starts at launch. Restart to apply; capabilities are exposed as lsp_* tools."))
		if store == nil {
			uikit.Section(c, localized(locale, "设置存储未启用", "Settings store not enabled"), func(*ui.Context) {
				uikit.EmptyState(c, localized(locale, "设置存储未启用，无法保存语言服务器。",
					"The settings store is not enabled, so language servers cannot be saved."))
			})
			return
		}
		loadLSPState(st, store)
		if st.listErr != "" {
			uikit.ErrorLine(c, localized(locale,
				"配置无法解析：", "Configuration could not be parsed: ")+st.listErr)
		}

		uikit.Section(c, localized(locale, "语言服务器", "Language servers"), func(*ui.Context) {
			if len(st.servers) == 0 && st.listErr == "" {
				uikit.EmptyState(c, localized(locale,
					"还没有配置语言服务器", "No language servers configured"))
			}
			// The servers are a collection: the official List keys the rows by
			// name, names them and owns the keyboard selection; the inline
			// set-active/remove actions keep their click semantics.
			bindSettingsList(&st.list, &st.sel, len(st.servers), func(index int) any {
				if index < 0 || index >= len(st.servers) {
					return nil
				}
				return strings.TrimSpace(st.servers[index].Name)
			}, func(index int) string {
				if index < 0 || index >= len(st.servers) {
					return ""
				}
				return firstNonEmpty(strings.TrimSpace(st.servers[index].Name),
					localized(locale, "(未命名服务器)", "(Untitled server)"))
			})
			ui.List(c, &st.list, len(st.servers)).
				Label(localized(locale, "语言服务器列表", "Language servers list")).
				Gap(design.Space1).
				Rows(func(row ui.ListRow) {
					buildLSPServerRow(row.Context, t, store, st, row.Index, locale, row.Selected())
				})
			design.Apply(ui.Text(c, localized(locale, "重启生效；仅活动服务器在启动时拉起。",
				"Restart to apply; only the active server starts at launch.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})

		uikit.Section(c, localized(locale, "工作区配置", "Workspace configuration"), func(*ui.Context) {
			ui.Form(c, func() {
				allow := lspWorkspaceOptIn(store.Get(contracts.SettingLSPWorkspaceConfig, ""))
				if switchField(c,
					localized(locale, "读取工作区 LSP 配置（.zeokeel/lsp.json）",
						"Read workspace LSP config (.zeokeel/lsp.json)"),
					localized(locale,
						"仓库中的 .zeokeel/lsp.json 可声明语言服务器命令；默认关闭，开启后重启生效；设置页中的配置优先。",
						"The repository's .zeokeel/lsp.json can declare language-server commands; off by default, restart to apply; settings-page configuration wins."),
					&allow) {
					value := ""
					if allow {
						value = "allow"
					}
					if err := store.Set(contracts.SettingLSPWorkspaceConfig, value); err != nil {
						st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
						st.statusErr = true
					} else {
						st.status = localized(locale, "已保存（重启后生效）", "Saved (applies after restart)")
						st.statusErr = false
					}
					c.Invalidate()
				}
			})
			uikit.ErrorLine(c, localized(locale,
				"风险：只应在你信任的仓库中开启；恶意仓库可通过该文件让工具启动任意命令。",
				"Risk: enable only in repositories you trust; a malicious repository could make tools launch arbitrary commands."))
		})

		uikit.Section(c, localized(locale, "新增服务器", "Add server"), func(*ui.Context) {
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "名称", "Name"), func() {
						ui.TextInput(c, &st.name).
							Label(inputLabel(locale, localized(locale, "名称", "Name"))).
							Placeholder(localized(locale, "例如 go", "e.g. go")).FillWidth()
					}).Description(localized(locale, "活动服务器的唯一名称。",
						"A unique name for the active server."))
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "命令", "Command"), func() {
						ui.TextInput(c, &st.command).
							Label(inputLabel(locale, localized(locale, "命令", "Command"))).
							Placeholder(localized(locale, "例如 gopls", "e.g. gopls")).FillWidth()
					}).Description(localized(locale, "启动语言服务器的可执行文件。",
						"The executable that starts the language server."))
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "参数", "Arguments"), func() {
						ui.TextInput(c, &st.args).
							Label(inputLabel(locale, localized(locale, "参数", "Arguments"))).
							Placeholder(localized(locale, "空格分隔，例如 serve -rpc.trace",
								"Space-separated, e.g. serve -rpc.trace")).FillWidth()
					}).Description(localized(locale, "按空格拆分为参数列表。",
						"Split on spaces into the argument list."))
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "工作目录", "Working directory"), func() {
						ui.TextInput(c, &st.dir).
							Label(inputLabel(locale, localized(locale, "工作目录", "Working directory"))).
							Placeholder(localized(locale, "可选", "optional")).FillWidth()
					}).Description(localized(locale, "可选；留空使用工作区根目录。",
						"Optional; empty uses the workspace root."))
				})
			})
			ui.Field(c, localized(locale, "语言 ID", "Language ID"), func() {
				ui.TextInput(c, &st.languageID).
					Label(inputLabel(locale, localized(locale, "语言 ID", "Language ID"))).
					Placeholder(localized(locale, "例如 go、python", "e.g. go, python")).FillWidth()
			}).Description(localized(locale,
				"重启后生效；工具名前缀为 lsp_（如 lsp_hover）。",
				"Applies after a restart; tools are prefixed lsp_ (e.g. lsp_hover)."))
			statusBadgesRow(c, t, st.status, st.statusErr)
			uikit.DialogFooter(c, []uikit.DialogAction{{
				Label: localized(locale, "添加", "Add"), Primary: true,
				OnClick: func() {
					addLSPServer(st, store, locale)
					c.Invalidate()
				},
			}})
		})

		if st.confirmRemove != "" {
			name := st.confirmRemove
			uikit.ConfirmDanger(c, &st.removeOpen, uikit.ConfirmDangerOptions{
				Key:   "lsp.remove",
				Title: localized(locale, "移除语言服务器", "Remove language server"),
				Message: localized(locale, "确认移除「", "Remove ") + name +
					localized(locale, "」？此操作无法撤销。", "? This cannot be undone."),
				Confirm: localized(locale, "移除服务器", "Remove server"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					removeLSPServer(st, store, name, locale)
					st.confirmRemove = ""
					c.Invalidate()
				},
			})
			if !st.removeOpen {
				st.confirmRemove = ""
			}
		}
	})
}
