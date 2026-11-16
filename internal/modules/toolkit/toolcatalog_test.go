package toolkit

// Narrow tool-catalog provider (2026-11 audit AR-06): diagnostics consumers
// need only WHICH tools exist, not the concrete rig registry. The toolkit
// provides contracts.KeyToolCatalog over the same live registry as the legacy
// KeyToolkit, so Names/Len must always agree with Registry.Specs — including
// after an MCP hot reload changes the tool set.

import (
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestToolCatalogMatchesRegistrySpecs(t *testing.T) {
	alpha := testMCPToolListServer(t, []map[string]any{oneTool("alpha_tool")})
	defer alpha.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"alpha","url":"` + alpha.URL + `"}]`,
	})
	root, registry, _, reloader := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)

	catalog, err := root.Require(contracts.KeyToolCatalog)
	if err != nil {
		t.Fatalf("require tool catalog: %v", err)
	}
	assertCatalogMatchesSpecs(t, catalog, registry.Specs())
	if catalog.Len() != registry.Len() {
		t.Fatalf("Len() = %d, want registry.Len() = %d", catalog.Len(), registry.Len())
	}
	if !catalogHasName(catalog, "mcp__alpha__alpha_tool") {
		t.Fatal("catalog misses the MCP tool registered at activation")
	}

	// The returned slice is a copy the caller may mutate.
	names := catalog.Names()
	if len(names) == 0 {
		t.Fatal("catalog is empty")
	}
	names[0] = "mutated-by-caller"
	if catalog.Names()[0] == "mutated-by-caller" {
		t.Fatal("Names() leaked its backing slice to the caller")
	}

	// Live view: a hot reload that adds a server must show up without restart.
	beta := testMCPToolListServer(t, []map[string]any{oneTool("beta_tool")})
	defer beta.Close()
	if err := settings.Set(contracts.SettingMCPServers,
		`[{"name":"alpha","url":"`+alpha.URL+`"},{"name":"beta","url":"`+beta.URL+`"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := reloader.Reload(nil); err != nil {
		t.Fatalf("reload: %v", err)
	}
	assertCatalogMatchesSpecs(t, catalog, registry.Specs())
	if !catalogHasName(catalog, "mcp__beta__beta_tool") {
		t.Fatal("catalog did not follow the hot reload (snapshot instead of live view)")
	}
}

func assertCatalogMatchesSpecs(t *testing.T, catalog contracts.ToolCatalog, specs []rig.ToolSpec) {
	t.Helper()
	names := catalog.Names()
	if len(names) != len(specs) {
		t.Fatalf("Names() has %d entries, want %d (Specs)", len(names), len(specs))
	}
	for index, spec := range specs {
		if names[index] != spec.Name {
			t.Fatalf("Names()[%d] = %q, want %q (Specs order)", index, names[index], spec.Name)
		}
	}
	if catalog.Len() != len(specs) {
		t.Fatalf("Len() = %d, want %d", catalog.Len(), len(specs))
	}
}

func catalogHasName(catalog contracts.ToolCatalog, name string) bool {
	for _, got := range catalog.Names() {
		if got == name {
			return true
		}
	}
	return false
}
