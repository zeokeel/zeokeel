// Package approvals implements ZeoKeel's approval backend module: the
// contracts.Approvals queue the agent gate and the approval UI share.
//
// It owns the approval policy ("workspace" (default), "read-only", "auto";
// "ask" and "never" remain headless-only assembly policies), the queue of
// pending tool-call approvals and the waiting/resolution handshake.
// The module is a kernel backend module: it requires the host logger, provides
// contracts.KeyApprovals and keeps all state in memory.
//
// # Semantics
//
//   - workspace (the default, mirrors the UI's 工作区可写 preset):
//     contracts.WorkspaceAutoAllowedTools (the read-only/informational tools
//     plus the workspace write tools) are allowed immediately; everything that
//     can leave the workspace — shell, network, MCP, settings writes — and
//     unknown tools ask.
//   - read-only (mirrors the UI's 只读 preset):
//     contracts.ReadOnlyAutoAllowedTools are allowed immediately and everything
//     else asks (the queue below).
//   - ask: every Request registers an "appr-<n>" pending entry, notifies
//     subscribers and blocks until Resolve answers it or the context ends.
//     Context cancellation removes the pending entry again. This policy is
//     headless-only: no UI permission mode maps to it.
//   - auto: Request returns ApprovalAllow immediately.
//   - never: Request returns ApprovalDeny immediately. Headless-only.
//
// Resolve is idempotent after the first answer: resolving an already answered
// ID again returns nil and does not change the answer. Resolving an ID that was
// never created (or whose waiting request was cancelled) returns
// ErrUnknownRequest. Invalid decisions return ErrInvalidDecision before touching
// the queue.
//
// Subscribers run synchronously outside the service lock; a panicking callback
// is logged and contained so a UI bug cannot break the agent run.
package approvals

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/gate"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/approvals"
	// ModeReadOnly allows the read-only/informational tools without asking and
	// queues everything else; it is the approvals policy of the UI's 只读 mode.
	ModeReadOnly = "read-only"
	// ModeAsk asks the user for every tool call. It is a headless assembly
	// policy only: no UI permission mode maps to it.
	ModeAsk = "ask"
	// ModeWorkspace (the default) allows the workspace file tools without
	// asking and queues everything else.
	ModeWorkspace = "workspace"
	// ModeAuto allows every request without asking (headless products).
	ModeAuto = "auto"
	// ModeNever denies every request without asking. It is a headless assembly
	// policy only: legacy stored "never" permission rows normalize to
	// read-only (see effectiveMode).
	ModeNever = "never"
	// requestIDPrefix is the prefix of generated approval IDs.
	requestIDPrefix = "appr-"
	// maxRememberedAnswers bounds the idempotency cache of resolved IDs.
	maxRememberedAnswers = 128
)

// Errors returned by Resolve.
var (
	// ErrUnknownRequest reports an ID that is not pending and was never
	// answered (or whose waiting request was cancelled).
	ErrUnknownRequest = errors.New("approvals: unknown request")
	// ErrInvalidDecision reports a decision outside allow/allow_always/deny.
	ErrInvalidDecision = errors.New("approvals: invalid decision")
)

// Config is the module's typed assembly config.
type Config struct {
	// Mode is the approval policy: "workspace" (default), "read-only", "ask",
	// "auto" or "never".
	Mode string `json:"mode"`
	// IgnorePermissionSetting makes the assembly mode authoritative: a stored
	// agent.permission row written by another product sharing the data
	// directory (for example the desktop app) cannot silently turn a headless
	// auto policy into one that waits forever for an approval nobody can give.
	// Desktop keeps the default (false): the user's runtime preset wins.
	IgnorePermissionSetting bool `json:"ignorePermissionSetting"`
}

