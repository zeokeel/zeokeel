package design_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/design/conform"
)

// These tests are the reliable half of the design-contract gate
// (docs/DESIGN-LINT.md §5.1: go test conformance is the layer that cannot be
// forgotten). They run in CI and locally on every `go test ./...`.

// repoRoot walks up from the test's working directory (internal/design) to the
// module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above the test directory")
		}
		dir = parent
	}
}

var (
	scanOnce     sync.Once
	scanRoot     string
	scanResult   *conform.Result
	scanBaseline *conform.Baseline
	scanRegistry *design.Registry
	scanErr      error
)

// repoScan scans the module once per test binary.
func repoScan(t *testing.T) (*conform.Result, *conform.Baseline, *design.Registry) {
	t.Helper()
	scanOnce.Do(func() {
		scanRoot = repoRoot(t)
		scanRegistry, scanErr = design.LoadRegistry()
		if scanErr != nil {
			return
		}
		allow := make([]conform.AllowEntry, 0, len(scanRegistry.Allowlist))
		for _, a := range scanRegistry.AllowEntries() {
			allow = append(allow, conform.AllowEntry{
				ID: a.ID, Paths: a.Paths, Rules: a.Rules, Owner: a.Owner, Reason: a.Reason, Until: a.Until,
			})
		}
		scanResult, scanErr = conform.Scan(conform.ScanOptions{
			Root:      scanRoot,
			Roots:     []string{"internal", "cmd"},
			Allowlist: allow,
		})
		if scanErr != nil {
			return
		}
		scanBaseline, scanErr = conform.LoadBaseline(filepath.Join(scanRoot, ".design-baseline.json"))
	})
	if scanErr != nil {
		t.Fatalf("design contract scan: %v", scanErr)
	}
	return scanResult, scanBaseline, scanRegistry
}

// freshViolations returns the violations not grandfathered by
// .design-baseline.json. It also marks the shared result's Baseline flags.
func freshViolations(t *testing.T) []conform.Violation {
	t.Helper()
	res, baseline, _ := repoScan(t)
	marked := baseline.Mark(res.Violations)
	out := make([]conform.Violation, 0, len(marked))
	for _, v := range marked {
		if v.Allowed {
			continue // registered self-drawn area (DESIGN.md §7.1), not new
		}
		out = append(out, v)
	}
	return out
}

