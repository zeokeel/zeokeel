package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// AttachmentDialogTitle / AttachmentFilterName are the localized copy the
// default AttachmentPicker uses; the toolbar sets them from the current locale
// right before invoking the seam (the seam signature stays stable for tests).
var (
	AttachmentDialogTitle = "选择附件"
	AttachmentFilterName  = "文件与图片"
)

// AttachmentPicker is the composer's file-picker seam: the default opens the
// native mygo file dialog; tests replace it with a scripted selection so no
// desktop backend is needed. It returns absolute paths.
var AttachmentPicker = func() ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:    AttachmentDialogTitle,
		Multiple: true,
		Filters: []mygo.FileFilter{{
			Name: AttachmentFilterName,
			Extensions: []string{
				"png", "jpg", "jpeg", "gif", "webp",
				"txt", "md", "json", "yaml", "yml", "go", "log", "csv",
			},
		}},
	})
}

const (
	// maxAttachmentBytes is the per-file composer limit (10 MiB).
	maxAttachmentBytes = 10 << 20
	// maxThumbnailBytes bounds what is decoded for a chip preview.
	maxThumbnailBytes = 10 << 20
)

// attachmentMIME maps a file extension to the MIME type the backend receives.
func attachmentMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt", ".md", ".json", ".yaml", ".yml", ".go", ".log", ".csv":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// stageAttachmentFiles reads the picked files into pending attachments, keeping
// the first readable error for the composer to show in DangerText.
func stageAttachmentFiles(st *state, paths []string) {
	st.attachErr = ""
	for _, path := range paths {
		name := filepath.Base(path)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			st.attachErr = st.tr("无法添加 "+name+"：不是可读的普通文件", "Could not add "+name+": not a readable regular file")
			continue
		}
		if info.Size() > maxAttachmentBytes {
			st.attachErr = st.tr(fmt.Sprintf("%s 超过 10 MB 附件上限", name), fmt.Sprintf("%s exceeds the 10 MB attachment limit", name))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			st.attachErr = st.tr("无法读取 "+name+"：", "Could not read "+name+": ") + err.Error()
			continue
		}
		st.attachSeq++
		attachment := contracts.Attachment{
			ID:   fmt.Sprintf("att-%d", st.attachSeq),
			Name: name,
			MIME: attachmentMIME(path),
			Data: data,
		}
		// Decode the preview now, while the bytes are already in hand: the
		// frame builder must never decode an image.
		if bitmap, err := decodeAttachmentThumbnail(attachment); err == nil && bitmap != nil {
			if st.thumbnails == nil {
				st.thumbnails = make(map[string]*ui.Bitmap)
			}
			st.thumbnails[attachment.ID] = bitmap
		}
		st.pendingAttachments = append(st.pendingAttachments, attachment)
	}
}

// pasteClipboardImage stages the clipboard image as a composer attachment (R18):
// mygo's paste hook sees an empty text for an image, so the composer asks here
// instead of silently doing nothing. A clipboard without an image gets a
// one-line hint that the attach menu and drag-and-drop are the supported paths,
// never a fake success.
func pasteClipboardImage(st *state, runtime contracts.UIRuntime, services ui.Services) bool {
	if st.attachBusy {
		return false
	}
	data, err := services.ReadClipboardData(transfer.PNG).Read(transfer.PNG)
	if err != nil || len(data) == 0 {
		st.toast(contracts.NotifyInfo, st.tr(
			"剪贴板里没有图片；请用「附件」或拖拽添加图片",
			"No image on the clipboard; use Attach or drag an image in"))
		runtime.Invalidate()
		return true
	}
	stageClipboardImage(st, data)
	runtime.Invalidate()
	return true
}

// stageClipboardImage appends one decoded clipboard PNG to the staged
// attachments through the same rules as a picked file: the size cap, the cached
// thumbnail (decoded here, never in the frame) and the localized error line.
func stageClipboardImage(st *state, data []byte) {
	if len(data) == 0 {
		return
	}
	if len(data) > maxAttachmentBytes {
		st.attachErr = st.tr("粘贴的图片超过 10 MB 附件上限",
			"The pasted image exceeds the 10 MB attachment limit")
		return
	}
	st.attachSeq++
	attachment := contracts.Attachment{
		ID:   fmt.Sprintf("att-%d", st.attachSeq),
		Name: fmt.Sprintf("pasted-image-%d.png", st.attachSeq),
		MIME: "image/png",
		Data: data,
	}
	// Decode the preview now, while the bytes are already in hand: the frame
	// builder must never decode an image.
	if bitmap, err := decodeAttachmentThumbnail(attachment); err == nil && bitmap != nil {
		if st.thumbnails == nil {
			st.thumbnails = make(map[string]*ui.Bitmap)
		}
		st.thumbnails[attachment.ID] = bitmap
	}
	st.attachErr = ""
	st.pendingAttachments = append(st.pendingAttachments, attachment)
}

