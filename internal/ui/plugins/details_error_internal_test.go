package plugins

// Internal tests for the connector details modal's error chain cleanup and
// status semantic color (2026-11-17 UI review P3): the raw backend chain keeps
// its technical detail but loses the repeated "mcp:" wrappers, and the status
// value uses the same semantic text color as the card badge.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestCleanMCPErrorChainStripsRepeatedWrappers(t *testing.T) {
	raw := "连接失败: mcp: initialize: mcp: http request: Post \"http://127.0.0.1:9/mcp\": dial tcp 127.0.0.1:9: connect: connection refused"
	got := cleanMCPErrorChain(raw)
	want := "连接失败: initialize: http request: Post \"http://127.0.0.1:9/mcp\": dial tcp 127.0.0.1:9: connect: connection refused"
	if got != want {
		t.Fatalf("cleanMCPErrorChain =\n%q\nwant\n%q", got, want)
	}
	if cleanMCPErrorChain("  ") != "" {
		t.Fatal("blank raw status must clean to empty (no disclosure)")
	}
	// A chain without the wrapper is untouched apart from whitespace.
	if got := cleanMCPErrorChain("dial tcp: connection refused"); got != "dial tcp: connection refused" {
		t.Fatalf("plain chain changed: %q", got)
	}
}

func TestConnectorStatusTextColorMatchesBadgeSemantic(t *testing.T) {
	for _, theme := range []*design.Tokens{design.Light(), design.Dark()} {
		if got := connectorStatusTextColor(theme, uikit.StatusError); got != theme.DangerText {
			t.Fatalf("error status color = %+v, want DangerText %+v", got, theme.DangerText)
		}
		if got := connectorStatusTextColor(theme, uikit.StatusNeedsAction); got != theme.WarningText {
			t.Fatalf("needs-action status color = %+v, want WarningText %+v", got, theme.WarningText)
		}
		if got := connectorStatusTextColor(theme, uikit.StatusSuccess); got != theme.SuccessText {
			t.Fatalf("success status color = %+v, want SuccessText %+v", got, theme.SuccessText)
		}
		if got := connectorStatusTextColor(theme, uikit.StatusIdle); got != theme.LabelPrimary {
			t.Fatalf("idle status color = %+v, want LabelPrimary %+v", got, theme.LabelPrimary)
		}
	}
}

// TestConnectorAuthStatusDisplaySemantic pins the authorization row's value
// and semantic color in both themes: authorized success, needed/expired
// needs-action, unknown quiet (DESIGN.md §5.2 status matrix).
func TestConnectorAuthStatusDisplaySemantic(t *testing.T) {
	st := &state{} // nil locale: uikit.T falls back to the Chinese copy
	for _, theme := range []*design.Tokens{design.Light(), design.Dark()} {
		if value, color := connectorAuthStatusDisplay(st, theme, contracts.MCPAuthStateAuthorized); value != "已授权" || color != theme.SuccessText {
			t.Fatalf("authorized = %q/%+v, want 已授权/SuccessText", value, color)
		}
		if value, color := connectorAuthStatusDisplay(st, theme, contracts.MCPAuthStateRequired); value != "需要授权" || color != theme.WarningText {
			t.Fatalf("required = %q/%+v, want 需要授权/WarningText", value, color)
		}
		if value, color := connectorAuthStatusDisplay(st, theme, contracts.MCPAuthStateExpired); value != "已失效" || color != theme.WarningText {
			t.Fatalf("expired = %q/%+v, want 已失效/WarningText", value, color)
		}
		if value, color := connectorAuthStatusDisplay(st, theme, contracts.MCPAuthState("")); value != "未知" || color != theme.LabelTertiary {
			t.Fatalf("unknown = %q/%+v, want 未知/LabelTertiary", value, color)
		}
	}
}
