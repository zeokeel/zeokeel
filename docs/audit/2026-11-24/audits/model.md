# ZeoKeel「模型配置」独立验收（穷尽版）

- 验收对象：`ad123e8 fix(web): make the model page tell the truth`（`web/src` 未被验收方改动；二进制 `.cache/bin/zeokeel-model` 冻结于该修复，bundle `075b82f4b0dc`）
- 方法：真网关（冻结二进制）+ 真 Chromium（chromium-1243，DPR=2）+ 每个用例一个**全新数据目录与全新网关进程**；所有结论来自我自己跑出的 DOM / WS 帧 / `/api/models` / `settings.json` / computed style / 截图
- 轮次：45 条校验用例、19 种目录形态、9 个视口、4 类状态 × 2 主题、10 项 a11y 事实、6 条修复声称端到端复跑
- 时间：2026-11-24 22:49–23:45（北京时间）
- 追加：用户追问「模型服务是否重复」→ 见 §11（结论：不是重复，是回退链 + 归属/文案错了 + 继承态说假话）

## 0. 判定摘要

| 维度 | 结论 |
|---|---|
| 6 条修复声称 | **6/6 成立**（逐条见 §3） |
| 10 条验收清单 | **10/10 PASS**（其中 #9、#10 带缺陷注记） |
| 整体可交付性（**当前 = round-5 构建 `8976472`**） | **模型页 PASS**：round-3 报的 2 个 P0 + 2 个 P1 + 悬空默认 + 5 条 P2 + 2 条行为备注全部修复并复验通过；清单 10/10 无回归。剩余：3 条新 P3 观察（§14.3，其中 O3 属聊天面启动延迟，非本页）+ 3 条一直未修的 P3（错误不替换 help、`/api/models` 坏行 500、P0-A/Esc/maxTokens 无单测） |
| 历史（round-3 判定，留档） | FAIL —— 2 个 P0（页脚保存覆盖目录导致数据丢失；`[null]` 白屏）+ 2 个 P1（小数写入 → `/api/models` 500；Esc 穿透离开设置页） |

round-3 时的一句话（留档）：**清单要的东西都做到了，但这一页仍有两条发布阻断级问题，且都不在清单里：①页脚「保存」会把「模型目录」写回打开页面时的旧快照——dirty 之后删除的模型会复活、改名会被回滚、刚加的模型会消失（数据丢失，且修复前就存在）；②目录里出现「合法 JSON 但不是合法行」（如 `[null]`）时模型页整页白屏、无 UI 恢复入口；再加上弹窗能亲手写出后端读不了的小数（`contextWindow=1024.5` → `/api/models` 500 → 输入框模型菜单变空）。**

（追加：用户追问「模型服务是否和模型行重复」→ 见 §11：不是重复，是回退链 + 归属/文案错 + 继承态说假话。）

**round-4 复验（新构建 `fa573b7` / `zeokeel-conn`）**：P0-A、P0-B、P1-B、悬空默认 **已修（PASS）**，原 10 条清单 **10/10 无回归**；但 **P1-A 只修了一半**——`maxTokens=12.5` 在 `contextWindow` 留空时仍会落盘并让 `/api/models` 500、输入框模型菜单变空（§13.3）。round-3 的 8 项 P2 里 3 项 PASS、5 项仍未修（§13.10）。

**第三轮复验（新构建 `8976472` / `zeokeel-conn2`）**：P1-A 残留、5 条 P2、2 条行为备注 **全部 PASS**；原 10 条清单、P0-A 六序、P0-B 八形态、Esc 十场景 **全部无回归**（§14）。剩下的是一条与本页无关的观察：`/c/:id` 刷新后 composer 要到 3.3–4.7s 才显示真实模型（round-4 为 1.2s），已定位为聊天面/启动侧，交给对应 owner（§14.3 O3）。

## 1. 复现装备

```bash
# 单例启动器（我用它跑 45 条校验用例、19 种形态、9 个视口）
node /data/zeokeel/.cache/ui-audit/out3/p16matrix.mjs   # 形态矩阵（每形态一个网关进程）
node out3/p15validation.mjs                              # 45 条字段/跨字段/双击/回车/关闭
node out3/p17numeric.mjs  out3/p17b.mjs                  # 数值边界 + 跨字段提交 + 清空
node out3/p18composer.mjs                                # 输入框模型菜单 10 种状态
node out3/p19visual.mjs                                  # 9 视口 + 状态色 + a11y
node out3/p20edges.mjs                                   # a11y / 搜索框 / 悬空默认 / 重启持久化 / 390
node out3/p21flows.mjs                                   # 声称 3/5/6/7 端到端 + 深色 390
node out3/p22contrast.mjs                                # 逐元素对比度（4 状态 × 2 主题）
node out3/p23session.mjs  out3/p24final.mjs              # 会话级默认/思考档 + 无默认/重复 id
# 单元测试侧（仓库自带）
cd web && node_modules/.bin/vitest run src/features/settings   # 2 files / 9 tests 全绿
```

证据 JSON：`out3/state-matrix2.json`（19 形态）、`validation-matrix.json`（45 用例）、`numeric-matrix.json`、`cross-field.json`、`composer-matrix.json`、`visual-a11y.json`、`contrast.json`、`edge-cases.json`、`claims.json`、`final-states.json`、`session.json`、`carash-shapes.json`（早期）、`item*.json`（早期）。
截图：`findings3/audit-model-*.png`（171 张）。

## 2. 清单表（10 条）

| # | 验收点 | 判定 | 证据（文件 : 关键值） |
|---|---|---|---|
| 1 | `chat.model` 只有一个控件 | **PASS** | 模型页 `.ant-select` 计数 = **1**（`[data-testid=model-default-picker]`，`label[for=zk-model-default-picker]`=默认模型，`input[role=combobox]`）；输入框清单里**没有** `zk-field-chat.model`；「模型服务」区 label 仅 接口地址/API Key/上下文预算/对话模式/系统提示词；**在搜索框输入「模型」或「chat.model」后** 仍是 1 个 select（`edge-cases.json .search`） |
| 2 | 空目录引导（非损坏）+ disabled + placeholder 说明原因 | **PASS** | 无键 / `""` / `"   "` 三种形态都渲染 `还没有模型。添加一个之后，输入框的模型选择器才能切换。`，`broken=false`，picker `ant-select-disabled`，placeholder `未设置：先从目录里添加一个模型`（`state-matrix2.json: absent-key/empty-string/whitespace`） |
| 3 | 加第一个模型→自动默认→刷新仍在 | **PASS** | 帧序列 `modelcatalog.models=[{"id":"first",…}]` → `chat.model=first`；行显示 `默认` Tag；picker=第一个；reload 后同；`settings.json` 持久化；**网关重启后仍在**（`claims.json claim3`、`edge-cases.json .restart`） |
| 4 | 五类校验错误 + 不写入 | **PASS（已扩到 45 例）** | 所有非法用例 `wrote=[]`（窗口内 0 条 `settings.set`）；文案见 §6；合法用例才写（`validation-matrix.json`） |
| 5 | 删默认：说明切换 + `chat.model` 跟随 + 无悬空 id | **PASS** | 确认框 `删除「乙」？/这是当前默认模型；删除后默认会切换到「甲」。`（后继 = 剩余首行）；帧 `models=[甲,丙]` → `chat.model=a`；`/api/models defaultModel=a`；删到空：`models=[]` + `chat.model=""` + `目录已被清空…`；删**非**默认行：无"切换"文案且只有 1 条 models 帧（`claims.json claim5`、`final-states.json successorLater`） |
| 6 | `[]` 态 = 已清空 | **PASS** | `目录已被清空。添加一个模型之后才能发送。`，无 broken；picker disabled；`/api/models` 200 `{"models":[]}`（`state-matrix2.json empty-array`） |
| 7 | 损坏态 + 恢复为空列表 | **PASS** | `this is not json` → broken 告警（title+hint+按钮）；点 `恢复为空列表` → 帧 `models=[]` → 已清空态 → 再添加成功并自动默认（`claims.json claim67`） |
| 8 | 设置页默认 ↔ `/c/:id` 选择器一致 | **PASS（含已记录限制）** | 选项==目录、当前值==`chat.model`；改选帧 `chat.model=local-b`；刷新/新会话/回设置页/`/api/models` 四处一致；跨标签页改目录会残留旧选项（重载即正确）；会话内 4 档思考可用且写入 `session.<id>.model.ds.thinking=reasoning-low`（`item8b/8c.json`、`session.json`） |
| 9 | 1440/390 截图 + 字段分层 + 底部按钮可见 | **PASS（1 处偏离）** | 9 个视口（320…1920）全部：无横向溢出、picker 在视口内、弹窗底部按钮**始终**在视口内（含把 body 滚到底后 `btnY` 不变）；偏离：错误**没有替换 help**，而是插在控件与 help 之间（§7 X3） |
| 10 | 深色可读（≥4 组 computed） | **PASS（2 处 a11y 注记）** | 19 组 computed 见 §9；正文 13.59、次级 4.52、help 4.41、错误 3.89、主按钮 2.87、禁用 placeholder ≈2.3（深浅两套都给数） |

## 3. 六条修复声称逐条核对

