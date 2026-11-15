# 03 · 设计系统 / 跨屏质量审计（ZeoKeel v0.2.37 · 2026-11-15）

> **只读审计**：未改代码/配置/其他文档；未运行 `go build`/`go test`（Lead 负责）。
> 证据口径：`grep` 计数均为**非测试文件**（不含 `*_test.go`）；PNG 取自 `.cache/ux-review/shots/`
> （manifest 04:15 生成，120 帧，HEAD `1f66af3`）；像素采样用 Go 标准库离线解码，未安装依赖。
> 行号对 HEAD `1f66af3`。存量（baseline/已接受残余）不重复算新账，但标注其真实用户影响。

## 1. 结论摘要

1. **基础设施层真收敛（不要再做）**：颜色零字面量（DS-01=0）、S0–S4 ramp 两主题同向（暗色 sidebar 比 page 深）、
   抽屉/浮层全部 `ShadowOverlay`（6 处）、选择态两级（侧栏 tint+2px 条；palette 瞬态 accent 填充）、主操作两落点、
   `38` 已消失。这些是过去几轮真正做完的部分，**新表面也基本沿用**（Insights 0 accent/0 primary、token 间距、暗色）。
2. **“共享层”没跟上，新表面开始分叉**：主题化分段控件被复制成 2 份（settings / insights），属性表仍是 2 套
   （uikit 右对齐 vs settings 160px 左对齐），Context 阅读列因 `.Grow(1)` 被拉宽 13% 且左移 64px，
   Context 的 8 列表格用了 **11 个裸数字宽度**——而 DS-03 只查方法实参、不查 `ui.TableColumn{Width:…}` 结构体字面量，lint 全盲。
3. **状态展示仍是 3 种解剖，不能宣称“状态只用 StatusBadge”**：`StatusBadge` 27 处调用之外，ToolRow 用行内状态词
   （刻意，已注释）、侧栏行尾状态词（DESIGN §5.2 已豁免），但仍有 3 处“中性底 + 成功字”残留（AP-21），
   而契约（AGENTS 规则 10 / DESIGN §5.5）没记录这些分层——规则与签名表面互相违反，下一轮会继续长出第四种。
4. **字号纪律仍未收敛，而且更“微缩”了**：`TypeBody` 仅 14 处引用，而 `TypeXXS` 182 / `TypeXS` 145 / `TypeXXXS` 93；
   Insights 一屏用了 5 档字号（`TypeM/S/L/XS/XXS`，违反“≤4 档”）；11px 仍有成句（“正在读取…/Esc 关闭/+N 条通知排队”）；
   Insights KPI 值直接用 `TypeL`（§5.3 规定只给设置页/对话框 H1），**规格 S11 自己批准了一个违反全局 type table 的用法**。
5. **两个直接影响用户的残留 + 一项验证债**：① toast 首条即覆盖右上内容（1512 下盖住最新用户气泡右段；1024 下占阅读列 39%，
   排队计数 11px 与消息元信息同一行视觉粘连，`narrow/narrow-session.png`）；② Insights 会话排行选中行前景未随 accent 反色
   （浅色 ink-on-royal ≈2.9:1、暗色 白-on-sky ≈2.1:1，键盘选中即不可读）；③ 新表面**没有窄窗/暗色/TextScale 帧**，
   窄窗测试只断言“文本存在”（不测遮挡/截断），TextScale 只有一个 token 传播测试——OS 大字号下会被裁切却没证据。

## 2. v2 规则落地矩阵

