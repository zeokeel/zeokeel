package shell

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

// filesRootTooltip is the hover text of the Files header's root segment and of
// the Start page's workspace-files entry: the absolute workspace root, so two
// workspaces that share a display name stay distinguishable.
func (a *App) filesRootTooltip() string {
	return a.filesRoot()
}

// invalidateFilesDirCache drops the cached directory listing so the next frame
// re-reads the disk. Reload calls it directly; navigation and session switches
// reset the whole Files state instead (resetFiles).
func (a *App) invalidateFilesDirCache() {
	panel := a.rightPanelUI()
	panel.dirCachePath = ""
	panel.dirCacheEntries = nil
	panel.dirCacheTotal = 0
	panel.dirCacheErr = nil
	panel.dirCacheAt = time.Time{}
}

// filesDirEntries lists one directory for the Files tab: directories first,
// then by natural case-insensitive name, capped at maxFilesEntries. The listing
// is cached for filesDirCacheTTL so the panel does not read the disk on every
// frame; Reload, a directory switch (another path) or a session switch
// (resetFiles) drops it. An error is cached for the same short window so a
// broken directory does not spin the disk either; Reload retries it.
func (a *App) filesDirEntries(dir string) ([]os.DirEntry, int, error) {
	panel := a.rightPanelUI()
	if panel.dirCachePath == dir && time.Since(panel.dirCacheAt) < filesDirCacheTTL {
		return panel.dirCacheEntries, panel.dirCacheTotal, panel.dirCacheErr
	}
	entries, err := os.ReadDir(dir)
	panel.dirCachePath = dir
	panel.dirCacheAt = time.Now()
	if err != nil {
		panel.dirCacheEntries = nil
		panel.dirCacheTotal = 0
		panel.dirCacheErr = err
		return nil, 0, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return naturalCompare(entries[i].Name(), entries[j].Name()) < 0
	})
	total := len(entries)
	if total > maxFilesEntries {
		entries = entries[:maxFilesEntries]
	}
	panel.dirCacheEntries = entries
	panel.dirCacheTotal = total
	panel.dirCacheErr = nil
	return entries, total, nil
}

// naturalCompare orders file names the way a file manager does: digit runs
// compare by numeric value (so "a2" sorts before "a10"), letters compare
// case-insensitively, and the raw name breaks ties so equal-looking names keep
// a stable order.
func naturalCompare(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, aj := digitRun(a, i)
		bi, bj := digitRun(b, j)
		if ai && bi {
			an, bn := a[i:aj], b[j:bj]
			if c := compareNumericRun(an, bn); c != 0 {
				return c
			}
			i, j = aj, bj
			continue
		}
		ca, cb := lowerASCII(a[i]), lowerASCII(b[j])
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case i == len(a) && j == len(b):
		return strings.Compare(a, b)
	case i == len(a):
		return -1
	default:
		return 1
	}
}

// digitRun reports whether s[i] starts a run of ASCII digits and where that
// run ends.
func digitRun(s string, i int) (bool, int) {
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return false, i
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return true, j
}

// compareNumericRun compares two ASCII digit runs by value without parsing
// them into integers: leading zeros are insignificant, then more significant
// digits mean a larger number, then the digits compare lexicographically.
func compareNumericRun(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// lowerASCII lowercases an ASCII byte and leaves every other byte unchanged, so
// non-ASCII names keep their UTF-8 order.
func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
