package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// serverCapabilities is what this gateway tells the client it supports. A
// capability that is not listed must not render its UI entry (the browser
// counterpart of the desktop "missing service key ⇒ hidden surface" rule).
// Everything here is implemented today; the remaining T1/T2 halves appear as
// they land, never before (§4.2, §8.4).
var serverCapabilities = []string{
	"session.list",
	"session.snapshot",
	"session.delta",
	"session.subscribe",
	"session.create",
	"run.cancel",
	"approvals",
	"ask",
	"plan",
	"artifacts",
	"settings",
	"settings.set",
	"workspace",
	"prompts",
	"notify",
	"foreground",
	// Orchestration: the inbox is the single external entry point, triage turns
	// its items into work, the lease layer owns claims/evidence/review, and the
	// approval action answers a pending request from any entry point.
	"inbox",
	"triage",
	"tasklease",
	"approvalaction",
	"notifyroute",
	"webhook",
	"lead",
	"credentials",
	"heartbeat",
	"browser",
	"teamnotes",
	"wechatlogin",
	"channeldispatch",
	"skilldrafts",
	"bots",
	"users",
}

// gatewayCapabilities is the handshake's capability list for this gateway
// instance: the static set above, plus the capabilities whose existence depends
// on the assembly. The client uses this list — never a probe method — to decide
// whether to render an entry (docs/TRANSPORT.md §4.2.1, the browser counterpart
// of the desktop rule "missing service key ⇒ hidden surface").
//
// The rule this encodes: **every domain or run mode that changes what the UI
// renders needs an entry here.** Otherwise the client's only options are to
// guess always-on, or to try and disable after a failure — i.e. to demote
// capability detection into trial and error.
func (g *Gateway) gatewayCapabilities() []string {
	caps := append([]string(nil), serverCapabilities...)
	for _, conditional := range conditionalCapabilities {
		if g.hasConditionalCapability(conditional.When) {
			caps = append(caps, conditional.Name)
		}
	}
	return caps
}

// hasConditionalCapability resolves one conditional capability's marker. The
// markers are the Go symbols conditionalCapabilities names; keeping the switch
// here (instead of per-capability code in gatewayCapabilities) means the
// generated contract and the handshake cannot drift.
func (g *Gateway) hasConditionalCapability(when string) bool {
	switch when {
	case "KeyContext":
		inspector, ok := g.root.Get(contracts.KeyContext)
		return ok && inspector != nil
	case "KeyTeam":
		team, ok := g.root.Get(contracts.KeyTeam)
		return ok && team != nil
	case "KeyUpdate":
		update, ok := g.root.Get(contracts.KeyUpdate)
		return ok && update != nil
	case "KeyTaskBoard":
		board, ok := g.root.Get(contracts.KeyTaskBoard)
		return ok && board != nil
	case "SessionRegenerator":
		chat, err := g.chatService()
		if err != nil {
			return false
		}
		_, ok := chat.(contracts.SessionRegenerator)
		return ok
	case "SessionEditor":
		chat, err := g.chatService()
		if err != nil {
			return false
		}
		_, ok := chat.(contracts.SessionEditor)
		return ok
	}
	// An unknown marker must not silently grant a capability.
	return false
}

// limitsDTO is the initialize result's limits block (§4.2).
type limitsDTO struct {
	MaxFrameBytes       int `json:"maxFrameBytes"`
	MaxAttachmentBytes  int `json:"maxAttachmentBytes"`
	SnapshotTailDefault int `json:"snapshotTailDefault"`
	DeltaRingBytes      int `json:"deltaRingBytes"`
}

