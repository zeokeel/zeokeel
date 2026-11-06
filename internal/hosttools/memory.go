package hosttools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// In-memory capability implementations. They are concurrency-safe and keep
// their state in the value, never in package variables, so tests and multiple
// products in one process stay isolated.

// AskedQuestion is one question a MemoryAsker recorded.
type AskedQuestion struct {
	Question string
	Options  []string
	Answer   string
}

// MemoryAsker answers from a queue of pre-seeded answers and records every
// question it was asked. With an empty queue it reports ErrNoInteractive: an
// in-memory host has no user to answer in real time.
type MemoryAsker struct {
	mu      sync.Mutex
	answers []string
	asked   []AskedQuestion
}

// NewMemoryAsker returns an asker that answers with answers, in order.
func NewMemoryAsker(answers ...string) *MemoryAsker {
	return &MemoryAsker{answers: append([]string(nil), answers...)}
}

// Enqueue appends answers to the queue.
func (a *MemoryAsker) Enqueue(answers ...string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.answers = append(a.answers, answers...)
	a.mu.Unlock()
}

// Ask implements Asker.
func (a *MemoryAsker) Ask(ctx context.Context, question string, options []string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if a == nil {
		return "", ErrNoInteractive
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("question is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record := AskedQuestion{Question: question, Options: append([]string(nil), options...)}
	if len(a.answers) == 0 {
		a.asked = append(a.asked, record)
		return "", ErrNoInteractive
	}
	answer := a.answers[0]
	a.answers = a.answers[1:]
	record.Answer = answer
	a.asked = append(a.asked, record)
	return answer, nil
}

// Asked returns a snapshot of every question asked, in order.
func (a *MemoryAsker) Asked() []AskedQuestion {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AskedQuestion(nil), a.asked...)
}

// PresentedArtifact is one artifact a MemoryPresenter recorded.
type PresentedArtifact struct {
	ID    string
	Title string
	Body  string
}

// MemoryPresenter records presented artifacts and returns sequential ids.
type MemoryPresenter struct {
	mu        sync.Mutex
	next      int
	artifacts []PresentedArtifact
}

// NewMemoryPresenter returns an empty presenter.
func NewMemoryPresenter() *MemoryPresenter { return &MemoryPresenter{} }

// Present implements Presenter.
func (p *MemoryPresenter) Present(ctx context.Context, title, body string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if p == nil {
		return "", errors.New("no presenter is connected")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("title is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	id := fmt.Sprintf("artifact-%d", p.next)
	p.artifacts = append(p.artifacts, PresentedArtifact{ID: id, Title: title, Body: body})
	return id, nil
}

// Artifacts returns a snapshot of the presented artifacts, in order.
func (p *MemoryPresenter) Artifacts() []PresentedArtifact {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PresentedArtifact(nil), p.artifacts...)
}

// MemoryPlan is an in-memory PlanController implementing the documented state
// machine: idle -> reviewing -> approved | cancelled.
type MemoryPlan struct {
	mu      sync.Mutex
	state   string
	content string
	review  int
}

// NewMemoryPlan returns a plan controller in the idle state.
func NewMemoryPlan() *MemoryPlan { return &MemoryPlan{state: PlanIdle} }

// State implements PlanController.
func (p *MemoryPlan) State() string {
	if p == nil {
		return PlanIdle
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Review implements PlanController. It puts the plan under review and returns
// the reviewing verdict.
func (p *MemoryPlan) Review(content string) (string, error) {
	if p == nil {
		return "", errors.New("no plan controller is connected")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("plan content is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.content = content
	p.state = PlanReviewing
	p.review++
	return PlanReviewing, nil
}

// Content returns the last plan submitted for review.
func (p *MemoryPlan) Content() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.content
}

// Exit implements PlanController: approve leaves review approved, cancel
// cancels the review, status reports the current state.
func (p *MemoryPlan) Exit(action string) (string, error) {
	if p == nil {
		return "", errors.New("no plan controller is connected")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	p.mu.Lock()
	defer p.mu.Unlock()
	switch action {
	case "status":
		return p.state, nil
	case "approve":
		if p.state != PlanReviewing {
			return p.state, fmt.Errorf("no plan is under review (state: %s)", p.state)
		}
		p.state = PlanApproved
		return p.state, nil
	case "cancel":
		if p.state != PlanReviewing {
			return p.state, fmt.Errorf("no plan is under review (state: %s)", p.state)
		}
		p.state = PlanCancelled
		return p.state, nil
	default:
		return p.state, fmt.Errorf("unknown plan action %q (want approve, cancel or status)", action)
	}
}

// MemoryGoals is an in-memory GoalStore holding one goal.
type MemoryGoals struct {
	mu      sync.Mutex
	next    int
	current *goalRecord
}

type goalRecord struct {
	id     string
	text   string
	status string
}

// NewMemoryGoals returns an empty goal store.
func NewMemoryGoals() *MemoryGoals { return &MemoryGoals{} }

// Set implements GoalStore: it creates the goal or replaces its text.
func (g *MemoryGoals) Set(text string) (string, error) {
	if g == nil {
		return "", errors.New("no goal store is connected")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("goal text is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.current == nil {
		g.next++
		g.current = &goalRecord{id: fmt.Sprintf("goal-%d", g.next), status: GoalActive}
	}
	g.current.text = text
	return g.current.id, nil
}

// Get implements GoalStore.
func (g *MemoryGoals) Get() (string, string, string, bool) {
	if g == nil {
		return "", "", "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.current == nil {
		return "", "", "", false
	}
	return g.current.id, g.current.text, g.current.status, true
}

// Clear implements GoalStore.
func (g *MemoryGoals) Clear() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	existed := g.current != nil
	g.current = nil
	return existed
}

// MemoryTeam is an in-memory TeamManager: it records members but starts no
// subagents.
type MemoryTeam struct {
	mu      sync.Mutex
	next    int
	members []TeamMember
}

// NewMemoryTeam returns an empty team.
func NewMemoryTeam() *MemoryTeam { return &MemoryTeam{} }

// Spawn implements TeamManager.
func (t *MemoryTeam) Spawn(name string) (string, error) {
	if t == nil {
		return "", errors.New("no team manager is connected")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("team member name is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	id := fmt.Sprintf("member-%d", t.next)
	t.members = append(t.members, TeamMember{ID: id, Name: name, Status: TeamActive})
	return id, nil
}

// List implements TeamManager.
func (t *MemoryTeam) List() []TeamMember {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TeamMember(nil), t.members...)
}

// Close implements TeamManager.
func (t *MemoryTeam) Close(id string) error {
	return t.setStatus(id, TeamClosed)
}

// Interrupt implements TeamManager.
func (t *MemoryTeam) Interrupt(id string) error {
	return t.setStatus(id, TeamInterrupted)
}

func (t *MemoryTeam) setStatus(id, status string) error {
	if t == nil {
		return errors.New("no team manager is connected")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("team member id is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for index := range t.members {
		if t.members[index].ID == id {
			t.members[index].Status = status
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownMember, id)
}

// contextError returns the context's error, tolerating a nil context.
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
