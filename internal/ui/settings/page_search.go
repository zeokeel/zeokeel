package settings

// The settings search's module half: a page declares its own searchable items
// (contracts.SettingsPageSpec.SearchItems) and the region each item lives in
// (its AnchorKey). When the host's navigation search lands on an item, it calls
// the page's Reveal with the anchor key before the page builds; the page opens
// the disclosure around the region, scrolls it into view and tints it briefly so
// the user sees exactly which control the search found.
//
// The page owns this because only the page knows its layout: the host owns the
// index (keywords and labels) and the page owns the reveal.

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// settingsSearchHighlightDuration is how long a located region keeps its "found
// it" tint before the page returns to its normal look.
const settingsSearchHighlightDuration = 1500 * time.Millisecond

// pageSearch is one page's search-locate state: the anchor key the host asked
// the page to reveal and the transient highlight around it. It is UI-thread
// state owned by the page definition.
type pageSearch struct {
	target string
	until  time.Time
}

// reveal records the host's request before the page builds. The page's build
// opens the disclosure around the region (a collapsed 高级选项 or 更多对话设置)
// and its region helper scrolls the region into view and tints it.
func (s *pageSearch) reveal(c *ui.Context, anchorKey string) {
	if s == nil || anchorKey == "" {
		return
	}
	s.target = anchorKey
	s.until = c.Now().Add(settingsSearchHighlightDuration)
}

// targetedAt reports whether this build was asked to reveal anchorKey.
func (s *pageSearch) targetedAt(anchorKey string) bool {
	return s != nil && anchorKey != "" && s.target == anchorKey
}

// regionInside reports whether the reveal targets a region whose key starts with
// prefix; the page uses it before building to open the disclosure that holds it
// (the region's own element is not built yet when the disclosure is collapsed).
func (s *pageSearch) regionInside(prefix string) bool {
	return s != nil && prefix != "" && len(s.target) >= len(prefix) && s.target[:len(prefix)] == prefix
}

// region wraps one searchable region of the page. The wrapper adds no padding or
// gap, so tinting a region never reflows the page; while the highlight is on,
// the region is also scrolled into view (again each frame, so a disclosure
// still animating open ends with the found region on screen) and the page keeps
// requesting frames until the tint clears.
func (s *pageSearch) region(c *ui.Context, anchorKey string, build func(*ui.Context)) {
	s.regionElement(c, anchorKey, ui.Column(c).FillWidth(), build)
}

// regionElement applies the search highlight to one anchored region.
func (s *pageSearch) regionElement(c *ui.Context, anchorKey string, element ui.Element, build func(*ui.Context)) {
	if s.targetedAt(anchorKey) && c.Now().Before(s.until) {
		t := design.Of(c)
		element = element.Background(t.BgSelected).Radius(design.RadiusSM)
		element.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter})
		// The highlight clears on a wall-clock deadline: keep frames coming so
		// the page repaints when it ends even when nothing else changes.
		c.Invalidate()
	}
	element.Children(func() { build(c) })
}

// --- module-declared search vocabulary ------------------------------------
//
// The words users search for live here, next to the pages that own the
// settings, instead of in a host-side table keyed by page ID (which drifted:
// "压缩" and "价格" were only reachable through the setting's own label, and a
// renamed page left its aliases behind). Both languages are always included, so
// the navigation search matches a Chinese or an English word in either UI
// language.

// modelPageKeywords covers the model configuration page: connections, keys,
// the context budget/compaction and the per-model pricing. Iterations are not
// searchable: the cap is assembly-level and has no user knob (v0.2.50).
func modelPageKeywords() []string {
	return []string{"模型", "model", "密钥", "api key", "apikey", "base url", "地址", "端点", "服务商",
		"provider", "上下文", "context", "上下文预算", "budget", "压缩", "compact", "compaction",
		"提示词", "prompt", "系统提示词", "价格", "price", "定价", "成本", "cost",
		"计费", "billing", "温度", "temperature", "top p", "思考", "推理", "reasoning", "测试连接", "目录", "catalog",
		"生成参数"}
}

