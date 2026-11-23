// Package sidebar contributes the runtime sidebar of the product: the primary
// "new chat" action, the searchable chat session list, the session lifecycle
// actions and the settings entry.
//
// It is a UI module like ui/home: a kernel Descriptor that requires the host UI
// runtime and the chat read model, registers slot items on activation and hands
// the unregister functions (and the chat subscription) to kernel effects so
// disposal unwinds them. The host owns the native Sidebar/SidebarSection shell;
// this module only injects native controls (SearchField, List, Button,
// ButtonBase, Modal, AlertDialog, Icon, Text, Badge) styled through
// internal/design. It never draws a control by hand.
//
// # Single-workspace sidebar
//
// The product opens one workspace folder at a time (WORKSPACE-UX, AGENT-SURFACES
// S12). With the workspace backend (contracts.KeyWorkspace) the sidebar shows
// one read-only line naming the open folder ("工作文件夹 / <name>", the folder's
// root as its tooltip); the line has no chevron and opens no dialog. The session
// list below is one native ui.List of THAT folder's conversations (rows keyed by
// session ID, named by their title, the choice follows the active session, Enter
// opens the chosen row and a single click opens the row from the row content).
// Every row's ⋯ action and its right-click share one menu. The old workspace
// tree, its groups, its indentation and its per-group new-chat buttons are gone;
// the registry still keeps every folder, but the chrome only displays the open
// one — registration and switching belong to the host and the settings surface,
// not to this module.
//
// The chosen row is the quiet persistent selection of the design system
// (DESIGN §5.2): the BgSelected tint plus a 2 DIP accent stripe on the left,
// not mygo's native saturated accent fill. The list keeps the native keyboard,
// focus, choice and accessibility selection: only the local theme the List is
// built with maps its chosen-row accent to the selection tint.
//
// The list shows user conversations only: archived sessions are owned by the
// settings surface's archive page, and sessions with a non-user explicit
// provenance (contracts.ChatSessionInfo.Origin — automation-run sessions and
// subagent children) are owned by their own surfaces. Hiding is provenance
// based, never title based: a user conversation whose title merely starts with
// the automation module's "自动化: " convention stays visible. Hidden sessions
// never render as a row or a search result. The sidebar is also
// the landing surface: there is no Home navigation row (the home screen stays
// registered for the host), so the new chat action and the session list are the
// destinations the sidebar offers.
//
// The product has exactly one view: sessions are ordered by UpdatedAt with
// pinned rows first; there is no view menu and no persisted sort/group setting.
// A host without a workspace backend keeps the same flat list of every session
// (the "会话" section) as a graceful fallback.
//
// # Session operations
//
// The search field filters sessions case-insensitively by title (an empty
// title is shown as "未命名会话") and searches message bodies below: the two
// result kinds share one query. The message read never runs inside a build
// frame: a changed query schedules one debounced background read whose result
// arrives through UIRuntime.Post with a sequence guard (the session Memory
// tab's pattern; 2026-11 audit AR-02). A title miss is never reported as "no
// results" while message hits are about to show — the quiet "标题无匹配" line
// follows the message results, and the single "没有匹配" state appears only
// when neither a title nor a message matched. Snippets are windowed around the
// first match and the matched words are emphasized.
//
// When an active session exists the footer shows the quiet rename/delete
// actions:
//
//   - rename is only offered when the chat service also implements
//     contracts.SessionRenamer; the button opens a native Modal with a
//     TextInput, save/cancel and an inline error (an empty title is rejected);
//   - delete opens a native modal confirmation that names the session and
//     warns the action cannot be undone; Cancel is the default action, so
//     only an explicit click on Delete deletes.
//
// The rename Modal is a second slot item on contracts.SlotOverlayDialogs
// (id sidebar.rename.dialog), so it can render above the whole window.
package sidebar

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/mcpreload"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Config is the module's typed assembly config.
type Config struct {
	// NewChatLabel is the primary action label (default "新建会话").
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

	// lastNewChat is when the "new session" row last created a session. One
	// pointer press can rebuild this row more than once inside the same input
	// event (the handler invalidates the frame and posts a navigation), and a
	// double click is one intent: without this guard one click made two empty
	// sessions (A4-I-04). The window is the desktop's double-click interval, so
	// a deliberate second click still starts a second session.
	lastNewChat time.Time

	// sessionMenuFocus holds one focus handle per session row ⋯ button, so a
	// confirmation the menu opened can give the keyboard back to that button
	// when it closes (A4 focus-return).
	sessionMenuFocus map[string]*ui.Handle
	// deleteTrigger is the ⋯ button that opened the pending session delete
	// confirmation.
	deleteTrigger *ui.Handle
	// sessionListFocus is the handle of the session list, the element a closed
	// dialog gives the keyboard back to.
	sessionListFocus *ui.Handle
	// deleteWasOpen records that the confirmation was up, so a close that only
	// becomes visible on a later frame still returns the keyboard to the row.
	deleteWasOpen bool
	// focusReturn names the session whose row action must stay revealed while a
	// dialog hands the keyboard back to it: the ⋯ is a hover-only control, and a
	// hidden control cannot take the focus. Cleared on the frame the focus
	// arrives.
	focusReturn string

	// locale localizes the sidebar's own copy on every frame, so a language
	// switch takes effect on the next frame without restarting the module.
	locale contracts.LocaleService
	// cfgNewChatLabel/cfgSettingsLabel/cfgSessionsTitle are explicit assembly
	// overrides; when empty the localized defaults are used.
	cfgNewChatLabel  string
	cfgSettingsLabel string
	cfgSessionsTitle string

	// ctx resolves the optional workspace backend lazily (no Requires).
	ctx cordis.Context

	// Session list state (flat mode): the native List's place/choice, the row
	// chosen and the active session last mirrored into it. The choice follows
	// the active session; the rows themselves are plain content because the
	// List owns hover, selection, keyboard and accessibility.
	sessionList   ui.ListState
	sessionRow    int
	sessionActive string

	// screens lists the registered screens so the sidebar can offer a
	// generic navigation item for every module screen it does not own (the
	// session list is the sidebar's own screen). Home is no longer a sidebar
	// destination, so it stays out of the generic rows as well.
	screens contracts.ScreenLister

	// Full-text message search state: the query the cached hits belong to and
	// the last result/error. Hits are re-queried only when the query changes,
	// and the read itself never runs inside a build frame: a changed query
	// schedules one debounced background read (searchPendingQuery/searchDue),
	// searchInFlight names the query whose read is running, and searchSeq is
	// the generation guard that drops stale results (a newer query or the
	// module's disposal bumps it). Mirrors the session Memory tab's pattern
	// (2026-11 audit AR-02).
	searchQuery        string
	searchHits         []contracts.MessageSearchHit
	searchErr          error
	searchPendingQuery string
	searchDue          time.Time
	searchInFlight     string
	searchSeq          int
	// searchList backs the message-search hits' official List; searchSel is the
	// row chosen (hit rows activate their conversation, group headers do not).
	searchList ui.ListState
	searchSel  int

	// notifier is the optional host toast bridge: archiving a session reports
	// success there (with Undo when the host supports action toasts).
	notifier contracts.Notifier

	renameOpen   bool
	renameTarget string
	renameDraft  string
	renameErr    string

	deleteOpen   bool
	deleteTarget string
	deleteTitle  string
	// deleteErr is the last failed delete confirmation: the dialog stays open
	// and shows it instead of closing on a failure that deleted nothing (a
	// false success; 2026-11 audit UX-10).
	deleteErr string
	// sessionOpErr is the last failed fork/pin/archive row-menu action, shown
	// as an error line under the session list so the failure is not silent
	// (2026-11 audit UX-10).
	sessionOpErr string
}

// T returns the localized copy for the current frame.
func (state *paneState) T(zh, en string) string { return uikit.T(state.locale, zh, en) }

// newChatLabel is the primary action label for the current frame. The product
// names the object it creates (会话 / Session) rather than the conversation
// activity, so the button, the accessibility name and the list headers all say
// one word for one thing (2026-11 copy audit F-02).
func (state *paneState) newChatLabel() string {
	return textutil.FirstNonEmpty(state.cfgNewChatLabel, state.T("新建会话", "New session"))
}

// newChatDoubleClickWindow is the interval inside which a second "new session"
// press is the same intent as the first (the desktop's double-click time).
const newChatDoubleClickWindow = 350 * time.Millisecond

// sessionMenuHandle returns the stable focus handle of one row's ⋯ button,
// creating it on first use: the widget identity must survive frames, so it
// lives with the pane's state rather than in the build.
func (state *paneState) sessionMenuHandle(id string) *ui.Handle {
	if state.sessionMenuFocus == nil {
		state.sessionMenuFocus = make(map[string]*ui.Handle)
	}
	if handle, ok := state.sessionMenuFocus[id]; ok {
		return handle
	}
	handle := &ui.Handle{}
	state.sessionMenuFocus[id] = handle
	return handle
}