// initializeParams is the handshake the client must send first.
// Optional params carry ",omitempty" so the generated contract (contract.json)
// can tell required from optional from the tag alone: a field without omitempty
// is one the handler rejects when empty.
type initializeParams struct {
	ClientName      string   `json:"clientName,omitempty"`
	ClientVersion   string   `json:"clientVersion,omitempty"`
	ProtocolVersion int      `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
}

// initializeResult is the handshake answer.
type initializeResult struct {
	Epoch           string    `json:"epoch"`
	ProtocolVersion int       `json:"protocolVersion"`
	ServerVersion   string    `json:"serverVersion"`
	Capabilities    []string  `json:"capabilities"`
	Limits          limitsDTO `json:"limits"`
}

// sessionParams is the common {sessionId} request body.
type sessionParams struct {
	SessionID string `json:"sessionId"`
}

// sessionSnapshotParams is the session.snapshot body.
type sessionSnapshotParams struct {
	SessionID       string `json:"sessionId"`
	Tail            int    `json:"tail,omitempty"`
	BeforeMessageID string `json:"beforeMessageId,omitempty"`
}

// sessionSnapshotResult is the session.snapshot answer.
type sessionSnapshotResult struct {
	Epoch           string     `json:"epoch"`
	Rev             uint64     `json:"rev"`
	Session         sessionDTO `json:"session"`
	Truncated       bool       `json:"truncated"`
	OldestMessageID string     `json:"oldestMessageId,omitempty"`
}

// listSnapshotResult is the session.list.snapshot answer.
type listSnapshotResult struct {
	Epoch    string           `json:"epoch"`
	Rev      uint64           `json:"rev"`
	Sessions []sessionInfoDTO `json:"sessions"`
}

// subscribeParams is the session.subscribe body.
type subscribeParams struct {
	SessionID string `json:"sessionId"`
	SinceRev  uint64 `json:"sinceRev,omitempty"`
}

// subscribeResult is the session.subscribe answer.
type subscribeResult struct {
	Epoch  string `json:"epoch"`
	Rev    uint64 `json:"rev"`
	Resync bool   `json:"resync"`
}

// activeSessionResult is the session.active.get answer.
type activeSessionResult struct {
	SessionID string `json:"sessionId"`
}

// pingResult is the ping answer.
type pingResult struct {
	Pong  bool   `json:"pong"`
	Epoch string `json:"epoch"`
}

// askAnswerParams is the ask.answer / ask.cancel body.
type askAnswerParams struct {
	ID     string `json:"id"`
	Answer string `json:"answer,omitempty"`
}

// planExitParams is the plan.exit body.
type planExitParams struct {
	Action string `json:"action"`
}

// planExitResult is the plan.exit answer.
type planExitResult struct {
	State string `json:"state"`
}

// notifyActionParams is the notify.action body.
type notifyActionParams struct {
	ID       string `json:"id"`
	ActionID string `json:"actionId,omitempty"`
}

// foregroundParams is the foreground.set body.
type foregroundParams struct {
	SessionID string `json:"sessionId"`
}

// dispatch runs one method. Every failure is a *protocolError with a stable
// code: the browser has no errors.Is, so it branches on codes and names, never
// on English text (§4.3).
// methodHandler is one dispatch entry.
type methodHandler func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError)

// methodTable is the control plane's dispatch table: the single place a method
// becomes callable. SupportedMethods exposes its keys, and
// TestTransportMethodsMatchDispatch asserts them against
// docs/TRANSPORT.md §5.1, so a method cannot exist in the document and the
// implementation only one at a time.
func (g *Gateway) methodTable() map[string]methodHandler {
	return map[string]methodHandler{
		"initialize": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.initialize(c, request.Params)
		},
		"ready": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			c.markReady()
			return struct{}{}, nil
		},
		"ping": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return pingResult{Pong: true, Epoch: g.epoch}, nil
		},
		"session.list.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.sessionListSnapshot(ctx)
		},
		"session.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.sessionSnapshot(ctx, request.Params)
		},
		"session.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.sessionSubscribe(ctx, c, request.Params)
		},
		"session.unsubscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.sessionUnsubscribe(ctx, c, request.Params)
		},
		"session.create": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.createSession(ctx, request.Params)
		},
		"session.rename": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.renameSession(ctx, request.Params)
		},
		"session.delete": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.deleteSession(ctx, request.Params)
		},
		"session.archive": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.setSessionFlag(ctx, request.Params, "archive")
		},
		"session.pin": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.setSessionFlag(ctx, request.Params, "pin")
		},
		"session.bindtask": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.bindSessionTask(ctx, request.Params)
		},
		"session.preset.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.getSessionPreset(ctx, request.Params)
		},
		"session.preset.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.setSessionPreset(ctx, request.Params)
		},
		"mcp.reload": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.reloadConnectors(ctx, request.Params)
		},
		"approvals.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.approvalsSnapshot(ctx)
		},
		"approvals.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.domainSubscribe(ctx, c, domainApprovals, request.Params)
		},
		"approval.resolve": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.resolveApproval(request.Params)
		},
		"ask.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.askSnapshot(ctx)
		},
		"ask.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.domainSubscribe(ctx, c, domainAsk, request.Params)
		},
		"plan.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.planSnapshot(ctx)
		},
		"plan.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.domainSubscribe(ctx, c, domainPlan, request.Params)
		},
		"artifacts.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.artifactsSnapshot(ctx)
		},
		"settings.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.settingsSnapshot(ctx)
		},
		"settings.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.domainSubscribe(ctx, c, domainSettings, request.Params)
		},
		"settings.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.setSetting(ctx, request.Params)
		},
		"workspace.list.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.workspacesSnapshot(ctx)
		},
		"prompts.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.promptsSnapshot(ctx, request.Params)
		},
		"prompts.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.promptsSubscribe(ctx, c, request.Params)
		},
		"prompts.unsubscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.promptsUnsubscribe(ctx, c, request.Params)
		},
		"prompts.enqueue": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.promptsEnqueue(request.Params)
		},
		"prompts.remove": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.promptsRemove(request.Params)
		},
		"prompts.move": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.promptsMove(request.Params)
		},
		"context.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.contextSnapshot(ctx, request.Params)
		},
		"context.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.contextSubscribe(ctx, c, request.Params)
		},
		"context.unsubscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.contextUnsubscribe(ctx, c, request.Params)
		},
		"tasks.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.tasksSnapshot(ctx, request.Params)
		},
		"tasks.subscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.tasksSubscribe(ctx, c, request.Params)
		},
		"tasks.unsubscribe": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.tasksUnsubscribe(ctx, c, request.Params)
		},
		"task.status": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskSetStatus(ctx, request.Params)
		},
		"inbox.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.inboxSnapshot(ctx, request.Params)
		},
		"inbox.dispatch": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.inboxDispatch(ctx, request.Params)
		},
		"inbox.triage": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.inboxTriage(ctx, request.Params)
		},
		"tasks.claim": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskClaim(ctx, request.Params)
		},
		"tasks.lease": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskLease(ctx, request.Params)
		},
		"tasks.heartbeat": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskHeartbeat(ctx, request.Params)
		},
		"tasks.submit": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskSubmit(ctx, request.Params)
		},
		"tasks.reviews": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskReviews(ctx, request.Params)
		},
		"tasks.review": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskReview(ctx, request.Params)
		},
		"tasks.tools.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskToolsSet(ctx, request.Params)
		},
		"tasks.tools.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskToolsGet(ctx, request.Params)
		},
		"tasks.approval.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskApprovalSet(ctx, request.Params)
		},
		"tasks.approval.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskApprovalGet(ctx, request.Params)
		},
		"tasks.approve": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskApprove(ctx, request.Params)
		},
		"tasks.reject-start": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskRejectStart(ctx, request.Params)
		},
		"tasks.requestedby.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskRequestedBy(ctx, request.Params)
		},
		"tasks.send.pending": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskSendPending(ctx, request.Params)
		},
		"tasks.send.approve": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskSendApprove(ctx, request.Params)
		},
		"tasks.budget.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskBudgetSet(ctx, request.Params)
		},
		"tasks.budget.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskBudgetGet(ctx, request.Params)
		},
		"tasks.budget.consume": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskBudgetConsume(ctx, request.Params)
		},
		"approval.token": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.approvalToken(ctx, request.Params)
		},
		"approval.action": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.approvalAction(ctx, request.Params)
		},
		"notify.route.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.notifyRoutesSnapshot(ctx, request.Params)
		},
		"notify.route.upsert": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.notifyRouteUpsert(ctx, request.Params)
		},
		"credentials.list": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.credentialsList(ctx, request.Params)
		},
		"credentials.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.credentialsSet(ctx, request.Params)
		},
		"credentials.delete": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.credentialsDelete(ctx, request.Params)
		},
		"webhook.sources": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.webhookSources(ctx, request.Params)
		},
		"webhook.source.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.webhookSourceSet(ctx, request.Params)
		},
		"webhook.source.remove": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.webhookSourceRemove(ctx, request.Params)
		},
		"lead.run": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.leadRun(ctx, request.Params)
		},
		"wechat.login.start": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.wechatLoginStart(ctx, request.Params)
		},
		"wechat.login.state": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.wechatLoginState(ctx, request.Params)
		},
		"notify.plan": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.notifyPlan(ctx, request.Params)
		},
		"notify.deliver": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.notifyDeliver(ctx, request.Params)
		},
		"users.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.usersSnapshot(ctx, request.Params)
		},
		"users.upsert": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.usersUpsert(ctx, request.Params)
		},
		"users.act": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.usersAct(ctx, request.Params)
		},
		"bots.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.botsSnapshot(ctx, request.Params)
		},
		"bots.upsert": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.botsUpsert(ctx, request.Params)
		},
		"bots.template.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.botsTemplateGet(ctx, request.Params)
		},
		"bots.template.put": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.botsTemplatePut(ctx, request.Params)
		},
		"skilldrafts.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.skillDraftsList(ctx, request.Params)
		},
		"skilldrafts.propose": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.skillDraftPropose(ctx, request.Params)
		},
		"skilldrafts.decide": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.skillDraftDecide(ctx, request.Params)
		},
		"skilldrafts.install": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.skillDraftInstall(ctx, request.Params)
		},
		"team.notes": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamNotes(ctx, request.Params)
		},
		"team.note.record": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamNoteRecord(ctx, request.Params)
		},
		"team.suggestions": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.noteSuggestions(ctx, request.Params)
		},
		"team.suggest": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.noteSuggest(ctx, request.Params)
		},
		"team.suggestion.promote": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.noteSuggestionPromote(ctx, request.Params)
		},
		"team.suggestion.dismiss": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.noteSuggestionDismiss(ctx, request.Params)
		},
		"team.note.review": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamNoteReview(ctx, request.Params)
		},
		"heartbeat.status": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.heartbeatStatus(ctx, request.Params)
		},
		"heartbeat.tick": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.heartbeatTick(ctx, request.Params)
		},
		"heartbeat.configure": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.heartbeatConfigure(ctx, request.Params)
		},
		"sandbox.status": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.sandboxStatus(ctx, request.Params)
		},
		"browser.status": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.browserStatus(ctx, request.Params)
		},
		"team.snapshot": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamSnapshot()
		},
		"team.spawn": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamSpawn(ctx, request.Params)
		},
		"team.send": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamSend(ctx, request.Params)
		},
		"team.wait": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamWait(ctx, request.Params)
		},
		"team.interrupt": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamInterrupt(request.Params)
		},
		"team.close": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamClose(request.Params)
		},
		"team.activity": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.teamActivity(request.Params)
		},
		"task.delete": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.taskDelete(ctx, request.Params)
		},
		"update.status": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.updateStatus(), nil
		},
		"update.check": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.updateCheck(ctx)
		},
		"update.download": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.updateDownload(ctx)
		},
		"update.install": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.updateInstall(request.Params)
		},
		"session.active.get": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.activeSession()
		},
		"run.cancel": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.cancelRun(request.Params)
		},
		"run.interject": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.interjectRun(ctx, request.Params)
		},
		"ask.answer": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.answerQuestion(request.Params)
		},
		"ask.cancel": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.cancelQuestion(request.Params)
		},
		"plan.exit": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.exitPlan(request.Params)
		},
		"notify.action": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.runNotificationAction(request.Params)
		},
		"foreground.set": func(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
			return g.setForeground(c, request.Params)
		},
	}
}

// SupportedMethods returns the control-plane methods this build answers, sorted.
// It is the implementation side of the protocol/implementation consistency test
// (and what internal/devtools/gwprobe/docs tools read).
func SupportedMethods() []string {
	var g Gateway
	table := g.methodTable()
	methods := make([]string, 0, len(table))
	for method := range table {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

// requestKey normalizes a JSON-RPC id to the string the in-flight table uses:
// numbers and strings are distinct ids on the wire, so the raw bytes keep them
// apart without guessing.
func requestKey(id json.RawMessage) string {
	trimmed := strings.TrimSpace(string(id))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	return trimmed
}

// blockingMethods are the control-plane methods that can wait on the backend.
// They run on their own goroutine so a waiting call cannot freeze the rest of
// the connection (ping, run.cancel, the next request); `$/cancel` cancels them
// by request id (AUDIT-2026-11-24 B-12, TRANSPORT §4.4).
var blockingMethods = map[string]bool{
	"team.wait":       true,
	"team.send":       true,
	"run.interject":   true,
	"update.check":    true,
	"update.download": true,
	"update.install":  true,
}

// isBlocking reports whether a method must run on its own goroutine.
func isBlocking(method string) bool { return blockingMethods[method] }

// registerInFlight stores a cancel func for one request id.
func (g *Gateway) registerInFlight(id string, cancel context.CancelFunc) {
	g.inFlightMu.Lock()
	if g.inFlight == nil {
		g.inFlight = make(map[string]context.CancelFunc)
	}
	g.inFlight[id] = cancel
	g.inFlightMu.Unlock()
}

// clearInFlight forgets one request id.
func (g *Gateway) clearInFlight(id string) {
	g.inFlightMu.Lock()
	delete(g.inFlight, id)
	g.inFlightMu.Unlock()
}

// cancelInFlight cancels one in-flight request and reports whether it existed.
func (g *Gateway) cancelInFlight(id string) bool {
	g.inFlightMu.Lock()
	cancel := g.inFlight[id]
	g.inFlightMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (g *Gateway) dispatch(ctx context.Context, c *client, request rpcRequest) (any, *protocolError) {
	method := request.Method
	if method == "$/cancel" {
		// §4.4: a cancel notification names the request it aborts. Only the
		// blocking methods above run on their own goroutine, so this is the set
		// it can reach; everything else already answered.
		var params struct {
			ID json.RawMessage `json:"id"`
		}
		if len(request.Params) > 0 {
			_ = json.Unmarshal(request.Params, &params)
		}
		if id := requestKey(params.ID); id != "" {
			g.cancelInFlight(id)
		}
		return nil, nil
	}
	if method != "initialize" && method != "ready" && method != "ping" && !c.isReady() {
		// §4.2: initialize must be the first request; only ready and ping may
		// be exchanged before the client confirms the handshake.
		return nil, errNotReady()
	}
	handler, ok := g.methodTable()[method]
	if !ok {
		return nil, errMethodNotFound(method)
	}
	return handler(ctx, c, request)
}

// initialize answers the handshake and enforces the protocol version.
func (g *Gateway) initialize(c *client, raw json.RawMessage) (any, *protocolError) {
	var params initializeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	version := params.ProtocolVersion
	if version == 0 {
		version = ProtocolVersion
	}
	if version != ProtocolVersion {
		c.fatal = true
		err := appError(codeUnsupportedProto, "unsupported_protocol", false)
		err.Data["serverVersion"] = ProtocolVersion
		return nil, err
	}
	return initializeResult{
		Epoch:           g.epoch,
		ProtocolVersion: ProtocolVersion,
		ServerVersion:   g.serverVersion(),
		Capabilities:    g.gatewayCapabilities(),
		Limits: limitsDTO{
			MaxFrameBytes:       MaxFrameBytes,
			MaxAttachmentBytes:  MaxAttachmentBytes,
			SnapshotTailDefault: SnapshotTailDefault,
			DeltaRingBytes:      DeltaRingBytes,
		},
	}, nil
}

// mcpReloadParams is the mcp.reload body.
//
// The disabled list is written by the gateway, not the client: the key is
// workspace-scoped (`mcp.disabledServers.<wsId>`) and the browser does not know
// the workspace id, so a client-side write landed on a key the toolkit never
// reads and the switch did nothing (round-3 conn audit F1).
type mcpReloadParams struct {
	Disabled *[]string `json:"disabled,omitempty"`
}

// reloadConnectors rebuilds the MCP connections from the stored settings and
// answers the fresh connector list.
//
// The settings page writes `mcp.servers` and the toolkit only builds its
// connections at activation, so without this method a saved connector needed a
// gateway restart to exist — while the page said 已保存 (round-2 C-03). The
// reloader is optional: a composition without MCP answers a plain error instead
// of pretending.
func (g *Gateway) reloadConnectors(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	reloader, ok := g.root.Get(contracts.KeyMCPReloader)
	if !ok || reloader == nil {
		return nil, appError(codeInternalError, "mcp_unavailable", false)
	}
	var params mcpReloadParams
	if len(raw) > 0 {
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
	}
	if params.Disabled != nil {
		service, ok := g.root.Get(contracts.KeySettings)
		if !ok || service == nil {
			return nil, appError(codeNotReady, "settings_unavailable", false)
		}
		names := make([]string, 0, len(*params.Disabled))
		for _, name := range *params.Disabled {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				names = append(names, trimmed)
			}
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			return nil, internalError(err)
		}
		if err := service.Set(contracts.MCPDisabledServersSettingKey(g.currentWorkspaceID()), string(encoded)); err != nil {
			return nil, internalError(err)
		}
	}
	plugins, err := reloader.Reload(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return struct {
		Connectors []capabilityConnectorDTO `json:"connectors"`
		Disabled   []string                 `json:"disabled"`
	}{Connectors: connectorDTOs(plugins), Disabled: g.disabledConnectorNames()}, nil
}

// currentWorkspaceID is the scope the toolkit reads its MCP rows under.
func (g *Gateway) currentWorkspaceID() string {
	service, ok := g.root.Get(contracts.KeyWorkspace)
	if !ok || service == nil {
		return ""
	}
	current, has, err := service.Current(context.Background())
	if err != nil || !has {
		return ""
	}
	return strings.TrimSpace(current.ID)
}

// disabledConnectorNames reads the disabled list the toolkit will honour.
func (g *Gateway) disabledConnectorNames() []string {
	service, ok := g.root.Get(contracts.KeySettings)
	if !ok || service == nil {
		return []string{}
	}
	raw := service.Get(contracts.MCPDisabledServersSettingKey(g.currentWorkspaceID()), "")
	names := []string{}
	if strings.TrimSpace(raw) == "" {
		return names
	}
	_ = json.Unmarshal([]byte(raw), &names)
	return names
}

// sessionListSnapshot returns the sidebar list.
func (g *Gateway) sessionListSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotList(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	sessions, ok := snapshot.payload.([]sessionInfoDTO)
	if !ok {
		return nil, internalError(errors.New("gateway: session list snapshot has an unexpected type"))
	}
	return listSnapshotResult{Epoch: g.epoch, Rev: snapshot.rev, Sessions: sessions}, nil
}

// sessionSnapshot returns one conversation, cut to the tail window.
func (g *Gateway) sessionSnapshot(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionSnapshotParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.SessionID) == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	snapshot, err := g.hub.snapshotSession(ctx, params.SessionID)
	if err != nil {
		return nil, internalError(err)
	}
	if !snapshot.found {
		return nil, appError(codeSessionNotFound, "session_not_found", false)
	}
	tail := params.Tail
	if tail <= 0 {
		tail = SnapshotTailDefault
	}
	if tail > 500 {
		tail = 500
	}
	session, ok := snapshot.payload.(sessionDTO)
	if !ok {
		return nil, internalError(errors.New("gateway: session snapshot has an unexpected type"))
	}
	end := len(session.Messages)
	if cursor := strings.TrimSpace(params.BeforeMessageID); cursor != "" {
		// Backward paging (§6.4): `beforeMessageId` is the cursor the previous
		// page returned as oldestMessageId, and the window ENDS just before it.
		// An unknown cursor is a client mistake, never "start from the newest".
		index := -1
		for position := range session.Messages {
			if session.Messages[position].ID == cursor {
				index = position
				break
			}
		}
		if index < 0 {
			return nil, errInvalidParams("beforeMessageId", "is not a message of this session")
		}
		end = index
	}
	start := end - tail
	if start < 0 {
		start = 0
	}
	truncated := start > 0
	oldest := ""
	if end > start {
		oldest = session.Messages[start].ID
	}
	session.Messages = session.Messages[start:end]
	return sessionSnapshotResult{
		Epoch:           g.epoch,
		Rev:             snapshot.rev,
		Session:         session,
		Truncated:       truncated,
		OldestMessageID: oldest,
	}, nil
}

// sessionSubscribe registers the client and replays what it missed.
func (g *Gateway) sessionSubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params subscribeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.SessionID) == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	reply, err := g.hub.subscribeSession(ctx, c.id, params.SessionID, params.SinceRev)
	if err != nil {
		return nil, internalError(err)
	}
	for _, entry := range reply.replay {
		payload, encodeErr := entry.encode()
		if encodeErr != nil {
			continue
		}
		c.enqueueControl(payload)
	}
	return subscribeResult{Epoch: g.epoch, Rev: reply.rev, Resync: reply.resync}, nil
}

// sessionUnsubscribe drops one subscription.
func (g *Gateway) sessionUnsubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params sessionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if err := g.hub.unsubscribeSession(ctx, c.id, params.SessionID); err != nil {
		return nil, internalError(err)
	}
	return struct{}{}, nil
}

// domainSubscribeParams is the body of a domain subscribe request: only the
// client's rev, so a reconnect can resume from the replay ring (§6.5).
type domainSubscribeParams struct {
	SinceRev uint64 `json:"sinceRev,omitempty"`
}

// domainSubscribeResult is the answer of every *.subscribe method.
type domainSubscribeResult struct {
	Epoch  string `json:"epoch"`
	Rev    uint64 `json:"rev"`
	Resync bool   `json:"resync"`
}

// domainSubscribe registers the client on a domain and replays what it missed.
func (g *Gateway) domainSubscribe(ctx context.Context, c *client, key string, raw json.RawMessage) (any, *protocolError) {
	var params domainSubscribeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	reply, err := g.hub.subscribeDomain(ctx, c.id, key, params.SinceRev)
	if err != nil {
		return nil, internalError(err)
	}
	for _, entry := range reply.replay {
		payload, encodeErr := entry.encode()
		if encodeErr != nil {
			continue
		}
		c.enqueueControl(payload)
	}
	return domainSubscribeResult{Epoch: g.epoch, Rev: reply.rev, Resync: reply.resync}, nil
}

// approvalResolveParams is the approval.resolve body (§5.1.3).
type approvalResolveParams struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// approvalResolveResult is the approval.resolve answer.
type approvalResolveResult struct {
	Resolved bool `json:"resolved"`
}

// resolveApproval answers one pending approval. The decision vocabulary is the
// contract's ("allow", "allow_always", "deny"): a browser must never send a
// neutral verb, and an unknown value is a parameter error, not a silent deny.
func (g *Gateway) resolveApproval(raw json.RawMessage) (any, *protocolError) {
	var params approvalResolveParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return nil, errInvalidParams("id", "is required")
	}
	decision := contracts.ApprovalDecision(strings.TrimSpace(params.Decision))
	switch decision {
	case contracts.ApprovalAllow, contracts.ApprovalAllowAlways, contracts.ApprovalDeny:
	default:
		return nil, errInvalidParams("decision", "want allow, allow_always or deny")
	}
	service, ok := g.root.Get(contracts.KeyApprovals)
	if !ok || service == nil {
		return nil, appError(codeNotReady, "approvals_unavailable", false)
	}
	if err := service.Resolve(id, decision); err != nil {
		// Resolving twice (or an expired request) is not a fault the caller can
		// fix; report it as a stale revision so the client re-snapshots.
		return nil, appError(codeRevisionGone, "approval_not_pending", false)
	}
	return approvalResolveResult{Resolved: true}, nil
}

// approvalsSnapshotResult is the approvals.snapshot answer (§5.1.3).
type approvalsSnapshotResult struct {
	Epoch                  string        `json:"epoch"`
	Rev                    uint64        `json:"rev"`
	Mode                   string        `json:"mode"`
	Permission             string        `json:"permission"`
	ApprovalTimeoutSeconds int           `json:"approvalTimeoutSeconds"`
	Pending                []approvalDTO `json:"pending"`
}

// askSnapshotResult is the ask.snapshot answer (§5.1.4).
type askSnapshotResult struct {
	Epoch   string  `json:"epoch"`
	Rev     uint64  `json:"rev"`
	Pending *askDTO `json:"pending"`
}

// planSnapshotResult is the plan.snapshot answer.
type planSnapshotResult struct {
	Epoch   string `json:"epoch"`
	Rev     uint64 `json:"rev"`
	State   string `json:"state"`
	Content string `json:"content"`
}

// artifactsSnapshotResult is the artifacts.snapshot answer.
type artifactsSnapshotResult struct {
	Epoch string        `json:"epoch"`
	Rev   uint64        `json:"rev"`
	Items []artifactDTO `json:"items"`
}

// settingsSnapshotResult is the settings.snapshot answer (§5.1.5).
type settingsSnapshotResult struct {
	Epoch string            `json:"epoch"`
	Rev   uint64            `json:"rev"`
	Rows  map[string]string `json:"rows"`
}

// workspacesSnapshotResult is the workspace.list.snapshot answer (§5.1.6).
type workspacesSnapshotResult struct {
	Epoch      string         `json:"epoch"`
	Rev        uint64         `json:"rev"`
	Workspaces []workspaceDTO `json:"workspaces"`
	CurrentID  string         `json:"currentId,omitempty"`
}

// approvalsSnapshot serves the approvals domain.
func (g *Gateway) approvalsSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotDomain(ctx, domainApprovals)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(approvalsSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: approvals snapshot has an unexpected type"))
	}
	return approvalsSnapshotResult{
		Epoch:                  g.epoch,
		Rev:                    snapshot.rev,
		Mode:                   payload.Mode,
		Permission:             payload.Permission,
		ApprovalTimeoutSeconds: payload.ApprovalTimeoutSeconds,
		Pending:                payload.Pending,
	}, nil
}

// askSnapshot serves the ask domain.
func (g *Gateway) askSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotDomain(ctx, domainAsk)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(askSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: ask snapshot has an unexpected type"))
	}
	return askSnapshotResult{Epoch: g.epoch, Rev: snapshot.rev, Pending: payload.Pending}, nil
}

// planSnapshot serves the plan domain.
func (g *Gateway) planSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotDomain(ctx, domainPlan)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(planSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: plan snapshot has an unexpected type"))
	}
	return planSnapshotResult{Epoch: g.epoch, Rev: snapshot.rev, State: payload.State, Content: payload.Content}, nil
}

// artifactsSnapshot serves the artifacts domain.
func (g *Gateway) artifactsSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotDomain(ctx, domainArtifacts)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(artifactsSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: artifacts snapshot has an unexpected type"))
	}
	return artifactsSnapshotResult{Epoch: g.epoch, Rev: snapshot.rev, Items: payload.Items}, nil
}

// settingsSnapshot serves the settings domain.
func (g *Gateway) settingsSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotDomain(ctx, domainSettings)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(settingsSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: settings snapshot has an unexpected type"))
	}
	return settingsSnapshotResult{Epoch: g.epoch, Rev: snapshot.rev, Rows: payload.Rows}, nil
}

// workspacesSnapshot serves the workspace list domain.
func (g *Gateway) workspacesSnapshot(ctx context.Context) (any, *protocolError) {
	snapshot, err := g.hub.snapshotDomain(ctx, domainWorkspace)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(workspacesSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: workspace snapshot has an unexpected type"))
	}
	return workspacesSnapshotResult{
		Epoch:      g.epoch,
		Rev:        snapshot.rev,
		Workspaces: payload.Workspaces,
		CurrentID:  payload.CurrentID,
	}, nil
}

// promptsSnapshotResult is the prompts.snapshot answer (§5.1.1 queue.*).
type promptsSnapshotResult struct {
	Epoch     string            `json:"epoch"`
	Rev       uint64            `json:"rev"`
	SessionID string            `json:"sessionId"`
	Prompts   []queuedPromptDTO `json:"prompts"`
}

// promptsEnqueueParams is the prompts.enqueue body.
type promptsEnqueueParams struct {
	SessionID string `json:"sessionId"`
	Prompt    string `json:"prompt"`
}

// promptsEnqueueResult is the prompts.enqueue answer. There is no `rev` field:
// it was declared but never assigned, so every answer carried rev:0 — a field
// the contract (§5.1) does not promise and a client must not read
// (AUDIT-2026-11-24 C).
type promptsEnqueueResult struct {
	Epoch  string `json:"epoch"`
	ID     string `json:"id"`
	Queued int    `json:"queued"`
}

// promptsRemoveParams is the prompts.remove body.
type promptsRemoveParams struct {
	SessionID string `json:"sessionId"`
	ID        string `json:"id"`
}

// promptsRemoveResult is the prompts.remove answer (no `rev`: see
// promptsEnqueueResult).
type promptsRemoveResult struct {
	Epoch  string `json:"epoch"`
	Queued int    `json:"queued"`
}

// promptsSnapshot serves one conversation's prompt inbox.
func (g *Gateway) promptsSnapshot(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	// A protocol read has to be consistent with a protocol write on the same
	// connection: the inbox has no backend change notification, so the cached
	// domain can still be pre-enqueue. Refresh before answering (the poller would
	// otherwise be the only path, and a snapshot right after prompts.enqueue read
	// [] — the flake in TestE2EQueueDrainsAfterTheRun).
	_ = g.hub.refreshNow(ctx, promptsKey(sessionID))
	snapshot, err := g.hub.snapshotDomain(ctx, promptsKey(sessionID))
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(promptsSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: prompts snapshot has an unexpected type"))
	}
	return promptsSnapshotResult{
		Epoch:     g.epoch,
		Rev:       snapshot.rev,
		SessionID: sessionID,
		Prompts:   payload.Prompts,
	}, nil
}

// promptsSubscribe registers the client on one inbox and replays what it missed.
// promptsSubscribeParams is the prompts.subscribe body.
type promptsSubscribeParams struct {
	SessionID string `json:"sessionId"`
	SinceRev  uint64 `json:"sinceRev,omitempty"`
}

func (g *Gateway) promptsSubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params promptsSubscribeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	reply, err := g.hub.subscribeDomain(ctx, c.id, promptsKey(sessionID), params.SinceRev)
	if err != nil {
		return nil, internalError(err)
	}
	for _, entry := range reply.replay {
		payload, encodeErr := entry.encode()
		if encodeErr != nil {
			continue
		}
		c.enqueueControl(payload)
	}
	return domainSubscribeResult{Epoch: g.epoch, Rev: reply.rev, Resync: reply.resync}, nil
}

// promptsUnsubscribe drops one conversation's prompt-inbox subscription.
//
// Per-session domains had no unsubscribe at all, so every conversation a tab had
// ever opened stayed subscribed for the life of the page: the gateway kept
// rebuilding that domain on every poll tick (a context rebuild re-folds the
// durable event log), and coalescing then had two live sessions to confuse
// (AUDIT-2026-11-24 A-4).
// promptsUnsubscribeParams is the prompts.unsubscribe body: the session only.
// It is a separate type from the subscribe body on purpose — a subscription has
// a resume point, an unsubscribe does not, and declaring `sinceRev` here would
// tell clients and fixtures that it meant something.
type promptsUnsubscribeParams struct {
	SessionID string `json:"sessionId"`
}

func (g *Gateway) promptsUnsubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params promptsUnsubscribeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	if err := g.hub.unsubscribeDomain(ctx, c.id, promptsKey(sessionID)); err != nil {
		return nil, internalError(err)
	}
	return struct{}{}, nil
}

// promptsEnqueue appends one prompt to a conversation's inbox.
//
// The backend owns the queue: EnqueuePrompt may start the prompt immediately on
// an idle session, so the call is made on the connection's request goroutine —
// never on the hub goroutine — and the resulting change is published by marking
// the inbox domain dirty.
func (g *Gateway) promptsEnqueue(raw json.RawMessage) (any, *protocolError) {
	var params promptsEnqueueParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	if strings.TrimSpace(params.Prompt) == "" {
		// The contract rejects an empty prompt; say so as a parameter error
		// rather than passing it down and translating an opaque backend error.
		return nil, errInvalidParams("prompt", "is required")
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	if _, found := chat.Session(sessionID); !found {
		return nil, appError(codeSessionNotFound, "session_not_found", false)
	}
	queue, ok := chat.(contracts.PromptQueue)
	if !ok {
		return nil, appError(codeCapabilityUnsupported, "prompts_not_supported", false)
	}
	entry, err := queue.EnqueuePrompt(context.Background(), sessionID, params.Prompt)
	if err != nil {
		return nil, internalError(err)
	}
	// Publish the new inbox now, so a watching client sees the prompt as a delta
	// instead of waiting for the next poll tick; markDomainDirty stays as the
	// fallback for a hub that is not running yet.
	_ = g.hub.refreshNow(context.Background(), promptsKey(sessionID))
	g.hub.markDomainDirty(promptsKey(sessionID))
	return promptsEnqueueResult{Epoch: g.epoch, ID: entry.ID, Queued: len(queue.QueuedPrompts(sessionID))}, nil
}

// promptsRemove removes one queued prompt (idempotent, per the contract).
// promptsMoveParams is the prompts.move body.
type promptsMoveParams struct {
	SessionID string `json:"sessionId"`
	ID        string `json:"id"`
	Index     int    `json:"index"`
}

// promptsMove reorders one queued prompt, keeping its id (TRANSPORT §5.1.4).
func (g *Gateway) promptsMove(raw json.RawMessage) (any, *protocolError) {
	var params promptsMoveParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	if strings.TrimSpace(params.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	if params.Index < 0 {
		return nil, errInvalidParams("index", "must not be negative")
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	if _, found := chat.Session(sessionID); !found {
		return nil, appError(codeSessionNotFound, "session_not_found", false)
	}
	mover, ok := chat.(contracts.PromptMover)
	if !ok {
		return nil, appError(codeCapabilityUnsupported, "prompts_move_not_supported", false)
	}
	if err := mover.MoveQueuedPrompt(context.Background(), sessionID, params.ID, params.Index); err != nil {
		if errors.Is(err, contracts.ErrQueueDraining) {
			return nil, appError(codeNotReady, "queue_draining", true)
		}
		return nil, internalError(err)
	}
	g.hub.markDomainDirty(promptsKey(sessionID))
	return struct{}{}, nil
}

func (g *Gateway) promptsRemove(raw json.RawMessage) (any, *protocolError) {
	var params promptsRemoveParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	if strings.TrimSpace(params.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	if _, found := chat.Session(sessionID); !found {
		return nil, appError(codeSessionNotFound, "session_not_found", false)
	}
	queue, ok := chat.(contracts.PromptQueue)
	if !ok {
		return nil, appError(codeCapabilityUnsupported, "prompts_not_supported", false)
	}
	// RemoveQueuedPrompt is idempotent: removing an unknown id is a no-op, so a
	// retry after a dropped connection succeeds instead of erroring.
	if err := queue.RemoveQueuedPrompt(context.Background(), sessionID, params.ID); err != nil {
		return nil, internalError(err)
	}
	_ = g.hub.refreshNow(context.Background(), promptsKey(sessionID))
	g.hub.markDomainDirty(promptsKey(sessionID))
	return promptsRemoveResult{Epoch: g.epoch, Queued: len(queue.QueuedPrompts(sessionID))}, nil
}

// sessionCreateParams is the session.create body.
type sessionCreateParams struct {
	Title string `json:"title,omitempty"`
}

// sessionCreateResult is the session.create answer.
type sessionCreateResult struct {
	Epoch     string `json:"epoch"`
	SessionID string `json:"sessionId"`
	Rev       uint64 `json:"rev"`
}

// createSession creates one conversation and makes it appear in the sidebar
// before the command returns.
//
// The list delta is built synchronously (refreshNow) instead of waiting for the
// hub's next dirty pass: a client that creates a session and immediately renders
// the sidebar must not see it arrive a beat later.
func (g *Gateway) createSession(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionCreateParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	title, protocolErr := validateSessionTitle(params.Title)
	if protocolErr != nil {
		return nil, protocolErr
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	sessionID, err := chat.NewSession(title)
	if err != nil {
		return nil, internalError(err)
	}
	// Warm the new conversation's domain (so its first snapshot is immediate)
	// and publish the sidebar change now.
	g.hub.markDirty(sessionID)
	listRev := uint64(0)
	if refreshErr := g.hub.refreshNow(ctx, listDomain); refreshErr != nil {
		g.logger.Printf("gateway: refresh session list after create: %v", refreshErr)
	} else if snapshot, snapErr := g.hub.snapshotDomain(ctx, listDomain); snapErr == nil {
		listRev = snapshot.rev
	}
	if _, snapErr := g.hub.snapshotDomain(ctx, dKey(sessionID)); snapErr != nil {
		g.logger.Printf("gateway: warm new session domain: %v", snapErr)
	}
	return sessionCreateResult{Epoch: g.epoch, SessionID: sessionID, Rev: listRev}, nil
}

// sessionMutateResult is the answer of the session mutators: the list revision
// the change produced, so the caller can tell its own update from an older one.
type sessionMutateResult struct {
	Epoch string `json:"epoch"`
	Rev   uint64 `json:"rev"`
}

// sessionRenameParams is the body of session.rename.
type sessionRenameParams struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title,omitempty"`
}

