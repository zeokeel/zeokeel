# A5 文案细节审计（ZeoKeel 重设计，2026-11-18）

审计人：copy-audit（A 阶段，只读）
范围：`internal/ui/*`（13 模块）、`internal/uiroot`、`internal/uikit`、`internal/design/conform` 的全部用户可见文案（中文为主，英文 locale 同步）
方法：全量 grep 词面 → 按概念聚类 → 逐条回源码读上下文 → 对照 `.cache/ui-conformance` 的 119 张 PNG 验证渲染结果
产出：本报告 + [A5-strings.tsv](A5-strings.tsv)（100 条可直接替换的中英文案：P0 20 / P1 42 / P2 38；7 列：位置 / 现有 zh / 现有 en / 建议 zh / 建议 en / 理由 / 优先级）

> **一处文档对不上**：任务书写「`docs/DESIGN.md` §3.7（状态语义）」，但现行 DESIGN.md 没有 §3.7（§3 只有 3.1 拒绝清单、3.2 现状违规证据）。状态语义的实际出处是 **§3 常驻规则 7「状态语义不可降级」+ §5.2 状态语义矩阵 + §5.2「状态展示三层」**。本审计按后者执行；建议 Lead 顺手修掉任务书/简报里的这个引用，避免后续成员找错位置。

---

## 0. 结论摘要（按影响排序）

1. **同一个对象有三到四个名字，且在同一屏内互换。** 会话对象在中文里有「会话(218)/对话(55)/讨论(1)」，英文里有 `Sessions / Chats / Conversation / Chat` 四种；屏幕阅读器听到的「新建会话」和可见按钮「新对话」是两个名字。这是本次审计最严重的单点问题（F-02）。
2. **导航目的地名与页面标题名冲突，且返回按钮写错名字。** 能力页在侧栏叫「能力」，会话页头的返回按钮却写「插件」（PNG `narrow/narrow-session.png` 左上角可见「‹ 插件」，侧栏同时写着「能力」）。一处代码 `session/view.go:514`（F-01）。
3. **子智能体一族在同一句里三个词。** `agentsurface.go:1226` 空态原文：「还没有子智能体。**子代理**/**队友**启动后会出现在这里。」，英文同句两个词（`child conversations` / `subagents`）；相邻两行动作分别是「继续此队友」和「继续此子代理」。工具友好名又用「启动子代理」（F-03）。
4. **同一页对同一件事给出相反结论：连接器启停要不要重启。** 可见的页首说明无条件写「启用变更在重启后生效」，而同文件里能识别热重载的 `connectorPageHint()` 是**死代码**（无调用点），开关动作的 toast 又写「已应用连接器启停变更」。默认装配有热重载，所以页面上那句是错的（PNG `p2-capabilities-connectors.png` 直接可读，F-04）。
5. **开发者文案进了产品。** 「这是 home 的默认 Hero 空态。」（`home/home.go:499`）与「这是一个独立编译的 UI 模块，通过注入点出现在宿主中。」（`welcome/welcome.go:44`）是默认显示给用户的字符串（F-05）。
6. **权限/沙箱的同一段说明有四份副本、三种标点。** `behavior.go:168`、`view.go:3610`、`view.go:3653`、`sandbox.go:64` 各自措辞不同，其中一份多出「其他操作会询问」、另两份没有；同一行还并存「（全局跟随）」和「（跟随全局）」（F-06）。
7. **设置页首屏是四行密集说明，其中一句是「已移到…」的元叙述。** 「权限模式已移到输入框工具栏的权限按钮：…」会随下一次改版失效，却占据了「行为与限额」的第一行（PNG `p2-behavior.png` 第 1 段；F-07）。
8. **空态里唯一的 Accent 主按钮只做聚焦。** 会话空态的主色按钮「开始输入 / Start typing」不发送、不新建，只把光标移进输入框（`session/view.go:1213`）；同一屏的标题「开始新对话」和副标题「还没有消息，开始一段新对话吧」还在重复（F-08）。
9. **数字与单位三套并存。** 状态条同行出现 `42K/128K tok`、`62 tok/s`、`缓存命中 86%`，消息元数据写 `42.1K tokens`，洞察 KPI 写「词元 tokens」、表头写「词元」；量词同时用「步工具调用 / 3 轮对话 / N 条已结束 / 0 名成员 / 2 条待审批」（F-09）。
10. **两处承诺与文档不一致：** 输入框提示承诺 `Cmd/Ctrl+Enter`，`/help` 键盘表里没有这一行；组合键书写同时存在「Ctrl+N」（格式化）与「Cmd/Ctrl+Enter」「Cmd/Ctrl+Shift+Space」（手写）三种形式（F-10）。

---

## 1. 现状（证据）

### 1.1 文案分布（含中文字面量的代码行，非测试）

| 包 | 行数 | 说明 |
|---|---:|---|
| `internal/ui/session` | 914 | 最大文案面：会话、输入框、轨迹、上下文面板、团队/子智能体、目标、待办 |
| `internal/ui/settings` | 803 | 设置 10 个内置页面 + 模型目录编辑器（长说明文本集中地） |
| `internal/ui/plugins` | 412 | 能力页：技能 / 连接器 / OAuth / 表单校验 |
| `internal/uikit` | 241 | 共享组合：审批卡、计划审阅、提问、推理、运行态、输入框壳 |
| `internal/ui/automation` | 142 | 自动化任务表单 + cron 说明 |
| `internal/uiroot` | 123 | 命令面板、快捷键表、设置壳、紧凑栏、右侧栏 |
| `internal/ui/sidebar` | 109 | 会话树、工作区菜单、消息搜索 |
| `internal/ui/home` | 85 | 落地页 / 首跑引导 |
| `internal/ui/rightpanel` | 67 | 文件、变更、工件 |
| `internal/ui/insights` | 67 | 用量汇总 |
| `internal/ui/toasts` | 43 | 通知短语表 |
| `internal/ui/approval` `interaction` `toolcards` `welcome` | 44 | |
| **合计（ui + uiroot + uikit）** | **3050** | 另有 `internal/design/conform` 42 行 |

本地化调用点约 **2400** 处：`st.tr(...)` 1468、`localized(...)` 720、`uikit.T / T(o.Locale)` 216。

### 1.2 机制（这是好的部分，先记下来）

- 全部文案是**行内中英对**：`uikit.T(locale, zh, en)`；每个模块一个 `st.tr` 别名，配置可覆盖的字段用 `localized(value, zhDefault, enDefault)`。没有资源文件、没有缺翻的 key、没有运行时查找失败。19 个包一致。
- 唯一例外是 **toast 短语表**（`toasts/toasts.go:531-598`）：运行时按**精确匹配**把创建时的中文串翻成英文，未知串原样透传。这个设计能工作，但它把「文案」变成了「数据」，是唯一可能漏翻的地方（F-14）。
- `uikit.FormatShortcut` 已经是「唯一组合键显示形式」的机器化实现，注释里写明了裁定（「去掉加号两侧空格，与命令面板的 Cmd+K 一致」）。审计发现的问题不是缺少机制，而是**有些地方绕过了它**（F-10）。
- `uikit.EmptyStateBlock` / `PageHeader` / `Section` / `ErrorLine` 已经是成型的文案骨架（标题 + 说明 + 动作 / 标题 + 副标题 / 分节 / 错误行可选中）。问题是一半的空态还在用更弱的 `EmptyState(text)`（F-12）。

### 1.3 渲染证据（PNG）

| PNG | 看什么 |
|---|---|
| `.cache/ui-conformance/narrow/narrow-session.png` | 侧栏「能力」 vs 页头「‹ 插件」；状态条 `1 轮对话 · 0 步工具调用 · 48 tok/s · 42K/128K tok · 缓存命中 86% · 上下文剩余 67%` |
| `.cache/ui-conformance/p1-chat.png` | 同行 `62 tok/s` 与 `42.1K tokens`；输入框提示 `Enter 发送 · Shift+Enter 换行`；页头 chips「智能体团队 / 子智能体」 |
| `.cache/ui-conformance/p1-approval.png` | 审批卡：`需要审批 团队任务` / `Esc 拒绝` / `允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。` / `来自会话：…` / 按钮 `拒绝 · 本会话内始终允许 · 允许` |
| `.cache/ui-conformance/p2-behavior.png` | 设置页首屏 4 行密集说明；分节「工作步骤」+ 字段「工作步骤展示」；`消息字号：标准 · 指令文件：AGENTS.md`；页脚 `恢复限额默认 / 保存` |
| `.cache/ui-conformance/p2-landing.png` | 空态「开始新对话 / 还没有消息，开始一段新对话吧。/ 开始输入」；`开始前还需要：` + `尚未配置 API Key（使用本地模型服务时可忽略）` |

---

## 2. 发现

编号规则：**F-xx | 严重度 | 位置 | 问题 | 为什么是问题 | 具体改法**。P0 = 用户会读到错误/矛盾/内部用词；P1 = 明显不专业或与承诺不符；P2 = 行业惯例/精修。

### 术语类

**F-01 | P0 | `session/view.go:514`（`backTargetLabel`）**
问题：会话页头返回按钮显示「插件 / Plugins」，而侧栏导航项与页面标题都是「能力 / Capabilities」。
为什么：同一个目的地在同一屏内两个名字；「插件」在产品里已不指任何东西（能力页 = 技能 + 连接器）。
改法：`return st.tr("能力", "Capabilities")`。同时把 `ScreenNavSpec`、页面标题、返回目标名做成**一处来源**（现在是三处手写：`uikit/screennav.go:23`、`plugins/view.go:62`、`session/view.go:514`）。

**F-02 | P0 | 全局（会话语义）**
问题：会话对象有 4 套叫法：中文「会话」(218 处) /「对话」(55 处) /「讨论」(1 处)；英文 `Sessions`(7) / `Chats`(2) / `Conversation`(4) / `Chat`(3)。同行混用两处：
- `home/home.go:432`「还没有**会话**。开始第一次**对话**，或先完成下面的准备：」
- `agentsurface.go:1226`（见 F-03）
可见/可听两名一处：按钮「新对话」，屏幕阅读器标签「新建会话」（`commands_help.go:54` 甚至把「新会话」的英文写成 `New conversation`）。标题回退处 `session.go:75`＝「对话」、`view.go:799`＝「会话」、`export.go:193`＝「Conversation」——同一个字段三种回退。
为什么：这是产品出现频率最高的名词，一个用户一天要读上百次。不一致会让「会话」和「对话」被理解成两个不同对象（例如「对话」是不是临时聊天？）。
改法：见 §3.1 术语表。核心裁定：**对象＝会话 / Session**；「对话」只保留为会话内的**视图标签**（`对话 / 轨迹 / 记忆 / 上下文` 的第一个）；动作统一「新建会话 / New session」；**不采用**「对话」作为通名（改造成本 218 处 vs 55 处，且「会话」与归档、工作区、未命名会话、会话操作、会话搜索等既有词族一致）。

**F-03 | P0 | `session/agentsurface.go:1226`、`:1223`、`:1288-1290`、`uikit/toolnames.go:75`**
问题：子智能体一族在中文有「子智能体(19)/子代理(13)/子会话(1)/队友(10)/成员(4)」，英文有 `Subagent / Child agent / Child conversation / Teammate / Member`。同一句：「还没有子智能体。**子代理**/**队友**启动后会出现在这里。」；相邻两行动作：「继续此队友」/「继续此子代理」；工具名「启动子代理」。
为什么：用户在审批卡、工具卡、子会话切换器、团队面板之间来回看，四条不同的词会被读成四种对象。
改法：**子智能体 / Subagent** 作上位词（含一次性与团队成员两种 kind）；**团队成员 / Team member** 作团队成员的全称（团队面板内可用短形式「成员 / Member」，因为上下文已限定）；**队友 / Teammate** 退役；**子代理**退役为「子智能体」。`childAgentKindLabel` 的 `Teammate` 分支改「团队成员 / Team member」，其余分支不动。

