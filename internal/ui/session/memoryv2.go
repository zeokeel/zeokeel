package session

// Memory tab over the SDK v2 memory store: search + type filter, compact list,
// L0/L1/L2 detail, quick add and delete. Reads stay off the frame thread.

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const memorySearchDebounce = 180 * time.Millisecond

var memoryTypeOptions = []struct {
	Label string
	Value string
}{
	{"全部", ""},
	{"偏好", "preferences"},
	{"档案", "profile"},
	{"实体", "entities"},
	{"事件", "events"},
	{"案例", "cases"},
	{"轨迹", "trajectories"},
	{"经验", "experiences"},
	{"工具", "tools"},
	{"技能", "skills"},
}

func memoryTypeLabel(value string) string {
	for _, option := range memoryTypeOptions {
		if option.Value == value {
			return option.Label
		}
	}
	return value
}

func memoryTypeOptionLabels() []string {
	labels := make([]string, len(memoryTypeOptions))
	for index, option := range memoryTypeOptions {
		labels[index] = option.Label
	}
	return labels
}

func memoryTypeValue(label string) string {
	for _, option := range memoryTypeOptions {
		if option.Label == label {
			return option.Value
		}
	}
	return ""
}

// memoryWorkspaceRoot resolves the current workspace root for the service call.
func memoryWorkspaceRoot(ctx cordis.Context, chat contracts.ChatService, sessionID string) string {
	workspace, ok := ctx.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return ""
	}
	// Prefer the workspace the session is bound to; the Memory tab must not
	// show another project's entries just because it is the current one.
	if chat != nil && strings.TrimSpace(sessionID) != "" {
		workspaceID := ""
		for _, info := range chat.Sessions() {
			if info.ID == sessionID {
				workspaceID = strings.TrimSpace(info.WorkspaceID)
				break
			}
		}
		if workspaceID != "" {
			if workspaces, listErr := workspace.List(context.Background()); listErr == nil {
				for _, candidate := range workspaces {
					if candidate.ID == workspaceID {
						return strings.TrimSpace(candidate.Root)
					}
				}
			}
		}
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(current.Root)
}

func startMemoryLoad(runtime uinative.UIRuntime, st *state, service contracts.MemoryService, query, typ string, chat contracts.ChatService, sessionID string) {
	st.memoryRefresh = false
	st.memoryPendingQuery = query
	st.memoryPendingType = typ
	st.memoryReading = true
	st.memorySeq++
	sequence := st.memorySeq
	go func() {
		var items []contracts.MemoryItem
		var err error
		root := memoryWorkspaceRoot(st.ctx, chat, sessionID)
		if root == "" {
			err = errNoWorkspace
		} else {
			var types []string
			if typ != "" {
				types = []string{typ}
			}
			items, err = service.List(context.Background(), root, query, types, 200)
		}
		runtime.Post(func() {
			if sequence != st.memorySeq {
				return
			}
			st.memoryItems = items
			st.memoryErr = ""
			if err != nil {
				st.memoryErr = err.Error()
			}
			st.memoryRoot = root
			st.memoryFresh = true
			st.memoryReading = false
			st.memoryList = ui.ListState{}
			st.memorySelected = -1
			runtime.Invalidate()
		})
	}()
}

func startMemoryDetail(runtime uinative.UIRuntime, st *state, service contracts.MemoryService, uri string) {
	st.memorySeq++
	sequence := st.memorySeq
	st.memoryReading = true
	go func() {
		var detail contracts.MemoryDetail
		var err error
		if st.memoryRoot == "" {
			err = errNoWorkspace
		} else {
			detail, err = service.Get(context.Background(), st.memoryRoot, uri)
		}
		runtime.Post(func() {
			if sequence != st.memorySeq {
				return
			}
			st.memoryReading = false
			if err != nil {
				st.memoryErr = err.Error()
				runtime.Invalidate()
				return
			}
			st.memoryDetail = &detail
			st.memoryDetailID = uri
			st.memoryErr = ""
			runtime.Invalidate()
		})
	}()
}

