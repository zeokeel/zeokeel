package app

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/zeokeel/zeokeel/internal/globalhotkey"
	"github.com/zeokeel/zeokeel/internal/updater"
)

// Round-3 product wiring: auto-update check, global hotkey and settings keys.

const (
	// updateFeedURLKey selects the update manifest URL; empty disables updates.
	updateFeedURLKey = "update.feed_url"
	// updateCheckOnStartKey opts into a background check on startup.
	updateCheckOnStartKey = "update.check_on_start"
	// globalHotkeySettingKey is the system-wide show-window shortcut, e.g.
	// "Ctrl+Alt+Z". Empty (the default) registers nothing.
	globalHotkeySettingKey = "global_hotkey"
)

// CheckForUpdate runs one update check with the persisted feed URL. It returns
// updater.ErrDisabled when no feed is configured and (nil, false, nil) when the
// feed has no newer release. The CLI (cmd/zeokeel) and a future UI action call
// it; it never starts a download itself.
func (a *app) CheckForUpdate(ctx context.Context) (*updater.Release, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	feed := a.settingString(updateFeedURLKey, "")
	if feed == "" {
		return nil, false, updater.ErrDisabled
	}
	cfg := updater.Config{
		CurrentVersion: BuildVersion,
		FeedURL:        feed,
		DataDir:        DataDir(),
	}
	check := a.updateCheck
	if check == nil {
		check = updater.Check
	}
	rel, available, err := check(ctx, cfg)
	if err != nil {
		return nil, false, err
	}
	if !available {
		return nil, false, nil
	}
	return rel, true, nil
}

// checkUpdatesOnStart reports whether update.check_on_start is enabled.
func (a *app) checkUpdatesOnStart() bool {
	return a.settingBool(updateCheckOnStartKey, false)
}

// hotkeyManager is the app's view of globalhotkey.Manager (seam for tests).
type hotkeyManager interface {
	Register(globalhotkey.Hotkey, func()) (globalhotkey.Handle, error)
	Unregister(globalhotkey.Handle) error
	Close() error
}

// syncGlobalHotkey reconciles the persisted global_hotkey setting with the
// platform registration. It never fails startup: an unsupported platform, an
// occupied shortcut or an invalid spec records hotkeyNotice and leaves the app
// running. An empty setting unregisters the current shortcut.
func (a *app) syncGlobalHotkey() error {
	spec := strings.TrimSpace(a.settingString(globalHotkeySettingKey, ""))
	a.hotkeyMu.Lock()
	defer a.hotkeyMu.Unlock()
	if spec == a.hotkeySpec && a.hotkeyManager != nil {
		return nil
	}
	// Tear the previous registration down before applying the new spec.
	if a.hotkeyManager != nil {
		if a.hotkeyHandle != nil {
			if err := a.hotkeyManager.Unregister(*a.hotkeyHandle); err != nil && !errors.Is(err, globalhotkey.ErrUnknownHandle) {
				log.Printf("zeokeel: unregister hotkey %q: %v", a.hotkeySpec, err)
			}
			a.hotkeyHandle = nil
		}
		if spec == "" || a.hotkeySpec == "" {
			if err := a.hotkeyManager.Close(); err != nil {
				log.Printf("zeokeel: close hotkey manager: %v", err)
			}
			a.hotkeyManager = nil
			a.hotkeySpec = ""
			a.hotkeyNotice = ""
			if spec == "" {
				return nil
			}
		} else {
			a.hotkeySpec = ""
		}
	}
	if spec == "" {
		a.hotkeySpec = ""
		a.hotkeyNotice = ""
		return nil
	}
	hotkey, err := globalhotkey.ParseHotkey(spec)
	if err != nil {
		a.hotkeySpec = ""
		a.hotkeyNotice = "global_hotkey " + spec + " is invalid: " + err.Error()
		log.Printf("zeokeel: %s", a.hotkeyNotice)
		return nil
	}
	manager := a.hotkeyManager
	if manager == nil {
		factory := a.newHotkeyManager
		if factory == nil {
			factory = func() (hotkeyManager, error) { return globalhotkey.New() }
		}
		created, cerr := factory()
		if cerr != nil {
			a.hotkeySpec = ""
			a.hotkeyManager = nil
			if errors.Is(cerr, globalhotkey.ErrUnsupported) {
				a.hotkeyNotice = "global hotkeys are not supported on this platform; " + globalHotkeySettingKey + " is ignored"
			} else {
				a.hotkeyNotice = "global hotkey unavailable: " + cerr.Error()
			}
			log.Printf("zeokeel: %s", a.hotkeyNotice)
			return nil
		}
		manager = created
		a.hotkeyManager = manager
	}
	action := a.showWindowFromHotkey
	if action == nil {
		action = a.showWindow
	}
	handle, rerr := manager.Register(hotkey, action)
	if rerr != nil {
		a.hotkeySpec = ""
		switch {
		case errors.Is(rerr, globalhotkey.ErrUnsupported):
			a.hotkeyNotice = "global hotkeys are not supported on this platform; " + globalHotkeySettingKey + " is ignored"
		case errors.Is(rerr, globalhotkey.ErrOccupied):
			a.hotkeyNotice = "global_hotkey " + spec + " is already used by another application"
		default:
			a.hotkeyNotice = "global_hotkey " + spec + " could not be registered: " + rerr.Error()
		}
		log.Printf("zeokeel: %s", a.hotkeyNotice)
		if closeErr := manager.Close(); closeErr != nil {
			log.Printf("zeokeel: close hotkey manager: %v", closeErr)
		}
		a.hotkeyManager = nil
		return nil
	}
	a.hotkeyHandle = &handle
	a.hotkeySpec = spec
	a.hotkeyNotice = ""
	return nil
}

// stopGlobalHotkeys unregisters the active shortcut and closes the manager. It
// is called on quit; repeated calls are safe.
func (a *app) stopGlobalHotkeys() {
	a.hotkeyMu.Lock()
	defer a.hotkeyMu.Unlock()
	manager := a.hotkeyManager
	if manager == nil {
		return
	}
	if a.hotkeyHandle != nil {
		if err := manager.Unregister(*a.hotkeyHandle); err != nil && !errors.Is(err, globalhotkey.ErrUnknownHandle) {
			log.Printf("zeokeel: unregister hotkey %q: %v", a.hotkeySpec, err)
		}
		a.hotkeyHandle = nil
	}
	if err := manager.Close(); err != nil {
		log.Printf("zeokeel: close hotkey manager: %v", err)
	}
	a.hotkeyManager = nil
	a.hotkeySpec = ""
}

// showWindow shows and focuses the main window from the hotkey dispatch
// goroutine, replaying the request if the window is not ready yet.
func (a *app) showWindow() {
	a.activationMu.Lock()
	fn := a.activateWindow
	if fn != nil {
		a.activationMu.Unlock()
		fn()
		return
	}
	a.activationPending = true
	a.activationMu.Unlock()
}
