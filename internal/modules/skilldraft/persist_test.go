package skilldraft

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// readDir names the entries of dir, sorted, so a test failure lists files
// deterministically.
func readDir(t *testing.T, dir string) ([]string, error) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// TestMissingStoreIsAFreshStart: no file is not an error.
func TestMissingStoreIsAFreshStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skilldraft")
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open on a missing store: %v", err)
	}
	listed, err := service.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("List = %v, want an empty store", listed)
	}
}

// TestInMemoryStoreSkipsDisk: an empty dir is the in-memory constructor, which
// must not invent a directory.
func TestInMemoryStoreSkipsDisk(t *testing.T) {
	service, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	draft(t, service, "memory-only")
	listed, err := service.List(context.Background(), 0)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = (%v, %v), want one in-memory draft", listed, err)
	}
}

// TestCorruptStoreIsTypedAndEmpty is the contract with the module: a corrupt file
// is reported, and the caller still gets a usable store so the product boots.
func TestCorruptStoreIsTypedAndEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, storeName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt store: %v", err)
	}

	service, err := Open(dir)
	if !errors.Is(err, contracts.ErrSkillDraftStoreCorrupt) {
		t.Fatalf("Open err = %v, want ErrSkillDraftStoreCorrupt", err)
	}
	if service == nil {
		t.Fatal("Open returned no service for a corrupt store; the product cannot boot")
	}
	listed, listErr := service.List(context.Background(), 0)
	if listErr != nil {
		t.Fatalf("List on corrupt open: %v", listErr)
	}
	if len(listed) != 0 {
		t.Fatalf("List on corrupt open = %v, want an empty store", listed)
	}

	// The empty store heals the file: the first new draft replaces it.
	created := draft(t, service, "fresh-start")
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open after heal: %v", err)
	}
	listed, err = reopened.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List after heal: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].Name != "fresh-start" {
		t.Fatalf("List after heal = %+v, want the new draft", listed)
	}
}

// TestUnreadableStoreIsAPlainError: a permission failure is not "corrupt" — it is
// a real error the caller must report, and there is no service to hand back.
func TestUnreadableStoreIsAPlainError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a 0000 file is still readable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, storeName)
	if err := os.WriteFile(path, []byte(`{"version":1,"drafts":[]}`), 0o000); err != nil {
		t.Fatalf("seed unreadable store: %v", err)
	}
	service, err := Open(dir)
	if err == nil {
		t.Fatal("Open on an unreadable store returned no error")
	}
	if errors.Is(err, contracts.ErrSkillDraftStoreCorrupt) {
		t.Fatalf("Open err = %v, want a plain read error, not corrupt", err)
	}
	if service != nil {
		t.Fatalf("Open returned a service (%+v) alongside a read error", service)
	}
}

// TestStorePathThatIsNotAFileIsAPlainError: the read failure path returns
// (nil, err) — a directory in the store's place is broken, not corrupt, and the
// caller must fail the boot rather than pretend the store is empty. This variant
// works when the tests run as root (where a chmod 0000 file is still readable).
func TestStorePathThatIsNotAFileIsAPlainError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, storeName), 0o700); err != nil {
		t.Fatalf("seed directory in the store's place: %v", err)
	}
	service, err := Open(dir)
	if err == nil {
		t.Fatal("Open on a directory in the store's place returned no error")
	}
	if errors.Is(err, contracts.ErrSkillDraftStoreCorrupt) {
		t.Fatalf("Open err = %v, want a plain read error, not corrupt", err)
	}
	if service != nil {
		t.Fatalf("Open returned a service (%+v) alongside a read error", service)
	}
}

// TestStoreFromANewerBuildIsCorrupt: a document this build cannot know is not
// guessed at.
func TestStoreFromANewerBuildIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, storeName)
	if err := os.WriteFile(path, []byte(`{"version":99,"drafts":[]}`), 0o600); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	service, err := Open(dir)
	if !errors.Is(err, contracts.ErrSkillDraftStoreCorrupt) {
		t.Fatalf("Open err = %v, want ErrSkillDraftStoreCorrupt", err)
	}
	if service == nil {
		t.Fatal("Open returned no service for a newer store")
	}
}

// TestStoreLoadDropsUnusableRows: one hand-edited row must not hide the rest.
func TestStoreLoadDropsUnusableRows(t *testing.T) {
	dir := t.TempDir()
	seeded := `{"version":1,"drafts":[
	  {"ID":"keep","TaskID":"task-1","Name":"keep-me","Summary":"s","Body":"## When to use\n\nx","Status":"pending"},
	  {"ID":"","Name":"no-id","Status":"pending"},
	  {"ID":"no-name","Name":"","Status":"pending"},
	  {"ID":"odd-status","Name":"odd","Status":"maybe"},
	  {"ID":"accepted","Name":"already","Summary":"s","Body":"## Steps\n\nx","Status":"accepted","Reviewer":"ann"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, storeName), []byte(seeded), 0o600); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	listed, err := service.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, draft := range listed {
		got[draft.ID] = draft.Status
	}
	want := map[string]string{"keep": contracts.SkillDraftPending, "accepted": contracts.SkillDraftAccepted}
	if len(got) != len(want) {
		t.Fatalf("List = %v, want only the usable rows %v", got, want)
	}
	for id, status := range want {
		if got[id] != status {
			t.Fatalf("row %q status = %q, want %q", id, got[id], status)
		}
	}
	// A dropped row took its decision with it, so acceptance still works.
	if _, err := service.Decide(context.Background(), "keep", true, "ann"); err != nil {
		t.Fatalf("Decide after lenient load: %v", err)
	}
}

// TestStoreIsPrivateAtomicAndVersioned pins the on-disk contract: 0600 file,
// 0700 directory, version 1, no temp file left behind, and a JSON document a
// human can read.
func TestStoreIsPrivateAtomicAndVersioned(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "skilldraft")
	service := openStore(t, dir, newClock())
	created := draft(t, service, "on-disk")

	path := filepath.Join(dir, storeName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("draft store mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(%s): %v", dir, err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("draft dir mode = %o, want 700", perm)
	}
	names, err := readDir(t, dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(names) != 1 || names[0] != storeName {
		t.Fatalf("draft dir holds %v, want only %s (no temp file)", names, storeName)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var document struct {
		Version int                        `json:"version"`
		Drafts  []contracts.TaskSkillDraft `json:"drafts"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("stored document is not JSON: %v", err)
	}
	if document.Version != storeVersion {
		t.Fatalf("stored version = %d, want %d", document.Version, storeVersion)
	}
	if len(document.Drafts) != 1 || document.Drafts[0].ID != created.ID {
		t.Fatalf("stored drafts = %+v, want the one draft", document.Drafts)
	}
	if document.Drafts[0].Body != created.Body {
		t.Fatalf("stored body differs from the returned body")
	}
}

// TestDecideRewritesTheDocument: a decision is durable, not just in memory.
func TestDecideRewritesTheDocument(t *testing.T) {
	dir := t.TempDir()
	service := openStore(t, dir, newClock())
	created := draft(t, service, "durable-decision")
	if _, err := service.Decide(context.Background(), created.ID, true, "ann"); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, storeName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var document struct {
		Drafts []contracts.TaskSkillDraft `json:"drafts"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode store: %v", err)
	}
	if len(document.Drafts) != 1 {
		t.Fatalf("stored drafts = %d, want 1", len(document.Drafts))
	}
	if document.Drafts[0].Status != contracts.SkillDraftAccepted || document.Drafts[0].Reviewer != "ann" {
		t.Fatalf("stored draft = %+v, want accepted by ann", document.Drafts[0])
	}
}