// sessionListHandle is the stable handle of the list that owns the session
// rows. A dialog hands the keyboard back HERE rather than to a row's ⋯: the
// list is always on screen and always in the accessibility tree, while the ⋯
// exists only while its row is hovered or focused (A4-I-02).
func (state *paneState) sessionListHandle() *ui.Handle {
	if state.sessionListFocus == nil {
		state.sessionListFocus = &ui.Handle{}
	}
	return state.sessionListFocus
}

// claimNewChat reports whether this press may create a session: it is the first
// press inside the double-click window. See paneState.lastNewChat.
func (state *paneState) claimNewChat() bool {
	now := time.Now()
	if !state.lastNewChat.IsZero() && now.Sub(state.lastNewChat) < newChatDoubleClickWindow {
		return false
	}
	state.lastNewChat = now
	return true
}

// settingsLabel is the footer settings label for the current frame.
func (state *paneState) settingsLabel() string {
	return textutil.FirstNonEmpty(state.cfgSettingsLabel, state.T("设置", "Settings"))
}

// sessionsTitle is the flat session-list section label for the current frame.
func (state *paneState) sessionsTitle() string {
	return textutil.FirstNonEmpty(state.cfgSessionsTitle, state.T("会话", "Sessions"))
}

// sessionTitle is the displayed title of one session in the current language.
func (state *paneState) sessionTitle(info contracts.ChatSessionInfo) string {
	if title := strings.TrimSpace(info.Title); title != "" {
		return title
	}
	return state.T("未命名会话", "Untitled session")
}

// Module registers the sidebar slots.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/sidebar", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(uinative.KeyUIRuntime)
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

		state := &paneState{
			ctx:              ctx,
			sessionRow:       -1,
			searchSel:        -1,
			cfgNewChatLabel:  strings.TrimSpace(cfg.NewChatLabel),
			cfgSettingsLabel: strings.TrimSpace(cfg.SettingsLabel),
			cfgSessionsTitle: strings.TrimSpace(cfg.SessionsTitle),
		}
		if locale, ok := ctx.Get(contracts.KeyLocale); ok {
			state.locale = locale
		}
		if lister, ok := ctx.Get(contracts.KeyScreenLister); ok {
			state.screens = lister
		}
		if notifier, ok := ctx.Get(contracts.KeyNotifier); ok {
			state.notifier = notifier
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
			return cordis.Effect{
				cordis.CleanupOf(unsubscribe),
				// Disposal drops any message-search read still in flight: its
				// Post lands after teardown and the sequence guard turns it
				// into a quiet no-op (2026-11 audit AR-02).
				cordis.CleanupOf(func() { state.searchSeq++ }),
			}
		}); err != nil {
			unsubscribe()
			return err
		}

		primary := uinative.SlotItem{
			Slot:  contracts.SlotSidebarPrimary,
			ID:    "sidebar.primary.new-chat",
			Order: 10,
			Build: func(c *ui.Context) {
				buildPrimary(c, design.Of(c), chat, nav, runtime, state)
			},
		}
		navSlot := uinative.SlotItem{
			Slot:  contracts.SlotSidebarNav,
			ID:    "sidebar.nav.sessions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildSessionList(c, design.Of(c), chat, nav, runtime, state, state.sessionsTitle())
			},
		}
		footer := uinative.SlotItem{
			Slot:  contracts.SlotSidebarFooter,
			ID:    "sidebar.footer.actions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildFooter(c, design.Of(c), chat, nav, runtime, state, state.settingsLabel())
			},
		}
		renameDialog := uinative.SlotItem{
			Slot:  contracts.SlotOverlayDialogs,
			ID:    "sidebar.rename.dialog",
			Order: 20,
			Build: func(c *ui.Context) {
				buildRenameDialog(c, design.Of(c), renamer, runtime, state)
			},
		}
		for _, item := range []uinative.SlotItem{primary, navSlot, footer, renameDialog} {
			if err := own(ctx, func() (func(), error) { return runtime.RegisterSlotItem(item) }); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(uinative.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel sidebar")
}

// buildPrimary is the sidebar's "new session" row: a 32 DIP ghost row (no edge,
// RadiusSM, BgHover under the pointer) with the Plus glyph, the label at 14/500
// and the Cmd/Ctrl+N key cap at the row's trailing edge — the first row of the
// source list rather than a filled dialog button (B-spec §3.1).
//
// It is deliberately NOT an accent-filled primary: the window's one filled
// primary is the composer's send while a conversation is open, and a filled
// button beside a selected row would be a second loud accent block on one
// screen (DESIGN §5.2 accent budget). The row keeps its button role and label,
// so Enter and Space still activate it and assistive technology still names it.
// The new session binds to the open folder on the agent side; the sidebar itself
// never switches folders.
func buildPrimary(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime uinative.UIRuntime, state *paneState) {
	label := state.newChatLabel()
	// The chord is the product's one formatter, so the cap follows the platform
	// (Cmd+N on macOS, Ctrl+N elsewhere) and matches the help table and the
	// command palette (A5 copy audit F-10).
	chord := uikit.FormatShortcut("CmdOrCtrl+N")
	ui.Row(c).FillWidth().
		Padding(design.Space2, design.Space3, design.Space1, design.Space3).
		Children(func() {
			// A ghost row, not a native button: the official Button would paint a
			// surface and a hairline edge, and §2 allows no borders inside the
			// sidebar. ButtonBase keeps the pointer, the keyboard (Enter/Space),
			// the focus ring and the accessible role; rowTintTransition paints
			// the hover/selection fill and fades it over MotionFast.
			row := ui.ButtonBase(c).FillWidth().Height(design.RowCompactHeight).
				PaddingX(design.Space2).Radius(design.RadiusSM).Label(label)
			row = rowTintTransition(c, t, row, false)
			row.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.Plus()).Size(design.IconSizeS, design.IconSizeS).
						TextColor(t.LabelSecondary)
					design.Apply(ui.Text(c, label), t.Text(design.TypeS)).
						TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
					// The key cap is the visible chord (B-spec §3.1), not a
					// tooltip: a tooltip is an overlay that would take the first
					// Escape from the surface (A4-I-01).
					uikit.Kbd(c, t, chord)
				})
			})
			if row.Clicked() && state.claimNewChat() {
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

// buildSessionList renders the sidebar's navigation column in the product's one
// view: the read-only current folder line ("工作文件夹 / <name>"), the search
// field, the flat conversation list of that folder and the message search below
// it. The workspace tree and the switch/manage dialog are gone (WORKSPACE-UX):
// the multi-folder registry still exists in the backend, but the chrome shows
// the one open folder and nothing else. A host without the workspace backend
// keeps the plain "会话" list it always had.
func buildSessionList(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime uinative.UIRuntime, state *paneState, title string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, design.Space0, design.Space0, design.Space0).
		Children(func() {
			// Generic module-screen destinations: every registered screen the
			// sidebar does not own itself (the session list is its own screen, and
			// the home screen is intentionally not a destination) gets one native
			// navigation item, so a new screen module (for example
			// ui/automation) appears without a sidebar edit.
			// The workspace backend is optional: without it (or when its list
			// fails) the sidebar keeps the flat list it always had. With it, the
			// header names the one open folder and the list shows only that
			// folder's conversations.
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
			current, hasCurrent := contracts.Workspace{}, false
			if hasWorkspace {
				if opened, ok, err := workspace.Current(context.Background()); err == nil && ok {
					current, hasCurrent = opened, true
				}
				buildWorkspaceLine(c, t, state, current, hasCurrent)
			} else {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, title), t.SectionLabel())
				})
			}
			if state.screens != nil {
				for _, id := range state.screens.Screens() {
					if id == "home" || id == "session" || strings.TrimSpace(id) == "" {
						continue
					}
					item := ui.SidebarItem(c, id, screenNavIcon(id), state.screenNavLabel(id))
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

			ui.Row(c).FillWidth().Padding(design.Space0, design.Space1, design.Space0, design.Space1).
				Children(func() {
					// The English name has to survive the 242 DIP the sidebar
					// gives the field: "Search sessions and messages" was cut to
					// "…and message:" there, so the English is the short form
					// (the Chinese already fits). One string serves as the
					// accessible label and the placeholder, so both stay honest.
					searchLabel := state.T("搜索会话与消息", "Search sessions…")
					ui.SearchField(c, &state.query).
						Label(searchLabel).
						Placeholder(searchLabel).FillWidth()
				})

			query := strings.TrimSpace(state.query)
			// Archived sessions never render in the sidebar (the settings
			// surface's archive page owns them), not in the list and not as a
			// search result.
			allSessions := visibleSessions(chat.Sessions())
			// The single-workspace view: only this folder's conversations. A
			// conversation without a binding (legacy data, or one created before
			// any workspace existed) and one whose folder was forgotten stay
			// reachable in the open folder instead of disappearing from the UI.
			scoped := allSessions
			if hasWorkspace {
				scoped = sessionsOfWorkspace(allSessions, workspaces, current, hasCurrent)
			}
			sessions := orderSessionList(filterSessions(scoped, query, state))
			// A query has two result kinds: session titles and message bodies.
			// The message read is debounced in the background (AR-02), so while
			// it is pending the empty states wait for it: a title miss never
			// reads as "no results" while the message matches below it are
			// about to show (R11). Message hits are scoped with the list: a hit
			// in another folder is not this folder's conversation.
			extraHidden := make(map[string]bool)
			if hasWorkspace {
				inScope := make(map[string]bool, len(scoped))
				for _, info := range scoped {
					inScope[info.ID] = true
				}
				for _, info := range allSessions {
					if !inScope[info.ID] {
						extraHidden[info.ID] = true
					}
				}
			}
			search := messageSearchFor(c, runtime, state, chat, query, extraHidden)
			titleHits := len(sessions)
			messageHits := len(search.hits)
			// degradedTitle is the title miss under message hits: the session
			// empty state becomes one quiet line after the message results.
			degradedTitle := query != "" && titleHits == 0 && messageHits > 0
			// overallEmpty is the one "no matches" state: neither a title nor
			// a message matched, or the host has no message searcher at all.
			// A pending message read suppresses it until the result lands.
			overallEmpty := query != "" && titleHits == 0 && messageHits == 0 && search.err == nil && !search.reading
			switch {
			case overallEmpty:
				// One empty state for the whole search, not a session one
				// followed by a message one (R11).
				sidebarEmptyLine(c, state.T("没有匹配", "No matches"), design.Space1)
			case len(sessions) == 0:
				// A query with message hits (or a message-search failure) leaves
				// the session section to the message section below; only the
				// plain no-query empty state and the settled title miss stay
				// here. A pending message read keeps the section quiet too.
				if query == "" {
					sidebarEmptyLine(c, state.T("还没有会话", "No sessions yet"), design.Space1)
				} else if !degradedTitle && !search.reading {
					sidebarEmptyLine(c, state.T("没有匹配的会话", "No matching sessions"), design.Space1)
				}
			default:
				buildFlatSessionList(c, t, chat, nav, runtime, state, sessions)
			}
			// A failed fork/pin/archive row action reports itself here, close to
			// the list it acted on, instead of failing silently (audit UX-10).
			if state.sessionOpErr != "" {
				ui.Column(c).FillWidth().Padding(design.Space1, design.Space2, design.Space0, design.Space2).
					Children(func() { buildSidebarError(c, t, state.sessionOpErr) })
			}
			// Full-text message search below the title-filtered list: a query
			// can match messages of sessions whose title does not match, so this
			// section renders even when the list above is empty.
			if query != "" && search.available && !overallEmpty {
				buildMessageSearch(c, t, chat, nav, runtime, state, query, search)
			}
			// The title miss under message hits: a quiet last line instead of a
			// "no matching sessions" headline before the results (R11).
			if degradedTitle {
				sidebarEmptyLine(c, state.T("标题无匹配", "No matching titles"), design.Space2)
			}
		})
}