// Module is the approvals backend descriptor. It provides
// contracts.KeyApprovals.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return err
		}
		settings, _ := ctx.Get(contracts.KeySettings)
		service, err := newServiceWithSettings(cfg.Mode, logger, settings)
		if err != nil {
			return err
		}
		service.ignorePermissionSetting = cfg.IgnorePermissionSetting
		// The notification copy follows the product language; a headless product
		// has no locale (and no notifier), so the zero value is harmless.
		service.locale, _ = ctx.Get(contracts.KeyLocale)
		// The optional notifier lets a parked run reach the user outside the
		// app (OS notification + in-app toast); a headless product without the
		// bridge keeps waiting silently exactly as before.
		if notifier, ok := ctx.Get(contracts.KeyNotifier); ok && notifier != nil {
			service.notifier = notifier
		}
		if _, err = ctx.Provide(contracts.KeyApprovals, contracts.Approvals(service)); err != nil {
			return err
		}
		// The action layer shares the same queue: desktop buttons, the mobile
		// approval page and chat replies all end up in Resolve.
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return fmt.Errorf("%s: token secret: %w", ModuleID, err)
		}
		_, err = ctx.Provide(contracts.KeyApprovalAction, contracts.ApprovalActionService(
			newActionService(service, actionConfig{Secret: secret})))
		return err
	}).
		AddProvides(module.Provides(contracts.KeyApprovals), module.Provides(contracts.KeyApprovalAction)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		WithName("ZeoKeel approvals backend")
}

// service is the contracts.Approvals implementation.
type service struct {
	mu     sync.Mutex
	mode   string
	logger contracts.Logger

	// settings is the optional permissions store: a non-empty
	// contracts.SettingAgentPermission overrides the assembly mode on every
	// call, so a runtime switch applies to the next tool call.
	settings contracts.SettingsService
	// ignorePermissionSetting pins the assembly mode for unattended products
	// (headless/ACP): a stored desktop preset cannot make them block on an
	// approval that has no answerer.
	ignorePermissionSetting bool

	// notifier is the optional host bridge used to notify the user that a run
	// parked on an approval (nil keeps the no-notification behavior).
	notifier contracts.Notifier

	// locale localizes the visible approval-waiting notification copy (nil
	// defaults to Chinese, matching the product's default language).
	locale contracts.LocaleService

	// queue is the SDK approval queue (ids, waiting, idempotent Resolve,
	// cancellation, subscriptions, optional timeout); the policy above and the
	// notice wording below stay here.
	queue *gate.Queue
}

var _ contracts.Approvals = (*service)(nil)

// newService validates and normalizes the configured mode.
func newService(mode string, logger contracts.Logger) (*service, error) {
	return newServiceWithSettings(mode, logger, nil)
}

// newServiceWithSettings builds the service with the optional permissions
// store (contracts.SettingAgentPermission). A nil store keeps the config mode.
func newServiceWithSettings(mode string, logger contracts.Logger, settings contracts.SettingsService) (*service, error) {
	normalized, err := normalizeMode(mode)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = discardLogger{}
	}
	svc := &service{
		mode:     normalized,
		logger:   logger,
		settings: settings,
	}
	svc.queue = gate.NewQueue(
		gate.WithQueuePrefix(requestIDPrefix),
		gate.WithQueueLogger(logger.Printf),
		gate.WithQueueResolvedLimit(maxRememberedAnswers),
		gate.WithQueueEnqueueHook(svc.approvalQueued),
	)
	return svc, nil
}

// normalizeMode canonicalizes one mode string; an empty config defaults to the
// workspace policy, which asks for nothing a normal workspace edit needs.
func normalizeMode(mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", ModeWorkspace:
		return ModeWorkspace, nil
	case ModeReadOnly, ModeAsk, ModeAuto, ModeNever:
		return normalized, nil
	default:
		return "", fmt.Errorf("approvals: invalid mode %q (want %q, %q, %q, %q or %q)",
			mode, ModeWorkspace, ModeReadOnly, ModeAsk, ModeAuto, ModeNever)
	}
}

// discardLogger drops approval log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// Mode implements contracts.Approvals: it reports the effective policy for the
// current permissions setting ("read-only", "workspace", "auto"; "ask"/"never"
// only through the headless assembly config).
func (s *service) Mode() string {
	return s.effectiveMode()
}

// effectiveMode resolves the configured mode and the optional permissions
// setting: a non-empty setting wins and is normalized through the three UI
// presets; the retired "ask"/"never" stored values normalize to read-only,
// unknown values degrade to workspace (the default) and the legacy "auto"
// value is an alias of full.
func (s *service) effectiveMode() string {
	s.mu.Lock()
	configured := s.mode
	s.mu.Unlock()
	if s.settings == nil || s.ignorePermissionSetting {
		return configured
	}
	raw := strings.ToLower(strings.TrimSpace(s.settings.Get(contracts.SettingAgentPermission, "")))
	if raw == "" {
		return configured
	}
	switch raw {
	case contracts.PermissionReadOnly, contracts.PermissionAsk, contracts.PermissionNever:
		return ModeReadOnly
	case contracts.PermissionWorkspace:
		return contracts.PermissionWorkspace
	case contracts.PermissionFull, ModeAuto:
		return ModeAuto
	default:
		return ModeWorkspace
	}
}

