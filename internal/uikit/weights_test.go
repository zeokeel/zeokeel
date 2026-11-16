package uikit_test

// UD-10 (2026-11 audit): the design system's weight tokens
// (design.WeightRegular/Medium/Semibold) are the single source of font weights.
// designlint only sees direct FontWeight literals, and the Markdown theme's
// weight fields are plain assignments it does not monitor at all — so this
// package-level guard pins that uikit itself never writes a raw weight,
// whether through FontWeight(...) or through a theme field assignment.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// TestMarkdownThemeWeightsKeepTheirValues pins that the tokenization did not
// change the rendered weights: links stay medium, headings stay semibold in
// all three Markdown themes (the guard below forbids the raw spellings).
func TestMarkdownThemeWeightsKeepTheirValues(t *testing.T) {
	tokens := design.Light()
	chat := uikit.ChatMarkdown(tokens, 0)
	if chat.Theme.LinkWeight != design.WeightMedium {
		t.Fatalf("chat link weight = %d, want %d", chat.Theme.LinkWeight, design.WeightMedium)
	}
	themes := map[string]*markdown.Options{
		"chat":      chat,
		"reasoning": uikit.ReasoningMarkdown(tokens, 0),
		"doc":       uikit.MarkdownDoc(tokens, 0),
	}
	for name, options := range themes {
		for index, heading := range options.Theme.Headings {
			if heading.Weight != design.WeightSemibold {
				t.Fatalf("%s heading %d weight = %d, want %d", name, index+1, heading.Weight, design.WeightSemibold)
			}
		}
	}
}

// TestNoRawFontWeightsInUIKit scans the package's non-test sources: every
// FontWeight argument and every weight-field assignment must name a design
// token, not a numeric literal (the values 400/500/600 are legal, but the
// token is the contract — a future weight change must be one edit).
func TestNoRawFontWeightsInUIKit(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read uikit dir: %v", err)
	}
	weightFields := map[string]bool{"Weight": true, "LinkWeight": true, "StrongWeight": true}
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
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "FontWeight" {
					return true
				}
				for _, arg := range x.Args {
					if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.INT {
						problems = append(problems, fset.Position(lit.Pos()).String()+": FontWeight("+lit.Value+")")
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || !weightFields[sel.Sel.Name] || i >= len(x.Rhs) {
						continue
					}
					if lit, ok := x.Rhs[i].(*ast.BasicLit); ok && (lit.Kind == token.INT || lit.Kind == token.FLOAT) {
						problems = append(problems, fset.Position(lit.Pos()).String()+": "+sel.Sel.Name+" = "+lit.Value)
					}
				}
			}
			return true
		})
	}
	if len(problems) > 0 {
		t.Fatalf("uikit writes raw font weights; use design.WeightRegular/Medium/Semibold (UD-10):\n  %s",
			strings.Join(problems, "\n  "))
	}
}
