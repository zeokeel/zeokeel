package state

import (
	"context"
	"sync"
	"sync/atomic"
)

// DefaultBatchSize is the maximum number of events the projector folds before
// publishing a snapshot and invoking OnPublish.
const DefaultBatchSize = 256

// Option configures a Projector.
type Option func(*projectorConfig)

type projectorConfig struct {
	sessionID string
	batchSize int
}

// WithSessionID seeds the projector's initial snapshot with a session ID. Use
// it when the session exists before the first Token/Reasoning event; token
// events carrying a non-empty SessionID also set it.
func WithSessionID(id string) Option {
	return func(c *projectorConfig) { c.sessionID = id }
}

// WithBatchSize sets the maximum number of events folded per snapshot commit.
// It bounds how long the projector can go without publishing while a busy
// runtime keeps the subscription channel non-empty. Values below 1 restore
// DefaultBatchSize.
func WithBatchSize(n int) Option {
	return func(c *projectorConfig) { c.batchSize = n }
}

// Projector is the single consumer of a Broker's subscription. It folds events
// into immutable Snapshots on its own goroutine and publishes the current
// snapshot through an atomic pointer, so UI goroutines can read it without
// locking and without racing the fold.
type Projector struct {
	ctx     context.Context
	events  <-chan Event
	cancel  func()
	done    chan struct{}
	once    sync.Once
	snap    atomic.Pointer[Snapshot]
	batch   int
	mu      sync.RWMutex
	onBatch func(*Snapshot)
}

// NewProjector subscribes to b and starts folding events until ctx is done or
// Close is called. The subscription is created before the goroutine starts, so
// no event published after NewProjector returns can be missed for lack of a
// consumer (the broker's bounded buffer and drop policy still apply).
//
// b must be non-nil; NewProjector panics otherwise. A nil ctx is treated as
// context.Background.
func NewProjector(ctx context.Context, b *Broker, opts ...Option) *Projector {
	if ctx == nil {
		ctx = context.Background()
	}
	if b == nil {
		panic("state: NewProjector called with a nil Broker")
	}

	cfg := projectorConfig{batchSize: DefaultBatchSize}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.batchSize < 1 {
		cfg.batchSize = DefaultBatchSize
	}

	events, cancel := b.Subscribe()
	p := &Projector{
		ctx:    ctx,
		events: events,
		cancel: cancel,
		done:   make(chan struct{}),
		batch:  cfg.batchSize,
	}
	initial := NewSnapshot()
	initial.SessionID = cfg.sessionID
	p.snap.Store(initial)
	go p.run()
	return p
}

// Snapshot returns the latest published snapshot. It never returns nil and the
// returned value must be treated as read-only. Safe for concurrent use.
func (p *Projector) Snapshot() *Snapshot {
	return p.snap.Load()
}

// OnPublish installs fn, called on the projector goroutine after each applied
// event batch with the snapshot now current. It is called at most once per
// batch and never concurrently with itself. Passing nil removes the callback.
//
// fn should be cheap (the M0-8 contract is "swap state, invalidate the view");
// a slow callback makes the projector fall behind and can eventually make the
// broker drop events for this subscriber. Do not call Close from fn.
func (p *Projector) OnPublish(fn func(*Snapshot)) {
	p.mu.Lock()
	p.onBatch = fn
	p.mu.Unlock()
}

// Close unsubscribes, stops the projector goroutine and waits for it to exit.
// It is idempotent. The last snapshot remains available from Snapshot.
//
// Close must not be called synchronously from an OnPublish callback: the
// callback runs on the goroutine Close waits for. Spawn a goroutine instead.
func (p *Projector) Close() {
	p.once.Do(func() {
		p.cancel()
		<-p.done
	})
}

func (p *Projector) run() {
	defer close(p.done)
	defer p.cancel()

	snap := p.snap.Load()
	for {
		var ev Event
		select {
		case <-p.ctx.Done():
			return
		case e, ok := <-p.events:
			if !ok {
				return
			}
			ev = e
		}

		batchEvents := make([]Event, 0, p.batch)
		batchEvents = append(batchEvents, ev)
		closed := false
		// Fold everything already buffered into this batch so one atomic swap
		// and one callback cover a burst, instead of one per token. ApplyBatch
		// copies each changed collection once for the whole burst instead of
		// once per event (AR-22).
	drain:
		for len(batchEvents) < p.batch {
			select {
			case e, ok := <-p.events:
				if !ok {
					closed = true
					break drain
				}
				batchEvents = append(batchEvents, e)
			default:
				break drain
			}
		}
		snap = ApplyBatch(snap, batchEvents)

		p.commit(snap)
		if closed {
			return
		}
	}
}

func applyEvent(prev *Snapshot, ev Event) *Snapshot {
	if ev == nil {
		return prev
	}
	return Apply(prev, ev)
}

func (p *Projector) commit(snap *Snapshot) {
	p.snap.Store(snap)
	p.mu.RLock()
	fn := p.onBatch
	p.mu.RUnlock()
	if fn != nil {
		fn(snap)
	}
}
