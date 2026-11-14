package settings

// Memory page: browse, search and manage every workspace's memory entries the
// agent's memory tools read/write (contracts.KeyMemory, optional). The
// management surface reads the optional ListAll half (the model-facing List is
// workspace-scoped); a quarantined memory file is reported through the optional
// MemoryRecoveryReporter. The page resolves the store per frame, so a late
// provided backend appears without a restart.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// MemoryPageID is the memory-management page identity.
	MemoryPageID = "memory"
	// MemoryPageSection places it with the other advanced pages.
	MemoryPageSection = "高级"
	// MemoryPageOrder places the page after data & privacy.
	MemoryPageOrder = 60
)

// memoryPageState is the page's UI-local state (UI thread only).
type memoryPageState struct {
	query         string
	draft         string
	notice        string
	noticeErr     bool
	confirmClear  bool
	confirmDelete string // memory ID waiting for the confirmation dialog
	deleteOpen    bool   // the per-entry confirmation's open flag (persistent for ConfirmDanger)

	// The 全部记忆 collection is the official List: keyed by memory ID, named by
	// the row text and single-row selectable for the keyboard; the edit/delete
	// buttons stay the row actions.
	list ui.ListState
	sel  int

	// Cached read model: the store is read only when the query changes or a
	// mutation drops the cache (add/edit/delete/tidy/clear/extract). Building
	// the page must not hit SQLite every frame (mygo-maintenance: keep slow I/O
	// off the UI thread).
	memories       []contracts.Memory
	memoriesQuery  string
	memoriesLoaded bool
	memoriesErr    error

	// Per-entry editing (optional: only with a contracts.MemoryUpdater store).
	// The modal keeps the entry's ID and scope so saving is an update in place.
	editing   bool
	editID    string
	editText  string
	editScope string
	editErr   string

	// Consolidation policy row (optional: only with a MemoryConsolidator).
	policyLoaded bool
	maxEntries   string
	maxAgeDays   string
	// Numeric mirrors for the official ui.NumberInput policy fields (0 means
	// unlimited, which is exactly what the stored "0" means).
	maxEntriesNum float64
	maxAgeDaysNum float64
	policyWarn    string
	policyNotice  string
	policyErr     bool

	// Model extraction (opt-in): one manual extraction at a time.
	extractRunning bool
}

// invalidateMemories drops the cached read model, so the next frame reads the
// store again after a mutation.
func (st *memoryPageState) invalidateMemories() {
	st.memoriesLoaded = false
	st.memoriesQuery = ""
	st.memoriesErr = nil
}

// memoryStatsLabel renders the entry count.
func memoryStatsLabel(locale contracts.LocaleService, count int) string {
	if localized(locale, "zh", "en") == "en" {
		if count == 1 {
			return "1 memory"
		}
		return fmt.Sprintf("%d memories", count)
	}
	return fmt.Sprintf("%d 条记忆", count)
}

// memoryScopeLabel localizes one stored memory scope.
func memoryScopeLabel(locale contracts.LocaleService, scope string) string {
	trimmed := strings.TrimSpace(scope)
	switch {
	case strings.EqualFold(trimmed, contracts.MemoryScopeGlobal):
		return localized(locale, "全局", "Global")
	case strings.EqualFold(trimmed, contracts.MemoryScopeWorkspace):
		return localized(locale, "工作区（旧数据）", "Workspace (legacy)")
	case strings.HasPrefix(strings.ToLower(trimmed), contracts.MemoryScopeWorkspacePrefix):
		return localized(locale, "工作区", "Workspace")
	default:
		return trimmed
	}
}

// memoryTimestamp renders one memory's creation time in the interface language
// (empty when unset); uikit.FormatDateTime keeps it consistent with the other
// product timestamps and makes the appearance page's date-format promise true.
func memoryTimestamp(locale contracts.LocaleService, created time.Time) string {
	return uikit.FormatDateTime(locale, created.Local())
}

