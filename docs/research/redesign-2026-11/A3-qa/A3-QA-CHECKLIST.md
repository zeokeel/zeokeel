# A3 收尾验收清单（视觉 QA 负责人版）

> 输入：`A3-visual-detail-audit.md`（46 条发现）+ 2026-11 新 token（圆角 4/6/8/12/16；边框 L1 6% / L2 9% / L3 15%，暗 7/10/16；hover 中性 4.5%；选中 9%/13%；字重烘焙；`ProseMeasure 640`；`BorderSubtle`/`BgOverlay`；动效 80/120/160/200ms）。
> 产出：本清单 = 收尾时逐条打勾的唯一依据。机器能判的交给 `A3-qa/measure.sh`，其余按「看图 + 像素采样」人判。
> **未改产品代码**；只写 `.cache/` 与 `docs/research/redesign-2026-11/A3-qa/`。

---

## 0. 怎么用（三步，30 分钟）

```bash
# ① 机器闸门（全绿才算过；FAIL 数量必须为 0）
docs/research/redesign-2026-11/A3-qa/measure.sh .cache/ui-conformance-v2 \
    --baseline .cache/ui-conformance --strict
#    → .cache/ui-conformance-qa/ui-conformance-v2.report.txt / .tsv

# ② 人看图（机器判不了的）——至少这 12 张，亮/暗/窄窗各覆盖
#   p1-chat p1-approval p1-trajectory p2-appearance p2-behavior p2-files
#   p2-file-preview p2-context p2-automation p2-model-settings p2-ask
#   narrow/narrow-session-inspector、narrow/dark-session、p2-context-dark
#   （用 read_image 逐张看；重点：边框线、圆角、字号、强调色、遮罩）

# ③ 逐条打勾下面的表：✅ 通过 / ⚠️ 记录（可接受）/ ❌ 阻塞（退回实现者）
```

**判定口径**
- **PASS**：判据全部满足，且脚本对应项 PASS。
- **WARN**：主观项可接受但需记录（例如无描边浮层按设计仅用阴影）；不阻塞，写进收尾报告。
- **FAIL**：任一硬指标不满足（线宽/字号/半径/对比/溢出/遮挡）→ 阻塞，退回对应实现者，重出快照后复跑。

**打勾前先确认三件事**：① 快照是同一 commit；② `manifest.txt` head 与代码一致；③ 画布仍是 1512×944 + 1024×720（变了就要先调 `measure.sh` 的探针坐标，用 `--list` 看当前表）。

---

## 1. 机器闸门（脚本必须全绿，`--strict` 退出码 0）

| 闸门 | 判据 | 阈值 | 脚本段 |
|---|---|---|---|
| G1 接缝线宽 | 侧栏／检查器／右栏／设置导航，每条都 1px | 宽=1；0px 与 ≥2px 都 FAIL | [2] |
| G2 接缝颜色 | = L2 合成（亮 ink9% / 暗 white10%） | Δ ≤ 8 | [2] |
| G3 accent 填充块 | ≥24×24 连通块 | ≤2/屏 | [3] |
| G4 卡片圆角 | 半径落在档位 | ∈{4,6,8,12,16}，±2 | [4] |
| G5 顶带高 | 72 | ±2 | [5] |
| G6 composer 框 | 高 90–170，左右边为内容列宽 | h∈[90,170]；w≈959±4 | [5] |
| G7 内容块宽 | ≤ TranscriptWidth | ≤968 | [5] |
| G8 正文块宽 | ≤ ProseMeasure+20 | ≤660 | [5] |
| G9 行距采样 | **信息项**（多行列表的"段"是文本行）| 只记录，不判 PASS/FAIL | [5] |
| G10 窄窗顶带留白 | 接缝左侧 40px 内无墨 | 最小亮度 ≥150 | [5] |
| G11 暗色 ramp | nav 亮度 < page | L(nav) < L(page) | [6] |
| G12 画布/场景 | 与基线一致 | 尺寸与文件清单 | [1][7] |

> **基线自检（重要）**：在**旧** `.cache/ui-conformance/` 上跑，脚本当前报 **14 项 FAIL**：11 × 侧栏 2px 双线、3 × 设置导航 0px、1 × 窄窗顶带溢出 —— 正是 A3 的 P0/P1 前三名。**新快照必须把这 14 项清零**；若某项仍红，就是该缺陷未修或修错。

---

## 2. 逐条验收（46 条 → 40 个验收项）

