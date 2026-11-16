package plugins

// Internal tests for the capability tab registry: the shell reads its tabs from
// one ordered list, so adding the future workflow tab is one entry (no shell
// change and no new screen). Skills and connectors are parallel entries at the
// same level.

import "testing"

func TestCapabilityTabSpecsAreParallel(t *testing.T) {
	specs := capabilityTabSpecs(Config{})
	if len(specs) != 2 {
		t.Fatalf("capability tabs = %d, want 2 (skills, connectors)", len(specs))
	}
	if specs[0].ID != "skills" || specs[1].ID != "connectors" {
		t.Fatalf("tab order = %q, %q; want skills, connectors", specs[0].ID, specs[1].ID)
	}
	for _, spec := range specs {
		if spec.LabelZh == "" || spec.LabelEn == "" {
			t.Fatalf("tab %q has an empty label (%q/%q)", spec.ID, spec.LabelZh, spec.LabelEn)
		}
		if spec.Build == nil {
			t.Fatalf("tab %q has no page builder", spec.ID)
		}
	}
}

func TestCapabilityTabLabelsUseConfiguredSections(t *testing.T) {
	specs := capabilityTabSpecs(Config{SkillsSection: "技能 X", MCPSection: "连接器 Y"})
	if specs[0].LabelZh != "技能 X" || specs[1].LabelZh != "连接器 Y" {
		t.Fatalf("configured labels not used: %q, %q", specs[0].LabelZh, specs[1].LabelZh)
	}
}

func TestCapabilityTabIndexIsClamped(t *testing.T) {
	st := &state{capabilityTab: 99}
	specs := capabilityTabSpecs(Config{})
	if got := st.clampCapabilityTab(specs); got != len(specs)-1 {
		t.Fatalf("clamped tab = %d, want %d", got, len(specs)-1)
	}
	st.capabilityTab = -1
	if got := st.clampCapabilityTab(specs); got != 0 {
		t.Fatalf("clamped negative tab = %d, want 0", got)
	}
}
