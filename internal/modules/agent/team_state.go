package agent

// Team run-state tracking. The rig team manager only reports availability
// ("active" or "interrupted"); a teammate whose run finished still reports
// "active", so the inspector used to draw it as running forever. This file
// tracks the actual run lifecycle by wrapping every rig.Subagent the team
// runner starts:
//
//   - the initial run (started inside runner.Start) is watched by one bounded
//     goroutine that records its outcome and asks the chat subscribers for a
//     frame; the goroutine exits when the run ends or the member is stopped;
//   - follow-up runs (Send/SendAsync) are recorded synchronously by the wrapper;
//   - the tracker keeps a bounded activity timeline (<=200 rows total, <=30 per
//     member) and reconciles the wrapped runs with the manager's member IDs.
//
// The agent module (which this workstream may not change) builds the manager
// and the contracts.TeamService adapter in two separate calls, so the tracker
// is found through a small table keyed by those instances instead of a service
// field.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigteam "github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// teamActivityLimit bounds the whole activity read model.
	teamActivityLimit = 200
	// teamActivityPerRunLimit bounds the activity rows attributed to one member.
	teamActivityPerRunLimit = 30
	// teamActivityTextRunes bounds one activity text.
	teamActivityTextRunes = 200
	// teamRunErrorRunes bounds a stored run error.
	teamRunErrorRunes = 300
	// teamClaimWindow bounds how far a wrapped run may start before the
	// manager's Created stamp of the member it belongs to (both are set around
	// runner.Start, so the run starts first; the window only tolerates clock
	// granularity).
	teamClaimWindow = 2 * time.Second
)

// teamTrackers maps a *service and its *rigteam.Manager to the tracker they
// share. Entries live for the process lifetime of the service (a disposed
// service has no more spawns); the map holds a small struct, no goroutines.
var teamTrackers sync.Map

// teamTrackerFor returns the registered tracker for key, creating it when the
// create function is given.
func teamTrackerFor(key any, create func() *teamTracker) *teamTracker {
	if value, ok := teamTrackers.Load(key); ok {
		if tracker, ok := value.(*teamTracker); ok {
			return tracker
		}
	}
	if create == nil {
		return nil
	}
	tracker := create()
	actual, _ := teamTrackers.LoadOrStore(key, tracker)
	return actual.(*teamTracker)
}

// teamTracker owns the run registry and the activity read model of one agent
// service (one process-wide team manager).
type teamTracker struct {
	notify func()

	mu      sync.Mutex
	nextID  uint64
	runs    []*trackedSubagent
	claims  map[string]*trackedSubagent // member ID -> wrapped run
	entries []activityEntry
}

// activityEntry is one activity row; run is the wrapped run it belongs to (nil
// for a team-level event) and memberID caches the resolved member ID so a
// removed member's history keeps its owner.
type activityEntry struct {
	activity contracts.TeamActivity
	run      *trackedSubagent
	memberID string
}

func newTeamTracker(notify func()) *teamTracker {
	return &teamTracker{
		notify: notify,
		claims: make(map[string]*trackedSubagent),
	}
}

// notifyChange asks the chat subscribers for a frame. Callers never hold the
// tracker lock: the notify chain hops to the UI thread.
func (t *teamTracker) notifyChange() {
	if t == nil || t.notify == nil {
		return
	}
	t.notify()
}

// track wraps one started rig.Subagent and starts its bounded watcher. The
// initial run is already in flight when the runner returns; effectiveModel is
// the model the runner resolved (the spec override or the parent model) and
// sessionID is the parent session that spawned the member ("" when unknown).
func (t *teamTracker) track(inner rig.Subagent, spec rig.SubagentSpec, effectiveModel, sessionID string) rig.Subagent {
	if t == nil || inner == nil {
		return inner
	}
	model := strings.TrimSpace(spec.Model)
	if model == "" {
		model = strings.TrimSpace(effectiveModel)
	}
	t.mu.Lock()
	t.nextID++
	run := &trackedSubagent{
		tracker:   t,
		inner:     inner,
		id:        fmt.Sprintf("run-%d", t.nextID),
		name:      strings.TrimSpace(spec.Name),
		model:     model,
		prompt:    collapseTeamText(spec.Prompt, 120),
		sessionID: strings.TrimSpace(sessionID),
	}
	t.runs = append(t.runs, run)
	t.mu.Unlock()

	run.beginRun()
	t.addActivity(run, "", contracts.TeamActivitySpawn, fmt.Sprintf("创建队友「%s」：%s", run.name, run.prompt), false)
	t.notifyChange()

	ctx, cancel := context.WithCancel(context.Background())
	run.mu.Lock()
	run.cancel = cancel
	run.mu.Unlock()
	go run.watch(ctx)
	return run
}

