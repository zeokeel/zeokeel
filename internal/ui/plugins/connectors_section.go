package plugins

// The Connectors tab: the configured and live MCP servers as an official
// ui.GridView of independent object cards. The tab is the only place a
// connector is configured: adding, editing, removing, testing, switching a
// server on/off and the workspace mcp.json opt-in all live here. The card model
// merges the stored settings rows (settings.mcp.servers, the editable source)
// with the live plugins-service snapshot (name, status, tool count), so one
// server never renders twice and a stored row the toolkit has not started yet
// still shows as "restart needed" instead of vanishing.
//
// Actions run through the settings/plugins contracts; the editing dialogs live
// in connectors_edit.go.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// The two MCP transports stored in settings.mcp.servers.
const (
	connectorTransportHTTP  = "http"
	connectorTransportStdio = "stdio"
)

// connectorCard is one rendered connector: a stored settings row (ConfigIndex
// >= 0) merged with its live state, or a live-only assembly/workspace server
// (ConfigIndex < 0, read-only). It is rebuilt every frame from the settings row
// and the service snapshot; nothing here outlives the frame.
type connectorCard struct {
	// Key is the card's stable grid identity: the stored name (plus the address
	// when two stored rows share a name), or the live server identity.
	Key string
	// Name is the connector's display label. Duplicate names are suffixed
	// ("name · #2") for this frame only; it is never written to settings or
	// used as a token key.
	Name string
	// ConfigName is the stored/server identity (settings row name or live
	// server name). It stays undecorated: enable switches, auth token keys and
	// the tool catalog all address this value.
	ConfigName string
	// Transport is connectorTransportHTTP or connectorTransportStdio.
	Transport string
	// Address is the URL for HTTP rows or "command args" for stdio rows.
	Address string
	// ConfigIndex is the stored row the card edits (-1 for live-only servers).
	ConfigIndex int
	// Enabled is the mcp.disabledServers state of the server.
	Enabled bool

	// Tools is the live tool count (0 without a live connection).
	Tools int
	// RawStatus is the backend's own state text (details modal / tooltip only).
	RawStatus string
	// StatusLabel/StatusKind are the localized badge.
	StatusLabel   string
	StatusKind    uikit.StatusKind
	StatusTooltip string
	// Failure marks a failed connection: the card shows the localized cause and
	// offers error details/retry instead of the raw backend error.
	Failure        bool
	FailureSummary string

	// Detail properties from the stored row and the live handshake.
	URL        string
	Command    string
	Args       []string
	Env        []string
	TimeoutMs  int
	ServerName string
	Version    string
	Protocol   string

	// OAuth 2.1 authorization state of a stored connector (zero for STDIO and
	// unauthenticated HTTP rows).
	AuthMode    contracts.MCPAuthMode
	AuthState   contracts.MCPAuthState
	AuthMessage string
	AuthCode    string
	AuthIssuer  string
	AuthScopes  []string
	AuthExpires time.Time
	// AuthAction marks a row that should offer the secondary "Authorize" /
	// "Re-authorize" action instead of the generic retry.
	AuthAction bool
}

// connectorsState is the Connectors tab's UI state (only touched on the UI
// thread).
type connectorsState struct {
	// grid is the official GridView's identity/place state.
	grid ui.GridState

	// loaded marks the one storage read (settings.mcp.servers).
	loaded bool
	// servers is the stored, editable connector list; it is the write source.
	servers []contracts.MCPServerConfig
	// parseErr reports a malformed stored JSON (the list stays read-only empty).
	parseErr string

	// optionsOpen is the "Connector options" disclosure state.
	optionsOpen bool
	// workspaceLoaded/workspaceOptIn/workspaceRoot back the workspace mcp.json
	// opt-in switch and its path hint (loaded once per page visit).
	workspaceLoaded bool
	workspaceOptIn  bool
	workspaceRoot   string

	// Add/edit dialog (one form serves both; editIndex = -1 is add mode).
	formOpen  bool
	editIndex int
	transport string
	name      string
	url       string
	command   string
	args      string
	env       string
	// headersText/headersOpen are the optional HTTP header draft (Name: value
	// per line) and its disclosure state. Headers apply to HTTP connectors
	// only; OAuth/Bearer auth takes precedence over a same-named header.
	headersText string
	headersOpen bool
	// timeoutMs is the optional per-server timeout draft (milliseconds, blank =
	// the toolkit default 30 s). It is a string so the form can reject a
	// non-numeric draft instead of silently saving 0.
	timeoutMs string
	// envErr is the environment field's own error (KEY=VALUE per line).
	envErr string
	// nameErr is the name field's own error: the one validation message that has
	// an obvious field to attach to, so it renders under 名称 instead of in the
	// dialog's reserved error row at the bottom of the form.
	nameErr string
	// formErr is the form-level validation/save error shown in the dialog's
	// reserved error row above the footer (never inside the scrolling body: an
	// error inside the clip was sliced in half at the fold, 2026-11-18 review).
	formErr string

	// testing/testStatus/testErr are the async connection test's state.
	testing    bool
	testStatus string
	testErr    bool

	// detailOpen/detailKey are the details modal's state.
	detailOpen bool
	detailKey  string
	// Tool list disclosure inside the details modal.
	toolListOpen bool
	toolQuery    string
	toolBusy     bool
	// detailErrOpen is the raw-error disclosure inside the details modal
	// (closed each time the modal opens: the localized cause leads).
	detailErrOpen bool

	// reloading is true while the toolkit rebuilds MCP after a settings write.
	reloading bool
	// removeOpen/removeIndex are the destructive remove confirmation's state.
	removeOpen  bool
	removeIndex int

	// OAuth form fields (HTTP rows only): the mode selector plus the static
	// bearer token and the optional OAuth overrides.
	authMode         string
	authToken        string
	authScopes       string
	authClientID     string
	authServer       string
	authAdvancedOpen bool

	// Interactive authorization modal state. The flow is prepared off the UI
	// thread, the browser is opened on the main thread, then Wait runs off the
	// UI thread; authFlow/authServer identify the pending flow.
	authOpen        bool
	authPreparing   bool
	authWaiting     bool
	authFlow        contracts.MCPAuthFlow
	authSeq         int
	authConfigIndex int
	authServerCfg   contracts.MCPServerConfig
	authError       string
	authNotice      string
	authStatus      contracts.MCPAuthStatus

	// revokeOpen/revokeServer confirm revoking one connector's token.
	revokeOpen   bool
	revokeServer contracts.MCPServerConfig
}

