package agent

// Tests for the durable child-session store: one stable identity per delegated
// child (teammate or subagent) that survives the app restart, persists the
// roster lifecycle, restores historical rows with the same IDs and migrates the
// v1 history document.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// bootAgentModuleAt activates the agent module against fake host services with
// one explicit data directory, so a test can dispose it and boot again to
// simulate reopening the app.
func bootAgentModuleAt(t *testing.T, dataDir string) (*service, contracts.TeamService, contracts.ChildAgentService, cordis.Context) {
	t.Helper()
	ctx := cordis.Root()
	provide(t, ctx, contracts.KeyToolkit, rigtool.NewRegistry())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(newFakeSettings(nil)))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(&fakeLogger{}))
	provide(t, ctx, contracts.KeyStore, contracts.Store(newFakeStore()))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	provide(t, ctx, contracts.KeyDataDir, dataDir)
	if err := Module().Factory(ctx, Config{}); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	chat, err := ctx.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require KeyChat: %v", err)
	}
	svc, ok := chat.(*service)
	if !ok {
		t.Fatalf("KeyChat = %T, want *service", chat)
	}
	team, err := ctx.Require(contracts.KeyTeam)
	if err != nil {
		t.Fatalf("require KeyTeam: %v", err)
	}
	children, err := ctx.Require(contracts.KeyChildAgents)
	if err != nil {
		t.Fatalf("require KeyChildAgents: %v", err)
	}
	return svc, team, children, ctx
}

// findMemberByName returns one member snapshot by display name.
func findMemberByName(members []contracts.TeamMember, name string) (contracts.TeamMember, bool) {
	for _, member := range members {
		if member.Name == name {
			return member, true
		}
	}
	return contracts.TeamMember{}, false
}

// findChildByName returns one child agent by display name.
func findChildByName(agents []contracts.ChildAgent, name string) (contracts.ChildAgent, bool) {
	for _, agent := range agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return contracts.ChildAgent{}, false
}

// countLiveMembers counts the manager-backed members of a snapshot.
func countLiveMembers(members []contracts.TeamMember) int {
	live := 0
	for _, member := range members {
		if !member.Historical {
			live++
		}
	}
	return live
}

// TestChildSessionsUnifiedIdentityAcrossRestart is the product test: a teammate
// and a synchronous subagent run, the app disposes, and a second boot with the
// same data directory lists both with the SAME child-session IDs while the team
// row and the child-agent row share one identity.
func TestChildSessionsUnifiedIdentityAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	provider := &captureProvider{}

	svc, team, children, ctx := bootAgentModuleAt(t, dataDir)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	member, err := team.Spawn(context.Background(), "历史研究员", "研究当前工作目录", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := team.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !strings.HasPrefix(member.ID, childSessionIDPrefix) {
		t.Fatalf("spawned member ID = %q, want a child-session ID", member.ID)
	}
	teammate, ok := findChildByName(children.ChildAgents(session), "历史研究员")
	if !ok {
		t.Fatalf("live teammate missing from ChildAgents: %+v", children.ChildAgents(session))
	}
	if teammate.ID != member.ID {
		t.Fatalf("team member ID %q != child-agent ID %q: identity is not unified", member.ID, teammate.ID)
	}
	activity := svc.beginSubagentActivity("历史子代理", session, "model-x", "汇总一下", false)
	svc.finishSubagentActivity(activity.ID, context.Background(), nil,
		contracts.SubagentResult{Text: "子代理的答案"})
	subLive, ok := findChildByName(children.ChildAgents(session), "历史子代理")
	if !ok || !strings.HasPrefix(subLive.ID, childSessionIDPrefix) {
		t.Fatalf("live subagent = %+v, want a child-session ID", subLive)
	}
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first boot: %v", err)
	}

	// Reopen: same data directory, fresh process state, same child-session IDs.
	_, team2, children2, ctx2 := bootAgentModuleAt(t, dataDir)
	defer func() { _ = ctx2.DisposeAll() }()
	members := team2.Members()
	restored, ok := findMemberByName(members, "历史研究员")
	if !ok {
		t.Fatalf("history member missing after restart: %+v", members)
	}
	if !restored.Historical || restored.ID != member.ID || restored.SessionID != session {
		t.Fatalf("restored member = %+v, want the same child-session ID %q", restored, member.ID)
	}
	restoredAgents := children2.ChildAgents(session)
	teammate2, ok := findChildByName(restoredAgents, "历史研究员")
	if !ok || !teammate2.Historical || teammate2.ID != member.ID {
		t.Fatalf("restored teammate = %+v, want Historical with ID %q", teammate2, member.ID)
	}
	subagent2, ok := findChildByName(restoredAgents, "历史子代理")
	if !ok || !subagent2.Historical || subagent2.ID != subLive.ID {
		t.Fatalf("restored subagent = %+v, want Historical with ID %q", subagent2, subLive.ID)
	}
	if foreign := children2.ChildAgents("another-session"); len(foreign) != 0 {
		t.Fatalf("history leaked into another session: %+v", foreign)
	}
	found, ok := children2.ChildAgent(teammate2.ID)
	if !ok || found.ID != member.ID {
		t.Fatalf("ChildAgent(%q) = %+v, %v", teammate2.ID, found, ok)
	}
	if _, err := children2.SendChildAgent(context.Background(), teammate2.ID, "继续"); err == nil ||
		!strings.Contains(err.Error(), "historical") {
		t.Fatalf("SendChildAgent(historical) error = %v, want a read-only refusal", err)
	}
}

