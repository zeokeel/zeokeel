// Package sidebar contributes the runtime sidebar of the product: the primary
// "new chat" action, the searchable chat session list, the session lifecycle
// actions and the settings entry.
//
// It is a UI module like ui/home: a kernel Descriptor that requires the host UI
// runtime and the chat read model, registers slot items on activation and hands
// the unregister functions (and the chat subscription) to kernel effects so
// disposal unwinds them. The host owns the native Sidebar/SidebarSection shell;
// this module only injects native controls (SearchField, ButtonBase, Button,
// Modal, AlertDialog, Icon, Text, Badge) styled through internal/design. It
// never draws a control by hand.
//
// # Session operations
//
// The list filters case-insensitively by session title (an empty title is shown
// as "未命名会话"). When an active session exists the footer shows the quiet
// rename/delete actions:
//
//   - rename is only offered when the chat service also implements
//     contracts.SessionRenamer; the button opens a native Modal with a
//     TextInput, save/cancel and an inline error (an empty title is rejected);
//   - delete opens a native AlertDialog that names the session and warns the
//     action cannot be undone; only the last button deletes.
//
// The rename Modal is a second slot item on contracts.SlotOverlayDialogs
// (id sidebar.rename.dialog), so it can render above the whole window.
package sidebar

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Config is the module's typed assembly config.
type Config struct {
	// NewChatLabel is the primary action label (default "新对话").
	NewChatLabel string `json:"newChatLabel"`
	// SettingsLabel is the footer settings button label (default "设置").
	SettingsLabel string `json:"settingsLabel"`
	// SessionsTitle is the small list heading above the sessions (default "会话").
	SessionsTitle string `json:"sessionsTitle"`
}

// paneState is the module instance's UI state, shared by the slot builds that
// render from it: the search query, the rename dialog draft/error and the
// pending delete confirmation. It lives on the capture heap (one per module
// activation) and is only touched while building a frame / handling a click on
// the UI thread.
type paneState struct {
	query string

	// ctx resolves the optional workspace backend lazily (no Requires), and
	// wsCollapsed remembers the per-workspace tree fold state.
	ctx         cordis.Context
	wsCollapsed map[string]bool

	// screens lists the registered screens so the sidebar can offer a
	// generic navigation item for every module screen except the two the
	// sidebar draws itself (home destination + the session list).
	screens contracts.ScreenLister

	// Full-text message search state: the query the cached hits belong to and
	// the last result/error. Hits are re-queried only when the query changes.
	searchQuery string
	searchHits  []contracts.MessageSearchHit
	searchErr   error

	renameOpen   bool
	renameTarget string
	renameDraft  string
	renameErr    string

	deleteOpen   bool
	deleteTarget string
	deleteTitle  string
}

// Module registers the sidebar slots.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/sidebar", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		nav, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}
		// Renaming is optional: a host that only implements ChatService keeps the
		// rest of the sidebar and simply does not get the rename entry.
		renamer, _ := chat.(contracts.SessionRenamer)

		newChatLabel := firstNonEmpty(cfg.NewChatLabel, "新对话")
		settingsLabel := firstNonEmpty(cfg.SettingsLabel, "设置")
		sessionsTitle := firstNonEmpty(cfg.SessionsTitle, "会话")
		state := &paneState{
			ctx:         ctx,
			wsCollapsed: make(map[string]bool),
		}
		if lister, ok := ctx.Get(contracts.KeyScreenLister); ok {
			state.screens = lister
		}

		// The chat service notifies from the agent goroutine; hop to the UI
		// thread with Post and let the host schedule a frame. The subscription
		// is unwound with the module through the kernel effect.
		unsubscribe, err := chat.Subscribe(func(string) {
			runtime.Post(func() { runtime.Invalidate() })
		})
		if err != nil {
			return fmt.Errorf("sidebar: subscribe chat: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(unsubscribe)}
		}); err != nil {
			unsubscribe()
			return err
		}

		primary := contracts.SlotItem{
			Slot:  contracts.SlotSidebarPrimary,
			ID:    "sidebar.primary.new-chat",
			Order: 10,
			Build: func(c *ui.Context) {
				buildPrimary(c, design.Of(c), chat, nav, runtime, newChatLabel)
			},
		}
		navSlot := contracts.SlotItem{
			Slot:  contracts.SlotSidebarNav,
			ID:    "sidebar.nav.sessions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildSessionList(c, design.Of(c), chat, nav, runtime, state, sessionsTitle)
			},
		}
		footer := contracts.SlotItem{
			Slot:  contracts.SlotSidebarFooter,
			ID:    "sidebar.footer.actions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildFooter(c, design.Of(c), chat, nav, runtime, state, settingsLabel)
			},
		}
		renameDialog := contracts.SlotItem{
			Slot:  contracts.SlotOverlayDialogs,
			ID:    "sidebar.rename.dialog",
			Order: 20,
			Build: func(c *ui.Context) {
				buildRenameDialog(c, design.Of(c), renamer, runtime, state)
			},
		}

		for _, item := range []contracts.SlotItem{primary, navSlot, footer, renameDialog} {
			if err := own(ctx, func() (func(), error) { return runtime.RegisterSlotItem(item) }); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel sidebar")
}

