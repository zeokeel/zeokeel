package creds_test

// Tests for the credential store. They are written against the exported
// surface only (package creds_test), so they pin the seam adapters and the
// assembly actually use: Open, Service methods, and the contracts types.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/creds"
)

func openStore(t *testing.T, dir string) (*creds.Service, error) {
	t.Helper()
	service, err := creds.Open(dir)
	if err != nil {
		return service, err
	}
	if service == nil {
		t.Fatal("Open returned a nil service without an error")
	}
	return service, nil
}

func mustOpen(t *testing.T, dir string) *creds.Service {
	t.Helper()
	service, err := openStore(t, dir)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return service
}

func credential(kind, id string, fields map[string]string) contracts.Credential {
	return contracts.Credential{Kind: kind, ID: id, Fields: fields}
}

func TestOpenOnAFreshDirectoryYieldsAnEmptyStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	service := mustOpen(t, dir)

	ctx := context.Background()
	for _, kind := range []string{"feishu", "wechat"} {
		infos, err := service.List(ctx, kind)
		if err != nil {
			t.Fatalf("List(%q): %v", kind, err)
		}
		if len(infos) != 0 {
			t.Fatalf("List(%q) = %+v, want empty", kind, infos)
		}
	}
	if _, found, err := service.Get(ctx, "feishu", "default"); err != nil || found {
		t.Fatalf("Get on a fresh store = (found %v, err %v), want (false, nil)", found, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("data dir was not created: %v", err)
	}
	if mode := statMode(t, dir); mode.Perm() != 0o700 {
		t.Fatalf("data dir mode = %v, want 0700", mode.Perm())
	}
	// A fresh store writes nothing until the first Set.
	if _, err := os.Stat(filepath.Join(dir, creds.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials file exists after Open: %v", err)
	}
}

func statMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode()
}

func TestRoundTripThroughDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	ctx := context.Background()
	service := mustOpen(t, dir)

	fields := map[string]string{"app_id": "cli_1", "app_secret": "s3cr3t"}
	if err := service.Set(ctx, credential("feishu", "default", fields)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, found, err := service.Get(ctx, "feishu", "default")
	if err != nil || !found {
		t.Fatalf("Get = (found %v, err %v), want (true, nil)", found, err)
	}
	if got.Kind != "feishu" || got.ID != "default" {
		t.Fatalf("Get identity = %+v, want feishu/default", got)
	}
	if !reflect.DeepEqual(got.Fields, fields) {
		t.Fatalf("Get fields = %v, want %v", got.Fields, fields)
	}

	// A restart must see the same credential (the whole point of the store).
	reopened := mustOpen(t, dir)
	got, found, err = reopened.Get(ctx, "feishu", "default")
	if err != nil || !found {
		t.Fatalf("Get after reopen = (found %v, err %v), want (true, nil)", found, err)
	}
	if !reflect.DeepEqual(got.Fields, fields) {
		t.Fatalf("Get after reopen fields = %v, want %v", got.Fields, fields)
	}
}

func TestSetReplacesAnExistingCredential(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	ctx := context.Background()
	service := mustOpen(t, dir)

	if err := service.Set(ctx, credential("feishu", "default", map[string]string{"app_id": "old", "app_secret": "old", "extra": "gone"})); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := service.Set(ctx, credential("feishu", "default", map[string]string{"app_id": "new"})); err != nil {
		t.Fatalf("second Set: %v", err)
	}

	got, found, err := service.Get(ctx, "feishu", "default")
	if err != nil || !found {
		t.Fatalf("Get = (found %v, err %v), want (true, nil)", found, err)
	}
	// Replacement, not merge: the old fields must be gone.
	want := map[string]string{"app_id": "new"}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Fatalf("fields after replace = %v, want %v", got.Fields, want)
	}

	infos, err := service.List(ctx, "feishu")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("List returned %d entries after replacing one credential, want 1", len(infos))
	}
}