### A. 接缝与容器边界（A3：VD-03/04/06/07/08/09/13/36/41）

| # | 判据（可量化） | 看图（PNG · 区域） | 通过条件 | 关联 |
|---|---|---|---|---|
| A1 | 侧栏↔正文：**恰好 1 条 1px 线**，颜色 = L2 合成 | `p1-chat` x≈279；灯/暗各一 | 脚本 [2] sidebar=PASS（宽 1、Δ≤8） | VD-03 |
| A2 | 检查器↔正文：1px L2 | `p2-files`/`p2-file-preview` x≈W-323 | 脚本 [2] inspector=PASS | VD-04 |
| A3 | 设置导航↔正文：**存在** 1px L2（旧为 0px） | `p2-appearance`/`p2-model-settings` 导航右缘 | 脚本 [2] settings-nav=PASS；放大图能看到一条**同灰度**的细线 | VD-04 |
| A4 | 三条接缝**同权重**：任何一条都不比另一条重/深 | 三张图并排看 | 肉眼无"某条特别黑"；采样色差 ≤4 | VD-03/04 |
| A5 | 浮层有边或有面：`Border(1,L2)` 或 `BgOverlay ≠ BgRaised` | `p2-ask`、`p2-palette`、`p2-appearance` 边框 | 亮色二选一成立；**暗色必须成立**（`#23232F` vs `#1B1B26`） | VD-41 |
| A6 | 浅色下卡片与页面可区分：页面 `#FFF` 时卡片边必须是 **L3(15%)**，且卡内不得再叠 L1/L2 线 | `p1-trajectory` 卡、`p2-capabilities-skills` 卡 | 卡边采样 Δ≈36±6；卡内看不出第三条线 | VD-06 |
| A7 | 用户气泡是**填充面**（无 1px 描边） | `p2-status-strip` 气泡 x≈1103..1360 | 气泡边无独立描边（或描边=填充同色）；与输入框形态不同 | VD-07 |
| A8 | 审批卡 = 中性卡 + 语义 rail + StatusBadge；无 22% 大色块 | `p1-approval` 卡头 473..527 | 卡头与卡身同底；侧 rail ≤3px `Warning`；**禁止**整块琥珀/米色底 | VD-08 |
| A9 | diff 只给增删行染色，块底=页面底，行号与代码同底 | `p2-approval-diff` 95..265 | 未变更行为页面底色；无"整块绿洗底"；无白/绿相间条缝 | VD-09 |
| A10 | 表格只有一套语言：行线，无竖线、无全网格 | `p2-context`、`p2-about-settings`、`p2-markdown-specimen` | 三处均无表头竖线；Markdown 表格无外框+无网格 | VD-13 |
| A11 | 提示条（interrupted）与审批卡形态不同 | `p2-interrupted` 顶部 | 提示条无边框（浅底或左条），不与审批卡同构 | VD-36 |

### B. 强调色与状态色（VD-05/45 + 预算）

| # | 判据（可量化） | 看图 | 通过条件 | 关联 |
|---|---|---|---|---|
| B1 | 展开侧栏导航图标 = `LabelSecondary`（`#4A5160`），只有**当前目的地**可为 accent | `p1-chat` x=26..34,y=142..230；`p2-capabilities-skills` 同一位置 | 图标采样 ≈ `#4A5160`；被选中的那一个可 ≈`#0D44F6`；与折叠 rail 图标**同色** | VD-05 |
| B2 | accent 填充块 ≤2/屏 | 脚本列出的 20 屏 | 脚本 [3] 全 PASS | 规则 10 |
| B3 | primary 按钮 ≤1/屏 | 各屏 | 目视 1 个实心主按钮 | 规则 10 |
| B4 | 红色元素 ≤2/屏 | `p2-capabilities-connectors`、`p2-memory-settings` | 红字/红徽章/红边合计 ≤2 | 规则 10 |
| B5 | 暗色三色语义可分：选中 tint 13% / 主操作 accent 满填 / 链接有下划或弱化 | `narrow/dark-session` | 三者肉眼可分，不是同亮同色 | VD-45 |

### C. 字号与字重（VD-10/11/28/42/43/44）

