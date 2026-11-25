package gateway

// Orchestration control-plane methods: the inbox (single external entry point),
// the task lease (claim / heartbeat / submit / review) and the approval action
// (one-time token, or a chat reply). They are request-response on purpose: the
// UI reads them when it opens the "等我" screen, and the Lead's triage loop
// reads them on its own tick. A push domain can be added later without changing
// these payloads.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- DTOs ------------------------------------------------------------------

type inboxItemDTO struct {
	ID         string   `json:"id"`
	Source     string   `json:"source"`
	Channel    string   `json:"channel,omitempty"`
	Sender     string   `json:"sender,omitempty"`
	DedupKey   string   `json:"dedupKey,omitempty"`
	Priority   int      `json:"priority,omitempty"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	Ref        string   `json:"ref,omitempty"`
	ReceivedAt string   `json:"receivedAt,omitempty"`
	LastSeenAt string   `json:"lastSeenAt,omitempty"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
	Status     string   `json:"status"`
	Count      int      `json:"count"`
	TaskIDs    []string `json:"taskIds,omitempty"`
}

type inboxStatsDTO struct {
	Total   int `json:"total"`
	New     int `json:"new"`
	Triaged int `json:"triaged"`
}

type evidenceDTO struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}

type taskLeaseDTO struct {
	TaskID     string        `json:"taskId"`
	Owner      string        `json:"owner"`
	Until      string        `json:"until,omitempty"`
	Heartbeat  string        `json:"heartbeat,omitempty"`
	State      string        `json:"state"`
	Evidence   []evidenceDTO `json:"evidence,omitempty"`
	ReviewNote string        `json:"reviewNote,omitempty"`
}

// taskToolsDTO is the M5 tool binding of one task. `tools` is deliberately not
// omitempty: null on the wire means "no binding" (the same shape tasks.tools.get
// answers when there is none), while [] never comes back from a read — a clear
// removes the binding instead of storing an empty one.
type taskToolsDTO struct {
	TaskID    string   `json:"taskId"`
	Tools     []string `json:"tools"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

// --- mapping ---------------------------------------------------------------

func rfc3339(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func inboxItemFromContract(item contracts.InboxItem) inboxItemDTO {
	dto := inboxItemDTO{
		ID: item.ID, Source: item.Source, Channel: item.Channel, Sender: item.Sender,
		DedupKey: item.DedupKey, Priority: item.Priority, Title: item.Title,
		Body: item.Body, Ref: item.Ref, Status: item.Status, Count: item.Count,
		ReceivedAt: rfc3339(item.ReceivedAt), LastSeenAt: rfc3339(item.LastSeenAt),
		ExpiresAt: rfc3339(item.ExpiresAt),
	}
	if len(item.TaskIDs) > 0 {
		dto.TaskIDs = append([]string(nil), item.TaskIDs...)
	}
	return dto
}

func taskLeaseFromContract(lease contracts.TaskLease) taskLeaseDTO {
	dto := taskLeaseDTO{
		TaskID: lease.TaskID, Owner: lease.Owner, State: lease.State,
		Until: rfc3339(lease.Until), Heartbeat: rfc3339(lease.Heartbeat),
		ReviewNote: lease.ReviewNote,
	}
	for _, evidence := range lease.Evidence {
		dto.Evidence = append(dto.Evidence, evidenceDTO{Kind: evidence.Kind, Ref: evidence.Ref, Note: evidence.Note})
	}
	return dto
}

func taskToolsFromContract(binding contracts.TaskTools) taskToolsDTO {
	dto := taskToolsDTO{TaskID: binding.TaskID, UpdatedAt: rfc3339(binding.UpdatedAt)}
	if len(binding.Tools) > 0 {
		dto.Tools = append([]string(nil), binding.Tools...)
	}
	return dto
}

func evidenceFromDTO(rows []evidenceDTO) []contracts.Evidence {
	if len(rows) == 0 {
		return nil
	}
	out := make([]contracts.Evidence, 0, len(rows))
	for _, row := range rows {
		out = append(out, contracts.Evidence{Kind: row.Kind, Ref: row.Ref, Note: row.Note})
	}
	return out
}

// --- service getters -------------------------------------------------------

func (g *Gateway) inboxService() (contracts.InboxService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyInbox)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "inbox unavailable")
	}
	return service, nil
}

func (g *Gateway) triageService() (contracts.TriageService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyTriage)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "triage unavailable")
	}
	return service, nil
}

func (g *Gateway) leaseService() (contracts.TaskLeaseService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyTaskLease)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "task lease unavailable")
	}
	return service, nil
}

func (g *Gateway) approvalActionService() (contracts.ApprovalActionService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyApprovalAction)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "approval action unavailable")
	}
	return service, nil
}

// orchestrationError maps the typed workflow errors to client errors instead of
// "internal error", so the UI can say "someone else has it" verbatim.
func orchestrationError(err error) *protocolError {
	switch {
	case errors.Is(err, contracts.ErrLeaseHeld),
		errors.Is(err, contracts.ErrLeaseNotHeld),
		errors.Is(err, contracts.ErrLeaseExpired),
		errors.Is(err, contracts.ErrLeaseEvidenceRequired),
		errors.Is(err, contracts.ErrLeaseApprovalRequired),
		errors.Is(err, contracts.ErrLeaseInvalid),
		errors.Is(err, contracts.ErrTaskToolsInvalid),
		errors.Is(err, contracts.ErrTaskToolsUnknownTask),
		errors.Is(err, contracts.ErrInboxUnknownItem),
		errors.Is(err, contracts.ErrInboxUnknownAction),
		errors.Is(err, contracts.ErrInboxInvalid),
		errors.Is(err, contracts.ErrTriageInvalid),
		errors.Is(err, contracts.ErrApprovalTokenInvalid),
		errors.Is(err, contracts.ErrApprovalTokenExpired),
		errors.Is(err, contracts.ErrApprovalTokenUsed),
		errors.Is(err, contracts.ErrApprovalNotPending),
		errors.Is(err, contracts.ErrApprovalNoPending):
		return protocolErrorWithMessage(codeInvalidRequest, err.Error())
	}
	return internalError(err)
}

// --- inbox -----------------------------------------------------------------

type inboxSnapshotResult struct {
	Items []inboxItemDTO `json:"items"`
	Stats inboxStatsDTO  `json:"stats"`
}

type inboxSnapshotParams struct {
	Statuses       []string `json:"statuses,omitempty"`
	Source         string   `json:"source,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	IncludeExpired bool     `json:"includeExpired,omitempty"`
}

