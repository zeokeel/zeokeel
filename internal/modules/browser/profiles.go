// Profile-scoped browsing: the isolation boundary behind the plan's M3/M5
// acceptance test ("two bots each logged into different sites' accounts do not
// see each other").
//
// The boundary is a **browser context**, not a window and not a tab. CDP's
// Target.createBrowserContext gives a fresh cookie jar — cookies, localStorage,
// IndexedDB, service workers, HTTP cache — and two contexts never share any of
// it. Windows and tabs do NOT isolate anything: two windows of the same profile
// share one cookie jar, so two bots in two windows are still one logged-in
// identity, and the second bot silently inherits (or overwrites) the first
// bot's session. That is exactly the failure the acceptance test is written to
// catch, so "separate account" has to mean "separate browser context".
//
// The mapping this file owns:
//
//	profileID ──(created once, reused)──▶ browserContextId
//	pageID ─────(recorded at open)─────▶ profileID
//
// and the adapter-level rules that make it safe:
//   - a profile's context is created on first use only; a *failed* creation is
//     not cached, so a retry must ask the browser again (caching a bad id would
//     make every later open fail, or worse, land pages in the default jar);
//   - pages opened through BrowserService.Open belong to the default context and
//     to no profile (""), so existing single-bot callers are untouched;
//   - an empty profile id is refused (ErrBrowserInvalid) instead of being
//     treated as "the default profile" — a magic "" would let a caller who meant
//     "isolate this bot" silently share the human's own logged-in jar;
//   - disposing a context also drops its pages from the adapter's bookkeeping,
//     because the browser closed those targets; a failed dispose keeps the
//     mapping so a retry can retry.
//
// Everything here is policy over the protocol: the CDP calls and their params
// live in cdp.go, the wire is injected, and the tests never launch a browser.
package browser

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The adapter is expected to satisfy the optional profile surface; this line is
// what keeps that promise honest when the interface changes.
var _ contracts.BrowserProfiles = (*Adapter)(nil)

// OpenInProfile opens rawURL in profileID's browser context, creating the
// context on first use. Pages of the same profile share cookies; pages of
// different profiles cannot see each other's.
func (a *Adapter) OpenInProfile(ctx context.Context, profileID, rawURL string) (contracts.PageInfo, error) {
	if strings.TrimSpace(profileID) == "" {
		// "No profile" is spelled Open: it means the default context, which is
		// the human's own jar. Silently accepting "" here would look isolated
		// and not be.
		return contracts.PageInfo{}, fmt.Errorf("%w: empty profile id (use Open for the default context)", contracts.ErrBrowserInvalid)
	}
	if err := a.ready(); err != nil {
		return contracts.PageInfo{}, err
	}
	// Validate before creating anything: a bad URL must not leave an empty
	// browser context behind (or spend a round trip on one).
	if err := ValidateURL(rawURL); err != nil {
		return contracts.PageInfo{}, err
	}
	runCtx, cancel := a.operationContext(ctx)
	defer cancel()
	contextID, err := a.contextFor(runCtx, profileID)
	if err != nil {
		return contracts.PageInfo{}, a.mapError(ctx, err)
	}
	page, err := a.cdp.OpenInContext(runCtx, rawURL, contextID)
	if err != nil {
		return contracts.PageInfo{}, a.mapError(ctx, err)
	}
	a.mu.Lock()
	a.pages[page.ID] = page
	a.pageProfile[page.ID] = profileID
	a.mu.Unlock()
	return page, nil
}

// contextFor returns profileID's context id, creating it if this is the first
// open for that profile. The round trip happens under profilesMu so two
// concurrent first opens of one profile cannot each create a context (which
// would split one bot's cookies across two jars).
func (a *Adapter) contextFor(ctx context.Context, profileID string) (string, error) {
	a.profilesMu.Lock()
	defer a.profilesMu.Unlock()
	if contextID, ok := a.contexts[profileID]; ok {
		return contextID, nil
	}
	contextID, err := a.cdp.CreateBrowserContext(ctx)
	if err != nil {
		// Nothing is recorded: a failed creation must not cache an id, so the
		// next attempt asks the browser for a fresh context.
		return "", err
	}
	a.contexts[profileID] = contextID
	return contextID, nil
}

// CloseProfile disposes profileID's browser context — the browser closes its
// pages and destroys its cookie jar — and forgets the profile. Closing a profile
// this adapter never opened is a no-op: there is nothing to dispose and nothing
// to forget.
func (a *Adapter) CloseProfile(ctx context.Context, profileID string) error {
	if strings.TrimSpace(profileID) == "" {
		return fmt.Errorf("%w: empty profile id", contracts.ErrBrowserInvalid)
	}
	if err := a.ready(); err != nil {
		return err
	}
	a.profilesMu.Lock()
	contextID, ok := a.contexts[profileID]
	a.profilesMu.Unlock()
	if !ok {
		return nil
	}
	runCtx, cancel := a.operationContext(ctx)
	defer cancel()
	if err := a.cdp.DisposeBrowserContext(runCtx, contextID); err != nil {
		// Keep the mapping: the context may still exist in the browser, and a
		// retry (or a later open) must be able to reach the same jar.
		return a.mapError(ctx, err)
	}
	a.profilesMu.Lock()
	// Only forget the pairing we actually disposed; a concurrent reopen could
	// have replaced it with a new context in the meantime.
	if a.contexts[profileID] == contextID {
		delete(a.contexts, profileID)
	}
	a.profilesMu.Unlock()
	a.forgetProfilePages(profileID)
	return nil
}

// forgetProfilePages drops the pages a disposed context took with it. The
// browser closed those targets; keeping them would let ProfileFor answer for a
// dead page and Status over-count.
func (a *Adapter) forgetProfilePages(profileID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for pageID, owner := range a.pageProfile {
		if owner != profileID {
			continue
		}
		delete(a.pageProfile, pageID)
		delete(a.pages, pageID)
	}
}

// ProfileFor reports the profile a page belongs to. ok is false (with
// ErrBrowserNotFound) when the page is not one this adapter opened; the profile
// is "" for pages opened through Open, which live in the default context and
// belong to no bot.
func (a *Adapter) ProfileFor(ctx context.Context, pageID string) (string, bool, error) {
	if err := a.knownPage(pageID); err != nil {
		return "", false, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	profileID, ok := a.pageProfile[pageID]
	if !ok {
		return "", true, nil
	}
	return profileID, true, nil
}
