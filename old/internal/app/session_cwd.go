package app

import (
	"log"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// sessionActivated keeps each session's working directory stable while the
// runtime agent (and the WorkingDirectoryState it shares across a workspace) is
// common to every session. When the current session changes, the previous
// session's cwd is remembered and the new session starts at its own saved
// directory, or at the workspace root when it has none.
//
// This is the smallest fix for the "AI is not in the workspace" report: a new
// session used to inherit whatever subdirectory the last session had entered
// (working_directory's state is keyed by workspace root), so the prompt's
// "Current working directory" and every relative tool path drifted out of the
// workspace root. DSH binds the working directory per session; ZeoKeel has one
// shared agent per workspace, so this restore-on-activation approach is the
// closest match without making every tool session-aware.
//
// It is registered through Controller.SetOnSessionActivated, which fires when a
// session becomes current via create/open (not for every turn), so a running
// turn's cwd cannot be changed underneath it.
func (a *app) sessionActivated(previousSessionID, sessionID string) {
	if a == nil || a.services == nil {
		return
	}
	a.dirMu.Lock()
	defer a.dirMu.Unlock()
	if a.dirStates == nil {
		// The agent has not been built for a workspace yet; the first build
		// creates the state at the workspace root.
		return
	}
	if previousSessionID != "" {
		if root, ok := a.sessionWorkspaceRoot(previousSessionID); ok {
			if state := a.dirStates[filepath.Clean(root)]; state != nil {
				if a.sessionCWDs == nil {
					a.sessionCWDs = make(map[string]string)
				}
				a.sessionCWDs[previousSessionID] = state.Rel()
			}
		}
	}
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	root, ok := a.sessionWorkspaceRoot(sessionID)
	if !ok {
		return
	}
	state := a.dirStates[filepath.Clean(root)]
	if state == nil {
		return
	}
	if err := state.SetRel(a.sessionCWDs[sessionID]); err != nil {
		log.Printf("zeokeel: session %s: restore working directory: %v", sessionID, err)
	}
}

// sessionWorkspaceRoot resolves the effective workspace root of a stored
// session. It mirrors rebuildAgent: a workspace with an empty root uses the app
// data directory, which is the root the agent was built for.
func (a *app) sessionWorkspaceRoot(sessionID string) (string, bool) {
	if a.services == nil {
		return "", false
	}
	st := a.services.Store()
	if st == nil {
		return "", false
	}
	sess, err := st.GetSession(sessionID)
	if err != nil || sess == nil {
		return "", false
	}
	return workspaceRootForSession(st, sess)
}

// workspaceRootForSession resolves a session's workspace root with the same
// empty-root fallback the app's agent build uses.
func workspaceRootForSession(st *store.Store, sess *store.Session) (string, bool) {
	if st == nil || sess == nil || strings.TrimSpace(sess.WorkspaceID) == "" {
		return "", false
	}
	ws, err := st.GetWorkspace(sess.WorkspaceID)
	if err != nil || ws == nil {
		return "", false
	}
	if root := strings.TrimSpace(ws.Root); root != "" {
		return root, true
	}
	return DataDir(), true
}