// buildPrimary is the brand "new chat" action: a native ButtonBase on the brand
// accent, 38 DIP tall, with the NewChat glyph and a 500-weight label. Pointer,
// keyboard and pressed/hover states are the native control's.
func buildPrimary(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, label string) {
	ui.Row(c).FillWidth().
		Padding(design.Space2, design.Space3, design.Space2, design.Space3).
		Children(func() {
			button := ui.ButtonBase(c).FillWidth().Height(38).Radius(design.RadiusMD).
				Background(t.Accent).Label(label)
			switch {
			case button.Pressed():
				button = button.Background(t.AccentPressed)
			case button.Hovered():
				button = button.Background(t.AccentHover)
			}
			button.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Center).
					Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.NewChat()).Size(16, 16).TextColor(t.AccentText)
					design.Apply(ui.Text(c, label), t.Text(design.TypeBody)).
						FontWeight(500).TextColor(t.AccentText)
				})
			})
			if button.Clicked() {
				// NewSession marks the session active; navigate afterwards. A
				// headless or partial assembly may not have the screen yet, so a
				// failed push must not break the frame.
				if _, err := chat.NewSession(""); err == nil {
					// The host reconciles its native Sidebar selection after this
					// build and may push the previously selected screen again; go
					// to the session screen on the next frame so that the
					// reconciliation sees the new destination.
					runtime.Post(func() { _ = nav.Push("session") })
					runtime.Invalidate()
				}
			}
		})
}

// buildSessionList renders the sidebar's navigation column: the Home
// destination first, then the "工作区" → session tree when a workspace backend
// is available (contracts.KeyWorkspace) or the legacy flat session list
// otherwise. The search field filters session titles across every group;
// clicking a session activates it and opens the session screen.
func buildSessionList(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState, title string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, 0, 0, 0).
		Children(func() {
			home := ui.SidebarItem(c, "home", icons.NewChat(), "首页")
			home.OnClick(func() {
				if err := nav.Push("home"); err == nil {
					runtime.Invalidate()
				}
			})
			// Generic module-screen destinations: every registered screen the
			// sidebar does not own itself (home destination and the session list
			// are handled above) gets one native navigation item, so a new screen
			// module (for example ui/automation) appears without a sidebar edit.
			if state.screens != nil {
				for _, id := range state.screens.Screens() {
					if id == "home" || id == "session" || strings.TrimSpace(id) == "" {
						continue
					}
					icon := icons.Data()
					switch id {
					case "automation":
						icon = icons.Clock()
					case "plugins":
						icon = icons.Plugin()
					}
					item := ui.SidebarItem(c, id, icon, screenNavLabel(id))
					screenID := id
					item.OnClick(func() {
						if err := nav.Push(screenID); err == nil {
							runtime.Invalidate()
						}
					})
				}
			}
			// The old UI separated the destinations from the conversation list
			// with one empty line before the section label.
			ui.Box(c).Height(design.Space2)

			// The workspace backend is optional: without it (or when its list
			// fails) the sidebar keeps the flat list it always had.
			workspace, hasWorkspace := state.ctx.Get(contracts.KeyWorkspace)
			var workspaces []contracts.Workspace
			if hasWorkspace {
				listed, err := workspace.List(context.Background())
				if err != nil {
					hasWorkspace = false
				} else {
					workspaces = listed
				}
			}
			if hasWorkspace {
				buildWorkspaceSectionHeader(c, t, workspace)
			} else {
				design.Apply(ui.Text(c, title), t.SectionLabel())
			}
			ui.Row(c).FillWidth().Padding(0, design.Space1, 0, design.Space1).
				Children(func() {
					ui.SearchField(c, &state.query).Label("搜索会话").Placeholder("搜索会话").FillWidth()
				})

			query := strings.TrimSpace(state.query)
			allSessions := chat.Sessions()
			sessions := pinnedFirst(filterSessions(allSessions, query))
			switch {
			case len(sessions) == 0:
				empty := "还没有会话"
				if query != "" {
					empty = "没有匹配的会话"
				}
				design.Apply(ui.Text(c, empty), t.TextMuted(design.TypeXXS)).
					Padding(design.Space1, design.Space2, 0, design.Space2)
			case hasWorkspace:
				buildWorkspaceTree(c, t, chat, nav, runtime, state, workspace,
					workspaces, allSessions, sessions, chat.ActiveSession(), query)
			default:
				active := chat.ActiveSession()
				for _, info := range sessions {
					buildSessionRow(c, t, chat, nav, runtime, state, info, info.ID == active, 0)
				}
			}
			// Full-text message search below the title-filtered list: a query
			// can match messages of sessions whose title does not match, so this
			// section renders even when the list above is empty.
			if query != "" {
				buildMessageSearch(c, t, chat, nav, runtime, state, query)
			}
		})
}