// newConnectorsState returns an empty connectors tab state.
func newConnectorsState() *connectorsState {
	return &connectorsState{
		editIndex:   -1,
		removeIndex: -1,
		transport:   connectorTransportHTTP,
	}
}

// buildConnectorsTab renders the Connectors tab's page body for one frame.
func buildConnectorsTab(c *ui.Context, t *design.Tokens, st *state) {
	p := st.connectors
	authService, hasAuth := st.ctx.Get(contracts.KeyMCPAuth)
	hasAuth = hasAuth && authService != nil
	catalog, hasCatalog := st.ctx.Get(contracts.KeyMCPToolCatalog)
	hasCatalog = hasCatalog && catalog != nil
	settings, hasSettings := st.ctx.Get(contracts.KeySettings)
	hasSettings = hasSettings && settings != nil
	service, hasPlugins := st.ctx.Get(contracts.KeyPlugins)
	hasPlugins = hasPlugins && service != nil

	if hasSettings {
		st.loadDisabledServers(settings, true)
		if !p.loaded {
			p.loadStorage(st, settings)
		}
	}
	cards := p.connectorCards(st, service, hasPlugins, authService, hasAuth)

	// The intro line comes first, then the count/action row — the same order
	// the Skills tab uses, so the two tabs read as one page family.
	// The page hint states when a change lands, read from the composition's
	// actual hot-reload capability (A5 P0): the unconditional "restart" sentence
	// was false wherever the reloader is mounted.
	design.Apply(ui.Text(c, connectorPageHint(st)),
		t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
	p.buildCollectionRow(c, t, st, service, hasPlugins, settings, hasSettings, len(cards))
	if p.parseErr != "" {
		uikit.ErrorLine(c, p.parseErr)
	}
	// A missing live service is not an empty list: the stored configuration
	// stays manageable, and the hint says why no live state shows.
	if !hasPlugins {
		hint := st.tr("连接器服务未启用", "Connector service not enabled")
		if hasSettings {
			hint += st.tr("，仅显示已保存的配置。", "; showing saved configuration only.")
		} else {
			hint += "。"
		}
		design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(2)
	}
	if !hasSettings {
		uikit.EmptyState(c, st.tr("设置存储未启用", "Settings store not enabled"))
		return
	}

	if len(cards) == 0 {
		uikit.EmptyStateBlock(c,
			st.tr("还没有配置 MCP 服务器", "No MCP connectors configured yet"),
			st.tr("点击「添加连接器」把外部 MCP 服务接入会话。",
				"Choose Add connector to bring an external MCP server into the session."),
			nil)
	} else {
		p.grid.Key = func(index int) any {
			if index < 0 || index >= len(cards) {
				return nil
			}
			return cards[index].Key
		}
		p.grid.Label = func(index int) string {
			if index < 0 || index >= len(cards) {
				return ""
			}
			return cards[index].Name
		}
		ui.GridView(c, &p.grid, len(cards), cardGridMinWidth, connectorCardHeight, func(index int) {
			p.buildCard(c, t, st, cards[index], settings, service, hasPlugins, authService, hasAuth)
		}).
			Label(st.tr("MCP 连接器列表", "MCP connectors list")).
			MaxHeight(cardGridViewportTwoRows)
	}

	// The workspace opt-in is a connector option, not a second connector list:
	// it stays a disclosure under the grid.
	ui.Collapsible(c, st.tr("连接器选项", "Connector options"), &p.optionsOpen, func() {
		// The one boolean option uses the settings pages' switch-row anatomy
		// (uikit.SwitchRow: caption at the section edge, switch at the trailing
		// edge of the SAME line, description below). A bare ui.Field left the
		// switch alone on the next line, reading as an orphaned control
		// (2026-11-18 review).
		// The row reads as one pair only inside the product's reading measure:
		// the settings pages put SwitchRow in a 640 DIP column, so a switch on the
		// page's right edge (some 500 DIP from its caption) looked detached
		// (2026-11-18 review). Caption and description share the 640 column.
		ui.Column(c).MaxWidth(design.ProseMeasure).Children(func() {
			if uikit.SwitchRow(c, uikit.SwitchRowOptions{
				Label:       st.tr("读取工作区 mcp.json", "Read workspace mcp.json"),
				Description: mcpWorkspaceHint(st, p),
				Value:       &p.workspaceOptIn,
			}) {
				p.saveWorkspaceOptIn(st, settings)
				c.Invalidate()
			}
		})
		// The opt-in lets repository content define commands that run with the
		// app's full permissions: disclose the trust exchange next to the path
		// hint (2026-11 audit (d), disclosure only). A static caution is
		// warning semantic — red stays for real failures.
		uikit.WarningLine(c, t, mcpStdioTrustDisclosure(st))
	})

	if p.formOpen {
		p.buildFormModal(c, t, st, settings)
	}
	if p.detailOpen {
		var toolCatalog contracts.MCPToolCatalog
		if hasCatalog {
			toolCatalog = catalog
		}
		p.buildDetailModal(c, t, st, cards, toolCatalog)
	}
	p.buildConnectorAuthModal(c, t, st, authService)
	p.buildRevokeConfirm(c, t, st, authService)
	p.buildRemoveConfirm(c, t, st, settings)
}

// buildCollectionRow is the tab's one action row: the connector count, the
// reconnect utility and the create action.
func (p *connectorsState) buildCollectionRow(c *ui.Context, t *design.Tokens, st *state,
	service contracts.PluginsService, hasPlugins bool, settings contracts.SettingsService,
	hasSettings bool, count int) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if count > 0 {
			design.Apply(ui.Text(c, connectorCountLabel(st, count)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1)
		} else {
			ui.Spacer(c).Grow(1)
		}
		if st.refreshing || p.reloading {
			label := st.tr("正在重连…", "Reconnecting…")
			if p.reloading {
				label = st.tr("正在应用连接器…", "Applying connectors…")
			}
			ui.Spinner(c).Label(label).
				Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
		}
		refreshLabel := st.tr("刷新连接", "Reconnect")
		refreshTooltip := st.tr("重新连接并刷新 MCP 连接器状态",
			"Reconnect and refresh MCP connector status")
		if st.refreshing {
			refreshTooltip = st.tr("正在重连…", "Reconnecting…")
		}
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:   "connectors.refresh",
			Label: refreshLabel, Tooltip: refreshTooltip,
			Icon: icons.Reload(), Size: design.IconSizeS,
			Disabled: st.refreshing || p.reloading || !hasPlugins,
			OnClick: func() {
				if hasPlugins && !st.refreshing {
					st.startMCPRefresh(service)
					c.Invalidate()
				}
			},
		})
		addLabel := st.tr("添加连接器", "Add connector")
		// A malformed stored JSON is read-only: adding would silently overwrite
		// configuration the user may want to repair by hand.
		canAdd := hasSettings && p.parseErr == ""
		add := design.Apply(ui.Button(c, addLabel), t.SecondaryButton()).Disabled(!canAdd)
		// A disabled control explains itself in the page body (the empty state or
		// the ErrorLine above), never in a tooltip (B-spec §4.4); the Description
		// still tells assistive technology why the action is unavailable.
		switch {
		case !hasSettings:
			add = add.Description(st.tr("设置存储未启用，无法保存连接器",
				"Settings store not enabled; connectors cannot be saved"))
		case p.parseErr != "":
			add = add.Description(st.tr("配置无法解析，请先修复设置中的 mcp.servers",
				"The configuration cannot be parsed; repair mcp.servers in settings first"))
		}
		if add.Clicked() && canAdd {
			p.openAddConnector()
			c.Invalidate()
		}
	})
}

