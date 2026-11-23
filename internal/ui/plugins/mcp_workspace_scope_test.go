package plugins_test

// Frame test for workspace-scoped connectors: the connectors page shows only
// the current workspace's rows, migrates legacy-global rows to it, and the add
// form writes the current WorkspaceID without disturbing another workspace.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestConnectorFormScopesToCurrentWorkspace(t *testing.T) {
	h, store := newConnectorHarness(t, &connectorPlugins{}, map[string]string{
		// One legacy-global row (no WorkspaceID) and one row owned by ws-2.
		contracts.SettingMCPServers: `[{"name":"legacy","url":"http://127.0.0.1:9/legacy"},` +
			`{"name":"other","url":"http://127.0.0.1:9/other","workspaceId":"ws-2"}]`,
	})
	provide[contracts.WorkspaceService](t, h.ctx, contracts.KeyWorkspace, &connectorWorkspace{root: t.TempDir()})
	tester := h.tester()
	h.click(tester, "连接器")

	h.assertTexts(tester, "legacy", "旧连接器已归入当前工作文件夹")
	h.assertNoTexts(tester, "other")

	rows := storedConnectorRows(t, store)
	if len(rows) != 2 {
		t.Fatalf("stored rows = %+v, want both scopes preserved", rows)
	}
	for _, row := range rows {
		switch row.Name {
		case "legacy":
			if row.WorkspaceID != "ws" {
				t.Fatalf("legacy row WorkspaceID = %q, want the current workspace ws", row.WorkspaceID)
			}
		case "other":
			if row.WorkspaceID != "ws-2" {
				t.Fatalf("other workspace row was rewritten: %+v", row)
			}
		}
	}

	// Adding a connector writes the current workspace ID and keeps ws-2's row.
	h.click(tester, "添加连接器")
	fillConnectorInput(t, h, tester, "名称 输入", "scoped")
	fillConnectorInput(t, h, tester, "URL 输入", "https://mcp.example.com/scoped")
	h.click(tester, "添加")

	rows = storedConnectorRows(t, store)
	if len(rows) != 3 {
		t.Fatalf("stored rows after add = %+v, want three", rows)
	}
	seen := map[string]string{}
	for _, row := range rows {
		seen[row.Name] = row.WorkspaceID
	}
	if seen["scoped"] != "ws" || seen["legacy"] != "ws" || seen["other"] != "ws-2" {
		t.Fatalf("stored workspace scopes = %v", seen)
	}
}
