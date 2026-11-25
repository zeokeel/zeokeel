package assembly_test

// Assembly-root tests. They exercise the embedded products end to end against a
// real kernel root: the products load the backend modules only,
// headless loads the backend-only product on a root that has no UI services and
// no display environment. Everything here is headless-safe: uiroot is pure Go
// state, and the desktop test never creates a mygo window.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"time"

	"errors"
	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"
	"slices"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
	"github.com/zeokeel/zeokeel/internal/modules/webhook"
)

// --- host service fakes (the process host provides these on the root) ---

type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: make(map[string]string)}
}

func (f *fakeSettings) Get(key, def string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.values[key]; ok {
		return value
	}
	return def
}

func (f *fakeSettings) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
	return nil
}

func (f *fakeSettings) All() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.values))
	for key, value := range f.values {
		out[key] = value
	}
	return out
}

type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (f *fakeLogger) Printf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, format)
}

// --- helpers ---

func newRoot(t *testing.T) cordis.Context {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	return root
}

func provide[T any](t *testing.T, root cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := root.Provide(key, value); err != nil {
		t.Fatalf("provide %q: %v", key.Name(), err)
	}
}

// provideHostServices provides the services every product's host provides:
// data dir, settings and log. The browser transport's own services are the
// gateway's business (internal/gateway), not this package's.
func provideHostServices(t *testing.T, root cordis.Context) {
	t.Helper()
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	var settings contracts.SettingsService = newFakeSettings()
	provide(t, root, contracts.KeySettings, settings)
	var logger contracts.Logger = &fakeLogger{}
	provide(t, root, contracts.KeyLogger, logger)
}

// --- products and registry ---

func TestProductsAndEntries(t *testing.T) {
	// desktop.json is gone with the native UI: the embedded assemblies are the
	// backend-only headless one, and serve reuses its entries.
	want := []string{assembly.ProductHeadless}
	if got := assembly.Products(); !slices.Equal(got, want) {
		t.Fatalf("Products() = %v, want %v", got, want)
	}
	wantAll := []string{assembly.ProductHeadless, assembly.ProductServe}
	if got := assembly.AllProducts(); !slices.Equal(got, wantAll) {
		t.Fatalf("AllProducts() = %v, want %v", got, wantAll)
	}
	for _, product := range []string{"headless", "headless.json"} {
		if _, err := assembly.Entries(product); err != nil {
			t.Fatalf("Entries(%q): %v", product, err)
		}
	}
	if _, err := assembly.Entries("mobile"); err == nil {
		t.Fatal("Entries(unknown product) succeeded")
	} else if !strings.Contains(err.Error(), "unknown product") {
		t.Fatalf("Entries(unknown product) = %v, want an unknown-product error", err)
	}
	// The retired windowed product must not resolve any more.
	if _, err := assembly.Entries("desktop"); err == nil {
		t.Fatal("Entries(desktop) still succeeds after the native UI retirement")
	}

	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	// Presence, not order: inserting a module must not fail an unrelated test
	// (2026-11-25 audit finding F1). Order is the assembly document's business.
	wantHeadless := []string{
		"zeokeel/appinfo", "zeokeel/sessionstore", "zeokeel/inbox", "zeokeel/goal", "zeokeel/approvals", "zeokeel/workspace",
		"zeokeel/insights",
		"zeokeel/skills", "zeokeel/sandbox", "zeokeel/toolkit", "zeokeel/triage", "zeokeel/lead", "zeokeel/tasklease", "zeokeel/notifyroute", "zeokeel/credentials", "zeokeel/webhook", "zeokeel/wechat", "zeokeel/feishu", "zeokeel/dingtalk", "zeokeel/email", "zeokeel/heartbeat", "zeokeel/browser", "zeokeel/hosttools", "zeokeel/memorystore", "zeokeel/teamnotes",
		"zeokeel/agent", "zeokeel/automation", "zeokeel/update", "zeokeel/importer", "zeokeel/acp",
	}
	loaded := make(map[string]bool, len(headless))
	for _, entry := range headless {
		loaded[entry.Module] = true
	}
	for _, want := range wantHeadless {
		if !loaded[want] {
			t.Errorf("headless assembly is missing %q", want)
		}
	}
	if len(headless) != len(wantHeadless) {
		t.Errorf("headless entries = %d, want %d (update wantHeadless when adding a module)", len(headless), len(wantHeadless))
	}
}

// TestHeadlessPinsPermissionSensitiveModules pins the shared-store contract
// (2026-11 audit): headless/ACP must not follow a desktop permission preset
// left in a shared settings store — BOTH the approval gate (approvals) and the
// OS sandbox mode (toolkit) pin their assembly defaults via
// ignorePermissionSetting. Desktop keeps following the live preset.
func TestHeadlessPinsPermissionSensitiveModules(t *testing.T) {
	configOf := func(entries []module.Entry, id string) map[string]any {
		t.Helper()
		for _, entry := range entries {
			if entry.ID == id {
				config, _ := entry.Config.(map[string]any)
				return config
			}
		}
		t.Fatalf("entry %q not found", id)
		return nil
	}
	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	for _, id := range []string{"approvals", "toolkit"} {
		if pinned, _ := configOf(headless, id)["ignorePermissionSetting"].(bool); !pinned {
			t.Errorf("headless %s must set ignorePermissionSetting=true", id)
		}
	}
	// serve reuses the headless entries with its own overrides; its approvals
	// override is checked in serve_test.go.
}

