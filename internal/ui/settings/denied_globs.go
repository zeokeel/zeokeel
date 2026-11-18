package settings

// Opt-in denied-globs field of the Data & privacy page (2026-11 audit RG-10).
// The toolkit reads toolkit.deniedGlobs at activation (a JSON string list) and
// hides the matching files from the FILE tools only. This is defense in depth
// for the model's file surface, NOT a security boundary: the shell bypass remains
// (bash/pwsh can still cat a denied file). The default is empty (opt-in) — a
// personal-scale agent gets no friction until the user asks for it. The UI edits
// the list as comma-separated globs and encodes the JSON list on save; a change
// applies on the next app restart (the toolkit resolves it once at activation).

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// The settings row is contracts.SettingToolkitDeniedGlobs (the key constant
// lives in contracts/settingskeys.go, shared with the toolkit consumer). The
// UI edits it as comma-separated globs and encodes the JSON list on save.

// buildDeniedGlobsSection renders the opt-in file-hiding field: one TextInput
// bound to the page's draft, with the disclosure that the default empty hides
// nothing, the shell bypass remains, and a change applies after a restart.
func buildDeniedGlobsSection(c *ui.Context, t *design.Tokens, store contracts.SettingsService,
	st *dataPrivacyState, locale contracts.LocaleService) {
	uikit.Section(c, localized(locale, "文件隐藏（高级）", "File hiding (advanced)"), func(*ui.Context) {
		if store == nil {
			uikit.EmptyState(c, localized(locale,
				"没有可用的设置存储，无法保存文件隐藏规则。",
				"No settings store is available, so file-hiding rules cannot be saved."))
			return
		}
		if !st.globsLoaded {
			st.globsDraft = deniedGlobsDisplay(store.Get(contracts.SettingToolkitDeniedGlobs, ""))
			st.globsLoaded = true
		}
		ui.Field(c, localized(locale, "隐藏的文件（glob）", "Hidden files (globs)"), func() {
			ui.TextInput(c, &st.globsDraft).
				Label(inputLabel(locale, localized(locale, "隐藏的文件", "Hidden files"))).
				Placeholder("**/.env, **/*.pem, **/*.key").Width(controlWidth)
		}).Description(localized(locale,
			"逗号分隔的 glob，匹配的文件对文件类工具（read_file/glob/grep/编辑/图片/LSP）隐藏。默认留空＝不隐藏任何文件；shell 旁路仍在（bash/pwsh 仍可读取这些文件），这不是安全边界。修改后重启应用生效。",
			"Comma-separated globs; matching files are hidden from the file tools (read_file/glob/grep/editor/image/LSP). Empty (the default) hides nothing; the shell bypass remains (bash/pwsh can still read these files), so this is not a security boundary. A change applies after restarting the app."))
		statusBadgesRow(c, t, st.globsStatus, st.globsStatusErr)
	})
}

// deniedGlobsDisplay renders the stored JSON list as the comma-separated draft.
// A malformed row falls back to the raw text so the user can see and fix it.
func deniedGlobsDisplay(raw string) string {
	list, err := contracts.ParseStringListJSON(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return strings.Join(normalizeGlobs(list), ", ")
}

// deniedGlobsDraftList splits the draft on commas/newlines into trimmed globs,
// dropping blanks.
func deniedGlobsDraftList(draft string) []string {
	parts := strings.FieldsFunc(draft, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	return normalizeGlobs(parts)
}

// normalizeGlobs trims each glob and drops the empty ones.
func normalizeGlobs(list []string) []string {
	out := make([]string, 0, len(list))
	for _, glob := range list {
		if trimmed := strings.TrimSpace(glob); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// deniedGlobsDirty reports whether the draft differs from the stored list.
func deniedGlobsDirty(st *dataPrivacyState, store contracts.SettingsService) bool {
	if store == nil {
		return false
	}
	stored, _ := contracts.ParseStringListJSON(store.Get(contracts.SettingToolkitDeniedGlobs, ""))
	return !equalGlobs(normalizeGlobs(stored), deniedGlobsDraftList(st.globsDraft))
}

// equalGlobs reports whether two glob lists carry the same entries in order.
func equalGlobs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// saveDeniedGlobs encodes the draft as the JSON list the toolkit reads and
// writes it; the status line reminds the user a restart applies the change.
func saveDeniedGlobs(st *dataPrivacyState, store contracts.SettingsService, locale contracts.LocaleService) {
	if store == nil {
		return
	}
	list := deniedGlobsDraftList(st.globsDraft)
	encoded, err := contracts.EncodeStringListJSON(list)
	if err != nil {
		st.globsStatus = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.globsStatusErr = true
		return
	}
	if err := store.Set(contracts.SettingToolkitDeniedGlobs, encoded); err != nil {
		st.globsStatus = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		st.globsStatusErr = true
		return
	}
	st.globsDraft = strings.Join(list, ", ")
	st.globsStatus = localized(locale, "已保存（重启应用后生效）", "Saved (applies after restarting the app)")
	st.globsStatusErr = false
}

// discardDeniedGlobs re-reads the stored list into the draft and clears the
// save status.
func discardDeniedGlobs(st *dataPrivacyState, store contracts.SettingsService) {
	st.globsLoaded = true
	st.globsDraft = ""
	if store != nil {
		st.globsDraft = deniedGlobsDisplay(store.Get(contracts.SettingToolkitDeniedGlobs, ""))
	}
	st.globsStatus = ""
	st.globsStatusErr = false
}
