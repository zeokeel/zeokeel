// Adapter for the local browser module. The adapter is the policy layer around
// the CDP protocol client: it owns which endpoint the pages belong to, the
// per-call timeout, the open-page bookkeeping Status reports, and the mapping
// from transport failures to the typed browser errors. Keeping policy here
// means cdp.go can be read side by side with the DevTools documentation.
package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// DefaultTimeout bounds a single browser operation when the assembly does not
// set one. A visible browser can be slow on first navigation (profile read,
// extension load), but it must never hang a task forever.
const DefaultTimeout = 45 * time.Second

// Config is the module's typed assembly config.
//
// Headless defaults to false on purpose: the plan's decision is a visible local
// window (the user can watch and take over); headless is a setting, not the
// default. Transport is injected so no test or product build launches a browser
// to prove the wiring.
type Config struct {
	// Endpoint is the browser's CDP endpoint, e.g. ws://127.0.0.1:9222/...
	// ("" = no browser configured; the service still loads and reports
	// disconnected, so the settings page can flip it on later).
	Endpoint string `json:"endpoint,omitempty"`
	// Headless asks for a windowless browser. False (the default) keeps the
	// window visible.
	Headless bool `json:"headless,omitempty"`
	// Timeout bounds one operation (zero = DefaultTimeout).
	Timeout time.Duration `json:"-"`
	// Transport is the CDP wire channel. A configured endpoint without a
	// transport is a wiring error (ErrBrowserUnavailable), not a silent
	// "connected" lie.
	Transport Transport `json:"-"`
	// Probe checks reachability for Status. nil means "an endpoint is
	// configured and we assume it works until a call fails".
	Probe func(ctx context.Context) error `json:"-"`
	// Now overrides the clock (tests).
	Now func() time.Time `json:"-"`
}

func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	return c
}

// Adapter implements contracts.BrowserService over a CDP endpoint. It also
// implements the optional contracts.BrowserProfiles surface (profiles.go).
type Adapter struct {
	endpoint string
	headless bool
	timeout  time.Duration
	probe    func(ctx context.Context) error
	cdp      *CDP

	mu    sync.Mutex
	pages map[string]contracts.PageInfo
	// pageProfile maps a page id to the profile that owns it. A page with no
	// entry was opened through Open and lives in the browser's default context.
	pageProfile map[string]string

	// profilesMu guards contexts and serializes context creation, so two
	// concurrent first opens of the same profile cannot each spawn a context.
	// It is only ever held across a createBrowserContext round trip, never
	// across page-level sends.
	profilesMu sync.Mutex
	// contexts maps a profile id to its CDP browser context id.
	contexts map[string]string
}

// New validates the wiring and builds the adapter. A nil transport with a
// configured endpoint is rejected immediately: it can only ever fail later, and
// failing at assembly time is what makes the misconfiguration visible.
func New(cfg Config) (*Adapter, error) {
	cfg = cfg.normalized()
	if cfg.Endpoint != "" && cfg.Transport == nil {
		return nil, fmt.Errorf("%w: endpoint %q configured without a transport", contracts.ErrBrowserUnavailable, cfg.Endpoint)
	}
	return &Adapter{
		endpoint:    cfg.Endpoint,
		headless:    cfg.Headless,
		timeout:     cfg.Timeout,
		probe:       cfg.Probe,
		cdp:         NewCDP(cfg.Transport, cfg.Now),
		pages:       make(map[string]contracts.PageInfo),
		pageProfile: make(map[string]string),
		contexts:    make(map[string]string),
	}, nil
}

// Endpoint reports the configured CDP endpoint ("" = none).
func (a *Adapter) Endpoint() string { return a.endpoint }

// Headless reports whether the adapter asks for a windowless browser. False is
// the product default (visible window).
func (a *Adapter) Headless() bool { return a.headless }

// Open validates the URL, creates a page target, and records it. A rejected URL
// is returned as ErrBrowserBadURL and nothing is sent to the browser.
func (a *Adapter) Open(ctx context.Context, rawURL string) (contracts.PageInfo, error) {
	if err := a.ready(); err != nil {
		return contracts.PageInfo{}, err
	}
	runCtx, cancel := a.operationContext(ctx)
	defer cancel()
	page, err := a.cdp.Open(runCtx, rawURL)
	if err != nil {
		return contracts.PageInfo{}, a.mapError(ctx, err)
	}
	a.mu.Lock()
	a.pages[page.ID] = page
	a.mu.Unlock()
	return page, nil
}

