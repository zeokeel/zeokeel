package shell

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// PD-10: the shell remembers the last open conversation and each session's
// composer draft in the settings store, so restarting the app lands back in
// the same session with the half-written message still there.
const (
	// settingLastSession is the session id opened when the app starts. The
	// restore only opens it: no turn is submitted, no message is sent.
	settingLastSession = "ui.last_session"
	// settingDraftPrefix keys one composer draft per session.
	settingDraftPrefix = "draft."
	// draftPersistDelay is the typing debounce: a draft is written at most
	// this often while the user types, and once more when typing stops.
	draftPersistDelay = 500 * time.Millisecond
)

// draftSettingKey is the settings key of one session's composer draft.
func draftSettingKey(sessionID string) string {
	return settingDraftPrefix + sessionID
}

// persistLastSession records the session the app should reopen on the next
// start. Called when the composer's session scope settles.
func (a *App) persistLastSession(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if got := a.shellSetting(settingLastSession, ""); got == sessionID {
		return
	}
	_ = a.setShellSetting(settingLastSession, sessionID)
}

// persistDraft writes one session's composer draft now.
func (a *App) persistDraft(sessionID, text string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = a.setShellSetting(draftSettingKey(sessionID), text)
	st := a.shellUI()
	if st.draftPersisted == nil {
		st.draftPersisted = make(map[string]string)
	}
	st.draftPersisted[sessionID] = text
	st.draftPersistedAt = time.Now()
}

// draftPersistenceDue reports whether the live draft still needs a write and
// the debounce window has elapsed.
func (a *App) draftPersistenceDue() bool {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return false
	}
	id := a.Ctrl.CurrentSessionID()
	if id == "" || a.backgroundScreen() != Session {
		return false
	}
	st := a.shellUI()
	prev, ok := st.draftPersisted[id]
	if ok && prev == a.draft {
		return false
	}
	return st.draftPersistedAt.IsZero() || time.Since(st.draftPersistedAt) >= draftPersistDelay ||
		strings.TrimSpace(a.draft) == ""
}

// flushDraftPersistence writes the live draft when it is due. View calls it
// every frame and arms one delayed frame while a write is still pending, so a
// keystroke typed right before an idle frame is not lost.
func (a *App) flushDraftPersistence(c *ui.Context) {
	if !a.draftPersistenceDue() {
		return
	}
	id := a.Ctrl.CurrentSessionID()
	a.persistLastSession(id)
	a.persistDraft(id, a.draft)
	if c != nil {
		c.After(draftPersistDelay)
	}
}

// restoreSessionState runs once on the first live frame: it reopens
// ui.last_session (read-only: no submit) and restores that session's saved
// composer draft. A missing/deleted session leaves the normal Home start.
func (a *App) restoreSessionState() {
	if a.Ctrl == nil || a.startupRestored {
		return
	}
	a.startupRestored = true
	// A frame that already shows a session (the host restored one, or a test
	// seeded the snapshot) owns the screen: never override it.
	if a.Snapshot() != nil {
		return
	}
	id := strings.TrimSpace(a.shellSetting(settingLastSession, ""))
	if id == "" {
		return
	}
	snap, err := a.Ctrl.OpenSession(context.Background(), id)
	if err != nil {
		// The remembered session may have been deleted: forget it so the next
		// frame does not retry the same missing id.
		_ = a.setShellSetting(settingLastSession, "")
		return
	}
	if saved := a.shellSetting(draftSettingKey(id), ""); saved != "" {
		a.draft = saved
		if a.drafts == nil {
			a.drafts = make(map[string]string)
		}
		a.drafts[id] = saved
		a.draftInitialized = true
		a.lastDraftScreen = Session
		a.lastDraftSession = id
	}
	a.SetSnapshot(snap)
	a.Screen = Session
}