// reconcile maps the manager's live members to their wrapped runs and returns
// the per-member run snapshot. Claims are stable, so a member keeps its state
// while rows are added above it; claims of removed members are released.
func (t *teamTracker) reconcile(members []rigteam.Member) map[string]teamRunSnapshot {
	out := make(map[string]teamRunSnapshot, len(members))
	if t == nil {
		return out
	}
	t.mu.Lock()
	live := make(map[string]bool, len(members))
	for _, member := range members {
		live[member.ID] = true
	}
	for id, run := range t.claims {
		if !live[id] && run != nil {
			run.mu.Lock()
			run.released = true
			run.mu.Unlock()
			delete(t.claims, id)
		}
	}
	for _, member := range members {
		run, ok := t.claims[member.ID]
		if !ok || run == nil {
			run = t.claimLocked(member)
			if run != nil {
				t.claims[member.ID] = run
			}
		}
		if run == nil {
			out[member.ID] = teamRunSnapshot{RunState: contracts.TeamRunIdle}
			continue
		}
		state := run.snapshot()
		state.Activity = t.activityForRunLocked(run)
		out[member.ID] = state
	}
	// Cache the resolved member ID on the activity rows so a later release
	// keeps the historical attribution.
	for index := range t.entries {
		entry := &t.entries[index]
		if entry.run == nil || entry.activity.MemberID != "" {
			continue
		}
		for memberID, run := range t.claims {
			if run == entry.run {
				entry.activity.MemberID = memberID
				entry.memberID = memberID
				break
			}
		}
	}
	t.mu.Unlock()
	return out
}

// claimLocked returns the wrapped run that belongs to member (t.mu held). The
// rig manager assigns a member its ID after runner.Start returned, so the run
// and the member are matched by display name and by the run starting just
// before the member's Created stamp; duplicates resolve in creation order.
func (t *teamTracker) claimLocked(member rigteam.Member) *trackedSubagent {
	claimed := make(map[*trackedSubagent]bool, len(t.claims))
	for _, run := range t.claims {
		claimed[run] = true
	}
	var best *trackedSubagent
	var bestDelta time.Duration
	for _, run := range t.runs {
		if claimed[run] || run.name != member.Name {
			continue
		}
		run.mu.Lock()
		started := run.started
		skip := run.released || run.closed || started.IsZero()
		run.mu.Unlock()
		if skip {
			continue
		}
		delta := member.Created.Sub(started)
		if delta < -teamClaimWindow {
			// The run started clearly after this member was created: it
			// belongs to a later member of the same name.
			continue
		}
		if delta < 0 {
			delta = -delta
		}
		if best == nil || delta < bestDelta {
			best, bestDelta = run, delta
		}
	}
	return best
}

// claimRun returns the wrapped run currently claimed by memberID (nil when the
// member has no tracked run).
func (t *teamTracker) claimRun(memberID string) *trackedSubagent {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.claims[memberID]
}

// release drops the claim of a closed member (its run stops recording).
func (t *teamTracker) release(memberID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if run, ok := t.claims[memberID]; ok {
		run.mu.Lock()
		run.released = true
		run.mu.Unlock()
		delete(t.claims, memberID)
	}
	t.mu.Unlock()
}

// claimMemberID returns the member ID a run currently belongs to.
func (t *teamTracker) claimMemberID(run *trackedSubagent) string {
	if t == nil || run == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for memberID, candidate := range t.claims {
		if candidate == run {
			return memberID
		}
	}
	for index := range t.entries {
		if t.entries[index].run == run && t.entries[index].memberID != "" {
			return t.entries[index].memberID
		}
	}
	return ""
}

