package runtime

import "strings"

// AdvisorySink is the model-visible delivery point for a notice produced
// outside the current turn (for example a background job finishing). The text
// is injected as a harness advisory: into the running turn's next model request
// when one is active, otherwise onto the next turn's prompt.
type AdvisorySink func(text string)

// AdvisorySinkBinder is implemented by tool instances that own an external
// event source. runtime.New hands the Agent's sink to every registered tool
// that implements it, so the source can notify the model without the tools
// package knowing about the runtime internals.
type AdvisorySinkBinder interface {
	SetAdvisorySink(sink AdvisorySink)
}

// bindAdvisorySinks wires the Agent's external-notice sink into the registered
// tools that support it. Tools are shared across rebuilds, so binding once in
// New is enough.
func (a *Agent) bindAdvisorySinks() {
	if a == nil {
		return
	}
	for _, t := range a.registryTerms() {
		if binder, ok := t.(AdvisorySinkBinder); ok {
			binder.SetAdvisorySink(a.enqueueExternalAdvisory)
		}
	}
}

// registryTerms returns every registered tool (including ones hidden from the
// current mode) so an external source is bound even when the mode that exposes
// its tool is selected later.
func (a *Agent) registryTerms() []Tool {
	if a == nil || a.registry == nil {
		return nil
	}
	return a.registry.List()
}

// enqueueExternalAdvisory delivers one notice to the active turn, or queues it
// for the next turn when no turn is running.
func (a *Agent) enqueueExternalAdvisory(text string) {
	text = strings.TrimSpace(text)
	if a == nil || text == "" {
		return
	}
	if tc := a.activeTurn.Load(); tc != nil {
		tc.enqueueAdvisory(text)
		return
	}
	a.advisoryMu.Lock()
	a.pendingAdvisories = append(a.pendingAdvisories, text)
	a.advisoryMu.Unlock()
}

// takePendingAdvisories drains the notices queued outside a turn.
func (a *Agent) takePendingAdvisories() []string {
	if a == nil {
		return nil
	}
	a.advisoryMu.Lock()
	defer a.advisoryMu.Unlock()
	if len(a.pendingAdvisories) == 0 {
		return nil
	}
	out := append([]string(nil), a.pendingAdvisories...)
	a.pendingAdvisories = nil
	return out
}

// appendAdvisoriesToPrompt appends external notices to the user prompt so a
// turn that starts after they were produced still gives the model the context.
func appendAdvisoriesToPrompt(prompt string, advisories []string) string {
	var b strings.Builder
	b.WriteString(prompt)
	for _, advisory := range advisories {
		if advisory = strings.TrimSpace(advisory); advisory == "" {
			continue
		}
		b.WriteString("\n\n[harness advisory] ")
		b.WriteString(advisory)
	}
	return b.String()
}
