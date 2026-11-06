package shell

import (
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// draftForSession returns the saved composer text for one session, creating
// the per-session map on first use. A session without a saved draft starts
// blank: text from another session is never restored into an existing
// conversation.
func (a *App) draftForSession(id string) string {
	if id == "" {
		return ""
	}
	if a.drafts == nil {
		a.drafts = make(map[string]string)
	}
	if text, ok := a.drafts[id]; ok {
		return text
	}
	// The draft may have been persisted by an earlier run (PD-10): load it
	// the first time this session's composer asks for its text.
	if saved := a.shellSetting(draftSettingKey(id), ""); saved != "" {
		a.drafts[id] = saved
		return saved
	}
	return ""
}

// saveSessionDraft stores text as one session's draft.
func (a *App) saveSessionDraft(id, text string) {
	if id == "" {
		return
	}
	if a.drafts == nil {
		a.drafts = make(map[string]string)
	}
	if text == "" {
		delete(a.drafts, id)
		return
	}
	a.drafts[id] = text
}

// transferDraftToSession seeds a brand-new session's draft with the Home
// draft. It is only called by an explicit workspace transfer (the Home
// workspace picker), never by opening an existing conversation.
func (a *App) transferDraftToSession(id string) {
	if id == "" {
		return
	}
	a.saveSessionDraft(id, a.draft)
	// The Home buffer moves rather than being copied: going Back to Home
	// starts blank, as DSH's workspace transfer does.
	a.homeDraft = ""
	a.draftInitialized = true
	a.lastDraftScreen = Session
	a.lastDraftSession = id
}

// syncDraftScope moves the live composer text between its per-surface slots
// when the screen or the current session changes. The first frame only records
// where the current text belongs, so tests and callers that set a draft before
// rendering keep it.
func (a *App) syncDraftScope() {
	if a.Ctrl == nil {
		return
	}
	// Settings is a modal over the background screen, and the composer beneath
	// it still owns the text typed there. Treat the modal as its background so
	// opening Settings cannot drop the buffer before a Cmd+N or a palette
	// jump moves it to the previous surface's own draft.
	screen := a.backgroundScreen()
	if screen != Home && screen != Session {
		return
	}
	id := ""
	if screen == Session {
		id = strings.TrimSpace(a.Ctrl.CurrentSessionID())
	}
	if !a.draftInitialized {
		a.draftInitialized = true
		a.lastDraftScreen = screen
		a.lastDraftSession = id
		return
	}
	if screen == a.lastDraftScreen && id == a.lastDraftSession {
		return
	}
	// Save the buffer where it was typed.
	switch a.lastDraftScreen {
	case Home:
		a.homeDraft = a.draft
	case Session:
		a.saveSessionDraft(a.lastDraftSession, a.draft)
		a.persistDraft(a.lastDraftSession, a.draft)
	}
	// Restore the buffer of the surface now shown.
	switch screen {
	case Home:
		a.draft = a.homeDraft
	case Session:
		a.draft = a.draftForSession(id)
	default:
		a.draft = ""
	}
	a.lastDraftScreen = screen
	a.lastDraftSession = id
}

// draftOwnerScreen is the surface the live buffer belongs to. Settings is a
// modal over the background screen; the last recorded surface is the most
// reliable owner when the modal is open (and matches backgroundScreen when a
// caller set Screen=Settings directly without opening it).
func (a *App) draftOwnerScreen() Screen {
	if a.draftInitialized {
		switch a.lastDraftScreen {
		case Home, Session:
			return a.lastDraftScreen
		}
	}
	return a.backgroundScreen()
}

// draftOwnerSessionID is the session the current buffer was last recorded
// against. The controller can already point at another session when a UI
// transition begins (a caller opened it first), so the recorded owner is safer
// than the controller's current id.
func (a *App) draftOwnerSessionID() string {
	if a.Ctrl == nil {
		return ""
	}
	if a.draftInitialized && a.lastDraftScreen == Session && a.lastDraftSession != "" {
		return a.lastDraftSession
	}
	return a.Ctrl.CurrentSessionID()
}

// moveDraftToSession saves the composer buffer of the surface it was typed on
// and loads the target session's own buffer. Transitions call it immediately,
// so even a caller that never rendered another frame cannot carry text into a
// different conversation.
func (a *App) moveDraftToSession(oldScreen Screen, oldID, newID string) {
	if a.Ctrl == nil {
		return
	}
	switch oldScreen {
	case Home:
		a.homeDraft = a.draft
	case Session:
		// Only a recorded owner is trustworthy. Without a frame the
		// controller may already point at the target, and saving there would
		// attach the old text to the new conversation.
		if a.draftInitialized {
			a.saveSessionDraft(oldID, a.draft)
		}
	}
	a.draft = a.draftForSession(newID)
	a.draftInitialized = true
	a.lastDraftScreen = Session
	a.lastDraftSession = newID
}

// homeAgentMode is the mode the Home chip shows and the next Home-created
// session starts in: the staged choice, else the persisted new-session preset.
// Both are canonicalized so an alias stored in agent.preset (for example
// "cordis") is displayed and staged as the mode the new session will use.
func (a *App) homeAgentMode() string {
	if a.Ctrl == nil {
		return runtime.ModeStandard
	}
	if m := strings.TrimSpace(a.homeMode); m != "" {
		if mode, err := runtime.NormalizeMode(m); err == nil {
			return mode
		}
	}
	if m := strings.TrimSpace(a.agentPresetDefault()); m != "" {
		return m
	}
	return runtime.ModeStandard
}

// homePermissionMode is the access mode the Home composer shows and applies to
// the next session it creates. It falls back to the General setting, which is
// the future-session default, without writing that setting from the composer.
func (a *App) homePermissionMode() string {
	if m := strings.TrimSpace(a.homePermission); m != "" {
		return m
	}
	return a.currentSettings().Permission
}

// homeModelID is the model the Home composer stages for the next session.
func (a *App) homeModelID() string {
	if id := strings.TrimSpace(a.homeModel); id != "" {
		return id
	}
	if a.Ctrl != nil {
		if id, err := a.Ctrl.Models().DefaultID(); err == nil {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// applyHomeComposerChoices writes the Home composer's staged mode, access,
// model and reasoning effort onto a freshly created session. It touches only
// that new session.
func (a *App) applyHomeComposerChoices(sessionID string) {
	if a.Ctrl == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	mode := a.homeAgentMode()
	if mode != "" {
		if err := a.Ctrl.SetMode(mode); err != nil {
			a.modeUI().err = err.Error()
		} else {
			a.modeUI().err = ""
		}
	}
	if perm := strings.TrimSpace(a.homePermission); perm != "" {
		a.Ctrl.SetSessionPermission(sessionID, perm)
	}
	if model := strings.TrimSpace(a.homeModel); model != "" {
		_ = a.Ctrl.SetSessionModel(sessionID, model)
	}
	// After the model: switching models clears an explicit effort, and the
	// staged Home choice is the new session's explicit level.
	if effort := strings.TrimSpace(a.homeEffort); effort != "" {
		_ = a.Ctrl.SetSessionReasoningEffort(sessionID, effort)
	}
}
