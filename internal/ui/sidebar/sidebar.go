// Package sidebar contributes the runtime sidebar of the product: the primary
// "new chat" action, the searchable chat session list, the session lifecycle
// actions and the settings entry.
//
// It is a UI module like ui/home: a kernel Descriptor that requires the host UI
// runtime and the chat read model, registers slot items on activation and hands
// the unregister functions (and the chat subscription) to kernel effects so
// disposal unwinds them. The host owns the native Sidebar/SidebarSection shell;
// this module only injects native controls (SearchField, List, Outline, Button,
// ButtonBase, Modal, AlertDialog, Icon, Text, Badge) styled through
// internal/design. It never draws a control by hand.
//
// # Session list and workspace tree
//
// The session list is a native ui.List: rows are keyed by session ID, named by
// their title, the choice follows the active session, Enter opens the chosen
// row and a single click opens the row (opened from the row content). With a
// workspace backend the rows become a native ui.Outline instead: workspace
// groups are the tree's roots (OutlineState.Open replaces the old collapse map)
// and their sessions the children, so the tree gains the disclosure arrows, the
// Up/Down and Right/Left keyboard and the tree semantics the host's source list
// uses. Every row's ⋯ action and its right-click share one menu; every
// workspace group row also carries the per-workspace new-chat action (⊕) that
// opens the workspace and starts a conversation in it.
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
// never render as a row, a tree leaf or a search result. The sidebar is also
// the landing surface: there is no Home navigation row (the home screen stays
// registered for the host), so the new chat action and the session list are the
// destinations the sidebar offers.
//
// The product has exactly one view: groups are ordered by recent activity and
// sessions by UpdatedAt (pinned first) inside them; there is no view menu and
// no persisted sort/group setting. A host without a workspace backend keeps
// the single flat list as a graceful fallback. Adding a workspace runs the
// native directory picker directly (the backend derives the name from the
// folder), so there is no naming dialog.
//
// # Session operations
//
// The search field filters sessions case-insensitively by title (an empty
// title is shown as "未命名会话") and searches message bodies below: the two
// result kinds share one query. A title miss is never reported as "no results"
// while message hits are about to show — the quiet "标题无匹配" line follows the
// message results, and the single "没有匹配" state appears only when neither a
// title nor a message matched. Snippets are windowed around the first match and
// the matched words are emphasized.
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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
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

	// Workspace tree state (group mode): the native Outline's rows and the
	// items showing their children (workspace keys), the row chosen and the
	// active session last mirrored into it. treeKnown remembers the roots
	// seeded open, so a workspace the user collapsed stays collapsed.
	tree       ui.OutlineState[string]
	treeRow    int
	treeActive string
	treeKnown  map[string]bool

	// Pending delete confirmation of one workspace opened from its row menu,
	// and the last failure of one of those menu actions.
	workspaceDeleteOpen bool
	workspaceDeleteID   string
	workspaceDeleteName string
	workspaceDeleteErr  string

	// screens lists the registered screens so the sidebar can offer a
	// generic navigation item for every module screen it does not own (the
	// session list is the sidebar's own screen). Home is no longer a sidebar
	// destination, so it stays out of the generic rows as well.
	screens contracts.ScreenLister

	// Full-text message search state: the query the cached hits belong to and
	// the last result/error. Hits are re-queried only when the query changes.
	searchQuery string
	searchHits  []contracts.MessageSearchHit
	searchErr   error
	// searchList backs the message-search hits' official List; searchSel is the
	// row chosen (hit rows activate their conversation, group headers do not).
	searchList ui.ListState
	searchSel  int

	// notifier is the optional host toast bridge: archiving a session reports
	// success there (with Undo when the host supports action toasts), and the
	// workspace add/report feedback prefers it over the header's status rows.
	notifier contracts.Notifier

	renameOpen   bool
	renameTarget string
	renameDraft  string
	renameErr    string

	// Add-workspace feedback: the last failure and the last quiet success
	// status (only used when the host has no notifier: the picker flow has no
	// naming dialog, one native folder chooser runs the whole action).
	addWorkspaceErr    string
	addWorkspaceStatus string
	// workspaceNewChatErr is the last per-workspace "new chat" action failure.
	workspaceNewChatErr string

	deleteOpen   bool
	deleteTarget string
	deleteTitle  string
}

// T returns the localized copy for the current frame.
func (state *paneState) T(zh, en string) string { return uikit.T(state.locale, zh, en) }

// newChatLabel is the primary action label for the current frame.
func (state *paneState) newChatLabel() string {
	return firstNonEmpty(state.cfgNewChatLabel, state.T("新对话", "New chat"))
}

// settingsLabel is the footer settings label for the current frame.
func (state *paneState) settingsLabel() string {
	return firstNonEmpty(state.cfgSettingsLabel, state.T("设置", "Settings"))
}