// modelPageSearchItems are the model page's searchable regions.
func modelPageSearchItems(locale contracts.LocaleService) []contracts.SettingsSearchItem {
	return []contracts.SettingsSearchItem{
		{ID: "context-budget", Label: localized(locale, "上下文预算", "Context budget"), AnchorKey: "model.context-budget",
			Keywords: []string{"压缩", "compact", "compaction", "自动压缩", "上下文", "context",
				"预算", "budget", "token", "prompt token"}},
		{ID: "system-prompt", Label: localized(locale, "系统提示词", "System prompt"), AnchorKey: "model.system-prompt",
			Keywords: []string{"提示词", "prompt", "系统提示", "system prompt", "instruction"}},
		{ID: "pricing", Label: localized(locale, "模型价格", "Model pricing"), AnchorKey: "model.catalog",
			Keywords: []string{"价格", "price", "pricing", "定价", "成本", "cost", "计费", "billing",
				"模型列表", "catalog", "模型目录"}},
		{ID: "api-key", Label: localized(locale, "API 密钥", "API key"), AnchorKey: "model.catalog",
			Keywords: []string{"密钥", "api key", "apikey", "服务商", "provider", "base url", "地址", "端点"}},
	}
}

// behaviorPageKeywords covers the conversation behavior page.
func behaviorPageKeywords() []string {
	return []string{"行为", "behavior", "权限", "permission", "审批", "approval", "沙箱", "sandbox",
		"工作步骤", "steps", "详细", "verbose", "精简", "minimal", "创作", "creative", "模式", "mode",
		"消息字号", "字号", "字体大小", "text size", "font size", "工作区指令", "agents.md", "instructions"}
}

// behaviorPageSearchItems are the behavior page's searchable regions.
func behaviorPageSearchItems(locale contracts.LocaleService) []contracts.SettingsSearchItem {
	return []contracts.SettingsSearchItem{
		{ID: "work-details", Label: localized(locale, "工作步骤", "Work details"), AnchorKey: "behavior.work-details",
			Keywords: []string{"工作步骤", "steps", "详细", "verbose", "精简", "minimal", "折叠", "collapse"}},
		{ID: "message-size", Label: localized(locale, "消息字号", "Message text size"), AnchorKey: "behavior.message-size",
			Keywords: []string{"字号", "字体大小", "字体", "text size", "font size", "对话文字大小", "message text"}},
		{ID: "workspace-instructions", Label: localized(locale, "工作区指令文件", "Workspace instructions file"), AnchorKey: "behavior.workspace-instructions",
			Keywords: []string{"工作区指令", "agents.md", "instruction", "指令文件", "workspace"}},
	}
}

// notificationsPageKeywords covers the notifications page.
func notificationsPageKeywords() []string {
	return []string{"通知", "notification", "气泡", "toast", "声音", "sound", "桌面", "desktop",
		"系统通知", "system notification", "通知中心", "notification center"}
}

// notificationsPageSearchItems are the notification page's searchable regions.
func notificationsPageSearchItems(locale contracts.LocaleService) []contracts.SettingsSearchItem {
	return []contracts.SettingsSearchItem{
		{ID: "toasts", Label: localized(locale, "通知气泡", "Notification toasts"), AnchorKey: "notifications.toasts",
			Keywords: []string{"气泡", "toast", "应用内通知", "in-app notification", "声音", "sound"}},
		{ID: "system", Label: localized(locale, "系统通知", "System notifications"), AnchorKey: "notifications.system",
			Keywords: []string{"系统通知", "system notification", "桌面", "desktop", "通知中心", "notification center"}},
	}
}

