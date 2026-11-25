package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Browser is the local-browser surface (plan §9.3: the user's own Chrome/Edge
// driven over the Chrome DevTools Protocol, never a cloud desktop, never a
// screen capture of the desktop). One page is one CDP target; the adapter owns
// which browser endpoint the pages belong to.
//
// The interface is deliberately small and page-oriented: a caller opens a URL,
// then names the returned page for title/screenshot/close. Nothing here exposes
// the CDP wire format, so a future transport (websocket, pipe, in-process fake)
// can be swapped without touching callers.
type BrowserService interface {
	// Open creates a page target for an http/https URL and returns it. The URL
	// is validated before anything is sent to the browser; a rejected URL
	// returns ErrBrowserBadURL and creates no target.
	Open(ctx context.Context, url string) (PageInfo, error)
	// Title reads the page's current document.title. Title is empty only until
	// the document has one; an unknown page returns ErrBrowserNotFound.
	Title(ctx context.Context, pageID string) (string, error)
	// Screenshot captures the page as PNG bytes. full asks the browser to
	// capture beyond the viewport (the whole scrollable page).
	Screenshot(ctx context.Context, pageID string, full bool) ([]byte, error)
	// Close closes the page target. It is safe to call once; an unknown page
	// returns ErrBrowserNotFound.
	Close(ctx context.Context, pageID string) error
	// Status reports the browser connection. It is a report, not a probe with
	// side effects: a disconnected browser reports Connected=false with a nil
	// error (mirroring WeChatService.Status).
	Status(ctx context.Context) (BrowserStatus, error)
}

// PageInfo identifies one open page (one CDP target).
type PageInfo struct {
	// ID is the CDP target id; every later call names it.
	ID string
	// URL is the URL that was opened (not a post-navigation URL).
	URL string
	// Title is empty at open time; call Title to read the document title.
	Title string
	// OpenedAt is when the target was created.
	OpenedAt time.Time
}

// BrowserStatus is what a settings/status surface shows.
type BrowserStatus struct {
	// Connected reports whether the browser answered: an endpoint is
	// configured and the configured probe (if any) succeeded. Because headless
	// is a setting and the window is visible by default, Connected says
	// "reachable", not "installed".
	Connected bool
	// Endpoint is the configured CDP endpoint ("" = none configured).
	Endpoint string
	// Pages is how many pages this adapter currently has open.
	Pages int
}

// Browser errors are typed so the caller can decide between retry, a settings
// hint, or a blocked task (plan §6.8: a lost browser blocks, it does not fail
// silently).
var (
	// ErrBrowserUnavailable means no browser is configured, the transport is
	// missing, or the endpoint call failed. It is the "cannot talk to the
	// browser right now" umbrella.
	ErrBrowserUnavailable = errors.New("browser: unavailable")
	// ErrBrowserBadURL means the URL cannot be opened at all (not http/https,
	// too long, or no host). Nothing was sent to the browser.
	ErrBrowserBadURL = errors.New("browser: bad url")
	// ErrBrowserTimeout means the browser did not answer before the configured
	// timeout (or the caller's deadline) expired.
	ErrBrowserTimeout = errors.New("browser: timeout")
	// ErrBrowserNotFound means the page id is not an open page.
	ErrBrowserNotFound = errors.New("browser: page not found")
	// ErrBrowserPermissionDenied means the browser refused the operation
	// (e.g. a policy-blocked navigation). Callers must surface it as an
	// approval/decision, never retry around it.
	ErrBrowserPermissionDenied = errors.New("browser: permission denied")
)

// KeyBrowser is provided by the browser module.
var KeyBrowser = cordis.NewServiceKey[BrowserService]("zeokeel/browser")

// BrowserProfiles is the OPTIONAL profile-scoped browser surface. It is
// deliberately a separate interface rather than three more methods on
// BrowserService: a read-only consumer (a status surface, a fake in a test) must
// not be forced to implement profile juggling just to open one page.
//
// A "profile" is a name the product uses for one bot's identity in the browser.
// In CDP terms it is a browser context: its own cookie jar, localStorage,
// IndexedDB and cache. That is the real isolation boundary — NOT a separate
// window, and not a separate tab — so two bots logged into different accounts on
// the same site genuinely cannot see each other's session. Pages opened through
// BrowserService.Open belong to the browser's default context and to no profile
// (""), which is also where the human's own logins live.
//
// Callers that need profiles assert this interface; KeyBrowser keeps handing out
// the smaller BrowserService.
type BrowserProfiles interface {
	// OpenInProfile creates a page for an http/https URL inside profileID's
	// browser context, creating the context on first use and reusing it
	// afterwards (so the cookies persist across pages and across calls). An
	// empty profileID returns ErrBrowserInvalid: "no profile" is spelled Open.
	OpenInProfile(ctx context.Context, profileID string, url string) (PageInfo, error)
	// CloseProfile disposes profileID's browser context, which closes its pages
	// and destroys its cookie jar, and forgets the profile. Closing a profile
	// this adapter never opened is a no-op (nil error, nothing sent).
	CloseProfile(ctx context.Context, profileID string) error
	// ProfileFor reports which profile a page belongs to. ok reports whether
	// the page is known at all; the profile is "" for pages opened through
	// BrowserService.Open (the default context).
	ProfileFor(ctx context.Context, pageID string) (profileID string, ok bool, err error)
}

// ErrBrowserInvalid means the caller passed an argument that cannot name a
// browser resource (today: an empty profile id). Nothing was sent to the
// browser. It is distinct from ErrBrowserBadURL so a caller can fix the call
// instead of showing a URL problem.
var ErrBrowserInvalid = errors.New("browser: invalid argument")