// startAttachmentPick opens the native file picker off the UI thread and
// publishes the result through UIRuntime.Post. mygo's Dialog.Open blocks its
// caller, so a synchronous call from a frame would freeze the window; the
// composer shows a busy attach action until the posted result stages the files.
// A second click while busy is ignored (the action is disabled as well).
func startAttachmentPick(runtime contracts.UIRuntime, st *state) {
	if st.attachBusy {
		return
	}
	AttachmentDialogTitle = st.tr("选择附件", "Choose attachments")
	AttachmentFilterName = st.tr("文件与图片", "Files and images")
	st.attachBusy = true
	st.attachErr = ""
	// The result is published to the conversation that opened the picker, so a
	// conversation switch while it was open cannot stage files into the wrong
	// chat.
	sessionID := st.draftSession
	runtime.Invalidate()
	go func() {
		paths, err := AttachmentPicker()
		runtime.Post(func() {
			st.attachBusy = false
			st.withComposerSession(sessionID, func() {
				if err != nil {
					st.attachErr = st.tr("无法打开文件选择器：", "Could not open the file picker: ") + err.Error()
					return
				}
				stageAttachmentFiles(st, paths)
			})
			runtime.Invalidate()
		})
	}()
}

// manualAttachmentErrors localizes the rejected entries of the manual path
// fallback, one line per entry, so a typo names the exact path.
func manualAttachmentErrors(st *state, invalid []uikit.InvalidAttachmentPath) []string {
	messages := make([]string, 0, len(invalid))
	for _, entry := range invalid {
		switch {
		case entry.Relative:
			messages = append(messages, st.tr("路径必须是绝对路径：", "Path must be absolute: ")+entry.Path)
		default:
			messages = append(messages, st.tr("文件不存在或不可读：", "File does not exist or is not readable: ")+entry.Path)
		}
	}
	return messages
}

// applyManualAttachmentPaths validates and stages one manual path-entry
// submission. Valid paths go through the ordinary staging path; invalid entries
// and staging errors stay inline in the dialog (which therefore does not close
// on a partial failure). It reports whether the dialog should close.
func applyManualAttachmentPaths(st *state, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		st.attachPathErr = st.tr("请输入至少一个文件路径", "Enter at least one file path")
		return false
	}
	valid, invalid := uikit.ResolveAttachmentPaths(raw)
	messages := manualAttachmentErrors(st, invalid)
	if len(valid) > 0 {
		before := len(st.pendingAttachments)
		stageAttachmentFiles(st, valid)
		staged := len(st.pendingAttachments) > before
		if !staged && st.attachErr == "" {
			// Defensive: the resolver said the paths exist but staging read
			// none of them, so tell the user instead of closing silently.
			messages = append(messages, st.tr("未能添加所选文件", "None of the files could be added"))
		}
		// The staging error belongs to this dialog while it is open; showing it
		// again under the composer would duplicate it.
		if st.attachErr != "" {
			messages = append(messages, st.attachErr)
			st.attachErr = ""
		}
	}
	if len(messages) == 0 {
		st.attachPathErr = ""
		return true
	}
	separator := "；"
	if st.english() {
		separator = "; "
	}
	st.attachPathErr = strings.Join(messages, separator)
	return false
}

// buildAttachmentPathDialog is the portal-less fallback (ZK-09): a native Modal
// where the user enters absolute paths directly instead of using the native
// file chooser that can be unavailable (or block) on portal-less X11. It shares
// the composer's staging path, so chips/MIME/preview behave identically.
func buildAttachmentPathDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.attachPathsOpen {
		return
	}
	add := func() {
		if applyManualAttachmentPaths(st, st.attachPathDraft) {
			st.attachPathsOpen = false
			st.attachPathDraft = ""
		}
		runtime.Invalidate()
	}
	closeDialog := func() {
		st.attachPathsOpen = false
		st.attachPathDraft = ""
		st.attachPathErr = ""
		runtime.Invalidate()
	}
	ui.Modal(c, &st.attachPathsOpen, func() {
		dialogEnter(c, add)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("手动添加附件", "Add attachments by path")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"输入文件的绝对路径，每行一个（Shift+Enter 换行）；也可以用逗号或分号分隔。",
				"Enter absolute file paths, one per line (Shift+Enter for a newline); commas or semicolons also work.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextArea(c, &st.attachPathDraft).Label(st.tr("文件路径", "File paths")).
				Placeholder("/home/you/notes.txt").FillWidth().MinHeight(design.Space6 * 3).
				OnTextKey(func(k ui.TextKey) bool {
					// Shift+Enter keeps the multiline list; a bare Enter adds.
					if k.Key != ui.KeyEnter || k.Mods != 0 {
						return false
					}
					add()
					return true
				})
			if st.attachPathErr != "" {
				buildDialogError(c, t, st.attachPathErr)
			}
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: closeDialog},
				{Label: st.tr("添加", "Add"), Primary: true,
					Disabled: strings.TrimSpace(st.attachPathDraft) == "",
					OnClick:  add},
			})
		})
	})
}