func (g *Gateway) inboxSnapshot(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.inboxService()
	if problem != nil {
		return nil, problem
	}
	var params inboxSnapshotParams
	if len(raw) > 0 {
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
	}
	items, err := service.List(ctx, contracts.InboxFilter{
		Statuses: params.Statuses, Source: params.Source, Limit: params.Limit,
		IncludeExpired: params.IncludeExpired,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	stats, err := service.Stats(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := inboxSnapshotResult{
		Items: make([]inboxItemDTO, 0, len(items)),
		Stats: inboxStatsDTO{Total: stats.Total, New: stats.New, Triaged: stats.Triaged},
	}
	for _, item := range items {
		result.Items = append(result.Items, inboxItemFromContract(item))
	}
	return result, nil
}

type inboxDispatchParams struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Detail      string   `json:"detail,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	BlockedBy   []string `json:"blockedBy,omitempty"`
	WriteScopes []string `json:"writeScopes,omitempty"`
	Note        string   `json:"note,omitempty"`
}

type inboxDispatchResult struct {
	TaskIDs []string     `json:"taskIds"`
	Created bool         `json:"created"`
	Item    inboxItemDTO `json:"item"`
}

func (g *Gateway) inboxDispatch(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.triageService()
	if problem != nil {
		return nil, problem
	}
	var params inboxDispatchParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	result, err := service.Apply(ctx, params.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject: params.Subject, Detail: params.Detail,
			BlockedBy: params.BlockedBy, WriteScopes: params.WriteScopes,
			Assignee: params.Assignee,
		},
		Note: params.Note,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return inboxDispatchResult{
		TaskIDs: result.TaskIDs, Created: result.Created,
		Item: inboxItemFromContract(result.Inbox),
	}, nil
}

type inboxTriageParams struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

func (g *Gateway) inboxTriage(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.triageService()
	if problem != nil {
		return nil, problem
	}
	var params inboxTriageParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	result, err := service.Apply(ctx, params.ID, contracts.TriageDecision{Action: params.Action, Note: params.Note})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return inboxItemFromContract(result.Inbox), nil
}

// --- task lease ------------------------------------------------------------

// leaseReadParams is a read: only the task id. Keeping it separate from
// leaseParams stops the generator from marking owner as required for a method
// that never uses it.
type leaseReadParams struct {
	ID string `json:"id"`
}

type leaseParams struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
}

func (g *Gateway) taskClaim(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params leaseParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	lease, err := service.Claim(ctx, params.ID, params.Owner)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskLeaseFromContract(lease), nil
}

func (g *Gateway) taskHeartbeat(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params leaseParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	lease, err := service.Heartbeat(ctx, params.ID, params.Owner)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskLeaseFromContract(lease), nil
}

type taskSubmitParams struct {
	ID       string        `json:"id"`
	Owner    string        `json:"owner"`
	Evidence []evidenceDTO `json:"evidence"`
}

func (g *Gateway) taskSubmit(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskSubmitParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	lease, err := service.Submit(ctx, params.ID, params.Owner, evidenceFromDTO(params.Evidence))
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskLeaseFromContract(lease), nil
}

type taskReviewParams struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Reviewer string `json:"reviewer"`
	Note     string `json:"note,omitempty"`
}

func (g *Gateway) taskReview(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskReviewParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	var (
		lease contracts.TaskLease
		err   error
	)
	switch params.Decision {
	case "accept":
		lease, err = service.Accept(ctx, params.ID, params.Reviewer, params.Note)
	case "reject":
		lease, err = service.Reject(ctx, params.ID, params.Reviewer, params.Note)
	default:
		return nil, errInvalidParams("decision", "want accept or reject")
	}
	if err != nil {
		return nil, orchestrationError(err)
	}
	// Acceptance is the one moment a task becomes team memory: record what was
	// done and what proved it, best-effort. A memory store failure must never
	// undo a review the reviewer already made.
	if params.Decision == "accept" {
		g.recordReviewNote(ctx, params.ID, params.Reviewer, params.Note, lease)
	}
	g.notifyReview(ctx, params.Decision, params.ID, params.Note)
	return taskLeaseFromContract(lease), nil
}

// recordReviewNote writes the accepted task into the shared team notes. It
// deliberately swallows failures after logging: the review already happened.
func (g *Gateway) recordReviewNote(ctx context.Context, taskID, reviewer, note string, lease contracts.TaskLease) {
	notes, ok := g.root.Get(contracts.KeyTeamNotes)
	if !ok || notes == nil {
		return
	}
	title := "任务验收：" + taskID
	if strings.TrimSpace(note) != "" {
		title = "任务验收：" + note
		if len([]rune(title)) > 120 {
			title = string([]rune(title)[:120])
		}
	}
	body := "任务 " + taskID + " 已验收。"
	if strings.TrimSpace(note) != "" {
		body += "\n\n验收意见：" + note
	}
	if len(lease.Evidence) > 0 {
		body += "\n\n证据："
		for _, evidence := range lease.Evidence {
			body += "\n- " + evidence.Kind + ": " + evidence.Ref
		}
	}
	if _, err := notes.Record(ctx, contracts.TeamNote{
		TaskID: taskID, Title: title, Body: body, Author: reviewer,
	}); err != nil {
		g.logger.Printf("gateway: record review note for %s: %v", taskID, err)
	}
}

// notifyReview tells the human that a task settled. It is the first real
// producer for the notification router: until now the route could be planned and
// tested but nothing in the product ever asked it to send anything.
func (g *Gateway) notifyReview(ctx context.Context, decision, taskID, note string) {
	dispatcher, ok := g.root.Get(contracts.KeyChannelDispatch)
	if !ok || dispatcher == nil {
		return
	}
	category := contracts.NotifyCategoryTaskDone
	text := "任务 " + taskID + " 已通过验收。"
	if decision != "accept" {
		category = contracts.NotifyCategoryTaskFailed
		text = "任务 " + taskID + " 被打回，需要返工。"
	}
	if strings.TrimSpace(note) != "" {
		text += "\n" + note
	}
	if _, err := dispatcher.Deliver(ctx, category, text); err != nil {
		g.logger.Printf("gateway: notify review for %s: %v", taskID, err)
	}
}

// --- approval action -------------------------------------------------------

type approvalTokenParams struct {
	ApprovalID string `json:"approvalId"`
}

type approvalTokenResult struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

func (g *Gateway) approvalToken(_ context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.approvalActionService()
	if problem != nil {
		return nil, problem
	}
	var params approvalTokenParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	token, expires, err := service.IssueToken(params.ApprovalID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return approvalTokenResult{Token: token, ExpiresAt: rfc3339(expires)}, nil
}

type approvalActionParams struct {
	ApprovalID string `json:"approvalId"`
	Decision   string `json:"decision"`
	Token      string `json:"token"`
}

func (g *Gateway) approvalAction(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.approvalActionService()
	if problem != nil {
		return nil, problem
	}
	var params approvalActionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	decision := contracts.ApprovalDecision(params.Decision)
	if decision != contracts.ApprovalAllow && decision != contracts.ApprovalDeny {
		return nil, errInvalidParams("decision", "want allow or deny")
	}
	if err := service.Apply(ctx, params.ApprovalID, decision, params.Token); err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"ok": true}, nil
}

// --- notification routes ---------------------------------------------------

type notifyRouteDTO struct {
	Category     string   `json:"category"`
	Primary      string   `json:"primary"`
	Fallback     []string `json:"fallback,omitempty"`
	QuietHours   string   `json:"quietHours,omitempty"`
	EscalationMS int64    `json:"escalationMs,omitempty"`
	Enabled      bool     `json:"enabled"`
}

type notifyRoutesResult struct {
	Routes []notifyRouteDTO `json:"routes"`
}

type notifyRouteUpsertParams struct {
	Category     string   `json:"category"`
	Primary      string   `json:"primary"`
	Fallback     []string `json:"fallback,omitempty"`
	QuietHours   string   `json:"quietHours,omitempty"`
	EscalationMS int64    `json:"escalationMs,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
}

func notifyRouteFromContract(route contracts.NotifyRoute) notifyRouteDTO {
	dto := notifyRouteDTO{
		Category: route.Category, Primary: route.Primary, QuietHours: route.QuietHours,
		EscalationMS: route.Escalation.Milliseconds(), Enabled: route.Enabled,
	}
	if len(route.Fallback) > 0 {
		dto.Fallback = append([]string(nil), route.Fallback...)
	}
	return dto
}

func (g *Gateway) notifyRouteService() (contracts.NotifyRouterService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyNotifyRoute)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "notification routing unavailable")
	}
	return service, nil
}

