package app

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/memory"
	"github.com/zeokeel/zeokeel/old/internal/provider/openai"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Auto-memory extractor (docs/design/auto-memory.md §5 path B).
//
// The extractor is a background worker: after every successful turn that
// produced output it counts the session's turns, and once the configured
// interval is reached it runs one model call in a goroutine (30s timeout) that
// proposes at most three durable memories. Candidates are scrubbed for secrets,
// validated, merged against the live memories of their scope and written with
// source=auto in the status the scope's settings mode asks for (auto -> active,
// suggest -> pending; off writes nothing). Every failure is logged and dropped:
// the extractor must never affect the conversation or block a turn.
const (
	// memoryExtractionTimeout bounds one extractor model call.
	memoryExtractionTimeout = 30 * time.Second
	// memoryExtractionRecentMessages / Bytes bound the transcript sent to the
	// extractor (design: ~20 messages / <=30 KiB).
	memoryExtractionRecentMessages = 20
	memoryExtractionRecentBytes    = 30 * 1024
	// memoryExtractionMaxMessageBytes clips one oversized message.
	memoryExtractionMaxMessageBytes = 4 * 1024
	// memoryExtractionReplyBytes caps the model reply read back.
	memoryExtractionReplyBytes = 64 * 1024
	// memoryConsolidationWriteThreshold triggers a consolidation pass after
	// this many automatic writes (in addition to the startup/6h cadence).
	memoryConsolidationWriteThreshold = 20
)

// memoryWorker owns the per-session turn counters, the in-flight set and the
// write counter of the automatic-memory pipeline. All exported seams
// (settings/newProvider/async/execute) are injectable so tests can drive the
// worker without a network model.
type memoryWorker struct {
	st       *store.Store
	settings func() (session.Settings, error)
	baseCtx  context.Context
	// newProvider builds the extractor provider from persisted settings. It
	// returns (nil, nil) when no usable model is configured; that is a silent
	// skip, not an error.
	newProvider func() (runtime.Provider, error)
	// execute runs one extraction for a session and returns how many memories
	// were written. Production uses extractAndMerge.
	execute func(ctx context.Context, sessionID, turnID string) (int, error)
	// async runs fn; production uses `go fn()`, tests run it inline.
	async func(fn func())
	// consolidate triggers one consolidation pass (nil when the app has no
	// maintenance loop, e.g. in worker-only tests).
	consolidate func()
	// permission resolves a session's access mode; Read Only sessions never
	// write automatic memories (the design's RT-03-consistent rule).
	permission func(sessionID string) string

	mu             sync.Mutex
	counts         map[string]int
	inflight       map[string]bool
	writesSinceRun int
	closed         bool
	wg             sync.WaitGroup
}

// newMemoryWorker builds the extractor worker for one app instance.
func newMemoryWorker(st *store.Store, settings func() (session.Settings, error), baseCtx context.Context) *memoryWorker {
	worker := &memoryWorker{
		st:       st,
		settings: settings,
		baseCtx:  baseCtx,
		counts:   make(map[string]int),
		inflight: make(map[string]bool),
		async:    func(fn func()) { go fn() },
	}
	worker.newProvider = func() (runtime.Provider, error) { return memoryProviderFromSettings(st) }
	worker.execute = worker.extractAndMerge
	return worker
}

// close stops launching new extractions. An extraction already in flight is
// cancelled through the worker context by the caller (app shutdown).
func (w *memoryWorker) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

// wait blocks until every launched extraction goroutine returned. It is used by
// tests and by a defensive shutdown path; production shutdown does not block on
// model calls.
func (w *memoryWorker) wait() {
	if w == nil {
		return
	}
	w.wg.Wait()
}

