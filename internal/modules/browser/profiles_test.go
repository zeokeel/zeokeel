package browser

// Profile tests: the isolation boundary in CDP is a browser context (its own
// cookie jar), so these tests pin the exact wire conversation per profile —
// createBrowserContext once, then createTarget carrying that context id — and
// the failure modes that would silently destroy isolation: a cached bad context
// id, an empty profile id, a plain Open dragged into a bot's jar, or a second
// context for the same profile.
//
// Everything runs over the fake Transport: no browser, no display, no network.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// profileScript is the fake browser behind a fakeTransport. It hands out
// context and target ids the way a browser does, and can be told to fail the
// first N context creations or disposals.
type profileScript struct {
	mu               sync.Mutex
	contexts         int
	targets          int
	contextCalls     int
	disposeCalls     int
	failContextCalls int // fail the first N Target.createBrowserContext calls
	failDisposeCalls int // fail the first N Target.disposeBrowserContext calls
}

func (s *profileScript) transport(t *testing.T) *fakeTransport {
	t.Helper()
	tr := &fakeTransport{}
	tr.reply = func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch method {
		case "Target.createBrowserContext":
			s.contextCalls++
			if s.contextCalls <= s.failContextCalls {
				return nil, errors.New("createBrowserContext: browser refused")
			}
			s.contexts++
			return json.RawMessage(`{"browserContextId":"ctx-` + strconv.Itoa(s.contexts) + `"}`), nil
		case "Target.createTarget":
			s.targets++
			return json.RawMessage(`{"targetId":"T-` + strconv.Itoa(s.targets) + `"}`), nil
		case "Target.disposeBrowserContext":
			s.disposeCalls++
			if s.disposeCalls <= s.failDisposeCalls {
				return nil, errors.New("disposeBrowserContext: browser refused")
			}
			return json.RawMessage(`{"success":true}`), nil
		default:
			t.Errorf("unexpected method %q", method)
			return nil, errors.New("unexpected method")
		}
	}
	return tr
}

func (s *profileScript) contextCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contexts
}

// wire formats the recorded conversation as "<method> <params-json>", in order,
// so a test can pin the exact CDP calls a profile makes (including which params
// are *absent*).
func wire(t *testing.T, tr *fakeTransport) []string {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	calls := make([]string, 0, len(tr.calls))
	for _, call := range tr.calls {
		encoded, err := json.Marshal(call.params)
		if err != nil {
			t.Fatalf("marshal params of %s: %v", call.method, err)
		}
		calls = append(calls, call.method+" "+string(encoded))
	}
	return calls
}

func formatWire(calls []string) string {
	if len(calls) == 0 {
		return "(nothing sent)"
	}
	return "\n  " + strings.Join(calls, "\n  ")
}

func assertWire(t *testing.T, tr *fakeTransport, want ...string) {
	t.Helper()
	got := wire(t, tr)
	if len(got) != len(want) {
		t.Fatalf("CDP conversation = %s\nwant %s", formatWire(got), formatWire(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("CDP call %d = %q, want %q\nfull conversation: %s", index, got[index], want[index], formatWire(got))
		}
	}
}

// assertNoSession checks that no recorded command rode on a page session:
// profile commands are browser-level (contexts) or target-level (createTarget).
func assertNoSession(t *testing.T, tr *fakeTransport) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, call := range tr.calls {
		if call.hasSession {
			t.Errorf("%s rode on page session %q, want none", call.method, call.session)
		}
	}
}

// contextIDsOnCreateTarget lists the browserContextId each in-context
// createTarget carried, in send order.
func contextIDsOnCreateTarget(t *testing.T, tr *fakeTransport) []string {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var ids []string
	for _, call := range tr.calls {
		if call.method != "Target.createTarget" {
			continue
		}
		params, ok := call.params.(createTargetInContextParams)
		if !ok {
			continue // a plain Open: default context, no selector
		}
		ids = append(ids, params.BrowserContextID)
	}
	return ids
}

