package gateway

// The wire contract, generated into contract.json (go:generate) and consumed by
// the web client's stand-ins — dev/ws-params.mjs, dev/mock-server.mjs and
// src/test/fake-gateway.ts — so that no stand-in keeps its own hand-copied list.
//
// Why this exists: the migration's tenth "stand-in looser than production" bug
// was a hand-written `'settings.set'` in the test fake while the real gateway
// never advertised it, so 171 tests were green and the settings screen was
// read-only against a real gateway. Asking stand-ins to be "stricter" cannot fix
// that class of bug: the list itself was a copy, and a copy that loses one entry
// silently invalidates the whole chain.
//
// Two rules keep this file honest:
//   - the capability half is NOT a second copy: static comes from
//     serverCapabilities (the same slice TestInitializeAdvertisesCapabilities
//     pins) and the conditional half is conditionalCapabilities, which
//     gatewayCapabilities() also iterates;
//   - the params half is derived by reflection from the very structs the
//     dispatch table decodes into (contractParams), with required = the tag has
//     no ",omitempty". A params struct that is not listed here fails
//     TestContractIsUpToDate.

//go:generate go run ./gencontract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// contractParams lists, for **every** dispatched method, the struct its handler
// decodes into (struct{}{} for the methods that take no params). TestContractIsUpToDate
// asserts this key set equals SupportedMethods(), so a new method must be
// classified here — and the generated contract therefore covers the whole wire
// surface rather than a hand-picked subset.
var contractParams = map[string]any{
	"initialize":              initializeParams{},
	"ready":                   struct{}{},
	"ping":                    struct{}{},
	"session.list.snapshot":   struct{}{},
	"session.snapshot":        sessionSnapshotParams{},
	"session.subscribe":       subscribeParams{},
	"session.unsubscribe":     sessionParams{},
	"session.create":          sessionCreateParams{},
	"session.active.get":      struct{}{},
	"run.cancel":              sessionParams{},
	"approvals.snapshot":      struct{}{},
	"approvals.subscribe":     domainSubscribeParams{},
	"approval.resolve":        approvalResolveParams{},
	"ask.snapshot":            struct{}{},
	"ask.subscribe":           domainSubscribeParams{},
	"ask.answer":              askAnswerParams{},
	"ask.cancel":              askAnswerParams{},
	"plan.snapshot":           struct{}{},
	"plan.subscribe":          domainSubscribeParams{},
	"plan.exit":               planExitParams{},
	"artifacts.snapshot":      struct{}{},
	"settings.snapshot":       struct{}{},
	"settings.subscribe":      domainSubscribeParams{},
	"settings.set":            settingsSetParams{},
	"workspace.list.snapshot": struct{}{},
	"notify.action":           notifyActionParams{},
	"foreground.set":          foregroundParams{},
	"prompts.snapshot":        sessionParams{},
	"prompts.subscribe":       promptsSubscribeParams{},
	"prompts.enqueue":         promptsEnqueueParams{},
	"prompts.remove":          promptsRemoveParams{},
	"context.snapshot":        contextSessionParams{},
	"context.subscribe":       contextSessionParams{},
	"tasks.snapshot":          tasksParams{},
	"tasks.subscribe":         tasksParams{},
	"task.status":             taskStatusParams{},
	"task.delete":             taskDeleteParams{},
}

// conditionalCapability is one capability that exists only when the assembly
// provides a backend half. When is a stable machine-readable marker (a Go
// symbol or interface name), never prose: the web stand-ins assert on it. The
// human explanation lives in docs/TRANSPORT.md §4.2.1.
type conditionalCapability struct {
	Name string `json:"name"`
	When string `json:"when"`
}

// conditionalCapabilities is the single source for both the capability list and
// the generated contract.
var conditionalCapabilities = []conditionalCapability{
	{Name: "context", When: "KeyContext"},
	{Name: "tasks", When: "KeyTaskBoard"},
	{Name: "regenerate", When: "SessionRegenerator"},
	{Name: "edit", When: "SessionEditor"},
}

// contractParamSpec is one method's parameter contract.
type contractParamSpec struct {
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

// contractCapabilities is the capability half of the contract.
type contractCapabilities struct {
	Static      []string                `json:"static"`
	Conditional []conditionalCapability `json:"conditional"`
}

// contractDocument is the whole generated file.
type contractDocument struct {
	// ProtocolVersion stamps the contract's shape, so a client can reject a
	// file it does not understand instead of misreading it.
	ProtocolVersion int                          `json:"protocolVersion"`
	Params          map[string]contractParamSpec `json:"params"`
	Capabilities    contractCapabilities         `json:"capabilities"`
}

// buildContract derives the contract from the Go declarations above. It is a
// pure function of the source, so its output is deterministic: parameters and
// capabilities are sorted, and the JSON encoder writes map keys in order.
func buildContract() (contractDocument, error) {
	document := contractDocument{
		ProtocolVersion: ProtocolVersion,
		Params:          make(map[string]contractParamSpec, len(contractParams)),
		Capabilities: contractCapabilities{
			Static:      append([]string(nil), serverCapabilities...),
			Conditional: append([]conditionalCapability(nil), conditionalCapabilities...),
		},
	}
	sort.Strings(document.Capabilities.Static)
	sort.Slice(document.Capabilities.Conditional, func(i, j int) bool {
		return document.Capabilities.Conditional[i].Name < document.Capabilities.Conditional[j].Name
	})
	for method, params := range contractParams {
		spec, err := paramSpecOf(params)
		if err != nil {
			return contractDocument{}, fmt.Errorf("%s: %w", method, err)
		}
		document.Params[method] = spec
	}
	return document, nil
}

// paramSpecOf reads one params struct: the json tag names the wire field, and
// ",omitempty" marks it optional.
func paramSpecOf(params any) (contractParamSpec, error) {
	spec := contractParamSpec{Required: []string{}, Optional: []string{}}
	typ := reflect.TypeOf(params)
	if typ == nil {
		return spec, fmt.Errorf("nil params value")
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return spec, fmt.Errorf("params must be a struct, got %s", typ.Kind())
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			// An untagged field would be encoded under its Go name by
			// encoding/json, which is never what the wire wants.
			return spec, fmt.Errorf("field %s has no json tag", field.Name)
		}
		name, options, _ := strings.Cut(tag, ",")
		if name == "" {
			return spec, fmt.Errorf("field %s has an empty json name", field.Name)
		}
		if strings.Contains(","+options, ",omitempty") {
			spec.Optional = append(spec.Optional, name)
			continue
		}
		spec.Required = append(spec.Required, name)
	}
	sort.Strings(spec.Required)
	sort.Strings(spec.Optional)
	return spec, nil
}

// ContractJSON renders the contract exactly as it is committed to
// contract.json: two-space indent, sorted keys, trailing newline.
func ContractJSON() ([]byte, error) {
	document, err := buildContract()
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