// observe folds one finished turn into the extractor counters. Only successful
// turns that produced output count, matching the design's trigger contract.
func (w *memoryWorker) observe(outcome session.TurnOutcome) {
	if w == nil || w.st == nil {
		return
	}
	if outcome.Failed || outcome.Stopped || !outcome.HasOutput {
		return
	}
	sessionID := strings.TrimSpace(outcome.SessionID)
	if sessionID == "" {
		return
	}
	settings, err := w.settings()
	if err != nil {
		return
	}
	settings = session.NormalizeSettings(settings)
	if !settings.MemoryExtractor {
		return
	}
	if w.permission != nil && w.permission(sessionID) == session.PermissionAsk {
		// Read Only sessions promise no persistent side effects; automatic
		// memories are exactly such a side effect.
		return
	}
	interval := settings.MemoryExtractionInterval
	if interval < 1 {
		interval = session.MemoryExtractionDefaultInterval
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.counts[sessionID]++
	if w.counts[sessionID] < interval {
		w.mu.Unlock()
		return
	}
	if w.inflight[sessionID] {
		// A run is already in flight for this session: ignore the duplicate
		// trigger instead of queueing a second extraction.
		w.mu.Unlock()
		return
	}
	w.counts[sessionID] = 0
	w.inflight[sessionID] = true
	w.mu.Unlock()

	ctx, cancel := context.WithTimeout(w.baseCtx, memoryExtractionTimeout)
	w.wg.Add(1)
	w.async(func() {
		defer w.wg.Done()
		defer cancel()
		defer func() {
			w.mu.Lock()
			delete(w.inflight, sessionID)
			w.mu.Unlock()
		}()
		writes, err := w.execute(ctx, sessionID, outcome.TurnID)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("zeokeel: memory extractor: session %s: %v", sessionID, err)
			}
			return
		}
		if writes > 0 {
			w.noteWrites(writes)
		}
	})
}

// noteWrites tracks automatic writes and triggers a consolidation pass when the
// configured threshold is crossed.
func (w *memoryWorker) noteWrites(writes int) {
	if w == nil || writes <= 0 {
		return
	}
	w.mu.Lock()
	w.writesSinceRun += writes
	trigger := w.writesSinceRun >= memoryConsolidationWriteThreshold
	if trigger {
		w.writesSinceRun = 0
	}
	consolidate := w.consolidate
	w.mu.Unlock()
	if trigger && consolidate != nil {
		consolidate()
	}
}

// extractAndMerge runs one extraction: prompt, model call, parse, scrub, merge
// and write. It returns the number of memories written.
func (w *memoryWorker) extractAndMerge(ctx context.Context, sessionID, turnID string) (int, error) {
	if w.st == nil {
		return 0, nil
	}
	settings, err := w.settings()
	if err != nil {
		return 0, err
	}
	settings = session.NormalizeSettings(settings)
	if !settings.MemoryExtractor {
		return 0, nil
	}
	provider, err := w.newProvider()
	if err != nil {
		return 0, err
	}
	if provider == nil {
		// No model configured: silent skip, never an error or a retry storm.
		return 0, nil
	}
	target, err := w.st.GetSession(sessionID)
	if err != nil {
		return 0, err
	}
	recent, err := w.recentMessages(sessionID)
	if err != nil {
		return 0, err
	}
	if len(recent) == 0 {
		return 0, nil
	}
	global, err := w.activeMemories(store.MemoryScopeGlobal, "")
	if err != nil {
		return 0, err
	}
	var workspace []store.Memory
	if workspaceID := strings.TrimSpace(target.WorkspaceID); workspaceID != "" {
		workspace, err = w.activeMemories(store.MemoryScopeWorkspace, workspaceID)
		if err != nil {
			return 0, err
		}
	}
	system, user := memory.ExtractionPrompt(global, workspace, recent)
	reply, err := completeProviderText(ctx, provider, system, user)
	if err != nil {
		return 0, err
	}
	candidates, err := memory.ParseExtraction(reply)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	scrubbed := make([]memory.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, found := memory.ScrubSecrets(candidate.Content); found {
			log.Printf("zeokeel: memory extractor: dropped a candidate containing secret-like content")
			continue
		}
		scrubbed = append(scrubbed, candidate)
	}
	if len(scrubbed) == 0 {
		return 0, nil
	}
	return w.applyCandidates(target, turnID, settings, scrubbed)
}

