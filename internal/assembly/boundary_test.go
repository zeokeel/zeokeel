package assembly_test

// Import-boundary assertions for the browser-first product (S6 of the native-UI
// retirement; the S0 rules these replace live in the pre-s4 tag).
//
// The rules, in the same order the tests assert them:
//
//  1. internal/contracts is the leaf shared surface: it imports no other
//     internal package and no engine.
//  2. Backend modules may import contracts, design, lib/* and the shared
//     libraries; never another module implementation, never host or assembly.
//  3. host must not reach into internal/modules/* directly.
//  4. The native UI engine (github.com/egoist/mygo) survives only in the shell:
//     internal/host (tray/hotkey/OS notifications), internal/lib/openurl
//     (OS launchers) and the two asset layers the web generator parses
//     (internal/brand, internal/design). Everything the browser talks to —
//     gateway, contracts, modules — must stay engine-free, proven both directly
//     and through the transitive dependency graph.
//
// Test dependencies are listed separately on purpose: internal/gateway's e2e
// tests load the real backend modules, so `go list -deps -test` there contains
// internal/modules/* and that is intended — the e2e only has value against the
// real modules. The rule that still holds for tests is the engine one: a test
// dependency must not drag mygo in either.

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/zeokeel/zeokeel/"

// engineModulePath is the native UI engine (github.com/egoist/mygo). The tray
// shell still uses it; nothing else may.
const engineModulePath = "github.com/egoist/mygo"

// engineAllowlist is the complete set of packages allowed to import the engine.
// It is a *decision*, not an observation: adding a package here means the
// product now carries a native dependency again (docs/plans/s4-execution-checklist.md).
var engineAllowlist = map[string]bool{
	"internal/host":        true, // tray icon, global hotkey, OS notifications
	"internal/lib/openurl": true, // open the browser client / OAuth URLs
	"internal/brand":       true, // palette + logo colors as mygo colors
	"internal/design":      true, // design tokens as mygo colors
}

// boundaryPkg is one package row of `go list`.
type boundaryPkg struct {
	path     string   // full import path
	internal []string // repo-relative internal imports
	external []string // non-internal imports
	// testExternal are the imports of the package's _test.go files. Go's
	// `.Imports` does not include them, which is how a `_test.go` could import
	// the native engine with every boundary green (AUDIT-2026-11-24 B-10).
	testExternal []string
	goFiles      int // number of non-test Go files
}

// listProductPackages runs `go list` over the product tree from the module
// root and parses the import rows.
func listProductPackages(t *testing.T) []boundaryPkg {
	t.Helper()
	rows := goList(t,
		"{{.ImportPath}}|{{join .Imports \",\"}}|{{join .TestImports \",\"}}|{{len .GoFiles}}",
		"./internal/...", "./cmd/...")
	var pkgs []boundaryPkg
	for _, line := range rows {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			t.Fatalf("go list row %q: want path|imports|testimports|gofiles", line)
		}
		p := boundaryPkg{path: parts[0]}
		for _, imp := range strings.Split(parts[1], ",") {
			if strings.HasPrefix(imp, modulePath) {
				p.internal = append(p.internal, strings.TrimPrefix(imp, modulePath))
				continue
			}
			p.external = append(p.external, imp)
		}
		for _, imp := range strings.Split(parts[2], ",") {
			if imp != "" && !strings.HasPrefix(imp, modulePath) {
				p.testExternal = append(p.testExternal, imp)
			}
		}
		n, err := strconv.Atoi(parts[3])
		if err != nil {
			t.Fatalf("go list row %q: gofiles %q: %v", line, parts[3], err)
		}
		p.goFiles = n
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages")
	}
	return pkgs
}

// backendModuleAllowed is the allowlist for internal/modules/*: contracts,
// design, lib/* and the shared backend libraries (docs/ARCHITECTURE.md §3).
func backendModuleAllowed(imp string) bool {
	switch imp {
	case "internal/contracts", "internal/design", "internal/hosttools", "internal/websearch", "internal/memorytools":
		return true
	}
	return strings.HasPrefix(imp, "internal/lib/")
}

