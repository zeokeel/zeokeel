package main

// Archived-session migration steps: the sidebar no longer carries a "show
// archived" view toggle; archived conversations live on the settings "归档"
// page, where they can be restored, reopened or deleted (with confirmation).
// Archiving itself still happens from the session row's native context menu.

import (
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// archiveSession creates (and archives) one conversation for the archive page
// and returns its ID. The caller restores the previously active session.
func (d *drive) archiveSession(title string) (string, error) {
	chat := d.chat()
	archiver, ok := chat.(contracts.SessionArchiver)
	if !ok {
		return "", errNoArchiver
	}
	id, err := chat.NewSession(title)
	if err != nil {
		return "", err
	}
	if err := archiver.SetSessionArchived(id, true); err != nil {
		_ = chat.DeleteSession(id)
		return "", err
	}
	return id, nil
}

// sessionArchived reports the archived flag of one conversation.
func (d *drive) sessionArchived(sessionID string) bool {
	for _, info := range d.chat().Sessions() {
		if info.ID == sessionID {
			return info.Archived
		}
	}
	return false
}

// sessionExists reports whether the conversation is still in the read model.
func (d *drive) sessionExists(sessionID string) bool {
	for _, info := range d.chat().Sessions() {
		if info.ID == sessionID {
			return true
		}
	}
	return false
}

func (d *drive) runArchiveSteps() {
	d.step("archive-sidebar-toggle-gone", "settings", "归档：侧栏不再有「显示已归档」开关",
		"会话页侧栏 footer 只有设置入口，没有「显示已归档/隐藏已归档」视图开关", func(c *check) {
			d.resetToSession()
			for _, label := range []string{"显示已归档", "隐藏已归档"} {
				c.require(!d.has(label), "侧栏仍显示归档视图开关「%s」", label)
			}
			c.require(d.has("设置"), "侧栏 footer 缺少设置入口")
			c.shot("archive-sidebar-toggle-gone")
		})

	d.step("archive-restore", "settings", "归档：设置归档页恢复会话",
		"归档一个会话 → 设置「归档」页列出它 → 点「恢复」→ 会话回到侧栏、归档页变空", func(c *check) {
			chat := d.chat()
			previousActive := chat.ActiveSession()
			title := "驱动归档会话"
			sessionID, err := d.archiveSession(title)
			if err != nil {
				c.failf("创建/归档会话失败：%v", err)
				return
			}
			defer func() {
				_ = chat.DeleteSession(sessionID)
				if previousActive != "" {
					_ = chat.SetActiveSession(previousActive)
				}
				d.syncSession()
			}()
			if err := d.openSettingsPageID("archive"); err != nil {
				c.failf("%v", err)
				return
			}
			if !c.has(title) {
				c.failf("归档页没有列出已归档会话「%s」", title)
				return
			}
			c.mustText("归档", "归档的会话")
			c.shot("archive-page")
			if !c.has("恢复") {
				c.failf("归档页缺少「恢复」动作")
				return
			}
			if !c.click("恢复") {
				return
			}
			d.drain(2)
			c.require(!d.sessionArchived(sessionID), "点击「恢复」后会话仍为已归档")
			c.require(c.has("没有归档的会话"), "恢复后归档页没有显示空态")
			c.shot("archive-restored")
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
			// The restored conversation is back in the sidebar list.
			if !c.has(title) {
				c.failf("恢复后侧栏没有重新显示「%s」", title)
			}
		})

	d.step("archive-delete-confirm", "settings", "归档：归档页删除需确认",
		"归档页点「删除」→ 原生确认对话框（取消/确认删除）→ 确认后会话被删除", func(c *check) {
			chat := d.chat()
			previousActive := chat.ActiveSession()
			title := "驱动归档删除会话"
			sessionID, err := d.archiveSession(title)
			if err != nil {
				c.failf("创建/归档会话失败：%v", err)
				return
			}
			defer func() {
				_ = chat.DeleteSession(sessionID)
				if previousActive != "" {
					_ = chat.SetActiveSession(previousActive)
				}
				d.syncSession()
			}()
			if err := d.openSettingsPageID("archive"); err != nil {
				c.failf("%v", err)
				return
			}
			if !c.has(title) {
				c.failf("归档页没有列出「%s」", title)
				return
			}
			if !c.click("删除") {
				return
			}
			d.drain(2)
			c.mustText("删除归档会话", "确认删除")
			c.shot("archive-delete-confirm")
			if !c.click("确认删除") {
				return
			}
			d.drain(2)
			c.require(!d.sessionExists(sessionID), "确认删除后会话仍存在")
			c.require(!c.has(title), "确认删除后归档页仍显示该会话")
		})

	d.step("archive-empty-state", "settings", "归档：无归档会话时显示空态",
		"没有归档会话时归档页显示「没有归档的会话」空态与解释文案", func(c *check) {
			// Clear any archived sessions the earlier steps left behind.
			chat := d.chat()
			if archiver, ok := chat.(contracts.SessionArchiver); ok {
				for _, info := range chat.Sessions() {
					if info.Archived {
						_ = archiver.SetSessionArchived(info.ID, false)
					}
				}
			}
			if err := d.openSettingsPageID("archive"); err != nil {
				c.failf("%v", err)
				return
			}
			d.drain(2)
			c.mustText("归档的会话", "没有归档的会话")
			c.shot("archive-empty")
			d.uiRoot.Navigator().CloseSettings()
			d.frame()
		})
}

// errNoArchiver reports a chat backend without the archive half.
var errNoArchiver = archiveError("chat backend does not support archived sessions")

type archiveError string

func (e archiveError) Error() string { return string(e) }
