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

// taskBudgetDTO is the per-task cost ceiling (G11). The limits are not
// omitempty: 0 means "unlimited" and the client must be able to tell that from a
// missing field, while `exhausted` is always present because it is the stop the
// UI keys on (a task blocked for budget shows used/limit and asks the Lead).
type taskBudgetDTO struct {
	TaskID     string `json:"taskId"`
	MaxTokens  int    `json:"maxTokens"`
	MaxMinutes int    `json:"maxMinutes"`
	UsedTokens int    `json:"usedTokens"`
	StartedAt  string `json:"startedAt,omitempty"`
	Exhausted  bool   `json:"exhausted"`
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

func taskBudgetFromContract(budget contracts.TaskBudget) taskBudgetDTO {
	return taskBudgetDTO{
		TaskID: budget.TaskID, MaxTokens: budget.MaxTokens, MaxMinutes: budget.MaxMinutes,
		UsedTokens: budget.UsedTokens, StartedAt: rfc3339(budget.StartedAt),
		Exhausted: budget.Exhausted,
	}
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
		// Binding a session to a task is a request problem (empty session id, a
		// task id that is not an id): the settings screen must show the field,
		// not "something broke".
		errors.Is(err, contracts.ErrSessionTaskBindingInvalid),
		errors.Is(err, contracts.ErrLeaseBudgetExhausted),
		errors.Is(err, contracts.ErrInboxUnknownItem),
		errors.Is(err, contracts.ErrInboxUnknownAction),
		errors.Is(err, contracts.ErrInboxInvalid),
		errors.Is(err, contracts.ErrTriageInvalid),
		errors.Is(err, contracts.ErrApprovalTokenInvalid),
		errors.Is(err, contracts.ErrApprovalTokenExpired),
		errors.Is(err, contracts.ErrApprovalTokenUsed),
		errors.Is(err, contracts.ErrApprovalNotPending),
		errors.Is(err, contracts.ErrApprovalNoPending),
		// Bot template validation is the form's problem, not a gateway fault:
		// a bad template slug, an over-budget name, a tool outside the alphabet,
		// an unknown memory scope, or a template id that does not exist.
		// Reporting those as internal errors made the settings screen say
		// "something broke" for a typo.
		errors.Is(err, contracts.ErrBotInvalid),
		errors.Is(err, contracts.ErrBotTemplateUnknown),
		// The roster's own refusals are the form's problem too: a bad role, an
		// unknown member, or demoting the only owner all have a fix the person
		// can apply, so they must not read as "something broke".
		errors.Is(err, contracts.ErrUserInvalid),
		errors.Is(err, contracts.ErrUserUnknown),
		errors.Is(err, contracts.ErrUserLastOwner):
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
	// BotID dispatches to a bot instance: the task is owned by that bot and its
	// allowed tools become the task's tool binding.
	BotID string `json:"botId,omitempty"`
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
	if problem := g.requireAction(ctx, contracts.ActionDispatch); problem != nil {
		return nil, problem
	}
	assignee := params.Assignee
	var boundTools []string
	if params.BotID != "" {
		bots, problem := g.botService()
		if problem != nil {
			return nil, problem
		}
		bot, err := bots.Get(ctx, params.BotID)
		if err != nil {
			return nil, orchestrationError(err)
		}
		assignee = bot.ID
		boundTools = bot.AllowedTools
		// One bot, one live task: a bot that is still working cannot be handed a
		// second job, or its context becomes two conversations fighting over one
		// memory. The refusal names the task that is already in flight.
		if busy, busyTask := g.botIsBusy(ctx, bot.ID); busy {
			return nil, protocolErrorWithMessage(codeInvalidRequest,
				"bot "+bot.ID+" is already working on "+busyTask+"; wait for it or dispatch to another bot")
		}
		// Dispatching to a bot is the only thing that currently "wakes" it: the
		// LastSeenAt column had no producer, so the settings screen could never
		// tell a working bot from one that never ran.
		g.touchBot(ctx, bot)
	}
	result, err := service.Apply(ctx, params.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task: &contracts.NewTask{
			Subject: params.Subject, Detail: params.Detail,
			BlockedBy: params.BlockedBy, WriteScopes: params.WriteScopes,
			Assignee: assignee,
		},
		Note: params.Note,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	// Who asked for it (M6 attribution): the board knows the work and the lease
	// knows the executor, but only this says which human wanted it.
	if requester, ok := g.root.Get(contracts.KeyTaskRequester); ok && requester != nil && len(result.TaskIDs) > 0 {
		if acting, has := g.actingUser(ctx); has {
			if err := requester.SetRequestedBy(ctx, result.TaskIDs[0], acting.ID); err != nil {
				g.logger.Printf("gateway: record requester for %s: %v", result.TaskIDs[0], err)
			}
		}
	}
	// The bot's tool grant becomes the task's binding: a dispatch to a bot is how
	// policy stops being a note and starts being a constraint.
	if len(boundTools) > 0 && len(result.TaskIDs) > 0 {
		if leases, ok := g.root.Get(contracts.KeyTaskLease); ok && leases != nil {
			if _, bindErr := leases.SetTools(ctx, result.TaskIDs[0], boundTools); bindErr != nil {
				g.logger.Printf("gateway: bind bot tools to %s: %v", result.TaskIDs[0], bindErr)
			}
		}
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
	if problem := g.requireAction(ctx, contracts.ActionApprove); problem != nil {
		return nil, problem
	}
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
		g.proposeSkillDraft(ctx, params.ID, params.Note, lease)
	}
	// before_send holds the words: the person approved the work, not the message.
	if g.reviewNeedsSendApproval(ctx, params.ID) {
		g.holdOutbound(ctx, params.ID, params.Decision, params.Note)
	} else {
		g.notifyReview(ctx, params.Decision, params.ID, params.Note)
	}
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

// reviewNeedsSendApproval reports whether this task's policy parks the outbound
// message instead of sending it.
func (g *Gateway) reviewNeedsSendApproval(ctx context.Context, taskID string) bool {
	leases, ok := g.root.Get(contracts.KeyTaskLease)
	if !ok || leases == nil {
		return false
	}
	policy, set, err := leases.Approval(ctx, taskID)
	if err != nil || !set {
		return false
	}
	return policy == contracts.TaskApprovalBeforeSend
}

// holdOutbound parks the message a before_send task wants to send. A failure to
// park is logged, never fatal: the review already happened.
func (g *Gateway) holdOutbound(ctx context.Context, taskID, decision, note string) {
	holder, ok := g.root.Get(contracts.KeyTaskSendHolder)
	if !ok || holder == nil {
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
	if err := holder.Hold(ctx, taskID, text); err != nil {
		g.logger.Printf("gateway: hold outbound for %s: %v", taskID, err)
		return
	}
	g.logger.Printf("gateway: outbound for %s held (%s) until approved", taskID, category)
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

// --- task budget (G11) -----------------------------------------------------

// taskBudgetSetParams carries the two ceilings. Both are optional: an omitted
// field is 0, which the module reads as unlimited, so "raise the token ceiling
// and leave the clock alone" is expressible without a second method. `id` is
// required. Negative values are rejected by the module (-32600), not clamped
// here — a negative ceiling is a caller bug, not a budget.
type taskBudgetSetParams struct {
	ID         string `json:"id"`
	MaxTokens  int    `json:"maxTokens,omitempty"`
	MaxMinutes int    `json:"maxMinutes,omitempty"`
}

// taskBudgetConsumeParams requires `tokens` (no omitempty): the accounting source
// reports usage, and a missing measurement must be a malformed request rather
// than a silent 0 that looks like "no change". An explicit 0 is legal — it is how
// a caller re-checks a time budget without reporting new tokens.
type taskBudgetConsumeParams struct {
	ID     string `json:"id"`
	Tokens int    `json:"tokens"`
}

// tasks.budget.set declares a task's ceiling and is also how the Lead resumes a
// stopped task (the claim goes back to claimed when there is headroom).
func (g *Gateway) taskBudgetSet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	if problem := g.requireAction(ctx, contracts.ActionDecideBudget); problem != nil {
		return nil, problem
	}
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskBudgetSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	budget, err := service.SetBudget(ctx, params.ID, contracts.TaskBudget{
		MaxTokens: params.MaxTokens, MaxMinutes: params.MaxMinutes,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskBudgetFromContract(budget), nil
}

// tasks.budget.get is the read half. A task with no budget answers
// {"budget":null} — the same absent shape as tasks.lease / tasks.tools.get — but
// a task whose usage was reported against an unlimited budget answers the row,
// because "spent 1200 tokens, no ceiling" is information the task screen needs.
func (g *Gateway) taskBudgetGet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params leaseReadParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	budget, found, err := service.Budget(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !found {
		return map[string]any{"budget": nil}, nil
	}
	return taskBudgetFromContract(budget), nil
}

// tasks.budget.consume reports usage and applies the stop. Crossing a ceiling is
// a typed domain refusal (-32600, with used/limit in the message): the write did
// happen — the tokens are counted — so the client re-reads the row to show it,
// and the task is already blocked by the time this returns.
func (g *Gateway) taskBudgetConsume(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.leaseService()
	if problem != nil {
		return nil, problem
	}
	var params taskBudgetConsumeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	budget, err := service.Consume(ctx, params.ID, params.Tokens)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return taskBudgetFromContract(budget), nil
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
	if problem := g.requireAction(ctx, contracts.ActionReviewMemory); problem != nil {
		return nil, problem
	}
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

// proposeSkillDraft is the plan's G12 trigger: a finished task proposes a skill.
// It proposes, never installs — the draft is pending until a human accepts it, and
// a failure here never touches the review that already happened.
func (g *Gateway) proposeSkillDraft(ctx context.Context, taskID, note string, lease contracts.TaskLease) {
	drafts, ok := g.root.Get(contracts.KeySkillDrafts)
	if !ok || drafts == nil {
		return
	}
	name := skillNameFromTaskID(taskID)
	if name == "" {
		return
	}
	summary := "从任务 " + taskID + " 沉淀的操作流程。"
	body := "任务 " + taskID + " 已验收。"
	if strings.TrimSpace(note) != "" {
		body += "\n\n验收意见：" + note
	}
	if len(lease.Evidence) > 0 {
		body += "\n\n完成时的证据："
		for _, evidence := range lease.Evidence {
			body += "\n- " + evidence.Kind + ": " + evidence.Ref
		}
	}
	if _, err := drafts.Draft(ctx, contracts.TaskSkillDraft{
		TaskID: taskID, Name: name, Summary: summary, Body: body,
	}); err != nil {
		g.logger.Printf("gateway: propose skill draft for %s: %v", taskID, err)
	}
}

// skillNameFromTaskID projects a task id onto the skill-name alphabet
// ([a-z0-9-], 2..64). An id that cannot be projected is not turned into a
// mangled name — no draft is better than a wrong one.
func skillNameFromTaskID(taskID string) string {
	lowered := strings.ToLower(strings.TrimSpace(taskID))
	var builder strings.Builder
	lastDash := false
	for _, r := range lowered {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	name := strings.Trim(builder.String(), "-")
	if len(name) > 56 {
		name = strings.Trim(name[:56], "-")
	}
	if len(name) < 2 {
		return ""
	}
	return name
}

// --- skill drafts ----------------------------------------------------------

type skillDraftDTO struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId,omitempty"`
	Name      string `json:"name"`
	Summary   string `json:"summary,omitempty"`
	Body      string `json:"body,omitempty"`
	Status    string `json:"status"`
	Reviewer  string `json:"reviewer,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type skillDraftsListResult struct {
	Drafts []skillDraftDTO `json:"drafts"`
}

type skillDraftProposeParams struct {
	TaskID  string `json:"taskId,omitempty"`
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body,omitempty"`
}

type skillDraftDecideParams struct {
	ID       string `json:"id"`
	Accept   *bool  `json:"accept"`
	Reviewer string `json:"reviewer"`
}

func skillDraftFromContract(draft contracts.TaskSkillDraft) skillDraftDTO {
	return skillDraftDTO{
		ID: draft.ID, TaskID: draft.TaskID, Name: draft.Name, Summary: draft.Summary,
		Body: draft.Body, Status: draft.Status, Reviewer: draft.Reviewer, CreatedAt: rfc3339(draft.CreatedAt),
	}
}

func (g *Gateway) skillDraftsService() (contracts.SkillDraftsService, *protocolError) {
	service, ok := g.root.Get(contracts.KeySkillDrafts)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "skill drafts unavailable")
	}
	return service, nil
}

func (g *Gateway) skillDraftsList(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.skillDraftsService()
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
	drafts, err := service.List(ctx, limit)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := skillDraftsListResult{Drafts: make([]skillDraftDTO, 0, len(drafts))}
	for _, draft := range drafts {
		result.Drafts = append(result.Drafts, skillDraftFromContract(draft))
	}
	return result, nil
}

func (g *Gateway) skillDraftPropose(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.skillDraftsService()
	if problem != nil {
		return nil, problem
	}
	var params skillDraftProposeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	draft, err := service.Draft(ctx, contracts.TaskSkillDraft{
		TaskID: params.TaskID, Name: params.Name, Summary: params.Summary, Body: params.Body,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return skillDraftFromContract(draft), nil
}

// skillDraftDecide accepts or rejects a proposal. accept is a pointer so an
// omitted field is refused instead of read as a rejection.
func (g *Gateway) skillDraftDecide(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.skillDraftsService()
	if problem != nil {
		return nil, problem
	}
	var params skillDraftDecideParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if params.Accept == nil {
		return nil, errInvalidParams("accept", "accept is required (true or false)")
	}
	draft, err := service.Decide(ctx, params.ID, *params.Accept, params.Reviewer)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return skillDraftFromContract(draft), nil
}

type skillDraftInstallParams struct {
	ID string `json:"id"`
}

// skillDraftInstall turns an accepted draft into a real skill file. Accepting a
// draft is a decision; installing is a second, explicit act — and a name that
// already exists is refused rather than silently overwritten.
func (g *Gateway) skillDraftInstall(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	drafts, problem := g.skillDraftsService()
	if problem != nil {
		return nil, problem
	}
	var params skillDraftInstallParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	editor, ok := g.root.Get(contracts.KeySkillsEditor)
	if !ok || editor == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "skills editor unavailable")
	}
	list, err := drafts.List(ctx, 200)
	if err != nil {
		return nil, orchestrationError(err)
	}
	var found *contracts.TaskSkillDraft
	for i := range list {
		if list[i].ID == params.ID {
			found = &list[i]
			break
		}
	}
	if found == nil {
		return nil, orchestrationError(contracts.ErrSkillDraftUnknown)
	}
	if found.Status != contracts.SkillDraftAccepted {
		return nil, errInvalidParams("id", "only an accepted draft can be installed")
	}
	skill, err := editor.CreateSkill(ctx, contracts.SkillDraft{
		Name: found.Name, Description: found.Summary, Body: found.Body,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"installed": true, "name": skill.Name}, nil
}

// botIsBusy reports the task a bot is currently holding, if any. A lease in
// claimed/awaiting_approval/blocked counts as busy: the task is its owner's until
// it settles or expires, and handing the bot a second job would split its
// attention.
func (g *Gateway) botIsBusy(ctx context.Context, botID string) (bool, string) {
	leases, ok := g.root.Get(contracts.KeyTaskLease)
	if !ok || leases == nil || botID == "" {
		return false, ""
	}
	board, ok := g.root.Get(contracts.KeyTaskBoard)
	if !ok || board == nil {
		return false, ""
	}
	tasks, err := board.List(ctx)
	if err != nil {
		return false, ""
	}
	for _, task := range tasks {
		if task.Owner != botID {
			continue
		}
		lease, held, err := leases.Lease(ctx, task.ID)
		if err != nil || !held {
			continue
		}
		switch lease.State {
		case contracts.LeaseStateClaimed, contracts.LeaseStateAwaitingApproval, contracts.LeaseStateBlocked:
			return true, task.ID
		}
	}
	return false, ""
}

// touchBot records that a bot was just handed work. Best-effort: a failed write
// must not fail the dispatch that already created the task.
func (g *Gateway) touchBot(ctx context.Context, bot contracts.BotInstance) {
	bots, ok := g.root.Get(contracts.KeyBots)
	if !ok || bots == nil {
		return
	}
	bot.LastSeenAt = time.Now()
	if _, err := bots.Update(ctx, bot); err != nil {
		g.logger.Printf("gateway: touch bot %s: %v", bot.ID, err)
	}
}

// --- bots ------------------------------------------------------------------

type botTemplateDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        string   `json:"role,omitempty"`
	Description string   `json:"description,omitempty"`
	Model       string   `json:"defaultModel,omitempty"`
	Prompt      string   `json:"systemPrompt,omitempty"`
	Tools       []string `json:"allowedTools,omitempty"`
	MemoryScope string   `json:"memoryScope,omitempty"`
}

type botInstanceDTO struct {
	ID          string   `json:"id"`
	TemplateID  string   `json:"templateId"`
	Name        string   `json:"name"`
	Owner       string   `json:"owner,omitempty"`
	Model       string   `json:"defaultModel,omitempty"`
	Tools       []string `json:"allowedTools,omitempty"`
	MemoryScope string   `json:"memoryScope,omitempty"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"createdAt,omitempty"`
	LastSeenAt  string   `json:"lastSeenAt,omitempty"`
}

type botsSnapshotResult struct {
	Templates []botTemplateDTO `json:"templates"`
	Instances []botInstanceDTO `json:"instances"`
}

type botsUpsertParams struct {
	ID          string   `json:"id"`
	TemplateID  string   `json:"templateId"`
	Name        string   `json:"name,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Model       string   `json:"defaultModel,omitempty"`
	Tools       []string `json:"allowedTools,omitempty"`
	MemoryScope string   `json:"memoryScope,omitempty"`
	Status      string   `json:"status,omitempty"`
}

// botTemplatePutParams is the editable form of a岗位 template. Only id and name
// are required; the rest has a meaningful empty value (an empty DefaultModel is
// "follow the session", an empty AllowedTools is the read-only default set, an
// empty MemoryScope is agent). The write is a replace, so an omitted optional
// field is cleared — the screen edits a full form it read from
// bots.template.get / bots.snapshot.
type botTemplatePutParams struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Role         string   `json:"role,omitempty"`
	Description  string   `json:"description,omitempty"`
	DefaultModel string   `json:"defaultModel,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	MemoryScope  string   `json:"memoryScope,omitempty"`
}

type botTemplateGetParams struct {
	ID string `json:"id"`
}

// botTemplateGetResult is `{template:...}` with an explicit null. An unknown id
// is an ordinary answer here (the form is empty, not broken), and keeping the
// key present means the client never has to distinguish "no template" from
// "field dropped by the encoder".
type botTemplateGetResult struct {
	Template *botTemplateDTO `json:"template"`
}

func botTemplateFromContract(template contracts.BotTemplate) botTemplateDTO {
	return botTemplateDTO{
		ID: template.ID, Name: template.Name, Role: template.Role, Description: template.Description,
		Model: template.DefaultModel, Prompt: template.SystemPrompt, Tools: template.AllowedTools,
		MemoryScope: template.MemoryScope,
	}
}

func botInstanceFromContract(instance contracts.BotInstance) botInstanceDTO {
	return botInstanceDTO{
		ID: instance.ID, TemplateID: instance.TemplateID, Name: instance.Name, Owner: instance.Owner,
		Model: instance.DefaultModel, Tools: instance.AllowedTools, MemoryScope: instance.MemoryScope,
		Status: instance.Status, CreatedAt: rfc3339(instance.CreatedAt), LastSeenAt: rfc3339(instance.LastSeenAt),
	}
}

func (g *Gateway) botService() (contracts.BotService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyBots)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "bots unavailable")
	}
	return service, nil
}

// botsSnapshot lists templates and instances together: a screen that shows one
// without the other cannot answer "which bot can I dispatch to?".
func (g *Gateway) botsSnapshot(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	service, problem := g.botService()
	if problem != nil {
		return nil, problem
	}
	templates, err := service.Templates(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	instances, err := service.Instances(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := botsSnapshotResult{
		Templates: make([]botTemplateDTO, 0, len(templates)),
		Instances: make([]botInstanceDTO, 0, len(instances)),
	}
	for _, template := range templates {
		result.Templates = append(result.Templates, botTemplateFromContract(template))
	}
	for _, instance := range instances {
		result.Instances = append(result.Instances, botInstanceFromContract(instance))
	}
	return result, nil
}

// botsUpsert creates or updates one instance (the natural flow is snapshot →
// modify → upsert).
func (g *Gateway) botsUpsert(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	if problem := g.requireAction(ctx, contracts.ActionManageBots); problem != nil {
		return nil, problem
	}
	service, problem := g.botService()
	if problem != nil {
		return nil, problem
	}
	var params botsUpsertParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	instance := contracts.BotInstance{
		ID: params.ID, TemplateID: params.TemplateID, Name: params.Name, Owner: params.Owner,
		DefaultModel: params.Model, AllowedTools: params.Tools, MemoryScope: params.MemoryScope,
		Status: params.Status,
	}
	existing, getErr := service.Get(ctx, params.ID)
	var saved contracts.BotInstance
	var err error
	if getErr == nil && existing.ID != "" {
		// Update requires the identity fields from the stored row.
		instance.TemplateID = existing.TemplateID
		instance.CreatedAt = existing.CreatedAt
		saved, err = service.Update(ctx, instance)
	} else {
		saved, err = service.Create(ctx, instance)
	}
	if err != nil {
		return nil, orchestrationError(err)
	}
	return botInstanceFromContract(saved), nil
}

// botsTemplatePut creates or replaces one reusable岗位 template (plan G1: the
// template is the reusable half of "Bot = 模板 + 记忆作用域 + 工具授予 + 默认模型").
//
// Two properties the caller may rely on, both pinned by tests:
//
//   - It is create-or-replace, not a patch. The stored template is exactly what
//     this request described; a field left out is cleared. Callers that mean
//     "change one field" must send the record back (get → modify → put).
//   - It never reshapes a bot that already exists. Instances store the model,
//     tool whitelist and memory scope they resolved at creation, so a template
//     edit applies to bots created afterwards only. A tool grant is a permission
//     surface: editing a template must not widen it behind the user's back.
func (g *Gateway) botsTemplatePut(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.botService()
	if problem != nil {
		return nil, problem
	}
	var params botTemplatePutParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	saved, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           params.ID,
		Name:         params.Name,
		Role:         params.Role,
		Description:  params.Description,
		DefaultModel: params.DefaultModel,
		SystemPrompt: params.SystemPrompt,
		AllowedTools: params.AllowedTools,
		MemoryScope:  params.MemoryScope,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return botTemplateFromContract(saved), nil
}

// botsTemplateGet reads one template for the edit form. An unknown id answers
// `{template:null}` rather than an error: the form is still open and empty, and
// "that template is gone" is a normal thing to discover after a delete or a
// restart with another store. The read is a snapshot of one template, while
// bots.snapshot remains the door for everything at once (templates + instances).
func (g *Gateway) botsTemplateGet(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.botService()
	if problem != nil {
		return nil, problem
	}
	var params botTemplateGetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	template, found, err := service.Template(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !found {
		return botTemplateGetResult{}, nil
	}
	dto := botTemplateFromContract(template)
	return botTemplateGetResult{Template: &dto}, nil
}

type taskSendPendingParams struct {
	ID string `json:"id"`
}

// taskSendPending reports the message a before_send task is holding, so the task
// screen can say "there is something waiting for you" instead of the person only
// finding out by trying to approve.
func (g *Gateway) taskSendPending(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	holder, ok := g.root.Get(contracts.KeyTaskSendHolder)
	if !ok || holder == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "held sends unavailable")
	}
	var params taskSendPendingParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	text, found, err := holder.Held(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !found {
		return map[string]any{"held": false}, nil
	}
	return map[string]any{"held": true, "text": text}, nil
}

type taskSendApproveParams struct {
	ID       string `json:"id"`
	Approver string `json:"approver"`
}

// taskSendApprove lets a held message leave. Approval is the whole point of the
// before_send policy, so an unknown task or an empty approver is refused rather
// than treated as consent.
func (g *Gateway) taskSendApprove(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	holder, ok := g.root.Get(contracts.KeyTaskSendHolder)
	if !ok || holder == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "held sends unavailable")
	}
	var params taskSendApproveParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Approver) == "" {
		return nil, errInvalidParams("approver", "approver is required")
	}
	text, err := holder.ApproveSend(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	dispatcher, ok := g.root.Get(contracts.KeyChannelDispatch)
	if !ok || dispatcher == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "channel dispatch unavailable")
	}
	receipt, err := dispatcher.Deliver(ctx, contracts.NotifyCategoryTaskDone, text)
	if err != nil {
		// The message left the hold but did not reach a channel; report it rather
		// than pretending the send happened.
		return nil, orchestrationError(err)
	}
	return map[string]any{
		"sent": true, "channel": receipt.Channel, "attempts": receipt.Attempts,
	}, nil
}

