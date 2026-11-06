package session_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// autoTitleProvider serves ordinary turns from a scripted fakeProvider and
// answers automatic-title requests separately. It is the provider-level
// acceptance seam: the controller must send a real title request and fall back
// when that request fails.
type autoTitleProvider struct {
	base     *fakeProvider
	title    string
	titleErr error

	mu            sync.Mutex
	titleRequests int
	titlePrompt   string
}

func (p *autoTitleProvider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	if isAutoTitleRequest(req) {
		p.mu.Lock()
		p.titleRequests++
		for _, msg := range req.Messages {
			p.titlePrompt += msg.Content + "\n"
		}
		p.mu.Unlock()
		if p.titleErr != nil {
			return nil, p.titleErr
		}
		ch := make(chan runtime.Chunk, 1)
		ch <- runtime.Chunk{Text: p.title, Done: true}
		close(ch)
		return ch, nil
	}
	return p.base.Stream(ctx, req)
}

func (p *autoTitleProvider) titleCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.titleRequests
}

func (p *autoTitleProvider) lastTitlePrompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.titlePrompt
}

// isAutoTitleRequest recognizes the provider request the title generator sends
// by its stable system instruction.
func isAutoTitleRequest(req runtime.Request) bool {
	return len(req.Messages) > 0 &&
		req.Messages[0].Role == runtime.RoleSystem &&
		req.Messages[0].Content == session.AutoTitleSystemPrompt
}

// waitStoredTitle waits for a session row to reach a title accepted by match.
// Title generation is asynchronous (it must not hold up turn completion), so
// tests observe the store rather than the turn's Wait.
func waitStoredTitle(t *testing.T, st *store.Store, id string, match func(string) bool) *store.Session {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sess, err := st.GetSession(id)
		if err == nil && match(sess.Title) {
			return sess
		}
		time.Sleep(2 * time.Millisecond)
	}
	sess, err := st.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession(%q): %v", id, err)
	}
	t.Fatalf("session title = %q, want the generated title", sess.Title)
	return nil
}