func TestRegistryIsExplicit(t *testing.T) {
	registry := assembly.Registry()
	for _, id := range []string{
		"zeokeel/appinfo",
		"zeokeel/toolkit",
		"zeokeel/agent",
		"zeokeel/hosttools",
		"zeokeel/automation",
		"zeokeel/update",
		"zeokeel/importer",
		"zeokeel/acp",
		"zeokeel/modelcatalog",
		"zeokeel/insights",
		"zeokeel/sessionstore",
		"zeokeel/goal",
		"zeokeel/workspace",
		"zeokeel/skills",
		"zeokeel/approvals",
		"zeokeel/sandbox",
	} {
		if !registry.Has(id) {
			t.Fatalf("Registry() lacks module %q", id)
		}
	}
	// The native UI's twelve modules are not part of any product any more.
	for _, id := range []string{"zeokeel-ui/home", "zeokeel-ui/session", "zeokeel-ui/settings"} {
		if registry.Has(id) {
			t.Fatalf("Registry() still registers the retired UI module %q", id)
		}
	}
	// No exact count: a count assertion is a change-detector (audit finding F2).
	// What matters is that the core modules are present and the retired UI ones
	// are not, both checked above / below.
	for _, id := range []string{"zeokeel/sessionstore", "zeokeel/inbox", "zeokeel/toolkit", "zeokeel/triage", "zeokeel/agent"} {
		if !registry.Has(id) {
			t.Errorf("Registry() is missing the required module %q", id)
		}
	}
}

func TestValidateEmbeddedProducts(t *testing.T) {
	for _, product := range assembly.Products() {
		if err := assembly.Validate(product); err != nil {
			t.Errorf("Validate(%s): %v", product, err)
		}
	}
}

// --- headless product: backend modules load, close unwinds ---