**F-04 | P0 | `plugins/connectors_section.go:219-221`（可见）vs `:881-891`（死代码），`plugins/mcp_server_switches.go:102-106`**
问题：连接器页首的说明是**无条件**的「启用变更在重启后生效」，而同一文件里已经有一个**能分辨热重载**的 `connectorPageHint()`（有 reloader 时输出「新增、编辑、启停与授权立即生效」）——但这个函数**没有任何调用点**（`grep -n 'connectorPageHint' internal/ui/plugins` 只有定义行 881）。默认装配提供热重载（`connectors_reload.go` + 开关 toast「已应用连接器启停变更」），所以页面上写的是错的；用户会以为必须重启（PNG `p2-capabilities-connectors.png` 第 1 段可直接读到这句）。
为什么：这是「安全/行为承诺」类文案，用户按它决定要不要重启应用；而页面下方就有「重试 / 启用」开关，行为与说明直接打架。
改法：把可见的 intro 行改成调用 `connectorPageHint(st)`（顺手修正它「看不到 mcp.json 场景」的缺口），删除无条件的旧句；`mcp_server_switches.go:102` 已经用 `mcpHotReload()` 分支，保持。若个别连接器（如 stdio/LSP 子进程）确实要重启，**逐类说明**，不要一句总述。

### 内容错误 / 自相矛盾

**F-05 | P0 | `home/home.go:499`、`welcome/welcome.go:44`**
问题：默认显示的字符串是模块实现说明：「这是 home 的默认 Hero 空态。」「这是一个独立编译的 UI 模块，通过注入点出现在宿主中。」（后者还是中文句里夹小写英文 home）。
为什么：产品文案里出现内部术语，等于对用户说「此处没做完」。
改法：删除 hero 提示；欢迎语改「欢迎使用 ZeoKeel。/ Welcome to ZeoKeel.」。

**F-06 | P0 | `settings/behavior.go:168`、`session/view.go:3610`、`session/view.go:3653`、`settings/sandbox.go:64`**
问题：权限与沙箱的说明有四份，措辞/标点不同（`＝` vs `：`），事实也不完全一致：
- `:3653`「只读：…免审批，**其他操作会询问**」；`:3610` 没有这句。
- 三个位置对「shell 类工具在沙箱可用时自动放行」的表述长度不同。
为什么：这类说明直接决定用户敢不敢开「所有权限」；两份措辞不同的安全说明里只要有一份过时，就是事故。
改法：抽成 `uikit` 的一份常量（`uikit.PermissionHelp(locale)`），三处引用同一份；句式统一为**定义列表**：`只读＝…；工作区修改（默认）＝…；所有权限＝…`（＝ 定义式保留，这是全文一致的做法），后面单独一句讲两层模型。**设置页只留一份**，输入框按钮与菜单项用 tooltip 引用同一份。

