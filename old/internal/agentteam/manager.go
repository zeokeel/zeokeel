// Package agentteam runs subagents asynchronously and exposes the small
// control surface the team tools need: list members, deliver a message to a
// member, and interrupt a member's current run.
//
// Framework decision: eino's adk/backgroundtask package was evaluated first.
// It provides durable task submission, cancellation intent, lifecycle status
// and a model-facing managed tool, but it has no inbox/steer operation (a
// working child cannot be handed a follow-up message), no name-addressed live
// member registry, and its subagent executor requires durable ADK session and
// checkpoint stores that ZeoKeel does not use. Reusing it would add those
// dependencies without providing send_message semantics. This package is
// therefore a small pure-Go manager over runtime.SubagentRunner, the same seam
// the synchronous subagent tool already uses.
//
// A member's message queue is processed one turn at a time. A message sent to
// a running member waits for the current RunSubagent call to return and then
// starts the next turn; a message sent to an idle member starts a turn
// immediately. Interrupt cancels the current turn's context, leaves already
// queued messages parked, and marks the member interrupted until a later Send
// resumes it. Every runner call runs on a manager-owned context so a member can
// outlive the tool invocation that spawned it.
package agentteam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// Status is a member's current lifecycle state.
type Status string

// Member states. A member is running while a queued or initial task is being
// executed; completed, failed and interrupted are terminal until the next
// Send starts another turn.
const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusInterrupted Status = "interrupted"
)

// Sentinel errors returned by Manager. Callers classify failures with
// errors.Is.
var (
	// ErrNoRunner reports a manager without a runtime.SubagentRunner.
	ErrNoRunner = errors.New("agentteam: no subagent runner configured")
	// ErrUnknownTarget reports a target id or name with no member.
	ErrUnknownTarget = errors.New("agentteam: unknown target")
	// ErrAmbiguousTarget reports a name shared by more than one member.
	ErrAmbiguousTarget = errors.New("agentteam: ambiguous target")
	// ErrEmptyTask reports Spawn with a blank task.
	ErrEmptyTask = errors.New("agentteam: empty task")
	// ErrEmptyMessage reports Send with a blank message.
	ErrEmptyMessage = errors.New("agentteam: empty message")
	// ErrClosed reports use of a Manager after Close.
	ErrClosed = errors.New("agentteam: manager closed")
	// ErrTooManyAgents reports Spawn at the configured member limit.
	ErrTooManyAgents = errors.New("agentteam: member limit reached")
	// ErrEmptyBatch reports SpawnBatch with no requests.
	ErrEmptyBatch = errors.New("agentteam: empty spawn batch")
)

// Config configures a Manager. Runner may be bound later with SetRunner, which
// lets runtime.New wire the parent Agent after the tools were registered.
type Config struct {
	// Runner executes one child-agent turn. It is required before Spawn.
	Runner runtime.SubagentRunner
	// BaseContext scopes every member. Close cancels it. Nil uses
	// context.Background().
	BaseContext context.Context
	// MaxAgents caps live members. Non-positive means no cap.
	MaxAgents int
	// Now is the clock. Nil uses time.Now. Tests inject a fake clock.
	Now func() time.Time
}

// SpawnRequest describes one asynchronous child. Task is the initial prompt,
// Agent names the child ("subagent" when empty) and Depth is the nesting depth
// passed to the runner.
type SpawnRequest struct {
	Task  string
	Agent string
	Depth int
	// SessionID is the session that requested the member. The manager uses it
	// to stop a session's asynchronous members when its turn is stopped; it is
	// empty for direct manager users that do not scope members to a session.
	SessionID string
}

