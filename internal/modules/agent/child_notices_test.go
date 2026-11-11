package agent

// Tests for the completion receipts of delegated children: the parent run's
// model context receives one receipt per finished child, exactly once, and the
// transcript-side derivation reads the same durable records.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// findNoticeInRequest reports whether one provider request carries the
// completion receipt text.
func findNoticeInRequest(request rig.Request, needle string) bool {
	for _, message := range request.Messages {
		for _, part := range message.Content {
			if strings.Contains(part.Text, needle) {
				return true
			}
		}
	}
	return false
}

// preparedNotices returns the currently pending receipts of one session.
func preparedNotices(svc *service, sessionID string) []rig.Message {
	notices, _ := svc.prepareChildNotices(sessionID)
	return notices
}

// consumeNotices stages and commits the receipts like one successful run.
func consumeNotices(svc *service, sessionID string) []rig.Message {
	notices, refs := svc.prepareChildNotices(sessionID)
	batch := &childNoticeBatch{service: svc, sessionID: sessionID, pending: refs}
	batch.commit()
	return notices
}

// TestChildNoticeMessagesConsumedOnce pins the dedupe/flag behaviour of the
// durable receipts.
func TestChildNoticeMessagesConsumedOnce(t *testing.T) {
	svc, _, _, ctx := bootAgentModuleAt(t, t.TempDir())
	defer func() { _ = ctx.DisposeAll() }()
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := childSessionRecord{
		ID:              svc.childSessions.newID(),
		ParentSessionID: session,
		Kind:            contracts.ChildAgentTeammate,
		Name:            "研究员",
		Status:          contracts.ChildAgentCompleted,
		Output:          "报告已完成",
		Finished:        time.Now(),
	}
	if !svc.childSessions.upsert(record) {
		t.Fatal("upsert child record reported no change")
	}
	first := consumeNotices(svc, session)
	if len(first) != 1 || first[0].Role != rig.RoleUser {
		t.Fatalf("first receipts = %+v, want one user message", first)
	}
	if text := first[0].Content[0].Text; !strings.Contains(text, "Background teammate") ||
		!strings.Contains(text, "报告已完成") {
		t.Fatalf("receipt text = %q", text)
	}
	if second := preparedNotices(svc, session); len(second) != 0 {
		t.Fatalf("second receipts = %+v, want none (already notified)", second)
	}
	stored, ok := svc.childSessions.record(record.ID)
	if !ok || !stored.Notified {
		t.Fatalf("stored record = %+v (ok=%v), want Notified", stored, ok)
	}
}

// TestChildNoticeSurvivesRecordRewrite is the regression test for the audit
// finding that Close/shutdown rewrote the record with Notified=false and made the
// parent model see the same receipt again.
func TestChildNoticeSurvivesRecordRewrite(t *testing.T) {
	svc, _, _, ctx := bootAgentModuleAt(t, t.TempDir())
	defer func() { _ = ctx.DisposeAll() }()
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	finished := time.Now().Add(-time.Minute)
	record := childSessionRecord{
		ID:              svc.childSessions.newID(),
		ParentSessionID: session,
		Kind:            contracts.ChildAgentTeammate,
		BackendRef:      "member-1",
		Name:            "研究员",
		Status:          contracts.ChildAgentCompleted,
		Output:          "done",
		Finished:        finished,
	}
	if !svc.childSessions.upsert(record) {
		t.Fatal("upsert child record reported no change")
	}
	if notices := consumeNotices(svc, session); len(notices) != 1 {
		t.Fatalf("initial receipts = %d, want 1", len(notices))
	}
	// Close and the shutdown archive rebuild the whole record from the live
	// member snapshot (Notified=false); the consumed flag must survive.
	rewrite := record
	rewrite.Notified = false
	rewrite.Updated = time.Now()
	// The store may report "unchanged" because the preserved Notified makes the
	// snapshot equal to the stored one; either way the flag must hold.
	svc.childSessions.upsert(rewrite)
	if notices := preparedNotices(svc, session); len(notices) != 0 {
		t.Fatalf("receipts after rewrite = %d, want 0", len(notices))
	}
	stored, _ := svc.childSessions.record(record.ID)
	if !stored.Notified {
		t.Fatal("Notified survived neither the rewrite nor persistence")
	}
}

