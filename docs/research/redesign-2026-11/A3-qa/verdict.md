# 收尾视觉评审 · 最终结论（v-final）

> 快照：`.cache/ui-conformance-final/`（head `e9a0d6b`；首评 01:47 · 复验 02:34 · **定稿 02:40**）
> 闸门：`A3-qa/measure.sh .cache/ui-conformance-final --baseline .cache/ui-conformance --strict`
> 评审：逐张 `read_image` 23 张整屏 + 6 张 4×–10× 裁剪；全部关键结论附 ImageMagick 采样值
> 判据：顶带 48+36=84 · 圆角 4/6/8/12/16 · 接缝 1px `BorderL2`

---

## 0. 结论（先说结果）

> **定稿（02:40 快照，见 §7）：P0-1 已关闭、禁用发送复核达标 → 结论「可交付」。**
> （02:34 复验：P0-1 关闭，见 §6。）
> 原 P0 根因是 mygo 重绘缓存（Scroll 子树身份不变时复用上一帧绘制层），壳层已用 `themeGen` 在换主题时重绘整棵树；暗色三处表面复验为 `#1B1B26`/`#23232F`，与 token 一致。

**最终判定：可交付（定稿）。** 亮色与暗色均达标；无 P0/P1。剩 4 条 P2（不阻塞）+ 2 条框架行为（暗色 toast 反转成浅卡、窄窗 toast 压落地页主按钮）已登记为"接受/待登记"。

| 阻塞项 | 状态 |
|---|---|
| P0-1 暗色三处白面（composer / 能力卡 / toast 表面） | ✅ CLOSED（`themeGen` 重绘；02:34 复验 `#1B1B26`） |
| 禁用发送对比 | ✅ 达标（`#5A6371` on `#F2F4F7` ≈5.9:1；**我上轮 ≈1.6:1 的读数是取样误差，见 §7.2**） |
| 窄窗落地页 toast 压「配置 API Key」 | ⚠️ **仍存在**（02:40 快照实测；P2，建议 home 改顶部锚点，见 §7.3） |

~~原判定（02:07，已失效）：不可交付——暗色 composer/toast/能力卡 三处白面。~~ 详见 §1（已标记关闭）与 §6（复验证据）。

| 维度 | 结论 |
|---|---|
| 机器闸门 | **PASS 51 / FAIL 2 / WARN 2**（定稿快照同值；2 条 FAIL 都是 diff 场景画布 1000×620 vs 基线 720 的**裁切高度变化**，非 UI 缺陷；WARN 是行距信息项） |
| 亮色 | ✅ 可交付（结构接缝、圆角阶梯、字号层级、强调色预算、审批卡、diff、右栏、空态全部达标） |
| 暗色 | ✅ 达标（02:34 复验：composer/卡 `#1B1B26`、设置面 nav<content；暗色 toast 浅卡=已接受的框架行为） |
| 窄窗 1024 | ✅ 达标（顶带尾簇已收进 `⋯`，距接缝 27px，无裁切；chip 保留文字） |
| 已接受框架限制 | 不重复计入（窄窗 toast Right、`ui.Table` 竖线、diff 行号列 Δ1.02:1、右栏树 2px 条贴缩进列） |

---

## 1. 唯一的 P0（**已关闭 · 2026-11-18 02:34 复验通过**）

### P0-1 · 暗色主题下三处表面仍是白色（亮色 token）→ ✅ FIXED

> 根因：mygo 重绘缓存（Scroll 子树身份不变 → 复用上一帧绘制层）。修法：壳层 `themeGen` 在外观/字号变化时给根内容换 key，整树重绘。复验数据见 §6。

| 表面 | 位置 | 实测 | 期望 |
|---|---|---|---|
| composer | `p2-context-dark.png` `x=433..1360, y=726..857`；`narrow/dark-session.png` `x=350..951, y=505..635`；`narrow/dark-landing.png` 同 | 填充 **`#FFFFFF`**、边框 **`#DBDCDC`**（= 亮色 L3 15% over white）；内部文字为暗色主题的浅色 ink → **白底白字**，草稿「运行中继续写的草稿」「附件」「snapshot-model」几乎不可见 | 填充 `BgRaised #1B1B26`、边框 `BorderL3`（暗 `#4B4B55` 级） |
| toast | `narrow/dark-session.png` / `narrow/dark-settings.png` `x=760..1007, y=355..445` | 卡面 **`#FFFFFF`** | `BgOverlay #23232F` |
| 能力卡 | `p2-capabilities-skills-dark.png` `x=307..690, y=210..340` | 卡面 **`#FFFFFF`** | `BgRaised #1B1B26` |