// sessionDeleteParams is the body of session.delete.
type sessionDeleteParams struct {
	SessionID string `json:"sessionId"`
}

// sessionArchiveParams is the body of session.archive.
type sessionArchiveParams struct {
	SessionID string `json:"sessionId"`
	Archived  bool   `json:"archived,omitempty"`
}

// sessionPinParams is the body of session.pin.
type sessionPinParams struct {
	SessionID string `json:"sessionId"`
	Pinned    bool   `json:"pinned,omitempty"`
}

// sessionBindTaskParams is the body of session.bindtask. An omitted or empty
// taskId clears the binding.
type sessionBindTaskParams struct {
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId,omitempty"`
}

// sessionBindTaskResult is the session.bindtask answer: the binding in effect
// after the call, so a client renders it without a second read ("" = unbound,
// including right after a clear).
type sessionBindTaskResult struct {
	TaskID string `json:"taskId"`
}

// bindSessionTask binds one conversation to one board task (M5 enforcement):
// while bound, the agent's tool gate refuses every tool outside that task's tool
// binding, whatever the session permission, remembered tools or sandbox state
// say. The answer carries the effective binding, not the request, so the client
// never has to guess what was stored.
func (g *Gateway) bindSessionTask(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionBindTaskParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID, protocolErr := requireSessionID(params.SessionID)
	if protocolErr != nil {
		return nil, protocolErr
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	binder, ok := chat.(contracts.SessionTaskBinder)
	if !ok {
		return nil, appError(codeInternalError, "taskbinding_unavailable", false)
	}
	if err := binder.BindSessionTask(ctx, sessionID, params.TaskID); err != nil {
		return nil, orchestrationError(err)
	}
	bound, err := binder.SessionTask(sessionID)
	if err != nil {
		return nil, orchestrationError(err)
	}
	// The binding is a settings row, so the settings domain is what a watcher
	// reads it from; refresh it before answering (the same rule as settings.set).
	if err := g.hub.refreshNow(ctx, domainSettings); err != nil {
		g.logger.Printf("gateway: refresh settings after bindtask: %v", err)
	}
	return sessionBindTaskResult{TaskID: bound}, nil
}

// sessionPresetParams is the body of session.preset.get and session.preset.set.
// presetId is optional on the write: omitting it clears the role, exactly like
// taskId on session.bindtask.
type sessionPresetParams struct {
	SessionID string `json:"sessionId"`
	PresetID  string `json:"presetId,omitempty"`
}

// sessionPresetResult is the answer of both halves: the selection in effect
// after the call ("" = no role), so a client renders the picker without a
// second read and can tell "cleared" from "the call failed".
type sessionPresetResult struct {
	PresetID string `json:"presetId"`
}

// getSessionPreset answers the session's selected agent preset (TRANSPORT
// §5.1.1). It is a read, so it does not refresh the settings domain.
func (g *Gateway) getSessionPreset(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionPresetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID, protocolErr := requireSessionID(params.SessionID)
	if protocolErr != nil {
		return nil, protocolErr
	}
	setter, problem := g.presetSetter()
	if problem != nil {
		return nil, problem
	}
	return sessionPresetResult{PresetID: setter.SessionPreset(sessionID)}, nil
}

// setSessionPreset selects (or clears) the session's agent preset — the product
// half of "this conversation runs as a role". The answer carries the selection
// in effect, and a write refreshes the settings domain because the selection is
// a settings row (the same rule as settings.set and session.bindtask).
func (g *Gateway) setSessionPreset(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionPresetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID, protocolErr := requireSessionID(params.SessionID)
	if protocolErr != nil {
		return nil, protocolErr
	}
	setter, problem := g.presetSetter()
	if problem != nil {
		return nil, problem
	}
	if err := setter.SetSessionPreset(sessionID, params.PresetID); err != nil {
		return nil, orchestrationError(err)
	}
	if err := g.hub.refreshNow(ctx, domainSettings); err != nil {
		g.logger.Printf("gateway: refresh settings after preset.set: %v", err)
	}
	// The session list row carries the role and whether it resolves, so the
	// selection has to reach it too — otherwise the composer warning would only
	// appear (or clear) on the next poll (2026-11-26 audit, 高).
	g.hub.markDomainDirty(listDomain)
	return sessionPresetResult{PresetID: setter.SessionPreset(sessionID)}, nil
}

// presetSetter resolves the chat backend's optional preset half. A product
// without roles reports the method as unavailable instead of pretending.
func (g *Gateway) presetSetter() (contracts.SessionPresetSetter, *protocolError) {
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	setter, ok := chat.(contracts.SessionPresetSetter)
	if !ok {
		return nil, appError(codeInternalError, "preset_unavailable", false)
	}
	return setter, nil
}

// renameSession sets a session's display title (TRANSPORT §5.1.1).
//
// Renaming is a sidebar concern, so the answer carries the list revision and
// the change reaches every client through the list delta.
func (g *Gateway) renameSession(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionRenameParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID, protocolErr := requireSessionID(params.SessionID)
	if protocolErr != nil {
		return nil, protocolErr
	}
	title, protocolErr := validateSessionTitle(params.Title)
	if protocolErr != nil {
		return nil, protocolErr
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	renamer, ok := chat.(contracts.SessionRenamer)
	if !ok {
		return nil, appError(codeInternalError, "rename_unavailable", false)
	}
	if err := renamer.RenameSession(sessionID, title); err != nil {
		return nil, internalError(err)
	}
	return g.publishSessionChange(ctx, sessionID)
}

// deleteSession removes a conversation and its transcript.
func (g *Gateway) deleteSession(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params sessionDeleteParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID, protocolErr := requireSessionID(params.SessionID)
	if protocolErr != nil {
		return nil, protocolErr
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	if err := chat.DeleteSession(sessionID); err != nil {
		return nil, internalError(err)
	}
	// The session's own settings rows (pinned / archived / unread /
	// allowedTools / model / preset / boundTask) are keyed by its id and outlive
	// the conversation otherwise: deleting 7 conversations left 65 rows behind
	// (round-3 session audit F4).
	g.purgeSessionSettings(sessionID)
	// Keep the cached domain: it is rebuilt from the store, which no longer has
	// the session, so watchers get a resync instead of a stale transcript.
	return g.publishSessionChange(ctx, sessionID)
}

// setSessionFlag archives/restores or pins/unpins one session.
func (g *Gateway) setSessionFlag(ctx context.Context, raw json.RawMessage, kind string) (any, *protocolError) {
	var (
		sessionID string
		applyErr  error
	)
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	switch kind {
	case "archive":
		var params sessionArchiveParams
		if decodeErr := decodeParams(raw, &params); decodeErr != nil {
			return nil, decodeErr
		}
		sessionID, applyErr = strings.TrimSpace(params.SessionID), nil
		if sessionID == "" {
			return nil, errInvalidParams("sessionId", "is required")
		}
		archiver, ok := chat.(contracts.SessionArchiver)
		if !ok {
			return nil, appError(codeInternalError, "archive_unavailable", false)
		}
		applyErr = archiver.SetSessionArchived(sessionID, params.Archived)
	case "pin":
		var params sessionPinParams
		if decodeErr := decodeParams(raw, &params); decodeErr != nil {
			return nil, decodeErr
		}
		sessionID, applyErr = strings.TrimSpace(params.SessionID), nil
		if sessionID == "" {
			return nil, errInvalidParams("sessionId", "is required")
		}
		pinner, ok := chat.(contracts.SessionPinner)
		if !ok {
			return nil, appError(codeInternalError, "pin_unavailable", false)
		}
		applyErr = pinner.SetSessionPinned(sessionID, params.Pinned)
	default:
		return nil, errInvalidParams("kind", "is unknown")
	}
	if applyErr != nil {
		return nil, internalError(applyErr)
	}
	return g.publishSessionChange(ctx, sessionID)
}

// purgeSessionSettings drops every `session.<id>.*` row for a deleted session.
func (g *Gateway) purgeSessionSettings(sessionID string) {
	service, ok := g.root.Get(contracts.KeySettings)
	if !ok || service == nil {
		return
	}
	prefix := "session." + sessionID + "."
	deleter, canDelete := service.(contracts.SettingsDeleter)
	for key := range service.All() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if canDelete {
			if err := deleter.Delete(key); err != nil {
				g.logger.Printf("gateway: purge %s: %v", key, err)
			}
			continue
		}
		if err := service.Set(key, ""); err != nil {
			g.logger.Printf("gateway: purge %s: %v", key, err)
		}
	}
}

// publishSessionChange rebuilds the list (and the session, when it still
// exists) and answers with the list revision.
func (g *Gateway) publishSessionChange(ctx context.Context, sessionID string) (any, *protocolError) {
	if sessionID != "" {
		g.hub.markDirty(sessionID)
	}
	rev := uint64(0)
	if err := g.hub.refreshNow(ctx, listDomain); err != nil {
		g.logger.Printf("gateway: refresh session list after mutation: %v", err)
	} else if snapshot, snapErr := g.hub.snapshotDomain(ctx, listDomain); snapErr == nil {
		rev = snapshot.rev
	}
	return sessionMutateResult{Epoch: g.epoch, Rev: rev}, nil
}

// requireSessionID is the shared "sessionId is required" guard.
func requireSessionID(raw string) (string, *protocolError) {
	sessionID := strings.TrimSpace(raw)
	if sessionID == "" {
		return "", errInvalidParams("sessionId", "is required")
	}
	return sessionID, nil
}

// validateSessionTitle applies the protocol's own guard rails. The contract
// defines no limit (the backend only trims), so the transport states one: a
// title is a sidebar label, and a 200-rune label with control characters is a
// caller bug, not a conversation.
func validateSessionTitle(raw string) (string, *protocolError) {
	title := strings.TrimSpace(raw)
	if runes := []rune(title); len(runes) > maxSessionTitleRunes {
		return "", errInvalidParams("title", "is longer than 200 characters")
	}
	for _, char := range title {
		if char < 0x20 || char == 0x7f {
			return "", errInvalidParams("title", "contains a control character")
		}
	}
	return title, nil
}

// maxSessionTitleRunes bounds a session title.
const maxSessionTitleRunes = 200

// settingsSetParams is the settings.set body.
type settingsSetParams struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// settingsSetResult is the settings.set answer.
type settingsSetResult struct {
	Epoch string `json:"epoch"`
	Rev   uint64 `json:"rev"`
}

// setSetting writes one settings row and publishes the change immediately.
//
// The gateway is the writer, so it does not wait for the settings poll: the row
// is stored and the settings domain rebuilt (refreshNow) before the command
// answers. A permission row additionally refreshes the approvals domain, because
// the approval mode is derived from it.
func (g *Gateway) setSetting(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params settingsSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(params.Key)
	if protocolErr := validateSettingKey(key); protocolErr != nil {
		return nil, protocolErr
	}
	if len(params.Value) > maxSettingValueBytes {
		return nil, errInvalidParams("value", "is longer than 64 KiB")
	}
	// `session.<id>.preset` is a settings row, but not a plain one: a raw write
	// here bypassed SetSessionPreset's validation, so a typo (or a hand-written
	// row) became "no role" and handed the session the full tool surface
	// (2026-11-26 audit). Route the write through the setter that validates it,
	// so the only way to store an unknown id is an older document — which the
	// agent still treats fail-closed.
	if sessionID, isPresetRow := contracts.ParseSessionPresetSettingKey(key); isPresetRow {
		setter, problem := g.presetSetter()
		if problem != nil {
			return nil, problem
		}
		if err := setter.SetSessionPreset(sessionID, strings.TrimSpace(params.Value)); err != nil {
			return nil, orchestrationError(err)
		}
		if err := g.hub.refreshNow(ctx, domainSettings); err != nil {
			g.logger.Printf("gateway: refresh settings after preset row: %v", err)
		}
		// Same reason as session.preset.set: the role lives in the session list
		// row the composer reads.
		g.hub.markDomainDirty(listDomain)
		rev := uint64(0)
		if snapshot, err := g.hub.snapshotDomain(ctx, domainSettings); err == nil {
			rev = snapshot.rev
		}
		return settingsSetResult{Epoch: g.epoch, Rev: rev}, nil
	}
	// `presets.agents` is a JSON document the agent reads its roles from, so a
	// malformed value is not a harmless string: the read side degrades to "no
	// user presets" and a session that selected one of those roles silently loses
	// every tool (2026-11-26 audit, 高). The write is refused with the reason
	// while the value is still attributable to this request.
	if key == contracts.SettingAgentPresets {
		if err := contracts.ValidateAgentPresets(params.Value); err != nil {
			return nil, errInvalidParams("value", err.Error())
		}
	}
	service, ok := g.root.Get(contracts.KeySettings)
	if !ok || service == nil {
		return nil, appError(codeNotReady, "settings_unavailable", false)
	}
	if err := service.Set(key, params.Value); err != nil {
		return nil, internalError(err)
	}
	if err := g.hub.refreshNow(ctx, domainSettings); err != nil {
		g.logger.Printf("gateway: refresh settings after set: %v", err)
	}
	if key == contracts.SettingAgentPermission {
		// The approval mode is derived from this row: a stale approvals snapshot
		// would let the card describe the wrong policy.
		g.hub.markDomainDirty(domainApprovals)
	}
	rev := uint64(0)
	if snapshot, err := g.hub.snapshotDomain(ctx, domainSettings); err == nil {
		rev = snapshot.rev
	}
	return settingsSetResult{Epoch: g.epoch, Rev: rev}, nil
}

// --- context timeline ------------------------------------------------------

// contextSnapshotResult is the context.snapshot answer.
type contextSnapshotResult struct {
	Epoch     string      `json:"epoch"`
	Rev       uint64      `json:"rev"`
	SessionID string      `json:"sessionId"`
	Found     bool        `json:"found"`
	Context   *contextDTO `json:"context,omitempty"`
}

// contextSnapshotParams is the body of context.snapshot: a full snapshot of one
// conversation's context timeline. It has no sinceRev — a snapshot is always
// whole, and increments are negotiated by context.subscribe. Declaring a
// parameter the handler ignores is worse than not having it: the stand-ins and
// the client both start believing it works.
type contextSnapshotParams struct {
	SessionID string `json:"sessionId"`
}

// contextSubscribeParams is the body of context.subscribe.
type contextSubscribeParams struct {
	SessionID string `json:"sessionId"`
	SinceRev  uint64 `json:"sinceRev,omitempty"`
}

// contextSnapshot serves one conversation's folded context timeline.
func (g *Gateway) contextSnapshot(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params contextSnapshotParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	key := contextKey(sessionID)
	snapshot, err := g.hub.snapshotDomain(ctx, key)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(contextSnapshotPayload)
	if !ok {
		return nil, internalError(errors.New("gateway: context snapshot has an unexpected type"))
	}
	return contextSnapshotResult{
		Epoch:     g.epoch,
		Rev:       snapshot.rev,
		SessionID: payload.SessionID,
		Found:     payload.Found,
		Context:   payload.Context,
	}, nil
}

// contextSubscribe registers the client on one conversation's context domain
// and replays its ring.
func (g *Gateway) contextSubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params contextSubscribeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	return g.domainSubscribe(ctx, c, contextKey(sessionID), raw)
}

// contextUnsubscribe drops one conversation's context-timeline subscription.
// contextUnsubscribeParams is the context.unsubscribe body.
type contextUnsubscribeParams struct {
	SessionID string `json:"sessionId"`
}

func (g *Gateway) contextUnsubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params contextUnsubscribeParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	if err := g.hub.unsubscribeDomain(ctx, c.id, contextKey(sessionID)); err != nil {
		return nil, internalError(err)
	}
	return struct{}{}, nil
}

