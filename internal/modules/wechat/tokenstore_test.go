package wechat_test

// TDD for the login-token store. The properties that matter are boring and
// strict: the token lives in one 0600 file, writes are atomic (a crash must not
// leave a truncated token that looks valid), and a corrupt file is reported as a
// typed error *without* being rewritten — the caller decides whether to
// quarantine it, so a bug in the JSON writer can never destroy the last login.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/modules/wechat"
)

func TestTokenStoreSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if err := store.Save("bot-token-1"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	token, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok || token != "bot-token-1" {
		t.Fatalf("Load = (%q, %v), want (bot-token-1, true)", token, ok)
	}

	info, err := os.Stat(filepath.Join(dir, "token.json"))
	if err != nil {
		t.Fatalf("stat token.json: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token.json mode = %04o, want 0600", mode)
	}

	// The temp file must be renamed away, not left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != "token.json" {
		t.Fatalf("store dir = %v, want only token.json", names)
	}
}

func TestTokenStoreLoadMissingIsNotAnError(t *testing.T) {
	store, err := wechat.OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	token, ok, err := store.Load()
	if err != nil || ok || token != "" {
		t.Fatalf("Load = (%q, %v, %v), want (\"\", false, nil)", token, ok, err)
	}
}

func TestTokenStoreCorruptJSONKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	corrupt := []byte(`{"token": "bot-to`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}

	token, ok, err := store.Load()
	if !errors.Is(err, wechat.ErrTokenStoreCorrupt) {
		t.Fatalf("Load err = %v, want ErrTokenStoreCorrupt", err)
	}
	if ok || token != "" {
		t.Fatalf("Load = (%q, %v), want no token on a corrupt file", token, ok)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read corrupt file: %v", err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt file was modified: %q", after)
	}
}

func TestTokenStoreRejectsJSONWithoutToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.json"), []byte(`{"updated_at":"2026-11-25T00:00:00Z"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if _, _, err := store.Load(); !errors.Is(err, wechat.ErrTokenStoreCorrupt) {
		t.Fatalf("Load err = %v, want ErrTokenStoreCorrupt", err)
	}
}

func TestTokenStoreClearIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if err := store.Save("bot-token-1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load after Clear = (ok %v, err %v), want gone", ok, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "token.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token.json still present after Clear: %v", err)
	}
}

func TestTokenStoreSaveEmptyClears(t *testing.T) {
	dir := t.TempDir()
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if err := store.Save("bot-token-1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save("   "); err != nil {
		t.Fatalf("Save(blank): %v", err)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load after blank Save = (ok %v, err %v), want gone", ok, err)
	}
}

func TestTokenStoreSaveReplacesAtomicallyAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	// A sloppy pre-existing file must not leak its mode into the next save.
	path := filepath.Join(dir, "token.json")
	if err := os.WriteFile(path, []byte(`{"token":"old"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := store.Save("bot-token-2"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %04o after overwrite, want 0600", mode)
	}
	token, _, err := store.Load()
	if err != nil || token != "bot-token-2" {
		t.Fatalf("Load = (%q, %v), want bot-token-2", token, err)
	}
}

func TestTokenStoreCreatesMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "wechat")
	store, err := wechat.OpenTokenStore(dir)
	if err != nil {
		t.Fatalf("OpenTokenStore: %v", err)
	}
	if err := store.Save("bot-token-1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok, err := store.Load(); err != nil || !ok {
		t.Fatalf("Load = (ok %v, err %v), want a token", ok, err)
	}
}

func TestOpenTokenStoreRejectsEmptyDir(t *testing.T) {
	if _, err := wechat.OpenTokenStore(""); err == nil {
		t.Fatal("OpenTokenStore(\"\") succeeded")
	} else if !strings.Contains(err.Error(), "dir") {
		t.Fatalf("err = %v, want a directory complaint", err)
	}
}
