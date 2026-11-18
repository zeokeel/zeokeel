package plugins_test

// Frame tests for connector hot reload: a successful settings write asks the
// toolkit's MCPReloader to rebuild the connections and reports the apply result
// instead of claiming a restart is required.

import (
	"context"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeMCPReloader struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeMCPReloader) Reload(context.Context) ([]contracts.MCPPlugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil, f.err
}

func (f *fakeMCPReloader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func provideReloader(t *testing.T, h *harness, reloader contracts.MCPReloader) {
	t.Helper()
	provide[contracts.MCPReloader](t, h.ctx, contracts.KeyMCPReloader, reloader)
}

func TestConnectorSaveAppliesWithoutRestart(t *testing.T) {
	reloader := &fakeMCPReloader{}
	h, store := newConnectorHarness(t, &connectorPlugins{}, nil)
	provideReloader(t, h, reloader)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	fillConnectorInput(t, h, tester, "名称 输入", "hot")
	fillConnectorInput(t, h, tester, "URL 输入", "https://mcp.example.com/mcp")
	h.click(tester, "添加")

	waitForConnectorText(t, tester, "已应用连接器变更")
	if got := reloader.count(); got != 1 {
		t.Fatalf("reload calls = %d, want 1", got)
	}
	if got := len(storedConnectorRows(t, store)); got != 1 {
		t.Fatalf("stored rows = %d, want 1", got)
	}
	h.assertNoTexts(tester, "重启生效")
}

func TestConnectorRemoveAppliesWithoutRestart(t *testing.T) {
	reloader := &fakeMCPReloader{}
	h, store := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	provideReloader(t, h, reloader)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器操作：files")
	if err := tester.Click("移除"); err != nil {
		t.Fatalf("open remove: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("移除连接器"); err != nil {
		t.Fatalf("confirm remove: %v; texts = %v", err, tester.Texts())
	}
	waitForConnectorText(t, tester, "已移除连接器")
	if got := reloader.count(); got != 1 {
		t.Fatalf("reload calls = %d, want 1", got)
	}
	if got := store.get(contracts.SettingMCPServers); got != "[]" {
		t.Fatalf("stored rows after remove = %q, want []", got)
	}
	h.assertNoTexts(tester, "files", "重启生效")
}

func TestConnectorEnableSwitchAppliesWithoutRestart(t *testing.T) {
	reloader := &fakeMCPReloader{}
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{
		{Name: "srv", URL: "http://127.0.0.1:9/mcp", Status: "已连接", Tools: 1},
	}}
	h, store := newConnectorHarness(t, service, nil)
	provideReloader(t, h, reloader)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "启用连接器 srv")
	waitForConnectorText(t, tester, "已应用连接器启停变更")
	if got := store.get(contracts.SettingMCPDisabledServers); got != `["srv"]` {
		t.Fatalf("disabled servers = %q", got)
	}
	if got := reloader.count(); got != 1 {
		t.Fatalf("reload calls = %d, want 1", got)
	}
}

func TestConnectorReloadFailureIsReported(t *testing.T) {
	reloader := &fakeMCPReloader{err: context.DeadlineExceeded}
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	provideReloader(t, h, reloader)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	fillConnectorInput(t, h, tester, "名称 输入", "slow")
	fillConnectorInput(t, h, tester, "URL 输入", "https://mcp.example.com/mcp")
	h.click(tester, "添加")
	waitForConnectorText(t, tester, "应用失败：")
}

func TestConnectorOAuthURLTrailingSlashNormalized(t *testing.T) {
	auth := &fakeMCPAuth{}
	h, store := newAuthConnectorHarness(t, &connectorPlugins{}, nil, auth)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	fillConnectorInput(t, h, tester, "名称 输入", "github")
	fillConnectorInput(t, h, tester, "URL 输入", "https://api.githubcopilot.com/mcp/")
	h.click(tester, "认证方式 输入")
	if err := tester.Click("OAuth 2.1"); err != nil {
		t.Fatalf("choose OAuth: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "GitHub 远程 MCP 不提供动态客户端注册")
	scrollConnectorTo(t, tester, "添加")
	h.click(tester, "添加")
	rows := storedConnectorRows(t, store)
	if len(rows) != 1 || rows[0].URL != "https://api.githubcopilot.com/mcp" {
		t.Fatalf("stored rows = %+v, want the URL without the trailing slash", rows)
	}
}

var _ = ui.KeyEscape
