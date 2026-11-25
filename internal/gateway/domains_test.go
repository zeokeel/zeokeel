package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The six small domains (task S1c): each one must satisfy "replay the delta onto
// the snapshot == the next snapshot", and each must be reachable over the wire.

// --- fakes -----------------------------------------------------------------

// fakeApprovals is an in-memory contracts.Approvals with a manual pending set.
type fakeApprovals struct {
	mu   sync.Mutex
	mode string
	// settings, when set, is the permission row the real module derives Mode()
	// from (agent.permission): the fake mirrors that so a settings write really
	// changes the approval policy.
	settings *fakeSettings
	pending  []contracts.ApprovalRequest
	waiting  map[string]chan contracts.ApprovalDecision
	subs     map[int]func()
	nextSub  int
}

func newFakeApprovals(mode string) *fakeApprovals {
	return &fakeApprovals{mode: mode, waiting: map[string]chan contracts.ApprovalDecision{}, subs: map[int]func(){}}
}

// Mode implements contracts.Approvals.
func (f *fakeApprovals) Mode() string {
	f.mu.Lock()
	settings := f.settings
	mode := f.mode
	f.mu.Unlock()
	if settings != nil {
		if row := settings.Get(contracts.SettingAgentPermission, ""); strings.TrimSpace(row) != "" {
			return contracts.NormalizePermissionMode(row)
		}
	}
	return mode
}

// Request implements contracts.Approvals: it parks until Resolve.
func (f *fakeApprovals) Request(ctx context.Context, request contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	channel := make(chan contracts.ApprovalDecision, 1)
	f.mu.Lock()
	f.pending = append(f.pending, request)
	f.waiting[request.ID] = channel
	f.mu.Unlock()
	f.notify()
	select {
	case decision := <-channel:
		return decision, nil
	case <-ctx.Done():
		return contracts.ApprovalDeny, ctx.Err()
	}
}

// Pending implements contracts.Approvals.
func (f *fakeApprovals) Pending() []contracts.ApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ApprovalRequest(nil), f.pending...)
}

// Resolve implements contracts.Approvals.
func (f *fakeApprovals) Resolve(id string, decision contracts.ApprovalDecision) error {
	f.mu.Lock()
	channel, ok := f.waiting[id]
	delete(f.waiting, id)
	for i, request := range f.pending {
		if request.ID == id {
			f.pending = append(f.pending[:i], f.pending[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
	if ok {
		channel <- decision
	}
	f.notify()
	return nil
}

// Subscribe implements contracts.Approvals.
func (f *fakeApprovals) Subscribe(fn func()) (func(), error) {
	f.mu.Lock()
	id := f.nextSub
	f.nextSub++
	f.subs[id] = fn
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
	}, nil
}

// setMode changes the effective policy. Like the real module, the mode is
// derived from the agent.permission row when a settings store is attached, so the
// fake writes that row too (otherwise the change would not be observable).
func (f *fakeApprovals) setMode(mode string) {
	f.mu.Lock()
	settings := f.settings
	f.mode = mode
	f.mu.Unlock()
	if settings != nil {
		_ = settings.Set(contracts.SettingAgentPermission, mode)
	}
	f.notify()
}

// notify calls the change subscribers synchronously, like the real service.
func (f *fakeApprovals) notify() {
	f.mu.Lock()
	subs := make([]func(), 0, len(f.subs))
	for _, fn := range f.subs {
		subs = append(subs, fn)
	}
	f.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

// fakeSettings is an in-memory contracts.SettingsService with no change
// notification (exactly like the real one).
type fakeSettings struct {
	mu   sync.Mutex
	rows map[string]string
}

func newFakeSettings(rows map[string]string) *fakeSettings {
	if rows == nil {
		rows = map[string]string{}
	}
	return &fakeSettings{rows: rows}
}

// Get implements contracts.SettingsService.
func (f *fakeSettings) Get(key, fallback string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.rows[key]; ok {
		return value
	}
	return fallback
}

// Set implements contracts.SettingsService.
func (f *fakeSettings) Set(key, value string) error {
	f.mu.Lock()
	f.rows[key] = value
	f.mu.Unlock()
	return nil
}

// Delete implements contracts.SettingsDeleter.
func (f *fakeSettings) Delete(key string) error {
	f.mu.Lock()
	delete(f.rows, key)
	f.mu.Unlock()
	return nil
}

// All implements contracts.SettingsService.
func (f *fakeSettings) All() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.rows))
	for key, value := range f.rows {
		out[key] = value
	}
	return out
}

// fakeWorkspace is an in-memory contracts.WorkspaceService.
type fakeWorkspace struct {
	mu      sync.Mutex
	list    []contracts.Workspace
	current string
	nextID  int
}

func newFakeWorkspace() *fakeWorkspace { return &fakeWorkspace{} }

// List implements contracts.WorkspaceService.
func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.Workspace(nil), f.list...), nil
}

// Current implements contracts.WorkspaceService.
func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, workspace := range f.list {
		if workspace.ID == f.current {
			return workspace, true, nil
		}
	}
	return contracts.Workspace{}, false, nil
}

// Create implements contracts.WorkspaceService.
func (f *fakeWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	workspace := contracts.Workspace{ID: fmt.Sprintf("w%d", f.nextID), Name: name, Root: root, CreatedAt: time.Now()}
	f.list = append(f.list, workspace)
	f.current = workspace.ID
	return workspace, nil
}