// Request implements contracts.Approvals: auto/never answer immediately, ask
// queues and blocks until Resolve or the context ends.
func (s *service) Request(ctx context.Context, request contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch mode := s.effectiveMode(); mode {
	case ModeAuto:
		return contracts.ApprovalAllow, nil
	case ModeNever:
		return contracts.ApprovalDeny, nil
	case ModeReadOnly:
		// Only the read-only/informational subset of the shared list is
		// auto-allowed here; workspace writes, network, settings writes and
		// unknown tools queue below.
		//
		// Shell and MCP do not reach this service at all any more: the agent gate
		// (internal/modules/agent/plan_mode.go) exempts both families before it
		// calls Request (personal-helper permission model, 2026-11-26), so a
		// Request for them would only happen from a stripped assembly. Keep the
		// list checks below them unchanged — they are the honest answer if one
		// ever arrives.
		if contracts.ReadOnlyAutoAllowed(request.Tool) {
			return contracts.ApprovalAllow, nil
		}
	case ModeWorkspace:
		// One frozen list is shared with the agent gate
		// (contracts.WorkspaceAutoAllowedTools): the workspace file tools plus
		// read-only/informational tools. Network and settings writes (and unknown
		// tools) still queue below.
		//
		// Shell and MCP are exempt in the gate before this call: they never enter
		// the queue in the shipping products, so their absence from the lists
		// above is not a gap in this table.
		if contracts.WorkspaceAutoAllowed(request.Tool) {
			return contracts.ApprovalAllow, nil
		}
	}

	// RG-04 (personal-scale ruling): an OPT-IN timeout for automation-origin
	// requests only. Default 0 = off; interactive approvals keep waiting.
	var timeout time.Duration
	if s.settings != nil && strings.EqualFold(strings.TrimSpace(request.Source), contracts.SessionOriginAutomation) {
		if seconds := contracts.NormalizeApprovalTimeoutSeconds(
			s.settings.Get(contracts.SettingAgentApprovalTimeoutSeconds, "")); seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}
	decision, err := s.queue.AskTimeout(ctx, gate.QueueRequest{
		Tool:      request.Tool,
		Summary:   request.Summary,
		Arguments: request.Arguments,
		SessionID: request.SessionID,
		Source:    request.Source,
	}, timeout)
	switch {
	case errors.Is(err, gate.ErrTimeout):
		// The queue recorded the denial: a late Resolve stays idempotent and the
		// gate reports a normal tool denial the run can continue from.
		s.logger.Printf("[approvals] request %s (%s, session %s) auto-denied after approval timeout",
			request.ID, request.Tool, request.SessionID)
		if s.notifier != nil {
			s.notifyApprovalIssue(s.approvalTimeoutText(request), request.SessionID)
		}
		return contracts.ApprovalDeny, nil
	case err != nil:
		return "", err
	}
	return approvalDecisionFromRig(decision), nil
}

// approvalQueued is the queue's enqueue hook: tell the host that a run parked on
// an approval (OS notification + in-app toast). The owning session travels as
// the notification's session tag when the host keeps one.
func (s *service) approvalQueued(request gate.QueueRequest) {
	if s.notifier == nil {
		return
	}
	s.notifyApprovalIssue(s.approvalWaitingText(contracts.ApprovalRequest{
		Tool: request.Tool, SessionID: request.SessionID, Source: request.Source,
	}), request.SessionID)
}

// notifyApprovalIssue sends one approval notice, tagged with the session when
// the notifier supports it.
func (s *service) notifyApprovalIssue(text, sessionID string) {
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		if sessionNotifier, ok := s.notifier.(contracts.SessionNotifier); ok && sessionNotifier != nil {
			sessionNotifier.NotifySystemFor(contracts.NotifyWarning, text, sessionID)
			return
		}
	}
	contracts.NotifyApprovalWaiting(s.notifier, text)
}

