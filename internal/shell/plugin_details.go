package shell

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// pluginProductVersion is the version the About surfaces report. It matches
// the DSH baseline's "Current version" and the catalog's about.version.
const pluginProductVersion = "0.1.0"

// settingsTabAbout is the Settings > About page. It is declared here because
// models_ui.go (which owns the settingsTab type) is outside this change's
// ownership; the value simply follows the last existing tab.
const settingsTabAbout settingsTab = settingsTabAgentPresets + 1

// pluginUIState is the live interaction state of the Plugins page and the
// settings surfaces added here. Like the other per-App stores (modeUIState,
// shellUIState) it is keyed by the App and never read by the static pixel
// path, so App{Ctrl: nil} keeps rendering the baseline exactly.
type pluginUIState struct {
	// Plugins page dialogs.
	aboutOpen bool
	addOpen   bool
	moreOpen  bool

	// Per-plugin detail editor.
	detailOpen   bool
	detailKey    string
	detailFields map[string]*string

	// One experimental plugin's explanation dialog.
	infoOpen bool
	infoName string

	// Data-folder dialog: the data directory, the database path it
	// contains, and the last open-folder failure, if any.
	configOpen bool
	configPath string
	configDir  string
	configErr  string

	// Settings > General font popovers.
	fontMoreOpen bool

	// Native hooks are injectable so tests never touch the desktop. nil means
	// the mygo API.
	openPath     func(string) error
	openExternal func(string) error
}

var pluginUIStates sync.Map // map[*App]*pluginUIState

// pluginUI returns the plugin/settings interaction state of a, making it on
// first use.
func (a *App) pluginUI() *pluginUIState {
	if s, ok := pluginUIStates.Load(a); ok {
		return s.(*pluginUIState)
	}
	actual, _ := pluginUIStates.LoadOrStore(a, &pluginUIState{})
	return actual.(*pluginUIState)
}

// pluginLocalized picks the UI language for strings added by this change.
// The catalog in i18n.go is outside this file's ownership, so the few new
// messages carry both languages here instead of adding catalog entries.
func (a *App) pluginLocalized(en, zh string) string {
	if a.lang() == LangZH {
		return zh
	}
	return en
}

// pluginDataDir is the directory the store and configuration live in. A live
// app asks its controller's store (which is the real data dir in production
// and the temporary dir in tests); a static app falls back to the product's
// default location.
func (a *App) pluginDataDir() string {
	if a.Ctrl != nil && a.Ctrl.Store() != nil {
		if dir := a.Ctrl.Store().Dir(); dir != "" {
			return dir
		}
	}
	if dir := os.Getenv("ZEOKEEL_DATA_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".zeokeel"
	}
	return filepath.Join(home, ".zeokeel")
}

// pluginConfigPath is the settings database the app opens.
func (a *App) pluginConfigPath() string {
	return filepath.Join(a.pluginDataDir(), "index.sqlite")
}

// errPluginNativeUnavailable is returned when the desktop backend is not
// running (the headless tester, a service-only process). Callers fall back to
// the in-app path dialog instead of touching a native API that cannot work.
var errPluginNativeUnavailable = errors.New("desktop backend is not ready")

// pluginOpener opens a directory in the OS file manager through mygo, unless
// a test injected a fake. Before App.Run the native call would block or
// panic, so it reports unavailable and the caller shows the fallback dialog.
func (a *App) pluginOpener() func(string) error {
	if fn := a.pluginUI().openPath; fn != nil {
		return fn
	}
	if !mygo.App.IsReady() {
		return func(string) error { return errPluginNativeUnavailable }
	}
	return mygo.Shell.OpenPath
}

// pluginExternalOpener opens an external URL through mygo, unless a test
// injected a fake. It has the same readiness guard as pluginOpener.
func (a *App) pluginExternalOpener() func(string) error {
	if fn := a.pluginUI().openExternal; fn != nil {
		return fn
	}
	if !mygo.App.IsReady() {
		return func(string) error { return errPluginNativeUnavailable }
	}
	return mygo.Shell.OpenExternal
}

// pluginFieldKind is the validation rule of one detail-editor field.
type pluginFieldKind int

const (
	pluginFieldText pluginFieldKind = iota
	pluginFieldInt
	pluginFieldURL
)

