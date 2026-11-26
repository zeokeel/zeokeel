package toolkit

// The agent-facing skill store honours skills.disabled. The Skills page keeps
// using the unfiltered KeySkills service so disabled skills stay visible and
// can be re-enabled; only the model-facing tool sees the filtered view.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type filteredSkillStore struct {
	inner    contracts.SkillsService
	settings contracts.SettingsService
	// scope reports the skills the calling session's role allows (M3b). nil or
	// an unrestricted answer keeps the historical behaviour: every discovered
	// skill is visible. The resolver runs per call because the scope is a
	// property of the session the tool call came from, not of the process.
	scope func(ctx context.Context) ([]string, bool)
}

// allowedSkillNames asks the scope for this call's allowed names. ok=false means
// "unrestricted" (the role named no skills).
func (s filteredSkillStore) allowedSkillNames(ctx context.Context) (map[string]bool, bool) {
	if s.scope == nil {
		return nil, false
	}
	names, restricted := s.scope(ctx)
	if !restricted {
		return nil, false
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return allowed, true
}

var _ contracts.SkillsService = filteredSkillStore{}
var _ rig.SkillStore = filteredSkillStore{}

// disabledSkillNames reads skills.disabled on every call so a UI toggle applies
// without rebuilding the toolkit.
func disabledSkillNames(settings contracts.SettingsService) map[string]bool {
	disabled := make(map[string]bool)
	if settings == nil {
		return disabled
	}
	names, err := contracts.ParseStringListJSON(settings.Get(contracts.SettingSkillsDisabled, ""))
	if err != nil {
		return disabled
	}
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			disabled[strings.ToLower(trimmed)] = true
		}
	}
	return disabled
}

func (s filteredSkillStore) Roots() []string {
	if s.inner == nil {
		return nil
	}
	return s.inner.Roots()
}

func (s filteredSkillStore) List(ctx context.Context) ([]rig.Skill, error) {
	if s.inner == nil {
		return nil, nil
	}
	skills, err := s.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	disabled := disabledSkillNames(s.settings)
	allowed, restricted := s.allowedSkillNames(ctx)
	kept := make([]rig.Skill, 0, len(skills))
	for _, skill := range skills {
		name := strings.ToLower(strings.TrimSpace(skill.Name))
		if disabled[name] {
			continue
		}
		if restricted && !allowed[name] {
			continue
		}
		kept = append(kept, skill)
	}
	return kept, nil
}

func (s filteredSkillStore) Read(ctx context.Context, name string) (rig.Skill, error) {
	if s.inner == nil {
		return rig.Skill{}, fmt.Errorf("skill %q is not available", name)
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if disabledSkillNames(s.settings)[key] {
		return rig.Skill{}, fmt.Errorf("skill %q is disabled", name)
	}
	if allowed, restricted := s.allowedSkillNames(ctx); restricted && !allowed[key] {
		// The role does not name this skill: refusing is the enforcement half of
		// the surface filter (a replayed or hand-written call must not widen it).
		return rig.Skill{}, fmt.Errorf("skill %q is not available to this role", name)
	}
	return s.inner.Read(ctx, name)
}
