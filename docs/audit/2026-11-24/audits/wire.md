# ZeoKeel 设置项「是否真生效」独立验收（wire2）

- 验收时间：2026-11-25 01:35–02:0x（北京时间，UTC+8）
- 验收对象：`web/src` 的 5 组设置项（工作步骤展示四档 / 收起时机 / 两个快捷键 / 应用内通知 / 归档会话显示）
- 验收方式：**真实网关 + 真 Chromium（Playwright）+ 真模型流**（本地假模型夹具，SSE 工具调用走网关真实工具链与审批链），不抽样、逐条跑主路径与失败/边界路径
- 代码改动：**未改 `web/src`**（`git status --porcelain web/src` 为空）

## 0. 版本自证（先证明"我验的是哪份代码"）

| 项 | 值 |
|---|---|
| 参考配置 `data-wire2/gateway.json` | port 9960 / bundleSha256 `028d3eab7402b7808a2afecde30b33005b5a2606852305e6ba2726884026203d` / builtAt 2026-11-24T17:34:10Z |
| 本次实例 `data-wire4/gateway.json` | port **9980** / epoch `e103b33b0202a3f7` / pid 7 / **bundleSha256 `028d3eab7402…6203d`（与参考一致）** / builtAt 2026-11-24T17:34:10Z |
| 二进制 | `/data/zeokeel/.cache/bin/zeokeel-wire2`，sha256 `9d32da95d553115f…7c6c3125`，mtime 11-25 01:34 |
| 启动自检 | `GET /` → **200**；`/favicon.svg` → **200**；`/api/capabilities`（无 Authorization）→ **401** |
| 运行中的客户端 bundle | `/assets/index-D-jJ4cus.js`，sha256 `c40ee83ebfc3c52a…3e7b379`，内含修复标记 `concise-summary`(1)、`next-input`(2)、`CmdOrCtrl`(1)、`palette-open`(1)、`toggle-sidebar`(1) |
| 对应源码 | 提交 `2dc7c5f feat(web): the work-steps and shortcut settings finally do something`（bundle 于该提交前 45 秒从同一工作树构建） |

> 说明：验收期间仓库又有 3 个提交（`0b7f29c`/`0e73c5c`/`2a9110e`），`git diff --stat 2dc7c5f..HEAD` 只动了 `web/src/design/global.css` 与 `web/src/shell/sidebar.tsx`（会话行菜单焦点/危险色）。与本轮 5 组设置的数据通路无关：`sidebar.tsx` 的 `showArchived` 过滤行在这些提交中未变（diff 中无该行改动），`thought-steps/keyboard/settings ia` 完全未动。运行中二进制仍是 `028d3eab…` 那份，因此本报告结论对 HEAD 同样成立（仅会话行菜单的配色/焦点外观更新）。

夹具：`model2.mjs`（端口 9972，OpenAI 兼容 SSE，按用户消息里的标记选脚本：`TWOSTEP`=两次成功 `read_file`、`FAILSTEP`=一次失败 `read_file`、`APPROVAL`=一次 `web_fetch`（审批停住）、`MIXED`=一成功一失败、`SLOW`=慢流、其它=纯文本）。工具调用由网关真实执行（`read_file` 属 workspace 自动放行，`web_fetch` 属审批停住家族）。

---

## 1. 总表

| # | 验收项 | 结果 | 一句话 |
|---|---|---|---|
| 1 | `ui.workSteps.mode` 四档 | **PASS** | 简洁=一行摘要且无成功步骤标题；标准=步骤标题但内容收起；详细/完全展开=内容展开（两档行为完全相同，见 F2） |
| 2 | `ui.workSteps.collapse` 收起时机 | **FAIL** | `completion` 回合结束即收起 ✅；`next-input` 回合结束保持展开 ✅，但**下一条消息发出后（乃至新回合结束、刷新后）都不收起** ❌ |
| 3 | 两个快捷键（改键/旧键失效/非法回落/刷新保持） | **PASS（主声称）** | Mod+K→Mod+J 生效、旧键失效、非法值回落默认、刷新保持、跨 tab 实时生效；**边界缺陷 F3（裸键吞输入）、F4（撞键静默丢动作）** |
| 4 | `ui.notifications` 关闭不再弹收据、打开恢复 | **PASS** | 审批等待 toast：ON 有 / OFF 无（6.1s 内 0 条）/ 再 ON 又有；完成 toast 同样受开关控制 |
| 5 | `ui.sessions.showArchived` 控制侧栏 | **PASS** | 默认隐藏 → 开启出现（跨 tab 实时）→ 关闭再隐藏；归档行菜单提供「取消归档」 |
| 6 | 设置说明文字与实际行为一致 | **FAIL** | 「详细/完全展开」无差别；「下次有新消息」永不收起；两行均无 help 文案；非法/未知值在 UI 上无任何提示（含"全不选中"） |
| 7 | 回归：其余字段仍可保存；错误回合不受影响 | **PASS** | 字号行保存并落地（`--zk-chat-font-size:17px`，清空后移除）；compact 下失败步骤仍可见；其余开关/文本行全部往返成功 |
| 8 | 边界：失败步骤 / 无步骤 / 流式中 / 拒绝审批 / 混合 | **PASS** | 只有失败步时失败步可见且展开，无摘要；无步骤时不出现空摘要；流式中列运行步、结算后变摘要；拒绝审批显示失败步；混合回合只显示失败步（成功步完全消失，见 F6 建议） |

