package shell

import (
	"errors"
	"fmt"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// filesNativeState holds the Files panel's native shell hooks: opening a
// workspace path with the OS default application and revealing it in the OS
// file manager. Like pluginUIState (plugin_details.go), the hooks are
// injectable so tests exercise the callers without a desktop backend; nil
// means the real mygo API.
//
// The state lives in this file rather than in rightPanelState so the native
// layer can land on its own (the panel state file is owned by a parallel
// change); merging it into rightPanelState later needs no caller change.
type filesNativeState struct {
	// openPath opens a file or directory with the OS default application.
	openPath func(string) error
	// revealPath reveals a file or directory in the OS file manager.
	revealPath func(string) error
}

var filesNativeStates sync.Map // map[*App]*filesNativeState

// filesNativeUI returns the Files panel's native hooks of a, making them on
// first use. Only the view goroutine reads it, like pluginUI.
func (a *App) filesNativeUI() *filesNativeState {
	if s, ok := filesNativeStates.Load(a); ok {
		return s.(*filesNativeState)
	}
	actual, _ := filesNativeStates.LoadOrStore(a, &filesNativeState{})
	return actual.(*filesNativeState)
}

// filesOpener opens a workspace path with the OS default application. A test
// may inject filesNativeUI().openPath; otherwise the mygo desktop backend
// does it. Before App.Run (the headless tester, a service-only process) the
// returned function reports errPluginNativeUnavailable instead of letting
// mygo's readiness panic surface, matching pluginOpener.
func (a *App) filesOpener() func(string) error {
	if fn := a.filesNativeUI().openPath; fn != nil {
		return fn
	}
	if !mygo.App.IsReady() {
		return func(string) error { return errPluginNativeUnavailable }
	}
	return mygo.Shell.OpenPath
}

// filesRevealer reveals a workspace path in the OS file manager. mygo's
// ShowItemInFolder returns no error, so a failed launch cannot be reported:
// this wrapper only guards the backend readiness, and the Files panel keeps
// the reveal control best-effort (the platform file manager either comes up
// or silently stays closed).
func (a *App) filesRevealer() func(string) error {
	if fn := a.filesNativeUI().revealPath; fn != nil {
		return fn
	}
	if !mygo.App.IsReady() {
		return func(string) error { return errPluginNativeUnavailable }
	}
	return func(path string) error {
		mygo.Shell.ShowItemInFolder(path)
		return nil
	}
}

// filesNativeRun runs one native open/reveal action for the Files tab and
// reports its failure: the panel keeps the message under the preview's
// metadata row and a toast carries it to the edge of the screen. A success
// clears the earlier panel error.
func (a *App) filesNativeRun(c *ui.Context, path string, action func(string) error) {
	panel := a.rightPanelUI()
	if err := action(path); err != nil {
		msg := a.filesNativeErrorText(err)
		panel.nativeErr = msg
		if c != nil {
			c.Toast(msg)
		}
		return
	}
	panel.nativeErr = ""
}

// filesNativeErrorText turns a native-call failure into the panel's message:
// the backend-not-ready case gets the "preview in the sidebar" hint (the same
// hint the present card shows), anything else keeps the OS error so the user
// can act on it.
func (a *App) filesNativeErrorText(err error) string {
	if errors.Is(err, errPluginNativeUnavailable) {
		return a.T("session.host_note")
	}
	return fmt.Sprintf(a.T("files.native_open_failed_v"), err)
}

// filesNativeAvailable reports whether the Files panel's native actions can
// work: an injected hook counts as available (tests drive the panel without a
// desktop), otherwise the mygo desktop backend must be running. The Files
// panel and the present card use it to show the "preview in the sidebar" hint
// without trying a call first.
func (a *App) filesNativeAvailable() bool {
	st := a.filesNativeUI()
	return st.openPath != nil || st.revealPath != nil || mygo.App.IsReady()
}
