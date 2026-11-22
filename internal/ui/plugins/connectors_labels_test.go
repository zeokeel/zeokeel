package plugins_test

// Duplicate connector names must stay distinguishable in the Connectors tab
// without rewriting the stored config name (the token key and enable switches
// depend on it).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestConnectorDuplicateNamesGetDisambiguatedLabels(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: `[
			{"name":"dup","url":"http://127.0.0.1:9001/mcp"},
			{"name":"dup","url":"http://127.0.0.1:9002/mcp"},
			{"name":"other","url":"http://127.0.0.1:9003/mcp"}
		]`,
	})
	tester := h.tester()
	h.click(tester, "连接器")

	// The first duplicate keeps the plain name; later duplicates are suffixed.
	h.assertTexts(tester,
		"dup", "dup · #2", "other",
		"连接器操作：dup", "连接器操作：dup · #2", "连接器操作：other",
		"启用连接器 dup", "启用连接器 dup · #2", "启用连接器 other",
	)
	h.assertNoTexts(tester, "other · #2")

	// Only display labels changed: the stored config rows still carry "dup".
	rows := storedConnectorRows(t, store)
	if len(rows) != 3 || rows[0].Name != "dup" || rows[1].Name != "dup" || rows[2].Name != "other" {
		t.Fatalf("stored rows = %+v, want unchanged duplicate config names", rows)
	}

	// The switch continues to address the config/token key "dup", not the
	// disambiguated label.
	h.click(tester, "启用连接器 dup · #2")
	if got := store.get(contracts.SettingMCPDisabledServers); got != `["dup"]` {
		t.Fatalf("disabled servers = %q, want [\"dup\"]", got)
	}
	rows = storedConnectorRows(t, store)
	if len(rows) != 3 || rows[0].Name != "dup" || rows[1].Name != "dup" {
		t.Fatalf("stored rows after switch = %+v", rows)
	}
}
