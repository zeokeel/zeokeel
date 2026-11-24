package gateway

import (
	"context"
	"errors"
)

// Snapshot and subscription plumbing shared by every domain (§6.5).
//
// A snapshot is served from the domain cache, or built on demand by a worker and
// parked on a waiter channel: the hub goroutine never calls a backend (§7.3). A
// subscription either replays the ring from the client's rev or tells the client
// to take a fresh snapshot.

// subscribeReply is the hub's answer to a subscribe request.
type subscribeReply struct {
	rev    uint64
	resync bool
	replay []ringEntry
}

// snapshotDomain returns the current snapshot of any domain, building it on
// demand.
func (h *hub) snapshotDomain(ctx context.Context, key string) (domainSnapshot, error) {
	type wait struct {
		ready bool
		snap  domainSnapshot
		ch    chan domainSnapshot
	}
	waited, err := hubCall(ctx, h, func(hh *hub) (wait, error) {
		domain := hh.domains[key]
		if domain != nil && domain.cache != nil {
			domain.touched = hh.nextTick()
			return wait{ready: true, snap: domainSnapshot{rev: domain.rev, payload: domain.cache, found: true}}, nil
		}
		ch := make(chan domainSnapshot, 1)
		hh.waiters[key] = append(hh.waiters[key], ch)
		if domain == nil {
			hh.domains[key] = hh.newDomain(key)
		}
		hh.ensureBuild(key)
		return wait{ch: ch}, nil
	})
	if err != nil {
		return domainSnapshot{}, err
	}
	if waited.ready {
		return waited.snap, nil
	}
	select {
	case snap := <-waited.ch:
		if snap.err != nil {
			if errors.Is(snap.err, errSessionGone) {
				// A conversation that no longer exists is an answer ("not found"),
				// not a fault: handlers turn found=false into session_not_found.
				return domainSnapshot{}, nil
			}
			// Any other build failure is reported with its real cause, not as an
			// empty payload every handler would misread as a type error.
			return domainSnapshot{}, snap.err
		}
		return snap, nil
	case <-ctx.Done():
		return domainSnapshot{}, ctx.Err()
	}
}

// snapshotList is the session list's snapshot.
func (h *hub) snapshotList(ctx context.Context) (domainSnapshot, error) {
	return h.snapshotDomain(ctx, listDomain)
}

// snapshotSession is one conversation's snapshot.
func (h *hub) snapshotSession(ctx context.Context, sessionID string) (domainSnapshot, error) {
	return h.snapshotDomain(ctx, dKey(sessionID))
}

// subscribeDomain registers a client on a domain and reports what it must do to
// catch up: replay the returned ring entries (in order) or take a snapshot.
//
// Global domains are broadcast to every ready client anyway; subscribing to one
// only asks for the replay, which is what makes resume-after-reconnect cheap.
func (h *hub) subscribeDomain(ctx context.Context, clientID, key string, sinceRev uint64) (subscribeReply, error) {
	return hubCall(ctx, h, func(hh *hub) (subscribeReply, error) {
		domain := hh.domains[key]
		if domain == nil {
			domain = hh.newDomain(key)
			hh.domains[key] = domain
			hh.ensureBuild(key)
		}
		domain.subs[clientID] = true
		domain.touched = hh.nextTick()
		// A domain with no snapshot yet has no chain to resume from: the client
		// must take a snapshot first, even when it presents sinceRev 0.
		resync := domain.cache == nil || !canReplay(domain, sinceRev)
		return subscribeReply{rev: domain.rev, resync: resync, replay: replayFrom(domain, sinceRev)}, nil
	})
}

// unsubscribeDomain removes a client from one domain.
func (h *hub) unsubscribeDomain(ctx context.Context, clientID, key string) error {
	_, err := hubExecute(ctx, h, func(hh *hub) (any, error) {
		if domain := hh.domains[key]; domain != nil {
			delete(domain.subs, clientID)
			domain.touched = hh.nextTick()
		}
		return nil, nil
	})
	return err
}

