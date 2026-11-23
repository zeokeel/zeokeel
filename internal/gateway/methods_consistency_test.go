package gateway

// Protocol/implementation consistency (task-14).
//
// The failure this prevents happened twice in one migration: docs/TRANSPORT.md
// §5.1 declared `session.create` and `settings.set`, the gateway never dispatched
// them, the browser called them, and nothing went red until a human compared the
// two by hand. This test makes the comparison mechanical: the document's method
// table and the gateway's dispatch table must agree, with one explicit escape
// hatch — a row whose 状态 column says 延后 (deferred) or that is written
// ~~struck through~~ (removed) is documented-but-intentionally-absent.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// documentedMethod is one row of a §5.1 table.
type documentedMethod struct {
	Name   string
	Status string
}

// transportMethods parses docs/TRANSPORT.md §5.1.
func transportMethods(t *testing.T) map[string]documentedMethod {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "TRANSPORT.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	methods := map[string]documentedMethod{}
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### 5.1 ") {
			inSection = true
			continue
		}
		if inSection && (strings.HasPrefix(trimmed, "## 6") || strings.HasPrefix(trimmed, "### 5.2")) {
			break
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		first := strings.TrimSpace(cells[0])
		if first == "方法" {
			continue
		}
		status := strings.TrimSpace(cells[len(cells)-1])
		if strings.HasPrefix(first, "↳") {
			// A notification row ("↳ 通知 `toast.posted`"), not a method.
			continue
		}
		// Rows without a code-formatted method name are notices
		// ("↳ 通知", separators), not methods.
		var names []string
		rest := first
		for {
			open := strings.Index(rest, "`")
			if open < 0 {
				break
			}
			closeIndex := strings.Index(rest[open+1:], "`")
			if closeIndex < 0 {
				break
			}
			names = append(names, rest[open+1:open+1+closeIndex])
			rest = rest[open+1+closeIndex+1:]
		}
		for _, name := range names {
			if !strings.Contains(name, ".") {
				continue
			}
			methods[name] = documentedMethod{Name: name, Status: status}
		}
	}
	if len(methods) == 0 {
		t.Fatal("parsed no methods from TRANSPORT.md §5.1 (did the heading change?)")
	}
	return methods
}

// methodIsExempt reports whether a documented row says the method is
// deliberately absent from this build.
func methodIsExempt(method documentedMethod) bool {
	if strings.Contains(method.Status, "延后") || strings.Contains(method.Status, "已移除") {
		return true
	}
	return strings.Contains(method.Name, "~~")
}

// TestTransportMethodsMatchDispatch keeps the document and the dispatch table
// from drifting apart in either direction.
func TestTransportMethodsMatchDispatch(t *testing.T) {
	documented := transportMethods(t)
	implemented := map[string]bool{}
	for _, name := range SupportedMethods() {
		implemented[name] = true
	}
	// The handshake trio is specified in §4.2/§4.4 rather than §5.1.
	for _, name := range []string{"initialize", "ready", "ping"} {
		if !implemented[name] {
			t.Errorf("the handshake method %q is missing from the dispatch table", name)
		}
		delete(implemented, name)
	}

	var missing, undeclared []string
	for name, method := range documented {
		if implemented[name] {
			continue
		}
		if methodIsExempt(method) {
			continue
		}
		// A documented row is absent from the build: red until either the
		// implementation lands or the row is marked 延后.
		missing = append(missing, name)
	}
	for name := range implemented {
		if _, ok := documented[name]; !ok {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Errorf("documented in TRANSPORT.md §5.1 but not dispatched (mark the row 延后 if intentional):\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(undeclared) > 0 {
		t.Errorf("dispatched but not documented in TRANSPORT.md §5.1:\n  %s",
			strings.Join(undeclared, "\n  "))
	}
}

// TestTransportDeferredRowsStayHonest fails when a row is marked deferred but the
// method does exist: a stale 延后 hides a working method from the web track.
func TestTransportDeferredRowsStayHonest(t *testing.T) {
	documented := transportMethods(t)
	implemented := map[string]bool{}
	for _, name := range SupportedMethods() {
		implemented[name] = true
	}
	for name, method := range documented {
		if !methodIsExempt(method) {
			continue
		}
		if implemented[name] {
			t.Errorf("%q is dispatched but its TRANSPORT.md row still says %q", name, method.Status)
		}
	}
}