// sessionsOfWorkspace keeps the conversations that belong to the open folder.
// A conversation bound to ANOTHER registered folder is hidden (the single-
// workspace rule), while a conversation without a binding or with a folder the
// registry no longer knows stays visible: hiding it would make it unreachable
// from any surface. With no open folder every conversation stays visible (the
// host is between folders, e.g. right after deleting the open one).
func sessionsOfWorkspace(all []contracts.ChatSessionInfo, workspaces []contracts.Workspace,
	current contracts.Workspace, hasCurrent bool) []contracts.ChatSessionInfo {
	if !hasCurrent || strings.TrimSpace(current.ID) == "" {
		return all
	}
	known := make(map[string]bool, len(workspaces))
	for _, workspace := range workspaces {
		known[workspace.ID] = true
	}
	currentID := strings.TrimSpace(current.ID)
	scoped := make([]contracts.ChatSessionInfo, 0, len(all))
	for _, info := range all {
		id := strings.TrimSpace(info.WorkspaceID)
		if id == currentID || id == "" || !known[id] {
			scoped = append(scoped, info)
		}
	}
	return scoped
}

// maxMessageHits bounds the message-search result rows shown per query.
const maxMessageHits = 8

// messageSearchDebounce is how long the message search waits after the last
// keystroke before reading the store: a typed word costs one background read,
// not one synchronous full-table query per character (2026-11 audit AR-02).
// Aligned with the session Memory tab's memorySearchDebounce.
const messageSearchDebounce = 180 * time.Millisecond

// messageSearchFrame is one frame's full-text search view: whether the host
// has a message searcher at all, the visible hits for the current query and the
// last search error. The query's result is cached in the pane state, so a frame
// searches once per query no matter how many sections read it. reading marks a
// query whose background read has not landed yet: the frame shows a searching
// line instead of hits, and the empty states wait for the result.
type messageSearchFrame struct {
	available bool
	reading   bool
	hits      []contracts.MessageSearchHit
	err       error
}

// messageSearchFor resolves the current query's message search from the pane
// state only — a cache read plus the debounce scheduling, never store I/O
// inside the build frame (2026-11 audit AR-02). A changed query schedules one
// debounced background read whose result arrives through UIRuntime.Post; the
// cached result of the current query is filtered to the conversations the
// sidebar shows. A host without a store/searcher keeps the title-only behavior
// (available false).
func messageSearchFor(c *ui.Context, runtime uinative.UIRuntime, state *paneState, chat contracts.ChatService, query string, extraHidden map[string]bool) messageSearchFrame {
	if query == "" {
		state.searchPendingQuery = ""
		return messageSearchFrame{}
	}
	store, ok := state.ctx.Get(contracts.KeyStore)
	if !ok || store == nil {
		return messageSearchFrame{}
	}
	searcher, ok := store.(contracts.MessageSearcher)
	if !ok {
		return messageSearchFrame{}
	}
	if state.searchQuery == query {
		// The cached result belongs to this query: serve it without a read.
		state.searchPendingQuery = ""
		hidden := hiddenSessionIDs(chat)
		for id := range extraHidden {
			hidden[id] = true
		}
		return messageSearchFrame{
			available: true,
			hits:      visibleMessageHits(state.searchHits, hidden, maxMessageHits),
			err:       state.searchErr,
		}
	}
	if state.searchInFlight != query {
		scheduleMessageSearch(c, runtime, state, searcher, query)
	} else {
		state.searchPendingQuery = ""
	}
	// The read for this query has not landed: no stale hits, no premature
	// empty state — the message section shows the searching line.
	return messageSearchFrame{available: true, reading: true}
}

// scheduleMessageSearch debounces one changed search query: the read starts on
// its own goroutine after the user stops typing (messageSearchDebounce), so a
// typed word costs one read instead of one synchronous SQLite query per
// keystroke. The frame clock drives the delay (c.After wakes the starting
// frame), so headless tests stay deterministic with Tester.Advance.
func scheduleMessageSearch(c *ui.Context, runtime uinative.UIRuntime, state *paneState, searcher contracts.MessageSearcher, query string) {
	if state.searchPendingQuery != query {
		state.searchPendingQuery = query
		state.searchDue = c.Now().Add(messageSearchDebounce)
		c.After(messageSearchDebounce)
		return
	}
	if !c.Now().Before(state.searchDue) {
		startMessageRead(runtime, state, searcher, query)
	}
}

// startMessageRead reads one query on its own goroutine and publishes the
// result to the UI thread. A newer read or the module's disposal (searchSeq)
// drops this one, so a result for an abandoned query never replaces the visible
// hits and a late result after teardown is a quiet no-op.
func startMessageRead(runtime uinative.UIRuntime, state *paneState, searcher contracts.MessageSearcher, query string) {
	state.searchPendingQuery = ""
	state.searchInFlight = query
	state.searchSeq++
	sequence := state.searchSeq
	go func() {
		hits, err := searcher.SearchMessages(context.Background(), query, maxMessageHits)
		hits = dedupeMessageHits(hits)
		runtime.Post(func() {
			if sequence != state.searchSeq {
				return
			}
			state.searchInFlight = ""
			state.searchQuery = query
			state.searchHits, state.searchErr = hits, err
			// A new result set starts at its top, not at the previous offset.
			state.searchList = ui.ListState{}
			state.searchSel = -1
			runtime.Invalidate()
		})
	}()
}