// sessionsTitle is the flat session-list section label for the current frame.
func (state *paneState) sessionsTitle() string {
	return firstNonEmpty(state.cfgSessionsTitle, state.T("会话", "Chats"))
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

		state := &paneState{
			ctx:              ctx,
			sessionRow:       -1,
			treeRow:          -1,
			searchSel:        -1,
			treeKnown:        make(map[string]bool),
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
				buildPrimary(c, design.Of(c), chat, nav, runtime, state)
			},
		}
		navSlot := contracts.SlotItem{
			Slot:  contracts.SlotSidebarNav,
			ID:    "sidebar.nav.sessions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildSessionList(c, design.Of(c), chat, nav, runtime, state, state.sessionsTitle())
			},
		}
		footer := contracts.SlotItem{
			Slot:  contracts.SlotSidebarFooter,
			ID:    "sidebar.footer.actions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildFooter(c, design.Of(c), chat, nav, runtime, state, state.settingsLabel())
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
		workspaceDialog := contracts.SlotItem{
			Slot:  contracts.SlotOverlayDialogs,
			ID:    "sidebar.workspace.dialog",
			Order: 30,
			Build: func(c *ui.Context) {
				workspace, _ := state.ctx.Get(contracts.KeyWorkspace)
				buildWorkspaceDeleteDialog(c, workspace, runtime, state)
			},
		}

		for _, item := range []contracts.SlotItem{primary, navSlot, footer, renameDialog, workspaceDialog} {
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

// buildPrimary is the brand "new chat" action: the official primary button at
// the standard 36 DIP control height with the NewChat glyph and its label as
// the content; the label stays the accessible name. With an active session the
// action drops to the neutral outlined secondary style: a chosen session row
// and a filled accent button next to it would be two loud accent blocks on one
// screen, and the screen's single primary belongs to the composer then (DESIGN
// §5.2 accent budget). The landing/empty state keeps the filled primary.
// Creating a chat in the current workspace also unfolds that workspace's group,
// so the fresh row is visible.
func buildPrimary(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState) {
	label := state.newChatLabel()
	hasSelection := strings.TrimSpace(chat.ActiveSession()) != ""
	ui.Row(c).FillWidth().
		Padding(design.Space2, design.Space3, design.Space2, design.Space3).
		Children(func() {
			var button ui.Element
			iconColor, textColor := t.AccentText, t.AccentText
			if hasSelection {
				// The outlined secondary action: the native button look keeps
				// hover/pressed/disabled, the brand fixes the control metrics.
				button = design.Apply(ui.Button(c, ""), t.SecondaryButton()).
					FillWidth().Label(label)
				iconColor, textColor = t.Accent, t.LabelPrimary
			} else {
				button = design.Apply(ui.PrimaryButton(c, ""), t.PrimaryButton()).
					FillWidth().Label(label)
			}
			button.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Center).
					Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.NewChat()).Size(design.IconSizeS, design.IconSizeS).TextColor(iconColor)
					design.Apply(ui.Text(c, label), t.Text(design.TypeBody)).
						FontWeight(500).TextColor(textColor)
				})
			})
			if button.Clicked() {
				// NewSession marks the session active; navigate afterwards. A
				// headless or partial assembly may not have the screen yet, so a
				// failed push must not break the frame.
				if _, err := chat.NewSession(""); err == nil {
					if current, ok := state.currentWorkspace(); ok {
						state.tree.Open.Add(workspaceTreeKey(current.ID))
					}
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

// currentWorkspace resolves the optional workspace backend's current
// workspace. It is a best-effort read: a host without the backend (or with a
// failing one) simply has no workspace to unfold.
func (state *paneState) currentWorkspace() (contracts.Workspace, bool) {
	workspace, ok := state.ctx.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return contracts.Workspace{}, false
	}
	current, has, err := workspace.Current(context.Background())
	if err != nil || !has {
		return contracts.Workspace{}, false
	}
	return current, true
}

// buildSessionList renders the sidebar's navigation column: the generic module
// screen destinations, then the "工作区" → session tree when a workspace backend
// is available (contracts.KeyWorkspace) or the legacy flat session list
// otherwise. There is no Home destination row (the home screen stays registered
// for the host, but the new-chat action and the session list are the sidebar's
// landing surface). The search field filters session titles across every group;
// clicking a session activates it and opens the session screen.
func buildSessionList(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState, title string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, design.Space0, design.Space0, design.Space0).
		Children(func() {
			// Generic module-screen destinations: every registered screen the
			// sidebar does not own itself (the session list is its own screen, and
			// the home screen is intentionally not a destination) gets one native
			// navigation item, so a new screen module (for example
			// ui/automation) appears without a sidebar edit.
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
				buildWorkspaceSectionHeader(c, t, workspace, runtime, state)
			} else {
				// The flat fallback exists only for a host without a workspace
				// backend: the product always groups by workspace when it can,
				// so there is no view menu here either.
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, title), t.SectionLabel())
				})
			}
			ui.Row(c).FillWidth().Padding(design.Space0, design.Space1, design.Space0, design.Space1).
				Children(func() {
					searchLabel := state.T("搜索会话与消息", "Search chats and messages")
					ui.SearchField(c, &state.query).
						Label(searchLabel).
						Placeholder(searchLabel).FillWidth()
				})

			query := strings.TrimSpace(state.query)
			// Archived sessions never render in the sidebar (the settings
			// surface's archive page owns them), not in the list and not as a
			// search result.
			allSessions := visibleSessions(chat.Sessions())
			sessions := orderSessionList(filterSessions(allSessions, query, state))
			// A query has two result kinds: session titles and message bodies.
			// Both are loaded before either section renders, so a title miss
			// never reads as "no results" while the message matches below it
			// are about to show (R11).
			search := messageSearchFor(state, chat, query)
			titleHits := len(sessions)
			messageHits := len(search.hits)
			// degradedTitle is the title miss under message hits: the session
			// empty state becomes one quiet line after the message results.
			degradedTitle := query != "" && titleHits == 0 && messageHits > 0
			// overallEmpty is the one "no matches" state: neither a title nor
			// a message matched, or the host has no message searcher at all.
			overallEmpty := query != "" && titleHits == 0 && messageHits == 0 && search.err == nil
			switch {
			case overallEmpty:
				// One empty state for the whole search, not a session one
				// followed by a message one (R11).
				design.Apply(ui.Text(c, state.T("没有匹配", "No matches")), t.TextMuted(design.TypeXXS)).
					Padding(design.Space1, design.Space2, design.Space0, design.Space2)
			case hasWorkspace:
				// Grouping is the product's only mode: the workspace tree (and
				// its per-workspace row actions) renders even with no sessions,
				// so a fresh install or a just-added workspace still shows its
				// group row and new-chat button.
				buildWorkspaceTree(c, t, chat, nav, runtime, state, workspace,
					workspaces, allSessions, sessions, query, degradedTitle)
			case len(sessions) == 0:
				// A query with message hits (or a message-search failure) leaves
				// the session section to the message section below; only the
				// plain no-query empty state and the unsearched title miss stay
				// here.
				if query == "" {
					design.Apply(ui.Text(c, state.T("还没有会话", "No sessions yet")),
						t.TextMuted(design.TypeXXS)).
						Padding(design.Space1, design.Space2, design.Space0, design.Space2)
				} else if !degradedTitle {
					design.Apply(ui.Text(c, state.T("没有匹配的会话", "No matching sessions")),
						t.TextMuted(design.TypeXXS)).
						Padding(design.Space1, design.Space2, design.Space0, design.Space2)
				}
			default:
				buildFlatSessionList(c, t, chat, nav, runtime, state, sessions)
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
				design.Apply(ui.Text(c, state.T("标题无匹配", "No matching titles")),
					t.TextMuted(design.TypeXXS)).
					Padding(design.Space2, design.Space2, design.Space0, design.Space2)
			}
		})
}

