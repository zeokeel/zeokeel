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
				dirInput := ui.TextInput(c, &st.dir).
					Label(inputLabel(locale, localized(locale, "DSH 数据目录", "DSH data directory"))).
					Placeholder("~/.dsh").FillWidth()
				if dirInput.Changed() {
					// Editing the path invalidates the previous scan: never show a
					// preview (or an import action) for another directory.
					st.preview = nil
					st.result = nil
					st.scanErr = ""
					st.dirErr = ""
					st.importErr = ""
				}
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
				busyLabel := localized(locale, "扫描中…", "Scanning…")
				if st.scanning {
					scanLabel = busyLabel
				}
				// The busy state is the official spinner with an accessible
				// progress name; the scan button keeps its disabled logic.
				buildBusySpinner(c, st.scanning, busyLabel)
				// H24: with a directory chosen, scanning is the obvious next
				// step, so it becomes the section's accent primary action.
				dirChosen := strings.TrimSpace(st.dir) != ""
				var scanButton ui.Element
				var scanStyle design.Style
				// Scan is the accent action only until a preview exists; after that
				// the section's import button is the page's one primary.
				if dirChosen && st.preview == nil && st.result == nil {
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
			// The source section has three distinct states, each naming its own
			// next step (R13): no directory chosen, a directory chosen but not
			// scanned, and a finished scan with nothing importable. The old
			// single "还没有可导入的数据" showed the last one even before a scan,
			// which read as "this directory is wrong".
			dirChosen := strings.TrimSpace(st.dir) != ""
			switch {
			case st.preview == nil && st.result == nil && st.dirErr == "" && st.scanErr == "" &&
				!st.scanning && !st.importing && !dirChosen:
				uikit.EmptyStateBlock(c,
					localized(locale, "选择 DSH 数据目录", "Choose a DSH data directory"),
					localized(locale, "选择 DSH 桌面版的数据目录后，点「扫描」预览；扫描只读，不改动任何数据。",
						"Choose the DSH desktop data directory, then Scan to preview; scanning is read-only."),
					nil)
			case st.preview == nil && st.result == nil && st.dirErr == "" && st.scanErr == "" &&
				!st.scanning && !st.importing && dirChosen:
				uikit.EmptyStateBlock(c,
					localized(locale, "点「扫描」预览", "Scan to preview"),
					localized(locale, "「扫描」只读，不会改动数据；确认预览后再点「导入数据」。",
						"Scan is read-only; review the preview, then Import data."),
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
		if preview.Sessions+preview.Messages+preview.Workspaces == 0 {
			// A finished scan with no importable rows is its own state: explain it
			// and keep the import action out of reach (there is nothing to write).
			uikit.EmptyStateBlock(c,
				localized(locale, "这个目录没有可导入的数据", "This directory has nothing to import"),
				localized(locale, "没有发现 DSH 的会话、消息或工作区；确认选择的是 DSH 桌面版的数据目录。",
					"No DSH sessions, messages or workspaces were found; check that this is the DSH desktop data directory."),
				nil)
			for _, note := range preview.Notes {
				text := design.Apply(ui.Text(c, note), t.Font(design.TypeXXS))
				if importNoteIsSkip(note) {
					text.TextColor(t.DangerText)
				} else {
					text.TextColor(t.LabelTertiary)
				}
			}
			return
		}
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
		busyLabel := localized(locale, "导入中…", "Importing…")
		if st.importing {
			importLabel = busyLabel
		}
		buildBusyPrimaryAction(c, t, st.importing || st.scanning, importLabel, busyLabel,
			st.importing || st.scanning, func() {
				if !st.importing && !st.scanning {
					startImportData(importer, runtime, st)
					c.Invalidate()
				}
			})
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