// --- team note suggestions (M1 queue) --------------------------------------

type noteSuggestionDTO struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Author string `json:"author,omitempty"`
	At     string `json:"at,omitempty"`
}

type noteSuggestionsResult struct {
	Suggestions []noteSuggestionDTO `json:"suggestions"`
}

type noteSuggestParams struct {
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Author string `json:"author,omitempty"`
}

type noteSuggestionDecisionParams struct {
	ID       string `json:"id"`
	Reviewer string `json:"reviewer"`
}

func noteSuggestionFromContract(suggestion contracts.TeamNoteSuggestion) noteSuggestionDTO {
	return noteSuggestionDTO{
		ID: suggestion.ID, Title: suggestion.Title, Body: suggestion.Body,
		Author: suggestion.Author, At: rfc3339(suggestion.At),
	}
}

func (g *Gateway) noteSuggestionsService() (contracts.TeamNoteSuggestions, *protocolError) {
	service, ok := g.root.Get(contracts.KeyTeamNoteSuggestions)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "team note suggestions unavailable")
	}
	return service, nil
}

func (g *Gateway) noteSuggestions(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.noteSuggestionsService()
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
	suggestions, err := service.Suggestions(ctx, limit)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := noteSuggestionsResult{Suggestions: make([]noteSuggestionDTO, 0, len(suggestions))}
	for _, suggestion := range suggestions {
		result.Suggestions = append(result.Suggestions, noteSuggestionFromContract(suggestion))
	}
	return result, nil
}

