package contracts

// AR-06 guard (2026-11 audit): the contract surface must not import the Rig
// tool framework (github.com/zeokeel/zeokeel-rig/tools and its subpackages).
// The concrete *tools.Registry service key lives in the shared backend seam
// internal/hosttools (KeyRegistry) for modules that register/invoke tools;
// UI and diagnostics consumers read the narrow rig-free KeyToolCatalog. The
// rig root/sandbox data types used by the store/memory/skills read models are
// a separate sanctioned surface and are not covered by this guard.

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

const rigToolsImport = "github.com/zeokeel/zeokeel-rig/tools"

func TestContractsFreeOfToolRegistryImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read contracts dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == rigToolsImport || strings.HasPrefix(path, rigToolsImport+"/") {
				t.Errorf("%s imports %q: contracts must stay free of the Rig tool framework (AR-06); the concrete registry key is internal/hosttools.KeyRegistry, diagnostics read KeyToolCatalog", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no contracts source files parsed")
	}
}