func (g *Gateway) notifyRoutesSnapshot(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, problem := g.notifyRouteService()
	if problem != nil {
		return nil, problem
	}
	routes, err := service.Routes(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := notifyRoutesResult{Routes: make([]notifyRouteDTO, 0, len(routes))}
	for _, route := range routes {
		result.Routes = append(result.Routes, notifyRouteFromContract(route))
	}
	return result, nil
}

func (g *Gateway) notifyRouteUpsert(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.notifyRouteService()
	if problem != nil {
		return nil, problem
	}
	var params notifyRouteUpsertParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}
	route := contracts.NotifyRoute{
		Category: params.Category, Primary: params.Primary, Fallback: params.Fallback,
		QuietHours: params.QuietHours, Escalation: time.Duration(params.EscalationMS) * time.Millisecond,
		Enabled: enabled,
	}
	if err := service.Upsert(ctx, route); err != nil {
		return nil, orchestrationError(err)
	}
	return notifyRouteFromContract(route), nil
}

// --- webhook sources -------------------------------------------------------

type webhookSourcesResult struct {
	Sources []webhookSourceDTO `json:"sources"`
}

type webhookSourceDTO struct {
	ID      string `json:"id"`
	Channel string `json:"channel,omitempty"`
}

type webhookSourceSetParams struct {
	ID      string `json:"id"`
	Secret  string `json:"secret"`
	Channel string `json:"channel,omitempty"`
}

