package plugins

// The Connectors tab's OAuth 2.1 authorization half: prepare the flow off the
// UI thread, open the browser on the main thread, wait for the loopback
// callback off the UI thread, then show the result. The protocol itself lives
// in rig; this file only drives contracts.MCPAuthService and renders state.

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/openurl"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// connectorAuthConfig returns the stored row of one card as an auth config.
func (p *connectorsState) connectorAuthConfig(index int) (contracts.MCPServerConfig, bool) {
	if index < 0 || index >= len(p.servers) {
		return contracts.MCPServerConfig{}, false
	}
	row := p.servers[index]
	if row.Auth == nil || row.Auth.Mode != contracts.MCPAuthOAuth2 {
		return contracts.MCPServerConfig{}, false
	}
	return row, true
}

// startConnectorAuth starts one interactive authorization for a stored row.
func (p *connectorsState) startConnectorAuth(st *state, auth contracts.MCPAuthService, index int) {
	if st == nil || auth == nil || p.authPreparing || p.authWaiting {
		return
	}
	row, ok := p.connectorAuthConfig(index)
	if !ok {
		return
	}
	p.authOpen = true
	p.authPreparing = true
	p.authWaiting = false
	p.authError = ""
	p.authNotice = ""
	p.authFlow = contracts.MCPAuthFlow{}
	p.authConfigIndex = index
	p.authServerCfg = row
	p.authSeq++
	seq := p.authSeq
	go func() {
		flow, err := auth.Prepare(context.Background(), row)
		st.runtime.Post(func() {
			if !p.authOpen || p.authSeq != seq {
				// The modal was closed or another flow started while Prepare
				// was in flight: the listener/PKCE state must be cancelled.
				if err == nil {
					auth.Cancel(row, flow)
				}
				return
			}
			p.authPreparing = false
			if err != nil {
				p.authError = authErrorText(st, err, row)
				st.runtime.Invalidate()
				return
			}
			p.authFlow = flow
			p.authWaiting = true
			if openErr := openurl.Open(flow.AuthURL); openErr != nil {
				p.authWaiting = false
				p.authError = st.tr("无法打开系统浏览器：", "Could not open the system browser: ") + openErr.Error()
				auth.Cancel(row, flow)
				st.runtime.Invalidate()
				return
			}
			st.runtime.Invalidate()
			go p.waitConnectorAuth(st, auth, row, flow, seq)
		})
	}()
}

// waitConnectorAuth waits for the callback/token exchange and posts the result.
func (p *connectorsState) waitConnectorAuth(st *state, auth contracts.MCPAuthService,
	row contracts.MCPServerConfig, flow contracts.MCPAuthFlow, seq int) {
	status, err := auth.Wait(context.Background(), row, flow)
	st.runtime.Post(func() {
		if !p.authOpen || p.authSeq != seq {
			return
		}
		p.authWaiting = false
		p.authStatus = status
		if err != nil {
			p.authError = authErrorText(st, err, row)
			st.runtime.Invalidate()
			return
		}
		p.authOpen = false
		p.authNotice = st.tr("授权完成", "Authorization complete")
		if status.State == contracts.MCPAuthStateAuthorized && err != nil {
			st.status = st.tr("已授权，但令牌保存或重连失败：", "Authorized, but saving the token or reconnecting failed: ") + err.Error()
			st.statusErr = true
		} else {
			st.status = st.tr("已授权", "Authorized")
			st.statusErr = false
		}
		st.runtime.Invalidate()
	})
}

// authErrorText localizes the well-known failure causes and keeps the raw
// diagnostic bounded for the details line.
func authErrorText(st *state, err error, cfg contracts.MCPServerConfig) string {
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "interactive_unavailable"):
		return st.tr("当前环境没有系统浏览器，无法完成交互式授权；请使用 Bearer 令牌。",
			"No system browser is available; use a Bearer token instead.")
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"),
		strings.Contains(lower, "callback_timeout"):
		return st.tr("等待浏览器授权超时，请重试。", "Timed out waiting for browser authorization; try again.")
	case strings.Contains(lower, "pkce"):
		return st.tr("授权服务器不支持安全的 PKCE(S256)，已拒绝授权。",
			"The authorization server does not advertise PKCE S256; authorization was refused.")
	case strings.Contains(lower, "issuer_mismatch"):
		return st.tr("授权服务器元数据校验失败（issuer 不匹配）。",
			"The authorization server metadata failed validation (issuer mismatch).")
	case strings.Contains(lower, "state_mismatch"), strings.Contains(lower, "iss_mismatch"):
		return st.tr("授权回调校验失败，请重新授权。", "The authorization callback failed validation; re-authorize.")
	case strings.Contains(lower, "registration"):
		if isGitHubCopilotMCPURL(cfg.URL) {
			return st.tr("GitHub 远程 MCP 不支持动态客户端注册；请改用「Bearer 令牌」填写 GitHub PAT（OAuth 令牌交换需要 client secret，当前版本不发送）。",
				"GitHub's remote MCP does not support dynamic client registration; use Bearer token with a GitHub PAT (OAuth token exchange needs a client secret, which this build does not send).")
		}
		return st.tr("客户端注册失败；该服务可能不支持动态注册。可填写预注册客户端 ID，或改用 Bearer 令牌。",
			"Client registration failed; the service may not support dynamic registration. Fill in a pre-registered client ID, or use a Bearer token.")
	case strings.Contains(lower, "revoked"):
		return st.tr("授权已被撤销。", "The authorization was revoked.")
	}
	bounded := message
	if runes := []rune(bounded); len(runes) > 200 {
		bounded = string(runes[:200]) + "…"
	}
	return st.tr("授权失败：", "Authorization failed: ") + bounded
}

