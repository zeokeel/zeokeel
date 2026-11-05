package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// quitApp is the More menu's Quit action. It is a variable so a test can
// observe the choice without terminating the test process.
var quitApp = func() { mygo.App.Quit() }

// Menu icons: an information circle and a power glyph, drawn on the same
// 16px grid as the settings icons.
var (
	menuIconInfo = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>`))

	menuIconPower = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.77.04"/></svg>`))
)

// sessionWhen renders the sidebar's relative timestamp.
func (a *App) sessionWhen(updated time.Time) string {
	if updated.IsZero() {
		return a.T("common.now")
	}
	d := time.Since(updated)
	switch {
	case d < time.Minute:
		return a.T("common.now")
	case d < time.Hour:
		return a.T("time.minutes_short", int(d.Minutes()))
	case d < 24*time.Hour:
		return a.T("time.hours_short", int(d.Hours()))
	default:
		return a.T("time.days_short", int(d.Hours()/24))
	}
}

// moreFooter is the sidebar's bottom row. A non-live render draws a plain
// row; a live shell makes it a real popover button.
func (a *App) moreFooter(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		ui.Row(c).Height(40).Margin(0, 10, 6, 10).Padding(6).
			AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, icons.More()).FontSize(16).TextColor(t.LabelSecondary)
			ui.Text(c, a.T("menu.more")).FontSize(14).FixedLineHeight(20).TextColor(t.LabelPrimary)
		})
		return
	}

	ui.Row(c).Height(40).Margin(0, 10, 6, 10).AlignItems(ui.Center).Children(func() {
		// The anchor owns the whole footer row (264x32 in this layout), so a
		// click anywhere along "More" opens the account menu. Its accessible
		// The accessible name mirrors the reference product's account menu;
		// the visible text stays "More".
		anchor := ui.Row(c).Height(32).Padding(6).Gap(design.Space2).
			Radius(design.RadiusMD).AlignItems(ui.Center).Grow(1).
			Label(a.menuText("Account menu", "账户菜单")).
			Role(ui.RoleMenuButton).Focusable()
		if anchor.Hovered() {
			anchor = anchor.Background(t.BgHover)
		}
		anchor.Children(func() {
			ui.Icon(c, icons.More()).FontSize(16).TextColor(t.LabelSecondary)
			ui.Text(c, a.T("menu.more")).FontSize(14).FixedLineHeight(20).TextColor(t.LabelPrimary)
		})
		anchor.OnClick(func() {
			a.closeSelectors()
			a.moreOpen = !a.moreOpen
		})
		anchor.Expanded(a.moreOpen)
		ui.Popover(c, anchor, &a.moreOpen, func() { a.moreMenu(c, t) })
	})
}

// moreMenu draws Settings / About / Quit in the popover.
func (a *App) moreMenu(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Gap(2).Children(func() {
		a.moreMenuItem(c, t, settingsIconGear, a.T("menu.settings"), func() {
			a.moreOpen = false
			a.openSettings()
		})
		a.moreMenuItem(c, t, menuIconInfo, a.T("menu.about"), func() {
			a.moreOpen = false
			a.aboutOpen = true
		})
		a.moreMenuItem(c, t, menuIconPower, a.T("menu.quit"), func() {
			a.moreOpen = false
			quitApp()
		})
	})
}

// moreMenuItem is one row of the More popover. It is a real menu item: named,
// focusable and activated by Enter/Space, so the account menu is not
// pointer-only. The same helper draws the right sidebar's New tab menu.
func (a *App) moreMenuItem(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string, fn func()) {
	row := ui.Row(c).Height(30).PaddingX(design.Space2).Gap(design.Space2).
		Radius(design.RadiusSM).AlignItems(ui.Center).
		Label(label).Role(ui.RoleMenuItem).Focusable()
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Image(c, icon).Size(16, 16).TextColor(t.LabelPrimary)
		settingsText(c, label, design.TypeBody, t.LabelPrimary)
	})
	row.OnClick(fn)
}

// aboutDialog is the About item's dialog.
func (a *App) aboutDialog(c *ui.Context, t *design.Tokens) {
	ui.Modal(c, &a.aboutOpen, func() {
		ui.Column(c).Width(380).Gap(design.Space2).Children(func() {
			ui.Text(c, "ZeoKeel").FontSize(20).FontWeight(600).TextColor(t.LabelPrimary)
			ui.Text(c, a.T("menu.pure_go_desktop_ai_agent_mygo_eino")).
				FontSize(13).TextColor(t.LabelSecondary)
			ui.Text(c, a.T("about.version")).FontSize(12).TextColor(t.LabelTertiary)
			ui.Row(c).Justify(ui.End).Padding(design.Space2, 0, 0, 0).Children(func() {
				if ui.Button(c, a.T("menu.close")).Clicked() {
					a.aboutOpen = false
				}
			})
		})
	})
}

