package agent

// Lazy tool discovery (rig tools/discovery): the request only carries the
// frequently used tools, and tool_search loads the rest on demand. The hidden
// tail is deliberate and small (worktrees, code intelligence, scheduling, every
// MCP server); everything else — including tools this list does not know — stays
// active so nothing silently disappears.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	"github.com/zeokeel/zeokeel-rig/tools/discovery"
)

// discoveryTailTools is the discoverable tail: families that are large (many
// schemas, many MCP tools), rare within one turn, or both. Everything else —
// including product, session and custom host-registered tools this list does not
// know — stays active, so a tool can never silently disappear from the model
// just because the discovery list was not updated.
var discoveryTailTools = []string{
	// worktrees
	"enter_worktree", "exit_worktree", "list_worktrees",
	// code intelligence
	"lsp_definition", "lsp_diagnostics", "lsp_hover", "lsp_implementation",
	"lsp_references", "lsp_symbols",
	// scheduling
	"schedule_create", "schedule_list", "schedule_update", "schedule_delete",
}

// discoveryActivation is the hidden tail: worktree/LSP/schedule families plus
// every MCP server (a prefix rule). A preset can force a tail tool back on.
var discoveryActivation = discovery.Activation{
	Tail:         discoveryTailTools,
	TailPrefixes: []string{"mcp__"},
}

// isDiscoverableTail reports whether a tool starts out hidden from the request.
func isDiscoverableTail(name string) bool {
	return discoveryActivation.Hidden(name)
}

