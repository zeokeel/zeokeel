package plugins

// The Connectors tab's dialogs: the add/edit connector form (HTTP URL or a
// STDIO subprocess with command/args/env, an optional per-server timeout and
// the async "test connection" probe), the details modal (property sheet with
// the masked environment and the selectable raw error) and the destructive
// remove confirmation. The connector model and the card page live in
// connectors_section.go.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// inputLabel is the accessible label of one form input: the visible field label
// plus the localized "input" suffix the product's forms use.
func inputLabel(st *state, label string) string {
	return label + " " + st.tr("输入", "input")
}

// transportDisplay maps the stored transport to the selector's display text.
func transportDisplay(transport string) string {
	if transport == connectorTransportStdio {
		return "STDIO"
	}
	return "HTTP"
}

// transportValue maps the selector's display text back to the stored value.
func transportValue(display string) string {
	if display == "STDIO" {
		return connectorTransportStdio
	}
	return connectorTransportHTTP
}

// Auth mode selector values (the stored strings are contracts.MCPAuthMode).
const (
	authModeNoneDisplay   = "无认证"
	authModeBearerDisplay = "Bearer 令牌"
	authModeOAuthDisplay  = "OAuth 2.1"
)

// authModeDisplay maps the stored mode to the selector's display text.
func authModeDisplay(st *state, mode string) string {
	switch contracts.MCPAuthMode(mode) {
	case contracts.MCPAuthBearer:
		return st.tr(authModeBearerDisplay, "Bearer token")
	case contracts.MCPAuthOAuth2:
		return st.tr(authModeOAuthDisplay, "OAuth 2.1")
	default:
		return st.tr(authModeNoneDisplay, "No authentication")
	}
}

// authModeValue maps the selector's display text back to the stored mode.
func authModeValue(st *state, display string) string {
	switch display {
	case st.tr(authModeBearerDisplay, "Bearer token"):
		return string(contracts.MCPAuthBearer)
	case st.tr(authModeOAuthDisplay, "OAuth 2.1"):
		return string(contracts.MCPAuthOAuth2)
	default:
		return ""
	}
}