// loadStorage reads settings.mcp.servers and the workspace opt-in once per page
// visit. A malformed JSON array is reported and leaves the list empty and
// read-only; the stored bytes are never rewritten.
func (p *connectorsState) loadStorage(st *state, settings contracts.SettingsService) {
	p.loaded = true
	p.workspaceLoaded = true
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		p.parseErr = st.tr("配置解析失败：", "Configuration parse failed: ") + err.Error()
		p.servers = nil
	} else {
		for index := range rows {
			rows[index] = contracts.NormalizeMCPAuthURL(rows[index])
		}
		p.servers = rows
	}
	p.workspaceOptIn = strings.EqualFold(
		strings.TrimSpace(settings.Get(contracts.SettingMCPWorkspaceOptIn, "false")), "true")
	if workspace, ok := st.ctx.Get(contracts.KeyWorkspace); ok && workspace != nil {
		if current, has, err := workspace.Current(context.Background()); err == nil && has {
			p.workspaceRoot = current.Root
		}
	}
}

// connectorCards merges the stored rows with the live snapshot into one card
// list: stored rows keep the settings order (and their edit/remove identity),
// then the live-only assembly/workspace servers follow sorted by name. A stored
// row matches a live server by name (or the handshake's server name).
func (p *connectorsState) connectorCards(st *state, service contracts.PluginsService,
	hasPlugins bool, auth contracts.MCPAuthService, hasAuth bool) []connectorCard {
	var live []contracts.MCPPlugin
	if hasPlugins {
		live = service.MCP()
	}
	used := make([]bool, len(live))
	nameCounts := make(map[string]int, len(p.servers))
	for _, row := range p.servers {
		nameCounts[strings.ToLower(strings.TrimSpace(row.Name))]++
	}

	cards := make([]connectorCard, 0, len(p.servers)+len(live))
	for index, row := range p.servers {
		matched := -1
		for liveIndex, plugin := range live {
			if used[liveIndex] || !connectorNameMatches(row.Name, plugin) {
				continue
			}
			matched = liveIndex
			break
		}
		if matched >= 0 {
			used[matched] = true
		}
		cards = append(cards, buildStoredConnectorCard(st, row, index, live, matched, nameCounts, auth, hasAuth))
	}

	liveOnly := make([]contracts.MCPPlugin, 0)
	seen := make(map[string]bool, len(live))
	for index, plugin := range live {
		if used[index] {
			continue
		}
		identity := strings.ToLower(textutil.FirstNonEmptyTrimmed(plugin.Name, plugin.ServerName, plugin.URL))
		if identity != "" && seen[identity] {
			continue
		}
		seen[identity] = true
		liveOnly = append(liveOnly, plugin)
	}
	sort.SliceStable(liveOnly, func(left, right int) bool {
		return strings.ToLower(liveOnly[left].Name) < strings.ToLower(liveOnly[right].Name)
	})
	for index, plugin := range liveOnly {
		cards = append(cards, buildLiveConnectorCard(st, plugin, index))
	}
	disambiguateConnectorNames(cards)
	return cards
}

