package agent

// AR-05 / AR-20 / (a) (2026-11 audit): the session screen used to deep-copy the
// whole conversation (attachment bytes included) 7–8 times per frame, and the
// sidebar list re-read four settings rows per session plus a full sort on every
// frame. Session() now returns a shared read-only snapshot cached per state
// version; Sessions() caches the sorted list skeleton (invalidated on any
// session/flag change) and fills the permission-disclosure fields from cheap
// global reads. These tests pin the cache identity, every invalidation path
// and the disclosure correctness.

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// countingSettings counts Get calls so a cached read path can be proven to
// stop touching the per-session flag rows.
type countingSettings struct {
	*fakeSettings
	mu   sync.Mutex
	gets int
}

func (s *countingSettings) Get(key, fallback string) string {
	s.mu.Lock()
	s.gets++
	s.mu.Unlock()
	return s.fakeSettings.Get(key, fallback)
}

func (s *countingSettings) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

// seedRichSession installs one assistant message with tools, reasoning
// segments and a byte attachment directly into the read model (the shape a
// real run produces) without starting a provider.
func seedRichSession(t *testing.T, svc *service, id string) {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID:        "m-rich",
		Role:      contracts.ChatRoleAssistant,
		Text:      "hello",
		Reasoning: "think",
		ReasoningSegments: []contracts.ReasoningSegment{
			{Text: "think", Iteration: 1, TextOffset: 0},
		},
		Tools: []contracts.ToolCall{{ID: "t1", Name: "echo", Status: "ok", TextOffset: 5}},
		Attachments: []contracts.Attachment{
			{ID: "a1", Name: "pic.png", MIME: "image/png", Data: []byte("attachment-bytes")},
		},
		CreatedAt: time.Now(),
	})
	state.assistant = len(state.messages) - 1
	state.updatedAt = time.Now()
}

func slicePointer(value any) uintptr {
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Slice || v.Len() == 0 {
		return 0
	}
	return v.Pointer()
}

func TestSessionSnapshotSharedBetweenCalls(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	id, err := svc.NewSession("cache")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedRichSession(t, svc, id)

	first, ok := svc.Session(id)
	if !ok {
		t.Fatal("Session missing")
	}
	for i := 0; i < 7; i++ {
		again, _ := svc.Session(id)
		if slicePointer(again.Messages) != slicePointer(first.Messages) {
			t.Fatalf("call %d: Messages backing array changed without a mutation (AR-05: every call deep-copied)", i)
		}
		if slicePointer(again.Messages[0].Tools) != slicePointer(first.Messages[0].Tools) {
			t.Fatalf("call %d: Tools backing array changed without a mutation", i)
		}
		if slicePointer(again.Messages[0].ReasoningSegments) != slicePointer(first.Messages[0].ReasoningSegments) {
			t.Fatalf("call %d: ReasoningSegments backing array changed without a mutation", i)
		}
		if slicePointer(again.Messages[0].Attachments) != slicePointer(first.Messages[0].Attachments) {
			t.Fatalf("call %d: Attachments backing array changed without a mutation", i)
		}
		if len(again.Messages[0].Attachments) > 0 &&
			slicePointer(again.Messages[0].Attachments[0].Data) != slicePointer(first.Messages[0].Attachments[0].Data) {
			t.Fatalf("call %d: attachment bytes were re-copied", i)
		}
		if !reflect.DeepEqual(again, first) {
			t.Fatalf("call %d: snapshot content drifted: %+v vs %+v", i, again, first)
		}
	}
}