// TestHeadlessAssemblyLoadsAndUnwinds is the load/unload contract of the one
// remaining embedded assembly: every backend module settles, its services are
// reachable, and Close withdraws them and unregisters every tool the module
// contributed. (The windowed product's UI group is gone with the native UI.)
func TestHeadlessAssemblyLoadsAndUnwinds(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{
		Version:   "9.9.9",
		Commit:    "deadbeef",
		BuildTime: "2026-11-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless): %v", err)
	}

	instances := loader.Instances()
	wantIDs := []string{
		"appinfo", "store", "inbox", "goal", "approvals", "workspace", "insights", "skills", "sandbox", "toolkit", "triage", "lead", "tasklease", "notifyroute", "creds", "webhook", "wechat", "feishu", "dingtalk", "email", "heartbeat", "browser", "hosttools", "memorystore", "teamnotes",
		"agent", "automation", "update", "importer", "acp",
	}
	gotIDs := make([]string, 0, len(instances))
	for _, instance := range instances {
		gotIDs = append(gotIDs, instance.ID())
		if state := instance.State(); state != cordis.Active {
			t.Fatalf("instance %q state = %s, want Active", instance.ID(), state)
		}
	}
	for _, want := range wantIDs {
		if !slices.Contains(gotIDs, want) {
			t.Errorf("headless instance %q is not loaded", want)
		}
	}
	if len(gotIDs) != len(wantIDs) {
		t.Errorf("headless instances = %v, want %v", gotIDs, wantIDs)
	}
	if warnings := loader.Warnings(); len(warnings) != 0 {
		t.Fatalf("headless warnings = %v, want none", warnings)
	}

	// The toolkit registry is the product's shared tool catalog: the hosttools
	// module registered its host tools into it during activation.
	toolkitRegistry, ok := root.Get(hosttools.KeyRegistry)
	if !ok {
		t.Fatal("tool registry service is not available on the root")
	}
	// headless.json sets hosttools interactive=false, so the plan-review pair is
	// intentionally absent: nothing could approve a plan. serve flips this (see
	// serve_test.go).
	hostToolNames := []string{"ask_user", "create_goal", "get_goal", "present", "update_goal"}
	for _, name := range hostToolNames {
		found := false
		for _, spec := range toolkitRegistry.Specs() {
			if spec.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("toolkit registry lacks host tool %q", name)
		}
	}

	// Inbox wiring: the assembly really provides the single external entry
	// point, and its rules work through the service the gateway will consume.
	// This is the interconnection check — a module that boots but provides
	// nothing (or provides a different key) fails here, not in production.
	inboxService, ok := root.Get(contracts.KeyInbox)
	if !ok || inboxService == nil {
		t.Fatal("KeyInbox service is not available on the root")
	}
	storedItem, merged, err := inboxService.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", Channel: "dm-1", Sender: "owner", DedupKey: "assembly-1", Title: "assembly check",
	})
	if err != nil || merged {
		t.Fatalf("inbox Add = (%+v, %v, %v), want a fresh item", storedItem, merged, err)
	}
	if _, merged, err := inboxService.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", Channel: "dm-1", Sender: "owner", DedupKey: "assembly-1", Title: "assembly check",
	}); err != nil || !merged {
		t.Fatalf("inbox dedupe through the assembly = (merged %v, err %v), want merged", merged, err)
	}
	if updated, err := inboxService.Triage(context.Background(), []string{storedItem.ID}, contracts.InboxActionTask, []string{"task-1"}); err != nil || updated != 1 {
		t.Fatalf("inbox Triage through the assembly = (%d, %v), want (1, nil)", updated, err)
	}

	// The WeChat adapter is wired but has no login token in a headless
	// assembly, so the honest answer is "unauthorized" — not a fake success.
	wechatService, ok := root.Get(contracts.KeyWeChat)
	if !ok || wechatService == nil {
		t.Fatal("KeyWeChat service is not available on the root")
	}
	if _, err := wechatService.PollOnce(context.Background()); !errors.Is(err, contracts.ErrWeChatUnauthorized) {
		t.Fatalf("wechat PollOnce without a token = %v, want ErrWeChatUnauthorized", err)
	}
	wechatStatus, err := wechatService.Status(context.Background())
	if err != nil || wechatStatus.Connected {
		t.Fatalf("wechat status = (%+v, %v), want disconnected", wechatStatus, err)
	}

	// The credential store is wired under the same data dir the adapters will
	// read from: a secret written here must come back, and List must not carry it.
	creds, ok := root.Get(contracts.KeyCredentials)
	if !ok || creds == nil {
		t.Fatal("KeyCredentials service is not available on the root")
	}
	if err := creds.Set(context.Background(), contracts.Credential{
		Kind: "wechat", ID: "default", Fields: map[string]string{"token": "assembly-secret"},
	}); err != nil {
		t.Fatalf("credentials Set: %v", err)
	}
	credential, exists, getErr := creds.Get(context.Background(), "wechat", "default")
	if getErr != nil || !exists || credential.Fields["token"] != "assembly-secret" {
		t.Fatalf("credentials Get = (%+v, %v, %v)", credential, exists, getErr)
	}
	infos, listErr := creds.List(context.Background(), "wechat")
	if listErr != nil || len(infos) != 1 || infos[0].ID != "default" {
		t.Fatalf("credentials List = (%+v, %v)", infos, listErr)
	}

	// The other two channel adapters are wired too, and both answer honestly
	// before any credential is configured.
	dingtalkService, ok := root.Get(contracts.KeyDingTalk)
	if !ok || dingtalkService == nil {
		t.Fatal("KeyDingTalk service is not available on the root")
	}
	if status, err := dingtalkService.Status(context.Background()); err != nil || status.Connected {
		t.Fatalf("dingtalk status = (%+v, %v), want disconnected", status, err)
	}
	feishuService, ok := root.Get(contracts.KeyFeishu)
	if !ok || feishuService == nil {
		t.Fatal("KeyFeishu service is not available on the root")
	}
	if challenge, ok := feishuService.VerifyURL([]byte(`{"type":"url_verification","challenge":"abc"}`)); !ok || challenge != "abc" {
		t.Fatalf("feishu VerifyURL = (%q, %v), want the challenge echoed", challenge, ok)
	}

	// Shared team notes are wired over the assembled memory store: a note must
	// land and come back (the URI lives inside the store's memory subtree).
	teamNotes, ok := root.Get(contracts.KeyTeamNotes)
	if !ok || teamNotes == nil {
		t.Fatal("KeyTeamNotes service is not available on the root")
	}
	if _, err := teamNotes.Record(context.Background(), contracts.TeamNote{
		ID: "n1", TaskID: "assembly-note", Title: "assembly note", Body: "landed", Author: "bot-1",
	}); err != nil {
		t.Fatalf("teamnotes Record through the assembly: %v", err)
	}

	// The autonomous loop is running: one pass through the assembled services
	// must reach the lead and the channels (and never error on a bare assembly).
	heartbeatService, ok := root.Get(contracts.KeyHeartbeat)
	if !ok || heartbeatService == nil {
		t.Fatal("KeyHeartbeat service is not available on the root")
	}
	if _, err := heartbeatService.Tick(context.Background()); err != nil {
		t.Fatalf("heartbeat Tick: %v", err)
	}

	// The browser is wired without an endpoint: it reports disconnected instead
	// of pretending a Chrome is attached.
	browserService, ok := root.Get(contracts.KeyBrowser)
	if !ok || browserService == nil {
		t.Fatal("KeyBrowser service is not available on the root")
	}
	if status, err := browserService.Status(context.Background()); err != nil || status.Connected {
		t.Fatalf("browser status = (%+v, %v), want disconnected", status, err)
	}

	// Email is wired too, and honestly disconnected without a mailbox config.
	emailService, ok := root.Get(contracts.KeyEmail)
	if !ok || emailService == nil {
		t.Fatal("KeyEmail service is not available on the root")
	}
	if status, err := emailService.Status(context.Background()); err != nil || status.Connected {
		t.Fatalf("email status = (%+v, %v), want disconnected", status, err)
	}

	// The triage pass is wired over the same inbox and triage services: with no
	// rules configured it must scan a fresh item and decline it, never lose it.
	leadService, ok := root.Get(contracts.KeyLead)
	if !ok || leadService == nil {
		t.Fatal("KeyLead service is not available on the root")
	}
	if _, _, err := inboxService.Add(context.Background(), contracts.InboxItem{
		Source: "webhook", Channel: "lead-check", Sender: "owner", DedupKey: "lead-check", Title: "lead check",
	}); err != nil {
		t.Fatalf("inbox Add for the lead pass: %v", err)
	}
	leadReport, err := leadService.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("lead RunOnce through the assembly: %v", err)
	}
	if leadReport.Scanned < 1 || leadReport.Failed != 0 {
		t.Fatalf("lead report = %+v, want at least one scanned and no failures", leadReport)
	}

	// The webhook adapter is the first channel. The headless assembly ships
	// with no configured sources (a secret does not belong in the assembly
	// document), so the wiring proof is the typed refusal: the assembled
	// adapter really consults its source table and never a missing service.
	webhookService, ok := root.Get(contracts.KeyWebhook)
	if !ok || webhookService == nil {
		t.Fatal("KeyWebhook service is not available on the root")
	}
	if _, err := webhookService.Receive(context.Background(), "github", "", []byte(`{"title":"x"}`)); !errors.Is(err, contracts.ErrWebhookUnknownSource) {
		t.Fatalf("webhook Receive with no configured source = %v, want ErrWebhookUnknownSource", err)
	}

	// Notification routing is pure policy: a bare gateway must still be able to
	// ask "where does an approval go?" and get the in-app terminal channel.
	router, ok := root.Get(contracts.KeyNotifyRoute)
	if !ok || router == nil {
		t.Fatal("KeyNotifyRoute service is not available on the root")
	}
	plan, err := router.Plan(context.Background(), contracts.NotifyCategoryApproval, time.Now(), nil)
	if err != nil || len(plan.Channels) == 0 || plan.Channels[len(plan.Channels)-1] != contracts.NotifyChannelInApp {
		t.Fatalf("notify plan through the assembly = (%+v, %v), want in-app", plan, err)
	}

	// The approval action layer shares the approvals queue. Headless runs in
	// "auto" mode, so nothing is pending: the wiring proof is the typed error.
	approvalAction, ok := root.Get(contracts.KeyApprovalAction)
	if !ok || approvalAction == nil {
		t.Fatal("KeyApprovalAction service is not available on the root")
	}
	if _, err := approvalAction.ApplyReply(context.Background(), "同意"); !errors.Is(err, contracts.ErrApprovalNoPending) {
		t.Fatalf("ApplyReply without a pending approval = %v, want ErrApprovalNoPending", err)
	}

	// The three-module chain: an inbound item becomes a real board task through
	// triage, and the inbox row points at the same id. This is the seam the
	// product depends on — if any of the three is wired to a different key, this
	// fails instead of silently creating orphan work.
	triageService, ok := root.Get(contracts.KeyTriage)
	if !ok || triageService == nil {
		t.Fatal("KeyTriage service is not available on the root")
	}
	board, ok := root.Get(contracts.KeyTaskBoard)
	if !ok || board == nil {
		t.Fatal("KeyTaskBoard service is not available on the root")
	}
	arrival, _, err := inboxService.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", Channel: "dm-2", Sender: "owner", DedupKey: "assembly-chain", Title: "chain",
	})
	if err != nil {
		t.Fatalf("inbox Add for the chain: %v", err)
	}
	decision, err := triageService.Apply(context.Background(), arrival.ID, contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "assembly chain task", Assignee: "bot-1"},
	})
	if err != nil || !decision.Created || len(decision.TaskIDs) != 1 {
		t.Fatalf("triage Apply through the assembly = (%+v, %v), want one created task", decision, err)
	}
	boardTasks, err := board.List(context.Background())
	if err != nil {
		t.Fatalf("board List: %v", err)
	}
	found := false
	for _, task := range boardTasks {
		if task.ID == decision.TaskIDs[0] {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the task %q created by triage is not on the board", decision.TaskIDs[0])
	}
	marked, err := inboxService.Get(context.Background(), arrival.ID)
	if err != nil || marked.Status != contracts.InboxStatusTask {
		t.Fatalf("inbox row after triage = (%+v, %v), want status task", marked, err)
	}

	// The fourth module: the claim layer. It must move the same board task
	// through claim → review → done while keeping the board in sync.
	leaseService, ok := root.Get(contracts.KeyTaskLease)
	if !ok || leaseService == nil {
		t.Fatal("KeyTaskLease service is not available on the root")
	}
	taskID := decision.TaskIDs[0]
	claimed, err := leaseService.Claim(context.Background(), taskID, "bot-1")
	if err != nil || claimed.Owner != "bot-1" {
		t.Fatalf("lease Claim through the assembly = (%+v, %v), want bot-1", claimed, err)
	}
	if _, err := leaseService.Submit(context.Background(), taskID, "bot-1", []contracts.Evidence{{Kind: "link", Ref: "https://example.test"}}); err != nil {
		t.Fatalf("lease Submit through the assembly: %v", err)
	}
	accepted, err := leaseService.Accept(context.Background(), taskID, "lead", "verified")
	if err != nil || accepted.State != contracts.LeaseStateDone {
		t.Fatalf("lease Accept through the assembly = (%+v, %v), want done", accepted, err)
	}
	boardTasks, err = board.List(context.Background())
	if err != nil {
		t.Fatalf("board List after accept: %v", err)
	}
	for _, task := range boardTasks {
		if task.ID == taskID && task.Status != contracts.TaskStatusCompleted {
			t.Fatalf("board task %q status = %q after accept, want completed", taskID, task.Status)
		}
	}

	// Goal v2: the durable per-session goal backend is assembled and the DSH
	// goal trio reaches it through the toolkit registry.
	goalService, ok := root.Get(contracts.KeyGoal)
	if !ok || goalService == nil {
		t.Fatal("KeyGoal service is not available on the root")
	}
	store, ok := root.Get(contracts.KeyStore)
	if !ok || store == nil {
		t.Fatal("KeyStore service is not available on the root")
	}
	if err := store.CreateSession(context.Background(), "sess-goal-assembly", "goal test"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	created, err := goalService.CreateGoal(context.Background(), "sess-goal-assembly", "ship the release", 0)
	if err != nil {
		t.Fatalf("CreateGoal on the assembled backend: %v", err)
	}
	if created.Revision != 1 || created.MaxGoalRounds != contracts.DefaultMaxGoalRounds ||
		created.Activation != contracts.GoalActivationArmed {
		t.Fatalf("assembled CreateGoal = %+v, want revision 1 / 256 rounds / armed", created)
	}
	goalCtx := contracts.WithAgentSession(context.Background(), "sess-goal-assembly")
	result, err := toolkitRegistry.Invoke(goalCtx, rig.ToolCall{ID: "goal-call", Name: "get_goal", Arguments: "{}"})
	if err != nil || result.IsError {
		t.Fatalf("get_goal through the assembled registry = %q / %v", result.Content, err)
	}
	if !strings.Contains(result.Content, created.ID) {
		t.Fatalf("get_goal content = %q, want the created goal %q", result.Content, created.ID)
	}
	// Deleting the session must not let a later goal round resurrect it.
	if err := store.DeleteSession(context.Background(), "sess-goal-assembly"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := goalService.AdmitRound(context.Background(), "sess-goal-assembly"); !errors.Is(err, contracts.ErrGoalNotFound) {
		t.Fatalf("AdmitRound on a deleted session = %v, want ErrGoalNotFound", err)
	}
	sessions, err := store.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	for _, session := range sessions {
		if session.ID == "sess-goal-assembly" {
			t.Fatal("a goal event resurrected the deleted session row")
		}
	}

	// The agent provides the context inspector the browser's Context tab reads.
	if inspector, ok := root.Get(contracts.KeyContext); !ok || inspector == nil {
		t.Fatal("KeyContext service is not available on the root")
	}

	// Build identity from the host was overlaid onto the appinfo module config.
	info, ok := root.Get(contracts.KeyAppInfo)
	if !ok {
		t.Fatal("KeyAppInfo service is not available on the root")
	}
	if info.Version != "9.9.9" || info.Commit != "deadbeef" || info.BuildTime != "2026-11-06T12:00:00Z" {
		t.Fatalf("appinfo = %+v, want the injected build identity", info)
	}

	// Closing the loader disposes the assembly: every service is withdrawn and
	// every contributed tool is unregistered through its kernel effect.
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if _, ok := root.Get(contracts.KeyAppInfo); ok {
		t.Fatal("appinfo service survived loader.Close")
	}
	for _, spec := range toolkitRegistry.Specs() {
		if slices.Contains(hostToolNames, spec.Name) {
			t.Fatalf("host tool %q survived loader.Close", spec.Name)
		}
	}
	if got := len(toolkitRegistry.Specs()); got == 0 {
		t.Fatal("toolkit module's own tools disappeared with the host tools")
	}
}

// --- headless product: loads without any UI service or display environment ---

// TestHeadlessAssemblyHasNoStubTeamTool is the FR-14 regression: the legacy
// hosttools in-memory "team" stub is gone, while the real rig team tools that
// the agent module registers (against contracts.KeyTeam) stay available.
func TestHeadlessAssemblyHasNoStubTeamTool(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{Version: "test"})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless): %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	registry, ok := root.Get(hosttools.KeyRegistry)
	if !ok || registry == nil {
		t.Fatal("tool registry service is not available on the root")
	}
	names := make(map[string]bool, 128)
	for _, spec := range registry.Specs() {
		names[spec.Name] = true
	}
	if names["team"] {
		t.Fatal("the legacy hosttools stub team tool is still registered")
	}
	// The real team surface (agent module + rig team manager) is still there.
	for _, name := range []string{
		"spawn_teammate", "list_agents", "wait_agent", "send_message", "interrupt_agent", "team_task_create",
	} {
		if !names[name] {
			t.Fatalf("real team tool %q missing after removing the stub (have %v)", name, names)
		}
	}
	// The remaining host tools are unchanged.
	for _, name := range []string{"ask_user", "present", "get_goal", "create_goal", "update_goal"} {
		if !names[name] {
			t.Fatalf("host tool %q missing", name)
		}
	}
}