// subscribeSession is subscribeDomain for a conversation.
func (h *hub) subscribeSession(ctx context.Context, clientID, sessionID string, sinceRev uint64) (subscribeReply, error) {
	return h.subscribeDomain(ctx, clientID, dKey(sessionID), sinceRev)
}

// unsubscribeSession is unsubscribeDomain for a conversation.
func (h *hub) unsubscribeSession(ctx context.Context, clientID, sessionID string) error {
	return h.unsubscribeDomain(ctx, clientID, dKey(sessionID))
}

// canReplay reports whether the ring still covers sinceRev. A ring entry whose
// baseRev is above sinceRev means the delta chain from sinceRev is gone.
func canReplay(domain *domainState, sinceRev uint64) bool {
	if sinceRev > domain.rev {
		return false
	}
	if sinceRev == domain.rev {
		return true
	}
	if len(domain.ring) == 0 {
		return false
	}
	return domain.ring[0].baseRev <= sinceRev
}

// replayFrom returns the ring entries newer than sinceRev, in rev order.
func replayFrom(domain *domainState, sinceRev uint64) []ringEntry {
	out := make([]ringEntry, 0, len(domain.ring))
	for _, entry := range domain.ring {
		if entry.rev > sinceRev {
			out = append(out, entry)
		}
	}
	return out
}

// observeDomain registers an in-process observer on one domain (the SSE run
// stream of §4.6) and returns the function that removes it.
func (h *hub) observeDomain(ctx context.Context, key string, observe func(deltaPayload)) (func(), error) {
	id, err := hubCall(ctx, h, func(hh *hub) (int, error) {
		if hh.domains[key] == nil {
			hh.domains[key] = hh.newDomain(key)
			hh.ensureBuild(key)
		}
		hh.observerSeq++
		id := hh.observerSeq
		if hh.observe[key] == nil {
			hh.observe[key] = make(map[int]func(deltaPayload))
		}
		hh.observe[key][id] = observe
		return id, nil
	})
	if err != nil {
		return nil, err
	}
	return func() {
		_, _ = hubExecute(context.Background(), h, func(hh *hub) (any, error) {
			delete(hh.observe[key], id)
			if len(hh.observe[key]) == 0 {
				delete(hh.observe, key)
			}
			return nil, nil
		})
	}, nil
}

// observeSession is observeDomain for a conversation.
func (h *hub) observeSession(ctx context.Context, sessionID string, observe func(*sessionDelta)) (func(), error) {
	return h.observeDomain(ctx, dKey(sessionID), func(delta deltaPayload) {
		if typed, ok := delta.(*sessionDelta); ok {
			observe(typed)
		}
	})
}

// registerClient adds one connection. The client starts unready: no delta is
// pushed before it acknowledges the handshake with `ready` (§4.2).
func (h *hub) registerClient(ctx context.Context, target *client) error {
	_, err := hubExecute(ctx, h, func(hh *hub) (any, error) {
		hh.clients[target.id] = target
		return nil, nil
	})
	return err
}

// unregisterClient drops one connection and every subscription it held.
func (h *hub) unregisterClient(target *client) {
	_, _ = hubExecute(context.Background(), h, func(hh *hub) (any, error) {
		delete(hh.clients, target.id)
		for _, domain := range hh.domains {
			delete(domain.subs, target.id)
		}
		return nil, nil
	})
}

// clientSnapshot returns the current connections (used by shutdown, which must
// close them from outside the hub goroutine).
func (h *hub) clientSnapshot(ctx context.Context) ([]*client, error) {
	return hubCall(ctx, h, func(hh *hub) ([]*client, error) {
		out := make([]*client, 0, len(hh.clients))
		for _, target := range hh.clients {
			out = append(out, target)
		}
		return out, nil
	})
}