// TestImportBoundaries asserts the module boundaries over the whole product
// tree. A violation names the package, the forbidden import and the rule, so the
// fix is mechanical.
func TestImportBoundaries(t *testing.T) {
	for _, p := range listProductPackages(t) {
		rel := strings.TrimPrefix(p.path, modulePath)
		if p.goFiles == 0 {
			continue // test-only harness package: not product code
		}
		for _, imp := range p.internal {
			var reason string
			switch {
			case strings.HasPrefix(rel, "internal/modules/"):
				if !backendModuleAllowed(imp) {
					reason = "backend modules may only import contracts/design/lib/* and the shared libraries; no other module implementation, no host/assembly"
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

// TestEngineIsQuarantinedToTheShell is rule 4's direct form: a package either
// imports the native engine and is on the allowlist, or it does not.
func TestEngineIsQuarantinedToTheShell(t *testing.T) {
	pkgs := listProductPackages(t)
	importers := make(map[string]bool, len(engineAllowlist))
	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.path, modulePath)
		importsEngine := false
		for _, imp := range p.external {
			if imp == engineModulePath || strings.HasPrefix(imp, engineModulePath+"/") {
				importsEngine = true
				break
			}
		}
		if !importsEngine {
			continue
		}
		if !engineAllowlist[rel] {
			t.Errorf("%s imports %s: the native engine is quarantined to %v (docs/plans/s4-execution-checklist.md S6)",
				rel, engineModulePath, allowedEnginePackages())
			continue
		}
		importers[rel] = true
	}
	if len(importers) == 0 {
		t.Fatalf("no package imports %s: the quarantine assertion would pass vacuously", engineModulePath)
	}
	// Every allowlisted package must still be a real importer, otherwise the
	// allowlist has gone stale (a package that dropped mygo belongs off it).
	for name := range engineAllowlist {
		if !importers[name] {
			t.Errorf("allowlist entry %q no longer imports %s; drop it from the allowlist", name, engineModulePath)
		}
	}
}

// TestEngineIsQuarantinedToTheShellInTests closes the `_test.go` hole: the main
// scan reads `.Imports`, which Go does not fill with test-only imports, so a
// test file anywhere could import the engine with every boundary green
// (AUDIT-2026-11-24 B-10). The allowlist is the same one: the shell and the
// asset packages may use the engine in their tests too (they own it), nothing
// else may.
func TestEngineIsQuarantinedToTheShellInTests(t *testing.T) {
	seen := 0
	for _, p := range listProductPackages(t) {
		rel := strings.TrimPrefix(p.path, modulePath)
		for _, imp := range p.testExternal {
			if imp != engineModulePath && !strings.HasPrefix(imp, engineModulePath+"/") {
				continue
			}
			seen++
			if !engineAllowlist[rel] {
				t.Errorf("%s (test files) imports %s: the native engine is quarantined to %v (S6)",
					rel, imp, allowedEnginePackages())
			}
		}
	}
	if seen == 0 {
		t.Errorf("no test file imports %s: the quarantine assertion would pass vacuously", engineModulePath)
	}
}

// allowedEnginePackages renders the allowlist for an error message.
func allowedEnginePackages() []string {
	names := make([]string, 0, len(engineAllowlist))
	for name := range engineAllowlist {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestContractsDependsOnNoEngine keeps the S0 invariant: a transport can depend
// on the contracts without dragging the engine into its binary.
func TestContractsDependsOnNoEngine(t *testing.T) {
	assertNoEngine(t, "./internal/contracts")
}

// TestBackendIsTransitivelyEngineFree is rule 4's transitive form: the browser
// transport and every backend module must have no mygo anywhere in their
// dependency graph, not merely no direct import.
func TestBackendIsTransitivelyEngineFree(t *testing.T) {
	for _, pattern := range []string{"./internal/gateway", "./internal/contracts", "./internal/modules/..."} {
		assertNoEngine(t, pattern)
	}
}

// assertNoEngine fails when the dependency graph below one package contains the
// native engine.
func assertNoEngine(t *testing.T, pattern string) {
	t.Helper()
	deps := goList(t, "{{.ImportPath}}", "-deps", pattern)
	if len(deps) == 0 {
		t.Fatalf("go list -deps %s returned no packages", pattern)
	}
	for _, dep := range deps {
		if dep == engineModulePath || strings.HasPrefix(dep, engineModulePath+"/") {
			t.Errorf("%s depends on %s: the browser transport and the backend must stay engine-free (S6 rule 4)", pattern, dep)
		}
	}
}

// TestTestDependenciesStayEngineFree documents and enforces the test tier.
//
// internal/gateway's e2e tests deliberately load the real backend modules, so
// its `go list -deps -test` graph contains internal/modules/* — that is the
// point of the e2e and is NOT a boundary violation. What must still hold is the
// engine rule: no test dependency may pull mygo into the test binary.
func TestTestDependenciesStayEngineFree(t *testing.T) {
	deps := goList(t, "{{.ImportPath}}", "-deps", "-test", "./internal/gateway")
	if len(deps) == 0 {
		t.Fatal("go list -deps -test ./internal/gateway returned no packages")
	}
	sawModule := false
	for _, dep := range deps {
		if dep == engineModulePath || strings.HasPrefix(dep, engineModulePath+"/") {
			t.Errorf("the gateway test binary depends on %s: e2e may load internal/modules/*, but never the native engine", dep)
		}
		if strings.HasPrefix(dep, modulePath+"internal/modules/") {
			sawModule = true
		}
	}
	if !sawModule {
		t.Error("the gateway e2e test graph no longer contains internal/modules/*: if the e2e stopped using the real modules, say so explicitly here")
	}
}

// moduleRoot resolves the repository root from this test's package directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	return root
}

// goList runs one go list invocation from the module root and returns its
// non-empty output lines.
func goList(t *testing.T, format string, patterns ...string) []string {
	t.Helper()
	// No `-e`: a package that fails to load must fail the assertion, not vanish
	// from it (AUDIT-2026-11-24 B-10: `-e` printed nothing for a broken package
	// and every boundary kept passing).
	args := append([]string{"list", "-f", format}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list %v: %v\n%s", patterns, err, exitErr.Stderr)
		}
		t.Fatalf("go list %v: %v", patterns, err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