// approvalDecisionFromRig maps the SDK's decision vocabulary to the product's.
func approvalDecisionFromRig(decision rig.Decision) contracts.ApprovalDecision {
	switch decision {
	case rig.DecisionAllow:
		return contracts.ApprovalAllow
	case rig.DecisionAllowAlways:
		return contracts.ApprovalAllowAlways
	default:
		return contracts.ApprovalDeny
	}
}

// approvalDecisionToRig is the inverse mapping used by Resolve.
func approvalDecisionToRig(decision contracts.ApprovalDecision) (rig.Decision, bool) {
	switch decision {
	case contracts.ApprovalAllow:
		return rig.DecisionAllow, true
	case contracts.ApprovalAllowAlways:
		return rig.DecisionAllowAlways, true
	case contracts.ApprovalDeny:
		return rig.DecisionDeny, true
	default:
		return "", false
	}
}

// approvalTimeoutText is the visible copy of the automation approval-timeout
// auto-deny notification (the waiting notice's sibling): it says the unattended
// request was denied because nobody answered within the configured window.
func (s *service) approvalTimeoutText(request contracts.ApprovalRequest) string {
	english := contracts.Language(s.locale) == "en"
	name := strings.TrimSpace(request.Tool)
	if english {
		return "Approval timed out and was auto-denied: " + name + " (automation)"
	}
	return "审批超时已自动拒绝：" + name + "（自动化）"
}

// approvalWaitingText is the visible copy of the approval-waiting notification.
// The payload is the stable tool id, localised at render time by the toast
// overlay; a background origin rides the translatable affix suffix.
func (s *service) approvalWaitingText(request contracts.ApprovalRequest) string {
	english := contracts.Language(s.locale) == "en"
	text := "审批等待："
	if english {
		text = "Approval waiting: "
	}
	text += strings.TrimSpace(request.Tool)
	if source := approvalSourceLabel(s.locale, request.Source); source != "" {
		if english {
			text += " (" + source + ")"
		} else {
			text += "（" + source + "）"
		}
	}
	return text
}

// approvalSourceLabel localizes a non-user run origin ("automation",
// "subagent"). The active human turn needs no label, and an unknown origin is
// omitted rather than leaking a raw origin id into visible copy.
func approvalSourceLabel(locale contracts.LocaleService, source string) string {
	english := contracts.Language(locale) == "en"
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "user":
		return ""
	case contracts.SessionOriginAutomation:
		if english {
			return "Automation"
		}
		return "自动化"
	case contracts.SessionOriginSubagent:
		if english {
			return "Subagent"
		}
		return "子代理"
	default:
		return ""
	}
}

// Pending implements contracts.Approvals.
func (s *service) Pending() []contracts.ApprovalRequest {
	queued := s.queue.Pending()
	requests := make([]contracts.ApprovalRequest, 0, len(queued))
	for _, request := range queued {
		requests = append(requests, contracts.ApprovalRequest{
			ID:        request.ID,
			Tool:      request.Tool,
			Summary:   request.Summary,
			Arguments: request.Arguments,
			CreatedAt: request.CreatedAt,
			SessionID: request.SessionID,
			Source:    request.Source,
		})
	}
	return requests
}

// Resolve implements contracts.Approvals: it answers one pending request. A
// second Resolve of an answered ID is a no-op; unknown IDs and invalid
// decisions are reported with the module's sentinels.
func (s *service) Resolve(id string, decision contracts.ApprovalDecision) error {
	rigDecision, ok := approvalDecisionToRig(decision)
	if !ok {
		return fmt.Errorf("%w: %q", ErrInvalidDecision, decision)
	}
	err := s.queue.Resolve(strings.TrimSpace(id), rigDecision)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gate.ErrUnknownRequest):
		return fmt.Errorf("%w: %q", ErrUnknownRequest, id)
	case errors.Is(err, gate.ErrInvalidDecision):
		return fmt.Errorf("%w: %q", ErrInvalidDecision, decision)
	default:
		return err
	}
}

// Subscribe implements contracts.Approvals: the UI redraws whenever the pending
// set changes (the SDK queue runs subscribers outside its lock and contains
// panics).
func (s *service) Subscribe(fn func()) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	return s.queue.Subscribe(fn)
}