| # | v2 规则 | 判定 | 证据（file:line / 计数 / PNG） | 缺口与用户影响 |
|---|---|---|---|---|
| 1 | 表面 ramp S0–S4，两主题同向 | ✅ 收敛 | `brand/palette.go:188-191`（暗 page `#111118` < sidebar `#0B0B10`）；像素：`p2-insights-dark.png` sidebar `#0B0B10`(lum .0035) / page `#111118`(.0059) | 无 |
| 2 | 抽屉/浮层 elevation | ✅ 收敛 | `uiroot/frame.go:718`、`toasts.go:398`、`agentsurface.go:495`、`runstatus.go:220`、`session/view.go:3986/4283`（共 6 处 `ShadowOverlay()`） | 无 |
| 3 | 选择态两级 | ✅ 收敛（持久） | `sidebar/sidebar.go:1524/1550`（2px Accent 条）+ `1679-1681`（List 主题把 Accent→BgSelected）；`frame.go:892`（palette 瞬态 accent 填充） | 新表面“选中行前景”未处理 → P1-3 |
| 4 | 强调色预算 ≤2 accent/≤1 primary | ✅ 收敛 | `insights/view.go` 全文无 Accent 填充；`p2-landing.png` 仅 1 个 primary；`p2-insights.png` 0 primary | 无 |
| 5 | 状态徽章只用 `StatusBadge` | ⚠️ 部分 | 27 处调用（`uikit/status.go:325`）；ToolRow 行内状态词（`uikit/toolrow.go:162-172`，刻意）；**残留 3 处中性底+状态字**：`rightpanel/files.go:261`、`sidebar/sidebar.go:965`、`settings/search.go:71` | “已配置/已保存/已添加”读作普通 chip，与 StatusBadge 的语义 tint 不一致；契约未记录行内状态词这一层 |
| 6 | 字号纪律（11 禁成句、≤4 档/屏） | ❌ 未收敛 | 引用数：Body 14 / S 30 / XS 145 / XXS 182 / XXXS 93；`insights/view.go:30,140,221-226,290,479` = 5 档；11px 成句：`rightpanel/view.go:551`、`uikit/reasoning.go:124/140`、`toasts/toasts.go:351`、`uiroot/frame.go:850`；ToolRow 状态词/预览也是 11（`toolrow.go:162-172,245`，见 P3-5） | 元数据/状态仍以 12px 为主，正文 14 只出现在消息；11px 读长句（英文更长）费力 |
| 7 | 控件高度 32/36/40/44，禁 38 | ✅ 基本收敛 | 31 处 token 引用；裸高度仅 4 处：`home/home.go:366`(36)、`settings/settings.go:1341`(56)、`settings/modelcatalog_editor.go:859`(130)、`uiroot/frame.go:169`(64)；`Height(38)` 已无 | baseline 仍有 stale `Height(38)`（`.design-baseline.json` 指向 `sidebar.go:308`，现为 Slot 注册）→ 基线卫生，P3-4 |
| 8 | 圆角档位、XL 仅 composer | ✅ 收敛 | `design/components.go:146` 唯一 `RadiusXL`；chip `ControlChip` 28/SM（`components.go:186-191`） | 无 |
| 9 | 页面主操作两落点 | ✅ 收敛 | Insights 刷新在顶带 actions（`insights.go:135-152`）；设置页脚唯一 primary；模型页 `SectionAction`（V12） | 无 |
| 10 | S2 内最多一层 S3 | ✅ 收敛 | 团队/任务卡 `BgBase+BorderL2+RadiusMD`（`agentsurface.go:612-615` 成员卡、`743-746` 任务卡），叠在 S4 浮层内 | 无 |
| 11 | PropertySheet 一种（160px 标签列/值左对齐/32 行） | ❌ 未收敛 | 两套：`uikit/uikit.go:104` `KeyValueRows`（值右对齐、无 Divider）**7 处调用含新 Context**（`context_pane.go:134/173/331`）；`settings/listhelper.go:105` 才是 v2 版，仅 2 页（`network.go:56`、`settings.go:553`） | 新 Context 概览标签与值相距约 1050px（像素：标签 x=368、值右缘 x=1422）；V18 的“已修”只在 2/9 面落地 |
| 12 | 分段控件一种、选中可见 | ❌ 未收敛 | 复制 2 份：`settings/listhelper.go:78-97` 与 `insights/view.go:125-151`；选中段像素 `#FFFFFF` vs 轨道 `#F1F4FE` = **1.10:1**、边框 `#D8D9D9` vs 轨道 1.3:1（`p2-appearance.png` y=243） | 选中主要靠 600 字重与 1px 边，填充对比 1.10:1（与 V4 判定“读作未选中”的旧值同级）；无 uikit 入口、未登记 registry |
| 13 | 图标按钮统一 `uikit.IconButton` | ❌ 未收敛 | 12 处 `ui.Button(c, "")` 图标按钮（`frame.go:609/629/738`、`session/view.go:445`、`goal.go:362/411`、`memory_tools.go:343/358`、`sidebar.go:349`）+ 9 处 `ButtonBase`（含 `IconButton` 内核 2 处；`sidebar.go:1433` 仍是 20×20）；尺寸 20/24/28/36 并存 | `goal.go:365` 20×20 < 24 目标区（AP-17）；新 goal dock 抄了“第二族”；hover 态两套实现 |
| 14 | 阅读列 960 一致 | ⚠️ 部分 | Chat：`uikit/session.go:140-142`（Row 居中 + 960，无 Grow）；Goal dock：`session/view.go:545-546`（同）；**Context** `context_pane.go:80-82` 多了 `.Grow(1)` | 像素：chat 正文左缘 420 / goal 427；Context 左缘 368，列宽约 1086（352–1438）vs 960；切页签时文本列左移 64px、行宽 +13% |
| 15 | 度量 token 化 | ⚠️ 部分（lint 盲区） | `context_pane.go:273-275,388-397` 共 11 个 `ui.TableColumn{Width:92/76/48/120/280/88/56…}`；DS-03 只遍历 `CallExpr`（`design/conform/rules.go:78-86,113-150`），结构体字段/`Margin` 不查 | 英文更长（“Completion”/“Cached”）在 76px 宽度下会截断；宽度无 token 单一来源 |
| 16 | 文档/registry 同步 | ⚠️ 部分 | `design/registry.json` = 20 components / 2 scenarios；无 Insights KPI/分段/Context/Goal dock 条目；DESIGN §7.2/§8.3 要求登记 | DS-08 只校验“已登记项与文档一致”，不校验“新表面是否登记”→ 新组件长期在契约外 |
| 17 | 暗色覆盖所有新表面 | ⚠️ 部分 | 新表面有 dark 帧（context/insights/goal/toolrow/markdown）；120 帧中暗色 11 帧，**无暗色团队浮层 / 运行中 composer / cold-resume / 多数设置页** | 暗色回归不可见；`narrow/dark-*.png` 实为 1512 宽，**没有“暗色+窄窗”组合** |
| 18 | 窄窗可用（900×600） | ⚠️ 部分 | 有 `p2-goal-narrow` 与 narrow 套件；**无 insights/context 窄帧**；`context_test.go:553`、`insights_test.go:497` 只断言文本存在 | 遮挡/截断/横向滚动均无几何断言（对比 `TestToolRowStateGutterDoesNotReflow` 的写法） |
| 19 | 焦点/Esc 分层 | ✅ 收敛 | goal 清除走 `ConfirmDanger`（`goal.go:461-480`）；`goal.go:411` 菜单有 Label；`uikit.go:342` 安全默认 | 无（命令族对话框见 V9 残余） |
| 20 | 动效克制 | ✅ 收敛 | 产品无自写 transition/tween；`ui.Spinner` 21 处；ReduceMotion 由框架处理（mygo `docs/ui/motion.md:545`） | 无 |
| 21 | a11y 播报（aria-live） | ⚠️ 部分 | 错误行走 `RoleAlert`（`uikit.go:150`）；`c.Announce` 仅 3 处（`frame.go:129/136/138`） | goal 阶段变化、Insights 加载完成/刷新、队列变化均无播报；IconButton 有 Label |
| 22 | i18n 完整性 | ⚠️ 部分 | 新表面 `tr()` 完整；但 `context_pane.go:390` 表头 “Prompt” 未本地化；English 帧（`p2-english.png`）审批 toast 正文仍是中文 | 中英混排表头；通知正文的跨模块 locale 未端到端验证 |
| 23 | TextScale 行为 | ⚠️ 部分 | `design/style.go:33-40` 约定“文本类控件自缩放高度”；仅 11 处 `t.Scale(`；`insights/view.go:128-132` 轨道 36/段 28 固定，TypeS 行高 24×1.5=36 会裁切 | 无 TextScale 帧测试/快照（仅 `tokens_test.go:14`、`uiroot/conformance_test.go:124` 验 token 传播） |
| 24 | 性能感/帧内 I/O | ✅ 基本 OK | Insights 后台 goroutine + `UIRuntime.Post` + 60s 冷却（`insights/insights.go:194-246`）；Context 每次进入读一次有界折叠（`observability.go:21,406-407`，200 调用）；transcript/表格虚拟化 | 唯一同步读在 build 内（`session/view.go:105-107`，锁内拷贝，量有界）；刷新中按钮 disabled 但无 spinner/播报（P3-2） |