// buildMessageSearch renders the durable full-text search results for the
// current query, grouped by conversation: each session gets one small header
// with the number of matching messages, and its hits below it (the old list
// repeated the session title on every row). The results come from
// messageSearchFor, which the session section also read, so the loading of the
// hits and the sections that show them cannot disagree about the match count.
func buildMessageSearch(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime uinative.UIRuntime, state *paneState, query string,
	search messageSearchFrame) {
	visibleHits := search.hits

	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, design.Space0, design.Space0, design.Space0).
		Children(func() {
			design.Apply(ui.Text(c, state.T("消息搜索", "Message search")), t.SectionLabel()).
				NoWrap().MaxLines(1)
			if search.reading {
				// The read happens off the UI thread (AR-02); say so instead of
				// flashing an empty state or leaving stale hits looking current
				// during the debounce. Same anatomy as the Memory tab's line.
				ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Spinner(c).Label(state.T("搜索中…", "Searching…")).
						Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
					design.Apply(ui.Text(c, state.T("搜索中…", "Searching…")),
						t.TextMuted(design.TypeXXS))
				})
				return
			}
			if search.err != nil {
				uikit.ErrorLine(c, state.T("搜索消息失败：", "Message search failed: ")+search.err.Error())
				return
			}
			if len(visibleHits) == 0 {
				sidebarEmptyLine(c, state.T("没有匹配的消息", "No matching messages"), design.Space1)
				return
			}
			design.Apply(ui.Text(c, state.T(
				strconv.Itoa(len(visibleHits))+" 条消息", strconv.Itoa(len(visibleHits))+" messages")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			rows := messageSearchRows(groupMessageHits(visibleHits, state))
			if state.searchSel >= len(rows) {
				state.searchSel = -1
			}
			state.searchList.Selected = &state.searchSel
			state.searchList.Key = func(index int) any {
				if index < 0 || index >= len(rows) {
					return nil
				}
				if rows[index].group != nil {
					return "group/" + strconv.Itoa(index)
				}
				return messageHitKey(rows[index].hit)
			}
			state.searchList.Label = func(index int) string {
				if index < 0 || index >= len(rows) {
					return ""
				}
				row := rows[index]
				if row.group != nil {
					return row.group.title
				}
				return row.groupTitle + " " + textutil.FirstNonEmpty(row.hit.Snippet, row.hit.Role)
			}
			list := ui.List(c, &state.searchList, len(rows)).
				Label(state.T("消息搜索", "Message search")).
				Gap(design.Space1).
				Rows(func(row ui.ListRow) {
					result := rows[row.Index]
					if result.group != nil {
						ui.Row(row.Context).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
							Padding(design.Space2, design.Space2, design.Space0, design.Space2).Children(func() {
							design.Apply(ui.Text(row.Context, result.group.title), t.Font(design.TypeXXS)).FontWeight(600).
								TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
							design.Apply(ui.Badge(row.Context, strconv.Itoa(len(result.group.hits))), t.Badge())
						})
						return
					}
					buildMessageHitRowContent(row.Context, t, state, result.hit, false, query)
				})
			// The optional session focus handoff: a message hit names the exact
			// message to show, and the session screen consumes that target on its
			// next build. A host without the service keeps the plain open.
			var focus contracts.SessionFocus
			if service, ok := state.ctx.Get(contracts.KeySessionFocus); ok {
				focus = service
			}
			activate := func() {
				if state.searchSel < 0 || state.searchSel >= len(rows) {
					return
				}
				if hit := rows[state.searchSel]; hit.group == nil {
					activateMessageHit(chat, nav, focus, runtime, hit.hit)
				}
			}
			if list.Changed() {
				activate()
			}
			if list.Submitted() {
				activate()
			}
		})
}

// messageHitGroup is one conversation's matching message hits.
type messageHitGroup struct {
	title string
	hits  []contracts.MessageSearchHit
}

// messageHitKey identifies one stored message hit; the durable search backend
// can return the same message twice (an imported index can hold it twice), and
// showing it twice would also duplicate the row's control identity.
func messageHitKey(hit contracts.MessageSearchHit) string {
	return hit.SessionID + "\x00" + hit.Role + "\x00" +
		hit.CreatedAt.UTC().Format(time.RFC3339Nano) + "\x00" + hit.Snippet
}

// dedupeMessageHits removes duplicate stored messages from a search result,
// preserving the order the search returned.
func dedupeMessageHits(hits []contracts.MessageSearchHit) []contracts.MessageSearchHit {
	seen := make(map[string]bool, len(hits))
	out := hits[:0]
	for _, hit := range hits {
		key := messageHitKey(hit)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, hit)
	}
	return out
}

// groupMessageHits folds the flat hit list into one group per conversation,
// preserving the order the search returned.
func groupMessageHits(hits []contracts.MessageSearchHit, state *paneState) []messageHitGroup {
	index := make(map[string]int, len(hits))
	groups := make([]messageHitGroup, 0, len(hits))
	for _, hit := range hits {
		key := strings.TrimSpace(hit.SessionID)
		if key == "" {
			key = "\x00" + hit.SessionTitle
		}
		at, ok := index[key]
		if !ok {
			at = len(groups)
			index[key] = at
			groups = append(groups, messageHitGroup{
				title: textutil.FirstNonEmpty(hit.SessionTitle, state.T("未命名会话", "Untitled session")),
			})
		}
		groups[at].hits = append(groups[at].hits, hit)
	}
	return groups
}

// messageSearchRow is one row of the message-search List: a conversation group
// header (its hit count) or one matching message.
type messageSearchRow struct {
	group      *messageHitGroup
	groupTitle string
	hit        contracts.MessageSearchHit
	ordinal    int
}

// messageSearchRows flattens the grouped hits into List rows: each section opens
// with its conversation header, then its matching messages.
func messageSearchRows(groups []messageHitGroup) []messageSearchRow {
	rows := make([]messageSearchRow, 0, len(groups)*2)
	for _, group := range groups {
		group := group
		rows = append(rows, messageSearchRow{group: &group})
		for hitIndex, hit := range group.hits {
			rows = append(rows, messageSearchRow{groupTitle: group.title, hit: hit, ordinal: hitIndex})
		}
	}
	return rows
}

// buildMessageHitRowContent renders one full-text hit: the role, time and the
// matching snippet. The search List owns selection, click and keyboard, so the
// row is content only. The snippet is centered on the first query match and
// draws the matches emphasized (R11/B9), so a long message does not show the
// query cut off at the row edge.
func buildMessageHitRowContent(c *ui.Context, t *design.Tokens, state *paneState,
	hit contracts.MessageSearchHit, showTitle bool, query string) {
	title := textutil.FirstNonEmpty(hit.SessionTitle, state.T("未命名会话", "Untitled session"))
	ui.Column(c).FillWidth().Gap(design.Space1).
		Padding(design.Space1, design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if showTitle {
				design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).
					FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			} else {
				ui.Spacer(c).Grow(1)
			}
			design.Apply(ui.Text(c, messageRoleLabel(hit.Role, state)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, formatHitTime(state.locale, hit.CreatedAt)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
		})
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			// The backend snippet is raw Markdown; clean it at render so the hit
			// reads as prose, not "## … ```bash … - …" noise (2026-11 UI review P2).
			buildHighlightedSnippet(c, t, query, uikit.PreviewProse(snippet, 0))
		}
	})
}

// buildHighlightedSnippet renders one message-search snippet with its query
// matches emphasized: the run before the first match is the quiet secondary
// text, each match is accent and semibold (B9). A snippet that does not contain
// the query (the store already excerpts around the match) renders unchanged.
func buildHighlightedSnippet(c *ui.Context, t *design.Tokens, query, snippet string) {
	spans := highlightSpans(snippetWindow(snippet, query), query, t)
	if len(spans) == 0 {
		spans = []ui.Span{{Text: snippet}}
	}
	design.Apply(ui.RichText(c, spans...), t.Font(design.TypeXXS)).
		TextColor(t.LabelSecondary).MaxLines(2)
}

// The snippet window measures: keep this many runes before the first match and
// this many after it, marking either cut with an ellipsis. The old row showed
// the message's start, so a match deep in the text was cut off.
const (
	snippetLead = 20
	snippetTail = 60
)

// snippetWindow cuts one snippet around its first query match. A snippet that
// already contains the match at its start (or does not contain it at all) is
// returned as-is; only a match that would fall out of the row moves the window.
func snippetWindow(snippet, query string) string {
	runes := []rune(snippet)
	start, _ := matchRunes(runes, query)
	if start < 0 {
		return snippet
	}
	from, to := start-snippetLead, start+len([]rune(query))+snippetTail
	if from < 0 {
		from = 0
	}
	if to > len(runes) {
		to = len(runes)
	}
	window := string(runes[from:to])
	if from > 0 {
		window = "…" + window
	}
	if to < len(runes) {
		window += "…"
	}
	return window
}

// highlightSpans splits text into the plain runs and its query matches, the
// matches accented and semibold. It returns nil when the text has no match, so
// the caller can render it as plain text.
func highlightSpans(text, query string, t *design.Tokens) []ui.Span {
	runes := []rune(text)
	matches := matchRunesAll(runes, query)
	if len(matches) == 0 {
		return nil
	}
	spans := make([]ui.Span, 0, len(matches)*2+1)
	last := 0
	for _, match := range matches {
		if match[0] > last {
			spans = append(spans, ui.Span{Text: string(runes[last:match[0]])})
		}
		spans = append(spans, ui.Span{
			Text:   string(runes[match[0]:match[1]]),
			Weight: 600,
			Color:  t.Accent,
		})
		last = match[1]
	}
	if last < len(runes) {
		spans = append(spans, ui.Span{Text: string(runes[last:])})
	}
	return spans
}