**总裁定：不通过。** 5 项修复声称中 4 项成立，`ui.workSteps.collapse=next-input` 的"下次有新消息才收起"语义**没有实现**（当前等价于"始终展开"）；另有 4 处一致性/边界缺陷，其中 2 处（裸键吞输入、撞键丢动作）会让用户"改完快捷键后输入或功能坏掉"。

---

## 2. 逐条证据

### 2.1 `ui.workSteps.mode` 四档（PASS）

同一会话（`sess-1795541879995006138`：一轮 2 个成功的 `read_file` 步骤，已结算），逐档在 **设置页改值 → 保存 → 刷新会话 → 测量 DOM**：

| 档位 | `settings.json` 落盘 | `.zk-thought__header` 文本数组 | 可见 `.zk-thought__content` | 摘要行 |
|---|---|---|---|---|
| 简洁 compact | `"compact"` | `["已完成 2 个步骤"]` | 0 | 有（check 图标，`collapsible:false`） |
| 标准 standard | `"standard"` | `["读取文件read_file/…/alpha.txt · 完成","读取文件read_file/…/beta.txt · 完成"]` | 0 | 无 |
| 详细 detailed | `"detailed"` | 同上两条步骤标题 | **2** | 无 |
| 完全展开 verbose | `"verbose"` | 同上两条步骤标题 | **2** | 无 |

- 简洁档的 `.zk-thought__header` 只有摘要一行，成功步骤标题在 DOM 里**完全不存在**（不是隐藏，而是根本没渲染）→ 与"把已完成 N 个步骤合并成一行、不列成功步骤"一致。
- 摘要文案带数量：2 个步骤 → "已完成 2 个步骤"。
- 跨 tab 实时性：A 页开着已结算会话，B 页把 mode 改 compact → A 页**不刷新**即变成摘要行；改 detailed → 立即两条内容展开（`wire2-p10-live.json`，截图 `audit-wire-49/50`）。
- 行缺失（干净 settings.json，无 `ui.workSteps.*`）时渲染等同 standard（2 步骤标题、内容收起），见 `wire2-p11-defaults.json`。

证据：`audit-wire-01-mode-{compact,standard,detailed,verbose}-settings.png`（设置帧，含单选选中态与"已保存"）、`audit-wire-02-mode-{...}.png`（同一回合四档渲染）、`wire2-p1-modes.json`。

### 2.2 收起时机（FAIL）

`data-wire4` 会话 `sess-1795542049605264448`（3 轮：两次审批停住的 `web_fetch` + 一次纯文本）。逐行统计可见 `.zk-thought__content` 数量：

| 步骤 | collapse=completion | collapse=next-input |
|---|---|---|
| 回合 1 运行中（审批停住，步骤 running） | 内容 **1**（运行步自动展开） | — |
| 回合 1 结算后 | 内容 **0**（已收起）✅ | 内容 **1**（保持展开）✅ |
| 回合 1 结算后刷新页面 | — | 内容 **1** |
| 发出第 2 条消息、第 2 轮运行中（回合 1 已结算） | — | 回合 1 内容仍 **1** ❌（应立即收起） |
| 第 2 轮结算后 | — | 回合 1 **1**、回合 2 **1** ❌ |
| 发出第 3 条消息（纯文本）、第 3 轮结算后 | — | 回合 1 **1**、回合 2 **1** ❌ |
| 再刷新 | — | 同上，仍 **1/1** ❌ |

结论：`completion` 档正确；`next-input` 档只做到"结算后不收起"，**没有任何"下一条消息 → 收起"的机制**，实际语义 = 永久展开（与"回答结束后"的唯一差别就是默认档会收起）。截图 `audit-wire-03/04`（completion 运行中→结算）与 `audit-wire-05/06/07/08`（next-input 刷新后、第 2 轮、第 3 轮后、再刷新）。原始数据 `wire2-p2-collapse.json`。

### 2.3 快捷键（主声称 PASS，边界 FAIL）

| 场景 | 结果 | 证据 |
|---|---|---|
| 默认（行缺失/空串）Mod+K | 打开命令面板 | `wire2-p11-defaults.json:shortcuts.paletteK=true`、`audit-wire-53`；空串同效 `p3 illegal-empty`、`p8 default-mod-k-while-typing` |
| 默认 Mod+B | 侧栏 264px→64px，再按恢复 264px | `wire2-p11-defaults.json:shortcuts.after="true"`（行缺失的干净实例） |
| 别名 Mod+Shift+P | 仍打开面板 —— 该别名为硬编码（`command-palette.tsx:110-118` 的 `palette-open-alias`），与配置行无关 | `wire2-p3-shortcuts.json:defaults-absent.aliasModShiftP=true`（该次运行的两行已是 Mod+J，别名仍生效） |
| 改 `Mod+J` 保存后（**同一文档，未刷新**） | Mod+K 不打开（keydown 未被 preventDefault）、Mod+J 打开 | `wire2-p3…:rebind-save-without-reload` |
| 刷新后 | 仍 Mod+J 生效、Mod+K 失效 | `rebind-after-reload`，`audit-wire-12/13` |
| 改 `Mod+Shift+B` | Mod+B 不再折叠、Mod+Shift+B 折叠/展开 | `audit-wire-14`（data-collapsed true→width 64） |
| 非法 `Mod+` / `K+J` / `乱写` / 空串 | 全部回落默认：Mod+K 仍打开、Mod+J 不再打开 | `illegal-*` 四例，`audit-wire-15` |
| 输入框聚焦时按 Mod+K | 面板打开、草稿不丢、焦点进入面板输入框 | `audit-wire-41`、`wire2-p8-boundary.json:default-mod-k-while-typing`（paletteOpen=true、草稿保留、焦点在面板 INPUT；判断：Mod 组合键在输入框里触发是合理的，面板不会吞字符） |
| 输入框聚焦时按 Mod+B | 侧栏照常折叠（264→64），草稿不丢 | `wire2-p8-boundary.json:mod-b-while-typing`、`audit-wire-42`（判断：同为 Mod 组合键，合理） |
| 恢复 `Mod+K`/`Mod+B` | 一切回到默认 | `restore-defaults`、`audit-wire-18` |