## 3. 升级机会

### P1（本轮应做：共享层收口 + 两个真实可读性缺陷）

**P1-1 把“主题化分段控件”和“属性表”收成 uikit 唯一实现**
- 问题：同一控件两份 25 行复制（settings/insights），属性表两套解剖；新 Context 抄了旧属性表。v2 的 V4/V18 声称已修，实际只修了旧调用点。
- 证据：`settings/listhelper.go:78-97` vs `insights/view.go:125-151`；`uikit/uikit.go:104`（7 处）vs `settings/listhelper.go:105`（2 处）；`p2-appearance.png` 选中段 1.10:1；`p2-context.png` 值右缘 x=1422。
- 建议：新增 `uikit.SegmentedControl`（包 `ui.SegmentedBase`，一个 look：选中段用 `BgSelected`(12% Accent) + `LabelPrimary`+600，与持久导航选择同一语义，不用白底）与 `uikit.PropertySheet`（160 列/值左对齐/32 行/Divider）；settings 2 处 + insights 1 处、7 处属性表迁移；registry 增加两项。
- 改动面：`internal/uikit`（新文件）+ settings/insights 调用点；DESIGN §6.2、§7.1、registry、UI-CONFORMANCE §1。
- 验证：帧测试断言 radio 语义/方向键/Changed 不回归；像素断言选中段与轨道对比 ≥3:1（仿 `uikit/onaccent_test.go`）；`p2-appearance/p2-insights/p2-context` 前后帧。
- 风险/依赖：settings 与 insights 的视觉会有一次可见变化，需快照评审；不引入依赖。
- 工作量：**M**。

