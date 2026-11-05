// Package state is ZeoKeel's UI state layer: a runtime event bus plus an
// immutable snapshot projector that every UI screen reads.
//
// The runtime never mutates UI state directly. It publishes values from the
// closed Event set (Token, Reasoning, ToolStart, ToolEnd, ApprovalRequest,
// Usage, Status, Error) to a Broker. A Projector is the broker's single
// consumer: it folds events, in publish order, into a Snapshot and swaps the
// current snapshot atomically (atomic.Pointer[Snapshot]). The view reads the
// snapshot once per frame via Projector.Snapshot and only reads it.
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
// Subscribe the projector before the first Publish (NewProjector does that)
// and keep the OnPublish callback cheap so the projector keeps up; drops are
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
