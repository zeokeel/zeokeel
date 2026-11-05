# 设计与 UX 复核 round 4（P0 修复验证 + v1 签收建议）

> 角色：UI/UX 研究员（Team 成员 ux-research） · 任务：task-32 · 日期：2026-11-05
> 复核对象：`HEAD 68bc842`（P0 修复提交）+ `HEAD 3eb3c2e`（round 3：工具卡/设置窗口等）的 **22 张预览 + 代码 + 文档**
> 方式：只读。22 张预览逐张 `read_image` + 关键区域放大 + ImageMagick 取色按 WCAG 2.x 公式计算；代码用 `git show/git grep HEAD` 抽查；`git archive HEAD` 导出到 `/tmp/repo-head4` 后独立复验。
> 未改动代码/图片/其他文档，未 `git add/commit`；唯一产物是本文件。评审时工作树干净（`git status` 为空）。

---

## 0. 一句话结论与签收判定

**P0 三项修复全部验证通过：深色字标白边已消失、浅色三级文字在四组表面均达 WCAG AA、Automation/Memory 破坏性操作已接入确认弹层 + Danger 语义；第四轮是"修 bug + 补验收"的收口轮，方向与执行都成立。但视觉结论仍然是"能用、统一、克制、不出彩"——v1 视觉/UX 可以"有条件签收"：收口前必须修掉 1 个新发现的 P1（工具卡错误面板浅色对比 4.28:1），并把 Settings 的双关闭入口收敛（或明确记录为 v1 取舍）；真机 IME/屏幕阅读器/DPI/通知矩阵是发布门禁，不是设计签收门禁。**

| 维度 | 判定 | 说明 |
| --- | --- | --- |
| 结构/信息架构 | ✅ 通过 | 9 区 + 工具卡 + 设置独立窗口，层级清晰 |
| 品牌一致性 | ✅ 通过 | 字标/色板/暗色表面/图标/字重一致；无 DSH 文案 |
| 可读性（WCAG） | ✅ 通过（1 处新例外） | 三级文字 4.76–5.29:1；深色全通过；**工具卡错误面板 4.28:1**（见 §3） |
| 交互一致性 | ◐ 有条件 | 破坏性操作已统一；Settings 关闭入口仍 3 重；卡片/tab 语法未收敛 |
| 桌面感 vs Web 感 | ✅/◐ | 设置独立窗口、菜单、通知、窗口状态已落地；滚动态/TextScale 仍为已知限制 |
| **v1 签收建议** | **有条件可签收（视觉/UX scope）** | 条件见 §4；剩余 P1/P2 可延后到 v1.1 并写入已知限制 |

**诚实评价**：本轮没有任何一项让产品"更出彩"，它的价值是让上一轮评审的硬伤真实消失、并让"完成"变得可验证。`Home` 仍像 landing page、列表页仍有双主按钮、卡片/tab 仍是两套语法——这些是"完成度"与"设计主张"的差距，不是 bug；建议 v1 先收口，v1.1 再做主张。

---

## 1. 复核范围与方法

- 预览：`docs/design/preview/` **22 张**（11 个视图 × 浅/深）：home、session、sidebar、settings、settings-window（新，900×720）、plugins、automation、trajectory、right-sidebar（Start）、right-sidebar-files、toolcard（新）。
- 文档：`docs/design/native-ui.md`（已扩到 §1–20）、ADR 0001–0005、`docs/design/known-limitations.md`、README、STATUS、`docs/research/05-final-audit.md`、`06-acceptance.md`、本报告前身 `review-round3.md`。
- 代码：`internal/brand/palette.go`、`internal/shell/{assets,home,chrome,settings_ui,automation_ui,memory_ui,toolcards,plugins,trajectory,right_sidebar}.go`、`internal/app/settings_window.go`、`internal/brand/brand_test.go`、`internal/shell/assets_test.go`。
- **HEAD 全绿复验**（`/tmp/repo-head4` 独立副本）：`gofmt -l` 空；`go vet ./...` exit 0；`go test -count=1 ./...` **exit 0**（17 包；shell 47.102s、session 32.907s）。22 张预览 22 个 PNG 均在 HEAD 跟踪（`git ls-files` 23 = README + 22 PNG）。
- 限制：headless 静态帧，看不到焦点/hover/动效/窗口标题栏/系统菜单/通知真实外观；未真机。对比度取色有 ±0.05 误差；结论以代码常量与实测像素交叉验证。

