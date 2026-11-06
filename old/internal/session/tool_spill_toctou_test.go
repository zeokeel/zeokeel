//go:build linux

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpillRefusesDirSwapAfterValidation pins the P3 TOCTOU hardening: the
// original RT-07 check validated the directory path, then opened the file by
// path. An attacker able to replace the verified directory with a symlink in
// that window redirected the write outside the workspace. The hardened path
// keeps an open directory descriptor (openat chain) and verifies the directory
// identity after creation, so the swap is detected and nothing is written
// outside the workspace.
func TestSpillRefusesDirSwapAfterValidation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	spillDir, err := safeSpillDir(root)
	if err != nil {
		t.Fatalf("safeSpillDir: %v", err)
	}

	// Deterministic race injection: after the implementation has validated and
	// opened the spill directory, swap it for a symlink pointing outside the
	// workspace (the exact check-use window).
	moved := filepath.Join(filepath.Dir(spillDir), "tool-results-moved")
	spillDirOpenedHook = func(validated string) {
		if validated != spillDir {
			t.Errorf("hook dir = %q, want %q", validated, spillDir)
		}
		if err := os.Rename(spillDir, moved); err != nil {
			t.Errorf("rename verified dir: %v", err)
			return
		}
		if err := os.Symlink(outside, spillDir); err != nil {
			t.Errorf("plant symlink: %v", err)
		}
	}
	t.Cleanup(func() { spillDirOpenedHook = nil })

	result := strings.Repeat("x", toolResultSpillBytes+64)
	_, ok, err := spillToolResultFile(root, "call-toctou", result)
	if err == nil {
		t.Fatal("spill succeeded after the validated directory was swapped for a symlink; want a refusal")
	}
	if ok {
		t.Fatal("spill reported ok after a directory swap")
	}

	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatalf("read outside dir: %v", readErr)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("spill wrote outside the workspace through the swapped dir: %v", names)
	}
}

// TestSpillNormalPathStillWrites pins that the hardened openat path still works
// for the ordinary case.
func TestSpillNormalPathStillWrites(t *testing.T) {
	root := t.TempDir()
	summary, ok, err := spillToolResultFile(root, "call-normal", strings.Repeat("y", toolResultSpillBytes+16))
	if err != nil || !ok {
		t.Fatalf("spill = ok:%v err:%v, want success", ok, err)
	}
	if !strings.Contains(summary, ".zeokeel/tool-results/call-normal.txt") {
		t.Fatalf("summary = %q, want the workspace-relative spill path", summary)
	}
	path := filepath.Join(root, ".zeokeel", "tool-results", "call-normal.txt")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("spilled file missing: %v", err)
	}
}
