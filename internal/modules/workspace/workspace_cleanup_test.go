package workspace

// Tests for the settings cleanup that runs when a workspace registration is
// deleted: scoped product keys and connector rows go, other scopes stay, and the
// workspace folder is untouched.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// cleanupSettings is an in-memory contracts.SettingsService + SettingsDeleter.
type cleanupSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *cleanupSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *cleanupSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *cleanupSettings) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

func (s *cleanupSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func TestDeletePurgesWorkspaceScopedSettingsOnly(t *testing.T) {
	dataDir := t.TempDir()
	service, err := newService(dataDir, Config{}, &testLogger{})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	root := t.TempDir()
	workspace, err := service.Create(context.Background(), "repo", root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	marker := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	notionLegacyToken := contracts.MCPAuthSettingKey("notion")
	rows := []contracts.MCPServerConfig{
		{Name: "notion", WorkspaceID: workspace.ID, URL: "https://notion.test/mcp"},
		{Name: "other", WorkspaceID: "ws-other", URL: "https://other.test/mcp"},
		{Name: "legacy", URL: "https://legacy.test/mcp"},
	}
	encoded, err := contracts.EncodeMCPServersJSON(rows)
	if err != nil {
		t.Fatalf("encode rows: %v", err)
	}
	settings := &cleanupSettings{values: map[string]string{
		contracts.SettingMCPServers:                          encoded,
		contracts.SkillsDirsSettingKey(workspace.ID):         "/tmp/scoped-a",
		contracts.SkillsDirsSettingKey("ws-other"):           "/tmp/scoped-b",
		contracts.MCPDisabledServersSettingKey(workspace.ID): `["notion"]`,
		contracts.MCPDisabledServersSettingKey("ws-other"):   `["other"]`,
		contracts.MCPDisabledToolsSettingKey(workspace.ID):   `["mcp__notion__search"]`,
		contracts.SettingToolsDisabled + "." + workspace.ID:  `["bash"]`,
		"mcp.oauth." + workspace.ID + ".notion":              `{"access_token":"a"}`,
		"mcp.oauth.ws-other.other":                           `{"access_token":"b"}`,
		notionLegacyToken:                                    `{"access_token":"legacy"}`,
	}}
	service.settings = settings

	if err := service.Delete(context.Background(), workspace.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Scoped keys of the deleted workspace are gone (not just empty).
	for _, key := range []string{
		contracts.SkillsDirsSettingKey(workspace.ID),
		contracts.MCPDisabledServersSettingKey(workspace.ID),
		contracts.MCPDisabledToolsSettingKey(workspace.ID),
		contracts.SettingToolsDisabled + "." + workspace.ID,
		"mcp.oauth." + workspace.ID + ".notion",
		notionLegacyToken,
	} {
		if _, ok := settings.All()[key]; ok {
			t.Fatalf("scoped setting %q survived workspace deletion: %v", key, settings.All())
		}
	}
	// Other workspaces and legacy-global data stay.
	for _, key := range []string{
		contracts.SkillsDirsSettingKey("ws-other"),
		contracts.MCPDisabledServersSettingKey("ws-other"),
		"mcp.oauth.ws-other.other",
	} {
		if settings.Get(key, "") == "" {
			t.Fatalf("unrelated setting %q was removed", key)
		}
	}
	remainingRows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		t.Fatalf("parse remaining rows: %v", err)
	}
	if len(remainingRows) != 2 || remainingRows[0].Name != "other" || remainingRows[1].Name != "legacy" {
		t.Fatalf("remaining connector rows = %+v, want other + legacy", remainingRows)
	}
	// The workspace folder and its files are not touched.
	if body, err := os.ReadFile(marker); err != nil || string(body) != "keep me" {
		t.Fatalf("workspace file after delete = %q/%v, want untouched", body, err)
	}
}
