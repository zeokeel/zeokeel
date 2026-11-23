package gateway

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Domain names (docs/TRANSPORT.md §6.2). A domain is one independently
// versioned read model: it owns a rev, a cached snapshot, a replay ring and the
// set of clients watching it. Every domain — the two conversation domains and
// the six small ones — rides this one mechanism; only the snapshot builder and
// the diff function differ per domain (domains.go).
const (
	// listDomain is the sidebar list; every ready client watches it.
	listDomain = "session.list"
	// maxSessionDomains bounds how many conversation domains stay cached in one
	// gateway run. Evicting one costs a resync, never a wrong frame.
	maxSessionDomains = 32
)

// domainState is one versioned read model.
type domainState struct {
	// key is the map key: "session.list", "session:<id>", "approvals", …
	key string
	// sessionID is set only for conversation domains.
	sessionID string
	// global marks a domain every ready client receives without subscribing
	// (approvals, ask, plan, artifacts, settings, workspace, the session list).
	// Conversation domains are delivered to their subscribers only.
	global bool
	rev    uint64
	// cache holds the domain's snapshot type (sessionDTO, []sessionInfoDTO,
	// approvalsSnapshot, …).
	cache any
	// ring is the bounded replay history. For an unwatched conversation domain
	// the ring collapses to one resync marker (per-token history nobody is
	// watching would be pure memory burn, §6.5).
	ring      []ringEntry
	ringBytes int
	subs      map[string]bool
	touched   uint64
}

// ringEntry is one replayable delta.
type ringEntry struct {
	baseRev uint64
	rev     uint64
	bytes   int
	payload deltaPayload
}

// hubResult is one answered command.
type hubResult struct {
	value any
	err   error
}

// hubCommand is one unit of work executed on the hub goroutine. Handlers run
// there so rev ordering and the caches have exactly one writer (§7.3).
type hubCommand struct {
	fn    func(*hub) (any, error)
	reply chan hubResult
}

// hub owns the per-domain revisions, caches, rings and subscribers, and is the
// only place a broadcast is composed. Backend reads (chat, settings, workspace)
// run on worker goroutines; only the diff/broadcast path is serialized here.
type hub struct {
	epoch     string
	running   atomic.Bool
	commands  chan hubCommand
	done      chan struct{}
	closeOnce sync.Once

	chat    contracts.ChatService
	sources *domainSources
	unsub   func()
	clients map[string]*client
	domains map[string]*domainState
	tick    uint64
	// refreshTimeout bounds a synchronous refresh's dispatch (tests shorten it).
	refreshTimeout time.Duration

	// Build coordination. pending/dirtySignal are touched from backend
	// goroutines (the chat subscription, the settings poller); the rest is
	// hub-goroutine state.
	mu sync.Mutex
	// builds tracks the background snapshot builds (ensureBuild) so stop can
	// wait for them: a build reads kernel services, and tearing the assembly
	// down while one is in flight races the kernel's own service withdrawal.
	builds      sync.WaitGroup
	pending     map[string]bool
	dirtySignal chan struct{}
	building    map[string]bool
	waiters     map[string][]chan domainSnapshot
	observe     map[string]map[int]func(deltaPayload)
	observerSeq int
}

// domainSnapshot is the answer to a snapshot request for any domain.
type domainSnapshot struct {
	rev     uint64
	payload any
	// found is false for a conversation that does not exist.
	found bool
}

// newHub builds the hub for one epoch. chat may be nil until Serve resolves it.
func newHub(epoch string, chat contracts.ChatService) *hub {
	return &hub{
		epoch:       epoch,
		commands:    make(chan hubCommand, 64),
		done:        make(chan struct{}),
		chat:        chat,
		clients:     make(map[string]*client),
		domains:     make(map[string]*domainState),
		pending:     make(map[string]bool),
		dirtySignal: make(chan struct{}, 1),
		building:    make(map[string]bool),
		waiters:     make(map[string][]chan domainSnapshot),
		observe:     make(map[string]map[int]func(deltaPayload)),
	}
}

// start subscribes to the chat service and runs the hub loop until ctx ends.
func (h *hub) start(ctx context.Context) error {
	if h.chat != nil {
		unsub, err := h.chat.Subscribe(func(sessionID string) {
			h.markDirty(sessionID)
		})
		if err != nil {
			return err
		}
		h.unsub = unsub
	}
	h.running.Store(true)
	go h.run(ctx)
	return nil
}