// maxMessageHits bounds the message-search result rows shown per query.
const maxMessageHits = 8

// messageSearchFrame is one frame's full-text search view: whether the host
// has a message searcher at all, the visible hits for the current query and the
// last search error. The query's result is cached in the pane state, so a frame
// searches once per query no matter how many sections read it.
type messageSearchFrame struct {
	available bool
	hits      []contracts.MessageSearchHit
	err       error
}

// messageSearchFor resolves the current query's message search: the cached
// store result, filtered to the conversations the sidebar shows. A host
// without a store/searcher keeps the title-only behavior (available false).
func messageSearchFor(state *paneState, chat contracts.ChatService, query string) messageSearchFrame {
	if query == "" {
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
	if state.searchQuery != query {
		state.searchQuery = query
		hits, err := searcher.SearchMessages(context.Background(), query, maxMessageHits)
		state.searchHits, state.searchErr = dedupeMessageHits(hits), err
	}
	return messageSearchFrame{
		available: true,
		hits:      visibleMessageHits(state.searchHits, hiddenSessionIDs(chat), maxMessageHits),
		err:       state.searchErr,
	}
}

// buildMessageSearch renders the durable full-text search results for the
// current query, grouped by conversation: each session gets one small header
// with the number of matching messages, and its hits below it (the old list
// repeated the session title on every row). The results come from
// messageSearchFor, which the session section also read, so the loading of the
// hits and the sections that show them cannot disagree about the match count.
func buildMessageSearch(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState, query string,
	search messageSearchFrame) {
	visibleHits := search.hits

	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, design.Space0, design.Space0, design.Space0).
		Children(func() {
			design.Apply(ui.Text(c, state.T("消息搜索", "Message search")), t.SectionLabel()).
				NoWrap().MaxLines(1)
			if search.err != nil {
				uikit.ErrorLine(c, state.T("搜索消息失败：", "Message search failed: ")+search.err.Error())
				return
			}
			if len(visibleHits) == 0 {
				design.Apply(ui.Text(c, state.T("没有匹配的消息", "No matching messages")), t.TextMuted(design.TypeXXS)).
					Padding(design.Space1, design.Space2, design.Space0, design.Space2)
				return
			}
			design.Apply(ui.Text(c, state.T(
				strconv.Itoa(len(visibleHits))+" 条消息", strconv.Itoa(len(visibleHits))+" messages")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
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
				return row.groupTitle + " " + firstNonEmpty(row.hit.Snippet, row.hit.Role)
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
			activate := func() {
				if state.searchSel < 0 || state.searchSel >= len(rows) {
					return
				}
				if hit := rows[state.searchSel]; hit.group == nil {
					activateMessageHit(chat, nav, runtime, hit.hit)
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
				title: firstNonEmpty(hit.SessionTitle, state.T("未命名会话", "Untitled session")),
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
	title := firstNonEmpty(hit.SessionTitle, state.T("未命名会话", "Untitled session"))
	ui.Column(c).FillWidth().Gap(design.Space1).
		Padding(design.Space1, design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if showTitle {
				design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).
					FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			} else {
				ui.Spacer(c).Grow(1)
			}
			design.Apply(ui.Text(c, messageRoleLabel(hit.Role, state)), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, formatHitTime(state.locale, hit.CreatedAt)), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary)
		})
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			buildHighlightedSnippet(c, t, query, snippet)
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

// activateMessageHit opens the conversation one search hit belongs to.
func activateMessageHit(chat contracts.ChatService, nav contracts.Navigator,
	runtime contracts.UIRuntime, hit contracts.MessageSearchHit) {
	_ = chat.SetActiveSession(hit.SessionID)
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

// buildSidebarError is the sidebar's danger row.
func buildSidebarError(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(3), th.Space(3)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).
			TextColor(t.DangerText).MaxLines(2)
	})
}

// ungroupedWorkspaceKey is the outline item of the synthetic "未分组" group.
const ungroupedWorkspaceKey = "\x00ungrouped"

// workspaceTreeKeyPrefix distinguishes a workspace group item from a session
// item in the outline (a session's item is its ID, a group's is its ID behind
// this prefix).
const workspaceTreeKeyPrefix = "workspace\x00"

// workspaceTreeKey is the outline item of one workspace group.
func workspaceTreeKey(id string) string { return workspaceTreeKeyPrefix + id }

// isWorkspaceTreeKey reports whether an outline item is a workspace group
// (including the synthetic ungrouped group).
func isWorkspaceTreeKey(key string) bool {
	return key == ungroupedWorkspaceKey || strings.HasPrefix(key, workspaceTreeKeyPrefix)
}

// workspaceIDFromTreeKey returns the workspace ID behind a group item ("" for
// the synthetic ungrouped group).
func workspaceIDFromTreeKey(key string) string {
	return strings.TrimPrefix(key, workspaceTreeKeyPrefix)
}

// buildWorkspaceSectionHeader is the one-line workspace heading above the
// workspace tree: the localized "Workspace" label and the add-workspace folder
// button. The view menu is gone (the product always groups by workspace, sorted
// by recent activity), and the current workspace is already carried by its
// highlighted group row below, so a second header line would only repeat it.
func buildWorkspaceSectionHeader(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, state *paneState) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, state.T("工作区", "Workspace")), t.SectionLabel()).
			NoWrap().MaxLines(1)
		ui.Spacer(c).Grow(1)
		addLabel := state.T("添加工作区", "Add workspace")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:     "sidebar.workspace.add",
			Label:   addLabel,
			Tooltip: state.T("打开文件夹并添加为工作区", "Open a folder and add it as a workspace"),
			Icon:    icons.Plus(),
			Size:    design.IconSizeS,
			// One click, one native folder chooser: the backend derives the
			// workspace name from the chosen folder, so there is no dialog.
			OnClick: func() { addWorkspace(state, workspace, runtime) },
		})
	})
	// The status/error rows live below the header (two badges in the 280px
	// header would squeeze the add action to zero width).
	if state.addWorkspaceStatus != "" {
		ui.Row(c).FillWidth().Children(func() {
			design.Apply(ui.Badge(c, state.addWorkspaceStatus), t.Badge()).TextColor(t.SuccessText)
		})
	}
	if state.addWorkspaceErr != "" {
		buildSidebarError(c, t, state.addWorkspaceErr)
	}
	if state.workspaceDeleteErr != "" {
		buildSidebarError(c, t, state.workspaceDeleteErr)
	}
	if state.workspaceNewChatErr != "" {
		buildSidebarError(c, t, state.workspaceNewChatErr)
	}
}