边界缺陷（详见 F3/F4）：
- **裸键 `K`** 被解析成合法绑定，在输入框里打第一个 `k` 就被 `preventDefault` 弹面板，正文只剩 `k`（`audit-wire-16`，`p8` 中 `keys[0].prevented=true`、`textarea="k"`）。
- **两行同键**（palette=`Mod+J`、sidebar=`Mod+J`）：`Mod+J` 只折叠侧栏，命令面板**永远打不开**，无任何提示（`audit-wire-17`，`p3 collision`：`paletteOpen=false, sidebar=false→true`）。

### 2.4 应用内通知（PASS）

| 开关 | 审批等待 toast | 完成 toast | 审批卡 |
|---|---|---|---|
| ON（行缺失=on / 显式 `"true"`） | **有**：「审批等待：web_fetch」，首次出现 3ms，rect 384×66 @ (1032,24)，opacity 1 | **有**：「会话「PLAIN completion toast probe」已完成」，结算后 176ms | 有 |
| OFF（`"false"`） | **无**（6.114s 轮询 0 条），审批卡正常出现（收件箱角标 1） | **无**（6.237s 0 条） | 有 |
| 再 ON（不刷新，实时） | **有**：「审批等待：web_fetch」，2ms | — | 有 |

截图：`audit-wire-20b-toast-on-crisp`（toast 不透明帧）、`audit-wire-20/22`（ON/恢复）、`audit-wire-21`（OFF：只有审批卡、无 toast）、`audit-wire-48-completion-toast-{on,off}`、`wire2-p4-notifications.json`、`wire2-p4c-completion-toast.json`。

### 2.5 归档会话显示（PASS）

会话 `sess-1795542365770127751`（标题 `ARCHME plain marker turn`）经行菜单「归档」：

| 状态 | 侧栏行数 | 该行 |
|---|---|---|
| 归档前 / 默认（`showArchived` 缺失=false） | 7 | 有 |
| 归档后（默认隐藏） | 6 | **无** ✅ |
| 开启 `ui.sessions.showArchived="true"` 并保存 | 7 | **有** ✅ |
| 关闭再保存 | 6 | **无** ✅ |

另：归档行的菜单项为「取消归档」（可逆）`["重命名","置顶","取消归档","删除"]`（`wire2-p5-archived.json: archivedMenu`、`audit-wire-27`）。跨 tab 实时：B 页开关保存后 A 页侧栏即时增减（`p10-live`：15→16→15，新增行正是 ARCHME）。截图 `audit-wire-23/24/25/26/28/51`。

### 2.6 说明文字一致性（FAIL）

设置页实测（`wire2-p6-copy.json`，DOM 文本）：

| 行 | label | help | 选项 | 实际行为 |
|---|---|---|---|---|
| `ui.workSteps.mode` | 工作步骤展示 | **无** | 简洁/标准/详细/完全展开 | 简洁、标准成立；**详细与完全展开渲染完全相同**（headers 2 / contents 2） |
| `ui.workSteps.collapse` | 收起时机 | **无** | 回答结束后/下次有新消息 | 「下次有新消息」**没有任何"新消息"触发点**，永不收起 |
| `ui.shortcut.commandPalette` | 命令面板快捷键 | **无**（唯一提示是空值时的 placeholder `Mod+K`） | — | 非法值照样保存，行为静默回落，无校验提示 |
| `ui.shortcut.toggleSidebar` | 折叠侧栏快捷键 | **无**（placeholder `Mod+B`） | — | 同上；与另一行撞键无提示 |
| `ui.sessions.showArchived` | 在侧栏显示已归档会话 | 无 | — | 与行为一致 ✅ |
| `ui.notifications` | 应用内通知 | 无 | — | 与行为一致（且行缺失按 on 显示 switch=true）✅ |

另：行缺失或存储非法值时，单选控件**一个都不选中**（`audit-wire-54-defaults-settings`、`audit-wire-45/47`），而实际行为是 标准/回答结束后 —— 用户无法从 UI 得知"当前生效的是哪一档"。对比同一页的「对话模式」行专门为此加了 `defaultValue: 'standard'`（`ia.ts:184-191` 注释写明"三个都不选中暗示未决定，不是真实状态"），这两行没有跟进。

### 2.7 回归（PASS）

