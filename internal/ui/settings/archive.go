package settings

// Archived sessions page: the settings-side home of sessions the user archived.
// It lists the archived sessions with their update time and message count, lets
// the user restore, delete (after the native confirmation) or reopen them, and
// explains the whole feature when the list is empty. Choosing a row (click or
// arrow keys) only selects it; Enter or a double click opens the conversation.
// The sidebar's "show archived" view option is gone; this page is the one place
// archived sessions live.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// ArchivePageID is the archived-sessions page identity.
	ArchivePageID = "archive"
	// ArchivePageSection places it in the general group, after the daily
	// settings pages and before the update/import services.
	ArchivePageSection = "高级"
	// ArchivePageOrder sits between behavior (10) and update (92).
	ArchivePageOrder = 20
)

// archiveState is the page's local state: the session waiting for the native
// delete confirmation ("" when none is open).
type archiveState struct {
	confirmDelete string
	// pendingDeleteID survives the confirmation clearing confirmDelete, so the
	// confirmed dialog knows which session to delete.
	pendingDeleteID string
	// deleteOpen is the confirmation dialog's open flag (uikit.ConfirmDanger
	// needs a persistent pointer so both its buttons can close it).
	deleteOpen bool
	// list backs the archived sessions' official List and sel is the row chosen
	// (a click or arrow key only chooses; Enter/double click opens that
	// conversation; restore/delete keep their buttons).
	list ui.ListState
	sel  int

	// pendingOpen is the session a double click on a row asked to open; the page
	// consumes it after the list is built instead of navigating from inside a
	// row builder.
	pendingOpen string
	// openErr is the last failure to restore a conversation before opening it
	// (PM-13: opening an archived session first un-archives it).
	openErr string
}

// archivedSessions filters the chat session list to the archived ones, keeping
// the newest-updated-first order the backend returns.
func archivedSessions(sessions []contracts.ChatSessionInfo) []contracts.ChatSessionInfo {
	archived := make([]contracts.ChatSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if session.Archived {
			archived = append(archived, session)
		}
	}
	return archived
}

// archiveSessionLabel renders one session's display title, with a fallback for
// untitled sessions.
func archiveSessionLabel(locale contracts.LocaleService, session contracts.ChatSessionInfo) string {
	if title := strings.TrimSpace(session.Title); title != "" {
		return title
	}
	return localized(locale, "未命名会话", "Untitled session")
}

// archiveSessionMeta renders one archived row's metadata: the localized update
// time and the message count.
func archiveSessionMeta(locale contracts.LocaleService, session contracts.ChatSessionInfo) string {
	parts := make([]string, 0, 2)
	if updated := uikit.FormatDateTime(locale, session.UpdatedAt); updated != "" {
		parts = append(parts, updated)
	}
	switch {
	case session.MessageCount == 1:
		parts = append(parts, localized(locale, "1 条消息", "1 message"))
	case session.MessageCount > 1:
		parts = append(parts, fmt.Sprintf(localized(locale, "%d 条消息", "%d messages"), session.MessageCount))
	}
	return strings.Join(parts, " · ")
}

// buildArchivePage renders the archived-session management page.
func buildArchivePage(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	st *archiveState, locale contracts.LocaleService) {
	chat, _ := kctx.Get(contracts.KeyChat)
	navigator, _ := kctx.Get(contracts.KeyNavigator)
	archiver, canArchive := chat.(contracts.SessionArchiver)

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "归档", "Archived"), "")
		if st.openErr != "" {
			uikit.ErrorLine(c, st.openErr)
		}
		if chat == nil {
			uikit.EmptyState(c, localized(locale, "会话服务未启用", "Session service is not enabled"))
			return
		}
		archived := archivedSessions(chat.Sessions())
		uikit.Section(c, localized(locale, "归档的会话", "Archived sessions"), func(*ui.Context) {
			if len(archived) == 0 {
				uikit.EmptyStateBlock(c,
					localized(locale, "没有归档的会话", "No archived sessions"),
					localized(locale,
						"在会话的「⋯」菜单里选择「归档」后，会话会出现在这里。",
						"Archive a conversation from its more menu and it will appear here."),
					nil)
				return
			}
			// The archived sessions are a collection: the official List keys them
			// by session ID, names the rows and owns click/keyboard selection;
			// Enter or a double click reopens that conversation (a click only
			// selects the row).
			if st.sel >= len(archived) {
				st.sel = -1
			}
			st.list.Selected = &st.sel
			st.list.Key = func(index int) any {
				if index < 0 || index >= len(archived) {
					return nil
				}
				return archived[index].ID
			}
			st.list.Label = func(index int) string {
				if index < 0 || index >= len(archived) {
					return ""
				}
				return archiveSessionLabel(locale, archived[index])
			}
			list := ui.List(c, &st.list, len(archived)).
				Label(localized(locale, "归档的会话列表", "Archived sessions list")).
				Gap(design.Space1).
				Rows(func(row ui.ListRow) {
					buildArchiveRow(row.Context, t, chat, archiver, canArchive, navigator, st, archived[row.Index], locale)
				})
			// Choosing a row (a click or Up/Down/Home/End) is navigation within the
			// list, not a command: only Enter/double click opens the conversation.
			// Otherwise keyboard users are thrown out of the settings surface by the
			// first arrow key they press (ST-09).
			if list.Submitted() && st.sel >= 0 && st.sel < len(archived) {
				st.pendingOpen = archived[st.sel].ID
			}
			if st.pendingOpen != "" {
				sessionID := st.pendingOpen
				st.pendingOpen = ""
				openArchivedSession(c, chat, archiver, navigator, st, sessionID, locale)
			}
		})
		// Deleting an archived session is irreversible: confirm it with the
		// native alert dialog, like every other destructive settings action.
		if st.confirmDelete != "" {
			title := localized(locale, "删除归档会话", "Delete archived session")
			message := localized(locale, "确认删除这条归档会话？此操作无法撤销。",
				"Delete this archived session? This cannot be undone.")
			for _, session := range archived {
				if session.ID == st.confirmDelete {
					message = localized(locale, "确认删除「", "Delete \"") +
						archiveSessionLabel(locale, session) +
						localized(locale, "」？此操作无法撤销。", "\"? This cannot be undone.")
					break
				}
			}
			uikit.ConfirmDanger(c, &st.deleteOpen, uikit.ConfirmDangerOptions{
				Key:     "archive.delete",
				Title:   title,
				Message: message,
				Confirm: localized(locale, "确认删除", "Confirm delete"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					if st.pendingDeleteID != "" {
						_ = chat.DeleteSession(st.pendingDeleteID)
					}
					c.Invalidate()
				},
			})
			if !st.deleteOpen {
				st.confirmDelete = ""
				st.pendingDeleteID = ""
			}
		}
	})
}