| # | 判据（可量化） | 看图 | 通过条件 | 关联 |
|---|---|---|---|---|
| C1 | 底部状态条 ≥12px | `p1-chat` y≈898..906 | 与 12px 基准字高一致；不再是小一号 | VD-10 |
| C2 | 消息元信息（时间/用时/tokens）≥12px | `p1-chat`/`p2-status-strip` 元信息行 | 同上；分隔符是图形点而非 11px 文本 | VD-11 |
| C3 | code fence 头部与"复制"≥12px，复制命中区 ≥24×24 | `p1-chat` 代码块 347..407 | 复制是 24 目标（图标 16）；语言标签 12px | VD-28 |
| C4 | 一屏 ≤4 档字号 | `p2-appearance`、`p1-chat` | 20/16/14/12（或 16/14/12）；不出现 5 档 | VD-42 |
| C5 | 字重来自 role（XL/L/M=600、S/XXXS=500 已烘焙）；需强调时只允许 `design.Weight*` | `p2-about-settings`、`p2-model-settings`、侧栏行标题 | 两处节标题字重一致；`grep FontWeight` 只出现 `design.Weight*`（无字面量） | VD-43 / 规则 12 |
| C6 | 元数据 12px 且不用 accent 上色 | 各屏 | 元信息=灰、12px | VD-44 |
| C7 | 13px 允许但不得与 12px 在同一层级混用 | `p2-model-settings` 行内三行 | 同屏同类信息只有一档 | VD-42 |

### D. 圆角与形状（VD-12/25/26/29/30/44）

| # | 判据（可量化） | 看图 | 通过条件 | 关联 |
|---|---|---|---|---|
| D1 | 卡片半径 ∈{4,6,8,12,16} | 脚本 [4] 的 6 张卡 | 全 PASS（±2） | VD-25 |
| D2 | 同屏圆角 ≤3 档 | 各屏（可 `grep -c 'Radius[A-Z]'`） | 会话屏 ≤3 | VD-25 |
| D3 | segmented 无"被圆角切断的 2px 下划线" | `p2-appearance` 主题切换、`p2-insights` 7天/30天 | 选中段是"抬升块"（白底+1px 边+600）**或**方角下划线；不得圆角+底边线并存 | VD-12 |
| D4 | composer 圆角 = `RadiusXL(16)` 且与卡片(12)差 ≤4 | `p1-chat` composer | 半径采样 16±2；不再出现 20 | VD-26 |
| D5 | chip 高度统一 28；徽章 20 | `p1-chat` 附件 chip、`p2-model-settings` 默认徽章 | 同角色同高 | VD-29/44 |
| D6 | 图标按钮只有两种形态：Ghost（无底）/Filled（方形） | 消息 ⋯、顶带 ⋯、弹窗 ✕ | 不出现"灰圆盘"第三形态 | VD-30 |

### E. 布局·遮挡·窄窗（VD-01/02/16/20/21/23/24/31/32/33/34/35/37/38/39/40）

| # | 判据（可量化） | 看图 | 通过条件 | 关联 |
|---|---|---|---|---|
| E1 | 窄窗 1024 + 检查器：顶带右侧无溢出、无裁切、不与「检查器」相撞 | `narrow/narrow-session-inspector` 顶部 0..42 | 脚本 [5] 顶带留白 PASS（最小亮度 ≥150）；放大看无残字 | **VD-01 (P0)** |
| E2 | toast 不覆盖阅读列、不覆盖设置页脚、模态打开时不压按钮 | `p1-chat`、`narrow/narrow-settings`、`p2-ask` | toast 在右下（或顶部非内容区）；一屏 1 张 + 计数 | **VD-02 (P0)** |
| E3 | 正文块 ≤660（ProseMeasure） | `p2-markdown-specimen`、`p2-context` | 脚本 [5] 正文块 PASS | VD-23 |
| E4 | 消息列右缘 = composer 右缘（差 ≤2px） | `p2-status-strip` 气泡 vs composer | 采样两边 x 之差 ≤2 | VD-24 |
| E5 | 右栏 header ≤2 层横线；文件名不重复 3 次 | `p2-file-preview` 0..122 | ≤2 条横线；`README.md` ≤2 次；accent 下划线 ≤1 | VD-16 |
| E6 | 禁用态发送按钮可见（≥3:1 或 L2 边） | `p1-chat` 发送 x≈1280..1363 | 填充/边框/文字任一达到 3:1；不再"隐形" | VD-20 |
| E7 | 空态建议按钮内边距 ≥12 | `p2-landing`、`narrow/narrow-landing` | 文字距边 ≥12；不再 168 定宽贴边 | VD-31 |
| E8 | palette 三列不碰撞；底部无死空白 | `p2-palette` | 描述列与 kbd 列分离；长描述省略+tooltip | VD-33 |
| E9 | 能力卡跨卡对齐（徽章/路径同列） | `p2-capabilities-skills` 两卡 | 两卡路径左缘差 ≤2 | VD-34 |
| E10 | about/context 定义表只画行间线；标签列 ≤160 | `p2-about-settings`、`p2-context` | 首行前/末行后无线；值列起始一致 | VD-37 |
| E11 | 右栏树行 leading slot 统一 | `p2-files` 树 | 文件行与文件夹行同左缘（图标位 16px） | VD-38 |
| E12 | 自动化行右侧 ≤2 常显动作，徽章与控件同基线 | `p2-automation` 第 2 行 | 常显 ≤2；徽章不与控件错行 | VD-18/39 |
| E13 | team overlay 空态收敛 | `p2-team-overlay` | 空分组折叠成一行；不至于"空态文案 > 内容" | VD-35 |
| E14 | 顶带目标 ≤5 个、间距 ≥12 | `p1-chat` 顶带 | 可点目标数 ≤5；间距 12 | VD-40 |
| E15 | 窄窗 chip 不退化到"纯图标无文字" | `narrow/narrow-session` 顶带 | chip 保留文字或 icon+数字 | VD-21 |
| E16 | ask 弹窗：选项左对齐、hint 与动作同行左侧 | `p2-ask` | 选项左对齐；hint 不在按钮下方居中 | VD-32 |