---

## 2. review-round3 Top10 状态更新（逐项验证）

| # | round3 问题 | 本轮状态 | 验证证据 |
| --- | --- | --- | --- |
| **P0-1** | 破坏性操作无确认 / 危险态不一致 | ✅ **已修复** | Automation 删除改为 `automationDeleteDialog`（`automation_ui.go:481/490/503/513`），Memory Clear all 改为 `memoryClearDialog`（`memory_ui.go:179/187`）：Cancel 自动聚焦、Esc/点外关闭、Enter 不误删、Danger 确认按钮、失败保持弹层。测试 `TestAutomationDeleteRequiresConfirmation`（automation_ui_test.go:246）、`TestMemoryClearAllRequiresConfirmation`（memory_ui_test.go:143）通过；预览中行内删除已变红（`preview/automation-native-{light,dark}.png`） |
| **P0-2** | 浅色三级文字不达 AA | ✅ **已修复** | `TextTertiary #7E8796→#646C7A`（`internal/brand/palette.go:148`）；实测：设置行说明 4.76:1（`#F1F3F5`）、侧栏工作区 5.06:1、会话“已完成”约 4.83:1、Trajectory 用量约 4.92:1；`TestPaletteContrast` 已覆盖四组表面 + `Text > Secondary > Tertiary` 层级（`brand_test.go:140-186`）；深色不变（5.64–7.43:1） |
| **P1-3** | 深色字标浅色位图/白边/2x 模糊 | ✅ **已修复** | 新增 `mark-dark@2x.png`（96×96，`logo-dark.svg` 派生）；`brandMark(t)` 按主题选择（`assets.go:50`，`home.go:125` hero、`chrome.go:85/150` Sidebar/rail）；assets 测试断言深色外圈 3px 无近白、浅色保留白容器、深色渲染外圈 ≤80/255（`assets_test.go:40/69/120`）。实测深色预览四角白边消失；`mark-dark@2x` 外圈近白像素 **0/1116** |
| **P1-4** | Settings 仍是应用内模态 + 双关闭 | ◐ **部分修复** | **独立非模态窗口已交付**：ADR 0005、`internal/app/settings_window.go`（单例/close-to-hide/状态同步/回退）、8 个测试、预览 `settings-window-native-{light,dark}.png`（900×720）；**但双关闭仍在**：`settings_ui.go:417/419` 的“关闭”文字按钮 + “×”在 overlay 与窗口两种宿主都渲染，独立窗口还会叠加系统标题栏关闭 → 最多 3 个关闭入口 |
| **P1-5** | Automation 中英混排 | ✅ **已修复（局部）** | `automationScheduleLabel`（automation_ui.go:282-315）+ i18n key（i18n.go:59/68）输出“每天 00:00 / 每小时 / 每 15 分钟”；`TestAutomationScheduleLabelLocalized`（automation_ui_test.go:352）通过；预览确认中文。**残留**：next/last 时间戳仍为 `2006-01-02 15:04` 数字格式（`native-ui.md` §19.2 已记录，但未进 known-limitations） |
| **P1-6** | TextScale 覆盖不全 | ❌ **未修 / 已记录** | 仍只缩放主题基座字号 + 会话正文（`preferences.go:90-100`），固定 type scale（约 115 处 `design.Type`）不缩放；`known-limitations.md` §2 明确记录为已知限制；本轮无代码变化 |
| **P1-7** | 预览/CI 视觉回归不足 | ◐ **部分修复** | 预览 18→**22**（新增 toolcard / settings-window），README 已更新；**但仍无状态矩阵**（确认弹层、空态/错误/窄窗/HC/TextScale/focus 未纳入预览）且 **CI 仍无 golden/视觉回归**（`known-limitations.md` §4、`scripts/ci.sh` 无 preview 步骤），新确认弹层完全没有预览 |
| **P1-8** | 视觉层级/Web 感 | ❌ **未修（可延后）** | Home 仍是居中 hero + 720 composer 的 landing 构图（`home-native-*.png`）；Plugins/Automation 仍是侧栏“新建会话”+ 页头“添加插件/新建”两个实心蓝主按钮；Session 用户气泡 `#E6ECFE` 与工具详情 tint 仍接近。属设计主张/打磨，建议 v1.1 |
| **P2-9** | 细节一致性包 | ◐ **部分修复** | 文档新增 §17–20、更新 review 状态；但卡片圆角仍两套（列表卡 16 / 面板卡 10，`components.go:27-28`、`plugins.go:240` vs `settings_ui.go:360`）、Session 下划线 tab vs 右栏 pill tab（`trajectory.go:86` vs `right_sidebar.go:265`）、`plugins.go:34-35` 两处采样色仍在、`native-ui.md` 仍缺 §5/§6 且 §13 子小节仍编号 12.x、README:50 的“下一阶段”仍列已完成的 P0 项 |
| **P2-10** | 浅色焦点环/轻边框对比 | ❌ **未修 / 未记录** | 浅色 Focus 仍为 accent 55% 透明（`palette.go:152`），合成约 2.73:1（<3:1）；轻边框约 1.24:1；未进 known-limitations。静态预览不可见焦点，标为风险 |

