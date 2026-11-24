package gateway

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Authentication (docs/TRANSPORT.md §3).
//
// The threat model is explicit: this stops cross-origin web pages and accidental
// connections, not malicious processes running as the same OS user (they can
// read gateway.json, which is 0600 but owned by that user). One process token,
// minted at startup, no accounts, no expiry.
const (
	// tokenCookie is the HttpOnly cookie the fragment exchange installs.
	tokenCookie = "zk"
	// cookieMaxAge keeps the cookie alive for the browser session only; the
	// token itself dies with the process.
	cookieMaxAge = 0
	// authFailureLimit and authCooldown implement the enumeration brake: 20
	// consecutive failures from one source freeze it for a minute (§3.2 C4).
	authFailureLimit = 20
	authCooldown     = 60 * time.Second
)

// auth owns the process token and every request-side check.
type auth struct {
	token  string
	origin string
	hosts  map[string]bool

	mu       sync.Mutex
	failures map[string]*failureRecord
}

// failureRecord counts consecutive failures from one source.
type failureRecord struct {
	count     int
	blockedAt time.Time
}

// newAuth captures the checks' fixed inputs: the token and the exact origin the
// client is served from.
func newAuth(token string, info Info) *auth {
	return &auth{
		token:    token,
		origin:   info.Origin(),
		hosts:    map[string]bool{listenHost + ":" + strconv.Itoa(info.Port): true, "localhost:" + strconv.Itoa(info.Port): true},
		failures: make(map[string]*failureRecord),
	}
}

// checkHost enforces C2: the Host header must be the loopback name we are served
// from, which is what makes DNS rebinding useless.
func (a *auth) checkHost(r *http.Request) *protocolError {
	if a.hosts[strings.ToLower(r.Host)] {
		return nil
	}
	return appError(codeBadOrigin, "bad_host", false)
}

// checkOrigin enforces C3: a present Origin must be our own; an absent Origin
// means a non-browser client and then requires a Bearer token.
func (a *auth) checkOrigin(r *http.Request) *protocolError {
	// An explicit Origin is authoritative: a request that names another site is
	// cross-site whatever credential it carries.
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if !strings.EqualFold(origin, a.origin) {
			return appError(codeBadOrigin, "bad_origin", false)
		}
		return nil
	}
	// Without an Origin, a bearer header is enough: it is secret the page itself
	// holds and another site cannot attach it (curl, the acceptance scripts, the
	// protocol tests). The cookie is different — the browser attaches it by itself
	// — so the cookie path below has to prove same-origin.
	if a.hasBearer(r) {
		return nil
	}
	// Browsers omit Origin on same-origin GETs (a fetch of a read model, an <img>,
	// a navigation), so requiring it refused every cookie-authenticated GET.
	// Sec-Fetch-Site is the browser's own statement about the request's
	// provenance and is set on the very requests that lack Origin.
	if site := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")); site != "" {
		if strings.EqualFold(site, "same-origin") {
			return nil
		}
		return appError(codeBadOrigin, "bad_origin", false)
	}
	// Older browsers send neither header on GET but do send Referer.
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		if parsed, err := url.Parse(referer); err == nil &&
			strings.EqualFold(parsed.Scheme+"://"+parsed.Host, a.origin) {
			return nil
		}
		return appError(codeBadOrigin, "bad_origin", false)
	}
	// Nothing proves where this came from: an unprovable ambient credential is
	// exactly what CSRF looks like.
	return appError(codeUnauthorized, "unauthorized", false)
}

// checkToken enforces C4: the cookie or a Bearer header must carry the process
// token.
func (a *auth) checkToken(r *http.Request) *protocolError {
	if a.hasBearer(r) {
		return nil
	}
	cookie, err := r.Cookie(tokenCookie)
	if err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(a.token)) == 1 {
		return nil
	}
	return appError(codeUnauthorized, "unauthorized", false)
}

// hasBearer reports a correct Authorization header.
func (a *auth) hasBearer(r *http.Request) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	value := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(value), []byte(a.token)) == 1
}

// tokenMatches reports whether a presented token is the process token.
func (a *auth) tokenMatches(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) == 1
}

// cookie builds the post-exchange cookie.
func (a *auth) cookie() *http.Cookie {
	return &http.Cookie{
		Name:     tokenCookie,
		Value:    a.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   cookieMaxAge,
	}
}

// inCooldown reports whether a source is currently frozen.
func (a *auth) inCooldown(remote string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	record := a.failures[remote]
	if record == nil || record.blockedAt.IsZero() {
		return false
	}
	if time.Since(record.blockedAt) < authCooldown {
		return true
	}
	delete(a.failures, remote)
	return false
}

// recordFailure counts one failure and reports whether the source just entered
// its cooldown.
func (a *auth) recordFailure(remote string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	record := a.failures[remote]
	if record == nil {
		record = &failureRecord{}
		a.failures[remote] = record
	}
	record.count++
	if record.count >= authFailureLimit {
		record.blockedAt = time.Now()
		record.count = 0
	}
}

// clearFailures forgets one source's failures after a success.
func (a *auth) clearFailures(remote string) {
	a.mu.Lock()
	delete(a.failures, remote)
	a.mu.Unlock()
}

// remoteSource is the key of the failure table: the peer address without the
// port (every tab of one browser shares the loopback source).
func remoteSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
