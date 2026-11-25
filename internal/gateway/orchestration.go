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
