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
	for _, want := range []string{"插件", "MCP 插件", "还没有配置 MCP 服务器", "工具目录", "bash"} {
		if !tester.HasText(want) {
			t.Fatalf("plugins frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The catalogue is a virtualized native List: a tool deep in the list is
	// reached through the search rather than built up front.
	searchRect, ok := tester.Find("搜索工具")
	if !ok {
		t.Fatalf("tool search field not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(searchRect.X+searchRect.W/2, searchRect.Y+searchRect.H/2)
	tester.Frame()
	for _, name := range []string{"read_file", "memory", "schedule_create", "skill"} {
		tester.Key(ui.Cmd, ui.KeyA)
		tester.Key(0, ui.KeyBackspace)
		tester.Frame()
		tester.Type(name)
		tester.Frame()
		if !tester.HasText(name) {
			t.Fatalf("search %q did not show the tool; texts = %v", name, tester.Texts())
		}
	}
	// The search filters both name and description: a name search hides the
	// other tools (the previous case narrowed to memory tools).
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
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