// disambiguateConnectorNames makes duplicate display labels distinguishable
// without touching ConfigName/Key/ConfigIndex: the first occurrence keeps the
// plain label, later occurrences get " · #N" in grid order. Matching is
// case-insensitive and whitespace-trimmed, like the connector identity rules.
func disambiguateConnectorNames(cards []connectorCard) {
	if len(cards) < 2 {
		return
	}
	totals := make(map[string]int, len(cards))
	keys := make([]string, len(cards))
	for index := range cards {
		key := strings.ToLower(strings.TrimSpace(cards[index].Name))
		keys[index] = key
		totals[key]++
	}
	seen := make(map[string]int, len(totals))
	for index := range cards {
		key := keys[index]
		seen[key]++
		if totals[key] > 1 && seen[key] > 1 {
			cards[index].Name = fmt.Sprintf("%s · #%d", cards[index].Name, seen[key])
		}
	}
}

// buildStoredConnectorCard builds the card of one settings row merged with its
// live state (matched is the live slice index, or -1).
func buildStoredConnectorCard(st *state, row contracts.MCPServerConfig, index int,
	live []contracts.MCPPlugin, matched int, nameCounts map[string]int,
	auth contracts.MCPAuthService, hasAuth bool) connectorCard {
	transport := connectorRowTransport(row)
	address := connectorRowAddress(row)
	name := strings.TrimSpace(row.Name)
	card := connectorCard{
		Key:         connectorStoredKey(name, address, nameCounts),
		Name:        textutil.FirstNonEmptyTrimmed(name, st.tr("未命名连接器", "Untitled connector")),
		ConfigName:  name,
		Transport:   transport,
		Address:     address,
		ConfigIndex: index,
		Enabled:     !st.serverDisabled(name),
		URL:         strings.TrimSpace(row.URL),
		Command:     strings.TrimSpace(row.Command),
		Args:        append([]string(nil), row.Args...),
		Env:         append([]string(nil), row.Env...),
		TimeoutMs:   row.TimeoutMs,
	}
	var plugin contracts.MCPPlugin
	liveMatched := matched >= 0
	if liveMatched {
		plugin = live[matched]
		card.Tools = plugin.Tools
		card.RawStatus = plugin.Status
		card.ServerName = plugin.ServerName
		card.Version = plugin.Version
		card.Protocol = plugin.Protocol
	}
	hotReload := st.mcpHotReload()
	switch {
	case !card.Enabled:
		card.StatusKind = uikit.StatusIdle
		card.StatusLabel = st.tr("已停用", "Disabled")
		if hotReload {
			card.StatusTooltip = st.tr("已停用（已断开，启用后自动重连）",
				"Disabled (disconnected; enabling reconnects automatically)")
		} else {
			if st.mcpHotReload() {
				card.StatusTooltip = st.tr("已停用（已断开，启用后自动重连）",
					"Disabled (disconnected; enabling reconnects automatically)")
			} else {
				card.StatusTooltip = st.tr("已停用（重启生效）", "Disabled (restart applies)")
			}
		}
	case liveMatched:
		card.StatusKind, card.StatusLabel = connectorStatus(st, plugin.Status)
		card.StatusTooltip = strings.TrimSpace(plugin.Status)
		card.Failure = pluginStatusFailed(plugin.Status)
	case hotReload && st.connectors != nil && st.connectors.reloading:
		card.StatusKind = uikit.StatusIdle
		card.StatusLabel = st.tr("正在应用…", "Applying…")
		card.StatusTooltip = st.tr("正在重建 MCP 连接", "Rebuilding the MCP connection")
	case hotReload:
		card.StatusKind = uikit.StatusIdle
		card.StatusLabel = st.tr("正在连接…", "Connecting…")
		card.StatusTooltip = st.tr("保存后自动连接，无需重启", "Connects automatically after saving")
	case st.pendingEnabled(card.ConfigName):
		card.StatusKind = uikit.StatusIdle
		card.StatusLabel = st.tr("待重启", "Restart needed")
		card.StatusTooltip = st.tr("已启用（重启后连接）", "Enabled (connects after restart)")
	default:
		card.StatusKind = uikit.StatusIdle
		card.StatusLabel = st.tr("待重启", "Restart needed")
		card.StatusTooltip = st.tr("保存后需重启连接", "Connects after a restart")
	}
	applyConnectorAuthStatus(st, &card, row, auth, hasAuth)
	if card.Failure {
		card.FailureSummary = mcpFailureSummary(st, card.RawStatus)
	}
	return card
}

