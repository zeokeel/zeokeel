package agent

// Tests for model-driven memory extraction.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeMemoryStore struct {
	mu        sync.Mutex
	memories  []contracts.Memory
	addErr    error
	added     []string
	scopes    []string
	failAfter int
}

func (m *fakeMemoryStore) List(context.Context) ([]contracts.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]contracts.Memory(nil), m.memories...), nil
}
func (m *fakeMemoryStore) Add(_ context.Context, memory contracts.Memory) (contracts.Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addErr != nil {
		return contracts.Memory{}, m.addErr
	}
	if m.failAfter > 0 && len(m.added) >= m.failAfter {
		return contracts.Memory{}, errors.New("disk full")
	}
	m.added = append(m.added, memory.Text)
	m.scopes = append(m.scopes, memory.Scope)
	if strings.TrimSpace(memory.Scope) == "" {
		memory.Scope = "global"
	}
	memory.ID = "mem-1"
	m.memories = append(m.memories, memory)
	return memory, nil
}
func (m *fakeMemoryStore) Remove(context.Context, string) bool { return true }
func (m *fakeMemoryStore) Search(context.Context, string) ([]contracts.Memory, error) {
	return nil, nil
}

// extractionProvider returns the scripted model output as one stream.
type extractionProvider struct {
	chunks   []string
	err      error
	requests int
}

func (p *extractionProvider) Name() string { return "extract" }
func (p *extractionProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.requests++
	if p.err != nil {
		return nil, p.err
	}
	chunks := make([]rig.Chunk, 0, len(p.chunks)+1)
	for _, delta := range p.chunks {
		chunks = append(chunks, rig.Chunk{Delta: delta})
	}
	chunks = append(chunks, rig.Chunk{FinishReason: "stop"})
	return &scriptedStream{chunks: chunks}, nil
}

func newExtractService(t *testing.T, settings *fakeSettings, store *fakeMemoryStore, provider *extractionProvider) (*service, string) {
	t.Helper()
	svc := newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.memoryStore = store
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	svc.sessions[sessionID].messages = []contracts.ChatMessage{
		{ID: "m1", Role: contracts.ChatRoleUser, Text: "第一问"},
		{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "第一答"},
		{ID: "m3", Role: contracts.ChatRoleUser, Text: "第二问"},
		{ID: "m4", Role: contracts.ChatRoleAssistant, Text: "第二答"},
	}
	svc.mu.Unlock()
	return svc, sessionID
}

func enabledExtractSettings() *fakeSettings {
	return newFakeSettings(map[string]string{contracts.SettingMemoryExtractEnabled: "true"})
}

func TestMemoryExtractDisabledByDefault(t *testing.T) {
	store := &fakeMemoryStore{}
	svc, sessionID := newExtractService(t, newFakeSettings(nil), store, &extractionProvider{})
	if _, err := svc.ExtractFromSession(context.Background(), sessionID, 0); err == nil {
		t.Fatal("ExtractFromSession without the setting = nil error")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.added) != 0 {
		t.Fatalf("disabled extraction added %v", store.added)
	}
}

func TestMemoryExtractRejectsMissingSessionAndShortConversation(t *testing.T) {
	store := &fakeMemoryStore{}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, &extractionProvider{})
	if _, err := svc.ExtractFromSession(context.Background(), "missing", 0); err == nil {
		t.Fatal("missing session accepted")
	}
	svc.mu.Lock()
	svc.sessions[sessionID].messages = svc.sessions[sessionID].messages[:3]
	svc.mu.Unlock()
	if _, err := svc.ExtractFromSession(context.Background(), sessionID, 0); err == nil {
		t.Fatal("short conversation accepted")
	}
}

func TestMemoryExtractSuccessAndDedup(t *testing.T) {
	store := &fakeMemoryStore{memories: []contracts.Memory{{ID: "old", Text: "  Existing   MEMORY "}}}
	provider := &extractionProvider{chunks: []string{"```json\n{\"memories\":[\" User likes Go \",\"existing memory\",\"New fact\",\"Another fact\"]}\n```"}}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, provider)
	added, err := svc.ExtractFromSession(context.Background(), sessionID, 2)
	if err != nil {
		t.Fatalf("ExtractFromSession: %v", err)
	}
	if len(added) != 2 || added[0].Text != "User likes Go" || added[1].Text != "New fact" {
		t.Fatalf("added = %+v", added)
	}
	if len(store.added) != 2 || store.added[0] != "User likes Go" || store.added[1] != "New fact" {
		t.Fatalf("store adds = %v", store.added)
	}
	if len(store.scopes) != 2 || store.scopes[0] != "" || store.scopes[1] != "" {
		t.Fatalf("Add scopes = %v, want empty scopes so the durable store applies Config.Scope (FR-25/M-07)", store.scopes)
	}
	if added[0].Scope != "global" {
		t.Fatalf("scope = %q, want the store's default", added[0].Scope)
	}
}

