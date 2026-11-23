package gateway

// Staleness check for the generated wire contract (contract.json).
//
// The file is consumed by the web client's stand-ins (dev/ws-params.mjs,
// dev/mock-server.mjs, src/test/fake-gateway.ts) so that none of them keeps a
// hand-copied method or capability list. A stale file is worse than no file: the
// stand-ins would agree with each other and disagree with the gateway, which is
// exactly the bug class this contract exists to kill.
//
// Regenerate with: go generate ./internal/gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// contractPath is the committed generated file.
func contractPath() string { return filepath.Join("contract.json") }

// TestContractIsUpToDate fails when contract.json no longer matches the Go
// declarations. The message names the offending method or capability so the fix
// is "run go generate", not a hunt.
func TestContractIsUpToDate(t *testing.T) {
	want, err := ContractJSON()
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	have, err := os.ReadFile(contractPath())
	if err != nil {
		t.Fatalf("read %s: %v (run `go generate ./internal/gateway`)", contractPath(), err)
	}
	if string(have) == string(want) {
		return
	}

	// Diff structurally so the failure points at the stale entry.
	var haveDoc, wantDoc contractDocument
	if err := json.Unmarshal(have, &haveDoc); err != nil {
		t.Fatalf("%s is not valid JSON: %v (run `go generate ./internal/gateway`)", contractPath(), err)
	}
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("generated contract is not valid JSON: %v", err)
	}
	var stale []string
	if haveDoc.ProtocolVersion != wantDoc.ProtocolVersion {
		stale = append(stale, "protocolVersion")
	}
	for method, spec := range wantDoc.Params {
		old, ok := haveDoc.Params[method]
		if !ok {
			stale = append(stale, "missing method: "+method)
			continue
		}
		if !sameStrings(old.Required, spec.Required) || !sameStrings(old.Optional, spec.Optional) {
			stale = append(stale, "params drifted: "+method)
		}
	}
	for method := range haveDoc.Params {
		if _, ok := wantDoc.Params[method]; !ok {
			stale = append(stale, "removed method: "+method)
		}
	}
	if !sameStrings(haveDoc.Capabilities.Static, wantDoc.Capabilities.Static) {
		stale = append(stale, "capabilities.static drifted")
	}
	if len(haveDoc.Capabilities.Conditional) != len(wantDoc.Capabilities.Conditional) {
		stale = append(stale, "capabilities.conditional drifted")
	} else {
		for index := range wantDoc.Capabilities.Conditional {
			if haveDoc.Capabilities.Conditional[index] != wantDoc.Capabilities.Conditional[index] {
				stale = append(stale, "conditional capability drifted: "+
					wantDoc.Capabilities.Conditional[index].Name)
			}
		}
	}
	sort.Strings(stale)
	if len(stale) == 0 {
		stale = append(stale, "whitespace or key order differs")
	}
	t.Fatalf("internal/gateway/contract.json is stale (run `go generate ./internal/gateway`):\n  %s",
		strings.Join(stale, "\n  "))
}

// TestContractCoversEveryMethod keeps the contract from being a hand-picked
// subset: every dispatched method must be classified in contractParams.
func TestContractCoversEveryMethod(t *testing.T) {
	document, err := buildContract()
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	dispatched := make(map[string]bool, len(SupportedMethods()))
	for _, method := range SupportedMethods() {
		dispatched[method] = true
	}
	for method := range dispatched {
		if _, ok := document.Params[method]; !ok {
			t.Errorf("method %q is dispatched but absent from contractParams (classify it in contract.go)", method)
		}
	}
	for method := range document.Params {
		if !dispatched[method] {
			t.Errorf("contractParams lists %q, which the gateway does not dispatch", method)
		}
	}
	if len(document.Params) != len(dispatched) {
		t.Fatalf("contract lists %d methods, dispatch has %d", len(document.Params), len(dispatched))
	}
}

// TestContractCapabilitiesAreTheHandshakeList keeps the two from drifting: the
// static half must be exactly the capability table the handshake advertises, and
// every conditional entry must resolve to a marker the gateway understands.
func TestContractCapabilitiesAreTheHandshakeList(t *testing.T) {
	document, err := buildContract()
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	static := append([]string(nil), serverCapabilities...)
	sort.Strings(static)
	want := append([]string(nil), document.Capabilities.Static...)
	sort.Strings(want)
	if !sameStrings(static, want) {
		t.Fatalf("contract static capabilities = %v, handshake advertises %v", want, static)
	}
	for _, conditional := range document.Capabilities.Conditional {
		switch conditional.When {
		case "KeyContext", "KeyTaskBoard", "SessionRegenerator", "SessionEditor":
		default:
			t.Errorf("conditional capability %q uses unknown marker %q", conditional.Name, conditional.When)
		}
	}
}

// sameStrings reports whether two string slices are equal (both are sorted by
// the generator, but the caller may pass an unordered copy).
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