// pluginDetailField is one persisted setting in a plugin's detail editor.
type pluginDetailField struct {
	key     string
	labelEN string
	labelZH string
	kind    pluginFieldKind
	def     string
	min     int
	max     int
	// secret fields hold credentials (API keys) and render masked.
	secret bool
}

// pluginDetailSpec describes the settings of one built-in plugin. The keys
// are stable strings persisted through the store, so the values survive a
// restart and a future runtime can read the same keys.
type pluginDetailSpec struct {
	key     string
	titleEN string
	titleZH string
	descEN  string
	descZH  string
	fields  []pluginDetailField
}

var pluginDetailSpecs = []pluginDetailSpec{
	{
		key: "subagent", titleEN: "Subagent", titleZH: "子代理",
		descEN: "Recursion depth and the model a nested subagent runs.",
		descZH: "设置嵌套子代理的递归深度与模型。",
		fields: []pluginDetailField{
			{key: "plugin.subagent.depth", labelEN: "Recursion depth", labelZH: "递归深度", kind: pluginFieldInt, def: "2", min: 1, max: 8},
			{key: "plugin.subagent.model", labelEN: "Model", labelZH: "模型", kind: pluginFieldText, def: ""},
		},
	},
	{
		key: "web_search", titleEN: "Web search", titleZH: "网络搜索",
		descEN: "Point this at a search service or local adapter that speaks ZeoKeel's protocol: POST {\"query\":...,\"count\":...} and return {\"results\":[{title,url,snippet}]}. The DeepSeek/OpenAI chat APIs are not search endpoints and need an adapter. The API key is sent as a Bearer token and is optional.",
		descZH: "请填写支持 ZeoKeel 协议的搜索服务或本地适配器：POST {\"query\":...,\"count\":...}，返回 {\"results\":[{title,url,snippet}]}。DeepSeek/OpenAI 聊天接口不是搜索地址，需要适配器。API 密钥会作为 Bearer token 发送，可选。",
		fields: []pluginDetailField{
			{key: "plugin.web_search.endpoint", labelEN: "Search endpoint (adapter URL)", labelZH: "搜索地址（适配器 URL）", kind: pluginFieldURL, def: ""},
			{key: "websearch.api_key", labelEN: "API key (Bearer token)", labelZH: "API 密钥（Bearer token）", kind: pluginFieldText, def: "", secret: true},
			{key: "plugin.web_search.max_results", labelEN: "Max results", labelZH: "最大结果数", kind: pluginFieldInt, def: "5", min: 1, max: 5},
		},
	},
	{
		key: "agent_loop", titleEN: "Agent loop", titleZH: "代理循环",
		descEN: "0 = no limit (default): the model decides when the turn ends. A positive value is a safety ceiling; reaching it keeps the completed work, shows a notice and lets you continue.",
		descZH: "0 = 不限（默认）：由模型自行决定何时结束。填正数则作为单轮安全上限：达到上限会保留已完成的工作、提示并允许继续。",
		fields: []pluginDetailField{
			{key: "plugin.agent_loop.max_iterations", labelEN: "Max iterations (0 = unlimited)", labelZH: "最大迭代次数（0 = 不限）", kind: pluginFieldInt, def: strconv.Itoa(runtime.DefaultMaxIterations), min: 0, max: runtime.MaxIterationsLimit},
		},
	},
	{
		key: "shell", titleEN: "Shell", titleZH: "Shell",
		descEN: "How long a command may run and how much output it may produce.",
		descZH: "限制命令运行时长以及输出大小。",
		fields: []pluginDetailField{
			{key: "plugin.shell.timeout_seconds", labelEN: "Command timeout (seconds)", labelZH: "命令超时（秒）", kind: pluginFieldInt, def: "120", min: 1, max: 300},
			{key: "plugin.shell.output_cap_kb", labelEN: "Output cap (KB)", labelZH: "输出上限（KB）", kind: pluginFieldInt, def: "256", min: 1, max: 10000},
		},
	},
}

// pluginDetailForName resolves a row's English title to its detail spec.
func pluginDetailForName(name string) (pluginDetailSpec, bool) {
	key := pluginKeyForLabel(name)
	for _, spec := range pluginDetailSpecs {
		if spec.key == key {
			return spec, true
		}
	}
	return pluginDetailSpec{}, false
}