// addActivity appends one bounded activity row and prunes the table. memberID
// is the fallback owner for events recorded after the claim was released (a
// close event); pass "" to resolve it from the current claims.
func (t *teamTracker) addActivity(run *trackedSubagent, memberID, kind, text string, isError bool) {
	if t == nil {
		return
	}
	text = collapseTeamText(text, teamActivityTextRunes)
	if strings.TrimSpace(text) == "" {
		return
	}
	if memberID == "" {
		memberID = t.claimMemberID(run)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	entry := activityEntry{
		activity: contracts.TeamActivity{
			ID:       fmt.Sprintf("team-activity-%d", t.nextID),
			MemberID: memberID,
			Kind:     kind,
			Text:     text,
			Time:     time.Now(),
			Error:    isError,
		},
		run:      run,
		memberID: memberID,
	}
	t.entries = append(t.entries, entry)
	t.pruneLocked()
}

// pruneLocked enforces the total and per-member bounds (t.mu held): the table
// keeps the newest teamActivityPerRunLimit rows of each run and the newest
// teamActivityLimit rows overall.
func (t *teamTracker) pruneLocked() {
	counts := make(map[*trackedSubagent]int, len(t.entries))
	kept := make([]activityEntry, 0, len(t.entries))
	for index := len(t.entries) - 1; index >= 0; index-- {
		entry := t.entries[index]
		if entry.run != nil {
			if counts[entry.run] >= teamActivityPerRunLimit {
				continue
			}
			counts[entry.run]++
		}
		kept = append(kept, entry)
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	if len(kept) > teamActivityLimit {
		kept = kept[len(kept)-teamActivityLimit:]
	}
	t.entries = kept
}

// activities returns the bounded activity table, oldest first, with member IDs
// resolved for the rows recorded before a member was claimed.
func (t *teamTracker) activities() []contracts.TeamActivity {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]contracts.TeamActivity, 0, len(t.entries))
	for _, entry := range t.entries {
		activity := entry.activity
		if activity.MemberID == "" {
			for memberID, run := range t.claims {
				if run == entry.run {
					activity.MemberID = memberID
					break
				}
			}
		}
		if activity.MemberID == "" {
			activity.MemberID = entry.memberID
		}
		out = append(out, activity)
	}
	return out
}

// activityForRunLocked returns a copy of one run's bounded activity rows
// (oldest first). The caller holds t.mu.
func (t *teamTracker) activityForRunLocked(run *trackedSubagent) []contracts.TeamActivity {
	if t == nil || run == nil {
		return nil
	}
	out := make([]contracts.TeamActivity, 0, teamActivityPerRunLimit)
	for _, entry := range t.entries {
		if entry.run != run {
			continue
		}
		activity := entry.activity
		if activity.MemberID == "" {
			activity.MemberID = entry.memberID
		}
		out = append(out, activity)
	}
	if len(out) > teamActivityPerRunLimit {
		out = out[len(out)-teamActivityPerRunLimit:]
	}
	return out
}

// trackedSubagent wraps one rig.Subagent and records its run lifecycle. It is
// safe to use concurrently (the rig Subagent contract already serializes runs).
type trackedSubagent struct {
	tracker *teamTracker
	inner   rig.Subagent

	id        string
	name      string
	model     string
	prompt    string
	sessionID string

	mu         sync.Mutex
	seq        int
	observed   int
	state      string
	started    time.Time
	finished   time.Time
	lastOutput string
	lastError  string
	usage      int
	iterations int
	cancel     context.CancelFunc
	closed     bool
	released   bool
}

var _ rig.Subagent = (*trackedSubagent)(nil)

// Name implements rig.Subagent.
func (t *trackedSubagent) Name() string { return t.inner.Name() }

// Send implements rig.Subagent: it marks the follow-up run and records its
// outcome when the call returns.
func (t *trackedSubagent) Send(ctx context.Context, message string) (rig.SubagentResult, error) {
	t.beginRun()
	t.tracker.addActivity(t, "", contracts.TeamActivitySend,
		fmt.Sprintf("发送消息给「%s」：%s", t.name, collapseTeamText(message, 120)), false)
	t.tracker.notifyChange()
	result, err := t.inner.Send(ctx, message)
	t.finishRun(result, err)
	return result, err
}

// Wait implements rig.Subagent: it records the outcome of the run it waited for
// when no newer run started meanwhile.
func (t *trackedSubagent) Wait(ctx context.Context) (rig.SubagentResult, error) {
	t.mu.Lock()
	seq := t.seq
	t.mu.Unlock()
	result, err := t.inner.Wait(ctx)
	t.record(seq, result, err)
	return result, err
}