func (g *Gateway) noteSuggest(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.noteSuggestionsService()
	if problem != nil {
		return nil, problem
	}
	var params noteSuggestParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	suggestion, err := service.Suggest(ctx, contracts.TeamNoteSuggestion{
		Title: params.Title, Body: params.Body, Author: params.Author,
	})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return noteSuggestionFromContract(suggestion), nil
}

func (g *Gateway) noteSuggestionPromote(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.noteSuggestionsService()
	if problem != nil {
		return nil, problem
	}
	var params noteSuggestionDecisionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	note, err := service.Promote(ctx, params.ID, params.Reviewer)
	if err != nil {
		return nil, orchestrationError(err)
	}
	return teamNoteFromContract(note), nil
}

func (g *Gateway) noteSuggestionDismiss(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	service, problem := g.noteSuggestionsService()
	if problem != nil {
		return nil, problem
	}
	var params noteSuggestionDecisionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := service.Dismiss(ctx, params.ID, params.Reviewer); err != nil {
		return nil, orchestrationError(err)
	}
	return map[string]any{"ok": true}, nil
}

// --- users (M6) ------------------------------------------------------------

type userDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type usersSnapshotResult struct {
	Users        []userDTO `json:"users"`
	ActingUserID string    `json:"actingUserId,omitempty"`
	ActingRole   string    `json:"actingRole,omitempty"`
}

type usersUpsertParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type usersActParams struct {
	ID string `json:"id"`
}

// actingUserSettingKey stores which roster member this browser session acts as.
// The product has one human today, so it defaults to the seeded owner; it is a
// settings row rather than a login session because M6 roles are a policy table,
// not an authentication system — the gateway trusts the local client.
func actingUserSettingKey() string { return "gateway.actingUser" }

func (g *Gateway) actingUser(ctx context.Context) (contracts.User, bool) {
	users, ok := g.root.Get(contracts.KeyUsers)
	if !ok || users == nil {
		return contracts.User{}, false
	}
	roster, err := users.Users(ctx)
	if err != nil || len(roster) == 0 {
		return contracts.User{}, false
	}
	acting := ""
	if settings, ok := g.root.Get(contracts.KeySettings); ok && settings != nil {
		acting = strings.TrimSpace(settings.Get(actingUserSettingKey(), ""))
	}
	if acting != "" {
		for _, user := range roster {
			if user.ID == acting {
				return user, true
			}
		}
	}
	// Fall back to the first owner: a roster always has one (the store refuses to
	// lose the last), so the product never becomes unusable after a bad setting.
	for _, user := range roster {
		if user.Role == contracts.UserRoleOwner {
			return user, true
		}
	}
	return roster[0], true
}