// pluginDetailForSeries resolves a stored key to its detail spec.
func pluginDetailForKey(key string) (pluginDetailSpec, bool) {
	for _, spec := range pluginDetailSpecs {
		if spec.key == key {
			return spec, true
		}
	}
	return pluginDetailSpec{}, false
}

// pluginFieldLabel localizes a detail field label.
func (a *App) pluginFieldLabel(f pluginDetailField) string {
	return a.pluginLocalized(f.labelEN, f.labelZH)
}

// pluginDetailTitle localizes a detail spec title.
func (a *App) pluginDetailTitle(spec pluginDetailSpec) string {
	return a.pluginLocalized(spec.titleEN, spec.titleZH)
}

// pluginDetailDesc localizes a detail spec description.
func (a *App) pluginDetailDesc(spec pluginDetailSpec) string {
	return a.pluginLocalized(spec.descEN, spec.descZH)
}

// pluginSetting reads a persisted plugin setting, falling back to def when it
// was never written or the store failed.
func (a *App) pluginSetting(key, def string) string {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return def
	}
	value, err := a.Ctrl.Store().GetSetting(key)
	if err != nil {
		return def
	}
	return value
}

// setPluginSetting persists one plugin setting.
func (a *App) setPluginSetting(key, value string) error {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return nil
	}
	return a.Ctrl.Store().SetSetting(key, value)
}

// openPluginInstance opens the detail editor for a built-in plugin, or the
// explanation dialog for an experimental one.
func (a *App) openPluginInstance(name string) {
	if spec, ok := pluginDetailForName(name); ok {
		a.openPluginDetail(spec)
		return
	}
	st := a.pluginUI()
	st.infoName = name
	st.infoOpen = true
}

// openPluginDetail seeds the editor with the persisted values.
func (a *App) openPluginDetail(spec pluginDetailSpec) {
	st := a.pluginUI()
	st.detailOpen = true
	st.detailKey = spec.key
	st.detailFields = make(map[string]*string, len(spec.fields))
	for _, f := range spec.fields {
		value := a.pluginSetting(f.key, f.def)
		st.detailFields[f.key] = &value
	}
}

// savePluginDetail validates every field, persists the values and closes the
// editor. A bad value keeps the dialog open with a toast.
func (a *App) savePluginDetail(c *ui.Context) {
	st := a.pluginUI()
	spec, ok := pluginDetailForKey(st.detailKey)
	if !ok {
		st.detailOpen = false
		return
	}
	values := make(map[string]string, len(spec.fields))
	for _, f := range spec.fields {
		value := strings.TrimSpace(*st.detailFields[f.key])
		switch f.kind {
		case pluginFieldInt:
			n, err := strconv.Atoi(value)
			if err != nil || n < f.min || n > f.max {
				c.Toast(fmt.Sprintf(
					a.pluginLocalized("%s must be a number between %d and %d", "%s 必须是 %d 到 %d 之间的数字"),
					a.pluginFieldLabel(f), f.min, f.max))
				return
			}
			value = strconv.Itoa(n)
		case pluginFieldURL:
			// An empty endpoint is valid: the runtime reads it as
			// "web_search not configured", and clearing a previously
			// configured URL must be possible without disabling the plugin.
			if value == "" {
				break
			}
			u, err := url.Parse(value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				c.Toast(fmt.Sprintf(
					a.pluginLocalized("%s must be an http(s) URL", "%s 必须是 http(s) 地址"),
					a.pluginFieldLabel(f)))
				return
			}
		}
		values[f.key] = value
	}
	for _, f := range spec.fields {
		if err := a.setPluginSetting(f.key, values[f.key]); err != nil {
			c.Toast(a.pluginLocalized("Could not save plugin settings", "插件设置保存失败"))
			return
		}
	}
	st.detailOpen = false
	// The runtime reads these keys when it builds the agent; rebuild now so
	// the saved depth, model, iteration cap or shell limits apply to the next
	// turn instead of waiting for an unrelated restart.
	if err := a.Ctrl.RebuildAgent("plugin-settings"); err != nil {
		c.Toast(a.pluginLocalized("Plugin settings saved; restart to apply", "插件设置已保存；重启后生效"))
		return
	}
	c.Toast(a.pluginLocalized("Plugin settings saved", "插件设置已保存"))
}

