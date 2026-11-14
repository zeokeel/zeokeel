package approvals

// Approval-waiting notifications: a parked approval must reach the user
// outside the app through the optional notifier bridge.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type recordingNotifier struct {
	mu        sync.Mutex
	level     string
	text      string
	sessionID string
	system    bool
	calls     int
}

func (n *recordingNotifier) SetHandler(func(contracts.Notification)) {}

func (n *recordingNotifier) Notify(level, text string) contracts.Notification {
	return n.NotifySystemFor(level, text, "")
}

func (n *recordingNotifier) Dismiss(string) {}

// NotifySystem makes recordingNotifier also satisfy contracts.SystemNotifier.
func (n *recordingNotifier) NotifySystem(level, text string) contracts.Notification {
	return n.NotifySystemFor(level, text, "")
}

// NotifySystemFor makes recordingNotifier satisfy contracts.SessionNotifier and
// records the session tag the approvals backend attaches.
func (n *recordingNotifier) NotifySystemFor(level, text, sessionID string) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.level, n.text, n.sessionID = level, text, sessionID
	n.calls++
	n.system = true
	return contracts.Notification{ID: "n", Level: level, Text: text, SessionID: sessionID}
}

func (n *recordingNotifier) SystemNotificationsSupported() bool { return true }

func (n *recordingNotifier) snapshot() (level, text, sessionID string, calls int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.level, n.text, n.sessionID, n.calls
}

// TestRequestNotifiesApprovalWaiting pins the notification the approvals
// backend raises after a request parks: warning level, the friendly localized
// tool name (never the raw protocol id), the localized non-user source and the
// owning conversation as the session tag (a click opens it; the raw id stays out
// of the visible copy).
func TestRequestNotifiesApprovalWaiting(t *testing.T) {
	svc, err := newService(ModeWorkspace, discardLogger{})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	notifier := &recordingNotifier{}
	svc.notifier = notifier

	const sessionID = "sess-approval-1"
	done := make(chan struct{})
	go func() {
		defer close(done)
		decision, requestErr := svc.Request(context.Background(), contracts.ApprovalRequest{
			Tool:      "team_task_create",
			Source:    contracts.SessionOriginAutomation,
			SessionID: sessionID,
			Summary:   "release checklist",
		})
		if requestErr != nil || decision != contracts.ApprovalAllow {
			t.Errorf("Request = (%q, %v), want allow", decision, requestErr)
		}
	}()

	pending := waitForPending(t, svc)
	level, text, taggedSession, calls := notifier.snapshot()
	if calls != 1 || level != contracts.NotifyWarning {
		t.Fatalf("notification = (%q, %q) calls=%d, want one warning", level, text, calls)
	}
	if taggedSession != sessionID {
		t.Fatalf("notification session tag = %q, want %q", taggedSession, sessionID)
	}
	if !notifier.system {
		t.Fatal("the OS half of the notifier was not used")
	}
	for _, want := range []string{"团队任务", "自动化"} {
		if !strings.Contains(text, want) {
			t.Errorf("notification %q does not mention %q", text, want)
		}
	}
	// The design invariant: visible copy never carries the raw tool id or the
	// raw session id (the session travels as the click tag).
	for _, raw := range []string{"team_task_create", contracts.SessionOriginAutomation, sessionID} {
		if strings.Contains(text, raw) {
			t.Errorf("notification %q leaked the raw id %q", text, raw)
		}
	}
	if err := svc.Resolve(pending, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	<-done
}

// TestRequestWithoutNotifierStaysSilent keeps the headless/no-bridge behavior:
// a parked request is queued and resolved with no notification call.
func TestRequestWithoutNotifierStaysSilent(t *testing.T) {
	svc, err := newService(ModeWorkspace, discardLogger{})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"})
	}()
	pending := waitForPending(t, svc)
	if err := svc.Resolve(pending, contracts.ApprovalDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	<-done
}

// waitForPending polls until one approval request is pending and returns its ID.
// Polling (instead of Subscribe) removes the race between Request registering
// its entry and the test installing a listener.
func waitForPending(t *testing.T, svc *service) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pending := svc.Pending(); len(pending) > 0 {
			return pending[0].ID
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no approval request became pending")
	return ""
}

// fakeLocale is the minimal contracts.LocaleService the copy tests need.
type fakeLocale struct{ lang string }

func (l fakeLocale) Language() string    { return l.lang }
func (l fakeLocale) SetLanguage(string)  {}
func (l fakeLocale) T(key string) string { return key }

// TestApprovalWaitingText covers the notification copy: localized prefix and
// tool display name, localized run origin, and no raw protocol id for a tool
// the kit does not know.
func TestApprovalWaitingText(t *testing.T) {
	tests := []struct {
		name    string
		locale  contracts.LocaleService
		request contracts.ApprovalRequest
		want    string
	}{
		{
			name:    "zh known tool",
			request: contracts.ApprovalRequest{Tool: "bash"},
			want:    "审批等待：运行命令",
		},
		{
			name:    "zh tool family and automation",
			request: contracts.ApprovalRequest{Tool: "team_task_create", Source: contracts.SessionOriginAutomation},
			want:    "审批等待：团队任务（自动化）",
		},
		{
			name:    "zh human turn has no source label",
			request: contracts.ApprovalRequest{Tool: "bash", Source: "user"},
			want:    "审批等待：运行命令",
		},
		{
			name:    "zh unknown tool keeps a generic label",
			request: contracts.ApprovalRequest{Tool: "acme_ping"},
			want:    "审批等待：工具调用",
		},
		{
			name:    "zh unknown origin is omitted",
			request: contracts.ApprovalRequest{Tool: "bash", Source: "some-new-origin"},
			want:    "审批等待：运行命令",
		},
		{
			name:    "en known tool and subagent",
			locale:  fakeLocale{lang: "en"},
			request: contracts.ApprovalRequest{Tool: "bash", Source: contracts.SessionOriginSubagent},
			want:    "Approval waiting: Run command (Subagent)",
		},
		{
			name:    "en unknown tool keeps a generic label",
			locale:  fakeLocale{lang: "en"},
			request: contracts.ApprovalRequest{Tool: "acme_ping"},
			want:    "Approval waiting: a tool call",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &service{locale: test.locale}
			if got := svc.approvalWaitingText(test.request); got != test.want {
				t.Fatalf("approvalWaitingText(%+v) = %q, want %q", test.request, got, test.want)
			}
		})
	}
}