type webhookSourceRemoveParams struct {
	ID string `json:"id"`
}

func (g *Gateway) webhookAdmin() (contracts.WebhookSourceAdmin, *protocolError) {
	service, ok := g.root.Get(contracts.KeyWebhook)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "webhook unavailable")
	}
	admin, ok := service.(contracts.WebhookSourceAdmin)
	if !ok {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "webhook source admin unavailable")
	}
	return admin, nil
}

func (g *Gateway) webhookSources(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	admin, problem := g.webhookAdmin()
	if problem != nil {
		return nil, problem
	}
	sources, err := admin.Sources(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := webhookSourcesResult{Sources: make([]webhookSourceDTO, 0, len(sources))}
	for _, source := range sources {
		result.Sources = append(result.Sources, webhookSourceDTO{ID: source.ID, Channel: source.Channel})
	}
	return result, nil
}

func (g *Gateway) webhookSourceSet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	admin, problem := g.webhookAdmin()
	if problem != nil {
		return nil, problem
	}
	var params webhookSourceSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Secret == "" {
		return nil, errInvalidParams("secret", "a source secret is required")
	}
	if err := admin.UpsertSource(ctx, contracts.WebhookSource{
		ID: params.ID, Secret: []byte(params.Secret), DefaultChannel: params.Channel,
	}); err != nil {
		return nil, orchestrationError(err)
	}
	return webhookSourceDTO{ID: params.ID, Channel: params.Channel}, nil
}

func (g *Gateway) webhookSourceRemove(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	admin, problem := g.webhookAdmin()
	if problem != nil {
		return nil, problem
	}
	var params webhookSourceRemoveParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := admin.RemoveSource(ctx, params.ID); err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"ok": true}, nil
}