// pluginUnsupportedReason returns the explanation shown when an experimental
// feature is switched on. Empty means the feature is supported.
func pluginUnsupportedReason(name string) (en, zh string) {
	switch name {
	case "agent_teams":
		return "Agent Teams is experimental. When enabled, team tools and the shared task board are registered for the next turn; disabling it removes them.",
			"代理团队仍处于实验阶段。启用后，团队工具与共享任务面板会在下一轮注册；停用后会被移除。"
	case "voice_input":
		return "Voice input needs local SenseVoice dependencies that this build does not ship; the preference is saved for a future release.",
			"语音输入需要本地 SenseVoice 依赖，此版本未内置；偏好已保存，供后续版本使用。"
	case "auto_review":
		return "Auto Authorization Review is experimental. Choose the Auto review EXP permission mode to have the model review mutating tool calls; this plugin switch stores the preference only.",
			"自动授权审核仍处于实验阶段。选择“自动审查（实验）”权限模式后，模型会审查会修改数据的工具调用；此插件开关仅保存偏好。"
	case "developer_tools":
		return "Developer Tools is experimental. When enabled, the inspect tool for raw session data is registered for the next turn; disabling it removes it.",
			"开发者工具仍处于实验阶段。启用后，用于原始会话数据的 inspect 工具会在下一轮注册；停用后会被移除。"
	}
	return "", ""
}

// refreshPlugins re-reads the persisted settings and re-renders the page. The
// plugin toggles themselves are read per frame, so the reload also refreshes
// the settings cache (font size, language, ...) and drops derived caches.
func (a *App) refreshPlugins(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	a.settingsLoaded = false
	a.settingsValue = session.DefaultSettings()
	if store := a.settingsStore(); store != nil {
		if cfg, err := store.Load(); err != nil {
			a.settingsErr = err
			c.Toast(a.pluginLocalized("Could not refresh plugins", "插件刷新失败"))
			return
		} else {
			a.settingsValue = session.NormalizeSettings(cfg)
		}
	}
	a.settingsLoaded = true
	a.settingsErr = nil
	a.langLoaded = false
	a.langValue = ""
	c.Toast(a.pluginLocalized("Plugins refreshed", "插件已刷新"))
}

// pluginDialogs draws the plugins/settings dialogs that are open. It is a
// no-op in the static App and builds nothing while every dialog is closed,
// so the baseline pixel path is unchanged.
func (a *App) pluginDialogs(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	st := a.pluginUI()
	if st.aboutOpen {
		a.pluginAboutDialog(c, t)
	}
	if st.addOpen {
		a.pluginAddDialog(c, t)
	}
	if st.moreOpen {
		a.pluginExperimentalDialog(c, t)
	}
	if st.infoOpen {
		a.pluginInfoDialog(c, t)
	}
	if st.detailOpen {
		a.pluginDetailDialog(c, t)
	}
	if st.configOpen {
		a.pluginConfigDialog(c, t)
	}
}

// pluginAboutDialog is the "About plugins" dialog: what the built-in plugins
// are, the product version and how settings are stored.
func (a *App) pluginAboutDialog(c *ui.Context, t *design.Tokens) {
	st := a.pluginUI()
	th := t.Theme()
	ui.Modal(c, &st.aboutOpen, func() {
		ui.Column(c).Width(th.Space(110)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, a.pluginLocalized("About plugins", "关于插件")).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, a.pluginLocalized(
				"Plugins extend the agent with tools for searching, delegation, command execution and the agent loop. Every plugin in this build is built in: there is no third-party registry or installer.",
				"插件为代理提供搜索、委派、命令执行和代理循环等工具。此版本的所有插件均为内置，没有第三方插件市场或安装器。"))
			nativeText(c, design.TypeXS, t.LabelSecondary, a.pluginLocalized(
				"Toggles and per-plugin settings are stored in the local configuration database and apply from the next turn.",
				"开关和每个插件的设置保存在本地配置数据库中，并在下一轮生效。"))
			nativeText(c, design.TypeXXS, t.LabelTertiary, a.T("about.version"))
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.close")).Clicked() {
					st.aboutOpen = false
				}
			})
		})
	})
}