// TestChildNoticeStableAcrossRestart covers the quit/restart replay.
func TestChildNoticeStableAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	svc, _, _, ctx := bootAgentModuleAt(t, dataDir)
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := childSessionRecord{
		ID:              svc.childSessions.newID(),
		ParentSessionID: session,
		Kind:            contracts.ChildAgentTeammate,
		Name:            "审查员",
		Status:          contracts.ChildAgentCompleted,
		Output:          "ok",
		Finished:        time.Now(),
	}
	svc.childSessions.upsert(record)
	if notices := consumeNotices(svc, session); len(notices) != 1 {
		t.Fatalf("initial receipts = %d, want 1", len(notices))
	}
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first module: %v", err)
	}
	svc2, _, _, ctx2 := bootAgentModuleAt(t, dataDir)
	defer func() { _ = ctx2.DisposeAll() }()
	if notices := preparedNotices(svc2, session); len(notices) != 0 {
		t.Fatalf("restart replayed %d receipt(s), want 0", len(notices))
	}
}

// TestChildNoticeRetriedAfterFailedRun pins the exactly-once-on-success rule: a
// failed request leaves the receipt pending for the next run.
func TestChildNoticeRetriedAfterFailedRun(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.childSessions.upsert(childSessionRecord{
		ID:              svc.childSessions.newID(),
		ParentSessionID: session,
		Kind:            contracts.ChildAgentTeammate,
		Name:            "审查员",
		Status:          contracts.ChildAgentCompleted,
		Output:          "审查完成",
		Finished:        time.Now(),
	})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) {
		return errorProvider{err: errors.New("429 rate limit exceeded")}, nil
	}
	if err := svc.Send(session, "第一次"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, session)
	if notices := preparedNotices(svc, session); len(notices) != 1 {
		t.Fatalf("pending receipts after failed run = %d, want 1", len(notices))
	}
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	if err := svc.Send(session, "重试"); err != nil {
		t.Fatalf("retry Send: %v", err)
	}
	waitIdle(t, svc, session)
	if !findNoticeInRequest(provider.lastRequest(), "Background teammate") {
		t.Fatal("retry request did not carry the pending receipt")
	}
	if notices := preparedNotices(svc, session); len(notices) != 0 {
		t.Fatalf("pending receipts after successful retry = %d, want 0", len(notices))
	}
}

