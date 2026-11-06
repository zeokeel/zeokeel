package lsp

import (
	"context"
	"path/filepath"
	"testing"
)

// TestStdioProviderHonorsConfiguredRootURI pins ServerConfig.RootURI: the
// configured override must be sent as initialize.rootUri (and used for the
// workspace folder), while a server without it keeps defaulting to the
// workspace root.
func TestStdioProviderHonorsConfiguredRootURI(t *testing.T) {
	overrideRoot := t.TempDir()
	server := fakeServerConfig(t, "ok")
	server.RootURI = PathToURI(overrideRoot)
	root := t.TempDir()
	provider := NewStdioProvider(ProviderConfig{
		Servers: map[string]ServerConfig{"go": server},
		Root:    root,
	})
	result, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Locations) != 2 {
		t.Fatalf("locations = %+v, want 2", result.Locations)
	}
	// The fake server derives its sibling location from the rootUri it received
	// in initialize, so this asserts the handshake used the configured override.
	want := PathToURI(filepath.Join(overrideRoot, "sibling.go"))
	if got := result.Locations[1].URI; got != want {
		t.Fatalf("initialize rootUri override not applied: sibling location = %q, want %q (the configured RootURI root)", got, want)
	}
}

// TestStdioProviderDefaultRootURIIsWorkspaceRoot guards the fallback: without a
// configured RootURI the workspace root remains the initialize rootUri.
func TestStdioProviderDefaultRootURIIsWorkspaceRoot(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := PathToURI(filepath.Join(root, "sibling.go"))
	if got := result.Locations[1].URI; got != want {
		t.Fatalf("default rootUri: sibling location = %q, want the workspace root %q", got, want)
	}
}
