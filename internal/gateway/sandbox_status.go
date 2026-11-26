package gateway

// The OS-sandbox status read model ("sandbox.status").
//
// Why it is a real method and not a log line: since the 2026-11-26
// personal-helper permission model, shell commands never park on an approval
// card, so the OS runner is the only thing that confines their file effects. On
// a host without one the model runs commands with the process's own
// permissions — a fact the user has to be able to see, because it is the
// difference between "the fence is real" and "the model was given a rule it can
// ignore" (docs/APPROVAL-SURFACES.md §S4.1 layer 4).
//
// The gateway does not invent the answer: contracts.SandboxStatus comes from the
// sandbox module, which derives Confinement from what the runner reports. An
// assembly without the sandbox module has no OS fence to report at all — but it
// also has no command exemption (commands go through the approval flow), so the
// row says exactly that instead of wearing the "unconfined" label.

import (
	"context"
	"encoding/json"
	"runtime"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sandboxStatusResult is the wire shape of sandbox.status (TRANSPORT §5.1.5).
//
// `confinement` is the field clients switch on: contracts.SandboxConfinementOS
// or contracts.SandboxConfinementNone. An empty value means "no sandbox module
// is assembled" and must never be rendered as confined — `commandsUnconfined`
// stays false in that case because commands are approval-gated there.
type sandboxStatusResult struct {
	Available          bool   `json:"available"`
	Platform           string `json:"platform"`
	Reason             string `json:"reason,omitempty"`
	Confinement        string `json:"confinement"`
	CommandsUnconfined bool   `json:"commandsUnconfined"`
	Note               string `json:"note,omitempty"`
}

// sandboxStatus answers sandbox.status.
func (g *Gateway) sandboxStatus(_ context.Context, _ json.RawMessage) (any, *protocolError) {
	service, ok := g.root.Get(contracts.KeySandbox)
	if !ok || service == nil {
		return sandboxStatusResult{
			Platform: runtime.GOOS,
			Note: "No OS sandbox module is assembled in this build, so there is no OS fence to report; " +
				"shell commands stay behind the approval gate instead of being exempt.",
		}, nil
	}
	status := service.Status()
	return sandboxStatusResult{
		Available:          status.Available,
		Platform:           status.Platform,
		Reason:             status.Reason,
		Confinement:        status.Confinement,
		CommandsUnconfined: status.CommandsUnconfined,
		Note:               status.Note,
	}, nil
}