// buildWorkspaceTree renders the workspace → session tree with mygo's native
// ui.Outline: the workspace groups are the tree's roots, their sessions the
// children, and the outline owns the disclosure arrows, the indentation, the
// keyboard (Up/Down move, Right/Left open and close) and the tree semantics
// assistive technology reads. OutlineState.Open replaces the hand-kept
// collapse map, so a fold survives frames and follows the row's item.
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
	query string,
	suppressQueryEmpty bool,
) {
	tree := prepareWorkspaceTree(state, workspace, workspaces, allSessions, matched, query)
	// The tree area owns its empty states: with a workspace backend the tree is
	// always the section body, so "nothing yet" is said here instead of hiding
	// the section (which would hide the add and new-chat actions with it).
	if len(tree.roots) == 0 {
		// Under message hits the session section stays quiet: the quiet "标题
		// 无匹配" line after the message results says it once (R11).
		if suppressQueryEmpty {
			return
		}
		if query != "" {
			design.Apply(ui.Text(c, state.T("没有匹配的会话", "No matching sessions")),
				t.TextMuted(design.TypeXXS)).
				Padding(design.Space1, design.Space2, design.Space0, design.Space2)
			return
		}
		ui.Column(c).FillWidth().Gap(design.Space1).
			Padding(design.Space2, design.Space2, design.Space0, design.Space2).Children(func() {
			design.Apply(ui.Text(c, state.T("还没有工作区", "No workspaces yet")),
				t.TextMuted(design.TypeXXS))
			design.Apply(ui.Text(c, state.T("点击 + 添加工作区", "Click + to add a workspace")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		})
		return
	}
	// Roots open by default: seed each root the first time it shows, so a
	// group the user folded stays folded and a new workspace still opens.
	present := make(map[string]bool, len(tree.roots))
	for _, root := range tree.roots {
		present[root] = true
		if state.treeKnown[root] {
			continue
		}
		state.treeKnown[root] = true
		state.tree.Open.Add(root)
	}
	for root := range state.treeKnown {
		if present[root] {
			continue
		}
		delete(state.treeKnown, root)
		state.tree.Open.Remove(root)
	}

	active := chat.ActiveSession()
	rows := workspaceTreeRows(tree, &state.tree.Open)
	rowOf := make(map[string]int, len(rows))
	for index, item := range rows {
		rowOf[item] = index
	}
	activeRow := -1
	if index, ok := rowOf[active]; ok {
		activeRow = index
	}
	if state.treeActive != active {
		state.treeActive = active
		state.treeRow = activeRow
	}
	if state.treeRow >= len(rows) {
		state.treeRow = -1
	}

	// The outline keys its rows by item (workspace item or session ID). Rows
	// are named by the text inside them (mygo's list semantics): a group row by
	// its ButtonBase label, a session row by its title, so the row's accessible
	// name and the pointer click target are the visual text.
	state.tree.List.Label = nil
	state.tree.List.Selected = &state.treeRow
	var outline ui.Element
	// The native chosen-row fill is painted from the theme's accent: build the
	// tree with a local theme whose accent is the quiet selection tint, so the
	// chosen row is the persistent navigation selection (BgSelected), not a
	// saturated accent block (V1). The list keeps its native keyboard, focus
	// and accessibility selection.
	ui.Themed(c, sessionSelectionTheme(t)).Background(ui.Transparent).FillWidth().Children(func() {
		outline = ui.Outline(c, &state.tree, tree.roots, func(item string) []string {
			return tree.children[item]
		}, func(item string) {
			if isWorkspaceTreeKey(item) {
				buildWorkspaceGroupRow(c, t, chat, nav, workspace, runtime, state, tree, item)
				return
			}
			session := tree.sessions[item]
			buildSessionRow(c, t, chat, nav, runtime, state, session,
				session.ID == active, rowOf[item] == state.treeRow, 28)
		})
	})
	if outline.Submitted() && state.treeRow >= 0 && state.treeRow < state.tree.Rows() {
		if item := state.tree.Item(state.treeRow); !isWorkspaceTreeKey(item) {
			openSession(chat, nav, runtime, state, item)
		}
	}
	// While the tree has no focus, keep the choice on the current session: a
	// keyboard cursor moved away from it is not a selection of another
	// conversation, and the returned focus starts at the current one.
	if !state.tree.List.FocusWithin(c) {
		state.treeRow = activeRow
	}
}

// workspaceTreeGroups is the prepared workspace tree: the outline's item graph
// (root groups → their session items), the labels and counts shown on the group
// rows, and the session read model by ID, so the row builders stay pure lookups.
type workspaceTreeGroups struct {
	roots     []string
	children  map[string][]string
	labels    map[string]string
	counts    map[string]int
	workspace map[string]contracts.Workspace
	sessions  map[string]contracts.ChatSessionInfo
	current   string
}

// prepareWorkspaceTree folds the sessions into their workspace groups. Sessions
// with an empty or unknown WorkspaceID go to the trailing "未分组" group; a
// non-empty search query keeps only the groups that still have matching
// sessions, and the order of the matched sessions inside a group is kept.
//
// The groups themselves are ordered by recent activity, newest first: a group
// takes the UpdatedAt of its newest visible session, and a workspace with no
// visible sessions falls back to its CreatedAt, so a freshly added workspace
// shows at the top. The synthetic "未分组" group always stays last.
func prepareWorkspaceTree(state *paneState, workspace contracts.WorkspaceService,
	workspaces []contracts.Workspace, allSessions, matched []contracts.ChatSessionInfo,
	query string) *workspaceTreeGroups {
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
		group := groupOf(info)
		byGroup[group] = append(byGroup[group], info)
	}
	tree := &workspaceTreeGroups{
		children:  make(map[string][]string, len(workspaces)+1),
		labels:    make(map[string]string, len(workspaces)+1),
		counts:    make(map[string]int, len(workspaces)+1),
		workspace: make(map[string]contracts.Workspace, len(workspaces)),
		sessions:  make(map[string]contracts.ChatSessionInfo, len(matched)),
	}
	if current, ok, err := workspace.Current(context.Background()); err == nil && ok {
		tree.current = current.ID
	}
	addGroup := func(key string, ws *contracts.Workspace, name string, count int, sessions []contracts.ChatSessionInfo) {
		tree.roots = append(tree.roots, key)
		tree.labels[key] = name
		tree.counts[key] = count
		if ws != nil {
			tree.workspace[key] = *ws
		}
		for _, info := range sessions {
			tree.children[key] = append(tree.children[key], info.ID)
			tree.sessions[info.ID] = info
		}
		// A group without sessions still shows its disclosure arrow: an empty,
		// non-nil children slice marks a branch (nil marks a leaf session).
		if tree.children[key] == nil {
			tree.children[key] = []string{}
		}
	}
	for _, ordered := range orderWorkspacesByActivity(workspaces, allSessions) {
		ws := ordered
		sessions := byGroup[ws.ID]
		if query != "" && len(sessions) == 0 {
			continue
		}
		addGroup(workspaceTreeKey(ws.ID), &ws,
			firstNonEmpty(ws.Name, ws.Root, state.T("未命名工作区", "Untitled workspace")),
			totals[ws.ID], sessions)
	}
	if sessions := byGroup[""]; len(sessions) > 0 || (query == "" && totals[""] > 0) {
		addGroup(ungroupedWorkspaceKey, nil, state.T("未分组", "Ungrouped"), totals[""], sessions)
	}
	return tree
}

// orderWorkspacesByActivity returns a copy of the workspaces ordered for the
// tree: each group's sort key is the UpdatedAt of its newest visible session,
// or the workspace's CreatedAt when it has none, newest first.
func orderWorkspacesByActivity(workspaces []contracts.Workspace,
	sessions []contracts.ChatSessionInfo) []contracts.Workspace {
	ordered := append([]contracts.Workspace(nil), workspaces...)
	if len(ordered) < 2 {
		return ordered
	}
	known := make(map[string]bool, len(ordered))
	for _, ws := range ordered {
		known[ws.ID] = true
	}
	newest := make(map[string]time.Time, len(ordered))
	for _, info := range sessions {
		id := strings.TrimSpace(info.WorkspaceID)
		if !known[id] || info.Archived {
			continue
		}
		if info.UpdatedAt.After(newest[id]) {
			newest[id] = info.UpdatedAt
		}
	}
	activity := func(ws contracts.Workspace) time.Time {
		if at := newest[ws.ID]; !at.IsZero() {
			return at
		}
		return ws.CreatedAt
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return activity(ordered[i]).After(activity(ordered[j]))
	})
	return ordered
}