// Open implements contracts.WorkspaceService.
func (f *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, workspace := range f.list {
		if workspace.ID == id {
			f.current = id
			return workspace, nil
		}
	}
	return contracts.Workspace{}, errors.New("workspace not found")
}

// Delete implements contracts.WorkspaceService.
func (f *fakeWorkspace) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, workspace := range f.list {
		if workspace.ID == id {
			f.list = append(f.list[:i], f.list[i+1:]...)
			if f.current == id {
				f.current = ""
			}
			return nil
		}
	}
	return nil
}

// Files implements contracts.WorkspaceService.
func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

// ReadFile implements contracts.WorkspaceService.
func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, errors.New("not implemented in the fake")
}

// Search implements contracts.WorkspaceService.
func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// setColor changes one workspace's colour (a "changed" delta).
func (f *fakeWorkspace) setColor(id, color string) {
	f.mu.Lock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].Color = color
		}
	}
	f.mu.Unlock()
}

// domainTestRig is a served gateway with the fake backends wired in.
type domainTestRig struct {
	*runningGateway
	chat      *fakeChat
	approvals *fakeApprovals
	settings  *fakeSettings
	workspace *fakeWorkspace
	tasks     *fakeTaskBoard
	context   *fakeContextInspector
}

// startDomainGateway starts a gateway with the small-domain backends.
func startDomainGateway(t *testing.T) *domainTestRig {
	t.Helper()
	// scriptedChat only adds the regenerate half (and a script that is nil
	// unless a test sets one); the domain rigs stay deterministic.
	scripted := newScriptedChat()
	scripted.addSession("s1", "First", "hello")
	chat := scripted.fakeChat
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionWorkspace})
	approvals := newFakeApprovals(contracts.PermissionWorkspace)
	approvals.settings = settings
	workspace := newFakeWorkspace()
	board := newFakeTaskBoard()
	inspector := newFakeContextInspector()

	root := cordisRootFor(t, scripted, approvals, settings, workspace, board, inspector)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gateway.pollInterval = 20 * time.Millisecond
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	served := serveGateway(t, gateway)
	return &domainTestRig{runningGateway: served, chat: chat, approvals: approvals,
		settings: settings, workspace: workspace, tasks: board, context: inspector}
}

// connect opens an authenticated control-plane connection.
func (r *domainTestRig) connect(t *testing.T) *testWSConn {
	t.Helper()
	conn := dialTestWS(t, r.address, map[string]string{
		"Origin":        r.info.Origin(),
		"Authorization": "Bearer " + r.info.Token,
	})
	t.Cleanup(conn.close)
	resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(t, conn.call("ready", nil))
	return conn
}

// --- task board fake -------------------------------------------------------

// fakeTaskBoard is an in-memory contracts.TaskBoard with a manual notify, so a
// test can mutate it "behind the gateway's back" (which is what the agent's task
// tools do in production).
type fakeTaskBoard struct {
	mu    sync.Mutex
	tasks []contracts.BoardTask
	next  int
	// deleted holds the tombstoned ids: the real board hides a deleted task
	// behind a tombstone (rig has no Delete), so the row survives for an undo.
	deleted map[string]contracts.BoardTask
	// sessions, when set, makes the fake implement contracts.SessionTaskBoard.
	sessions map[string][]contracts.BoardTask
}

// newFakeTaskBoard returns an empty board.
func newFakeTaskBoard() *fakeTaskBoard {
	return &fakeTaskBoard{deleted: map[string]contracts.BoardTask{}}
}

// List implements contracts.TaskBoard.
func (f *fakeTaskBoard) List(context.Context) ([]contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.BoardTask(nil), f.tasks...), nil
}

// Create implements contracts.TaskBoard.
func (f *fakeTaskBoard) Create(_ context.Context, subject, description string) (contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	now := time.Now().UTC().Truncate(time.Second)
	task := contracts.BoardTask{
		ID: fmt.Sprintf("t%d", f.next), Subject: subject, Description: description,
		Status: contracts.TaskStatusPending, Created: now, Updated: now,
	}
	f.tasks = append(f.tasks, task)
	return task, nil
}

// SetStatus implements contracts.TaskBoard.
func (f *fakeTaskBoard) SetStatus(_ context.Context, id, status string) (contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := range f.tasks {
		if f.tasks[index].ID == id {
			f.tasks[index].Status = status
			f.tasks[index].Updated = time.Now().UTC().Truncate(time.Second)
			return f.tasks[index], nil
		}
	}
	return contracts.BoardTask{}, fmt.Errorf("task %q not found", id)
}

// Delete implements contracts.TaskBoard: the row is tombstoned (hidden), not
// erased, exactly like the real board.
func (f *fakeTaskBoard) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := range f.tasks {
		if f.tasks[index].ID == id {
			f.deleted[id] = f.tasks[index]
			f.tasks = append(f.tasks[:index], f.tasks[index+1:]...)
			return nil
		}
	}
	return fmt.Errorf("task %q not found", id)
}

// RestoreTask implements contracts.TaskBoardRestorer: the same row comes back
// with its id, status, owner and timestamps.
func (f *fakeTaskBoard) RestoreTask(_ context.Context, id string) (contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task, ok := f.deleted[id]
	if !ok {
		return contracts.BoardTask{}, fmt.Errorf("task %q is not deleted", id)
	}
	delete(f.deleted, id)
	f.tasks = append(f.tasks, task)
	return task, nil
}

