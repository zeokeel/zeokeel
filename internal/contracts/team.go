package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// TeamMember is one in-process teammate (a spawned subagent) as shown by the
// Agents inspector and the team tools.
type TeamMember struct {
	// ID is the manager-generated reference ("member-1").
	ID string
	// Name is the display name passed on spawn.
	Name string
	// Status is "active" or "interrupted".
	Status string
	// Created is when the member was spawned.
	Created time.Time
	// Last is the latest finished answer ("" while none finished).
	Last string
}

// TeamService is the product-side view/control surface over the rig team
// manager: the inspector lists members, steers them with follow-up messages and
// interrupts them; the agent module registers the model-facing team tools.
type TeamService interface {
	// Members lists the live teammates, oldest first.
	Members() []TeamMember
	// Spawn starts a named teammate with an initial prompt.
	Spawn(ctx context.Context, name, prompt, model string) (TeamMember, error)
	// SendAsync queues a follow-up message without waiting for its answer.
	SendAsync(id, message string) error
	// Send delivers a follow-up message and returns the teammate's answer.
	Send(ctx context.Context, id, message string) (string, error)
	// Wait blocks until the teammate finishes its current run.
	Wait(ctx context.Context, id string) (string, error)
	// Interrupt cancels the teammate's current run.
	Interrupt(id string) error
	// Close stops and removes the teammate.
	Close(id string) error
}

// KeyTeam is provided by the agent module (rig team.NewManager).
var KeyTeam = cordis.NewServiceKey[TeamService]("zeokeel/team")