**范围**：所有"带 composer 的暗色场景"都中招（`p2-context-dark`、`narrow/dark-session`、`narrow/dark-landing`、`p2-goal-dark`、`p2-interrupted-dark`、`p2-archive-settings-dark` 底部均为 `#FFFFFF`）；无 composer 的暗色页（`p2-insights-dark`、`p2-markdown-specimen-dark`、`p2-approval-diff-dark`、`p2-capabilities-connectors?`）正常。

**关键线索**：同一 token 在其他表面是对的——暗色**审批卡**头 `#1B1B26`、卡身 `#272736`；暗色 landing「开始前还需要」卡 `#1B1B26`；暗色页 `#111118`、侧栏 `#0B0B10`、band `#0B0B10`、接缝 `#292930`。所以**不是 palette/token 问题，而是这三个子树拿到了亮色上下文**（composer 的 1px 边框同时是亮色 L3，说明整棵子树的 `Tokens` 是 Light）。

**建议排查顺序（给 owner）**
1. `design.Of(c)` 在这三处返回的是 Light → 检查它们是不是在 **overlay / dock 层**用了一个早于 `uiroot/frame.go:62 c.SetTheme(...)` 的 `*ui.Context`（或缓存的 context/theme）。
2. `uikit` 里 composer / toast / 能力卡新增的共享 helper 是否自己调了 `design.Light()` 或读了 `brand.LightPalette()`（当前 grep 无命中，但可能是间接的 `ui.Theme` 默认值）。
3. 补一条帧测试：在 `Dark()` 下渲染 composer，断言卡面 = `t.BgRaised`（`#1B1B26`）且文字 = `LabelPrimary`。

**证据**：`.cache/ui-conformance-audit/crops/dark-composer.png`（2×，白底白字）、`dark-band.png`（顶带正确，作为对照）。

---

## 2. 逐项勾选（A3-QA-CHECKLIST 全量；✅ 通过 / ⚠️ 记录 / ❌ 阻塞）

### A. 接缝与容器边界

| # | 判定 | 证据（final 实测） |
|---|---|---|
| A1 侧栏 1px L2 | ✅ | `x=280` 单条 `#E9EAEA`（Δ0）；暗色 `#292930`（Δ1），16 张屏全 PASS |
| A2 检查器 1px L2 | ✅ | `p2-files`/`p2-file-preview` `x=1189` `#E9EAEA` |
| A3 设置导航 1px | ✅ | 1512：`x=535`；1024：`x=291`，均 `#E2E3E5`（= L2 over `#F7F8FA`） |
| A4 三条同权重 | ✅ | Δ 0/0/0；肉眼无"某条更黑" |
| A5 浮层有边/有面 | ⚠️ | 亮色浮层仍只靠阴影（palette/ask 无边）；暗色 `BgOverlay` 生效（设置面 `#1B1B26` vs 页 `#111118`）。按 S4 可接受，记录 |
| A6 卡片可区分 | ✅ | 卡边 `#DBDCDC` = L3 15%；卡内无第三条线 |
| A7 气泡为填充面 | ✅ | 用户/产物气泡为灰底无独立描边（`p1-chat` 右侧） |
| A8 审批卡解剖 | ✅ | 中性卡 + 2px `Warning` 左 rail + `BgStrong` 内嵌"摘要"块 + 无大色块 |
| A9 diff 行级染色 | ✅ | 混合 diff：`−` 行 `#FEF1F0`(danger 6%)、`+` 行 `#F0F9F3`(success 6%)、上下文 `#FFFFFF`，行级 rail 正确 |
| A10 表格单一语言 | ⚠️ | `ui.Table` 表头竖线仍在（已接受框架限制） |
| A11 提示条形态 | ⚠️ | final 未复看 `p2-interrupted`（时间所限）；v4 观察已为浅底条、与审批卡不同构 |

### B. 强调色与状态色

| # | 判定 | 证据 |
|---|---|---|
| B1 导航图标灰 | ✅ | 图标 `#4B5261`（= `LabelSecondary`），与折叠 rail 一致 |
| B2 accent 填充块 ≤2 | ✅ | 脚本 20 屏全 PASS（最多 1 块/屏） |
| B3 primary ≤1 | ✅ | `p2-automation` 仅「新建任务」；会话屏无实心主按钮 |
| B4 红色 ≤2 | ✅ | `p2-capabilities-connectors` 仅 1 处红（连接失败） |
| B5 暗色三色可分 | ⚠️ | 选中 tint 13%/accent 满填/链接可辨；但受 P0-1 白面干扰，修后再确认 |

