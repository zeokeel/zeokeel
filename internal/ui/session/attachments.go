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
					// There is no manual path entry to fall back to: say why the
					// picker failed and name the paths that still work (drag and
					// drop, pasting an image), instead of asking for a typed path.
					st.attachErr = st.tr("无法打开文件选择器：", "Could not open the file picker: ") + err.Error() +
						st.tr("；请拖入文件或粘贴图片", "; drag files in or paste an image instead")
					return
				}
				stageAttachmentFiles(st, paths)
			})
			runtime.Invalidate()
		})
	}()
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
			// The staged images of this composer are one gallery: previewing one
			// pages through the others before they are sent.
			stagedImages := imageAttachments(st.pendingAttachments)
			stagedIndex := 0
			ui.Row(c).FillWidth().Wrap().Gap(design.Space1).Children(func() {
				for _, attachment := range st.pendingAttachments {
					attachment := attachment
					var onPreview func()
					if attachment.IsImage() {
						imageIndex := stagedIndex
						stagedIndex++
						onPreview = func() {
							st.openAttachmentGallery(stagedImages, imageIndex)
							runtime.Invalidate()
						}
					}
					buildAttachmentChip(c, t, st, attachment, true, func() {
						removePendingAttachment(st, attachment.ID)
						runtime.Invalidate()
					}, onPreview)
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

// buildMessageAttachments renders the read-only chips of a user message. An
// image chip is a button that opens the message's image gallery (P1), so the
// reader can see the original behind the 32 DIP thumbnail; a non-image chip
// stays inert.
func buildMessageAttachments(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, attachments []contracts.Attachment) {
	if len(attachments) == 0 {
		return
	}
	images := imageAttachments(attachments)
	imageIndex := 0
	ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space1).Children(func() {
		for _, attachment := range attachments {
			attachment := attachment
			var onPreview func()
			if attachment.IsImage() {
				index := imageIndex
				imageIndex++
				onPreview = func() {
					st.openAttachmentGallery(images, index)
					runtime.Invalidate()
				}
			}
			buildAttachmentChip(c, t, st, attachment, false, nil, onPreview)
		}
	})
}

// buildAttachmentChip is one native attachment chip: optional image preview (or
// a file glyph), the file name, its size and, while staged, the preview (images
// only) and remove actions. A message's image chip is a plain ui.Button: the
// whole chip opens the image gallery, and mygo keeps the native hover, pressed,
// focus and keyboard states instead of a hand-written click surface. The staged
// and non-image chips keep the quiet BgStrong fill with no outline (P2-12);
// their actions are the shared icon buttons (24 DIP hit target).
func buildAttachmentChip(c *ui.Context, t *design.Tokens, st *state, attachment contracts.Attachment,
	removable bool, onRemove, onPreview func()) {
	th := t.Theme()
	key := "session.attachment." + attachment.ID
	name := firstNonEmpty(attachment.Name, st.tr("未命名附件", "Untitled attachment"))
	if onPreview != nil && !removable {
		// A sent image's chip is the open-original action: native button metrics
		// only (no background/border override), so the theme's hover/pressed/
		// focus states stay visible and Enter/Space work.
		button := design.Apply(ui.Button(c.Key(key), ""), t.Font(design.TypeXXS), func(el ui.Element) ui.Element {
			return el.Height(design.ControlCompactHeight).PaddingX(design.Space2).
				Gap(design.Space1).Radius(design.RadiusSM)
		}).Label(name).Tooltip(st.tr("查看原图", "View the original"))
		button.Children(func() {
			buildAttachmentChipContent(c, t, st, attachment, name, th)
		})
		if button.Clicked() {
			onPreview()
		}
		return
	}
	chip := design.Apply(ui.Row(c.Key(key)), t.ControlChip(), func(el ui.Element) ui.Element {
		return el.Background(t.BgStrong).Border(0, ui.Transparent)
	}).AlignItems(ui.Center).Shrink(0).Label(name)
	chip.Children(func() {
		buildAttachmentChipContent(c, t, st, attachment, name, th)
		if onPreview != nil {
			uikit.IconButton(c, uikit.IconButtonOptions{
				Key:     key + ".preview",
				Label:   st.tr("查看原图 ", "View the original ") + name,
				Tooltip: st.tr("查看原图", "View the original"),
				Icon:    icons.Image(),
				Size:    design.IconSizeS,
				OnClick: onPreview,
			})
		}
		if removable {
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

// buildAttachmentChipContent draws a chip's leading thumbnail/glyph and its
// name/size copy; the caller owns the surface (button or quiet chip).
func buildAttachmentChipContent(c *ui.Context, t *design.Tokens, st *state, attachment contracts.Attachment,
	name string, th *ui.Theme) {
	if bitmap := st.attachmentThumbnail(attachment); bitmap != nil {
		ui.Image(c, bitmap).Size(th.Space(8), th.Space(8)) // 32 DIP preview
	} else {
		// A file is a file and an image is an image: the icon is the affordance,
		// not a network globe.
		icon := icons.File()
		if attachment.IsImage() {
			icon = icons.Image()
		}
		ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
	}
	design.Apply(ui.Text(c, name), t.Font(design.TypeXXS)).
		TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
	if attachment.Size() > 0 {
		design.Apply(ui.Text(c, formatAttachmentSize(attachment.Size())), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).NoWrap()
	}
}

// --- attachment image gallery (P1) ------------------------------------------

// galleryMargin keeps the lightbox clear of the window edges.
const galleryMargin = design.Space6

// attachmentGallery is the attachment image lightbox state: the images of one
// message (or of the staged composer attachments), the shown index, the decoded
// originals and the always-built viewport probe. One gallery is open at a time;
// closing drops the attachment bytes and bitmaps so a large original is not kept
// alive behind a closed dialog.
type attachmentGallery struct {
	open     bool
	images   []contracts.Attachment
	index    int
	entries  map[int]galleryEntry
	viewport ui.Handle
}

// galleryEntry is one image's decoded original, or the honest reason it cannot
// be shown; it is decoded once per gallery, never in a plain frame.
type galleryEntry struct {
	bitmap *ui.Bitmap
	notice string
}

// imageAttachments filters one attachment collection to the images, in order.
func imageAttachments(attachments []contracts.Attachment) []contracts.Attachment {
	images := make([]contracts.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.IsImage() {
			images = append(images, attachment)
		}
	}
	return images
}

// openAttachmentGallery opens the lightbox on one image of images. It runs from
// a click handler inside the frame build, so the shown original is decoded once
// here (a frame never decodes an image) and the other images decode only when
// paged to.
func (st *state) openAttachmentGallery(images []contracts.Attachment, index int) {
	if st == nil || len(images) == 0 {
		return
	}
	g := &st.gallery
	g.open = true
	g.images = append([]contracts.Attachment(nil), images...)
	g.index = clampGalleryIndex(index, len(images))
	g.entries = make(map[int]galleryEntry, len(images))
	st.galleryEntry(g.index)
}

// closeAttachmentGallery closes the lightbox and releases the originals.
func (st *state) closeAttachmentGallery() {
	if st == nil {
		return
	}
	st.gallery.open = false
	st.gallery.images = nil
	st.gallery.entries = nil
	st.gallery.index = 0
}

// move pages the open gallery by delta, wrapping at both ends.
func (g *attachmentGallery) move(delta int) {
	if !g.open || len(g.images) < 2 {
		return
	}
	g.index = (g.index + delta + len(g.images)) % len(g.images)
}

// clampGalleryIndex bounds one shown index into a gallery of count images.
func clampGalleryIndex(index, count int) int {
	if count <= 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= count {
		return count - 1
	}
	return index
}

// galleryEntry returns the decoded original of the image at index, or the honest
// notice that says why it cannot be shown. The result is cached for the gallery's
// lifetime, so switching back and forth decodes nothing twice.
func (st *state) galleryEntry(index int) galleryEntry {
	g := &st.gallery
	if index < 0 || index >= len(g.images) {
		return galleryEntry{notice: st.tr("没有这张图片", "This image is not here")}
	}
	if entry, ok := g.entries[index]; ok {
		return entry
	}
	attachment := g.images[index]
	entry := galleryEntry{}
	switch {
	case !attachment.IsImage():
		entry.notice = st.tr("这个附件不是图片", "This attachment is not an image")
	case len(attachment.Data) == 0:
		// A restored history keeps only the attachment's name: be honest instead
		// of showing an empty frame.
		entry.notice = st.tr("历史消息没有保存图片数据，无法显示原图",
			"The stored message has no image data, so the original cannot be shown")
	case len(attachment.Data) > maxThumbnailBytes:
		entry.notice = st.tr("图片超过 10 MB 预览上限，无法显示原图",
			"The image exceeds the 10 MB preview limit, so the original cannot be shown")
	default:
		bitmap, err := ui.DecodeBitmap(attachment.Data)
		if err != nil || bitmap == nil {
			entry.notice = st.tr("无法解码这张图片，无法显示原图",
				"This image could not be decoded, so the original cannot be shown")
			break
		}
		entry.bitmap = bitmap
	}
	g.entries[index] = entry
	return entry
}

// galleryImageSize fits one original inside the room the dialog leaves: it never
// scales an image up, and keeps the bitmap's aspect ratio when it scales down.
func galleryImageSize(bitmap *ui.Bitmap, maxW, maxH float32) (float32, float32) {
	if bitmap == nil {
		return 0, 0
	}
	sourceW, sourceH := bitmap.Size()
	if sourceW <= 0 || sourceH <= 0 {
		return 0, 0
	}
	width, height := float32(sourceW), float32(sourceH)
	if maxW > 0 && width > maxW {
		height *= maxW / width
		width = maxW
	}
	if maxH > 0 && height > maxH {
		width *= maxH / height
		height = maxH
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

// buildAttachmentGallery draws the attachment image lightbox: the inert
// full-window viewport probe (built every frame, so the first lightbox frame
// already knows the window), then the official ui.Modal with the original image,
// its file name and size, the close action and, for a message with several
// images, the previous/next paging. Modal owns the dialog semantics (focus,
// Escape, a click on the scrim, the standard look); this file owns only the
// image content and paging. No image editing is offered.
func buildAttachmentGallery(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st == nil {
		return
	}
	g := &st.gallery
	// The probe is out of the flow and out of the pointer's path; its last
	// frame's bounds are the window the dialog fits the original inside.
	ui.Overlay(c, func() {
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			IgnoreInput(true).AccessHidden(true).Bind(&g.viewport)
	})
	if !g.open || len(g.images) == 0 {
		return
	}
	g.index = clampGalleryIndex(g.index, len(g.images))
	single := len(g.images) == 1
	attachment := g.images[g.index]
	entry := st.galleryEntry(g.index)
	viewport := g.viewport.Bounds(c)
	if viewport.W <= 0 || viewport.H <= 0 {
		// The probe never laid out (a dialog opened in the window's first
		// frame): fit against a conservative default instead of clipping.
		viewport = ui.Rect{W: design.TranscriptWidth + 2*galleryMargin, H: 720}
	}
	// The modal panel pads by Space5 on each side and caps itself at the window
	// minus that padding; the image fits inside the room that leaves, with the
	// dialog's own header and paging row counted as chrome.
	chrome := design.RowCompactHeight + 2*design.Space2
	if !single {
		chrome += design.RowCompactHeight + design.Space2
	}
	fittedW, fittedH := galleryImageSize(entry.bitmap,
		viewport.W-2*galleryMargin-2*design.Space5,
		viewport.H-2*galleryMargin-2*design.Space5-chrome)
	name := firstNonEmpty(attachment.Name, st.tr("未命名图片", "Untitled image"))
	modal := ui.Modal(c, &g.open, func() {
		ui.Column(c).Gap(design.Space2).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).
						FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary).
						MaxLines(1).Ellipsis("…").Tooltip(name)
					meta := formatAttachmentSize(attachment.Size())
					if !single {
						meta = fmt.Sprintf(st.tr("第 %d / %d 张 · %s", "%d of %d · %s"),
							g.index+1, len(g.images), meta)
					}
					if strings.TrimSpace(meta) != "" {
						design.Apply(ui.Text(c, meta), t.Font(design.TypeXXS)).
							TextColor(t.LabelTertiary).NoWrap()
					}
				})
				uikit.IconButton(c, uikit.IconButtonOptions{
					Key:     "session.gallery.close",
					Label:   st.tr("关闭大图", "Close image"),
					Tooltip: st.tr("关闭大图", "Close image"),
					Icon:    icons.Close(),
					Size:    design.IconSizeS,
					OnClick: func() {
						g.open = false
						runtime.Invalidate()
					},
				})
			})
			if entry.bitmap != nil {
				ui.Image(c, entry.bitmap).Size(fittedW, fittedH).Fit(ui.Contain).Label(name)
			} else {
				// An original that cannot be shown keeps its honest reason, with
				// the warning glyph so it does not read as an empty dialog.
				ui.Row(c).AlignItems(ui.Start).Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.AlertTriangle()).Size(design.Space4, design.Space4).TextColor(t.WarningText)
					design.Apply(ui.Text(c, entry.notice), t.Font(design.TypeXS)).
						TextColor(t.WarningText).MaxLines(3)
				})
			}
			if !single {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(design.Space3).Children(func() {
					uikit.IconButton(c, uikit.IconButtonOptions{
						Key:     "session.gallery.previous",
						Label:   st.tr("上一张", "Previous image"),
						Tooltip: st.tr("上一张", "Previous image"),
						// icons has no left chevron; the shared icon button's
						// Content slot takes the right one turned around.
						Content: func(c *ui.Context) {
							ui.Icon(c, icons.ChevronRight()).
								Size(design.IconSizeS, design.IconSizeS).
								TextColor(t.LabelSecondary).Rotate(180)
						},
						Size: design.IconSizeS,
						OnClick: func() {
							g.move(-1)
							runtime.Invalidate()
						},
					})
					design.Apply(ui.Text(c, fmt.Sprintf(st.tr("%d / %d", "%d / %d"), g.index+1, len(g.images))),
						t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
					uikit.IconButton(c, uikit.IconButtonOptions{
						Key:     "session.gallery.next",
						Label:   st.tr("下一张", "Next image"),
						Tooltip: st.tr("下一张", "Next image"),
						Icon:    icons.ChevronRight(),
						Size:    design.IconSizeS,
						OnClick: func() {
							g.move(1)
							runtime.Invalidate()
						},
					})
				})
			}
		})
	})
	modal.Label(st.tr("查看图片：", "View image: ") + name)
	if !single {
		// Left/Right page while the dialog owns the keyboard. The modal is built
		// last, so its own base reads Escape and the backdrop click first; the
		// arrows only reach this when they are not taken by a focused control.
		if modal.OverlayShortcut(0, ui.KeyLeft) {
			g.move(-1)
			runtime.Invalidate()
		}
		if modal.OverlayShortcut(0, ui.KeyRight) {
			g.move(1)
			runtime.Invalidate()
		}
	}
}