// Delivery is the result of Send. It reports acceptance, not the target's
// reply: the reply belongs to the target's own run and is visible through
// Members.
type Delivery struct {
	MessageID string `json:"message_id"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Status    string `json:"status"`
	Pending   int    `json:"pending"`
}

// Member is an immutable snapshot of one team member.
type Member struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	SessionID   string    `json:"session_id,omitempty"`
	Status      Status    `json:"status"`
	Task        string    `json:"task,omitempty"`
	CurrentTask string    `json:"current_task,omitempty"`
	Output      string    `json:"output,omitempty"`
	Error       string    `json:"error,omitempty"`
	Depth       int       `json:"depth,omitempty"`
	Turns       int       `json:"turns"`
	QueueLen    int       `json:"queue_len"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// pendingMessage is one queued turn.
type pendingMessage struct {
	id   string
	text string
}

// member is the mutable record guarded by Manager.mu.
type member struct {
	id        string
	name      string
	sessionID string
	task      string
	// spawnCtx is the context the member was spawned from (the spawning turn's
	// tool context). The run context inherits its approval channel through
	// runtime.WithTurnApprovalFrom, so a gated tool can still ask the user via
	// the parent session; cancellation intentionally comes from the manager, so
	// the member's lifetime does not end with the spawning turn.
	spawnCtx    context.Context
	status      Status
	currentTask string
	output      string
	errText     string
	depth       int
	turns       int
	queue       []pendingMessage
	running     bool
	interrupted bool
	cancel      context.CancelFunc
	turnDone    chan struct{}
	createdAt   time.Time
	updatedAt   time.Time
}

// Manager owns the members and their goroutines. It is safe for concurrent
// use.
type Manager struct {
	mu      sync.Mutex
	runner  runtime.SubagentRunner
	members map[string]*member
	order   []string
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  bool
	max     int
	now     func() time.Time
	nextID  int
	nextMsg int
}

// New returns an empty manager. A nil BaseContext uses context.Background().
func New(cfg Config) *Manager {
	base := cfg.BaseContext
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		runner:  cfg.Runner,
		members: make(map[string]*member),
		ctx:     ctx,
		cancel:  cancel,
		max:     cfg.MaxAgents,
		now:     now,
	}
}

// SetRunner binds (or replaces) the child runner. Existing turns finish with
// the runner they started with; later turns use the new one.
func (m *Manager) SetRunner(runner runtime.SubagentRunner) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.runner = runner
	m.mu.Unlock()
}

// Spawn starts one member and returns its snapshot. ctx is checked before the
// goroutine starts but does not own the member's lifetime: an asynchronous
// child outlives the tool call that created it.
func (m *Manager) Spawn(ctx context.Context, req SpawnRequest) (Member, error) {
	members, err := m.SpawnBatch(ctx, []SpawnRequest{req})
	if err != nil {
		return Member{}, err
	}
	return members[0], nil
}

// SpawnBatch starts one member per request and returns their snapshots in
// request order. It validates every request and the manager's member limit
// before starting any goroutine, so a rejected batch never leaves partial
// members behind.
func (m *Manager) SpawnBatch(ctx context.Context, reqs []SpawnRequest) ([]Member, error) {
	if len(reqs) == 0 {
		return nil, ErrEmptyBatch
	}

	type prepared struct {
		task      string
		name      string
		depth     int
		sessionID string
	}
	batch := make([]prepared, len(reqs))
	for i, req := range reqs {
		task := strings.TrimSpace(req.Task)
		if task == "" {
			return nil, ErrEmptyTask
		}
		name := strings.TrimSpace(req.Agent)
		if name == "" {
			name = "subagent"
		}
		batch[i] = prepared{task: task, name: name, depth: req.Depth, sessionID: strings.TrimSpace(req.SessionID)}
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if m.runner == nil {
		return nil, ErrNoRunner
	}
	if m.max > 0 && len(m.members)+len(batch) > m.max {
		return nil, ErrTooManyAgents
	}

	out := make([]Member, 0, len(batch))
	for _, req := range batch {
		mem := m.spawnLocked(ctx, req.task, req.name, req.sessionID, req.depth)
		out = append(out, mem.snapshot())
	}
	return out, nil
}

// spawnLocked creates and starts one member. m.mu must be held.
func (m *Manager) spawnLocked(spawnCtx context.Context, task, name, sessionID string, depth int) *member {
	m.nextID++
	id := fmt.Sprintf("agent-%d", m.nextID)
	now := m.now().UTC()
	mem := &member{
		id:        id,
		name:      name,
		sessionID: sessionID,
		task:      task,
		spawnCtx:  spawnCtx,
		status:    StatusQueued,
		depth:     depth,
		createdAt: now,
		updatedAt: now,
	}
	m.members[id] = mem
	m.order = append(m.order, id)

	m.nextMsg++
	mem.queue = append(mem.queue, pendingMessage{
		id:   fmt.Sprintf("%s-msg-%d", id, m.nextMsg),
		text: task,
	})
	m.startLocked(mem)
	return mem
}

// Members returns every member in creation order.
func (m *Manager) Members() []Member {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Member, 0, len(m.order))
	for _, id := range m.order {
		if mem := m.members[id]; mem != nil {
			out = append(out, mem.snapshot())
		}
	}
	return out
}

// Snapshot returns every member in creation order. It is an alias of Members
// kept for callers that treat a roster read as a point-in-time snapshot.
func (m *Manager) Snapshot() []Member {
	return m.Members()
}

// Member returns one member snapshot resolved by exact id first and then by a
// unique name. An unknown or ambiguous target returns a wrapped
// ErrUnknownTarget or ErrAmbiguousTarget.
func (m *Manager) Member(target string) (Member, error) {
	if m == nil {
		return Member{}, fmt.Errorf("%w: empty target", ErrUnknownTarget)
	}
	target = strings.TrimSpace(target)
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, err := m.findLocked(target)
	if err != nil {
		return Member{}, err
	}
	return mem.snapshot(), nil
}