| 声称 | 判定 | 直接证据 |
|---|---|---|
| 1) `chat.model` 只有一个控件（目录顶部选择器；旧文本框已删） | **成立** | 见清单 1；IA 里 `chat.model` 仅作为声明存在（`ia.ts:135`），渲染路径走 `ModelCatalogEditor`（`settings-screen.tsx:324`），DOM 实测无第二个控件；**修复前/后对照截图**：[140-beforefix-model-service-field](audit-model-140-beforefix-model-service-field.png)（旧二进制仍有「模型服务→模型」文本框）vs [133-delete](audit-model-133-delete.png)（修复后该行已消失，只剩 接口地址/API Key） |
| 2) 空目录加第一个模型→自动成为默认 | **成立** | 两帧 set + picker/行标签 + 重启后仍在 |
| 3) 删默认会说明切换到 X 并切换（无其它则清空），不留悬空 id | **成立（主路径）**；**已悬空的默认仍会悬空**（§4 P2-A/P2-B） | 确认框文案与帧；`/api/models` 比对；但"本来就是 ghost + 删掉唯一一行"这条路径下 `chat.model` 仍是 `ghost` |
| 4) 三态：未配置 / 已清空 / 损坏（可恢复）；`[]` 不再报损坏 | **成立** | 19 形态矩阵：`absent/""/"  "` → 未配置；`[]` → 已清空；`{...}`/`null`/非 JSON → 损坏；`[]` 不报损坏 |
| 5) 字段校验（id 形状+唯一、baseURL、contextWindow 范围、maxTokens≤window；错误在字段下方） | **成立（形状层面）**，但**非整数可通过**（§4 P1-A） | 45 条用例全部按字段报错且不写入；`1024.5` 与 `12.5` 通过校验并写入 → 后端 500 |
| 6) 弹窗内部滚动，1440×900 下保存无需先滚页面 | **成立** | body `scrollHeight 784 / clientHeight 504 / overflow:auto`；按钮 y=668–700；`pageScrollY=0`；9 视口复验 |

## 4. 缺陷（按严重度）

### P0-A —— 合法 JSON 但含 `null` 元素：**模型页整页白屏**

- 形态：`modelcatalog.models` = `[null]`、`[null,{…}]`、`[{…},null]` 三种都触发。
- 现象：`/settings/model` 与 `/settings`（默认落到模型页）**整棵 React 树卸载**：`#root` 为空、`body.innerText=""`、无侧栏；`pageerror: TypeError: Cannot read properties of null (reading 'id')`；页面停在白屏，**没有任何 UI 恢复入口**（`恢复为空列表` 不渲染），只能改 `settings.json` 或用浏览器后退/重载到别的路由。
- 爆炸半径实测：`/`（聊天）正常；`/settings/behavior` 正常；`/settings`、`/settings/model` 白屏；从 `/` 重载可恢复。
- 复现：`bash out3/start3.sh nullrow 9951 9961 out3/s-nullrow.json` → 打开 `/settings/model`（或 `node out3/p16matrix.mjs`，看 `state-matrix2.json .null-only/.null-first/.null-last`）。
- 根因：`web/src/features/settings/model-catalog.tsx:54-64` 的 `parseRows` 只校验"是不是数组"，元素不校验；随后 `:84` `catalog.some((row) => String(row.id) === current)` 对 `null` 取 `.id` 抛错（`:196-199` 的 `options` 映射同理会二次抛）。
- 证据：截图 [70-shape-null-only-BLANK](audit-model-70-shape-null-only-BLANK.png)、[null-first](audit-model-70-shape-null-first-BLANK.png)、[null-last](audit-model-70-shape-null-last-BLANK.png)、[61-null-chat-route](audit-model-61-null-chat-route.png)（聊天页仍好）、[62-null-settings-blank](audit-model-62-null-settings-blank.png)。
- 建议修法（3 行量级）：`parseRows` 里把元素过滤成对象，并把"有非法元素"并入 broken 态给出诊断：
  ```ts
  const rows = decoded.filter((r): r is CatalogRow => !!r && typeof r === 'object' && !Array.isArray(r));
  const broken = rows.length !== decoded.length;   // 有元素被拒 → 走损坏态，可一键恢复
  ```
  同时 `:84`/`:196` 全用 `String(row?.id ?? '')` 兜底；补一条 vitest：`[null]` → 渲染 broken 且不抛。

### P0-B —— 页面底部「保存」会把模型目录写回旧快照：**删除的模型复活、改过的名字被回滚、刚加的模型消失**

- 触发条件（很日常）：**先**在「模型服务」区改任意一项（页面变 dirty）**再**用弹窗增/删/改模型，**然后**点页面底部「保存」。
- 实测三个变体（每个都是独立干净实例）：

```text
① 加模型后保存（dirty 后加）       afterMutation=3 行 → footer 帧 modelcatalog.models=[旧的 2 行] → afterSave=2 行   ← 新模型消失
② dirty 后删 local-a，再保存        afterMutation=[local-b] → footer 帧 models=[local-b, local-a] → afterSave=[local-b, local-a]  ← 删掉的模型复活
③ dirty 后把 local-a 改名"改过名字"，再保存   footer 帧 models=[…name:"本地 A"] → afterSave 名字回滚    ← 改名丢失
```

- 帧原文（②）：`settings.set modelcatalog.models=[{"id":"local-b",…},{"id":"local-a","name":"本地 A",…}]` + `settings.set chat.baseURL=https://dirty.example.com/v1`
- 复现：`node out3/p25footer.mjs`、`node out3/p27resurrect.mjs`（`out3/footer-race.json`、`footer-wipe.json`、`resurrect.json`），截图 [131-footer-save](audit-model-131-footer-save.png)、[133-delete](audit-model-133-delete.png)、[133-rename](audit-model-133-rename.png)
- 对照（修复前 vs 修复后）：同一实例同一操作，修复前二进制（`.cache/bin/zeokeel-audit2`）的「模型服务」区还多一个 `模型` 文本框（正是本次删掉的那个重复控件），见 [140-beforefix-model-service-field](audit-model-140-beforefix-model-service-field.png)；保存复活的 bug 两个版本都在。
- 属性：**不是这次修复引入的**——用修复前的二进制（`.cache/bin/zeokeel-audit2`，20:06 构建）跑同一用例，行为完全一致（`out3/resurrect-old-binary.json`）。但它落在这次要"说真话"的同一页面上，属发布阻断级。
- 根因：`modelcatalog.models` 被当作页面自己的字段声明进 IA（`web/src/features/settings/ia.ts:131-136`，注释写明"只为 IA 诚实测试与搜索声明"），于是
  - `pageValues()`（`ia.ts:383-389`）把它抄进 `values`/`baseline`；
  - "页面干净时才采纳网关新值"的同步（`settings-screen.tsx:222-231`，`if (dirty) return`）在 dirty 期间不更新它；
  - 点保存时 `changedRows()`（`ia.ts:392-405`）发现 `values` 与当前行不一致，就把**旧快照**当作用户输入写回去。
- 修法（一行级）：让"组件自管"的 section 不参与页脚字段模型：
  ```ts
  // ia.ts:383-389 与 392-405 两处都加
  if (section.component) continue;      // 该 section 自己负责写入
  ```
  或给 `SettingsField` 增加 `managed: true` 标记，`pageValues/changedRows/dirty` 全部跳过；再补 vitest："dirty 状态下目录变化后保存不得写 modelcatalog.models"。

### P1-A —— 非整数 contextWindow / maxTokens 通过校验并写入 → `/api/models` 500 → 输入框模型菜单变空

- 现象：`contextWindow=1024.5` 或 `maxTokens=12.5` 都被接受、写入（`"contextWindow":1024.5`）；随后 `GET /api/models` 返回 **HTTP 500** `json: cannot unmarshal number 1024.5 into Go struct field .17.contextWindow of type int`；聊天页输入框模型下拉变成 **“暂无数据”**（当前模型仍显示裸 `local-b`），直到把该行改掉。
- 复现：`node out3/p17numeric.mjs`（`ctx-decimal` / `max-decimal` 两例 `api=500`）；页面级后果见 `out3/item10b`… 实测截图 [71-composer-after-bad-row](audit-model-71-composer-after-bad-row.png)。
- 根因：`model-catalog.tsx:350-364`（contextWindow）与 `:369-388`（maxTokens）的 validator 只做区间比较；`InputNumber` 未设 `precision={0}` / `step` 校验。
- 建议修法：validator 加 `Number.isInteger(value)` 判定（错误文案"需要整数 tokens"），并给两个 `InputNumber` 加 `precision={0}`；后端 `HandleModels` 对不可解析的目录行降级为诊断（见 X4）。

### P1-B —— 弹窗/下拉里按 Esc 会同时触发设置页的"离开守卫"