// seed adds one task directly (the "someone else mutated the board" path).
func (f *fakeTaskBoard) seed(subject, status string) contracts.BoardTask {
	task, _ := f.Create(context.Background(), subject, "")
	if status != "" {
		task, _ = f.SetStatus(context.Background(), task.ID, status)
	}
	return task
}

// --- context inspector fake ------------------------------------------------

// fakeContextInspector is an in-memory contracts.ContextInspector: a test bumps
// the revision to simulate the agent folding a new event.
type fakeContextInspector struct {
	mu      sync.Mutex
	views   map[string]contracts.SessionContext
	missing map[string]bool
}

// newFakeContextInspector returns an inspector with no recorded sessions.
func newFakeContextInspector() *fakeContextInspector {
	return &fakeContextInspector{views: map[string]contracts.SessionContext{}, missing: map[string]bool{}}
}

// SessionContext implements contracts.ContextInspector.
func (f *fakeContextInspector) SessionContext(sessionID string) (contracts.SessionContext, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing[sessionID] {
		return contracts.SessionContext{}, false
	}
	view, ok := f.views[sessionID]
	if !ok {
		return contracts.SessionContext{}, false
	}
	return view, true
}

// record folds one provider call into the fake view: it bumps the revision and
// appends a request, exactly the shape the agent produces.
func (f *fakeContextInspector) record(sessionID string, promptTokens int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	view := f.views[sessionID]
	view.Revision++
	view.Snapshot = contracts.ContextSnapshot{
		Model: "test-model", Window: 200000,
		SystemPromptTokens: 1200, SystemPromptBytes: 5200,
		History:         contracts.ContextHistory{Messages: 3, User: 1, Assistant: 2},
		Injected:        contracts.ContextInjected{AgentsMD: true},
		EstimatedPrompt: promptTokens,
	}
	view.Requests = append(view.Requests, contracts.ContextRequest{
		Seq: int64(view.Revision), Iteration: len(view.Requests) + 1,
		Model: "test-model", Prompt: promptTokens, Total: promptTokens + 20,
		ContextTokens: promptTokens,
	})
	view.Used = promptTokens
	view.Window = 200000
	f.views[sessionID] = view
}

// awaitDelta reads domain notifications until one matches, then returns its
// params. A domain may legitimately emit more than one delta for a sequence of
// mutations (the poll coalesces or splits them), so a test that wants "the delta
// that describes X" must not assume it is the very next frame.
func awaitDelta(t *testing.T, conn *testWSConn, method string, match func(params map[string]any) bool) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		frame := conn.receive(func(candidate string) bool { return candidate == method })
		params, _ := frame["params"].(map[string]any)
		if match(params) {
			return params
		}
	}
	t.Fatalf("no %s matching the condition within 8 frames", method)
	return nil
}

// itemWithID returns the first entry of a list whose id matches.
func itemWithID(list any, id string) map[string]any {
	items, _ := list.([]any)
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["id"] == id {
			return row
		}
	}
	return nil
}

// --- delta appliers (client-side) ------------------------------------------

// applyItemSet replays an added/removed delta onto an id-keyed snapshot.
func applyItemSet(t *testing.T, items []any, delta *itemSetDelta) []any {
	t.Helper()
	byID := map[string]any{}
	var order []string
	for _, item := range items {
		id := itemID(item)
		if _, seen := byID[id]; !seen {
			order = append(order, id)
		}
		byID[id] = item
	}
	for _, id := range delta.Removed {
		delete(byID, id)
	}
	for _, item := range delta.Added {
		id := itemID(item)
		if _, seen := byID[id]; !seen {
			order = append(order, id)
		}
		byID[id] = item
	}
	out := make([]any, 0, len(byID))
	for _, id := range order {
		if item, ok := byID[id]; ok {
			out = append(out, item)
		}
	}
	return out
}

// idsOf is the sorted id list of a DTO list.
func idsOf(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, itemID(item))
	}
	sort.Strings(out)
	return out
}

// --- tests -----------------------------------------------------------------