// mustProfileAdapter builds an adapter over a scripted profile transport.
func mustProfileAdapter(t *testing.T, script *profileScript) (*Adapter, *fakeTransport) {
	t.Helper()
	tr := script.transport(t)
	return mustNew(t, Config{Endpoint: testEndpoint, Transport: tr}), tr
}

func TestOpenInProfileCreatesOneContextPerProfile(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	first, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/one")
	if err != nil {
		t.Fatalf("OpenInProfile(alice, one): %v", err)
	}
	second, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/two")
	if err != nil {
		t.Fatalf("OpenInProfile(alice, two): %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("both pages got id %q", first.ID)
	}

	// Exactly one context for the profile, and every page opened inside it.
	assertWire(t, tr,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://example.com/one","browserContextId":"ctx-1"}`,
		`Target.createTarget {"url":"https://example.com/two","browserContextId":"ctx-1"}`,
	)
	assertNoSession(t, tr)

	if got := script.contextCount(); got != 1 {
		t.Errorf("contexts created = %d, want 1 for a reused profile", got)
	}
	for _, page := range []contracts.PageInfo{first, second} {
		profileID, ok, err := adapter.ProfileFor(ctx, page.ID)
		if err != nil || !ok || profileID != "alice" {
			t.Errorf("ProfileFor(%q) = (%q, %v, %v), want (alice, true, nil)", page.ID, profileID, ok, err)
		}
	}
}

func TestTwoProfilesNeverShareAContext(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	alice, err := adapter.OpenInProfile(ctx, "alice", "https://site-a.example/login")
	if err != nil {
		t.Fatalf("OpenInProfile(alice): %v", err)
	}
	bob, err := adapter.OpenInProfile(ctx, "bob", "https://site-b.example/login")
	if err != nil {
		t.Fatalf("OpenInProfile(bob): %v", err)
	}
	aliceAgain, err := adapter.OpenInProfile(ctx, "alice", "https://site-a.example/inbox")
	if err != nil {
		t.Fatalf("OpenInProfile(alice, again): %v", err)
	}

	assertWire(t, tr,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://site-a.example/login","browserContextId":"ctx-1"}`,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://site-b.example/login","browserContextId":"ctx-2"}`,
		`Target.createTarget {"url":"https://site-a.example/inbox","browserContextId":"ctx-1"}`,
	)
	assertNoSession(t, tr)

	// The isolation claim, stated directly: each page carries its own profile's
	// context id, and the two profiles' ids differ (separate cookie jars).
	ids := contextIDsOnCreateTarget(t, tr)
	want := []string{"ctx-1", "ctx-2", "ctx-1"}
	if len(ids) != len(want) {
		t.Fatalf("in-context createTargets = %v, want %v", ids, want)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("createTarget %d carried context %q, want %q", index, ids[index], want[index])
		}
	}
	if ids[0] == ids[1] {
		t.Fatalf("alice and bob shared context %q: one cookie jar for two bots", ids[0])
	}

	for page, wantProfile := range map[contracts.PageInfo]string{alice: "alice", bob: "bob", aliceAgain: "alice"} {
		profileID, ok, err := adapter.ProfileFor(ctx, page.ID)
		if err != nil || !ok || profileID != wantProfile {
			t.Errorf("ProfileFor(%q) = (%q, %v, %v), want (%q, true, nil)", page.ID, profileID, ok, err, wantProfile)
		}
	}
}

