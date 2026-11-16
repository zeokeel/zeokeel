package plugins_test

// Frame tests for the Connectors tab's OAuth half: the authentication section
// of the form, the "needs authorization" card state, the two-phase browser
// authorization and revocation. The protocol is faked at the
// contracts.MCPAuthService seam (rig owns the real protocol).

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/openurl"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// fakeMCPAuth is a scriptable contracts.MCPAuthService.
type fakeMCPAuth struct {
	mu sync.Mutex

	status contracts.MCPAuthStatus
	flow   contracts.MCPAuthFlow

	prepareErr     error
	prepareBlock   chan struct{}
	prepareStarted chan struct{}
	waitErr        error
	waitStatus     contracts.MCPAuthStatus

	prepareCalls int
	waitCalls    int
	cancelCalls  int
	revokeCalls  int
}

func (f *fakeMCPAuth) Status(contracts.MCPServerConfig) contracts.MCPAuthStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeMCPAuth) Prepare(context.Context, contracts.MCPServerConfig) (contracts.MCPAuthFlow, error) {
	f.mu.Lock()
	f.prepareCalls++
	started, block := f.prepareStarted, f.prepareBlock
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block != nil {
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prepareErr != nil {
		return contracts.MCPAuthFlow{}, f.prepareErr
	}
	if f.flow.AuthURL == "" {
		f.flow = contracts.MCPAuthFlow{
			AuthURL:     "https://auth.example.com/authorize",
			RedirectURI: "http://127.0.0.1:43210/callback",
			Issuer:      "https://auth.example.com",
		}
	}
	return f.flow, nil
}

func (f *fakeMCPAuth) Wait(context.Context, contracts.MCPServerConfig, contracts.MCPAuthFlow) (contracts.MCPAuthStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCalls++
	if f.waitErr != nil {
		return f.status, f.waitErr
	}
	if f.waitStatus.State != "" {
		f.status = f.waitStatus
	} else {
		f.status = contracts.MCPAuthStatus{State: contracts.MCPAuthStateAuthorized}
	}
	return f.status, nil
}

func (f *fakeMCPAuth) Cancel(contracts.MCPServerConfig, contracts.MCPAuthFlow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCalls++
}

func (f *fakeMCPAuth) Refresh(context.Context, contracts.MCPServerConfig) (contracts.MCPAuthStatus, error) {
	return f.Status(contracts.MCPServerConfig{}), nil
}

func (f *fakeMCPAuth) Revoke(context.Context, contracts.MCPServerConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeCalls++
	f.status = contracts.MCPAuthStatus{State: contracts.MCPAuthStateNone}
	return nil
}

func (f *fakeMCPAuth) counts() (prepare, wait, cancel, revoke int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prepareCalls, f.waitCalls, f.cancelCalls, f.revokeCalls
}

func (f *fakeMCPAuth) setStatus(status contracts.MCPAuthStatus) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

// newAuthConnectorHarness mounts the screen with settings and an auth service.
func newAuthConnectorHarness(t *testing.T, service contracts.PluginsService,
	values map[string]string, auth contracts.MCPAuthService) (*harness, *connectorSettings) {
	t.Helper()
	h := newHarness(t, service, nil, plugins.Config{})
	store := newConnectorSettings(values)
	provide[contracts.SettingsService](t, h.ctx, contracts.KeySettings, store)
	if auth != nil {
		provide[contracts.MCPAuthService](t, h.ctx, contracts.KeyMCPAuth, auth)
	}
	return h, store
}

// useOpenURL replaces the browser launcher seam for one test.
func useOpenURL(t *testing.T, fn func(string) error) *[]string {
	t.Helper()
	var opened []string
	previous := openurl.Open
	openurl.Open = func(raw string) error {
		opened = append(opened, raw)
		if fn != nil {
			return fn(raw)
		}
		return nil
	}
	t.Cleanup(func() { openurl.Open = previous })
	return &opened
}

const oauthConnectorJSON = `[{"name":"notion","url":"https://mcp.example.com/mcp","auth":{"mode":"oauth2","scopes":["files:read"]}}]`

func TestConnectorFormSavesOAuthAuthentication(t *testing.T) {
	auth := &fakeMCPAuth{}
	h, store := newAuthConnectorHarness(t, &connectorPlugins{}, nil, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")

	fillConnectorInput(t, h, tester, "名称 输入", "notion")
	fillConnectorInput(t, h, tester, "URL 输入", "https://mcp.example.com/mcp")
	h.click(tester, "认证方式 输入")
	if err := tester.Click("OAuth 2.1"); err != nil {
		t.Fatalf("choose OAuth: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.click(tester, "OAuth 2.1 高级选项")
	fillConnectorInput(t, h, tester, "作用域 输入", "files:read")
	fillConnectorInput(t, h, tester, "客户端 ID（可选） 输入", "pre-client")
	scrollConnectorTo(t, tester, "添加")
	h.click(tester, "添加")

	rows := storedConnectorRows(t, store)
	if len(rows) != 1 || rows[0].Auth == nil {
		t.Fatalf("stored rows = %+v, want one OAuth row", rows)
	}
	row := rows[0]
	if row.Auth.Mode != contracts.MCPAuthOAuth2 || row.Auth.ClientID != "pre-client" {
		t.Fatalf("stored auth = %+v", row.Auth)
	}
	if len(row.Auth.Scopes) != 1 || row.Auth.Scopes[0] != "files:read" {
		t.Fatalf("stored scopes = %v", row.Auth.Scopes)
	}
	h.assertTexts(tester, "待重启")
}

func TestConnectorCardNeedsAuthorizationAndCompletesFlow(t *testing.T) {
	auth := &fakeMCPAuth{status: contracts.MCPAuthStatus{State: contracts.MCPAuthStateRequired}}
	opened := useOpenURL(t, nil)
	h, _ := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.assertTexts(tester, "需要授权", "授权")
	h.click(tester, "授权")
	waitForConnectorText(t, tester, "已授权")

	prepare, waitCalls, _, _ := auth.counts()
	if prepare != 1 || waitCalls != 1 {
		t.Fatalf("auth calls prepare=%d wait=%d, want 1/1", prepare, waitCalls)
	}
	if len(*opened) != 1 || (*opened)[0] != "https://auth.example.com/authorize" {
		t.Fatalf("opened URLs = %v", *opened)
	}
}

func TestConnectorAuthorizationFailureKeepsModalRetry(t *testing.T) {
	auth := &fakeMCPAuth{
		status:     contracts.MCPAuthStatus{State: contracts.MCPAuthStateRequired},
		prepareErr: errors.New("discovery_failed: no protected resource metadata"),
	}
	h, _ := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "授权")
	waitForConnectorText(t, tester, "授权失败：")
	h.assertTexts(tester, "重试", "取消")
	if err := tester.Click("取消"); err != nil {
		t.Fatalf("cancel modal: %v", err)
	}
	tester.Frame()
}

func TestConnectorRevokeAuthorization(t *testing.T) {
	auth := &fakeMCPAuth{status: contracts.MCPAuthStatus{State: contracts.MCPAuthStateAuthorized}}
	h, _ := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器操作 notion")
	if err := tester.Click("撤销授权"); err != nil {
		t.Fatalf("open revoke: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "撤销连接器授权")
	if err := tester.Click("撤销授权"); err != nil {
		t.Fatalf("confirm revoke: %v; texts = %v", err, tester.Texts())
	}
	waitForConnectorText(t, tester, "已撤销授权")
	if _, _, _, revoke := auth.counts(); revoke != 1 {
		t.Fatalf("revoke calls = %d, want 1", revoke)
	}
}

func TestConnectorCancelWhilePrepareInFlight(t *testing.T) {
	auth := &fakeMCPAuth{
		status:         contracts.MCPAuthStatus{State: contracts.MCPAuthStateRequired},
		prepareBlock:   make(chan struct{}),
		prepareStarted: make(chan struct{}, 1),
	}
	h, _ := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "授权")
	select {
	case <-auth.prepareStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Prepare did not start")
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatalf("cancel while preparing: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	close(auth.prepareBlock)
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, cancel, _ := auth.counts()
		if cancel == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("in-flight flow was not cancelled; cancel calls = %d", cancel)
		}
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConnectorEditPreservesOAuthAuthentication(t *testing.T) {
	auth := &fakeMCPAuth{status: contracts.MCPAuthStatus{State: contracts.MCPAuthStateRequired}}
	h, store := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器操作 notion")
	if err := tester.Click("编辑"); err != nil {
		t.Fatalf("open edit: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "编辑连接器", "OAuth 2.1")
	scrollConnectorTo(t, tester, "保存")
	h.click(tester, "保存")
	rows := storedConnectorRows(t, store)
	if len(rows) != 1 || rows[0].Auth == nil {
		t.Fatalf("editing lost the auth config: %+v", rows)
	}
	if rows[0].Auth.Mode != contracts.MCPAuthOAuth2 || len(rows[0].Auth.Scopes) != 1 {
		t.Fatalf("stored auth after edit = %+v", rows[0].Auth)
	}
}

func TestConnectorAuthorizationEscapeCancelsFlow(t *testing.T) {
	useOpenURL(t, nil)
	auth := &fakeMCPAuth{
		status:         contracts.MCPAuthStatus{State: contracts.MCPAuthStateRequired},
		prepareBlock:   make(chan struct{}),
		prepareStarted: make(chan struct{}, 1),
	}
	h, _ := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "授权")
	select {
	case <-auth.prepareStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Prepare did not start")
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	close(auth.prepareBlock)
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, cancel, _ := auth.counts()
		if cancel == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Esc-dismissed flow was not cancelled; cancel calls = %d", cancel)
		}
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	// The in-flight guard must be released so a retry can start.
	h.click(tester, "授权")
	if prepare, _, _, _ := auth.counts(); prepare != 2 {
		t.Fatalf("retry after Esc did not start a new flow (prepare=%d)", prepare)
	}
}

func TestConnectorRemoveDropsOAuthToken(t *testing.T) {
	auth := &fakeMCPAuth{status: contracts.MCPAuthStatus{State: contracts.MCPAuthStateAuthorized}}
	tokenKey := contracts.MCPAuthSettingKey("notion")
	h, store := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: oauthConnectorJSON,
		tokenKey:                    `{"version":1}`,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器操作 notion")
	if err := tester.Click("移除"); err != nil {
		t.Fatalf("open remove: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("移除连接器"); err != nil {
		t.Fatalf("confirm remove: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := store.get(tokenKey); got != "" {
		t.Fatalf("removed connector kept its OAuth token: %q", got)
	}
}

func TestConnectorGitHubRegistrationFailureSuggestsPAT(t *testing.T) {
	auth := &fakeMCPAuth{
		status:     contracts.MCPAuthStatus{State: contracts.MCPAuthStateRequired},
		prepareErr: errors.New("mcp oauth: client_registration_failed: registration_endpoint missing"),
	}
	useOpenURL(t, nil)
	h, _ := newAuthConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: `[{"name":"github","url":"https://api.githubcopilot.com/mcp/","auth":{"mode":"oauth2"}}]`,
	}, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "授权")
	waitForConnectorText(t, tester, "GitHub 远程 MCP 不支持动态客户端注册")
	h.assertTexts(tester, "Bearer 令牌", "GitHub PAT")
}

var _ = ui.KeyEscape