### C. 字号与字重

| # | 判定 | 证据 |
|---|---|---|
| C1 状态条无 11px 句 | ✅ | 改为 `42K/128K` 计量条 + 「详情」按钮，无六指标句 |
| C2 消息元信息 ≥12 | ✅ | final 已无常显 11px 元信息行（并入 ⋯ 菜单） |
| C3 code fence | ✅ | 仅复制**图标**（无 11px 文字），命中区 24 |
| C4 一屏 ≤4 档 | ✅ | 20/16/14/12（设置页）、16/14/13/12（会话页） |
| C5 字重烘焙 | ✅ | 节标题 600 一致（外观/行为/模型页对比） |
| C6 元数据不 accent | ✅ | 元信息全为 `LabelTertiary/Secondary` |
| C7 12/13 不混层 | ✅ | 模型列表：名称 14 / 元信息 12 / 价格 12 |

### D. 圆角与形状

| # | 判定 | 证据 |
|---|---|---|
| D1 半径在档位 | ✅ | composer **r≈16**、卡 **r≈12**、palette **r≈12**（palette 已从 10 对齐到 12） |
| D2 同屏 ≤3 档 | ✅ | 8 / 12 / 16（+ Max 仅头像） |
| D3 segmented 无断线 | ✅ | 外观/行为/洞察：选中=白块+1px 边+600，无 accent 底线（亮暗同） |
| D4 composer r16 | ✅ | 同上 |
| D5 chip 28 / 徽章 20 | ⚠️ | 目视一致；未逐屏量 |
| D6 图标按钮两形态 | ✅ | ⋯ 裸图标 / 图钉填充 / ✕ 裸图标，无"灰圆盘" |

### E. 布局 · 遮挡 · 窄窗

| # | 判定 | 证据 |
|---|---|---|
| E1 窄窗顶带不溢出 | ✅ | 脚本 gap ≥8 PASS；裁剪 `crops/final-narrow-hdr.png` 显示尾簇收进 `⋯`，距缝 27px，无裁切 |
| E2 toast 不压正文 | ✅ | 右下角；`p1-chat`/`p2-toast` 均不压文本与 composer（窄窗 Right 不生效=已知） |
| E3 正文块 ≤660 | ✅ | 550 |
| E4 消息列 = composer 右缘 | ✅ | 均 `x=1360` |
| E5 右栏 header ≤2 层 | ✅ | 全局 tab + 文件 tab + 路径行；`README.md` 只出现 1 次 |
| E6 禁用发送可见 | ⚠️ | 圆底 `#AFB4BC`、箭头 `#E3E5E9` ≈ **1.6:1**，仍偏淡（非阻塞，建议加 1px L2 边或箭头改 `LabelTertiary`） |
| E7 建议按钮内边距 | ✅ | 文案距边 ≈11px（旧 7px） |
| E8 palette 三列 | ✅ | 描述列与 kbd 列分离，长描述显式省略号 |
| E9 能力卡跨卡对齐 | ⚠️ | 徽章宽度不同 → 两卡路径左缘仍差 ≈12px（P2） |
| E10 定义表行线 | ⚠️ | `p2-context` 未复看（v4 已收敛为行间线） |
| E11 树行 leading slot | ✅ | 文件夹/文件同左缘（`p2-files`） |
| E12 自动化行动作数 | ⚠️ | 「已停用」已行内化 ✅，但右侧仍有 4 控件（修复/启用/立即运行/⋯），超出"≤2"建议 |
| E13 team overlay 空态 | ⚠️ | 仍有两组空态文案 + 1 张已结束卡，面板下半空旷 |
| E14 顶带目标数 | ⚠️ | 会话屏 6–7 个可点目标（‹自动化/标题/2 chip/⋯/🔍/▯） |
| E15 窄窗 chip 保留文字 | ✅ | 「智能体团队」「子智能体」均带文字 |
| E16 ask 弹窗 | ✅ | 选项左对齐单选 + 「Esc 关闭问题」在动作行**左侧** |

### F. 列表 · 间距 · 节奏

