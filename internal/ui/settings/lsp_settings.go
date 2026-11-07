package settings

// LSP page: edit the optional single language server the toolkit exposes as
// lsp_* tools. The page writes the same lsp.server settings row the toolkit
// reads at activation, so a saved server applies after a restart.

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

// lspState is the page's form draft and status (UI thread only). The row is
// loaded once per module visit; malformed stored JSON is flagged instead of
// pre-filling the fields.
type lspState struct {
	loaded bool
	broken bool

	command    string
	args       string
	dir        string
	languageID string
	env        []string

	status    string
	statusErr bool
}

// loadLSPState reads the stored row once: valid values pre-fill the form; a
// malformed row sets broken (the fields stay empty and a danger line explains
// that saving overwrites it).
func loadLSPState(st *lspState, store contracts.SettingsService) {
	if st.loaded || store == nil {
		return
	}
	st.loaded = true
	raw := store.Get(contracts.SettingLSPServer, "")
	cfg, err := contracts.ParseLSPServerConfig(raw)
	if err != nil {
		st.broken = true
		return
	}
	st.command = cfg.Command
	st.args = strings.Join(cfg.Args, " ")
	st.dir = cfg.Dir
	st.languageID = cfg.LanguageID
	st.env = append([]string(nil), cfg.Env...)
}

// saveLSPState validates the draft and writes the lsp.server row. An empty
// command disables the server (the row is cleared).
func saveLSPState(st *lspState, store contracts.SettingsService, locale contracts.LocaleService) {
	command := strings.TrimSpace(st.command)
	if command == "" {
		if err := store.Set(contracts.SettingLSPServer, ""); err != nil {
			st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
			st.statusErr = true
			return
		}
		st.broken = false
		st.args = ""
		st.dir = ""
		st.languageID = ""
		st.env = nil
		st.status = localized(locale, "已关闭语言服务器", "Language server disabled")
		st.statusErr = false
		return
	}
	cfg := contracts.LSPServerConfig{
		Command:    command,
		Args:       strings.Fields(st.args),
		Dir:        strings.TrimSpace(st.dir),
		LanguageID: strings.TrimSpace(st.languageID),
		Env:        st.env,
	}
	encoded, err := contracts.EncodeLSPServerConfig(cfg)
	if err == nil {
		err = store.Set(contracts.SettingLSPServer, encoded)
	}
	if err != nil {
		st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	st.broken = false
	st.status = localized(locale, "已保存（重启后生效）", "Saved (applies after restart)")
	st.statusErr = false
}

// buildLSPPage renders the optional language-server configuration.
func buildLSPPage(c *ui.Context, store contracts.SettingsService, st *lspState, locale contracts.LocaleService) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, "LSP", localized(locale,
			"可选的语言服务器（单个）：配置后重启生效，其能力以 lsp_* 工具提供。",
			"Optional single language server: applies after a restart; its capabilities are exposed as lsp_* tools."))
		if store == nil {
			design.Apply(ui.Text(c, localized(locale, "设置存储未启用", "Settings store not enabled")),
				t.TextMuted(design.TypeXS))
			return
		}
		loadLSPState(st, store)
		if st.broken {
			design.Apply(ui.Text(c, localized(locale,
				"当前配置无法解析；保存有效配置可覆盖它。",
				"Current configuration is invalid; saving a valid one overwrites it.")),
				t.Font(design.TypeXS)).TextColor(t.DangerText)
		}
		panel(c, t, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				sectionTitle(c, t, localized(locale, "语言服务器", "Language server"))
				ui.Spacer(c).Grow(1)
				if !st.broken && strings.TrimSpace(st.command) != "" {
					design.Apply(ui.Badge(c, localized(locale, "已配置", "Configured")), t.Badge()).
						TextColor(t.Success)
				}
			})
			if !st.broken {
				current := strings.TrimSpace(st.command)
				if current == "" {
					design.Apply(ui.Text(c, localized(locale, "当前未配置语言服务器", "No language server configured")),
						t.TextMuted(design.TypeXXS))
				} else {
					summary := current
					if args := strings.Join(strings.Fields(st.args), " "); args != "" {
						summary += " " + args
					}
					design.Apply(ui.Text(c, localized(locale, "当前：", "Current: ")+summary),
						t.MonoFamily()).FontSize(11).TextColor(t.LabelTertiary)
				}
			}
			field(c, t, localized(locale, "命令", "Command"), func() {
				ui.TextInput(c, &st.command).Label(inputLabel(locale, localized(locale, "命令", "Command"))).
					Placeholder(localized(locale, "例如 gopls", "e.g. gopls")).FillWidth()
			})
			field(c, t, localized(locale, "参数", "Arguments"), func() {
				ui.TextInput(c, &st.args).Label(inputLabel(locale, localized(locale, "参数", "Arguments"))).
					Placeholder(localized(locale, "空格分隔，例如 serve -rpc.trace", "Space-separated, e.g. serve -rpc.trace")).FillWidth()
			})
			field(c, t, localized(locale, "工作目录", "Working directory"), func() {
				ui.TextInput(c, &st.dir).Label(inputLabel(locale, localized(locale, "工作目录", "Working directory"))).
					Placeholder(localized(locale, "可选，默认为当前目录", "Optional, defaults to the current directory")).FillWidth()
			})
			field(c, t, localized(locale, "语言 ID", "Language ID"), func() {
				ui.TextInput(c, &st.languageID).Label(inputLabel(locale, localized(locale, "语言 ID", "Language ID"))).
					Placeholder(localized(locale, "例如 go、python", "e.g. go, python")).FillWidth()
			})
			design.Apply(ui.Text(c, localized(locale,
				"保存后重启生效；工具名前缀为 lsp_（如 lsp_hover）。命令留空保存即关闭语言服务器。",
				"Applies after a restart; tools are prefixed lsp_ (e.g. lsp_hover). Saving an empty command disables the server.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			save := design.Apply(ui.PrimaryButton(c, localized(locale, "保存", "Save")), t.PrimaryButton())
			if save.Clicked() {
				saveLSPState(st, store, locale)
			}
			ui.Spacer(c).Grow(1)
			if st.status != "" {
				statusBadge(c, t, st.status, st.statusErr)
			}
		})
	})
}