// --- task board ------------------------------------------------------------

// tasksSnapshotParams is the body of tasks.snapshot: a full snapshot of the task
// board (optionally scoped to one conversation). No sinceRev — see
// contextSnapshotParams for the reason.
type tasksSnapshotParams struct {
	SessionID string `json:"sessionId,omitempty"`
}

// tasksSubscribeParams is the body of tasks.subscribe.
type tasksSubscribeParams struct {
	SessionID string `json:"sessionId,omitempty"`
	SinceRev  uint64 `json:"sinceRev,omitempty"`
}

// tasksSnapshotResult is the tasks.snapshot answer.
type tasksSnapshotResult struct {
	Epoch     string    `json:"epoch"`
	Rev       uint64    `json:"rev"`
	SessionID string    `json:"sessionId,omitempty"`
	Tasks     []taskDTO `json:"tasks"`
}

// taskStatusParams is the task.status body (task board status vocabulary).
type taskStatusParams struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	SessionID string `json:"sessionId,omitempty"`
}

// taskDeleteParams is the task.delete body.
type taskDeleteParams struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId,omitempty"`
}

// taskWriteResult is the answer of both write methods: the new rev plus the row
// the client just changed (nil for a deletion).
type taskWriteResult struct {
	Epoch string   `json:"epoch"`
	Rev   uint64   `json:"rev"`
	Task  *taskDTO `json:"task,omitempty"`
}