func TestPlainOpenStaysInTheDefaultContext(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	plain, err := adapter.Open(ctx, "https://example.com/human")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	profiled, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/bot")
	if err != nil {
		t.Fatalf("OpenInProfile: %v", err)
	}

	// The plain target carries no browserContextId at all (no selector key in the
	// params), and no context was created before it: Open is untouched by
	// profiles.
	assertWire(t, tr,
		`Target.createTarget {"url":"https://example.com/human","newWindow":false}`,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://example.com/bot","browserContextId":"ctx-1"}`,
	)
	assertNoSession(t, tr)

	profileID, ok, err := adapter.ProfileFor(ctx, plain.ID)
	if err != nil || !ok || profileID != "" {
		t.Fatalf("ProfileFor(plain page) = (%q, %v, %v), want (\"\", true, nil)", profileID, ok, err)
	}
	profileID, ok, err = adapter.ProfileFor(ctx, profiled.ID)
	if err != nil || !ok || profileID != "alice" {
		t.Fatalf("ProfileFor(profiled page) = (%q, %v, %v), want (alice, true, nil)", profileID, ok, err)
	}
}

func TestCloseProfileDisposesAndForgets(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	alice, err := adapter.OpenInProfile(ctx, "alice", "https://a.example/")
	if err != nil {
		t.Fatalf("OpenInProfile(alice): %v", err)
	}
	bob, err := adapter.OpenInProfile(ctx, "bob", "https://b.example/")
	if err != nil {
		t.Fatalf("OpenInProfile(bob): %v", err)
	}
	if err := adapter.CloseProfile(ctx, "bob"); err != nil {
		t.Fatalf("CloseProfile(bob): %v", err)
	}

	// Bob's page went with the context; alice's is untouched.
	if _, _, err := adapter.ProfileFor(ctx, bob.ID); !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Errorf("ProfileFor(bob page after close) = %v, want ErrBrowserNotFound", err)
	}
	status, err := adapter.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Pages != 1 {
		t.Errorf("status.Pages = %d, want 1 (only alice's page is left)", status.Pages)
	}
	if profileID, ok, err := adapter.ProfileFor(ctx, alice.ID); err != nil || !ok || profileID != "alice" {
		t.Errorf("ProfileFor(alice page) = (%q, %v, %v), want (alice, true, nil)", profileID, ok, err)
	}

	// Reopening bob starts from a brand-new, empty jar: the mapping was
	// forgotten, so nothing is reused.
	reopened, err := adapter.OpenInProfile(ctx, "bob", "https://b.example/again")
	if err != nil {
		t.Fatalf("OpenInProfile(bob) after close: %v", err)
	}

	assertWire(t, tr,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://a.example/","browserContextId":"ctx-1"}`,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://b.example/","browserContextId":"ctx-2"}`,
		`Target.disposeBrowserContext {"browserContextId":"ctx-2"}`,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://b.example/again","browserContextId":"ctx-3"}`,
	)
	if profileID, _, err := adapter.ProfileFor(ctx, reopened.ID); err != nil || profileID != "bob" {
		t.Errorf("ProfileFor(reopened bob page) = (%q, %v), want bob", profileID, err)
	}
}

func TestCloseUnknownProfileIsNoOp(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)

	if err := adapter.CloseProfile(context.Background(), "ghost"); err != nil {
		t.Fatalf("CloseProfile(unknown) = %v, want nil (nothing to dispose)", err)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0 for an unknown profile", got)
	}
}

func TestEmptyProfileIDIsInvalid(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	for _, profileID := range []string{"", "   "} {
		if _, err := adapter.OpenInProfile(ctx, profileID, "https://example.com/"); !errors.Is(err, contracts.ErrBrowserInvalid) {
			t.Errorf("OpenInProfile(%q) = %v, want ErrBrowserInvalid", profileID, err)
		}
		if err := adapter.CloseProfile(ctx, profileID); !errors.Is(err, contracts.ErrBrowserInvalid) {
			t.Errorf("CloseProfile(%q) = %v, want ErrBrowserInvalid", profileID, err)
		}
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0: an invalid profile id must not reach the browser", got)
	}
}

