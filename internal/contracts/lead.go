package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// LeadReport is what one triage pass did. It is deliberately countable: the UI
// shows "这轮处理了 N 条"，and a failure names its item instead of aborting.
type LeadReport struct {
	// Scanned is how many new items the pass looked at.
	Scanned int
	// Dispatched / Ignored / Skipped / Failed count the outcomes.
	Dispatched int
	Ignored    int
	Skipped    int
	Failed     int
	// Errors names the items that failed (one line each).
	Errors []string
}

// LeadService is the triage pass: it asks the planner about every new inbox
// item and applies the decisions through the triage service. It never sends
// anything to a channel by itself.
type LeadService interface {
	// RunOnce performs one bounded pass.
	RunOnce(ctx context.Context) (LeadReport, error)
}

// KeyLead is provided by the lead module.
var KeyLead = cordis.NewServiceKey[LeadService]("zeokeel/lead")