// workspaceTreeRows flattens the tree as the outline shows it: every root, and
// below each open group its sessions. The outline builds the same rows; this
// copy is only used to place and read the choice before it is built.
func workspaceTreeRows(tree *workspaceTreeGroups, open *ui.Selection[string]) []string {
	rows := make([]string, 0, len(tree.roots)+len(tree.sessions))
	var walk func(items []string)
	walk = func(items []string) {
		for _, item := range items {
			rows = append(rows, item)
			if kids := tree.children[item]; kids != nil && open.Has(item) {
				walk(kids)
			}
		}
	}
	walk(tree.roots)
	return rows
}

// buildWorkspaceGroupRow renders one workspace group as the content of an
// outline row: the folder glyph, the name and the session count the old
// hand-drawn group row showed, plus the row's ⋯ menu and its per-workspace
// new-chat action. A click on the row folds or unfolds the group, as it did
// before the migration; the outline's own arrow does the same and Left/Right
// do it from the keyboard. The name's tooltip is the workspace root, so two
// folders with the same base name stay apart.
func buildWorkspaceGroupRow(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, state *paneState, tree *workspaceTreeGroups, key string) {
	name := tree.labels[key]
	count := tree.counts[key]
	current := key != ungroupedWorkspaceKey && workspaceIDFromTreeKey(key) == tree.current
	c.Key("workspace/" + key)
	row := ui.ButtonBase(c).FillWidth().Height(32).PaddingX(design.Space2).
		Radius(design.RadiusSM).Label(name)
	// The row action buttons stay out of the way until the row is hovered,
	// focused or the current workspace, as the session rows' action does.
	revealed := current || row.Hovered() || row.FocusWithin()
	if ws, ok := tree.workspace[key]; ok {
		if root := strings.TrimSpace(ws.Root); root != "" {
			row = row.Tooltip(root)
		}
	}
	row.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Folder()
			iconColor := t.LabelSecondary
			if current {
				icon = icons.FolderOpen()
				iconColor = t.Accent
			}
			ui.Icon(c, icon).Size(design.IconSizeS, design.IconSizeS).TextColor(iconColor)
			textColor := t.LabelSecondary
			if current {
				textColor = t.LabelPrimary
			}
			// MinWidth(0) lets the name shrink below its longest unbroken word,
			// so a long single-token workspace name cannot push the row's
			// count/actions (⋯/⊕) outside the 280-DIP sidebar.
			design.Apply(ui.Text(c, name), t.Text(design.TypeXS)).
				FontWeight(500).TextColor(textColor).Grow(1).MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Badge(c, strconv.Itoa(count)), t.Badge())
			if workspace != nil && key != ungroupedWorkspaceKey {
				buildWorkspaceRowActions(c, t, workspace, runtime, state, tree, key, name, revealed)
				buildWorkspaceNewChatAction(c, t, workspace, chat, nav, runtime, state, key, name, revealed)
			}
		})
	})
	row.OnClick(func() {
		if state.tree.Open.Has(key) {
			state.tree.Open.Remove(key)
		} else {
			state.tree.Open.Add(key)
		}
		// Move the tree's choice to the row that was clicked, so Right/Left and
		// Up/Down continue from it.
		state.treeRow = outlineItemRow(&state.tree, key)
		runtime.Invalidate()
	})
	if workspace != nil && key != ungroupedWorkspaceKey {
		row.ContextMenu(workspaceGroupMenu(state, runtime, workspace, tree, key, name))
	}
}

