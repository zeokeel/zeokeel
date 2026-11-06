package shell

import (
	"errors"
	"testing"

	"github.com/egoist/mygo"
)

// TestFilesNativeHooksInjectable covers the Files panel's native hooks: a test
// may inject the opener and revealer on the App's filesNativeUI state, and the
// helpers hand those functions to the panel instead of the mygo desktop API.
func TestFilesNativeHooksInjectable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})

	var opened, revealed string
	h.app.filesNativeUI().openPath = func(path string) error {
		opened = path
		return nil
	}
	h.app.filesNativeUI().revealPath = func(path string) error {
		revealed = path
		return nil
	}

	if err := h.app.filesOpener()("/workspace/report.md"); err != nil {
		t.Fatalf("filesOpener() = %v, want the injected hook's nil", err)
	}
	if err := h.app.filesRevealer()("/workspace/design"); err != nil {
		t.Fatalf("filesRevealer() = %v, want the injected hook's nil", err)
	}
	if opened != "/workspace/report.md" {
		t.Fatalf("injected opener received %q, want %q", opened, "/workspace/report.md")
	}
	if revealed != "/workspace/design" {
		t.Fatalf("injected revealer received %q, want %q", revealed, "/workspace/design")
	}
}

// TestFilesNativeHooksPropagateErrors proves the helpers keep the injected
// hook's failure as an error the panel can toast, rather than swallowing it.
func TestFilesNativeHooksPropagateErrors(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	openErr := errors.New("no handler for that file type")
	revealErr := errors.New("file manager did not start")
	h.app.filesNativeUI().openPath = func(string) error { return openErr }
	h.app.filesNativeUI().revealPath = func(string) error { return revealErr }

	if err := h.app.filesOpener()("/workspace/report.md"); !errors.Is(err, openErr) {
		t.Fatalf("filesOpener() = %v, want %v", err, openErr)
	}
	if err := h.app.filesRevealer()("/workspace/design"); !errors.Is(err, revealErr) {
		t.Fatalf("filesRevealer() = %v, want %v", err, revealErr)
	}
}

// TestFilesNativeHooksUnavailableWithoutBackend covers the headless case: with
// no injected hooks and no running mygo application, both helpers report the
// shared unavailable error (callers turn it into the "no desktop" hint)
// instead of calling into mygo and panicking.
func TestFilesNativeHooksUnavailableWithoutBackend(t *testing.T) {
	if mygo.App.IsReady() {
		t.Skip("desktop backend is running: the unavailable branch is not reachable here")
	}
	h := newLiveApp(t, &shellProvider{})

	if err := h.app.filesOpener()("/workspace/report.md"); !errors.Is(err, errPluginNativeUnavailable) {
		t.Fatalf("filesOpener() = %v, want errPluginNativeUnavailable", err)
	}
	if err := h.app.filesRevealer()("/workspace/report.md"); !errors.Is(err, errPluginNativeUnavailable) {
		t.Fatalf("filesRevealer() = %v, want errPluginNativeUnavailable", err)
	}
}

// TestFilesNativeHooksStayPerApp proves the injected hooks belong to one App:
// another App (a second window in production, a second fixture in tests) keeps
// the real backend path and is not steered by the first App's fakes.
func TestFilesNativeHooksStayPerApp(t *testing.T) {
	if mygo.App.IsReady() {
		t.Skip("desktop backend is running: the unavailable branch is not reachable here")
	}
	first := newLiveApp(t, &shellProvider{})
	first.app.filesNativeUI().openPath = func(string) error { return nil }

	if err := first.app.filesOpener()("/a"); err != nil {
		t.Fatalf("first app filesOpener() = %v, want its injected hook", err)
	}
	second := &App{}
	if err := second.filesOpener()("/a"); !errors.Is(err, errPluginNativeUnavailable) {
		t.Fatalf("second app filesOpener() = %v, want errPluginNativeUnavailable", err)
	}
}