// tasksDomainKey maps the optional conversation to a domain key.
func tasksDomainKey(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return domainTasks
	}
	return tasksKey(strings.TrimSpace(sessionID))
}

// tasksSnapshot serves the task board domain.
func (g *Gateway) tasksSnapshot(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params tasksSnapshotParams
	if len(raw) > 0 {
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
	}
	key := tasksDomainKey(params.SessionID)
	snapshot, err := g.hub.snapshotDomain(ctx, key)
	if err != nil {
		return nil, internalError(err)
	}
	payload, ok := snapshot.payload.(tasksSnapshot)
	if !ok {
		return nil, internalError(errors.New("gateway: tasks snapshot has an unexpected type"))
	}
	return tasksSnapshotResult{
		Epoch:     g.epoch,
		Rev:       snapshot.rev,
		SessionID: payload.SessionID,
		Tasks:     payload.Tasks,
	}, nil
}

// tasksSubscribe registers the client on the board domain (global or
// per-session) and replays its ring.
func (g *Gateway) tasksSubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params tasksSubscribeParams
	if len(raw) > 0 {
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
	}
	return g.domainSubscribe(ctx, c, tasksDomainKey(params.SessionID), raw)
}

// tasksUnsubscribe drops one task-board subscription (the process-wide board
// when sessionId is empty, one conversation's board otherwise).
// tasksUnsubscribeParams is the tasks.unsubscribe body; an omitted sessionId
// means the process-wide board.
type tasksUnsubscribeParams struct {
	SessionID string `json:"sessionId,omitempty"`
}

