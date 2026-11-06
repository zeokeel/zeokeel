// Package session connects ZeoKeel's persistence, event and runtime layers for
// one conversation turn.
//
// A Controller owns the live view of the session most recently opened or
// submitted to. It keeps an immutable state.Snapshot per session and updates
// it as events arrive; the UI reads Snapshot once per frame and re-renders
// after OnUpdate fires.
//
// The lifecycle of one Submit is:
//
//	persist user message + "message" event
//	seed snapshot PhaseRunning, persist sessions.status=running
//	Agent.Run -> for each state.Event:
//	    persist event -> fold into snapshot -> Broker.Publish -> OnUpdate
//	terminal Status -> persist assistant messages + sessions.status
//	turn unregistered
//
// Submit is asynchronous; it returns after the prompt is accepted. Completion
// is signalled by PhaseIdle (or PhaseFailed) through OnUpdate/Snapshot, and by
// Wait for headless callers. Stop cancels the current turn; it is idempotent
// and returns without waiting for the runtime goroutine.
//
// The first successfully completed turn also gives the session its automatic
// title. A configured Config.TitleGenerator asks the model (normally through
// the same provider as the turn); when it is missing, fails, times out or
// returns no usable title, the first user message is collapsed to one line and
// truncated instead. The title is written off the turn goroutine and published
// with a repaint OnUpdate, so the header and sidebar update live. If the first
// turn fails or is stopped, the session keeps its one title attempt for the
// next successfully completed turn, which still titles from the original user
// message. A manual RenameSession always wins, and a session is auto-titled at
// most once; both rules are persisted in per-session settings, so they survive
// a restart. Close cancels an in-flight title request and waits for it to
// unwind.
//
// Titles that predate this feature have no manual-title marker. A title
// changed outside RenameSession is detected and treated as manual, and a
// session loaded from an older store is only auto-titled while it still has a
// placeholder name ("New Session", "Untitled"). The same placeholder rule
// applies to a creation title: a placeholder passed to CreateSession may be
// replaced on the first completed turn, while a meaningful title is
// deliberately preserved, including automation's task title. Headless callers
// no longer need RenameSession merely to keep a creation title. A model-written
// title is capped at AutoTitleMaxChars (80 runes) and the fallback derived from
// the first user message at AutoTitleFallbackChars (60 runes); both are
// truncated with an ellipsis.
//
// The store's messages table is the source of truth for transcript text and
// the events table rebuilds tool cards, usage and errors. Reopening a session
// (or constructing a new Controller over the same store) therefore restores
// the full history without replaying model calls.
//
// Controllers are safe for concurrent use. A Submit for a different session is
// accepted while another turn is in flight, but the runtime serializes the
// shared agent's turns, and a session rejects a second concurrent Submit with
// an error wrapping ErrTurnRunning. Snapshot and OnUpdate follow the current
// session, so switching sessions mid-turn keeps background turns running while
// the visible snapshot follows the user's selection.
//
// Snapshot is the UI's source of truth. Controller maintains the per-session
// snapshots with state.Apply, the same fold the state layer exposes; a
// broker-wide state.Projector is session-unaware (it cannot represent a user
// message or a session switch, since the event set has no reset event) and is
// therefore no longer a second production consumer (AR-05). New closes a
// projector supplied by older wiring so the duplicate fold stops.
package session