// maxMessageHits bounds the message-search result rows shown per query.
const maxMessageHits = 8

// buildMessageSearch renders the durable full-text search results for the
// current query. The optional store must implement contracts.MessageSearcher;
// results are cached per query so repeated frames do not re-run the query.
func buildMessageSearch(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState, query string) {
	store, ok := state.ctx.Get(contracts.KeyStore)
	if !ok || store == nil {
		return
	}
	searcher, ok := store.(contracts.MessageSearcher)
	if !ok {
		return
	}
	if state.searchQuery != query {
		state.searchQuery = query
		state.searchHits, state.searchErr = searcher.SearchMessages(context.Background(), query, maxMessageHits)
	}

	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, 0, 0, 0).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, "消息搜索"), t.SectionLabel())
				ui.Spacer(c).Grow(1)
				if state.searchErr == nil {
					design.Apply(ui.Badge(c, strconv.Itoa(len(state.searchHits))), t.Badge())
				}
			})
			if state.searchErr != nil {
				buildSidebarError(c, t, "搜索消息失败："+state.searchErr.Error())
				return
			}
			if len(state.searchHits) == 0 {
				design.Apply(ui.Text(c, "没有匹配的消息"), t.TextMuted(design.TypeXXS)).
					Padding(design.Space1, design.Space2, 0, design.Space2)
				return
			}
			for _, hit := range state.searchHits {
				buildMessageHitRow(c, t, chat, nav, runtime, hit)
			}
		})
}

// buildMessageHitRow is one full-text hit: session title, role, time and the
// matching snippet; clicking activates that session and opens it.
func buildMessageHitRow(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, hit contracts.MessageSearchHit) {
	title := firstNonEmpty(hit.SessionTitle, "未命名会话")
	label := title + " " + firstNonEmpty(hit.Snippet, hit.Role)
	row := ui.ButtonBase(c).FillWidth().Padding(design.Space1, design.Space2).
		Radius(design.RadiusSM).Label(label)
	if row.Hovered() {
		row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).
					FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				design.Apply(ui.Text(c, messageRoleLabel(hit.Role)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary)
				design.Apply(ui.Text(c, formatHitTime(hit.CreatedAt)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary)
			})
			if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
				design.Apply(ui.Text(c, snippet), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).MaxLines(2)
			}
		})
	})
	row.OnClick(func() {
		_ = chat.SetActiveSession(hit.SessionID)
		runtime.Post(func() { _ = nav.Push("session") })
		runtime.Invalidate()
	})
}

// messageRoleLabel renders a message role for a search hit.
func messageRoleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "用户"
	case "assistant":
		return "助手"
	case "tool":
		return "工具"
	case "":
		return "消息"
	default:
		return strings.TrimSpace(role)
	}
}

// formatHitTime renders a hit timestamp compactly (empty when unset).
func formatHitTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("01-02 15:04")
}