// matchRunes finds the first case-insensitive occurrence of query in runes and
// returns its rune bounds, or -1 when it does not occur.
func matchRunes(runes []rune, query string) (int, int) {
	matches := matchRunesAll(runes, query)
	if len(matches) == 0 {
		return -1, 0
	}
	return matches[0][0], matches[0][1]
}

// matchRunesAll returns every non-overlapping case-insensitive occurrence of
// query in runes, as rune bound pairs.
func matchRunesAll(runes []rune, query string) [][2]int {
	needle := []rune(query)
	if len(needle) == 0 {
		return nil
	}
	var matches [][2]int
	for at := 0; at+len(needle) <= len(runes); {
		if runesMatchAt(runes, needle, at) {
			matches = append(matches, [2]int{at, at + len(needle)})
			at += len(needle)
			continue
		}
		at++
	}
	return matches
}

// runesMatchAt reports whether needle matches runes at at, case insensitively.
func runesMatchAt(runes, needle []rune, at int) bool {
	for index := range needle {
		if at+index >= len(runes) {
			return false
		}
		if unicode.ToLower(runes[at+index]) != unicode.ToLower(needle[index]) {
			return false
		}
	}
	return true
}

// activateMessageHit opens the conversation one search hit belongs to and, when
// the hit names the message and the session module offers the optional focus
// handoff, asks it to show that message: the click lands on the matched message
// instead of the conversation's followed end.
func activateMessageHit(chat contracts.ChatService, nav contracts.Navigator,
	focus contracts.SessionFocus, runtime uinative.UIRuntime, hit contracts.MessageSearchHit) {
	_ = chat.SetActiveSession(hit.SessionID)
	if focus != nil {
		if messageID := strings.TrimSpace(hit.MessageID); messageID != "" {
			focus.FocusMessage(hit.SessionID, messageID)
		}
	}
	runtime.Post(func() { _ = nav.Push("session") })
	runtime.Invalidate()
}

// messageRoleLabel renders a message role for a search hit.
func messageRoleLabel(role string, state *paneState) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return state.T("用户", "User")
	case "assistant":
		return state.T("助手", "Assistant")
	case "tool":
		return state.T("工具", "Tool")
	case "":
		return state.T("消息", "Message")
	default:
		return strings.TrimSpace(role)
	}
}

// formatHitTime renders a hit timestamp in the user's date format (empty when
// unset), through the shared locale helper.
func formatHitTime(locale contracts.LocaleService, value time.Time) string {
	return uikit.FormatDateTime(locale, value)
}

// buildSidebarError is the sidebar's danger row: the shared uikit.ErrorLine
// anatomy (a role-alert live region a screen reader announces, a selectable
// message, the TypeXS metadata step), not a hand-built Row+Icon+11px Text that
// never reads as an alert (2026-11 audit UD-16). The 280 DIP sidebar holds the
// TypeXS line.
func buildSidebarError(c *ui.Context, t *design.Tokens, text string) {
	uikit.ErrorLine(c, text)
}

// sidebarEmptyLine renders one converged empty-state line (uikit.EmptyState,
// the TypeXS metadata step) with the sidebar's standard inset padding, so the
// list's several "nothing here" states share one anatomy instead of ad-hoc
// muted Text at drifting sizes (2026-11 audit UD-15).
func sidebarEmptyLine(c *ui.Context, text string, padTop float32) {
	ui.Column(c).FillWidth().
		Padding(padTop, design.Space2, design.Space0, design.Space2).Children(func() {
		uikit.EmptyState(c, text)
	})
}

// buildWorkspaceLine renders the sidebar's single read-only workspace line:
// "工作文件夹 / <current name>", with the folder's root as the name's tooltip.
// It carries no chevron and opens no dialog: the product shows one folder, and
// registration/switching belong to the host and the settings surface. A host
// whose registry has no open folder (a fresh or just-emptied registry) reads
// "未选择工作文件夹" instead of the pair.
func buildWorkspaceLine(c *ui.Context, t *design.Tokens, state *paneState,
	current contracts.Workspace, hasCurrent bool) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space1, design.Space1).Children(func() {
		ui.Icon(c, icons.Folder()).Size(design.IconSizeS, design.IconSizeS).
			TextColor(t.LabelTertiary)
		if !hasCurrent {
			design.Apply(ui.Text(c, state.T("未选择工作文件夹", "No workspace folder selected")),
				t.TextMuted(design.TypeXXS)).TextColor(t.LabelTertiary).
				Grow(1).MaxLines(1).Ellipsis("…")
			return
		}
		design.Apply(ui.Text(c, state.T("工作文件夹 /", "Workspace folder /")),
			t.TextMuted(design.TypeXXS)).TextColor(t.LabelTertiary).
			Shrink(0).MaxLines(1)
		// The name is the line's identity; its tooltip is the folder's root,
		// so two folders with the same base name stay apart.
		name := design.Apply(ui.Text(c, workspaceName(state, current, true)),
			t.Text(design.TypeXS)).TextColor(t.LabelPrimary).
			FontWeight(design.WeightMedium).Grow(1).Shrink(1).
			MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
		if root := strings.TrimSpace(current.Root); root != "" {
			name.Tooltip(root)
		}
	})
}

// workspaceName is a workspace's visible name: the registered name, else the
// folder's base name, else the "untitled" copy.
func workspaceName(state *paneState, workspace contracts.Workspace, has bool) string {
	if !has {
		return state.T("未选择工作文件夹", "No workspace folder selected")
	}
	if name := strings.TrimSpace(workspace.Name); name != "" {
		return name
	}
	if root := strings.TrimSpace(workspace.Root); root != "" {
		return textutil.FirstNonEmpty(filepath.Base(filepath.Clean(root)), root)
	}
	return state.T("未命名工作文件夹", "Untitled workspace")
}

// sessionLeadingGlyph draws a conversation's leading glyph: the run's phase
// (clock while waiting for the first output, spinner while the answer streams)
// or the conversation mark when idle. emphasized accents it for the chosen or
// active conversation.
func sessionLeadingGlyph(c *ui.Context, t *design.Tokens, state *paneState,
	session contracts.ChatSessionInfo, emphasized bool) {
	iconColor := t.LabelSecondary
	if emphasized {
		iconColor = t.Accent
	}
	switch {
	case session.Running && session.Waiting:
		ui.Icon(c, icons.Clock()).Size(design.IconSizeS, design.IconSizeS).TextColor(iconColor)
	case session.Running:
		ui.Spinner(c).Label(state.T("运行中", "Running")).Size(design.IconSizeS, design.IconSizeS)
	default:
		ui.Icon(c, icons.Message()).Size(design.IconSizeS, design.IconSizeS).TextColor(iconColor)
	}
}

// buildFlatSessionList renders the flat session list with mygo's native List:
// the rows are keyed by session ID, named by their title (the list's search
// ahead and assistive technology read it), and the choice follows the active
// session. Enter and a double click open the chosen session (the List's
// Submitted); a single pointer click opens through the row content's observer.
func buildFlatSessionList(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime uinative.UIRuntime, state *paneState,
	sessions []contracts.ChatSessionInfo) {
	active := chat.ActiveSession()
	// P2-7: mirror the active conversation into the list's choice only while the
	// conversation screen is current; on another screen the screen destination is
	// the single selection (see buildWorkspaceTree).
	selection := active
	if nav.Current() != "session" {
		selection = ""
	}
	activeRow := -1
	for index := range sessions {
		if sessions[index].ID == selection {
			activeRow = index
			break
		}
	}
	// Mirror the current session into the native choice (Selected) whenever it
	// changes outside the list; a keyboard cursor moved within the list is not
	// overwritten.
	if state.sessionActive != selection {
		state.sessionActive = selection
		state.sessionRow = activeRow
	}
	if state.sessionRow >= len(sessions) {
		state.sessionRow = -1
	}
	state.sessionList.Key = func(row int) any { return sessions[row].ID }
	state.sessionList.Label = func(row int) string { return sessionRowLabel(state, sessions[row]) }
	state.sessionList.Selected = &state.sessionRow
	var list ui.Element
	// The local selection theme: the native chosen-row fill is the quiet
	// selection tint, not the accent block (V1).
	ui.Themed(c, sessionSelectionTheme(t)).Background(ui.Transparent).FillWidth().Children(func() {
		list = ui.List(c, &state.sessionList, len(sessions)).Rows(func(row ui.ListRow) {
			// One row height for every conversation row: 32 DIP (B-spec §3.1),
			// not the 36 DIP control step the old fallback used to take.
			buildSessionRow(row.Context, t, chat, nav, runtime, state, sessions[row.Index],
				sessions[row.Index].ID == active, row.Index == state.sessionRow, design.RowCompactHeight, false)
		}).Bind(state.sessionListHandle())
	})
	if state.focusReturn != "" {
		// The dialog that owed the keyboard is gone: take it here, on the frame
		// the list is certainly on screen. The current row stays the session the
		// action was taken on, so the keyboard continues where it was.
		state.focusReturn = ""
		list.Focus(ui.FocusOptions{PreventScroll: true})
	}
	if list.Submitted() && state.sessionRow >= 0 && state.sessionRow < len(sessions) {
		openSession(chat, nav, runtime, state, sessions[state.sessionRow].ID)
	}
	// Without the list's focus, show the current session as the choice again:
	// a cursor left elsewhere is not a conversation switch.
	if !state.sessionList.FocusWithin(c) {
		state.sessionRow = activeRow
	}
}

