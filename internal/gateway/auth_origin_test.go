package gateway

import (
	"net/http"
	"testing"
)

// Browsers do not send `Origin` on a same-origin GET, so a cookie-authenticated GET
// answered 401 and every REST read model (capabilities, insights, automation, files,
// session memory) failed in the real client while curl with a bearer token passed.
// The CSRF rule still has to hold: the credential is ambient (the HttpOnly cookie),
// so anything that is not provably same-origin must be refused.

func requestWithHeaders(t *testing.T, gw *runningGateway, path, cookie string, headers map[string]string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+gw.address+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if cookie != "" {
		request.Header.Set("Cookie", "zk="+cookie)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	var body [512]byte
	n, _ := response.Body.Read(body[:])
	return response.StatusCode, string(body[:n])
}

func TestCookieAuthenticatedGetWithoutOriginIsAllowed(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	// The browser's own signal on a same-origin request.
	if status, body := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token,
		map[string]string{"Sec-Fetch-Site": "same-origin"}); status != http.StatusOK {
		t.Fatalf("same-origin GET = %d %q, want 200", status, body)
	}
	// Older clients signal it with a matching Referer instead.
	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token,
		map[string]string{"Referer": gw.info.Origin() + "/inbox"}); status != http.StatusOK {
		t.Fatalf("referer GET = %d, want 200", status)
	}
	// And an explicit matching Origin still works (POST-like requests send it).
	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token,
		map[string]string{"Origin": gw.info.Origin()}); status != http.StatusOK {
		t.Fatalf("origin GET = %d, want 200", status)
	}
}

// The CSRF half: an ambient credential with a cross-site or missing provenance must
// stay refused.
func TestCookieAuthenticatedGetRefusesUnprovenOrigin(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token,
		map[string]string{"Sec-Fetch-Site": "cross-site"}); status != http.StatusForbidden {
		t.Fatalf("cross-site GET = %d, want 403", status)
	}
	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token,
		map[string]string{"Origin": "https://evil.example"}); status != http.StatusForbidden {
		t.Fatalf("foreign origin GET = %d, want 403", status)
	}
	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token,
		map[string]string{"Referer": "https://evil.example/page"}); status != http.StatusForbidden {
		t.Fatalf("foreign referer GET = %d, want 403", status)
	}
	// No provenance at all: not provable, so not allowed.
	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", gw.info.Token, nil); status != http.StatusUnauthorized {
		t.Fatalf("bare cookie GET = %d, want 401", status)
	}
	// A wrong cookie never passes, whatever the provenance says.
	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", "not-the-token",
		map[string]string{"Sec-Fetch-Site": "same-origin"}); status != http.StatusUnauthorized {
		t.Fatalf("wrong cookie GET = %d, want 401", status)
	}
}

// A bearer token is not ambient: it needs no origin proof (curl, the acceptance
// scripts and the protocol tests all rely on this).
func TestBearerGetNeedsNoOrigin(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	if status, _ := requestWithHeaders(t, gw, "/api/capabilities", "",
		map[string]string{"Authorization": "Bearer " + gw.info.Token}); status != http.StatusOK {
		t.Fatalf("bearer GET = %d, want 200", status)
	}
}
