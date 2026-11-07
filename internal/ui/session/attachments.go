package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// AttachmentPicker is the composer's file-picker seam: the default opens the
// native mygo file dialog; tests replace it with a scripted selection so no
// desktop backend is needed. It returns absolute paths.
var AttachmentPicker = func() ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:    "选择附件",
		Multiple: true,
		Filters: []mygo.FileFilter{{
			Name: "文件与图片",
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
			st.attachErr = "无法添加 " + name + "：不是可读的普通文件"
			continue
		}
		if info.Size() > maxAttachmentBytes {
			st.attachErr = fmt.Sprintf("%s 超过 10 MB 附件上限", name)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			st.attachErr = "无法读取 " + name + "：" + err.Error()
			continue
		}
		st.attachSeq++
		st.pendingAttachments = append(st.pendingAttachments, contracts.Attachment{
			ID:   fmt.Sprintf("att-%d", st.attachSeq),
			Name: name,
			MIME: attachmentMIME(path),
			Data: data,
		})
	}
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

// attachmentThumbnail decodes (once) the preview bitmap of an image attachment.
// Nil means "no preview": a non-image, empty data, or bytes the decoder refuses.
func (st *state) attachmentThumbnail(attachment contracts.Attachment) *ui.Bitmap {
	if !attachment.IsImage() || len(attachment.Data) == 0 || len(attachment.Data) > maxThumbnailBytes {
		return nil
	}
	if bitmap, ok := st.thumbnails[attachment.ID]; ok {
		return bitmap
	}
	bitmap, err := ui.DecodeBitmap(attachment.Data)
	if err != nil {
		return nil
	}
	if st.thumbnails == nil {
		st.thumbnails = make(map[string]*ui.Bitmap)
	}
	st.thumbnails[attachment.ID] = bitmap
	return bitmap
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
	if len(st.pendingAttachments) == 0 && st.attachErr == "" {
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		if len(st.pendingAttachments) > 0 {
			ui.Row(c).FillWidth().Gap(design.Space1).Children(func() {
				for _, attachment := range st.pendingAttachments {
					attachment := attachment
					buildAttachmentChip(c, t, st, attachment, true, func() {
						removePendingAttachment(st, attachment.ID)
						runtime.Invalidate()
					})
				}
			})
		}
		if st.attachErr != "" {
			buildErrorLine(c, t, st.attachErr)
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
	chip := design.Apply(ui.Row(c), t.Panel()).
		Padding(design.Space1, design.Space2).Gap(design.Space1).
		AlignItems(ui.Center).Shrink(0)
	chip.Children(func() {
		if bitmap := st.attachmentThumbnail(attachment); bitmap != nil {
			ui.Image(c, bitmap).Size(th.Space(12), th.Space(12)) // 48 DIP preview
		} else {
			icon := icons.Folder()
			if attachment.IsImage() {
				icon = icons.Globe()
			}
			ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
		}
		design.Apply(ui.Text(c, firstNonEmpty(attachment.Name, "未命名附件")), t.Font(design.TypeXXS)).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		if attachment.Size() > 0 {
			design.Apply(ui.Text(c, formatAttachmentSize(attachment.Size())), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		}
		if removable {
			remove := ui.Button(c, "×").Label("移除附件 " + attachment.Name).
				Tooltip("移除附件").Height(th.Space(5)).PaddingX(design.Space1).
				Radius(design.RadiusSM).TextColor(t.LabelSecondary)
			if remove.Clicked() && onRemove != nil {
				onRemove()
			}
		}
	})
}