// buildMemoryPane renders the Memory tab.
func buildMemoryPane(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state, chat contracts.ChatService, sessionID string) {
	service := st.memoryService
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		centered := func(children func(*ui.Context)) {
			ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				design.Apply(ui.Column(c), t.Gap(design.Space3)).
					Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
					children(c)
				})
			})
		}
		if service == nil {
			uikit.EmptyStateBlock(c, st.tr("记忆后端不可用", "Memory backend unavailable"),
				st.tr("当前构建没有装配 v2 记忆后端。", "This build has no v2 memory backend."), nil)
			return
		}
		if st.memoryDetail != nil {
			buildMemoryDetail(c, t, runtime, st, service)
			return
		}
		query := strings.TrimSpace(st.memoryQuery)
		if !st.memoryFresh || st.memoryRefresh {
			startMemoryLoad(runtime, st, service, query, st.memoryType, chat, sessionID)
		} else if query != st.memoryPendingQuery || st.memoryType != st.memoryPendingType {
			if st.memoryPendingQuery != query || st.memoryPendingType != st.memoryType {
				st.memoryPendingQuery = query
				st.memoryPendingType = st.memoryType
				st.memorySearchDue = c.Now().Add(memorySearchDebounce)
				c.After(memorySearchDebounce)
			}
			if !c.Now().Before(st.memorySearchDue) {
				startMemoryLoad(runtime, st, service, query, st.memoryType, chat, sessionID)
			}
		}
		centered(func(c *ui.Context) {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.SearchField(c.Key("session.memory.search"), &st.memoryQuery).
					Label(st.tr("搜索记忆", "Search memory")).
					Placeholder(st.tr("搜索记忆", "Search memory")).
					Grow(1)
			})
			choice := memoryTypeLabel(st.memoryType)
			selector := ui.Select(c.Key("session.memory.type"), &choice, memoryTypeOptionLabels()).
				Label(st.tr("记忆类型", "Memory type"))
			if selector.Changed() {
				if value := memoryTypeValue(choice); value != st.memoryType {
					st.memoryType = value
					st.memoryPendingType = value
					st.memoryRefresh = true
				}
			}
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				meta := st.tr("记忆", " memories")
				if len(st.memoryItems) > 0 {
					meta = st.tr("条记忆", " memories")
				}
				design.Apply(ui.Text(c, intText(len(st.memoryItems))+meta),
					t.TextMuted(design.TypeXXS))
				if st.memoryReading {
					ui.Spinner(c).Label(st.tr("读取中…", "Loading…")).
						Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
				}
			})
			if st.memoryErr != "" {
				uikit.ErrorLine(c, st.tr("读取记忆失败：", "Failed to read memory: ")+st.memoryErr)
			}
		})
		switch {
		case st.memoryErr != "":
		case st.memoryReading && len(st.memoryItems) == 0:
		case len(st.memoryItems) == 0 && query != "":
			uikit.EmptyStateBlock(c, st.tr("没有匹配的记忆", "No matching memories"),
				st.tr("换一个关键词或类型试试。", "Try another keyword or type."), nil)
		case len(st.memoryItems) == 0:
			uikit.EmptyStateBlock(c, st.tr("还没有记忆", "No memories yet"),
				st.tr("在下方写一条，后续会话都可以使用。", "Write one below; later conversations can use it."), nil)
		default:
			st.memoryList.Key = func(index int) any {
				if index < 0 || index >= len(st.memoryItems) {
					return nil
				}
				return st.memoryItems[index].URI
			}
			st.memoryList.Label = func(index int) string {
				if index < 0 || index >= len(st.memoryItems) {
					return ""
				}
				return st.memoryItems[index].Title
			}
			st.memoryList.Selected = &st.memorySelected
			ui.List(c, &st.memoryList, len(st.memoryItems)).
				Label(st.tr("记忆列表", "Memories")).
				Grow(1).
				Rows(func(row ui.ListRow) {
					item := st.memoryItems[row.Index]
					centered(func(c *ui.Context) {
						ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
							ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
								design.Apply(ui.Text(c, item.Title), t.Font(design.TypeXS)).
									MaxLines(1).Ellipsis("…")
								if item.Pinned {
									ui.Icon(c, icons.Sparkles()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.Accent)
								}
							})
							if item.Summary != "" {
								design.Apply(ui.Text(c, item.Summary), t.TextMuted(design.TypeXXS)).
									MaxLines(2).Ellipsis("…")
							}
							design.Apply(ui.Text(c, memoryTypeLabel(item.Type)+" · "+memoryAge(st, item.Updated)),
								t.TextMuted(design.TypeXXXS)).MaxLines(1)
						})
					})
					if row.Selected() && st.memoryDetailID != item.URI {
						st.memoryDetailID = item.URI
						st.memoryErr = ""
						startMemoryDetail(runtime, st, service, item.URI)
					}
				})
		}
		centered(func(c *ui.Context) {
			buildMemoryQuickAdd(c, t, runtime, st, service)
		})
		uikit.ConfirmDanger(c, &st.memoryConfirmOpen, uikit.ConfirmDangerOptions{
			Key:     "session.memory.delete",
			Title:   st.tr("删除这条记忆？", "Delete this memory?"),
			Message: st.tr("删除后无法恢复。", "This cannot be undone."),
			Confirm: st.tr("删除", "Delete"),
			Cancel:  st.tr("取消", "Cancel"),
			OnConfirm: func() {
				uri := st.memoryConfirmURI
				if uri == "" || st.memoryRoot == "" {
					return
				}
				go func() {
					err := service.Delete(context.Background(), st.memoryRoot, uri)
					runtime.Post(func() {
						if err != nil {
							st.memoryErr = err.Error()
							runtime.Invalidate()
							return
						}
						st.memoryDetail = nil
						st.memoryDetailID = ""
						st.memoryRefresh = true
						runtime.Invalidate()
					})
				}()
			},
		})
	})
}

