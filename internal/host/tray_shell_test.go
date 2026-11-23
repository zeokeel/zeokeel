package host

// The tray shell's decision logic (S1 of the native-UI retirement): the gateway
// product must never fail to serve because of the tray.

import (
	"os"
	goruntime "runtime"
	"testing"

	"github.com/zeokeel/zeokeel/internal/gateway"
	"github.com/zeokeel/zeokeel/internal/lib/openurl"
)

// TestTrayEnabledFlagWins pins --no-tray: a server admin asked for no icon.
func TestTrayEnabledFlagWins(t *testing.T) {
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "")
	if trayEnabled(true) {
		t.Fatal("--no-tray still enabled the tray")
	}
}

// TestTrayEnabledLinuxNeedsADisplay pins the silent skip: no display means no
// tray attempt at all (no warning either — the process is a server).
func TestTrayEnabledLinuxNeedsADisplay(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("the display probe is Linux-only")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if trayEnabled(false) {
		t.Fatal("a display-less Linux session enabled the tray")
	}
	t.Setenv("DISPLAY", ":0")
	if !trayEnabled(false) {
		t.Fatal("an X11 session did not enable the tray")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if !trayEnabled(false) {
		t.Fatal("a Wayland session did not enable the tray")
	}
}

// TestBrowserWindowShowOpensTheClient pins the adapter: every "window" gesture
// the tray offers becomes "open the browser client", and none of them panics
// without an opener.
func TestBrowserWindowShowOpensTheClient(t *testing.T) {
	opened := 0
	window := &BrowserWindow{Open: func() error { opened++; return nil }}
	window.Show()
	window.Restore()
	window.Focus()
	window.Hide()
	unsubscribe := window.OnClose(nil)
	unsubscribe()
	if opened != 3 {
		t.Fatalf("show/restore/focus opened the client %d times, want 3", opened)
	}
	// A window without an opener is silent, not a panic (bare test hosts).
	(&BrowserWindow{}).Show()
}

// TestOpenClientURLReadsGatewayJSON pins that the tray opens the running
// gateway's URL, and that a missing gateway.json is a readable error.
func TestOpenClientURLReadsGatewayJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+gateway.InfoFileName, []byte(`{"port":9932,"token":"t0k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var openedURL string
	original := openurl.Open
	openurl.Open = func(raw string) error { openedURL = raw; return nil }
	defer func() { openurl.Open = original }()
	if err := openClientURL(dir); err != nil {
		t.Fatalf("openClientURL: %v", err)
	}
	if openedURL != "http://127.0.0.1:9932/#token=t0k" {
		t.Fatalf("opened %q", openedURL)
	}
	if err := openClientURL(t.TempDir()); err == nil {
		t.Fatal("a missing gateway.json did not fail")
	}
}
