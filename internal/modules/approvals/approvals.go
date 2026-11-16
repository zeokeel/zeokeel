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
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
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
		_, err = ctx.Provide(contracts.KeyApprovals, contracts.Approvals(service))
		return err
	}).
		AddProvides(module.Provides(contracts.KeyApprovals)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		WithName("ZeoKeel approvals backend")
}

// resolution is one answer delivered to a waiting Request.
type resolution struct {
	decision contracts.ApprovalDecision
	err      error
}

// pendingRequest is one waiting approval.
type pendingRequest struct {
	request contracts.ApprovalRequest
	result  chan resolution
}

// subscription is one Subscribe callback.
type subscription struct {
	fn func()
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

	nextSeq uint64
	pending map[string]*pendingRequest

	// resolved remembers the first answer per ID for idempotent Resolve calls.
	resolved      map[string]contracts.ApprovalDecision
	resolvedOrder []string

	subscribers []*subscription
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
	return &service{
		mode:     normalized,
		logger:   logger,
		settings: settings,
		pending:  make(map[string]*pendingRequest),
		resolved: make(map[string]contracts.ApprovalDecision),
	}, nil
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
		// auto-allowed; workspace writes, shell, network, MCP, settings writes
		// and unknown tools queue below.
		if contracts.ReadOnlyAutoAllowed(request.Tool) {
			return contracts.ApprovalAllow, nil
		}
	case ModeWorkspace:
		// One frozen list is shared with the agent gate
		// (contracts.WorkspaceAutoAllowedTools): the workspace file tools plus
		// read-only/informational tools. Shell, network, MCP, settings writes and
		// unknown tools still queue below.
		if contracts.WorkspaceAutoAllowed(request.Tool) {
			return contracts.ApprovalAllow, nil
		}
	}

	s.mu.Lock()
	s.nextSeq++
	id := requestIDPrefix + strconv.FormatUint(s.nextSeq, 10)
	request.ID = id
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now()
	}
	entry := &pendingRequest{request: request, result: make(chan resolution, 1)}
	s.pending[id] = entry
	s.mu.Unlock()
	s.notify()
	if s.notifier != nil {
		// The owning session travels as the notification's session tag when the
		// host keeps one (contracts.SessionNotifier), so a click on the OS
		// notification opens the conversation that is waiting. Plain notifiers
		// still get the toast.
		text := s.approvalWaitingText(request)
		if sessionID := strings.TrimSpace(request.SessionID); sessionID != "" {
			if sessionNotifier, ok := s.notifier.(contracts.SessionNotifier); ok && sessionNotifier != nil {
				sessionNotifier.NotifySystemFor(contracts.NotifyWarning, text, sessionID)
			} else {
				contracts.NotifyApprovalWaiting(s.notifier, text)
			}
		} else {
			contracts.NotifyApprovalWaiting(s.notifier, text)
		}
	}

	// RG-04 (2026-11 audit, personal-scale ruling): an OPT-IN timeout for
	// automation-origin requests only. Default 0 = off — interactive approvals
	// keep waiting exactly as before (the run-level wall-clock breaker still
	// backstops every run). When configured, an unattended request nobody
	// answers is auto-denied, recorded and notified instead of parking until
	// the whole run is cancelled.
	var timeoutC <-chan time.Time
	if s.settings != nil && strings.EqualFold(strings.TrimSpace(request.Source), contracts.SessionOriginAutomation) {
		if seconds := contracts.NormalizeApprovalTimeoutSeconds(
			s.settings.Get(contracts.SettingAgentApprovalTimeoutSeconds, "")); seconds > 0 {
			timer := time.NewTimer(time.Duration(seconds) * time.Second)
			defer timer.Stop()
			timeoutC = timer.C
		}
	}

	select {
	case resolved := <-entry.result:
		return resolved.decision, resolved.err
	case <-timeoutC:
		s.mu.Lock()
		timedOut := false
		if current, ok := s.pending[id]; ok && current == entry {
			delete(s.pending, id)
			// Record the deny: a late Resolve stays idempotent, and the gate
			// reports a normal tool denial the run can continue from.
			s.rememberAnswerLocked(id, contracts.ApprovalDeny)
			timedOut = true
		}
		s.mu.Unlock()
		if !timedOut {
			// A human answer landed between the timer firing and this lock:
			// deliver it instead of the deny.
			resolved := <-entry.result
			return resolved.decision, resolved.err
		}
		s.notify()
		s.logger.Printf("[approvals] request %s (%s, session %s) auto-denied after approval timeout",
			id, request.Tool, request.SessionID)
		if s.notifier != nil {
			text := s.approvalTimeoutText(request)
			if sessionID := strings.TrimSpace(request.SessionID); sessionID != "" {
				if sessionNotifier, ok := s.notifier.(contracts.SessionNotifier); ok && sessionNotifier != nil {
					sessionNotifier.NotifySystemFor(contracts.NotifyWarning, text, sessionID)
				} else {
					contracts.NotifyApprovalWaiting(s.notifier, text)
				}
			} else {
				contracts.NotifyApprovalWaiting(s.notifier, text)
			}
		}
		return contracts.ApprovalDeny, nil
	case <-ctx.Done():
		s.mu.Lock()
		if current, ok := s.pending[id]; ok && current == entry {
			// Nobody answered: drop the pending request and tell the UI.
			delete(s.pending, id)
			s.mu.Unlock()
			s.notify()
		} else {
			s.mu.Unlock()
		}
		return "", ctx.Err()
	}
}

