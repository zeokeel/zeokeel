package dingtalk_test

// TDD for the DingTalk outbound adapter. The thing that goes wrong in production
// is the outbound body shape: a wrong field set is a silent no-op on the platform
// side. The ingress (callback signature, replay window, inbox filing) is gone by
// decision — see docs/research/channel-capabilities.md §6 — and
// internal/contracts/channel_surface_test.go pins that it does not come back.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
)

// fakeRobot is the outbound half of the platform.
type fakeRobot struct {
	mu       sync.Mutex
	requests []capturedRequest
	status   int
}

type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

func (f *fakeRobot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		captured := capturedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()}
		_ = json.NewDecoder(r.Body).Decode(&captured.body)
		f.mu.Lock()
		f.requests = append(f.requests, captured)
		status := f.status
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"processQueryKey": "pq-1"})
	})
	return mux
}

func (f *fakeRobot) last() *capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return &f.requests[len(f.requests)-1]
}

func (f *fakeRobot) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func newFixture(t *testing.T) (*dingtalk.Adapter, *fakeRobot) {
	t.Helper()
	robot := &fakeRobot{}
	httpServer := httptest.NewServer(robot.handler())
	t.Cleanup(httpServer.Close)
	adapter := dingtalk.NewAdapter(dingtalk.Config{
		BaseURL:   httpServer.URL,
		Token:     "access-token",
		RobotCode: "dingrobot1",
	})
	return adapter, robot
}

func TestSendPostsTheDocumentedBody(t *testing.T) {
	adapter, robot := newFixture(t)

	if err := adapter.Send(context.Background(), "cid-1", "收到，我在查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	captured := robot.last()
	if captured == nil {
		t.Fatal("no request reached the robot API")
	}
	if captured.method != http.MethodPost {
		t.Fatalf("method = %s", captured.method)
	}
	if captured.path != "/v1.0/robot/groupMessages/send" {
		t.Fatalf("path = %s", captured.path)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("authorization = %q", got)
	}
	if got := captured.header.Get("x-acs-dingtalk-access-token"); got != "access-token" {
		t.Fatalf("access token header = %q", got)
	}
	if captured.body["robotCode"] != "dingrobot1" {
		t.Fatalf("robotCode = %v", captured.body["robotCode"])
	}
	if captured.body["openConversationId"] != "cid-1" {
		t.Fatalf("openConversationId = %v", captured.body["openConversationId"])
	}
	if captured.body["msgKey"] != "sampleText" {
		t.Fatalf("msgKey = %v", captured.body["msgKey"])
	}
	param, ok := captured.body["msgParam"].(string)
	if !ok {
		t.Fatalf("msgParam = %v, want a JSON string", captured.body["msgParam"])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(param), &decoded); err != nil {
		t.Fatalf("msgParam is not JSON: %v (%s)", err, param)
	}
	if decoded["content"] != "收到，我在查" {
		t.Fatalf("msgParam content = %v", decoded["content"])
	}
}

func TestSendUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		adapter, robot := newFixture(t)
		robot.status = status
		if err := adapter.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnauthorized) {
			t.Fatalf("status %d: err = %v, want ErrDingTalkUnauthorized", status, err)
		}
	}
}

func TestSendUnavailable(t *testing.T) {
	adapter, robot := newFixture(t)
	robot.status = http.StatusInternalServerError
	if err := adapter.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnavailable) {
		t.Fatalf("err = %v, want ErrDingTalkUnavailable", err)
	}

	// A network failure is "unavailable", not a crash or a silent success.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	offline := dingtalk.NewAdapter(dingtalk.Config{
		BaseURL: deadURL, Token: "access-token", RobotCode: "dingrobot1",
	})
	if err := offline.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnavailable) {
		t.Fatalf("offline err = %v, want ErrDingTalkUnavailable", err)
	}
}