// removePendingAttachment drops one staged attachment (and its cached preview).
func removePendingAttachment(st *state, id string) {
	kept := st.pendingAttachments[:0]
	for _, attachment := range st.pendingAttachments {
		if attachment.ID == id {
			delete(st.thumbnails, attachment.ID)
			continue
		}
		kept = append(kept, attachment)
	}
	st.pendingAttachments = kept
}

// decodeAttachmentThumbnail decodes the preview bitmap of an image attachment
// once. Nil means "no preview": a non-image, empty data, or bytes the decoder
// refuses.
func decodeAttachmentThumbnail(attachment contracts.Attachment) (*ui.Bitmap, error) {
	if !attachment.IsImage() || len(attachment.Data) == 0 || len(attachment.Data) > maxThumbnailBytes {
		return nil, nil
	}
	return ui.DecodeBitmap(attachment.Data)
}

// attachmentThumbnail reads the cached preview of an image attachment; the
// bitmap was decoded when the file was staged, so this is a pure lookup.
func (st *state) attachmentThumbnail(attachment contracts.Attachment) *ui.Bitmap {
	if st.thumbnails == nil {
		return nil
	}
	return st.thumbnails[attachment.ID]
}

// formatAttachmentSize renders a chip's size in B/KB/MB.
func formatAttachmentSize(size int) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}

// buildPendingAttachments renders the staged chips (and the staging error) at
// the top of the composer card.
func buildPendingAttachments(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if len(st.pendingAttachments) == 0 && st.attachErr == "" &&
		st.commandErr == "" && !st.compacting && !st.attachBusy {
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		if st.attachBusy {
			// The picker blocks its caller, but it runs off the UI thread: the
			// composer stays live and says what it is waiting for.
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Spinner(c).Label(st.tr("选择中…", "Choosing…")).
					Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, st.tr("选择中…", "Choosing…")), t.TextMuted(design.TypeXXS))
			})
		}
		if len(st.pendingAttachments) > 0 {
			ui.Row(c).FillWidth().Wrap().Gap(design.Space1).Children(func() {
				for _, attachment := range st.pendingAttachments {
					attachment := attachment
					buildAttachmentChip(c, t, st, attachment, true, func() {
						removePendingAttachment(st, attachment.ID)
						runtime.Invalidate()
					})
				}
			})
		}
		if st.compacting {
			// A running compaction is a loading state, not a result: it shows
			// while it runs and its outcome goes to a transient toast.
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Spinner(c).Label(st.tr("压缩中…", "Compacting…")).
					Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, st.tr("压缩中…", "Compacting…")), t.TextMuted(design.TypeXXS))
			})
		}
		if st.attachErr != "" {
			buildErrorLine(c, t, st.attachErr)
		}
		if st.commandErr != "" {
			buildErrorLine(c, t, st.commandErr)
		}
	})
}

// buildMessageAttachments renders the read-only chips of a user message.
func buildMessageAttachments(c *ui.Context, t *design.Tokens, st *state, attachments []contracts.Attachment) {
	if len(attachments) == 0 {
		return
	}
	ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space1).Children(func() {
		for _, attachment := range attachments {
			buildAttachmentChip(c, t, st, attachment, false, nil)
		}
	})
}

// buildAttachmentChip is one native attachment chip: optional image preview (or
// a file glyph), the file name, its size and, while staged, a remove button.
func buildAttachmentChip(c *ui.Context, t *design.Tokens, st *state, attachment contracts.Attachment, removable bool, onRemove func()) {
	th := t.Theme()
	key := "session.attachment." + attachment.ID
	chip := design.Apply(ui.Row(c.Key(key)), t.ControlChip()).
		AlignItems(ui.Center).Shrink(0).Label(attachment.Name)
	chip.Children(func() {
		if bitmap := st.attachmentThumbnail(attachment); bitmap != nil {
			ui.Image(c, bitmap).Size(th.Space(8), th.Space(8)) // 32 DIP preview
		} else {
			// A file is a file and an image is an image: the icon is the
			// affordance, not a network globe.
			icon := icons.File()
			if attachment.IsImage() {
				icon = icons.Image()
			}
			ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
		}
		design.Apply(ui.Text(c, firstNonEmpty(attachment.Name, st.tr("未命名附件", "Untitled attachment"))), t.Font(design.TypeXXS)).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		if attachment.Size() > 0 {
			design.Apply(ui.Text(c, formatAttachmentSize(attachment.Size())), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		}
		if removable {
			// The one icon-button entry component keeps the 24 DIP minimum hit
			// target (AP-17); the old 20 DIP close button was below it (P3-10).
			uikit.IconButton(c, uikit.IconButtonOptions{
				Label:   st.tr("移除附件 ", "Remove attachment ") + attachment.Name,
				Tooltip: st.tr("移除附件", "Remove attachment"),
				Icon:    icons.Close(),
				Size:    design.IconSizeS,
				OnClick: onRemove,
			})
		}
	})
}