**F-07 | P0 | `settings/behavior.go:267`**
问题：「权限模式已移到输入框工具栏的权限按钮：工作区修改 / 所有权限 / 只读。」是元叙述（描述产品改版动作），且是设置页第一段。
为什么：改版说明会立刻过期；且它占用首屏最贵的一行，读者来这页是要设限额的。
改法：删除；同一句话搬进输入框权限按钮的 tooltip（那里用户正要看）。

**F-08 | P0 | `session/view.go:1203/1206/1213`，`session/session.go:76`**
问题：会话空态三层文案都在说同一件事：标题「开始新对话」、副标题「还没有消息，开始一段新对话吧。」、按钮「开始输入」（只聚焦输入框），而右侧三条建议提示才是真正可执行的内容。
为什么：空态是首跑用户第一屏；主色按钮不做事是最伤信任的一类细节。
改法：标题「发第一条消息 / Send your first message」；副标题换成价值主张（「描述你想完成的事，ZeoKeel 会读代码、改文件并验证结果。」）；**删除「开始输入」按钮**，让三条建议提示成为唯一动作组。若必须保留一个按钮，标签要写成它真实的效果（聚焦输入框），但那会是个更差的按钮——建议删。

### 微文案

**F-09 | P1 | `session/view.go:4246-4279`、`insights/view.go:187/248`、`agentsurface.go:2105`**
问题：单位与量词三套：`tok` / `tokens` / `词元`；`步工具调用` / `3 轮对话` / `N 条已结束` / `0 名成员` / `2 条待审批`。
为什么：状态条是产品里被看最多的文本行；「1 步工具调用」在中文里不成立（「步」不是工具调用的量词），「N 条已结束」用在成员上也错。
改法：单位统一 **tokens**（速度 `tokens/s`）；量词按对象固定：会话「个/轮」、工具调用「次」、消息「条」、成员「名」、审批「个」（见 §3.2.8）。

**F-10 | P1 | `session/view.go:3473/3479`、`commands_help.go:60`、`settings/hotkey_settings.go:342`**
问题：组合键三种写法并存：格式化输出 `Ctrl+N`、手写 `Cmd/Ctrl+Enter`、手写示例 `Cmd/Ctrl+Shift+Space`（另有一处内部常量 `Cmd/Ctrl + N` 带空格，靠调用方 `FormatShortcut` 兜住）。`/help` 键盘表缺 `Ctrl+Enter` 一行，而输入框提示承诺了它。
为什么：企业级工具里组合键是「必须能照着按」的信息；两种写法会让用户去试一个按不出来的键。
改法：面向用户的组合键**一律过 `uikit.FormatShortcut`**；文案里禁止手写 `Cmd/Ctrl`；`helpShortcuts()` 补 `CmdOrCtrl+Enter` 行。**裁定：不用 ⌘⇧ 符号**（理由见 §4.2）。

**F-11 | P1 | `uikit/reasoning.go:80/2700/1587`、`home/composer.go:528`**
问题：中文把「思考（轨迹）」和「推理（等级）」分成两个词（这是对的），英文却都写 `Reasoning`，与「推理等级 / Reasoning level」撞词；另有第四种叫法「思考强度」。
为什么：英文用户看到 `Reasoning` 两处出现在不同层级（轨迹标题 vs 等级菜单），会以为是一个设置。
改法：轨迹＝**思考 / Thinking**；能力＝**推理等级 / Reasoning level**；「思考强度」→「推理等级」。这把中文和英文一一对上。

**F-12 | P1 | `sidebar/sidebar.go:1173`、各模块空态**
问题：空态有两种骨架。强骨架 `EmptyStateBlock(title, hint, action)` 有 23 处（自动化、洞察、记忆、LSP…），弱骨架 `EmptyState(text)` 有 33 处（侧栏「还没有工作区」+ 下一行「点击 + 添加工作区」）。
为什么：弱骨架没有动作、没有标题层级，且「点击」是鼠标专属措辞（键盘/读屏用户不适用）。
改法：统一 `EmptyStateBlock`；空态三句式（见 §3.2.2）；把「点击」改成「选择」或直接给按钮。

**F-13 | P1 | `rightpanel/view.go:363`、`agentsurface.go:941/1870`、`context_pane.go:1100`**
问题：加载文案两式：「正在读取文件…」vs「读取任务板…」「读取输出…」。
为什么：同一状态两种句式，读起来像两个不同系统。
改法：统一「正在 + 动词 + …」。

**F-14 | P1 | `toasts/toasts.go:531-598`**
问题：toast 短语表用精确匹配翻译；未知串原样透传 → 英文界面可能出现中文 toast。另有等级词「成功 / Success」「提示 / Info」在图标+颜色之外又占一行。
为什么：这是唯一可能漏翻的路径；等级词是三重编码。
改法：短期——把所有 `NotifySuccess` 的调用串补进短语表（或在调用点就传本地化后的串）；长期——通知契约带 `TextKey + 参数`，由渲染层翻译。等级词改为**仅无障碍标签**（可见行只留图标与颜色），需要文字强调时用「失败」/「需要审批」。

**F-15 | P1 | `settings/modelcatalog.go:765`、`modelcatalog_editor.go:890`、`sidebar/workspace_add.go:160`**
问题：`删除模型 / Remove model`、`确认删除 / Confirm remove`——中文写「删除」（暗示删数据、不可逆），英文写 `Remove`（移出列表）。而 LSP 处是「移除 / Remove」，两套。
为什么：这个词决定了用户敢不敢点。
改法：目录类条目（模型、LSP、连接器、工作区）统一「移除 / Remove」；真删除数据（记忆、会话、归档）统一「删除 / Delete」。

**F-16 | P1 | `session/view.go:4258/4265`、`insights/view.go:248`、`agentsurface.go:2105`（单位）；`session/view.go:4310`（名词序）**
问题（并入 F-09 的数字层）：`对话回合 3` vs `3 轮对话`；`%d turns · %d tool steps`。
改法：tooltip 改「对话轮次 / Turns」，与状态条同序。

**F-17 | P2 | 全局引号**
问题：「」73 处 / “”7 处 / 『』2 处；英文用直引号 `\"`。
改法：见 §3.2.7（推荐“”，保留「」为备选并给出两套迁移清单）。

**F-18 | P1 | 全局错误行（`uikit.ErrorLine` 的 60+ 调用点）**
问题：绝大多数错误是 `"X失败：" + err.Error()`，没有「下一步」，且 `err.Error()` 常是英文 Go 错误；少数地方（`automation/view.go:341`）还直接拼原始值。全产品只有连接器错误映射（`plugins/view.go:100-116`）做到了「可读原因 + 括号里的下一步」，这套做法应当推广成默认。
为什么：错误行是用户最需要「然后我该做什么」的地方；只有原因没有出路等于让用户猜。
改法：三要素模板（见 §3.2.3）：**发生了什么 + 为什么（可折叠原始错误）+ 下一步动作**。至少给可操作错误补一句建议（「请检查服务商密钥后重试」/「请重试，或从日志复制详情」）。

**F-19 | P1 | `uikit/format.go:29`（`FormatDateTime`）、各列表**
问题：全产品只有绝对时间（`2026-11-18 09:00` / `Jan 2, 2006 3:04 PM`），会话列表没有相对时间。
为什么：AI 客户端里「刚刚/3 分钟前/昨天」是判断「哪个是我刚用的会话」的主要线索；只有绝对时间要求用户自己算。
改法：新增 `uikit.FormatRelativeTime`，规则见 §3.2.9；会话列表/最近列表用相对时间、tooltip 给绝对时间；自动化「下次运行」保持绝对时间。