func violationsOfRule(violations []conform.Violation, rule string) []conform.Violation {
	var out []conform.Violation
	for _, v := range violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

func formatViolations(violations []conform.Violation) string {
	var b strings.Builder
	for i, v := range violations {
		if i == 10 {
			fmt.Fprintf(&b, "… and %d more\n", len(violations)-i)
			break
		}
		fmt.Fprintf(&b, "\n  %s:%d:%d: %s [%s] %s\n    fix: %s", v.File, v.Line, v.Column, v.Rule, v.AP, v.Message, v.Fix)
	}
	return b.String()
}

func assertNoNewRule(t *testing.T, rule string) {
	t.Helper()
	fresh := violationsOfRule(freshViolations(t), rule)
	if len(fresh) > 0 {
		t.Fatalf("%s: %d new violation(s) outside .design-baseline.json (new UI code must be clean; stock goes through the design-system owner):%s",
			rule, len(fresh), formatViolations(fresh))
	}
}

// TestDesignRegistryIsValid proves the embedded registry parses and satisfies
// its schema: unique IDs, non-empty entries, known rules, valid time-boxed
// allowlist (DS-08).
func TestDesignRegistryIsValid(t *testing.T) {
	r, err := design.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if r.Version != 1 {
		t.Errorf("version = %d, want 1", r.Version)
	}
	if len(r.Components) < 2 {
		t.Errorf("components = %d, want at least the appendix A entries", len(r.Components))
	}
	if len(r.Scenarios) < 2 {
		t.Errorf("scenarios = %d, want at least the appendix A entries", len(r.Scenarios))
	}
	if len(r.AntiPatterns) < 18 {
		t.Errorf("anti_patterns = %d, want the full AP-01..AP-18 list", len(r.AntiPatterns))
	}
	if len(r.Allowlist) < 9 {
		t.Errorf("allowlist = %d, want the DESIGN.md §7.1 self-drawn areas", len(r.Allowlist))
	}
	// Every rule the lint layer implements must be in the registry's AP map.
	for _, rule := range conform.Rules() {
		if ap := rule.AP; ap != "" {
			if _, ok := r.AntiPatternByID(ap); !ok {
				t.Errorf("rule %s refers to anti-pattern %s, which is not in the registry", rule.ID, ap)
			}
		}
	}
	// Every allowlist entry must carry owner/reason/until exactly as DESIGN.md
	// §7.2 requires.
	for _, a := range r.AllowEntries() {
		if a.Owner == "" || a.Reason == "" || a.Until == "" {
			t.Errorf("allowlist %q: owner/reason/until must all be set (DS-06b); got owner=%q reason=%q until=%q",
				a.ID, a.Owner, a.Reason, a.Until)
		}
		if _, err := time.Parse("2006-01-02", a.Until); err != nil {
			t.Errorf("allowlist %q: until %q is not yyyy-mm-dd", a.ID, a.Until)
		}
	}
}

// TestDesignRegistryMatchesDocs keeps the registry, DESIGN.md and the module
// version in sync (DS-08): the machine-readable contract may not drift from
// the document humans read.
func TestDesignRegistryMatchesDocs(t *testing.T) {
	root := repoRoot(t)
	r, err := design.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}

	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), "github.com/egoist/mygo "+r.Source.Mygo) {
		t.Errorf("registry source.mygo = %q does not match go.mod's mygo requirement", r.Source.Mygo)
	}

	designDoc, err := os.ReadFile(filepath.Join(root, "docs", "DESIGN.md"))
	if err != nil {
		t.Fatalf("read docs/DESIGN.md: %v", err)
	}
	doc := string(designDoc)
	for _, c := range r.Components {
		if !strings.Contains(doc, c.Entry) {
			t.Errorf("component %q entry %q does not appear in docs/DESIGN.md (DS-08: update §1.2/§6.3)", c.ID, c.Entry)
		}
	}
	for _, s := range r.Scenarios {
		if !strings.Contains(doc, s.Entry) {
			t.Errorf("scenario %q entry %q does not appear in docs/DESIGN.md (DS-08: update §2.1)", s.ID, s.Entry)
		}
	}
	for _, ap := range r.AntiPatterns {
		if !strings.Contains(doc, ap.ID) {
			t.Errorf("anti-pattern %q does not appear in docs/DESIGN.md §3.1 (DS-08)", ap.ID)
		}
	}
	for _, a := range r.Allowlist {
		for _, p := range a.Paths {
			abs := filepath.Join(root, filepath.FromSlash(p))
			if _, err := os.Stat(abs); err != nil {
				t.Errorf("allowlist %q path %q does not exist (DS-08): %v", a.ID, p, err)
			}
		}
	}
}