// buildSidebarError is the sidebar's danger row.
func buildSidebarError(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(3), th.Space(3)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).
			TextColor(t.DangerText).MaxLines(2)
	})
}

// ungroupedWorkspaceKey is the fold key of the synthetic "未分组" group.
const ungroupedWorkspaceKey = "\x00ungrouped"

// buildWorkspaceSectionHeader is the section label above the workspace tree:
// "工作区" plus a badge with the current workspace's name.
func buildWorkspaceSectionHeader(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, "工作区"), t.SectionLabel())
		if current, ok, err := workspace.Current(context.Background()); err == nil && ok {
			design.Apply(ui.Badge(c, firstNonEmpty(current.Name, current.Root)), t.Badge())
		}
	})
}

// buildWorkspaceTree renders the workspace → session groups. Sessions with an
// empty or unknown WorkspaceID go to the trailing "未分组" group; a non-empty
// search query keeps only the groups that still have matching sessions.
func buildWorkspaceTree(
	c *ui.Context,
	t *design.Tokens,
	chat contracts.ChatService,
	nav contracts.Navigator,
	runtime contracts.UIRuntime,
	state *paneState,
	workspace contracts.WorkspaceService,
	workspaces []contracts.Workspace,
	allSessions, matched []contracts.ChatSessionInfo,
	activeID, query string,
) {
	known := make(map[string]bool, len(workspaces))
	for _, ws := range workspaces {
		known[ws.ID] = true
	}
	groupOf := func(info contracts.ChatSessionInfo) string {
		id := strings.TrimSpace(info.WorkspaceID)
		if id == "" || !known[id] {
			return ""
		}
		return id
	}
	totals := make(map[string]int, len(workspaces)+1)
	for _, info := range allSessions {
		totals[groupOf(info)]++
	}
	byGroup := make(map[string][]contracts.ChatSessionInfo, len(workspaces)+1)
	for _, info := range matched {
		key := groupOf(info)
		byGroup[key] = append(byGroup[key], info)
	}
	currentID := ""
	if current, ok, err := workspace.Current(context.Background()); err == nil && ok {
		currentID = current.ID
	}

	for _, ws := range workspaces {
		sessions := byGroup[ws.ID]
		if query != "" && len(sessions) == 0 {
			continue
		}
		buildWorkspaceGroup(c, t, chat, nav, runtime, state, ws.ID,
			firstNonEmpty(ws.Name, ws.Root, "未命名工作区"), sessions, totals[ws.ID],
			ws.ID == currentID, activeID)
	}

	sessions := byGroup[""]
	if len(sessions) == 0 && (query != "" || totals[""] == 0) {
		return
	}
	buildWorkspaceGroup(c, t, chat, nav, runtime, state, ungroupedWorkspaceKey,
		"未分组", sessions, totals[""], false, activeID)
}

// buildWorkspaceGroup renders one collapsible workspace row and, while it is
// expanded, its indented session rows.
func buildWorkspaceGroup(
	c *ui.Context,
	t *design.Tokens,
	chat contracts.ChatService,
	nav contracts.Navigator,
	runtime contracts.UIRuntime,
	state *paneState,
	key, name string,
	sessions []contracts.ChatSessionInfo,
	count int,
	current bool,
	activeID string,
) {
	collapsed := state.wsCollapsed[key]
	row := ui.ButtonBase(c).FillWidth().Height(32).PaddingX(design.Space2).
		Radius(design.RadiusSM).Label(name)
	if row.Hovered() || current {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Folder()
			iconColor := t.LabelSecondary
			if current {
				icon = icons.FolderOpen()
				iconColor = t.Accent
			}
			ui.Icon(c, icon).Size(16, 16).TextColor(iconColor)
			textColor := t.LabelSecondary
			if current {
				textColor = t.LabelPrimary
			}
			design.Apply(ui.Text(c, name), t.Text(design.TypeXS)).
				FontWeight(500).TextColor(textColor).Grow(1).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Badge(c, strconv.Itoa(count)), t.Badge())
			chevron := icons.ChevronDown()
			if collapsed {
				chevron = icons.ChevronRight()
			}
			ui.Icon(c, chevron).Size(14, 14).TextColor(t.LabelTertiary)
		})
	})
	row.OnClick(func() {
		state.wsCollapsed[key] = !state.wsCollapsed[key]
		runtime.Invalidate()
	})
	if collapsed {
		return
	}
	for _, info := range sessions {
		buildSessionRow(c, t, chat, nav, runtime, state, info, info.ID == activeID, design.Space3)
	}
}