**F-20 | P1 | `uikit/approval.go:279`、`settings/memory.go:288/306`、`settings/archive.go:166`**
问题：危险确认缺「规模」：清空全部记忆只说「此操作无法撤销」，不说是多少条；「确认删除这条记忆？」是自问自答句式。
为什么：不可逆动作的确认框必须让用户看清自己删的是什么、有多少。
改法：`确认框四要素`（§3.2.4）：**动作动词 + 对象全名 + 规模/后果 + 可否撤销**。

**F-21 | P1 | `session/view.go:3173`**
问题：输入框占位符 `输入消息…` / `Ask ZeoKeel…`——中文是操作说明，英文是品牌邀请句。
为什么：同一位置的两种语言应同语域；英文那句还让中文用户以为是产品名专属功能。
改法：`输入消息…` / `Message ZeoKeel…`（或两边都用邀请句「问点什么…」/「Ask anything…」，但需同时改）。

### 层级与 a11y

**F-22 | P1 | `settings/behavior.go:288/291`**
问题：分节标题「工作步骤 / Work details」与字段标签「工作步骤展示 / Work details」重复，英文**完全同名**（读屏会听到两个同名元素）。
为什么：层级文案的职责是「分节说范围、字段说取值」，重复等于浪费一行并制造 a11y 歧义。
改法：分节「工作步骤 / Work details」，字段「展示密度 / Display density」。

**F-23 | P2 | `settings/behavior.go:387`、`settings/settings.go:777`**
问题：「以下限额需点页脚「保存」后生效；上方的显示偏好即时生效。」用布局位置（页脚/上方）指代控件；「恢复限额默认 / Restore limit defaults」语序生硬。
为什么：布局一改说明就错；语序问题直接影响专业感。
改法：「以下限额点「保存」后生效。」/「These limits apply after you click Save.」；按钮「恢复默认值 / Restore defaults」。

**F-24 | P2 | `agentsurface.go:874` 等 ~40 处 a11y 拼接**
问题：`Label("打开成员 " + name)`——中文名字拼进去出现「打开成员 张三」的多余空格；另有 `Label("消息字号：")` 带尾冒号。
为什么：空格在中文里读起来像停顿；尾标点在朗读里无意义。
改法：加 `uikit.LabelWith(locale, zhPrefix, name, enPrefix)`，中文不加空格、英文加；Label 不带尾标点。

**F-25 | P1 | `settings/capabilities.go:253/260/258/265`、`automation/view.go:600`、`uikit/planreview.go:145`、`home/home.go:499`**
问题：中文标签/句子里直接留英文**普通名词**：「`Subagent 并发`」「`Team member 并发`」「一个 `Team member` 同时运行的最多子代理数」「任务触发时发给 `agent` 的提示词」「告诉 `agent` 计划需要怎么改…」「这是 `home` 的默认 Hero 空态」。
为什么：专有名词（ZeoKeel、MCP、OAuth、LSP、API Key、Base URL）保留拉丁文是对的；但 agent / home / Team member / Subagent 是普通名词，混排后中文读者要在两种语言间切换，且中英混排的空格规则无从适用。
改法：见 §3.2.7 的白名单规则——**产品名与协议名保留原文，概念词一律翻译**（agent→智能体、home→首页、Team member→团队成员、Subagent→子智能体）。

**F-26 | P2 | `settings/capabilities.go:39`（`CapabilitiesPageTitle`）**
问题：设置里曾有一个也叫「能力」的页面（内容是委派并发 / Shell / 审批超时 / 并行工具上限），现在已并入「行为与限额」的「运行限额」分节、`showHeader=false`，标题成了死文案；但常量名与字符串仍是「能力 / Capabilities」，与侧栏那个「能力」页同名。
为什么：维护者改文案时会把两者当成同一页；一旦有人把 `showHeader` 打回 `true`，设置里会冒出第二个「能力」标题。
改法：常量改「智能体限额 / Agent limits」（内容确实是运行上限），或直接删除未使用的 `showHeader` 分支。

---

## 3. 建议（可执行）

### 3.1 术语表（唯一叫法 + 迁移影响）

> 原则：**一个概念一个词**；复合词可用短形式（「成员」在团队面板内），但独立出现必须用全称；英文与中文一一对应，不允许一个中文词对应多个英文词。

