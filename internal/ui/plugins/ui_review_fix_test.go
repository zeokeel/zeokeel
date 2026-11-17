package plugins_test

// Frame tests for the 2026-11-17 UI review proposal (capabilities scope):
//
//   - the stdio/LSP trust disclosures paint in the warning semantic (a static
//     caution), keeping the screen's red budget for real failures;
//   - the connector card's enable switch keeps the card's bottom padding (the
//     failed card's content used to overflow and press it onto the border);
//   - 诊断与设置 is one section with named subgroups (no orphan heading), and
//     the connectors tab leads with its intro line like the skills tab.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// reviewColorNear reports whether two colors are within tolerance per channel.
func reviewColorNear(got, want ui.Color, tolerance int) bool {
	close := func(a, b uint8) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= tolerance
	}
	return close(got.R, want.R) && close(got.G, want.G) && close(got.B, want.B)
}

// assertWarningInk asserts the text showing label paints in the warning
// semantic and never in danger red (anti-aliased edges blend toward the
// background; core glyph pixels carry the full token color).
func assertWarningInk(t *testing.T, tester *ui.Tester, label string, tokens *design.Tokens) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("%q not found; texts = %v", label, tester.Texts())
	}
	img := tester.Image()
	found := false
	for y := int(rect.Y); y < int(rect.Y+rect.H); y++ {
		for x := int(rect.X); x < int(rect.X+rect.W); x++ {
			c := img.RGBAAt(x, y)
			px := ui.Color{R: c.R, G: c.G, B: c.B, A: c.A}
			if reviewColorNear(px, tokens.DangerText, 3) {
				t.Fatalf("%q paints danger red at (%d,%d); a static caution is warning semantic",
					label, x, y)
			}
			if reviewColorNear(px, tokens.WarningText, 3) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("%q shows no warning ink %+v; texts = %v", label, tokens.WarningText, tester.Texts())
	}
}

// --- warning semantic for the trust disclosures ------------------------------

// TestStdioFormDisclosureIsWarning covers the STDIO form's trust line in both
// themes: warning ink, never the danger red that pushed the form over the
// screen's red budget.
func TestStdioFormDisclosureIsWarning(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "添加连接器")
	chooseConnectorTransport(t, h, tester, "STDIO")
	h.assertTexts(tester, stdioTrustZh)
	assertWarningInk(t, tester, stdioTrustZh+"。", design.Light())

	h.uiRoot.Theme().SetDark(true)
	tester.Frame()
	assertWarningInk(t, tester, stdioTrustZh+"。", design.Dark())
}

// TestWorkspaceOptInDisclosureIsWarning covers the connector-options disclosure
// next to the workspace mcp.json switch.
func TestWorkspaceOptInDisclosureIsWarning(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.click(tester, "连接器选项")
	h.assertTexts(tester, stdioTrustZh)
	assertWarningInk(t, tester, stdioTrustZh+"。", design.Light())
}

// --- connector card switch padding -------------------------------------------

// TestConnectorCardSwitchKeepsBottomPadding pins the failed card's geometry:
// below the enable switch there is the card's normal bottom padding before the
// panel border (the old fixed card height let the failed card's content
// overflow, pressing the switch onto the border).
func TestConnectorCardSwitchKeepsBottomPadding(t *testing.T) {
	service := &connectorPlugins{mcp: []contracts.MCPPlugin{
		{Name: "broken", URL: "http://127.0.0.1:9/mcp",
			Status: "连接失败: dial tcp 127.0.0.1:9: connection refused"},
	}}
	h, _ := newConnectorHarness(t, service, nil)
	tester := h.tester()
	h.click(tester, "连接器")
	h.assertTexts(tester, "连接失败", "查看报错详情", "重试")

	sw, ok := tester.AccessByRole(ui.RoleSwitch, "启用连接器 broken")
	if !ok {
		t.Fatalf("enable switch missing; texts = %v", tester.Texts())
	}
	img := tester.Image()
	fill := design.Light().BgRaised
	x := int(sw.Bounds.X + sw.Bounds.W/2)
	gap := 0
	for y := int(sw.Bounds.Y+sw.Bounds.H) + 1; y < int(sw.Bounds.Y+sw.Bounds.H)+40; y++ {
		c := img.RGBAAt(x, y)
		px := ui.Color{R: c.R, G: c.G, B: c.B, A: c.A}
		if !reviewColorNear(px, fill, 3) {
			break // the panel border (or anything but card fill) ends the padding
		}
		gap++
	}
	if float32(gap) < design.Space3-2 {
		t.Fatalf("only %d DIP of card fill below the switch, want >= %v (the card's bottom padding)",
			gap, design.Space3-2)
	}
}

// --- diagnostics group + tab order -------------------------------------------

// TestCapabilitiesDiagnosticsIsOneGroup pins the restructured footer: 诊断与设置
// is a level-2 section heading and the inventories are level-3 subgroups inside
// it (no orphan heading followed by a peer section); without either host
// service the whole group is hidden.
func TestCapabilitiesDiagnosticsIsOneGroup(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, nil)
	tester := h.tester()
	tester.Frame()
	group, ok := tester.AccessByRole(ui.RoleHeading, "诊断与设置")
	if !ok {
		t.Fatalf("diagnostics group heading missing; texts = %v", tester.Texts())
	}
	if group.Level != 2 {
		t.Fatalf("诊断与设置 heading level = %d, want 2", group.Level)
	}
	sub, ok := tester.AccessByRole(ui.RoleHeading, "扩展设置")
	if !ok {
		t.Fatalf("extension settings subgroup missing; texts = %v", tester.Texts())
	}
	if sub.Level != 3 {
		t.Fatalf("扩展设置 heading level = %d, want a subgroup (3) of 诊断与设置", sub.Level)
	}

	// A bare assembly with neither settings nor a module lister hides the
	// group entirely instead of painting an orphan heading.
	bare := newHarness(t, nil, nil, plugins.Config{})
	bareTester := bare.tester()
	bareTester.Frame()
	if bareTester.HasText("诊断与设置") {
		t.Fatalf("orphan diagnostics heading on a bare assembly; texts = %v", bareTester.Texts())
	}
}

// TestConnectorsTabLeadsWithIntro pins the unified tab order: the connectors
// tab's intro line sits above its count/action row, the same order the skills
// tab uses.
func TestConnectorsTabLeadsWithIntro(t *testing.T) {
	h, _ := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		contracts.SettingMCPServers: connectorFilesJSON,
	})
	tester := h.tester()
	h.click(tester, "连接器")
	intro, ok := tester.Find("连接器把外部 MCP 服务（第三方工具服务，如浏览器、数据库、搜索）接入会话；启用变更在重启后生效。")
	if !ok {
		t.Fatalf("connectors intro missing; texts = %v", tester.Texts())
	}
	count, ok := tester.Find("1 个连接器")
	if !ok {
		t.Fatalf("connector count missing; texts = %v", tester.Texts())
	}
	if intro.Y >= count.Y {
		t.Fatalf("intro line (y=%v) must precede the count row (y=%v), like the skills tab",
			intro.Y, count.Y)
	}
}