**P1-2 Context 阅读列与 Chat/Goal dock 对齐（去掉 `.Grow(1)`）**
- 问题：Context 页正文列比 Chat 宽 13%、左移 64px，切换页签时文本列横移；表格随之拉宽，加重 P2-2 的“数值贴右”。
- 证据：`context_pane.go:80-82` 在 `Row Justify(Center)` 里对 960 列加 `.Grow(1)`；对比 `uikit/session.go:140-142`、`session/view.go:545-546` 无 Grow。像素：`p2-context.png` 内容左缘 368、右缘≈1438；`p2-goal.png` 正文左缘 420、目标条 427。
- 建议：删 `.Grow(1)`，列保持 `Width(960).MaxWidthPercent(92)`（与 SessionThread 同构），`PaddingX(Space4)` 保留；把这段布局抽成 `uikit.ReadingColumn`（chat/context/goal dock 共用），或在 `uikit` 注释里钉死“阅读列 = 960 居中，不许 Grow”。
- 改动面：`session/context_pane.go` 一条链 + 可选 uikit 帮助函数；帧测试。
- 验证：新增帧测试断言 Context body 左缘 == Chat body 左缘（Tester 的 AccessByRole/Element 几何）；快照 `p2-context`。
- 风险：仅布局，无行为风险。
- 工作量：**S**。

