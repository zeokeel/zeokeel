# Changelog

All notable changes to ZeoKeel are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- **Settings consolidation**: the separate 模型目录 (Model catalog) page was
  merged into the 模型 (Model) page — one place now owns the provider
  connection, the model list, thinking-family/mode editing, custom modes and
  per-model generation parameters. The page title stays 模型; the standalone
  catalog page and its navigation entry are gone. The merged page keeps the
  connection form's footer Save and immediate model-list edits, with the
  editor's generation/custom-mode groups behind one 生成与自定义模式 disclosure.
- The provider quick-preset "自定义" chip was renamed to 手动配置 / Manual setup
  to distinguish it from the thinking-family "自定义" (custom) option.
- **Transcript order**: assistant text and tool cards now interleave in event
  order (`ToolCall.TextOffset`), so a reply that called tools keeps its
  pre-tool text above the calls and its final text below them instead of
  drawing the whole reply above the tool cards.
- **Quieter tool calls**: transcript tool cards are now compact one-line rows
  (muted icon/name, subtle status, one-line result preview and a small 详情
  disclosure) instead of full-width panels; decision tool cards (plan review,
  exit plan, ask user) stay prominent and the trajectory pane keeps its
  detailed cards.
- **Default permission is now workspace-level**: an unset `agent.permission`
  resolves to workspace everywhere (desktop assembly, approvals gate, session
  menu, behavior page). Workspace mode auto-allows the workspace file tools
  (read/write/edit/str_replace/list/glob/grep) without asking; shell, network,
  MCP and unknown tools still ask. The composer label stays quiet
  (工作区权限/Workspace) while full/never and non-default chat modes keep the
  warning label.
- **Unlimited iterations by default**: the agent assembly no longer sets
  `maxIterations: 20`; 0 means no practical limit (the run passes an effectively
  unreachable cap to Rig, which otherwise defaults to 20). The model page's
  runtime summary shows 不限制/unlimited when unset; a numeric value still caps
  the run.
- **mygo v0.8.3**: upgraded from v0.7.4 and adapted to the stricter Tester
  hit-testing, the richer menu APIs (model menu now uses `Menu.MaxWidth` +
  `MenuItem.Subtitle`/`Trailing` for model notes and context windows), the
  official `NewTesterWith`/`Tester.AccessByRole` test APIs, and GitHub
  alerts/mentions/references in Markdown. Hand-written import/skill input
  accessibility labels now follow the shared `inputLabel()` pattern.
- **Domestic Go module mirror**: the Makefile, `scripts/verify.sh` and CI
  workflows default to `GOPROXY=https://goproxy.cn,direct` with
  `GOSUMDB=sum.golang.org`; offline builds can override both to `off`.

### Fixed (UI conformance, mygo v0.8.3)

A full UI/UX conformance pass against `mygo v0.8.3/docs/ui` and the
`design-guide` skill; the inventory and the rules live in
[docs/UI-CONFORMANCE.md](docs/UI-CONFORMANCE.md).

- **Shell**: the sidebar and the inspector are native `ui.Split` panes with
  persisted widths and `SplitReset`; the inspector draws as an accessible
  modal drawer (`RoleDialog`, Escape, focus trap, scrim token); the window menu
  presentation is unified with `Context.SetMenuPresentation(ui.PopoverMenus)`;
  the top band is a `ui.Toolbar` with visible command-palette and inspector
  entries; the palette list has a name and `ActiveDescendant`; screen changes
  and the inspector announce to assistive technology; chrome toggles ignore
  key auto-repeat; the rail is a keyboard `FocusGroup`; a back affordance and
  Alt+Left follow the navigation stack.
- **Brand theme**: `Theme.InactiveAccent` keeps the brand accent in inactive
  windows (matching the hand-drawn accent surfaces, locked by a test);
  `design.Of` wires the desktop text-size preference into `Tokens.TextScale`
  and the root theme scales its `FontSize` by the same factor, so native
  controls and brand-drawn text grow together (both covered by tests).
- **Shared kit**: `uikit.DialogFooter` supports a default (focused, Enter)
  action; `PageHeader`/`Section` titles are headings; `ErrorLine` is an alert
  live region with a real icon and selectable text; `KeyValueRows` values are
  selectable; one shared `uikit.EnterTo` and one shared `uikit.ToolIcon` mapping
  replace the per-module copies.
- **Right inspector**: the Changes pane renders the official
  `editor.DiffView` (word-level inline changes, line numbers, hunks) with a
  bounded text fallback for unparsable patches; Files/Log/Tools/Tasks/Agents
  are native `ui.List`s with keyboard selection and row a11y; the terminal
  shell picker is a `ui.Select` and its output follows the end; the directory
  listing and the session log are cached and refreshed explicitly instead of
  reading disk/SQLite every frame; empty states, file/image icons and
  selectable previews follow the guide.