func TestSessionSnapshotInvalidatedByMutations(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newQueueService(t, nil, settings, &captureProvider{})
	id, err := svc.NewSession("invalidate")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedRichSession(t, svc, id)
	base, _ := svc.Session(id)

	// A streaming text delta changes the message content.
	svc.handleEvent(context.Background(), id, "", rig.Event{Type: rig.EventTextDelta, Iteration: 1, Text: " more"}, nil)
	afterDelta, _ := svc.Session(id)
	if afterDelta.Messages[0].Text != "hello more" {
		t.Fatalf("stale snapshot after text delta: %q", afterDelta.Messages[0].Text)
	}
	if slicePointer(afterDelta.Messages) == slicePointer(base.Messages) {
		t.Fatal("snapshot not rebuilt after a text delta")
	}

	// A tool call appends to the tool list.
	call := &rig.ToolCall{ID: "t2", Name: "echo", Arguments: `{}`}
	svc.handleEvent(context.Background(), id, "", rig.Event{Type: rig.EventToolCall, Iteration: 1, Call: call}, nil)
	afterTool, _ := svc.Session(id)
	if len(afterTool.Messages[0].Tools) != 2 {
		t.Fatalf("stale snapshot after tool call: %+v", afterTool.Messages[0].Tools)
	}

	// A rename changes the title.
	if err := svc.RenameSession(id, "renamed"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	afterRename, _ := svc.Session(id)
	if afterRename.Title != "renamed" {
		t.Fatalf("stale snapshot after rename: %q", afterRename.Title)
	}

	// A full run (Send → finish) changes messages and running state.
	beforeRun, _ := svc.Session(id)
	if err := svc.Send(id, "run please"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	afterSend, _ := svc.Session(id)
	if !afterSend.Running || len(afterSend.Messages) != len(beforeRun.Messages)+2 {
		t.Fatalf("stale snapshot after Send: running=%v messages=%d", afterSend.Running, len(afterSend.Messages))
	}
	finished := waitIdle(t, svc, id)
	if finished.Running {
		t.Fatal("run did not finish")
	}
	last := finished.Messages[len(finished.Messages)-1]
	if last.Streaming || last.Text != "ok" {
		t.Fatalf("stale snapshot after finish: %+v", last)
	}
}

// TestSessionSnapshotConcurrentReads drives mutations and reads from separate
// goroutines: the cache must be race-free (-race) and never return a torn
// snapshot.
func TestSessionSnapshotConcurrentReads(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	id, err := svc.NewSession("concurrent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedRichSession(t, svc, id)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			svc.handleEvent(context.Background(), id, "", rig.Event{
				Type: rig.EventTextDelta, Iteration: 1, Text: fmt.Sprintf("-%d", i),
			}, nil)
		}
		close(stop)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			session, ok := svc.Session(id)
			if !ok || len(session.Messages) == 0 {
				t.Error("Session lost the seeded message")
				return
			}
		}
	}()
	wg.Wait()
	final, _ := svc.Session(id)
	if want := 200; countDeltas(final.Messages[0].Text) != want {
		t.Fatalf("final text carries %d deltas, want %d", countDeltas(final.Messages[0].Text), want)
	}
}

func countDeltas(text string) int {
	count := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '-' {
			count++
		}
	}
	return count
}

// --- AR-20: the session list cache -----------------------------------------