| 概念 | 现有叫法（位置） | 推荐叫法 | 迁移影响 |
|---|---|---|---|
| 会话对象（`ChatSession`） | 会话（218：侧栏、归档、洞察、导入、toast）／对话（55：`home.go:432`、`session.go:75`、`view.go:799`、nav `screennav.go:28`、tab）／讨论（`view.go:1249`）／Chats（`sidebar.go:238`、`view.go:5072`）／Conversation（`view.go:799/822`、`export.go:108/193`、toast `会话已归档`） | **会话 / Session** | 中：约 55 处（对话）→会话，1 处（讨论）→会话；英：Chats 2、Conversation 4、Chat（对象义）3 → Session。代码 `session.Config.Title` 默认值同步改。**注意**：`view.go:884` TabChat 的「对话 / Chat」**保留**（视图标签），`screennav.go:28` 改为「会话 / Session」 |
| 会话内视图（tab） | 对话 / 轨迹 / 记忆 / 上下文 | **对话 / 轨迹 / 记忆 / 上下文**（Chat / Trajectory / Memory / Context） | 无（这是「对话」唯一保留处，需在 DESIGN 里写明例外） |
| 一轮交互 | 轮对话（`view.go:4246`）／对话回合（`view.go:4310`）／turns | **轮 / turn**（数值后缀「3 轮」；tooltip「对话轮次 / Turns」） | 2 处；无 en 影响 |
| 新会话动作 | 新对话（`sidebar.go:228`、`home.go:470`、`frame.go:283`、`view.go:1209`）／新建会话（a11y `sidebar.go:1526`）／New chat／New conversation（`commands_help.go:54`） | **新建会话 / New session** | 中 6 处、英 4 处；**会破坏快照/驱动测试的文本断言**（`devtools/ui-snapshot/main.go:981`、`ui-drive/steps_landing.go:28` 等 10+ 处断言「新对话」「开始新对话」），必须同 PR 更新 |
| 子智能体（上位） | 子智能体（19：chips、面板、切换器）／子代理（13：`toolnames.go:75`、`toolpreview.go:71`、`capabilities.go:258/265`、`agentsurface.go:1290`）／child agent（`agentsurface.go:1329`、`:2007`）／Child conversation（`:1223`） | **子智能体 / Subagent** | 中 13 处、英 3 处；工具友好名与预览行是关键路径 |
| 团队成员 | 队友（10：`agentsurface.go:767/1125/1288/2282/2338/2342/2397/2403`、kind 标签 `:1327`）／成员（4：`成员 · N`、`打开成员`、`0 名成员`）／Teammate | **团队成员 / Team member**（面板内短形式「成员 / Member」） | 中 11 处、英 4 处；`childAgentKindLabel` 的 kind 标签 + 空态 + 动作标签 + a11y |
| 团队 | 智能体团队 / 团队 / Agent team | **智能体团队 / Agent team**（chip、面板标题）；正文可简作「团队」 | 无 |
| 首席智能体 | 主智能体 / Lead agent | **主智能体 / Lead agent** | 无 |
| 工作区 | 工作区（124）／项目（1：`view.go:3119`「工作区（本会话项目）」）／Workspace | **工作区 / Workspace** | 1 处（删掉括号里的「本会话项目」） |
| 模型 | 模型 / Model | **模型 / Model** | 无 |
| 模型来源 | 服务商（9：设置、home）／提供方（1：`view.go:3813`）／Provider | **服务商 / Provider** | 1 处 |
| 审批（工具） | 审批（40）／待审批／Approval／Deny | **审批 / Approval**；动作 **允许 / 拒绝**（Allow / Deny） | 无（现状已一致） |
| 计划审阅动作 | 批准（`toolcards.go:435`、`planreview.go:172`、`interaction.go:314`） | **批准 / Approve**（只用于计划） | 需在文案规范里写死：审批≠批准 |
| 授权（OAuth） | 授权（52：连接器、需要授权、重新授权、撤销授权） | **授权 / Authorize**（保留） | 无（现状已一致，术语与审批分离是正确的） |
| 权限 | 权限模式／全局权限模式／权限／Permission | **权限模式 / Permission mode**；预设 **只读 / 工作区修改 / 所有权限** | **`automation/view.go:1098`「工作区可写」→「工作区修改」（P0）**；`（全局）`/`（全局跟随）`/`（跟随全局）` 三种后缀统一为 `（全局）` |
| 工具调用 | 工具调用（16）／步骤（11）／步工具调用／tool call／tool step | **工具调用 / Tool call**（单位「次」） | 中 3 处、英 3 处；「工作步骤」作为**设置项名**保留（见下行） |
| 步骤展示密度 | 工作步骤／工作步骤展示／Work details | 分节 **工作步骤 / Work details**，字段 **展示密度 / Display density** | 2 处 |
| 推理能力 | 思考（45）／推理（13）／推理等级／思考强度 | 轨迹 **思考 / Thinking**；能力 **推理等级 / Reasoning level** | 英 3 处（Reasoning→Thinking）、中 1 处（思考强度→推理等级） |
| 上下文 | 上下文 / Context | **上下文 / Context** | 无 |
| 用量单位 | tokens／tok／词元 | **tokens**（速度 `tokens/s`） | 中 2 处（词元）、英/中 3 处（tok） |
| 记忆 | 记忆 / Memory | **记忆 / Memory** | 无 |
| 能力页 | 能力（`plugins/view.go:62` 页面标题、`uikit/screennav.go:34` 导航、`plugins.go:46` 配置默认）／插件（1：`session/view.go:514` 返回目标）／Capabilities | **能力 / Capabilities** | 1 处（P0）。**注意同名冲突**：设置里曾有一个也叫「能力」的委派限额页，已并入「运行限额」，其 `CapabilitiesPageTitle` 是死文案（F-26） |
| 连接器 | 连接器（38）／插件／Connector | **连接器 / Connector** | 无 |
| 技能 | 技能 / Skill | **技能 / Skill** | 无 |
| 自动化 | 自动化（25）／Automation | **自动化 / Automation** | 无 |
| 触发规则 | 运行计划／计划／Schedule | **运行计划 / Schedule**（保留「运行」定语以区分代理计划） | 「安装计划」（`update.go:276`）→「准备安装」 |
| 代理计划 | 计划／计划审阅 / Plan review | **计划 / Plan** | 无 |
| 右侧栏 | 右侧栏（`frame.go:763/971` 的 Role 标签、`:869/871` 的开关、`:620` 的拖拽把手、`commands_help.go:58`）／检查器（`frame.go:148/150` 的读屏播报、`:785/787` 的窄窗停靠头、`:964/974` 的抽屉）／Right sidebar／Right panel（`:620`）／Inspector（其余） | **右侧栏 / Right sidebar** | 中 6 处（检查器→右侧栏）、英 5 处（Inspector/right panel→Right sidebar）。两类叫法各占一半，是本次最典型的「两种说法都通，但不能同时存在」 |
| 命令面板 | 快捷操作（`frame.go:852` 按钮）／命令面板（`commands_help.go`、`navigator`）／Quick actions | **命令面板 / Command palette**（按钮 label 与 tooltip 都可短，但必须是同一个词） | 中 1 处、英 1 处 |
| 输入框 | 输入框／会话输入框／Composer | **输入框 / Composer**；会话内的 a11y 名用「会话输入框 / Session composer」（限定语是合法的） | 无 |
| 队列 | 排队（动词当按钮）／排队消息／Queue | 动作 **排队发送 / Queue**；名词 **队列 / Queue** | 中 2 处 |
| 系统沙箱 | OS 沙箱／系统沙箱／OS 围栏 | **系统沙箱 / OS sandbox** | 中 3 处（OS 沙箱、OS 围栏 → 系统沙箱；英文保持 OS sandbox） |

### 3.2 文案规范 v1（写进 DESIGN.md 新小节）

#### 3.2.1 按钮动词

- **一个按钮一个动词**；动词必须描述真实效果。禁止：名词按钮（`附件` 除外，它是图标+名词的工具栏惯例）、`确定`（用具体动作）、只聚焦的主色按钮。
- 打开浮层/对话框的**菜单项**末尾加 `…`（现状已一致：`添加工作区…`、`打开模型设置…`）；**按钮不加** `…`（现状混用，`home.go` 的「配置模型 / 添加工作区」会打开对话框却不带 …，需统一为不加，并让菜单项保持带 …）。

| 场景 | 动词（zh / en） | 备注 |
|---|---|---|
| 发送消息 | 发送 / Send | 现有 |
| 停止生成 | 停止 / Stop；停止中… / Stopping… | 现有（好） |
| 生成中发送 | 排队发送 / Queue | 现「排队 / Queue」，名词用「队列」 |
| 重试 | 重试 / Retry | 现有 |
| 恢复会话/归档 | 恢复 / Restore | 现有 |
| 归档 | 归档 / Archive | 现有；归档页标题「归档 / Archived」是名词，OK |
| 删除数据 | 删除 / Delete | 记忆、会话、归档原文 |
| 移出列表 | 移除 / Remove | 模型、LSP、连接器、工作区 |
| 确认危险动作 | 动词 + 对象（「删除全部记忆」/「Delete all memories」） | 不用「确认删除」（自问自答） |
| 关闭浮层 | 关闭 / Close；忽略状态行用「关闭 / Dismiss」 | 现有 |
| 加载中按钮 | 动词中…（`选择中…`/`应用连接器…`） | 现有（好），补 `正在` 前缀统一见 3.2.5 |

#### 3.2.2 空态三句式

```
标题：陈述现状（名词短语，不带句号）      例：还没有会话 / No sessions yet
说明：一句「下一动作或原因」（可省）      例：发送第一条消息开始，或先完成下面的准备：
动作：一个（最多两个）真实按钮           例：新建会话 / 配置 API Key
```

- 禁止在说明里重复标题（现状反例：`开始新对话` + `还没有消息，开始一段新对话吧。`）。
- 禁止「点击 +」这类鼠标专属措辞（用「选择 + 添加工作区 / Select + to add a workspace」或直接给按钮）。
- 用词分层：**还没有 X**（用户还没做）／**没有匹配的 X**（搜索无果）／**X 未启用**（装配层缺少服务）。现状三种顺序都有（「未启用记忆后端」vs「记忆后端未启用」），统一为后者。

#### 3.2.3 错误三要素

