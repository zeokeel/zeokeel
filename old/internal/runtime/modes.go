package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/adk/prebuilt/supervisor"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// Agent modes. The three supported presets own both the eino topology and the
// model-visible tool set:
//
//   - ModeStandard builds a single adk.ChatModelAgent over the configured
//     Provider and tool Registry. This is the historical behavior and the only
//     mode that works without a tool-calling model.
//   - ModeMinimal exposes exactly terminal_open, terminal_list, terminal_read,
//     terminal_send, terminal_signal, terminal_close, read_image and
//     working_directory. Context compaction still applies because the runtime
//     applies it around every topology.
//   - ModeCreator adds settings_get/settings_set to the standard tool set so
//     the agent can customize ZeoKeel through conversation and write skills
//     under <root>/skills/<name>/SKILL.md with the standard fs tools.
//
// Three legacy topologies remain accepted as internal aliases so older saved
// sessions and tests keep working, but they are not listed by Modes and have
// no UI entry:
//
//   - ModeDeep builds the deep-agent topology: a ChatModelAgent plus a
//     write_todos planning tool and a task tool that delegates to a subagent.
//     eino's prebuilt deep agent (adk/prebuilt/deep) has the same shape, but it
//     imports adk/filesystem, which requires github.com/bmatcuk/doublestar/v4
//     in go.mod; the topology is therefore assembled from ADK primitives in
//     mode_deep.go so the module graph does not change.
//   - ModeSupervisor builds eino's prebuilt supervisor (adk/prebuilt/supervisor):
//     a supervisor ChatModelAgent that transfers to a general-purpose subagent.
//   - ModePlanExecute builds eino's prebuilt plan-execute-replan agent
//     (adk/prebuilt/planexecute): a planner, an executor and a replanner loop.
const (
	// ModeStandard is a single ChatModelAgent with the full registry.
	ModeStandard = "standard"
	// ModeMinimal exposes the six DSH terminal tools, read_image and
	// working_directory only.
	ModeMinimal = "minimal"
	// ModeCreator is standard plus settings authoring for customization.
	ModeCreator = "creator"

	// ModeDeep is the legacy deep-agent topology alias.
	ModeDeep = "deep"
	// ModeSupervisor is the legacy eino prebuilt supervisor alias.
	ModeSupervisor = "supervisor"
	// ModePlanExecute is the legacy eino prebuilt plan-execute alias.
	ModePlanExecute = "planexecute"
)

// modeAliases maps accepted, non-displayed spellings to their canonical mode.
// "cordis" is DSH's internal preset id for Creator mode.
var modeAliases = map[string]string{
	"cordis":       ModeCreator,
	"minimal-mode": ModeMinimal,
	"minimal_mode": ModeMinimal,
	"creator-mode": ModeCreator,
	"creator_mode": ModeCreator,
}

// minimalToolNames is Minimal mode's complete model-visible tool set, matching
// DSH's minimal preset: the six terminal session tools, read_image and
// working_directory. It is an exact allowlist rather than a terminal_* prefix
// rule, so a terminal_-prefixed registry tool that is not one of the six DSH
// tools cannot leak into the preset by accident.
var minimalToolNames = map[string]bool{
	"terminal_open":     true,
	"terminal_list":     true,
	"terminal_read":     true,
	"terminal_send":     true,
	"terminal_signal":   true,
	"terminal_close":    true,
	"read_image":        true,
	"working_directory": true,
}

// creatorOnlyToolNames are registered by the app for Creator mode only. The
// standard and legacy tool sets hide them, matching DSH's separation between
// ordinary work and customization.
var creatorOnlyToolNames = map[string]bool{
	"settings_get": true,
	"settings_set": true,
}

// Modes returns the supported, user-visible agent modes in display order. The
// empty string is accepted by Config.Mode and NormalizeMode as an alias for
// ModeStandard, but is normalized away before an Agent is built. The legacy
// deep/supervisor/planexecute aliases are intentionally absent.
func Modes() []string {
	return []string{ModeStandard, ModeMinimal, ModeCreator}
}