- 其他字段仍可保存：`ui.chatFontSize=17` → `settings.json` 落 `"17"`、`--zk-chat-font-size:17px` 生效（`audit-wire-39`）；清空后行归 `""`、CSS 变量移除。`ui.notifications`、`ui.sessions.showArchived`、两个快捷键行均完成"改→存→刷新→读回"往返。
- `ui.workSteps.*` 改动不影响错误回合：`FAILSTEP` 回合在 compact 下失败步骤仍完整可见（标题含"失败"、内容 1、无摘要）；standard 下同样可见（`audit-wire-32/38`）。

### 2.8 边界与失败路径（PASS）

| 边界 | 结果 |
|---|---|
| compact + 回合只有失败步骤 | 失败步骤可见 + 内容展开（1），**无**摘要行 ✅ `audit-wire-32` |
| compact + 回合无步骤 | 完全没有 `.zk-thought`，无"已完成 0 个步骤"之类空行 ✅ `audit-wire-33` |
| compact + 流式中（审批停住） | 运行中的步骤照常列出（不允许摘要盖住正在跑的东西）✅ `audit-wire-34`；允许后结算 → 变"已完成 1 个步骤" `audit-wire-35` |
| compact + 用户拒绝审批 | 失败步骤（`rig: tool call denied by approval gate`）可见且展开，无摘要 ✅ `audit-wire-36` |
| compact + 一成功一失败 | 只显示失败步骤、无摘要（成功步不显示也不计数）→ 见 F6 建议 `audit-wire-37` |
| compact + 两回合（先失败后成功） | 每回合各自判定：回合 1 失败步、回合 2 摘要"已完成 2 个步骤" ✅ `audit-wire-43` |
| 存储值非法（`mode="weird"` / `"COMPACT"`，`collapse="bogus"`，重启读盘） | 渲染回落 standard/completion（2 步骤标题、内容 0、无摘要），不崩 ✅ `wire2-p9-corrupt.json`、`audit-wire-44/46`；但设置页显示"未选中"（F5） |

---

## 3. 失败项（现象 / 复现 / 期望 / 建议修法）

### F1【高】`ui.workSteps.collapse=next-input`「下次有新消息」不收起 —— 实际是"永久展开"

- **现象**：选「下次有新消息」后，回合结算保持展开；此后发出下一条用户消息（同一会话）、新回合结算、甚至刷新页面，旧回合内容始终展开（content 计数恒为 1）。与「回答结束后」的唯一差别是默认档会收起，等于该档什么都没实现。
- **复现**：`bash startC.sh wire4 <bin> 9980 9972` → 设置→外观→收起时机=下次有新消息 → 新会话发 `APPROVAL one`（允许）→ 发 `APPROVAL two`（允许）→ 发 `PLAIN follow-up`；每步测 `.zk-thought__content` 可见数：1 → 1 → 1（`wire2-p2-collapse.json`）。
- **期望**：下一条用户消息进入会话时，之前回合折叠（可见 content 0）；当前回合保持展开。
- **根因/修法**：`web/src/chat/thought-steps.tsx:103` `const keepOpen = collapse === 'next-input' && !streaming;` 只有 `streaming` 一个变量，组件里没有任何"这一轮是不是最后一轮/是否已来新消息"的输入；`autoExpanded` 因此对已结算回合永远为全展开。建议：
  1. 由 `transcript.tsx` / `assistant-body.tsx` 传入 `isLatestTurn`（或 `turnIndex` + 会话最新轮次），`keepOpen = collapse==='next-input' && !streaming && isLatestTurn`；或
  2. 在"用户消息追加"时给历史回合发一个 collapse 信号（把 `userExpanded` 置 `[]`，`thought-steps.tsx:36` 的本地 state 需要受控化）。
  3. 顺手给该行加 help（见 F6）。
- **证据**：`audit-wire-05..08`、`wire2-p2-collapse.json`。

### F2【中】「详细」与「完全展开」行为完全相同（两个选项、一种效果）

- **现象**：同一回合，detailed 与 verbose 的 `.zk-thought__header` 与可见 content 数完全一致（2 / 2），差别只在标签文字；用户切换这两档看不到任何变化。
- **复现**：设置→外观→工作步骤展示，分别选详细/完全展开，刷新会话测量（`wire2-p1-modes.json`）。
- **期望**：两档应有可感知差异（例如 detailed 展开工具节点但仍折叠 reasoning/原始参数；verbose 连 reasoning 与原始参数一起展开），或合并为一档并删掉另一个选项。
- **建议修法**：`web/src/chat/thought-steps.tsx:102` `const wantsAllOpen = mode === 'detailed' || mode === 'verbose';` —— 为 detailed 定义另一个展开集合（只开 tool 节点，不开 `reasoning` 与"原始参数"），或删除 `verbose`（`web/src/features/settings/ia.ts:266-273` 的选项与 i18n 的 `settings.ia.alwaysExpanded`）。

### F3【中】不带修饰键的"裸键"被当成合法快捷键，直接吞掉输入

