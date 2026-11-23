package assembly_test

// The gateway product's entry tree (docs/TRANSPORT.md §2.6).
//
// These assertions are the regression guards for the two headless settings that
// silently break a browser client if they leak into serve:
//
//   - approvals pinned to "auto" ⇒ Approvals.Request returns allow immediately,
//     so the browser never sees an approval request;
//   - host-tools "interactive: false" ⇒ the host-tools module clears its Asker,
//     so ask_user is unavailable even though the gateway provides
//     contracts.KeyUserInteraction.

import (
	"testing"

	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// findEntry returns one entry by module ID.
func findEntry(t *testing.T, entries []module.Entry, moduleID string) module.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Module == moduleID {
			return entry
		}
	}
	t.Fatalf("entry for module %q is missing", moduleID)
	return module.Entry{}
}

// entryConfig returns an entry's config as a map.
func entryConfig(t *testing.T, entry module.Entry) map[string]any {
	t.Helper()
	config, ok := entry.Config.(map[string]any)
	if !ok {
		t.Fatalf("entry %q config is %T, want an object", entry.Module, entry.Config)
	}
	return config
}

// TestServeEntriesUnpinApprovals is the non-negotiable one: the gateway must
// run the same approval policy as the desktop product.
func TestServeEntriesUnpinApprovals(t *testing.T) {
	entries, err := assembly.ServeEntries()
	if err != nil {
		t.Fatalf("ServeEntries: %v", err)
	}
	approvals := entryConfig(t, findEntry(t, entries, "zeokeel/approvals"))
	if mode := approvals["mode"]; mode != contracts.PermissionWorkspace {
		t.Fatalf("serve approvals mode = %v, want %q (auto would never show an approval request)", mode, contracts.PermissionWorkspace)
	}
	if _, pinned := approvals["ignorePermissionSetting"]; pinned {
		t.Fatalf("serve approvals still pins ignorePermissionSetting: %v", approvals)
	}

	// The shipped headless product keeps its own policy: serve's override must
	// not rewrite the embedded file.
	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	headlessApprovals := entryConfig(t, findEntry(t, headless, "zeokeel/approvals"))
	if mode := headlessApprovals["mode"]; mode != "auto" {
		t.Fatalf("headless approvals mode = %v, want auto (serve must not mutate the file)", mode)
	}
	if ignore, ok := headlessApprovals["ignorePermissionSetting"]; !ok || ignore != true {
		t.Fatalf("headless approvals lost ignorePermissionSetting: %v", headlessApprovals)
	}
}

// TestServeEntriesEnableInteractiveTools covers the second headless leak: the
// ask_user tool needs the interactive switch even though the gateway provides
// the interaction service.
func TestServeEntriesEnableInteractiveTools(t *testing.T) {
	entries, err := assembly.ServeEntries()
	if err != nil {
		t.Fatalf("ServeEntries: %v", err)
	}
	hosttools := entryConfig(t, findEntry(t, entries, "zeokeel/hosttools"))
	if hosttools["interactive"] != true {
		t.Fatalf("serve hosttools interactive = %v, want true", hosttools["interactive"])
	}
	// Every other headless config is carried over untouched, so serve runs the
	// same backend the desktop and headless products run.
	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	serveIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		serveIDs = append(serveIDs, entry.ID+"/"+entry.Module)
	}
	headlessIDs := make([]string, 0, len(headless))
	for _, entry := range headless {
		headlessIDs = append(headlessIDs, entry.ID+"/"+entry.Module)
	}
	if len(serveIDs) != len(headlessIDs) {
		t.Fatalf("serve has %d entries, headless has %d", len(serveIDs), len(headlessIDs))
	}
	for i := range serveIDs {
		if serveIDs[i] != headlessIDs[i] {
			t.Fatalf("entry %d: serve %q != headless %q", i, serveIDs[i], headlessIDs[i])
		}
	}
}

// TestServeEntriesPassPreflight proves the overridden tree still validates:
// the config keys serve writes are the ones the modules declare.
func TestServeEntriesPassPreflight(t *testing.T) {
	entries, err := assembly.ServeEntries()
	if err != nil {
		t.Fatalf("ServeEntries: %v", err)
	}
	if err := assembly.ValidateEntries(entries); err != nil {
		t.Fatalf("serve entries failed pre-flight: %v", err)
	}
}