// CloseMember stops one member: its queued messages are dropped and its
// running turn is canceled. The call waits for the member to settle. Closing an
// idle member is an accepted no-op. The manager stays open for later Spawn or
// Send calls.
func (m *Manager) CloseMember(target string) error {
	if m == nil {
		return ErrClosed
	}
	target = strings.TrimSpace(target)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	mem, err := m.findLocked(target)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	mem.queue = nil
	var done chan struct{}
	if mem.running {
		mem.interrupted = true
		mem.updatedAt = m.now().UTC()
		if mem.cancel != nil {
			mem.cancel()
		}
		done = mem.turnDone
	}
	m.mu.Unlock()

	if done != nil {
		<-done
	}
	return nil
}

// CloseMembers stops every member, dropping queued messages and canceling
// running turns. It waits for the stopped members to settle and returns how
// many running turns it canceled. It is idempotent and leaves the manager open.
func (m *Manager) CloseMembers() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0
	}
	var pending []chan struct{}
	stopped := 0
	for _, id := range m.order {
		mem := m.members[id]
		if mem == nil {
			continue
		}
		mem.queue = nil
		if !mem.running {
			continue
		}
		mem.interrupted = true
		mem.updatedAt = m.now().UTC()
		if mem.cancel != nil {
			mem.cancel()
		}
		if mem.turnDone != nil {
			pending = append(pending, mem.turnDone)
		}
		stopped++
	}
	m.mu.Unlock()

	for _, done := range pending {
		<-done
	}
	return stopped
}

// StopSession stops the members a session requested: it drops their queued
// messages, cancels their running turns and waits for them to settle. The
// manager stays open for later Spawn calls and members of other sessions are
// untouched. It returns how many running turns it canceled, and is safe to call
// for a session that has no members (an accepted no-op).
func (m *Manager) StopSession(sessionID string) int {
	if m == nil {
		return 0
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0
	}
	var pending []chan struct{}
	stopped := 0
	for _, id := range m.order {
		mem := m.members[id]
		if mem == nil || mem.sessionID != sessionID {
			continue
		}
		mem.queue = nil
		if !mem.running {
			continue
		}
		mem.interrupted = true
		mem.updatedAt = m.now().UTC()
		if mem.cancel != nil {
			mem.cancel()
		}
		if mem.turnDone != nil {
			pending = append(pending, mem.turnDone)
		}
		stopped++
	}
	m.mu.Unlock()

	for _, done := range pending {
		<-done
	}
	return stopped
}

// Send queues message for the target and starts a turn when the target is idle.
// It returns acceptance only; the target's output is not collected here.
func (m *Manager) Send(target, message string) (Delivery, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return Delivery{}, ErrEmptyMessage
	}
	target = strings.TrimSpace(target)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Delivery{}, ErrClosed
	}
	mem, err := m.findLocked(target)
	if err != nil {
		return Delivery{}, err
	}
	m.nextMsg++
	msg := pendingMessage{
		id:   fmt.Sprintf("%s-msg-%d", mem.id, m.nextMsg),
		text: message,
	}
	mem.queue = append(mem.queue, msg)
	mem.updatedAt = m.now().UTC()
	if !mem.running {
		m.startLocked(mem)
	}
	return Delivery{
		MessageID: msg.id,
		AgentID:   mem.id,
		AgentName: mem.name,
		Status:    "delivered",
		Pending:   len(mem.queue),
	}, nil
}

// Interrupt asks the target's current turn to stop. The call returns as soon
// as cancellation is requested; use Wait to observe the settled member. It is
// an accepted no-op for an idle member. Queued messages stay parked until a
// later Send resumes the member.
func (m *Manager) Interrupt(target string) (Member, error) {
	target = strings.TrimSpace(target)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Member{}, ErrClosed
	}
	mem, err := m.findLocked(target)
	if err != nil {
		m.mu.Unlock()
		return Member{}, err
	}
	cancel := mem.cancel
	if mem.running {
		mem.interrupted = true
		mem.updatedAt = m.now().UTC()
	}
	snap := mem.snapshot()
	m.mu.Unlock()

	if snap.Status == StatusRunning || cancel != nil {
		if cancel != nil {
			cancel()
		}
	}
	return snap, nil
}