// applyConnectorAuthStatus folds the toolkit's read-only OAuth snapshot onto one
// stored connector card. Authorization states take precedence over the generic
// connection failure copy: the user action is "authorize", not "retry".
func applyConnectorAuthStatus(st *state, card *connectorCard, row contracts.MCPServerConfig,
	auth contracts.MCPAuthService, hasAuth bool) {
	if st == nil || card == nil || !hasAuth || auth == nil || row.Auth == nil {
		return
	}
	card.AuthMode = row.Auth.Mode
	if row.Auth.Mode != contracts.MCPAuthOAuth2 {
		return
	}
	status := auth.Status(row)
	card.AuthMode = contracts.MCPAuthOAuth2
	card.AuthState = status.State
	card.AuthMessage = status.Message
	card.AuthCode = status.Code
	card.AuthIssuer = status.Issuer
	card.AuthScopes = append([]string(nil), status.Scopes...)
	card.AuthExpires = status.ExpiresAt
	if !card.Enabled {
		return
	}
	switch status.State {
	case contracts.MCPAuthStateRequired:
		card.StatusKind = uikit.StatusNeedsAction
		card.StatusLabel = st.tr("需要授权", "Authorization needed")
		card.StatusTooltip = textutil.FirstNonEmptyTrimmed(status.Message, st.tr("OAuth 2.1 授权尚未完成", "OAuth 2.1 authorization is not complete"))
		card.Failure = false
		card.AuthAction = true
		card.FailureSummary = st.tr("需要授权（OAuth 2.1）", "Authorization needed (OAuth 2.1)")
	case contracts.MCPAuthStateExpired:
		card.StatusKind = uikit.StatusNeedsAction
		card.StatusLabel = st.tr("授权已失效", "Authorization expired")
		card.StatusTooltip = textutil.FirstNonEmptyTrimmed(status.Message, st.tr("需要重新授权", "Re-authorization required"))
		card.Failure = false
		card.AuthAction = true
		card.FailureSummary = st.tr("授权已失效，请重新授权", "Authorization expired; re-authorize to reconnect")
	case contracts.MCPAuthStateAuthorizing:
		card.StatusKind = uikit.StatusNeedsAction
		card.StatusLabel = st.tr("授权中", "Authorizing")
		card.StatusTooltip = st.tr("正在等待浏览器授权", "Waiting for browser authorization")
		card.Failure = false
		card.AuthAction = true
		card.FailureSummary = st.tr("等待浏览器授权…", "Waiting for browser authorization…")
	}
}

