package agent

// Tests for DSH cold resume: a historical teammate/subagent continues from its
// durable child session, persists the resumed turns, and can be resumed again
// after another restart.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newColdResumeService builds a bare service with a durable child-session store
// in a temp data directory (so a second service simulates a restart).
func newColdResumeService(t *testing.T, store *fakeStore, dataDir string, provider rig.Provider) *service {
	t.Helper()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	svc.dataDir = dataDir
	svc.childSessions = newChildSessionStore(dataDir, &fakeLogger{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc
}

// seedHistoricalChild records one finished historical child session.
func seedHistoricalChild(t *testing.T, svc *service, parentSessionID, kind, name, prompt, output string) childSessionRecord {
	t.Helper()
	record := childSessionRecord{
		ID:              svc.childSessions.newID(),
		ParentSessionID: parentSessionID,
		Kind:            kind,
		Name:            name,
		Prompt:          prompt,
		Output:          output,
		Status:          contracts.ChildAgentCompleted,
		Started:         time.Now().Add(-time.Hour),
		Finished:        time.Now().Add(-time.Minute),
		Notified:        true,
	}
	if !svc.childSessions.upsert(record) {
		t.Fatal("failed to seed the historical child record")
	}
	return record
}

// TestColdResumeHistoricalSubagent covers the core path: the resumed run loads
// the seeded child history, appends the new turn to the durable child session,
// updates the record and can be resumed again (including after a restart).
func TestColdResumeHistoricalSubagent(t *testing.T) {
	dataDir := t.TempDir()
	store := newFakeStore()
	svc := newColdResumeService(t, store, dataDir, &captureProvider{})
	parentID, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentSubagent, "researcher", "summarize the repo", "the first summary")
	children := newChildAgentsService(svc, nil)

	historical, ok := children.ChildAgent(record.ID)
	if !ok || !historical.Historical || !historical.Resumable {
		t.Fatalf("historical child = %+v (ok=%v), want Historical+Resumable", historical, ok)
	}
	answer, err := children.SendChildAgent(context.Background(), record.ID, "now list the TODOs")
	if err != nil {
		t.Fatalf("SendChildAgent(historical): %v", err)
	}
	if answer != "ok" {
		t.Fatalf("resumed answer = %q, want the scripted provider output", answer)
	}
	history, err := store.History().Load(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("load child history: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("child history = %d messages, want seeded prompt/answer + follow-up/reply", len(history))
	}
	if history[0].Role != rig.RoleUser || history[0].Text() != "summarize the repo" ||
		history[1].Role != rig.RoleAssistant || history[1].Text() != "the first summary" ||
		history[2].Role != rig.RoleUser || history[2].Text() != "now list the TODOs" ||
		history[3].Role != rig.RoleAssistant || history[3].Text() != "ok" {
		t.Fatalf("child history = %+v, want the seeded original + resumed turn", history)
	}
	updated, ok := svc.childSessions.record(record.ID)
	if !ok || updated.Status != contracts.ChildAgentCompleted || updated.Output != "ok" || updated.Notified != true {
		t.Fatalf("resumed record = %+v (ok=%v), want completed/output/sync-notified", updated, ok)
	}
	if len(updated.Activity) < 2 {
		t.Fatalf("resumed record activity = %+v, want send/finish rows", updated.Activity)
	}
	after, _ := children.ChildAgent(record.ID)
	if !after.Historical || !after.Resumable || after.Output != "ok" {
		t.Fatalf("child after resume = %+v, want a Historical+Resumable row with the new answer", after)
	}

	// A second resume keeps accumulating in the same durable child session.
	if _, err := children.SendChildAgent(context.Background(), record.ID, "one more question"); err != nil {
		t.Fatalf("second SendChildAgent: %v", err)
	}
	history, _ = store.History().Load(context.Background(), record.ID)
	if len(history) != 6 {
		t.Fatalf("child history after second resume = %d, want 6", len(history))
	}

	// A fresh process (same store + data dir) resumes the same child again.
	restarted := newColdResumeService(t, store, dataDir, &captureProvider{})
	restartedChildren := newChildAgentsService(restarted, nil)
	answer, err = restartedChildren.SendChildAgent(context.Background(), record.ID, "after restart")
	if err != nil {
		t.Fatalf("SendChildAgent after restart: %v", err)
	}
	if answer != "ok" {
		t.Fatalf("after-restart answer = %q", answer)
	}
	history, _ = store.History().Load(context.Background(), record.ID)
	if len(history) != 8 {
		t.Fatalf("child history after restart resume = %d, want 8", len(history))
	}
}

// TestColdResumeHistoricalTeammateViaTeamService covers TeamService.Send/Async:
// a historical teammate with no live manager member resumes its child session.
func TestColdResumeHistoricalTeammateViaTeamService(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	parentID, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentTeammate, "worker", "first task", "done")
	team := newTeamService(svc)

	answer, err := team.Send(context.Background(), record.ID, "follow up")
	if err != nil {
		t.Fatalf("TeamService.Send(historical): %v", err)
	}
	if answer != "ok" {
		t.Fatalf("TeamService.Send answer = %q", answer)
	}
	if updated, ok := svc.childSessions.record(record.ID); !ok || updated.Status != contracts.ChildAgentCompleted {
		t.Fatalf("teammate record after resume = %+v (ok=%v)", updated, ok)
	}

	// send_message semantics: SendAsync acks immediately and resumes in the
	// background (the record finishes asynchronously and stays unnotified so the
	// parent's next run receives the completion receipt).
	if err := team.SendAsync(record.ID, "async follow up"); err != nil {
		t.Fatalf("TeamService.SendAsync(historical): %v", err)
	}
	waitFor(t, "the async cold resume to finish", func() bool {
		return !svc.coldResumeActive(record.ID)
	})
	if updated, ok := svc.childSessions.record(record.ID); !ok || updated.Status != contracts.ChildAgentCompleted || updated.Notified {
		t.Fatalf("async resumed record = %+v (ok=%v), want completed and unnotified", updated, ok)
	}
}

// TestColdResumeOverlayIsVisibleWhileRunning covers the read-model overlay: a
// resumed child shows as a live running row until the run finishes.
func TestColdResumeOverlayIsVisibleWhileRunning(t *testing.T) {
	gated := newGatedProvider()
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return gated, nil }
	parentID, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentSubagent, "worker", "task", "done")
	children := newChildAgentsService(svc, nil)
	if _, err := svc.ColdResumeChildAsync(record.ID, "keep going"); err != nil {
		t.Fatalf("ColdResumeChildAsync: %v", err)
	}
	waitFor(t, "the cold resume overlay", func() bool { return svc.coldResumeActive(record.ID) })
	live, ok := children.ChildAgent(record.ID)
	if !ok || live.Historical || live.Resumable || live.Status != contracts.ChildAgentRunning {
		t.Fatalf("child while resuming = %+v (ok=%v), want a live running row", live, ok)
	}
	gated.releaseNow()
	waitFor(t, "the cold resume to finish", func() bool { return !svc.coldResumeActive(record.ID) })
	finished, ok := children.ChildAgent(record.ID)
	if !ok || !finished.Historical || !finished.Resumable || finished.Status != contracts.ChildAgentCompleted {
		t.Fatalf("child after resume = %+v (ok=%v), want Historical+Resumable completed", finished, ok)
	}
}