// buildWorkspaceNewChatAction renders the workspace row's right-side new-chat
// button (the user's main shortcut): it opens the workspace, starts an empty
// session in it and goes to the session screen. The click is the inner
// button's own, so it does not fold the group row; a success unfolds the group
// (the fresh row must be visible) and a failure lands in the inline error row.
func buildWorkspaceNewChatAction(c *ui.Context, t *design.Tokens,
	workspace contracts.WorkspaceService, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState,
	key, name string, revealed bool) {
	label := fmt.Sprintf(state.T("在“%s”中新建会话", "New chat in “%s”"), name)
	activate := func() {
		state.workspaceNewChatErr = ""
		if _, err := workspace.Open(context.Background(), workspaceIDFromTreeKey(key)); err != nil {
			state.workspaceNewChatErr = state.T("打开工作区失败：", "Could not open the workspace: ") + err.Error()
			runtime.Invalidate()
			return
		}
		if _, err := chat.NewSession(""); err != nil {
			state.workspaceNewChatErr = state.T("新建会话失败：", "Could not start a new chat: ") + err.Error()
			runtime.Invalidate()
			return
		}
		// The session belongs to the now-current workspace: unfold its group so
		// the new row is visible, then go to the session screen on the next frame
		// (the host reconciles its native Sidebar selection after this build).
		state.tree.Open.Add(key)
		runtime.Post(func() { _ = nav.Push("session") })
		runtime.Invalidate()
	}
	plus := uikit.IconButton(c, uikit.IconButtonOptions{
		Key:     "sidebar.workspace.new-chat",
		Label:   label,
		Tooltip: label,
		Icon:    icons.Plus(),
		Size:    design.IconSizeS,
		OnClick: activate,
	})
	if !revealed {
		plus = plus.Opacity(0).IgnoreInput(true)
	}
}

// workspaceGroupMenu is the one menu the workspace row's ⋯ button and its
// right-click share, as mygo recommends for a row action and its context menu.
func workspaceGroupMenu(state *paneState, runtime contracts.UIRuntime,
	workspace contracts.WorkspaceService, tree *workspaceTreeGroups, key, name string) func(m *ui.Menu) {
	ws := tree.workspace[key]
	return func(m *ui.Menu) {
		if m.Item(state.T("打开工作区", "Open workspace")).ID("open").Chosen() {
			if _, err := workspace.Open(context.Background(), ws.ID); err != nil {
				state.workspaceDeleteErr = state.T("打开工作区失败：", "Could not open the workspace: ") + err.Error()
			} else {
				state.workspaceDeleteErr = ""
			}
			runtime.Invalidate()
		}
		m.Separator()
		remove := m.Item(state.T("删除工作区", "Delete workspace")).ID("delete").Danger()
		if root := strings.TrimSpace(ws.Root); root != "" {
			remove.Subtitle(root)
		}
		if remove.Chosen() {
			state.workspaceDeleteOpen = true
			state.workspaceDeleteID = ws.ID
			state.workspaceDeleteName = name
			state.workspaceDeleteErr = ""
			runtime.Invalidate()
		}
	}
}

// buildWorkspaceRowActions renders the row-end ⋯ menu button of a workspace
// row, opened with the menu lined up to the button's end (the menu MyGo draws
// in the window), so it never overhangs the sidebar.
func buildWorkspaceRowActions(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, state *paneState, tree *workspaceTreeGroups, key, name string,
	revealed bool) {
	menuLabel := state.T("工作区操作：", "Workspace actions: ") + name
	more := ui.ButtonBase(c).Size(20, 20).Center().Radius(design.RadiusSM).
		Label(menuLabel).Tooltip(menuLabel)
	if more.Hovered() {
		more = more.Background(t.BgHover)
	}
	if !revealed {
		more = more.Opacity(0).IgnoreInput(true)
	}
	more.Children(func() {
		ui.Icon(c, icons.More()).Size(14, 14).TextColor(t.LabelTertiary)
	})
	more.PopoverMenuButton(workspaceGroupMenu(state, runtime, workspace, tree, key, name),
		ui.PopoverMenuButtonOptions{At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd})
}

