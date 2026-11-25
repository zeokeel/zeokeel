package wechat_test

// TDD for the QR login flow. The point of these tests is the seam between "a
// human scanned a code" and "the adapter is logged in": only a confirmed state
// may persist a token, a confirmation that lands after the code's deadline must
// be refused, and a QR id the adapter never issued must never be polled.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/wechat"
)

// newLoginFixture wires an adapter to a token store and seeds the fake platform
// with one usable QR code, so each test only overrides what it exercises.
func newLoginFixture(t *testing.T) (*wechat.Adapter, *fakeILink, *clock, string) {
	t.Helper()
	server, url, inboxSvc, dir, clk := newStoreFixture(t)
	server.loginCode = "qr-1"
	server.loginURL = "https://ilinkai.weixin.qq.com/qr/qr-1"
	server.loginTTL = 300
	server.loginStatus = "wait"
	server.loginToken = "fresh-bot-token"

	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	adapter, err := wechat.OpenAdapter(inboxSvc, wechat.Config{BaseURL: url, Now: clk.now}, store)
	if err != nil {
		t.Fatalf("OpenAdapter: %v", err)
	}
	return adapter, server, clk, dir
}

// loginStats exposes the fake's login counters without reaching into fields.
func (f *fakeILink) loginStats() (calls, polls int, ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginCalls, f.loginPolls, append([]string(nil), f.loginIDs...)
}

func (f *fakeILink) qrCodeMethod() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginMethod
}

func (f *fakeILink) setLoginStatus(status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loginStatus = status
}

func TestStartLoginReturnsTheQRCodeURLAndExpiry(t *testing.T) {
	adapter, server, clk, _ := newLoginFixture(t)

	login, err := adapter.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if want := "https://ilinkai.weixin.qq.com/qr/qr-1"; login.QRCodeURL != want {
		t.Fatalf("QRCodeURL = %q, want %q", login.QRCodeURL, want)
	}
	if login.QRCodeID != "qr-1" {
		t.Fatalf("QRCodeID = %q, want qr-1 (the UI needs it to poll)", login.QRCodeID)
	}
	if want := clk.now().Add(300 * time.Second); !login.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", login.ExpiresAt, want)
	}
	if method := server.qrCodeMethod(); method != http.MethodPost {
		t.Fatalf("getloginqrcode method = %q, want POST", method)
	}
}

func TestStartLoginToleratesTheDataEnvelope(t *testing.T) {
	adapter, server, _, _ := newLoginFixture(t)
	server.loginEnvelope = true

	login, err := adapter.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if login.QRCodeURL == "" || login.QRCodeID != "qr-1" || login.ExpiresAt.IsZero() {
		t.Fatalf("enveloped login = %+v, want the same values as the flat shape", login)
	}
}

func TestStartLoginRejectsAnIncompleteQRCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		breakIt func(*fakeILink)
	}{
		{"missing qrcode", func(f *fakeILink) { f.loginCode = "" }},
		{"missing qrcode_url", func(f *fakeILink) { f.loginURL = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, server, _, _ := newLoginFixture(t)
			tc.breakIt(server)
			if _, err := adapter.StartLogin(context.Background()); !errors.Is(err, contracts.ErrWeChatLoginUnavailable) {
				t.Fatalf("err = %v, want ErrWeChatLoginUnavailable", err)
			}
		})
	}
}

func TestStartLoginTypesPlatformFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, contracts.ErrWeChatUnauthorized},
		{http.StatusForbidden, contracts.ErrWeChatUnauthorized},
		{http.StatusInternalServerError, contracts.ErrWeChatLoginUnavailable},
	} {
		adapter, server, _, _ := newLoginFixture(t)
		server.loginHTTP = tc.status
		if _, err := adapter.StartLogin(context.Background()); !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestLoginStateWalksPendingWaitingConfirmed(t *testing.T) {
	adapter, server, _, dir := newLoginFixture(t)
	ctx := context.Background()
	login, err := adapter.StartLogin(ctx)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}

	for _, step := range []struct {
		platform string
		want     contracts.WeChatLoginState
	}{
		{"wait", contracts.WeChatLoginPending},
		{"pending", contracts.WeChatLoginPending},
		{"scanned", contracts.WeChatLoginWaiting},
		{"waiting", contracts.WeChatLoginWaiting},
	} {
		server.setLoginStatus(step.platform)
		state, token, err := adapter.LoginState(ctx, login.QRCodeID)
		if err != nil {
			t.Fatalf("LoginState(%q): %v", step.platform, err)
		}
		if state != step.want || token != "" {
			t.Fatalf("LoginState(%q) = (%q, %q), want (%q, \"\")", step.platform, state, token, step.want)
		}
	}

	// A scan that has not been confirmed must not persist anything.
	if _, err := os.Stat(filepath.Join(dir, "token.json")); !os.IsNotExist(err) {
		t.Fatalf("token.json exists before confirmation (err = %v)", err)
	}
	status, err := adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatal("adapter reported Connected before the login was confirmed")
	}

	server.setLoginStatus("confirmed")
	state, token, err := adapter.LoginState(ctx, login.QRCodeID)
	if err != nil {
		t.Fatalf("LoginState(confirmed): %v", err)
	}
	if state != contracts.WeChatLoginConfirmed {
		t.Fatalf("state = %q, want confirmed", state)
	}
	if token != "fresh-bot-token" {
		t.Fatalf("token = %q, want the platform's bot_token", token)
	}

	status, err = adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status after confirmed: %v", err)
	}
	if !status.Connected {
		t.Fatalf("status = %+v after confirmed, want Connected", status)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "token.json"))
	if err != nil {
		t.Fatalf("token.json after confirmed: %v", err)
	}
	if !strings.Contains(string(raw), "fresh-bot-token") {
		t.Fatalf("token.json = %s, want the login token", raw)
	}

	// The QR id is one-shot: replaying a confirmed login must not re-persist.
	server.setLoginStatus("confirmed")
	if _, _, err := adapter.LoginState(ctx, login.QRCodeID); !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("replayed LoginState err = %v, want ErrWeChatLoginExpired", err)
	}

	// The persisted token is the one used on the wire, not just a flag.
	server.pushMessage("m1", "user@im.wechat", "hi", "token-1")
	if _, err := adapter.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce after login: %v", err)
	}
	if got, want := server.lastAuthHeader(), "Bearer fresh-bot-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}

	_, polls, ids := server.loginStats()
	if polls == 0 || ids[0] != login.QRCodeID {
		t.Fatalf("getloginstate ids = %v (polls %d), want %q", ids, polls, login.QRCodeID)
	}
}

func TestLoginStateTreatsAPlatformExpiryAsExpired(t *testing.T) {
	adapter, server, _, dir := newLoginFixture(t)
	ctx := context.Background()
	login, err := adapter.StartLogin(ctx)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	server.setLoginStatus("expired")

	state, token, err := adapter.LoginState(ctx, login.QRCodeID)
	if state != contracts.WeChatLoginExpired {
		t.Fatalf("state = %q, want expired", state)
	}
	if !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("err = %v, want ErrWeChatLoginExpired", err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
	if _, err := os.Stat(filepath.Join(dir, "token.json")); !os.IsNotExist(err) {
		t.Fatalf("an expired login persisted a token (err = %v)", err)
	}
}

func TestLoginStateTreatsAnEmptyStatusAsExpired(t *testing.T) {
	adapter, server, _, _ := newLoginFixture(t)
	ctx := context.Background()
	login, err := adapter.StartLogin(ctx)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	server.setLoginStatus("")

	state, token, err := adapter.LoginState(ctx, login.QRCodeID)
	if state != contracts.WeChatLoginExpired || token != "" || !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("LoginState with no status = (%q, %q, %v), want (expired, \"\", ErrWeChatLoginExpired)", state, token, err)
	}
}