// splitAuthScopes accepts comma/space/newline separated scopes.
func splitAuthScopes(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// openAddConnector opens the form in add mode with a clean HTTP draft.
func (p *connectorsState) openAddConnector() {
	p.formOpen = true
	p.editIndex = -1
	p.transport = connectorTransportHTTP
	p.resetFormDraft()
}

// openEditConnector loads one stored row into the form for in-place editing.
func (p *connectorsState) openEditConnector(card connectorCard) {
	if card.ConfigIndex < 0 || card.ConfigIndex >= len(p.servers) {
		return
	}
	row := p.servers[card.ConfigIndex]
	p.formOpen = true
	p.editIndex = card.ConfigIndex
	p.transport = connectorRowTransport(row)
	p.name = strings.TrimSpace(row.Name)
	p.url = strings.TrimSpace(row.URL)
	p.authMode = ""
	p.authToken = ""
	p.authScopes = ""
	p.authClientID = ""
	p.authServer = ""
	p.authAdvancedOpen = false
	if auth := row.Auth; auth != nil {
		p.authMode = string(auth.Mode)
		p.authToken = auth.Token
		p.authScopes = strings.Join(auth.Scopes, ", ")
		p.authClientID = auth.ClientID
		p.authServer = auth.AuthorizationServer
	}
	p.headersText = formatMCPHeadersText(row.Headers)
	p.headersOpen = strings.TrimSpace(p.headersText) != ""
	p.command = strings.TrimSpace(row.Command)
	p.args = strings.Join(row.Args, " ")
	p.env = strings.Join(row.Env, "\n")
	p.timeoutMs = ""
	if row.TimeoutMs > 0 {
		p.timeoutMs = strconv.Itoa(row.TimeoutMs)
	}
	p.envErr = ""
	p.formErr = ""
	p.testing = false
	p.testStatus = ""
	p.testErr = false
}

// resetFormDraft clears the form fields and returns to add mode.
func (p *connectorsState) resetFormDraft() {
	p.editIndex = -1
	p.name = ""
	p.url = ""
	p.authMode = ""
	p.authToken = ""
	p.authScopes = ""
	p.authClientID = ""
	p.authServer = ""
	p.authAdvancedOpen = false
	p.headersText = ""
	p.headersOpen = false
	p.command = ""
	p.args = ""
	p.env = ""
	p.timeoutMs = ""
	p.envErr = ""
	p.formErr = ""
	p.testing = false
	p.testStatus = ""
	p.testErr = false
}

// buildFormModal renders the add/edit connector dialog. One official form
// serves both modes; validation errors stay in the dialog and prevent the
// write.
func (p *connectorsState) buildFormModal(c *ui.Context, t *design.Tokens, st *state,
	settings contracts.SettingsService) {
	title := st.tr("添加连接器", "Add connector")
	submitLabel := st.tr("添加", "Add")
	if p.editIndex >= 0 {
		title = st.tr("编辑连接器", "Edit connector")
		submitLabel = st.tr("保存", "Save")
	}
	stdio := p.transport == connectorTransportStdio
	ui.Modal(c, &p.formOpen, func() {
		design.Apply(ui.Column(c).Key("connectors.form"), t.Panel()).
			Width(design.DialogWidthM).Padding(design.Space4).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key: "connectors.form", Title: title,
				Subtitle: transportDisplay(p.transport),
				OnClose: func() {
					p.formOpen = false
					p.resetFormDraft()
					c.Invalidate()
				},
			})
			testLabel := st.tr("测试连接", "Test connection")
			if p.testing {
				testLabel = st.tr("正在测试…", "Testing…")
			}
			ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).Children(func() {
				ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
					ui.Field(c, st.tr("传输", "Transport"), func() {
						choice := transportDisplay(p.transport)
						selector := ui.Select(c, &choice, []string{"HTTP", "STDIO"}).
							Label(inputLabel(st, st.tr("传输", "Transport")))
						if selector.Changed() {
							p.transport = transportValue(choice)
							c.Invalidate()
						}
					})
					ui.Field(c, st.tr("名称", "Name"), func() {
						ui.TextInput(c, &p.name).
							Label(inputLabel(st, st.tr("名称", "Name"))).
							Placeholder(st.tr("例如：files", "For example: files")).
							FillWidth().AutoFocus()
					})
					if stdio {
						ui.Fieldset(c, st.tr("STDIO 进程", "STDIO process"), func() {
							ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
								ui.Column(c).FillWidth().Grow(1).Children(func() {
									ui.Field(c, st.tr("命令", "Command"), func() {
										ui.TextInput(c, &p.command).
											Label(inputLabel(st, st.tr("命令", "Command"))).
											Placeholder(st.tr("例如：npx", "For example: npx")).FillWidth()
									})
								})
								ui.Column(c).FillWidth().Grow(2).Children(func() {
									ui.Field(c, st.tr("参数", "Arguments"), func() {
										ui.TextInput(c, &p.args).
											Label(inputLabel(st, st.tr("参数", "Arguments"))).
											Placeholder(st.tr("空格分隔，例如：-y server-github",
												"Space-separated, e.g. -y server-github")).FillWidth()
									}).Description(st.tr("空格分隔。", "Space-separated."))
								})
							})
							ui.Field(c, st.tr("环境变量", "Environment"), func() {
								envInput := ui.TextArea(c, &p.env).
									Label(inputLabel(st, st.tr("环境变量", "Environment"))).
									Placeholder(st.tr("每行 KEY=VALUE，例如：GITHUB_TOKEN=abc",
										"One KEY=VALUE per line, e.g. GITHUB_TOKEN=abc")).
									MinHeight(design.Space6 * 2).FillWidth()
								if envInput.Changed() {
									p.envErr = ""
								}
							}).Description(st.tr("每行一个 KEY=VALUE；值可以包含空格。",
								"One KEY=VALUE per line; values may contain spaces.")).
								Error(p.envErr)
							// The command runs as a child process with the app's
							// full permissions, outside the OS sandbox: disclose
							// the trust model before the user saves it (2026-11
							// audit (d), disclosure only — no spawn approval).
							uikit.ErrorLine(c, mcpStdioTrustDisclosure(st))
						})
					} else {
						ui.Field(c, "URL", func() {
							ui.TextInput(c, &p.url).Label(inputLabel(st, "URL")).
								Placeholder("https://mcp.example.com/mcp").FillWidth()
						})
						ui.Field(c, st.tr("认证方式", "Authentication"), func() {
							choice := authModeDisplay(st, p.authMode)
							selector := ui.Select(c, &choice, []string{
								st.tr(authModeNoneDisplay, "No authentication"),
								st.tr(authModeBearerDisplay, "Bearer token"),
								st.tr(authModeOAuthDisplay, "OAuth 2.1"),
							}).Label(inputLabel(st, st.tr("认证方式", "Authentication")))
							if selector.Changed() {
								p.authMode = authModeValue(st, choice)
								c.Invalidate()
							}
						}).Description(st.tr(
							"Bearer 令牌作为 Authorization 头发送；OAuth 2.1 在保存后于卡片上完成浏览器授权。",
							"Bearer tokens are sent as the Authorization header; OAuth 2.1 authorizes in the browser from the saved card."))
						if isGitHubCopilotMCPURL(p.url) {
							design.Apply(ui.Text(c, st.tr(
								"GitHub 远程 MCP 不提供动态客户端注册，且令牌交换需要 client secret；请选择「Bearer 令牌」填入 GitHub PAT。",
								"GitHub's remote MCP does not offer dynamic client registration and its token exchange needs a client secret; choose Bearer token and paste a GitHub PAT.",
							)), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(3)
						}
						switch contracts.MCPAuthMode(p.authMode) {
						case contracts.MCPAuthBearer:
							ui.Field(c, st.tr("Bearer 令牌", "Bearer token"), func() {
								ui.TextInput(c, &p.authToken).
									Label(inputLabel(st, st.tr("Bearer 令牌", "Bearer token"))).
									Placeholder(st.tr("粘贴访问令牌", "Paste the access token")).
									FillWidth().Password()
							}).Description(st.tr("令牌保存在设置中，诊断与模型读取时会被隐藏。",
								"The token is stored in settings and redacted from diagnostics/model reads."))
						case contracts.MCPAuthOAuth2:
							ui.Collapsible(c, st.tr("OAuth 2.1 高级选项", "OAuth 2.1 options"), &p.authAdvancedOpen, func() {
								ui.Field(c, st.tr("作用域", "Scopes"), func() {
									ui.TextInput(c, &p.authScopes).
										Label(inputLabel(st, st.tr("作用域", "Scopes"))).
										Placeholder(st.tr("留空使用服务器要求（逗号或空格分隔）",
											"Blank uses the server challenge (comma/space separated)")).
										FillWidth()
								})
								ui.Field(c, st.tr("客户端 ID（可选）", "Client ID (optional)"), func() {
									ui.TextInput(c, &p.authClientID).
										Label(inputLabel(st, st.tr("客户端 ID（可选）", "Client ID (optional)"))).
										Placeholder(st.tr("留空使用动态注册", "Blank uses dynamic registration")).
										FillWidth()
								}).Description(st.tr(
									"仅适用于支持公开客户端的授权服务器；GitHub 的令牌交换还需要 client secret，请改用 Bearer PAT。",
									"Only for authorization servers that accept public clients; GitHub also needs a client secret at token exchange, so use a Bearer PAT."))
								ui.Field(c, st.tr("授权服务器（可选）", "Authorization server (optional)"), func() {
									ui.TextInput(c, &p.authServer).
										Label(inputLabel(st, st.tr("授权服务器（可选）", "Authorization server (optional)"))).
										Placeholder(st.tr("留空从服务器元数据发现", "Blank discovers from server metadata")).
										FillWidth()
								})
							})
						}
					}
					if !stdio {
						ui.Collapsible(c, st.tr("HTTP 请求头（可选）", "HTTP headers (optional)"),
							&p.headersOpen, func() {
								ui.Field(c, st.tr("请求头", "Headers"), func() {
									ui.TextArea(c, &p.headersText).
										Label(inputLabel(st, st.tr("请求头", "Headers"))).
										Placeholder("X-API-Key: ...").
										FillWidth()
								}).Description(st.tr(
									"每行 Name: value；用于非标准认证/网关头。Authorization 头会被 Bearer/OAuth 认证覆盖；诊断与模型读取时会脱敏。",
									"One Name: value per line for custom auth/gateway headers. Authorization is overridden by Bearer/OAuth; values are redacted from diagnostics/model reads."))
							})
					}
					// The optional per-server timeout applies to both transports: one
					// request/handshake bound, blank (or 0) keeps the toolkit default.
					ui.Field(c, st.tr("超时 (ms)", "Timeout (ms)"), func() {
						ui.TextInput(c, &p.timeoutMs).
							Label(inputLabel(st, st.tr("超时 (ms)", "Timeout (ms)"))).
							Placeholder(st.tr("留空 = 默认 30 秒", "Blank = default 30s")).FillWidth()
					}).Description(st.tr("毫秒；0 或留空使用默认 30 秒。",
						"Milliseconds; 0 or blank uses the default 30s."))
					if p.formErr != "" {
						uikit.ErrorLine(c, p.formErr)
					}
					if p.testStatus != "" {
						if p.testErr {
							uikit.ErrorLine(c, p.testStatus)
						} else {
							design.Apply(ui.Text(c, p.testStatus), t.Font(design.TypeXS)).
								TextColor(t.SuccessText).MaxLines(2)
						}
					}
					if p.testing {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
							ui.Spinner(c).Label(st.tr("正在测试…", "Testing…")).
								Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
							design.Apply(ui.Text(c, st.tr("正在测试…", "Testing…")),
								t.Font(design.TypeXXS)).TextColor(t.LabelSecondary)
						})
					}
				})
			})
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: testLabel, Disabled: p.testing, OnClick: func() {
					p.startConnectorTest(st)
					c.Invalidate()
				}},
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					p.formOpen = false
					p.resetFormDraft()
					c.Invalidate()
				}},
				{Label: submitLabel, Primary: true, OnClick: func() {
					p.saveConnector(st, settings)
					c.Invalidate()
				}},
			})
		})
	})
}