func buildMemoryDetail(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state, service contracts.MemoryService) {
	centered := func(children func(*ui.Context)) {
		ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Column(c), t.Gap(design.Space3)).
				Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
				children(c)
			})
		})
	}
	detail := st.memoryDetail
	centered(func(c *ui.Context) {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			back := design.Apply(ui.Button(c, st.tr("返回", "Back")), t.SecondaryButton())
			if back.Clicked() {
				st.memoryDetail = nil
				st.memoryDetailID = ""
				st.memorySelected = -1
				st.memoryErr = ""
			}
			design.Apply(ui.Text(c, memoryTypeLabel(detail.Type)+" · "+memoryAge(st, detail.Updated)),
				t.TextMuted(design.TypeXXS))
			ui.Row(c).Grow(1)
			edit := design.Apply(ui.Button(c, st.tr("编辑", "Edit")), t.SecondaryButton())
			if edit.Clicked() {
				st.memoryEditing = true
				st.memoryEditTitle = detail.Title
				st.memoryEditSummary = detail.Summary
				st.memoryEditBody = detail.Content
				st.memoryEditErr = ""
			}
			pinLabel := st.tr("置顶", "Pin")
			if detail.Pinned {
				pinLabel = st.tr("取消置顶", "Unpin")
			}
			pin := design.Apply(ui.Button(c, pinLabel), t.SecondaryButton())
			if pin.Clicked() {
				go func() {
					_, err := service.Write(context.Background(), st.memoryRoot, contracts.MemoryDraft{
						URI: detail.URI, Type: detail.Type, Title: detail.Title, Summary: detail.Summary,
						Overview: detail.Overview, Content: detail.Content, Pinned: !detail.Pinned,
					})
					runtime.Post(func() {
						if err != nil {
							st.memoryErr = err.Error()
							runtime.Invalidate()
							return
						}
						startMemoryDetail(runtime, st, service, detail.URI)
					})
				}()
			}
			del := design.Apply(ui.Button(c, st.tr("删除", "Delete")), t.SecondaryButton())
			if del.Clicked() {
				st.memoryConfirmURI = detail.URI
				st.memoryConfirmOpen = true
			}
		})
		design.Apply(ui.Text(c, detail.Title), t.Font(design.TypeM))
		if detail.Summary != "" {
			design.Apply(ui.Text(c, detail.Summary), t.TextMuted(design.TypeBody))
		}
	})
	if st.memoryEditing {
		centered(func(c *ui.Context) {
			ui.Field(c, st.tr("标题", "Title"), func() {
				ui.TextInput(c, &st.memoryEditTitle).Label(st.tr("标题", "Title")).FillWidth()
			})
			ui.Field(c, st.tr("摘要", "Summary"), func() {
				ui.TextInput(c, &st.memoryEditSummary).Label(st.tr("摘要", "Summary")).FillWidth()
			})
			ui.Field(c, st.tr("正文", "Content"), func() {
				ui.TextArea(c, &st.memoryEditBody).Label(st.tr("正文", "Content")).FillWidth().
					MinHeight(design.Space8 * 6)
			})
			ui.Row(c).FillWidth().Gap(design.Space2).Children(func() {
				save := uikit.DialogAction{Label: st.tr("保存", "Save")}
				_ = save
				saveButton := design.Apply(ui.Button(c, st.tr("保存", "Save")), t.PrimaryButton())
				if saveButton.Clicked() {
					draft := contracts.MemoryDraft{
						URI: detail.URI, Type: detail.Type, Title: strings.TrimSpace(st.memoryEditTitle),
						Summary: strings.TrimSpace(st.memoryEditSummary), Overview: detail.Overview,
						Content: st.memoryEditBody, Pinned: detail.Pinned,
					}
					go func() {
						_, err := service.Write(context.Background(), st.memoryRoot, draft)
						runtime.Post(func() {
							if err != nil {
								st.memoryEditErr = err.Error()
								runtime.Invalidate()
								return
							}
							st.memoryEditing = false
							st.memoryEditErr = ""
							startMemoryDetail(runtime, st, service, detail.URI)
						})
					}()
				}
				cancel := design.Apply(ui.Button(c, st.tr("取消", "Cancel")), t.SecondaryButton())
				if cancel.Clicked() {
					st.memoryEditing = false
					st.memoryEditErr = ""
				}
			})
			if st.memoryEditErr != "" {
				uikit.ErrorLine(c, st.tr("保存失败：", "Save failed: ")+st.memoryEditErr)
			}
		})
	}
	if !st.memoryEditing {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			centered(func(c *ui.Context) {
				if detail.Overview != "" {
					design.Apply(ui.Text(c, detail.Overview), t.Font(design.TypeXS))
				}
				if detail.Content != "" {
					design.Apply(ui.Text(c, detail.Content), t.Font(design.TypeBody))
				}
				if detail.Overview == "" && detail.Content == "" && detail.Summary == "" {
					uikit.EmptyStateBlock(c, st.tr("没有正文", "No content"), "", nil)
				}
			})
		})
	}
	if st.memoryErr != "" {
		centered(func(c *ui.Context) {
			uikit.ErrorLine(c, st.tr("读取记忆失败：", "Failed to read memory: ")+st.memoryErr)
		})
	}
	uikit.ConfirmDanger(c, &st.memoryConfirmOpen, uikit.ConfirmDangerOptions{
		Key:     "session.memory.delete",
		Title:   st.tr("删除这条记忆？", "Delete this memory?"),
		Message: st.tr("删除后无法恢复。", "This cannot be undone."),
		Confirm: st.tr("删除", "Delete"),
		Cancel:  st.tr("取消", "Cancel"),
		OnConfirm: func() {
			uri := st.memoryConfirmURI
			if uri == "" || st.memoryRoot == "" {
				return
			}
			go func() {
				err := service.Delete(context.Background(), st.memoryRoot, uri)
				runtime.Post(func() {
					if err != nil {
						st.memoryErr = err.Error()
						runtime.Invalidate()
						return
					}
					st.memoryDetail = nil
					st.memoryDetailID = ""
					st.memoryRefresh = true
					runtime.Invalidate()
				})
			}()
		},
	})
}