// TestCloseKeepsChildIdentity pins the single-identity rule for the close path:
// the row that moves from live to 已结束 keeps the same child-session ID.
func TestCloseKeepsChildIdentity(t *testing.T) {
	svc, team, _, ctx := bootAgentModuleAt(t, t.TempDir())
	defer func() { _ = ctx.DisposeAll() }()
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	member, err := team.Spawn(context.Background(), "收尾队友", "收尾", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := team.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	childID := member.ID
	if live, ok := findMemberByName(team.Members(), "收尾队友"); !ok || live.RunState != contracts.TeamRunDone {
		t.Fatalf("live member before close = %+v (ok=%v), want done", live, ok)
	}
	if err := team.Close(childID); err != nil {
		t.Fatalf("Close(%q): %v", childID, err)
	}
	members := team.Members()
	if countLiveMembers(members) != 0 {
		t.Fatalf("live members after close = %+v", members)
	}
	restored, ok := findMemberByName(members, "收尾队友")
	if !ok {
		t.Fatalf("closed member missing from the historical rows: %+v", members)
	}
	if !restored.Historical || restored.ID != childID {
		t.Fatalf("closed member = %+v, want the same child-session ID %q", restored, childID)
	}
	if restored.RunState != contracts.TeamRunDone {
		t.Fatalf("closed member RunState = %q, want done (the finished run's outcome)", restored.RunState)
	}
}

// TestChildSessionStoreRoundTrip pins the document: a second store on the same
// directory reads the records, resolves the backend reference and keeps the ID.
func TestChildSessionStoreRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	store := newChildSessionStore(dataDir, &fakeLogger{})
	record := childSessionRecord{
		ID:              store.newID(),
		ParentSessionID: "s1",
		BackendRef:      "member-1",
		Kind:            contracts.ChildAgentTeammate,
		Name:            "研究员",
		Model:           "m",
		Prompt:          "dig",
		Status:          contracts.ChildAgentCompleted,
		Output:          "answer",
		Started:         time.Now().Add(-time.Minute),
		Finished:        time.Now(),
		Activity:        []childSessionActivity{{ID: "a1", Kind: contracts.TeamActivityFinish, Text: "完成"}},
	}
	if !store.upsert(record) {
		t.Fatal("upsert reported no change for a new record")
	}
	if _, err := os.Stat(filepath.Join(dataDir, childSessionFileName)); err != nil {
		t.Fatalf("child-session document not written: %v", err)
	}

	reloaded := newChildSessionStore(dataDir, &fakeLogger{})
	records := reloaded.records()
	if len(records) != 1 || records[0].ID != record.ID || records[0].Name != "研究员" {
		t.Fatalf("reloaded records = %+v", records)
	}
	if byRef, ok := reloaded.byBackendRef("member-1", "s1", contracts.ChildAgentTeammate); !ok || byRef.ID != record.ID {
		t.Fatalf("byBackendRef = %+v, %v", byRef, ok)
	}
	if byID, ok := reloaded.record(record.ID); !ok || byID.Output != "answer" {
		t.Fatalf("record(id) = %+v, %v", byID, ok)
	}
	// Re-archiving keeps the ID and updates in place.
	record.Output = "second answer"
	reloaded.upsert(record)
	if records := reloaded.records(); len(records) != 1 || records[0].Output != "second answer" {
		t.Fatalf("records after re-upsert = %+v", records)
	}
	reloaded.clear()
	if records := reloaded.records(); len(records) != 0 {
		t.Fatalf("records after clear = %+v", records)
	}
}