// saveConnector validates the draft and writes the add/edit result back to
// settings.mcp.servers. A failed write restores the in-memory list; a form
// error keeps the dialog open.
func (p *connectorsState) saveConnector(st *state, settings contracts.SettingsService) {
	row, ok := p.connectorDraft(st, true)
	if !ok {
		return
	}
	previous := append([]contracts.MCPServerConfig(nil), p.servers...)
	rows := append([]contracts.MCPServerConfig(nil), p.servers...)
	if p.editIndex >= 0 && p.editIndex < len(rows) {
		rows[p.editIndex] = row
	} else {
		rows = append(rows, row)
	}
	encoded, err := contracts.EncodeMCPServersJSON(rows)
	if err == nil {
		err = settings.Set(contracts.SettingMCPServers, encoded)
	}
	if err != nil {
		p.servers = previous
		p.formErr = st.tr("保存失败：", "Save failed: ") + err.Error()
		st.status = p.formErr
		st.statusErr = true
		return
	}
	// A changed OAuth connector must not keep a token issued for its old
	// identity/URL/scopes: delete the stale record best-effort.
	if p.editIndex >= 0 && p.editIndex < len(previous) {
		old := previous[p.editIndex]
		if oauthRecordMustBeDropped(old, row) {
			_ = settings.Set(contracts.MCPAuthSettingKey(old.Name), "")
		}
	}
	p.servers = rows
	p.formOpen = false
	p.resetFormDraft()
	st.requestMCPReload(st.tr("已应用连接器变更", "Connector changes applied"))
}