// TestChildNoticeInjectedIntoNextRun is the end-to-end test: a finished child
// record is appended to the parent model's next request as a user message, and
// only to that run.
func TestChildNoticeInjectedIntoNextRun(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.childSessions.upsert(childSessionRecord{
		ID:              svc.childSessions.newID(),
		ParentSessionID: session,
		Kind:            contracts.ChildAgentTeammate,
		Name:            "审查员",
		Status:          contracts.ChildAgentCompleted,
		Output:          "审查完成",
		Finished:        time.Now(),
	})
	if err := svc.Send(session, "继续"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, session)
	if !findNoticeInRequest(provider.lastRequest(), "Background teammate") {
		t.Fatalf("first run request carries no completion receipt")
	}
	provider.mu.Lock()
	before := len(provider.requests)
	provider.mu.Unlock()
	if err := svc.Send(session, "再来一次"); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	waitIdle(t, svc, session)
	provider.mu.Lock()
	requests := append([]rig.Request(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) <= before {
		t.Fatalf("second run did not reach the provider")
	}
	for _, request := range requests[before:] {
		if findNoticeInRequest(request, "Background teammate") {
			t.Fatalf("second run repeated the completion receipt: %+v", request.Messages)
		}
	}
}

// TestChildAttributionUsesRunScopedSession covers the delegation attribution
// fix: a child started by a run of session B belongs to B even when another
// conversation is active.
func TestChildAttributionUsesRunScopedSession(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	parent, err := svc.NewSession("活跃会话")
	if err != nil {
		t.Fatalf("NewSession parent: %v", err)
	}
	requester, err := svc.NewSession("发起会话")
	if err != nil {
		t.Fatalf("NewSession requester: %v", err)
	}
	if err := svc.SetActiveSession(parent); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	ctx := contracts.WithAgentSession(context.Background(), requester)
	if _, err := svc.Run(ctx, contracts.SubagentRequest{Prompt: "检查构建"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, record := range svc.childSessions.records() {
		if record.Kind == contracts.ChildAgentSubagent && record.Name != "" {
			if record.ParentSessionID != requester {
				t.Fatalf("child ParentSessionID = %q, want requester %q", record.ParentSessionID, requester)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("delegated child was not recorded")
	}
	if notices := preparedNotices(svc, parent); len(notices) != 0 {
		t.Fatalf("active session received %d notice(s) of another conversation's child", len(notices))
	}
}

// TestChildSessionStoreCloseStopsWrites pins the dispose teardown: a late write
// after close must not touch the document.
func TestChildSessionStoreCloseStopsWrites(t *testing.T) {
	dataDir := t.TempDir()
	svc, _, _, ctx := bootAgentModuleAt(t, dataDir)
	first := childSessionRecord{
		ID:       svc.childSessions.newID(),
		Kind:     contracts.ChildAgentSubagent,
		Name:     "first",
		Status:   contracts.ChildAgentCompleted,
		Finished: time.Now(),
	}
	if !svc.childSessions.upsert(first) {
		t.Fatal("first upsert failed")
	}
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	svc.childSessions.close()
	late := childSessionRecord{
		ID:       svc.childSessions.newID(),
		Kind:     contracts.ChildAgentSubagent,
		Name:     "late",
		Status:   contracts.ChildAgentCompleted,
		Finished: time.Now(),
	}
	if svc.childSessions.upsert(late) {
		t.Fatal("upsert after close reported an update")
	}
	svc2, _, _, ctx2 := bootAgentModuleAt(t, dataDir)
	defer func() { _ = ctx2.DisposeAll() }()
	records := svc2.childSessions.records()
	if len(records) != 1 || records[0].ID != first.ID {
		t.Fatalf("restored records = %+v, want only the pre-close record", records)
	}
}

// TestModelReceiptsSkipOneShotSubagents: a synchronous subagent's answer is
// already the subagent tool result in the parent history; only continuable
// teammate completions get a separate model-facing receipt.
func TestModelReceiptsSkipOneShotSubagents(t *testing.T) {
	svc, _, _, ctx := bootAgentModuleAt(t, t.TempDir())
	defer func() { _ = ctx.DisposeAll() }()
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.childSessions.upsert(childSessionRecord{
		ID: svc.childSessions.newID(), ParentSessionID: session,
		Kind: contracts.ChildAgentSubagent, Name: "一次性子代理",
		Status: contracts.ChildAgentCompleted, Output: "answer", Finished: time.Now(),
	})
	if notices := preparedNotices(svc, session); len(notices) != 0 {
		t.Fatalf("one-shot subagent produced %d model receipt(s), want 0", len(notices))
	}
	svc.childSessions.upsert(childSessionRecord{
		ID: svc.childSessions.newID(), ParentSessionID: session,
		Kind: contracts.ChildAgentTeammate, Name: "队友",
		Status: contracts.ChildAgentCompleted, Output: "done", Finished: time.Now(),
	})
	notices := preparedNotices(svc, session)
	if len(notices) != 1 || !strings.Contains(notices[0].Content[0].Text, "Background teammate") {
		t.Fatalf("teammate receipts = %+v, want one background teammate message", notices)
	}
}

// drainSpyTool is a NoticeSource whose queue is drained by the wrapper.
type drainSpyTool struct {
	inner sessionCaptureTool
	drain func()
}

func (t *drainSpyTool) Name() string           { return "wait_agent" }
func (t *drainSpyTool) Description() string    { return "spy" }
func (t *drainSpyTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *drainSpyTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}
func (t *drainSpyTool) PendingNotices() []string {
	if t.drain != nil {
		t.drain()
	}
	return []string{"subagent member-1 (worker) finished: answer"}
}

// TestTrackedTeamToolDropsManagerNotices pins the single-channel decision: the
// rig manager's completion notices are drained (bounded queue) but not surfaced
// to the model, because child_notices.go delivers session-scoped receipts.
func TestTrackedTeamToolDropsManagerNotices(t *testing.T) {
	drained := 0
	inner := &drainSpyTool{drain: func() { drained++ }}
	wrapped := &trackedTeamTool{inner: inner}
	if notices := wrapped.PendingNotices(); len(notices) != 0 {
		t.Fatalf("tracked team tool surfaced %v, want none", notices)
	}
	if drained != 1 {
		t.Fatalf("manager notices were not drained (drains=%d)", drained)
	}
}