```
<发生了什么>：<可读原因>。<下一步动作>
（原始错误放第二行或「复制详情」）
```
- 行内错误用 `uikit.ErrorLine`（已是 role=alert + 可选中）。
- 前缀统一「无法…」或「…失败」二选一：**动作失败**用「保存失败：」；**取不到东西**用「无法读取任务：」。现状混用（「读取任务失败：」「无法打开文件夹选择器：」）。
- 可恢复错误必须给动作：模型/密钥类→「配置模型 / 配置 API Key」；沙箱类→「查看权限模式」；网络类→「重试」。

#### 3.2.4 危险确认四要素

```
标题：动作 + 对象       删除自动化任务
正文：对象全名 + 规模 + 后果 + 可否撤销
     「每日构建检查」将被删除；它的 12 条运行历史一并删除，无法撤销。
按钮：动词 + 对象（危险色）           删除任务
取消：取消（Default 焦点）
```
正面例子（保留并推广）：`automation/view.go:1194`「“%s”将被删除；删除后可在通知里撤销。」、`workspace_add.go:160`「…文件夹不会被删除。」

#### 3.2.5 加载与进行中

- 统一「正在 + 动词 + …」：`正在读取文件…`；修正 `读取任务板…`→`正在读取任务板…`、`读取输出…`→`正在读取输出…`。
- Spinner 的 a11y 标签不带省略号（`正在汇总`/`Aggregating`），可见文本带省略号——现状 `正在汇总` 与 `正在汇总用量…` 两处不一致，统一。
- 长任务要有阶段词（压缩中、应用中、连接中），不要复用「加载中」。

#### 3.2.6 占位符

| 类型 | 规则 | 例 |
|---|---|---|
| 输入提示 | 动词短语 + `…`，两种语言同语域 | `输入消息…` / `Message ZeoKeel…` |
| 搜索 | `搜索 + 对象 + …` | `搜索会话…` |
| 示例值 | **`例如：值` / `e.g. value`**（现状 6 处带冒号、3 处不带、4 处 For example:） | `例如：deepseek-chat` / `e.g. deepseek-chat` |
| 可选字段 | **`可选` / `Optional`**；有行为时写行为（`留空使用动态注册`） | 现状「可留空/可选/留空…」三式 |
| 留空回退 | `留空 = <默认值>` | `留空回退输入价` → `留空则沿用输入价` |

#### 3.2.7 标点、空格、引号

- 中文句子用全角标点；`。` 结句，不用半角冒号连句（现状 `当前回复尚未结束：加入排队…`）。
- 中英混排：汉字与拉丁/数字之间加空格（`48 tok/s`、`3 次工具调用`）；**汉字之间不加空格**（拼接类文案的尾空格是 bug：`"前往 " + 中文标题` → `"前往" + 标题`）。
- **中英混排白名单**：只有**产品名**（ZeoKeel、DSH）与**协议/技术专名**（MCP、OAuth、PKCE、LSP、API Key、Base URL、Cron、JSON、Markdown、tokens 作为单位）可保留拉丁文；**概念词必须翻译**（agent→智能体、home→首页、Team member→团队成员、Subagent→子智能体、session→会话）。违反现状见 F-25。
- 引号：**中文与英文统一使用弯引号 “ ”（U+201C/U+201D）**——中文侧按 GB/T 15834 大陆规范，也是 macOS/飞书/Claude 等中文界面的做法。现状中文以 `「」` 为主（73 处）、`“”` 仅 7 处、`『』` 2 处（`view.go:2370`、`behavior.go:387`），需机械统一；英文侧禁止直引号 `"`（`toasts.go:592-593`、`approval.go:280` 等处）。**备选方案**（若团队更偏好 `「」` 的视觉区分度）：保留 `「」`，把上述 7 处 `“”` 改为 `「」`，英文统一 `“”`。两案**只选一个**并写进 DESIGN。
- 省略号统一 `…`（现状 336 处 `…` vs 1 处 `...`，那 1 处在路径示例 `go test ./...` 里，正确）。
- 定义式用 `＝` 且只在「X＝Y」定义列表里用（现状一致，保留）；不要与 `：` 混用在同一句。
- 分隔符：同一视觉带内统一 `·`（现有大面积使用），英文 ` · `。

#### 3.2.8 数字、单位、量词

| 项 | 规则 | 例 |
|---|---|---|
| 用量 | 单位 `tokens`（不缩写）；速度 `tokens/s` | `42K/128K tokens`、`62 tokens/s` |
| 百分比 | `86%`（半角，紧贴数字） | 现有 |
| 量词 | 会话「个」、轮次「轮」、工具调用「次」、消息「条」、成员「名」、审批「个」、连接器「个」、任务「个」 | 现状「条已结束（成员）」「步工具调用」错 |
| 计数格式 | 数字与量词间空格（`3 次`）；列表头用 `成员 · 3` 时数字前不加量词 | 现状 |
| 成本 | `$0.42`（`formatCostUSD`），标注「估算 / Estimated」 | 现有（好） |

#### 3.2.9 时间

- 绝对：zh `2026-11-18 09:00`；en `Nov 18, 2026 9:00 AM`（现有 `FormatDateTime`，保留）。
- **新增相对**：`<1 分钟 刚刚 / just now`；`<60 分钟 N 分钟前 / N min ago`；`当天 N 小时前 / N hr ago`；`昨天 09:00 / Yesterday 9:00 AM`；`7 天内 周三 09:00 / Wed 9:00 AM`；`更早 2026-11-12 / Nov 12`。
- 用在会话列表、最近会话、运行历史；tooltip 与「下次运行」用绝对时间。
- 时长：`353ms` / `2.5s` / `1m 20s` — 现有 `formatUsageDuration`，补「用时」与 en `Took` 的同位置一致性。

#### 3.2.10 快捷键

- **唯一显示形式 = `uikit.FormatShortcut`**：修饰键用平台词（`Cmd` on macOS / `Ctrl` elsewhere），`+` 连接且**两侧不加空格**，键名大写（`Ctrl+Shift+Space`）。
- **不用 ⌘⇧ 符号**（理由见 §4.2）。
- 面向用户文案禁止手写组合键；需要双语同句时也走格式化（`Cmd/Ctrl` 只在「无法得知平台」的极端场景使用，且必须是无空格形式）。
- `/help` 键盘表是**权威清单**：任何在 UI 里承诺的键都必须在这里有一行（现缺 `Ctrl+Enter`）。

#### 3.2.11 英文（en）附加规则

- 句子大小写：状态条/元数据整句用句首大写，段内并列项统一小写（现状 `Cache hit 86% · context 67% left` 混用）。
- 单复数走既有 `sessionCountLabel`（1 turn / 2 turns），不要新增例外。
- 英文长度预算：按钮标签 ≤ 24 字符。`Always allow in this session`（27）是唯一超标项，**不要**为了长度改成 `Always allow here`（丢掉「本会话」这一关键限定，属语义降级）；处理办法见 §4.3。
- 术语大小写：`Provider`、`Session`、`Subagent`、`Connector`、`Reasoning level` 在标题/label 中大写首字母；正文小写。

### 3.3 层级文案

