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
	"session.rename":          sessionRenameParams{},
	"session.delete":          sessionDeleteParams{},
	"session.archive":         sessionArchiveParams{},
	"session.pin":             sessionPinParams{},
	"session.bindtask":        sessionBindTaskParams{},
	"session.active.get":      struct{}{},
	"run.cancel":              sessionParams{},
	"run.interject":           runInterjectParams{},
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
	"mcp.reload":              mcpReloadParams{},
	"notify.action":           notifyActionParams{},
	"foreground.set":          foregroundParams{},
	"prompts.snapshot":        sessionParams{},
	"prompts.subscribe":       promptsSubscribeParams{},
	"prompts.unsubscribe":     promptsUnsubscribeParams{},
	"prompts.enqueue":         promptsEnqueueParams{},
	"prompts.remove":          promptsRemoveParams{},
	"prompts.move":            promptsMoveParams{},
	"context.snapshot":        contextSnapshotParams{},
	"context.subscribe":       contextSubscribeParams{},
	"context.unsubscribe":     contextUnsubscribeParams{},
	"tasks.snapshot":          tasksSnapshotParams{},
	"tasks.subscribe":         tasksSubscribeParams{},
	"tasks.unsubscribe":       tasksUnsubscribeParams{},
	"task.status":             taskStatusParams{},
	"task.delete":             taskDeleteParams{},
	"update.status":           struct{}{},
	"update.check":            struct{}{},
	"update.download":         struct{}{},
	"update.install":          updateInstallParams{},
	"team.snapshot":           struct{}{},
	"team.spawn":              teamSpawnParams{},
	"team.send":               teamSendParams{},
	"team.wait":               teamIDParams{},
	"team.interrupt":          teamIDParams{},
	"team.close":              teamIDParams{},
	"team.activity":           teamActivityParams{},
	"inbox.snapshot":          inboxSnapshotParams{},
	"inbox.dispatch":          inboxDispatchParams{},
	"inbox.triage":            inboxTriageParams{},
	"tasks.lease":             leaseReadParams{},
	"tasks.claim":             leaseParams{},
	"tasks.heartbeat":         leaseParams{},
	"tasks.submit":            taskSubmitParams{},
	"tasks.review":            taskReviewParams{},
	"tasks.tools.set":         taskToolsSetParams{},
	"tasks.tools.get":         leaseReadParams{},
	"tasks.approval.set":      taskApprovalSetParams{},
	"tasks.approval.get":      leaseReadParams{},
	"tasks.approve":           taskApproveParams{},
	"tasks.reject-start":      taskRejectStartParams{},
	"tasks.send.pending":      taskSendPendingParams{},
	"tasks.send.approve":      taskSendApproveParams{},
	"tasks.budget.set":        taskBudgetSetParams{},
	"tasks.budget.get":        leaseReadParams{},
	"tasks.budget.consume":    taskBudgetConsumeParams{},
	"approval.token":          approvalTokenParams{},
	"approval.action":         approvalActionParams{},
	"notify.route.snapshot":   struct{}{},
	"notify.route.upsert":     notifyRouteUpsertParams{},
	"credentials.list":        credentialsListParams{},
	"credentials.set":         credentialsSetParams{},
	"credentials.delete":      credentialsDeleteParams{},
	"webhook.sources":         struct{}{},
	"webhook.source.set":      webhookSourceSetParams{},
	"webhook.source.remove":   webhookSourceRemoveParams{},
	"lead.run":                struct{}{},
	"wechat.login.start":      struct{}{},
	"wechat.login.state":      wechatLoginStateParams{},
	"notify.plan": struct {
		Category string `json:"category"`
	}{},
	"notify.deliver":    notifyDeliverParams{},
	"bots.snapshot":     struct{}{},
	"bots.upsert":       botsUpsertParams{},
	"bots.template.get": botTemplateGetParams{},
	"bots.template.put": botTemplatePutParams{},
	"skilldrafts.snapshot": struct {
		Limit int `json:"limit,omitempty"`
	}{},
	"skilldrafts.propose": skillDraftProposeParams{},
	"skilldrafts.decide":  skillDraftDecideParams{},
	"skilldrafts.install": skillDraftInstallParams{},
	"team.suggestions": struct {
		Limit int `json:"limit,omitempty"`
	}{},
	"team.suggest":            noteSuggestParams{},
	"team.suggestion.promote": noteSuggestionDecisionParams{},
	"team.suggestion.dismiss": noteSuggestionDecisionParams{},
	"team.notes": struct {
		Limit int `json:"limit,omitempty"`
	}{},
	"team.note.record":    teamNotesRecordParams{},
	"team.note.review":    teamNoteReviewParams{},
	"heartbeat.status":    struct{}{},
	"heartbeat.tick":      struct{}{},
	"heartbeat.configure": heartbeatConfigureParams{},
	"browser.status":      struct{}{},
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
	{Name: "team", When: "KeyTeam"},
	{Name: "update", When: "KeyUpdate"},
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