// buildMemoryPage renders the memory management page. Entries are rows with a
// hairline divider (not cards inside a card), every destructive action asks for
// confirmation with the native alert dialog, and the section controls say
// whether they apply at once.
func buildMemoryPage(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	st *memoryPageState, settingsStore contracts.SettingsService, locale contracts.LocaleService) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "记忆", "Memory"), "")
		store, hasStore := kctx.Get(contracts.KeyMemory)
		if !hasStore || store == nil {
			uikit.EmptyState(c, localized(locale, "未启用记忆后端", "Memory backend not enabled"))
			return
		}
		// FR-06: a quarantined memory file must be visible here, not only in the
		// log: the user needs to know their previous entries were backed up.
		if reporter, ok := store.(contracts.MemoryRecoveryReporter); ok {
			if notice := strings.TrimSpace(reporter.RecoveryNotice()); notice != "" {
				uikit.ErrorLine(c, localized(locale, "记忆文件已损坏，已隔离备份并暂时从空记忆继续：",
					"The memory file was corrupt: it was quarantined and the app continues with empty memory: ")+notice)
			}
		}
		// Editing needs the optional update half. Backends that only implement
		// the rig store keep their row actions unchanged (delete still works);
		// the edit button is hidden rather than rendered disabled, because a
		// permanently disabled control gives no reason and no path forward.
		updater, _ := store.(contracts.MemoryUpdater)

		query := strings.TrimSpace(st.query)
		if !st.memoriesLoaded || st.memoriesQuery != query {
			st.memoriesLoaded = true
			st.memoriesQuery = query
			// FR-25: the settings page is the management surface, so it reads
			// every workspace's entries (the model and the session tab keep the
			// workspace-filtered view). Search filters the same full set so a
			// workspace entry never disappears from the management page.
			if allLister, ok := store.(contracts.MemoryAllLister); ok {
				all, err := allLister.ListAll(context.Background())
				st.memories, st.memoriesErr = filterMemoryQuery(all, query), err
			} else if query != "" {
				st.memories, st.memoriesErr = store.Search(context.Background(), query)
			} else {
				st.memories, st.memoriesErr = store.List(context.Background())
			}
		}
		memories, loadErr := st.memories, st.memoriesErr

		uikit.Section(c, localized(locale, "全部记忆", "All memories"), func(*ui.Context) {
			ui.SearchField(c, &st.query).
				Label(localized(locale, "搜索记忆", "Search memory")).
				Placeholder(localized(locale, "搜索记忆…", "Search memory…")).FillWidth()
			// The management row stays available even when the initial read
			// failed, so tidy/clear can retry the store.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if loadErr == nil {
					design.Apply(ui.Text(c, memoryStatsLabel(locale, len(memories))),
						t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
				} else {
					ui.Spacer(c).Grow(1)
				}
				// "整理记忆" was not understandable (B17): the action removes
				// duplicate entries and keeps the earliest one; the tooltip says
				// exactly that and that other memories are untouched.
				if design.Apply(ui.Button(c, localized(locale, "去重并合并", "Merge duplicates")),
					t.SecondaryButton()).Tooltip(localized(locale,
					"删除重复的记忆，保留最早的一条；不重复的记忆不受影响",
					"Remove duplicate memories, keeping the earliest; memories without duplicates are untouched")).Clicked() {
					tidyMemoriesPage(store, st, locale)
					st.invalidateMemories()
					c.Invalidate()
				}
				if design.Apply(ui.Button(c, localized(locale, "全部清空", "Clear all")),
					t.DangerButton()).Tooltip(localized(locale,
					"删除全部记忆", "Delete every memory")).Clicked() {
					st.confirmClear = true
					c.Invalidate()
				}
			})
			if loadErr != nil {
				uikit.ErrorLine(c, localized(locale, "读取记忆失败：", "Failed to read memory: ")+loadErr.Error())
				return
			}

			switch {
			case len(memories) == 0 && query != "":
				uikit.EmptyState(c, localized(locale, "没有匹配的记忆", "No matching memories"))
			case len(memories) == 0:
				uikit.EmptyState(c, localized(locale, "还没有记忆", "No memories yet"))
			default:
				// The memories are a collection: the official List keys the rows
				// by memory ID (so the choice follows the entry when the search
				// filters the list), names them for assistive technology and
				// owns the keyboard selection; the inline edit/delete buttons
				// keep their click semantics.
				bindSettingsList(&st.list, &st.sel, len(memories), func(index int) any {
					if index < 0 || index >= len(memories) {
						return nil
					}
					return memories[index].ID
				}, func(index int) string {
					if index < 0 || index >= len(memories) {
						return ""
					}
					return memoryRowLabel(locale, memories[index])
				})
				ui.List(c, &st.list, len(memories)).
					Label(localized(locale, "记忆列表", "Memories")).
					Gap(design.Space1).
					Rows(func(row ui.ListRow) {
						buildMemoryPageEntry(row.Context, t, store, updater, st,
							memories[row.Index], locale, row.Selected())
					})
			}
		})

		// Adding a memory stays next to the list it extends, before the policy
		// and extraction sections (H19): the old order buried the add form
		// below two long policy blocks.
		uikit.Section(c, localized(locale, "添加记忆", "Add memory"), func(*ui.Context) {
			draft := strings.TrimSpace(st.draft)
			// One add path for the button and for Enter in the field, so the two
			// cannot drift (mygo text-input.md: Submitted is Enter in a text
			// input; a desktop form's single-line field submits on Enter).
			add := func() {
				content := strings.TrimSpace(st.draft)
				if content == "" {
					return
				}
				// The empty scope lets the durable store apply its configured
				// default (Config.Scope, FR-25) instead of hardcoding global.
				_, err := store.Add(context.Background(), contracts.Memory{Text: content})
				if err != nil {
					st.notice = localized(locale, "添加记忆失败：", "Failed to add memory: ") + err.Error()
					st.noticeErr = true
				} else {
					st.draft = ""
					st.notice = localized(locale, "已添加", "Added")
					st.noticeErr = false
					st.invalidateMemories()
				}
				c.Invalidate()
			}
			ui.Field(c, localized(locale, "记忆内容", "Memory text"), func() {
				input := ui.TextInput(c, &st.draft).
					Label(inputLabel(locale, localized(locale, "记忆内容", "Memory text"))).
					Placeholder(localized(locale, "记忆内容", "Memory text")).FillWidth()
				if input.Submitted() {
					add()
				}
			})
			if st.notice != "" && st.noticeErr {
				uikit.ErrorLine(c, st.notice)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{{
				Label: localized(locale, "添加", "Add"), Primary: true,
				Disabled: draft == "",
				OnClick:  add,
			}})
			if st.notice != "" && !st.noticeErr {
				statusBadgesRow(c, t, st.notice, false)
			}
		})

		buildMemoryConsolidationPanel(c, t, store, settingsStore, st, locale)

		buildMemoryExtractionPanel(c, t, kctx, settingsStore, st, locale)

		// Per-entry edit dialog: only a store with the optional update half
		// gets 编辑 buttons, so this opens only from one of those rows.
		if updater != nil && st.editing {
			buildMemoryEditDialog(c, t, updater, st, locale)
		}

		// Clear-all confirmation: the shared danger dialog with the safe outcome
		// as its default (Enter never wipes the store).
		if st.confirmClear {
			uikit.ConfirmDanger(c, &st.confirmClear, uikit.ConfirmDangerOptions{
				Key:   "memory.clear-all",
				Title: localized(locale, "清空全部记忆", "Clear all memories"),
				Message: localized(locale, "确认删除全部记忆？此操作无法撤销。",
					"Delete all memories? This cannot be undone."),
				Confirm: localized(locale, "清空", "Clear"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					clearMemoriesPage(store, st, locale)
					st.invalidateMemories()
					c.Invalidate()
				},
			})
		}

		// One-entry deletion: the same shared danger dialog.
		if st.confirmDelete != "" {
			target := st.confirmDelete
			uikit.ConfirmDanger(c, &st.deleteOpen, uikit.ConfirmDangerOptions{
				Key:   "memory.delete",
				Title: localized(locale, "删除记忆", "Delete memory"),
				Message: localized(locale, "确认删除这条记忆？此操作无法撤销。",
					"Delete this memory? This cannot be undone."),
				Confirm: localized(locale, "确认删除", "Confirm delete"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					if store.Remove(context.Background(), target) {
						st.notice = ""
						st.noticeErr = false
					} else {
						st.notice = localized(locale, "删除记忆失败：", "Failed to delete memory: ") + target
						st.noticeErr = true
					}
					st.confirmDelete = ""
					st.invalidateMemories()
					c.Invalidate()
				},
			})
			if !st.deleteOpen {
				st.confirmDelete = ""
			}
		}
	})
}

