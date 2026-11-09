package settings_test

// Tests for the MCP page's second-batch depth (E-17): in-place editing of a
// stored server, the async "test connection" action (spinner, no frame freeze,
// localized result) and the submit action staying visible in the STDIO form.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// useMCPConnectionTester swaps the connection-test seam for one test.
func useMCPConnectionTester(t *testing.T, fn func(context.Context, contracts.MCPServerConfig) error) {
	t.Helper()
	previous := settings.MCPConnectionTester
	settings.MCPConnectionTester = fn
	t.Cleanup(func() { settings.MCPConnectionTester = previous })
}

func TestMCPServerRowEditsInPlace(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"srv","url":"http://127.0.0.1:9/mcp"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.click(tester, "编辑")
	h.assertTexts(tester, "编辑服务器", "保存", "取消编辑")
	clearAndType(t, h, tester, "URL 输入", "http://127.0.0.1:10/mcp")
	h.click(tester, "保存")

	rows := parseStoredMCPRows(t, store)
	if len(rows) != 1 || rows[0].URL != "http://127.0.0.1:10/mcp" || rows[0].Name != "srv" {
		t.Fatalf("stored rows = %+v, want the edited row only", rows)
	}
	h.assertTexts(tester, "已保存，重启生效")
	h.assertNoTexts(tester, "编辑服务器")
	h.assertTexts(tester, "添加服务器")
}

func TestMCPConnectionTestIsAsyncAndDoesNotSave(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	useMCPConnectionTester(t, func(context.Context, contracts.MCPServerConfig) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	})

	clearAndType(t, h, tester, "名称 输入", "探针")
	clearAndType(t, h, tester, "URL 输入", "http://127.0.0.1:9/mcp")
	h.click(tester, "测试连接")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection test did not start")
	}
	// The frame keeps rendering while the test is in flight.
	h.assertTexts(tester, "正在测试…")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("testing saved the draft: %q", got)
	}
	close(release)
	waitForText(t, tester, "连接测试通过")
	h.assertNoTexts(tester, "正在测试…")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("testing saved the draft after finishing: %q", got)
	}
}

func TestMCPConnectionTestLocalizesKnownCause(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	useMCPConnectionTester(t, func(context.Context, contracts.MCPServerConfig) error {
		return errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
	})
	clearAndType(t, h, tester, "名称 输入", "探针")
	clearAndType(t, h, tester, "URL 输入", "http://127.0.0.1:9/mcp")
	h.click(tester, "测试连接")
	waitForText(t, tester, "无法连接到服务器")
}

func TestMCPSubmitStaysVisibleWithStdioForm(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	selectStdioTransport(t, h, tester)
	h.assertTexts(tester, "测试连接", "添加")

	// The submit action lives in the section header, so the tall STDIO form
	// cannot push it below the fold (the old in-body footer did exactly that).
	rect, ok := tester.Find("添加")
	if !ok || rect.W == 0 || rect.H == 0 {
		t.Fatalf("添加 action is not visible in the STDIO form: rect=%+v ok=%v texts=%v", rect, ok, tester.Texts())
	}
	clearAndType(t, h, tester, "名称 输入", "本地服务器")
	clearAndType(t, h, tester, "命令 输入", "npx")
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("click 添加: %v", err)
	}
	tester.Frame()
	if rows := parseStoredMCPRows(t, store); len(rows) != 1 || rows[0].Command != "npx" {
		t.Fatalf("stored rows = %+v, want the stdio row", rows)
	}
}
