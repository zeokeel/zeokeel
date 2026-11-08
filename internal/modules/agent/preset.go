package agent

// Agent presets: a named persona/prompt fragment, optional default model and
// optional model-visible tool allowlist. A session stores only the preset ID;
// every run resolves the preset fresh from the presets.agents settings row so
// edits apply on the next turn. Preset instructions are appended after the base
// prompt and workspace instructions: base → AGENTS.md → persona → prompt.

import (
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SetSessionPreset implements contracts.SessionPresetSetter: it selects one
// configured agent preset for a session ("" clears it). Unknown sessions and
// unknown preset IDs are rejected.
func (s *service) SetSessionPreset(sessionID, presetID string) error {
	sessionID = strings.TrimSpace(sessionID)
	presetID = strings.TrimSpace(presetID)
	s.mu.Lock()
	_, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if presetID != "" {
		if _, found := s.agentPresetByID(presetID); !found {
			return fmt.Errorf("agent: unknown agent preset %q", presetID)
		}
	}
	s.mu.Lock()
	if presetID == "" {
		delete(s.sessionPresets, sessionID)
	} else {
		s.sessionPresets[sessionID] = presetID
	}
	s.mu.Unlock()
	return nil
}

// SessionPreset reports the session's selected preset ID ("" when none).
func (s *service) SessionPreset(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionPresets[strings.TrimSpace(sessionID)]
}

// effectiveAgentPresets parses the presets.agents row, preserving its order and
// dropping rows without an ID/name (malformed JSON behaves as no presets).
func (s *service) effectiveAgentPresets() []contracts.AgentPreset {
	if s.settings == nil {
		return nil
	}
	presets, err := contracts.ParseAgentPresets(s.settings.Get(contracts.SettingAgentPresets, ""))
	if err != nil {
		s.logger.Printf("[agent] parse %s: %v", contracts.SettingAgentPresets, err)
		return nil
	}
	out := make([]contracts.AgentPreset, 0, len(presets))
	for _, preset := range presets {
		preset.ID = strings.TrimSpace(preset.ID)
		preset.Name = strings.TrimSpace(preset.Name)
		if preset.ID == "" || preset.Name == "" {
			continue
		}
		preset.Persona = strings.TrimSpace(preset.Persona)
		preset.Prompt = strings.TrimSpace(preset.Prompt)
		preset.Model = strings.TrimSpace(preset.Model)
		tools := make([]string, 0, len(preset.Tools))
		for _, tool := range preset.Tools {
			if trimmed := strings.TrimSpace(tool); trimmed != "" {
				tools = append(tools, trimmed)
			}
		}
		preset.Tools = tools
		out = append(out, preset)
	}
	return out
}

// agentPresetByID resolves one preset from the settings row.
func (s *service) agentPresetByID(id string) (contracts.AgentPreset, bool) {
	id = strings.TrimSpace(id)
	for _, preset := range s.effectiveAgentPresets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return contracts.AgentPreset{}, false
}

// agentPresetForSessionLocked resolves the selected preset for a session. The
// caller holds s.mu (reading settings itself is goroutine-safe).
func (s *service) agentPresetForSessionLocked(sessionID string) (contracts.AgentPreset, bool) {
	id := strings.TrimSpace(s.sessionPresets[sessionID])
	if id == "" {
		return contracts.AgentPreset{}, false
	}
	return s.agentPresetByID(id)
}

// withPresetInstructions appends the preset persona and prompt to the run's
// system prompt (persona first, prompt second). Missing/empty fragments are
// skipped; the base prompt and workspace instructions stay first.
func (s *service) withPresetInstructions(sessionID string, cfg contracts.ChatConfig) contracts.ChatConfig {
	preset, ok := s.agentPresetForSession(sessionID)
	if !ok {
		return cfg
	}
	for _, fragment := range []string{preset.Persona, preset.Prompt} {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		if strings.TrimSpace(cfg.SystemPrompt) == "" {
			cfg.SystemPrompt = fragment
			continue
		}
		cfg.SystemPrompt = strings.TrimRight(cfg.SystemPrompt, "\n") + "\n\n" + fragment
	}
	return cfg
}

// agentPresetForSession reads the selected preset without the service lock.
func (s *service) agentPresetForSession(sessionID string) (contracts.AgentPreset, bool) {
	s.mu.Lock()
	id := strings.TrimSpace(s.sessionPresets[strings.TrimSpace(sessionID)])
	s.mu.Unlock()
	if id == "" {
		return contracts.AgentPreset{}, false
	}
	return s.agentPresetByID(id)
}

// toolsForSession returns the registry tools filtered by the selected preset's
// allowlist (empty allowlist keeps every tool, preserving registration order).
// The chat-mode/plan-mode gates still apply on top of this filtered surface.
func (s *service) toolsForSession(sessionID string) []rig.Tool {
	tools := s.registry.Tools()
	preset, ok := s.agentPresetForSession(sessionID)
	if !ok || len(preset.Tools) == 0 {
		return tools
	}
	allowed := make(map[string]bool, len(preset.Tools))
	for _, name := range preset.Tools {
		allowed[name] = true
	}
	filtered := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if allowed[strings.TrimSpace(tool.Name())] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}