// oauthRecordMustBeDropped reports whether editing a connector invalidates its
// persisted OAuth token (mode/name/URL/client/scopes/AS changed).
func oauthRecordMustBeDropped(previous, next contracts.MCPServerConfig) bool {
	if previous.Auth == nil || previous.Auth.Mode != contracts.MCPAuthOAuth2 {
		return false
	}
	if next.Auth == nil || next.Auth.Mode != contracts.MCPAuthOAuth2 {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(previous.Name), strings.TrimSpace(next.Name)) {
		return true
	}
	if strings.TrimSpace(previous.URL) != strings.TrimSpace(next.URL) ||
		strings.TrimSpace(previous.Auth.ClientID) != strings.TrimSpace(next.Auth.ClientID) ||
		strings.TrimSpace(previous.Auth.AuthorizationServer) != strings.TrimSpace(next.Auth.AuthorizationServer) {
		return true
	}
	return strings.Join(previous.Auth.Scopes, "\x00") != strings.Join(next.Auth.Scopes, "\x00")
}

// connectorDraft validates the form and returns the settings row. Problems are
// reported in p.formErr (or the environment field's own p.envErr); false means
// the draft may not be submitted or tested.
func (p *connectorsState) connectorDraft(st *state, checkDuplicates bool) (contracts.MCPServerConfig, bool) {
	p.formErr = ""
	name := strings.TrimSpace(p.name)
	if name == "" {
		p.formErr = st.tr("名称不能为空", "The name is required")
		return contracts.MCPServerConfig{}, false
	}
	timeoutMs, timeoutErr := parseConnectorTimeout(st, p.timeoutMs)
	if timeoutErr != "" {
		p.formErr = timeoutErr
		return contracts.MCPServerConfig{}, false
	}
	row := contracts.MCPServerConfig{Name: name, TimeoutMs: timeoutMs}
	if p.editIndex >= 0 && p.editIndex < len(p.servers) {
		row.Headers = p.servers[p.editIndex].Headers
	}

	if p.transport == connectorTransportStdio {
		command := strings.TrimSpace(p.command)
		switch {
		case command == "":
			p.formErr = st.tr("命令不能为空", "The command is required")
			return contracts.MCPServerConfig{}, false
		case strings.ContainsRune(command, '\x00'):
			p.formErr = st.tr("命令不能包含 NUL", "The command must not contain NUL")
			return contracts.MCPServerConfig{}, false
		}
		args := strings.Fields(p.args)
		if checkDuplicates {
			for index, existing := range p.servers {
				if index == p.editIndex {
					continue
				}
				if connectorRowTransport(existing) == connectorTransportStdio &&
					strings.TrimSpace(existing.Command) == command &&
					strings.Join(existing.Args, "\x00") == strings.Join(args, "\x00") {
					p.formErr = st.tr("该服务器已存在", "That server already exists")
					return contracts.MCPServerConfig{}, false
				}
			}
			if p.duplicateName(name) {
				p.formErr = st.tr("该名称已存在", "That name already exists")
				return contracts.MCPServerConfig{}, false
			}
		}
		env, envErr := parseConnectorEnv(st, p.env)
		if envErr != "" {
			p.envErr = envErr
			return contracts.MCPServerConfig{}, false
		}
		p.envErr = ""
		row.Transport = connectorTransportStdio
		row.Command = command
		row.Args = args
		row.Env = env
		row.Headers = nil
		return row, true
	}

	urlValue := strings.TrimSpace(p.url)
	switch {
	case urlValue == "":
		p.formErr = st.tr("URL 不能为空", "The URL is required")
		return contracts.MCPServerConfig{}, false
	case !validSearchEndpoint(urlValue):
		p.formErr = st.tr("URL 无效：请输入 http(s) URL", "Invalid URL: enter an http(s) URL")
		return contracts.MCPServerConfig{}, false
	}
	if checkDuplicates {
		for index, existing := range p.servers {
			if index == p.editIndex {
				continue
			}
			if connectorRowTransport(existing) == connectorTransportHTTP &&
				strings.EqualFold(strings.TrimSpace(existing.URL), urlValue) {
				p.formErr = st.tr("该 URL 已存在", "That URL already exists")
				return contracts.MCPServerConfig{}, false
			}
		}
		if p.duplicateName(name) {
			p.formErr = st.tr("该名称已存在", "That name already exists")
			return contracts.MCPServerConfig{}, false
		}
	}
	// HTTP rows keep the legacy {name,url} shape (no transport field).
	row.URL = urlValue
	if auth, authErr := p.authDraft(st); authErr != "" {
		p.formErr = authErr
		return contracts.MCPServerConfig{}, false
	} else if auth != nil {
		row.Auth = auth
	}
	headers, headersErr := parseMCPHeadersText(p.headersText)
	if headersErr != "" {
		p.formErr = headersErr
		return contracts.MCPServerConfig{}, false
	}
	row.Headers = headers
	return contracts.NormalizeMCPAuthURL(row), true
}