// requireAction refuses a privileged call the acting user may not make. When no
// roster exists at all (a product built without the users module) the check is
// inert, exactly like every other optional dependency here.
func (g *Gateway) requireAction(ctx context.Context, action contracts.UserAction) *protocolError {
	user, ok := g.actingUser(ctx)
	if !ok {
		return nil
	}
	if contracts.Allowed(user.Role, action) {
		return nil
	}
	return protocolErrorWithMessage(codeInvalidRequest,
		"user "+user.ID+" ("+user.Role+") may not "+string(action))
}

func (g *Gateway) usersSnapshot(ctx context.Context, _ json.RawMessage) (any, *protocolError) {
	users, ok := g.root.Get(contracts.KeyUsers)
	if !ok || users == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "users unavailable")
	}
	roster, err := users.Users(ctx)
	if err != nil {
		return nil, orchestrationError(err)
	}
	result := usersSnapshotResult{Users: make([]userDTO, 0, len(roster))}
	for _, user := range roster {
		result.Users = append(result.Users, userDTO{
			ID: user.ID, Name: user.Name, Role: user.Role, CreatedAt: rfc3339(user.CreatedAt),
		})
	}
	if acting, ok := g.actingUser(ctx); ok {
		result.ActingUserID = acting.ID
		result.ActingRole = acting.Role
	}
	return result, nil
}