// sessionSelectionTheme is the local theme the session list builds with:
// mygo paints a chosen row with Theme.Accent and Theme.AccentText, so a theme
// whose accent is the 12% selection tint turns the native chosen fill into the
// quiet persistent selection (BgSelected fill, LabelPrimary text) instead of a
// saturated accent block, while the list keeps its native keyboard, focus and
// accessibility selection (DESIGN §5.2 selection model, V1). The focus ring and
// the hover tint keep the theme's own colors. Only the persistent navigation
// list uses it; transient lists (the message-search results, menus, the
// command palette) keep the native accent fill.
func sessionSelectionTheme(t *design.Tokens) *ui.Theme {
	theme := *t.Theme()
	theme.Accent = t.BgSelected
	theme.AccentHover = t.BgSelected
	theme.AccentPressed = t.BgSelected
	theme.InactiveAccent = t.BgSelected
	theme.AccentText = t.LabelPrimary
	return &theme
}

// selectedBarWidth is the 2 DIP accent stripe of the persistent navigation
// selection (DESIGN §5.2: BgSelected + 2px Accent left bar). It is derived from
// the spacing unit so the stripe stays on the 4 DIP grid's half step.
const selectedBarWidth = design.Space1 / 2

// rowTint paints a self-drawn ButtonBase row's interaction states: selected and
// pressed use the stronger selection tint, hover the lighter hover tint. mygo's
// ButtonBase has no look of its own, so a row must paint all three explicitly
// (native buttons carry their own state machine).
//
// The tint fades over design.MotionFast instead of switching: a fill that cuts
// on every hover is the largest "this feels cheap" difference against the tools
// this product sits beside (B-spec §1.5/§4.1). The transition is attached by the
// caller through rowTintTransition, because mygo needs the element's identity to
// stay stable across the frames it animates between.
func rowTint(t *design.Tokens, el ui.Element, selected bool) ui.Element {
	switch {
	case el.Pressed():
		return el.Background(t.BgSelected)
	case selected:
		return el.Background(t.BgSelected)
	case el.Hovered():
		return el.Background(t.BgHover)
	default:
		return el
	}
}

// rowTintTransition attaches the row's colour transition, then paints the
// state. A row that only calls rowTint would still cut; this is the one call a
// self-drawn row needs, so the timing cannot go missing on one of them.
func rowTintTransition(c *ui.Context, t *design.Tokens, el ui.Element, selected bool) ui.Element {
	return rowTint(t, el.Transition(design.TransitionColors(c)), selected)
}

// ScreenNavSpec delegates to the shared uikit navigation table — the single
// source both the expanded sidebar rows and the host's collapsed rail read. The
// table moved to uikit so uiroot can consume it without importing this UI module
// (module boundary); the exported entry point stays for existing callers/tests
// (2026-11 UI review, boundary ruling).
func ScreenNavSpec(id string) (icon *ui.SVG, zh, en string) {
	return uikit.ScreenNavSpec(id)
}

// ScreenNavIcon is ScreenNavSpec's glyph, for the sidebar rows.
func ScreenNavIcon(id string) *ui.SVG {
	return uikit.ScreenNavIcon(id)
}

// ScreenNavLabel is ScreenNavSpec's localized label; an unregistered id falls
// back to the id itself.
func ScreenNavLabel(locale contracts.LocaleService, id string) string {
	return uikit.ScreenNavLabel(locale, id)
}

// screenNavIcon maps a registered screen id to the same glyph the host's
// collapsed rail uses, through the package's single ScreenNavSpec.
func screenNavIcon(id string) *ui.SVG { return ScreenNavIcon(id) }

// screenNavLabel renders a registered screen id as a sidebar label, through the
// package's single ScreenNavSpec.
func (state *paneState) screenNavLabel(id string) string { return ScreenNavLabel(state.locale, id) }

// screenNavLabel renders a registered screen id as a sidebar label: the known
// product screens get their localized title, other ids fall back to the id (the
// screen's registered title is not exposed by the ScreenLister read model).

// filterSessions keeps the sessions whose localized display title contains
// query, case insensitively. An empty query keeps every session. The title is
// the frame's localized one, so searching "Untitled" in English finds a
// blank-titled conversation the way "未命名" does in Chinese.
func filterSessions(sessions []contracts.ChatSessionInfo, query string, state *paneState) []contracts.ChatSessionInfo {
	if query == "" {
		return sessions
	}
	needle := strings.ToLower(query)
	filtered := make([]contracts.ChatSessionInfo, 0, len(sessions))
	for _, info := range sessions {
		if strings.Contains(strings.ToLower(state.sessionTitle(info)), needle) {
			filtered = append(filtered, info)
		}
	}
	return filtered
}

// pinnedFirst moves pinned sessions to the front while keeping the existing
// order inside both partitions. Sessions arrive newest-updated first, so this
// only reorders pinned vs unpinned; search results inherit the same order.
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

// hasHiddenOrigin reports whether a session's explicit provenance marks it as
// one the sidebar never shows. contracts.ChatSessionInfo.Origin is "" for a
// user session and carries the creating surface otherwise (the automation
// backend records "automation" for scheduled-run sessions, "subagent" for
// child conversations); every non-user origin is hidden. The title is
// deliberately not consulted: a user conversation may be titled "自动化: …"
// without being an automation run (audit F6).
func hasHiddenOrigin(info contracts.ChatSessionInfo) bool {
	return strings.TrimSpace(info.Origin) != contracts.SessionOriginUser
}

// visibleSessions drops the sessions the sidebar never renders: archived
// sessions (the settings surface's archive page owns them) and sessions with a
// non-user origin (the Automation screen owns its run history; subagent
// children are not user conversations). The remaining order is untouched
// (updated newest first).
func visibleSessions(sessions []contracts.ChatSessionInfo) []contracts.ChatSessionInfo {
	visible := make([]contracts.ChatSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if session.Archived || hasHiddenOrigin(session) {
			continue
		}
		visible = append(visible, session)
	}
	return visible
}

// recentSessions returns a copy ordered by recent activity: UpdatedAt
// descending, with the input order kept for equal timestamps (a stable sort
// keeps sessions created in the same instant in their read-model order).
func recentSessions(sessions []contracts.ChatSessionInfo) []contracts.ChatSessionInfo {
	if len(sessions) < 2 {
		return sessions
	}
	ordered := append([]contracts.ChatSessionInfo(nil), sessions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
	})
	return ordered
}

// orderSessionList applies the sidebar's one product ordering: recent activity
// first, pinned sessions ahead of the others. Archived sessions are already
// filtered out by visibleSessions.
func orderSessionList(sessions []contracts.ChatSessionInfo) []contracts.ChatSessionInfo {
	return pinnedFirst(recentSessions(sessions))
}

// hiddenSessionIDs is the set of conversations the sidebar never shows, for
// filtering the full-text search results (which come from the store and carry
// no archive or provenance flag of their own): archived sessions and sessions
// with a non-user origin. The read model is the provenance source, so a user
// title that merely looks like an automation run stays searchable.
func hiddenSessionIDs(chat contracts.ChatService) map[string]bool {
	hidden := make(map[string]bool)
	for _, session := range chat.Sessions() {
		if session.Archived || hasHiddenOrigin(session) {
			hidden[session.ID] = true
		}
	}
	return hidden
}

// visibleMessageHits drops search hits of hidden conversations (archived and
// non-user-origin sessions) and applies the row limit after that filter, so a
// hidden conversation's messages never take a slot. The read model is the only
// provenance source; a stored hit whose session is no longer in the read model
// carries no provenance and is not guessed to be a run from its title.
func visibleMessageHits(hits []contracts.MessageSearchHit, hidden map[string]bool, limit int) []contracts.MessageSearchHit {
	visible := make([]contracts.MessageSearchHit, 0, len(hits))
	for _, hit := range hits {
		if hidden[hit.SessionID] {
			continue
		}
		visible = append(visible, hit)
		if len(visible) == limit {
			break
		}
	}
	return visible
}