func TestGetReturnsADeepCopy(t *testing.T) {
	ctx := context.Background()
	service := mustOpen(t, filepath.Join(t.TempDir(), "credentials"))
	fields := map[string]string{"app_secret": "keep-me"}
	if err := service.Set(ctx, credential("feishu", "default", fields)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Mutating the caller's input after Set must not corrupt the store.
	fields["app_secret"] = "mutated-after-set"

	got, _, err := service.Get(ctx, "feishu", "default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Fields["app_secret"] != "keep-me" {
		t.Fatalf("store kept the caller's mutable map: %q", got.Fields["app_secret"])
	}

	// Mutating the returned value must not corrupt the store either.
	got.Fields["app_secret"] = "mutated-after-get"
	again, _, err := service.Get(ctx, "feishu", "default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.Fields["app_secret"] != "keep-me" {
		t.Fatalf("store handed out its internal map: %q", again.Fields["app_secret"])
	}
}

func TestDeleteRemovesTheCredentialAndUnknownIsNotFound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	ctx := context.Background()
	service := mustOpen(t, dir)

	if err := service.Set(ctx, credential("feishu", "default", map[string]string{"app_secret": "x"})); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := service.Delete(ctx, "feishu", "default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := service.Get(ctx, "feishu", "default"); err != nil || found {
		t.Fatalf("Get after Delete = (found %v, err %v), want (false, nil)", found, err)
	}

	if err := service.Delete(ctx, "feishu", "default"); !errors.Is(err, contracts.ErrCredentialNotFound) {
		t.Fatalf("Delete of an unknown credential = %v, want ErrCredentialNotFound", err)
	} else if !strings.Contains(err.Error(), "feishu/default") {
		t.Fatalf("Delete error = %q, want the identity %q wrapped around the sentinel", err, "feishu/default")
	}

	// Deleting must persist: a reopened store must not resurrect the credential.
	reopened := mustOpen(t, dir)
	if _, found, err := reopened.Get(ctx, "feishu", "default"); err != nil || found {
		t.Fatalf("Get after reopen = (found %v, err %v), want (false, nil)", found, err)
	}
}

func TestListReturnsKindAndIDOnly(t *testing.T) {
	ctx := context.Background()
	service := mustOpen(t, filepath.Join(t.TempDir(), "credentials"))
	secret := "TOPSECRET-value-42"
	for _, id := range []string{"b-account", "a-account"} {
		if err := service.Set(ctx, credential("feishu", id, map[string]string{"app_secret": secret})); err != nil {
			t.Fatalf("Set(%q): %v", id, err)
		}
	}
	if err := service.Set(ctx, credential("wechat", "default", map[string]string{"token": secret})); err != nil {
		t.Fatalf("Set(wechat): %v", err)
	}

	infos, err := service.List(ctx, "feishu")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []contracts.CredentialInfo{{Kind: "feishu", ID: "a-account"}, {Kind: "feishu", ID: "b-account"}}
	if !reflect.DeepEqual(infos, want) {
		t.Fatalf("List = %+v, want %+v (sorted by id)", infos, want)
	}

	// The type itself is the guarantee: no field map exists, so no serialized
	// list can carry a secret.
	infoType := reflect.TypeFor[contracts.CredentialInfo]()
	if infoType.NumField() != 2 {
		t.Fatalf("contracts.CredentialInfo has %d fields, want exactly 2 (Kind, ID) — the list path must not carry secrets", infoType.NumField())
	}
	for _, name := range []string{"Kind", "ID"} {
		if _, ok := infoType.FieldByName(name); !ok {
			t.Fatalf("contracts.CredentialInfo is missing field %q", name)
		}
	}
	encoded, err := json.Marshal(infos)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("List serialization leaked the secret: %s", encoded)
	}
}

func TestCorruptFileYieldsErrStoreCorruptAndAnEmptyUsableStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	corrupt := []byte(`{"version":1,"credentials":[{"kind":`)
	path := filepath.Join(dir, creds.FileName)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	service, err := creds.Open(dir)
	if !errors.Is(err, creds.ErrStoreCorrupt) {
		t.Fatalf("Open(corrupt) = %v, want ErrStoreCorrupt", err)
	}
	if !errors.Is(err, contracts.ErrStoreCorrupt) {
		t.Fatalf("creds.ErrStoreCorrupt and contracts.ErrStoreCorrupt must be the same sentinel; got %v", err)
	}
	if service == nil {
		t.Fatal("Open(corrupt) returned a nil store; it must stay usable")
	}

	ctx := context.Background()
	if infos, err := service.List(ctx, "feishu"); err != nil || len(infos) != 0 {
		t.Fatalf("List on a corrupt store = (%+v, %v), want empty", infos, err)
	}
	// Open itself must not have touched the corrupt bytes (the caller decides
	// whether to quarantine): it read them and stopped. This is checked before
	// any Set, because a Set is what legitimately replaces the file.
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corrupt file: %v", err)
	}
	if string(restored) != string(corrupt) {
		t.Fatalf("Open rewrote the corrupt file: %q", restored)
	}

	// The store must still work, so the product keeps running after a bad file.
	if err := service.Set(ctx, credential("feishu", "default", map[string]string{"app_secret": "new"})); err != nil {
		t.Fatalf("Set after corrupt Open: %v", err)
	}
	if got, found, err := service.Get(ctx, "feishu", "default"); err != nil || !found || got.Fields["app_secret"] != "new" {
		t.Fatalf("Get after corrupt Open = (%+v, %v, %v)", got, found, err)
	}
}