func TestMemoryExtractBadJSONAndEmpty(t *testing.T) {
	svg, sessionID := newExtractService(t, enabledExtractSettings(), &fakeMemoryStore{}, &extractionProvider{chunks: []string{"not json"}})
	if _, err := svg.ExtractFromSession(context.Background(), sessionID, 0); err == nil {
		t.Fatal("bad JSON accepted")
	}
	empty, emptySession := newExtractService(t, enabledExtractSettings(), &fakeMemoryStore{}, &extractionProvider{chunks: []string{`{"memories":[]}`}})
	added, err := empty.ExtractFromSession(context.Background(), emptySession, 0)
	if err != nil || len(added) != 0 {
		t.Fatalf("empty extraction = %+v, %v", added, err)
	}
}

func TestMemoryExtractBusy(t *testing.T) {
	store := &fakeMemoryStore{}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, &extractionProvider{chunks: []string{`{"memories":["x"]}`}})
	svc.extracting.Store(true)
	defer svc.extracting.Store(false)
	if _, err := svc.ExtractFromSession(context.Background(), sessionID, 0); err == nil || !strings.Contains(err.Error(), "busy") && !strings.Contains(err.Error(), "running") {
		t.Fatalf("busy extraction = %v", err)
	}
}

func TestMemoryExtractAddFailure(t *testing.T) {
	store := &fakeMemoryStore{addErr: errors.New("disk full")}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, &extractionProvider{chunks: []string{`{"memories":["x"]}`}})
	added, err := svc.ExtractFromSession(context.Background(), sessionID, 0)
	if err == nil || len(added) != 0 {
		t.Fatalf("add failure = %+v, %v", added, err)
	}
}

func TestMemoryExtractMaxCap(t *testing.T) {
	items := `{"memories":["1","2","3","4","5","6","7"]}`
	store := &fakeMemoryStore{}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, &extractionProvider{chunks: []string{items}})
	added, err := svc.ExtractFromSession(context.Background(), sessionID, 99)
	if err != nil || len(added) != 5 {
		t.Fatalf("capped extraction = %d, %v", len(added), err)
	}
}

// TestMemoryExtractDropsSecretsAndTruncatesCandidates covers M-07: candidates
// are hard-validated (secrets dropped, text bounded) instead of trusting the
// model to follow the prompt.
func TestMemoryExtractDropsSecretsAndTruncatesCandidates(t *testing.T) {
	long := strings.Repeat("长", 300)
	payload := `{"memories":["password= hunter2","sk-live-abcdefghijklmnop","normal preference",%q]}`
	store := &fakeMemoryStore{}
	provider := &extractionProvider{chunks: []string{strings.ReplaceAll(payload, "%q", `"`+long+`"`)}}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, provider)
	added, err := svc.ExtractFromSession(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("ExtractFromSession: %v", err)
	}
	if len(added) != 2 {
		t.Fatalf("added = %d entries (%v), want the normal one and the truncated one", len(added), store.added)
	}
	if added[0].Text != "normal preference" {
		t.Fatalf("first kept candidate = %q", added[0].Text)
	}
	if runes := []rune(added[1].Text); len(runes) != 200 {
		t.Fatalf("long candidate runes = %d, want 200", len(runes))
	}
	for _, text := range store.added {
		if strings.Contains(text, "hunter2") || strings.Contains(text, "sk-live") {
			t.Fatalf("secret survived extraction: %q", text)
		}
	}
}

// TestMemoryExtractPartialAddFailureKeepsWrittenEntries covers M-06: entries
// written before the failure are returned together with the error instead of
// being collapsed into "nothing worked".
func TestMemoryExtractPartialAddFailureKeepsWrittenEntries(t *testing.T) {
	store := &fakeMemoryStore{failAfter: 1}
	provider := &extractionProvider{chunks: []string{`{"memories":["first fact","second fact"]}`}}
	svc, sessionID := newExtractService(t, enabledExtractSettings(), store, provider)
	added, err := svc.ExtractFromSession(context.Background(), sessionID, 5)
	if err == nil {
		t.Fatal("partial failure returned no error")
	}
	if len(added) != 1 || added[0].Text != "first fact" {
		t.Fatalf("added = %+v, want the one persisted entry returned with the error", added)
	}
}