// approvalTimeoutText is the visible copy of the automation approval-timeout
// auto-deny notification (the waiting notice's sibling): it names the tool by
// its friendly localized name and says the unattended request was denied
// because nobody answered within the configured window.
func (s *service) approvalTimeoutText(request contracts.ApprovalRequest) string {
	english := uikit.Language(s.locale) == "en"
	name := uikit.ToolDisplayName(s.locale, request.Tool)
	if name == "" || name == strings.TrimSpace(request.Tool) {
		name = "工具调用"
		if english {
			name = "a tool call"
		}
	}
	if english {
		return "Approval timed out and was auto-denied: " + name + " (automation)"
	}
	return "审批超时已自动拒绝：" + name + "（自动化）"
}

// approvalWaitingText is the visible copy of the approval-waiting notification.
// It names the tool by its friendly, localized display name and never by the raw
// protocol id (the raw id stays in the approval dialog's badge tooltip and the
// trajectory, where the tool row owns it); a request from a background run
// carries its localized origin so the user knows why it appeared.
func (s *service) approvalWaitingText(request contracts.ApprovalRequest) string {
	english := uikit.Language(s.locale) == "en"
	text := "审批等待："
	if english {
		text = "Approval waiting: "
	}
	name := uikit.ToolDisplayName(s.locale, request.Tool)
	// A tool the kit does not know has no friendly name; its raw protocol id
	// must not reach visible copy, so the notification falls back to a generic
	// label (the approval dialog still shows the request's details).
	if name == "" || name == strings.TrimSpace(request.Tool) {
		name = "工具调用"
		if english {
			name = "a tool call"
		}
	}
	text += name
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
	english := uikit.Language(locale) == "en"
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
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]contracts.ApprovalRequest, 0, len(s.pending))
	for _, entry := range s.pending {
		requests = append(requests, entry.request)
	}
	sort.SliceStable(requests, func(i, j int) bool {
		if !requests[i].CreatedAt.Equal(requests[j].CreatedAt) {
			return requests[i].CreatedAt.Before(requests[j].CreatedAt)
		}
		return requestSequence(requests[i].ID) < requestSequence(requests[j].ID)
	})
	return requests
}

// requestSequence parses the numeric suffix of a generated approval ID.
func requestSequence(id string) uint64 {
	if !strings.HasPrefix(id, requestIDPrefix) {
		return 0
	}
	sequence, err := strconv.ParseUint(strings.TrimPrefix(id, requestIDPrefix), 10, 64)
	if err != nil {
		return 0
	}
	return sequence
}

// Resolve implements contracts.Approvals. The first answer wins; resolving an
// already answered ID again returns nil without changing the answer.
func (s *service) Resolve(id string, decision contracts.ApprovalDecision) error {
	if !validDecision(decision) {
		return fmt.Errorf("%w: %q", ErrInvalidDecision, decision)
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok {
		_, answered := s.resolved[id]
		s.mu.Unlock()
		if answered {
			return nil // idempotent after the first answer
		}
		return fmt.Errorf("%w: %q", ErrUnknownRequest, id)
	}
	delete(s.pending, id)
	s.rememberAnswerLocked(id, decision)
	entry.result <- resolution{decision: decision}
	s.mu.Unlock()
	s.notify()
	return nil
}

// rememberAnswerLocked records the first answer for idempotent Resolve calls.
func (s *service) rememberAnswerLocked(id string, decision contracts.ApprovalDecision) {
	s.resolved[id] = decision
	s.resolvedOrder = append(s.resolvedOrder, id)
	if len(s.resolvedOrder) <= maxRememberedAnswers {
		return
	}
	oldest := s.resolvedOrder[0]
	s.resolvedOrder = s.resolvedOrder[1:]
	delete(s.resolved, oldest)
}

// validDecision reports whether decision is one of the three contract values.
func validDecision(decision contracts.ApprovalDecision) bool {
	switch decision {
	case contracts.ApprovalAllow, contracts.ApprovalAllowAlways, contracts.ApprovalDeny:
		return true
	default:
		return false
	}
}

// Subscribe implements contracts.Approvals.
func (s *service) Subscribe(fn func()) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	listener := &subscription{fn: fn}
	s.mu.Lock()
	s.subscribers = append(s.subscribers, listener)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for index, candidate := range s.subscribers {
				if candidate == listener {
					s.subscribers = append(s.subscribers[:index], s.subscribers[index+1:]...)
					return
				}
			}
		})
	}, nil
}

// notify calls every subscriber outside the service lock, containing panics.
func (s *service) notify() {
	s.mu.Lock()
	subscribers := append([]*subscription(nil), s.subscribers...)
	s.mu.Unlock()
	for _, listener := range subscribers {
		if listener == nil || listener.fn == nil {
			continue
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logger.Printf("[approvals] subscriber panicked: %v", recovered)
				}
			}()
			listener.fn()
		}()
	}
}
