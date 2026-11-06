package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// errAttachmentPickerUnavailable reports that the native file picker cannot
// run in this process (no desktop backend), so the user must be told instead
// of silently doing nothing.
var errAttachmentPickerUnavailable = errors.New("shell: attachment picker unavailable")

// pickAttachmentFiles is the production multi-file picker. It is a variable so
// tests can replace it, mirroring pickWorkspaceDirectory.
var pickAttachmentFiles = func() ([]string, error) {
	if !mygo.App.IsReady() {
		return nil, errAttachmentPickerUnavailable
	}
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:    "Attach files",
		Multiple: true,
	})
}

// chooseAttachmentFiles is the composer's Files action: open the real file
// picker and stage the selection. A session composer hands the files to the
// controller, which copies them into the data directory and stages their
// chips; the Home composer stages them with its own draft because the session
// does not exist yet.
func (a *App) chooseAttachmentFiles(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	pick := a.PickFiles
	if pick == nil {
		if !mygo.App.IsReady() {
			a.attachmentPickerFallback(c)
			return
		}
		pick = pickAttachmentFiles
	}
	paths, err := pick()
	if err != nil {
		if errors.Is(err, errAttachmentPickerUnavailable) {
			a.attachmentPickerFallback(c)
		} else {
			c.Toast(fmt.Sprintf(a.menuText("Could not open the file picker: %v",
				"无法打开文件选择器：%v"), err))
		}
		return
	}
	selected := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			selected = append(selected, path)
		}
	}
	if len(selected) == 0 {
		return
	}
	// The Home composer owns its files with the Home draft: there is no
	// session yet, and staging them on the last-opened session stranded them
	// there when Home Send created a different one. They are copied onto the
	// new session by attachHomeAttachments just before its first Submit.
	if a.backgroundScreen() == Home {
		a.stageHomeAttachments(c, selected)
		return
	}
	sessionID := a.Ctrl.CurrentSessionID()
	if strings.TrimSpace(sessionID) == "" {
		c.Toast(a.menuText("Open a session before attaching files.",
			"请先打开会话再添加附件。"))
		return
	}
	if _, err := a.Ctrl.AttachFiles(context.Background(), sessionID, selected); err != nil {
		c.Toast(fmt.Sprintf(a.menuText("Could not attach file: %v",
			"无法添加附件：%v"), err))
	}
}

// homeAttachment is one file staged for the Home draft before a session
// exists. Path is the picker's source file; ID identifies its chip while the
// file is only staged in memory.
type homeAttachment struct {
	ID   string
	Path string
	Name string
}

// nextHomeAttachmentID returns a chip id that is stable for the life of one
// Home draft. The id is deliberately not a store attachment id: the file has
// not been copied into a session yet.
func (a *App) nextHomeAttachmentID() string {
	a.homeAttachmentSeq++
	return fmt.Sprintf("home-%d", a.homeAttachmentSeq)
}

// stageHomeAttachments validates the picked files and appends them to the Home
// draft. Nothing is copied until Home Send creates the session, so a draft the
// user abandons leaves no rows or files behind.
func (a *App) stageHomeAttachments(c *ui.Context, paths []string) {
	if len(paths) == 0 {
		return
	}
	if len(a.homeAttachments)+len(paths) > session.MaxAttachmentsPerTurn {
		c.Toast(fmt.Sprintf(a.menuText("You can attach at most %d files to one message.",
			"每条消息最多添加 %d 个附件。"), session.MaxAttachmentsPerTurn))
		return
	}
	staged := make([]homeAttachment, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			c.Toast(fmt.Sprintf(a.menuText("Could not attach %s: not a regular file.",
				"无法添加 %s：不是普通文件。"), filepath.Base(path)))
			return
		}
		if info.Size() > session.MaxAttachmentBytes {
			c.Toast(fmt.Sprintf(a.menuText("%s exceeds the %d MiB attachment limit.",
				"%s 超过 %d MiB 附件上限。"), filepath.Base(path),
				session.MaxAttachmentBytes/(1<<20)))
			return
		}
		staged = append(staged, homeAttachment{
			ID:   a.nextHomeAttachmentID(),
			Path: path,
			Name: filepath.Base(path),
		})
	}
	a.homeAttachments = append(a.homeAttachments, staged...)
}

// homeAttachmentSnapshot renders the Home draft's staged files through the
// same chip component as a live session snapshot. SessionID stays empty, which
// tells removal to drop the staged path instead of deleting a store row.
func (a *App) homeAttachmentSnapshot() *state.Snapshot {
	if len(a.homeAttachments) == 0 {
		return nil
	}
	snap := state.NewSnapshot()
	for _, f := range a.homeAttachments {
		snap.Attachments = append(snap.Attachments, state.Attachment{ID: f.ID, Name: f.Name})
	}
	return snap
}