**P1-3 Insights 会话排行的选中行前景（accent 反色缺失）**
- 问题：键盘 ↑↓ 选中行时 `ui.List` 用 accent 填充整行，但行内文字显式设了 resting 色，不随选中反色——浅色 2.9:1、暗色 2.1:1，选中即不可读。
- 证据：`insights/view.go:434` 设置 `Selected`；`465-499` 行内 `TextColor(LabelPrimary/LabelTertiary/LabelSecondary)`；mygo `ui/table.go:711-721` `Selected` 会设 Accent bg + AccentText，但显式色覆盖；对比 `UI-CONFORMANCE §1.3` 已把该 bug 类写进规则（`onaccent_test`）。
- 建议：行内改 `if row.Selected() { 用 t.AccentText / uikit.OnAccentBadge }`（`ui.ListRow.Selected()` 已提供），成本徽章用 OnAccentBadge；补测试。
- 改动面：`insights/view.go` 单文件 + 一个帧/像素测试。
- 验证：像素断言选中行文字与 accent 底 ≥4.5:1（亮/暗两测）；帧测试断言 `Selected` 时文本色。
- 风险：无。
- 工作量：**S**。

### P2（下一轮：跨屏一致 + 验证补强）

**P2-1 TableColumn 宽度 token 化 + DS-03 扩到复合字面量**
- 问题：新 Context 用 11 个裸宽度，lint 看不见；英文表头在 76px 会截断，未来宽度调整无单一来源。
- 证据：`context_pane.go:273-275,388-397`；`design/conform/rules.go:78-86` 只匹配方法名，`113-150` 只取 `CallExpr` 实参。
- 建议：加 `design.TableColNarrow/Wide/…` 或 `uikit.TableCellWidth(role)` 一组 token（4 的倍数），替换 11 处；designlint DS-03 增加 `*ast.CompositeLit` + `ui.TableColumn` 类型识别；`Margin/BorderWidth` 进方法集前先评审误报。
- 改动面：`internal/design`、`conform/rules.go`、context_pane；基线只降不升。
- 验证：`designlint --strict` 0 new；`p2-context` 中文/英文帧（英文列头不截断）。
- 风险：designlint 改动会扫出新存量裸值（预期少量，入 baseline 需 owner 批准——按纪律应直接修）。
- 工作量：**M**。

**P2-2 宽窗表格“数值贴右、首列吃满”**
- 问题：1512 宽下 Insights 按天表首列约 860px（`MaxWidth:192` 对 `Width:0` 共享列无效），数值列全挤在右缘；Context 模型调用同样（模型列拉伸、8 列贴右）——行阅读需横跳约 860–900px，像空 dashboard。
- 证据：像素 `p2-insights.png` y=262 分隔线 x=1167/1263/1359/1423/1487；`insights/view.go:238-249`（只有 Min/Max，无 Width）；`p2-context-sections.png`。
- 建议：给表格区加 `MaxWidth(design.TranscriptWidth)`（或页面 measure）并左对齐；或首列给显式 `Width` + 尾部占位列；KPI 行与表格对齐同一 measure。不要改成卡片/图表。
- 改动面：`insights/view.go`、`context_pane.go` 的 Section/Table 容器；`uikit` 可加 `TableMeasure`。
- 验证：新增 1512 帧像素断言列边界；窄窗帧确认横向滚动仍正常。
- 风险：宽窗观感变化，需设计确认 measure（960 vs SettingsWidth 820）。
- 工作量：**S–M**。