- 现象 A（干净页）：打开「添加模型」→ Esc → 弹窗关闭 **且** 页面跳到 `/`（设置页被离开）。
- 现象 B（脏页）：改任一字段 → 打开弹窗 → Esc → 弹窗关闭 **且** 叠出「放弃未保存的更改?」确认框（`.ant-modal` 由 1 变 2）。
- 现象 C（同一根因）：把焦点放到「默认模型」下拉、Esc 关下拉，**页面同样被离开**（`edge-cases.json .a11y.afterEscUrl = http://127.0.0.1:9975/`）。
- 复现：`node out3/p11.mjs`、`node out3/p20edges.mjs`（a11y 段）。
- 根因：`settings-screen.tsx:246-260` 在 `settingsDirtyGuard(60)` 层无条件 claim Esc（注释写明"Esc inside settings always means leave this page"），而新弹窗 `model-catalog.tsx:278-296` 既没在 `overlay(70)` 层 claim，也没 `keyboard={false}`；antd 自己的 Esc 处理与文档捕获层各关各的。全站既有正确范式：`web/src/ui/confirm-danger.tsx:32-40`。
- 建议修法：
  ```tsx
  useEscClaim({ id: 'model-dialog', level: 'overlay', active: opening,
               onEscape: () => { setOpening(false); return true; } });
  ```
  下拉场景同理：`onDropdownVisibleChange` 期间临时 claim `overlay`（或给 `.zk-model-default` 容器加 keydown 拦截并在关闭后释放）。补 vitest：弹窗打开时 Esc 不触发 `settings-dirty-guard`。

### P2-A —— 悬空默认值的提示文案说错原因（且这是常见状态）

- 现象：`chat.model="ghost"` 而目录里有模型 → picker placeholder 仍是 `未设置：先从目录里添加一个模型`（目录并不空）；同一状态下 `未设置` 与下方 `默认模型「ghost」已不在目录里…` 两句矛盾。目录有模型但 `chat.model` 为空（手工种子/导入的常见结果）同样显示这句。
- 证据：`final-states.json .noDefault`（`placeholder=未设置：先从目录里添加一个模型`，而 rows=甲/乙）、截图 [50-stale-default](audit-model-50-stale-default.png)、[121-no-default](audit-model-121-no-default.png)。
- 修法：`model-catalog.tsx:194` 按状态取文案：`catalog.length===0 ? defaultUnset : staleUnmatched`；并把 `.zk-model-stale` 与该文案统一到一条信息。

### P2-B —— "已悬空的默认 + 删掉唯一一行" 之后 `chat.model` 仍悬空

- 现象：种子 `catalog=[local-a]`、`chat.model="ghost"`；删除 local-a（非当前）→ 目录 `[]`、picker disabled、`目录已被清空…`，但 `chat.model` **仍是 `"ghost"`**，`默认模型「ghost」已不在目录里` 继续显示。之后若用户加模型而不点默认，`ghost` 会一直悬着。
- 证据：`edge-cases.json .staleDeleteLast`（`chat.model now = "ghost"`）、截图 [100-stale-delete-last](audit-model-100-stale-delete-last.png)。
- 修法：`remove()`（`model-catalog.tsx:153-159`）在 `next.length === 0` 时无条件把 `chat.model` 清空（不只当 `row.id === current`）；顺带在 `save([])` 路径统一处理。

### P2-C —— 合法 JSON 但元素形状不对：页面渲染 `undefined` / `NaNk`（"不骗人"目标的反例）

| 种子 | 页面实际显示 | `/api/models` |
|---|---|---|
| `[{}]` | 行标题字面 **`undefined`** | 200（id 空串） |
| `[1,2,3]` | 三行都显示 `undefined` | 500 |
| `[{"id":"x","contextWindow":"abc"}]` | 行副标题 `x · 跟随全局服务地址 · 密钥未设置 · NaNk` | 500 |
| `[{"id":123,"name":"num"}]` | 行显示 `num / 123 · …` | 500（number→string） |
| `[{"id":"t",…,"thinkingModes":"nope","temperature":"hot","maxTokens":"lots","contextWindow":-5}]` | 渲染正常 | 500 |

证据：`state-matrix2.json`、截图 [70-shape-empty-object](audit-model-70-shape-empty-object.png)、[numeric-array](audit-model-70-shape-numeric-array.png)、[numeric-context-window](audit-model-70-shape-numeric-context-window.png)。
修法：同 P0（parseRows 做元素级校验 + broken 态），并在行渲染处 `String(row?.name || row?.id || '未命名模型')`。

### P3 组（质量/一致性，逐条都有实测）

| 编号 | 现象 | 证据 | 修法 |
|---|---|---|---|
| X3 | 错误**没有替换 help**：`控件 → 错误(红) → help(灰)` 三段堆叠（1440：300/22 与 322/24；390：308/44 与 352/44），字段变高 | `item910b.json` | 有错误时 `extra={errors.length ? undefined : help}`，或改用 `Form.Item help/validateStatus` 自绘 |
| X4 | 目录行不可解析时 `/api/models` 直接 500 抛 Go 原文（`invalid character…` / `cannot unmarshal…`） | `state-matrix2.json` 各 api 字段 | `internal/gateway/read_models.go:762 handleModels`（HTTP 入口 `:796`）把坏行降级为 `{"models":[],"catalogError":"invalid-row"}` |
| X5 | 清空数字字段会写入 `"contextWindow":null,"maxTokens":null`（本意是删键，`=== ''` 判定漏了 `null`） | `cross-field.json .clear-numbers` | `submit()`（`model-catalog.tsx:125-146`）把 `null` 与 `''` 同样 `delete row[key]` |
| X6 | id 唯一性区分大小写：`local-b` 与 `LOCAL-B` 可共存，`/api/models` 返回两条，设置页两行都带「默认」Tag，输入框下拉两条都 selected | `final-states.json .dupDefault`、截图 [123-dup-default](audit-model-123-dup-default.png) | 明确语义：如按后端一致（大小写敏感）就保留，但在新增时给"疑似重复"提示；如应拒绝则 compare 前 `toLowerCase()` |
| X7 | 编辑路径的跨字段错误**不实时出现**（值来自 setFieldsValue、未被 touch），只在点保存时才报 | `cross-field.json .edit-cross-submit`（`live=[] / afterSubmit=["不能大于上下文窗口"]`） | `maxTokens` 的 `rules` 改为 `dependencies` + `shouldUpdate`，或在 `contextWindow` 的 `onChange` 里 `form.validateFields(['maxTokens'])` |
| X8 | 回车不提交（`press Enter` → 0 帧、弹窗仍开），也无提示 | `numeric-matrix.json .enter-key` | 需要就 `onPressEnter={() => void submit()}`，不需要可不动（但要在 PR 里写明） |
| X9 | 名称无长度上限（300 字可存），列表/确认框/输入框都会原样渲染 300 字 | `numeric-matrix.json .name-300`、[103-390-long-name](audit-model-103-390-long-name.png) | `maxLength`（如 60）+ 行内 ellipsis；否则 390 下确认框标题被撑满 |
| X10 | 禁用态 placeholder 对比度极低：浅色 ≈1.8:1、深色 ≈2.3:1（而这句正是"为什么不能选"的说明） | `contrast.json .empty-light/.empty-dark .pickerValue` | 用 `--zk-label-secondary` 级别的色给禁用 placeholder，或干脆把原因只放在下方 `.zk-model-empty`（现有 6.07/8.15）并把 placeholder 改成短词「未设置」 |
| X11 | 表单错误**没有** `role="alert"/aria-live`，且校验失败后焦点仍停在「保存」按钮、不滚到第一个错误字段 | `edge-cases.json .a11y`（`errorA11y.role=null`、`focusAfterError=BUTTON`） | `Form` 加 `scrollToFirstError`，错误节点加 `role="alert"`（antd 可用 `help` 包装） |
| X12 | 该组件**零单元测试**：`model-catalog.tsx` 只被 `settings-screen.tsx` import，`src` 内没有任何 test 渲染它；`ia.test.ts` 只断言 key 被声明（`:45`） | `grep -rn "ModelCatalog" web/src` / `vitest run src/features/settings` → 9 tests 全绿但与组件无关 | 至少补：空态/三态/五条校验/删默认切默认/P0 的 `[null]` 回归 |

## 5. 状态矩阵（19 种目录形态，每形态一个新网关进程）

| 形态 | 页面 | picker | `/api/models` |
|---|---|---|---|
| 无键 / `""` / `"   "` | `还没有模型…` | disabled | 200 |
| `[]` | `目录已被清空…` | disabled | 200 |
| `[{ok}]` | 1 行 | 可用 | 200 |
| **`[null]` / `[null,{…}]` / `[{…},null]`** | **白屏（P0）** | 不存在 | 200（返回 id ""） |
| `[{}]` | 行显示 `undefined` | 可用 | 200 |
| `[{"id":123}]` | 行显示 `123` | 可用 | **500** |
| `[{"contextWindow":"abc"}]` | 行显示 `NaNk` | 可用 | **500** |
| `[1,2,3]` | 3 行 `undefined` | 可用 | **500** |
| `{"a":1}` | broken 告警 | 无 | **500** |
| `null` | broken 告警 | 无 | 200（`defaultModel` 仍指向不在列表里的 id） |
| `this is not json` | broken 告警 | 无 | **500** |
| 重复 id ×2 | 2 行（默认时两行都带 Tag） | 可用 | 200 |
| 缺 name | 1 行（回退到 id） | 可用 | 200 |
| 字段类型错乱 | 1 行 | 可用 | **500** |
| 60 行 | 60 行全渲染 | 可用 | 200 |

