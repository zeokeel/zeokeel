package agent

// Tests for the durable child-agent history: the finished teammates and
// synchronous subagents are written to one bounded JSON document and restored
// as read-only historical rows after a restart, so the session shell still
// lists the team and the subagents it ran.

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

// TestChildAgentsRestoredAfterRestart is the product test: a teammate and a
// synchronous subagent run, the app disposes (quit), and a second boot with the
// same data directory still lists both as historical, read-only child agents.
func TestChildAgentsRestoredAfterRestart(t *testing.T) {
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
	if members := team.Members(); len(liveTeamMembers(members)) != 1 {
		t.Fatalf("live members before restart = %+v", members)
	}
	// A live, finished member is listed once: the history row is deduped
	// against the live manager snapshot.
	if agents := children.ChildAgents(session); len(agents) != 1 {
		t.Fatalf("ChildAgents before restart = %+v, want the single live teammate", agents)
	}
	activity := svc.beginSubagentActivity("历史子代理", session, "model-x", "汇总一下", false)
	svc.finishSubagentActivity(activity.ID, context.Background(), nil,
		contracts.SubagentResult{Text: "子代理的答案"})
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first boot: %v", err)
	}

	// Reopen: same data directory, fresh process state.
	_, team2, children2, ctx2 := bootAgentModuleAt(t, dataDir)
	defer func() { _ = ctx2.DisposeAll() }()

	members := team2.Members()
	restored, ok := findMemberByName(members, "历史研究员")
	if !ok {
		t.Fatalf("history member missing after restart: %+v", members)
	}
	if !restored.Historical || restored.SessionID != session || restored.RunState != contracts.TeamRunDone ||
		strings.TrimSpace(restored.Last) != "ok" {
		t.Fatalf("restored member = %+v, want a done historical row scoped to %q", restored, session)
	}
	agents := children2.ChildAgents(session)
	teammate, ok := findChildByName(agents, "历史研究员")
	if !ok {
		t.Fatalf("restored teammate missing from ChildAgents: %+v", agents)
	}
	if !teammate.Historical || teammate.Kind != contracts.ChildAgentTeammate ||
		teammate.Status != contracts.ChildAgentCompleted || teammate.Output != "ok" {
		t.Fatalf("restored teammate = %+v", teammate)
	}
	subagent, ok := findChildByName(agents, "历史子代理")
	if !ok {
		t.Fatalf("restored subagent missing from ChildAgents: %+v", agents)
	}
	if !subagent.Historical || subagent.Kind != contracts.ChildAgentSubagent ||
		subagent.Status != contracts.ChildAgentCompleted || subagent.Output != "子代理的答案" {
		t.Fatalf("restored subagent = %+v", subagent)
	}
	// The history rows are scoped to the session that spawned them.
	if foreign := children2.ChildAgents("another-session"); len(foreign) != 0 {
		t.Fatalf("ChildAgents(other session) = %+v, want the history scoped away", foreign)
	}
	// Direct lookup and the read-only follow-up contract.
	found, ok := children2.ChildAgent(teammate.ID)
	if !ok || found.Name != "历史研究员" {
		t.Fatalf("ChildAgent(%q) = %+v, %v", teammate.ID, found, ok)
	}
	if _, err := children2.SendChildAgent(context.Background(), teammate.ID, "继续"); err == nil ||
		!strings.Contains(err.Error(), "historical") {
		t.Fatalf("SendChildAgent(historical) error = %v, want a read-only refusal", err)
	}
	// The restored rows do not carry the manager's availability word.
	if restored.Status == "active" {
		t.Fatalf("restored member Status = %q, want a terminal availability", restored.Status)
	}
}

