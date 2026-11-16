package openurl

// AR-21 (2026-11 audit): the browser launcher seam had no direct tests. These
// pin the command construction (trim before the platform call, error
// propagation) by replacing the platform step, and the public seam contract
// the OAuth flow and UI tests use.

import (
	"errors"
	"testing"
)

func TestOpenTrimsURLBeforeLaunching(t *testing.T) {
	var got []string
	launchErr := errors.New("no browser")
	openExternal = func(raw string) error {
		got = append(got, raw)
		return launchErr
	}
	t.Cleanup(func() { openExternal = defaultOpenExternal })

	if err := Open("  https://example.com/oauth \n"); err != launchErr {
		t.Fatalf("Open error = %v, want the launcher error propagated", err)
	}
	if len(got) != 1 || got[0] != "https://example.com/oauth" {
		t.Fatalf("opened URLs = %q, want the trimmed URL once", got)
	}
}

func TestOpenEmptyURLStillDelegates(t *testing.T) {
	var got []string
	openExternal = func(raw string) error {
		got = append(got, raw)
		return nil
	}
	t.Cleanup(func() { openExternal = defaultOpenExternal })

	if err := Open("  "); err != nil {
		t.Fatalf("Open error = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("opened URLs = %q, want one empty-string call (trim only)", got)
	}
}

func TestPublicSeamIsReplaceable(t *testing.T) {
	orig := Open
	t.Cleanup(func() { Open = orig })

	var opened []string
	Open = func(raw string) error { opened = append(opened, raw); return nil }
	if err := Open("https://example.com"); err != nil {
		t.Fatalf("fake Open: %v", err)
	}
	if len(opened) != 1 || opened[0] != "https://example.com" {
		t.Fatalf("seam not used: opened=%q", opened)
	}
}