// formatMCPHeadersText renders the stored header map as one sorted "Name: value"
// line per entry.
func formatMCPHeadersText(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+": "+headers[name])
	}
	return strings.Join(lines, "\n")
}

// validHeaderName accepts RFC 7230 token characters.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// parseMCPHeadersText parses "Name: value" lines. Blank lines and # comments
// are ignored; duplicate names are rejected so a typo cannot silently shadow an
// authentication header.
func parseMCPHeadersText(raw string) (map[string]string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ""
	}
	headers := make(map[string]string)
	for lineNumber, line := range strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		index := strings.Index(line, ":")
		if index <= 0 {
			return nil, fmt.Sprintf("请求头第 %d 行格式应为 Name: value", lineNumber+1)
		}
		name := strings.TrimSpace(line[:index])
		value := strings.TrimSpace(line[index+1:])
		if !validHeaderName(name) {
			return nil, fmt.Sprintf("请求头第 %d 行名称无效", lineNumber+1)
		}
		if value == "" {
			return nil, fmt.Sprintf("请求头第 %d 行缺少值", lineNumber+1)
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, exists := headers[canonical]; exists {
			return nil, fmt.Sprintf("请求头 %s 重复", canonical)
		}
		headers[canonical] = value
	}
	if len(headers) == 0 {
		return nil, ""
	}
	return headers, ""
}

// authDraft validates the authentication section of one HTTP draft. A blank
// bearer token means "no authentication" (historical rows keep working).
func (p *connectorsState) authDraft(st *state) (*contracts.MCPAuthConfig, string) {
	switch contracts.MCPAuthMode(p.authMode) {
	case contracts.MCPAuthBearer:
		token := strings.TrimSpace(p.authToken)
		if token == "" {
			return nil, ""
		}
		return &contracts.MCPAuthConfig{Mode: contracts.MCPAuthBearer, Token: token}, ""
	case contracts.MCPAuthOAuth2:
		if !validOAuthEndpoint(p.url) {
			return nil, st.tr("OAuth 仅支持 https URL（本机回环地址可用 http）",
				"OAuth requires an https URL (loopback http is allowed)")
		}
		server := strings.TrimSpace(p.authServer)
		if server != "" {
			if !validOAuthEndpoint(server) {
				return nil, st.tr("授权服务器必须使用 https（本机回环地址可用 http）",
					"The authorization server must use https (loopback http is allowed)")
			}
		}
		return &contracts.MCPAuthConfig{
			Mode:                contracts.MCPAuthOAuth2,
			Scopes:              splitAuthScopes(p.authScopes),
			ClientID:            strings.TrimSpace(p.authClientID),
			AuthorizationServer: server,
		}, ""
	default:
		return nil, ""
	}
}

// duplicateName reports whether another stored row already uses name.
func (p *connectorsState) duplicateName(name string) bool {
	for index, existing := range p.servers {
		if index == p.editIndex {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(existing.Name), name) {
			return true
		}
	}
	return false
}

// isGitHubCopilotMCPURL reports the GitHub-hosted remote MCP endpoint, whose
// OAuth service has no dynamic client registration and requires a client secret
// at token exchange (a PAT is the supported path in this build).
func isGitHubCopilotMCPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "api.githubcopilot.com" || strings.HasSuffix(host, ".githubcopilot.com")
}

// validOAuthEndpoint accepts https, or http only for loopback hosts.
func validOAuthEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
		return host == "127.0.0.1" || host == "::1" || host == "localhost"
	default:
		return false
	}
}

// validSearchEndpoint reports whether raw is an absolute http(s) URL with a
// host: the connector endpoint shape the toolkit can dial.
func validSearchEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

// splitConnectorEnvLines splits the environment draft on newlines (never on
// spaces): the hint promises one KEY=VALUE per line, so values such as
// "TOKEN=a b" stay whole.
func splitConnectorEnvLines(value string) []string {
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

// parseConnectorEnv turns the KEY=VALUE draft into the stored env slice and
// reports the first malformed line: every non-empty line needs a non-empty KEY
// before the "=".
func parseConnectorEnv(st *state, value string) ([]string, string) {
	lines := splitConnectorEnvLines(value)
	if len(lines) == 0 {
		return nil, ""
	}
	for index, line := range lines {
		key, _, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, st.tr(
				fmt.Sprintf("环境变量格式无效（第 %d 行）：每行应为 KEY=VALUE", index+1),
				fmt.Sprintf("Invalid environment row %d: each line must be KEY=VALUE", index+1))
		}
	}
	return lines, ""
}

// parseConnectorTimeout validates the optional timeout draft: blank (or 0)
// keeps the toolkit default, a positive whole number is milliseconds and
// anything else is a field error.
func parseConnectorTimeout(st *state, value string) (int, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, ""
	}
	ms, err := strconv.Atoi(trimmed)
	if err != nil || ms < 0 {
		return 0, st.tr(
			"超时必须是整数毫秒（0 或留空 = 默认 30 秒）",
			"The timeout must be a whole number of milliseconds (0 or blank = default 30s)")
	}
	return ms, ""
}