- **Sidebar**: the workspace tree is a native `ui.Outline` (arrow keys open and
  close), session rows are a `ui.List` with `Key`/`Label`/`Selected`; right
  click and the row's ⋯ menu share one menu built with `ContextMenu` +
  `PopoverMenuButtonOptions`; destructive items use `MenuItem.Danger()`; the
  add-workspace dialog uses the shared footer with Enter submission.
- **Session**: the goal chip uses the official `PopoverMenuButton` menu
  (Danger items, stable IDs) instead of a hand-drawn popover; permission, chat
  mode, thinking, model and preset choices are `MenuItem.Radio` (single-choice
  semantics) with model `ID`/`Subtitle`/`Trailing`; the slash/@ suggestion
  popups are named list boxes with option roles, positions and an
  `ActiveDescendant`; dialogs accept Enter, and dialog widths use the design
  tokens; the English session menu is "Session settings" so it no longer
  collides with the global Settings.
- **Settings**: the memory page caches its read model (no per-frame SQLite),
  and add/edit/tidy/clear/extract invalidate it; dialogs submit on Enter
  (model catalog, presets, memory, policy); the data & privacy page makes
  exporting the primary action and opening the data directory secondary;
  diagnostics use localized unknown values; the stale English subtitle is
  fixed; settings dialog widths use tokens.
- **Other screens**: Home recent sessions are a `ui.List` with localized
  dates; Automation summaries are localized, truncated and hide internal
  session IDs, its enable switch and filters use Switch/Radio; Plugins show a
  localized tool summary with the original text in the tooltip and condense
  MCP errors; Approval uses `uikit.DialogFooter` with Deny as the focused
  default (Enter = deny); toasts use `ShadowOverlay` and grid tokens; icons for
  image/file reads are the correct glyphs, and an icon invariant test locks the
  SVG grid/stroke rules.
- **Bounded transcript**: a conversation builds only its newest 200 messages by
  default (a "load earlier messages (N)" row reveals the hidden range in
  steps), so streaming a reply no longer re-lays out thousands of rows in a
  long session; the test covers the tail window and the load-earlier step.
- **Number inputs where the official control fits**: the memory consolidation
  policy (max entries / max retention), the model runtime limits (max
  iterations / context budget, each with a "use the module default" switch that
  clears the settings row), the model context-window fields and the model
  generation parameters (temperature, Top P, max output tokens — each with a
  "use the provider default" switch) are native `ui.NumberInput`s: stepper
  buttons, arrow keys, range clamping and immediate application. The page tests
  were rewritten to the control's range semantics with one shared
  `typeNumberInput` helper, and the old free-text "must be a number / in range"
  error paths are gone.
- **One action-placement rule for settings**: `uikit.PageHeaderAction` and
  `uikit.SectionAction` put a page's immediate-effect actions (new skill,
  refresh, check updates) on the page header row and a section's utility
  actions (restore presets/defaults, export the diagnostic report) on the
  section heading row. The explicit-save pages keep their single primary action
  in the host footer, so the old mix of bottom-of-content and right-aligned
  action rows is gone; frame tests pin the placement and the migrated pages'
  tests pass.
