package state

import (
	"sync"
	"sync/atomic"
)

// DefaultBufferSize is the per-subscriber channel capacity used by NewBroker.
const DefaultBufferSize = 256

// Broker is a bounded, non-blocking fan-out event bus: the runtime publishes,
// the projector subscribes as the single consumer.
//
// Publish never blocks. When a subscriber's buffer is full the event is
// dropped for that subscriber and counted (see Dropped); other subscribers are
// unaffected. Fast consumers therefore never wait on slow ones, and a slow
// subscriber can never stall the runtime.
//
// The zero value is usable and behaves like NewBroker.
type Broker struct {
	mu      sync.RWMutex
	subs    map[*subscriber]struct{}
	closed  bool
	buffer  int
	dropped atomic.Uint64
}

type subscriber struct {
	ch chan Event
}

// NewBroker returns a Broker whose subscribers buffer DefaultBufferSize events.
func NewBroker() *Broker {
	return NewBrokerWithBuffer(DefaultBufferSize)
}

// NewBrokerWithBuffer returns a Broker with a bufferSize-event buffer per
// subscriber. Values below 1 are clamped to 1.
func NewBrokerWithBuffer(bufferSize int) *Broker {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &Broker{buffer: bufferSize}
}

// Publish delivers ev to every current subscriber without blocking. It is safe
// to call concurrently from any number of goroutines. A nil ev, or a publish
// after Close, is ignored.
func (b *Broker) Publish(ev Event) {
	if ev == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for sub := range b.subs {
		select {
		case sub.ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Subscribe registers a subscriber and returns its receive channel plus an
// idempotent cancel function that unsubscribes and closes the channel. The
// channel is closed when cancel or Close runs; a subscription taken after
// Close is already closed.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	if b.subs == nil {
		b.subs = make(map[*subscriber]struct{})
	}
	sub := &subscriber{ch: make(chan Event, b.bufferSize())}
	b.subs[sub] = struct{}{}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if _, ok := b.subs[sub]; ok {
				delete(b.subs, sub)
				close(sub.ch)
			}
		})
	}
	return sub.ch, cancel
}

// Dropped returns the total number of events dropped because a subscriber's
// buffer was full. It is safe to call at any time.
func (b *Broker) Dropped() uint64 {
	return b.dropped.Load()
}

// Close closes every subscriber channel and rejects later publishes. It is
// idempotent and safe to call concurrently with Publish and Subscribe.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for sub := range b.subs {
		delete(b.subs, sub)
		close(sub.ch)
	}
}

// bufferSize returns the per-subscriber capacity, defaulting for a zero-value
// Broker.
func (b *Broker) bufferSize() int {
	if b.buffer > 0 {
		return b.buffer
	}
	return DefaultBufferSize
}