// buildLiveConnectorCard builds a read-only card for a live-only assembly or
// workspace server: it has no settings row, so edit/remove stay unavailable.
func buildLiveConnectorCard(st *state, plugin contracts.MCPPlugin, index int) connectorCard {
	identity := textutil.FirstNonEmptyTrimmed(plugin.Name, plugin.ServerName, plugin.URL)
	name := textutil.FirstNonEmptyTrimmed(identity, st.tr("未命名连接器", "Untitled connector"))
	configName := textutil.FirstNonEmptyTrimmed(identity, name)
	transport := connectorTransportHTTP
	if strings.TrimSpace(plugin.URL) == "" {
		transport = connectorTransportStdio
	}
	card := connectorCard{
		Key:         "live:" + mcpServerKey(plugin, index),
		Name:        name,
		ConfigName:  configName,
		Transport:   transport,
		Address:     strings.TrimSpace(plugin.URL),
		ConfigIndex: -1,
		Enabled:     !st.serverDisabled(configName),
		Tools:       plugin.Tools,
		RawStatus:   plugin.Status,
		ServerName:  plugin.ServerName,
		Version:     plugin.Version,
		Protocol:    plugin.Protocol,
	}
	switch {
	case !card.Enabled:
		card.StatusKind = uikit.StatusIdle
		card.StatusLabel = st.tr("已停用", "Disabled")
		card.StatusTooltip = st.tr("已停用（重启生效）", "Disabled (restart applies)")
	default:
		card.StatusKind, card.StatusLabel = connectorStatus(st, plugin.Status)
		card.StatusTooltip = strings.TrimSpace(plugin.Status)
		card.Failure = pluginStatusFailed(plugin.Status)
	}
	if card.Failure {
		card.FailureSummary = mcpFailureSummary(st, card.RawStatus)
	}
	return card
}