// Stop implements rig.Subagent: it cancels the watcher and marks the current
// run interrupted before stopping the inner subagent.
func (t *trackedSubagent) Stop() error {
	t.mu.Lock()
	cancel := t.cancel
	t.cancel = nil
	wasRunning := t.state == contracts.TeamRunRunning
	if wasRunning {
		t.state = contracts.TeamRunInterrupted
		t.finished = time.Now()
	}
	t.closed = true
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if wasRunning {
		t.tracker.addActivity(t, "", contracts.TeamActivityInterrupt,
			fmt.Sprintf("「%s」的当前运行已停止", t.name), false)
	}
	return t.inner.Stop()
}

// beginRun marks a new in-flight run.
func (t *trackedSubagent) beginRun() {
	t.mu.Lock()
	t.seq++
	t.state = contracts.TeamRunRunning
	t.started = time.Now()
	t.finished = time.Time{}
	t.lastError = ""
	t.mu.Unlock()
}

// finishRun records the result of the run started by Send.
func (t *trackedSubagent) finishRun(result rig.SubagentResult, err error) {
	t.mu.Lock()
	seq := t.seq
	t.mu.Unlock()
	t.record(seq, result, err)
}

// record stores one completed run outcome when it belongs to the current run.
func (t *trackedSubagent) record(seq int, result rig.SubagentResult, err error) {
	t.mu.Lock()
	if seq != t.seq || t.observed == seq || t.closed {
		t.mu.Unlock()
		return
	}
	t.observed = seq
	t.finished = time.Now()
	t.lastOutput = strings.TrimSpace(result.Output)
	t.usage += result.Usage.TotalTokens
	t.iterations += result.Iterations
	if err != nil {
		t.state = contracts.TeamRunFailed
		t.lastError = truncateTeamText(err.Error(), teamRunErrorRunes)
	} else {
		t.state = contracts.TeamRunDone
		t.lastError = ""
	}
	state := t.state
	output := t.lastOutput
	runErr := t.lastError
	t.mu.Unlock()

	switch state {
	case contracts.TeamRunFailed:
		t.tracker.addActivity(t, "", contracts.TeamActivityError,
			fmt.Sprintf("「%s」运行失败：%s", t.name, runErr), true)
	default:
		t.tracker.addActivity(t, "", contracts.TeamActivityFinish,
			fmt.Sprintf("「%s」已完成：%s", t.name, collapseTeamText(output, 120)), false)
	}
	t.tracker.notifyChange()
}

// watch waits for the initial background run and records it. It is the single
// goroutine per spawn and exits when the run ends or the subagent is stopped.
func (t *trackedSubagent) watch(ctx context.Context) {
	result, err := t.inner.Wait(ctx)
	if ctx.Err() != nil {
		return
	}
	t.mu.Lock()
	seq := t.seq
	t.mu.Unlock()
	t.record(seq, result, err)
}

// snapshot returns the current run state for the member conversion.
func (t *trackedSubagent) snapshot() teamRunSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return teamRunSnapshot{
		RunState:    t.state,
		Error:       t.lastError,
		LastOutput:  t.lastOutput,
		Model:       t.model,
		Started:     t.started,
		Finished:    t.finished,
		UsageTokens: t.usage,
		Iterations:  t.iterations,
		SessionID:   t.sessionID,
	}
}

// teamRunSnapshot is the run-state half of one contracts.TeamMember.
type teamRunSnapshot struct {
	RunState    string
	Error       string
	LastOutput  string
	Model       string
	Started     time.Time
	Finished    time.Time
	UsageTokens int
	Iterations  int
	// SessionID is the parent session that spawned the member ("" when the
	// backend could not attribute it).
	SessionID string
	// Activity is the member's bounded recent activity (oldest first), copied
	// out of the tracker so the contract member owns its slice.
	Activity []contracts.TeamActivity
}

// collapseTeamText collapses whitespace and bounds text to limit runes.
func collapseTeamText(text string, limit int) string {
	// The activity timeline is one muted line: strip the Markdown structure the
	// teammate's answer carries (fence blocks, heading/list/quote markers,
	// inline code/emphasis) before collapsing the whitespace, so a summary never
	// shows "## … ```text …" noise.
	var kept []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>-*+ \t")
		trimmed = strings.ReplaceAll(trimmed, "`", "")
		trimmed = strings.ReplaceAll(trimmed, "**", "")
		trimmed = strings.ReplaceAll(trimmed, "__", "")
		if trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	collapsed := strings.Join(strings.Fields(strings.Join(kept, " ")), " ")
	return truncateTeamText(collapsed, limit)
}

// truncateTeamText bounds text to limit runes with a trailing ellipsis.
func truncateTeamText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