**P2-3 Toast 内容安全区（R16 残余的量化与修法）**
- 问题：这是已接受的 R16 残余，但证据比文档更重：1512 下首条 toast（x=1216–1495）覆盖右对齐用户气泡（阅读列右缘 1376，气泡右缘≈1366）→ `p1-chat.png` 最新提问尾巴被盖 4s；1024 下 toast 占阅读列 39%，且 “+3 条通知排队”（11px）与消息元信息同一行、视觉粘连（`narrow/narrow-session.png` y≈227）。
- 证据：`toasts.go:52`（2 条）、`66`（inset 84）、`70`（min 280）、`351`（排队句 11px）；像素测量。
- 建议（两选一）：A) toast 可见时由壳层给主内容列加“顶部安全区”（toast 数量×高度），toast 关闭后恢复——需 host/uiroot 与 toasts 模块共享状态（slot 顺序即可）；B) 最小改动：toast 宽度压到 240 + 右侧 12、最多 1 条 + 排队计数移入 toast 内，会话右上元信息/动作行改到气泡内不依赖右上角。倾向 A（符合“右上固定”规格且彻底）。
- 改动面：`uiroot`（安全区传递）、`toasts`、`uikit/session` 顶部 padding；帧测试。
- 验证：帧测试断言 toast 矩形与最新用户气泡/更新时间元信息矩形不相交（Tester 几何）；`p1-chat`/`narrow-session` 前后帧；暗色帧。
- 风险：涉及壳层顺序与多表面；不要把 toast 移到底部（会盖 composer/状态条）。
- 工作量：**M**。

**P2-4 状态展示分层写成契约 + 清 3 处 AP-21 残留**
- 问题：契约写“状态只用 StatusBadge”，但 ToolRow/侧栏行尾是刻意行内状态词；同时 3 处仍是中性底+状态字。规则既不准确也没被执行。
- 证据：`DESIGN.md §5.5` 规则 6、`AGENTS.md` 规则 10；`toolrow.go:162-172`；`DESIGN.md:525-534`（侧栏豁免）；`files.go:261`、`sidebar.go:965`、`search.go:71`。
- 建议：在 DESIGN §5.2 增加“状态展示三层”：①对象头/卡头/任务=StatusBadge；②紧凑行/签名 ToolRow/侧栏行尾=StatusText（附色 + 文字）；③瞬时成功提示可用 Badge，但必须 `SuccessText` + 图标（或直接改 StatusBadge）。3 处残留迁移或明确豁免并注释。
- 改动面：文档 + 3 处代码；DESIGN-LINT DS-14 的 grep 检查描述同步。
- 验证：`grep 't.Badge()).TextColor(t.(Danger|Warning|Success)Text)'` 归零；快照复看。
- 风险：低；文档变更需 owner。
- 工作量：**S**。

**P2-5 补验证：新表面窄窗/暗色/TextScale 帧 + 几何断言**
- 问题：新表面只有默认尺寸亮/暗帧；窄窗测试只断言文本存在；TextScale 无帧。导致 P1-2/P2-2 这类问题只能靠像素采样发现。
- 证据：manifest 120 帧中无 `p2-insights-narrow`/`p2-context-narrow`；`narrow/dark-*.png` 实为 1512 宽；`context_test.go:553-567` 仅 `assertTexts`；TextScale 仅 `tokens_test.go:14`。
- 建议：ui-snapshot 增加 insights/context 的 1024 与 dark×1024 场景；narrow 测试加矩形断言（读 `AccessByRole` 几何，仿 `TestToolRowStateGutterDoesNotReflow`）；加 `Tester.SetPreferences{TextScale:1.5}` 的 frame test（至少 chat/insights/context/composer）+ 修 P3-1 的固定高。
- 改动面：`internal/devtools/ui-snapshot`、`internal/ui/insights`、`internal/ui/session` 测试。
- 验证：新增帧人工过一遍；测试断言不裁切（composer 高度/行高）。
- 风险：快照数量增加，评审成本 +；不引入依赖。
- 工作量：**M**。