// errMCPOAuthAuthorizationRequired marks an OAuth target whose live probe needs
// a browser authorization first (there is no token in the unsaved draft).
var errMCPOAuthAuthorizationRequired = errors.New("OAuth authorization required")

// MCPConnectionTester is the seam behind the async "test connection" action:
// the default probes the configured endpoint/executable, tests replace it.
var MCPConnectionTester = defaultMCPConnectionTest

// defaultMCPConnectionTest probes one draft server: a stdio command must exist
// on PATH, an HTTP endpoint must answer a JSON-RPC initialize request. Any HTTP
// answer below 500 counts as reachable (401/403 are reported as auth problems).
func defaultMCPConnectionTest(ctx context.Context, cfg contracts.MCPServerConfig) error {
	if connectorRowTransport(cfg) == connectorTransportStdio {
		command := strings.TrimSpace(cfg.Command)
		if command == "" {
			return errors.New("command is required")
		}
		if _, err := exec.LookPath(command); err != nil {
			return fmt.Errorf("executable not found: %w", err)
		}
		return nil
	}
	endpoint := strings.TrimSpace(cfg.URL)
	if endpoint == "" {
		return errors.New("url is required")
	}
	if cfg.Auth != nil && cfg.Auth.Mode == contracts.MCPAuthOAuth2 {
		return errMCPOAuthAuthorizationRequired
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"ZeoKeel","version":"probe"}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range cfg.Headers {
		request.Header.Set(name, value)
	}
	if cfg.Auth != nil && cfg.Auth.Mode == contracts.MCPAuthBearer {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Auth.Token))
	}
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

// startConnectorTest validates the draft without saving it and runs the
// connection probe off the UI thread; the localized result posts back to the
// frame.
func (p *connectorsState) startConnectorTest(st *state) {
	if p.testing || st.runtime == nil {
		return
	}
	p.testStatus = ""
	p.testErr = false
	// A validation failure keeps the form's own error line; nothing is tested.
	row, ok := p.connectorDraft(st, false)
	if !ok {
		return
	}
	p.testing = true
	runtime := st.runtime
	locale := st.locale
	go func() {
		err := MCPConnectionTester(context.Background(), row)
		runtime.Post(func() {
			p.testing = false
			if err != nil {
				p.testStatus = mcpTestStatusText(locale, err)
				p.testErr = true
			} else {
				p.testStatus = uikit.T(locale, "连接测试通过", "Connection test passed")
				p.testErr = false
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
		return uikit.T(locale, "找不到命令：请确认可执行文件在 PATH 中",
			"The command was not found: make sure the executable is on PATH")
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"),
		strings.Contains(lower, "timed out"):
		return uikit.T(locale, "连接超时（服务器未及时响应）",
			"Connection timed out (the server did not answer in time)")
	case strings.Contains(lower, "oauth authorization required"):
		return uikit.T(locale, "需要 OAuth 授权：保存后在卡片点击「授权」完成浏览器授权。",
			"OAuth authorization is required: save the connector, then use Authorize on its card.")
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"),
		strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"):
		return uikit.T(locale, "鉴权失败（请检查访问令牌或 OAuth 授权）",
			"Authentication failed (check the token or OAuth authorization)")
	case strings.Contains(lower, "refused"), strings.Contains(lower, "no such host"),
		strings.Contains(lower, "dial tcp"), strings.Contains(lower, "connect:"):
		return uikit.T(locale, "无法连接到服务器", "Could not reach the server")
	default:
		bounded := message
		if runes := []rune(bounded); len(runes) > 160 {
			bounded = string(runes[:160]) + "…"
		}
		return uikit.T(locale, "连接测试失败：", "Connection test failed: ") + bounded
	}
}

// buildDetailModal renders the read-only details of one connector: the property
// sheet with the masked environment, and the selectable raw backend error for a
// failed connection.
func (p *connectorsState) buildDetailModal(c *ui.Context, t *design.Tokens, st *state,
	cards []connectorCard, catalog contracts.MCPToolCatalog) {
	if !p.detailOpen {
		return
	}
	card, ok := findConnectorCard(cards, p.detailKey)
	if !ok {
		// The card disappeared (removed or refreshed away): close quietly.
		p.detailOpen = false
		return
	}
	ui.Modal(c, &p.detailOpen, func() {
		design.Apply(ui.Column(c).Key("connectors.detail"), t.Panel()).
			Width(design.DialogWidthM).Padding(design.Space4).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key: "connectors.detail", Title: card.Name,
				Subtitle: transportDisplay(card.Transport),
				OnClose: func() {
					p.detailOpen = false
					c.Invalidate()
				},
			})
			uikit.PropertySheet(c, uikit.PropertySheetOptions{
				Rows: connectorDetailRows(st, card), Key: "connectors.detail", Locale: st.locale,
			})
			buildConnectorToolList(c, t, st, p, card, catalog)
			if card.Failure {
				if raw := strings.TrimSpace(card.RawStatus); raw != "" {
					design.Apply(ui.Text(c, raw), t.Font(design.TypeXXS)).
						TextColor(t.DangerText).Selectable().MaxLines(4)
				}
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("关闭", "Close"), Default: true, OnClick: func() {
					p.detailOpen = false
					c.Invalidate()
				}},
			})
		})
	})
}

