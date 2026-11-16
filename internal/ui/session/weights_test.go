package session_test

// UD-10 (2026-11 audit): the design system's weight tokens
// (design.WeightRegular/Medium/Semibold) are the single source of font weights.
// designlint only sees direct FontWeight literals — the session screen's
// baselined direct FontWeight(600)存量 are tracked there. This guard closes the
// OTHER half of the UD-10 double-track: a raw weight hidden behind a local
// variable or a package constant (weight := 500; agentHeadingWeight = 600),
// which designlint's numeric-literal rule cannot see. Every weight-named
// identifier must take its value from a design token, never a raw literal, so a
// future weight change is one edit in internal/design.
//
// It reuses the uikit weights_test AST approach, scoped to the indirect
// assignments (the direct literals here are designlint baseline entries).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoIndirectRawFontWeights scans the package's non-test sources: any
// weight-named identifier (local variable or package constant) assigned a raw
// 400/500/600 literal is a UD-10 bypass — it must name design.Weight* instead.
func TestNoIndirectRawFontWeights(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read session dir: %v", err)
	}
	rawWeight := map[string]bool{"400": true, "500": true, "600": true}
	isWeightName := func(name string) bool {
		return strings.Contains(strings.ToLower(name), "weight")
	}
	var problems []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		literalWeight := func(expr ast.Expr) (string, bool) {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.INT || !rawWeight[lit.Value] {
				return "", false
			}
			return lit.Value, true
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok || !isWeightName(ident.Name) || i >= len(x.Rhs) {
						continue
					}
					if value, ok := literalWeight(x.Rhs[i]); ok {
						problems = append(problems, fset.Position(ident.Pos()).String()+": "+ident.Name+" = "+value)
					}
				}
			case *ast.ValueSpec:
				for i, ident := range x.Names {
					if !isWeightName(ident.Name) || i >= len(x.Values) {
						continue
					}
					if value, ok := literalWeight(x.Values[i]); ok {
						problems = append(problems, fset.Position(ident.Pos()).String()+": "+ident.Name+" = "+value)
					}
				}
			}
			return true
		})
	}
	if len(problems) > 0 {
		t.Fatalf("session hides raw font weights behind variables/constants; use design.WeightRegular/Medium/Semibold (UD-10):\n  %s",
			strings.Join(problems, "\n  "))
	}
}
