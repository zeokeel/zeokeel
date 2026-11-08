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
)

const (
	// LSPPageID is the optional language-server page identity.
	LSPPageID = "lsp"
	// LSPPageSection places it in the 高级 group with diagnostics/update.
	LSPPageSection = "高级"
	// LSPPageOrder places the page after diagnostics (95) and shortcuts.
	LSPPageOrder = 105
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

	name       string
	command    string
	args       string
	dir        string
	languageID string

	status    string
	statusErr bool
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

// buildLSPPage renders the multi-server language-server configuration.
func buildLSPPage(c *ui.Context, store contracts.SettingsService, st *lspState, locale contracts.LocaleService) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, "LSP", localized(locale,
			"可选的语言服务器（可配置多个）；仅活动服务器在启动时拉起，重启生效，其能力以 lsp_* 工具提供。",
			"Optional language servers (multiple allowed); only the active server starts at launch. Restart to apply; capabilities are exposed as lsp_* tools."))
		if store == nil {
			design.Apply(ui.Text(c, localized(locale, "设置存储未启用", "Settings store not enabled")),
				t.TextMuted(design.TypeXS))
			return
		}
		loadLSPState(st, store)
		if st.listErr != "" {
			design.Apply(ui.Text(c, localized(locale, "配置无法解析：", "Configuration could not be parsed: ")+st.listErr),
				t.Font(design.TypeXS)).TextColor(t.DangerText)
		}

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "语言服务器", "Language servers"))
			if len(st.servers) == 0 && st.listErr == "" {
				design.Apply(ui.Text(c, localized(locale, "还没有配置语言服务器", "No language servers configured")),
					t.TextMuted(design.TypeXXS))
			}
			for _, server := range st.servers {
				server := server
				active := st.active != "" && server.Name == st.active
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, server.Name), t.Font(design.TypeS)).
						FontWeight(600).TextColor(t.LabelPrimary)
					summary := server.Command
					if args := strings.Join(server.Args, " "); args != "" {
						summary += " " + args
					}
					design.Apply(ui.Text(c, summary), t.MonoFamily()).FontSize(11).
						TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
					if active {
						design.Apply(ui.Badge(c, localized(locale, "活动中", "Active")), t.Badge()).
							TextColor(t.Success)
					} else {
						if ui.Button(c, localized(locale, "设为活动", "Set active")).Clicked() {
							setActiveLSPServer(st, store, server.Name, locale)
							c.Invalidate()
						}
					}
					remove := ui.Button(c, localized(locale, "移除", "Remove")).TextColor(t.DangerText)
					if remove.Clicked() {
						removeLSPServer(st, store, server.Name, locale)
						c.Invalidate()
					}
				})
			}
			design.Apply(ui.Text(c, localized(locale, "重启生效；仅活动服务器在启动时拉起。",
				"Restart to apply; only the active server starts at launch.")),
				t.TextMuted(design.TypeXXXS)).MaxLines(2)
		})

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "工作区配置", "Workspace configuration"))
			allow := lspWorkspaceOptIn(store.Get(contracts.SettingLSPWorkspaceConfig, ""))
			toggle := ui.Toggle(c, &allow, localized(locale,
				"读取工作区 LSP 配置（.zeokeel/lsp.json）",
				"Read workspace LSP config (.zeokeel/lsp.json)"))
			if toggle.Changed() {
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
			design.Apply(ui.Text(c, localized(locale,
				"仓库中的 .zeokeel/lsp.json 可声明语言服务器命令；默认关闭，开启后重启生效；设置页中的配置优先。",
				"The repository's .zeokeel/lsp.json can declare language-server commands; off by default, restart to apply; settings-page configuration wins.")),
				t.TextMuted(design.TypeXXS)).MaxLines(3)
			design.Apply(ui.Text(c, localized(locale,
				"风险：只应在你信任的仓库中开启；恶意仓库可通过该文件让工具启动任意命令。",
				"Risk: enable only in repositories you trust; a malicious repository could make tools launch arbitrary commands.")),
				t.Font(design.TypeXXS)).TextColor(t.DangerText).MaxLines(3)
		})

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "新增服务器", "Add server"))
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.TextInput(c, &st.name).Label(inputLabel(locale, localized(locale, "名称", "Name"))).
						Placeholder(localized(locale, "例如 go", "e.g. go")).FillWidth()
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.TextInput(c, &st.command).Label(inputLabel(locale, localized(locale, "命令", "Command"))).
						Placeholder(localized(locale, "例如 gopls", "e.g. gopls")).FillWidth()
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.TextInput(c, &st.args).Label(inputLabel(locale, localized(locale, "参数", "Arguments"))).
						Placeholder(localized(locale, "空格分隔，例如 serve -rpc.trace", "Space-separated, e.g. serve -rpc.trace")).FillWidth()
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.TextInput(c, &st.dir).Label(inputLabel(locale, localized(locale, "工作目录", "Working directory"))).
						Placeholder(localized(locale, "可选", "optional")).FillWidth()
				})
			})
			ui.TextInput(c, &st.languageID).Label(inputLabel(locale, localized(locale, "语言 ID", "Language ID"))).
				Placeholder(localized(locale, "例如 go、python", "e.g. go, python")).FillWidth()
			design.Apply(ui.Text(c, localized(locale,
				"保存后重启生效；工具名前缀为 lsp_（如 lsp_hover）。",
				"Applies after a restart; tools are prefixed lsp_ (e.g. lsp_hover).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			if add := design.Apply(ui.PrimaryButton(c, localized(locale, "添加", "Add")), t.PrimaryButton()); add.Clicked() {
				addLSPServer(st, store, locale)
				c.Invalidate()
			}
		})

		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			if save := design.Apply(ui.PrimaryButton(c, localized(locale, "保存", "Save")), t.PrimaryButton()); save.Clicked() {
				if writeLSPServers(st, store, locale,
					localized(locale, "已保存（重启后生效）", "Saved (applies after restart)")) {
					st.refreshActive()
				}
				c.Invalidate()
			}
			ui.Spacer(c).Grow(1)
			if st.status != "" {
				statusBadge(c, t, st.status, st.statusErr)
			}
		})
	})
}