// attachHomeAttachments copies the Home draft's staged files onto sessionID
// and clears the staging. On error the staging stays, so the caller can keep
// the files visible and let the user retry instead of silently dropping them.
func (a *App) attachHomeAttachments(sessionID string) error {
	if a.Ctrl == nil || len(a.homeAttachments) == 0 {
		return nil
	}
	paths := make([]string, 0, len(a.homeAttachments))
	for _, f := range a.homeAttachments {
		paths = append(paths, f.Path)
	}
	if _, err := a.Ctrl.AttachFiles(context.Background(), sessionID, paths); err != nil {
		return err
	}
	a.homeAttachments = nil
	return nil
}

// removeHomeAttachment drops one staged Home file by chip id.
func (a *App) removeHomeAttachment(attachmentID string) {
	if len(a.homeAttachments) == 0 {
		return
	}
	out := a.homeAttachments[:0]
	for _, f := range a.homeAttachments {
		if f.ID == attachmentID {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		a.homeAttachments = nil
		return
	}
	a.homeAttachments = out
}

// attachmentPickerFallback keeps the Files entry useful when no desktop
// backend can show a native dialog: it says why, then opens the @ mention
// palette the entry historically provided so the user can still reference a
// workspace file by typing.
func (a *App) attachmentPickerFallback(c *ui.Context) {
	c.Toast(a.menuText("The file picker is unavailable right now.",
		"文件选择器当前不可用。"))
	a.insertComposerToken(c, "@")
}

// pendingAttachmentChips renders the files staged for the next turn above the
// composer. Every chip carries a remove control that deletes the row and the
// copied file.
func (a *App) pendingAttachmentChips(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if a.Ctrl == nil || snap == nil || len(snap.Attachments) == 0 {
		return
	}
	// A snapshot with no session id is the Home draft's staging: it has no
	// controller rows yet, so removal drops the staged path instead.
	sessionID := strings.TrimSpace(snap.SessionID)
	ui.Row(c).Key("attachment-chips").Gap(design.Space1).Wrap().
		AlignItems(ui.Center).Children(func() {
		for _, attachment := range snap.Attachments {
			a.attachmentChip(c, t, sessionID, attachment, true)
		}
	})
}

// messageAttachmentChips renders the files sent with one user message.
func (a *App) messageAttachmentChips(c *ui.Context, t *design.Tokens, sessionID string, attachments []state.Attachment) {
	if len(attachments) == 0 {
		return
	}
	ui.Row(c).Gap(design.Space1).Wrap().Justify(ui.End).Children(func() {
		for _, attachment := range attachments {
			a.attachmentChip(c, t, sessionID, attachment, false)
		}
	})
}

// attachmentChip draws one filename chip. A removable chip owns a "×" button
// whose accessible label names the file, so removing it is unambiguous for
// both the pointer and the accessibility tree.
func (a *App) attachmentChip(c *ui.Context, t *design.Tokens, sessionID string, attachment state.Attachment, removable bool) {
	chip := ui.Row(c).Height(24).Padding(0, design.Space2).Gap(design.Space1).
		AlignItems(ui.Center).Radius(design.RadiusXS).
		Background(t.BgHover).Border(1, t.BorderL2)
	chip.Children(func() {
		ui.Text(c, attachment.Name).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
		if !removable {
			return
		}
		label := a.tr("Remove attachment: "+attachment.Name, "移除附件："+attachment.Name)
		remove := ui.Button(c, "×").Size(16, 16).Padding(0).Radius(design.RadiusXS).
			Label(label).Tooltip(label).TextColor(t.LabelTertiary)
		if remove.Clicked() {
			a.removeAttachment(c, sessionID, attachment.ID)
		}
	})
}

// removeAttachment deletes one staged attachment and its copied file. An
// empty sessionID means the Home draft, whose staged files have not been
// copied into a session yet.
func (a *App) removeAttachment(c *ui.Context, sessionID, attachmentID string) {
	if strings.TrimSpace(sessionID) == "" {
		a.removeHomeAttachment(attachmentID)
		return
	}
	if a.Ctrl == nil {
		return
	}
	if err := a.Ctrl.RemoveAttachment(context.Background(), sessionID, attachmentID); err != nil {
		c.Toast(fmt.Sprintf(a.menuText("Could not remove attachment: %v",
			"无法移除附件：%v"), err))
	}
}
