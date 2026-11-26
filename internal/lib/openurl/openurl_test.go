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

func TestOpenEmptyURLIsRefused(t *testing.T) {
	called := false
	openExternal = func(string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { openExternal = defaultOpenExternal })

	if err := Open("  \n"); err == nil {
		t.Fatal("Open(blank) = nil, want a refusal")
	}
	if called {
		t.Fatal("the launcher ran for a blank URL")
	}
}

// The platform command is what makes this package work without a UI framework;
// pin it for the three platforms the product builds for.
func TestLauncherArgsPerPlatform(t *testing.T) {
	cases := map[string]string{
		"darwin":  "open",
		"linux":   "xdg-open",
		"windows": "rundll32",
	}
	for goos, want := range cases {
		name, args, err := launcherArgs(goos, "https://example.com")
		if err != nil || name != want || len(args) == 0 {
			t.Fatalf("%s = (%q, %v, %v), want %q", goos, name, args, err, want)
		}
	}
	if _, _, err := launcherArgs("plan9", "x"); err == nil {
		t.Fatal("unsupported platform should error")
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