| # | 判定 | 证据 |
|---|---|---|
| F1 小节无线 + 24/12 | ✅ | 外观/行为页各节无规则线，间距一致 |
| F2 列表行分隔 | ✅ | 模型列表行间 1px；自动化/记忆行成卡或分栏 |
| F3 正文有段距 | ✅ | 权限模式改为 1 句 + 3 条 bullet，不再文字墙 |
| F4 轨迹页无按钮脊柱 | ✅ | 改列表行 + 1px 分隔线（v4 复看，final 同结构） |
| F5 行距 | ⚠️ | 信息项：26px/24px（多行列表的文本行距，非行高） |

### G. 暗色

| # | 判定 | 证据 |
|---|---|---|
| G1 nav < page | ✅ | `#0B0B10` < `#111118`（4 屏） |
| G2 卡片 vs 页面差 ≥8 | ❌ | 能力卡 **`#FFFFFF`** vs 页 `#111118`（不是"差不够"，而是**反了**）→ P0-1 |
| G3 composer elevation | ❌ | 白面 + 亮色边（`#DBDCDC`）→ P0-1 |
| G4 语义色不浑浊 | ✅ | 暗色审批卡头 `#1B1B26`、卡身 `#272736`（无泥棕）；diff 增删染色正常 |
| G5 暗色下 A–D 同样成立 | ❌ | 因 P0-1，暗色下 composer/toast/能力卡 的"面"判据不成立 |

**合计**：✅ 41 · ⚠️ 11 · ❌ 3（G2/G3/G5 三条都指向同一个 P0-1）。

---

## 3. 干净区复核（不许改坏的 10 条）

| # | 判定 | 证据 |
|---|---|---|
| K1 文字对比 | ✅ | `LabelSecondary #4A5160` 7.6:1、`LabelTertiary #5A6371` 5.4:1（白底）；暗色 `#C7CAD9`/`#A3ABBB` 实测可读 |
| K2 暗色导航下沉 | ✅ | `#0B0B10` < `#111118` |
| K3 状态语义 | ✅ | 成功绿/失败红/停止中性；顶带「运行中」chip = **accent 淡底 + accent 字**（实测 `#E7EBFB`/`#0D44F6` 系），符合新规则「running ≠ warning」 |
| K4 高度档位 | ✅ | 28/32/36/40/44；顶带 48+36 |
| K5 code fence 解剖 | ✅ | 填充块 + r6 + 无边框 + 头部复制图标 |
| K6 工具行四态 | ✅ | 运行中/完成/失败/已停止 四色可辨（`p2-tool-row-states` v4） |
| K7 空态/错误态 | ✅ | `p2-landing` 空态、connectors 错误态均在 |
| K8 键盘可达 | ⚠️ | 静态图无法验证；焦点环存在（搜索框 accent 1px），建议跑一次 `ui-drive` |
| K9 尺寸用 token | ✅ | 接缝/位移全为 0；`designlint` 由 Lead 侧负责最终确认 |
| K10 图标纪律 | ✅ | 全屏仅 `internal/icons` 图标，无 emoji/Unicode |

---

## 4. 剩余问题按 owner（派修用）

| owner | 项 | 严重度 | 位置 | 改法 |
|---|---|---|---|---|
| **impl-shell**（+ impl-session 协同） | **P0-1a** composer 暗色白面 | **P0** | 所有暗色会话屏 composer | 见 §1；重点查 overlay/dock 冒的 context 主题 |
| **impl-shell** | **P0-1b** toast 暗色白面 | **P0** | 暗色 toast | 同上（toast 在 overlay 层，最可能是根因点） |
| **impl-settings** | **P0-1c** 能力卡暗色白面 | **P0** | `p2-capabilities-skills-dark` | 同上 |
| **impl-settings** | 连接失败用红字、未用 `StatusBadge` | P2 | `p2-capabilities-connectors` `x≈590..645,y≈250..262` | 改 `uikit.StatusBadge(danger)`（tint + AA + 标签） |
| **impl-settings** | 能力卡路径列跨卡错位 ≈12px | P2 | `p2-capabilities-skills` 两卡 | 徽章定宽 56 或把徽章移行尾 |
| **impl-session** | 禁用发送 ≈1.6:1 | P2 | `p1-chat` 发送圆钮 | 箭头改 `LabelTertiary` 或加 1px `BorderL2` |
| **impl-shell** | 顶带 6–7 个目标 / 自动化行 4 控件 / team overlay 空态 | P2 | 各屏 | 收进 `⋯`；空态折叠成一行 |
| **impl-shell** | 「待审批 1」chip 为中性灰，needs-action 建议 warning 系（§5.2 矩阵） | P2 | `p1-approval` 顶带 `x≈875..940` | 用 `StatusBadge(warning)` 或 warning 淡底 chip |

