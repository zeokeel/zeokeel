package contracts

// v0.2.50 removal pin (user ruling): loop protection and every tool-loop user
// configuration surface are gone. The settings key table must not define any
// agent.loop* budget key nor the chat.maxIterations user knob — the iteration
// cap is assembly-level product definition (agent.Config.MaxIterations), not a
// settings row. MessageErrorKindLoop stays so old persisted run_end events still
// restore with a classified (historical, neutral) error kind.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNoToolLoopUserSettingsKeys scans the contracts key table (every non-test
// source file) for string literals defining a removed user key. Comments may
// still name the history; a literal key would resurrect the configuration
// surface, so it fails the build's test gate.
func TestNoToolLoopUserSettingsKeys(t *testing.T) {
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
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if strings.HasPrefix(value, "agent.loop") || value == "chat.maxIterations" {
				t.Errorf("%s defines the removed tool-loop user key %q: loop protection and its configuration surface are gone (v0.2.50)", name, value)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no contracts source files parsed")
	}
}

// TestClassifyErrorKeepsLegacyLoopKind pins the history compatibility: a durable
// run_end error persisted by an older version's loop breaker still classifies as
// kind loop, so a restored session renders the neutral historical note instead
// of a generic failure.
func TestClassifyErrorKeepsLegacyLoopKind(t *testing.T) {
	for _, errText := range []string{
		"loop guard: wall clock budget exceeded",
		"loop guard: token budget exceeded (5000000/4000000)",
		"loop guard: identical tool call repeated 5 times: echo",
		"loop guard: no file diff/mutation after 40 calls",
		"loop guard: task budget exceeded",
	} {
		if got := ClassifyError(nil, errText); got != MessageErrorKindLoop {
			t.Fatalf("ClassifyError(%q) = %q, want the legacy kind %q", errText, got, MessageErrorKindLoop)
		}
	}
}