// buildArchiveRow renders one archived session: the title opens it, and the row
// carries explicit restore and delete actions.
func buildArchiveRow(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	archiver contracts.SessionArchiver, canArchive bool, navigator contracts.Navigator,
	st *archiveState, session contracts.ChatSessionInfo, locale contracts.LocaleService) {
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space1).Children(func() {
		// The row's title is plain content: the surrounding List paints hover and
		// selection, and Enter/double click opens the conversation.
		ui.Column(c).FillWidth().Grow(1).Gap(design.Space1).
			Padding(design.Space2, design.Space2, design.Space2, 0).Children(func() {
			design.Apply(ui.Text(c, archiveSessionLabel(locale, session)),
				t.Font(design.TypeBody)).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			if meta := archiveSessionMeta(locale, session); meta != "" {
				design.Apply(ui.Text(c, meta), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).MaxLines(1)
			}
		})
		if canArchive {
			if design.Apply(ui.Button(c, localized(locale, "恢复", "Restore")),
				t.SecondaryButton()).Tooltip(localized(locale,
				"恢复后回到侧栏", "Restore it to the sidebar")).Clicked() {
				_ = archiver.SetSessionArchived(session.ID, false)
				c.Invalidate()
			}
		}
		// The destructive delete is the row's ⋯ danger item, so a page of
		// archived sessions never paints one red button per row.
		label := localized(locale, "归档会话操作", "Archived session actions")
		menu := uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "archive-actions/" + session.ID,
			Label:   label,
			Tooltip: label,
			Icon:    icons.More(),
			Size:    design.IconSizeS,
		})
		menu.PopoverMenuButton(func(m *ui.Menu) {
			remove := m.Item(localized(locale, "删除会话", "Delete session"))
			remove.Danger()
			if remove.Chosen() {
				st.confirmDelete = session.ID
				st.pendingDeleteID = session.ID
				st.deleteOpen = true
				c.Invalidate()
			}
		})
	})
	// A double click on the row (or a double click on the title text inside it)
	// opens the conversation, like Enter: the click chooses the row, the second
	// press is the command. RowDoubleClicked reports it while the row builds.
	if row.RowDoubleClicked() {
		st.pendingOpen = session.ID
	}
}

// openArchivedSession restores the conversation to the sidebar, closes the
// settings surface, makes sessionID active and pushes the conversation screen,
// so clicking an archived title really opens it — and the conversation is still
// reachable after the user leaves it (PM-13; opening it used to leave the
// session archived and therefore invisible in the sidebar).
func openArchivedSession(c *ui.Context, chat contracts.ChatService,
	archiver contracts.SessionArchiver, navigator contracts.Navigator,
	st *archiveState, sessionID string, locale contracts.LocaleService) {
	if archiver != nil {
		if err := archiver.SetSessionArchived(sessionID, false); err != nil {
			st.openErr = localized(locale, "恢复归档会话失败：", "Could not restore the archived session: ") + err.Error()
			c.Invalidate()
			return
		}
	}
	st.openErr = ""
	if err := chat.SetActiveSession(sessionID); err != nil {
		return
	}
	if navigator != nil {
		navigator.CloseSettings()
		_ = navigator.Push("session")
	}
	c.Invalidate()
}