// isRunning reports whether the hub goroutine is serving commands.
func (h *hub) isRunning() bool { return h != nil && h.running.Load() }

// stop releases the chat subscription.
func (h *hub) stop() {
	h.running.Store(false)
	if h.unsub != nil {
		h.unsub()
		h.unsub = nil
	}
	// Wait for in-flight builds: each one reads backend services, and the caller
	// (Gateway.Close) disposes the assembly right after this returns. The wait is
	// bounded so a stuck backend cannot hang shutdown.
	settled := make(chan struct{})
	go func() {
		h.builds.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
	}
}

// markDirty records that one conversation changed. It is called synchronously
// from the agent goroutine, so it only records state and pokes the loop.
func (h *hub) markDirty(sessionID string) {
	h.mu.Lock()
	h.pending[dKey(sessionID)] = true
	// The sidebar row changes with the session (title, running, unread,
	// message count), so every session change dirties the list too.
	h.pending[listDomain] = true
	h.mu.Unlock()
	h.poke()
}

// markDomainDirty records that one of the small domains changed.
func (h *hub) markDomainDirty(key string) {
	if !h.isRunning() {
		return
	}
	h.mu.Lock()
	h.pending[key] = true
	h.mu.Unlock()
	h.poke()
}

// refreshWatchedPrompts marks the inbox domains that have a subscriber dirty.
// It is the pull half of the prompts domain: the backend (contracts.PromptQueue)
// has no change notification, so the poller re-reads subscribed inboxes.
//
// The scan runs on the hub goroutine: h.domains is owned by it (builds insert
// domains without taking h.mu), so a poller reading the map directly is a data
// race — the -race build caught exactly that (§ "h.domains owner" in the hub
// comment). Only h.pending is mutex-protected, because external goroutines write
// it.
func (h *hub) refreshWatchedPrompts() {
	h.refreshWatchedPrefix(domainPromptsPrefix)
}

// refreshWatchedPrefix marks every watched domain whose key starts with prefix
// dirty. It is the pull half of the per-session domains that have no change
// notification (prompts, context): only subscribed domains are re-read, so an
// idle gateway costs nothing for conversations nobody is looking at.
func (h *hub) refreshWatchedPrefix(prefix string) {
	if !h.isRunning() {
		return
	}
	marked, err := hubCall(context.Background(), h, func(hh *hub) (int, error) {
		count := 0
		for key, domain := range hh.domains {
			if !strings.HasPrefix(key, prefix) || len(domain.subs) == 0 {
				continue
			}
			hh.pending[key] = true
			count++
		}
		return count, nil
	})
	if err != nil {
		return
	}
	if marked > 0 {
		h.poke()
	}
}

// poke wakes the hub loop.
func (h *hub) poke() {
	select {
	case h.dirtySignal <- struct{}{}:
	default:
	}
}

// run is the hub goroutine.
func (h *hub) run(ctx context.Context) {
	defer h.closeOnce.Do(func() { close(h.done) })
	for {
		select {
		case <-ctx.Done():
			return
		case command := <-h.commands:
			value, err := command.fn(h)
			select {
			case command.reply <- hubResult{value: value, err: err}:
			case <-ctx.Done():
			}
		case <-h.dirtySignal:
			h.processDirty()
		}
	}
}

// processDirty starts one rebuild per dirty domain.
func (h *hub) processDirty() {
	h.mu.Lock()
	keys := make([]string, 0, len(h.pending))
	for key := range h.pending {
		keys = append(keys, key)
		h.pending[key] = false
	}
	h.mu.Unlock()
	sort.Strings(keys)
	for _, key := range keys {
		h.ensureBuild(key)
	}
}

// hubExecute runs fn on the hub goroutine and waits for the result.
func hubExecute(ctx context.Context, h *hub, fn func(*hub) (any, error)) (any, error) {
	reply := make(chan hubResult, 1)
	select {
	case h.commands <- hubCommand{fn: fn, reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.done:
		return nil, errors.New("gateway: gateway is shutting down")
	}
	select {
	case result := <-reply:
		return result.value, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.done:
		return nil, errors.New("gateway: gateway is shutting down")
	}
}

// hubCall is the typed form of hubExecute.
func hubCall[T any](ctx context.Context, h *hub, fn func(*hub) (T, error)) (T, error) {
	value, err := hubExecute(ctx, h, func(hh *hub) (any, error) {
		typed, fnErr := fn(hh)
		return typed, fnErr
	})
	var zero T
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, errors.New("gateway: internal hub result type mismatch")
	}
	return typed, nil
}

