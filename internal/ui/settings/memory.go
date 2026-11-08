package settings

// Memory page: browse, search and manage the global memory store the agent's
// memory tools read/write (contracts.KeyMemory, optional). The page resolves
// the store per frame, so a late-provided backend appears without a restart.

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
)

const (
	// MemoryPageID is the memory-management page identity.
	MemoryPageID = "memory"
	// MemoryPageSection places it with the other advanced pages.
	MemoryPageSection = "高级"
	// MemoryPageOrder places the page after data & privacy.
	MemoryPageOrder = 107
)

// memoryPageState is the page's UI-local state (UI thread only).
type memoryPageState struct {
	query        string
	draft        string
	notice       string
	noticeErr    bool
	confirmClear bool

	// Consolidation policy row (optional: only with a MemoryConsolidator).
	policyLoaded bool
	maxEntries   string
	maxAgeDays   string
	policyWarn   string
	policyNotice string
	policyErr    bool

	// Model extraction (opt-in): one manual extraction at a time.
	extractRunning bool
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
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "global":
		return localized(locale, "全局", "Global")
	case "workspace":
		return localized(locale, "工作区", "Workspace")
	default:
		return strings.TrimSpace(scope)
	}
}

// memoryTimestamp renders one memory's creation time (empty when unset).
func memoryTimestamp(created time.Time) string {
	if created.IsZero() {
		return ""
	}
	return created.Local().Format("2006-01-02 15:04")
}