## 6. 校验矩阵（45 条：字段 / 边界 / 跨字段 / 交互）

**id（14 条）**：空/纯空格 → `请填写模型 ID`；含空格、前导空格、`-` `.` `_` `/` `:` 开头、CJK、内嵌 Tab → `只能使用字母、数字和 . _ : / -，最长 120 字符，且不能以符号开头`；120 字符 → 接受（写入且长度正确）；121 字符 → 拒绝；`a.b:c/d-e_f9` → 接受；`local-a` → `这个 ID 已经在目录里`；`LOCAL-A` → 接受（大小写不敏感=允许，见 X6）。**全部非法用例 0 写入。**

**baseURL（14 条）**：空 → 接受（回落全局）；`http://x`、`https://api.example.com/v1`、`http://[::1]:8080/v1`、`https://x/v1?k=1`、2000 字符 URL → 接受；`HTTP://x`（大写 scheme）、`ftp://x`、`not-a-url`、`http://x y`、` http://x`、`http://`、`http:/x`、`javascript:alert(1)` → 全部拒绝并显示 `需要以 http:// 或 https:// 开头，且不能包含空格`。**全部非法用例 0 写入。**
→ 唯一可疑：`HTTP://` 被拒。RFC 3986 里 scheme 不区分大小写，用户会困惑；建议比较前 `toLowerCase()`（P3）。

**contextWindow（8 条）**：空 → 接受（键被删）；`0` → 接受；`1`/`1023`/`2000001` → 拒绝 `留空或 0 表示未知；否则需在 1024–2000000 之间`；`1024`/`2000000` → 接受；**`1024.5` → 接受（P1-A）**；`-1024`/`abc` → Enter 被 InputNumber 吃掉，最终写 0（安全）。

**maxTokens（5 条）**：`=contextWindow` → 接受；`>contextWindow` → 拒绝 `不能大于上下文窗口`；无窗口 + `999999` → 接受；**`12.5` → 接受（P1-A）**；`-5` → 夹成 0。

**跨字段（3 条）**：新增流程里先填 maxTokens 再下调 window → **实时**报错、提交被拦、0 写入；编辑流程下调 window → 不实时报错但**提交被拦**（X7）；编辑时上调 maxTokens 超窗口 → 实时报错 + 提交被拦。

**交互（5 条）**：连点两次「保存」→ 只有 1 条 set、只新增 1 行（无重复）；回车不提交（X8）；「×」关闭丢弃草稿、重开为空表单；表单 8 个字段全部按 `label → 控件 → 错误` 分层；保存成功后 `settings.json` 与 `/api/models` 同步。

## 7. 响应式矩阵（9 个视口，浅色）

（`out3/visual-matrix.json .widths`，逐视口实测）

| 视口 | 横向溢出 | 弹窗 | body sh/ch (overflow) | 底部按钮 | 滚到 body 底部后按钮 |
|---|---|---|---|---|---|
| 320×640 | 无 | 304×474 @y108 | 824 / 358 auto | 530–562 在视口内 | 不动、仍在视口 |
| 360×740 | 无 | 344×530 @y108 | 824 / 414 auto | 586–618 在视口内 | 同上 |
| 390×844 | 无 | 374×589 @y108 | 824 / 473 auto | 645–677 在视口内 | 同上 |
| 414×896 | 无 | 398×618 @y108 | 804 / 502 auto | 674–706 在视口内 | 同上 |
| 768×1024 | 无 | 560×636 @y100 | 784 / 520 auto | 684–716 在视口内 | 同上 |
| 1024×768 | 无 | 560×546 @y100 | 784 / 430 auto | 594–626 在视口内 | 同上 |
| 1280×800 | 无 | 560×564 @y100 | 784 / 448 auto | 612–644 在视口内 | 同上 |
| 1440×900 | 无 | 560×620 @y100 | 784 / 504 auto | 668–700 在视口内 | 同上 |
| 1920×1080 | 无 | 560×636 @y100 | 784 / 520 auto | 684–716 在视口内 | 同上 |

390 专项：长名称行 `scrollWidth==clientWidth`（截断不溢出）；删除确认框 `x=0,w=390`，`默认会切换到「另一个」` 完整可见（[104-390-delete-confirm](audit-model-104-390-delete-confirm.png)）。

## 8. 深色（`data-theme=dark`）+ 浅色 逐角色对比度

| 角色 | 浅色 | 深色 |
|---|---|---|
| 正文/标题/标签 | 16.48 | 13.59 |
| 行副标题 | **3.36** | 4.52 |
| 弹窗 help | **3.36** | 4.41 |
| 弹窗错误 | **3.27** | **3.89** |
| 弹窗主按钮（白字） | 6.61 | **2.87** |
| 空态/清空提示 | 6.07 | 8.15 |
| 损坏提示标题 / hint | 17.15 / 5.51 | 14.06 / 6.36 |
| 悬空默认提示 | 6.79 | 8.72 |
| 「默认」Tag | 5.50 | 4.91 |
| 页脚状态文字 | 3.36 | 4.52 |
| 禁用 picker placeholder | **1.79**（0.25 黑 on 0.04 黑） | **≈2.3**（0.25 白 on 0.08 白 + #111118） |

结论：深色**可读性达标**（正文 13+、辅助 4.4+），只有三处低于 AA 4.5 且两处属既有 token（错误色、主按钮），一处是本次新增的禁用 placeholder（X10）。深色 390 也单独测了（[116-dark-390-settings](audit-model-116-dark-390-settings.png)、[117-dark-390-modal](audit-model-117-dark-390-modal.png)）。

## 9. a11y 事实

| 项 | 实测 | 判定 |
|---|---|---|
| picker `label[for]` ↔ `input#id` | 正确关联 | ✓ |
| picker role / tabIndex | `role=combobox`、`tabIndex=0`、`aria-expanded` 存在 | ✓ |
| 键盘操作 | 聚焦后 Enter 打开、↓+Enter 选择并写入 `chat.model`（帧 `chat.model=local-a`） | ✓ |
| 弹窗语义 | `role=dialog`、`aria-modal=true`、`aria-labelledby` | ✓ |
| 弹窗打开焦点 | 落在 `.ant-modal-close`（×），不是第一个输入框 | 一般 |
| Tab 顺序 | 名称→ID→服务地址→API 密钥→思考系列→上下文窗口→最大输出→备注→footer | ✓ |
| 错误可访问性 | 错误节点 `role=null / aria-live=null`；输入框有 `aria-invalid=true` 与 `aria-describedby` | ✗（X11） |
| 校验失败焦点 | 停在「保存」按钮，无 `scrollToFirstError` | ✗（X11） |
| 状态区 role | broken `role=alert`；stale `role=status` | ✓ |

## 10. 与 DSH 的模型配置面对照（用户新增方向：配置项太少）

我用本机安装的 `@deepseek-ai/dsh` **0.2.2-rc.5** 作为参照（`node_modules/@deepseek-ai/`）。

| 层 | DSH 提供 | 证据 |
|---|---|---|
| 提供方 | 目录内置（OpenAI/Anthropic/Kimi…）+ 自定义 API：**协议**（OpenAI / Anthropic / Gemini 兼容）、route、显示名、Base URL、API Key（含 `$DSH_HOME/.env` 环境回落、密钥存储与清除） | `dsh-client-ui-settings-models/src/client/CustomProviderCard.tsx:238-293`、`ProviderEditor.tsx:420-600`、`locales.ts:18-23` |
| 传输策略 | 重试策略：模式 normal/always、`maxRetries`、`retryableCodes`、`backoff{initialDelayMs,maxDelayMs,jitterRatio}` | `RetryPolicyEditor.tsx:20-45` |
| 单模型 | id、显示名、`contextWindow`、`maxTokens`、**输入模态**（text/image + 图片预算）、**推理档位**（off/low/medium/high，可改每档 wire 值）、拖拽排序 | `ModelRow.tsx:34-60`、`ModelInputTypes.tsx:35-60`、`ModelReasoningEfforts.tsx:17-40` |
| 能力元数据 | `LlmModelInfo{inputModalities,badge,…}`、`LlmModelContext`、`LlmModelReasoningInfo{efforts,default}`、`LlmImageRequestBudget`、`systemPromptUpdate` | `dsh-llm/src/types.ts:369-430` |

ZeoKeel 现状：**后端模型行有 14 个字段**（`internal/modules/modelcatalog/catalog.go:30-62`：`id,name,note,contextWindow,baseURL,apiKey,family,defaultThinking,thinkingModes[],temperature,topP,maxTokens,3×价格`），其中 `temperature/topP/maxTokens` 会真正下发（`internal/contracts/modelcatalog.go:180-198`），思考档位由 `family`+`thinkingModes` 解析（`internal/contracts/thinking.go:160-175`），价格进 `ModelInfo.HasPricing/Cost`（`read_models.go:745-751`）。

而**页面只暴露 8 个**（`web/src/features/settings/model-catalog.tsx:293-390`）：名称、模型 ID、服务地址、API 密钥、上下文窗口、思考系列、最大输出、备注。

差距（按性价比排序）：