// connectorDetailRows is the details dialog's property sheet.
func connectorDetailRows(st *state, card connectorCard) []uikit.PropertyRow {
	rows := []uikit.PropertyRow{
		{Label: st.tr("类型", "Type"), Value: transportDisplay(card.Transport)},
	}
	if value := strings.TrimSpace(card.URL); value != "" {
		rows = append(rows, uikit.PropertyRow{Label: "URL", Value: value, Mono: true})
	}
	if value := strings.TrimSpace(card.Command); value != "" {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("命令", "Command"), Value: value, Mono: true})
	}
	if len(card.Args) > 0 {
		rows = append(rows, uikit.PropertyRow{
			Label: st.tr("参数", "Arguments"), Value: strings.Join(card.Args, " "), Mono: true,
		})
	}
	for _, entry := range card.Env {
		rows = append(rows, uikit.PropertyRow{
			Label: st.tr("环境变量", "Environment"), Value: maskMCPEnvValue(st, entry), Mono: true,
		})
	}
	if card.TimeoutMs > 0 {
		rows = append(rows, uikit.PropertyRow{
			Label: st.tr("超时", "Timeout"), Value: fmt.Sprintf("%d ms", card.TimeoutMs), Mono: true,
		})
	}
	if value := strings.TrimSpace(card.ServerName); value != "" {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("服务器", "Server"), Value: value, Mono: true})
	}
	if value := strings.TrimSpace(card.Version); value != "" {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("版本", "Version"), Value: value, Mono: true})
	}
	if value := strings.TrimSpace(card.Protocol); value != "" {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("协议", "Protocol"), Value: value, Mono: true})
	}
	if card.AuthMode == contracts.MCPAuthOAuth2 {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("认证", "Authentication"), Value: "OAuth 2.1"})
		switch card.AuthState {
		case contracts.MCPAuthStateAuthorized:
			rows = append(rows, uikit.PropertyRow{Label: st.tr("授权状态", "Authorization"), Value: st.tr("已授权", "Authorized")})
		case contracts.MCPAuthStateRequired:
			rows = append(rows, uikit.PropertyRow{Label: st.tr("授权状态", "Authorization"), Value: st.tr("需要授权", "Authorization needed")})
		case contracts.MCPAuthStateExpired:
			rows = append(rows, uikit.PropertyRow{Label: st.tr("授权状态", "Authorization"), Value: st.tr("已失效", "Expired")})
		default:
			rows = append(rows, uikit.PropertyRow{Label: st.tr("授权状态", "Authorization"), Value: textutil.FirstNonEmptyTrimmed(string(card.AuthState), st.tr("未知", "Unknown"))})
		}
		if card.AuthIssuer != "" {
			rows = append(rows, uikit.PropertyRow{Label: st.tr("授权服务器", "Authorization server"), Value: card.AuthIssuer, Mono: true})
		}
		if len(card.AuthScopes) > 0 {
			rows = append(rows, uikit.PropertyRow{Label: st.tr("作用域", "Scopes"), Value: strings.Join(card.AuthScopes, " "), Mono: true})
		}
		if !card.AuthExpires.IsZero() {
			rows = append(rows, uikit.PropertyRow{Label: st.tr("令牌到期", "Token expires"), Value: uikit.FormatDateTime(st.locale, card.AuthExpires)})
		}
		if card.AuthCode != "" {
			rows = append(rows, uikit.PropertyRow{Label: st.tr("错误码", "Error code"), Value: card.AuthCode, Mono: true})
		}
	} else if card.AuthMode == contracts.MCPAuthBearer {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("认证", "Authentication"), Value: st.tr("Bearer 令牌", "Bearer token")})
	} else if card.Transport == connectorTransportHTTP {
		rows = append(rows, uikit.PropertyRow{Label: st.tr("认证", "Authentication"), Value: st.tr("无", "None")})
	}
	rows = append(rows,
		uikit.PropertyRow{Label: st.tr("工具数", "Tools"), Value: fmt.Sprintf("%d", card.Tools)},
		uikit.PropertyRow{Label: st.tr("状态", "Status"), Value: card.StatusLabel},
	)
	return rows
}

// findConnectorCard returns the card with key from the current frame's list.
func findConnectorCard(cards []connectorCard, key string) (connectorCard, bool) {
	for _, card := range cards {
		if card.Key == key {
			return card, true
		}
	}
	return connectorCard{}, false
}