func (g *Gateway) tasksUnsubscribe(ctx context.Context, c *client, raw json.RawMessage) (any, *protocolError) {
	var params tasksUnsubscribeParams
	if len(raw) > 0 {
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
	}
	if err := g.hub.unsubscribeDomain(ctx, c.id, tasksDomainKey(params.SessionID)); err != nil {
		return nil, internalError(err)
	}
	return struct{}{}, nil
}

// taskSetStatus moves one task to another board status (pending / in_progress /
// completed). This is the "stop" half of the chip's commands: an in-progress
// background task goes back to pending. The status vocabulary is the contract's;
// an unknown value is a parameter error, never a silent no-op.
func (g *Gateway) taskSetStatus(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params taskStatusParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return nil, errInvalidParams("id", "is required")
	}
	status := strings.TrimSpace(params.Status)
	switch status {
	case contracts.TaskStatusPending, contracts.TaskStatusInProgress, contracts.TaskStatusCompleted:
	default:
		return nil, errInvalidParams("status", "must be pending, in_progress or completed")
	}
	board := g.taskBoard(strings.TrimSpace(params.SessionID))
	if board == nil {
		return nil, appError(codeNotReady, "task_board_unavailable", false)
	}
	task, err := board.SetStatus(ctx, id, status)
	if err != nil {
		return nil, internalError(err)
	}
	// A status write to completed is a close that bypasses the lease review: the
	// lease's Accept is the audited path, and this command is the chip that
	// skips it (2026-11-26 audit, 高 — "reviewer ≠ owner, every task leaves a
	// record" was bypassable by the very button the client offers). The record is
	// written as an explicit unaudited close instead of the command being
	// refused, so the client keeps working while the audit trail stops lying.
	if status == contracts.TaskStatusCompleted {
		g.recordDirectClose(ctx, task.ID, "user", "tasks.status")
	}
	return g.publishTaskChange(ctx, params.SessionID, &task)
}