### F. 列表·间距·节奏（VD-14/15/17/19）

| # | 判据（可量化） | 看图 | 通过条件 | 关联 |
|---|---|---|---|---|
| F1 | 设置页小节：标题下**无**规则线；上 24 / 下 12 | `p2-appearance`、`p2-behavior`、`p2-archive-settings` | 各页一致；无"有的有有的没有" | VD-14 |
| F2 | 列表行有分隔或间距 ≥24 | `p2-model-settings`、`p2-memory-settings` | 行归属清晰，不粘连 | VD-15 |
| F3 | 设置正文有段距（≥8） | `p2-behavior` 5 行规则 | 每条成块，不再是文字墙 | VD-19 |
| F4 | 轨迹页无"按钮脊柱" | `p1-trajectory` 右缘 | 无 5+ 个并列描边按钮；改为行 hover 出现 | VD-17 |
| F5 | 列表行归属清晰（分隔线或 ≥24 间距）；文本行距在 18–24 且为整数 | `p2-automation`、`p2-model-settings` | 行不粘连；文本行距不出现 27 这类漂移 | 规则 13 |

### G. 暗色（VD-22 + D/亮色同判）

| # | 判据（可量化） | 看图 | 通过条件 | 关联 |
|---|---|---|---|---|
| G1 | nav 比 page 深 | `narrow/dark-session`、`p2-context-dark` | 脚本 [6] 全 PASS | ramp |
| G2 | 卡片 vs 页面亮度差 ≥8 级 | 同图 | 采样 L 差 ≥8（旧 `#1B1B26` vs `#111118` = 6 不够） | VD-22 |
| G3 | composer 有 elevation（L3 边或更亮底） | `narrow/dark-session` composer | 与页面可分辨；阴影不可见时靠边/底 | VD-22 |
| G4 | 语义色不刺眼也不闷：warning 头块不再是泥棕 | `p2-approval-diff-dark` | 头块不再 `#4B3820`；按 A8 用 rail+badge | VD-22 |
| G5 | 暗色下 A1–A11、D1–D3 同样成立 | 各 `*-dark` | 同上判据 | 全 |

---

## 3. 干净区（**别改坏的 10 条**，收尾时逐条确认"没退化"）