- **Dead injection points removed**: the contract's `SlotTopBarBrand/Center/
  Actions` and `SlotOverlaySettings/Palette` constants, the welcome module's
  top-bar marker registration and the corresponding test assertions are gone.
  The shell has no top bar (screen headers plus the content-top toolbar own the
  global actions); the architecture/injection-point docs now say so instead of
  inviting modules to register slots the host never renders.
- **Native dialogs take their title as an argument**: the package-level dialog
  title variables (workspace folder, session export markdown/JSONL, data export
  and diagnostic report) are gone; every picker/saver seam now receives the
  frame's localized title, so two windows in different languages cannot race a
  shared global. Tests capture the title from the seam argument and one
  regression probe now checks both languages instead of skipping a known defect.
- **Transcript is a native List**: the conversation transcript now uses the
  official `ui.List` (`ListState.Key` by message ID, `Label`, `FollowEnd` for
  streaming, `HoldPlace` when a fold opens, `ScrollToEnd` behind the floating
  back-to-latest action) instead of a `Scroll` plus a bounded message loop, so
  only the visible rows build and long conversations stay fast. The help
  dialog's command list and the trajectory timeline use `ui.List` as well.
- **Read-only code preview**: Files' source previews use the official
  `editor.View` in read-only mode (syntax highlighting by extension, line
  numbers, soft wrap, virtualization) instead of a plain mono text block; the
  tests read the content from the editor's accessibility text value.
- **Agent preset tools are a TokenField**: the comma-separated tools text
  input is the official `ui.TokenField` (tokens as chips, Enter/comma to add,
  Backspace to remove), and the preset still persists `[]string`.
- **More native Lists**: the Memory tab's entries and the Automation screen's
  scheduled tasks are official `ui.List`s (keyed by ID, named rows, virtualized
  scrolling) with the search/tools/add rows fixed around them, so the last
  hand-drawn scrolling collections are gone.
- **Bounded per-message UI state**: switching conversations drops the fold /
  usage / unfolded-actions / transcript-window entries of the conversation that
  no longer shows (one internal test covers the pruning), and sent attachments'
  decoded thumbnail caches are released, so a long-running app does not grow
  state for every session it ever opened.
- **Grouped subprocess fields**: the MCP add form's STDIO fields are a native
  `ui.Fieldset` with a legend, so assistive technology reads the group with
  every field inside it.

## [0.1.0] - 2026-11-08

The first public ZeoKeel release: a compile-time modular AI agent for the
desktop and the headless/ACP worlds, written in Go and drawn with native mygo
controls (no webview, no browser engine).

### Added

- **Compile-time modular architecture**: one binary, assembly products
  (`desktop` / `headless`), a kernel of contribution points, and `assembly.json`
  entries that enable or trim modules without runtime plugin shipping.
- **Desktop product**: native chat UI with streaming answers, reasoning
  preview, tool cards, session history, workspaces, right-side inspector
  (overview/tools/log/files/changes/terminal/team/tasks), command palette,
  keyboard shortcuts, tray/notification integration and global hotkey.
- **Headless + ACP**: the same binary runs `--headless` (with tools, memory,
  sessions, automations) or as an ACP stdio server for editor integration,
  including image prompt blocks.
- **Agent capabilities**: tools (shell/file/read/edit/todo), approvals and
  permission modes (ask/workspace/full/never), agent/chat modes
  (normal/plan/minimal/creator), subagents and team management, goals with
  continuation, plan review, memos/memory, skills, MCP servers, LSP tools,
  prompt/agent presets, session references and context compaction.
- **Model configuration**: providers and quick presets, per-session model
  switching, per-model thinking configuration for Qwen / DeepSeek / other
  families with custom modes, per-model generation parameters
  (temperature / top_p / max_tokens), runtime limits (max iterations, context
  budget, stream-completeness check) and a model catalog editor.
- **Settings surface**: 19 pages in five groups, with low-frequency advanced
  pages collapsed behind one group; progressive disclosure on the model,
  behavior and model-catalog pages; searchable navigation, fixed footer
  actions and unsaved-changes guards.
- **i18n**: Chinese and English UI, localized errors and date formatting.
- **Packaging**: cross-platform release builds with explicit OS + architecture
  asset names — `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
  (`.tar.gz`) and `windows/amd64`, `windows/arm64` (`.zip` with `zeokeel.exe`);
  version/commit/build-time injected at link time, `CGO_ENABLED=0` pure-Go
  builds stripped with `-s -w`, gzip/zip-compressed archives, `SHA256SUMS.txt`
  and a download table in the release notes; CI on push/PR (including a
  cross-compile check for every release target) and a tag-driven release
  workflow.

### Fixed

- ACP failures are reported as JSON-RPC errors; interrupted turns persist
  partial assistant output and can be regenerated without losing reasoning.
- Oversized tool results spill to disk for both desktop and headless sessions.
- Truncated model streams are reported instead of silently appearing complete
  (zeokeel-rig `RequireCompleteStream`, v0.13.1).
- Attachment picker runs off the UI thread; manual path fallback for
  portal-less environments; per-conversation composer drafts and attachments.
- Composer action row simplified (attach menu, model, session menu, send);
  narrow-window overflow and session-menu accessibility fixes.
- Memory update/edit, model test-connection details, approval copy, and a
  large batch of UI interaction fixes found by independent audits.

### Verification

- `./scripts/verify.sh` (2026-11-08): **10/10 PASS** — gofmt, build, vet,
  full test suite, `-race`, regression **284 PASS / 0 SKIP / 0 FAIL**, UI
  snapshot (83 steps / 81 PNGs), desktop (Xvfb) / headless / ACP smokes.
- Desktop smoke: window + tray + single instance under Xvfb, no panics.
- Model thinking/generation mapping verified by captured request bodies and
  unit tests; SDK dependency is `github.com/zeokeel/zeokeel-rig v0.13.1`.

[0.1.0]: https://github.com/zeokeel/zeokeel/releases/tag/v0.1.0
