package prompt

import (
	"strings"
	"testing"
)

// TestPromptModeSections pins the agent-mode section contract: one ordered
// section per DSH preset exists in the registry, only the selected preset's
// policy is rendered (after collaboration policy and before file references),
// aliases select the same policy, EN/ZH both render, and the Minimal/Creator
// dynamics describe the tools actually visible.
func TestPromptModeSections(t *testing.T) {
	type modeCase struct {
		id      string
		order   int
		marker  string
		zh      string
		aliases []string
	}
	cases := []modeCase{
		{
			id:      "mode:minimal",
			order:   OrderModeMinimal,
			marker:  "Minimal mode is active",
			zh:      "极简模式已启用",
			aliases: []string{"minimal", "minimal-mode", "minimal_mode"},
		},
		{
			id:      "mode:creator",
			order:   OrderModeCreator,
			marker:  "Creator mode is active",
			zh:      "创造模式已启用",
			aliases: []string{"creator", "creator-mode", "creator_mode", "cordis"},
		},
	}

	orders := make(map[string]int)
	for _, section := range Sections() {
		orders[section.ID] = section.Order
	}
	for _, tc := range cases {
		order, ok := orders[tc.id]
		if !ok {
			t.Fatalf("prompt registry is missing section %q", tc.id)
		}
		if order != tc.order {
			t.Fatalf("section %q order = %d, want %d", tc.id, order, tc.order)
		}
	}
	if !(orders["mode:minimal"] < orders["mode:creator"] &&
		orders["mode:creator"] < OrderFileReference) {
		t.Fatalf("mode section order = minimal:%d creator:%d, want ascending before file reference (%d)",
			orders["mode:minimal"], orders["mode:creator"], OrderFileReference)
	}

	// Standard adds no preset policy.
	standard := richContext()
	standard.Mode = ""
	standard.AgentMode = "standard"
	standardOut := Compose(standard)
	for _, tc := range cases {
		if strings.Contains(standardOut, tc.marker) {
			t.Fatalf("standard prompt unexpectedly contains %q:\n%s", tc.marker, standardOut)
		}
	}

	for _, tc := range cases {
		for _, alias := range tc.aliases {
			alias := alias
			t.Run(tc.id+"/"+alias, func(t *testing.T) {
				ctx := richContext()
				ctx.Mode = ""
				ctx.AgentMode = alias
				out := Compose(ctx)
				if !strings.Contains(out, tc.marker) {
					t.Fatalf("AgentMode %q prompt is missing %q:\n%s", alias, tc.marker, out)
				}
				for _, other := range cases {
					if other.id != tc.id && strings.Contains(out, other.marker) {
						t.Fatalf("AgentMode %q prompt contains another preset's policy %q", alias, other.marker)
					}
				}
				if strings.Contains(out, "Plan mode is active") || strings.Contains(out, "Agent Teams is active") {
					t.Fatalf("AgentMode %q preset leaked into collaboration policy:\n%s", alias, out)
				}
				markerAt := strings.Index(out, tc.marker)
				if markerAt < 0 || markerAt > strings.Index(out, "Tokens prefixed with @") {
					t.Fatalf("AgentMode %q policy is not before the file-reference section:\n%s", alias, out)
				}
				zh := ctx
				zh.Language = "zh"
				if !strings.Contains(Compose(zh), tc.zh) {
					t.Fatalf("AgentMode %q ZH prompt is missing %q", alias, tc.zh)
				}
			})
		}
	}

	// A collaboration mode and an agent preset compose together in order.
	planCreator := richContext()
	planCreator.Mode = "plan"
	planCreator.AgentMode = "creator"
	combined := Compose(planCreator)
	planAt := strings.Index(combined, "Plan mode is active")
	creatorAt := strings.Index(combined, "Creator mode is active")
	if planAt < 0 || creatorAt < 0 || planAt > creatorAt {
		t.Fatalf("plan -> creator order is wrong: plan=%d creator=%d\n%s", planAt, creatorAt, combined)
	}

	// Minimal's dynamic policy follows the terminal group being visible or not.
	minimalWithTerminal := richContext()
	minimalWithTerminal.Mode = ""
	minimalWithTerminal.AgentMode = "minimal"
	minimalWithTerminal.Tools = []string{"terminal_open", "terminal_read", "read_image", "working_directory"}
	if out := Compose(minimalWithTerminal); !strings.Contains(out, "Only the terminal_* tools, read_image and working_directory are available") {
		t.Fatalf("Minimal policy does not describe the terminal tool set:\n%s", out)
	}
	minimalNoTerminal := minimalWithTerminal
	minimalNoTerminal.Tools = []string{"read_image", "working_directory"}
	if out := Compose(minimalNoTerminal); !strings.Contains(out, "Only read_image and working_directory are available") {
		t.Fatalf("Minimal policy does not describe a terminal-less build:\n%s", out)
	}

	// Creator's dynamic policy follows the write tools being visible or not.
	creatorWritable := richContext()
	creatorWritable.Mode = ""
	creatorWritable.AgentMode = "creator"
	creatorWritable.Tools = []string{"write_file", "edit_file", "settings_get", "settings_set", "read_file"}
	writableOut := Compose(creatorWritable)
	if !strings.Contains(writableOut, "write reusable skills") || !strings.Contains(writableOut, "settings_get and settings_set") {
		t.Fatalf("Creator policy does not describe settings and skill authoring:\n%s", writableOut)
	}
	creatorReadOnly := creatorWritable
	creatorReadOnly.Tools = []string{"settings_get", "settings_set", "read_file"}
	readOnlyOut := Compose(creatorReadOnly)
	if !strings.Contains(readOnlyOut, "skill writing is unavailable under the current policy") {
		t.Fatalf("Creator policy does not describe a read-only build:\n%s", readOnlyOut)
	}
	if strings.Contains(readOnlyOut, "with the standard file tools") {
		t.Fatalf("Creator read-only policy advertises write tools it does not have:\n%s", readOnlyOut)
	}
}
