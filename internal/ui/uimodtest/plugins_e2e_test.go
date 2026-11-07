package uimodtest

// Plugins screen end-to-end: the desktop assembly exposes the toolkit's MCP
// plugin service (none configured in tests) and the full tool catalog, which
// the screen lists by category with search.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestPluginsScreenEndToEnd(t *testing.T) {
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), &memSettings{values: map[string]string{}})
	defer func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}()
	plugins, err := root.Require(contracts.KeyPlugins)
	if err != nil {
		t.Fatalf("require plugins: %v", err)
	}
	if servers := plugins.MCP(); len(servers) != 0 {
		t.Fatalf("MCP servers = %+v, want none in tests", servers)
	}
	if err := uiRoot.Navigator().Push("plugins"); err != nil {
		t.Fatalf("Push(plugins): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	for _, want := range []string{"插件", "MCP 插件", "还没有配置 MCP 服务器", "工具目录", "bash", "read_file", "memory", "schedule_create", "skill"} {
		if !tester.HasText(want) {
			t.Fatalf("plugins frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The catalog search filters both name and description.
	searchRect, ok := tester.Find("搜索工具")
	if !ok {
		t.Fatalf("tool search field not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(searchRect.X+searchRect.W/2, searchRect.Y+searchRect.H/2)
	tester.Frame()
	tester.Type("memory")
	tester.Frame()
	if !tester.HasText("memory") {
		t.Fatalf("search did not keep memory tools; texts = %v", tester.Texts())
	}
	if tester.HasText("bash") {
		t.Fatalf("search did not filter out bash; texts = %v", tester.Texts())
	}
}