// recordDirectClose hands one unaudited close to the lease layer's review log.
//
// The write is best effort and never fails the command: the task is already
// completed by the time this runs, so turning a missing audit row into an error
// would invite the client to retry a write that succeeded. An assembly without
// the lease module has no review log at all — the same assembly also answers
// `tasks.reviews` as unavailable — and that is logged instead of hidden.
func (g *Gateway) recordDirectClose(ctx context.Context, taskID, actor, source string) {
	service, ok := g.root.Get(contracts.KeyTaskLease)
	if !ok || service == nil {
		g.logger.Printf("gateway: task %s closed via %s without a lease module to record it", taskID, source)
		return
	}
	auditor, ok := service.(contracts.TaskCloseAuditor)
	if !ok {
		g.logger.Printf("gateway: task %s closed via %s but the lease service cannot record it", taskID, source)
		return
	}
	if err := auditor.RecordDirectClose(ctx, taskID, actor, source); err != nil {
		g.logger.Printf("gateway: record direct close of task %s: %v", taskID, err)
	}
}

// taskDelete removes one task from the board (the chip's "cancel").
func (g *Gateway) taskDelete(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params taskDeleteParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return nil, errInvalidParams("id", "is required")
	}
	board := g.taskBoard(strings.TrimSpace(params.SessionID))
	if board == nil {
		return nil, appError(codeNotReady, "task_board_unavailable", false)
	}
	// Remember the row before it disappears: the undo toast restores that very
	// row (the board hides deletes behind a tombstone, so its id and fields
	// survive), and the notice needs a subject a human can recognise.
	deleted := taskDTO{ID: id}
	for _, task := range g.tasksSnapshotOf(board, ctx) {
		if task.ID == id {
			deleted = task
			break
		}
	}
	if err := board.Delete(ctx, id); err != nil {
		return nil, internalError(err)
	}
	g.offerTaskUndo(ctx, board, deleted, params.SessionID)
	return g.publishTaskChange(ctx, params.SessionID, nil)
}

