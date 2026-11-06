package shell

import (
	"context"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// AR-08: the frame must never wait on the database. The composer's @ mention
// menu and the sidebar render session/workspace lists, which used to hit the
// store synchronously inside the view. The shell now keeps a cache that is
// primed on first use (so the first frame shows the same data as before) and
// refreshed by a background goroutine; while a refresh runs, the view keeps
// rendering the cached list, so a slow disk delays only the refresh, never a
// frame.

const (
	shellListFreshFor = 1500 * time.Millisecond
	shellListLoadWait = 25 * time.Millisecond
)

// sessionListCache caches the store's session list.
type sessionListCache struct {
	mu       sync.Mutex
	items    []store.Session
	loadedAt time.Time
	loading  bool
	loadSlow bool
}

// sessionLister is the injectable store seam used by the tests to simulate a
// slow database without touching the real store.
var sessionLister func(ctx context.Context) ([]store.Session, error)

// sessions returns the cached session list, refreshing it in the background
// when stale. The first call primes the cache; it waits only very briefly (see
// shellListLoadWait) so even the first frame stays responsive, and an empty
// list is a valid render state.
func (a *App) sessions() []store.Session {
	if a.Ctrl == nil {
		return nil
	}
	cache := a.shellUI().sessionCache
	if cache == nil {
		cache = &sessionListCache{}
		a.shellUI().sessionCache = cache
	}
	cache.mu.Lock()
	fresh := time.Since(cache.loadedAt) < shellListFreshFor
	items := cache.items
	loading := cache.loading
	cache.mu.Unlock()
	if fresh || loading {
		return items
	}
	// Prime synchronously for the first load only: the sidebar and mention
	// menu must show real data immediately after startup. Once loaded, every
	// refresh happens off the frame thread.
	firstLoad := cache.loadedAt.IsZero()
	if firstLoad {
		list, err := a.loadSessionList(context.Background())
		if err == nil {
			cache.mu.Lock()
			cache.items = list
			cache.loadedAt = time.Now()
			cache.mu.Unlock()
			return list
		}
		return nil
	}
	cache.mu.Lock()
	cache.loading = true
	cache.loadSlow = false
	cache.mu.Unlock()
	go func() {
		started := time.Now()
		list, err := a.loadSessionList(context.Background())
		cache.mu.Lock()
		cache.loading = false
		if err == nil {
			cache.items = list
			cache.loadedAt = time.Now()
			cache.loadSlow = time.Since(started) > shellListLoadWait
		}
		cache.mu.Unlock()
	}()
	return items
}

// loadSessionList reads through the injectable seam, or the controller.
func (a *App) loadSessionList(ctx context.Context) ([]store.Session, error) {
	if sessionLister != nil {
		return sessionLister(ctx)
	}
	if a.Ctrl == nil {
		return nil, nil
	}
	return a.Ctrl.ListSessions(ctx)
}

// invalidateSessionList marks the cache stale so the next frame refreshes it
// in the background (used after a session is created, renamed or deleted).
func (a *App) invalidateSessionList() {
	cache := a.shellUI().sessionCache
	if cache == nil {
		return
	}
	cache.mu.Lock()
	cache.loadedAt = time.Time{}
	cache.mu.Unlock()
}

// sessionsForWorkspace returns the cached sessions of one workspace. It never
// queries the store: the caller renders titles from the shared session list.
func (a *App) sessionsForWorkspace(workspaceID string) []store.Session {
	out := make([]store.Session, 0, 8)
	for _, sess := range a.sessions() {
		if sess.WorkspaceID == workspaceID {
			out = append(out, sess)
		}
	}
	return out
}

// invalidateSessionList is called by the session mutations below so a new or
// deleted session shows up on the next frame instead of after the TTL.
func (a *App) sessionListLoadedAt() time.Time {
	cache := a.shellUI().sessionCache
	if cache == nil {
		return time.Time{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.loadedAt
}