// buildSessionRow renders one session row's content: the message glyph, the
// title, the pinned/running badges and one quiet ⋯ menu at the row's end. The
// row itself is a native List/Outline row: it paints hover and selection, owns
// the keyboard and names the row for assistive technology. A single pointer
// click on the title area opens the session (the List's Submitted opens it for
// Enter and a double click); the same menu serves the ⋯ button and the row's
// right-click, as mygo recommends.
func buildSessionRow(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime uinative.UIRuntime, state *paneState,
	session contracts.ChatSessionInfo, active, chosen bool, height float32,
	leadingInPrefix bool) {
	label := state.sessionTitle(session)
	renamer, _ := chat.(contracts.SessionRenamer)
	forker, _ := chat.(contracts.SessionForker)
	pinner, _ := chat.(contracts.SessionPinner)
	archiver, _ := chat.(contracts.SessionArchiver)
	menu := func(m *ui.Menu) {
		if renamer != nil && m.Item(state.T("重命名", "Rename")).Chosen() {
			state.renameOpen = true
			state.renameErr = ""
			state.renameTarget = session.ID
			state.renameDraft = sessionTitleByID(chat, session.ID, state)
		}
		if forker != nil && m.Item(state.T("创建副本", "Duplicate")).Chosen() {
			newID, err := forker.ForkSession(session.ID)
			if err != nil {
				// A failed fork is reported, not silent (audit UX-10).
				state.sessionOpErr = state.T("创建副本失败：", "Could not duplicate the session: ") + err.Error()
				runtime.Invalidate()
			} else if strings.TrimSpace(newID) != "" {
				state.sessionOpErr = ""
				_ = chat.SetActiveSession(newID)
				runtime.Post(func() { _ = nav.Push("session") })
				runtime.Invalidate()
			}
		}
		if pinner != nil {
			pinLabel := state.T("置顶会话", "Pin session")
			if session.Pinned {
				pinLabel = state.T("取消置顶", "Unpin session")
			}
			if m.Item(pinLabel).Chosen() {
				if err := pinner.SetSessionPinned(session.ID, !session.Pinned); err != nil {
					state.sessionOpErr = state.T("更新置顶失败：", "Could not update the pin: ") + err.Error()
					runtime.Invalidate()
				} else {
					state.sessionOpErr = ""
					runtime.Invalidate()
				}
			}
		}
		if archiver != nil && !session.Archived {
			// Archived sessions never render here; the settings surface's
			// archive page is where they can be restored. Archiving reports
			// success through a toast (with Undo when the host supports it).
			if m.Item(state.T("归档会话", "Archive session")).Chosen() {
				if err := archiver.SetSessionArchived(session.ID, true); err != nil {
					state.sessionOpErr = state.T("归档失败：", "Could not archive the session: ") + err.Error()
					runtime.Invalidate()
				} else {
					state.sessionOpErr = ""
					raiseSessionArchivedToast(state, chat, runtime, session.ID, label)
					runtime.Invalidate()
				}
			}
		}
		// Deleting a conversation is destructive: the menu item carries the
		// danger color and the row dialog still confirms it.
		if m.Item(state.T("删除", "Delete")).Danger().Chosen() {
			state.deleteOpen = true
			state.deleteErr = ""
			state.deleteTarget = session.ID
			state.deleteTitle = state.sessionTitle(session)
			state.deleteTrigger = state.sessionMenuHandle(session.ID)
		}
		if !active {
			m.Separator()
			if m.Item(state.T("设为当前", "Set as current")).Chosen() {
				openSession(chat, nav, runtime, state, session.ID)
			}
		}
	}

	c.Key("session/" + session.ID)
	// Grow, not FillWidth: the row content sits beside the List's own leading
	// space, and a 100%-width child would overflow it and squeeze the row-end
	// actions.
	content := ui.Row(c).Grow(1).Shrink(1).AlignItems(ui.Center).Gap(design.Space1)
	// The menu control is built always (so assistive technology describes it)
	// but stays invisible and out of the pointer's way until the row is
	// hovered or focused, as mygo's IgnoreInput pattern for a row action asks.
	// The chosen row is not special: its live status word needs the row-end
	// room more than an always-visible ⋯ does, and a right-click opens the
	// same menu.
	revealed := content.Hovered() || content.FocusWithin()
	content.Children(func() {
		if !leadingInPrefix {
			// The flat fallback list has no row prefix: the persistent
			// selection's accent stripe and the conversation glyph are the
			// row's own first children. The stripe is built on every row
			// (transparent when resting) so the title does not shift when the
			// selection moves.
			bar := ui.Box(c).Width(selectedBarWidth).Height(height).Shrink(0)
			if chosen {
				bar = bar.Background(t.Accent)
			}
		}
		// The title area is the click target that opens the session. The List
		// row also selects on a click; observing the pointer here gives the
		// single click the source-list behavior (open the conversation), while
		// Enter and a double click stay the native List's Submitted.
		title := ui.Row(c).FillWidth().Height(height).AlignItems(ui.Center).
			Gap(design.Space2).
			PaddingEnd(design.Space1).Radius(design.RadiusSM)
		if leadingInPrefix {
			// The prefix already paid the stripe, the indent and the glyph:
			// the title needs only the gap after the leading slot.
			title = title.PaddingStart(design.Space1)
		} else {
			// The stripe and the row gap are paid for here, so the title's text
			// keeps the same inset as a resting row's.
			title = title.PaddingStart(design.Space3 - selectedBarWidth - design.Space1)
		}
		title.ObserveInput(func(ev ui.InputEvent) {
			if ev.Kind != ui.InputPointerUp || ev.Button != 0 {
				return
			}
			openSession(chat, nav, runtime, state, session.ID)
		})
		title.Children(func() {
			// The leading glyph carries the run's phase: a clock while the
			// prompt waits for the model's first output (the reply has not
			// started), the spinner once the answer streams, and the
			// conversation glyph when idle. The state word sits at the row end,
			// so the title keeps most of its width.
			if !leadingInPrefix {
				sessionLeadingGlyph(c, t, state, session, chosen || active)
			}
			// The session row's title is the 13 DIP list step, 500 at rest
			// (B-spec §3.1: 会话行 32 高、标题 13). One role carries 13 and its
			// baked weight is 400, so the row names the weight explicitly: the
			// resting/read vs unread pair IS this row's affordance, and the
			// unread one must scan stronger before its dot registers. This is
			// the single documented exception to the role-baked weight rule.
			titleText := design.Apply(ui.Text(c, label), t.Text(design.TypeXS)).
				FontWeight(design.WeightMedium).
				TextColor(t.LabelPrimary).Grow(1).MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
			if session.Unread {
				titleText = titleText.FontWeight(design.WeightSemibold)
			}
			// Pinned is a stable property, not a run state, so it keeps the
			// quiet badge; the live run state moved to the row end (see below).
			if session.Pinned {
				design.Apply(ui.Badge(c, state.T("置顶", "Pinned")), t.Badge())
			}
			// The active session is already carried by the accent icon and the
			// selected row; a 当前 badge was redundant and stole width from the
			// title.
		})
		menuLabel := state.T("会话操作：", "Session actions: ") + label
		// The row's ⋯ is the product's one icon-button entry, not a fourth
		// hand-written state machine: uikit.IconButton carries the Label, the
		// ≥24 DIP hit target, the ink and the MotionFast colour transition
		// between rest, hover and pressed (2026-11 audit UD-09/UD-18, A1
		// matrix "sidebar.go:2205 → 迁移"). Ghost, not the outlined Native
		// look: §2 forbids borders inside a sidebar row, and the row's own
		// hover tint already says the row is live.
		// Bound to the row's stable handle: the delete confirmation gives the
		// keyboard back through it when it closes (A4-I-02). No Tooltip — the
		// open menu is an Escape-owning surface (A4-I-01).
		more := uikit.IconButton(c, uikit.IconButtonOptions{
			Key:      "sidebar.session.menu/" + session.ID,
			Label:    menuLabel,
			Icon:     icons.More(),
			Size:     design.IconSizeS,
			InkColor: t.LabelSecondary,
		}).Bind(state.sessionMenuHandle(session.ID))
		if !revealed {
			// Built always so assistive technology can name it, out of the
			// pointer's way until the row is hovered or focused. v0.9.2 cannot
			// animate the opacity of an element that is already built (only
			// Enter/Exit motions fade), so the reveal itself is instant; the
			// button's own colour transition is what fades.
			more = more.Opacity(0).IgnoreInput(true)
		}
		// The menu opens below the button, lined up with its end, so it never
		// overhangs the sidebar's content.
		more.PopoverMenuButton(menu, ui.PopoverMenuButtonOptions{
			At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd,
		})
		// The row's live detail: the state word while the model works (等待回复
		// before its first output, 运行中 after), or one accent dot for a
		// finished reply the user has not opened yet. It is the last child, so
		// it stays put at the row end when the hover-only menu appears, and the
		// title keeps the row's reading edge.
		sessionLiveStatus(c, t, state, session)
	})
	content.ContextMenu(menu)
}

// sessionLiveStatus draws a session row's live detail at its end: the run
// state while the model is working, or the unread mark of a reply that arrived
// in the background. The detail belongs at the row end so the title keeps the
// column's one left-aligned reading edge.
func sessionLiveStatus(c *ui.Context, t *design.Tokens, state *paneState,
	session contracts.ChatSessionInfo) {
	if session.Running {
		label := state.T("运行中", "Running")
		if session.Waiting {
			label = state.T("等待回复", "Waiting")
		}
		// The leading spinner carries the motion; this micro label keeps the
		// state readable when motion is reduced or the frame is still, without
		// taking a session title's last characters.
		design.Apply(ui.Text(c, label), t.Font(design.TypeXXXS)).
			TextColor(t.LabelSecondary).MaxLines(1).Shrink(0)
		return
	}
	if !session.Unread {
		return
	}
	// The dot is the visible mark; sessionRowLabel carries the words for
	// assistive technology and list type-ahead, so the state is never
	// color-only.
	ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
		Background(t.Accent).Shrink(0)
}

