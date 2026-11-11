package agent

// Tests for the team run-state tracker and the bounded activity read model: the
// rig manager only reports availability, so these tests pin the truthful
// running/done/failed/interrupted states, the completion notification and the
// activity bounds the inspector relies on.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// gatedProvider is a rig.Provider whose stream blocks until release is closed:
// the tests need a teammate that is observably running before it finishes.
type gatedProvider struct {
	release chan struct{}
	err     error

	mu    sync.Mutex
	calls int
}

func newGatedProvider() *gatedProvider {
	return &gatedProvider{release: make(chan struct{})}
}

func (p *gatedProvider) Name() string { return "gated" }

func (p *gatedProvider) Stream(ctx context.Context, _ rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.err != nil {
		return nil, p.err
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "team answer"},
		{FinishReason: "stop"},
	}}, nil
}

func (p *gatedProvider) releaseNow() {
	select {
	case <-p.release:
	default:
		close(p.release)
	}
}

// waitForTeam polls until check is true or the deadline passes.
func waitForTeam(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// teamMemberState returns the member with id, or nil.
func teamMemberState(members []contracts.TeamMember, id string) *contracts.TeamMember {
	for index := range members {
		if members[index].ID == id {
			return &members[index]
		}
	}
	return nil
}

// hasActivityKind reports whether the activity read model holds one entry of
// kind (optionally for memberID; "" matches any owner).
func hasActivityKind(t *testing.T, teamService contracts.TeamService, kind, memberID string) bool {
	t.Helper()
	lister, ok := teamService.(contracts.TeamActivityLister)
	if !ok {
		t.Fatalf("team service %T does not implement TeamActivityLister", teamService)
	}
	for _, activity := range lister.TeamActivities() {
		if activity.Kind != kind {
			continue
		}
		if memberID == "" || activity.MemberID == memberID {
			return true
		}
	}
	return false
}

func TestTeamRunStateTracksInitialRun(t *testing.T) {
	provider := newGatedProvider()
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	var notifications int
	var notifyMu sync.Mutex
	unsubscribe, err := svc.Subscribe(func(string) {
		notifyMu.Lock()
		notifications++
		notifyMu.Unlock()
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsubscribe()

	member, err := teamService.Spawn(context.Background(), "研究员", "核对发布清单", "team-model")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitForTeam(t, "run state running", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunRunning
	})
	running := teamMemberState(teamService.Members(), member.ID)
	if running == nil || running.Model != "team-model" || running.Started.IsZero() {
		t.Fatalf("running member = %+v, want model/started populated", running)
	}

	provider.releaseNow()
	waitForTeam(t, "run state done", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunDone
	})
	done := teamMemberState(teamService.Members(), member.ID)
	if done == nil || done.Finished.IsZero() || !strings.Contains(done.Last, "team answer") || done.Error != "" {
		t.Fatalf("finished member = %+v, want finished answer and no error", done)
	}
	if !hasActivityKind(t, teamService, contracts.TeamActivitySpawn, member.ID) {
		t.Fatalf("activity model = %+v, want a spawn event for %s",
			teamService.(contracts.TeamActivityLister).TeamActivities(), member.ID)
	}
	if !hasActivityKind(t, teamService, contracts.TeamActivityFinish, member.ID) {
		t.Fatalf("activity model = %+v, want a finish event for %s",
			teamService.(contracts.TeamActivityLister).TeamActivities(), member.ID)
	}
	notifyMu.Lock()
	got := notifications
	notifyMu.Unlock()
	if got == 0 {
		t.Fatal("no chat notification after the teammate finished; idle windows would not repaint")
	}
}

func TestTeamRunStateTracksFailure(t *testing.T) {
	provider := newGatedProvider()
	provider.err = errors.New("provider exploded")
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "失败者", "try hard", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	provider.releaseNow()
	waitForTeam(t, "run state failed", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunFailed
	})
	failed := teamMemberState(teamService.Members(), member.ID)
	if failed == nil || !strings.Contains(failed.Error, "provider exploded") || failed.Last != "" {
		t.Fatalf("failed member = %+v, want error and no stale answer", failed)
	}
	if !hasActivityKind(t, teamService, contracts.TeamActivityError, member.ID) {
		t.Fatalf("activity model = %+v, want an error event",
			teamService.(contracts.TeamActivityLister).TeamActivities())
	}
}