func TestOpenInProfileBadURLSendsNothing(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)

	if _, err := adapter.OpenInProfile(context.Background(), "alice", "file:///etc/passwd"); !errors.Is(err, contracts.ErrBrowserBadURL) {
		t.Fatalf("OpenInProfile(file) = %v, want ErrBrowserBadURL", err)
	}
	// Not even the context: a rejected URL must not spend a round trip or leave
	// an empty jar behind.
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0", got)
	}
}

func TestFailedContextCreationIsNotCached(t *testing.T) {
	script := &profileScript{failContextCalls: 1}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	_, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/one")
	if !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("OpenInProfile(failing context) = %v, want ErrBrowserUnavailable", err)
	}
	if !strings.Contains(err.Error(), "browser refused") {
		t.Errorf("error %q lost the transport cause", err)
	}
	assertWire(t, tr, `Target.createBrowserContext {"disposeOnDetach":false}`)

	// The retry asks the browser again instead of reusing a phantom id.
	page, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/one")
	if err != nil {
		t.Fatalf("OpenInProfile(retry): %v", err)
	}
	assertWire(t, tr,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://example.com/one","browserContextId":"ctx-1"}`,
	)
	if profileID, _, err := adapter.ProfileFor(ctx, page.ID); err != nil || profileID != "alice" {
		t.Errorf("ProfileFor(retried page) = (%q, %v), want alice", profileID, err)
	}

	// And the successful creation is cached: a third open reuses ctx-1.
	if _, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/two"); err != nil {
		t.Fatalf("OpenInProfile(third): %v", err)
	}
	if got := script.contextCount(); got != 1 {
		t.Errorf("contexts created = %d, want 1 after one successful creation", got)
	}
	if got := contextIDsOnCreateTarget(t, tr); len(got) != 2 || got[0] != "ctx-1" || got[1] != "ctx-1" {
		t.Errorf("context ids on createTarget = %v, want [ctx-1 ctx-1]", got)
	}
}

func TestFailedDisposeKeepsTheMapping(t *testing.T) {
	script := &profileScript{failDisposeCalls: 1}
	adapter, tr := mustProfileAdapter(t, script)
	ctx := context.Background()

	if _, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/"); err != nil {
		t.Fatalf("OpenInProfile: %v", err)
	}
	if err := adapter.CloseProfile(ctx, "alice"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Fatalf("CloseProfile(failing dispose) = %v, want ErrBrowserUnavailable", err)
	}

	// The browser may still own ctx-1, so the profile must still point at it
	// rather than spawning a second jar for the same bot.
	if _, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/next"); err != nil {
		t.Fatalf("OpenInProfile(after failed dispose): %v", err)
	}
	if got := script.contextCount(); got != 1 {
		t.Errorf("contexts created = %d, want 1: a failed dispose must not orphan the mapping", got)
	}
	assertWire(t, tr,
		`Target.createBrowserContext {"disposeOnDetach":false}`,
		`Target.createTarget {"url":"https://example.com/","browserContextId":"ctx-1"}`,
		`Target.disposeBrowserContext {"browserContextId":"ctx-1"}`,
		`Target.createTarget {"url":"https://example.com/next","browserContextId":"ctx-1"}`,
	)
}

func TestProfilesWithoutEndpointAreUnavailable(t *testing.T) {
	tr := &fakeTransport{}
	adapter := mustNew(t, Config{Transport: tr})
	ctx := context.Background()

	if _, err := adapter.OpenInProfile(ctx, "alice", "https://example.com/"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("OpenInProfile = %v, want ErrBrowserUnavailable", err)
	}
	if err := adapter.CloseProfile(ctx, "alice"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("CloseProfile = %v, want ErrBrowserUnavailable", err)
	}
	if _, _, err := adapter.ProfileFor(ctx, "T-1"); !errors.Is(err, contracts.ErrBrowserUnavailable) {
		t.Errorf("ProfileFor = %v, want ErrBrowserUnavailable", err)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0 without an endpoint", got)
	}
}