// ensureBuild starts one worker rebuild for a domain unless one is in flight.
// The backend read happens off the hub goroutine (§7.3).
func (h *hub) ensureBuild(key string) {
	h.mu.Lock()
	if h.building[key] {
		h.mu.Unlock()
		return
	}
	h.building[key] = true
	h.builds.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.builds.Done()
		payload, err := h.buildDomain(key)
		_, _ = hubExecute(context.Background(), h, func(hh *hub) (any, error) {
			hh.applyBuild(key, payload, err)
			return nil, nil
		})
	}()
}

// errSessionGone reports a conversation that disappeared between two builds.
var errSessionGone = errors.New("gateway: session is gone")

// buildDomain reads one domain's snapshot from its backend. It runs on a worker
// goroutine, never on the hub goroutine.
func (h *hub) buildDomain(key string) (any, error) {
	if sessionID, ok := sessionKey(key); ok {
		if h.chat == nil {
			return nil, errors.New("gateway: chat backend is not available")
		}
		session, found := h.chat.Session(sessionID)
		if !found {
			return nil, errSessionGone
		}
		return newSessionDTO(session), nil
	}
	if key == listDomain {
		if h.chat == nil {
			return nil, errors.New("gateway: chat backend is not available")
		}
		return newSessionListDTO(h.chat.Sessions()), nil
	}
	// The small domains read their backend through the gateway (domains.go).
	if h.sources == nil {
		return nil, errors.New("gateway: domain sources are not configured")
	}
	return h.sources.build(key)
}

// refreshNow rebuilds one domain on the CALLER's goroutine (so a backend write
// is published before the command returns) and applies it on the hub. The
// backend read never runs on the hub goroutine, which is what keeps a slow
// service from stalling every broadcast (§7.3).
//
// refreshNow dispatches ONE command to the hub and waits for its reply, so it
// must never be called from a hub command function (that would wait for the very
// goroutine it blocks). The wait is bounded by refreshTimeout, which turns such a
// mistake into a fast, named error instead of a hung gateway.
func (h *hub) refreshNow(ctx context.Context, key string) error {
	if !h.isRunning() {
		return errors.New("gateway: hub is not running")
	}
	payload, err := h.buildDomain(key)
	if err != nil {
		return err
	}
	timeout := h.refreshTimeout
	if timeout <= 0 {
		timeout = defaultRefreshTimeout
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err = hubExecute(dispatchCtx, h, func(hh *hub) (any, error) {
		hh.applyBuild(key, payload, nil)
		return nil, nil
	})
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return errors.New("gateway: hub refresh timed out; refreshNow must not be called from a hub command")
	}
	return err
}

// defaultRefreshTimeout bounds a synchronous refresh's dispatch.
const defaultRefreshTimeout = 10 * time.Second

// applyBuild installs a rebuilt snapshot, bumps the rev, records the delta in
// the ring and broadcasts it.
func (h *hub) applyBuild(key string, payload any, buildErr error) {
	h.mu.Lock()
	h.building[key] = false
	again := h.pending[key]
	h.pending[key] = false
	h.mu.Unlock()

	domain := h.domains[key]
	if buildErr != nil {
		if domain != nil {
			domain.rev++
			h.publish(domain, h.resyncDelta(domain, domain.rev-1, domain.rev))
			h.answerWaiters(key, domainSnapshot{rev: domain.rev})
		} else {
			h.answerWaiters(key, domainSnapshot{})
		}
		if again {
			h.ensureBuild(key)
		}
		return
	}
	if domain == nil {
		// A snapshot was requested before anyone subscribed: make the domain so
		// the snapshot's rev is the one a later subscribe resumes from.
		domain = h.newDomain(key)
		h.domains[key] = domain
	}
	if domain.cache == nil {
		domain.cache = payload
	} else {
		delta := h.diffDomain(key, domain.cache, payload)
		domain.cache = payload
		if delta != nil {
			domain.rev++
			delta.setRevs(domain.rev-1, domain.rev)
			h.publish(domain, delta)
		}
	}
	h.answerWaiters(key, domainSnapshot{rev: domain.rev, payload: payload, found: true})
	h.evictSessionDomains()
	if again {
		h.ensureBuild(key)
	}
}