// applyCandidates merges the candidates into each scope's live memories and
// writes the resulting decisions with source=auto.
func (w *memoryWorker) applyCandidates(target *store.Session, turnID string, settings session.Settings, candidates []memory.Candidate) (int, error) {
	byScope := make(map[string][]memory.Candidate)
	for _, candidate := range candidates {
		byScope[candidate.Scope] = append(byScope[candidate.Scope], candidate)
	}
	writes := 0
	var firstErr error
	for scope, grouped := range byScope {
		workspaceID := ""
		mode := memory.NormalizeMode(settings.MemoryWorkspaceMode)
		if scope == memory.ScopeGlobal {
			mode = memory.NormalizeMode(settings.MemoryGlobalMode)
		} else {
			workspaceID = strings.TrimSpace(target.WorkspaceID)
			if workspaceID == "" {
				continue
			}
		}
		status, allowed := memory.StatusForMode(mode)
		if !allowed {
			continue
		}
		existing, err := w.liveMemories(scope, workspaceID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, decision := range memory.MergeCandidates(existing, grouped, memory.MaxCandidates) {
			if decision.Action == memory.ActionSkip {
				continue
			}
			written, err := w.writeDecision(scope, workspaceID, status, target, turnID, decision)
			if err != nil {
				log.Printf("zeokeel: memory extractor: write %s for session %s: %v", decision.Action, target.ID, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if written {
				writes++
			}
		}
	}
	return writes, firstErr
}

// writeDecision persists one merge decision. Exact-hash duplicates are handled
// by the store's scoped create (it refreshes the existing row); a supersede in
// auto mode additionally retires the old row.
func (w *memoryWorker) writeDecision(scope, workspaceID, status string, target *store.Session, turnID string, decision memory.Decision) (bool, error) {
	row := store.Memory{
		Scope:       scope,
		WorkspaceID: workspaceID,
		Kind:        decision.Candidate.Kind,
		Source:      store.MemorySourceAuto,
		Status:      status,
		Content:     decision.Candidate.Content,
		Confidence:  decision.Candidate.Confidence,
		Importance:  importanceForConfidence(decision.Candidate.Confidence),
		SessionID:   target.ID,
		TurnID:      turnID,
	}
	created, err := w.st.CreateMemoryScoped(row)
	if err != nil {
		return false, err
	}
	if created == nil {
		return false, nil
	}
	// A re-captured candidate that already existed as a suggestion becomes
	// active once the scope is in auto mode; suggest mode never demotes an
	// active memory.
	if status == store.MemoryStatusActive && created.Status != store.MemoryStatusActive {
		if _, err := w.st.SetMemoryStatus(created.ID, store.MemoryStatusActive); err != nil {
			return false, err
		}
	}
	if decision.Action == memory.ActionSupersede && status == store.MemoryStatusActive &&
		decision.ExistingID != "" && decision.ExistingID != created.ID {
		if err := w.st.SupersedeMemory(decision.ExistingID, created.ID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// activeMemories returns a scope's active memories for prompt dedupe context.
func (w *memoryWorker) activeMemories(scope, workspaceID string) ([]store.Memory, error) {
	return w.st.ListMemoriesScoped(scope, workspaceID, store.MemoryStatusActive, store.MaxMemoryLimit)
}

// liveMemories returns the active+pending rows MergeCandidates deduplicates
// against (superseded rows are history and must not be revived).
func (w *memoryWorker) liveMemories(scope, workspaceID string) ([]store.Memory, error) {
	rows, err := w.st.ListMemoriesScoped(scope, workspaceID, "", store.MaxMemoryLimit)
	if err != nil {
		return nil, err
	}
	live := make([]store.Memory, 0, len(rows))
	for _, row := range rows {
		switch row.Status {
		case store.MemoryStatusActive, store.MemoryStatusPending:
			live = append(live, row)
		}
	}
	return live, nil
}

// recentMessages returns the newest usable conversation messages, bounded by
// count and by total bytes.
func (w *memoryWorker) recentMessages(sessionID string) ([]memory.Message, error) {
	messages, err := w.st.ListMessages(sessionID)
	if err != nil {
		return nil, err
	}
	recent := make([]memory.Message, 0, memoryExtractionRecentMessages)
	total := 0
	for index := len(messages) - 1; index >= 0 && len(recent) < memoryExtractionRecentMessages; index-- {
		message := messages[index]
		if message.Role != string(runtime.RoleUser) && message.Role != string(runtime.RoleAssistant) {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		// Injected referenced-session snapshots are untrusted text from other
		// sessions. They must never enter automatic memory analysis: otherwise
		// a referenced session's instructions could be laundered into a durable
		// active memory and re-injected into future system prompts
		// (ADR 0015 §3). IsContextText checks the stable envelope, so a normal
		// user message that merely starts with the heading is still kept.
		if sessionreference.IsContextText(content) {
			continue
		}
		budget := memoryExtractionRecentBytes - total
		if budget <= 0 {
			break
		}
		if limit := memoryExtractionMaxMessageBytes; budget > limit {
			budget = limit
		}
		if len(content) > budget {
			content = memoryClipText(content, budget)
		}
		total += len(content)
		recent = append(recent, memory.Message{Role: message.Role, Content: content})
	}
	// Walked newest-first; restore chronological order for the prompt.
	for left, right := 0, len(recent)-1; left < right; left, right = left+1, right-1 {
		recent[left], recent[right] = recent[right], recent[left]
	}
	return recent, nil
}

// memoryClipText bounds text to limit bytes on a UTF-8 boundary.
func memoryClipText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + " ...[truncated]"
}

// importanceForConfidence derives the consolidation importance from the
// extractor's confidence: higher-confidence memories survive the active cap
// longer.
func importanceForConfidence(confidence float64) int {
	if confidence <= 0 || confidence > 1 {
		return 0
	}
	return int(math.Round(confidence * 10))
}

// memoryProviderFromSettings builds a one-shot extractor provider from the same
// provider settings the agent uses. (nil, nil) means "no usable model": the
// extractor silently skips.
func memoryProviderFromSettings(st *store.Store) (runtime.Provider, error) {
	get := func(key, env, def string) string {
		if st != nil {
			if value, err := st.GetSetting(key); err == nil && strings.TrimSpace(value) != "" {
				return value
			}
		}
		if env != "" {
			if value := strings.TrimSpace(os.Getenv(env)); value != "" {
				return value
			}
		}
		return def
	}
	baseURL := get("provider.base_url", "ZEOKEEL_PROVIDER_BASE_URL", openai.DefaultBaseURL)
	apiKey := get("provider.api_key", "DEEPSEEK_API_KEY", "")
	if apiKey == "" && !keylessLocalProvider(baseURL) {
		return nil, nil
	}
	model := get("provider.model", "ZEOKEEL_PROVIDER_MODEL", openai.DefaultModel)
	config := openai.Config{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: memoryExtractionTimeout,
	}
	if proxy := networkProxyFromSetting(get); proxy != nil {
		config.HTTPClient = providerProxyHTTPClient(proxy)
	}
	provider, err := openai.New(config)
	if err != nil {
		return nil, fmt.Errorf("memory extractor provider: %w", err)
	}
	return provider, nil
}

// completeProviderText runs one non-streaming-style completion through the
// runtime provider interface and returns the concatenated text, bounded.
func completeProviderText(ctx context.Context, provider runtime.Provider, system, user string) (string, error) {
	stream, err := provider.Stream(ctx, runtime.Request{Messages: []runtime.Message{
		{Role: runtime.RoleSystem, Content: system},
		{Role: runtime.RoleUser, Content: user},
	}})
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-stream:
			if !ok {
				return builder.String(), nil
			}
			if chunk.Err != nil {
				return "", chunk.Err
			}
			if chunk.Text != "" {
				if builder.Len()+len(chunk.Text) > memoryExtractionReplyBytes {
					return builder.String(), nil
				}
				builder.WriteString(chunk.Text)
			}
			if chunk.Done {
				return builder.String(), nil
			}
		}
	}
}

// observeTurnAndMemory is the controller's single TurnFinishedHook: it fans the
// outcome out to the existing observers and then to the auto-memory extractor.
// It lives here so the app.go wiring stays a one-line registration.
func (a *app) observeTurnAndMemory(outcome session.TurnOutcome) {
	if a == nil {
		return
	}
	a.observeTurnFinished(outcome)
	a.observeMemoryTurn(outcome)
}

// observeMemoryTurn is the app's turn-finished hook for the extractor. It is a
// no-op until startServices installs the worker.
func (a *app) observeMemoryTurn(outcome session.TurnOutcome) {
	if a == nil {
		return
	}
	a.memoryMu.Lock()
	worker := a.memoryWorker
	a.memoryMu.Unlock()
	if worker == nil {
		return
	}
	worker.observe(outcome)
}

// startMemoryMaintenance installs the extractor worker and starts the
// startup+6h consolidation loop. It is called once from startServices; both are
// torn down by stopMemoryMaintenance before the store closes.
func (a *app) startMemoryMaintenance(ctrl *session.Controller) {
	if a == nil || ctrl == nil {
		return
	}
	st := ctrl.Store()
	if st == nil {
		return
	}
	a.memoryMu.Lock()
	if a.memoryCancel != nil {
		a.memoryMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := newMemoryWorker(st, func() (session.Settings, error) { return ctrl.Settings().Load() }, ctx)
	worker.consolidate = func() { go func() { _, _ = ConsolidateMemories(ctx, st) }() }
	worker.permission = ctrl.SessionPermissionOrDefault
	a.memoryWorker = worker
	a.memoryCancel = cancel
	a.memoryMu.Unlock()
	go memoryConsolidationLoop(ctx, st, memoryConsolidationInterval)
}

// stopMemoryMaintenance cancels the maintenance loop and disables the worker.
func (a *app) stopMemoryMaintenance() {
	if a == nil {
		return
	}
	a.memoryMu.Lock()
	cancel := a.memoryCancel
	worker := a.memoryWorker
	a.memoryCancel = nil
	a.memoryWorker = nil
	a.memoryMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if worker != nil {
		worker.close()
	}
}