// buildMemoryPage renders the memory management page.
func buildMemoryPage(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	st *memoryPageState, settingsStore contracts.SettingsService, locale contracts.LocaleService) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, localized(locale, "记忆", "Memory"), localized(locale,
			"查看、搜索与管理全局记忆。",
			"Browse, search and manage global memories."))
		store, hasStore := kctx.Get(contracts.KeyMemory)
		if !hasStore || store == nil {
			design.Apply(ui.Text(c, localized(locale, "未启用记忆后端", "Memory backend not enabled")),
				t.TextMuted(design.TypeXS))
			return
		}

		query := strings.TrimSpace(st.query)
		var (
			memories []contracts.Memory
			loadErr  error
		)
		if query != "" {
			memories, loadErr = store.Search(context.Background(), query)
		} else {
			memories, loadErr = store.List(context.Background())
		}

		panel(c, t, func() {
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
				if ui.Button(c, localized(locale, "整理记忆", "Tidy")).
					Tooltip(localized(locale, "删除重复的记忆，保留最早的一条", "Remove duplicate memories, keeping the earliest")).Clicked() {
					tidyMemoriesPage(store, st, locale)
					c.Invalidate()
				}
				clear := ui.Button(c, localized(locale, "全部清空", "Clear all")).
					Tooltip(localized(locale, "删除全部记忆", "Delete every memory")).TextColor(t.DangerText)
				if clear.Clicked() {
					st.confirmClear = true
					c.Invalidate()
				}
			})
			if loadErr != nil {
				buildMemoryPageError(c, t, localized(locale, "读取记忆失败：", "Failed to read memory: ")+loadErr.Error())
				return
			}

			switch {
			case len(memories) == 0 && query != "":
				design.Apply(ui.Text(c, localized(locale, "没有匹配的记忆", "No matching memories")),
					t.TextMuted(design.TypeXS))
			case len(memories) == 0:
				design.Apply(ui.Text(c, localized(locale, "还没有记忆", "No memories yet")),
					t.TextMuted(design.TypeXS))
			default:
				for _, memory := range memories {
					memory := memory
					buildMemoryPageEntry(c, t, store, st, memory, locale)
				}
			}
		})

		buildMemoryConsolidationPanel(c, t, store, settingsStore, st, locale)

		buildMemoryExtractionPanel(c, t, kctx, settingsStore, st, locale)

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "添加记忆", "Add memory"))
			ui.TextInput(c, &st.draft).
				Label(inputLabel(locale, localized(locale, "记忆内容", "Memory text"))).
				Placeholder(localized(locale, "记忆内容", "Memory text")).FillWidth()
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if st.notice != "" {
					if st.noticeErr {
						buildMemoryPageError(c, t, st.notice)
					} else {
						statusBadge(c, t, st.notice, false)
					}
				}
				ui.Spacer(c).Grow(1)
				draft := strings.TrimSpace(st.draft)
				add := design.Apply(ui.PrimaryButton(c, localized(locale, "添加", "Add")), t.PrimaryButton()).
					Disabled(draft == "")
				if add.Clicked() && draft != "" {
					_, err := store.Add(context.Background(), contracts.Memory{Text: draft, Scope: "global"})
					if err != nil {
						st.notice = localized(locale, "添加记忆失败：", "Failed to add memory: ") + err.Error()
						st.noticeErr = true
					} else {
						st.draft = ""
						st.notice = localized(locale, "已添加", "Added")
						st.noticeErr = false
					}
					c.Invalidate()
				}
			})
		})

		// Clear-all confirmation: a native AlertDialog above the page content.
		if st.confirmClear {
			switch ui.AlertDialog(c, &st.confirmClear,
				localized(locale, "清空全部记忆", "Clear all memories"),
				localized(locale, "确认删除全部记忆？此操作无法撤销。",
					"Delete all memories? This cannot be undone."),
				localized(locale, "取消", "Cancel"),
				localized(locale, "清空", "Clear")) {
			case 1:
				clearMemoriesPage(store, st, locale)
				c.Invalidate()
			default:
				c.Invalidate()
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
		}
	}
	panel(c, t, func() {
		sectionTitle(c, t, localized(locale, "整理策略", "Consolidation policy"))
		design.Apply(ui.Text(c, localized(locale,
			"按策略确定性整理（不调用模型）：过期删除 + 超限保留最新；0 或留空表示不限制。",
			"Deterministic consolidation (no model call): drop expired entries, then keep the newest up to the cap; 0 or empty means unlimited.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		ui.TextInput(c, &st.maxEntries).
			Label(inputLabel(locale, localized(locale, "最大条目数", "Max entries"))).
			Placeholder(localized(locale, "0（不限）", "0 (unlimited)")).FillWidth()
		ui.TextInput(c, &st.maxAgeDays).
			Label(inputLabel(locale, localized(locale, "保留天数", "Max age (days)"))).
			Placeholder(localized(locale, "0（不限）", "0 (unlimited)")).FillWidth()
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if ui.Button(c, localized(locale, "按策略整理", "Consolidate")).Clicked() {
				applyMemoryPolicy(c, consolidator, settingsStore, st, locale)
			}
			ui.Spacer(c).Grow(1)
			if st.policyNotice != "" && !st.policyErr {
				statusBadge(c, t, st.policyNotice, false)
			}
		})
		if st.policyWarn != "" {
			buildMemoryPageError(c, t, st.policyWarn)
		}
		if st.policyNotice != "" && st.policyErr {
			buildMemoryPageError(c, t, st.policyNotice)
		}
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
	memories, err := store.List(ctx)
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
		setMemoryNotice(st, localized(locale, "整理记忆失败：", "Tidy failed: ")+fmt.Sprintf("%d", failed), true)
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
	memories, err := store.List(ctx)
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

// memoryExtractEnabled reports whether model extraction is enabled: only the
// exact "true" value turns it on (default and malformed values are off).
func memoryExtractEnabled(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
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
			st.notice = localized(locale, "提取失败：", "Extraction failed: ") + err.Error()
			st.noticeErr = true
			return
		}
		st.notice = memoryExtractedLabel(locale, len(memories))
		st.noticeErr = false
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
// enable toggle, its privacy/cost note and the manual "extract from current
// session" action (shown only when a backend is provided).
func buildMemoryExtractionPanel(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	settingsStore contracts.SettingsService, st *memoryPageState, locale contracts.LocaleService) {
	panel(c, t, func() {
		sectionTitle(c, t, localized(locale, "模型提取", "Model extraction"))
		enabled := memoryExtractEnabled(settingsStore.Get(contracts.SettingMemoryExtractEnabled, ""))
		toggleLabel := localized(locale,
			"启用模型提取（从最近对话提取记忆，会调用模型）",
			"Enable model extraction (extract memories from recent chats; calls the model)")
		toggle := ui.Toggle(c, &enabled, toggleLabel)
		if toggle.Changed() {
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
		design.Apply(ui.Text(c, localized(locale,
			"调用当前服务商，读取最近 20 条消息，最多写入 5 条记忆；仅手动触发，不做后台定时提取。",
			"Calls the current provider, reads the recent 20 messages and stores at most 5 memories; manual trigger only, no background extraction.")),
			t.TextMuted(design.TypeXXS)).MaxLines(3)
		design.Apply(ui.Text(c, localized(locale,
			"会消耗模型额度并向服务商发送对话内容。",
			"Uses your model quota and sends conversation content to the provider.")),
			t.Font(design.TypeXXS)).TextColor(t.DangerText).MaxLines(2)

		extractor, hasExtractor := kctx.Get(contracts.KeyMemoryExtractor)
		if !hasExtractor || extractor == nil {
			design.Apply(ui.Text(c, localized(locale, "未启用记忆提取后端", "Memory extraction backend not enabled")),
				t.TextMuted(design.TypeXXS))
			return
		}
		activeID := ""
		if chat, ok := kctx.Get(contracts.KeyChat); ok && chat != nil {
			activeID = strings.TrimSpace(chat.ActiveSession())
		}
		label := localized(locale, "从当前会话提取", "Extract from current session")
		if st.extractRunning {
			label = localized(locale, "提取中…", "Extracting…")
		}
		disabled := !enabled || activeID == "" || st.extractRunning
		button := design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton()).Disabled(disabled)
		if button.Clicked() && !disabled && !st.extractRunning {
			runtime, _ := kctx.Get(contracts.KeyUIRuntime)
			startMemoryExtraction(runtime, extractor, st, activeID, locale)
			c.Invalidate()
		}
		switch {
		case !enabled:
			design.Apply(ui.Text(c, localized(locale, "先启用模型提取后再提取", "Enable model extraction first")),
				t.TextMuted(design.TypeXXXS))
		case activeID == "":
			design.Apply(ui.Text(c, localized(locale, "当前没有活动会话", "No active session")),
				t.TextMuted(design.TypeXXXS))
		}
	})
}

// buildMemoryPageEntry renders one memory row: body, scope/time metadata and a
// native delete action.
func buildMemoryPageEntry(c *ui.Context, t *design.Tokens, store contracts.MemoryStore,
	st *memoryPageState, memory contracts.Memory, locale contracts.LocaleService) {
	design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, memory.Text), t.Font(design.TypeXS)).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(3)
			remove := ui.Button(c, localized(locale, "删除", "Delete")).
				TextColor(t.DangerText).Shrink(0)
			if remove.Clicked() {
				if store.Remove(context.Background(), memory.ID) {
					st.notice = ""
					st.noticeErr = false
				} else {
					st.notice = localized(locale, "删除记忆失败：", "Failed to delete memory: ") + memory.ID
					st.noticeErr = true
				}
				c.Invalidate()
			}
		})
		meta := make([]string, 0, 2)
		if scope := memoryScopeLabel(locale, memory.Scope); scope != "" {
			meta = append(meta, scope)
		}
		if created := memoryTimestamp(memory.Created); created != "" {
			meta = append(meta, created)
		}
		if len(meta) > 0 {
			design.Apply(ui.Text(c, strings.Join(meta, " · ")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
		}
	})
}

// buildMemoryPageError renders one DangerText error line.
func buildMemoryPageError(c *ui.Context, t *design.Tokens, text string) {
	ui.Row(c).FillWidth().Children(func() {
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText)
	})
}
