// Package gateway is ZeoKeel's gateway product: the backend of the browser
// client.
//
// It is the transport that is not the native window: the process serves the
// state plane over WebSocket (snapshot + delta domains, docs/TRANSPORT.md §4/§6)
// and the per-run token stream over `POST /api/chat/run` (SSE, §4.6). It never
// creates a window and never imports github.com/egoist/mygo: the native tray
// shell is a separate, tiny process.
//
// Package layout mirrors docs/TRANSPORT.md §9.4:
//
//	gateway.go   Options, the Gateway lifecycle (lock → bind → gateway.json → serve)
//	port.go      port negotiation, gateway.json, single-instance lock
//	auth.go      process token, cookie/#fragment exchange, Host/Origin checks
//	ws.go        server-side RFC 6455 framing
//	proto.go     JSON-RPC 2.0 frames, error codes, newline framing
//	dto.go       the wire shape of the read models
//	delta.go     snapshot diffing (session + session list)
//	hub.go       per-domain epoch/rev/ring, subscribers, per-client outbox
//	snapshot.go  on-demand snapshot builds and subscribe/replay
//	client.go    one connection: outbox, read loop, frame dispatch
//	methods.go   the method table
//	services.go  the host services the headless assembly never provides
//	server.go    HTTP: client bundle, /auth, /ws, /quit
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/gateway/webclient"
)

// gateway errors the host maps to process behaviour.
var (
	// ErrAlreadyRunning means another gateway owns this data directory; the
	// error's message names the running instance's URL (§2.4).
	ErrAlreadyRunning = errAlreadyRunning
	// ErrNoDataDir means the gateway was started without a data directory.
	ErrNoDataDir = errors.New("gateway: a data directory is required")
)

// defaultProduct is the name published in gateway.json when the host does not
// pass one. It matches assembly.ProductServe; the gateway package deliberately
// does not import internal/assembly (that package registers the native UI
// modules and would drag github.com/egoist/mygo into the gateway binary).
const defaultProduct = "serve"

// Options configures one gateway process.
type Options struct {
	// DataDir is where gateway.json, gateway.lock, settings and logs live.
	DataDir string
	// Port is the first port to try: 0 means the documented default range
	// (9932..9941 then a kernel port); a negative value asks the kernel
	// directly (tests).
	Port int
	// Product and Version are published in gateway.json for the tray shell.
	Product string
	Version string
	// Logger is the process log; the token is never written to it.
	Logger contracts.Logger
	// Out receives the one-line "open this URL" message. Defaults to io.Discard.
	Out io.Writer
}

// Gateway is one running gateway process.
type Gateway struct {
	opts  Options
	epoch string
	token string
	info  Info
	auth  *auth
	// blobs holds uploaded attachments between POST /api/blobs and the run that
	// names them (TRANSPORT §6.4).
	blobs *blobStore

	root     cordis.Context
	serveCtx context.Context
	hub      *hub
	listener net.Listener
	server   *http.Server
	// handlers tracks in-flight HTTP handlers (including hijacked WS readers and
	// the SSE run streams) so shutdown can wait for them before the assembly is
	// disposed: they keep touching backend services until their connection dies.
	handlers sync.WaitGroup
	webFS    fs.FS
	lock     *fileLock
	logger   contracts.Logger
	out      io.Writer

	sequence atomic.Uint64

	// Services (the five keys the headless assembly never provides).
	interaction *interactionService
	artifacts   *artifactService
	plan        *planService
	notifier    *notifyService
	foreground  *foregroundService

	// Service state, guarded by mu.
	mu              sync.Mutex
	questions       map[string]*pendingQuestion
	questionHandler func(contracts.UserQuestion)
	artifactHandler func(contracts.Artifact)
	artifactItems   []contracts.Artifact
	toastHandler    func(contracts.Notification)
	// toastActions holds the not-yet-clicked action of each posted toast, keyed
	// by the toast id. The action id is stored inside, so a caller may reuse one
	// stable id ("restore") across toasts without two pending undos colliding.
	toastActions       map[string]*notificationAction
	foregroundByClient map[string]foregroundEntry
	foregroundTick     uint64

	// pollInterval overrides the settings/workspace poll cadence (tests).
	pollInterval time.Duration

	// Run streams for POST /api/chat/run, keyed by clientRunId (idempotency).
	runMu sync.Mutex
	runs  map[string]*runStream

	// inFlight tracks the control-plane requests that run on their own goroutine
	// because they may block (team.wait, team.send, run.interject), keyed by the
	// JSON-RPC request id, so $/cancel can actually cancel them
	// (AUDIT-2026-11-24 B-12).
	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc

	shutdownOnce   sync.Once
	shutdown       chan struct{}
	shutdownReason atomic.Value
}