// --- lead triage -----------------------------------------------------------

type leadRunResult struct {
	Scanned    int      `json:"scanned"`
	Dispatched int      `json:"dispatched"`
	Ignored    int      `json:"ignored"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Errors     []string `json:"errors,omitempty"`
}

func (g *Gateway) leadService() (contracts.LeadService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyLead)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "lead unavailable")
	}
	return service, nil
}

// leadRun performs one bounded triage pass. It is idempotent in effect: items
// already turned into work are not new any more, so a second call is cheap.
func (g *Gateway) leadRun(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, problem := g.leadService()
	if problem != nil {
		return nil, problem
	}
	report, err := service.RunOnce(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return leadRunResult{
		Scanned: report.Scanned, Dispatched: report.Dispatched, Ignored: report.Ignored,
		Skipped: report.Skipped, Failed: report.Failed, Errors: report.Errors,
	}, nil
}

// tasks.lease is the read half of the lease surface. Without it the task screen
// can only see evidence right after a write in the same session; with it, opening
// a task shows what a teammate already submitted.
func (g *Gateway) taskLease(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params leaseParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	lease, found, err := service.Lease(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !found {
		return map[string]any{"lease": nil}, nil
	}
	return taskLeaseFromContract(lease), nil
}

// --- task tools (M5 execution policy) --------------------------------------

// taskToolsSetParams needs its own struct (not leaseParams + a slice): `tools`
// is a required field, and the generator derives required from the missing
// ",omitempty". The handler rejects a missing/null list rather than reading it
// as "clear" — a policy must never be removed by a malformed request.
type taskToolsSetParams struct {
	ID    string   `json:"id"`
	Tools []string `json:"tools"`
}

// tasks.tools.set binds the tools one task's owner may run. Rebinding replaces
// the previous set; an empty list clears the binding.
func (g *Gateway) taskToolsSet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskToolsSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Tools == nil {
		return nil, errInvalidParams("tools", "required (send [] to clear the binding)")
	}
	binding, err := service.SetTools(ctx, params.ID, params.Tools)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskToolsFromContract(binding), nil
}

// tasks.tools.get is the read half: the task screen shows the policy a task was
// dispatched with, and a task without one answers {"tools":null} — the same
// shape as tasks.lease's {"lease":null}, so the client has one absent case.
func (g *Gateway) taskToolsGet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params leaseReadParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	binding, found, err := service.Tools(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !found {
		return map[string]any{"tools": nil}, nil
	}
	return taskToolsFromContract(binding), nil
}

// --- task pre-start approval (G2) ------------------------------------------

// taskApprovalSetParams requires `policy`: a missing field must be a malformed
// request (-32602), never a silent clear of an approval gate.
type taskApprovalSetParams struct {
	ID     string `json:"id"`
	Policy string `json:"policy"`
}

// taskApprovalSetResult is the stored policy (normalized by the service).
type taskApprovalSetResult struct {
	Policy string `json:"policy"`
}

// taskApprovalGetResult is the read half. `set` distinguishes "configured" from
// the default; `policy` is always a real value, never "".
type taskApprovalGetResult struct {
	Policy string `json:"policy"`
	Set    bool   `json:"set"`
}

type taskApproveParams struct {
	ID       string `json:"id"`
	Approver string `json:"approver"`
}

// taskRejectStartParams requires `note`: a refusal without a reason is what the
// task screen would show as an unexplained bounce.
type taskRejectStartParams struct {
	ID       string `json:"id"`
	Approver string `json:"approver"`
	Note     string `json:"note"`
}

// tasks.approval.set declares whether a task needs confirmation before start.
func (g *Gateway) taskApprovalSet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskApprovalSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Policy) == "" {
		return nil, errInvalidParams("policy", "want none or before_start")
	}
	policy, err := service.SetApproval(ctx, params.ID, params.Policy)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskApprovalSetResult{Policy: policy}, nil
}

// tasks.approval.get answers {policy,set} for a task; a task with no policy
// answers the default (none, false) rather than an absent shape, so the client
// has no second empty case to handle.
func (g *Gateway) taskApprovalGet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params leaseReadParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	policy, set, err := service.Approval(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !set || policy == "" {
		policy = contracts.TaskApprovalNone
	}
	return taskApprovalGetResult{Policy: policy, Set: set}, nil
}

// tasks.approve lets a parked claim start.
func (g *Gateway) taskApprove(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskApproveParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	lease, err := service.Approve(ctx, params.ID, params.Approver)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskLeaseFromContract(lease), nil
}

// tasks.reject-start refuses a start and returns the task to the pool.
func (g *Gateway) taskRejectStart(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskRejectStartParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := service.RejectStart(ctx, params.ID, params.Approver, params.Note); err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"ok": true}, nil
}

// --- credentials -----------------------------------------------------------

type credentialInfoDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type credentialsListResult struct {
	Credentials []credentialInfoDTO `json:"credentials"`
}

type credentialsListParams struct {
	Kind string `json:"kind,omitempty"`
}

type credentialsSetParams struct {
	Kind   string            `json:"kind"`
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

type credentialsDeleteParams struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (g *Gateway) credentialsService() (contracts.CredentialStore, *protocolError) {
	service, ok := g.root.Get(contracts.KeyCredentials)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "credentials unavailable")
	}
	return service, nil
}

// credentialsList returns identities only: the store's info type has no field
// map, so a secret cannot leak through this method even by accident.
func (g *Gateway) credentialsList(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.credentialsService()
	if problem != nil {
		return nil, problem
	}
	var params credentialsListParams
	if len(raw) > 0 {
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
	}
	infos, err := service.List(ctx, params.Kind)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := credentialsListResult{Credentials: make([]credentialInfoDTO, 0, len(infos))}
	for _, info := range infos {
		result.Credentials = append(result.Credentials, credentialInfoDTO{Kind: info.Kind, ID: info.ID})
	}
	return result, nil
}

func (g *Gateway) credentialsSet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.credentialsService()
	if problem != nil {
		return nil, problem
	}
	var params credentialsSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if len(params.Fields) == 0 {
		return nil, errInvalidParams("fields", "at least one field is required")
	}
	if err := service.Set(ctx, contracts.Credential{Kind: params.Kind, ID: params.ID, Fields: params.Fields}); err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"ok": true}, nil
}

func (g *Gateway) credentialsDelete(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.credentialsService()
	if problem != nil {
		return nil, problem
	}
	var params credentialsDeleteParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := service.Delete(ctx, params.Kind, params.ID); err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"ok": true}, nil
}

// --- heartbeat & browser status --------------------------------------------

type heartbeatStatusResult struct {
	Running         bool     `json:"running"`
	LeadIntervalMS  int64    `json:"leadIntervalMs"`
	ChannelInterval int64    `json:"channelIntervalMs"`
	LastTick        string   `json:"lastTick,omitempty"`
	LeadRuns        int      `json:"leadRuns"`
	Dispatched      int      `json:"dispatched"`
	Polls           int      `json:"polls"`
	NewMessages     int      `json:"newMessages"`
	Errors          []string `json:"errors,omitempty"`
}

type heartbeatTickResult struct {
	LeadRuns    int      `json:"leadRuns"`
	Dispatched  int      `json:"dispatched"`
	Polls       int      `json:"polls"`
	NewMessages int      `json:"newMessages"`
	Errors      []string `json:"errors,omitempty"`
}

func (g *Gateway) heartbeatService() (contracts.HeartbeatService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyHeartbeat)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "heartbeat unavailable")
	}
	return service, nil
}

// heartbeatStatus reports the autonomous loop: whether it runs, how often, and
// what the last pass did. It is the answer to "is the bot actually working?".
func (g *Gateway) heartbeatStatus(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, problem := g.heartbeatService()
	if problem != nil {
		return nil, problem
	}
	status, err := service.Status(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := heartbeatStatusResult{
		Running: status.Running, LeadIntervalMS: status.LeadInterval.Milliseconds(),
		ChannelInterval: status.ChannelInterval.Milliseconds(), LastTick: rfc3339(status.LastTick),
		LeadRuns: status.LastReport.LeadRuns, Dispatched: status.LastReport.Dispatched,
		Polls: status.LastReport.Polls, NewMessages: status.LastReport.NewMessages,
		Errors: status.LastReport.Errors,
	}
	return result, nil
}

// heartbeatTick runs one pass now ("马上查一次").
func (g *Gateway) heartbeatTick(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, problem := g.heartbeatService()
	if problem != nil {
		return nil, problem
	}
	report, err := service.Tick(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return heartbeatTickResult{
		LeadRuns: report.LeadRuns, Dispatched: report.Dispatched, Polls: report.Polls,
		NewMessages: report.NewMessages, Errors: report.Errors,
	}, nil
}

type browserStatusResult struct {
	Connected bool   `json:"connected"`
	Endpoint  string `json:"endpoint,omitempty"`
	Pages     int    `json:"pages"`
}

func (g *Gateway) browserStatus(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, ok := g.root.Get(contracts.KeyBrowser)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "browser unavailable")
	}
	status, err := service.Status(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return browserStatusResult{Connected: status.Connected, Endpoint: status.Endpoint, Pages: status.Pages}, nil
}

// --- shared team notes -----------------------------------------------------

type teamNoteDTO struct {
	ID         string `json:"id"`
	TaskID     string `json:"taskId,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Author     string `json:"author,omitempty"`
	At         string `json:"at,omitempty"`
	Reviewed   bool   `json:"reviewed"`
	ReviewedBy string `json:"reviewedBy,omitempty"`
	ReviewedAt string `json:"reviewedAt,omitempty"`
}