// TestApprovalsSnapshotDeltaReplay is the approvals equivalence test: snapshot,
// resolve, delta, replay == next snapshot.
func TestApprovalsSnapshotDeltaReplay(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	// Park one request by calling Request from a goroutine (as the agent does).
	// Request blocks until Resolve, so the goroutine reports readiness first.
	waiting := make(chan struct{})
	go func() {
		close(waiting)
		_, _ = rig.approvals.Request(context.Background(), contracts.ApprovalRequest{
			ID: "req-1", Tool: "shell", Summary: "run ls", Arguments: `{"cmd":"ls"}`,
			CreatedAt: time.Now(), SessionID: "s1", Source: "user",
		})
	}()
	<-waiting

	// The delta reaches every ready client (approvals is a global domain).
	delta := conn.receive(func(method string) bool { return method == "approvals.delta" })
	params, _ := delta["params"].(map[string]any)
	added, _ := params["added"].([]any)
	if len(added) != 1 {
		t.Fatalf("approvals.delta added = %v, want one request", params["added"])
	}
	first, _ := added[0].(map[string]any)
	if first["id"] != "req-1" || first["sessionId"] != "s1" || first["source"] != "user" {
		t.Fatalf("approval carried the wrong facts: %v", first)
	}

	// Snapshot after the change.
	snapshot := resultOf(t, conn.call("approvals.snapshot", nil))
	rev := uint64(snapshot["rev"].(float64))
	pendingSnapshot, _ := snapshot["pending"].([]any)
	if len(pendingSnapshot) != 1 {
		t.Fatalf("approvals.snapshot pending = %v", snapshot["pending"])
	}
	if snapshot["mode"] != contracts.PermissionWorkspace || snapshot["permission"] != contracts.PermissionWorkspace {
		t.Fatalf("approvals mode/permission = %v/%v", snapshot["mode"], snapshot["permission"])
	}

	// Resolve it: a removal delta, and replaying it onto the snapshot equals the
	// next snapshot.
	_ = rig.approvals.Resolve("req-1", contracts.ApprovalAllow)
	removal := conn.receive(func(method string) bool { return method == "approvals.delta" })
	removalParams, _ := removal["params"].(map[string]any)
	decoded := decodeItemSetDelta(t, removalParams)
	if len(decoded.Removed) != 1 || decoded.Removed[0] != "req-1" {
		t.Fatalf("removal delta = %+v", decoded)
	}
	after := resultOf(t, conn.call("approvals.snapshot", nil))
	afterPending, _ := after["pending"].([]any)
	if len(afterPending) != 0 {
		t.Fatalf("approvals.snapshot after resolve = %v", after["pending"])
	}
	if got := idsOf(applyItemSet(t, pendingSnapshot, decoded)); len(got) != 0 {
		t.Fatalf("replaying the delta did not reach the next snapshot: %v", got)
	}
	if uint64(after["rev"].(float64)) != decoded.Rev {
		t.Fatalf("delta rev %d != snapshot rev %v", decoded.Rev, after["rev"])
	}
	if uint64(removalParams["baseRev"].(float64)) != rev {
		t.Fatalf("delta baseRev %v != snapshot rev %d", removalParams["baseRev"], rev)
	}
}

