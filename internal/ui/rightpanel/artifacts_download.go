package rightpanel

import (
	"os"
	"strings"
	"unicode"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ExportArtifactSaver is the artifact download seam: the default opens the
// native save dialog; tests replace it with a scripted path. It returns the
// chosen path ("" when the user cancelled) or a dialog error.
var ExportArtifactSaver = func(options mygo.SaveDialogOptions) (string, error) {
	return mygo.Dialog.Save(options)
}

// slugifyArtifactTitle turns an artifact title into a safe Markdown file name:
// Unicode letters and digits are kept, every other rune becomes one dash,
// leading/trailing dashes are trimmed, the result is capped at 60 runes and an
// empty slug falls back to "artifact".
func slugifyArtifactTitle(title string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if builder.Len() > 0 && !lastDash {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	runes := []rune(slug)
	if len(runes) > 60 {
		slug = strings.Trim(string(runes[:60]), "-")
	}
	if slug == "" {
		return "artifact"
	}
	return slug
}

// saveArtifact writes one artifact's Markdown body through the native save
// dialog: cancellation is a no-op, dialog/write failures land in the section's
// danger line and success shows the saved path.
func saveArtifact(st *state, runtime contracts.UIRuntime, artifact contracts.Artifact) {
	options := mygo.SaveDialogOptions{
		Title:       uikit.T(st.locale, "保存产物", "Save artifact"),
		DefaultPath: slugifyArtifactTitle(artifact.Title) + ".md",
		Filters: []mygo.FileFilter{{
			Name:       "Markdown",
			Extensions: []string{"md"},
		}},
	}
	path, err := ExportArtifactSaver(options)
	switch {
	case err != nil:
		st.artifactsErr = uikit.T(st.locale, "保存失败：", "Save failed: ") + err.Error()
		st.artifactsNotice = ""
		st.artifactsSavedPath = ""
	case strings.TrimSpace(path) == "":
		// Cancelled: nothing changes.
	default:
		if err := os.WriteFile(path, []byte(artifact.Body), 0o600); err != nil {
			st.artifactsErr = uikit.T(st.locale, "保存失败：", "Save failed: ") + err.Error()
			st.artifactsNotice = ""
			st.artifactsSavedPath = ""
		} else {
			st.artifactsErr = ""
			st.artifactsNotice = uikit.T(st.locale, "已保存", "Saved")
			st.artifactsSavedPath = path
		}
	}
	runtime.Invalidate()
}
