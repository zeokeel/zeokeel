package assembly_test

// Import-boundary assertions (2026-11 audit AR-07/AR-08/AR-22). The module
// boundaries in AGENTS.md §1 / docs/ARCHITECTURE.md §3 were human-reviewed only
// until this test: docs/ARCHITECTURE.md claimed "CI 用 go list 断言" while no
// such assertion existed. This test is that assertion — it shells out to
// `go list` over the product packages and checks every internal import against
// the allowlist ruling (AR-07, lead decision):
//
//   - UI modules (internal/ui/*) may import only contracts/uikit/design/icons/
//     brand/lib/* plus third-party modules. brand is open to the UI layer for
//     the logo assets; internal/{hosttools,memorytools,websearch} are shared
//     backend libraries, not module implementations.
//   - Backend modules (internal/modules/*) may import contracts, uikit,
//     design, lib/* and the shared libraries (docs/ARCHITECTURE.md §3), never
//     another module implementation, never the UI modules/uiroot/host/
//     assembly. (approvals localizes its notifications through uikit.Language/
//     ToolDisplayName — the shared composition layer is below both siblings.)
//   - host must not reach into internal/modules/* directly (AR-22: the one
//     such edge, host→modules/update, was removed by moving CompareVersions
//     into internal/lib/version).
//   - uikit stays below the UI modules; uiroot (the shell) never imports a UI
//     module implementation; contracts is the leaf shared surface.
//
// Test-only packages (no non-test Go files, e.g. internal/ui/uimodtest and
// internal/regression) are integration harnesses: their test imports are
// deliberately wider and are not product code, so the product rules skip them.

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/zeokeel/zeokeel/"

// boundaryPkg is one package row of `go list`.
type boundaryPkg struct {
	path     string   // full import path
	internal []string // repo-relative internal imports
	goFiles  int      // number of non-test Go files
}

// listProductPackages runs `go list` over the product tree from the module
// root and parses the import rows.
func listProductPackages(t *testing.T) []boundaryPkg {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	cmd := exec.Command("go", "list", "-f",
		"{{.ImportPath}}|{{join .Imports \",\"}}|{{len .GoFiles}}",
		"./internal/...", "./cmd/...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}
	var pkgs []boundaryPkg
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			t.Fatalf("go list row %q: want path|imports|gofiles", line)
		}
		p := boundaryPkg{path: parts[0]}
		for _, imp := range strings.Split(parts[1], ",") {
			if strings.HasPrefix(imp, modulePath) {
				p.internal = append(p.internal, strings.TrimPrefix(imp, modulePath))
			}
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			t.Fatalf("go list row %q: gofiles %q: %v", line, parts[2], err)
		}
		p.goFiles = n
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages")
	}
	return pkgs
}

// uiModuleAllowed is the AR-07 allowlist for internal/ui/* product imports.
func uiModuleAllowed(imp string) bool {
	switch imp {
	case "internal/contracts", "internal/uikit", "internal/design", "internal/icons", "internal/brand":
		return true
	}
	return strings.HasPrefix(imp, "internal/lib/")
}

// backendModuleAllowed is the allowlist for internal/modules/*: contracts,
// uikit/design (docs/ARCHITECTURE.md §3), lib/* and the three shared backend
// libraries (AR-07 ruling).
func backendModuleAllowed(imp string) bool {
	switch imp {
	case "internal/contracts", "internal/uikit", "internal/design",
		"internal/hosttools", "internal/memorytools", "internal/websearch":
		return true
	}
	return strings.HasPrefix(imp, "internal/lib/")
}

// TestImportBoundaries asserts the module-boundary allowlist over the whole
// product tree. A violation names the package, the forbidden import and the
// rule it breaks, so the fix is mechanical.
func TestImportBoundaries(t *testing.T) {
	for _, p := range listProductPackages(t) {
		rel := strings.TrimPrefix(p.path, modulePath)
		if p.goFiles == 0 {
			continue // test-only harness package: not product code
		}
		for _, imp := range p.internal {
			var reason string
			switch {
			case strings.HasPrefix(rel, "internal/ui/"):
				if !uiModuleAllowed(imp) {
					reason = "UI modules may only import contracts/uikit/design/icons/brand/lib/* (AGENTS.md §1, AR-07 allowlist)"
				}
			case rel == "internal/uikit":
				switch {
				case imp == "internal/contracts" || imp == "internal/design" || imp == "internal/icons":
				case strings.HasPrefix(imp, "internal/lib/"):
				default:
					reason = "uikit is the shared composition layer: it must not depend on UI modules/uiroot/host/modules"
				}
			case rel == "internal/uiroot":
				switch {
				case imp == "internal/contracts" || imp == "internal/uikit" || imp == "internal/design" || imp == "internal/icons" || imp == "internal/brand":
				case strings.HasPrefix(imp, "internal/lib/"):
				default:
					reason = "the shell (uiroot) must not import UI module implementations, backend modules or host"
				}
			case strings.HasPrefix(rel, "internal/modules/"):
				if !backendModuleAllowed(imp) {
					reason = "backend modules may only import contracts/uikit/design/lib/* and the shared libraries hosttools/memorytools/websearch; no other module implementation, no UI modules/uiroot/host/assembly"
				}
			case rel == "internal/host":
				if strings.HasPrefix(imp, "internal/modules/") {
					reason = "host must not import backend modules directly (AR-22); go through assembly or move shared code into internal/lib"
				}
			case rel == "internal/contracts":
				reason = "contracts is the leaf shared surface: it must not import any other internal package"
			}
			if reason != "" {
				t.Errorf("%s imports %s: %s", rel, imp, reason)
			}
		}
	}
}