// TestDesignRegistryValidation rejects malformed registries, especially
// allowlist entries missing owner/reason/until or with an expired deadline
// (DS-06b). It exercises ValidateAt with a fixed clock so the test does not
// depend on the wall date.
func TestDesignRegistryValidation(t *testing.T) {
	const validAllow = `{"id":"a1","paths":["internal/ui"],"rules":["DS-06"],"what":"w","owner":"o","reason":"r","until":"2027-06-30"}`
	registry := func(version int, allow string) string {
		return fmt.Sprintf(`{
			"version": %d,
			"source": {"mygo": "v0.9.0", "entries": "internal/uikit"},
			"components": [{"id": "c1", "entry": "uikit.X", "package": "internal/uikit"}],
			"scenarios": [{"id": "s1", "entry": "uikit.Y", "forbid": ["Card"]}],
			"anti_patterns": [{"id": "AP-01", "rule": "DS-01", "message": "m"}],
			"allowlist": [%s]
		}`, version, allow)
	}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{name: "valid", doc: registry(1, validAllow)},
		{
			name:    "bad version",
			doc:     registry(2, validAllow),
			wantErr: "version = 2",
		},
		{
			name:    "missing owner",
			doc:     registry(1, `{"id":"a1","paths":["internal/ui"],"what":"w","reason":"r","until":"2027-06-30"}`),
			wantErr: "owner is empty",
		},
		{
			name:    "missing reason",
			doc:     registry(1, `{"id":"a1","paths":["internal/ui"],"what":"w","owner":"o","until":"2027-06-30"}`),
			wantErr: "reason is empty",
		},
		{
			name:    "missing until",
			doc:     registry(1, `{"id":"a1","paths":["internal/ui"],"what":"w","owner":"o","reason":"r"}`),
			wantErr: "until is empty",
		},
		{
			name:    "malformed until",
			doc:     registry(1, `{"id":"a1","paths":["internal/ui"],"what":"w","owner":"o","reason":"r","until":"30/06/2027"}`),
			wantErr: "not yyyy-mm-dd",
		},
		{
			name:    "expired until",
			doc:     registry(1, `{"id":"a1","paths":["internal/ui"],"what":"w","owner":"o","reason":"r","until":"2026-06-30"}`),
			wantErr: "expired",
		},
		{
			name:    "absolute path",
			doc:     registry(1, `{"id":"a1","paths":["/data/mygo/internal/ui"],"what":"w","owner":"o","reason":"r","until":"2027-06-30"}`),
			wantErr: "repo-relative",
		},
		{
			name:    "unknown rule",
			doc:     registry(1, `{"id":"a1","paths":["internal/ui"],"rules":["DS-99"],"what":"w","owner":"o","reason":"r","until":"2027-06-30"}`),
			wantErr: "unknown rule",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := design.ParseRegistry([]byte(tt.doc))
			if err != nil {
				t.Fatalf("ParseRegistry: %v", err)
			}
			err = parsed.ValidateAt(now)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAt: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateAt: want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateAt error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}

	// Unknown JSON fields are a schema typo, not silently ignored.
	if _, err := design.ParseRegistry([]byte(registry(1, validAllow))); err != nil {
		t.Fatalf("parse valid registry: %v", err)
	}
	bad := strings.Replace(registry(1, validAllow), `"version"`, `"veto"`, 1)
	if _, err := design.ParseRegistry([]byte(bad)); err == nil {
		t.Fatal("ParseRegistry accepted an unknown field; want a decode error")
	}
}

// TestNoLiteralColors is DS-01 as a test: no new color literal in the UI.
func TestNoLiteralColors(t *testing.T) { assertNoNewRule(t, "DS-01") }

// TestNoRawMetricLiterals is DS-03 as a test: no new raw numeric metric.
func TestNoRawMetricLiterals(t *testing.T) { assertNoNewRule(t, "DS-03") }

// TestNoEmojiOrHandwrittenIcons is DS-07 as a test.
func TestNoEmojiOrHandwrittenIcons(t *testing.T) { assertNoNewRule(t, "DS-07") }

// TestDialogFooterHasNoDangerDefault is DS-10 as a test: a destructive action
// must never be the Enter default (AP-12).
func TestDialogFooterHasNoDangerDefault(t *testing.T) { assertNoNewRule(t, "DS-10") }

// TestNoNewUnbaselinedViolations is the general gate: every violation the scan
// reports must either be clean or grandfathered in .design-baseline.json. This
// is what makes "new code is zero-tolerance" true even for rules whose stock
// is not cleaned up yet.
func TestNoNewUnbaselinedViolations(t *testing.T) {
	if fresh := freshViolations(t); len(fresh) > 0 {
		t.Fatalf("%d violation(s) outside .design-baseline.json; fix them (the baseline may only shrink, DESIGN-LINT §4.2):%s",
			len(fresh), formatViolations(fresh))
	}
}