func TestSendRefusesWithoutTokenOrText(t *testing.T) {
	robot := &fakeRobot{}
	httpServer := httptest.NewServer(robot.handler())
	t.Cleanup(httpServer.Close)
	noToken := dingtalk.NewAdapter(dingtalk.Config{BaseURL: httpServer.URL})
	if err := noToken.Send(context.Background(), "cid-1", "hi"); !errors.Is(err, contracts.ErrDingTalkUnauthorized) {
		t.Fatalf("err = %v, want ErrDingTalkUnauthorized", err)
	}
	if robot.count() != 0 {
		t.Fatal("a send without a token still hit the network")
	}

	adapter, robot2 := newFixture(t)
	if err := adapter.Send(context.Background(), "cid-1", "   "); !errors.Is(err, contracts.ErrDingTalkEmptyMessage) {
		t.Fatalf("err = %v, want ErrDingTalkEmptyMessage", err)
	}
	if err := adapter.Send(context.Background(), "", "hi"); !errors.Is(err, contracts.ErrDingTalkInvalid) {
		t.Fatalf("err = %v, want ErrDingTalkInvalid for a missing chat id", err)
	}
	if robot2.count() != 0 {
		t.Fatal("a refused send still hit the network")
	}
}

func TestStatusReportsTheConfiguredToken(t *testing.T) {
	adapter, _ := newFixture(t)
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Connected {
		t.Fatalf("status = %+v, want connected with a configured token", status)
	}

	offline := dingtalk.NewAdapter(dingtalk.Config{})
	disconnected, err := offline.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if disconnected.Connected {
		t.Fatal("reported connected without an access token")
	}
}

func TestCancelledContextSendsNothing(t *testing.T) {
	adapter, robot := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.Send(ctx, "cid-1", "hi"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if robot.count() != 0 {
		t.Fatal("a cancelled send still hit the network")
	}
	if _, err := adapter.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Status err = %v, want context.Canceled", err)
	}
}

// TestStatusIsNotConnectedWithoutARobotCode pins the honest status. Every send
// body carries `robotCode`, so a credential row holding only a token cannot
// deliver anything: the platform answers 400. Reporting Connected for that row
// is what made the settings page call a broken setup healthy and let the router
// schedule sends that were guaranteed to fail.
func TestStatusIsNotConnectedWithoutARobotCode(t *testing.T) {
	tokenOnly := dingtalk.NewAdapter(dingtalk.Config{Token: "access-token"})
	status, err := tokenOnly.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want NOT connected: a token alone cannot send, the body always carries robotCode", status)
	}
	// The state must be diagnosable, not merely false: the page has to be able
	// to say which field is missing.
	if !strings.Contains(status.Reason, "robotCode") {
		t.Fatalf("status.Reason = %q, want it to name the missing robotCode", status.Reason)
	}
	if len(status.MissingFields) != 1 || status.MissingFields[0] != "robotCode" {
		t.Fatalf("status.MissingFields = %v, want [robotCode]", status.MissingFields)
	}
}

// TestStatusNamesEveryMissingField pins that a completely unconfigured adapter
// reports both halves, so the page can prompt for the whole credential instead
// of one field at a time.
func TestStatusNamesEveryMissingField(t *testing.T) {
	status, err := dingtalk.NewAdapter(dingtalk.Config{}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Connected {
		t.Fatalf("status = %+v, want not connected", status)
	}
	if len(status.MissingFields) != 2 || status.MissingFields[0] != "token" || status.MissingFields[1] != "robotCode" {
		t.Fatalf("status.MissingFields = %v, want [token robotCode]", status.MissingFields)
	}
}

// TestSendRefusesWithoutRobotCode is the send-side half: a request with an empty
// robotCode is a guaranteed 400, so it must fail locally with a readable reason
// instead of spending a network round trip to learn that.
func TestSendRefusesWithoutRobotCode(t *testing.T) {
	robot := &fakeRobot{}
	httpServer := httptest.NewServer(robot.handler())
	t.Cleanup(httpServer.Close)
	adapter := dingtalk.NewAdapter(dingtalk.Config{
		BaseURL: httpServer.URL,
		Token:   "access-token",
	})
	err := adapter.Send(context.Background(), "cid-1", "hi")
	if err == nil {
		t.Fatal("Send succeeded with no robot code")
	}
	if !errors.Is(err, contracts.ErrDingTalkInvalid) {
		t.Fatalf("err = %v, want ErrDingTalkInvalid", err)
	}
	if !strings.Contains(err.Error(), "robotCode") {
		t.Fatalf("err = %v, want it to name robotCode", err)
	}
	if robot.count() != 0 {
		t.Fatal("a send with no robot code still hit the network")
	}
}