**统计**：✅ 已修复 4 项（P0-1/2、P1-3/5）；◐ 部分 3 项（P1-4/7、P2-9）；❌ 未修/已记录 3 项（P1-6/8、P2-10）。P0 全部通过。

---

## 3. 新发现的问题（修复引入 / 新预览暴露）

> 未发现 P0 级新问题。以下两项需要本轮或收口前处理。

### P1-新-1｜工具卡错误面板浅色对比不足 4.28:1（新预览暴露）

- **现象**：toolcard 预览中的错误面板在浅色下为 `#DC2626` 文字（标题“错误” 11px、正文 mono 12px、“复制错误” 11px）叠在 `#FCEEEE`（Danger 8% over Raised）上，实测 **4.28:1 < 4.5:1**（WCAG AA 正常文本不通过）；深色为 `#F87171` on `#2C222C` = **5.53:1** 通过。
- **证据**：`docs/design/preview/toolcard-native-light.png`（错误面板）；`internal/shell/toolcards.go:335-370`（panel `Background(t.Danger.Alpha(0.08).Over(t.BgRaised))`、标题/正文/Copy 全部 `TextColor(t.Danger)`）；取色计算 4.28:1。
- **性质**：并非本轮 P0 修复引入（toolcard 是 round 3 新增），但 **round3 评审时没有 toolcard 预览，所以是"预览补全后暴露"**；`brand_test` 目前没有"danger 文字 on danger tint"的断言（WarningText 有，DangerText 没有）。
- **建议**：新增 `DangerText`（浅色建议 `#B91C1C`：在 `#FCEEEE` 上 5.73:1、白底 6.47:1）或把错误面板正文改为 LabelPrimary/加深底色；给 `TestPaletteContrast` 增加 danger-on-tint 与 success/warning-on-tint 配对，防止其他 tint 面板复现。

### P1-新-2｜Settings 关闭入口 3 重（carried，且窗口化后更明显）

- **现象**：overlay 与独立窗口都渲染“关闭”文字按钮 + “×”（`settings_ui.go:417/419`，`settingsHeaderLive` 不区分宿主）；独立窗口还叠加系统标题栏关闭 → 用户面对 3 个关闭入口。
- **建议**：宿主化收敛——窗口宿主只保留系统标题栏关闭（或只保留一个 ×），overlay 只保留 ×；若保留文字“关闭”，则删掉 ×。`SettingsView` 里已有 `hasSettingsWindowHost()`（settings_ui.go:299），可复用来隐藏。