// buildCard draws one connector as an independent object card: header with the
// name, status badge and ⋯ actions; the transport address; the failure summary
// with details/retry; the metadata row; and the server-level enable switch.
func (p *connectorsState) buildCard(c *ui.Context, t *design.Tokens, st *state,
	card connectorCard, settings contracts.SettingsService, service contracts.PluginsService,
	hasPlugins bool, auth contracts.MCPAuthService, hasAuth bool) {
	// One capability card (B-spec §3.4.6): panel surface at RadiusLG 12, 16 DIP
	// inside, a 14/600 title and a 12/18 two-line description; hover brightens the
	// fill and never adds a shadow (a card is not an elevation).
	cardBody := design.Apply(ui.Column(c).Key("connector/"+card.Key), t.Panel()).
		FillWidth().FillHeight().Padding(design.SpaceSection).Gap(design.Space2)
	if cardBody.Hovered() {
		cardBody = cardBody.Background(t.BgHover)
	}
	cardBody.Transition(design.TransitionColors(c)).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.Plug()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
			// TypeBody with the one documented semibold exception (B-spec §1.3).
			design.Apply(ui.Text(c, card.Name), t.Font(design.TypeBody)).
				FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary).
				Grow(1).Shrink(1).MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
			badge := uikit.StatusBadge(c, t, card.StatusKind, card.StatusLabel)
			if tooltip := strings.TrimSpace(card.StatusTooltip); tooltip != "" {
				badge = badge.Tooltip(tooltip)
			}
			actionsLabel := rowActionLabel(st, st.tr("连接器操作", "Connector actions"), card.Name)
			menu := uikit.IconButton(c, uikit.IconButtonOptions{
				Key:   "connector.menu/" + card.Key,
				Label: actionsLabel, Tooltip: actionsLabel,
				Icon: icons.More(), Size: design.IconSizeS,
			})
			menu.PopoverMenuButton(func(m *ui.Menu) {
				if m.Item(st.tr("详情", "Details")).Chosen() {
					p.detailOpen = true
					p.detailKey = card.Key
					p.toolListOpen = false
					p.toolQuery = ""
					p.detailErrOpen = false
					c.Invalidate()
				}
				// Only a stored settings row can be edited or removed: a live
				// assembly/workspace server has no row to rewrite.
				if card.ConfigIndex < 0 {
					return
				}
				if card.AuthMode == contracts.MCPAuthOAuth2 {
					// Tokens refresh automatically (near expiry + background loop);
					// an authorized connector must not keep suggesting re-auth.
					if card.AuthState != contracts.MCPAuthStateAuthorized {
						authLabel := st.tr("授权", "Authorize")
						if card.AuthState == contracts.MCPAuthStateExpired {
							authLabel = st.tr("重新授权", "Re-authorize")
						}
						if m.Item(authLabel).Chosen() {
							p.startConnectorAuth(st, auth, card.ConfigIndex)
							c.Invalidate()
						}
					}
					if card.AuthState == contracts.MCPAuthStateAuthorized {
						revoke := m.Item(st.tr("撤销授权", "Revoke authorization"))
						revoke.Danger()
						if revoke.Chosen() {
							p.revokeServer = p.servers[card.ConfigIndex]
							p.revokeOpen = true
							c.Invalidate()
						}
					}
					m.Separator()
				}
				if m.Item(st.tr("编辑", "Edit")).Chosen() {
					p.openEditConnector(card)
					c.Invalidate()
				}
				remove := m.Item(st.tr("移除", "Remove"))
				remove.Danger()
				if remove.Chosen() {
					p.removeIndex = card.ConfigIndex
					p.removeOpen = true
					c.Invalidate()
				}
			})
		})

		address := textutil.FirstNonEmptyTrimmed(card.Address, mcpDetailValue(""))
		design.Apply(ui.Text(c, address), t.MonoText(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Tooltip(card.Address)
		switch {
		case card.Failure:
			rawStatus := strings.TrimSpace(card.RawStatus)
			design.Apply(ui.Text(c, card.FailureSummary), t.Font(design.TypeXXS)).
				TextColor(t.DangerText).MaxLines(1).Ellipsis("…").Tooltip(rawStatus)
		case card.AuthAction && card.FailureSummary != "":
			design.Apply(ui.Text(c, card.FailureSummary), t.Font(design.TypeXXS)).
				TextColor(t.WarningText).MaxLines(1).Ellipsis("…").Tooltip(card.AuthMessage)
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			// A transport is a type, not a status: neutral badge (DESIGN.md §5.2).
			design.Apply(ui.Badge(c, connectorTransportBadge(card.Transport)), t.Badge())
			design.Apply(ui.Text(c, connectorToolCountLabel(st, card.Tools)),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
			ui.Spacer(c).Grow(1)
			if card.AuthAction {
				authLabel := st.tr("授权", "Authorize")
				if card.AuthState == contracts.MCPAuthStateExpired || card.AuthState == contracts.MCPAuthStateAuthorized {
					authLabel = st.tr("重新授权", "Re-authorize")
				}
				if design.Apply(ui.Button(c, authLabel), t.SecondaryButton()).Clicked() {
					p.startConnectorAuth(st, auth, card.ConfigIndex)
					c.Invalidate()
				}
				if design.Apply(ui.Button(c, st.tr("查看详情", "View details")),
					t.SecondaryButton()).Clicked() {
					p.detailOpen = true
					p.detailKey = card.Key
					p.detailErrOpen = false
					c.Invalidate()
				}
				return
			}
			if !card.Failure {
				return
			}
			if design.Apply(ui.Button(c, st.tr("查看报错详情", "View error details")),
				t.SecondaryButton()).Clicked() {
				p.detailOpen = true
				p.detailKey = card.Key
				p.detailErrOpen = false
				c.Invalidate()
			}
			retry := design.Apply(ui.Button(c, st.tr("重试", "Retry")),
				t.SecondaryButton()).Disabled(st.refreshing).
				Tooltip(st.tr("重新连接并刷新连接器状态", "Reconnect and refresh connector status"))
			if retry.Clicked() && !st.refreshing && hasPlugins {
				st.startMCPRefresh(service)
				c.Invalidate()
			}
		})

		ui.Spacer(c).Grow(1)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1).Children(func() {
			ui.Spacer(c).Grow(1)
			design.Apply(ui.Text(c, st.tr("启用", "Enable")), t.TextMuted(design.TypeXXS))
			enabled := card.Enabled
			sw := ui.Switch(c, &enabled)
			sw.Label(st.tr("启用连接器 ", "Enable connector ") + card.Name)
			if sw.Changed() {
				st.setServerEnabled(card.ConfigName, enabled, settings)
				c.Invalidate()
			}
		})
	})
}

