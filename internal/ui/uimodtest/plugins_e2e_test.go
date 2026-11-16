package uimodtest

// Capabilities screen end-to-end: the desktop assembly mounts the two parallel
// capability tabs (skills, connectors) on the built-in module inventory and the
// plugin settings. The tool catalogue is no longer a capability surface.

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
	// The page skeleton: title and the two parallel tabs. The skills tab is the
	// first page; the assembly provides the skills editor, so 新建技能 is there.
	for _, want := range []string{"能力", "技能", "连接器", "新建技能"} {
		if !tester.HasText(want) {
			t.Fatalf("capabilities frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("工具目录") {
		t.Fatalf("the capability page still renders the removed tool catalogue; texts = %v", tester.Texts())
	}
	// The connectors tab is a full page of its own, with its own create action.
	if err := tester.Click("连接器"); err != nil {
		t.Fatalf("open connectors tab: %v", err)
	}
	tester.Frame()
	for _, want := range []string{"连接器", "添加连接器"} {
		if !tester.HasText(want) {
			t.Fatalf("connectors tab missing %q; texts = %v", want, tester.Texts())
		}
	}

}