// initialActiveTools is the run's starting active set over the session's
// exposed surface (preset/mode already applied): the SDK activation rule (every
// exposed tool except the hidden tail, forced names kept, unknown tools active)
// plus whatever the session activated with tool_search.
func (s *service) initialActiveTools(sessionID string, tools []rig.Tool) []string {
	if s == nil {
		return nil
	}
	activation := discoveryActivation
	if preset, ok := s.agentPresetForSession(sessionID); ok {
		activation.Forced = preset.Tools
	}
	names := discovery.ActiveNames(tools, activation)
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[name] = true
	}
	// Tools the runtime always keeps active count as well.
	for _, tool := range tools {
		if tool == nil || rig.IsToolDiscoverable(tool) {
			continue
		}
		if name := strings.TrimSpace(tool.Name()); name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	s.mu.Lock()
	if state := s.sessions[sessionID]; state != nil {
		for name := range state.activeTools {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	s.mu.Unlock()
	return names
}

// activeToolSet filters one run's tool surface down to what the model may call
// at the start of the run (the prompt guidance must describe the same surface
// the request carries).
func (s *service) activeToolSet(sessionID string, tools []rig.Tool) map[string]bool {
	active := make(map[string]bool, len(tools))
	for _, name := range s.initialActiveTools(sessionID, tools) {
		active[name] = true
	}
	// Tools the runtime always keeps active count as well.
	for _, tool := range tools {
		if tool != nil && len(tool.Name()) > 0 && !rig.IsToolDiscoverable(tool) {
			active[tool.Name()] = true
		}
	}
	return active
}

// discoverableTools lists the session's exposed tools that are neither active
// nor always-active, as tool_search items.
func (s *service) discoverableTools(ctx context.Context) []discovery.Item {
	return s.discoverableToolsFor(rigagent.ToolSessionID(ctx))
}

// discoverableToolsFor is discoverableTools with the session resolved by the
// caller (unit tests cannot build rig's tool context).
func (s *service) discoverableToolsFor(sessionID string) []discovery.Item {
	surface := s.toolsForSession(sessionID)
	active := s.activeToolSet(sessionID, surface)
	items := make([]discovery.Item, 0, len(surface))
	for _, tool := range surface {
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name())
		if name == "" || active[name] || !rig.IsToolDiscoverable(tool) {
			continue
		}
		items = append(items, discovery.Item{Name: name, Summary: summariseToolDescription(tool.Description())})
	}
	return items
}

// summariseToolDescription keeps the first sentence of a tool description (the
// list is a hint, not documentation).
func summariseToolDescription(description string) string {
	text := strings.TrimSpace(description)
	if index := strings.IndexByte(text, '.'); index > 0 && index < 160 {
		text = text[:index]
	}
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

// activateSessionTools is tool_search's Activate callback: it asks the session's
// live runner to load the names and remembers them for the following runs.
func (s *service) activateSessionTools(ctx context.Context, names []string) (activated, unknown []string) {
	return s.activateSessionToolsFor(rigagent.ToolSessionID(ctx), names)
}

// activateSessionToolsFor is activateSessionTools with the session resolved by
// the caller.
func (s *service) activateSessionToolsFor(sessionID string, names []string) (activated, unknown []string) {
	if sessionID == "" {
		return nil, names
	}
	runner := s.sessionRunner(sessionID)
	if runner == nil {
		return nil, names
	}
	activated, unknown = runner.ActivateTools(sessionID, names...)
	if len(activated) > 0 {
		s.mu.Lock()
		if state := s.sessions[sessionID]; state != nil {
			if state.activeTools == nil {
				state.activeTools = make(map[string]bool, len(activated))
			}
			for _, name := range activated {
				state.activeTools[strings.TrimSpace(name)] = true
			}
		}
		s.mu.Unlock()
	}
	return activated, unknown
}

// bindSessionRunner records the live runner so tool_search can activate tools
// inside the run that asked (activation must reach the same request stream).
func (s *service) bindSessionRunner(sessionID string, runner any) {
	activator, ok := runner.(rig.ToolActivator)
	if !ok {
		return
	}
	s.mu.Lock()
	if state := s.sessions[sessionID]; state != nil {
		state.runner = activator
	}
	s.mu.Unlock()
}

// clearSessionRunner drops the run's activator when the turn finishes.
func (s *service) clearSessionRunner(sessionID string) {
	s.mu.Lock()
	if state := s.sessions[sessionID]; state != nil {
		state.runner = nil
	}
	s.mu.Unlock()
}

// sessionRunner returns the session's live runner, or nil outside a run.
func (s *service) sessionRunner(sessionID string) rig.ToolActivator {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state := s.sessions[sessionID]; state != nil {
		return state.runner
	}
	return nil
}

// discoveryName is the model-visible name of the discovery tool (kept as a
// helper so registration and cleanup cannot drift).
func discoveryName() string { return discovery.ToolSearch }

// activeRunTools keeps the tools the run may call at its start (the prompt
// guidance must describe the same surface the request carries).
func activeRunTools(tools []rig.Tool, active map[string]bool) []rig.Tool {
	kept := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if active[name] || !rig.IsToolDiscoverable(tool) {
			kept = append(kept, tool)
		}
	}
	return kept
}

// registerDiscoveryTool installs tool_search into the shared registry once per
// activation. The callbacks reach this service's per-session state.
func (s *service) registerDiscoveryTool(tool rig.Tool) error {
	if s == nil || s.registry == nil {
		return nil
	}
	// A reloaded module can meet its own previous registration (the cleanup
	// effect of the old instance may still be pending): replace it instead of
	// failing the whole agent module on a duplicate name.
	if existing := s.registry.Specs(); len(existing) > 0 {
		for _, spec := range existing {
			if spec.Name == discovery.ToolSearch {
				s.registry.Unregister(discovery.ToolSearch)
				break
			}
		}
	}
	if err := s.registry.Register(tool); err != nil {
		return fmt.Errorf("register tool_search: %w", err)
	}
	return nil
}

// newDiscoveryTool builds the tool_search instance bound to this service (its
// cleanup must only unregister the instance it registered).
func (s *service) newDiscoveryTool() rig.Tool {
	return discovery.New(discovery.Options{
		List:     s.discoverableTools,
		Activate: s.activateSessionTools,
	})
}