| # | 缺失 | 现状 | 影响 | 建议 |
|---|---|---|---|---|
| 1 | `temperature` / `topP` | 后端有、下发有、页面无 | 采样参数只能手改 JSON | 弹窗加两个 InputNumber（我已实测"编辑器不认识的字段会原样保留"，加字段零风险） |
| 2 | 三个价格 | 后端有、`/api/models` 有 DTO、Cost 已实现，页面无 | 上下文成本永远"未知"，等于白做 | 加 3 个 InputNumber（输入/缓存/输出），行内显示单价 |
| 3 | `defaultThinking` / 自定义档位 | 契约已支持，页面只给"思考系列"下拉 | 非内置家族无法声明自己档位 | 用 `/api/models` 已返回的 `thinkingModes` 填一个默认档位下拉 |
| 4 | 输入模态（text/image） | 概念都没有 | 图片能力只能靠猜；多模态预算无真值 | 新增字段 + 两个复选（参考 DSH `ModelInputTypes`） |
| 5 | 协议/提供方分组 | 全部行 OpenAI 兼容 | Anthropic/Gemini 原生端点接不了；每行重复填 key | provider 卡片（baseURL/apiKey 共享）+ 每行只填 id/name，属大改 |
| 6 | 重试策略 | 无 | 429/5xx 行为不可调 | 「模型服务」区加一组行（provider 级） |
| 7 | 目录内排序 | 无（只能删了重加） | 默认菜单顺序 = 存储顺序 | 拖拽或上/下移按钮 |

## 11. 「模型服务」这一块为什么还留着？算不算和模型行重复？（用户追问）

结论：**功能上没有重复，是"回退链"；但这个区现在混了三类东西 + 两处文案把覆盖方向说反了，所以看起来像重复。** 而且继承场景下页面和 API 都在说假话。

### 11.1 真实解析链（代码级）

`internal/contracts/modelcatalog.go:209-222 ModelConnection`：**模型行自己的 baseURL/apiKey 优先**，行里没写才回退到全局 `chat.baseURL` / `chat.apiKey`，全局再空才用装配/环境默认。也就是说：

```
模型行 baseURL/apiKey  →  chat.baseURL/chat.apiKey（全局回退）  →  装配配置 / 环境变量
```

再加一句：`modelcatalog` 模块启动时会把"老的全局连接"**一次性迁进模型目录**成为一行（`LegacyConnectionModelInfo`，同文件 225-245 行），所以目录才是连接事实的新家，全局那对只剩"回退"身份。

### 11.2 实测（干净实例：全局填 `https://global.example.com/v1` + `GLOBAL-KEY`，一个模型自带地址、一个不写）

```
行 1: 自带地址 · 默认 · own · https://own.example.com/v1 · 密钥已设置
行 2: 继承全局 · inherit · 跟随全局服务地址 · 密钥未设置          ← 实际会用到 GLOBAL-KEY
模型服务区: 接口地址 / API Key / 上下文预算 / 对话模式 / 系统提示词
            接口地址 help = "留空跟随模型提供方默认值。"            ← 说的是"全局→provider"，与行内文案方向相反
GET /api/models → [{id:own, baseURL:"https://own.example.com/v1", apiKeySet:true},
                   {id:inherit, apiKeySet:false}]                   ← 回退后的地址/密钥都不解析
```

截图 [130-global-vs-row](audit-model-130-global-vs-row.png)；`out3/item-fallback`：`data-audit-fallback/settings.json` + 上述 `/api/models` 原文。

### 11.3 这个区的三个问题

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| X13a | **归属混了**：`chat.contextBudget`（上下文预算）、`chat.mode`（对话模式）、`chat.systemPrompt`（系统提示词）是**对话/Agent 级**设置，不是模型连接；它们挂在"模型服务"标题下，才让人以为"模型又配一遍" | `ia.ts:137-160`（同区 5 行）；`internal/modules/agent/context_budget.go:7,86`、`plan_mode.go:179`、`chat.go:400` | 把这三行移到「行为与限额」；「模型」页只留连接事实（全局回退 + 目录） |
| X13b | **文案方向相反**：全局 `接口地址` 的 help 是"留空跟随模型提供方默认值"，行内是"跟随全局服务地址"——谁覆盖谁，用户读不出来 | `zh.ts:506`（`settings.ia.help.baseUrl`）vs `zh.ts:587`（`settings.model.inheritProvider`） | 区标题改「默认连接（模型目录的回退）」；help 改"模型行留空时用它；这里留空则用启动配置/环境变量"，并在区里显示"当前 N 个模型继承此地址" |
| X13c | **继承时说了假话**：行内写"密钥未设置"、`/api/models` 给 `apiKeySet:false`，但该行实际会用全局 key；API 也**不返回**回退后的 baseURL | 上面 §11.2 实测；`internal/gateway/read_models.go:770-790` 只序列化行内字段 | 行内密钥改三态：`自带密钥` / `继承全局密钥` / `未配置`；`/api/models` 增加 `effectiveBaseURL` + `keySource: own|global|env|none`（只读字段，不改写入面） |
| X13d | 顺带：`chat.mode` 是自由文本框，而它只接受 `standard/minimal/creator`，**拼错会被静默归一成 standard** | `plan_mode.go:183-199`（default → standard）；`ia.ts:153`（`kind:'text'`） | 改成和「权限模式」一样的单选（`radio`），或至少加下拉；未知值不要静默吞掉 |

**要不要删掉这个区？** 不建议整块删：`chat.baseURL/apiKey` 仍是目录之外唯一的全局回退（且装配/环境变量场景仍要用它）。最小改法是 §11.3 的 X13a + X13b：**改名 + 只留两行连接事实 + 显示继承计数**，重复感就消失了；X13c 让"到底用哪个地址/密钥"第一次在 UI 和 API 里都说得清。

## 12. 我这轮**没**覆盖到 / 需要有人补

1. 真实 provider 连通性：全程 fakemodel（`127.0.0.1:99xx/v1`），`baseURL` 只验证了形状，没验证"能连通"。
2. 英文界面（`en.ts` 新增 17 条 key）未逐条目视；全部跑的是 `zh`。
3. 并发写入（两个标签页同时保存同一行）未测 last-write-wins 行为。
4. 设置页历史/撤销（`settings.set` 是否可回滚）未测。
5. 打开弹窗时 `transition` 过程中的重复点击（我在 700ms 后点击，未覆盖 0–50ms 抖动窗口）。
6. 触屏真实手势（只有 390/320 视口尺寸，没有 touch 事件）。
7. P0-B（页脚保存覆盖目录）我只覆盖了"页面字段先 dirty 再改目录"这一触发序；反向顺序（先改目录再 dirty）实测**不会**覆盖（`out3/footer-wipe.json`），但"多个标签页各自 dirty"的组合未测。
8. WS 帧捕获有一处漏抓（会话内切思考档：`session.json .thinkingFrames=[]`，但 `settings.json` 里确实写入了 `session.<id>.model.ds.thinking=reasoning-low`）——是采集侧问题，不是产品问题，报告里如实标注。

## 13. 复验（round-4，新冻结构建：commit `fa573b7` / 二进制 `zeokeel-conn` / bundle `7b034a2355f7`）

- 环境：`data-model3` 的网关（`gateway.json` → port 9916，token `ny2w…`，日志确认 bundle `7b034a2355f7`，构建 2026-11-24T15:44:16Z）；**每个用例另起全新网关进程 + 全新数据目录**（`ZK_BIN=/data/zeokeel/.cache/bin/zeokeel-conn`），互不污染
- 新增截图 76 张：`findings3/audit-model-r2-*.png`；数据：`out3/r2-*.json`（帧原文、DOM 采样、`settings.json`、`/api/models` 原文）

### 13.1 判定总表

| 项 | round-3 判定 | round-4 复验 | 关键证据 |
|---|---|---|---|
| **P0-A** 页脚保存覆盖目录 | 数据丢失 | **PASS**（6 种顺序 + 3 条回归探针全过） | 每次保存只发 `chat.baseURL`/`chat.mode`；目录行保留、删除不复活、改名不回滚 |
| **P0-B** 非法行白屏 | 白屏无恢复 | **PASS**（8 种非法形态全部进损坏态并可恢复） | `#root` 18219 字符、侧栏在、`pageerror` 0、`restore` 可用、恢复后能加 |
| **P1-A** 小数 tokens | 落盘 → API 500 | **FAIL（部分修好）**：`contextWindow` 小数/越界/`1e3`/`-1` 全拦住，**但 `maxTokens=12.5` 在没有 `contextWindow` 时仍落盘 → `/api/models` 500 → 输入框模型菜单“暂无数据”** | §13.3 |
| **P1-B** Esc 穿透 | 弹窗/下拉 Esc 离开设置页 | **PASS**（10 个场景） | 3 种浮层 × 干净/脏页：URL 仍在 `/settings/model`、无叠层、草稿保留；无浮层时 B2 行为保持 |
| **悬空默认**（3 路径） | 2 条路径漏 | **PASS**（3 路径 + 2 条探针） | §13.5 |
| 原 10 条清单 | 10/10 PASS | **10/10 PASS，无回归** | §13.6 |
| round-3 的 8 项 P2 | — | **3 PASS / 5 仍未修** | §13.7 |