type teamNoteReviewParams struct {
	ID       string `json:"id"`
	Reviewer string `json:"reviewer"`
	Accept   *bool  `json:"accept"`
	At       string `json:"at,omitempty"`
}

type teamNotesListResult struct {
	Notes []teamNoteDTO `json:"notes"`
}

type teamNotesRecordParams struct {
	ID     string `json:"id,omitempty"`
	TaskID string `json:"taskId,omitempty"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Author string `json:"author,omitempty"`
}

func (g *Gateway) teamNotesService() (contracts.TeamNotesService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyTeamNotes)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "team notes unavailable")
	}
	return service, nil
}

func teamNoteFromContract(note contracts.TeamNote) teamNoteDTO {
	return teamNoteDTO{
		ID: note.ID, TaskID: note.TaskID, Title: note.Title, Body: note.Body,
		Author: note.Author, At: rfc3339(note.At),
		Reviewed: note.Reviewed, ReviewedBy: note.ReviewedBy, ReviewedAt: rfc3339(note.ReviewedAt),
	}
}

// teamNotes lists the shared notes (the memory the team actually reads).
func (g *Gateway) teamNotes(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.teamNotesService()
	if problem != nil {
		return nil, problem
	}
	limit := 50
	if len(raw) > 0 {
		var params struct {
			Limit int `json:"limit,omitempty"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.Limit > 0 && params.Limit <= 200 {
			limit = params.Limit
		}
	}
	notes, err := service.List(ctx, limit)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := teamNotesListResult{Notes: make([]teamNoteDTO, 0, len(notes))}
	for _, note := range notes {
		result.Notes = append(result.Notes, teamNoteFromContract(note))
	}
	return result, nil
}

