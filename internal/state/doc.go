// Package state is ZeoKeel's UI state layer: the runtime event set, a bounded
// event bus (Broker) and the pure fold (Apply) that turns events into immutable
// Snapshots.
//
// The runtime never mutates UI state directly. It publishes values from the
// closed Event set (Token, Reasoning, ToolStart, ToolEnd, ApprovalRequest,
// Usage, Status, Error) to a Broker. Production UI state is folded by
// session.Controller into one Snapshot per session with Apply; the view reads
// that snapshot and only reads it.
//
// Projector is retained as a deprecated single-session consumer of a Broker's
// subscription (AR-05): it folds events on its own goroutine into one Snapshot
// and swaps it atomically. It cannot represent a user message or a session
// switch, so production no longer consumes it; keep it only for experiments or
// tests, and prefer Apply for any new fold.
//
// Folding is deterministic. Applying the same events in the same order
// always produces the same snapshot, and Apply never mutates its input: every
// changed slice or map is copied. That is what makes events.jsonl replayable
// and lets the UI goroutine read a snapshot while later events are folded.
// Treat *Snapshot as read-only; the package keeps old snapshots valid for as
// long as they are referenced (no pooling, no mutation in place).
//
// Snapshot.Seq counts applied events. It is not a publish counter: an event
// dropped by a slow subscriber is never applied and does not advance Seq.
// A legacy Projector must be subscribed before the first Publish (NewProjector
// does that); keep its OnPublish callback cheap so it keeps up. Drops are
// observable via Broker.Dropped.
//
// Concurrency contract:
//
//	Broker.Publish      safe from any number of goroutines
//	Broker.Subscribe    safe at any time (channels returned after Close are closed)
//	Broker.Close        idempotent; publishes after Close are ignored
//	Broker.Dropped      safe at any time
//	Projector.Snapshot  safe from any goroutine; returns a read-only value
//	Projector.OnPublish safe at any time; takes effect on the next batch
//	Projector.Close     must not be called from inside an OnPublish callback
//
// Phase is owned by Status events; the projector does not invent phase
// transitions from other events. Likewise tool status is the state implied by
// the most recent tool event for that CallID, not a validated state machine:
// runtime out-of-order bugs stay visible instead of being silently masked.
package state