| # | 不能变的东西 | 判据 | 取证 |
|---|---|---|---|
| K1 | 文字对比度 | `LabelSecondary` 亮 ≥7:1、`LabelTertiary` 亮 ≥5:1（含选中 tint 上）；暗色 ≥7:1 | 采样 `#4A5160`/`#5A6371` |
| K2 | 暗色导航下沉 | `#0B0B10` < `#111118` | 脚本 [6] |
| K3 | 状态语义 | success/danger/warning 三色只做 fill/badge/icon；小字用 `*Text`；**running ≠ warning**（新规则：进行中 = Accent spinner + `LabelSecondary` 词） | `p2-tool-row-states`、`p1-approval` |
| K4 | 控件高度档位 | 只有 28/32/36/40/44，无 38 | 采样 3 个控件 |
| K5 | code fence 解剖 | 填充块 + 小圆角 + 无边框 + 复制在头右 | `p1-chat` 代码块 |
| K6 | 工具行状态四态 | 运行中/完成/失败/已停止 四色可辨，行高 36 | `p2-tool-row-states` |
| K7 | 空态与错误态文案仍在 | 空态有说明+动作；错误有 `*Text` 语义色 | `p2-landing`、`p2-capabilities-connectors` |
| K8 | 键盘可达 | 焦点环可见（≥3:1）、Esc 语义、Tab 顺序 | 跑一次 `ui-drive`/帧测；看 focus 截图 |
| K9 | 尺寸只用 token | `DialogWidth*`、`TranscriptWidth`、`ProseMeasure` 无裸数值 | `designlint --strict` 无新增 |
| K10 | 图标纪律 | 只用 `internal/icons`，16/24 网格，无 emoji/Unicode 图标 | `grep` + 目视 |

---

## 4. token 对照：A3 建议 vs 最终 token（验收按"右列"判）

| A3 建议 | 最终 token | 验收口径 |
|---|---|---|
| 接缝统一 1px BorderL2 | ✅ L2 = 9%（暗 10%） | 按 1px + L2 判（G1/G2） |
| 圆角收敛 8/10/16 | ✅ 4/6/8/12/16（composer XL16） | 按 5 档判，**同屏 ≤3 档**（D1/D2） |
| 卡片必须与页面有表面差 | ⚠️ 未采纳（亮色 Surface 仍 `#FFF`） | 改判：卡片边 = **L3 15%**，且卡内不得三线叠加（A6） |
| 用户气泡改填充 | ⚠️ 待确认 | 按 A7 判（无独立描边） |
| 状态条 6 项裁到 3 项 | ✅ 字号改 12（`TypeXXS`） | 字号达标即可；**信息项不删**（AGENTS §17）（C1） |
| 11px 禁止成句 | ✅ `TypeXXXS` 只留 kbd/gutter/微标签 | C1–C3 |
| 字重烘焙 | ✅ XL/L/M=600、S/XXXS=500 | C5：call site 不传字面量，强调只用 `design.Weight*` |
| 阅读列 960 → 816 | ✅ 采纳 `ProseMeasure 640`（列宽不变，正文限宽） | 正文 ≤660（E3）；列宽仍 ≤968 |
| 浮层加边 | ✅ 新增 `BgOverlay`（暗 `#23232F`） | A5：暗色靠面差即可，亮色靠边或面差 |
| hover 可见 | ✅ hover 改中性 4.5% + hover 用 `BorderL3` | 交互帧测试/`ui-drive` 截图看 hover 差 |
| 动效 | ✅ 80/120/160/200 | 代码核对 + 一次 `ui-drive`（不在 PNG 内） |

---

## 5. 收尾时我会做什么（等你说「收尾」）

1. **复跑闸门**：`measure.sh .cache/ui-conformance-v2 --baseline .cache/ui-conformance --strict`，贴 PASS/FAIL 汇总 + 失败项明细。
2. **逐张 `read_image` 新图**：优先 `narrow/*`（尤其 `narrow-session-inspector`）、`*-dark`、`p1-chat`、`p1-approval`、`p2-appearance`、`p2-files`、`p2-file-preview`、`p2-context`、`p2-automation`、`p2-model-settings`、`p2-ask`、`p2-palette`；发现可疑处再 4×–9× 裁剪（`crops/`）。
3. **逐条勾选本清单**，产出 `A3-qa/verdict.md`：每条 ✅/⚠️/❌ + 证据（新 PNG 路径 + 坐标 + 采样值），❌ 项写明退回给哪个实现者、期望值。
4. **回归三条红线**：P0 两项（E1 窄窗、E2 toast）+ 接缝（A1–A4）必须全绿才给"可发布"结论；否则给出"有条件通过 + 阻塞清单"。

---

## 6. 脚本说明与已知限制