// Title reads document.title for a page this adapter opened.
func (a *Adapter) Title(ctx context.Context, pageID string) (string, error) {
	if err := a.knownPage(pageID); err != nil {
		return "", err
	}
	runCtx, cancel := a.operationContext(ctx)
	defer cancel()
	title, err := a.cdp.Title(runCtx, pageID)
	if err != nil {
		return "", a.mapError(ctx, err)
	}
	return title, nil
}

// Screenshot captures the page as PNG (full = whole scrollable page).
func (a *Adapter) Screenshot(ctx context.Context, pageID string, full bool) ([]byte, error) {
	if err := a.knownPage(pageID); err != nil {
		return nil, err
	}
	runCtx, cancel := a.operationContext(ctx)
	defer cancel()
	shot, err := a.cdp.Screenshot(runCtx, pageID, full)
	if err != nil {
		return nil, a.mapError(ctx, err)
	}
	return shot, nil
}

// Close closes a page this adapter opened and forgets it. Unknown pages are
// reported as ErrBrowserNotFound without a round trip.
func (a *Adapter) Close(ctx context.Context, pageID string) error {
	if err := a.knownPage(pageID); err != nil {
		return err
	}
	runCtx, cancel := a.operationContext(ctx)
	defer cancel()
	if err := a.cdp.Close(runCtx, pageID); err != nil {
		return a.mapError(ctx, err)
	}
	a.mu.Lock()
	delete(a.pages, pageID)
	// The page is gone from this adapter's bookkeeping either way; a profile
	// mapping left behind would make ProfileFor answer for a dead target.
	delete(a.pageProfile, pageID)
	a.mu.Unlock()
	return nil
}

// Status reports the endpoint, reachability and open-page count. It is a
// report: a disconnected browser yields Connected=false with a nil error, so a
// status surface never has to render an error state for "not configured yet".
func (a *Adapter) Status(ctx context.Context) (contracts.BrowserStatus, error) {
	a.mu.Lock()
	pages := len(a.pages)
	a.mu.Unlock()
	status := contracts.BrowserStatus{Endpoint: a.endpoint, Pages: pages}
	if a.endpoint == "" {
		return status, nil
	}
	status.Connected = true
	if a.probe != nil {
		probeCtx, cancel := a.operationContext(ctx)
		defer cancel()
		if err := a.probe(probeCtx); err != nil {
			status.Connected = false
		}
	}
	return status, nil
}

// ready fails every operation that needs a browser when none is wired.
func (a *Adapter) ready() error {
	if a == nil || a.endpoint == "" {
		return fmt.Errorf("%w: no endpoint configured", contracts.ErrBrowserUnavailable)
	}
	if a.cdp == nil || a.cdp.transport == nil {
		return fmt.Errorf("%w: no transport", contracts.ErrBrowserUnavailable)
	}
	return nil
}

// knownPage rejects empty or unknown page ids before any send.
func (a *Adapter) knownPage(pageID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if pageID == "" {
		return fmt.Errorf("%w: empty page id", contracts.ErrBrowserNotFound)
	}
	a.mu.Lock()
	_, ok := a.pages[pageID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", contracts.ErrBrowserNotFound, pageID)
	}
	return nil
}

// operationContext applies the configured timeout on top of the caller's ctx.
func (a *Adapter) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, a.timeout)
}

// mapError folds transport failures into the typed browser errors. Bad-URL and
// permission-denied decisions pass through untouched: they are caller
// decisions, not infrastructure noise.
func (a *Adapter) mapError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, contracts.ErrBrowserBadURL),
		errors.Is(err, contracts.ErrBrowserInvalid),
		errors.Is(err, contracts.ErrBrowserPermissionDenied),
		errors.Is(err, contracts.ErrBrowserNotFound):
		return err
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", contracts.ErrBrowserTimeout, err)
	default:
		return fmt.Errorf("%w: %w", contracts.ErrBrowserUnavailable, err)
	}
}