// TestColdResumeSendMessageToolHook covers the model-facing send_message path:
// a historical teammate target is intercepted, acked and resumed in the
// background instead of failing as an unknown manager member.
func TestColdResumeSendMessageToolHook(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	parentID, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentTeammate, "researcher", "task", "done")
	wrapper := &trackedTeamTool{
		inner:      &fakeSendTool{name: "send_message"},
		resumeSend: svc.coldResumeHistoricalSend,
	}
	result, err := wrapper.Invoke(context.Background(), rig.ToolCall{
		ID: "call-1", Name: "send_message",
		Arguments: `{"target":"researcher","message":"continue please"}`,
	})
	if err != nil {
		t.Fatalf("send_message invoke: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "cold-resumed") {
		t.Fatalf("send_message result = %+v, want the cold-resume ack", result)
	}
	waitFor(t, "the send_message cold resume to finish", func() bool {
		return !svc.coldResumeActive(record.ID)
	})
	if updated, ok := svc.childSessions.record(record.ID); !ok || updated.Status != contracts.ChildAgentCompleted {
		t.Fatalf("record after send_message resume = %+v (ok=%v)", updated, ok)
	}
}

// fakeSendTool is a team send tool that always fails as an unknown member; the
// cold-resume hook must prevent it from being invoked for a historical target.
type fakeSendTool struct{ name string }

func (f *fakeSendTool) Name() string           { return f.name }
func (f *fakeSendTool) Description() string    { return "send" }
func (f *fakeSendTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (f *fakeSendTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "send_message: member not found", IsError: true}, nil
}

// TestColdResumeSafeErrors covers the refusal paths: unknown child, empty
// message, a child with nothing to seed, and a concurrent resume.
func TestColdResumeSafeErrors(t *testing.T) {
	dataDir := t.TempDir()
	svc := newColdResumeService(t, newFakeStore(), dataDir, &captureProvider{})
	parentID, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := svc.ColdResumeChild(context.Background(), "child-missing", "hi"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown child resume = %v, want a not-found error", err)
	}
	record := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentSubagent, "empty", "", "")
	if _, err := svc.ColdResumeChild(context.Background(), record.ID, "hi"); !errors.Is(err, ErrColdResumeUnavailable) {
		t.Fatalf("child without seed history = %v, want ErrColdResumeUnavailable", err)
	}
	record2 := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentSubagent, "seeded", "task", "")
	if _, err := svc.ColdResumeChild(context.Background(), record2.ID, "   "); !errors.Is(err, ErrColdResumeUnavailable) {
		t.Fatalf("empty message = %v, want ErrColdResumeUnavailable", err)
	}

	gated := newGatedProvider()
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return gated, nil }
	record3 := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentSubagent, "busy", "task", "")
	if _, err := svc.ColdResumeChildAsync(record3.ID, "first"); err != nil {
		t.Fatalf("first async resume: %v", err)
	}
	waitFor(t, "the cold resume overlay", func() bool { return svc.coldResumeActive(record3.ID) })
	if _, err := svc.ColdResumeChild(context.Background(), record3.ID, "second"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("concurrent resume = %v, want an already-running error", err)
	}
	gated.releaseNow()
	waitFor(t, "the cold resume to finish", func() bool { return !svc.coldResumeActive(record3.ID) })
}