func TestUnknownVersionIsCorrupt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := []byte(`{"version":99,"credentials":[]}`)
	if err := os.WriteFile(filepath.Join(dir, creds.FileName), seed, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service, err := creds.Open(dir)
	if !errors.Is(err, creds.ErrStoreCorrupt) {
		t.Fatalf("Open(future version) = %v, want ErrStoreCorrupt", err)
	}
	if service == nil {
		t.Fatal("Open returned a nil store for an unknown version")
	}
}

func TestFileIsWrittenAtomicallyWithPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	ctx := context.Background()
	service := mustOpen(t, dir)

	if err := service.Set(ctx, credential("feishu", "default", map[string]string{"app_secret": "x"})); err != nil {
		t.Fatalf("Set: %v", err)
	}
	path := filepath.Join(dir, creds.FileName)
	if mode := statMode(t, path).Perm(); mode != 0o600 {
		t.Fatalf("credentials file mode = %v, want 0600", mode)
	}
	if mode := statMode(t, dir).Perm(); mode != 0o700 {
		t.Fatalf("credentials dir mode = %v, want 0700", mode)
	}

	// Every Set/Delete must leave no temp file behind (a leaked temp file is
	// both a mess and a second copy of the secret).
	for i := 0; i < 3; i++ {
		if err := service.Set(ctx, credential("feishu", fmt.Sprintf("acct-%d", i), map[string]string{"app_secret": "x"})); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}
	if err := service.Delete(ctx, "feishu", "acct-0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != creds.FileName {
			t.Fatalf("stray file left in the store dir: %q", entry.Name())
		}
	}

	// The on-disk file must be the versioned document, not an internal shape.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Version     int `json:"version"`
		Credentials []struct {
			Kind   string            `json:"kind"`
			ID     string            `json:"id"`
			Fields map[string]string `json:"fields"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode on-disk document: %v\n%s", err, raw)
	}
	if doc.Version != 1 {
		t.Fatalf("on-disk version = %d, want 1", doc.Version)
	}
	if len(doc.Credentials) != 3 {
		t.Fatalf("on-disk credentials = %d, want 3 (default, acct-1, acct-2)", len(doc.Credentials))
	}
}

// wellFormedIdent reports whether value matches the identity alphabet the store
// enforces. The validation table uses it to tell "the id is the defect" apart
// from "the id is fine, something else is wrong".
func wellFormedIdent(value string) bool {
	return regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(value)
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	service := mustOpen(t, filepath.Join(t.TempDir(), "credentials"))

	tooMany := make(map[string]string, 17)
	for i := 0; i < 17; i++ {
		tooMany[fmt.Sprintf("field_%02d", i)] = "v"
	}
	exactly16 := make(map[string]string, 16)
	for i := 0; i < 16; i++ {
		exactly16[fmt.Sprintf("field_%02d", i)] = "v"
	}

	cases := []struct {
		name       string
		credential contracts.Credential
		wantErr    bool
	}{
		{name: "valid", credential: credential("feishu", "default", map[string]string{"app_secret": "x"})},
		{name: "valid with dash and underscore", credential: credential("ding-talk_v2", "acct_1-a", map[string]string{"k": "v"})},
		{name: "valid 32 char kind", credential: credential("a2345678901234567890123456789012", "default", map[string]string{"k": "v"})},
		{name: "valid no fields", credential: credential("feishu", "default", nil)},
		{name: "valid 16 fields", credential: credential("feishu", "default", exactly16)},
		{name: "valid 4096 byte value", credential: credential("feishu", "default", map[string]string{"k": strings.Repeat("v", 4096)})},
		{name: "empty kind", credential: credential("", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "uppercase kind", credential: credential("Feishu", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "kind leading digit", credential: credential("1feishu", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "kind leading dash", credential: credential("-feishu", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "kind with space", credential: credential("fei shu", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "kind too long", credential: credential("a23456789012345678901234567890123", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "kind with slash", credential: credential("feishu/tenant", "default", map[string]string{"k": "v"}), wantErr: true},
		{name: "empty id", credential: credential("feishu", "", map[string]string{"k": "v"}), wantErr: true},
		{name: "uppercase id", credential: credential("feishu", "Default", map[string]string{"k": "v"}), wantErr: true},
		{name: "id too long", credential: credential("feishu", "a23456789012345678901234567890123", map[string]string{"k": "v"}), wantErr: true},
		{name: "empty field key", credential: credential("feishu", "default", map[string]string{"": "v"}), wantErr: true},
		{name: "17 fields", credential: credential("feishu", "default", tooMany), wantErr: true},
		{name: "value 4097 bytes", credential: credential("feishu", "default", map[string]string{"k": strings.Repeat("v", 4097)}), wantErr: true},
	}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// One identity per case, so "was it stored?" cannot be answered by a
			// credential an earlier (valid) case left behind. Cases whose id is
			// itself part of the defect keep that id.
			if wellFormedIdent(testCase.credential.ID) {
				testCase.credential.ID = fmt.Sprintf("case-%d", index)
			}
			err := service.Set(ctx, testCase.credential)
			if testCase.wantErr {
				if !errors.Is(err, contracts.ErrCredentialInvalid) {
					t.Fatalf("Set = %v, want ErrCredentialInvalid", err)
				}
				// A rejected credential must never be stored. Bad ids/kinds are
				// rejected by the lookup too, so only well-formed identities are
				// checked here.
				if idErr := service.Delete(ctx, testCase.credential.Kind, testCase.credential.ID); idErr == nil {
					t.Fatalf("rejected credential %s/%s was stored anyway", testCase.credential.Kind, testCase.credential.ID)
				} else if !errors.Is(idErr, contracts.ErrCredentialInvalid) && !errors.Is(idErr, contracts.ErrCredentialNotFound) {
					t.Fatalf("unexpected error probing the rejected credential: %v", idErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%s/%s) = %v, want nil", testCase.credential.Kind, testCase.credential.ID, err)
			}
		})
	}
}

func TestMalformedLookupsAreRejectedNotSilentlyEmpty(t *testing.T) {
	ctx := context.Background()
	service := mustOpen(t, filepath.Join(t.TempDir(), "credentials"))

	if _, _, err := service.Get(ctx, "Feishu", "default"); !errors.Is(err, contracts.ErrCredentialInvalid) {
		t.Fatalf("Get(bad kind) = %v, want ErrCredentialInvalid", err)
	}
	if err := service.Delete(ctx, "feishu", ""); !errors.Is(err, contracts.ErrCredentialInvalid) {
		t.Fatalf("Delete(bad id) = %v, want ErrCredentialInvalid", err)
	}
	if _, err := service.List(ctx, "fei shu"); !errors.Is(err, contracts.ErrCredentialInvalid) {
		t.Fatalf("List(bad kind) = %v, want ErrCredentialInvalid", err)
	}
}

func TestSetAndGetAreSafeForConcurrentUse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	ctx := context.Background()
	service := mustOpen(t, dir)

	const workers = 8
	const iterations = 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*iterations*2)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// A shared identity written by every worker: the store must
				// serialize the writes and never lose/mix fields.
				shared := credential("feishu", "shared", map[string]string{"app_secret": fmt.Sprintf("w%d-%d", worker, i)})
				if err := service.Set(ctx, shared); err != nil {
					errs <- fmt.Errorf("Set(shared): %w", err)
					return
				}
				own := credential("feishu", fmt.Sprintf("w%d-%d", worker, i), map[string]string{"app_secret": "own"})
				if err := service.Set(ctx, own); err != nil {
					errs <- fmt.Errorf("Set(own): %w", err)
					return
				}
				if got, found, err := service.Get(ctx, "feishu", "shared"); err != nil {
					errs <- fmt.Errorf("Get(shared): %w", err)
					return
				} else if !found {
					errs <- errors.New("Get(shared): credential disappeared")
					return
				} else if got.Fields["app_secret"] == "" {
					errs <- errors.New("Get(shared): empty app_secret")
					return
				}
				if _, err := service.List(ctx, "feishu"); err != nil {
					errs <- fmt.Errorf("List: %w", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	infos, err := service.List(ctx, "feishu")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// 1 shared + workers*iterations own identities.
	if want := 1 + workers*iterations; len(infos) != want {
		t.Fatalf("List returned %d credentials, want %d", len(infos), want)
	}
	// The final on-disk document must still be readable (no half write won).
	reopened := mustOpen(t, dir)
	reopenedInfos, err := reopened.List(ctx, "feishu")
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(reopenedInfos) != len(infos) {
		t.Fatalf("reopened store has %d credentials, want %d", len(reopenedInfos), len(infos))
	}
}

func TestOpenRejectsAnEmptyDirectory(t *testing.T) {
	if service, err := creds.Open("   "); err == nil || service != nil {
		t.Fatalf("Open(blank dir) = (%v, %v), want (nil, error)", service, err)
	}
}

func TestCancelledContextIsHonoured(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := mustOpen(t, filepath.Join(t.TempDir(), "credentials"))
	if err := service.Set(ctx, credential("feishu", "default", map[string]string{"k": "v"})); err == nil {
		t.Fatal("Set with a cancelled context succeeded, want the context error")
	}
}
