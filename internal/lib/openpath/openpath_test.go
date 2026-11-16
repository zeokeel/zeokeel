package openpath

// AR-21 (2026-11 audit): the launcher seams had no direct tests. These pin the
// command construction (trim before the platform call, error propagation) by
// replacing the platform step, and the public seam contract the UI tests use.

import (
	"errors"
	"testing"
)

func TestOpenTrimsPathBeforeLaunching(t *testing.T) {
	var got []string
	launchErr := errors.New("no launcher")
	openPath = func(path string) error {
		got = append(got, path)
		return launchErr
	}
	t.Cleanup(func() { openPath = defaultOpenPath })

	if err := Open("  /tmp/交付文件.md \t"); err != launchErr {
		t.Fatalf("Open error = %v, want the launcher error propagated", err)
	}
	if len(got) != 1 || got[0] != "/tmp/交付文件.md" {
		t.Fatalf("launched paths = %q, want the trimmed path once", got)
	}
}

func TestOpenEmptyPathStillDelegates(t *testing.T) {
	var got []string
	openPath = func(path string) error {
		got = append(got, path)
		return nil
	}
	t.Cleanup(func() { openPath = defaultOpenPath })

	if err := Open("   "); err != nil {
		t.Fatalf("Open error = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("launched paths = %q, want one empty-string call (trim only)", got)
	}
}

func TestShowInFolderTrimsPathAndReturnsNil(t *testing.T) {
	var got []string
	showInFolder = func(path string) { got = append(got, path) }
	t.Cleanup(func() { showInFolder = defaultShowInFolder })

	if err := ShowInFolder(" /tmp/x "); err != nil {
		t.Fatalf("ShowInFolder error = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != "/tmp/x" {
		t.Fatalf("revealed paths = %q, want the trimmed path once", got)
	}
}

func TestPublicSeamsAreReplaceable(t *testing.T) {
	origOpen, origShow := Open, ShowInFolder
	t.Cleanup(func() { Open, ShowInFolder = origOpen, origShow })

	var opened, shown []string
	Open = func(path string) error { opened = append(opened, path); return nil }
	ShowInFolder = func(path string) error { shown = append(shown, path); return nil }

	if err := Open("a"); err != nil {
		t.Fatalf("fake Open: %v", err)
	}
	if err := ShowInFolder("b"); err != nil {
		t.Fatalf("fake ShowInFolder: %v", err)
	}
	if len(opened) != 1 || opened[0] != "a" || len(shown) != 1 || shown[0] != "b" {
		t.Fatalf("seams not used: opened=%q shown=%q", opened, shown)
	}
}