// Wait blocks until the target has no running turn or ctx is done. An idle,
// completed, failed or interrupted member returns immediately.
func (m *Manager) Wait(ctx context.Context, target string) (Member, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target = strings.TrimSpace(target)
	for {
		m.mu.Lock()
		mem, err := m.findLocked(target)
		if err != nil {
			m.mu.Unlock()
			return Member{}, err
		}
		if !mem.running {
			snap := mem.snapshot()
			m.mu.Unlock()
			return snap, nil
		}
		done := mem.turnDone
		m.mu.Unlock()
		if done == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return Member{}, ctx.Err()
		case <-done:
		}
	}
}

// Close cancels every member and waits for their goroutines to exit. It is
// idempotent.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
}

// startLocked starts the member's worker. m.mu must be held.
func (m *Manager) startLocked(mem *member) {
	if mem.running {
		return
	}
	mem.running = true
	mem.status = StatusRunning
	mem.turnDone = make(chan struct{})
	done := mem.turnDone
	m.wg.Add(1)
	go m.loop(mem, done)
}

// loop drains the member's queue one runner call at a time and exits when the
// queue is empty, the member is interrupted, or the manager closes.
func (m *Manager) loop(mem *member, done chan struct{}) {
	defer m.wg.Done()
	defer close(done)

	for {
		m.mu.Lock()
		switch {
		case m.closed:
			m.settleLocked(mem, StatusInterrupted, "manager closed")
			m.mu.Unlock()
			return
		case mem.interrupted:
			mem.interrupted = false
			m.settleLocked(mem, StatusInterrupted, "")
			m.mu.Unlock()
			return
		case len(mem.queue) == 0:
			mem.running = false
			mem.cancel = nil
			mem.currentTask = ""
			mem.updatedAt = m.now().UTC()
			m.mu.Unlock()
			return
		}

		msg := mem.queue[0]
		mem.queue = mem.queue[1:]
		mem.currentTask = msg.text
		mem.status = StatusRunning
		mem.updatedAt = m.now().UTC()
		ctx, cancel := context.WithCancel(m.ctx)
		mem.cancel = cancel
		runner := m.runner
		name := mem.name
		depth := mem.depth
		spawnCtx := mem.spawnCtx
		m.mu.Unlock()
		// Keep the spawning turn's approval channel (its event sink, so the
		// user can approve a gated tool through the parent session) without
		// inheriting that turn's cancellation: the member keeps running on the
		// manager-owned context and only dies on interrupt/close.
		runCtx := runtime.WithTurnApprovalFrom(spawnCtx, ctx)

		if runner == nil {
			cancel()
			m.mu.Lock()
			m.settleLocked(mem, StatusFailed, ErrNoRunner.Error())
			m.mu.Unlock()
			return
		}

		result, err := runner.RunSubagent(runCtx, runtime.SubagentRequest{
			Task:  msg.text,
			Agent: name,
			Depth: depth,
		})
		canceled := ctx.Err() != nil
		cancel()

		m.mu.Lock()
		mem.turns++
		mem.updatedAt = m.now().UTC()
		if canceled || mem.interrupted {
			mem.interrupted = false
			m.settleLocked(mem, StatusInterrupted, "")
			m.mu.Unlock()
			return
		}
		if err != nil {
			m.settleLocked(mem, StatusFailed, err.Error())
			m.mu.Unlock()
			return
		}
		mem.output = result.Output
		mem.errText = ""
		mem.status = StatusCompleted
		mem.currentTask = ""
		m.mu.Unlock()
	}
}

// settleLocked marks a member idle with status and error text.
func (m *Manager) settleLocked(mem *member, status Status, errText string) {
	mem.status = status
	mem.errText = errText
	mem.currentTask = ""
	mem.running = false
	mem.cancel = nil
	mem.updatedAt = m.now().UTC()
}

// findLocked resolves a target by exact id first, then by exact unique name.
// m.mu must be held.
func (m *Manager) findLocked(target string) (*member, error) {
	if target == "" {
		return nil, fmt.Errorf("%w: empty target", ErrUnknownTarget)
	}
	if mem, ok := m.members[target]; ok {
		return mem, nil
	}
	var found *member
	for _, id := range m.order {
		mem := m.members[id]
		if mem == nil || mem.name != target {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: %q names more than one member; use the id", ErrAmbiguousTarget, target)
		}
		found = mem
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTarget, target)
	}
	return found, nil
}

// snapshot copies a member without acquiring the manager lock.
func (mem *member) snapshot() Member {
	return Member{
		ID:          mem.id,
		Name:        mem.name,
		SessionID:   mem.sessionID,
		Status:      mem.status,
		Task:        mem.task,
		CurrentTask: mem.currentTask,
		Output:      mem.output,
		Error:       mem.errText,
		Depth:       mem.depth,
		Turns:       mem.turns,
		QueueLen:    len(mem.queue),
		CreatedAt:   mem.createdAt,
		UpdatedAt:   mem.updatedAt,
	}
}
