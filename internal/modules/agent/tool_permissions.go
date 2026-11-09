package agent

// Session-scoped "always allow" tool memory. When the user answers the tool
// approval dialog with "always allow in this session", the tool name is
// remembered in settings under contracts.SessionAllowedToolsSettingKey(sessionID)
// and the gate stops asking for that tool in that session. The set is per
// session and never global: deleting the session or the UI's explicit clear
// forgets it.
//
// Decisions never touch the global permission mode (agent.permission); plan/chat
// modes and a per-session permission override still take precedence in the gate,
// and the deny-all "never" policy still denies.

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// parseAllowedTools decodes one persisted settings row into the remembered tool
// names, sorted and deduplicated. A missing row, malformed JSON or a non-array
// value means "nothing remembered" (never an error: a corrupt row must not break
// the run or activation).
func parseAllowedTools(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// allowedToolsRow reads the persisted set of one session (empty when unknown).
func (s *service) allowedToolsRow(sessionID string) []string {
	if s.settings == nil {
		return []string{}
	}
	return parseAllowedTools(s.settings.Get(contracts.SessionAllowedToolsSettingKey(sessionID), ""))
}

// AllowedTools implements contracts.SessionToolPermissions: the session's
// remembered "always allow" tools, sorted and deduplicated (never nil).
func (s *service) AllowedTools(sessionID string) []string {
	return s.allowedToolsRow(strings.TrimSpace(sessionID))
}

// ClearAllowedTools implements contracts.SessionToolPermissions: it forgets
// every remembered tool of the session by writing an empty row.
func (s *service) ClearAllowedTools(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if s.settings == nil {
		return ErrSettingsUnavailable
	}
	s.allowedToolsMu.Lock()
	defer s.allowedToolsMu.Unlock()
	if err := s.settings.Set(contracts.SessionAllowedToolsSettingKey(sessionID), ""); err != nil {
		return err
	}
	return nil
}

// allowedToolForSession reports whether the session already remembers name as
// always allowed.
func (s *service) allowedToolForSession(sessionID, name string) bool {
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if sessionID == "" || name == "" {
		return false
	}
	for _, allowed := range s.allowedToolsRow(sessionID) {
		if allowed == name {
			return true
		}
	}
	return false
}

// rememberAllowedTool persists one "always allow in this session" answer. The
// read-modify-write is serialized by allowedToolsMu: while the agent loop calls
// the gate serially per run, two runs of different sessions (or a future
// parallel loop) would otherwise lose a concurrently remembered name. Write
// failures are logged only: the current run was allowed anyway.
func (s *service) rememberAllowedTool(sessionID, name string) {
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if s.settings == nil || sessionID == "" || name == "" {
		return
	}
	s.allowedToolsMu.Lock()
	defer s.allowedToolsMu.Unlock()
	current := s.allowedToolsRow(sessionID)
	for _, allowed := range current {
		if allowed == name {
			return
		}
	}
	current = append(current, name)
	sort.Strings(current)
	encoded, err := json.Marshal(current)
	if err != nil {
		s.logger.Printf("[agent] encode allowed tools of session %s: %v", sessionID, err)
		return
	}
	if err := s.settings.Set(contracts.SessionAllowedToolsSettingKey(sessionID), string(encoded)); err != nil {
		s.logger.Printf("[agent] persist allowed tools of session %s: %v", sessionID, err)
	}
}

// clearAllowedToolsRow deletes the session's remembered set as part of session
// deletion (never fails the delete; failures are logged).
func (s *service) clearAllowedToolsRow(sessionID string) {
	if err := s.ClearAllowedTools(sessionID); err != nil && s.settings != nil {
		s.logger.Printf("[agent] clear allowed tools of session %s: %v", sessionID, err)
	}
}