**用法**
```bash
A3-qa/measure.sh [快照目录] [--baseline DIR] [--strict] [--list]
```
- 无参时自动找 `.cache/ui-conformance-v2`，否则 `.cache/ui-conformance`。
- 输出：`.cache/ui-conformance-qa/<slug>.report.txt`（人读）+ `.tsv`（六列：check/screen/metric/value/expected/verdict）。
- `--list` 打印探针表（screen|kind|参数）；布局大改时按新坐标改脚本顶部 `PROBES`。
- 期望值集中在脚本顶部 `CONFIG` 段：`BORDER_L2_PCT=9`、`DARK_L2_PCT=10`、`RADII`、`ACCENT_*`、`PROSE_MAX=660`、`COLUMN_MAX=968`、`BAND_H=72`。**再改 token 只改这里。**

**判据实现方式（便于复核）**
- 接缝：在 3 个 y 上做等色游程，找"长度 ≤4 且与左右都不同"的平台（阴影渐变不会被计入），同一 x 命中 ≥2 次才算稳定线；相邻线合并成一条 → 宽 2 = 双线 FAIL。
- accent 块：`-fuzz 12%` 抠出 accent → 连通域 → 只统计 ≥24×24（图标 16、下划线 1px、描边不计，与"≤2 个 accent 填充块"同口径）。
- 圆角：以卡片外底色为参照，自适应阈值（max(20, 行内最大对比×85%)）找左/上边界，再在 26×26 角部网格里找"每行首个边像素"回到最小偏移的行号 = 半径。
- composer：把内容列中段切成竖条，按行求"非底色覆盖率"，覆盖率 >200/255 的行 = 整行是线 → 取跨度 90–170 的最下一对 = 上下边。
- 正文块：`-transparent <页底色> -trim` 的 bbox 宽。

**已知限制（不影响结论，需人判兜底）**
1. **hover/pressed/focus 不在静态快照里**：G/H 的交互项需 `ui-drive` 或帧测试补图；脚本只判静态几何。
2. **无描边浮层**（设置面当前仅阴影）：圆角项会报 WARN「角部无法拟合」——这本身就是 A5 的信号；补上边后自动转 PASS。
3. **非 1512×944 的画布**：探针坐标为基线坐标，画布变化时接缝/几何会大面积 FAIL 且提示"无接缝线"；先改 `PROBES` 或对同尺寸快照跑。
4. **模糊语义项**（"看着舒服"）不可自动判：D3/D6/E16/F4 等仍需人看图。
5. 脚本不读代码：像"call site 是否还传 `FontWeight`""是否新增自绘"这类，仍由 `designlint --strict` + `grep` 兜底（清单 K9/K10）。
---

## 7. 工具自检记录（不是评审结论）

脚本已在两个目录上实跑，用于证明"能跑、判得对"：

| 目录 | 结果 | 说明 |
|---|---|---|
| `.cache/ui-conformance`（旧基线） | PASS 40 / FAIL 14 / WARN 2 | 14 个 FAIL = 11×侧栏 2px 双线 + 3×设置导航 0px + 1×窄窗顶带溢出，与 A3 的 P0/P1 前三名逐条对应 → **判据方向正确** |
| `.cache/ui-conformance-v2`（2026-11-18 00:24 快照，119 张） | PASS 39 / FAIL 14 / WARN 3 | 见下；仅作冒烟，不代表收尾结论 |

v2 冒烟暴露的三类未闭合项（供实现者参考，收尾时重跑）：

| 项 | 实测 | 判断 |
|---|---|---|
| 侧栏接缝 | `x=279 #D4D6D8`（侧栏自身 L3 15%）+ `x=280 #E9EAEA`（L2 9%）仍为 **2px 双线**（暗色 `#323236`+`#292930`） | VD-03/A1 **未修**：侧栏 `BorderWidth(0,1,0,0)` 与 Split divider 仍在各画一笔 |
| 设置导航接缝 | `p2-appearance`/`p2-model-settings`/`narrow-settings` 仍 **0px** | VD-04/A3 **未修** |
| 窄窗顶带 | `narrow-session-inspector` 接缝左侧 40px 最小亮度 **80**（旧 91），裁剪图仍见「⋯ 🔍 台 ▯ 5」被裁并与「检查器」相撞 | VD-01/E1 **未修**（P0） |
| 卡片圆角 | 卡 `r≈12`、palette `r≈10` | 新档位已生效（12 命中）；palette 10 介于 8/12，建议统一到浮层档 12 |
| 检查器接缝 | `#E9EAEA` Δ0 | L2 9% 已正确落地 |

> 结论：token 层（圆角/边框色/字号/ramp）已见效；**结构接缝与窄窗溢出属于壳层绘制点重复问题，脚本会一直红到壳层改完**。