- **现象**：把「命令面板快捷键」设为 `K`（解析器接受为 `{key:'k', mod:false}`），在会话输入框打 `k` 时 keydown 被 `preventDefault` 并弹出命令面板，正文只剩后一个字符（实测 textarea 值 `"k"`，用户丢掉第一次按键）；面板还夺走焦点，连续输入被打断。UI 无任何提示。
- **复现**：设置→外观→命令面板快捷键=`K`→保存→刷新→在输入框键入 `kk`（`wire2-p8-boundary.json:bare-key-shortcut`，`audit-wire-16`）。
- **期望**：至少一个修饰键（Mod/Ctrl/Alt/Shift）才允许绑定字母/数字裸键；否则视为非法值 → 回落默认（并在设置行提示）。
- **建议修法**：`web/src/keyboard/shortcut-combo.ts:60` `combo.key = NAMED_KEYS[lower] ?? (part.length === 1 ? part.toLowerCase() : '');` —— 在 `parseShortcut` 返回前加 `if (!combo.mod && !combo.alt && !combo.shift) return null;`（`Escape`/`F*` 之类如确需裸绑定，单独白名单）。同时把这条写进 `shortcut-combo.test.ts`（现在测试反而断言 `parseShortcut('K')` **非 null**，把缺陷锁死了）。

### F4【中】两行绑定同一个组合键时，静默丢掉一个动作

- **现象**：命令面板与折叠侧栏都设 `Mod+J` → 按 `Mod+J` 只折叠侧栏，命令面板再也打不开；没有冲突提示，用户会以为面板坏了。
- **复现**：两行都填 `Mod+J` 保存，回到会话按 `Mod+J`（`wire2-p3-shortcuts.json:collision-same-chord`，`audit-wire-17`）。
- **期望**：保存时检测两行冲突并提示/拒绝，或让同一组合键上"无副作用的动作"都执行（面板打开 + 侧栏折叠）。
- **建议修法**：`web/src/keyboard/shortcut-arbiter.ts:60` 只取"最后注册"的一个匹配（`sort(...)[0]`）；`web/src/keyboard/use-shortcut-settings.ts` 没有冲突校验。建议在 `readShortcutSettings` 里比较两个 combo 并把冲突暴露给设置页（`settings-screen.tsx` 的 help/错误位），或让 arbiter 对 `passive` 型 handler 多命中并依次执行。

### F5【低】非法/未知存储值在设置页显示为"什么都没选"

- **现象**：`settings.json` 里 `ui.workSteps.mode="weird"`/`"COMPACT"`、`collapse="bogus"` 时，行为回落 standard/completion（正确），但两行的单选**全部未选中**，用户看不出当前实际生效的档位；行缺失（干净安装）时同样"全不选中"。
- **复现**：`bash startD.sh wire5 <bin> 9982 9972`（写入非法值）后打开 设置→外观（`wire2-p9-corrupt.json`、`audit-wire-44..47`、`audit-wire-54`）。
- **期望**：UI 显示与实际生效一致（标准/回答结束后），或明确标注"存储值无效，按标准处理"。
- **建议修法**：给两行加 `defaultValue`（参照 `web/src/features/settings/ia.ts:184-191` 对 `chat.mode` 的做法）：`radio(K.workStepsMode, …, {defaultValue:'standard'})`、`radio(K.workStepsCollapse, …, {defaultValue:'completion'})`；对"有值但不在选项里"的情况在 `settings-screen.tsx:523` 的 `Radio.Group` 旁补一行提示（`aria-live`）。

### F6【低】compact + 一成功一失败时，成功步骤完全消失且不计入摘要

- **现象**：`MIXED` 回合（1 个成功 + 1 个失败）在简洁档只显示失败步骤，既没有成功步骤行，也没有"已完成 1 个步骤"的摘要，读者无法知道还执行过一次成功调用。
- **判断**：需求只要求"失败步骤必须仍可见"（本次满足），所以按清单不算失败；但信息完整性上是缺口。
- **建议修法**：`web/src/chat/thought-steps.tsx:84-98` —— 当 `actionable.length > 0 && steps.length > actionable.length` 时，在列表尾部追加一行 `已完成 N 个步骤` 的 `collapsible:false` 摘要（N = 成功数），保留"失败可见"的同时不丢成功计数。

---

## 4. 额外观察（超出 5 项声称，供后续排期）

1. **完成 toast 对"当前会话"并不静默**：在会话页里完成一轮后仍弹出「会话「…」已完成」（`wire2-p4b-toast-frame.json:afterApprove`；`wire2-p4c` 中 ON 档结算后 176ms 出现）。这与"完成 toast 对活跃会话静默"的预期不符；服务端静默判定依赖 host 提供的前台会话（`internal/modules/agent/run_notify.go:17-40`，`foreground` 只有托盘壳注入），纯浏览器产品下 `active` 未必等于用户正在看的会话。**不影响第 4 项结论**（开关确实能关掉它：OFF 档 6.237s 内 0 条），但"完成 toast 会打扰正在看的会话"本身值得单独立项。
2. 归档行在侧栏里与普通行外观相同（无"已归档"标记/弱化），开启显示后难以区分；不影响开关本身。
3. `ui.chatFontSize` 清空后行仍是 `""`（未删除行），行为上等价于未设置。
4. **拒绝审批时工具卡顶部红字是原始 code `other`**（`audit-wire-36`：卡片第一行红字 `other`，真正的说明 `rig: tool call denied by approval gate: web_fetch` 在"输出"框里）。`ToolCardBody` 把 `step.error` 原样渲染（`web/src/chat/tool-card.tsx:23-28`），而审批拒绝的 error 字段是稳定 code 而非人话；中文界面里出现英文裸 code，读起来像故障。与第 8 项的"失败步骤仍可见"不冲突，建议单列一条文案修。

## 5. 需补验收点（本轮没覆盖到的）