// sessionRowLabel is the row's accessible name and type-ahead text: the
// session title plus its live state, in the words the row's glyphs and dot
// cannot speak.
func sessionRowLabel(state *paneState, session contracts.ChatSessionInfo) string {
	label := state.sessionTitle(session)
	switch {
	case session.Running && session.Waiting:
		return label + "，" + state.T("等待回复", "waiting for a reply")
	case session.Running:
		return label + "，" + state.T("运行中", "running")
	case session.Unread:
		return label + "，" + state.T("未读回复", "unread reply")
	default:
		return label
	}
}

// openSession activates a session, makes its workspace current and opens the
// session screen on the next frame. The screen switch is queued because this
// frame's host-side native Sidebar reconciliation would otherwise restore the
// previously selected destination; an already active session is not set again.
func openSession(chat contracts.ChatService, nav contracts.Navigator,
	runtime uinative.UIRuntime, state *paneState, sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if chat.ActiveSession() != sessionID {
		_ = chat.SetActiveSession(sessionID)
	}
	switchSessionWorkspace(chat, state, sessionID)
	runtime.Post(func() { _ = nav.Push("session") })
	runtime.Invalidate()
}

// switchSessionWorkspace makes the workspace a session belongs to current, so
// the agent's file/shell tools and the Files inspector follow the open
// conversation instead of whichever workspace the user last selected. It is
// best effort: a session without a binding, a missing backend or a failing open
// never blocks the conversation switch.
func switchSessionWorkspace(chat contracts.ChatService, state *paneState, sessionID string) {
	if state == nil {
		return
	}
	workspace, ok := state.ctx.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return
	}
	target := ""
	for _, info := range chat.Sessions() {
		if info.ID == sessionID {
			target = strings.TrimSpace(info.WorkspaceID)
			break
		}
	}
	if target == "" {
		return
	}
	if current, has, err := workspace.Current(context.Background()); err == nil && has && current.ID == target {
		return
	}
	if _, err := workspace.Open(context.Background(), target); err == nil {
		// The agent tools/plugins follow the conversation's folder.
		mcpreload.Trigger(state.ctx)
	}
}

// buildFooter is the docked sidebar footer from the old shell: the settings
// entry plus the archived-sessions view option, both as navigation rows (36
// DIP, icon and label, the whole row clickable) so the footer reads as part of
// the sidebar instead of two small outlined buttons. It also owns the
// window-level delete confirmation; session rename/delete live on the row's
// native popover menu.
func buildFooter(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime uinative.UIRuntime,
	state *paneState, label string) {
	// A modal can flip the dialog's open flag while it dismisses itself, after
	// the builders ran: the frame that sees it already closed must still do the
	// close bookkeeping, or the keyboard return never happens (A4-I-02).
	if !state.deleteOpen && state.deleteWasOpen {
		state.deleteWasOpen = false
		state.focusReturn = state.deleteTarget
		state.deleteTarget = ""
		state.deleteTitle = ""
		state.deleteErr = ""
		runtime.Invalidate()
	}
	if state.deleteOpen {
		state.deleteWasOpen = true
		deleteTitle := textutil.FirstNonEmpty(state.deleteTitle, state.T("未命名会话", "Untitled session"))
		target := state.deleteTarget
		messageTemplate := state.T("“%s”及其全部消息将被删除，此操作无法撤销。",
			"“%s” and all its messages will be deleted. This cannot be undone.")
		message := fmt.Sprintf(messageTemplate, deleteTitle)
		buildDangerConfirm(c, t, &state.deleteOpen,
			state.T("删除会话", "Delete session"), message, state.deleteErr,
			state.T("删除", "Delete"), state.T("取消", "Cancel"), state.deleteTrigger,
			func() {
				if err := chat.DeleteSession(target); err != nil {
					// A failed delete keeps the dialog open and names the
					// failure: closing it would leave the session in place and
					// read as a success (audit UX-10).
					state.deleteErr = state.T("删除会话失败：", "Could not delete the session: ") + err.Error()
					state.deleteOpen = true
					runtime.Invalidate()
					return
				}
				state.deleteErr = ""
				runtime.Invalidate()
			},
			func() {
				// The confirm callback may have reopened the dialog after a
				// failure; only a real close drops the pending target.
				if state.deleteOpen {
					return
				}
				// The keyboard goes back to the ⋯ that opened the dialog on the
				// NEXT frame: the modal restores focus to its own opener while it
				// closes, so a request made in this frame is overwritten
				// (A4 focus-return).
				// The keyboard goes back to this row's action on the next build,
				// where the row can make its ⋯ visible before focusing it: a
				// hover-only control is out of the accessibility tree and cannot
				// take the focus (A4-I-02).
				if state.deleteTrigger != nil {
					state.deleteTrigger = nil
					state.focusReturn = target
				}
				state.deleteTarget = ""
				state.deleteTitle = ""
				state.deleteErr = ""
				runtime.Invalidate()
			})
		return
	}

	// The footer is a docked section, not a floating last row: a hairline
	// closes the scrolling list above it, and the settings row keeps the same
	// 36-DIP height and inset as the navigation rows so the column has one
	// alignment.
	ui.Column(c).FillWidth().Children(func() {
		ui.Column(c).FillWidth().PaddingX(design.Space3).Children(func() {
			ui.Divider(c)
		})
		ui.Row(c).FillWidth().
			Padding(design.Space2, design.Space3, design.Space2, design.Space3).
			Children(func() {
				c.Key("sidebar/footer/settings")
				settingsRow := ui.ButtonBase(c).FillWidth().Height(design.ControlHeight).
					PaddingX(design.Space3).Radius(design.RadiusSM).Label(label)
				settingsRow = rowTintTransition(c, t, settingsRow, false)
				settingsRow.Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						ui.Icon(c, icons.Sliders()).Size(design.IconSizeS, design.IconSizeS).
							TextColor(t.LabelSecondary)
						design.Apply(ui.Text(c, label), t.Text(design.TypeS)).TextColor(t.LabelSecondary)
					})
				})
				if settingsRow.Clicked() {
					nav.ToggleSettings()
					runtime.Invalidate()
				}
			})
	})
}

// buildRenameDialog renders the rename Modal on the window-level overlay slot.
// It is only visible while the footer's rename action asked for it; Escape or a
// click outside closes it through the native Modal's open pointer. It follows
// the product dialog convention (uikit.DialogFooter, the input focused as the
// dialog opens, Enter saving): the same rules every other dialog in the product
// has.
func buildRenameDialog(c *ui.Context, t *design.Tokens, renamer contracts.SessionRenamer,
	runtime uinative.UIRuntime, state *paneState) {
	if !state.renameOpen || renamer == nil {
		return
	}
	target := state.renameTarget
	save := func() {
		title := strings.TrimSpace(state.renameDraft)
		switch {
		case title == "":
			state.renameErr = state.T("标题不能为空", "Title cannot be empty")
		case target == "":
			state.renameErr = state.T("没有活动会话", "No active session")
		default:
			if err := renamer.RenameSession(target, title); err != nil {
				state.renameErr = err.Error()
			} else {
				state.renameOpen = false
				state.renameErr = ""
			}
		}
		runtime.Invalidate()
	}
	ui.Modal(c, &state.renameOpen, func() {
		ui.Column(c).Width(design.DialogWidthXS).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, state.T("重命名会话", "Rename session")), t.Text(design.TypeM)).FontWeight(600)
			// One line of input, focused as the dialog opens; Enter saves in
			// place while the input keeps the keyboard.
			ui.TextInput(c, &state.renameDraft).Label(state.T("会话标题", "Session title")).
				FillWidth().AutoFocus().OnTextKey(uikit.EnterTo(save))
			if state.renameErr != "" {
				design.Apply(ui.Text(c, state.renameErr), t.TextMuted(design.TypeXS)).
					TextColor(t.DangerText)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: state.T("取消", "Cancel"), OnClick: func() {
					state.renameOpen = false
					state.renameErr = ""
					runtime.Invalidate()
				}},
				{Label: state.T("保存", "Save"), Primary: true, OnClick: save},
			})
		})
	})
}

// sessionTitleByID looks a session up and returns its display title.
func sessionTitleByID(chat contracts.ChatService, sessionID string, state *paneState) string {
	if session, ok := chat.Session(sessionID); ok {
		if title := strings.TrimSpace(session.Title); title != "" {
			return title
		}
	}
	for _, info := range chat.Sessions() {
		if info.ID == sessionID {
			return state.sessionTitle(info)
		}
	}
	return state.T("未命名会话", "Untitled session")
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