func (g *Gateway) usersUpsert(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	if problem := g.requireAction(ctx, contracts.ActionManageBots); problem != nil {
		return nil, problem
	}
	users, ok := g.root.Get(contracts.KeyUsers)
	if !ok || users == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "users unavailable")
	}
	var params usersUpsertParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	user, err := users.Put(ctx, contracts.User{ID: params.ID, Name: params.Name, Role: params.Role})
	if err != nil {
		return nil, orchestrationError(err)
	}
	return userDTO{ID: user.ID, Name: user.Name, Role: user.Role, CreatedAt: rfc3339(user.CreatedAt)}, nil
}

// usersAct records which roster member this browser acts as.
func (g *Gateway) usersAct(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	users, ok := g.root.Get(contracts.KeyUsers)
	if !ok || users == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "users unavailable")
	}
	var params usersActParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if _, err := users.Get(ctx, params.ID); err != nil {
		return nil, orchestrationError(err)
	}
	if settings, ok := g.root.Get(contracts.KeySettings); ok && settings != nil {
		if err := settings.Set(actingUserSettingKey(), params.ID); err != nil {
			return nil, orchestrationError(err)
		}
	}
	return map[string]any{"actingUserId": params.ID}, nil
}

type taskRequestedByParams struct {
	ID string `json:"id"`
}

// taskRequestedBy answers which human asked for a task. The board knows the work
// and the lease the executor; this is the third leg of attribution.
func (g *Gateway) taskRequestedBy(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	requester, ok := g.root.Get(contracts.KeyTaskRequester)
	if !ok || requester == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "task attribution unavailable")
	}
	var params taskRequestedByParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	userID, found, err := requester.RequestedBy(ctx, params.ID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	if !found {
		return map[string]any{"requestedBy": nil}, nil
	}
	return map[string]any{"requestedBy": userID}, nil
}
