package settings

// Memory page: browse, search and manage the global memory store the agent's
// memory tools read/write (contracts.KeyMemory, optional). The page resolves
// the store per frame, so a late-provided backend appears without a restart.

import (
	"context"
	"fmt"
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
	query     string
	draft     string
	notice    string
	noticeErr bool
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
	st *memoryPageState, locale contracts.LocaleService) {
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
			if loadErr != nil {
				buildMemoryPageError(c, t, localized(locale, "读取记忆失败：", "Failed to read memory: ")+loadErr.Error())
				return
			}
			design.Apply(ui.Text(c, memoryStatsLabel(locale, len(memories))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)

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