1. `next-input` 的组件级单测：现有 `thought-steps` 测试没有"新用户消息到达 → 旧回合折叠"的用例，需要假 `collapse='next-input'` + rerender 新 turn。
2. 快捷键的**事件派发**测试：`shortcut-combo.test.ts` 只测 parse/format，没有测 `ShortcutArbiter.handleKey`（裸键、撞键、输入框聚焦、重复键 `event.repeat`）。
3. 设置页对"非法/未知存储值"的可见性用例（含 `readWorkSteps` 回落值与单选显示一致性）。
4. 收起时机/模式的 help 文案（当前 4 个相关行全无说明文字，与同页其他行不一致）。
5. 浏览器产品形态下"完成 toast 静默"的契约（见观察 1），需要产品层面确认是否应当静默。

## 6. 复现与附件

- 实例：`bash startC.sh wire4 /data/zeokeel/.cache/bin/zeokeel-wire2 9980 9972`（`startC.sh` 只写基础 settings 并直接跑二进制；`startB.sh` 同形但会拉 `fakemodel`）
- 夹具：`node model2.mjs`（9972）；跨实例非法值用 `ZK_MODE=weird ZK_COLLAPSE=bogus bash startD.sh wire5 <bin> 9982 9972`
- 探针脚本（都在 `/data/zeokeel/.cache/ui-audit/`）：`lib.mjs`（测量/设置操作）、`p1-modes.mjs`、`p2-collapse.mjs`、`p3-shortcuts.mjs`、`p4-notifications.mjs`、`p4b-toast-frame.mjs`、`p4c-completion-toast.mjs`、`p5-archived.mjs`、`p6-copy.mjs`、`p7-regression.mjs`、`p8-boundary.mjs`、`p9-corrupt.mjs`、`p10-live.mjs`、`p11-defaults.mjs`
- 数据：`findings3/wire2-p{1..11}*.json`（每条包含 settings.json 落盘值、DOM 文本数组、几何 rect）
- 截图（64 张，`findings3/audit-wire-*.png`）：01/02 四档 ×（设置帧+渲染）、03–08 收起时机两档、09–18 快捷键（含非法/裸键/撞键/恢复）、19–22 通知（含 20b 不透明 toast 帧、48 完成 toast on/off）、23–28 归档、29–31 说明文字、32–38 边界与回归、39 字号回归、40–43 占位/输入焦点/两回合、44–47 非法值、49–51 跨 tab 实时、52–54 干净默认值

> 截图命名对照：`-settings` 后缀 = 设置帧；其余 = 会话/侧栏渲染帧；`-midrun` = 流式中；`-after-*` = 结算后。
---

# 第二轮复验（提交 `62fd3a1`）

- 复验时间：2026-11-25 02:03–02:20（北京时间）
- 复验对象：上一轮 5 条失败 + 2 条待补（`62fd3a1 fix(web): 下次有新消息 now means that, and a bare key is not a shortcut`）

## R2.0 版本自证

| 项 | 值 |
|---|---|
| 新二进制 | `.cache/bin/zeokeel-wire3`，sha256 `8b8e082019f6561823c4daae1fb4116f57463e74b8b31f2874eaf9495ee186ab`，mtime 11-25 01:59 |
| 实例 A（`startB.sh`，全新目录 `data-wire8`，port **9984**） | bundleSha256 `d0af8388f2155feecc713ccde2fa6fff583944674d87c6df19bc0d2625570d78`，builtAt **2026-11-24T17:59:16Z** |
| 实例 B（`startC.sh`，`data-wire6`，port **9990**，指向本地 SSE 夹具 9992） | 同一 bundleSha256 `d0af8388…`（与 A 一致），builtAt 17:59:16Z |
| 自检（B） | `/`=200、`/favicon.svg`=200、`/api/capabilities`(无授权)=401 |
| 客户端 bundle | `/assets/index-BbHYkER9.js`，sha256 `fc933421b7de7969…38a74d`，含二轮新增文案「简洁只列需要你处理的步骤」「留到你说下一句再收起」→ 运行的是 `62fd3a1` 构建（上一轮为 `028d3eab…`，已换新） |

> 说明：复验期间仓库又新增 `ce8f5c5`（会话行菜单焦点/Enter 自带确认 + 2 条 i18n），`git diff --stat 62fd3a1..HEAD` 只动 `web/src/shell/sidebar.tsx` 与 `web/src/i18n/*`，未触及 `thought-steps/transcript/assistant-body/keyboard/**/settings/ia.ts`，因此本轮结论对 HEAD 同样成立。

夹具 `model2.mjs` 新增 `REASON` 脚本：先流 `delta.reasoning_content`，再发两个成功 `read_file`，用于区分"思考节点"与"工具卡"的展开；`TWOSTEP`/`FAILSTEP`/`MIXED`/`APPROVAL`/`PLAIN` 沿用。

## R2.1 结论总表