### 13.2 P0-A：6 种顺序，帧级证明「目录行与页面字段互不覆盖」

种子：目录 `[local-b, local-a]`，`chat.model=local-b`；`dirty()` 每次写入**不同**的 `chat.baseURL`（上一次的旧值会被 React 视作无变化）。

| # | 顺序 | 结果目录 | 页脚 保存 的帧 |
|---|---|---|---|
| 1 | 页面 dirty → 新增 → 保存 | `[local-b, local-a, added-x]` | `chat.baseURL=https://edited-1…` |
| 2 | 页面 dirty → 删除 local-a → 保存 | `[local-b]`（**未复活**） | `chat.baseURL=https://edited-2…` |
| 3 | 页面 dirty → 改名 local-a → 保存 | `local-a` 名字=`改过名字`（**未回滚**） | `chat.baseURL=https://edited-3…` |
| 4 | 先新增 → 再 dirty → 保存 | `[local-b, local-a, added-y]` | `chat.baseURL=https://edited-4…` |
| 5 | 交替两轮：dirty+add alt-1 → 保存 → dirty+add alt-2 → 保存 → dirty+删 alt-1 → 保存 | 每步 `[b,a,alt-1]` → `[b,a,alt-1,alt-2]` → `[b,a,alt-2]` | 每次只 `chat.baseURL=edited-5/6/7…` |
| 6 | 交替：dirty → 新增 mid → 保存 → 再新增 after | `[b,a,mid]` 保存后 → `[b,a,mid,after]` | `chat.baseURL=https://edited-8…` |

回归探针（`out3/r2-regressions.json`）：

- **目录变化不再让页面变 dirty**：加完模型后页脚仍 `disabled=true`、文案 `已与网关快照一致`（符合新设计：目录自己写自己）
- **草稿仍拥有非组件行**：改 `Agent 预设` 文本域 → 保存只发 `presets.agents=[…]`，目录 `c1` 保留
- **dirty 后再加一行再保存**：只发 `chat.mode=minimal`，目录 `[a, e1]` 保留

截图：[r2-01](audit-model-r2-01-dirty-add.png)、[r2-02](audit-model-r2-02-dirty-delete.png)、[r2-03](audit-model-r2-03-dirty-rename.png)、[r2-04](audit-model-r2-04-add-dirty.png)、[r2-05](audit-model-r2-05-alternate.png)、[r2-06](audit-model-r2-06-interleave.png)、[r2-70](audit-model-r2-70-draft-vs-catalog.png)、[r2-73](audit-model-r2-73-dirty-order.png)

### 13.3 P1-A：还差一条路径（阻断）

**仍然落盘的输入：`maxTokens = 12.5` 且 `上下文窗口` 留空。**

```text
openAdd → 名称/ID 填好 → 最大输出 tokens = 12.5 → 保存
结果：弹窗关闭（open=false）、无任何错误、帧 settings.set modelcatalog.models=[{…"contextWindow":0,"maxTokens":12.5}]
      GET /api/models → 500  {"message":"json: cannot unmarshal number 12.5 into Go struct field .maxTokens of type int"}
聊天页输入框模型菜单 → 下拉「暂无数据」，当前模型只剩裸文本 y（截图 r2-82）
```

其余四种（含 lead 点名的）都拦住了：

| 输入 | 结果 |
|---|---|
| `contextWindow=1024.5` | 拒绝（字段下报 `留空或 0 表示未知；否则需在 1024–2000000 之间`），0 写入 |
| `contextWindow=1024.5` + `maxTokens=512` | 拒绝，0 写入 |
| `maxTokens=12.5` + `contextWindow=4096` | 拒绝（`请填写整数`），0 写入 |
| `maxTokens=12.5` 先填、再填 `contextWindow=4096` | 拒绝（`请填写整数`），0 写入 |
| `contextWindow=1e3` | 拒绝，0 写入（**但输入框显示 1000、报的是范围错**——见 §13.8） |
| `contextWindow=-1` | InputNumber 夹成 0，落盘 `contextWindow:0`（合法），API 200 |
| `contextWindow=2000001` | 拒绝，0 写入 |
| `maxTokens=0.5 / 1.5`（有窗口） | 拒绝（`请填写整数`） |
| `contextWindow=1024.0001 / 2500000` | 拒绝 |
| `contextWindow=2000000 & maxTokens=2000000` | 落盘，`/api/models` 200 |
| `contextWindow=1048576 & maxTokens=8192` | 落盘，`/api/models` 200，`defaultModel == chat.model == int-model` ✅ |

**根因（`web/src/features/settings/model-catalog.tsx:388-397`，maxTokens 的 validator）**：

```ts
const window = Number(getFieldValue('contextWindow') ?? 0);
if (!value || !window) return Promise.resolve();   // ← 没有窗口就直接放行，整数检查被跳过
if (!Number.isInteger(value)) return Promise.reject(new Error(t('settings.model.err.integer')));
```

**修法**：把整数检查提到窗口判断之前。

```ts
if (value === undefined || value === null || value === 0) return Promise.resolve();
if (!Number.isInteger(value)) return Promise.reject(new Error(t('settings.model.err.integer')));
const window = Number(getFieldValue('contextWindow') ?? 0);
if (!window) return Promise.resolve();
return value <= window ? Promise.resolve() : Promise.reject(new Error(t('settings.model.err.maxTokens')));
```

另：已落盘的坏行在页面上**看不出来**（列表正常、编辑弹窗预填 `12.5`、补上窗口也不报错，`out3/r2-p1a-residue.json .reopen-fractional-row`），用户只能从 `/api/models` 500 或输入框菜单变空反推——建议保存前对整行做一次"网关可读性"自检（幂等：把行 JSON 与已知类型逐字段比对），或后端在 `settings.set` 时拒绝非整数。

截图：[r2-80](audit-model-r2-80-frac-max-no-window.png)、[r2-81](audit-model-r2-81-frac-reopen.png)、[r2-82](audit-model-r2-82-composer-broken-catalog.png)、[r2-20-ctx-decimal](audit-model-r2-20-ctx-decimal.png)、[r2-20-max-decimal-with-window](audit-model-r2-20-max-decimal-with-window.png)、[r2-20-max-decimal-no-window](audit-model-r2-20-max-decimal-no-window.png)、[r2-20-ctx-exp](audit-model-r2-20-ctx-exp.png)、[r2-20-integers-ok](audit-model-r2-20-integers-ok.png)

### 13.4 P0-B：8 种非法形态（每种一个新网关）

| 种子 | `#root` 长度 | 侧栏 | 损坏态 + 恢复按钮 | 行数 | pageerror | 恢复后 | 再加模型 |
|---|---|---|---|---|---|---|---|
| `[null]` | 18219 | 有 | 有 | 0 | 0 | `[]` + 已清空 | ✅ `after-restore` |
| `[{}]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| `[1,2,3]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| `[{"id":""}]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| `[{"id":1}]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| `[null,{…ok}]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| `[{…ok},7]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| `[{"id":"   "}]` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |
| （对照）`[{ok}]` | 20040 | 有 | 无（正常渲染） | 1 | 0 | — | — |
| （对照）`[]` | 19142 | 有 | 无（`目录已被清空`） | 0 | 0 | — | — |
| （对照）`this is not json` | 18219 | 有 | 有 | 0 | 0 | 同上 | ✅ |

说明：`[numbers]`/`[{id:number}]` 那几行控制台里有一条 `500` 的网络错误，来自并行的 `GET /api/models`（后端未改，仍对坏行 500，见 §13.9 X4），**不是** pageerror；页面本身正常进损坏态。

截图：[r2-10-null-only](audit-model-r2-10-shape-null-only.png)、[empty-object](audit-model-r2-10-shape-empty-object.png)、[numbers](audit-model-r2-10-shape-numbers.png)、[empty-string-id](audit-model-r2-10-shape-empty-string-id.png)、[numeric-id](audit-model-r2-10-shape-numeric-id.png)、[null-mixed](audit-model-r2-10-shape-null-mixed.png)、[number-mixed](audit-model-r2-10-shape-number-mixed.png)、[whitespace-id](audit-model-r2-10-shape-whitespace-id.png)、[valid-one](audit-model-r2-10-shape-valid-one.png)、[恢复后](audit-model-r2-11-null-only-restored.png)

### 13.5 P1-B：10 个 Esc 场景

| 场景 | 结果 |
|---|---|
| 弹窗 + Esc（干净页） | 弹窗关、URL 仍 `/settings/model`、无叠层 |
| 弹窗 + Esc（脏页） | 弹窗关、页面在、草稿 `https://draft-esc…` 保留、**无**「放弃未保存」 |
| 下拉 + Esc（干净/脏） | 下拉关、URL 不变、草稿保留 |
| Popconfirm + Esc（干净/脏） | 浮层关、URL 不变、草稿保留 |
| 弹窗 Esc 两次（干净） | 第一次关弹窗，第二次按设计离开页面 → `/`（B2 语义保持） |
| 弹窗 Esc 两次（脏） | 第二次弹「放弃未保存的更改?」（不跳走） |
| 无浮层 Esc（干净/脏） | `/` / 弹「放弃未保存的更改?」（原行为保持） |
| 弹性：两次 Esc 间隔 150ms（弹窗动画期） | 页面留在 `/settings/model`，弹窗关闭（第二次被吞，可接受） |