| 层级 | 规则 | 现状 |
|---|---|---|
| 顶带标题（TypeM/600） | 名词短语 = 屏名，与导航项**同名** | 能力页违反（F-01） |
| 设置页 H1（TypeL/600） | = 导航项名，副标题只在能补充「这页管什么」时使用 | 16 处 `PageHeader` 调用里 14 处副标题为空（只有 LSP「语言服务：为智能体提供代码诊断与补全。」与能力限额页有）——**可以空，但不要用正文长段代替副标题**（F-07）。LSP 那句是本产品最标准的副标题范例 |
| 分节 H2（TypeM/600） | 说范围，不复述字段 | `工作步骤/工作步骤展示` 违反（F-22） |
| 字段 label | 名词短语，不带尾标点；`ui.Field` 的 Description 说「取值含义」 | 少量 Label 带 `：`（F-24） |
| 对话框 | 标题=动作+对象；副标题=后果；正文=细节 | 审批卡、删除框现状较好（§3.2.4） |
| 空态 | 见 §3.2.2 | 一半好一半弱（F-12） |

### 3.4 a11y 朗读规则

1. **Label 不带尾标点/尾空格**；拼接用 `uikit.LabelWith`（中文无空格，英文空格）。
2. **同一视图内不出现两个同名元素**（分节 vs 字段同名是最常见的 F-22）。
3. 状态词必须同时有文字载体：侧栏运行态已有「运行中/等待回复/未读回复」文字 + 图标（合规，DESIGN §5.2 已规定）；toast 的等级词改为**仅 a11y**（F-14）。
4. 错误行已是 `RoleAlert`（好）；危险确认的正文必须是**元素级描述**，不能只有「此操作无法撤销」。
5. `Role`/`Level` 与文案语义一致：顶带 `RoleBanner`、侧栏 `RoleComplementary`、分节 `Level(2)`（现状正确）。

### 3.5 迁移批次与写集建议（供 Lead 排期）

| 批次 | 内容 | 建议写集（互不重叠） | 验收 |
|---|---|---|---|
| B0（先做，1 个 PR） | 术语表落地：会话/对话、子智能体/子代理/队友、能力/插件、右侧栏/检查器、服务商/提供方、工作区/项目、工作区可写/工作区修改 | `internal/uikit/uikit.go`+`screennav.go`（共享词表）、`internal/ui/session/*.go`、`internal/ui/sidebar/*.go`、`internal/ui/home/*.go`、`internal/ui/settings/{behavior,modelcatalog,capabilities}.go`、`internal/ui/automation/view.go`、`internal/ui/plugins/*.go`、`internal/uiroot/frame.go` | 本报告 §3.1 每个概念的 grep 计数归零（除声明的例外）；`go test ./internal/ui/... ./internal/uiroot -count=1` |
| B1 | P0 内容错误：F-01 返回名、F-04 重启矛盾、F-05 开发者文案、F-06 权限说明单一来源、F-07 元叙述、F-08 空态 | `internal/ui/session/view.go`、`internal/ui/session/agentsurface.go`、`internal/ui/settings/behavior.go`、`internal/ui/plugins/connectors_section.go`、`internal/ui/home/home.go`、`internal/ui/welcome/welcome.go`、新增 `internal/uikit/permission_help.go`（单一来源） | 权限说明只剩一份可 grep 的常量；PNG 复核 `p2-behavior.png` 首屏 |
| B2 | 单位/量词/时间：F-09、F-16、F-19 | `internal/ui/session/view.go`（状态条）、`internal/ui/insights/view.go`、`internal/ui/sidebar/sidebar.go`（相对时间）、`internal/uikit/format.go`（新增 `FormatRelativeTime`） | 状态条同一行只有一种单位词；新增帧测试覆盖相对时间分档 |
| B3 | 组合键与帮助：F-10 | `internal/ui/session/{view,commands_help}.go`、`internal/ui/settings/hotkey_settings.go`；可选 `internal/uiroot/frame.go:1506`（让 `commandShortcut` 直接返回格式化结果，去掉对调用方的隐式依赖） | `grep -rn 'Cmd/Ctrl' internal/ui`（排除注释与 `parseShortcutChord` 的输入别名）归零；`/help` 含 Ctrl+Enter 行 |
| B4 | 标点/引号/混排：F-17、F-24、F-25 | 全仓机械替换（引号、尾空格、`LabelWith`） | `grep -c '「'` 与 `“` 只剩一套；无 `"…"+中文` 的空格拼接 |
| B5 | 错误与确认模板：F-18、F-20、F-23 | `internal/uikit/uikit.go`（ErrorLine 增加可选 action/detail）、各设置页确认框 | 每个危险确认都含数量/对象/不可逆声明 |

> 注意：B0 会同时打破 `internal/devtools/ui-snapshot`、`ui-drive` 与各模块 `*_test.go` 的文本断言，**必须与文案改动同一个 PR** 更新断言，否则测试保护的是旧文案（这是本次迁移最主要的隐性成本）。

---

## 4. 风险与取舍

### 4.1 改造量的取舍

- 「会话 vs 对话」选「会话」意味着要改 55 处中文与 9 处英文，但反向（选「对话」）要改 218 处，且会与「归档会话」「未命名会话」「会话操作」「会话搜索」「会话输入框」等既有词族冲突。选「会话」。
- 术语迁移**必然打破**快照与驱动测试的文本断言（`internal/devtools/ui-snapshot`、`ui-drive`、各模块 `*_test.go` 里的 `assertTexts`）。这不是额外风险而是**验收清单**：迁移 PR 里必须同步改断言，否则测试是在保护旧文案。

### 4.2 为什么不用 ⌘⇧ 符号

1. 现有 `uikit.FormatShortcut` 已按 GOOS 输出 `Cmd`/`Ctrl` 文本，一处改动即可全局一致；符号需要第二张映射表 + 平台分支。
2. kbd 微标签用 11/12 DIP 等宽字体（`MonoText(TypeXXXS)` / `MonoFamily()+TypeXXS`），字体栈不保证覆盖 ⌘⇧⌥⌃，缺字会渲染成 tofu。
3. 文本形式可在帮助/文档/搜索里被 grep 与朗读（读屏把 ⌘ 读成「command」的兼容性也不如 `Cmd`）。
> 若未来要做，唯一入口仍是 `FormatShortcut`（加一个 `symbols bool`），不要在文案里手写。

### 4.3 英文长度

- 审批三按钮 `Deny / Always allow in this session / Allow` 在 1024 窄窗 + 英文下是本产品最长的按钮串。**不建议**压缩语义（`Always allow here` 会丢失「本会话」这一关键限定）。替代：footer 允许换行到两行，或把中间项降级为菜单（`允许 ▾`），但后者改变了审批的「三个并列决定」结构，需 Lead 裁定。
- 长说明（权限/沙箱、cron 语法、去重规则）应改为「一句摘要 + 可展开详情」，而不是把 150 字塞进 `Description`（现状 `denied_globs.go:47`、`capabilities.go:292`、`automation/view.go:611` 都是 100+ 字单段）。

### 4.4 不建议做的「减法」

- 不删状态条信息项（AGENTS §3.17）；只统一单位与量词。
- 不删团队/子智能体入口；只统一名词。
- 不为了「统一」把空态统一成更弱的形态——统一到 `EmptyStateBlock`，允许 title+hint 两行。

### 4.5 与 `B-spec-v2.md` 的冲突与衔接（Lead 需裁定）

写报告时 Lead 的 `B-spec-v2.md`（00:10）已在仓里，逐条对照后有四件事必须先裁定，否则 C 阶段会同时实现两套文案：

