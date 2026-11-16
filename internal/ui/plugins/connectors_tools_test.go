package plugins_test

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeMCPToolCatalog struct {
	mu    sync.Mutex
	tools map[string][]contracts.MCPTool
	calls []string
}

func (f *fakeMCPToolCatalog) Tools(server string) []contracts.MCPTool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.MCPTool(nil), f.tools[server]...)
}

func (f *fakeMCPToolCatalog) SetToolEnabled(_ context.Context, server, tool string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, server+"/"+tool)
	list := f.tools[server]
	for index := range list {
		if list[index].Name == tool {
			list[index].Enabled = enabled
		}
	}
	f.tools[server] = list
	return nil
}

func TestConnectorDetailsToolSwitches(t *testing.T) {
	catalog := &fakeMCPToolCatalog{tools: map[string][]contracts.MCPTool{
		"files": {
			{Name: "mcp__files__echo", Description: "Echo text", Enabled: true},
			{Name: "mcp__files__search", Description: "Search", Enabled: true},
		},
	}}
	h, _ := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	provide[contracts.MCPToolCatalog](t, h.ctx, contracts.KeyMCPToolCatalog, catalog)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器操作 files")
	if err := tester.Click("详情"); err != nil {
		t.Fatalf("open details: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.click(tester, "工具 (2)")
	tester.Frame()
	if err := tester.Click("mcp__files__echo"); err != nil {
		t.Fatalf("toggle tool: %v; texts = %v", err, tester.Texts())
	}
	waitForConnectorText(t, tester, "工具开关已应用")
	catalog.mu.Lock()
	calls := append([]string(nil), catalog.calls...)
	catalog.mu.Unlock()
	if len(calls) != 1 || calls[0] != "files/mcp__files__echo" {
		t.Fatalf("tool switch calls = %v", calls)
	}
}
