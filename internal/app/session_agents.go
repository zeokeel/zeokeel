package app

import (
	"log"
	"strings"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// Per-session agents (RT-04).
//
// The app installs a session.Controller AgentFactory so every session gets its
// own runtime.Agent, built for that session's workspace root, access mode and
// model. This lets turns of different sessions run in parallel and keeps an
// approval wait local to its own session. The shared bundle built by
// BuildServicesWith stays as the fallback/title/team host and is what older
// paths (RunHeadless CLI, tests) use when no factory is installed.
//
// The session bundles are owned here: the factory records each one, and the
// controller's discard callback closes it when the agent is invalidated
// (settings/plugin/model change, permission switch) or the controller closes.

// installSessionAgentFactory enables per-session agents on ctrl. The app must
// have a.services set (the factory reads settings through the controller).
func (a *app) installSessionAgentFactory(ctrl *session.Controller) {
	if ctrl == nil {
		return
	}
	a.bundlesMu.Lock()
	if a.sessionBundles == nil {
		a.sessionBundles = make(map[string]*AgentBundle)
	}
	a.bundlesMu.Unlock()
	ctrl.SetAgentFactory(a.buildSessionAgent, a.discardSessionAgent)
	// Each per-session bundle owns its own approval queue; decisions must be
	// resolved against that session's queue.
	ctrl.SetAgentGateFactory(a.sessionGate)
}

// sessionGate returns the approval queue of sessionID's per-session bundle (nil
// before the agent is built or after it was discarded).
func (a *app) sessionGate(sessionID string) *runtime.ApprovalQueue {
	a.bundlesMu.Lock()
	defer a.bundlesMu.Unlock()
	if bundle := a.sessionBundles[sessionID]; bundle != nil {
		return bundle.Gate
	}
	return nil
}

// buildSessionAgent builds (or rebuilds) sessionID's agent through the normal
// tool/agent builder, bound to that session's workspace root and access mode.
func (a *app) buildSessionAgent(sessionID string) (*runtime.Agent, error) {
	ctrl := a.services
	if ctrl == nil {
		return nil, session.ErrNoAgent
	}
	st := ctrl.Store()
	root, ok := a.sessionWorkspaceRoot(sessionID)
	if !ok || strings.TrimSpace(root) == "" {
		root = st.Dir()
	}
	permission := ctrl.SessionPermissionOrDefault(sessionID)
	// Declarative agent presets (FP-06): resolve the session's effective preset
	// (explicit selection, default, legacy mode alias, fallback) and let the
	// build filter tools, inject the persona and pick the topology. An unknown
	// preset never fails the build: ResolvePreset falls back to standard with a
	// warning the app logs.
	preset, warnings := ctrl.ResolvePreset(sessionID)
	for _, warning := range warnings {
		log.Printf("zeokeel: session %s preset: %s", sessionID, warning)
	}
	opts := a.toolOptions(st, root, permission)
	opts.Presets = ctrl.Presets()
	opts.PresetID = preset.ID
	opts.QuestionGate = a.questionGateForSession(sessionID)
	// Serialize with rebuildAgent: both replace bundles and close old ones.
	a.buildMu.Lock()
	defer a.buildMu.Unlock()
	bundle, err := buildAgentWithOptions(st, root, DefaultInstruction, opts)
	if err != nil {
		return nil, err
	}
	if bundle == nil || bundle.Agent == nil {
		return nil, nil
	}
	// Surface fail-closed preset problems (for example an allowlist that
	// matched no registered tool) to the session UI.
	ctrl.SetPresetWarnings(sessionID, bundle.PresetWarnings)
	a.bundlesMu.Lock()
	if a.sessionBundles == nil {
		a.sessionBundles = make(map[string]*AgentBundle)
	}
	old := a.sessionBundles[sessionID]
	a.sessionBundles[sessionID] = bundle
	a.bundlesMu.Unlock()
	if old != nil && old != bundle && old.Close != nil {
		if cerr := old.Close(); cerr != nil {
			log.Printf("zeokeel: closing replaced agent bundle for session %s: %v", sessionID, cerr)
		}
	}
	return bundle.Agent, nil
}

// discardSessionAgent closes the bundle that owned agent when the controller
// stops using it.
func (a *app) discardSessionAgent(sessionID string, agent *runtime.Agent) {
	a.bundlesMu.Lock()
	bundle := a.sessionBundles[sessionID]
	delete(a.sessionBundles, sessionID)
	a.bundlesMu.Unlock()
	if bundle == nil || bundle.Agent != agent {
		return
	}
	if bundle.Close != nil {
		if err := bundle.Close(); err != nil {
			log.Printf("zeokeel: closing agent bundle for session %s: %v", sessionID, err)
		}
	}
}

// invalidateSessionAgents drops every cached per-session agent (settings,
// plugin or model changes apply to the next turn of every session).
func (a *app) invalidateSessionAgents() {
	if a.services == nil {
		return
	}
	a.services.InvalidateAgents()
}

// closeSessionBundles closes every per-session bundle (app shutdown).
func (a *app) closeSessionBundles() {
	a.bundlesMu.Lock()
	bundles := a.sessionBundles
	a.sessionBundles = make(map[string]*AgentBundle)
	a.bundlesMu.Unlock()
	for sessionID, bundle := range bundles {
		if bundle != nil && bundle.Close != nil {
			if err := bundle.Close(); err != nil {
				log.Printf("zeokeel: closing agent bundle for session %s: %v", sessionID, err)
			}
		}
	}
}