// contractRule is one machine-checkable rule of an endpoint's request body.
//
// The shapes are deliberately flat and JS-friendly (no regexes, no expressions):
// a stand-in validates with a four-case switch instead of re-implementing the
// handler's conditionals.
//
//	exclusive      the listed fields may not appear together
//	requires       when `when` is present, `field` must be present (and, with
//	               nonEmpty, must not be blank)
//	forbidden      when `when` is present, `field` must be ABSENT
//	requiredUnless `field` is required unless one of `unless` is present
type contractRule struct {
	Kind     string   `json:"kind"`
	Fields   []string `json:"fields,omitempty"`
	Field    string   `json:"field,omitempty"`
	When     string   `json:"when,omitempty"`
	Unless   []string `json:"unless,omitempty"`
	NonEmpty bool     `json:"nonEmpty,omitempty"`
}

// contractEndpoint is one non-JSON-RPC endpoint of the wire surface.
//
// Endpoints live OUTSIDE `params`: that map is keyed by JSON-RPC method and
// TestContractCoversEveryMethod pins its key set to SupportedMethods(), so
// folding POST /api/chat/run into it would recreate the "contract says a method
// the gateway does not dispatch" bug this file exists to prevent. The endpoint
// carries its own transport/method/path instead.
type contractEndpoint struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Fields    contractParamSpec `json:"fields"`
	Rules     []contractRule    `json:"rules"`
}

// contractDocument is the whole generated file.
type contractDocument struct {
	// ProtocolVersion stamps the contract's shape, so a client can reject a
	// file it does not understand instead of misreading it.
	ProtocolVersion int                          `json:"protocolVersion"`
	Params          map[string]contractParamSpec `json:"params"`
	Endpoints       []contractEndpoint           `json:"endpoints"`
	Capabilities    contractCapabilities         `json:"capabilities"`
}

// runEndpointRules is the mode logic of POST /api/chat/run, expressed once so
// the stand-ins stop re-implementing it with their own conditionals. It mirrors
// handleChatRun's validation exactly; the handler keeps the prose reasons.
var runEndpointRules = []contractRule{
	// Two different rewrites of the same history.
	{Kind: "exclusive", Fields: []string{"regenerate", "editMessageId"}},
	// Edit mode replaces the target message's text, so text IS the new content.
	{Kind: "requires", Field: "text", When: "editMessageId", NonEmpty: true},
	// Regenerate re-runs the prompt the backend already holds; sending text
	// would append a second prompt.
	{Kind: "forbidden", Field: "text", When: "regenerate"},
	// A plain send needs the prompt.
	{Kind: "requiredUnless", Field: "text", Unless: []string{"regenerate"}, NonEmpty: true},
}

// buildEndpoints derives the non-JSON-RPC endpoints. The field list comes from
// the very struct the handler decodes (runRequest), so a new field cannot be
// added without appearing here.
func buildEndpoints() ([]contractEndpoint, error) {
	fields, err := paramSpecOf(runRequest{})
	if err != nil {
		return nil, fmt.Errorf("runRequest: %w", err)
	}
	rules := append([]contractRule(nil), runEndpointRules...)
	return []contractEndpoint{{
		Name:      "chat.run",
		Transport: "sse",
		Method:    "POST",
		Path:      "/api/chat/run",
		Fields:    fields,
		Rules:     rules,
	}}, nil
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
	endpoints, err := buildEndpoints()
	if err != nil {
		return contractDocument{}, err
	}
	document.Endpoints = endpoints
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