// tasksSnapshotOf reads the board's rows for one undo capture.
func (g *Gateway) tasksSnapshotOf(board contracts.TaskBoard, ctx context.Context) []taskDTO {
	rows, err := board.List(ctx)
	if err != nil {
		return nil
	}
	out := make([]taskDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskDTO{ID: row.ID, Subject: row.Subject})
	}
	return out
}

// offerTaskUndo posts the "撤销" toast for a deletion. The action id is the stable
// "restore" (the toast id keeps two pending undos apart), and the callback brings
// the same row back — never a re-created look-alike.
func (g *Gateway) offerTaskUndo(ctx context.Context, board contracts.TaskBoard, deleted taskDTO, sessionID string) {
	restorer, ok := board.(contracts.TaskBoardRestorer)
	if !ok {
		// A board that cannot restore gets a plain notice: promising an undo the
		// backend cannot honour would be worse than not showing one.
		g.notifier.Notify(contracts.NotifyInfo, "已删除任务："+deleted.Subject)
		return
	}
	label := "已删除任务"
	if strings.TrimSpace(deleted.Subject) != "" {
		label += "：" + deleted.Subject
	}
	g.notifier.NotifyActionNamed(contracts.NotifyInfo, label, "撤销", "restore", func() {
		if _, err := restorer.RestoreTask(context.Background(), deleted.ID); err != nil {
			g.logger.Printf("gateway: undo task %q: %v", deleted.ID, err)
			return
		}
		if err := g.hub.refreshNow(context.Background(), tasksDomainKey(sessionID)); err != nil {
			g.logger.Printf("gateway: refresh task board after undo: %v", err)
		}
	})
}

// publishTaskChange refreshes the board domain right after a gateway write, so
// the caller's reply carries the rev its own change landed on. The board has no
// change notification (§6.3), so unrelated mutations only reach clients on the
// next poll.
func (g *Gateway) publishTaskChange(ctx context.Context, sessionID string, task *contracts.BoardTask) (any, *protocolError) {
	key := tasksDomainKey(sessionID)
	if err := g.hub.refreshNow(ctx, key); err != nil {
		g.logger.Printf("gateway: refresh task board after write: %v", err)
	}
	rev := uint64(0)
	if snapshot, err := g.hub.snapshotDomain(ctx, key); err == nil {
		rev = snapshot.rev
	}
	result := taskWriteResult{Epoch: g.epoch, Rev: rev}
	if task != nil {
		result.Task = &taskDTO{
			ID:          task.ID,
			Subject:     task.Subject,
			Description: task.Description,
			Status:      task.Status,
			Owner:       task.Owner,
			BlockedBy:   task.BlockedBy,
			WriteScopes: task.WriteScopes,
			Created:     timePointer(task.Created),
			Updated:     timePointer(task.Updated),
		}
	}
	return result, nil
}

// maxSettingValueBytes bounds one settings value (the system prompt is the
// largest legitimate row and stays far below it).
const maxSettingValueBytes = 64 << 10

// validateSettingKey checks the shape of a settings key.
//
// The settings store is a generic key/value surface: it cannot tell "unknown" from
// "written by a module that was not loaded", so an unknown key is accepted (that
// is the contract). What the transport does reject is a key that cannot be one:
// empty, too long, or carrying characters no module name uses.
func validateSettingKey(key string) *protocolError {
	if key == "" {
		return errInvalidParams("key", "is required")
	}
	if len(key) > 128 {
		return errInvalidParams("key", "is longer than 128 bytes")
	}
	for i, char := range key {
		alphanumeric := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if !alphanumeric && char != '.' && char != '_' && char != '-' {
			return errInvalidParams("key", "contains an invalid character")
		}
		if i == 0 && !alphanumeric {
			return errInvalidParams("key", "must start with a letter or a digit")
		}
	}
	return nil
}

// activeSession reports the backend's active conversation.
func (g *Gateway) activeSession() (any, *protocolError) {
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	return activeSessionResult{SessionID: chat.ActiveSession()}, nil
}

// interjectRun stops the in-flight turn of a session and immediately starts a new one
// with the interjected text — "插话".
//
// This is steering by restart, not mid-turn injection: the rig agent loads history
// once per run and its TurnHook has no per-iteration seam, so a message cannot be
// slipped into the running conversation. Cancelling and resending gives the model the
// partial answer plus the new instruction, which is what a user needs, and does it in
// one hop so the client cannot race the cancel with a 409.
func (g *Gateway) interjectRun(ctx context.Context, raw json.RawMessage) (any, *protocolError) {
	var params runInterjectParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.SessionID) == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	if strings.TrimSpace(params.Text) == "" {
		return nil, errInvalidParams("text", "is required")
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	var attachments []contracts.Attachment
	if len(params.Attachments) > 0 {
		resolved, problem := g.resolveAttachments(params.Attachments)
		if problem != nil {
			return nil, problem
		}
		attachments = resolved
	}
	if _, found := chat.Session(params.SessionID); !found {
		return nil, appError(codeSessionNotFound, "session_not_found", false)
	}
	if err := chat.Cancel(params.SessionID); err != nil {
		return nil, internalError(err)
	}
	// The cancel is a request: wait (briefly) for the backend to settle, then start
	// the new turn. A run that refuses to stop is reported, not silently doubled.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var sendErr error
		if len(attachments) > 0 {
			sender, ok := chat.(contracts.AttachmentSender)
			if !ok {
				return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "attachments_not_supported")
			}
			sendErr = sender.SendWithAttachments(params.SessionID, params.Text, attachments)
		} else {
			sendErr = chat.Send(params.SessionID, params.Text)
		}
		if sendErr == nil {
			return map[string]any{"ok": true}, nil
		}
		if time.Now().After(deadline) {
			return nil, interjectSendError(sendErr)
		}
		// Retry only the two transient refusals (the previous turn has not
		// settled yet / history is compacting). Anything else — an unknown
		// session, a rejected prompt — is permanent, and retrying it for three
		// seconds hid the real error and blocked the caller doing it
		// (AUDIT-2026-11-24 C).
		if !errors.Is(sendErr, contracts.ErrSessionRunning) && !errors.Is(sendErr, contracts.ErrChatCompacting) {
			return nil, interjectSendError(sendErr)
		}
		select {
		case <-ctx.Done():
			return nil, appError(codeCancelled, "cancelled", false)
		case <-time.After(80 * time.Millisecond):
		}
	}
}

// interjectSendError maps the backend's refusal to a protocol error.
//
// The message stays the stable contract name (§4.3): the English error text of
// the agent backend is not a protocol message, and the code has to say what the
// client can do about it.
func interjectSendError(err error) *protocolError {
	switch {
	case errors.Is(err, contracts.ErrSessionRunning):
		return appError(codeBackpressure, "run_already_active", true)
	case errors.Is(err, contracts.ErrChatCompacting):
		return appError(codeBackpressure, "session_compacting", true)
	default:
		return internalError(err)
	}
}

// cancelRun requests that the in-flight run of a session stops. The stop is
// only confirmed by the session delta's running:false (§4.4).
func (g *Gateway) cancelRun(raw json.RawMessage) (any, *protocolError) {
	var params sessionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, errInvalidParams("sessionId", "is required")
	}
	chat, err := g.chatService()
	if err != nil {
		return nil, internalError(err)
	}
	// An unknown session is the client's mistake and has its own code (§4.3);
	// the backend's English "unknown chat session" text is not a protocol
	// message and must not be pasted into one (AUDIT-2026-11-24 C).
	if _, found := chat.Session(sessionID); !found {
		return nil, appError(codeSessionNotFound, "session_not_found", false)
	}
	if err := chat.Cancel(sessionID); err != nil {
		return nil, internalError(err)
	}
	return struct{}{}, nil
}

// setForeground records which conversation the user is watching, for the
// agent's "is the user looking at this?" notification rule.
func (g *Gateway) setForeground(c *client, raw json.RawMessage) (any, *protocolError) {
	var params foregroundParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	g.setForegroundSession(c.id, params.SessionID)
	return struct{}{}, nil
}

// decodeParams unmarshals a request's params into target.
func decodeParams(raw json.RawMessage, target any) *protocolError {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return errInvalidParams("params", err.Error())
	}
	return nil
}

// internalError maps an error to a protocol error, keeping cancellation
// distinct from a fault.
func internalError(err error) *protocolError {
	if err == nil {
		return errInternal("unknown error")
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return appError(codeCancelled, "cancelled", true)
	}
	return errInternal(err.Error())
}

// encode renders one ring entry as a notification frame.
func (entry ringEntry) encode() ([]byte, error) {
	if entry.payload == nil {
		return nil, errors.New("gateway: empty replay entry")
	}
	return encodeNotification(entry.payload.notification(), entry.payload)
}

// runInterjectParams is run.interject's body: the text to steer the session with.
type runInterjectParams struct {
	SessionID   string   `json:"sessionId"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}