func TestTeamRunStateTracksInterruptAndClose(t *testing.T) {
	provider := newGatedProvider()
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "可中断", "long job", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitForTeam(t, "run state running", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunRunning
	})
	if err := teamService.Interrupt(member.ID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitForTeam(t, "run state interrupted", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunInterrupted
	})
	if !hasActivityKind(t, teamService, contracts.TeamActivityInterrupt, member.ID) {
		t.Fatalf("activity model = %+v, want an interrupt event",
			teamService.(contracts.TeamActivityLister).TeamActivities())
	}

	if err := teamService.Close(member.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if members := liveTeamMembers(teamService.Members()); len(members) != 0 {
		t.Fatalf("Members after Close = %+v, want no live member", members)
	}
	if !hasActivityKind(t, teamService, contracts.TeamActivityClose, member.ID) {
		t.Fatalf("activity model = %+v, want a close event for %s",
			teamService.(contracts.TeamActivityLister).TeamActivities(), member.ID)
	}
}

func TestTeamActivityBounds(t *testing.T) {
	tracker := newTeamTracker(nil)
	run := &trackedSubagent{tracker: tracker, name: "bounded"}
	tracker.mu.Lock()
	tracker.runs = append(tracker.runs, run)
	tracker.mu.Unlock()

	for index := 0; index < 50; index++ {
		tracker.addActivity(run, "", contracts.TeamActivityTool, fmt.Sprintf("member row %d", index), false)
	}
	if got := len(tracker.activities()); got > teamActivityPerRunLimit {
		t.Fatalf("per-member activity rows = %d, want <= %d", got, teamActivityPerRunLimit)
	}
	for index := 0; index < 260; index++ {
		tracker.addActivity(nil, "", contracts.TeamActivityTool, fmt.Sprintf("team row %d", index), false)
	}
	if got := len(tracker.activities()); got > teamActivityLimit {
		t.Fatalf("total activity rows = %d, want <= %d", got, teamActivityLimit)
	}
	activities := tracker.activities()
	if len(activities) == 0 || !strings.Contains(activities[len(activities)-1].Text, "team row 259") {
		t.Fatalf("activity tail = %+v, want the newest row kept", activities)
	}
}

func TestTeamToolInvocationRecordsActivity(t *testing.T) {
	provider := newGatedProvider()
	svc, teamService, ctx := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	toolkit, err := ctx.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	result, err := toolkit.Invoke(context.Background(), rig.ToolCall{
		ID: "team-spawn", Name: "spawn_teammate",
		Arguments: `{"name":"工具队友","prompt":"检查发布说明"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("spawn_teammate = %q/%v", result.Content, err)
	}
	waitForTeam(t, "tool-spawned member", func() bool {
		return len(teamService.Members()) == 1
	})
	member := teamService.Members()[0]
	if member.RunState != contracts.TeamRunRunning {
		t.Fatalf("tool-spawned member run state = %q, want running", member.RunState)
	}
	if !hasActivityKind(t, teamService, contracts.TeamActivitySpawn, member.ID) {
		t.Fatalf("activity model = %+v, want a spawn event attributed to %s",
			teamService.(contracts.TeamActivityLister).TeamActivities(), member.ID)
	}
	provider.releaseNow()
	waitForTeam(t, "tool-spawned member done", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunDone
	})
}

// TestTeamMemberActivityIsPopulated pins the contract member's activity: the
// tracker's bounded event rows (spawn/send/finish/failed/…) reach Members() via
// the run snapshot, so the inspector's member detail can render the timeline.
func TestTeamMemberActivityIsPopulated(t *testing.T) {
	provider := newGatedProvider()
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "研究员", "核对发布清单", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitForTeam(t, "run state running", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunRunning
	})
	provider.releaseNow()
	waitForTeam(t, "run state done", func() bool {
		current := teamMemberState(teamService.Members(), member.ID)
		return current != nil && current.RunState == contracts.TeamRunDone
	})

	done := teamMemberState(teamService.Members(), member.ID)
	if done == nil {
		t.Fatal("member disappeared")
	}
	if len(done.Activity) == 0 {
		t.Fatal("TeamMember.Activity is empty: the tracker activity never reached the member snapshot")
	}
	kinds := make(map[string]bool, len(done.Activity))
	attributed := 0
	for _, activity := range done.Activity {
		kinds[activity.Kind] = true
		if activity.ID == "" || activity.Text == "" {
			t.Fatalf("activity row = %+v, want ID and Text", activity)
		}
		if activity.MemberID == member.ID {
			attributed++
		}
	}
	if !kinds[contracts.TeamActivitySpawn] || !kinds[contracts.TeamActivityFinish] {
		t.Fatalf("activity kinds = %v, want spawn and finish", kinds)
	}
	if attributed == 0 {
		t.Fatalf("activity rows are not attributed to %s: %+v", member.ID, done.Activity)
	}
	if len(done.Activity) > 30 {
		t.Fatalf("member activity rows = %d, want <= 30 (bounded)", len(done.Activity))
	}
}