// TestAutoTitleGenerated covers the happy path: after the first completed turn
// the controller asks the provider for a title, stores it, and does not repeat
// the request on later turns.
func TestAutoTitleGenerated(t *testing.T) {
	provider := &autoTitleProvider{
		base:  &fakeProvider{respond: echoRespond},
		title: "Deploy Pipeline Fix",
	}
	h := newHarness(t, provider)
	h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	const firstPrompt = "why does the deploy pipeline keep failing?"
	if err := h.ctrl.Submit(ctx, id, firstPrompt); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	sess := waitStoredTitle(t, h.store, id, func(title string) bool {
		return title == "Deploy Pipeline Fix"
	})
	if sess.Title != "Deploy Pipeline Fix" {
		t.Fatalf("auto title = %q, want %q", sess.Title, "Deploy Pipeline Fix")
	}
	if got := provider.titleCallCount(); got != 1 {
		t.Fatalf("title provider calls = %d, want 1", got)
	}
	prompt := provider.lastTitlePrompt()
	if !strings.Contains(prompt, firstPrompt) {
		t.Fatalf("title request prompt does not carry the first user message: %q", prompt)
	}
	if !strings.Contains(prompt, "echo:"+firstPrompt) {
		t.Fatalf("title request prompt does not carry the assistant reply: %q", prompt)
	}

	// Once per session: a second completed turn must not rename it again.
	if err := h.ctrl.Submit(ctx, id, "and what about the tests?"); err != nil {
		t.Fatalf("Submit second turn: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	stored, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stored.Title != "Deploy Pipeline Fix" {
		t.Fatalf("title after second turn = %q, want the first turn's title", stored.Title)
	}
	if got := provider.titleCallCount(); got != 1 {
		t.Fatalf("title provider calls after second turn = %d, want 1", got)
	}
}

// TestAutoTitleProviderFallback covers the fallback contract: when the
// provider title request fails (or returns no usable title) the session is
// titled with the truncated first user message instead of staying untitled.
func TestAutoTitleProviderFallback(t *testing.T) {
	t.Run("stream error", func(t *testing.T) {
		provider := &autoTitleProvider{
			base:     &fakeProvider{respond: echoRespond},
			titleErr: errors.New("title provider unavailable"),
		}
		h := newHarness(t, provider)
		h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

		ctx := context.Background()
		id, err := h.ctrl.CreateSession(ctx, "New Session")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		const prompt = "Fix the flaky deploy pipeline"
		if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitIdleAndDrain(t, h.ctrl)

		sess := waitStoredTitle(t, h.store, id, func(title string) bool {
			return title != "New Session"
		})
		if sess.Title != prompt {
			t.Fatalf("fallback title = %q, want the first user message %q", sess.Title, prompt)
		}
		if got := provider.titleCallCount(); got != 1 {
			t.Fatalf("title provider calls = %d, want 1", got)
		}
	})

	t.Run("empty title", func(t *testing.T) {
		provider := &autoTitleProvider{
			base:  &fakeProvider{respond: echoRespond},
			title: "   \n\t ",
		}
		h := newHarness(t, provider)
		h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

		ctx := context.Background()
		id, err := h.ctrl.CreateSession(ctx, "New Session")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		const prompt = "Investigate the flaky deploy pipeline\nand explain the retry backoff behaviour in detail"
		if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitIdleAndDrain(t, h.ctrl)

		sess := waitStoredTitle(t, h.store, id, func(title string) bool {
			return title != "New Session"
		})
		if sess.Title == prompt {
			t.Fatal("long first user message was stored verbatim instead of truncated")
		}
		if !strings.HasPrefix(sess.Title, "Investigate the flaky deploy pipeline and explain") {
			t.Fatalf("fallback title = %q, want a truncation of the first user message", sess.Title)
		}
		if !strings.HasSuffix(sess.Title, "…") {
			t.Fatalf("truncated fallback title = %q, want an ellipsis", sess.Title)
		}
		if got := len([]rune(sess.Title)); got > session.AutoTitleFallbackChars {
			t.Fatalf("fallback title length = %d, want at most %d", got, session.AutoTitleFallbackChars)
		}
		if strings.Contains(sess.Title, "\n") {
			t.Fatalf("fallback title kept a newline: %q", sess.Title)
		}
	})

	t.Run("control-only title", func(t *testing.T) {
		provider := &autoTitleProvider{
			base:  &fakeProvider{respond: echoRespond},
			title: "\x1b[31m\x1b[0m\u202E",
		}
		h := newHarness(t, provider)
		h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

		ctx := context.Background()
		id, err := h.ctrl.CreateSession(ctx, "New Session")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		const prompt = "fallback after an unusable title"
		if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitIdleAndDrain(t, h.ctrl)

		sess := waitStoredTitle(t, h.store, id, func(title string) bool {
			return title != "New Session"
		})
		if sess.Title != prompt {
			t.Fatalf("fallback title = %q, want the first user message %q", sess.Title, prompt)
		}
		if got := provider.titleCallCount(); got != 1 {
			t.Fatalf("title provider calls = %d, want 1", got)
		}
	})

	t.Run("fallback is sanitized", func(t *testing.T) {
		provider := &autoTitleProvider{
			base:     &fakeProvider{respond: echoRespond},
			titleErr: errors.New("title provider unavailable"),
		}
		h := newHarness(t, provider)
		h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

		ctx := context.Background()
		id, err := h.ctrl.CreateSession(ctx, "New Session")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		const prompt = "Fix \x1b[31mred\x1b[0m tests"
		if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitIdleAndDrain(t, h.ctrl)

		const want = "Fix red tests"
		waitStoredTitle(t, h.store, id, func(title string) bool { return title == want })
	})
}

// TestManualRenameNotOverwritten covers the manual-title guard: a title the
// user chose is never replaced by the automatic titler, whether the rename
// happens before the first turn or after the automatic title was written.
func TestManualRenameNotOverwritten(t *testing.T) {
	provider := &autoTitleProvider{
		base:  &fakeProvider{respond: echoRespond},
		title: "Generated Title",
	}
	h := newHarness(t, provider)
	h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.ctrl.RenameSession(ctx, id, "My Manual Title"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "first turn after a manual rename"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	stored, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stored.Title != "My Manual Title" {
		t.Fatalf("manually renamed title = %q, want it untouched", stored.Title)
	}
	if got := provider.titleCallCount(); got != 0 {
		t.Fatalf("title provider calls after a manual rename = %d, want 0", got)
	}

	// A manual rename after the automatic title also wins and stays won.
	autoID, err := h.ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession(auto): %v", err)
	}
	if err := h.ctrl.Submit(ctx, autoID, "name this session from the model"); err != nil {
		t.Fatalf("Submit(auto): %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	waitStoredTitle(t, h.store, autoID, func(title string) bool {
		return title == "Generated Title"
	})
	if _, err := h.ctrl.RenameSession(ctx, autoID, "Chosen By User"); err != nil {
		t.Fatalf("RenameSession(auto): %v", err)
	}
	if err := h.ctrl.Submit(ctx, autoID, "another turn must not undo the rename"); err != nil {
		t.Fatalf("Submit(auto second turn): %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	stored, err = h.store.GetSession(autoID)
	if err != nil {
		t.Fatalf("GetSession(auto): %v", err)
	}
	if stored.Title != "Chosen By User" {
		t.Fatalf("title after rename and another turn = %q, want %q", stored.Title, "Chosen By User")
	}
}

// TestAutoTitleRenameRaceWithFinalWrite is the regression test for a rename
// that completes while the title writer is between its eligibility check and
// the final store update. The generator closes started just before returning,
// so RenameSession runs exactly when writeAutoTitle re-checks and writes. The
// two paths must be serialized: every completed manual rename stays the stored
// title, no matter which one wins the critical section.
func TestAutoTitleRenameRaceWithFinalWrite(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	const iterations = 1500
	var failures atomic.Int64
	for i := 0; i < iterations; i++ {
		want := fmt.Sprintf("Manual Race %d", i)
		started := make(chan struct{})
		renameDone := make(chan struct{})
		var once sync.Once
		h.ctrl.SetTitleGenerator(session.TitleFunc(func(context.Context, string, string, string) (string, error) {
			once.Do(func() { close(started) })
			return fmt.Sprintf("Automatic Race %d", i), nil
		}))
		id, err := h.ctrl.CreateSession(ctx, "New Session")
		if err != nil {
			t.Fatalf("CreateSession(%d): %v", i, err)
		}
		if err := h.ctrl.Submit(ctx, id, fmt.Sprintf("race write prompt %d", i)); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
		go func() {
			<-started
			if _, err := h.ctrl.RenameSession(ctx, id, want); err != nil {
				t.Errorf("RenameSession(%d): %v", i, err)
			}
			close(renameDone)
		}()
		waitIdleAndDrain(t, h.ctrl)
		select {
		case <-renameDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: rename never completed", i)
		}
		// The title write lands within microseconds of the generator
		// returning; watch the stored title for a few milliseconds so a late
		// overwrite is caught.
		deadline := time.Now().Add(5 * time.Millisecond)
		for time.Now().Before(deadline) {
			sess, err := h.store.GetSession(id)
			if err != nil {
				t.Fatalf("GetSession(%d): %v", i, err)
			}
			if sess.Title == want {
				time.Sleep(200 * time.Microsecond)
				continue
			}
			failures.Add(1)
			t.Logf("iteration %d: rename %q was overwritten by %q", i, want, sess.Title)
			break
		}
	}
	if got := failures.Load(); got > 0 {
		t.Fatalf("%d/%d completed manual renames were overwritten by the concurrent title write", got, iterations)
	}
}

// TestAutoTitleFailedFirstTurnThenRetry is the regression test for a failed
// first attempt. The failure must not consume the session's one automatic
// title: the next successfully completed turn titles the session from the
// original prompt, even though the session now has two user rows.
func TestAutoTitleFailedFirstTurnThenRetry(t *testing.T) {
	base := &fakeProvider{respond: echoRespond, streamErr: errors.New("provider exploded")}
	provider := &autoTitleProvider{base: base, title: "Retry Title"}
	h := newHarness(t, provider)
	h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	const firstPrompt = "fix the flaky deploy pipeline"
	if err := h.ctrl.Submit(ctx, id, firstPrompt); err != nil {
		t.Fatalf("Submit first (failing) turn: %v", err)
	}
	waitPhase(t, h.ctrl, state.PhaseFailed)
	h.ctrl.Wait()

	stored, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession after failure: %v", err)
	}
	if stored.Title != "New Session" {
		t.Fatalf("title after a failed first turn = %q, want the placeholder untouched", stored.Title)
	}
	if got := provider.titleCallCount(); got != 0 {
		t.Fatalf("title provider calls after a failed first turn = %d, want 0", got)
	}

	// The provider recovers; the retry is a normal successful turn.
	base.streamErr = nil
	const retryPrompt = "please try the deploy pipeline fix again"
	if err := h.ctrl.Submit(ctx, id, retryPrompt); err != nil {
		t.Fatalf("Submit retry turn: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	waitStoredTitle(t, h.store, id, func(title string) bool {
		return title == "Retry Title"
	})
	if got := provider.titleCallCount(); got != 1 {
		t.Fatalf("title provider calls after the successful retry = %d, want 1", got)
	}
	prompt := provider.lastTitlePrompt()
	if !strings.Contains(prompt, firstPrompt) {
		t.Fatalf("retry title request does not carry the original user message: %q", prompt)
	}
	if !strings.Contains(prompt, "echo:"+retryPrompt) {
		t.Fatalf("retry title request does not carry the successful turn's reply: %q", prompt)
	}
}

// TestAutoTitlePreFeatureManualTitleNotOverwritten covers the upgrade path:
// a session renamed before the manual-title marker existed has only a new
// title in the store. The automatic titler must treat that title as chosen by
// the user and leave it alone.
func TestAutoTitlePreFeatureManualTitleNotOverwritten(t *testing.T) {
	t.Run("changed after creation", func(t *testing.T) {
		provider := &autoTitleProvider{base: &fakeProvider{respond: echoRespond}, title: "Generated New Name"}
		h := newHarness(t, provider)
		h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

		ctx := context.Background()
		id, err := h.ctrl.CreateSession(ctx, "New Session")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		// Simulate the old RenameSession path: only the sessions row changes,
		// so there is no manual-title marker in settings.
		if _, err := h.store.UpdateSessionTitle(id, "Chosen Before Upgrade"); err != nil {
			t.Fatalf("UpdateSessionTitle: %v", err)
		}
		if err := h.ctrl.Submit(ctx, id, "first turn after the old rename"); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitIdleAndDrain(t, h.ctrl)

		stored, err := h.store.GetSession(id)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if stored.Title != "Chosen Before Upgrade" {
			t.Fatalf("pre-feature manual title = %q, want it preserved", stored.Title)
		}
		if got := provider.titleCallCount(); got != 0 {
			t.Fatalf("title provider calls after a pre-feature manual title = %d, want 0", got)
		}
	})

	t.Run("session loaded from store", func(t *testing.T) {
		provider := &autoTitleProvider{base: &fakeProvider{respond: echoRespond}, title: "Generated New Name"}
		h := newHarness(t, provider)
		h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

		ctx := context.Background()
		sess, err := h.store.CreateSession(h.workspace, "Chosen Before Upgrade")
		if err != nil {
			t.Fatalf("CreateSession(store): %v", err)
		}
		if _, err := h.ctrl.OpenSession(ctx, sess.ID); err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		if err := h.ctrl.Submit(ctx, sess.ID, "first turn after loading an old session"); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		waitIdleAndDrain(t, h.ctrl)

		stored, err := h.store.GetSession(sess.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if stored.Title != "Chosen Before Upgrade" {
			t.Fatalf("loaded pre-feature manual title = %q, want it preserved", stored.Title)
		}
		if got := provider.titleCallCount(); got != 0 {
			t.Fatalf("title provider calls for a loaded manual title = %d, want 0", got)
		}
	})
}

// TestAutoTitleCreationTitlePreserved covers a session created with a
// meaningful title, which is how app.startAutomation names each run after its
// automation task. That title is a deliberate choice rather than a
// placeholder, so the auto-titler must leave it alone: it is the only stored
// link from a run back to its task.
func TestAutoTitleCreationTitlePreserved(t *testing.T) {
	provider := &autoTitleProvider{base: &fakeProvider{respond: echoRespond}, title: "Model Picked Name"}
	h := newHarness(t, provider)
	h.ctrl.SetTitleGenerator(session.LLMTitleGenerator{Provider: provider, Model: "title-model"})

	ctx := context.Background()
	const creationTitle = "Nightly Release Report"
	id, err := h.ctrl.CreateSession(ctx, creationTitle)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "summarize yesterday's releases"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	// Let an asynchronous title write land, if one happens.
	time.Sleep(300 * time.Millisecond)

	stored, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stored.Title != creationTitle {
		t.Fatalf("creation title = %q, want it preserved across the first turn", stored.Title)
	}
	if got := provider.titleCallCount(); got != 0 {
		t.Fatalf("title provider calls for a deliberately named session = %d, want 0", got)
	}

	// The guard persists: another turn must not title the session either.
	if err := h.ctrl.Submit(ctx, id, "a later turn must not rename it"); err != nil {
		t.Fatalf("Submit(second): %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	stored, err = h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession after second turn: %v", err)
	}
	if stored.Title != creationTitle {
		t.Fatalf("creation title after second turn = %q, want %q", stored.Title, creationTitle)
	}
	if got := provider.titleCallCount(); got != 0 {
		t.Fatalf("title provider calls after second turn = %d, want 0", got)
	}
}

// TestAutoTitleNotifyWithNoCurrentSession covers the repaint signal when the
// current session went away while a background title request was blocked.
// Listeners still have to run so the global sidebar picks up the new title.
func TestAutoTitleNotifyWithNoCurrentSession(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	h.ctrl.SetTitleGenerator(session.TitleFunc(func(ctx context.Context, _, _, _ string) (string, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			return "Background No Current", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}))

	if err := h.ctrl.Submit(ctx, id, "title this while I look at another session"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("title generator never ran")
	}

	// Move to a second session, then delete it so no session is current.
	secondID, err := h.ctrl.CreateSession(ctx, "New Session")
	if err != nil {
		t.Fatalf("CreateSession(second): %v", err)
	}
	if err := h.ctrl.DeleteSession(ctx, secondID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := h.ctrl.CurrentSessionID(); got != "" {
		t.Fatalf("CurrentSessionID() = %q, want no current session", got)
	}

	var collector snapshotCollector
	h.ctrl.OnUpdate(collector.add)
	close(release)
	waitStoredTitle(t, h.store, id, func(title string) bool {
		return title == "Background No Current"
	})

	deadline := time.Now().Add(10 * time.Second)
	for len(collector.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if len(collector.all()) == 0 {
		t.Fatal("background title write did not notify OnUpdate listeners with no current session")
	}
	if collector.all()[0] == nil {
		t.Fatal("background title notification delivered a nil snapshot")
	}
}

// TestAutoTitleNormalization is table-driven coverage for model output that
// wraps a title in markdown emphasis, quotes or a label, including a quoted
// markdown heading.
func TestAutoTitleNormalization(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "bold", raw: "**Fix the deploy**", want: "Fix the deploy"},
		{name: "bold underscore", raw: "__Fix the deploy__", want: "Fix the deploy"},
		{name: "strikethrough", raw: "~~Fix the deploy~~", want: "Fix the deploy"},
		{name: "backticks", raw: "`Fix the deploy`", want: "Fix the deploy"},
		{name: "quoted markdown heading", raw: "\"# Deploy pipeline fix\"", want: "Deploy pipeline fix"},
		{name: "curly quoted markdown heading", raw: "“# Deploy pipeline fix”", want: "Deploy pipeline fix"},
		{name: "heading", raw: "## Deploy pipeline fix", want: "Deploy pipeline fix"},
		{name: "single quotes", raw: "'Fix the deploy'", want: "Fix the deploy"},
		{name: "labelled bold", raw: "Session title: **Fix the deploy**", want: "Fix the deploy"},
		{name: "bold with trailing punctuation", raw: "**Fix the deploy**.", want: "Fix the deploy"},
		{name: "multi line first non-empty", raw: "\n**Fix the deploy**\nexplanation", want: "Fix the deploy"},
		{name: "unbalanced trailing emphasis", raw: "Fix the deploy**", want: "Fix the deploy"},
		{name: "plain title", raw: "Deploy pipeline fix", want: "Deploy pipeline fix"},
		{name: "trailing hash is content", raw: "Learn C#", want: "Learn C#"},
		{name: "leading content hash", raw: "#1 issue after the deploy", want: "#1 issue after the deploy"},
		{name: "ansi color codes", raw: "Fix \x1b[31mred\x1b[0m tests", want: "Fix red tests"},
		{name: "osc title sequence", raw: "Deploy \x1b]0;pwn\x07 fix", want: "Deploy fix"},
		{name: "unterminated osc sequence", raw: "Deploy \x1b]0;payload", want: "Deploy"},
		{name: "bidi overrides", raw: "Deploy \u202Eevitisop\u202C fix", want: "Deploy evitisop fix"},
		{name: "zero width space", raw: "Deploy\u200Bfix", want: "Deployfix"},
		{name: "c1 control", raw: "Deploy\u0085fix", want: "Deployfix"},
		{name: "c1 osc sequence", raw: "Deploy \u009D0;pwn\u0007 fix", want: "Deploy fix"},
		{name: "c1 csi sequence", raw: "Fix \u009B31mred\u009B0m tests", want: "Fix red tests"},
		{name: "delete control", raw: "Deploy\u007Ffix", want: "Deployfix"},
		{name: "direction isolate", raw: "\u2066Deploy\u2069 fix", want: "Deploy fix"},
		{name: "byte order mark", raw: "\uFEFFDeploy fix", want: "Deploy fix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen := session.LLMTitleGenerator{Provider: &autoTitleProvider{title: tt.raw}}
			got, err := gen.GenerateTitle(context.Background(), "session-id", "fix the deploy", "done")
			if err != nil {
				t.Fatalf("GenerateTitle(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("normalize(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