// pluginAddDialog is the product-policy answer to "Add plugin": there is no
// third-party installation path, and the dialog says what to do instead.
func (a *App) pluginAddDialog(c *ui.Context, t *design.Tokens) {
	st := a.pluginUI()
	th := t.Theme()
	ui.Modal(c, &st.addOpen, func() {
		ui.Column(c).Width(th.Space(115)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, a.T("plugins.add")).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, a.pluginLocalized(
				"All plugins are built in to ZeoKeel. This build has no third-party plugin registry, package format or installer, so there is nothing to add from outside the app.",
				"所有插件均内置于 ZeoKeel。此版本没有第三方插件市场、包格式或安装器，因此无法从外部添加插件。"))
			nativeText(c, design.TypeXS, t.LabelSecondary, a.pluginLocalized(
				"Use the built-in plugin rows below to enable or disable a plugin, and \"View\" to change its settings.",
				"使用下方的内置插件行启用或停用插件，点击“查看”可修改其设置。"))
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.close")).Clicked() {
					st.addOpen = false
				}
			})
		})
	})
}

// pluginExperimentalDialog is the experimental section's "More" dialog: it
// lists every experimental feature and why it is not available yet.
func (a *App) pluginExperimentalDialog(c *ui.Context, t *design.Tokens) {
	st := a.pluginUI()
	th := t.Theme()
	ui.Modal(c, &st.moreOpen, func() {
		ui.Column(c).Width(th.Space(120)).Gap(th.Space(2)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, a.T("plugins.experimental_plugins")).FontWeight(600)
			for _, plugin := range experimentalPluginDefs {
				en, zh := pluginUnsupportedReason(plugin.name)
				ui.Column(c).Gap(th.Space(0.5)).Children(func() {
					nativeText(c, design.TypeXS, t.LabelPrimary, a.pluginTitle(plugin)).FontWeight(500)
					nativeText(c, design.TypeXXS, t.LabelTertiary, a.pluginLocalized(en, zh))
				})
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.close")).Clicked() {
					st.moreOpen = false
				}
			})
		})
	})
}

// pluginInfoDialog explains one experimental plugin when its name is clicked.
func (a *App) pluginInfoDialog(c *ui.Context, t *design.Tokens) {
	st := a.pluginUI()
	th := t.Theme()
	ui.Modal(c, &st.infoOpen, func() {
		ui.Column(c).Width(th.Space(110)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, a.pluginRowName(st.infoName)).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, a.pluginRowDesc(pluginRowDescForName(st.infoName)))
			en, zh := pluginUnsupportedReason(pluginKeyForLabel(st.infoName))
			if en != "" {
				nativeText(c, design.TypeXXS, t.LabelTertiary, a.pluginLocalized(en, zh))
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.close")).Clicked() {
					st.infoOpen = false
				}
			})
		})
	})
}

// pluginRowDescForName returns the English description of a row title so the
// catalog mapper can localize it.
func pluginRowDescForName(name string) string {
	switch name {
	case "Agent Teams":
		return "Enable team collaboration, team tools, the member roster, and the shared task board."
	case "Voice input":
		return "Transcribe recordings locally with SenseVoice; first use requires installing dependencies"
	case "Auto Authorization Review":
		return "Add an Auto review permission mode that uses the model to assess authorization before each tool call."
	case "Developer Tools":
		return "Inspect and debug raw session data, grouped chat messages, and the NodeJS backend."
	}
	return ""
}

// pluginDetailDialog is the per-plugin editor. Save validates and persists,
// Cancel discards the draft.
func (a *App) pluginDetailDialog(c *ui.Context, t *design.Tokens) {
	st := a.pluginUI()
	spec, ok := pluginDetailForKey(st.detailKey)
	if !ok {
		st.detailOpen = false
		return
	}
	th := t.Theme()
	ui.Modal(c, &st.detailOpen, func() {
		ui.Column(c).Width(th.Space(120)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary,
				fmt.Sprintf(a.tr("%s settings", "%s设置"), a.pluginDetailTitle(spec))).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, a.pluginDetailDesc(spec))
			for _, f := range spec.fields {
				ui.Column(c).Gap(th.Space(1)).Children(func() {
					// The visible label carries a colon so the input's exact
					// accessible label is the only match for the field name.
					nativeText(c, design.TypeXXS, t.LabelTertiary, a.pluginFieldLabel(f)+":")
					input := st.detailFields[f.key]
					if input == nil {
						value := f.def
						input = &value
					}
					field := ui.TextInput(c, input).Label(a.pluginFieldLabel(f)).
						Placeholder(f.def)
					if f.secret {
						field.Password()
					}
				})
			}
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.cancel")).Clicked() {
					st.detailOpen = false
				}
				if ui.PrimaryButton(c, a.T("menu.save")).Clicked() {
					a.savePluginDetail(c)
				}
			})
		})
	})
}