// New creates a gateway for one process run and claims the data directory.
//
// The single-instance lock is taken here, not in Serve: a second launch must
// fail before the assembly opens the shared database and the log. Nothing is
// bound and gateway.json is not written until Serve runs, so the host can
// provide the services before the assembly loads (they must exist during module
// activation). A caller that never serves must call Close to release the lock.
func New(root cordis.Context, opts Options) (*Gateway, error) {
	if opts.DataDir == "" {
		return nil, ErrNoDataDir
	}
	if opts.Logger == nil {
		return nil, errors.New("gateway: a logger is required")
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Product == "" {
		opts.Product = defaultProduct
	}
	token, epoch, err := randomToken()
	if err != nil {
		return nil, err
	}
	g := &Gateway{
		opts:               opts,
		epoch:              epoch,
		token:              token,
		root:               root,
		logger:             opts.Logger,
		out:                opts.Out,
		questions:          make(map[string]*pendingQuestion),
		toastActions:       make(map[string]*notificationAction),
		foregroundByClient: make(map[string]foregroundEntry),
		shutdown:           make(chan struct{}),
		blobs:              newBlobStore(),
	}
	// The host creates the data directory before calling New; a standalone
	// embedder (or a first run) may not have, and failing here must be a clear
	// error rather than a raw "open …gateway.lock: no such file".
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("gateway: create data dir %q: %w", opts.DataDir, err)
	}
	lock, err := acquireLock(opts.DataDir)
	if err != nil {
		return nil, err
	}
	g.lock = lock
	g.interaction = &interactionService{gw: g}
	g.artifacts = &artifactService{gw: g}
	g.plan = newPlanService(g)
	g.notifier = &notifyService{gw: g}
	g.foreground = &foregroundService{gw: g}
	return g, nil
}

// Provide registers the five host services on the kernel root. It must run
// before the assembly loads: the host-tools module reads them during activation
// and registers its capabilities from what it finds.
func (g *Gateway) Provide(root cordis.Context) error {
	if _, err := root.Provide(contracts.KeyUserInteraction, contracts.UserInteraction(g.interaction)); err != nil {
		return fmt.Errorf("gateway: provide user interaction: %w", err)
	}
	if _, err := root.Provide(contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(g.artifacts)); err != nil {
		return fmt.Errorf("gateway: provide artifact presenter: %w", err)
	}
	if _, err := root.Provide(contracts.KeyPlanReviewer, contracts.PlanReviewer(g.plan)); err != nil {
		return fmt.Errorf("gateway: provide plan reviewer: %w", err)
	}
	if _, err := root.Provide(contracts.KeyNotifier, contracts.Notifier(g.notifier)); err != nil {
		return fmt.Errorf("gateway: provide notifier: %w", err)
	}
	if _, err := root.Provide(contracts.KeyForegroundSession, contracts.ForegroundSession(g.foreground)); err != nil {
		return fmt.Errorf("gateway: provide foreground session: %w", err)
	}
	return nil
}

// Info returns this process's published description. It is valid only after
// Serve has bound the listener.
func (g *Gateway) Info() Info { return g.info }

// Epoch returns this process's random epoch.
func (g *Gateway) Epoch() string { return g.epoch }