// teamNoteRecord writes one shared note.
func (g *Gateway) teamNoteRecord(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.teamNotesService()
	if problem != nil {
		return nil, problem
	}
	var params teamNotesRecordParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	note, err := service.Record(ctx, contracts.TeamNote{
		ID: params.ID, TaskID: params.TaskID, Title: params.Title, Body: params.Body, Author: params.Author,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return teamNoteFromContract(note), nil
}

// --- wechat QR login -------------------------------------------------------

type wechatLoginStartResult struct {
	QRCodeID  string `json:"qrId"`
	QRCodeURL string `json:"qrUrl"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

type wechatLoginStateParams struct {
	QRCodeID string `json:"qrId"`
}

type wechatLoginStateResult struct {
	State     string `json:"state"`
	Connected bool   `json:"connected"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

func (g *Gateway) wechatLoginService() (contracts.WeChatLoginService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyWeChatLogin)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "wechat login unavailable")
	}
	return service, nil
}

// wechatLoginStart returns a QR the human scans with the WeChat app.
func (g *Gateway) wechatLoginStart(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, problem := g.wechatLoginService()
	if problem != nil {
		return nil, problem
	}
	login, err := service.StartLogin(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return wechatLoginStartResult{
		QRCodeID: login.QRCodeID, QRCodeURL: login.QRCodeURL, ExpiresAt: rfc3339(login.ExpiresAt),
	}, nil
}

// wechatLoginState polls one login. "expired" is an answer, not an error: a
// polling loop that treats it as a failure stops before the user can retry.
func (g *Gateway) wechatLoginState(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.wechatLoginService()
	if problem != nil {
		return nil, problem
	}
	var params wechatLoginStateParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	state, _, err := service.LoginState(ctx, params.QRCodeID)
	if err != nil && !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		return nil, orchestrationError(err)
	}
	connected := false
	if status, statusErr := g.wechatStatusOrZero(ctx); statusErr == nil {
		connected = status
	}
	return wechatLoginStateResult{State: string(state), Connected: connected}, nil
}

func (g *Gateway) wechatStatusOrZero(ctx context.Context) (bool, error) {
	service, ok := g.root.Get(contracts.KeyWeChat)
	if !ok || service == nil {
		return false, nil
	}
	status, err := service.Status(ctx)
	if err != nil {
		return false, err
	}
	return status.Connected, nil
}

