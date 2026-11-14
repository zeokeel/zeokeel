package host

// Tests for the window close/quit guard: closing or quitting with backend work
// in flight asks first (and a cancelled answer keeps the app open), a confirmed
// close does not ask twice, the close-to-tray path defers to the tray listener,
// and the teardown/OS-session-ending paths never prompt.

import (
	"testing"

	"github.com/egoist/mygo"
)

// guardPolicy builds a guard with counters; confirmAnswer is what the user
// clicks (true = quit anyway), and confirmCalls counts the prompts.
func guardPolicy(work bool, confirmAnswer bool) (*closeGuard, *int) {
	confirmCalls := 0
	guard := newCloseGuard(closeGuardDeps{
		runningWork: func() bool { return work },
		confirm: func() bool {
			confirmCalls++
			return confirmAnswer
		},
		keepAlive: func() bool { return true },
	})
	return guard, &confirmCalls
}

func TestCloseGuardIdleCloseProceeds(t *testing.T) {
	guard, confirmCalls := guardPolicy(false, false)
	event := &mygo.CloseEvent{}
	guard.handleClose(event)
	if event.DefaultPrevented() {
		t.Fatal("an idle close was prevented")
	}
	if *confirmCalls != 0 {
		t.Fatalf("confirm prompts for an idle close = %d, want 0", *confirmCalls)
	}
}

func TestCloseGuardAsksWithRunningWork(t *testing.T) {
	guard, confirmCalls := guardPolicy(true, false)
	event := &mygo.CloseEvent{}
	guard.handleClose(event)
	if !event.DefaultPrevented() {
		t.Fatal("cancelling the confirmation did not keep the window open")
	}
	if *confirmCalls != 1 {
		t.Fatalf("confirm prompts = %d, want 1", *confirmCalls)
	}
}

func TestCloseGuardConfirmedCloseProceedsAndDoesNotAskTwice(t *testing.T) {
	guard, confirmCalls := guardPolicy(true, true)
	event := &mygo.CloseEvent{}
	guard.handleClose(event)
	if event.DefaultPrevented() {
		t.Fatal("a confirmed close was prevented")
	}
	// The last-window quit that follows the close must not prompt again.
	quit := &mygo.QuitEvent{}
	if !guard.allowQuitEvent(quit) {
		t.Fatal("the quit after a confirmed close was cancelled")
	}
	if quit.DefaultPrevented() {
		t.Fatal("the quit after a confirmed close was prevented")
	}
	if *confirmCalls != 1 {
		t.Fatalf("confirm prompts after the close-then-quit sequence = %d, want 1", *confirmCalls)
	}
}

func TestCloseGuardNilConfirmCancels(t *testing.T) {
	guard := newCloseGuard(closeGuardDeps{
		runningWork: func() bool { return true },
		keepAlive:   func() bool { return true },
	})
	event := &mygo.CloseEvent{}
	guard.handleClose(event)
	if !event.DefaultPrevented() {
		t.Fatal("a missing confirmation dialog dropped the running work")
	}
}

func TestCloseGuardShutdownPathProceeds(t *testing.T) {
	guard := newCloseGuard(closeGuardDeps{
		runningWork: func() bool { return true },
		confirm:     func() bool { return false },
		keepAlive:   func() bool { return false },
	})
	event := &mygo.CloseEvent{}
	guard.handleClose(event)
	if event.DefaultPrevented() {
		t.Fatal("the teardown close was prevented")
	}
	quit := &mygo.QuitEvent{}
	if !guard.allowQuitEvent(quit) {
		t.Fatal("the teardown quit was cancelled")
	}
}

func TestCloseGuardCloseToTrayDefersToTray(t *testing.T) {
	guard := newCloseGuard(closeGuardDeps{
		runningWork: func() bool { return true },
		confirm:     func() bool { return false },
		closeToTray: true,
		keepAlive:   func() bool { return true },
	})
	event := &mygo.CloseEvent{}
	guard.handleClose(event)
	if event.DefaultPrevented() {
		t.Fatal("the guard prevented a close-to-tray hide")
	}
}

func TestQuitGuardIdleQuitProceeds(t *testing.T) {
	guard, confirmCalls := guardPolicy(false, false)
	if !guard.allowQuitEvent(&mygo.QuitEvent{}) {
		t.Fatal("an idle quit was cancelled")
	}
	if *confirmCalls != 0 {
		t.Fatalf("confirm prompts for an idle quit = %d, want 0", *confirmCalls)
	}
}

func TestQuitGuardAsksWithRunningWork(t *testing.T) {
	guard, confirmCalls := guardPolicy(true, false)
	event := &mygo.QuitEvent{}
	if guard.allowQuitEvent(event) {
		t.Fatal("a quit with running work was allowed without asking")
	}
	if !event.DefaultPrevented() {
		t.Fatal("the cancelled quit was not prevented")
	}
	if *confirmCalls != 1 {
		t.Fatalf("confirm prompts = %d, want 1", *confirmCalls)
	}
}

func TestQuitGuardConfirmedQuitProceeds(t *testing.T) {
	guard, confirmCalls := guardPolicy(true, true)
	if !guard.allowQuitEvent(&mygo.QuitEvent{}) {
		t.Fatal("the confirmed quit was cancelled")
	}
	if !guard.allowQuitEvent(&mygo.QuitEvent{}) {
		t.Fatal("a repeated quit event was cancelled")
	}
	if *confirmCalls != 1 {
		t.Fatalf("confirm prompts after a confirmed quit = %d, want 1", *confirmCalls)
	}
}

func TestQuitGuardSessionEndNeverAsks(t *testing.T) {
	guard, confirmCalls := guardPolicy(true, false)
	event := &mygo.QuitEvent{SessionEnding: true}
	if !guard.allowQuitEvent(event) {
		t.Fatal("the OS session-ending quit was cancelled")
	}
	if *confirmCalls != 0 {
		t.Fatalf("confirm prompts during a session end = %d, want 0", *confirmCalls)
	}
}

func TestQuitConfirmCopyLocalized(t *testing.T) {
	zhTitle, zhMessage, zhDetail, zhCancel, zhQuit := quitConfirmCopy(false)
	enTitle, enMessage, enDetail, enCancel, enQuit := quitConfirmCopy(true)
	for name, value := range map[string]string{
		"zh title": zhTitle, "zh message": zhMessage, "zh detail": zhDetail,
		"zh cancel": zhCancel, "zh quit": zhQuit,
		"en title": enTitle, "en message": enMessage, "en detail": enDetail,
		"en cancel": enCancel, "en quit": enQuit,
	} {
		if value == "" {
			t.Fatalf("%s is empty", name)
		}
	}
	if zhTitle == enTitle || zhQuit == enQuit {
		t.Fatal("the confirmation copy is not localized")
	}
}