// parseMemoryPolicyInt parses one policy input: empty means 0 (unlimited),
// negative or non-numeric input is invalid and normalized to 0.
func parseMemoryPolicyInt(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, true
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// buildMemoryConsolidationPanel renders the deterministic consolidation policy
// row (caps/retention). It is hidden when the store does not implement
// contracts.MemoryConsolidator; tidy/clear stay available either way.
func buildMemoryConsolidationPanel(c *ui.Context, t *design.Tokens, store contracts.MemoryStore,
	settingsStore contracts.SettingsService, st *memoryPageState, locale contracts.LocaleService) {
	consolidator, ok := store.(contracts.MemoryConsolidator)
	if !ok || consolidator == nil {
		return
	}
	if !st.policyLoaded {
		st.policyLoaded = true
		if settingsStore != nil {
			st.maxEntries = strings.TrimSpace(settingsStore.Get(contracts.SettingMemoryMaxEntries, ""))
			st.maxAgeDays = strings.TrimSpace(settingsStore.Get(contracts.SettingMemoryMaxAgeDays, ""))
			if value, ok := parseMemoryPolicyInt(st.maxEntries); ok {
				st.maxEntriesNum = float64(value)
			}
			if value, ok := parseMemoryPolicyInt(st.maxAgeDays); ok {
				st.maxAgeDaysNum = float64(value)
			}
		}
	}
	uikit.Section(c, localized(locale, "整理策略", "Consolidation policy"), func(*ui.Context) {
		design.Apply(ui.Text(c, localized(locale,
			"0 或留空表示不限制；保存后立即整理。",
			"0 or empty means unlimited; consolidation runs on save.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
			ui.Column(c).FillWidth().Grow(1).Children(func() {
				ui.Field(c, localized(locale, "最大条目数", "Max entries"), func() {
					// The official number input gives the field stepper buttons,
					// arrow keys, range clamping and immediate application
					// (mygo number-input.md); 0 still means unlimited.
					entries := ui.NumberInput(c.Key("memory.maxEntries"), &st.maxEntriesNum,
						0, 100000, 10).
						Label(inputLabel(locale, localized(locale, "最大条目数", "Max entries")))
					if entries.Changed() {
						st.maxEntries = strconv.Itoa(int(st.maxEntriesNum))
					}
				}).Description(localized(locale, "超过后删除最旧的记忆；0 表示不限。",
					"Entries beyond the cap are the oldest ones; 0 means unlimited."))
			})
			ui.Column(c).FillWidth().Grow(1).Children(func() {
				ui.Field(c, localized(locale, "保留天数", "Max age (days)"), func() {
					days := ui.NumberInput(c.Key("memory.maxAgeDays"), &st.maxAgeDaysNum,
						0, 3650, 1).
						Label(inputLabel(locale, localized(locale, "保留天数", "Max age (days)")))
					if days.Changed() {
						st.maxAgeDays = strconv.Itoa(int(st.maxAgeDaysNum))
					}
				}).Description(localized(locale, "早于该天数的记忆会被删除；0 表示不限。",
					"Memories older than this many days are dropped; 0 means unlimited."))
			})
		})
		if st.policyWarn != "" {
			uikit.ErrorLine(c, st.policyWarn)
		}
		if st.policyNotice != "" && st.policyErr {
			uikit.ErrorLine(c, st.policyNotice)
		}
		statusBadgesRow(c, t, st.policyNotice, st.policyErr)
		uikit.DialogFooter(c, []uikit.DialogAction{{
			Label: localized(locale, "按策略整理", "Consolidate"), Primary: true,
			OnClick: func() {
				applyMemoryPolicy(c, consolidator, settingsStore, st, locale)
			},
		}})
	})
}

// applyMemoryPolicy persists the normalized policy and runs one consolidation
// call, surfacing the report or failure in the panel.
func applyMemoryPolicy(c *ui.Context, consolidator contracts.MemoryConsolidator,
	settingsStore contracts.SettingsService, st *memoryPageState, locale contracts.LocaleService) {
	maxEntries, entriesOK := parseMemoryPolicyInt(st.maxEntries)
	maxAgeDays, ageOK := parseMemoryPolicyInt(st.maxAgeDays)
	st.policyWarn = ""
	if !entriesOK || !ageOK {
		st.policyWarn = localized(locale,
			"无效数字，已按 0（不限）处理",
			"Invalid number; using 0 (unlimited)")
	}
	st.maxEntries = strconv.Itoa(maxEntries)
	st.maxAgeDays = strconv.Itoa(maxAgeDays)
	var saveErr error
	if settingsStore != nil {
		if err := settingsStore.Set(contracts.SettingMemoryMaxEntries, st.maxEntries); err != nil {
			saveErr = err
		}
		if err := settingsStore.Set(contracts.SettingMemoryMaxAgeDays, st.maxAgeDays); err != nil && saveErr == nil {
			saveErr = err
		}
	}
	report, err := consolidator.Consolidate(context.Background(), contracts.MemoryPolicy{
		MaxEntries: maxEntries,
		MaxAge:     time.Duration(maxAgeDays) * 24 * time.Hour,
	})
	switch {
	case err != nil:
		st.policyNotice = localized(locale, "整理失败：", "Consolidation failed: ") + err.Error()
		st.policyErr = true
	case saveErr != nil:
		st.policyNotice = localized(locale, "保存策略失败：", "Failed to save policy: ") + saveErr.Error()
		st.policyErr = true
	default:
		st.policyNotice = fmt.Sprintf(localized(locale,
			"已整理：过期 %d / 超限 %d / 剩余 %d",
			"Consolidated: expired %d / over cap %d / remaining %d"),
			report.RemovedExpired, report.RemovedOverCap, report.Remaining)
		st.policyErr = false
	}
	// A consolidation may have removed entries: the cached list must re-read.
	st.invalidateMemories()
	c.Invalidate()
}

// normalizeMemoryKey is the duplicate key of the tidy action: trimmed, inner
// whitespace collapsed and case-insensitive.
func normalizeMemoryKey(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

// memoryNotice renders one success/error result in the add panel's row.
func setMemoryNotice(st *memoryPageState, text string, failed bool) {
	st.notice = text
	st.noticeErr = failed
}

// tidyMemoriesPage removes duplicate memories, keeping the earliest of each
// normalized text; failures land in the page's danger line.
func tidyMemoriesPage(store contracts.MemoryStore, st *memoryPageState, locale contracts.LocaleService) {
	ctx := context.Background()
	memories, err := managementMemories(store, ctx)
	if err != nil {
		setMemoryNotice(st, localized(locale, "读取记忆失败：", "Failed to read memory: ")+err.Error(), true)
		return
	}
	sort.SliceStable(memories, func(i, j int) bool {
		return memories[i].Created.Before(memories[j].Created)
	})
	seen := make(map[string]bool, len(memories))
	removed, failed := 0, 0
	for _, memory := range memories {
		key := normalizeMemoryKey(memory.Text)
		if seen[key] {
			if store.Remove(ctx, memory.ID) {
				removed++
			} else {
				failed++
			}
			continue
		}
		seen[key] = true
	}
	switch {
	case failed > 0:
		setMemoryNotice(st, localized(locale, "去重并合并失败：", "Merge duplicates failed: ")+fmt.Sprintf("%d", failed), true)
	case removed > 0:
		setMemoryNotice(st, memoryTidiedLabel(locale, removed), false)
	default:
		setMemoryNotice(st, localized(locale, "没有可整理的重复记忆", "No duplicate memories to tidy"), false)
	}
}

// memoryTidiedLabel renders the tidy success count.
func memoryTidiedLabel(locale contracts.LocaleService, count int) string {
	if localized(locale, "zh", "en") == "en" {
		if count == 1 {
			return "Tidied 1 memory"
		}
		return fmt.Sprintf("Tidied %d memories", count)
	}
	return fmt.Sprintf("已整理 %d 条", count)
}

// clearMemoriesPage removes every memory; failures land in the danger line.
func clearMemoriesPage(store contracts.MemoryStore, st *memoryPageState, locale contracts.LocaleService) {
	ctx := context.Background()
	memories, err := managementMemories(store, ctx)
	if err != nil {
		setMemoryNotice(st, localized(locale, "清空失败：", "Clear failed: ")+err.Error(), true)
		return
	}
	failed := 0
	for _, memory := range memories {
		if !store.Remove(ctx, memory.ID) {
			failed++
		}
	}
	if failed > 0 {
		setMemoryNotice(st, localized(locale, "清空失败：", "Clear failed: ")+fmt.Sprintf("%d", failed), true)
		return
	}
	setMemoryNotice(st, localized(locale, "已清空", "Cleared"), false)
}

// managementMemories reads every workspace's entries for the management
// actions (tidy/clear), falling back to the workspace view for stores without
// the optional ListAll half.
func managementMemories(store contracts.MemoryStore, ctx context.Context) ([]contracts.Memory, error) {
	if allLister, ok := store.(contracts.MemoryAllLister); ok {
		return allLister.ListAll(ctx)
	}
	return store.List(ctx)
}

// filterMemoryQuery applies the page's search box to an entry set.
func filterMemoryQuery(memories []contracts.Memory, query string) []contracts.Memory {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return memories
	}
	filtered := make([]contracts.Memory, 0, len(memories))
	for _, memory := range memories {
		if strings.Contains(strings.ToLower(memory.Text), query) {
			filtered = append(filtered, memory)
		}
	}
	return filtered
}

// memoryExtractEnabled reports whether model extraction is enabled: only the
// exact "true" value turns it on (default and malformed values are off).
func memoryExtractEnabled(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
}

// memoryPartialExtractedLabel renders the "N written before the failure" notice.
func memoryPartialExtractedLabel(locale contracts.LocaleService, count int) string {
	if localized(locale, "zh", "en") == "en" {
		return fmt.Sprintf("%d memor(ies) were written before the failure: ", count)
	}
	return fmt.Sprintf("已写入 %d 条记忆后失败：", count)
}

// memoryExtractedLabel renders the extraction success count.
func memoryExtractedLabel(locale contracts.LocaleService, count int) string {
	if localized(locale, "zh", "en") == "en" {
		if count == 1 {
			return "Added 1 memory"
		}
		return fmt.Sprintf("Added %d memories", count)
	}
	return fmt.Sprintf("已添加 %d 条记忆", count)
}

// startMemoryExtraction runs one manual extraction off the UI thread and posts
// its result back. The caller owns the running flag, so repeated clicks are
// ignored while a run is in flight.
func startMemoryExtraction(runtime contracts.UIRuntime,
	extractor contracts.MemoryExtractionService, st *memoryPageState,
	sessionID string, locale contracts.LocaleService) {
	st.extractRunning = true
	finish := func(memories []contracts.Memory, err error) {
		st.extractRunning = false
		if err != nil {
			// M-06: entries written before the failure are durable; say how
			// many survived and refresh so they are visible instead of being
			// silently hidden behind "extraction failed".
			if len(memories) > 0 {
				st.notice = memoryPartialExtractedLabel(locale, len(memories)) + err.Error()
			} else {
				st.notice = localized(locale, "提取失败：", "Extraction failed: ") + err.Error()
			}
			st.noticeErr = true
			st.invalidateMemories()
			return
		}
		st.notice = memoryExtractedLabel(locale, len(memories))
		st.noticeErr = false
		st.invalidateMemories()
	}
	go func() {
		memories, err := extractor.ExtractFromSession(context.Background(), sessionID, 5)
		if runtime == nil {
			finish(memories, err)
			return
		}
		runtime.Post(func() {
			finish(memories, err)
			runtime.Invalidate()
		})
	}()
}

// buildMemoryExtractionPanel renders the opt-in model-extraction block: the
// enable switch, its privacy/cost note and the manual "extract from current
// session" action (shown only when a backend is provided).
func buildMemoryExtractionPanel(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	settingsStore contracts.SettingsService, st *memoryPageState, locale contracts.LocaleService) {
	uikit.Section(c, localized(locale, "模型提取", "Model extraction"), func(*ui.Context) {
		enabled := memoryExtractEnabled(settingsStore.Get(contracts.SettingMemoryExtractEnabled, ""))
		toggleLabel := localized(locale,
			"启用模型提取（从最近对话提取记忆，会调用模型）",
			"Enable model extraction (extract memories from recent chats; calls the model)")
		ui.Form(c, func() {
			if switchField(c, toggleLabel,
				"",
				&enabled) {
				value := ""
				if enabled {
					value = "true"
				}
				if err := settingsStore.Set(contracts.SettingMemoryExtractEnabled, value); err != nil {
					setMemoryNotice(st, localized(locale, "保存失败：", "Save failed: ")+err.Error(), true)
				} else {
					setMemoryNotice(st, localized(locale, "已保存", "Saved"), false)
				}
				c.Invalidate()
			}
		})
		uikit.ErrorLine(c, localized(locale,
			"会消耗模型额度并向服务商发送对话内容。",
			"Uses your model quota and sends conversation content to the provider."))

		extractor, hasExtractor := kctx.Get(contracts.KeyMemoryExtractor)
		if !hasExtractor || extractor == nil {
			uikit.EmptyState(c, localized(locale,
				"未启用记忆提取后端", "Memory extraction backend not enabled"))
			return
		}
		activeID := ""
		if chat, ok := kctx.Get(contracts.KeyChat); ok && chat != nil {
			activeID = strings.TrimSpace(chat.ActiveSession())
		}
		label := localized(locale, "从当前会话提取", "Extract from current session")
		busyLabel := localized(locale, "提取中…", "Extracting…")
		if st.extractRunning {
			label = busyLabel
		}
		disabled := !enabled || activeID == "" || st.extractRunning
		// The Add form owns the page's one primary; extraction is a secondary
		// action (one primary per page, DESIGN.md §5.2).
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			buildBusySpinner(c, st.extractRunning, busyLabel)
			extract := design.Apply(ui.Button(c, label), t.SecondaryButton()).Disabled(disabled)
			if extract.Clicked() && !disabled && !st.extractRunning {
				runtime, _ := kctx.Get(contracts.KeyUIRuntime)
				startMemoryExtraction(runtime, extractor, st, activeID, locale)
				c.Invalidate()
			}
		})
		switch {
		case !enabled:
			design.Apply(ui.Text(c, localized(locale, "先启用模型提取后再提取", "Enable model extraction first")),
				t.TextMuted(design.TypeXXS))
		case activeID == "":
			design.Apply(ui.Text(c, localized(locale, "当前没有活动会话", "No active session")),
				t.TextMuted(design.TypeXXS))
		}
	})
}

