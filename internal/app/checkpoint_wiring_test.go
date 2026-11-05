package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// resumeProvider is a deterministic provider for the checkpoint wiring tests.
// It records every request and replies with one fixed assistant message.
type resumeProvider struct {
	mu    sync.Mutex
	reply string
	reqs  []runtime.Request
}

var _ runtime.Provider = (*resumeProvider)(nil)

func (p *resumeProvider) Stream(_ context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	reply := p.reply
	p.mu.Unlock()

	out := make(chan runtime.Chunk, 1)
	out <- runtime.Chunk{Text: reply, Done: true}
	close(out)
	return out, nil
}

func (p *resumeProvider) requestAt(i int) (runtime.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.reqs) {
		return runtime.Request{}, false
	}
	return p.reqs[i], true
}

// newCheckpointController exercises the standalone checkpoint path: the agent
// is built against the opened store with only the store-backed checkpoint
// adapter and History deliberately nil, so the checkpoint is the source of the
// resumed conversation. This is NOT the production wiring — buildAgent always
// configures History and treats the checkpoint as a best-effort mirror; that
// contract is covered by TestCheckpointProductionWiring and
// TestCheckpointProductionMirrorOnly (internal/app/compaction_wiring_test.go).
func newCheckpointController(t *testing.T, dir string, provider runtime.Provider) (*session.Controller, func() error, *store.Store) {
	t.Helper()
	var wired *store.Store
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		wired = st
		agent, err := runtime.New(runtime.Config{
			Provider:        provider,
			Instruction:     "checkpoint wiring test",
			CheckpointStore: &storeCheckpoint{st: st},
		})
		if err != nil {
			return nil, err
		}
		return &AgentBundle{Agent: agent}, nil
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	return ctrl, cleanup, wired
}

// checkpointTurns decodes the checkpoint history into role:content keys.
func checkpointTurns(t *testing.T, cp *store.Checkpoint, err error) []string {
	t.Helper()
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("store returned no checkpoint")
	}
	var history []runtime.Message
	if err := json.Unmarshal(cp.History, &history); err != nil {
		t.Fatalf("decode checkpoint history %s: %v", cp.History, err)
	}
	out := make([]string, 0, len(history))
	for _, msg := range history {
		out = append(out, string(msg.Role)+":"+msg.Content)
	}
	return out
}

// conversationMessages returns the role:content keys of the conversation part
// of a provider request. The leading system instruction is part of the agent
// topology, not the checkpointed conversation, so it is skipped.
func conversationMessages(req runtime.Request) []string {
	out := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		if msg.Role == runtime.RoleSystem {
			continue
		}
		out = append(out, string(msg.Role)+":"+msg.Content)
	}
	return out
}

// TestCheckpointPersistResume is the restart-resume acceptance test: a
// completed turn is checkpointed to the store, the controller and agent are
// rebuilt against that same store (a process restart), and the next turn sees
// both the previous history and the new prompt.
func TestCheckpointPersistResume(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	firstProvider := &resumeProvider{reply: "first-reply"}
	first, cleanupFirst, firstStore := newCheckpointController(t, dir, firstProvider)
	t.Cleanup(func() { _ = cleanupFirst() })

	id, err := first.CreateSession(ctx, "checkpoint resume")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := first.Submit(ctx, id, "first prompt"); err != nil {
		t.Fatalf("Submit(first): %v", err)
	}
	waitWiringIdle(t, first)
	first.Wait()

	cp, err := firstStore.LoadCheckpoint(id)
	if err != nil {
		t.Fatalf("LoadCheckpoint after first turn: %v", err)
	}
	if cp == nil {
		t.Fatal("no checkpoint after the first completed turn")
	}
	if cp.TurnID == "" {
		t.Fatal("checkpoint has no TurnID")
	}
	if cp.Phase != runtime.CheckpointPhaseCompleted {
		t.Fatalf("checkpoint Phase = %q, want %q", cp.Phase, runtime.CheckpointPhaseCompleted)
	}
	wantAfterFirst := []string{"user:first prompt", "assistant:first-reply"}
	if got := checkpointTurns(t, cp, nil); strings.Join(got, "|") != strings.Join(wantAfterFirst, "|") {
		t.Fatalf("checkpoint after first turn = %v, want %v", got, wantAfterFirst)
	}

	// Close the store/controller/agent, then rebuild from the same directory.
	if err := cleanupFirst(); err != nil {
		t.Fatalf("cleanup first services: %v", err)
	}

	secondProvider := &resumeProvider{reply: "second-reply"}
	second, cleanupSecond, secondStore := newCheckpointController(t, dir, secondProvider)
	t.Cleanup(func() { _ = cleanupSecond() })

	if _, err := second.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if err := second.Submit(ctx, id, "second prompt"); err != nil {
		t.Fatalf("Submit(second): %v", err)
	}
	waitWiringIdle(t, second)
	second.Wait()

	req, ok := secondProvider.requestAt(0)
	if !ok {
		t.Fatal("restarted provider received no request")
	}
	wantRequest := []string{"user:first prompt", "assistant:first-reply", "user:second prompt"}
	if got := conversationMessages(req); strings.Join(got, "|") != strings.Join(wantRequest, "|") {
		t.Fatalf("resumed provider messages = %v, want %v", got, wantRequest)
	}

	resumed, err := secondStore.LoadCheckpoint(id)
	if err != nil {
		t.Fatalf("LoadCheckpoint after resumed turn: %v", err)
	}
	wantStored := []string{
		"user:first prompt",
		"assistant:first-reply",
		"user:second prompt",
		"assistant:second-reply",
	}
	if got := checkpointTurns(t, resumed, nil); strings.Join(got, "|") != strings.Join(wantStored, "|") {
		t.Fatalf("resumed checkpoint = %v, want %v", got, wantStored)
	}
	if resumed.TurnID == "" || resumed.TurnID == cp.TurnID {
		t.Fatalf("resumed TurnID = %q, want a new turn id (previous %q)", resumed.TurnID, cp.TurnID)
	}
}

// TestCheckpointDeletedWithSession pins the data-safety contract: deleting a
// session also deletes its recovery checkpoint.
func TestCheckpointDeletedWithSession(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	provider := &resumeProvider{reply: "reply"}
	ctrl, cleanup, st := newCheckpointController(t, dir, provider)
	t.Cleanup(func() { _ = cleanup() })

	id, err := ctrl.CreateSession(ctx, "checkpoint delete")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	if _, err := st.LoadCheckpoint(id); err != nil {
		t.Fatalf("LoadCheckpoint before delete: %v", err)
	}
	if err := ctrl.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.LoadCheckpoint(id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("LoadCheckpoint after DeleteSession error = %v, want ErrNotFound", err)
	}
	if _, err := st.GetSession(id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSession after DeleteSession error = %v, want ErrNotFound", err)
	}
}
