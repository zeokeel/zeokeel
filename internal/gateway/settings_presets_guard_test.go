package gateway

// The presets.agents write guard (2026-11-26 audit, 高).
//
// `presets.agents` is the settings row the agent reads its roles from, and it was
// stored verbatim: a hand-edited or truncated JSON value (or a row array whose
// rows have no id/name) went in without complaint, the agent's read side quietly
// degraded to "no user presets", and a session that had selected one of those
// roles ended up with no tools at all — every call refused, nothing on screen
// saying why. The write is where the value is still attributable to somebody, so
// the write is where it must be refused: an invalid row never reaches the store.
//
// This file covers the refusal; `TestSettingsSetAcceptsValidAgentPresets` keeps
// the honest half (a good row is still stored, verbatim).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSettingsSetRejectsMalformedAgentPresets(t *testing.T) {
	chat := newFakeChat()
	served, settings := startWriteGateway(t, chat, 0)
	rig := &domainTestRig{runningGateway: served, chat: chat, settings: settings}
	defer rig.stop(t)
	conn := rig.connect(t)

	cases := []struct {
		name  string
		value string
	}{
		{"truncated JSON", `[{"id":"p1","name":"P1"`},
		{"not JSON at all", `presets: p1`},
		{"an object instead of an array", `{"id":"p1","name":"P1"}`},
		{"a row without an id", `[{"name":"No id"}]`},
		{"a row without a name", `[{"id":"p1"}]`},
		{"a blank id", `[{"id":"   ","name":"Blank"}]`},
		{"two rows with the same id", `[{"id":"p1","name":"A"},{"id":"p1","name":"B"}]`},
		{"a blank tool name", `[{"id":"p1","name":"P1","tools":["read_file","  "]}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			protocolErr := errorOf(t, conn.call("settings.set", map[string]any{
				"key": contracts.SettingAgentPresets, "value": tc.value,
			}))
			if protocolErr["code"] != float64(codeInvalidParams) {
				t.Fatalf("value %q = %v, want invalid params", tc.value, protocolErr)
			}
			message, _ := protocolErr["message"].(string)
			if message == "" {
				t.Fatalf("value %q was refused without a message: %v", tc.value, protocolErr)
			}
			// The refusal is not cosmetic: nothing was stored, so the bad row
			// cannot become "no roles" on the next read either.
			if stored := settings.Get(contracts.SettingAgentPresets, ""); stored != "" {
				t.Fatalf("a refused write reached the store: %q", stored)
			}
		})
	}
}

// TestSettingsSetAcceptsValidAgentPresets is the other half: the guard rejects
// malformed rows only — a legitimate row (built-in overrides, extra roles,
// explicit empty tool lists) is still stored verbatim.
func TestSettingsSetAcceptsValidAgentPresets(t *testing.T) {
	chat := newFakeChat()
	served, settings := startWriteGateway(t, chat, 0)
	rig := &domainTestRig{runningGateway: served, chat: chat, settings: settings}
	defer rig.stop(t)
	conn := rig.connect(t)

	value := `[{"id":"auditor","name":"Auditor","tools":["read_file","grep"]},{"id":"blank","name":"No tools","tools":[]}]`
	resultOf(t, conn.call("settings.set", map[string]any{
		"key": contracts.SettingAgentPresets, "value": value,
	}))
	if stored := settings.Get(contracts.SettingAgentPresets, ""); stored != value {
		t.Fatalf("stored row = %q, want it verbatim: %q", stored, value)
	}
}
