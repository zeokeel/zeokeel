package shell

import (
	"os"
	"path/filepath"
	"strings"
)

// viewedSessionID is the session the screen shows: the snapshot's SessionID
// wins, because a background submit can move the controller's current session
// underneath the view. The session log panel uses the same precedence
// (start_tab.go: sessionID := Ctrl.CurrentSessionID() then the snapshot).
func (a *App) viewedSessionID() string {
	if snap := a.Snapshot(); snap != nil && strings.TrimSpace(snap.SessionID) != "" {
		return strings.TrimSpace(snap.SessionID)
	}
	if a.Ctrl == nil {
		return ""
	}
	return strings.TrimSpace(a.Ctrl.CurrentSessionID())
}

// viewedSessionWorkspaceRoot resolves the workspace root of the session the
// screen shows. A workspace without a root falls back to the data directory,
// the same root the app builds the agent for, so the tree and the tools always
// agree on the workspace.
func (a *App) viewedSessionWorkspaceRoot() (string, bool) {
	id := a.viewedSessionID()
	if id == "" || a.Ctrl == nil || a.Ctrl.Store() == nil {
		return "", false
	}
	sess, err := a.Ctrl.Store().GetSession(id)
	if err != nil || sess == nil || strings.TrimSpace(sess.WorkspaceID) == "" {
		return "", false
	}
	ws, err := a.Ctrl.Store().GetWorkspace(sess.WorkspaceID)
	if err != nil || ws == nil {
		return "", false
	}
	if root := strings.TrimSpace(ws.Root); root != "" {
		return root, true
	}
	return filesDataDirFallback(), true
}

// filesDataDirFallback mirrors app.DataDir for a workspace whose root is
// empty: the data directory is where the app's agent is built in that case.
// The shell cannot import internal/app (app imports shell), so the few lines
// live here.
func filesDataDirFallback() string {
	if dir := os.Getenv("ZEOKEEL_DATA_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".zeokeel")
	}
	return filepath.Join(home, ".zeokeel")
}

// filesRoot is the Files tab's tree root: the workspace of the session on
// screen. On Home, or before a session opens, the selected workspace is the
// best answer and keeps the pre-session Files panel working.
func (a *App) filesRoot() string {
	if root, ok := a.viewedSessionWorkspaceRoot(); ok {
		return root
	}
	return a.workspaceRoot()
}

// ensureFilesSession resets the Files tab's navigation when the session on
// screen changes: the directory, the preview and a transient read error belong
// to the session they were opened in, so another session starts at its own
// workspace root instead of inheriting a path that may not exist there.
//
// The first frame only records the session: the panel may have been rendered
// on another tab first, and opening Files for the first time must not throw
// away a navigation a frame already set up.
func (a *App) ensureFilesSession() {
	st := a.rightPanelUI()
	id := a.viewedSessionID()
	if !st.filesSessionSet {
		st.filesSessionSet = true
		st.filesSessionID = id
		return
	}
	if st.filesSessionID != id {
		a.resetFiles()
		st.filesSessionID = id
		st.nativeErr = ""
	}
}

// artifactPreviewRel resolves a presented artifact's path to a
// workspace-relative path under the shown session's workspace. Absolute paths
// outside the workspace and relative paths that escape it are not previewable:
// the Files panel reads only inside the workspace, so the caller shows the
// outside-workspace explanation instead of opening the file.
func (a *App) artifactPreviewRel(path string) (string, bool) {
	p := strings.TrimSpace(path)
	if p == "" || a.Ctrl == nil {
		return "", false
	}
	rootAbs, err := filepath.Abs(a.filesRoot())
	if err != nil {
		return "", false
	}
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, filepath.FromSlash(target))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// openArtifactPreview opens a presented artifact in the Files tab: the same
// sidebar preview a tree row opens. Paths the workspace does not contain are
// not read; the panel shows the outside-workspace explanation instead, so the
// card never pretends a file is previewable when it is not.
func (a *App) openArtifactPreview(path string) {
	p := strings.TrimSpace(path)
	if p == "" {
		return
	}
	a.ensureFilesSession()
	a.rightOpen = true
	a.rightTab = filesTab
	if rel, ok := a.artifactPreviewRel(p); ok {
		a.openFilePreview(rel)
		return
	}
	a.filePath = filepath.Clean(filepath.FromSlash(p))
	a.fileText = ""
	a.fileTrunc = false
	a.fileLimited = false
	a.fileErr = a.T("files.that_path_is_outside_the_workspace")
	a.rightPanelUI().nativeErr = ""
}
