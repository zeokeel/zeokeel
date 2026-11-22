// Package goal provides the ZeoKeel goal backend module. The durable state
// machine lives in rig/session/goal; this module wires it to the product store
// and contracts.
package goal

import (
	"context"
	"fmt"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	riggoal "github.com/zeokeel/zeokeel-rig/session/goal"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ModuleID is the stable module identity used by assembly documents.
const ModuleID = "zeokeel/goal"

// goalEventType and goalSnapshot are the SDK's durable event shape (tests decode
// it directly).
const goalEventType = riggoal.EventType

type goalSnapshot = riggoal.Snapshot

// Config is the module's typed assembly config.
type Config struct {
	// DefaultMaxGoalRounds overrides the automatic continuation budget applied
	// when a create request passes 0.
	DefaultMaxGoalRounds int `json:"defaultMaxGoalRounds"`
}

// eventStore is the narrow durable session-log view (contracts.Store satisfies
// it; tests use a tiny fake).
type eventStore interface {
	Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error)
	AppendEvent(ctx context.Context, event rig.SessionEvent) error
}

// sessionLookup is the optional session-existence check (contracts.Store
// satisfies it; a nil lookup skips the deleted-session guard).
type sessionLookup interface {
	GetSessionMeta(ctx context.Context, sessionID string) (contracts.SessionMeta, bool, error)
}

// service adapts the SDK goal service to the product contract and keeps the
// effective default budget visible to tests/module checks.
type service struct {
	*riggoal.Service
	defaultMaxRounds int
}

var _ contracts.GoalService = (*service)(nil)

// newService builds the adapter; sessions may be nil.
func newService(store eventStore, sessions sessionLookup, logger contracts.Logger, defaultMaxRounds int) *service {
	if defaultMaxRounds <= 0 {
		defaultMaxRounds = contracts.DefaultMaxGoalRounds
	}
	var exists riggoal.SessionExistsFunc
	if sessions != nil {
		exists = func(ctx context.Context, sessionID string) (bool, error) {
			_, found, err := sessions.GetSessionMeta(ctx, sessionID)
			return found, err
		}
	}
	return &service{
		Service:          riggoal.New(store, exists, logger, defaultMaxRounds),
		defaultMaxRounds: defaultMaxRounds,
	}
}

// discardLogger drops goal log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// Module is the goal backend descriptor. It provides contracts.KeyGoal and
// requires the durable session store (contracts.KeyStore).
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		store, err := ctx.Require(contracts.KeyStore)
		if err != nil {
			return fmt.Errorf("%s: require session store: %w", ModuleID, err)
		}
		var logger contracts.Logger = discardLogger{}
		if service, ok := ctx.Get(contracts.KeyLogger); ok && service != nil {
			logger = service
		}
		goalService := newService(store, store, logger, cfg.DefaultMaxGoalRounds)
		if _, err := ctx.Provide(contracts.KeyGoal, contracts.GoalService(goalService)); err != nil {
			return fmt.Errorf("%s: provide goal service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyGoal)).
		AddRequires(module.Requires(contracts.KeyStore)).
		WithName("ZeoKeel goal backend")
}