func TestHeadlessAssemblyNeedsNoUIServices(t *testing.T) {
	root := newRoot(t)
	// Deliberately no uiroot: the headless host provides settings and log only.
	provideHostServices(t, root)

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless): %v", err)
	}
	defer func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	}()

	instances := loader.Instances()
	if len(instances) == 0 {
		t.Fatal("headless loaded no instances")
	}
	instance := instances[0]
	if instance.ID() != "appinfo" || instance.ModuleID() != "zeokeel/appinfo" {
		t.Fatalf("headless instance = %s/%s, want appinfo/zeokeel/appinfo", instance.ID(), instance.ModuleID())
	}
	if state := instance.State(); state != cordis.Active {
		t.Fatalf("appinfo state = %s, want Active", state)
	}
	// Look up by id: an index would break the moment a module is inserted
	// before it (audit finding F1).
	var hostInstance *module.Instance
	for _, instance := range instances {
		if instance.ID() == "hosttools" {
			hostInstance = instance
			break
		}
	}
	if hostInstance == nil {
		t.Fatal("headless did not load hosttools")
	}
	if hostInstance.ModuleID() != "zeokeel/hosttools" {
		t.Fatalf("hosttools module = %s, want zeokeel/hosttools", hostInstance.ModuleID())
	}
	if state := hostInstance.State(); state != cordis.Active {
		t.Fatalf("hosttools state = %s, want Active", state)
	}
	// The non-interactive product keeps the host tools that can still act
	// without a UI (ask_user degrades to a readable message; present/goal/team
	// keep their in-process behavior) but hides the plan-review pair: a plan
	// submitted for review could never be approved in a headless session, so
	// exposing plan_review/exit_plan_mode only dead-ends the agent.
	toolkitRegistry, ok := root.Get(hosttools.KeyRegistry)
	if !ok {
		t.Fatal("tool registry service is not available on the headless root")
	}
	registered := make(map[string]bool, len(toolkitRegistry.Specs()))
	for _, spec := range toolkitRegistry.Specs() {
		registered[spec.Name] = true
	}
	for _, name := range []string{"ask_user", "get_goal", "create_goal", "update_goal", "present"} {
		if !registered[name] {
			t.Fatalf("headless toolkit registry lacks host tool %q", name)
		}
	}
	if registered["goal"] {
		t.Fatal("headless toolkit registry still exposes the retired Goal v1 `goal` tool")
	}
	for _, name := range []string{"plan_review", "exit_plan_mode"} {
		if registered[name] {
			t.Fatalf("headless toolkit registry exposes plan tool %q, want it hidden", name)
		}
	}
	if warnings := loader.Warnings(); len(warnings) != 0 {
		t.Fatalf("headless warnings = %v, want none", warnings)
	}
	// The product genuinely has no UI: no UI service was provided and the
	// headless assembly never asks for one.
	// No window-owned service is provided at all any more: the browser
	// transport provides its own (gateway.Provide), and the process host
	// contributes only data dir/settings/log.
	if _, ok := root.Get(contracts.KeyNavigator); ok {
		t.Fatal("the headless root unexpectedly has a navigator service")
	}
	if _, ok := root.Get(contracts.KeyTheme); ok {
		t.Fatal("the headless root unexpectedly has a theme service")
	}
	info, ok := root.Get(contracts.KeyAppInfo)
	if !ok {
		t.Fatal("appinfo service is not available")
	}
	if info.Version != "0.1.0" {
		t.Fatalf("headless appinfo version = %q, want the injected 0.1.0", info.Version)
	}
}

