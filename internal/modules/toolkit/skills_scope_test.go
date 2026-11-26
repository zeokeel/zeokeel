package toolkit

// Role-scoped skills (M3b): the skill store the model sees is filtered by the
// session's role, and Read refuses a skill outside it — the surface filter is
// the visible half, Read is the rule.

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
)

func TestFilteredSkillStoreAppliesTheRoleScope(t *testing.T) {
	settings := newAuthTestSettings()
	store := filterSkillStore{
		list: []rig.Skill{{Name: "deploy", Path: "/d"}, {Name: "secrets", Path: "/s"}},
	}
	filtered := filteredSkillStore{
		inner:    store,
		settings: settings,
		scope: func(context.Context) ([]string, bool) {
			return []string{"deploy"}, true
		},
	}
	ctx := context.Background()
	listed, err := filtered.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "deploy" {
		t.Fatalf("List = %+v, want only the role's skill", listed)
	}
	if _, err := filtered.Read(ctx, "secrets"); err == nil || !strings.Contains(err.Error(), "not available to this role") {
		t.Fatalf("Read(secrets) err = %v, want the role refusal", err)
	}
	if _, err := filtered.Read(ctx, "deploy"); err != nil {
		t.Fatalf("Read(deploy): %v", err)
	}
}

func TestFilteredSkillStoreUnrestrictedKeepsEverything(t *testing.T) {
	settings := newAuthTestSettings()
	store := filterSkillStore{list: []rig.Skill{{Name: "deploy"}, {Name: "secrets"}}}
	filtered := filteredSkillStore{
		inner:    store,
		settings: settings,
		scope: func(context.Context) ([]string, bool) {
			return nil, false // the role names no skills
		},
	}
	ctx := context.Background()
	listed, err := filtered.List(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("List = (%+v, %v), want everything", listed, err)
	}
	if _, err := filtered.Read(ctx, "secrets"); err != nil {
		t.Fatalf("Read(secrets) under an unrestricted role: %v", err)
	}
}

// No resolver at all (an assembly without the agent's scope service) must keep
// today's behaviour rather than hiding every skill.
func TestFilteredSkillStoreWithoutAScopeIsOpen(t *testing.T) {
	settings := newAuthTestSettings()
	store := filterSkillStore{list: []rig.Skill{{Name: "deploy"}}}
	filtered := filteredSkillStore{inner: store, settings: settings}
	if listed, err := filtered.List(context.Background()); err != nil || len(listed) != 1 {
		t.Fatalf("List = (%+v, %v), want the skill", listed, err)
	}
}