### P2-新-3｜新确认弹层没有预览（状态覆盖缺口）

- **现象**：P0-1 新增的 `automationDeleteDialog` / `memoryClearDialog` 是新的关键交互状态，但 22 张预览里没有它们；确认弹层的视觉（Danger 按钮对比、文案、宽度）只能靠测试与代码判断。
- **建议**：加 2 张浅色（或 2×2 浅/深）确认弹层预览，纳入 `TestRender*NativePreview`；同时把“确认弹层/空态/错误/窄窗/HC/TextScale/focus”写进 preview README 的覆盖矩阵。

### P2-新-4｜README/known-limitations 与现状有漂移

- `README.md:50` 的“下一阶段”仍写“按 review-round3 Top10 修…（破坏性操作确认、三级文字对比度、深色字标位图等）”——这三项已经是 HEAD 完成项，属过期陈述。
- `known-limitations.md` 缺：toolcard 错误面板对比（修完前）、next/last 时间戳本地化、默认浅色焦点环/Focus 对比、Settings 双关闭、确认弹层预览覆盖；“工具卡内容已可选中”表述偏宽（错误面板正文仍是普通 `ui.Text`，未接 markdown 选择）。
- 建议随本轮修复合入一并更新，避免发布口径自相矛盾。

### P3 观察（不阻塞）

- **三级/二级文字层级压缩**：Secondary 6.04:1 vs Tertiary 5.29:1（RGB 每通道差 7–9），层级仍有序（brand test 断言），视觉上可区分但已接近；建议后续用字号/字重区分，不要再继续加深三级色。
- **深色字标是"黑容器 + 品牌图形"**：叠在深色侧栏 `#13131C` 上是一个略深的圆角方块（`preview/sidebar-native-dark.png` 可辨），叠在页面 `#0D0D14` 上几乎隐形；这是 `logo-dark.svg` 的设计身份，可接受，记录为有意行为即可。
- **工具卡 read_image 卡重复显示尺寸**（“48×48”出现在标题行和 payload 行），属信息密度小噪声。
- **设置独立窗口内容标题“设置”会与 OS 窗口标题重复**，常见桌面模式，可接受；预览无标题栏，真机再确认。

---

## 4. v1 签收建议

### 4.1 建议：**有条件可签收**（"自有品牌视觉 + 桌面交互本地化" scope）

P0 修复与 HEAD 全绿已复验；结构/品牌/可读性/桌面感主线达标。但作为"设计签收"，以下 3 项应当在签收记录前完成（工作量都很小）：

| # | 签收前必须完成 | 理由 |
| --- | --- | --- |
| 1 | 修工具卡错误面板对比（`DangerText` 或改底色），并加 danger-on-tint 对比测试 | 唯一的 P1 可读性问题，错误信息是重要内容；修复 <1 小时 |
| 2 | Settings 关闭入口收敛为 1 个（或明确写入 known-limitations 作为 v1 取舍） | 3 重关闭是当前桌面感最明显的"没收干净" |
| 3 | 更新 README 下一阶段、known-limitations（补 §3 新 P2-4 的缺口） | 发布口径不能把已完成项写成待办 |

**可延后（建议写入 known-limitations/v1.1 计划，不阻塞签收）**：TextScale 全量覆盖（P1-6）、预览状态矩阵 + CI golden（P1-7）、Home 工作台化/单主按钮/卡片与 tab 语法（P1-8、P2-9）、浅色焦点环与轻边框（P2-10）、Trajectory 虚拟化、通知动作/后台审批、滚动条常显。

**发布门禁（不是设计签收门禁，但发布前必须）**：真机 IME、VoiceOver/Narrator/Orca、1.25×/1.5×/2× DPI、系统通知权限与外观、Windows/Linux 设置窗口菜单栏——`known-limitations.md` §1 已列为未验证，发布记录中应显式接受。

### 4.2 与 `docs/research/06-acceptance.md` must-complete 对照

