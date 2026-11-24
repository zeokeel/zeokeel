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
		case "KeyContext", "KeyTaskBoard", "KeyTeam", "KeyUpdate", "SessionRegenerator", "SessionEditor":
		default:
			t.Errorf("conditional capability %q uses unknown marker %q", conditional.Name, conditional.When)
		}
	}
}

// TestContractCoversTheRunEndpoint keeps POST /api/chat/run in the contract: it is
// the protocol's most complex request body (three run modes with mutually
// exclusive fields), and the stand-ins used to re-implement its validation by
// hand — one of them silently dropped editMessageId, so the mock accepted an edit
// and ran a plain turn. The field list is asserted against the struct the handler
// decodes, so it cannot go stale; the rules are asserted one by one because a
// mode rule disappearing is exactly the regression this test exists for.
func TestContractCoversTheRunEndpoint(t *testing.T) {
	document, err := buildContract()
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	if len(document.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want exactly chat.run", len(document.Endpoints))
	}
	endpoint := document.Endpoints[0]
	if endpoint.Name != "chat.run" || endpoint.Method != "POST" || endpoint.Path != "/api/chat/run" {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if endpoint.Transport != "sse" {
		t.Fatalf("transport = %q, want sse (the response is an event stream)", endpoint.Transport)
	}

	// The field lists must be the struct's json tags, exactly.
	spec, err := paramSpecOf(runRequest{})
	if err != nil {
		t.Fatalf("paramSpecOf(runRequest): %v", err)
	}
	if !sameStrings(endpoint.Fields.Required, spec.Required) || !sameStrings(endpoint.Fields.Optional, spec.Optional) {
		t.Fatalf("run fields = %+v, want %+v", endpoint.Fields, spec)
	}
	if !sameStrings(spec.Required, []string{"clientRunId", "sessionId"}) {
		t.Fatalf("required fields = %v, want clientRunId and sessionId", spec.Required)
	}

	// Every rule must reference fields the endpoint actually has, and the four
	// mode rules must all be present.
	known := map[string]bool{}
	for _, field := range append(append([]string{}, spec.Required...), spec.Optional...) {
		known[field] = true
	}
	for _, rule := range endpoint.Rules {
		for _, field := range rule.Fields {
			if !known[field] {
				t.Errorf("rule %+v references unknown field %q", rule, field)
			}
		}
		for _, field := range []string{rule.Field, rule.When} {
			if field != "" && !known[field] {
				t.Errorf("rule %+v references unknown field %q", rule, field)
			}
		}
		for _, field := range rule.Unless {
			if !known[field] {
				t.Errorf("rule %+v references unknown field %q", rule, field)
			}
		}
	}
	wantRules := []contractRule{
		{Kind: "exclusive", Fields: []string{"regenerate", "editMessageId"}},
		{Kind: "requires", Field: "text", When: "editMessageId", NonEmpty: true},
		{Kind: "forbidden", Field: "text", When: "regenerate"},
		{Kind: "requiredUnless", Field: "text", Unless: []string{"regenerate"}, NonEmpty: true},
	}
	if len(endpoint.Rules) != len(wantRules) {
		t.Fatalf("rules = %+v, want %+v", endpoint.Rules, wantRules)
	}
	for index, want := range wantRules {
		got := endpoint.Rules[index]
		if got.Kind != want.Kind || got.Field != want.Field || got.When != want.When ||
			got.NonEmpty != want.NonEmpty || !sameStrings(got.Fields, want.Fields) || !sameStrings(got.Unless, want.Unless) {
			t.Fatalf("rule %d = %+v, want %+v", index, got, want)
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