// TestApprovalsSnapshotCarriesTimeoutAndExpiry pins the automation-only expiry
// rule (RG-04): interactive requests never expire.
func TestApprovalsSnapshotCarriesTimeoutAndExpiry(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	_ = rig.settings.Set(contracts.SettingAgentApprovalTimeoutSeconds, "30")
	conn := rig.connect(t)

	interactive := contracts.ApprovalRequest{ID: "req-user", Tool: "shell", CreatedAt: time.Now(), SessionID: "s1", Source: "user"}
	automated := contracts.ApprovalRequest{ID: "req-auto", Tool: "shell", CreatedAt: time.Now(), SessionID: "s2", Source: contracts.SessionOriginAutomation}
	go func() { _, _ = rig.approvals.Request(context.Background(), interactive) }()
	go func() { _, _ = rig.approvals.Request(context.Background(), automated) }()

	deadline := time.Now().Add(2 * time.Second)
	var pending []any
	var mode string
	var timeout float64
	for {
		snapshot := resultOf(t, conn.call("approvals.snapshot", nil))
		items, _ := snapshot["pending"].([]any)
		if len(items) == 2 {
			pending = items
			mode = snapshot["mode"].(string)
			timeout = snapshot["approvalTimeoutSeconds"].(float64)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("both approvals never appeared: %v", snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mode != contracts.PermissionWorkspace || timeout != 30 {
		t.Fatalf("mode/timeout = %q/%v", mode, timeout)
	}
	for _, raw := range pending {
		item, _ := raw.(map[string]any)
		_, hasExpiry := item["expiresAt"]
		switch item["id"] {
		case "req-auto":
			if !hasExpiry {
				t.Fatalf("the automation request has no expiresAt: %v", item)
			}
		case "req-user":
			if hasExpiry {
				t.Fatalf("an interactive request must not expire: %v", item)
			}
		}
	}
}

// TestSettingsPollingPublishesDelta proves the honest degradation: settings has
// no change notification, so the gateway polls and publishes a delta only when
// the rows actually changed.
func TestSettingsPollingPublishesDelta(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	snapshot := resultOf(t, conn.call("settings.snapshot", nil))
	rows, _ := snapshot["rows"].(map[string]any)
	if rows[contracts.SettingAgentPermission] != contracts.PermissionWorkspace {
		t.Fatalf("settings.snapshot rows = %v", rows)
	}
	rev := uint64(snapshot["rev"].(float64))

	// A write from a source the gateway cannot observe directly.
	if err := rig.settings.Set("ui.theme.mode", "dark"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	delta := conn.receive(func(method string) bool { return method == "settings.delta" })
	params, _ := delta["params"].(map[string]any)
	if params["rows"] == nil {
		t.Fatalf("settings.delta carried no rows: %v", params)
	}
	changed, _ := params["rows"].(map[string]any)
	if changed["ui.theme.mode"] != "dark" {
		t.Fatalf("settings.delta rows = %v", changed)
	}
	if uint64(params["baseRev"].(float64)) != rev {
		t.Fatalf("settings.delta baseRev = %v, want %d", params["baseRev"], rev)
	}

	// Replaying the delta onto the snapshot equals the next snapshot.
	next := resultOf(t, conn.call("settings.snapshot", nil))
	nextRows, _ := next["rows"].(map[string]any)
	replayed := map[string]any{}
	for key, value := range rows {
		replayed[key] = value
	}
	if removed, ok := params["removed"].([]any); ok {
		for _, key := range removed {
			delete(replayed, key.(string))
		}
	}
	for key, value := range changed {
		replayed[key] = value
	}
	if len(replayed) != len(nextRows) {
		t.Fatalf("replayed rows %d != snapshot rows %d", len(replayed), len(nextRows))
	}
	for key, value := range nextRows {
		if replayed[key] != value {
			t.Fatalf("row %q replayed as %v, snapshot has %v", key, replayed[key], value)
		}
	}
}

// TestWorkspaceListSnapshotDeltaReplay covers add → changed → remove.
func TestWorkspaceListSnapshotDeltaReplay(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	before := resultOf(t, conn.call("workspace.list.snapshot", nil))
	rev := uint64(before["rev"].(float64))

	created, err := rig.workspace.Create(context.Background(), "ZeoKeel", "/tmp/zk")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	addDelta := conn.receive(func(method string) bool { return method == "workspace.list.delta" })
	addParams, _ := addDelta["params"].(map[string]any)
	added, _ := addParams["added"].([]any)
	if len(added) != 1 {
		t.Fatalf("workspace add delta = %v", addParams)
	}
	if addParams["currentId"] != created.ID {
		t.Fatalf("workspace delta currentId = %v, want %q", addParams["currentId"], created.ID)
	}
	if uint64(addParams["baseRev"].(float64)) != rev {
		t.Fatalf("workspace delta baseRev = %v, want %d", addParams["baseRev"], rev)
	}

	// Recolor: eventually a "changed" delta carrying the new color.
	rig.workspace.setColor(created.ID, "violet")
	changeParams := awaitDelta(t, conn, "workspace.list.delta", func(params map[string]any) bool {
		row := itemWithID(params["changed"], created.ID)
		return row != nil && row["color"] == "violet"
	})
	if uint64(changeParams["rev"].(float64)) <= rev {
		t.Fatalf("workspace change delta rev = %v, want > %d", changeParams["rev"], rev)
	}

	// Delete: a removed delta, and the snapshot agrees.
	if err := rig.workspace.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	awaitDelta(t, conn, "workspace.list.delta", func(params map[string]any) bool {
		removed, _ := params["removed"].([]any)
		for _, id := range removed {
			if id == created.ID {
				return true
			}
		}
		return false
	})
	after := resultOf(t, conn.call("workspace.list.snapshot", nil))
	if items, _ := after["workspaces"].([]any); len(items) != 0 {
		t.Fatalf("workspace.list.snapshot after delete = %v", after["workspaces"])
	}
}

// TestAskDomainFollowsTheBridge covers the pending-question snapshot and delta.
func TestAskDomainFollowsTheBridge(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	empty := resultOf(t, conn.call("ask.snapshot", nil))
	if empty["pending"] != nil {
		t.Fatalf("ask.snapshot with no question = %v", empty)
	}
	// The bridge parks a question; the domain must follow.
	answers := make(chan string, 1)
	go func() {
		service, _ := rig.gateway.root.Get(contracts.KeyUserInteraction)
		answer, err := service.Ask(context.Background(), "which file?", []string{"a", "b"})
		if err != nil {
			answers <- ""
			return
		}
		answers <- answer
	}()
	delta := conn.receive(func(method string) bool { return method == "ask.delta" })
	params, _ := delta["params"].(map[string]any)
	pending, _ := params["pending"].(map[string]any)
	if pending["text"] != "which file?" {
		t.Fatalf("ask.delta pending = %v", params)
	}
	snapshot := resultOf(t, conn.call("ask.snapshot", nil))
	snapshotPending, _ := snapshot["pending"].(map[string]any)
	if snapshotPending["id"] != pending["id"] {
		t.Fatalf("ask.snapshot = %v, delta = %v", snapshotPending, pending)
	}

	// Answering clears it.
	service, _ := rig.gateway.root.Get(contracts.KeyUserInteraction)
	service.Answer(pending["id"].(string), "b")
	cleared := conn.receive(func(method string) bool { return method == "ask.delta" })
	clearedParams, _ := cleared["params"].(map[string]any)
	if clearedParams["pending"] != nil {
		t.Fatalf("ask.delta after answer = %v", clearedParams)
	}
	if got := <-answers; got != "b" {
		t.Fatalf("Ask returned %q", got)
	}
}

// TestPlanDomainFollowsTheRenderer covers the plan snapshot and delta.
func TestPlanDomainFollowsTheRenderer(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	initial := resultOf(t, conn.call("plan.snapshot", nil))
	if initial["state"] != planIdle {
		t.Fatalf("initial plan state = %v", initial["state"])
	}
	reviewer, _ := rig.gateway.root.Get(contracts.KeyPlanReviewer)
	if _, err := reviewer.Review(context.Background(), "step 1\nstep 2"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	delta := conn.receive(func(method string) bool { return method == "plan.delta" })
	params, _ := delta["params"].(map[string]any)
	if params["state"] != planReviewing || params["content"] != "step 1\nstep 2" {
		t.Fatalf("plan.delta = %v", params)
	}
	snapshot := resultOf(t, conn.call("plan.snapshot", nil))
	if snapshot["state"] != planReviewing || snapshot["content"] != "step 1\nstep 2" {
		t.Fatalf("plan.snapshot = %v", snapshot)
	}
	if _, err := reviewer.Exit("approve"); err != nil {
		t.Fatalf("Exit: %v", err)
	}
	approved := conn.receive(func(method string) bool { return method == "plan.delta" })
	approvedParams, _ := approved["params"].(map[string]any)
	if approvedParams["state"] != planApproved {
		t.Fatalf("plan.delta after approve = %v", approvedParams)
	}
}

// TestArtifactsDomainAppendsDelta covers the artifact list domain.
func TestArtifactsDomainAppendsDelta(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	initial := resultOf(t, conn.call("artifacts.snapshot", nil))
	rev := uint64(initial["rev"].(float64))
	presenter, _ := rig.gateway.root.Get(contracts.KeyArtifactPresenter)
	if _, err := presenter.Present(context.Background(), "Report", "# body"); err != nil {
		t.Fatalf("Present: %v", err)
	}
	delta := conn.receive(func(method string) bool { return method == "artifacts.delta" })
	params, _ := delta["params"].(map[string]any)
	decoded := decodeItemSetDelta(t, params)
	if len(decoded.Added) != 1 {
		t.Fatalf("artifacts.delta = %v", params)
	}
	if uint64(params["baseRev"].(float64)) != rev {
		t.Fatalf("artifacts.delta baseRev = %v, want %d", params["baseRev"], rev)
	}
	after := resultOf(t, conn.call("artifacts.snapshot", nil))
	items, _ := after["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("artifacts.snapshot = %v", after["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["title"] != "Report" {
		t.Fatalf("artifact = %v", item)
	}
}

// TestApprovalsModeChangeForcesResync: the mode is a domain-level scalar, so a
// permission change is a resync, not a silent patch.
func TestApprovalsModeChangeForcesResync(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	before := resultOf(t, conn.call("approvals.snapshot", nil))
	rig.approvals.setMode(contracts.PermissionFull)
	delta := conn.receive(func(method string) bool { return method == "approvals.delta" })
	params, _ := delta["params"].(map[string]any)
	if params["resync"] != true {
		t.Fatalf("a mode change should resync, got %v", params)
	}
	after := resultOf(t, conn.call("approvals.snapshot", nil))
	if after["permission"] != contracts.PermissionFull {
		t.Fatalf("approvals.snapshot permission = %v", after["permission"])
	}
	if uint64(after["rev"].(float64)) == uint64(before["rev"].(float64)) {
		t.Fatal("the approvals rev did not move on a mode change")
	}
}

// TestDomainSubscribeResumesFromReplay pins the §6.5 resume path for the small
// domains: subscribing with an old rev replays the ring.
func TestDomainSubscribeResumesFromReplay(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	if err := rig.settings.Set("ui.font.scale", "large"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	delta := conn.receive(func(method string) bool { return method == "settings.delta" })
	params, _ := delta["params"].(map[string]any)
	deltaRev := uint64(params["rev"].(float64))
	baseRev := uint64(params["baseRev"].(float64))

	// A fresh connection resumes from baseRev and must get the same delta again.
	second := rig.connect(t)
	reply := resultOf(t, second.call("settings.subscribe", map[string]any{"sinceRev": baseRev}))
	if reply["resync"] != false {
		t.Fatalf("settings.subscribe from a replayable rev = %v, want no resync", reply)
	}
	replayed := second.receive(func(method string) bool { return method == "settings.delta" })
	replayedParams, _ := replayed["params"].(map[string]any)
	if uint64(replayedParams["rev"].(float64)) != deltaRev {
		t.Fatalf("replayed rev = %v, want %d", replayedParams["rev"], deltaRev)
	}
	// And from an unreachable rev it asks for a snapshot.
	stale := resultOf(t, second.call("settings.subscribe", map[string]any{"sinceRev": 999}))
	if stale["resync"] != true {
		t.Fatalf("settings.subscribe from the future = %v, want resync", stale)
	}
}

// decodeItemSetDelta decodes one wire item-set delta.
func decodeItemSetDelta(t *testing.T, params map[string]any) *itemSetDelta {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var delta itemSetDelta
	if err := json.Unmarshal(payload, &delta); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	return &delta
}

// TestTasksDomainReplayMatchesNextSnapshot is the per-domain replay law for the
// task board (§5.1.8): a client that took a snapshot, then applied every delta
// in order, must hold exactly what the next snapshot says.
//
// The board has no change notification, so the delta arrives on the poll: the
// test waits for the real notification instead of refreshing by hand, which is
// also what the browser experiences.
func TestTasksDomainReplayMatchesNextSnapshot(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	before := resultOf(t, conn.call("tasks.snapshot", nil))
	beforeRev := uint64(before["rev"].(float64))
	items, _ := before["tasks"].([]any)
	if len(items) != 0 {
		t.Fatalf("tasks.snapshot = %v, want an empty board", before["tasks"])
	}
	subscribed := resultOf(t, conn.call("tasks.subscribe", map[string]any{"sinceRev": beforeRev}))
	if subscribed["resync"] != false {
		t.Fatalf("tasks.subscribe from a warm rev = %v, want no resync", subscribed)
	}

	// Someone else (the agent's task tools) changes the board behind the
	// gateway's back: one new task, one status change.
	first := rig.tasks.seed("write the report", contracts.TaskStatusPending)
	second := rig.tasks.seed("audit the diff", contracts.TaskStatusInProgress)
	if _, err := rig.tasks.SetStatus(context.Background(), first.ID, contracts.TaskStatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// The board has no notification, so one poll covers both mutations: wait for
	// the delta it publishes and replay it onto the snapshot we took.
	replayed := items
	var deltaRev uint64
	for deltaRev <= beforeRev {
		delta := conn.receive(func(method string) bool { return method == "tasks.delta" })
		params, _ := delta["params"].(map[string]any)
		decoded := decodeItemSetDelta(t, params)
		replayed = applyItemSet(t, replayed, decoded)
		deltaRev = uint64(params["rev"].(float64))
	}
	_ = second

	after := resultOf(t, conn.call("tasks.snapshot", nil))
	if uint64(after["rev"].(float64)) != deltaRev {
		t.Fatalf("tasks.snapshot rev = %v, replayed up to %d", after["rev"], deltaRev)
	}
	fresh, _ := after["tasks"].([]any)
	if len(fresh) != 2 {
		t.Fatalf("tasks.snapshot after the changes = %v, want two tasks", after["tasks"])
	}
	byID := map[string]string{}
	for _, task := range replayed {
		row, _ := task.(map[string]any)
		byID[fmt.Sprint(row["id"])] = fmt.Sprint(row["status"])
	}
	for _, task := range fresh {
		row, _ := task.(map[string]any)
		id := fmt.Sprint(row["id"])
		if byID[id] != fmt.Sprint(row["status"]) {
			t.Fatalf("replayed status of %s = %q, snapshot says %q (replayed=%v snapshot=%v)",
				id, byID[id], row["status"], replayed, fresh)
		}
	}
	if len(byID) != len(fresh) {
		t.Fatalf("replayed %d tasks, snapshot has %d", len(byID), len(fresh))
	}
}

// TestTaskCommandsDriveTheBoard pins the write half: status moves a task and
// answers with the new rev + row, delete removes it, and the status vocabulary
// is validated (an unknown status is a parameter error, never a silent no-op).
func TestTaskCommandsDriveTheBoard(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	task := rig.tasks.seed("ship it", contracts.TaskStatusPending)
	before := resultOf(t, conn.call("tasks.snapshot", nil))
	beforeRev := uint64(before["rev"].(float64))

	reply := resultOf(t, conn.call("task.status", map[string]any{
		"id": task.ID, "status": contracts.TaskStatusInProgress,
	}))
	if uint64(reply["rev"].(float64)) <= beforeRev {
		t.Fatalf("task.status rev = %v, want > %d", reply["rev"], beforeRev)
	}
	row, _ := reply["task"].(map[string]any)
	if row["status"] != contracts.TaskStatusInProgress {
		t.Fatalf("task.status row = %v", row)
	}

	// The delta reaches a subscriber, and the snapshot agrees with the reply.
	delta := conn.receive(func(method string) bool { return method == "tasks.delta" })
	if params, _ := delta["params"].(map[string]any); len(params) == 0 {
		t.Fatalf("task.status produced no delta: %v", delta)
	}
	after := resultOf(t, conn.call("tasks.snapshot", nil))
	rows, _ := after["tasks"].([]any)
	if len(rows) != 1 {
		t.Fatalf("tasks.snapshot after status = %v", after["tasks"])
	}
	if got, _ := rows[0].(map[string]any); got["status"] != contracts.TaskStatusInProgress {
		t.Fatalf("snapshot status = %v", got["status"])
	}

	// "cancel" removes the row.
	resultOf(t, conn.call("task.delete", map[string]any{"id": task.ID}))
	final := resultOf(t, conn.call("tasks.snapshot", nil))
	if rows, _ := final["tasks"].([]any); len(rows) != 0 {
		t.Fatalf("tasks.snapshot after delete = %v, want none", rows)
	}

	// An unknown status is rejected, and the board keeps its value.
	protocolErr := errorOf(t, conn.call("task.status", map[string]any{"id": task.ID, "status": "done"}))
	if protocolErr["message"] == "" {
		t.Fatalf("task.status unknown status error = %v, want a message", protocolErr)
	}
}

// TestContextDomainReplayMatchesNextSnapshot is the per-domain replay law for the
// context timeline (§5.1.8): because the backend re-folds the whole view and only
// exposes a Revision, the delta is a whole replacement — so "apply the delta,
// then compare with the next snapshot" must hold trivially, and it is exactly
// what the client does.
func TestContextDomainReplayMatchesNextSnapshot(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	// A cold conversation has no recorded context: found=false, no numbers.
	cold := resultOf(t, conn.call("context.snapshot", map[string]any{"sessionId": "s1"}))
	if cold["found"] != false {
		t.Fatalf("cold context.snapshot = %v, want found=false", cold)
	}
	if _, ok := cold["context"]; ok {
		t.Fatalf("cold context.snapshot carries a context object: %v", cold)
	}
	beforeRev := uint64(cold["rev"].(float64))
	subscribed := resultOf(t, conn.call("context.subscribe", map[string]any{"sessionId": "s1", "sinceRev": beforeRev}))
	if subscribed["resync"] != false {
		t.Fatalf("context.subscribe from a warm rev = %v, want no resync", subscribed)
	}

	// The agent folds a provider call: the revision moves.
	rig.context.record("s1", 1234)
	delta := conn.receive(func(method string) bool { return method == "context.delta" })
	params, _ := delta["params"].(map[string]any)
	if params["found"] != true {
		t.Fatalf("context.delta = %v, want found=true", params)
	}
	if uint64(params["baseRev"].(float64)) != beforeRev {
		t.Fatalf("context.delta baseRev = %v, want %d", params["baseRev"], beforeRev)
	}
	deltaContext, _ := params["context"].(map[string]any)
	if deltaContext["used"] != float64(1234) {
		t.Fatalf("context.delta used = %v, want 1234", deltaContext["used"])
	}

	// Applying the delta means replacing the local view with it verbatim.
	after := resultOf(t, conn.call("context.snapshot", map[string]any{"sessionId": "s1"}))
	if after["found"] != true {
		t.Fatalf("context.snapshot after a fold = %v", after)
	}
	snapshotContext, _ := after["context"].(map[string]any)
	if fmt.Sprint(snapshotContext) != fmt.Sprint(deltaContext) {
		t.Fatalf("replayed view differs from the snapshot:\n replay=%v\n snapshot=%v", deltaContext, snapshotContext)
	}
	if uint64(after["rev"].(float64)) != uint64(params["rev"].(float64)) {
		t.Fatalf("rev mismatch: snapshot %v, delta %v", after["rev"], params["rev"])
	}

	// An unchanged revision must not move the domain again.
	rig.context.record("s1", 1234) // bumps revision, so this one does move
	second := conn.receive(func(method string) bool { return method == "context.delta" })
	secondParams, _ := second["params"].(map[string]any)
	if uint64(secondParams["rev"].(float64)) <= uint64(params["rev"].(float64)) {
		t.Fatalf("a second fold did not bump the domain rev: %v", secondParams)
	}
}

// TestContextSnapshotRequiresSession pins the parameter contract: the domain is
// per-conversation, so a missing sessionId is a parameter error (not an empty
// timeline that would render as "this conversation has no context").
func TestContextSnapshotRequiresSession(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	protocolErr := errorOf(t, conn.call("context.snapshot", map[string]any{}))
	if protocolErr["message"] == "" {
		t.Fatalf("context.snapshot without sessionId = %v, want an error", protocolErr)
	}
	protocolErr = errorOf(t, conn.call("context.subscribe", map[string]any{"sinceRev": 0}))
	if protocolErr["message"] == "" {
		t.Fatalf("context.subscribe without sessionId = %v, want an error", protocolErr)
	}
}

// TestInitializeAdvertisesCapabilities pins the handshake's capability list.
//
// Two things are asserted:
//   - the static set is exactly what §4.2.1 documents (so a capability cannot be
//     added or dropped silently);
//   - the assembly-dependent entries appear only when the backend half exists —
//     context (KeyContext), regenerate (SessionRegenerator) and tasks
//     (KeyTaskBoard) — because the client decides whether to render the Context
//     tab, the regenerate menu entry and the background-task chip from this list,
//     never by trying and failing.
//
// Run with -v to see the exact JSON the browser receives.
func TestInitializeAdvertisesCapabilities(t *testing.T) {
	initialize := func(rig *domainTestRig) []string {
		t.Helper()
		conn := dialTestWS(t, rig.address, map[string]string{
			"Origin":        rig.info.Origin(),
			"Authorization": "Bearer " + rig.info.Token,
		})
		t.Cleanup(conn.close)
		result := resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
		list, _ := result["capabilities"].([]any)
		out := make([]string, 0, len(list))
		for _, capability := range list {
			if name, ok := capability.(string); ok {
				out = append(out, name)
			}
		}
		encoded, _ := json.Marshal(list)
		t.Logf("initialize capabilities = %s", encoded)
		return out
	}

	// The full assembly: every conditional capability is present.
	full := startDomainGateway(t)
	defer full.stop(t)
	caps := initialize(full)
	for _, want := range []string{
		"session.list", "session.snapshot", "session.delta", "session.subscribe",
		"session.create", "run.cancel", "approvals", "ask", "plan", "artifacts",
		"settings", "settings.set", "workspace", "prompts", "notify", "foreground",
		"context", "regenerate", "edit", "tasks",
	} {
		if !containsString(caps, want) {
			t.Fatalf("capabilities = %v, want it to contain %q", caps, want)
		}
	}
	if len(caps) != len(serverCapabilities)+4 {
		t.Fatalf("capabilities = %v, want %d static + 4 conditional entries", caps, len(serverCapabilities))
	}

	// The static set is what the document lists: a capability added to (or
	// dropped from) the code without touching §4.2.1 fails here.
	wantStatic := []string{
		"session.list", "session.snapshot", "session.delta", "session.subscribe",
		"session.create", "run.cancel", "approvals", "ask", "plan", "artifacts",
		"settings", "settings.set", "workspace", "prompts", "notify", "foreground",
		"inbox", "triage", "tasklease", "approvalaction", "notifyroute", "webhook", "lead", "credentials",
	}
	sortedStatic := append([]string(nil), serverCapabilities...)
	sort.Strings(sortedStatic)
	wantSorted := append([]string(nil), wantStatic...)
	sort.Strings(wantSorted)
	if !slices.Equal(sortedStatic, wantSorted) {
		t.Fatalf("serverCapabilities = %v, want %v (§4.2.1)", sortedStatic, wantSorted)
	}

	// A bare gateway (chat present, but no KeyContext/KeyTaskBoard and a chat
	// that cannot regenerate) must advertise the static set only.
	chat := &fakeChatOnly{newFakeChat()}
	settings := newFakeSettings(nil)
	root := cordisRootFor(t, chat, nil, settings, nil, nil, nil)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	bare := &domainTestRig{runningGateway: serveGateway(t, gateway)}
	defer bare.stop(t)
	bareCaps := initialize(bare)
	for _, absent := range []string{"context", "regenerate", "edit", "tasks"} {
		if containsString(bareCaps, absent) {
			t.Fatalf("a bare assembly advertised %q: %v", absent, bareCaps)
		}
	}
	if len(bareCaps) != len(serverCapabilities) {
		t.Fatalf("bare capabilities = %v, want the %d static entries", bareCaps, len(serverCapabilities))
	}
}
