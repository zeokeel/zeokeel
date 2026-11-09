package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Import page constants. Section "general" matches About (90) and 更新 (92);
// Order 94 keeps this page after both, so the default page stays About.
const (
	ImportPageID      = "import"
	ImportPageSection = "高级"
	ImportPageOrder   = 30
	ImportPageTitle   = "导入"
)

// ImportDirectoryPicker is the DSH data-directory seam: the default opens the
// native mygo directory dialog with a localized title; tests replace it with a
// scripted selection.
var ImportDirectoryPicker = func(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     title,
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// importState is the Import page's UI state: the chosen directory, the scan
// preview/error and the import result/error.
type importState struct {
	dir string

	scanning  bool
	importing bool

	preview *contracts.ImportPreview
	scanErr string
	dirErr  string

	result    *contracts.ImportResult
	importErr string
}

func newImportState() *importState { return &importState{} }

// buildImportPage renders the page: the source directory row (native input +
// browse seam + scan), the read-only scan preview and the import action with
// its result. No progress percentages: the service reports counts only.
func buildImportPage(c *ui.Context, t *design.Tokens, kctx cordis.Context, runtime contracts.UIRuntime,
	st *importState, locale contracts.LocaleService) {
	importer, hasImporter := kctx.Get(contracts.KeyImporter)

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "导入", "Import"), localized(locale,
			"从 DSH 桌面版把会话和工作区搬过来：选好它的数据目录，先扫描预览，确认后再导入。扫描不会改动任何数据。",
			"Bring your sessions and workspaces over from DSH desktop: pick its data directory, scan to preview, then import. Scanning changes nothing."))
		if !hasImporter {
			uikit.EmptyState(c, localized(locale,
				"导入服务未启用", "Import service is not enabled"))
			return
		}

		uikit.Section(c, localized(locale, "数据来源", "Source"), func(*ui.Context) {
			ui.Field(c, localized(locale, "DSH 数据目录", "DSH data directory"), func() {
				ui.TextInput(c, &st.dir).
					Label(inputLabel(locale, localized(locale, "DSH 数据目录", "DSH data directory"))).
					Placeholder("~/.dsh").FillWidth()
			}).Description(localized(locale,
				"DSH 桌面版的数据目录；先扫描预览，再导入。",
				"The DSH desktop data directory; scan to preview, then import."))
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if design.Apply(ui.Button(c, localized(locale, "浏览…", "Browse…")),
					t.SecondaryButton()).Tooltip(localized(locale,
					"选择 DSH 数据目录", "Choose the DSH data directory")).Clicked() {
					dir, err := ImportDirectoryPicker(localized(locale,
						"选择 DSH 数据目录", "Choose the DSH data directory"))
					switch {
					case err != nil:
						st.dirErr = err.Error()
					case strings.TrimSpace(dir) != "":
						st.dir = dir
						st.dirErr = ""
						st.preview = nil
						st.result = nil
						st.scanErr = ""
						st.importErr = ""
					}
					runtime.Invalidate()
				}
				scanLabel := localized(locale, "扫描", "Scan")
				if st.scanning {
					scanLabel = localized(locale, "扫描中…", "Scanning…")
				}
				// H24: with a directory chosen, scanning is the obvious next
				// step, so it becomes the section's accent primary action.
				dirChosen := strings.TrimSpace(st.dir) != ""
				var scanButton ui.Element
				var scanStyle design.Style
				if dirChosen {
					scanButton = ui.PrimaryButton(c, scanLabel)
					scanStyle = t.PrimaryButton()
				} else {
					scanButton = ui.Button(c, scanLabel)
					scanStyle = t.SecondaryButton()
				}
				scan := design.Apply(scanButton, scanStyle).
					Tooltip(localized(locale,
						"只读扫描该目录，不写入任何数据",
						"Scan the directory read-only; nothing is written"))
				scan.Disabled(st.scanning || st.importing || strings.TrimSpace(st.dir) == "")
				if scan.Clicked() && !st.scanning && !st.importing {
					startImportScan(importer, runtime, st)
				}
			})
			if st.dirErr != "" {
				uikit.ErrorLine(c, localized(locale, "无法打开目录选择器：",
					"Could not open the directory picker: ")+st.dirErr)
			}
			if st.scanErr != "" {
				uikit.ErrorLine(c, localized(locale, "扫描失败：", "Scan failed: ")+st.scanErr)
			}
			if st.preview == nil && st.result == nil && st.dirErr == "" && st.scanErr == "" &&
				!st.scanning && !st.importing {
				uikit.EmptyStateBlock(c,
					localized(locale, "还没有可导入的数据", "Nothing to import yet"),
					localized(locale, "选择一个 DSH 数据目录，先「扫描」预览，再「导入数据」。",
						"Choose a DSH data directory, Scan to preview, then Import data."),
					nil)
			}
		})

		if st.preview != nil {
			buildImportPreview(c, t, importer, runtime, st, locale)
		}
		if st.importErr != "" {
			uikit.ErrorLine(c, localized(locale, "导入失败：", "Import failed: ")+st.importErr)
		}
		if st.result != nil {
			buildImportResult(c, t, *st.result, locale)
		}
	})
}