// shortcutsDialog documents the shortcuts, opened from Settings > Keyboard
// shortcuts or Cmd/Ctrl+/. Each row's Edit shortcut control records a new
// binding; Restore all defaults clears the overrides. The rows come from
// shortcutRows so the reference cannot drift from the registered bindings.
func (a *App) shortcutsDialog(c *ui.Context, t *design.Tokens) {
	ui.Modal(c, &a.shortcutsOpen, func() {
		ui.Column(c).Width(480).Gap(design.Space2).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.T("settings.keyboard_shortcuts")).FontSize(16).FontWeight(600).
					TextColor(t.LabelPrimary).Grow(1)
				if a.hasShortcutOverrides() {
					if ui.Button(c, a.T("settings.restore_all_defaults")).Clicked() {
						a.restoreShortcutDefaults()
						a.shortcutEditID = ""
					}
				} else {
					// Restore all defaults stays disabled until a binding
					// differs from its default.
					ui.Row(c).Height(30).PaddingX(design.Space2).Radius(design.RadiusSM).
						Background(t.BgHover).AlignItems(ui.Center).
						Label(a.T("settings.restore_all_defaults")).
						Role(ui.RoleButton).Disabled(true).Children(func() {
						ui.Text(c, a.T("settings.restore_all_defaults")).
							FontSize(12).TextColor(t.LabelTertiary)
					})
				}
			})
			ui.Text(c, a.T("settings.view_and_edit_available_shortcuts_and_input_actions")).
				FontSize(12).TextColor(t.LabelTertiary)
			ui.TextInput(c, &a.shortcutsQuery).Label(a.T("settings.search_shortcuts")).
				Placeholder(a.T("settings.search_shortcuts"))
			// The shortcut reference also lists the composer's input keys.
			// They are fixed (Enter/Shift+Enter), so they are reference rows
			// rather than editable bindings.
			ui.Column(c).Gap(1).Children(func() {
				ui.Text(c, a.tr("Message input", "消息输入")).FontSize(11).
					FixedLineHeight(16).TextColor(t.LabelTertiary)
				for _, item := range []struct{ action, keys string }{
					{a.T("composer.send_message"), "Enter"},
					{a.tr("New line", "换行"), "Shift+Enter"},
				} {
					ui.Row(c).Height(30).PaddingX(design.Space2).AlignItems(ui.Center).
						Gap(design.Space2).Children(func() {
						ui.Text(c, item.action).FontSize(13).TextColor(t.LabelPrimary).Grow(1)
						ui.Text(c, item.keys).FontSize(12).TextColor(t.LabelTertiary)
					})
				}
			})
			ui.Scroll(c).Height(380).Children(func() {
				ui.Column(c).Gap(1).Children(func() {
					query := strings.ToLower(strings.TrimSpace(a.shortcutsQuery))
					for _, item := range a.shortcutRows() {
						row := item
						if query != "" && !strings.Contains(strings.ToLower(row.action), query) &&
							!strings.Contains(strings.ToLower(row.keys), query) {
							continue
						}
						if a.shortcutEditID == row.id {
							a.shortcutEditRow(c, t, row)
							continue
						}
						ui.Row(c).Height(38).PaddingX(design.Space2).AlignItems(ui.Center).
							Gap(design.Space2).Radius(design.RadiusSM).Children(func() {
							ui.Text(c, row.action).FontSize(13).TextColor(t.LabelPrimary).Grow(1)
							ui.Text(c, row.keys).FontSize(12).TextColor(t.LabelTertiary)
							edit := ui.Row(c).Height(26).PaddingX(8).Radius(design.RadiusSM).
								Background(t.BgHover).AlignItems(ui.Center).
								Label(a.T("settings.edit_shortcut_for") + row.action).
								Role(ui.RoleButton).Focusable()
							edit.Children(func() {
								ui.Text(c, a.T("menu.edit")).FontSize(12).
									TextColor(t.LabelSecondary)
							})
							edit.OnClick(func() {
								a.shortcutEditID = row.id
								a.shortcutDraft = row.keys
								a.shortcutEditErr = ""
							})
						})
					}
				})
			})
			ui.Row(c).Justify(ui.End).Padding(design.Space2, 0, 0, 0).Children(func() {
				if ui.Button(c, a.T("menu.close")).Clicked() {
					a.shortcutsOpen = false
				}
			})
		})
	})
}