// Serve binds the listener, publishes gateway.json, starts the hub and the HTTP
// server, and blocks until ctx is canceled or a client asks to quit.
func (g *Gateway) Serve(ctx context.Context) error {
	defer g.releaseLock()

	listener, err := listen(g.opts.Port)
	if err != nil {
		return err
	}
	g.listener = listener
	port := listener.Addr().(*net.TCPAddr).Port
	g.info = newInfo(g.epoch, port, g.token, g.opts.Product, g.serverVersion())
	if err := writeInfo(g.opts.DataDir, g.info); err != nil {
		_ = listener.Close()
		return err
	}
	defer removeInfo(g.opts.DataDir)
	g.auth = newAuth(g.token, g.info)

	chat, err := g.root.Require(contracts.KeyChat)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("gateway: require the chat service (is the agent module loaded?): %w", err)
	}
	g.serveCtx = ctx
	g.hub = newHub(g.epoch, chat)
	g.hub.sources = &domainSources{gateway: g}
	if err := g.hub.start(ctx); err != nil {
		_ = listener.Close()
		return fmt.Errorf("gateway: subscribe chat: %w", err)
	}
	hub := g.hub
	defer hub.stop()
	// Push sources for the small domains: the approvals service notifies on
	// every pending-set change, so the browser sees it immediately.
	if approvals, ok := g.root.Get(contracts.KeyApprovals); ok && approvals != nil {
		if unsub, err := approvals.Subscribe(func() { g.hub.markDomainDirty(domainApprovals) }); err == nil {
			defer unsub()
		} else {
			g.logger.Printf("gateway: subscribe approvals: %v", err)
		}
	}
	// Pull sources: the settings store and the workspace registry have no
	// change notification at all (contracts.SettingsService is Get/Set/All, the
	// workspace service has no Subscribe). Rather than pretend they push, the
	// gateway polls them and publishes a delta when the content actually
	// changed — the rev only moves on a real change, so an idle poll is free.
	go g.pollDomains(ctx)

	g.server = &http.Server{
		Handler:           g.handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		err := g.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	// The URL is the only place the token is printed; the log carries the port
	// alone (§3.1).
	if bundle, ok := webclient.Fingerprint(); ok {
		g.logger.Printf("gateway: %s", bundle.Line)
	} else {
		g.logger.Printf("gateway: no browser client embedded (GET / answers 503; run scripts/build-web-client.sh)")
	}
	fmt.Fprintf(g.out, "gateway: %s\n", g.info.URL())
	fmt.Fprintf(g.out, "gateway.json: %s\n", infoPath(g.opts.DataDir, InfoFileName))
	g.logger.Printf("gateway: listening on %s (epoch %s)", g.info.Origin(), g.epoch)

	select {
	case <-ctx.Done():
	case <-g.shutdown:
		g.logger.Printf("gateway: shutting down: %v", g.shutdownReason.Load())
	case err := <-serveErr:
		if err != nil {
			g.clientShutdown()
			return fmt.Errorf("gateway: serve: %w", err)
		}
	}
	g.clientShutdown()
	return nil
}

// Close releases the data directory without serving. Serve releases it itself;
// Close is for a caller that builds a gateway and stops before serving.
func (g *Gateway) Close() error {
	g.releaseLock()
	return nil
}

// releaseLock drops the single-instance lock.
func (g *Gateway) releaseLock() {
	if g.lock != nil {
		if err := g.lock.Close(); err != nil {
			g.logger.Printf("gateway: release lock: %v", err)
		}
		g.lock = nil
	}
}

// requestShutdown asks Serve to stop (POST /quit from the tray shell, or a
// signal handler that prefers the graceful path).
func (g *Gateway) requestShutdown(reason string) {
	g.shutdownOnce.Do(func() {
		g.shutdownReason.Store(reason)
		close(g.shutdown)
	})
}

// clientShutdown stops the HTTP server and every connection. Clients get one
// gateway.shutdown notification first so they can stop reconnecting (§2.7).
func (g *Gateway) clientShutdown() {
	if g.hub != nil && g.hub.isRunning() {
		g.broadcastEvent("gateway.shutdown", map[string]any{"reason": "shutdown"})
		clients, err := g.hub.clientSnapshot(context.Background())
		if err == nil {
			for _, target := range clients {
				// Give the writer a moment to flush the shutdown frame before
				// the socket goes away (bounded, never a hang).
				target.waitDrained(200 * time.Millisecond)
				target.close()
			}
		}
	}
	if g.server != nil {
		// Close, not Shutdown: an SSE stream never goes idle on its own, so a
		// graceful drain would just wait out its deadline. Closing the
		// connections cancels each request context, which is what the streaming
		// handlers select on (§4.6).
		_ = g.server.Close()
	}
	if g.listener != nil {
		_ = g.listener.Close()
		g.listener = nil
	}
	g.waitHandlers(shutdownWait)
}

// waitHandlers waits for the in-flight handlers to return, bounded: a handler
// that ignores its context must not hang shutdown forever.
func (g *Gateway) waitHandlers(timeout time.Duration) {
	settled := make(chan struct{})
	go func() {
		g.handlers.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-time.After(timeout):
		g.logger.Printf("gateway: shutdown: a request handler is still running after %s", timeout)
	}
}