| 06 报告收口项 | 本轮状态 |
| --- | --- |
| 最终 HEAD 干净 + gofmt/vet/build/ci --fast/go test 全绿 | ✅ 本轮复验 HEAD 68bc842 全绿、工作树干净（ci.sh --fast 由 Lead 复跑） |
| 最终 HEAD 预览/测试卫生（22 张、预览不写工作树） | ✅ 22 张跟踪、测试在 `/tmp` 副本运行后仓库无写入 |
| 合并 task-25 UX 评审 + task-26 审计 | ◐ task-25 已合入（round3）并由本报告（round4）复核；task-26 审计结论仍基于 b3ff65f，建议在 68bc842 上刷新 |
| 残余 DSH 清理：plugins.go:34-35、注释 | ◐ 采样色 2 处仍在（`#6E7278/#C2C6CA`）；生产注释 DSH/baseline 提及仍 66 处（多为说明性），建议排期清理 |
| 真机缺口显式接受（写入 known limitations） | ✅ `known-limitations.md` 已建立，覆盖 IME/a11y/DPI/通知/窗口等；本报告 §3 补充其缺口 |

---

## 5. 最值得做的 3 件事（若继续投入设计，而不是继续加功能）

1. **把"危险/错误态在 tint 上的对比"系统化**：修 toolcard 错误面板 4.28:1，抽 `DangerText`/`SuccessText`/`WarningText` 语义色，给所有 tint 面板加对比测试；这是当前唯一能"被用户看见"的可读性缺陷，也是最容易复发的模式。
2. **把桌面窗口/入口收干净**：Settings 双关闭收敛、窗口宿主与 overlay 行为对齐、确认弹层与设置窗口补预览；再排真机矩阵。桌面感来自这些"不别扭"的细节，不来自再改颜色。
3. **让 Home/列表页有主张**：Home 加"最近会话/继续上次/快捷入口"密度块或收窄 hero；列表页只留一个实心主按钮；统一卡片与 tab 语法。现在"能用但不出彩"的核心就在这里——不是缺视觉特效，而是缺少属于 ZeoKeel 的工作台结构。

---

## 6. 附录 A：22 张预览复核记录（逐组）

| 组 | 浅/深观察 | 结论 |
| --- | --- | --- |
| home | 浅色 hero/composer 正常；**深色 hero + 侧栏字标白边消失**，黑容器与页面融合 | P0-3 通过 |
| session | 工具详情内嵌代码块（bash/text + Copy）层级清楚；`已完成`/时间戳明显更可读 | 可读性通过；tint 层级仍偏近 |
| sidebar | 工作区标签/`刚刚` 可读；行内动作常显；深色字标无白边 | 通过 |
| settings（overlay） | 行说明 4.76:1；主题卡/控件正常；仍 modal+scrim；头部“关闭”+× | P1-4 部分 |
| settings-window（新） | 900×720 非模态内容；nav/页面与 overlay 同源一致；头部仍“关闭”+×；无标题栏（真机再验） | 窗口化成立，关闭待收敛 |
| plugins | 三级说明更可读；双主按钮仍在（侧栏+页头） | P1-8 未修（可延后） |
| automation | “每天 00:00 / 每小时 / 每 15 分钟”本地化；删除变 Danger 红；四级说明可读 | P1-5 通过；确认弹层未预览 |
| trajectory | 时间戳/用量更可读；事件色点/下划线 tab 不变 | 通过 |
| right-sidebar（Start） | 卡片/图标盘一致；说明文字更可读 | 通过 |
| right-sidebar-files | 文件行/面包屑正常；三级色正常 | 通过 |
| toolcard（新） | 工具卡 markdown/copy/图片/错误面板一致性好；**错误面板浅色 4.28:1** | 新 P1（§3） |

## 7. 附录 B：证据索引与复验命令

