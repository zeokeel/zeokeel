package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

// startDomainGateway starts a gateway with the small-domain backends.
func startDomainGateway(t *testing.T) *domainTestRig {
	t.Helper()
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionWorkspace})
	approvals := newFakeApprovals(contracts.PermissionWorkspace)
	approvals.settings = settings
	workspace := newFakeWorkspace()

	root := cordisRootFor(t, chat, approvals, settings, workspace)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gateway.pollInterval = 20 * time.Millisecond
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	served := serveGateway(t, gateway)
	return &domainTestRig{runningGateway: served, chat: chat, approvals: approvals, settings: settings, workspace: workspace}
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

	// Recolor: a "changed" delta.
	rig.workspace.setColor(created.ID, "violet")
	changeDelta := conn.receive(func(method string) bool { return method == "workspace.list.delta" })
	changeParams, _ := changeDelta["params"].(map[string]any)
	changed, _ := changeParams["changed"].([]any)
	if len(changed) != 1 {
		t.Fatalf("workspace change delta = %v", changeParams)
	}
	item, _ := changed[0].(map[string]any)
	if item["color"] != "violet" {
		t.Fatalf("changed item = %v", item)
	}

	// Delete: a removed delta, and the snapshot agrees.
	if err := rig.workspace.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	removeDelta := conn.receive(func(method string) bool { return method == "workspace.list.delta" })
	removeParams, _ := removeDelta["params"].(map[string]any)
	removed, _ := removeParams["removed"].([]any)
	if len(removed) != 1 || removed[0] != created.ID {
		t.Fatalf("workspace remove delta = %v", removeParams)
	}
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