**P2-6 图标按钮收口到 uikit.IconButton**
- 问题：`uikit.IconButton` 已是唯一入口（`iconbutton.go:99`），但 shell/会话/侧栏仍有 12 处 `ui.Button(c,"")` + 9 处 ButtonBase 图标按钮，尺寸 20/24/28/36；goal dock 20×20 低于 24 目标区。
- 证据：见矩阵 #13；`goal.go:362-366/409-418`；`sidebar.go:1433`。
- 建议：给 IconButton 增加 `Variant: Native`（用 `ui.Button` 实现，保留 shell 的 native hover/pressed）后迁移 12 处；统一 24/28 两档；goal 清除按钮 hover-only 改为 focus-visible 同显，并补键盘路径测试。
- 改动面：`uikit/iconbutton.go`、uiroot/sh 会话/侧栏调用点；帧测试。
- 验证：`grep 'ui.Button(c, "")'` 降到 0（保留 allowlist 例外需登记）；hover/focus/disabled 帧测试。
- 风险：触及壳层 chrome，需逐屏过帧；迁移量大。
- 工作量：**M–L**。

### P3（打磨与债务）

**P3-1 字号收尾 + 指标值 role**
- 问题：11px 成句 5 处；Insights 5 档字号；KPI 值 `TypeL` 与 §5.3 冲突；Context 时间用 11（规则要求时间/计数用 XXS）。
- 证据：`rightpanel/view.go:551`、`reasoning.go:124/140`、`toasts.go:351`、`insights/view.go:222`、`context_pane.go:520`。
- 建议：成句改 12；新增 `TypeMetric`（20/28/600，仅指标值）写入 §5.3，S11 同步；Context 时间→XXS；`TypeXXXS` 保留 kbd/微标签/终端 gutter。
- 改动面：`design/tokens.go` + 约 6 处调用 + 文档；验证：快照 + 新增 DS-15 评审 checklist。
- 工作量：**S**。

**P3-2 刷新/加载反馈 + 播报**
- 问题：Insights 刷新中按钮 disabled 但无 spinner/完成播报；Context 加载态是纯文本且几乎不可达（同步读在 build 前完成，`context_pane.go:95-96` 近似死分支）；goal 阶段变化无 `c.Announce`。
- 证据：`insights/view.go:38-53`（只有 Disabled）、`insights/view.go:74-75`（仅无数据时显示加载态）；`context_pane.go:95-96`；`goal.go:32-46`（状态映射）无 Announce；`grep -rn 'c.Announce' internal` 仅 3 处。
- 建议：刷新按钮加 `ui.Spinner` 或 aria-busy + `c.Announce`（完成/失败）；goal 状态变化用 `c.Announce`；Context 删除死分支或补 real spinner。
- 改动面：`insights/view.go`、`session/view.go`/`goal.go`；帧测试。
- 工作量：**S**。

**P3-3 i18n 收尾**
- 问题：Context 表头 “Prompt” 中文界面未翻译；English 帧审批 toast 正文仍中文（`approvals.go:341` 有本地化实现，疑 locale 未到模块或通知早于切换）；snapshot fixture 只有中文。
- 证据：`context_pane.go:390`；`p2-english.png` 右上 toast；`approvals.go:341-352` 的 `english :=` 分支；`ui-snapshot/main.go:657` 的 fixture 文案。
- 建议：表头改 `st.tr("输入","Prompt")`；English 帧端到端验证通知（加 approvals i18n 帧测试）；snapshot 支持 en fixture。
- 改动面：`context_pane.go:390`、approvals/snapshot；工作量 **S**。