func TestSessionsListCacheAvoidsPerSessionSettingsReads(t *testing.T) {
	settings := &countingSettings{fakeSettings: newFakeSettings(nil)}
	svc := newService(Config{}, newRegistryForTest(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	for i := 0; i < 3; i++ {
		if _, err := svc.NewSession(fmt.Sprintf("s%d", i)); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
	}
	first := svc.Sessions()
	if len(first) != 3 {
		t.Fatalf("sessions = %d, want 3", len(first))
	}
	afterFirst := settings.count()
	// The first build reads the four flag rows per session (pin/archive/unread/
	// origin); the cached rebuild must not touch them again.
	for i := 0; i < 5; i++ {
		again := svc.Sessions()
		if !reflect.DeepEqual(again, first) {
			t.Fatalf("cached call %d drifted: %+v vs %+v", i, again, first)
		}
	}
	added := settings.count() - afterFirst
	// Each cached call may spend the two cheap GLOBAL disclosure reads; the
	// 4×3 per-session flag rows must never be re-read without a change.
	if added > 2*5 {
		t.Fatalf("cached Sessions() performed %d settings reads across 5 calls, want ≤2 global reads/call (AR-20: 4 reads/session/frame)", added)
	}
}

func TestSessionsListCacheInvalidation(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newService(Config{}, newRegistryForTest(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	id, err := svc.NewSession("flags")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := svc.Sessions()[0]; got.Pinned || got.Archived || got.Unread {
		t.Fatalf("fresh session flags = %+v, want all false", got)
	}
	if err := svc.SetSessionPinned(id, true); err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}
	if got := svc.Sessions()[0]; !got.Pinned {
		t.Fatal("pin change not visible after SetSessionPinned (stale list cache)")
	}
	if err := svc.SetSessionArchived(id, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}
	if got := svc.Sessions()[0]; !got.Archived {
		t.Fatal("archive change not visible after SetSessionArchived")
	}
	svc.setSessionUnread(id, true)
	svc.notify(id)
	if got := svc.Sessions()[0]; !got.Unread {
		t.Fatal("unread change not visible after the completion mark")
	}
	if err := svc.RenameSession(id, "renamed"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if got := svc.Sessions()[0]; got.Title != "renamed" {
		t.Fatalf("rename not visible: %q", got.Title)
	}
	other, err := svc.NewSession("second")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := svc.Sessions(); len(got) != 2 {
		t.Fatalf("new session not listed: %d", len(got))
	}
	if err := svc.DeleteSession(other); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := svc.Sessions(); len(got) != 1 {
		t.Fatalf("deleted session still listed: %d", len(got))
	}
}

// TestSessionsListCorrectnessControl compares the cached list against an
// independently computed oracle (flags straight from settings, ordering by
// UpdatedAt desc / ID desc) before and after mutations.
func TestSessionsListCorrectnessControl(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newService(Config{}, newRegistryForTest(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		id, err := svc.NewSession(fmt.Sprintf("session-%d", i))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		ids = append(ids, id)
	}
	// Distinct update clocks, one pin, one archive, one unread, one origin.
	svc.mu.Lock()
	for i, id := range ids {
		svc.sessions[id].updatedAt = time.Unix(int64(1700000000+i), 0)
	}
	svc.mu.Unlock()
	if err := svc.SetSessionPinned(ids[0], true); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := svc.SetSessionArchived(ids[1], true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := settings.Set(contracts.SessionUnreadSettingKey(ids[2]), "true"); err != nil {
		t.Fatalf("unread row: %v", err)
	}
	if err := settings.Set(contracts.SessionOriginSettingKey(ids[2]), contracts.SessionOriginAutomation); err != nil {
		t.Fatalf("origin row: %v", err)
	}
	svc.notify(ids[2])

	oracle := func() []contracts.ChatSessionInfo {
		svc.mu.Lock()
		out := make([]contracts.ChatSessionInfo, 0, len(svc.sessions))
		for _, state := range svc.sessions {
			out = append(out, contracts.ChatSessionInfo{
				ID: state.id, WorkspaceID: state.workspaceID, Title: state.title,
				MessageCount: len(state.messages), Running: state.running, Waiting: state.waiting,
				UpdatedAt: state.updatedAt,
			})
		}
		svc.mu.Unlock()
		for i := range out {
			out[i].Pinned = settings.Get(contracts.SessionPinnedSettingKey(out[i].ID), "") == "true"
			out[i].Archived = settings.Get(contracts.SessionArchivedSettingKey(out[i].ID), "") == "true"
			out[i].Unread = settings.Get(contracts.SessionUnreadSettingKey(out[i].ID), "") == "true"
			out[i].Origin = settings.Get(contracts.SessionOriginSettingKey(out[i].ID), "")
		}
		// Same ordering rule as the service: UpdatedAt desc, ID desc.
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				swap := false
				if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
					swap = out[i].UpdatedAt.Before(out[j].UpdatedAt)
				} else {
					swap = out[i].ID < out[j].ID
				}
				if swap {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}
	compare := func(label string) {
		t.Helper()
		got, want := svc.Sessions(), oracle()
		if len(got) != len(want) {
			t.Fatalf("%s: len = %d, want %d", label, len(got), len(want))
		}
		for i := range want {
			g, w := got[i], want[i]
			if g.ID != w.ID || g.Title != w.Title || g.Pinned != w.Pinned || g.Archived != w.Archived ||
				g.Unread != w.Unread || g.Origin != w.Origin || g.MessageCount != w.MessageCount ||
				g.Running != w.Running || g.Waiting != w.Waiting || !g.UpdatedAt.Equal(w.UpdatedAt) {
				t.Fatalf("%s: row %d = %+v, want %+v", label, i, g, w)
			}
		}
	}
	compare("initial")
	compare("cached") // second call hits the cache
	if err := svc.SetSessionPinned(ids[2], true); err != nil {
		t.Fatalf("pin: %v", err)
	}
	compare("after pin change")
	svc.mu.Lock()
	svc.sessions[ids[0]].updatedAt = time.Unix(1700000999, 0)
	svc.mu.Unlock()
	svc.notify(ids[0])
	compare("after reorder")
}

// --- (a): permission disclosure on the list read model ----------------------

// settingsApprovals mimics the desktop approvals service: Mode() follows the
// stored agent.permission preset (the real service also honors the headless
// ignorePermissionSetting pin; the disclosure reads whatever Mode reports).
type settingsApprovals struct {
	fakeApprovals
	settings *fakeSettings
}

func (a settingsApprovals) Mode() string {
	return contracts.NormalizePermissionMode(a.settings.Get(contracts.SettingAgentPermission, ""))
}

func TestSessionsPermissionDisclosure(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	svc := newService(Config{}, newRegistryForTest(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), settingsApprovals{settings: settings})
	global, err := svc.NewSession("global")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	override, err := svc.NewSession("override")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	readonly, err := svc.NewSession("readonly")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(override, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	if err := svc.SetSessionPermission(readonly, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	byID := func() map[string]contracts.ChatSessionInfo {
		out := map[string]contracts.ChatSessionInfo{}
		for _, info := range svc.Sessions() {
			out[info.ID] = info
		}
		return out
	}
	infos := byID()
	if got := infos[global]; got.PermissionScope != contracts.PermissionScopeGlobal ||
		got.EffectiveApprovalMode != contracts.PermissionWorkspace || got.EffectiveSandboxMode != contracts.PermissionWorkspace {
		t.Fatalf("global session disclosure = %+v, want global/workspace/workspace", got)
	}
	// The core (a) fact: a session-level "full" override relaxes APPROVALS
	// only — the OS sandbox mode still follows the global preset.
	if got := infos[override]; got.PermissionScope != contracts.PermissionScopeSession ||
		got.EffectiveApprovalMode != contracts.PermissionFull || got.EffectiveSandboxMode != contracts.PermissionWorkspace {
		t.Fatalf("full-override disclosure = %+v, want session/full/workspace (OS mode stays global)", got)
	}
	// Reverse leak disclosure: a read-only session under a global full preset
	// still runs unconfined at the OS level.
	if err := settings.Set(contracts.SettingAgentPermission, contracts.PermissionFull); err != nil {
		t.Fatalf("set global permission: %v", err)
	}
	infos = byID()
	if got := infos[readonly]; got.EffectiveApprovalMode != contracts.PermissionReadOnly ||
		got.EffectiveSandboxMode != contracts.PermissionFull {
		t.Fatalf("reverse-leak disclosure = %+v, want read-only approval under full OS mode", got)
	}
	if got := infos[global]; got.EffectiveSandboxMode != contracts.PermissionFull ||
		got.EffectiveApprovalMode != contracts.PermissionFull {
		t.Fatalf("global full disclosure = %+v, want full/full (fresh global read, no stale cache)", got)
	}
	if got := infos[override]; got.EffectiveSandboxMode != contracts.PermissionFull {
		t.Fatalf("override OS mode = %q, want full (follows global)", got.EffectiveSandboxMode)
	}
}

// newRegistryForTest builds an empty rig registry (the list/disclosure tests
// never run tools).
func newRegistryForTest() *rigtools.Registry { return rigtools.NewRegistry() }