// pluginConfigDialog is the "Open data folder" dialog. The action targets
// the data directory, not a single configuration file: the dialog names the
// folder, highlights the index.sqlite database inside it, copies that
// database path, and opens the folder in the OS file manager. It is the
// honest surface for the control that used to be labelled "Open
// configuration file" while opening a directory.
func (a *App) pluginConfigDialog(c *ui.Context, t *design.Tokens) {
	st := a.pluginUI()
	th := t.Theme()
	ui.Modal(c, &st.configOpen, func() {
		ui.Column(c).Width(th.Space(135)).Gap(th.Space(3)).Children(func() {
			// A distinct title from the Settings row's "Open configuration
			// file" button: the dialog shows the data folder and database,
			// and reusing that name made two different controls share one
			// accessible name.
			nativeText(c, design.TypeM, t.LabelPrimary, a.tr("Configuration and database", "配置与数据库")).FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, a.tr(
				"ZeoKeel keeps its settings in index.sqlite inside this data folder. Copy the database path, or open the folder in the system file manager.",
				"ZeoKeel 的设置保存在该数据目录下的 index.sqlite 中。可复制数据库路径，或在系统文件管理器中打开该目录。"))
			nativeText(c, design.TypeXXXS, t.LabelTertiary, a.tr("Data folder", "数据目录"))
			ui.Row(c).Padding(th.Space(2), th.Space(2)).Radius(design.RadiusSM).
				Background(t.BgStrong).Children(func() {
				nativeText(c, design.TypeXXS, t.LabelPrimary, st.configDir)
			})
			nativeText(c, design.TypeXXXS, t.LabelTertiary, a.tr("Database", "数据库"))
			// The database row is tinted so it is the highlighted target the
			// Copy path action writes; the tint is the brand accent, not a
			// sampled blue.
			ui.Row(c).Padding(th.Space(2), th.Space(2)).Radius(design.RadiusSM).
				Background(t.Accent.Alpha(0.10)).Border(1, t.Accent.Alpha(0.30)).Children(func() {
				nativeText(c, design.TypeXXS, t.LabelPrimary, st.configPath).MaxLines(2)
			})
			if st.configErr != "" {
				nativeText(c, design.TypeXXS, t.WarningText, st.configErr)
			}
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				// Each control's accessible name contains its visible text
				// and is unique in the dialog.
				copyLabel := a.tr("Copy database path", "复制数据库路径")
				copyButton := ui.Button(c, copyLabel).Label(copyLabel)
				if copyButton.Clicked() {
					c.WriteClipboard(st.configPath)
					c.Toast(a.tr("Database path copied", "数据库路径已复制"))
				}
				openFileLabel := a.tr("Open database file", "打开数据库文件")
				openFile := ui.Button(c, openFileLabel).Label(openFileLabel)
				if openFile.Clicked() {
					if err := a.pluginOpener()(st.configPath); err != nil {
						st.configErr = a.tr("Could not open the configuration file.",
							"无法打开配置文件。")
					} else {
						st.configErr = ""
						c.Toast(a.tr("Opened configuration file", "已打开配置文件"))
					}
				}
				openButton := ui.Button(c, a.tr("Open folder", "打开文件夹"))
				if openButton.Clicked() {
					if err := a.pluginOpener()(st.configDir); err != nil {
						st.configErr = a.tr("Could not open the system file manager.",
							"无法打开系统文件管理器。")
					} else {
						st.configErr = ""
						c.Toast(a.tr("Opened data folder", "已打开数据目录"))
					}
				}
				if ui.PrimaryButton(c, a.pluginLocalized("Done", "完成")).Clicked() {
					st.configOpen = false
				}
			})
		})
	})
}

// openConfigurationLocation opens the data-folder dialog. The control used
// to try the file manager directly and only show the path when that failed;
// now the dialog is always the target, so the data directory, the database
// path within it and the Copy path action are all visible at once.
func (a *App) openConfigurationLocation(c *ui.Context) {
	st := a.pluginUI()
	st.configDir = a.pluginDataDir()
	st.configPath = a.pluginConfigPath()
	st.configErr = ""
	st.configOpen = true
}

