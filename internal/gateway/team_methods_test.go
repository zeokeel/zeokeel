package gateway

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The browser's team screen had no transport at all: contracts.KeyTeam was provided
// by the agent module, but the gateway exposed no team.* method, so the screen could
// only render its empty state. These pin the seven methods' behaviour.

type fakeTeam struct {
	members     []contracts.TeamMember
	activities  []contracts.TeamActivity
	spawned     []string
	sentAsync   []string
	sentSync    []string
	waited      []string
	interrupted []string
	closed      []string
}

func (f *fakeTeam) Members() []contracts.TeamMember { return f.members }

func (f *fakeTeam) Spawn(_ context.Context, name, prompt, model string) (contracts.TeamMember, error) {
	f.spawned = append(f.spawned, name+"|"+prompt+"|"+model)
	member := contracts.TeamMember{ID: "member-1", Name: name, Status: "active", RunState: "idle", Model: model, Created: time.Now()}
	f.members = append(f.members, member)
	return member, nil
}

func (f *fakeTeam) SendAsync(id, message string) error {
	f.sentAsync = append(f.sentAsync, id+"|"+message)
	return nil
}

func (f *fakeTeam) Send(_ context.Context, id, message string) (string, error) {
	f.sentSync = append(f.sentSync, id+"|"+message)
	return "the answer", nil
}

func (f *fakeTeam) Wait(_ context.Context, id string) (string, error) {
	f.waited = append(f.waited, id)
	return "waited answer", nil
}

func (f *fakeTeam) Interrupt(id string) error {
	f.interrupted = append(f.interrupted, id)
	return nil
}

func (f *fakeTeam) Close(id string) error {
	f.closed = append(f.closed, id)
	return nil
}

func (f *fakeTeam) TeamActivities() []contracts.TeamActivity { return f.activities }

func newTeamGateway(t *testing.T, team contracts.TeamService) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if team != nil {
		if _, err := root.Provide(contracts.KeyTeam, contracts.TeamService(team)); err != nil {
			t.Fatalf("provide team: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return gateway
}

func TestTeamSnapshotCarriesMembersAndActivity(t *testing.T) {
	created := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	team := &fakeTeam{
		members: []contracts.TeamMember{{
			ID: "member-1", Name: "研究员", Status: "active", RunState: "running",
			Model: "qwen3.8-flash-next", Created: created, Last: "上一次的回答",
		}},
		activities: []contracts.TeamActivity{{
			ID: "act-1", MemberID: "member-1", Kind: "spawn", Text: "派发研究员", Time: created,
		}},
	}
	gw := newTeamGateway(t, team)

	payload, problem := gw.teamSnapshot()
	if problem != nil {
		t.Fatalf("teamSnapshot: %v", problem)
	}
	body, _ := json.Marshal(payload)
	var decoded teamSnapshotDTO
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Members) != 1 || decoded.Members[0].Name != "研究员" ||
		decoded.Members[0].RunState != "running" || decoded.Members[0].Created != "2026-11-24T10:00:00Z" {
		t.Fatalf("member = %+v", decoded.Members)
	}
	if len(decoded.Activities) != 1 || decoded.Activities[0].Kind != "spawn" || decoded.Activities[0].At == "" {
		t.Fatalf("activity = %+v", decoded.Activities)
	}
}

func TestTeamSpawnSendWaitInterruptClose(t *testing.T) {
	team := &fakeTeam{}
	gw := newTeamGateway(t, team)

	if _, problem := gw.teamSpawn(context.Background(),
		json.RawMessage(`{"name":"研究员","prompt":"研究当前目录","model":"qwen3.8-flash-next"}`)); problem != nil {
		t.Fatalf("spawn: %v", problem)
	}
	if len(team.spawned) != 1 {
		t.Fatalf("spawn did not reach the service: %v", team.spawned)
	}

	// Async send queues; sync send waits for the answer.
	if _, problem := gw.teamSend(context.Background(),
		json.RawMessage(`{"id":"member-1","message":"继续","async":true}`)); problem != nil {
		t.Fatalf("send async: %v", problem)
	}
	if len(team.sentAsync) != 1 || len(team.sentSync) != 0 {
		t.Fatalf("async send misrouted: %v / %v", team.sentAsync, team.sentSync)
	}
	answer, problem := gw.teamSend(context.Background(),
		json.RawMessage(`{"id":"member-1","message":"同步问一句"}`))
	if problem != nil {
		t.Fatalf("send sync: %v", problem)
	}
	if answerMap, ok := answer.(map[string]any); !ok || answerMap["answer"] != "the answer" {
		t.Fatalf("sync send answer = %v", answer)
	}

	if _, problem := gw.teamWait(context.Background(), json.RawMessage(`{"id":"member-1"}`)); problem != nil {
		t.Fatalf("wait: %v", problem)
	}
	if _, problem := gw.teamInterrupt(json.RawMessage(`{"id":"member-1"}`)); problem != nil {
		t.Fatalf("interrupt: %v", problem)
	}
	if _, problem := gw.teamClose(json.RawMessage(`{"id":"member-1"}`)); problem != nil {
		t.Fatalf("close: %v", problem)
	}
	if len(team.waited) != 1 || len(team.interrupted) != 1 || len(team.closed) != 1 {
		t.Fatalf("calls: waited=%v interrupted=%v closed=%v", team.waited, team.interrupted, team.closed)
	}
}

func TestTeamMethodsValidateParams(t *testing.T) {
	gw := newTeamGateway(t, &fakeTeam{})

	if _, problem := gw.teamSpawn(context.Background(), json.RawMessage(`{"name":"只有名字"}`)); problem == nil {
		t.Fatal("spawn without a prompt must be refused")
	}
	if _, problem := gw.teamSend(context.Background(), json.RawMessage(`{"id":"member-1"}`)); problem == nil {
		t.Fatal("send without a message must be refused")
	}
	for _, call := range []func() *protocolError{
		func() *protocolError { _, p := gw.teamWait(context.Background(), json.RawMessage(`{}`)); return p },
		func() *protocolError { _, p := gw.teamInterrupt(json.RawMessage(`{}`)); return p },
		func() *protocolError { _, p := gw.teamClose(json.RawMessage(`{}`)); return p },
	} {
		if problem := call(); problem == nil {
			t.Fatal("a missing id must be refused")
		}
	}
}

// The handshake must advertise `team` when the backend exists: the client gates the
// whole screen on this capability, so a missing entry hides a working feature.
func TestTeamCapabilityIsAdvertisedWhenTheBackendExists(t *testing.T) {
	gw := newTeamGateway(t, &fakeTeam{})
	if !slices.Contains(gw.gatewayCapabilities(), "team") {
		t.Fatalf("capabilities = %v, want team", gw.gatewayCapabilities())
	}
	withoutBackend := newTeamGateway(t, nil)
	if slices.Contains(withoutBackend.gatewayCapabilities(), "team") {
		t.Fatalf("team advertised without a backend")
	}
}

// A build without the team backend answers capability_unsupported instead of
// pretending the team is empty.
func TestTeamWithoutBackendReportsCapabilityUnsupported(t *testing.T) {
	gw := newTeamGateway(t, nil)
	_, problem := gw.teamSnapshot()
	if problem == nil || problem.Code != codeCapabilityUnsupported {
		t.Fatalf("teamSnapshot without a backend = %v", problem)
	}
}
