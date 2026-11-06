package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// HeadlessRuntime is the long-lived, event-streaming agent runtime the ACP
// server (and other out-of-process clients) drive instead of the CLI-shaped
// RunHeadless. It owns one session stack and serves many sessions concurrently:
// one session.Controller with one runtime.Agent per session (RT-04), each bound
// to the workspace directory passed to NewSession.
//
// Rules (see internal/acp/doc.go):
//   - It never writes to stdout: diagnostics go through the optional Logger
//     (default: the std logger, i.e. stderr).
//   - It never takes the desktop single-instance lock.
//   - Approval requests are auto-denied by default (no UI); a later ACP round
//     can map session/request_permission onto the approval hook.
//   - Subscribe streams the per-turn semantic events of one session, in order,
//     for the ACP projection (text/reasoning, tools, usage, terminal status).
type HeadlessRuntime interface {
	// NewSession creates a session bound to the absolute directory cwd and
	// returns its id. A cwd that is not an existing directory is an error.
	NewSession(ctx context.Context, cwd string) (sessionID string, err error)
	// Submit starts one prompt on sessionID. It returns once the turn is
	// accepted; completion is observed through Subscribe.
	Submit(ctx context.Context, sessionID, text string) error
	// Cancel stops sessionID's in-flight turn (a no-op for unknown sessions).
	Cancel(ctx context.Context, sessionID string) error
	// Subscribe streams the session's semantic events. The unsubscribe func is
	// idempotent and closes the channel; call it before Close.
	Subscribe(sessionID string) (<-chan state.Event, func(), error)
	// Close stops every turn, closes per-session agents/bundles and the store.
	Close(ctx context.Context) error
}

// HeadlessRuntimeConfig configures NewHeadlessRuntime.
type HeadlessRuntimeConfig struct {
	// DataDir is the ZeoKeel data directory; empty uses DataDir()
	// (ZEOKEEL_DATA_DIR or ~/.zeokeel). A dedicated directory keeps an ACP
	// client's sessions away from the desktop app's.
	DataDir string
	// Logger receives diagnostics (never stdout); nil uses the std logger.
	Logger func(format string, args ...any)
}

// headlessRuntime is the HeadlessRuntime implementation.
type headlessRuntime struct {
	app     *app
	ctrl    *session.Controller
	cleanup func() error
	logger  func(format string, args ...any)

	mu     sync.Mutex
	closed bool
}

// NewHeadlessRuntime builds the headless session stack. It returns an error when
// the data directory cannot be opened or no model is configured.
func NewHeadlessRuntime(cfg HeadlessRuntimeConfig) (HeadlessRuntime, error) {
	dir := strings.TrimSpace(cfg.DataDir)
	if dir == "" {
		dir = DataDir()
	}
	if dir == "" {
		return nil, errors.New("app: headless runtime: empty data directory")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Printf
	}

	a := &app{}
	var startup *AgentBundle
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		root := workspaceRootForStore(st, dir)
		bundle, berr := buildAgentWithOptions(st, root, DefaultInstruction, a.toolOptions(st, root, ""))
		startup = bundle
		return bundle, berr
	})
	if err != nil {
		return nil, fmt.Errorf("app: headless runtime: %w", err)
	}
	if startup == nil {
		_ = cleanup()
		return nil, errors.New("app: headless runtime: no model is configured (set provider.api_key / the default model, or DEEPSEEK_API_KEY; local providers may omit the key)")
	}

	a.services = ctrl
	a.bundle = startup
	ctrl.SetModelConfiguredFunc(a.hasConfiguredModel)
	if startup.Agent != nil {
		ctrl.SetAgentModeSwitcher(startup.Agent.SetMode)
	}
	ctrl.SetAgentRebuilder(a.rebuildAgent)
	ctrl.SetGoalStore(storeToolState{st: ctrl.Store()})
	// One agent per ACP session: sessions of different clients/targets run in
	// parallel and an approval wait is local to its session (RT-04).
	a.installSessionAgentFactory(ctrl)
	a.sessionFactory = true
	// No UI: every gated tool call is denied immediately, so a prompt can never
	// hang waiting for a human. A later ACP round maps session/request_permission
	// onto this hook instead.
	ctrl.SetOnApprovalRequested(func(_ string, request state.ApprovalRequest) {
		ctrl.Deny(request.CallID)
	})

	return &headlessRuntime{app: a, ctrl: ctrl, cleanup: cleanup, logger: logger}, nil
}

// ensureWorkspaceForCwd returns the id of the workspace rooted at cwd, creating
// one when needed.
func (r *headlessRuntime) ensureWorkspaceForCwd(cwd string) (string, error) {
	st := r.ctrl.Store()
	workspaces, err := st.ListWorkspaces()
	if err != nil {
		return "", fmt.Errorf("app: headless runtime: list workspaces: %w", err)
	}
	for _, ws := range workspaces {
		root := strings.TrimSpace(ws.Root)
		if root == "" {
			continue
		}
		if abs, aerr := filepath.Abs(root); aerr == nil && abs == cwd {
			return ws.ID, nil
		}
	}
	name := filepath.Base(cwd)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "ACP workspace"
	}
	ws, err := st.CreateWorkspace(name, cwd)
	if err != nil {
		return "", fmt.Errorf("app: headless runtime: create workspace %q: %w", cwd, err)
	}
	return ws.ID, nil
}

// NewSession implements HeadlessRuntime.
func (r *headlessRuntime) NewSession(ctx context.Context, cwd string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return "", errors.New("app: headless runtime: closed")
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("app: headless runtime: cwd is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("app: headless runtime: resolve cwd %q: %w", cwd, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("app: headless runtime: cwd %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("app: headless runtime: cwd %q is not a directory", abs)
	}
	workspaceID, err := r.ensureWorkspaceForCwd(abs)
	if err != nil {
		return "", err
	}
	sessionID, err := r.ctrl.CreateSessionInWorkspace(ctx, workspaceID, "ACP session")
	if err != nil {
		return "", fmt.Errorf("app: headless runtime: create session: %w", err)
	}
	return sessionID, nil
}

// Submit implements HeadlessRuntime.
func (r *headlessRuntime) Submit(ctx context.Context, sessionID, text string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return errors.New("app: headless runtime: closed")
	}
	if err := r.ctrl.Submit(ctx, sessionID, text); err != nil {
		return fmt.Errorf("app: headless runtime: submit: %w", err)
	}
	return nil
}

// Cancel implements HeadlessRuntime.
func (r *headlessRuntime) Cancel(ctx context.Context, sessionID string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return errors.New("app: headless runtime: closed")
	}
	r.ctrl.StopSession(sessionID)
	return nil
}

// Subscribe implements HeadlessRuntime.
func (r *headlessRuntime) Subscribe(sessionID string) (<-chan state.Event, func(), error) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, nil, errors.New("app: headless runtime: closed")
	}
	return r.ctrl.SubscribeSessionEvents(sessionID)
}

// Close implements HeadlessRuntime. It is idempotent.
func (r *headlessRuntime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	_ = ctx
	// Drop and close per-session agent bundles, then the shared stack.
	r.app.services = r.ctrl
	r.app.services.InvalidateAgents()
	r.app.closeSessionBundles()
	if err := r.ctrl.Close(); err != nil {
		return fmt.Errorf("app: headless runtime: close controller: %w", err)
	}
	if r.cleanup != nil {
		if err := r.cleanup(); err != nil {
			return fmt.Errorf("app: headless runtime: cleanup: %w", err)
		}
	}
	return nil
}