func buildMemoryQuickAdd(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state, service contracts.MemoryService) {
	draft := strings.TrimSpace(st.memoryDraft)
	ui.Column(c.Key("session.memory.add")).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &st.memoryDraft).Label(st.tr("新建记忆", "New memory")).
				Placeholder(st.tr("记一条…", "Remember…")).Grow(1)
			save := ui.Button(c, st.tr("保存", "Save"))
			design.Apply(save, t.PrimaryButton())
			if draft == "" {
				save.Disabled(true)
			}
			if save.Clicked() && draft != "" {
				text := draft
				st.memoryDraft = ""
				st.memoryErr = ""
				go func() {
					item, err := service.Write(context.Background(), memoryWorkspaceRootCtx(st.ctx), contracts.MemoryDraft{
						Type:    memoryvfsTypeDefault(st),
						Title:   firstLine(text),
						Summary: firstLine(text),
						Content: text,
					})
					runtime.Post(func() {
						if err != nil {
							st.memoryErr = err.Error()
							runtime.Invalidate()
							return
						}
						st.memoryRefresh = true
						st.memoryDetail = nil
						st.memoryDetailID = ""
						_ = item
						runtime.Invalidate()
					})
				}()
			}
		})
		if st.memoryErr != "" {
			uikit.ErrorLine(c, st.tr("保存失败：", "Save failed: ")+st.memoryErr)
		}
	})
}

func memoryWorkspaceRootCtx(ctx cordis.Context) string { return memoryWorkspaceRoot(ctx, nil, "") }

// requestMemoryRefresh asks the next Memory-tab frame to reload from the store.
func requestMemoryRefresh(st *state) { st.memoryRefresh = true }

func memoryvfsTypeDefault(st *state) string {
	if st.memoryType != "" {
		return st.memoryType
	}
	return "preferences"
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	runes := []rune(text)
	if len(runes) > 60 {
		runes = runes[:60]
	}
	return string(runes)
}

func intText(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func memoryAge(st *state, updated time.Time) string {
	if updated.IsZero() {
		return ""
	}
	age := time.Since(updated)
	switch {
	case age < time.Minute:
		return st.tr("刚刚", "just now")
	case age < time.Hour:
		return st.tr("几分钟前", "minutes ago")
	case age < 24*time.Hour:
		return st.tr("几小时前", "hours ago")
	case age < 7*24*time.Hour:
		return st.tr("几天前", "days ago")
	default:
		return updated.Format("2006-01-02")
	}
}

var errNoWorkspace = &memoryNoWorkspaceError{}

type memoryNoWorkspaceError struct{}

func (*memoryNoWorkspaceError) Error() string { return "no workspace is open" }