// screenNavLabel renders a registered screen id as a sidebar label: the known
// product screens get their localized title, other ids fall back to the id.
func screenNavLabel(id string) string {
	switch id {
	case "automation":
		return "自动化"
	case "plugins":
		return "插件"
	default:
		return id
	}
}

// filterSessions keeps the sessions whose display title contains query, case
// insensitively. An empty query keeps every session.
func filterSessions(sessions []contracts.ChatSessionInfo, query string) []contracts.ChatSessionInfo {
	if query == "" {
		return sessions
	}
	needle := strings.ToLower(query)
	filtered := make([]contracts.ChatSessionInfo, 0, len(sessions))
	for _, info := range sessions {
		if strings.Contains(strings.ToLower(sessionTitle(info)), needle) {
			filtered = append(filtered, info)
		}
	}
	return filtered
}

// pinnedFirst moves pinned sessions to the front while keeping the existing
// order inside both partitions. Sessions arrive newest-updated first, so this
// only reorders pinned vs unpinned; workspace groups and search results inherit
// the same order.
func pinnedFirst(sessions []contracts.ChatSessionInfo) []contracts.ChatSessionInfo {
	if len(sessions) < 2 {
		return sessions
	}
	ordered := make([]contracts.ChatSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if session.Pinned {
			ordered = append(ordered, session)
		}
	}
	for _, session := range sessions {
		if !session.Pinned {
			ordered = append(ordered, session)
		}
	}
	return ordered
}

// buildSessionRow is one brand session row: a native ButtonBase at the brand row
// metrics (36 DIP, 8 DIP radius, hover/selected tint) with the accessible label
// and the native click behavior.
func buildSessionRow(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState,
	session contracts.ChatSessionInfo, active bool, indent float32) {
	label := sessionTitle(session)
	renamer, _ := chat.(contracts.SessionRenamer)
	forker, _ := chat.(contracts.SessionForker)
	pinner, _ := chat.(contracts.SessionPinner)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1).
		Padding(0, 0, 0, indent).Children(func() {
		row := ui.ButtonBase(c).Grow(1).Height(36).PaddingX(design.Space3).
			Radius(design.RadiusSM).Label(label)
		if active || row.Hovered() {
			row = row.Background(t.BgHover)
		}
		row.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				icon := icons.Message()
				iconColor := t.LabelSecondary
				if active {
					icon = icons.NewChat()
					iconColor = t.Accent
				}
				ui.Icon(c, icon).Size(16, 16).TextColor(iconColor)
				design.Apply(ui.Text(c, label), t.Text(design.TypeS)).
					Grow(1).MaxLines(1).Ellipsis("…")
				if session.Pinned {
					design.Apply(ui.Badge(c, "置顶"), t.Badge())
				}
				// The active session is already carried by the accent icon and
				// the selected-row tint; a 当前 badge was redundant and stole
				// width from the title.
				if session.Running {
					design.Apply(ui.Badge(c, "运行中"), t.Badge())
				}
			})
		})
		row.OnClick(func() {
			_ = chat.SetActiveSession(session.ID)
			// Queue the screen switch for the next frame: this frame's host-side
			// native Sidebar reconciliation would otherwise restore the previously
			// selected destination.
			runtime.Post(func() { _ = nav.Push("session") })
			runtime.Invalidate()
		})
		// The old shell composed inline row actions: a native popover menu on
		// the row itself (rename/delete/activate) instead of footer buttons.
		ui.PopoverMenuButton(c, "⋯", func(m *ui.Menu) {
			if renamer != nil && m.Item("重命名").Chosen() {
				state.renameOpen = true
				state.renameErr = ""
				state.renameTarget = session.ID
				state.renameDraft = sessionTitleByID(chat, session.ID)
			}
			if forker != nil && m.Item("创建副本").Chosen() {
				if newID, err := forker.ForkSession(session.ID); err == nil && strings.TrimSpace(newID) != "" {
					_ = chat.SetActiveSession(newID)
					runtime.Post(func() { _ = nav.Push("session") })
					runtime.Invalidate()
				}
			}
			if pinner != nil {
				pinLabel := "置顶会话"
				if session.Pinned {
					pinLabel = "取消置顶"
				}
				if m.Item(pinLabel).Chosen() {
					if err := pinner.SetSessionPinned(session.ID, !session.Pinned); err == nil {
						runtime.Invalidate()
					}
				}
			}
			if m.Item("删除").Chosen() {
				state.deleteOpen = true
				state.deleteTarget = session.ID
				state.deleteTitle = sessionTitle(session)
			}
			if !active {
				m.Separator()
				if m.Item("设为当前").Chosen() {
					_ = chat.SetActiveSession(session.ID)
					runtime.Invalidate()
				}
			}
		})
	})
}