1. **`B-spec-v2.md:116`「主操作『新对话』」与术语表冲突。** B-spec 同时用「新对话」（按钮）和「会话行」（`B-spec-v2.md:119`）——这正是本次 P0（F-02）要消灭的「按钮叫 A、列表叫 B」。两条路选一：
   - **A（本报告推荐）**：按钮「新建会话 / New session」，全文 `会话`；代价 55 处中文 + 9 处英文 + 快照断言。
   - **B（保持 B-spec 的短标签）**：按钮「新对话」，则**必须**把对象名也统一成「对话」（218 处），并同步 `归档对话`、`未命名对话`、`对话操作`、`对话搜索`——工作量更大，且与 DSH/Codex 的「会话」词族分叉。**不建议**。
   - **不可接受**：两者并存（= 现状）。
2. **`⌘N` / `⌘↵` 符号（`B-spec-v2.md:116/146/185`）与本报告 §4.2 冲突，且有一个功能性 bug**：`⌘` 只在 macOS 存在，Windows/Linux 上同一个 kbd 位应显示 `Ctrl+N` / `Ctrl+Enter`。若 B-spec 写死 `⌘`，C 阶段会做出在 Windows 上「按不出来的键」。建议 B-spec 改成「kbd 位显示 `FormatShortcut(chord)` 的结果」，并在文档里用 `Cmd+N`（平台词）而不是 `⌘N` 表达。若 Lead 坚持 `⌘` 符号，则必须同时给出非 macOS 的替代符号（`Ctrl+`），并接受等宽字体缺字风险（见 §4.2）。
3. **审批 footer 新结构的文案需要在 C 阶段前定稿**（`B-spec-v2.md:146`）。按 §3.1 术语表，建议的最终标签：
   | 位 | zh | en | 备注 |
   |---|---|---|---|
   | 次按钮 | 拒绝 | Deny | 现有 |
   | 主按钮 | **允许一次** | **Allow once** | 比现有「允许」更好：把范围写进动词，与「本会话内始终允许」形成对照 |
   | 菜单 | 本会话内始终允许 | Always allow in this session | 沿用现有串（B-spec 写「本会话始终允许」，少一个「内」，需与设置页/帮助文案同步） |
   | 菜单 | 总是允许（写入设置） | Always allow (saves to settings) | 这句是新增的能力，必须说明「写入设置」这个副作用 |
   | 菜单 | 查看规则 | View rules | |
   | 范围提示 | 仅本次 / 本会话 | This call only / This session | B-spec 说「范围以 12 tertiary 显示在主按钮左侧」，但没给文案 |
4. **`B-spec-v2.md:139` 状态条改成三段 + `详情` popover，会顺手解决 F-09**：strip 里只留 `42K/128K`（不带单位），单位词移到 popover 内的「轮次 / 步数 / 缓存命中 / 剩余」行，于是在同一行不会再出现 `tok` 与 `tokens` 打架。要求：**popover 内统一 `tokens`**，且 `详情` 这个按钮名与 popover 标题一致（建议 popover 标题「用量详情 / Usage details」，按钮「详情 / Details」）。

> 除以上四条，B-spec 与本次审计无冲突：它已经在用「会话行」「子智能体 chip」「检查器」，前两个与术语表一致，第三个需按 §3.1 统一为「右侧栏」。

---

## 5. 附：证据清单

### 5.1 命令（可复现）

```bash
# 文案规模
grep -rn --include=*.go -P '"[^"]*[\x{4e00}-\x{9fff}][^"]*"' internal/ui internal/uiroot internal/uikit | grep -v _test.go | wc -l   # 3050
grep -rn --include=*.go -c 'st\.tr(' internal/ui | grep -v _test | awk -F: '{s+=$2} END{print s}'                              # 1468
# 术语计数
for w in 会话 对话 讨论 子智能体 子代理 队友 成员 工作区 项目 服务商 提供方 审批 授权 工具调用 步骤 思考 推理 上下文 记忆 连接器 插件 能力 自动化; do ...
# 英文侧
grep -rn --include=*.go -E '"(Chats|Sessions|Conversation|Chat)"' internal/ui internal/uiroot internal/uikit | grep -v _test.go
# 标点
grep -rn --include=*.go -o '「' internal/ui internal/uiroot internal/uikit | grep -v _test.go | wc -l   # 73
grep -rn --include=*.go -o '“' internal/ui internal/uiroot internal/uikit | grep -v _test.go | wc -l   # 7
grep -rn --include=*.go -o '…' internal/ui internal/uiroot internal/uikit | grep -v _test.go | wc -l   # 336
# 组合键
grep -rn --include=*.go -oP 'Cmd/Ctrl[^"，。"）]*' internal/ui internal/uikit internal/uiroot | grep -v _test.go
```

### 5.2 关键源码位置

| 主题 | 位置 |
|---|---|
| 本地化机制 | `internal/uikit/locale.go:12`（`T`）、`:26`（`FormatDateTime`）、`:44`（`FormatClock`） |
| 组合键格式化 | `internal/uikit/format.go:77`（`FormatShortcut`）、`internal/uiroot/shortcuts.go:72`（`parseShortcutChord`） |
| 导航单一来源 | `internal/uikit/screennav.go:23`（`ScreenNavSpec`） |
| 语言表 | `internal/uiroot/locale.go:30-40` |
| 快捷键盘表 | `internal/ui/session/commands_help.go:48`（`helpShortcuts`） |
| 空态骨架 | `internal/uikit/uikit.go:125`（`EmptyState`）、`:132`（`EmptyStateBlock`）、`:396`（`EmptyStateHero`） |
| 页头/分节 | `internal/uikit/uikit.go:22`（`PageHeader`）、`:37`（`Section`）、`:68`（`SectionAction`） |
| 错误行 | `internal/uikit/uikit.go:152`（`ErrorLine`） |
| 危险确认 | `internal/uikit/uikit.go:347`（`ConfirmDanger`） |
| 审批卡文案 | `internal/uikit/approval.go:230-290` |
| 运行态/队列 | `internal/uikit/runstatus.go:106-433` |
| 思考轨迹 | `internal/uikit/reasoning.go:80-140` |
| toast 短语表 | `internal/ui/toasts/toasts.go:531-598` |
| 状态条 | `internal/ui/session/view.go:4234-4330` |

### 5.3 PNG

见 §1.3 表；全部位于 `.cache/ui-conformance/`（119 张，manifest `generated: 2026-11-17T23:56:05+08:00`，head `e9a0d6b`，与当前工作树一致）。

### 5.4 结论：哪些已经很好，别改坏

- 审批卡正文（`允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。`）——**后果 + 限定**写全了，是全文最好的错误/风险文案模板。
- 自动化删除确认「删除后可在通知里撤销」——**给了退路**。
- 工作区移除「文件夹不会被删除」——**说明了不会发生什么**，比只说「将被移除」强得多。
- 首跑引导「尚未配置 API Key（使用本地模型服务时可忽略）」——**给了例外条件**。
- 输入框排队提示「当前回复尚未结束：加入排队，本轮结束后自动发送」——**解释了为什么**（仅需改标点与名词）。
- 连接器错误映射（`plugins/view.go:100-116`）：「鉴权失败（请检查访问令牌）」「连接超时（服务器未及时响应）」——**原因 + 下一步**写在同一行，是错误文案的最佳模板，应抽成 `uikit` 的通用做法推广。
- 空态三句式、`EmptyStateBlock`、`ErrorLine`（role=alert+可选中）、审批动词不中性化——结构对了，只差逐个字符串对齐。