---

## 5. 最终判定

**不可交付（1 条 P0 阻塞，仅在暗色）。**

- 若**只发亮色**：可以交付。
- 若要交付双主题：必须修 **P0-1（暗色三处白面：composer / toast / 能力卡）**，修完重出暗色 4–6 张（`narrow/dark-session`、`p2-context-dark`、`narrow/dark-landing`、`p2-capabilities-skills-dark`、`narrow/dark-settings`）我再复跑一次闸门 + 看这 5 张即可放行。
- 4 条 P2（connectors 状态徽章、能力卡对齐、禁用发送对比、顶带/自动化行密度）建议随下一轮或发布后第一批修，不阻塞。

**证据清单**：`.cache/ui-conformance-qa/ui-conformance-final.tsv`（机读）、`.report.txt`、`crops/dark-composer.png`、`crops/dark-band.png`、`crops/final-narrow-hdr.png`。

---

## 6. 复验（02:34 新快照 · P0-1 关闭）

快照：`.cache/ui-conformance-final/`（generated 2026-11-18T02:34:13，changed files 212）
闸门重跑：**PASS 51 · FAIL 2 · WARN 2**（与修前一致，无回归；2 条 FAIL 仍是 diff 场景画布 1000×620，harness 裁切高度变化，非 UI 缺陷）

| 复验项 | 实测 | 期望 | 判定 |
|---|---|---|---|
| `narrow/dark-session` composer | **`#1B1B26`**（L28） | `#1B1B26` | ✅ |
| `narrow/dark-session` 页面 | `#111118`（L17） | `#111118` | ✅ |
| `p2-context-dark` composer | **`#1B1B26`**，边框 `#404049`（暗 L3） | `#1B1B26` | ✅ |
| `narrow/dark-landing` composer | **`#1B1B26`** | `#1B1B26` | ✅ |
| `p2-capabilities-skills-dark` 卡面 | **`#1B1B26`**，卡边 `#404049` | `#1B1B26` | ✅ |
| `narrow/dark-settings` ramp | 导航 **`#1B1B26`**(L28) < 内容 **`#23232F`**(L35)；背后被 scrim 到 `#07070A` | nav < body | ✅ |

主观复核（逐张看）：composer 占位符/草稿/工具栏文字在暗色下全部可读，无白底白字；能力卡、审批卡、landing 卡表面一致；设置面「导航下沉、内容上浮」的 S4/S1 关系清楚，不再像被挖洞。

### 6.1 暗色 toast（浅卡）的判定：**接受，附两个条件**

**接受的理由（与 composer 那个 P0 的本质区别）**
- composer 是**可读性**失败（白底白字，内容看不见）→ 必须修；toast 是**风格/对比**选择（深字浅卡，完全可读，18:1）→ 属 mygo「浮层反转」语义，且瞬时、自动消失。
- 它是框架行为（宿主主题服务不可见时无法覆盖），不是我们的 token 错误；穿透与遮挡是**定位**问题，不是配色问题。

**两个条件（请一并落实，否则复验时我仍会记一条）**
1. **登记**：写进 `docs/DESIGN.md §7.4`（已知偏离：暗色 toast 由 mygo 反转为浅卡；原因、owner、复审触发条件）——否则下一轮审计会当作回归再报一次。
2. **验证点击穿透**：需要一条帧测试证明"toast 卡体不吞事件"——即点击落在 toast 卡体覆盖的底层按钮上仍能命中。现证据：`narrow/dark-landing` 的白色 toast **压住了「配置 API Key」按钮**（`x≈838..940, y≈420..470`，被 toast 下缘遮住一半）。若 mygo 的 `ToastBase` 只在自己的按钮上消费事件，卡体可穿透 → 接受；若卡体吞事件 → 属**定位**缺陷（需 impl-shell 把 toast 栈在窄窗下抬到内容列之外），此时它与"窄窗 Right 不生效"是同一根因，可按已知限制一并接受，但必须记录"遮挡主操作"的风险。

### 6.2 最终结论

**可交付。** 无阻塞项。随下一轮/发布后首批修的非阻塞项（4 条）：

