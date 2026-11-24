package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The capabilities screen (R23) fetches /api/capabilities; the gateway answered 404
// "unknown method", so the screen rendered its error state. The data is already
// available from two services (skills + MCP plugins), so this pins the shaping.

type fakeSkills struct{ skills []rig.Skill }

func (f *fakeSkills) List(context.Context) ([]rig.Skill, error) { return f.skills, nil }
func (f *fakeSkills) Read(context.Context, string) (rig.Skill, error) {
	return rig.Skill{}, nil
}
func (f *fakeSkills) Roots() []string { return []string{"/skills"} }

type fakePlugins struct{ plugins []contracts.MCPPlugin }

func (f *fakePlugins) MCP() []contracts.MCPPlugin { return f.plugins }
func (f *fakePlugins) Refresh(context.Context) []contracts.MCPPlugin {
	return f.plugins
}

func newCapabilityGateway(t *testing.T) *runningGateway {
	t.Helper()
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	root := testRoot(t, chat)
	skills := &fakeSkills{skills: []rig.Skill{
		{Name: "antd", Description: "Ant Design v6 helpers", Path: "/skills/antd/SKILL.md"},
		{Name: "mygo-maintenance", Description: "mygo rules", Path: "/skills/mygo/SKILL.md"},
	}}
	if _, err := root.Provide(contracts.KeySkills, contracts.SkillsService(skills)); err != nil {
		t.Fatalf("provide skills: %v", err)
	}
	plugins := &fakePlugins{plugins: []contracts.MCPPlugin{
		{Name: "zk-test-mcp", Tools: 6, Status: "已连接", Protocol: "2024-11-05"},
		{Name: "remote", URL: "https://mcp.example/mcp", Tools: 0, Status: "连接失败"},
	}}
	if _, err := root.Provide(contracts.KeyPlugins, contracts.PluginsService(plugins)); err != nil {
		t.Fatalf("provide plugins: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway)
}

func TestCapabilitiesServesSkillsAndConnectors(t *testing.T) {
	gw := newCapabilityGateway(t)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/capabilities", true)
	if status != http.StatusOK {
		t.Fatalf("GET /api/capabilities = %d %q", status, body)
	}
	var payload capabilitiesDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if len(payload.Skills) != 2 {
		t.Fatalf("skills = %+v", payload.Skills)
	}
	if payload.Skills[0].Name != "antd" || payload.Skills[0].Source != "/skills/antd" {
		t.Fatalf("skill row = %+v", payload.Skills[0])
	}
	if len(payload.Connectors) != 2 {
		t.Fatalf("connectors = %+v", payload.Connectors)
	}
	// A stdio server has no URL; the transport column must say so instead of
	// leaving an empty cell.
	if payload.Connectors[0].Transport != "stdio" || payload.Connectors[0].Tools != 6 {
		t.Fatalf("stdio connector = %+v", payload.Connectors[0])
	}
	if payload.Connectors[1].Transport != "http" || payload.Connectors[1].Status == "" {
		t.Fatalf("http connector = %+v", payload.Connectors[1])
	}
}

// A build without those services still answers with empty lists: an empty screen is
// honest, a 404 is what made this dead.
func TestCapabilitiesWithoutServicesIsEmptyNotBroken(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/capabilities", true)
	if status != http.StatusOK {
		t.Fatalf("GET /api/capabilities = %d %q", status, body)
	}
	var payload capabilitiesDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if payload.Skills == nil || payload.Connectors == nil {
		t.Fatalf("empty payload must carry empty arrays, not null: %q", body)
	}
	if len(payload.Skills) != 0 || len(payload.Connectors) != 0 {
		t.Fatalf("unexpected rows: %+v", payload)
	}
}

func TestCapabilitiesNeedsTheToken(t *testing.T) {
	gw := newCapabilityGateway(t)
	defer gw.stop(t)
	if status, _ := filesGet(t, gw, "/api/capabilities", false); status != http.StatusUnauthorized {
		t.Fatalf("without a token = %d, want 401", status)
	}
}