// buildImportPreview renders the scan counts, the findings notes and the
// import action (which only exists once a preview did).
func buildImportPreview(c *ui.Context, t *design.Tokens, importer contracts.ImporterService,
	runtime contracts.UIRuntime, st *importState, locale contracts.LocaleService) {
	preview := *st.preview
	uikit.Section(c, localized(locale, "扫描结果", "Scan result"), func(*ui.Context) {
		importStatRow(c, t, localized(locale, "会话", "Sessions"), preview.Sessions)
		importStatRow(c, t, localized(locale, "消息", "Messages"), preview.Messages)
		importStatRow(c, t, localized(locale, "工作区", "Workspaces"), preview.Workspaces)
		importStatRow(c, t, localized(locale, "跳过", "Skipped"), preview.Skipped)
		for _, note := range preview.Notes {
			text := design.Apply(ui.Text(c, note), t.Font(design.TypeXXS))
			if importNoteIsSkip(note) {
				text.TextColor(t.DangerText)
			} else {
				text.TextColor(t.LabelTertiary)
			}
		}
		importLabel := localized(locale, "导入数据", "Import data")
		if st.importing {
			importLabel = localized(locale, "导入中…", "Importing…")
		}
		uikit.DialogFooter(c, []uikit.DialogAction{{
			Label: importLabel, Primary: true,
			Disabled: st.importing || st.scanning,
			OnClick: func() {
				if !st.importing && !st.scanning {
					startImportData(importer, runtime, st)
					c.Invalidate()
				}
			},
		}})
	})
}

// buildImportResult renders what the import run actually wrote.
func buildImportResult(c *ui.Context, t *design.Tokens, result contracts.ImportResult,
	locale contracts.LocaleService) {
	uikit.Section(c, localized(locale, "导入结果", "Import result"), func(*ui.Context) {
		importStatRow(c, t, localized(locale, "导入会话", "Imported sessions"), result.ImportedSessions)
		importStatRow(c, t, localized(locale, "导入消息", "Imported messages"), result.ImportedMessages)
		importStatRow(c, t, localized(locale, "导入工作区", "Imported workspaces"), result.ImportedWorkspaces)
		importStatRow(c, t, localized(locale, "跳过", "Skipped"), result.Skipped)
		if len(result.Errors) == 0 {
			design.Apply(ui.Text(c, localized(locale, "没有错误", "No errors")),
				t.TextMuted(design.TypeXXS))
			return
		}
		for _, entry := range result.Errors {
			uikit.ErrorLine(c, entry)
		}
	})
}

// importStatRow is one label + count badge row.
func importStatRow(c *ui.Context, t *design.Tokens, label string, count int) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
		ui.Spacer(c).Grow(1)
		design.Apply(ui.Badge(c, fmt.Sprintf("%d", count)), t.Badge())
	})
}

// importNoteIsSkip reports whether a scan note describes a skipped entry, so
// the page can surface it in DangerText.
func importNoteIsSkip(note string) bool {
	lower := strings.ToLower(note)
	for _, needle := range []string{"skip", "跳过", "unsupported", "不支持", "corrupt", "损坏", "无法"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// startImportScan runs one read-only scan off the UI thread and publishes the
// preview through UIRuntime.Post.
func startImportScan(importer contracts.ImporterService, runtime contracts.UIRuntime, st *importState) {
	st.scanning = true
	st.scanErr = ""
	st.preview = nil
	st.importErr = ""
	st.result = nil
	dir := strings.TrimSpace(st.dir)
	runtime.Invalidate()
	go func() {
		preview, err := importer.Scan(context.Background(), dir)
		runtime.Post(func() {
			st.scanning = false
			if err != nil {
				st.scanErr = err.Error()
			} else {
				st.preview = &preview
				st.scanErr = ""
			}
			runtime.Invalidate()
		})
	}()
}

// startImportData runs the import off the UI thread and publishes the result.
func startImportData(importer contracts.ImporterService, runtime contracts.UIRuntime, st *importState) {
	st.importing = true
	st.importErr = ""
	st.result = nil
	dir := strings.TrimSpace(st.dir)
	runtime.Invalidate()
	go func() {
		result, err := importer.Import(context.Background(), dir)
		runtime.Post(func() {
			st.importing = false
			if err != nil {
				st.importErr = err.Error()
			} else {
				st.result = &result
				st.importErr = ""
			}
			runtime.Invalidate()
		})
	}()
}
