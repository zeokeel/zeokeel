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
		errors.Is(err, contracts.ErrLeaseInvalid),
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
	return taskLeaseFromContract(lease), nil
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