func TestLoginStateRejectsAnUnknownQRCodeWithoutCallingThePlatform(t *testing.T) {
	adapter, server, _, _ := newLoginFixture(t)

	state, token, err := adapter.LoginState(context.Background(), "never-issued")
	if state != contracts.WeChatLoginExpired || token != "" {
		t.Fatalf("LoginState(unknown) = (%q, %q), want (expired, \"\")", state, token)
	}
	if !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("err = %v, want ErrWeChatLoginExpired", err)
	}
	if _, polls, _ := server.loginStats(); polls != 0 {
		t.Fatalf("getloginstate calls = %d, want 0 for an id we never issued", polls)
	}
}

func TestLoginStateExpiresTheStoredQRCodeLocally(t *testing.T) {
	adapter, server, clk, _ := newLoginFixture(t)
	ctx := context.Background()
	login, err := adapter.StartLogin(ctx)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	clk.advance(301 * time.Second)

	state, token, err := adapter.LoginState(ctx, login.QRCodeID)
	if state != contracts.WeChatLoginExpired || token != "" || !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("LoginState after the deadline = (%q, %q, %v), want expired", state, token, err)
	}
	if _, polls, _ := server.loginStats(); polls != 0 {
		t.Fatalf("getloginstate calls = %d, want 0 for a locally expired code", polls)
	}
}

func TestLoginStateRefusesAConfirmationThatLandsAfterTheDeadline(t *testing.T) {
	adapter, server, clk, dir := newLoginFixture(t)
	ctx := context.Background()
	login, err := adapter.StartLogin(ctx)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	// The platform takes its time and answers "confirmed" after the code died.
	server.onLoginState = func() { clk.advance(301 * time.Second) }
	server.setLoginStatus("confirmed")

	state, token, err := adapter.LoginState(ctx, login.QRCodeID)
	if state != contracts.WeChatLoginExpired || token != "" {
		t.Fatalf("LoginState(late confirm) = (%q, %q), want (expired, \"\")", state, token)
	}
	if !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("err = %v, want ErrWeChatLoginExpired", err)
	}
	status, err := adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatal("a confirmation past the deadline still logged the adapter in")
	}
	if _, err := os.Stat(filepath.Join(dir, "token.json")); !os.IsNotExist(err) {
		t.Fatalf("a stale confirmation persisted a token (err = %v)", err)
	}
}

func TestLoginStateTypesPlatformFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, contracts.ErrWeChatUnauthorized},
		{http.StatusInternalServerError, contracts.ErrWeChatLoginUnavailable},
	} {
		adapter, server, _, _ := newLoginFixture(t)
		ctx := context.Background()
		login, err := adapter.StartLogin(ctx)
		if err != nil {
			t.Fatalf("StartLogin: %v", err)
		}
		server.loginHTTP = tc.status
		state, token, err := adapter.LoginState(ctx, login.QRCodeID)
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		if state != "" || token != "" {
			t.Fatalf("status %d: state/token = (%q, %q), want empty on a transport failure", tc.status, state, token)
		}
	}
}

func TestLoginRefusesAnEmptyQRCodeID(t *testing.T) {
	adapter, server, _, _ := newLoginFixture(t)
	state, _, err := adapter.LoginState(context.Background(), "   ")
	if state != contracts.WeChatLoginExpired || !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("LoginState(blank) = (%q, %v), want (expired, ErrWeChatLoginExpired)", state, err)
	}
	if _, polls, _ := server.loginStats(); polls != 0 {
		t.Fatalf("getloginstate calls = %d, want 0", polls)
	}
}

func TestLoginStateDoesNotNeedARunningLogin(t *testing.T) {
	adapter, _, _, dir := newLoginFixture(t)
	ctx := context.Background()
	// A fresh process has no QR ids; the settings page must get "expired" and
	// start a new login, not a panic or a phantom connected account.
	if _, err := os.Stat(filepath.Join(dir, "token.json")); !os.IsNotExist(err) {
		t.Fatalf("token.json exists before any login (err = %v)", err)
	}
	status, err := adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want disconnected", status)
	}
	if _, _, err := adapter.LoginState(ctx, "qr-1"); !errors.Is(err, contracts.ErrWeChatLoginExpired) {
		t.Fatalf("err = %v, want ErrWeChatLoginExpired for an id from a previous process", err)
	}
}