// memoryDraftDirty reports whether the 添加记忆 input holds an unsaved draft.
func memoryDraftDirty(st *memoryPageState) bool { return strings.TrimSpace(st.draft) != "" }

// discardMemoryDraft clears the add-memory draft and the transient notices, so
// the host's Discard action reopens a clean memory page.
func discardMemoryDraft(st *memoryPageState) {
	st.draft = ""
	st.notice = ""
	st.noticeErr = false
	st.policyNotice = ""
	st.policyErr = false
	st.policyWarn = ""
}

// memoryRowLabel is the accessible text of one memory row: its full body, with
// a placeholder for an empty entry so the List never gets a nameless row.
func memoryRowLabel(locale contracts.LocaleService, memory contracts.Memory) string {
	if text := strings.TrimSpace(memory.Text); text != "" {
		return text
	}
	return localized(locale, "(空记忆)", "(Empty memory)")
}

// buildMemoryPageEntry renders one memory row: the body, scope/time metadata and
// the native actions — 编辑 (only when the store implements
// contracts.MemoryUpdater) and the confirmed danger delete. The official List
// owns the row paint; selected is its choice.
func buildMemoryPageEntry(c *ui.Context, t *design.Tokens, store contracts.MemoryStore,
	updater contracts.MemoryUpdater, st *memoryPageState, memory contracts.Memory,
	locale contracts.LocaleService, selected bool) {
	textColor, metaColor, _ := listRowTextColors(t, selected)
	ui.Column(c).FillWidth().Padding(design.Space1, design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, memory.Text), t.Font(design.TypeXS)).
				TextColor(textColor).Grow(1).MaxLines(3)
			// One trailing ⋯ menu owns both row actions; the destructive delete is
			// its danger item, so a page of memories never paints a red button per
			// row (DESIGN.md §5.2 danger budget).
			label := localized(locale, "记忆操作", "Memory actions")
			menu := uikit.IconButton(c, uikit.IconButtonOptions{
				Key:     "memory-actions/" + memory.ID,
				Label:   label,
				Tooltip: label,
				Icon:    icons.More(),
				Size:    design.IconSizeS,
			})
			menu.PopoverMenuButton(func(m *ui.Menu) {
				if updater != nil {
					if m.Item(localized(locale, "编辑记忆", "Edit memory")).Chosen() {
						startMemoryEdit(c, st, memory)
					}
				}
				remove := m.Item(localized(locale, "删除记忆", "Delete memory"))
				remove.Danger()
				if remove.Chosen() {
					st.confirmDelete = memory.ID
					st.deleteOpen = true
					c.Invalidate()
				}
			})
		})
		meta := make([]string, 0, 2)
		if scope := memoryScopeLabel(locale, memory.Scope); scope != "" {
			meta = append(meta, scope)
		}
		if created := memoryTimestamp(locale, memory.Created); created != "" {
			meta = append(meta, created)
		}
		if len(meta) > 0 {
			design.Apply(ui.Text(c, strings.Join(meta, " · ")), t.Font(design.TypeXXS)).
				TextColor(metaColor)
		}
	})
}