| # | 复验项 | 结果 | 证据摘要 |
|---|---|---|---|
| 1 | `next-input` 语义 | **PASS** | 结算后最新回合展开 1；发出下一条消息**当刻**旧回合 0；新回合结算后 1；刷新一致；`completion` 对照立即 0 |
| 2 | 详细 vs 完全展开 | **PASS** | 详细：工具卡 2 展开、推理不展开（contents=2）；完全展开：连推理 3 个（contents=3）；标准 0；简洁 `[推理, 已完成 2 个步骤]` 0 |
| 3 | 裸键 | **PASS** | `K`/`1`/`Space` 存盘后行为回落 `Mod+K`；输入框键入 `k 1K`、空格、数字全部 `preventDefault=false`、面板不弹、字符全留下 |
| 4 | 撞键 | **PASS（行为）+ 待补（提示）** | 两行 `Mod+K`：面板可用、侧栏不动；`Mod+B` 折叠（侧栏回落默认）。设置页无「冲突」字样/无 help → 按你的说明记录为待补 |
| 5 | 默认选中 | **PASS** | 全新目录 `settings.json` 无这两行；页面**稳定显示** 标准 + 回答结束后（early/late 两次采样一致），两行 help 就位 |
| 6 | 简洁混合回合 | **PASS** | `["…does-not-exist.txt · 失败","已完成 1 个步骤"]`，contents=1，失败步可读且摘要给出被隐藏的成功数 |
| 7 | 回归 | **PASS** | 四档渲染、边界（只有失败步/无步骤）、通知 OFF 5.005s 无 toast / ON 3ms 有、归档 6→5→6→5、字号 17 落地、`Mod+J` 刷新保持 |

**第二轮总裁定：上一轮 3 条 FAIL（next-input / 详细·完全展开 / 说明一致性主项）与 2 条边界缺陷（裸键、撞键行为）全部修复；设置页文案（help + 默认选中）已补。剩余为"提示类"待补项（撞键/非法值无 UI 提示、非法存储值仍全不选中），不影响行为正确性。**

## R2.2 逐条证据

### 1. `next-input`（会话 `sess-1795543550…`，两轮均含 `web_fetch` 审批）

| 时点 | 行（用户/助手）展开内容数 |
|---|---|
| 回合 1 结算后（最新行） | 助手行 **1**（保持展开） |
| 回合 2 审批停住、尚未结算 | 回合 1 助手行 **0**（新消息一出现即收起）；回合 2 助手行 **1**（运行中步骤展开） |
| 回合 2 结算后 | 回合 1 **0**、回合 2 **1**（最新回合保持展开） |
| 刷新页面 | 回合 1 **0**、回合 2 **1**（与刷新前一致） |
| 切 `completion` 后刷新 | 回合 1 **0**、回合 2 **0**（最新回合也立即收起） |
| `completion` 下第 3 轮结算 | 全部 **0** |

补充说明"perTurn=[]"：若第二轮是纯文本（无可展开步骤），两轮可见内容都是 0，这是预期（第二轮没有内容可展开）。用 `APPROVAL turn one` + `PLAIN turn two` 复现：回合 1 结算 1 → 第二轮后 `[(u,1,0), (a,0,0)]` → 刷新一致（`wire2-r2-plain-second.json`）。因此你观察到的"两轮全收起"与"新回合若有步骤应保持展开"并不矛盾——关键看第二轮有没有步骤。

截图：`audit-wire-r2-03..08`、`audit-wire-r2-30`；数据 `wire2-r2-next-input.json`、`wire2-r2-plain-second.json`。

### 2. 详细 vs 完全展开（`REASON` 回合：推理 + 2 个成功工具步）

| 档位 | `.zk-thought__header` 文本 | 可见 `.zk-thought__content` | 逐节点展开（content 可见） |
|---|---|---|---|
| 标准 | `["推理","读取文件read_file/…alpha.txt · 完成","…beta.txt · 完成"]` | 0 | 推理 ✗ / 工具 ✗ / 工具 ✗ |
| 详细 | 同上 3 条 | **2** | 推理 ✗ / 工具 ✓ / 工具 ✓ |
| 完全展开 | 同上 3 条 | **3** | 推理 ✓ / 工具 ✓ / 工具 ✓ |
| 简洁 | `["推理","已完成 2 个步骤"]` | 0 | 推理 ✗ / 摘要（不可展开） |

（判据说明：antd X 的 ThoughtChain 节点没有 `aria-expanded`，chevron 的 `aria-label` 恒为 `right`，视觉展开靠 CSS 旋转；因此用 content 盒高度 > 0 作为"已展开"的判据。）

截图 `audit-wire-r2-09-mode-{standard,detailed,verbose,compact}.png`，数据 `wire2-r2-modes.json`。

### 3. 裸键

| 存值 | 设置页显示 | `Mod+K` | 输入框键入 | keydown 是否 prevented |
|---|---|---|---|---|
| `K` | `K`（原样，无提示） | 打开面板（回落默认） | `k 1K` | `k`,` `,`1`,`K` 全 **false** |
| `1` | `1` | 打开面板 | `1` | `1` false |
| `Space` | `Space` | 打开面板 | ` a b` | ` `,`a`,` `,`b` 全 false |

面板全程不弹，输入一个字都没丢。带修饰键的 `Mod+K`、`Mod+B`、`Mod+J` 仍正常（见第 4/7 项）。截图 `audit-wire-r2-10..12`，数据 `wire2-r2-keys.json`。

### 4. 撞键

| 场景 | 结果 |
|---|---|
| 两行都写 `Mod+K` | `Mod+K` → **面板打开**、侧栏不动（`data-collapsed=false, 264px`）；`Mod+B` → 侧栏折叠（侧栏回落默认 `Mod+B`）；再按展开 |
| 两行都写 `Mod+J` | `Mod+J` → **面板打开**、侧栏不动；`Mod+B` → 侧栏折叠/展开 |
| 设置页提示 | **无**：DOM 全文搜不到「冲突 / conflict」，两个快捷键行 `help` 为空（`conflictText.hasConflictWord=false, help=[]`） |

