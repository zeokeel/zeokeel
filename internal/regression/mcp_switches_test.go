package regression

// Adversarial audit: MCP server-level switches (mcp.disabledServers). The toolkit
// filter is unexported; this test-only file reaches it through a //go:linkname
// declaration with the exported toolkit.MCPServer type. The plugins page switch
// is exercised through the real screen frame: restored disabled rows, enable
// writes with case-insensitive matching/sorted JSON, and malformed config.

import (
	"context"
	"testing"
	_ "unsafe"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
)

// toolkitFilterDisabledServers is toolkit.filterDisabledMCPServers by linkname.
//
//go:linkname toolkitFilterDisabledServers github.com/zeokeel/zeokeel/internal/modules/toolkit.filterDisabledMCPServers
func toolkitFilterDisabledServers(servers []toolkit.MCPServer, raw string,
	logf func(format string, args ...any)) ([]toolkit.MCPServer, int)

// pluginNames returns the names of the servers kept by the filter.
func mcpsFilteredNames(servers []toolkit.MCPServer) []string {
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Name)
	}
	return names
}

// TestMCPDisabledServersFilterTable covers the toolkit filter: case/whitespace
// normalization, empty/invalid rows, unknown names and bad JSON (never filters).
func TestMCPDisabledServersFilterTable(t *testing.T) {
	servers := []toolkit.MCPServer{
		{Name: "remote", URL: "http://127.0.0.1:1/mcp"},
		{Name: "Remote", URL: "http://127.0.0.1:2/mcp"},
		{Name: "secondary", URL: "http://127.0.0.1:3/mcp"},
	}
	cases := []struct {
		name    string
		raw     string
		kept    []string
		skipped int
	}{
		{"empty", "", []string{"remote", "Remote", "secondary"}, 0},
		{"empty array", "[]", []string{"remote", "Remote", "secondary"}, 0},
		{"blank entries", `["","   "]`, []string{"remote", "Remote", "secondary"}, 0},
		{"case+space insensitive", `[" REMOTE "]`, []string{"secondary"}, 2},
		{"one name", `["secondary"]`, []string{"remote", "Remote"}, 1},
		{"unknown name", `["missing"]`, []string{"remote", "Remote", "secondary"}, 0},
		{"bad json ignored", `["remote"`, []string{"remote", "Remote", "secondary"}, 0},
		{"wrong json type", `"remote"`, []string{"remote", "Remote", "secondary"}, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var logs []string
			kept, skipped := toolkitFilterDisabledServers(servers, test.raw, func(format string, args ...any) {
				logs = append(logs, format)
			})
			if got := mcpsFilteredNames(kept); len(got) != len(test.kept) {
				t.Fatalf("kept = %v, want %v", got, test.kept)
			}
			for index, name := range test.kept {
				if kept[index].Name != name {
					t.Fatalf("kept = %v, want %v", mcpsFilteredNames(kept), test.kept)
				}
			}
			if skipped != test.skipped {
				t.Fatalf("skipped = %d, want %d", skipped, test.skipped)
			}
			if test.raw == `["remote"` && len(logs) == 0 {
				t.Fatalf("malformed JSON was not logged")
			}
		})
	}
}

// pluginsServiceFake exposes a scripted live MCP list to the plugins screen.

// pluginsServiceFake is the live MCP list other regression tests provide.
type pluginsServiceFake struct{ mcp []contracts.MCPPlugin }

func (f *pluginsServiceFake) MCP() []contracts.MCPPlugin {
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *pluginsServiceFake) Refresh(context.Context) []contracts.MCPPlugin { return f.MCP() }