// shortcutEditRow is the inline editor for one binding: a text field plus Save
// and Cancel. Saving writes keybinding.<id> so the binding applies next frame.
func (a *App) shortcutEditRow(c *ui.Context, t *design.Tokens, row shortcutRow) {
	ui.Column(c).Gap(design.Space1).Children(func() {
		ui.Row(c).Height(38).PaddingX(design.Space2).AlignItems(ui.Center).
			Gap(design.Space2).Radius(design.RadiusSM).Background(t.BgHover).
			Children(func() {
				ui.Text(c, row.action).FontSize(13).TextColor(t.LabelPrimary).Grow(1)
				field := ui.TextInput(c, &a.shortcutDraft).
					Label(a.T("settings.shortcut_for") + row.action).
					Placeholder("Cmd+Shift+K").Width(150).Bind(&a.shortcutEditRef)
				if a.shortcutEditErr != "" {
					field.BorderColor(t.Danger)
				}
				if ui.Button(c, a.T("menu.save")).Clicked() {
					if err := a.saveShortcutOverride(row.id, a.shortcutDraft); err != nil {
						a.shortcutEditErr = err.Error()
						a.shortcutEditRef.Focus(c)
					} else {
						a.shortcutEditID = ""
						a.shortcutEditErr = ""
					}
				}
				if ui.Button(c, a.T("menu.cancel")).Clicked() {
					a.shortcutEditID = ""
					a.shortcutEditErr = ""
				}
			})
		if a.shortcutEditErr != "" {
			ui.Text(c, a.shortcutEditErr).FontSize(11).FixedLineHeight(16).
				TextColor(t.Danger).Padding(0, design.Space2)
		}
	})
}

// beginRename opens the rename dialog for a session.
func (a *App) beginRename(id, title string) {
	a.renameID = id
	a.renameDraft = title
	a.renameOpen = true
	a.deleteOpen = false
	a.moreOpen = false
	a.closeSelectors()
}

// renameDialog edits and saves a session title.
func (a *App) renameDialog(c *ui.Context, t *design.Tokens) {
	ui.Modal(c, &a.renameOpen, func() {
		ui.Column(c).Width(420).Gap(design.Space3).Children(func() {
			ui.Text(c, a.T("menu.rename_session")).FontSize(16).FontWeight(600).
				TextColor(t.LabelPrimary)
			ui.TextInput(c, &a.renameDraft).Label(a.T("menu.session_title")).AutoFocus().
				OnSubmit(func() { a.confirmRename(c) })
			ui.Row(c).Gap(design.Space2).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.cancel")).Clicked() {
					a.renameOpen = false
				}
				if ui.Button(c, a.T("menu.save")).Clicked() {
					a.confirmRename(c)
				}
			})
		})
	})
}

// confirmRename persists the edited title and toasts the outcome.
func (a *App) confirmRename(c *ui.Context) {
	title := strings.TrimSpace(a.renameDraft)
	if title == "" {
		c.Toast(a.T("menu.title_cannot_be_empty"))
		return
	}
	if a.Ctrl == nil {
		a.renameOpen = false
		return
	}
	if _, err := a.Ctrl.RenameSession(context.Background(), a.renameID, title); err != nil {
		c.Toast(a.T("menu.could_not_rename_session"))
		return
	}
	a.renameOpen = false
	c.Toast(a.T("menu.session_renamed"))
}

// beginDelete opens the delete confirmation.
func (a *App) beginDelete(id, title string) {
	a.deleteID = id
	a.deleteTitle = title
	a.deleteOpen = true
	a.renameOpen = false
	a.moreOpen = false
	a.closeSelectors()
}

// deleteDialog confirms a destructive delete.
func (a *App) deleteDialog(c *ui.Context, t *design.Tokens) {
	ui.Modal(c, &a.deleteOpen, func() {
		ui.Column(c).Width(420).Gap(design.Space3).Children(func() {
			ui.Text(c, a.T("menu.delete_session")).FontSize(16).FontWeight(600).
				TextColor(t.LabelPrimary)
			ui.Text(c, fmt.Sprintf(a.T("menu.delete_q_this_cannot_be_undone"), a.deleteTitle)).
				FontSize(13).TextColor(t.LabelSecondary)
			ui.Row(c).Gap(design.Space2).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("menu.cancel")).Clicked() {
					a.deleteOpen = false
				}
				del := ui.Button(c, a.T("menu.delete")).TextColor(t.Danger)
				if del.Clicked() {
					a.confirmDelete(c)
				}
			})
		})
	})
}

// confirmDelete removes the session and opens the next one, if any.
func (a *App) confirmDelete(c *ui.Context) {
	id := a.deleteID
	if a.Ctrl != nil {
		if err := a.Ctrl.DeleteSession(context.Background(), id); err != nil {
			c.Toast(a.T("menu.could_not_delete_session"))
			return
		}
	}
	a.deleteOpen = false

	if a.Ctrl != nil && a.Ctrl.CurrentSessionID() == "" {
		sessions, err := a.Ctrl.ListSessions(context.Background())
		if err == nil && len(sessions) > 0 {
			_, _ = a.Ctrl.OpenSession(context.Background(), sessions[0].ID)
			a.SetSnapshot(a.Ctrl.Snapshot())
			a.Screen = Session
		} else {
			a.Screen = Home
		}
	}
	c.Toast(a.T("menu.session_deleted"))
}