// --- pre-flight failures happen before any fiber is created ---

func TestPreflightValidationFailures(t *testing.T) {
	appinfoEntry := module.Entry{ID: "appinfo", Module: "zeokeel/appinfo"}
	// The agent requires the session store, the approvals service and the shared
	// tool registry (the serve host provides the last one): with only the agent
	// in the tree nothing provides them, which is the missing-provider case.
	agentEntry := module.Entry{ID: "agent", Module: "zeokeel/agent"}

	tests := []struct {
		name    string
		entries []module.Entry
		want    string
	}{
		{
			name:    "unknown module",
			entries: []module.Entry{{ID: "ghost", Module: "zeokeel/missing"}},
			want:    "unknown module",
		},
		{
			name: "duplicate instance id",
			entries: []module.Entry{
				appinfoEntry,
				{ID: "appinfo", Module: "zeokeel/appinfo"},
			},
			want: "duplicate instance ID",
		},
		{
			name:    "missing provider",
			entries: []module.Entry{agentEntry},
			want:    "no enabled entry in the assembly provides",
		},
		{
			name: "rejected config",
			entries: []module.Entry{{
				ID:     "appinfo",
				Module: "zeokeel/appinfo",
				Config: map[string]any{"version": 42},
			}},
			want: "decode config",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := assembly.ValidateEntries(test.entries)
			if err == nil {
				t.Fatalf("ValidateEntries(%s) succeeded, want a pre-flight error", test.name)
			}
			var validation *module.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error type = %T, want *module.ValidationError", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

// A pre-flight failure must not leave a partially loaded tree or any residue:
// the same root can load the valid product afterwards.
func TestPreflightFailureLeavesRootUsable(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)

	if err := assembly.ValidateEntries([]module.Entry{{ID: "ghost", Module: "zeokeel-ui/ghost"}}); err == nil {
		t.Fatal("invalid entry tree passed validation")
	}
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless) after a failed preflight: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
}

// TestMain lets the OS-sandbox helper re-exec this test binary: the assembly
// loads the always-on sandbox module, and on hosts without bwrap its Landlock
// fallback spawns this executable as a hidden helper (otherwise the whole test
// suite would recursively re-run inside the helper).
func TestMain(m *testing.M) {
	if osrunner.MaybeRunHelper(os.Args) {
		return
	}
	os.Exit(m.Run())
}

// A module can be registered and never composed. That is exactly how the Insights
// screen died: `zeokeel/insights` was in Registry() but in no product's entries, so
// `KeyInsights` was never provided at runtime and the screen's endpoint answered
// with zeros. This test makes the gap impossible to leave silently.
func TestEveryRegisteredModuleIsComposedIntoAProduct(t *testing.T) {
	entries, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	composed := make(map[string]bool, len(entries))
	for _, entry := range entries {
		composed[entry.Module] = true
	}
	// Registered on purpose, composed on purpose never: nothing consumes
	// KeyModelCatalog (the agent reads the modelcatalog.models settings row
	// directly), so composing it would add a settings migration with no reader.
	intentionallyUncomposed := map[string]string{
		"zeokeel/modelcatalog": "no consumer of KeyModelCatalog: the agent reads the modelcatalog.models settings row directly",
		"zeokeel/host/group":   "a no-op container that only carries an Entry.Children subtree; no product nests one since the UI group retired",
	}
	for _, info := range assembly.Registry().Modules() {
		if composed[info.ID] {
			continue
		}
		if reason, ok := intentionallyUncomposed[info.ID]; ok && reason != "" {
			continue
		}
		t.Fatalf("module %q is registered but composed into no product; either add it to "+
			"assemblies/headless.json or record why it is intentionally uncomposed", info.ID)
	}
}

// TestM0ChainEndToEnd is the capstone interconnection proof for the Grok Bot M0
// slice: one signed delivery travels the whole chain on the *assembled*
// services — webhook → inbox → lead triage → task board → lease (evidence,
// review) → notification routing. Every step is a real service from the root, so
// a module wired to the wrong key fails here instead of in production.
func TestM0ChainEndToEnd(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{
		Version: "9.9.9", Commit: "deadbeef", BuildTime: "2026-11-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("load headless: %v", err)
	}
	defer func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	}()

	ctx := context.Background()
	inboxService, ok := root.Get(contracts.KeyInbox)
	if !ok {
		t.Fatal("KeyInbox missing")
	}
	webhookService, ok := root.Get(contracts.KeyWebhook)
	if !ok {
		t.Fatal("KeyWebhook missing")
	}
	admin, ok := webhookService.(contracts.WebhookSourceAdmin)
	if !ok {
		t.Fatal("webhook does not expose source admin")
	}
	leadService, ok := root.Get(contracts.KeyLead)
	if !ok {
		t.Fatal("KeyLead missing")
	}
	leadRules, ok := leadService.(contracts.LeadRuleAdmin)
	if !ok {
		t.Fatal("lead does not expose rule admin")
	}
	board, ok := root.Get(contracts.KeyTaskBoard)
	if !ok {
		t.Fatal("KeyTaskBoard missing")
	}
	leaseService, ok := root.Get(contracts.KeyTaskLease)
	if !ok {
		t.Fatal("KeyTaskLease missing")
	}
	router, ok := root.Get(contracts.KeyNotifyRoute)
	if !ok {
		t.Fatal("KeyNotifyRoute missing")
	}

	// 1. A sender is configured, and the Lead has a rule that dispatches it.
	secret := []byte("end-to-end-secret-0001")
	if err := admin.UpsertSource(ctx, contracts.WebhookSource{ID: "github", Secret: secret, DefaultChannel: "repo"}); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if err := leadRules.SetRules(ctx, []contracts.LeadRule{{
		Source: "webhook", Assignee: "bot-1", SubjectPrefix: "事件：",
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	// 2. A signed delivery arrives.
	body := []byte(`{"title":"崩溃告警","body":"#12 连续失败","ref":"gh://12"}`)
	delivery, err := webhookService.Receive(ctx, "github", webhook.Sign(secret, time.Now(), body), body)
	if err != nil {
		t.Fatalf("webhook.Receive: %v", err)
	}
	if delivery.Item.Source != "webhook" || delivery.Item.Status != contracts.InboxStatusNew {
		t.Fatalf("delivery = %+v, want a new webhook item", delivery.Item)
	}

	// 3. One triage pass turns it into a task owned by bot-1.
	report, err := leadService.RunOnce(ctx)
	if err != nil {
		t.Fatalf("lead.RunOnce: %v", err)
	}
	if report.Scanned != 1 || report.Dispatched != 1 || report.Failed != 0 {
		t.Fatalf("lead report = %+v, want one dispatched and no failures", report)
	}
	updated, err := inboxService.Get(ctx, delivery.Item.ID)
	if err != nil || updated.Status != contracts.InboxStatusTask || len(updated.TaskIDs) != 1 {
		t.Fatalf("inbox item after triage = (%+v, %v), want status task with one id", updated, err)
	}
	taskID := updated.TaskIDs[0]

	boardTasks, err := board.List(ctx)
	if err != nil {
		t.Fatalf("board.List: %v", err)
	}
	var found *contracts.BoardTask
	for i := range boardTasks {
		if boardTasks[i].ID == taskID {
			found = &boardTasks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("task %q created by triage is not on the board", taskID)
	}
	if found.Owner != "bot-1" || !strings.HasPrefix(found.Subject, "事件：") {
		t.Fatalf("board task = %+v, want owner bot-1 and the rule prefix", found)
	}

	// 4. The claim layer takes it: evidence is mandatory, review completes it.
	if _, err := leaseService.Claim(ctx, taskID, "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := leaseService.Submit(ctx, taskID, "bot-1", nil); !errors.Is(err, contracts.ErrLeaseEvidenceRequired) {
		t.Fatalf("Submit without evidence = %v, want ErrLeaseEvidenceRequired", err)
	}
	if _, err := leaseService.Submit(ctx, taskID, "bot-1", []contracts.Evidence{{Kind: "link", Ref: "gh://12"}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := leaseService.Accept(ctx, taskID, "lead", "verified"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	boardTasks, _ = board.List(ctx)
	for _, task := range boardTasks {
		if task.ID == taskID && task.Status != contracts.TaskStatusCompleted {
			t.Fatalf("board task %q status = %q after accept", taskID, task.Status)
		}
	}

	// 5. Notification routing answers for the approval category.
	plan, err := router.Plan(ctx, contracts.NotifyCategoryApproval, time.Now(), nil)
	if err != nil || len(plan.Channels) == 0 || plan.Channels[len(plan.Channels)-1] != contracts.NotifyChannelInApp {
		t.Fatalf("notify plan = (%+v, %v), want in-app terminal channel", plan, err)
	}
}

// TestFeishuChainEndToEnd is the second channel through the same assembled chain:
// a signed Feishu event travels adapter → inbox → lead triage → task board. The
// webhook chain proved the wire; this proves the adapter's own seam (signature,
// envelope parsing, dedupe key) really reaches the same services.
func TestFeishuChainEndToEnd(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{
		Version: "9.9.9", Commit: "deadbeef", BuildTime: "2026-11-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("load headless: %v", err)
	}
	defer func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	}()

	ctx := context.Background()
	inboxService, ok := root.Get(contracts.KeyInbox)
	if !ok {
		t.Fatal("KeyInbox missing")
	}
	feishuService, ok := root.Get(contracts.KeyFeishu)
	if !ok || feishuService == nil {
		t.Fatal("KeyFeishu missing")
	}
	leadService, ok := root.Get(contracts.KeyLead)
	if !ok {
		t.Fatal("KeyLead missing")
	}
	leadRules, ok := leadService.(contracts.LeadRuleAdmin)
	if !ok {
		t.Fatal("lead does not expose rule admin")
	}
	board, ok := root.Get(contracts.KeyTaskBoard)
	if !ok {
		t.Fatal("KeyTaskBoard missing")
	}

	if err := leadRules.SetRules(ctx, []contracts.LeadRule{{
		Source: "feishu", Assignee: "bot-feishu", SubjectPrefix: "飞书：",
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	body := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},` +
		`"event":{"sender":{"sender_id":{"open_id":"ou_1"}},` +
		`"message":{"message_id":"om_1","chat_id":"oc_1","message_type":"text",` +
		`"content":"{\"text\":\"帮我看下退款率\"}"}}}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "assembly-nonce"
	// The headless assembly configures no encrypt key, so the signature covers an
	// empty key — exactly what the adapter computes from its own config.
	mac := sha256.Sum256(append([]byte(timestamp+nonce), body...))
	headers := map[string]string{
		"X-Lark-Signature":         hex.EncodeToString(mac[:]),
		"X-Lark-Request-Timestamp": timestamp,
		"X-Lark-Request-Nonce":     nonce,
	}

	item, merged, err := feishuService.Receive(ctx, headers, body)
	if err != nil {
		t.Fatalf("feishu.Receive through the assembly: %v", err)
	}
	if merged || item.Source != contracts.FeishuSource || item.Channel != "oc_1" {
		t.Fatalf("item = %+v merged=%v, want a feishu inbox row", item, merged)
	}

	report, err := leadService.RunOnce(ctx)
	if err != nil {
		t.Fatalf("lead.RunOnce: %v", err)
	}
	if report.Dispatched != 1 || report.Failed != 0 {
		t.Fatalf("lead report = %+v, want the feishu item dispatched", report)
	}

	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatalf("board.List: %v", err)
	}
	found := false
	for _, task := range tasks {
		if task.Owner == "bot-feishu" && strings.HasPrefix(task.Subject, "飞书：") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no task owned by bot-feishu on the board: %+v", tasks)
	}

	updated, err := inboxService.Get(ctx, item.ID)
	if err != nil || updated.Status != contracts.InboxStatusTask {
		t.Fatalf("inbox row after triage = (%+v, %v), want status task", updated, err)
	}
}