// aboutSettingsPage is Settings > About: version, build information, a
// project link and copy actions.
func (a *App) aboutSettingsPage(c *ui.Context, t *design.Tokens) {
	info, ok := debug.ReadBuildInfo()
	modulePath := "github.com/zeokeel/zeokeel"
	moduleVersion := pluginProductVersion
	if ok {
		if info.Main.Path != "" {
			modulePath = info.Main.Path
		}
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			moduleVersion = info.Main.Version
		}
	}
	build := fmt.Sprintf("Go %s · %s/%s", goruntime.Version(), goruntime.GOOS, goruntime.GOARCH)
	details := fmt.Sprintf("ZeoKeel %s (%s)\n%s", pluginProductVersion, moduleVersion, build)

	ui.Column(c).Gap(design.Space3).Children(func() {
		ui.Column(c).Gap(design.Space1).Children(func() {
			settingsText(c, a.T("menu.about")+" ZeoKeel", design.TypeM, t.LabelPrimary).
				FontWeight(600)
			settingsText(c, a.T("about.version"), design.TypeXXS, t.LabelTertiary)
		})
		ui.Column(c).Gap(2).Children(func() {
			settingsText(c, build, design.TypeBody, t.LabelPrimary)
			settingsText(c, modulePath, design.TypeXXS, t.LabelTertiary)
		})
		ui.Row(c).Gap(design.Space2).Children(func() {
			link := ui.Row(c).Height(28).PaddingX(10).Radius(design.RadiusSM).
				Border(1, t.BorderL3).AlignItems(ui.Center).
				Role(ui.RoleButton).Focusable().
				Label(a.pluginLocalized("Open project repository", "打开项目仓库"))
			link.Children(func() {
				settingsText(c, a.pluginLocalized("Project repository", "项目仓库"),
					design.TypeXXS, t.LabelPrimary)
			})
			link.OnClick(func() {
				if err := a.pluginExternalOpener()("https://" + modulePath); err != nil {
					c.Toast(a.pluginLocalized("Could not open the link", "无法打开链接"))
				}
			})
			copyVersion := ui.Row(c).Height(28).PaddingX(10).Radius(design.RadiusSM).
				Background(settingsFillColor(t)).AlignItems(ui.Center).
				Label(a.pluginLocalized("Copy version", "复制版本")).
				Role(ui.RoleButton).Focusable()
			copyVersion.Children(func() {
				settingsText(c, a.pluginLocalized("Copy version", "复制版本"),
					design.TypeXXS, t.LabelPrimary)
			})
			copyVersion.OnClick(func() {
				c.WriteClipboard(pluginProductVersion)
				c.Toast(a.pluginLocalized("Version copied", "版本已复制"))
			})
			copyBuild := ui.Row(c).Height(28).PaddingX(10).Radius(design.RadiusSM).
				Background(settingsFillColor(t)).AlignItems(ui.Center).
				Label(a.pluginLocalized("Copy build info", "复制构建信息")).
				Role(ui.RoleButton).Focusable()
			copyBuild.Children(func() {
				settingsText(c, a.pluginLocalized("Copy build info", "复制构建信息"),
					design.TypeXXS, t.LabelPrimary)
			})
			copyBuild.OnClick(func() {
				c.WriteClipboard(details)
				c.Toast(a.pluginLocalized("Build info copied", "构建信息已复制"))
			})
		})
		ui.Column(c).Gap(2).Children(func() {
			settingsText(c, a.pluginLocalized("Configuration location", "配置位置"),
				design.TypeXXS, t.LabelTertiary)
			settingsText(c, a.pluginConfigPath(), design.TypeXXS, t.LabelPrimary)
		})
	})
}

// configuredPresets returns the agent presets shown on Settings > Agent
// presets. The runtime's mode list is the single source of truth.
func configuredPresets() []agentModeOption { return agentModeOptions() }