// NormalizeMode validates mode and returns its canonical value. "" means
// ModeStandard. The legacy topologies and DSH's "cordis" spelling are accepted
// as aliases; unknown modes are an error.
func NormalizeMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ModeStandard, nil
	}
	if alias, ok := modeAliases[mode]; ok {
		return alias, nil
	}
	switch mode {
	case ModeStandard, ModeMinimal, ModeCreator,
		ModeDeep, ModeSupervisor, ModePlanExecute:
		return mode, nil
	}
	return "", fmt.Errorf("runtime: unknown agent mode %q (want %s)", mode, strings.Join(Modes(), ", "))
}

// modeAllowsTool reports whether a registered tool is visible in mode. Minimal
// uses the exact minimalToolNames allowlist; the Creator settings tools are the
// only other registry entries whose visibility depends on a mode.
func modeAllowsTool(mode, name string) bool {
	switch mode {
	case ModeMinimal:
		return minimalToolNames[name]
	default:
		return !creatorOnlyToolNames[name]
	}
}

// strictMode reports whether the runtime can reject unknown model-requested
// tool names itself. The DSH modes own a complete model-visible set; the
// prebuilt legacy topologies add eino built-ins (write_todos, task,
// transfer_to_agent, ...) that are not in the registry, so they leave unknown
// names to eino.
func strictMode(mode string) bool {
	switch mode {
	case ModeDeep, ModeSupervisor, ModePlanExecute:
		return false
	default:
		return true
	}
}