截图：[r2-30-modal-clean](audit-model-r2-30-modal-clean.png)、[modal-dirty](audit-model-r2-30-modal-dirty.png)、[select-dirty](audit-model-r2-30-select-dirty.png)、[popconfirm-dirty](audit-model-r2-30-popconfirm-dirty.png)、[no-overlay-dirty](audit-model-r2-30-no-overlay-dirty.png)、[esc-fast](audit-model-r2-72-esc-fast.png)

### 13.6 悬空默认：3 条路径 + 2 条探针

| 场景 | 帧 | 结果 |
|---|---|---|
| 删默现行（有后继，`[甲,乙,丙]` 默认乙） | `models=[甲,丙]` → `chat.model=a` | picker=甲，`/api/models defaultModel=a`，无 stale |
| 删唯一行（它是默认） | `models=[]` → `chat.model=` | picker 无法选、`目录已被清空…`、`defaultModel` 省略 |
| 默认已悬空（ghost）+ 删别的行（剩 1 行） | `models=[甲]` → `chat.model=a` | stale 消失、picker=甲、`defaultModel=a` |
| 默认已悬空 + 删唯一行 | `models=[]` → `chat.model=` | stale 消失、picker disabled、`defaultModel` 省略 |
| 未选默认（`chat.model=""`）+ 删一行 | `models=[甲]` → `chat.model=a` | **副作用**：用户没选过默认，删行后被自动设为剩余首行 |
| 删非默现行（默认=甲） | 只有 `models=[甲]` | `chat.model` 不动（无多余写入） |

截图：[r2-40-A](audit-model-r2-40-A-default-with-successor.png)、[r2-40-B](audit-model-r2-40-B-last-row-default.png)、[r2-40-C](audit-model-r2-40-C-stale-delete-other.png)、[r2-40-C2](audit-model-r2-40-C2-stale-delete-only.png)、[r2-40-D](audit-model-r2-40-D-no-default-delete.png)、[r2-40-E](audit-model-r2-40-E-delete-nondefault.png)

### 13.7 原 10 条清单（新构建回归）

| # | 判定 | 证据 |
|---|---|---|
| 1 | PASS | `.ant-select` 计数 1（搜索框输入 `chat.model` 后仍 1）；无 `chat.model` 输入框；模型服务区 label 仍是 接口地址/API Key/上下文预算/对话模式/系统提示词 |
| 2 | PASS | 未配置：`还没有模型…`、picker disabled、placeholder `未设置：先从目录里添加一个模型`、无 broken |
| 3 | PASS | 帧 `models=[first…]` → `chat.model=first`；行带「默认」；reload 后同；`defaultModel=first` |
| 4 | PASS | 5 例全 `wrote=0`：id 空格/重复、url 非法、ctx 500、max>window 各自字段报错 |
| 5 | PASS | 确认框 `删除「乙」？…默认会切换到「甲」。`；帧 `models=[甲]`+`chat.model=a`；`defaultModel=a` |
| 6 | PASS | `[]` → `目录已被清空。添加一个模型之后才能发送。`（见 §13.4 对照） |
| 7 | PASS | 损坏告警 + `恢复为空列表` → 帧 `models=[]` → 已清空态 |
| 8 | PASS | 设置页 picker=乙，会话 composer=乙，下拉 `[甲,乙]`（乙选中）；改选甲 → 帧 `chat.model=a`；reload 后=甲 |
| 9 | PASS（同 round-3 偏离） | 1440：弹窗 560×620 @y100，body 784/504 auto，按钮 y=668 在视口；390：374×589 @y108，body 824/473，按钮 y=645；无横向溢出。**错误仍不替换 help**（err y=300 h=22 → help y=322 h=24） |
| 10 | PASS | 深色 computed：picker `rgb(20,20,20)`/`rgba(255,255,255,.85)`；行标题 .85；行副标题 .45；Tag `rgb(17,26,44)`/`rgb(60,137,232)`；输入 `rgb(20,20,20)`/.85 |

### 13.8 行为变化（非阻断，但要写进变更说明）

1. **未选默认时删除任意行会把默认设为剩余首行**（§13.6 第 5 行）。合理（避免无默认），但属于"删除顺手改了另一个设置"，建议只在"删默认/默认悬空"时重指，或至少不与用户的显式空白冲突。
2. **目录变化不再让页脚变 dirty**：加完模型页脚仍显示 `已与网关快照一致`（`disabled=true`）。符合新设计，但"保存"按钮与目录无关这件事，用户第一次会困惑；建议在模型目录区放一句"目录的改动即时生效"。
3. **`1e3` 的提示自相矛盾**：输入框显示 `1000`，错误却说"需在 1024–2000000 之间"（表单值是 NaN，显示值是格式化后的 1000）。建议对非整数/无法解析的输入用"请填写整数"。
4. `contextWindow` 小数报的是"范围"文案而不是"请填写整数"（`err.integer` 只用在 maxTokens 上）。

### 13.9 仍然存疑 / 未修（按优先级）

1. **P1-A 残留**（阻断）：`maxTokens` 小数 + 无 `contextWindow`。
2. **X4 未修**：`/api/models` 对坏行仍 500（`[null]` 反而 200 返回空行 id）。叠加 1 就有用户可见后果（输入框菜单变空）。
3. **六项 P2/P3 未修**（见 §13.10 表）：错误无 aria-live、编辑路径跨字段不实时、清空数字写 `null`、id 大小写重复、名称无长度上限、`chat.mode` 自由文本、悬空 placeholder 文案。
4. **本轮修复没有自动化测试**：`git show --stat fa573b7` 只改 3 个源文件，没有新增/修改测试；`web/src` 里仍无任何测试渲染 `ModelCatalogEditor`。P0-A/P0-B/P1-B 三条都只能靠浏览器验收，建议至少补：目录脏顺序保存不覆盖、`[null]` 进损坏态、Esc 不离开页面、`maxTokens` 小数（含无窗口）。
5. `overlayOwnsEscape()` 是 DOM 嗅探（`.ant-modal-wrap` / `.ant-select-dropdown` / `.ant-popover` + `getBoundingClientRect`），antd 升级改名即静默失效；建议加测试锁住。
6. 双标签页"各自 dirty + 目录并发"未测；真实 provider 连通性未测；触屏手势未测。

### 13.10 round-3 的 8 项 P2：本轮逐条判定

| # | 项 | round-4 判定 | 证据（`out3/r2-p2.json`） |
|---|---|---|---|
| 1 | 悬空默认的 placeholder 文案 | **FAIL（未修）** | 目录里有 1 行、`chat.model=ghost` 时 picker 仍显示 `未设置：先从目录里添加一个模型`，而同一屏的 stale 文案说「ghost」不在目录里——两句互相矛盾；截图 [r2-60](audit-model-r2-60-stale-copy.png) |
| 2 | 「已悬空默认 + 删唯一行」 | **PASS** | `chat.model=""`、stale 消失、picker disabled、`目录已被清空…` |
| 3 | 错误是否 aria-live | **FAIL（未修）** | `role=null`、`aria-live=null`；输入框有 `aria-invalid=true` + `aria-describedby=name_help`；校验失败后焦点仍在「保存」按钮，无 `scrollToFirstError` |
| 4 | 编辑路径跨字段实时校验 | **FAIL（未修）** | 编辑态把窗口降到 2048（maxTokens 4096）：`live=[]`，点保存才报 `不能大于上下文窗口` |
| 5 | 清空数字是否写 `null` | **FAIL（未修）** | 行落成 `{"contextWindow":null,"maxTokens":null}`（`/api/models` 200；语义上"删键"没做到） |
| 6 | id 大小写重复 | **仍允许**（建议明确语义，不算缺陷） | `LOCAL-B` 与 `local-b` 并存，`/api/models` 返回两条，设置页可分别编辑 |
| 7 | 名称长度上限 | **FAIL（未修）** | 300 字名称保存成功（`nameLen=300`），行/确认框/输入框都原样渲染 |
| 8 | `chat.mode` 自由文本 vs 只认 `standard/minimal/creator` | **FAIL（未修）** | 仍是 `<input type="text">`（同区 radio 计数 0）；输入 `creater` 原样保存，后端静默归一，无提示 |

（另有 round-3 的 P3 未修项与本次无关者：错误不替换 help（§13.7 #9 已复测）、`/api/models` 坏行 500（§13.9）、`modelcatalog` 组件无单测。）

## 14. 第三轮复验（round-5，新冻结构建：commit `8976472` / 二进制 `zeokeel-conn2` / bundle `3f982a3517a4`）

- 构建一致性：`zeokeel-conn2`（00:14 构建）与 `data-model4` 实例日志都是 `bundle sha256 3f982a3517a4 (6 files, built 2026-11-24T16:14:11Z)`。每个用例仍是**全新网关 + 全新数据目录**（`ZK_BIN=zeokeel-conn2`），端口从各自 `gateway.json` 读。
- 新增截图 80 张：`findings3/audit-model-r3-*.png`；数据：`out3/r3-*.json`。