// TestChildSessionStoreMigratesV1 pins the migration from the first durable
// release's child-agents.json: records survive under the new schema with IDs.
func TestChildSessionStoreMigratesV1(t *testing.T) {
	dataDir := t.TempDir()
	legacy := legacyDocument{
		Version: childSessionLegacyVersion,
		Members: []legacyHistoryMember{{
			OriginalID: "member-1", SessionID: "s1", Name: "旧队友", RunState: contracts.TeamRunDone,
			Last: "answer", Started: time.Now().Add(-time.Hour), Finished: time.Now(),
		}},
		Subagents: []legacyHistorySubagent{{
			OriginalID: "subagent-1", SessionID: "s1", Name: "旧子代理",
			Status: contracts.SubagentStatusDone, Detail: "done", Started: time.Now().Add(-time.Hour),
		}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("encode legacy document: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, childSessionLegacyFileName), data, 0o644); err != nil {
		t.Fatalf("write legacy document: %v", err)
	}
	store := newChildSessionStore(dataDir, &fakeLogger{})
	records := store.records()
	if len(records) != 2 {
		t.Fatalf("migrated records = %+v, want two", records)
	}
	for _, record := range records {
		if !strings.HasPrefix(record.ID, childSessionIDPrefix) {
			t.Fatalf("migrated record ID = %q, want a child-session ID", record.ID)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, childSessionLegacyFileName) + ".migrated"); err != nil {
		t.Fatalf("legacy document was not moved aside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, childSessionFileName)); err != nil {
		t.Fatalf("migrated document not written: %v", err)
	}
}

// TestChildSessionStoreQuarantinesCorruptDocument pins the resilience rule.
func TestChildSessionStoreQuarantinesCorruptDocument(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, childSessionFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt document: %v", err)
	}
	store := newChildSessionStore(dataDir, &fakeLogger{})
	if records := store.records(); len(records) != 0 {
		t.Fatalf("corrupt document loaded records: %+v", records)
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("corrupt document not quarantined: %v %v", matches, err)
	}
}

// TestChildSessionStorePruneBounds pins the retention bounds: an old document
// loads bounded and stale records are dropped.
func TestChildSessionStorePruneBounds(t *testing.T) {
	dataDir := t.TempDir()
	now := time.Now()
	records := make([]childSessionRecord, 0, childSessionKeep+5)
	for index := 0; index < childSessionKeep+5; index++ {
		records = append(records, childSessionRecord{
			ID:      "child-" + string(rune('a'+index%26)) + string(rune('a'+index/26)),
			Kind:    contracts.ChildAgentTeammate,
			Status:  contracts.ChildAgentCompleted,
			Updated: now.Add(time.Duration(index) * time.Minute),
		})
	}
	records = append(records, childSessionRecord{
		ID: "child-stale", Kind: contracts.ChildAgentTeammate,
		Status: contracts.ChildAgentCompleted, Updated: now.Add(-childSessionTTL - time.Hour),
	})
	document := childSessionFile{Version: childSessionVersion, Children: records}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, childSessionFileName), data, 0o644); err != nil {
		t.Fatalf("write document: %v", err)
	}
	store := newChildSessionStore(dataDir, &fakeLogger{})
	got := store.records()
	if len(got) != childSessionKeep {
		t.Fatalf("pruned records = %d, want %d", len(got), childSessionKeep)
	}
	for _, record := range got {
		if record.ID == "child-stale" {
			t.Fatal("stale record survived the TTL prune")
		}
	}
}