// startMemoryEdit opens the edit modal prefilled with one entry's text. The
// entry's scope rides along in the draft so saving preserves it (the dialog
// edits the text only; editing scope is not a product feature).
func startMemoryEdit(c *ui.Context, st *memoryPageState, memory contracts.Memory) {
	st.editing = true
	st.editID = memory.ID
	st.editText = memory.Text
	st.editScope = memory.Scope
	st.editErr = ""
	c.Invalidate()
}

// buildMemoryEditDialog renders the native edit modal: the current text in a
// text area, a cancel action that closes without saving, and a primary save
// that calls contracts.MemoryUpdater.Update with the entry's ID, the new text
// and its existing scope. Failures keep the modal open with an inline error.
func buildMemoryEditDialog(c *ui.Context, t *design.Tokens, updater contracts.MemoryUpdater,
	st *memoryPageState, locale contracts.LocaleService) {
	ui.Modal(c, &st.editing, func() {
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "编辑记忆", "Edit memory")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
			ui.Field(c, localized(locale, "编辑内容", "Edit text"), func() {
				ui.TextArea(c, &st.editText).
					Label(inputLabel(locale, localized(locale, "编辑内容", "Edit text"))).
					FillWidth().MinHeight(design.Space6 * 4).AutoFocus()
			})
			if st.editErr != "" {
				uikit.ErrorLine(c, st.editErr)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
					st.editing = false
					st.editErr = ""
					c.Invalidate()
				}},
				{Label: localized(locale, "保存", "Save"), Primary: true, OnClick: func() {
					saveMemoryEdit(c, updater, st, locale)
				}},
			})
		})
	})
}

// saveMemoryEdit validates the draft and saves it in place: success closes the
// modal and shows the page's success badge, a failure keeps it open with the
// danger line so the user can fix the entry.
func saveMemoryEdit(c *ui.Context, updater contracts.MemoryUpdater,
	st *memoryPageState, locale contracts.LocaleService) {
	text := strings.TrimSpace(st.editText)
	if text == "" {
		st.editErr = localized(locale, "内容不能为空", "Text is required")
		c.Invalidate()
		return
	}
	if _, err := updater.Update(context.Background(), st.editID, text, st.editScope); err != nil {
		st.editErr = localized(locale, "更新记忆失败：", "Failed to update memory: ") + err.Error()
		c.Invalidate()
		return
	}
	st.editing = false
	st.editErr = ""
	st.editID = ""
	st.editText = ""
	st.editScope = ""
	setMemoryNotice(st, localized(locale, "已更新", "Updated"), false)
	st.invalidateMemories()
	c.Invalidate()
}