// appearancePageKeywords covers the appearance page. The message text size is
// the Behavior page's setting, so the appearance page declares no font/size
// words for it: an alias points at the page that owns the control (the old
// host-side table listed "字号" here and drifted from the UI).
func appearancePageKeywords() []string {
	return []string{"外观", "appearance", "主题", "theme", "深色", "dark", "浅色", "light", "跟随系统",
		"system", "外观模式", "语言", "language", "中文", "英文", "english", "chinese", "热键", "hotkey",
		"快捷键", "shortcut", "托盘", "tray", "开机", "启动", "autostart", "单实例", "single instance"}
}

// appearancePageSearchItems are the appearance page's searchable regions.
func appearancePageSearchItems(locale contracts.LocaleService) []contracts.SettingsSearchItem {
	return []contracts.SettingsSearchItem{
		{ID: "theme", Label: localized(locale, "主题", "Theme"), AnchorKey: "appearance.theme",
			Keywords: []string{"主题", "theme", "深色", "dark", "浅色", "light", "跟随系统", "system", "外观模式"}},
		{ID: "language", Label: localized(locale, "界面语言", "Interface language"), AnchorKey: "appearance.language",
			Keywords: []string{"语言", "language", "中文", "英文", "english", "chinese"}},
		{ID: "desktop", Label: localized(locale, "桌面集成", "Desktop integration"), AnchorKey: "appearance.desktop",
			Keywords: []string{"托盘", "tray", "开机", "启动", "autostart", "单实例", "single instance",
				"桌面集成", "desktop"}},
		{ID: "hotkey", Label: localized(locale, "全局热键", "Global hotkey"), AnchorKey: "appearance.hotkey",
			Keywords: []string{"热键", "hotkey", "快捷键", "shortcut", "全局快捷键", "global shortcut"}},
	}
}

// diagnosticsPageKeywords covers the read-only diagnostics page.
func diagnosticsPageKeywords() []string {
	return []string{"诊断", "diagnostic", "日志", "log", "信息", "info", "提交", "commit",
		"构建", "build", "复制", "copy", "运行时", "runtime"}
}

// shortcutsPageKeywords covers the keyboard shortcuts page.
func shortcutsPageKeywords() []string {
	return []string{"快捷键", "shortcut", "键位", "keybinding", "恢复默认", "restore default", "键盘", "keyboard"}
}

// updatePageKeywords covers the update page.
func updatePageKeywords() []string {
	return []string{"更新", "update", "版本", "version", "检查更新", "自动更新", "changelog", "升级", "upgrade"}
}

// importPageKeywords covers the import page.
func importPageKeywords() []string {
	return []string{"导入", "import", "dsh", "迁移", "migrate", "数据目录", "data dir"}
}

// searchPageKeywords covers the web search page.
func searchPageKeywords() []string {
	return []string{"搜索", "search", "网络搜索", "web", "引擎", "engine", "api key", "服务商", "provider"}
}

// archivePageKeywords covers the archived sessions page.
func archivePageKeywords() []string {
	return []string{"归档", "archive", "恢复", "restore", "已归档", "会话", "session"}
}

// memoryPageKeywords covers the memory page.
func memoryPageKeywords() []string {
	return []string{"记忆", "memory", "提取", "extract", "去重", "tidy", "全局", "global", "整理", "consolidate"}
}

// lspPageKeywords covers the language-server page.
func lspPageKeywords() []string {
	return []string{"lsp", "语言服务", "language server", "补全", "completion", "诊断", "diagnostics"}
}

// networkPageKeywords covers the network page.
func networkPageKeywords() []string {
	return []string{"网络", "network", "代理", "proxy", "超时", "timeout", "证书", "certificate", "tls",
		"http", "https"}
}

// dataPrivacyPageKeywords covers the data & privacy page.
func dataPrivacyPageKeywords() []string {
	return []string{"数据", "data", "隐私", "privacy", "导出", "export", "清除", "清空", "clear",
		"备份", "backup", "重置", "reset"}
}