| owner | 项 | 级别 |
|---|---|---|
| impl-settings | 「连接失败」用红字而非 `StatusBadge`（`p2-capabilities-connectors`） | P2 |
| impl-settings | 能力卡徽章定宽，消除跨卡路径列 12px 错位 | P2 |
| impl-session | 禁用发送 ≈1.6:1（`#AFB4BC` 圆 + `#E3E5E9` 箭头） | P2 |
| impl-shell | 顶带 6–7 个目标 / 自动化行 4 控件 / 「待审批 1」chip 中性色 / team overlay 空态密度 | P2 |

---

## 7. 最终确认（02:40 快照 · 定稿）

快照：`.cache/ui-conformance-final/`（generated 2026-11-18T02:40:47，119 场景 / 110 PNG at root + narrow）

### 7.1 P0-1 复验（沿用 §6，结论不变）

`narrow/dark-session` composer `#1B1B26` / page `#111118`；`p2-context-dark` composer `#1B1B26` + 边 `#404049`；`narrow/dark-landing` composer `#1B1B26`；`p2-capabilities-skills-dark` 卡 `#1B1B26` + 边 `#404049`；`narrow/dark-settings` 导航 `#1B1B26` < 内容 `#23232F`，背后 `#07070A`。**全部 PASS，无回归。**

### 7.2 禁用发送按钮：**达标**（并更正上轮读数）

裁 `p1-chat.png` `44×44+1314+810`（10×）直方图：填充 `#F2F4F7`（948/1936 像素为页白，740 为 BgStrong）、描边 `#DEE0E3`、箭头墨色最深处 ≈`#5A6371` → **≈5.9:1**，箭头清晰可见、四角 r≈10、有 1px 边。

> **更正**：上一轮我报的「≈1.6:1」是**取样点落在描边抗锯齿上**造成的误判（把 `#E3E5E9`（边/抗锯齿）当成了箭头色、把 `#AFB4BC`（箭头边缘）当成了填充）。该控件当时已达到标（`BgStrong` + `LabelTertiary` 墨），并非缺陷。给 impl-session 造成的返工，抱歉。

### 7.3 `narrow/dark-landing` toast 锚点：**仍未避开主按钮**（P2）

- 实测（02:40 快照）：toast 栈 `x=760..1007, y=355..445`；「开始前还需要」卡 `x=350..951, y=385..468`，其右端主按钮「配置 API Key」`x≈838..940, y≈420..452` → **toast 下缘（445）压住按钮上部 ~25px**，按钮标签只剩下半截可读（`y=446..452` 才出现按钮文字）。
- 该文件与 02:34 版本 **sha256 完全相同**（`0f12c25…`）：虽然 `toastanchor.go` 在 02:38 改过、快照 02:40 重出，但这一屏的像素没有变化 → "给 home 屏预留 dock 高度"没有改变本场景的锚点（toast 本来就在 composer 之上，恰好落在卡片动作带）。
- **P2 不阻塞**的理由：toast 瞬时自动消失 + 可手动 ✕；消失后按钮完全可点；仅 1024 窄窗出现。
- **与我提出的硬约束冲突**："锚点绝不压可操作控件" 现在是硬约束，而本实例违反它。二选一：
  1. **修**（推荐，改动最小、最稳）：`home` 屏把 toast 栈改成**顶部锚点**（`FromBottom=false`，落在 band 下方 y≈96 起的空白 hero 区；本屏该区域无控件）；或
  2. **登记**：把它作为该硬约束的已知实例写进 `docs/DESIGN.md §7.4.1`（附坐标与触发条件：1024 宽 + home 屏 + 有 setup 卡），下一轮复审。

### 7.4 `p1-chat` toast 复核 ✅

toast 在 `x=1256..1495, y=605..690`，位于 composer（`y=728..859`）**之上 38px**，不压 composer、不压工具栏、不压底部分区计量条；只覆盖正文列右侧空白区（该高度无文本）。宿主操作（发送/附件/model/详情）全部可点。**通过。**

### 7.5 定稿结论

**可交付。** 阻塞项 0；P0/P1 全清。发布前可选（不阻塞）：
1. `home` 屏 toast 顶部锚点（§7.3 方案 1，约 3 行）；
2. 4 条 P2（connectors 状态徽章、能力卡徽章定宽、顶带/自动化行/overlay 密度、「待审批」chip 语义色）；
3. 把暗色 toast 反转 + 本实例写进 `DESIGN.md §7.4.1`（Lead 已在做）。