// cancelConnectorAuth drops the pending flow (safe on the UI thread).
func (p *connectorsState) cancelConnectorAuth(st *state, auth contracts.MCPAuthService) {
	p.authSeq++
	if p.authFlow.AuthURL != "" && st != nil && auth != nil {
		auth.Cancel(p.authServerCfg, p.authFlow)
	}
	p.authOpen = false
	p.authPreparing = false
	p.authWaiting = false
	p.authFlow = contracts.MCPAuthFlow{}
	p.authError = ""
	if st != nil {
		st.runtime.Invalidate()
	}
}

// buildConnectorAuthModal renders the authorization modal for one flow.
func (p *connectorsState) buildConnectorAuthModal(c *ui.Context, t *design.Tokens, st *state,
	auth contracts.MCPAuthService) {
	if !p.authOpen {
		return
	}
	name := strings.TrimSpace(p.authServerCfg.Name)
	if name == "" {
		name = st.tr("连接器", "Connector")
	}
	modal := ui.Modal(c, &p.authOpen, func() {
		design.Apply(ui.Column(c.Key("connectors.auth")), t.Panel()).
			Width(design.DialogWidthM).Padding(design.Space4).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key: "connectors.auth", Title: st.tr("授权连接器", "Authorize connector"),
				Subtitle: name + " · OAuth 2.1",
				OnClose: func() {
					p.cancelConnectorAuth(st, auth)
				},
			})
			switch {
			case p.authError != "":
				uikit.ErrorLine(c, p.authError)
				design.Apply(ui.Text(c, st.tr(
					"可检查浏览器是否完成授权后重试。",
					"Check the browser, then retry.")), t.TextMuted(design.TypeXS)).MaxLines(2)
			case p.authPreparing:
				authSpinnerRow(c, t, st.tr("正在准备授权…", "Preparing authorization…"))
			case p.authWaiting:
				authSpinnerRow(c, t, st.tr("等待浏览器授权…", "Waiting for browser authorization…"))
				design.Apply(ui.Text(c, st.tr(
					"已在系统浏览器中打开授权页面；完成后会自动返回。",
					"The authorization page was opened in your browser; it returns automatically.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
				if redirect := strings.TrimSpace(p.authFlow.RedirectURI); redirect != "" {
					design.Apply(ui.Text(c, redirect), t.MonoText(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					if design.Apply(ui.Button(c, st.tr("重新打开浏览器", "Reopen browser")),
						t.SecondaryButton()).Clicked() {
						if err := openurl.Open(p.authFlow.AuthURL); err != nil {
							p.authError = st.tr("无法打开浏览器：", "Could not open the browser: ") + err.Error()
						}
						c.Invalidate()
					}
					if design.Apply(ui.Button(c, st.tr("复制授权链接", "Copy authorization link")),
						t.SecondaryButton()).Clicked() {
						c.WriteClipboard(p.authFlow.AuthURL)
					}
				})
			default:
				design.Apply(ui.Text(c, st.tr("授权流程已结束。", "The authorization flow has finished.")),
					t.TextMuted(design.TypeXS))
			}
			footer := []uikit.DialogAction{}
			if p.authError != "" {
				footer = append(footer, uikit.DialogAction{Label: st.tr("重试", "Retry"), OnClick: func() {
					index := p.authConfigIndex
					p.cancelConnectorAuth(st, auth)
					p.startConnectorAuth(st, auth, index)
				}})
			}
			footer = append(footer, uikit.DialogAction{Label: st.tr("取消", "Cancel"), Default: p.authError != "",
				OnClick: func() { p.cancelConnectorAuth(st, auth) }})
			uikit.DialogFooter(c, footer)
		})
	})
	// Esc / click-outside dismisses the modal without running a footer action;
	// the pending flow must still be cancelled or the in-flight guard would
	// block every later authorization.
	if modal.Dismissed() {
		p.cancelConnectorAuth(st, auth)
	}
}

// authSpinnerRow is the preparing/waiting line: spinner + status copy.
func authSpinnerRow(c *ui.Context, t *design.Tokens, label string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Spinner(c).Label(label).Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
	})
}

// buildRevokeConfirm confirms and runs one token revocation.
func (p *connectorsState) buildRevokeConfirm(c *ui.Context, t *design.Tokens, st *state,
	auth contracts.MCPAuthService) {
	if !p.revokeOpen {
		return
	}
	cfg := p.revokeServer
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = st.tr("连接器", "Connector")
	}
	uikit.ConfirmDanger(c, &p.revokeOpen, uikit.ConfirmDangerOptions{
		Key:   "connectors.revoke",
		Title: st.tr("撤销连接器授权", "Revoke connector authorization"),
		Message: st.tr("撤销「", "Revoke the authorization of \"") + name +
			st.tr("」的 OAuth 授权？下次连接需要重新授权。", "\"? The next connection will require authorization again."),
		Confirm: st.tr("撤销授权", "Revoke authorization"),
		Cancel:  st.tr("取消", "Cancel"),
		OnConfirm: func() {
			go func() {
				err := auth.Revoke(context.Background(), cfg)
				st.runtime.Post(func() {
					if err != nil {
						st.status = st.tr("撤销授权失败：", "Failed to revoke authorization: ") + err.Error()
						st.statusErr = true
					} else {
						st.status = st.tr("已撤销授权", "Authorization revoked")
						st.statusErr = false
					}
					st.runtime.Invalidate()
				})
			}()
		},
	})
	if !p.revokeOpen {
		p.revokeServer = contracts.MCPServerConfig{}
	}
}