// ModeTools filters the model-visible tool names for a mode and adds the
// tools that mode provides itself. It is a pure helper for callers that need
// to describe a mode's tools without building an Agent; Agent.effectiveTools
// is the corresponding live implementation.
func ModeTools(mode string, available []string) []string {
	normalized, err := NormalizeMode(mode)
	if err != nil {
		normalized = ModeStandard
	}
	out := make([]string, 0, len(available)+3)
	seen := make(map[string]bool, len(available)+3)
	for _, name := range available {
		if !modeAllowsTool(normalized, name) || seen[name] {
			continue
		}
		out = append(out, name)
		seen[name] = true
	}
	add := func(name string) {
		if !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	switch normalized {
	case ModeCreator:
		add("settings_get")
		add("settings_set")
	}
	sort.Strings(out)
	return out
}

// modelHandlers are the eino ChatModelAgent middleware shared by every topology
// the runtime builds directly. They attach images returned by tools to the
// conversation before the next model call. The planexecute executor is built by
// eino's prebuilt package, which does not accept handlers; the provider adapter
// performs the same injection again for every provider-backed request, so that
// mode still forwards tool images.
func (a *Agent) modelHandlers() []adk.ChatModelAgentMiddleware {
	return []adk.ChatModelAgentMiddleware{newImageInjector()}
}

// buildMode constructs the eino agent topology for snap.mode. a holds
// everything mode-independent (registry, gate, name, settings); snap carries
// the mutable instruction/model inputs explicitly so the builder never has to
// read them from Agent (or take agentMu) while a rebuild holds the lock. The
// returned agent is swapped in by New, SetMode, SetModel and
// refreshInstruction.
func (a *Agent) buildMode(snap agentSnapshot) (adk.Agent, error) {
	mode := snap.mode
	toolsConfig := adk.ToolsConfig{
		ToolsNodeConfig: compose.ToolsNodeConfig{Tools: a.einoToolsForMode(mode)},
	}

	switch mode {
	case ModeStandard, ModeMinimal, ModeCreator:
		return adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
			Name:          a.name,
			Description:   "ZeoKeel agent runtime",
			Instruction:   snap.instruction,
			Model:         snap.chatModel,
			MaxIterations: a.einoMaxIterations(),
			ToolsConfig:   toolsConfig,
			Handlers:      a.modelHandlers(),
		})
	case ModeDeep:
		return a.buildDeepAgent(snap)
	case ModeSupervisor:
		instruction := snap.instruction
		if instruction != "" {
			instruction += "\n\n"
		}
		instruction += "You coordinate sub-agents. Hand a task to a sub-agent when its" +
			" description fits better than handling it yourself, and answer directly" +
			" once you have enough information."
		root, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
			Name:          a.name,
			Description:   "ZeoKeel supervisor",
			Instruction:   instruction,
			Model:         snap.chatModel,
			MaxIterations: a.einoMaxIterations(),
			ToolsConfig:   toolsConfig,
			Handlers:      a.modelHandlers(),
		})
		if err != nil {
			return nil, fmt.Errorf("runtime: build supervisor: %w", err)
		}
		sub, err := a.buildSubagentAgent(context.Background(), "general-purpose", snap)
		if err != nil {
			return nil, err
		}
		agent, err := supervisor.New(context.Background(), &supervisor.Config{
			Supervisor: root,
			SubAgents:  []adk.Agent{sub},
		})
		if err != nil {
			return nil, fmt.Errorf("runtime: build supervisor agent: %w", err)
		}
		return agent, nil
	case ModePlanExecute:
		tcm, ok := snap.chatModel.(model.ToolCallingChatModel)
		if !ok {
			return nil, fmt.Errorf("runtime: mode %q requires a tool-calling chat model (configure Config.Provider)", mode)
		}
		planner, err := planexecute.NewPlanner(context.Background(), &planexecute.PlannerConfig{
			ToolCallingChatModel: tcm,
		})
		if err != nil {
			return nil, fmt.Errorf("runtime: build planner: %w", err)
		}
		executor, err := planexecute.NewExecutor(context.Background(), &planexecute.ExecutorConfig{
			Model:         snap.chatModel,
			ToolsConfig:   toolsConfig,
			MaxIterations: a.einoMaxIterations(),
		})
		if err != nil {
			return nil, fmt.Errorf("runtime: build executor: %w", err)
		}
		replanner, err := planexecute.NewReplanner(context.Background(), &planexecute.ReplannerConfig{
			ChatModel: tcm,
		})
		if err != nil {
			return nil, fmt.Errorf("runtime: build replanner: %w", err)
		}
		agent, err := planexecute.New(context.Background(), &planexecute.Config{
			Planner:       planner,
			Executor:      executor,
			Replanner:     replanner,
			MaxIterations: a.einoMaxIterations(),
		})
		if err != nil {
			return nil, fmt.Errorf("runtime: build plan-execute agent: %w", err)
		}
		return agent, nil
	default:
		return nil, fmt.Errorf("runtime: unknown agent mode %q", mode)
	}
}

// einoToolsForMode wraps the mode's model-visible tools for eino. newEinoTool
// keeps the Gate and auto-review wiring, so approvals and automatic review
// behave the same in every mode.
func (a *Agent) einoToolsForMode(mode string) []tool.BaseTool {
	list := a.effectiveTools(mode)
	out := make([]tool.BaseTool, 0, len(list))
	for _, t := range list {
		out = append(out, newEinoTool(t, a.gate, a.reviewer, a.questionGate))
	}
	return out
}

// effectiveTools returns the tools the model can see in mode. The registry is
// the source of truth for ordinary tools; the mode adds its own tool when it
// is absent (the app registers the concrete instances so the registry, tests
// and settings screen can see them, while a bare runtime still gets the
// documented behavior).
func (a *Agent) effectiveTools(mode string) []Tool {
	normalized, err := NormalizeMode(mode)
	if err != nil {
		normalized = ModeStandard
	}
	registered := a.registry.List()
	out := make([]Tool, 0, len(registered)+3)
	present := make(map[string]bool, len(registered)+3)
	for _, t := range registered {
		name := t.Name()
		if !modeAllowsTool(normalized, name) {
			continue
		}
		out = append(out, t)
		present[name] = true
	}
	switch normalized {
	case ModeCreator:
		if !present["settings_get"] {
			out = append(out, NewSettingsGetTool(a.settings))
		}
		if !present["settings_set"] {
			out = append(out, NewSettingsSetTool(a.settings))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