// outlineItemRow returns the row an outline item shows in the last built pass,
// or -1 when it does not show.
func outlineItemRow(state *ui.OutlineState[string], item string) int {
	for row := state.Rows() - 1; row >= 0; row-- {
		if state.Item(row) == item {
			return row
		}
	}
	return -1
}

// buildFlatSessionList renders the flat session list with mygo's native List:
// the rows are keyed by session ID, named by their title (the list's search
// ahead and assistive technology read it), and the choice follows the active
// session. Enter and a double click open the chosen session (the List's
// Submitted); a single pointer click opens through the row content's observer.
func buildFlatSessionList(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState,
	sessions []contracts.ChatSessionInfo) {
	active := chat.ActiveSession()
	activeRow := -1
	for index := range sessions {
		if sessions[index].ID == active {
			activeRow = index
			break
		}
	}
	// Mirror the current session into the native choice (Selected) whenever it
	// changes outside the list; a keyboard cursor moved within the list is not
	// overwritten.
	if state.sessionActive != active {
		state.sessionActive = active
		state.sessionRow = activeRow
	}
	if state.sessionRow >= len(sessions) {
		state.sessionRow = -1
	}
	state.sessionList.Key = func(row int) any { return sessions[row].ID }
	state.sessionList.Label = func(row int) string { return state.sessionTitle(sessions[row]) }
	state.sessionList.Selected = &state.sessionRow
	var list ui.Element
	// Same local theme as the workspace tree: the native chosen-row fill is the
	// quiet selection tint, not the accent block (V1).
	ui.Themed(c, sessionSelectionTheme(t)).Background(ui.Transparent).FillWidth().Children(func() {
		list = ui.List(c, &state.sessionList, len(sessions)).Rows(func(row ui.ListRow) {
			buildSessionRow(row.Context, t, chat, nav, runtime, state, sessions[row.Index],
				sessions[row.Index].ID == active, row.Index == state.sessionRow, 36)
		})
	})
	if list.Submitted() && state.sessionRow >= 0 && state.sessionRow < len(sessions) {
		openSession(chat, nav, runtime, state, sessions[state.sessionRow].ID)
	}
	// Without the list's focus, show the current session as the choice again:
	// a cursor left elsewhere is not a conversation switch.
	if !state.sessionList.FocusWithin(c) {
		state.sessionRow = activeRow
	}
}

// sessionSelectionTheme is the local theme the session list/tree builds with:
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

// ScreenNavSpec is the single navigation mapping for a registered screen id:
// its glyph and its localized labels. The expanded sidebar rows and the host's
// collapsed rail must both read it (the host rail draws the same destinations),
// so a new screen adds its entry here once and the rail cannot drift onto a
// different glyph or an untranslated label.
func ScreenNavSpec(id string) (icon *ui.SVG, zh, en string) {
	switch id {
	case "home":
		return icons.Home(), "首页", "Home"
	case "session":
		return icons.Message(), "对话", "Chat"
	case "automation":
		return icons.Clock(), "自动化", "Automation"
	case "plugins":
		return icons.Plugin(), "插件", "Plugins"
	case "settings":
		return icons.Sliders(), "设置", "Settings"
	default:
		return icons.Data(), "", ""
	}
}

// ScreenNavIcon is ScreenNavSpec's glyph, for the sidebar rows.
func ScreenNavIcon(id string) *ui.SVG {
	icon, _, _ := ScreenNavSpec(id)
	return icon
}