// TestChildHistoryStoreRoundTrip pins the document: a second store on the same
// directory reads the archived teammate and subagent, and re-archiving the same
// backend child keeps one record with one stable row key.
func TestChildHistoryStoreRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	store := newChildHistoryStore(dataDir, &fakeLogger{})
	store.upsertMember(childHistoryMember{
		OriginalID: "member-1", SessionID: "s1", Name: "研究员", Model: "m",
		Prompt: "dig", RunState: contracts.TeamRunDone, Last: "answer",
		Started: time.Now().Add(-time.Minute), Finished: time.Now(),
		Activity: []childHistoryActivity{{ID: "a1", Kind: contracts.TeamActivityFinish, Text: "完成"}},
	})
	store.upsertSubagent(childHistorySubagent{
		OriginalID: "subagent-1", SessionID: "s1", Name: "子代理", Status: contracts.SubagentStatusDone,
		Detail: "done answer", Started: time.Now().Add(-time.Minute), Finished: time.Now(),
	})
	if _, err := os.Stat(filepath.Join(dataDir, childHistoryFileName)); err != nil {
		t.Fatalf("history document not written: %v", err)
	}

	reloaded := newChildHistoryStore(dataDir, &fakeLogger{})
	members := reloaded.memberRecords()
	if len(members) != 1 || members[0].Name != "研究员" || members[0].Key == "" {
		t.Fatalf("reloaded members = %+v", members)
	}
	firstKey := members[0].Key
	subagents := reloaded.subagentRecords()
	if len(subagents) != 1 || subagents[0].Name != "子代理" || subagents[0].Detail != "done answer" {
		t.Fatalf("reloaded subagents = %+v", subagents)
	}

	// Re-archiving the same member updates in place: one record, same key.
	reloaded.upsertMember(childHistoryMember{
		OriginalID: "member-1", SessionID: "s1", Name: "研究员", RunState: contracts.TeamRunDone,
		Last: "second answer", Started: time.Now().Add(-time.Minute), Finished: time.Now(),
	})
	members = reloaded.memberRecords()
	if len(members) != 1 {
		t.Fatalf("members after re-archive = %+v, want one record", members)
	}
	if members[0].Key != firstKey {
		t.Fatalf("row key changed on re-archive: %q -> %q", firstKey, members[0].Key)
	}
	if members[0].Last != "second answer" {
		t.Fatalf("re-archived answer = %q", members[0].Last)
	}
	// Persisted: a third store sees the update.
	third := newChildHistoryStore(dataDir, &fakeLogger{})
	if got := third.memberRecords(); len(got) != 1 || got[0].Last != "second answer" {
		t.Fatalf("persisted members = %+v", got)
	}
}

// TestChildHistoryStoreQuarantinesCorruptDocument pins the resilience rule: an
// unparsable document is moved aside and the module starts with empty history.
func TestChildHistoryStoreQuarantinesCorruptDocument(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, childHistoryFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt history: %v", err)
	}
	store := newChildHistoryStore(dataDir, &fakeLogger{})
	if records := store.memberRecords(); len(records) != 0 {
		t.Fatalf("corrupt document loaded records: %+v", records)
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("corrupt document not quarantined: %v %v", matches, err)
	}
}

// TestChildHistoryPruneBounds pins the retention bounds.
func TestChildHistoryPruneBounds(t *testing.T) {
	now := time.Now()
	members := make([]childHistoryMember, 0, childHistoryMemberKeep+5)
	for index := 0; index < childHistoryMemberKeep+5; index++ {
		members = append(members, childHistoryMember{
			OriginalID: "member-" + string(rune('a'+index)),
			Updated:    now.Add(time.Duration(index) * time.Minute),
		})
	}
	pruned := pruneHistoryMembers(members, now.Add(-childHistoryTTL), childHistoryMemberKeep)
	if len(pruned) != childHistoryMemberKeep {
		t.Fatalf("pruned members = %d, want %d", len(pruned), childHistoryMemberKeep)
	}
	if !pruned[0].Updated.After(pruned[len(pruned)-1].Updated) {
		t.Fatal("pruned members are not ordered newest first")
	}
	stale := []childHistoryMember{{OriginalID: "member-old", Updated: now.Add(-childHistoryTTL - time.Hour)}}
	if got := pruneHistoryMembers(stale, now.Add(-childHistoryTTL), childHistoryMemberKeep); len(got) != 0 {
		t.Fatalf("stale member survived the TTL: %+v", got)
	}
	subagents := make([]childHistorySubagent, 0, childHistorySubagentKeep+3)
	for index := 0; index < childHistorySubagentKeep+3; index++ {
		subagents = append(subagents, childHistorySubagent{
			OriginalID: "subagent-" + string(rune('a'+index)),
			Updated:    now.Add(time.Duration(index) * time.Minute),
		})
	}
	if got := pruneHistorySubagents(subagents, now.Add(-childHistoryTTL), childHistorySubagentKeep); len(got) != childHistorySubagentKeep {
		t.Fatalf("pruned subagents = %d, want %d", len(got), childHistorySubagentKeep)
	}
}

// TestChildHistoryDocumentShape pins the persisted JSON shape (the version
// field guards future migrations).
func TestChildHistoryDocumentShape(t *testing.T) {
	dataDir := t.TempDir()
	store := newChildHistoryStore(dataDir, &fakeLogger{})
	store.upsertSubagent(childHistorySubagent{OriginalID: "subagent-1", Name: "子代理", Status: contracts.SubagentStatusDone})
	data, err := os.ReadFile(filepath.Join(dataDir, childHistoryFileName))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var document childHistoryFile
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if document.Version != childHistoryVersion {
		t.Fatalf("document version = %d, want %d", document.Version, childHistoryVersion)
	}
}