// publish records one delta in the ring and sends it to its watchers.
func (h *hub) publish(domain *domainState, delta deltaPayload) {
	base, rev := delta.revs()
	h.pushRing(domain, ringEntry{baseRev: base, rev: rev, bytes: delta.retainBytes(), payload: delta})
	h.broadcast(domain, delta)
}

// pushRing appends one replayable entry, collapsing the history when nobody is
// watching and trimming it to the ring budget otherwise.
func (h *hub) pushRing(domain *domainState, entry ringEntry) {
	if !domain.global && len(domain.subs) == 0 {
		domain.ring = []ringEntry{{
			baseRev: entry.baseRev,
			rev:     entry.rev,
			bytes:   64,
			payload: h.resyncDelta(domain, entry.baseRev, entry.rev),
		}}
		domain.ringBytes = 64
		return
	}
	domain.ring = append(domain.ring, entry)
	domain.ringBytes += entry.bytes
	for len(domain.ring) > DeltaRingEntries || (domain.ringBytes > DeltaRingBytes && len(domain.ring) > 1) {
		domain.ringBytes -= domain.ring[0].bytes
		domain.ring = domain.ring[1:]
	}
}

// broadcast sends one delta: conversation domains to their subscribers, global
// domains to every ready client (they are process-wide and cheap).
func (h *hub) broadcast(domain *domainState, delta deltaPayload) {
	if domain.global {
		for _, target := range h.clients {
			if target.isReady() {
				target.enqueueDelta(delta)
			}
		}
	} else {
		for clientID := range domain.subs {
			target := h.clients[clientID]
			if target == nil || !target.isReady() {
				continue
			}
			target.enqueueDelta(delta)
		}
	}
	for _, observe := range h.observe[domain.key] {
		observe(delta)
	}
}

// broadcastEvent pushes one domain-less event (ask.opened, toast.posted, …) to
// every ready client.
func (h *hub) broadcastEvent(method string, params any) {
	payload, err := encodeNotification(method, params)
	if err != nil {
		return
	}
	for _, target := range h.clients {
		if !target.isReady() {
			continue
		}
		target.enqueueControl(payload)
	}
}

// newDomain creates an empty domain for key.
func (h *hub) newDomain(key string) *domainState {
	h.tick++
	domain := &domainState{key: key, subs: make(map[string]bool), touched: h.tick}
	if sessionID, ok := sessionKey(key); ok {
		domain.sessionID = sessionID
	} else {
		// The session list and the six small domains are process-wide: every
		// ready client receives them without subscribing.
		domain.global = true
	}
	return domain
}

// evictSessionDomains keeps the number of cached conversation domains bounded,
// dropping the least recently touched subscriber-less one.
func (h *hub) evictSessionDomains() {
	count := 0
	for _, domain := range h.domains {
		if domain.sessionID != "" {
			count++
		}
	}
	for count > maxSessionDomains {
		var victimKey string
		var victim *domainState
		for key, domain := range h.domains {
			if domain.sessionID == "" || len(domain.subs) > 0 {
				continue
			}
			if victim == nil || domain.touched < victim.touched {
				victimKey, victim = key, domain
			}
		}
		if victim == nil {
			return
		}
		delete(h.domains, victimKey)
		count--
	}
}

// answerWaiters answers every snapshot request parked on a domain.
func (h *hub) answerWaiters(key string, snapshot domainSnapshot) {
	waiters := h.waiters[key]
	if len(waiters) == 0 {
		return
	}
	delete(h.waiters, key)
	for _, waiter := range waiters {
		select {
		case waiter <- snapshot:
		default:
		}
	}
}

// nextTick advances the LRU clock.
func (h *hub) nextTick() uint64 {
	h.tick++
	return h.tick
}

// dKey is the map key of one conversation domain.
func dKey(sessionID string) string { return "session:" + sessionID }

// sessionKey reports whether key addresses a conversation domain.
func sessionKey(key string) (string, bool) {
	if strings.HasPrefix(key, "session:") {
		return strings.TrimPrefix(key, "session:"), true
	}
	return "", false
}