// saveWorkspaceOptIn writes the workspace mcp.json opt-in and rolls the switch
// back on a failed write.
func (p *connectorsState) saveWorkspaceOptIn(st *state, settings contracts.SettingsService) {
	enabled := p.workspaceOptIn
	if err := settings.Set(contracts.SettingMCPWorkspaceOptIn, fmt.Sprintf("%t", enabled)); err != nil {
		p.workspaceOptIn = !enabled
		st.status = st.tr("保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	st.requestMCPReload(st.tr("已应用工作区 mcp.json 变更", "Workspace mcp.json changes applied"))
}

// mcpStdioTrustDisclosure is the stdio/LSP trust-channel disclosure (2026-11
// audit (d)): stdio/command connectors and LSP start child processes directly
// from configuration, outside the OS sandbox — configuration is code
// execution. Shared by the connector form's STDIO section and the workspace
// mcp.json opt-in, in the LSP settings risk-line style. Personal-agent scale:
// disclosure only, no spawn approval is added.
func mcpStdioTrustDisclosure(st *state) string {
	return st.tr(
		"stdio/命令型连接器与 LSP 由配置直接启动子进程，不经 OS 沙箱包装；配置即代码执行，只应添加你信任的来源。",
		"stdio/command connectors and LSP start child processes directly from configuration, without OS sandbox wrapping; configuration is code execution — only add sources you trust.")
}

// mcpWorkspaceHint names the workspace file the opt-in reads and when it
// applies.
func mcpWorkspaceHint(st *state, p *connectorsState) string {
	if root := strings.TrimSpace(p.workspaceRoot); root != "" {
		path := filepath.Join(root, "mcp.json")
		if st.mcpHotReload() {
			return st.tr("从 "+path+" 读取；开启/关闭会立即应用。",
				"Reads "+path+"; toggling applies immediately.")
		}
		return st.tr("启动时读取 "+path+"；修改后重启生效。",
			"Extra servers are read from "+path+" on launch; restart to apply changes.")
	}
	return st.tr("打开工作区并重启后读取其 mcp.json。",
		"Open a workspace and restart to read its mcp.json.")
}

// connectorRowTransport reports the effective transport of one stored row: rows
// with a command (or an explicit stdio transport) run a child process,
// everything else uses Streamable HTTP (legacy {name,url} rows included).
func connectorRowTransport(row contracts.MCPServerConfig) string {
	if strings.EqualFold(strings.TrimSpace(row.Transport), connectorTransportStdio) ||
		strings.TrimSpace(row.Command) != "" {
		return connectorTransportStdio
	}
	return connectorTransportHTTP
}

// connectorRowAddress is the card address of one stored row: the URL for HTTP
// rows, or "command args..." for stdio rows.
func connectorRowAddress(row contracts.MCPServerConfig) string {
	if connectorRowTransport(row) == connectorTransportStdio {
		return strings.TrimSpace(strings.TrimSpace(row.Command) + " " + strings.Join(row.Args, " "))
	}
	return strings.TrimSpace(row.URL)
}

// connectorNameMatches reports whether a stored row name identifies the live
// server (by configured name or the handshake's server name).
func connectorNameMatches(storedName string, plugin contracts.MCPPlugin) bool {
	name := strings.ToLower(strings.TrimSpace(storedName))
	if name == "" {
		return false
	}
	for _, candidate := range []string{plugin.Name, plugin.ServerName} {
		if strings.ToLower(strings.TrimSpace(candidate)) == name {
			return true
		}
	}
	return false
}

// connectorStoredKey is the stable grid identity of a stored row. Duplicate
// names keep distinct identity through the address instead of the index.
func connectorStoredKey(name, address string, nameCounts map[string]int) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch {
	case normalized == "":
		return "stored:address:" + address
	case nameCounts[normalized] > 1:
		return "stored:" + normalized + "\x00" + address
	default:
		return "stored:" + normalized
	}
}

// connectorStatus folds one backend status onto the shared status vocabulary
// and the card's short localized label; the raw text stays the tooltip.
func connectorStatus(st *state, raw string) (uikit.StatusKind, string) {
	kind := pluginStatusKind(raw)
	switch kind {
	case uikit.StatusSuccess:
		return kind, st.tr("已连接", "Connected")
	case uikit.StatusError:
		return kind, st.tr("连接失败", "Connection failed")
	default:
		return uikit.StatusIdle, st.tr("未知", "Unknown")
	}
}

// connectorTransportBadge is the compact badge text of one transport.
func connectorTransportBadge(transport string) string {
	if transport == connectorTransportStdio {
		return "stdio"
	}
	return "HTTP"
}

// connectorCountLabel is the tab's connector count copy.
func connectorCountLabel(st *state, count int) string {
	if st.english() {
		if count == 1 {
			return "1 connector"
		}
		return fmt.Sprintf("%d connectors", count)
	}
	return fmt.Sprintf("%d 个连接器", count)
}

// connectorToolCountLabel is the card's tool-count copy.
func connectorToolCountLabel(st *state, count int) string {
	if st.english() {
		return fmt.Sprintf("%d tools", count)
	}
	return fmt.Sprintf("%d 个工具", count)
}

// connectorPageHint describes when connector changes apply: hot reload keeps
// the copy honest, while older compositions still need a restart.
func connectorPageHint(st *state) string {
	if st != nil && st.mcpHotReload() {
		return st.tr(
			"连接器把外部 MCP 服务（第三方工具服务，如浏览器、数据库、搜索）接入会话；新增、编辑、启停与授权立即生效。",
			"Connectors bring external MCP servers (third-party tool services such as browsers, databases and search) into the session; add, edit, enablement and authorization apply immediately.")
	}
	return st.tr(
		"连接器把外部 MCP 服务（第三方工具服务，如浏览器、数据库、搜索）接入会话；启用变更在重启后生效。",
		"Connectors bring external MCP servers (third-party tool services such as browsers, databases and search) into the session; enablement changes apply after a restart.")
}
