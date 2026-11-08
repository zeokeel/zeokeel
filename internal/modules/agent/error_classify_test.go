package agent

// Tests for backend error classification and its placement in the read model.

import (
	"context"
	"errors"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestClassifyErrorTable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		msg  string
		want string
	}{
		{"nil", nil, "", "other"},
		{"auth 401", errors.New("HTTP 401 Unauthorized"), "", "auth"},
		{"auth key", nil, "invalid api key provided", "auth"},
		{"forbidden", errors.New("403 forbidden"), "", "auth"},
		{"rate 429", errors.New("429 too many requests"), "", "rate_limit"},
		{"rate limit", nil, "Rate limit exceeded", "rate_limit"},
		{"quota", errors.New("quota exceeded"), "", "rate_limit"},
		{"timeout", errors.New("request timeout"), "", "timeout"},
		{"deadline", context.DeadlineExceeded, "", "timeout"},
		{"network refused", errors.New("dial tcp: connection refused"), "", "network"},
		{"dns", errors.New("lookup api.example: no such host"), "", "network"},
		{"network unreachable", errors.New("network is unreachable"), "", "network"},
		{"context length", errors.New("maximum context length exceeded"), "", "context"},
		{"token limit", nil, "too many tokens in prompt", "context"},
		{"cancelled", context.Canceled, "", "cancelled"},
		{"other", errors.New("something exploded"), "", "other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := contracts.ClassifyError(test.err, test.msg); got != test.want {
				t.Fatalf("ClassifyError(%v, %q) = %q, want %q", test.err, test.msg, got, test.want)
			}
		})
	}
}

// errorProvider fails every stream with a fixed error.
type errorProvider struct{ err error }

func (p errorProvider) Name() string { return "error" }
func (p errorProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	return nil, p.err
}

func TestRunFailureSetsErrorKind(t *testing.T) {
	svc := newService(Config{}, rigtools.NewRegistry(), newFakeSettings(nil), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) {
		return errorProvider{err: errors.New("429 rate limit exceeded")}, nil
	}
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	var last contracts.ChatMessage
	if len(session.Messages) > 0 {
		last = session.Messages[len(session.Messages)-1]
	}
	if last.Role != contracts.ChatRoleAssistant || last.Error == "" {
		t.Fatalf("last message = %+v, want assistant error", last)
	}
	if last.ErrorKind != "rate_limit" {
		t.Fatalf("ErrorKind = %q, want rate_limit", last.ErrorKind)
	}
}

func TestSuccessfulRunClearsErrorKind(t *testing.T) {
	provider := &captureProvider{}
	svc := newService(Config{}, rigtools.NewRegistry(), newFakeSettings(nil), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	last := session.Messages[len(session.Messages)-1]
	if last.Error != "" || last.ErrorKind != "" {
		t.Fatalf("successful run error fields = %q/%q", last.Error, last.ErrorKind)
	}
}