### 14.1 判定总表

| # | 要求 | 判定 | 关键证据 |
|---|---|---|---|
| 1 | P1-A 残留（小数 tokens） | **PASS** | 7 个用例全对：`maxTokens=12.5`（有/无窗口）→ `请填写整数`、0 写入、行不存在；`contextWindow=1024.5 / 1e3 / 2500000 / 2000001` → 范围错误、0 写入；整数 `1048576/8192` → 落盘且 `/api/models` 200 |
| 2 | P2-1 悬空 placeholder | **PASS** | 目录有行 + `chat.model=ghost`（或未选默认）→ placeholder `选择一个默认模型`，与 stale 文案并列不再矛盾；目录为空才用 `未设置：先从目录里添加一个模型` + disabled |
| 3 | P2-2 aria-live / 焦点 | **PASS** | 提交失败出现 `<p class="zk-form-error" role="alert">有字段还没填对，已定位到第一个问题。</p>`；焦点：全空 → `input#name`（`data-testid=model-field-name`），只有 ID 空 → `input#id`；修好并提交后汇总消失、弹窗关闭；仍有错时汇总保留 |
| 4 | P2-3 跨字段实时校验 | **PASS** | 编辑态把窗口 4096→2048，**300ms 内**就出现 `不能大于上下文窗口`（无需点保存）；再降到 1024 依旧实时 |
| 5 | P2-4 清空数字 | **PASS** | 清空 contextWindow/maxTokens 后行只剩 `{id,name}`，`nullKeys=[]`，`/api/models` 200 |
| 6 | P2-5 名称长度 | **PASS** | 300 字 → `名称最多 80 个字符`、未保存；80 字 → 保存（`nameLen=80`）；81 字 → 拒绝（边界精确） |
| 7 | P2-6 `chat.mode` 三选一 | **PASS** | 无 `#zk-field-chat.mode` 文本输入；`role=radiogroup` 三项 standard/minimal/creator；选「标准」→ 帧 `chat.mode=standard`；选「极简」→ `minimal` 且刷新后仍选中 |
| 8 | 行为 1（未设默认时删行） | **PASS** | `chat.model=""` + 删一行 → 只有 `modelcatalog.models` 一条帧，`chat.model` 保持 `""` |
| 9 | 行为 2（即时生效说明） | **PASS** | 有目录时出现 `目录与默认模型的改动即时生效，不需要再点本页底部的保存。`；加完模型页脚仍 `已与网关快照一致` + `disabled=true` |
| 10 | 回归（清单 + P0-A + P0-B + Esc） | **PASS** | 见 §14.2 |

### 14.2 回归明细（新构建）

- **原 10 条清单：10/10 PASS**（`out3/r3-checklist.json`）
  1) `.ant-select` 计数 1（搜索态仍 1）、无 `chat.model` 输入框；2) 未配置 → `还没有模型…` + disabled；3) 首模型帧 `models=[first]`→`chat.model=first`，reload 后带「默认」tag、`defaultModel=first`；4) 5 类校验全 0 写入；5) 删默认 → 确认框「切换到甲」+ 帧 `chat.model=a`、`defaultModel=a`；6) `[]` → 已清空；7) 损坏 → `恢复为空列表` → 帧 `models=[]`；8) 设置页 乙 ↔ composer 乙、改选甲帧 `chat.model=a`（**刷新后需等设置快照落地，见 §14.3 O3**）；9) 1440 弹窗 560×620/body 784-504/按钮 y=668；390 374×589/body 824-473/按钮 y=645（**错误块现在在 y=344，因为汇总行把表单下移 44px，按钮仍在视口内**）；10) 深色 8 组 computed 与上轮一致。
- **P0-A 六种顺序：6/6 PASS**（`out3/r3-p0a.json`）——页脚保存始终只发 `chat.baseURL=`，目录行保留/删除不复活/改名不回滚；交替两轮与交错写入均正确。
- **P0-B 八种非法形态：8/8 PASS**（`out3/r3-p0b.json`）——`[null]`/`[{}]`/`[1,2,3]`/`[{"id":""}]`/`[{"id":1}]`/混合/空白 id 全部进损坏态（`#root` 19176 字符、侧栏在、`pageerror` 0、恢复按钮可用），恢复后可再加；`[{ok}]`、`[]`、非 JSON 对照正常。
- **Esc 十场景：10/10 PASS**（`out3/r3-p1b.json`）——弹窗/下拉/Popconfirm × 干净/脏页都只关浮层、URL 保持 `/settings/model`、草稿保留；两次 Esc 与无浮层行为符合 B2 契约。
- **新增边界**：id 120 字符 → 保存（`idLen=120`）；121 → `只能使用字母、数字和 . _ : / -…`；空 id → `请填写模型 ID`（名称上限的改动没有破坏 id 规则）。
- **单元测试**：`web/src/features/settings/model-catalog.parse.test.ts` 新增（8 种非法形态 + 3 种正常态），`vitest run src/features/settings` → 3 文件 / 20 测试全绿；上次报告"组件零测试"这条**部分改善**（parseRows 有测试，P0-A/Esc/maxTokens 仍无单元测试）。

### 14.3 新的观察（P3，非阻断）

- **O1 `chat.mode` 未设置时三个选项都不选中**：`chat.mode` 缺失时 `initialChecked=[]`，而网关实际按 `standard` 生效（`plan_mode.go:183-199`）。建议预选 `standard` 或显示"未设置（按标准生效）"——这与本页"说真话"的同一条原则。
- **O2 radiogroup 没有可访问名**：`role=radiogroup` 但 `aria-label=null`，需要确认 `<label>` 是否通过 `aria-labelledby` 关联；同页其他字段已有 `htmlFor` 关联。
- **O3 `/c/:id` 刷新后模型名要等 3.3–4.7s（round-4 是 1.2s）**：四轮交错测量（`out3/r3-perf.json`）：r5 = 3340ms / 4718ms，r4 = 1211ms / 1195ms；同期「权限」选择器两版都是 ~340ms，模型页自身 settle 两版都是 0.64–0.97s（`out3/r3-boot2.json`）。WS 时间线（`out3/r3-perf2.json`）显示 r5 在 reload 后 ~2.9s 才连上 WS，随后 settings.snapshot 才带回 `chat.model`。**这是聊天面/启动侧的事，不是模型页**，但会让"刷新后模型显示正确"看起来像没修；建议交给聊天面 owner 用同一探针复现。
- **O4 名称 80 是字符数**：80 个汉字可保存（81 拒绝），390 下行内会 ellipsize，可接受。
- **O5 空目录时"即时生效"说明不显示**：空态只显示 `目录已被清空…`，有行时才出现即时生效说明；属合理取舍，记录备查。

### 14.4 存疑 / 需补

1. O3 的启动/聊天面延迟需要聊天面 owner 定因（我已给可复现探针 `out3/r3perf2.mjs`）。
2. P0-A / Esc / maxTokens 三条仍无单元测试（只有 parseRows 有）；建议补。
3. `/api/models` 对坏行仍 500（后端未改；现在 UI 不会再写出小数，但手改 `settings.json` 仍能造成）。
4. 双标签页"各自 dirty + 目录并发"、真实 provider 连通性、触屏手势仍未测（与上轮一致）。
5. 本轮未重跑 45 条全量校验矩阵（清单第 4 条的 5 类 + 新增边界已覆盖），如需可再跑 `out3/p15validation.mjs`（指向 `ZK_BIN=zeokeel-conn2`）。

## 15. 产物索引

- 报告：`/data/zeokeel/.cache/ui-audit/findings3/model.md`
- 截图：`findings3/audit-model-*.png`（333 张，含 round-4 的 `audit-model-r2-*.png` 76 张、round-5 的 `audit-model-r3-*.png` 80 张；命名即用途：`70-shape-*` 形态（`-BLANK` = 白屏）、`80-*` 数值、`85-88-*` 跨字段、`90-*` 输入框菜单、`95-*` 视口、`96-*` 状态色、`110-124-*` 声称复跑、`130-*` 全局 vs 行、`131-133-*` 页脚保存覆盖、`140-*` 修复前对照、`r2-*` round-4 复验、`r3-*` round-5 复验）
- 数据：`out3/*.json`（round-3 与 round-4 两批；全部含 WS 帧原文 / DOM 采样 / API 原文 / computed 值；round-4 = `r2-p0a.json`、`r2-p0b.json`、`r2-p1a.json`、`r2-p1a-residue.json`、`r2-p1b.json`、`r2-stale.json`、`r2-checklist.json`、`r2-p2.json`、`r2-regressions.json`；round-5 = `r3-p0a.json`、`r3-p0b.json`、`r3-p1b.json`、`r3-checklist.json`、`r3-items.json`、`r3-final.json`、`r3-perf.json`、`r3-perf2.json`、`r3-boot2.json`、`r3-composer.json`）
- 脚本：`out3/harness.mjs`（每例一个网关进程的夹具，`ZK_BIN` 可切二进制）、`out3/p10…p27*.mjs`、`out3/start3.sh`、`out3/wsset.mjs`
- 未改动 `web/src`；所有写入都在 `.cache/ui-audit/**` 临时目录