package agent

// End-to-end test for the background-job completion notification seam: the
// toolkit proxies implement rig.NoticeSource, the run's repeat-guard wrapper
// forwards that capability, and Rig appends the drained notices as a user
// message before the next model request. This is what makes Rig's job prompt
// ("You are notified in-session when a job finishes", copied verbatim from
// DSH) true; before this seam the notices were queued and never delivered.

import (
	"context"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// queuedNoticeTool implements rig.NoticeSource and queues one completion notice
// the first time it is invoked (the moment a background job settles).
type queuedNoticeTool struct {
	mu      sync.Mutex
	pending []string
}

func (t *queuedNoticeTool) Name() string           { return "queued_notice" }
func (t *queuedNoticeTool) Description() string    { return "queues a job completion notice" }
func (t *queuedNoticeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *queuedNoticeTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	t.mu.Lock()
	t.pending = append(t.pending, "background job job-1 (pwsh: echo hi) finished [status: completed, exit code: 0]. Read its output with job_output.")
	t.mu.Unlock()
	return rig.ToolResult{Content: "ok"}, nil
}

// PendingNotices implements rig.NoticeSource; the drain is consuming.
func (t *queuedNoticeTool) PendingNotices() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	notices := t.pending
	t.pending = nil
	return notices
}

// noticeScriptProvider calls queued_notice once, then returns a final answer; it
// records every request so the test can inspect the injected notice message.
type noticeScriptProvider struct {
	mu       sync.Mutex
	calls    int
	requests []rig.Request
}

func (p *noticeScriptProvider) Name() string { return "notice-script" }

func (p *noticeScriptProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "queued_notice", Arguments: "{}"}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

// TestRunDeliversBackgroundJobNotices proves the notification reaches the next
// model request as a "Background notifications:" user message.
func TestRunDeliversBackgroundJobNotices(t *testing.T) {
	tool := &queuedNoticeTool{}
	provider := &noticeScriptProvider{}
	svc := newTestService(t, Config{}, nil, nil, tool)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("notices")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "start a job"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)

	provider.mu.Lock()
	requests := append([]rig.Request(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	notified := false
	for _, message := range requests[1].Messages {
		if message.Role != rig.RoleUser {
			continue
		}
		var text strings.Builder
		for _, part := range message.Content {
			text.WriteString(part.Text)
		}
		body := text.String()
		if strings.Contains(body, "Background notifications:") &&
			strings.Contains(body, "background job job-1") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("second request carries no background notification message: %+v", requests[1].Messages)
	}
}