// buildRemoveConfirm asks before dropping one stored settings row. The safe
// outcome (Cancel) takes the dialog's focus and Enter; the danger action is a
// danger button without Default.
func (p *connectorsState) buildRemoveConfirm(c *ui.Context, t *design.Tokens, st *state,
	settings contracts.SettingsService) {
	if p.removeIndex < 0 || p.removeIndex >= len(p.servers) {
		p.removeOpen = false
		return
	}
	index := p.removeIndex
	name := strings.TrimSpace(p.servers[index].Name)
	if name == "" {
		name = st.tr("未命名连接器", "Untitled connector")
	}
	uikit.ConfirmDanger(c, &p.removeOpen, uikit.ConfirmDangerOptions{
		Key:   "mcp.remove",
		Title: st.tr("移除 MCP 连接器", "Remove MCP connector"),
		Message: st.tr("确认移除「", "Remove ") + name +
			st.tr("」？此操作无法撤销。", "? This cannot be undone."),
		Confirm: st.tr("移除连接器", "Remove connector"),
		Cancel:  st.tr("取消", "Cancel"),
		OnConfirm: func() {
			p.removeConnector(st, settings, index)
			c.Invalidate()
		},
	})
	if !p.removeOpen {
		p.removeIndex = -1
	}
}

// removeConnector drops one stored row and writes the remaining servers back.
// A failed write restores the in-memory list and reports the error.
func (p *connectorsState) removeConnector(st *state, settings contracts.SettingsService, index int) {
	if index < 0 || index >= len(p.servers) {
		return
	}
	previous := append([]contracts.MCPServerConfig(nil), p.servers...)
	removed := p.servers[index]
	kept := make([]contracts.MCPServerConfig, 0, len(p.servers)-1)
	kept = append(kept, p.servers[:index]...)
	kept = append(kept, p.servers[index+1:]...)
	encoded, err := contracts.EncodeMCPServersJSON(kept)
	if err == nil {
		err = settings.Set(contracts.SettingMCPServers, encoded)
	}
	if err != nil {
		p.servers = previous
		st.status = st.tr("保存失败：", "Save failed: ") + err.Error()
		st.statusErr = true
		return
	}
	if removed.Auth != nil && removed.Auth.Mode == contracts.MCPAuthOAuth2 {
		_ = settings.Set(contracts.MCPAuthSettingKey(removed.Name), "")
	}
	p.servers = kept
	if p.editIndex == index {
		p.formOpen = false
		p.resetFormDraft()
	} else if p.editIndex > index {
		p.editIndex--
	}
	st.requestMCPReload(st.tr("已移除连接器", "Connector removed"))
}

// buildConnectorToolList renders the per-server model-visible tool switches
// inside the details modal. Toggling writes the existing tools.disabled list
// and asks the catalog to rebuild; the global shortcut list stays untouched.
func buildConnectorToolList(c *ui.Context, t *design.Tokens, st *state, p *connectorsState,
	card connectorCard, catalog contracts.MCPToolCatalog) {
	if catalog == nil || card.ConfigIndex < 0 {
		return
	}
	tools := catalog.Tools(card.Name)
	if len(tools) == 0 {
		return
	}
	label := fmt.Sprintf("%s (%d)", st.tr("工具", "Tools"), len(tools))
	ui.Collapsible(c, label, &p.toolListOpen, func() {
		filtered := tools
		if query := strings.TrimSpace(strings.ToLower(p.toolQuery)); query != "" {
			filtered = make([]contracts.MCPTool, 0, len(tools))
			for _, tool := range tools {
				if strings.Contains(strings.ToLower(tool.Name), query) ||
					strings.Contains(strings.ToLower(tool.Description), query) {
					filtered = append(filtered, tool)
				}
			}
		}
		if len(tools) > 8 {
			ui.SearchField(c, &p.toolQuery).Label(st.tr("搜索工具", "Search tools")).
				Placeholder(st.tr("按名称或说明过滤…", "Filter by name or description…")).FillWidth()
		}
		if p.toolBusy {
			ui.Spinner(c).Label(st.tr("正在应用工具开关…", "Applying tool switches…")).
				Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
		}
		ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
				for _, tool := range filtered {
					enabled := tool.Enabled
					box := ui.Checkbox(c, &enabled, tool.Name).Disabled(p.toolBusy)
					if description := strings.TrimSpace(tool.Description); description != "" {
						box.Tooltip(description)
					}
					if box.Changed() && !p.toolBusy {
						p.toggleMCPTool(st, catalog, card.Name, tool.Name, enabled)
						c.Invalidate()
					}
				}
				if len(filtered) == 0 {
					design.Apply(ui.Text(c, st.tr("没有匹配的工具", "No matching tools")),
						t.TextMuted(design.TypeXS))
				}
			})
		})
	})
}

// toggleMCPTool applies one tool switch off the UI thread; the catalog rebuilds
// the MCP runtime and posts the result back.
func (p *connectorsState) toggleMCPTool(st *state, catalog contracts.MCPToolCatalog,
	server, tool string, enabled bool) {
	if p.toolBusy || st == nil || catalog == nil {
		return
	}
	p.toolBusy = true
	go func() {
		err := catalog.SetToolEnabled(context.Background(), server, tool, enabled)
		st.runtime.Post(func() {
			p.toolBusy = false
			if err != nil {
				st.status = st.tr("应用工具开关失败：", "Failed to apply tool switch: ") + err.Error()
				st.statusErr = true
			} else {
				st.status = st.tr("工具开关已应用", "Tool switch applied")
				st.statusErr = false
			}
			st.runtime.Invalidate()
		})
	}()
}