// TestDesignBaselineIsValid keeps the stock ledger well-formed and time-boxed.
func TestDesignBaselineIsValid(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".design-baseline.json"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	var doc struct {
		Version int `json:"version"`
		Ignored []struct {
			Rule    string `json:"rule"`
			File    string `json:"file"`
			Text    string `json:"text"`
			Owner   string `json:"owner"`
			Expires string `json:"expires"`
		} `json:"ignored"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	if doc.Version != 1 {
		t.Errorf("baseline version = %d, want 1", doc.Version)
	}
	known := map[string]bool{}
	for _, rule := range conform.Rules() {
		known[rule.ID] = true
	}
	for i, e := range doc.Ignored {
		if !known[e.Rule] {
			t.Errorf("baseline[%d]: unknown rule %q", i, e.Rule)
		}
		if e.File == "" || e.Text == "" {
			t.Errorf("baseline[%d]: file/text must be set (matching identity is rule+file+text)", i)
		}
		if e.Owner == "" {
			t.Errorf("baseline[%d] (%s %s): owner is empty (DESIGN-LINT §4.2)", i, e.Rule, e.File)
		}
		if _, err := time.Parse("2006-01-02", e.Expires); err != nil {
			t.Errorf("baseline[%d] (%s %s): expires %q is not yyyy-mm-dd", i, e.Rule, e.File, e.Expires)
		}
	}
}

// TestNoInventedOfficialAPI is DS-08's anti-fabrication check: the source must
// not call the mygo APIs that do not exist in v0.9.0 (docs/DESIGN.md §7.4,
// AP-14). The application-level uikit.DialogFooter is fine; ui.DialogFooter
// is not.
func TestNoInventedOfficialAPI(t *testing.T) {
	root := repoRoot(t)
	// Selector names under github.com/egoist/mygo/ui that do not exist in
	// v0.9.0 (docs/DESIGN.md §7.4 lists the common mistakes).
	forbiddenSelectors := map[string]bool{
		"DialogFooter":   true,
		"Markdown":       true,
		"DatePicker":     true,
		"CommandPalette": true,
		"SearchDialog":   true,
		"Dialog":         true,
		"Section":        true,
		"PageHeader":     true,
	}
	problems := scanMygoUISelectors(t, root, forbiddenSelectors)
	if len(problems) > 0 {
		t.Fatalf("source uses APIs that mygo v0.9.0 does not have (DESIGN.md §7.4 / AP-14):\n  %s", strings.Join(problems, "\n  "))
	}
	// docs/DESIGN.md §7.4 must keep documenting every name we police, so the
	// check and the document cannot drift.
	designDoc, err := os.ReadFile(filepath.Join(root, "docs", "DESIGN.md"))
	if err != nil {
		t.Fatalf("read docs/DESIGN.md: %v", err)
	}
	for _, want := range []string{"ui.DialogFooter", "ui.Markdown", "ui.DatePicker", "ui.CommandPalette", "ui.Dialog", "ui.Section", "PageHeader", "Button.Default"} {
		if !strings.Contains(string(designDoc), want) {
			t.Errorf("docs/DESIGN.md §7.4 no longer documents %s, but the conformance test polices it", want)
		}
	}
}

// scanMygoUISelectors reports forbidden selectors on the mygo ui package plus
// method calls like ui.Button(...).Default() that name a non-existent element
// API.
func scanMygoUISelectors(t *testing.T, root string, forbidden map[string]bool) []string {
	t.Helper()
	var problems []string
	trim := root + string(filepath.Separator)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "vendor" || name == "testdata" || name == "node_modules" || name == "dist" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.ToSlash(strings.TrimPrefix(path, trim)), src, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		uiAlias := map[string]bool{}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil || p != "github.com/egoist/mygo/ui" {
				continue
			}
			name := "ui"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			uiAlias[name] = true
		}
		if len(uiAlias) == 0 {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, trim))
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && uiAlias[id.Name] && forbidden[x.Sel.Name] {
					problems = append(problems, fmt.Sprintf("%s:%d: ui.%s does not exist in mygo v0.9.0", rel, fset.Position(x.Pos()).Line, x.Sel.Name))
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Default" || len(x.Args) != 0 {
					return true
				}
				if usesMygoUIChain(sel.X, uiAlias) {
					problems = append(problems, fmt.Sprintf("%s:%d: .Default() does not exist on mygo ui elements (DESIGN.md §7.4)", rel, fset.Position(sel.Pos()).Line))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan for fabricated APIs: %v", err)
	}
	return problems
}

// usesMygoUIChain reports whether expr is (a chain over) a call into the mygo
// ui package: ui.Button(c).Default() has Fun ui.Button and must be caught.
func usesMygoUIChain(expr ast.Expr, uiAlias map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && uiAlias[id.Name] {
				return true
			}
		}
		return usesMygoUIChain(e.Fun, uiAlias)
	case *ast.SelectorExpr:
		return usesMygoUIChain(e.X, uiAlias)
	}
	return false
}