// teamNoteReview marks one shared note as reviewed (or rejected). accept is a
// pointer on purpose: an omitted field must be refused, not read as "rejected".
func (g *Gateway) teamNoteReview(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.teamNotesService()
	if problem != nil {
		return nil, problem
	}
	var params teamNoteReviewParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Accept == nil {
		return nil, errInvalidParams("accept", "accept is required (true or false)")
	}
	at := time.Time{}
	if params.At != "" {
		parsed, err := time.Parse(time.RFC3339, params.At)
		if err != nil {
			return nil, errInvalidParams("at", "want RFC3339")
		}
		at = parsed
	}
	note, err := service.Review(ctx, contracts.TeamNoteReview{
		NoteID: params.ID, Reviewer: params.Reviewer, Accept: *params.Accept, At: at,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return teamNoteFromContract(note), nil
}

type heartbeatConfigureParams struct {
	Enabled         *bool `json:"enabled,omitempty"`
	LeadIntervalMS  int64 `json:"leadIntervalMs,omitempty"`
	ChannelInterval int64 `json:"channelIntervalMs,omitempty"`
}

// heartbeatConfigure retunes or stops the autonomous loop while it runs. It
// returns the status so the caller sees the effect instead of guessing.
func (g *Gateway) heartbeatConfigure(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.heartbeatService()
	if problem != nil {
		return nil, problem
	}
	var params heartbeatConfigureParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	update := contracts.HeartbeatConfig{Enabled: params.Enabled}
	if params.LeadIntervalMS != 0 {
		update.LeadInterval = time.Duration(params.LeadIntervalMS) * time.Millisecond
	}
	if params.ChannelInterval != 0 {
		update.ChannelInterval = time.Duration(params.ChannelInterval) * time.Millisecond
	}
	if err := service.Configure(ctx, update); err != nil {
		return nil, orchestrationError(err)
	}
	return g.heartbeatStatus(ctx, nil)
}

// --- notification delivery -------------------------------------------------

type notifyDeliverParams struct {
	Category string `json:"category"`
	Text     string `json:"text"`
}

type notifyDeliverResult struct {
	Channel  string   `json:"channel"`
	Attempts []string `json:"attempts,omitempty"`
	Deferred bool     `json:"deferred"`
	Reason   string   `json:"reason,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

type notifyPlanResult struct {
	Channels []string `json:"channels"`
	Deferred bool     `json:"deferred"`
	Reason   string   `json:"reason,omitempty"`
}

// notifyPlan shows where a category would go right now, without sending.
func (g *Gateway) notifyPlan(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	router, ok := g.root.Get(contracts.KeyNotifyRoute)
	if !ok || router == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "notification routing unavailable")
	}
	var params struct {
		Category string `json:"category"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	plan, err := router.Plan(ctx, params.Category, time.Now(), g.channelHealth(ctx))
	if err != nil {
		return nil, orchestrationError(err)
	}
	return notifyPlanResult{Channels: plan.Channels, Deferred: plan.Deferred, Reason: plan.Reason}, nil
}

// notifyDeliver sends one message through the plan and reports what actually
// happened — including the channels it fell back from.
func (g *Gateway) notifyDeliver(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	dispatcher, ok := g.root.Get(contracts.KeyChannelDispatch)
	if !ok || dispatcher == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "channel dispatch unavailable")
	}
	var params notifyDeliverParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Text) == "" {
		return nil, errInvalidParams("text", "text is required")
	}
	receipt, err := dispatcher.Deliver(ctx, params.Category, params.Text)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return notifyDeliverResult{
		Channel: receipt.Channel, Attempts: receipt.Attempts, Deferred: receipt.Deferred,
		Reason: receipt.Reason, Errors: receipt.Errors,
	}, nil
}

// channelHealth is what the router may consider reachable right now.
func (g *Gateway) channelHealth(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if service, ok := g.root.Get(contracts.KeyWeChat); ok && service != nil {
		if status, err := service.Status(ctx); err == nil {
			out[contracts.NotifyChannelWeChat] = status.Connected
		}
	}
	if service, ok := g.root.Get(contracts.KeyDingTalk); ok && service != nil {
		if status, err := service.Status(ctx); err == nil {
			out[contracts.NotifyChannelDingTalk] = status.Connected
		}
	}
	if service, ok := g.root.Get(contracts.KeyFeishu); ok && service != nil {
		if status, err := service.Status(ctx); err == nil {
			out[contracts.NotifyChannelFeishu] = status.Connected
		}
	}
	if service, ok := g.root.Get(contracts.KeyEmail); ok && service != nil {
		if status, err := service.Status(ctx); err == nil {
			out[contracts.NotifyChannelEmail] = status.Connected
		}
	}
	return out
}
