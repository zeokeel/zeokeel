package gateway

// Protocol/implementation consistency.
//
// The wire surface used to be documented in docs/TRANSPORT.md §5.1 and this test
// parsed that table. The documentation tree is gone, so the single declaration is
// the generated contract (contract.json, built from contract.go by
// `go generate ./internal/gateway`) — the same table the web stand-ins consume,
// so the browser and the gateway cannot silently disagree either.
//
// The failure this prevents happened twice in one migration: a method declared
// and never dispatched, the browser calling it, and nothing going red until a
// human compared the two by hand.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// contractMethods is the declared wire surface: every method the contract names.
func contractMethods(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := ContractJSON()
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	var contract struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	if len(contract.Params) == 0 {
		t.Fatal("the contract declares no methods")
	}
	methods := make(map[string]bool, len(contract.Params))
	for name := range contract.Params {
		methods[name] = true
	}
	return methods
}

// TestTransportMethodsMatchDispatch keeps the declaration and the dispatch table
// from drifting apart in either direction.
func TestTransportMethodsMatchDispatch(t *testing.T) {
	declared := contractMethods(t)
	implemented := map[string]bool{}
	for _, name := range SupportedMethods() {
		implemented[name] = true
	}
	// The handshake trio is declared in the contract like any other method, so no
	// special case is needed here: if one of them stops being dispatched the loop
	// below reports it.

	var missing, undeclared []string
	for name := range declared {
		if !implemented[name] {
			missing = append(missing, name)
		}
	}
	for name := range implemented {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Errorf("declared in contract.json but not dispatched:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(undeclared) > 0 {
		t.Errorf("dispatched but not declared in contract.json (run go generate ./internal/gateway):\n  %s",
			strings.Join(undeclared, "\n  "))
	}
}