| 结论 | 证据 |
| --- | --- |
| HEAD 全绿 | `/tmp/repo-head4`：`gofmt` 空、`go vet` 0、`go test -count=1 ./...` 0（shell 47.102s / session 32.907s；17 包） |
| 22 张预览 | `git ls-files docs/design/preview` = 23（README+22 PNG）；`ls docs/design/preview/*.png | wc -l` = 22；settings-window 为 900×720，其余 1600×1000 |
| 三级文字修复 | `internal/brand/palette.go:148`；实测：settings 4.76、sidebar 5.06、session 4.83、trajectory 4.92；`brand_test.go:140-186`（四表面 + 层级） |
| 深色字标修复 | `mark-dark@2x.png`（96×96）；`assets.go:50` `brandMark`；`home.go:125`、`chrome.go:85/150`；`assets_test.go:40/69/120`；实测资产外圈近白 0/1116，深色预览无白角 |
| 破坏性确认 | `automation_ui.go:481/490/503/513`；`memory_ui.go:179/187`；测试 `automation_ui_test.go:246`、`memory_ui_test.go:143`；预览删除红色 |
| 计划文案本地化 | `automation_ui.go:282-315`；`i18n.go:59/68`；测试 `automation_ui_test.go:352`；预览“每天 00:00/每小时/每 15 分钟” |
| 设置独立窗口 | `docs/decisions/0005-settings-window.md`；`internal/app/settings_window.go`；8 个测试；`settings_ui.go:264 SettingsView`；预览 2 张 |
| 双关闭未收敛 | `settings_ui.go:417/419`（header 同时“关闭”+×，未按 host 区分） |
| 工具卡错误面板 4.28:1 | `toolcards.go:335-370`（Danger 8% 背景 + Danger 文字）；预览 `toolcard-native-light.png`；计算 4.28:1（`#DC2626` on `#FCEEEE`） |
| 未修项证据 | `preferences.go:90-100`（TextScale 局部）；`palette.go:152`（Focus 55% → 2.73:1）；`components.go:27-28`/`plugins.go:240` vs `settings_ui.go:360`（圆角两套）；`trajectory.go:86` vs `right_sidebar.go:265`（tab 两套）；`plugins.go:34-35`（采样色）；`README.md:50`（过期下一阶段）；`native-ui.md` §5/§6 缺失、§13 子节编号 12.x |
| CI 无视觉回归 | `scripts/ci.sh` 无 preview/golden 步骤；`known-limitations.md` §4 自述 |

## 8. 限制与证据不足

- 静态帧看不到焦点/hover/动效/窗口标题栏/系统菜单/通知外观；焦点环对比（2.73:1）是代码合成推断，不是实测。
- 未真机：IME、VoiceOver/Narrator/Orca、1.25×/1.5×/2× DPI、通知权限/外观、Windows/Linux 设置窗口菜单栏 —— 均按 `known-limitations.md` 视为证据不足。
- 确认弹层、设置窗口标题栏、空态/错误态未进入预览；本报告对它们的视觉判断基于代码/测试，标为"未验证"。
- 本报告是专家评审，无用户数据；"出不出彩"建议按 `docs/research/03-ux-design.md` §8 的 5–8 人形成性测试再校准。

---

## 附录：收口前 P1 修复（2026-11-05，第五轮）

| 问题 | 状态 | 证据 |
| --- | --- | --- |
| 新 P1：toolcard 错误面板浅色 4.28:1（#DC2626 on #FCEEEE） | **已修复** | 品牌新增 `DangerText`：浅色 `#B91C1C`（danger 8% tint 上 5.7:1）、深色 `#FCA5A5`；toolcard 错误文字/复制/图标改用 `DangerText`，tint/border 仍用 `Danger`；`TestPaletteContrast` 新增 danger-on-tint 配对断言；预览重生成 |
| P2：Settings 窗口化后 "关闭" + × + 系统标题栏 3 重关闭 | **已修复** | 独立窗口模式隐藏标题栏文字 "关闭"，只保留面板 ×（Esc 仍可关闭）；overlay 回退保留原双入口；设置测试通过 |
| README 下一阶段仍列已完成 P0、known-limitations 缺口 | **已修复** | README 阶段 6 更新、known-limitations 增补；本报告附录记录 |