// buildFooter is the quiet sidebar footer from the old shell: the settings
// entry, plus the window-level delete confirmation (session rename/delete live
// on the row's native popover menu).
func buildFooter(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime,
	state *paneState, label string) {
	if state.deleteOpen {
		message := fmt.Sprintf("“%s”及其全部消息将被删除，此操作无法撤销。", firstNonEmpty(state.deleteTitle, "未命名会话"))
		switch ui.AlertDialog(c, &state.deleteOpen, "删除会话", message, "取消", "删除") {
		case 1:
			if err := chat.DeleteSession(state.deleteTarget); err == nil {
				runtime.Invalidate()
			}
			state.deleteTarget = ""
			state.deleteTitle = ""
		case 0, ui.AlertDismissed:
			state.deleteTarget = ""
			state.deleteTitle = ""
			runtime.Invalidate()
		}
		return
	}

	ui.Column(c).FillWidth().Gap(design.Space1).
		Padding(design.Space2, design.Space3, design.Space2, design.Space3).
		Children(func() {
			ui.Row(c).FillWidth().Children(func() {
				settings := ui.Button(c, label)
				settings.Children(func() {
					ui.Icon(c, icons.Sliders()).Size(16, 16).TextColor(t.LabelSecondary)
				})
				if settings.Clicked() {
					nav.ToggleSettings()
				}
			})
		})
}

// buildRenameDialog renders the rename Modal on the window-level overlay slot.
// It is only visible while the footer's rename action asked for it; Escape or a
// click outside closes it through the native Modal's open pointer.
func buildRenameDialog(c *ui.Context, t *design.Tokens, renamer contracts.SessionRenamer,
	runtime contracts.UIRuntime, state *paneState) {
	if !state.renameOpen || renamer == nil {
		return
	}
	target := state.renameTarget
	ui.Modal(c, &state.renameOpen, func() {
		ui.Column(c).Width(360).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, "重命名会话"), t.Text(design.TypeM)).FontWeight(600)
			ui.TextInput(c, &state.renameDraft).Label("会话标题").FillWidth()
			if state.renameErr != "" {
				design.Apply(ui.Text(c, state.renameErr), t.TextMuted(design.TypeXS)).
					TextColor(t.DangerText)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				cancel := ui.Button(c, "取消")
				if cancel.Clicked() {
					state.renameOpen = false
					state.renameErr = ""
					runtime.Invalidate()
				}
				save := ui.PrimaryButton(c, "保存")
				if save.Clicked() {
					title := strings.TrimSpace(state.renameDraft)
					switch {
					case title == "":
						state.renameErr = "标题不能为空"
					case target == "":
						state.renameErr = "没有活动会话"
					default:
						if err := renamer.RenameSession(target, title); err != nil {
							state.renameErr = err.Error()
						} else {
							state.renameOpen = false
							state.renameErr = ""
							runtime.Invalidate()
						}
					}
				}
			})
		})
	})
}

// sessionTitle is the display title of one session ("未命名会话" when blank).
func sessionTitle(info contracts.ChatSessionInfo) string {
	if title := strings.TrimSpace(info.Title); title != "" {
		return title
	}
	return "未命名会话"
}

// sessionTitleByID looks a session up and returns its display title.
func sessionTitleByID(chat contracts.ChatService, sessionID string) string {
	if session, ok := chat.Session(sessionID); ok {
		if title := strings.TrimSpace(session.Title); title != "" {
			return title
		}
	}
	for _, info := range chat.Sessions() {
		if info.ID == sessionID {
			return sessionTitle(info)
		}
	}
	return "未命名会话"
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