// ScreenNavLabel is ScreenNavSpec's localized label; an unregistered id falls
// back to the id itself.
func ScreenNavLabel(locale contracts.LocaleService, id string) string {
	_, zh, en := ScreenNavSpec(id)
	if zh == "" && en == "" {
		return id
	}
	return uikit.T(locale, zh, en)
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
	nav contracts.Navigator, runtime contracts.UIRuntime, state *paneState,
	session contracts.ChatSessionInfo, active, chosen bool, height float32) {
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
			if newID, err := forker.ForkSession(session.ID); err == nil && strings.TrimSpace(newID) != "" {
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
				if err := pinner.SetSessionPinned(session.ID, !session.Pinned); err == nil {
					runtime.Invalidate()
				}
			}
		}
		if archiver != nil && !session.Archived {
			// Archived sessions never render here; the settings surface's
			// archive page is where they can be restored. Archiving reports
			// success through a toast (with Undo when the host supports it).
			if m.Item(state.T("归档会话", "Archive session")).Chosen() {
				if err := archiver.SetSessionArchived(session.ID, true); err == nil {
					raiseSessionArchivedToast(state, chat, runtime, session.ID, label)
					runtime.Invalidate()
				}
			}
		}
		// Deleting a conversation is destructive: the menu item carries the
		// danger color and the row dialog still confirms it.
		if m.Item(state.T("删除", "Delete")).Danger().Chosen() {
			state.deleteOpen = true
			state.deleteTarget = session.ID
			state.deleteTitle = state.sessionTitle(session)
		}
		if !active {
			m.Separator()
			if m.Item(state.T("设为当前", "Set as current")).Chosen() {
				openSession(chat, nav, runtime, state, session.ID)
			}
		}
	}

	c.Key("session/" + session.ID)
	content := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1)
	// The menu control is built always (so assistive technology describes it)
	// but stays invisible and out of the pointer's way until the row is
	// hovered, focused or chosen, as mygo's IgnoreInput pattern for a row
	// action asks.
	revealed := active || chosen || content.Hovered() || content.FocusWithin()
	content.Children(func() {
		// The persistent selection's accent stripe, full row height. The slot is
		// built on every row (transparent when resting) so the chosen row's
		// title does not shift when the selection moves.
		bar := ui.Box(c).Width(selectedBarWidth).Height(height).Shrink(0)
		if chosen {
			bar = bar.Background(t.Accent)
		}
		// The title area is the click target that opens the session. The List
		// row also selects on a click; observing the pointer here gives the
		// single click the source-list behavior (open the conversation), while
		// Enter and a double click stay the native List's Submitted.
		title := ui.Row(c).FillWidth().Height(height).AlignItems(ui.Center).
			Gap(design.Space2).
			// The stripe and the row gap are paid for here, so the title's text
			// keeps the same inset as a resting row's.
			PaddingStart(design.Space3 - selectedBarWidth - design.Space1).
			PaddingEnd(design.Space3).Radius(design.RadiusSM)
		title.ObserveInput(func(ev ui.InputEvent) {
			if ev.Kind != ui.InputPointerUp || ev.Button != 0 {
				return
			}
			openSession(chat, nav, runtime, state, session.ID)
		})
		title.Children(func() {
			// Every conversation keeps the message glyph, also the current
			// one: NewChat belongs to the new-session action only (the same
			// glyph for two meanings was one of the fix-round findings). The
			// current conversation is carried by the accent icon, its bold
			// title and the chosen row's tint + left stripe.
			icon := icons.Message()
			iconColor := t.LabelSecondary
			if chosen || active {
				iconColor = t.Accent
			}
			ui.Icon(c, icon).Size(design.IconSizeS, design.IconSizeS).TextColor(iconColor)
			titleText := design.Apply(ui.Text(c, label), t.Text(design.TypeS)).
				TextColor(t.LabelPrimary).Grow(1).MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
			if active || chosen {
				titleText = titleText.FontWeight(500)
			}
			// The chosen row is the quiet selection tint, not the accent fill,
			// so its badges keep the resting badge look (SH-03's on-accent fill
			// was for the old full-blue row).
			badge := func(label string) {
				design.Apply(ui.Badge(c, label), t.Badge())
			}
			if session.Pinned {
				badge(state.T("置顶", "Pinned"))
			}
			// The active session is already carried by the accent icon and the
			// selected row; a 当前 badge was redundant and stole width from the
			// title.
			if session.Running {
				badge(state.T("运行中", "Running"))
			}
		})
		menuLabel := state.T("会话操作：", "Session actions: ") + label
		c.Key("session/" + session.ID + "/menu")
		more := ui.ButtonBase(c).Size(24, 24).Center().Radius(design.RadiusSM).
			Label(menuLabel).Tooltip(menuLabel)
		moreColor := t.LabelSecondary
		if more.Hovered() {
			more = more.Background(t.BgHover)
		}
		if !revealed {
			more = more.Opacity(0).IgnoreInput(true)
		}
		more.Children(func() {
			ui.Icon(c, icons.More()).Size(design.IconSizeS, design.IconSizeS).TextColor(moreColor)
		})
		// The menu opens below the button, lined up with its end, so it never
		// overhangs the sidebar's content.
		more.PopoverMenuButton(menu, ui.PopoverMenuButtonOptions{
			At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd,
		})
	})
	content.ContextMenu(menu)
}

// openSession activates a session, makes its workspace current and opens the
// session screen on the next frame. The screen switch is queued because this
// frame's host-side native Sidebar reconciliation would otherwise restore the
// previously selected destination; an already active session is not set again.
func openSession(chat contracts.ChatService, nav contracts.Navigator,
	runtime contracts.UIRuntime, state *paneState, sessionID string) {
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
	_, _ = workspace.Open(context.Background(), target)
}

// buildFooter is the docked sidebar footer from the old shell: the settings
// entry plus the archived-sessions view option, both as navigation rows (36
// DIP, icon and label, the whole row clickable) so the footer reads as part of
// the sidebar instead of two small outlined buttons. It also owns the
// window-level delete confirmation; session rename/delete live on the row's
// native popover menu.
func buildFooter(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime,
	state *paneState, label string) {
	if state.deleteOpen {
		deleteTitle := firstNonEmpty(state.deleteTitle, state.T("未命名会话", "Untitled session"))
		target := state.deleteTarget
		messageTemplate := state.T("“%s”及其全部消息将被删除，此操作无法撤销。",
			"“%s” and all its messages will be deleted. This cannot be undone.")
		message := fmt.Sprintf(messageTemplate, deleteTitle)
		buildDangerConfirm(c, t, &state.deleteOpen,
			state.T("删除会话", "Delete session"), message,
			state.T("删除", "Delete"), state.T("取消", "Cancel"),
			func() {
				if err := chat.DeleteSession(target); err == nil {
					runtime.Invalidate()
				}
			},
			func() {
				state.deleteTarget = ""
				state.deleteTitle = ""
				runtime.Invalidate()
			})
		return
	}

	ui.Column(c).FillWidth().Gap(design.Space1).
		Padding(design.Space2, design.Space3, design.Space2, design.Space3).
		Children(func() {
			c.Key("sidebar/footer/settings")
			settingsRow := ui.ButtonBase(c).FillWidth().Height(36).PaddingX(design.Space3).
				Radius(design.RadiusSM).Label(label)
			settingsRow = rowTint(t, settingsRow, false)
			settingsRow.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.Sliders()).Size(16, 16).TextColor(t.LabelSecondary)
					design.Apply(ui.Text(c, label), t.Text(design.TypeS)).TextColor(t.LabelSecondary)
				})
			})
			if settingsRow.Clicked() {
				nav.ToggleSettings()
				runtime.Invalidate()
			}
		})
}

// buildRenameDialog renders the rename Modal on the window-level overlay slot.
// It is only visible while the footer's rename action asked for it; Escape or a
// click outside closes it through the native Modal's open pointer. It follows
// the product dialog convention (uikit.DialogFooter, the input focused as the
// dialog opens, Enter saving): the same rules every other dialog in the product
// has.
func buildRenameDialog(c *ui.Context, t *design.Tokens, renamer contracts.SessionRenamer,
	runtime contracts.UIRuntime, state *paneState) {
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

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