**P3-4 旧残余与基线卫生**
- 问题：V9 设置页 6 处手写 TypeL/600 对话框标题（`settings/memory.go:811`、`skills_edit.go:93/137`、`modelcatalog_editor.go:611`、`modelcatalog.go:604`、`presets.go:675`）vs 5 处 `DialogHeader`；V22 会话记忆行/设置记忆行两套行尾操作（`session/memory_tools.go:334` vs `settings/memory.go:233`）；baseline 156 条含 stale `Height(38)`。
- 证据：上述 file:line；`.design-baseline.json` 156 条（146 DS-03 + 10 DS-06，`Height(38)` 指向的 `sidebar.go:308` 现为 Slot 注册）。
- 建议：对话框标题迁 DialogHeader；记忆行抽 `uikit.ListRow`（下一轮）；清 stale baseline（需 owner 按纪律执行）。
- 工作量：**S**。

**P3-5 工具行折叠摘要的可读性（11px 等宽 + 原始键值串）**
- 问题：团队/任务类工具的折叠预览把参数原样拼成一行，如
  “created shared team task id: task-1 subject: 检查发布清单 status: pending owner: – blocked_by: – write…”，
  11px 等宽、中英混排 150+ 字符；用户扫不出“这个工具做了什么”。
- 证据：`p2-tool-row-running.png` y≈285；`uikit/toolrow.go:239-249`（mono TypeXXXS 预览）、`uikit/toolrow.go:424`（`ToolPreview` 截断）；
  工具行本身是签名表面（不要求重做），只改摘要来源。
- 建议：为已注册工具提供“人类摘要器”（团队/任务/bash/文件各一条：动词 + 目标 + 关键值），未知工具才回退原始键值；
  摘要 12px（XXS），原始参数保留在展开面板与 tooltip；等宽只留给命令/路径。
- 改动面：`uikit/toolcall.go`/渲染注册器 + 少量测试；不改变行结构、不动 gutter/状态词。
- 验证：`p2-tool-row-*` 前后帧；帧测试断言团队工具摘要不含 “id:/subject:/status:” 之类协议键。
- 风险：属于工具行语义，需与 surface owner 对齐（本次只提可读性证据）。
- 工作量：**S–M**。

## 4. 不建议做（anti-scope）

1. **不要为了“统一”重画已验证的签名表面**：Agent Run Band、State-first Tool Row（固定 gutter 不 reflow）、Markdown 阅读主题、品牌暗色 ramp 都已达标，重做只会引入回归。
2. **不要引图表库/canvas/卡片 dashboard**：Insights 的克制（无图、无卡墙、0 accent）是对的；数据页的问题是宽度与属性表，不是“缺图”。
3. **不要把 toast 移到底部**（会盖 composer/状态条），也不要为了躲 toast 隐藏消息元信息（AGENTS 规则 17）。
4. **不要新增第二套主题/设计 token 层**；分段/属性表/图标按钮都应进 `internal/uikit` 而不是新包。
5. **不要为过 lint 向 `.design-baseline.json` 加新条目**；P2-1 若扫出存量，应直接修或经 owner 批准后 shrink。
6. **不要在窄窗隐藏状态/信息来“解决拥挤”**（V27 的教训：零值入口已回滚过一次）。

## 5. 局限

- 未运行 `go build`/`go test`/`designlint`（按分工由 Lead 负责），结论基于静态代码 + 现有 120 帧 + 自写像素采样；lint 计数未实测，只用 grep 复算。
- 像素采样为点/行采样（Go stdlib），不是整帧 diff；对比度数值由 sRGB 相对亮度计算，未做真实显示器校准。
- 未生成新帧（快照工具在本会话期间仍在生成，未重复运行）；`p2-context.png` 的列宽推断依赖可见像素，误差 ±2px。
- 未覆盖需真实桌面验证的项：OS TextScale 下的实际裁切、真实剪贴板、系统通知镜像、输入法；未做性能剖析，仅审代码路径（Insights/Context 均为后台/有界读）。
- 团队/审批/计划/提问等 surface 的交互语义由其他审计条目负责；本文只对其共享层（属性表/分段/图标按钮/状态解剖）发言。
- 结论对应 HEAD `1f66af3`（v0.2.37）；后续表面继续演进时，请把 P1-1 的 uikit 组件作为新表面的唯一入口。