// agentPresetDefault is the persisted agent.preset value normalized to its
// canonical runtime mode, or an empty string when none was chosen or the stored
// value is not a supported mode. The controller normalizes the same setting
// when it creates a session, so the shell must compare and display the same
// canonical mode: "cordis" is Creator mode even though the picker only lists
// "creator".
func (a *App) agentPresetDefault() string {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return ""
	}
	value, err := a.Ctrl.Store().GetSetting("agent.preset")
	if err != nil || strings.TrimSpace(value) == "" {
		return ""
	}
	mode, err := runtime.NormalizeMode(value)
	if err != nil {
		return ""
	}
	return mode
}

// applyDefaultAgentPreset gives a newly opened session the preset persisted by
// Settings > Agent presets. A blank or invalid value leaves the controller's
// default (Standard) alone.
func (a *App) applyDefaultAgentPreset() {
	if a.Ctrl == nil {
		return
	}
	mode := strings.TrimSpace(a.agentPresetDefault())
	if mode == "" || mode == a.Ctrl.Mode() {
		return
	}
	_ = a.Ctrl.SetMode(mode)
}

// setAgentPresetDefault persists the chosen preset and reports it. The value
// is applied to every session the shell creates from now on by
// applyDefaultAgentPreset, so the setting is not merely stored.
func (a *App) setAgentPresetDefault(c *ui.Context, mode string) {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return
	}
	if err := a.Ctrl.Store().SetSetting("agent.preset", canonicalAgentMode(mode)); err != nil {
		c.Toast(a.pluginLocalized("Could not set the default preset", "设置默认预设失败"))
		return
	}
	c.Toast(fmt.Sprintf(a.pluginLocalized("Default preset: %s", "默认预设：%s"),
		a.agentModeLabel(mode)))
}

// agentPresetsPage is Settings > Agent presets: every runtime preset with its
// description and a persisted Set default action.
func (a *App) agentPresetsPage(c *ui.Context, t *design.Tokens) {
	// The stored value may be an alias accepted by runtime.NormalizeMode; the
	// page lists canonical presets, so compare the normalized value or the
	// selected preset would lose its "Default" badge. An unset preset stays
	// empty, so no row is marked as the default.
	current := a.agentPresetDefault()
	if current != "" {
		current = canonicalAgentMode(current)
	}
	ui.Column(c).Gap(design.Space3).Children(func() {
		ui.Column(c).Gap(design.Space1).Children(func() {
			settingsText(c, a.T("settings.agent_presets"), design.TypeM, t.LabelPrimary).
				FontWeight(600)
			settingsText(c, a.pluginLocalized(
				"Choose the preset new sessions start with. The value is stored as agent.preset.",
				"选择新会话默认使用的预设，值保存为 agent.preset。"), design.TypeXXS, t.LabelTertiary)
		})
		for _, preset := range configuredPresets() {
			selected := preset.Mode == current
			row := ui.Row(c).Height(64).Padding(design.Space3, design.Space3).
				Gap(design.Space3).Radius(design.RadiusMD).
				Border(1, t.BorderL2).AlignItems(ui.Center)
			if selected {
				row = row.Background(settingsFillColor(t)).BorderColor(settingsCardSelectedEdge)
			}
			row.Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
						settingsText(c, a.agentModeLabel(preset.Mode), design.TypeBody,
							t.LabelPrimary).FontWeight(500)
						if selected {
							a.pluginDefaultBadge(c, t)
						}
					})
					settingsText(c, a.agentModeHint(preset.Mode), design.TypeXXS,
						t.LabelTertiary)
				})
				if !selected {
					btn := ui.Row(c).Height(28).PaddingX(10).Radius(design.RadiusSM).
						Background(settingsFillColor(t)).AlignItems(ui.Center).
						Label(a.pluginLocalized("Set default "+preset.Label,
							"设为默认 "+preset.Label)).
						Role(ui.RoleButton).Focusable()
					btn.Children(func() {
						settingsText(c, a.pluginLocalized("Set default", "设为默认"),
							design.TypeXXS, t.LabelPrimary)
					})
					btn.OnClick(func() { a.setAgentPresetDefault(c, preset.Mode) })
				}
			})
		}
	})
}

// pluginDefaultBadge is the small "Default" pill beside the chosen preset.
func (a *App) pluginDefaultBadge(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(18).PaddingX(design.Space2).Radius(design.RadiusMax).
		Background(t.Accent.Alpha(0.10)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.pluginLocalized("Default", "默认")).FontSize(10).
			LineHeight(pluginLine(10, 10)).FontWeight(500).TextColor(t.Accent)
	})
}