行为符合你描述的"侧栏回落默认、面板仍可用"；提示缺失按"待补"记录（见 R2.3-R2.1）。截图 `audit-wire-r2-13/14`，数据 `wire2-r2-keys.json`。

### 5. 默认选中（全新目录 `data-wire8`，`settings.json` 无 `ui.workSteps.*`）

- `工作步骤展示`：**标准** 选中；`收起时机`：**回答结束后** 选中（两次采样：页面出现后 300ms 与 2.8s，结果一致，说明不是首帧默认值造成的假象）
- help 文案：「简洁只列需要你处理的步骤；详细展开工具卡；完全展开连思考一起展开。」「回答结束后立即收起，或留到你说下一句再收起。」
- 截图 `audit-wire-r2-01-defaults-settings.png`、`audit-wire-r2-02-defaults-page.png`、`audit-wire-r2-26-defaults-late.png`；数据 `wire2-r2-defaults.json`、`wire2-r2-defaults-late.json`

### 6. 简洁 + 混合回合

`MIXED`（1 成功 + 1 失败）→ header：`["读取文件read_file/…does-not-exist.txt · 失败","已完成 1 个步骤"]`，contents=1，summary 命中；成功步仅以数字形式出现在摘要里，失败步完整可读。截图 `audit-wire-r2-16-compact-mixed.png`，数据 `wire2-r2-mixed-regression.json`。

### 7. 回归

| 项 | 结果 |
|---|---|
| 四档（两个成功步回合） | 简洁 1 摘要/0；标准 2/0；详细 2/2；完全展开 2/2（该回合无推理，两档一致属预期） |
| 边界：简洁 + 只有失败步 | 失败步 1 条可见、contents=1、**无摘要** |
| 边界：简洁 + 无步骤 | 无 `.zk-thought`、无「已完成 0 个步骤」 |
| 通知 | OFF：5.005s 内 0 条；ON：3ms「审批等待：web_fetch」（截图 `r2-17/18`） |
| 归档 | 6 行 → 归档后 5 → 开启显示 6（ARCHR2 可见）→ 关闭后 5 且 ARCHR2 不在（截图 `r2-19/20/22`） |
| 字号 | `17` 落盘 + `--zk-chat-font-size:17px`；清空后移除（截图 `r2-21`） |
| 快捷键改键 | 存 `Mod+J` 后刷新：`Mod+K` 不开、`Mod+J` 开、`Mod+B` 折叠（截图 `r2-15`） |

## R2.3 剩余问题（不阻塞本轮结论，建议下一轮清）

- **R2-1【低】撞键提示缺失**：`web/src/keyboard/use-shortcut-settings.ts:30-40` 已算出 `conflict`（`{key}`），但 `web/src/features/settings/settings-screen.tsx` / `ia.ts` 没有任何消费点 → 设置页看不到。修法：在快捷键两行的 `help` 位渲染 `conflict`（例如「与折叠侧栏快捷键冲突，已按默认 Mod+B 处理」），或保存时 `Alert`。
- **R2-2【低】非法快捷键值原样存盘**：`K`/`1`/`Space`/`乱写` 都能保存，字段无校验态、无提示，行为静默回落默认。建议在 `FieldRow` 的 text 行上给 `ui.shortcut.*` 增加"可解析"校验（复用 `parseShortcut`），保存前给出内联错误。
- **R2-3【低】非法存储值仍显示"全不选中"**：`mode="weird"` 时行为回落 standard（2 标题/0 内容，`wire2-r2-corrupt.json`），但两行单选在快照到达后清空为未选中（`wire2-r2-corrupt-late.json`：early/late 均无选中；另有一次更早采样 `r2-corrupt.mjs` 读到首帧默认「标准」，说明先显示默认再清空）。行缺失场景已修；"有值但不在选项里"仍无提示。修法：`fieldValue`/`FieldRow` 对"值不在 options"给出「存储值无效，按标准处理」的一行提示。
- **观察（非本轮引入）**：ThoughtChain 节点 chevron 无 `aria-expanded` 且 `aria-label` 恒为 `right`，展开态无法从 DOM/辅助技术读出；展开判据只能看 content 高度。

## R2.4 需补验收点（二轮）

1. 撞键与非法值：需要"设置页可见提示"的验收（当前只能靠行为断言）。
2. 手动展开过的旧回合在"下一条消息"后是否应保持展开（现在 `userExpanded` 优先，永远尊重用户操作）→ 需产品确认。
3. 首帧默认值 → 快照到达后的"清空"过渡（R2-3）是否需要在视觉上消除（避免一闪）。

## R2.5 附件

- 脚本（`/data/zeokeel/.cache/ui-audit/`）：`r2-defaults.mjs`、`r2a-next-input.mjs`、`r2b-modes.mjs`、`r2c-keys.mjs`、`r2d-mixed-regression.mjs`、`r2e-modes2.mjs`、`r2f-plain-second.mjs`、`r2-corrupt.mjs`、`chk-corrupt.mjs`、`chk-corrupt2.mjs`、`chk-archive.mjs`
- 数据：`findings3/wire2-r2-*.json`（含 settings.json 落盘、early/late 采样、逐节点展开状态）
- 截图：`findings3/audit-wire-r2-01..30.png`（36 张）