// clientContext is the context a connection's hub calls use. It is the serve
// context, never the HTTP request context: a hijacked connection's request
// context is not a reliable lifetime signal after the upgrade.
func (g *Gateway) clientContext() context.Context {
	if g.serveCtx == nil {
		return context.Background()
	}
	return g.serveCtx
}

// pollDomains refreshes the domains whose backend has no change notification.
//
// Settings: contracts.SettingsService has no watcher, so a write from any source
// (a module, the host, or a settings.set once that lands) is seen up to one poll
// late. That is a real ceiling, documented in docs/TRANSPORT.md §6.3 — the
// browser must not assume a settings write echoes back instantly.
// Workspace and the task board: same (§6.2 lists both as "on-demand snapshot +
// explicit invalidate"); the board additionally refreshes its domain right after
// a gateway-side write, so a client's own task.status/task.delete echoes back
// without waiting for the poll.
func (g *Gateway) pollDomains(ctx context.Context) {
	interval := g.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// One immediate pass so the first snapshot is already warm.
	g.refreshPolledDomains()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.refreshPolledDomains()
		}
	}
}

// defaultPollInterval is the settings/workspace poll cadence (docs/TRANSPORT.md
// §6.3 documents the resulting latency ceiling).
const defaultPollInterval = 2 * time.Second

// refreshPolledDomains marks the pull domains dirty; the hub diffs them.
func (g *Gateway) refreshPolledDomains() {
	if !g.hub.isRunning() {
		return
	}
	g.hub.markDomainDirty(domainSettings)
	g.hub.markDomainDirty(domainWorkspace)
	// The task board has no change notification either: the agent's task tools
	// and the browser's own commands both mutate it, and only the latter bump the
	// rev immediately (taskWriteResult). Everything else shows up within one poll
	// (§6.3 documents the ceiling).
	g.hub.markDomainDirty(domainTasks)
	// Prompt inboxes are per-session and only interesting while a client is
	// watching one: the backend drains the queue itself (a run settling starts
	// the next prompt), so the inbox can change without any gateway write.
	g.hub.refreshWatchedPrompts()
	// The context timeline has no change notification either: the agent folds it
	// from the durable event log on read. Only subscribed conversations are
	// re-read (same ceiling as the prompt inbox: 2s).
	g.hub.refreshWatchedPrefix(domainContextPrefix)
	// The approval mode is a settings row, so a permission change must reach the
	// approvals snapshot even when the pending set did not move.
	g.hub.markDomainDirty(domainApprovals)
}

// chatService resolves the chat backend lazily: the gateway is constructed
// before the assembly provides it.
func (g *Gateway) chatService() (contracts.ChatService, error) {
	return g.root.Require(contracts.KeyChat)
}

// broadcastEvent pushes one domain-less event to every ready client.
func (g *Gateway) broadcastEvent(method string, params any) {
	if g.hub == nil || !g.hub.isRunning() {
		return
	}
	_, _ = hubExecute(context.Background(), g.hub, func(hh *hub) (any, error) {
		hh.broadcastEvent(method, params)
		return nil, nil
	})
}

// clientGone is called once per closed connection.
func (g *Gateway) clientGone(target *client) {
	g.clearForegroundSession(target.id)
	if g.hub != nil {
		g.hub.unregisterClient(target)
	}
}

// newID mints one per-process identifier.
func (g *Gateway) newID(prefix string) string {
	return prefix + "-" + strconv.FormatUint(g.sequence.Add(1), 36)
}

// serverVersion is the version string reported in initialize.
func (g *Gateway) serverVersion() string {
	if g.opts.Version == "" {
		return "dev"
	}
	return g.opts.Version
}

// newPlanService builds the plan bridge in the idle state.
func newPlanService(gw *Gateway) *planService {
	return &planService{gw: gw, state: planIdle}
}

// errors surfaced by the services.
var (
	errQuestionCancelled   = errors.New("ask: the question was dismissed")
	errPlanContentRequired = errors.New("plan: content is required")
	errNoPlanUnderReview   = errors.New("plan: no plan is under review")
	errUnknownPlanAction   = errors.New("plan: unknown action (want approve, cancel or status)")
)