func TestProfileForUnknownPageIsNotFound(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)

	profileID, ok, err := adapter.ProfileFor(context.Background(), "T-missing")
	if !errors.Is(err, contracts.ErrBrowserNotFound) {
		t.Fatalf("ProfileFor(unknown) = (%q, %v, %v), want ErrBrowserNotFound", profileID, ok, err)
	}
	if ok || profileID != "" {
		t.Errorf("ProfileFor(unknown) = (%q, %v), want (\"\", false)", profileID, ok)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0 for an unknown page", got)
	}
}

func TestConcurrentFirstOpensShareOneContext(t *testing.T) {
	script := &profileScript{}
	adapter, tr := mustProfileAdapter(t, script)

	const opens = 8
	var wg sync.WaitGroup
	errs := make(chan error, opens)
	for index := 0; index < opens; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if _, err := adapter.OpenInProfile(context.Background(), "alice", "https://example.com/"+strconv.Itoa(index)); err != nil {
				errs <- err
			}
		}(index)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent OpenInProfile: %v", err)
	}

	// One jar for one bot: concurrent first opens must not each create a context.
	if got := script.contextCount(); got != 1 {
		t.Fatalf("contexts created = %d, want 1", got)
	}
	ids := contextIDsOnCreateTarget(t, tr)
	if len(ids) != opens {
		t.Fatalf("in-context createTargets = %d, want %d", len(ids), opens)
	}
	for _, id := range ids {
		if id != "ctx-1" {
			t.Fatalf("createTarget carried context %q, want ctx-1 for every page", id)
		}
	}
}

// The protocol layer fails closed: it refuses to create or dispose without an
// explicit context id, so a missing id can never silently mean "the default
// jar" or "everything".
func TestContextCommandsRefuseAnEmptyContextID(t *testing.T) {
	tr := &fakeTransport{}
	cdp := NewCDP(tr, nil)
	ctx := context.Background()

	if _, err := cdp.OpenInContext(ctx, "https://example.com/", ""); !errors.Is(err, ErrProtocol) {
		t.Errorf("OpenInContext(no context) = %v, want ErrProtocol", err)
	}
	if err := cdp.DisposeBrowserContext(ctx, ""); !errors.Is(err, ErrProtocol) {
		t.Errorf("DisposeBrowserContext(no context) = %v, want ErrProtocol", err)
	}
	if got := tr.count(); got != 0 {
		t.Fatalf("sends = %d, want 0: an empty context id must not reach the browser", got)
	}
}

func TestCreateBrowserContextPinsDisposeOnDetachFalse(t *testing.T) {
	tr := scriptedTransport(t, map[string]json.RawMessage{
		"Target.createBrowserContext": json.RawMessage(`{"browserContextId":"ctx-7"}`),
	})
	cdp := NewCDP(tr, nil)

	contextID, err := cdp.CreateBrowserContext(context.Background())
	if err != nil {
		t.Fatalf("CreateBrowserContext: %v", err)
	}
	if contextID != "ctx-7" {
		t.Fatalf("context id = %q, want ctx-7", contextID)
	}
	if got := wire(t, tr); len(got) != 1 || got[0] != `Target.createBrowserContext {"disposeOnDetach":false}` {
		t.Fatalf("CDP calls = %s, want the documented createBrowserContext params", formatWire(got))
	}
	if _, hasSession := SessionFrom(context.Background()); hasSession {
		t.Error("CreateBrowserContext must not run on a page session")
	}
}

func TestCreateBrowserContextMissingIDIsAProtocolError(t *testing.T) {
	tr := scriptedTransport(t, map[string]json.RawMessage{
		"Target.createBrowserContext": json.RawMessage(`{}`),
	})
	cdp := NewCDP(tr, nil)
	if _, err := cdp.CreateBrowserContext(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Fatalf("CreateBrowserContext(empty reply) = %v, want ErrProtocol", err)
	}
}
