# 模型配置（设置 → 模型 + 输入框联动） 功能可用性深审

- 实例：`http://127.0.0.1:9872`（ZeoKeel serve dev @ cd29938，data-dir `.cache/ui-audit/data-model`）
- 覆盖流程：**25 个**；截图：**34 张**（`shots/r2-model-*.png`）；记录的操作：**约 120 次**（点击 / 键盘输入 / WS 帧 + HTTP 响应抓取）
- 证据口径：真实 Chromium（Playwright）。`settings.set` 走 WS，用 `page.on('websocket')` 抓 `framesent/framereceived`；`GET /api/models` 用 `page.on('response')` + 页面内 `fetch` 双取；所有"界面没反应"的结论都同时给出"确实没有 WS 帧"。

## 类型统计

| 类型 | 条数 | 编号 |
|---|---|---|
| M 缺失 | 6 | M1–M6 |
| F 假入口 | 0 | —（没有"点了不发请求"的按钮，问题都在别的类型里） |
| S 静默失败 | 6 | S1, S2, S3, S4, S5, S6 |
| U 不可恢复 | 1 | U1（390 窄屏内容列宽 0） |
| H 半接线 | 7 | H1–H7 |
| V 视觉/规范（BRIEF 未列，单列） | 2 | V1, V2 |

---

## 流程总表

| # | 流程 | 结论 | 类型 | 证据 |
|---|---|---|---|---|
| 1 | 添加模型（只填 name+id） | 成功；但**额外写入 `contextWindow:0,maxTokens:0`** | H6 | WS `settings.set modelcatalog.models` = `[{"id":"min-1","name":"极简模型","contextWindow":0,"maxTokens":0}]` |
| 2 | 添加（全字段 + 非法 baseURL `not-a-url:8080/v1`） | **零校验通过**，界面无错误、后端无 400 | S1 | 同一帧里 `"baseURL":"not-a-url:8080/v1"`；行文本 `全字段full-1 · not-a-url:8080/v1 · 密钥已设置 · 128k` |
| 3 | 添加（重复 id） | 允许保存；`/api/models` 返回**两条同 id**，agent 侧只认第一条 | S5 | 帧里两个 `"id":"min-1"`；`/api/models` 出现两条 `min-1` |
| 4 | 添加（空 name） | 字段级必填拦截，弹窗不关 | 正常 | `请输入名称 / 请输入模型 ID`；`r2-model-empty-name.png` |
| 5 | 添加（id 含空格/中文/大写/300 字符） | **全部接受** | S1 | 依次落盘 `my model`、`我的模型`、`DeepSeek-Chat`、`x`×300 |
| 6 | 添加（baseURL 前后空格 / 尾斜杠） | 原样保存；`/api/models` 原样回显带空格 | S1 | `"baseURL":" http://127.0.0.1:9882/v1 "`（未 trim） |
| 7 | 编辑：改 name + contextWindow | 成功（`65536` 落盘） | 正常 | WS 帧 `"contextWindow":65536`（注：`fill()` 直塞 InputNumber 会丢值，真人键盘输入正常） |
| 8 | 编辑：apiKey 留空 | 保留原密钥（语义正确） | 正常 | 提交帧无 apiKey 字段，落盘仍 `"apiKey":"sk-test-123"` |
| 9 | 编辑：改 id | **输入框永久 disabled，无替代入口** | M1 | `r2-model-1440-edit-modal.png`；`idDisabled:true`（1440/390 均如此） |
| 10 | 设为默认 | 立即写 `chat.model`，Tag 迁移，干净表单会把"模型服务→模型"同步过去 | 正常 | WS `settings.set {"key":"chat.model","value":"full-1"}`；`#zk-field-chat.model` 变 `full-1` |
| 11 | 设为默认 × 表单脏值 | **文本框里的半截输入静默覆盖"设为默认"** | H2 | 输 `par-a` 未保存 → 点另一行"设为默认"（网关 `par-b`）→ 保存 → `settings.set chat.model=par-a`，"已保存" |
| 12 | 删除非默认模型 | 正常，Popconfirm 确认 | 正常 | `删除「全字段改名」？`；列表 9→8 |
| 13 | 删除重复 id 的一行 | **同 id 的两行一起消失** | S5 | 删第 3 行（`min-1`/重复ID）后 9→8 行，两条 `min-1` 都没有了 |
| 14 | 删除**默认**模型 | 无拦截无提示；`chat.model` 留悬空 id | H1 | 删 `full-1` 后 `/api/models` = `{"models":[6 条不含 full-1],"defaultModel":"full-1"}`；`#zk-field-chat.model` 仍 `full-1` |
| 15 | 删空目录（0 行） | 报**错误**"模型目录不是合法 JSON，已停止渲染。" | S2 | 落盘 `"value":"[]"`（合法）；`r2-model-1440-empty-list.png` |
| 16 | 空目录下发消息 | **照常跑通**（用已删除的 `chat.model` + 全局 baseURL） | S3 | 回复 `Hello from the acceptance fixture...`；页脚 `plain-a`；`/api/models={"models":[],"defaultModel":"plain-a"}` |
| 17 | 目录行自带 baseURL vs 全局 | **行内地址赢** | 正常 | 全局存死端口 9999，行内 9882 的模型跑通；无行内地址的模型 `connection refused` |
| 18 | 全局地址不可达的报错 | 有明确错误（3 处重复），泄露内部术语 | 正常 | `连接模型服务失败（网络不可达）` + `provider: openai / rig: …`；`r2-model-dead-provider.png` |
| 19 | 清空 `chat.apiKey` 后保存 | **直接写空串**，与目录行"留空=保留"相反 | H7 | WS `settings.set {"key":"chat.apiKey","value":""}`；settings.json `'chat.apiKey': ''`；仅"已保存" |
| 20 | `Agent 预设` 非法 JSON | **保存成功**，界面"已保存"，服务端仅一行日志 | S4 | WS `settings.set presets.agents={ not json ]`；`r2-model-preset-invalid-json.png` |
| 21 | contextWindow=-5 / 超巨值 / maxTokens(900000)>contextWindow(1000) | 负数静默变 0；巨大值与跨字段矛盾全部接受 | S1 | 输 `-5` → 落盘 `0`，无提示；`bound-a` 落盘 `contextWindow:1000,maxTokens:900000` |
| 22 | 输入框选择器：首屏/刷新 | 先显示"选择模型"，约 1s 后回填 | S6 | 300ms 采样 `选择模型`；1000ms 后 `ID带中文` |
| 23 | 输入框切换模型 | **写全局 `chat.model`**，所有会话一起变；草稿保留 | H3 | `settings.set chat.model=我的模型`；另一会话 picker 同步变；草稿 `DRAFT-1` 原样 |
| 24 | 运行中切换模型 | 本轮不受影响，**消息不记录产生它的模型** | M5 | 运行中 picker `慢速模型`→`并发A`，本轮照常流完；`SnapshotMessage` 无 model 字段 |
| 25 | 跨标签页新增模型 | 会话页选择器**不刷新**，必须重载页面 | H5 | 设置页加 `xpage-a`（4 行）；另一页下拉仍 3 项；`reload` 后变 4 项 |

---

## 发现

### 缺失（M）

#### M1 模型行没有"改 ID"能力，删+建是唯一路径，而删+建必然丢密钥 [P1]
- 操作：设置 → 模型 → 任一行"编辑" → 看"模型 ID"。
- DOM/截图：`r2-model-1440-edit-modal.png`、`r2-model-390-edit-modal.png`；`idDisabled:true`。
- 代码：`web/src/features/settings/model-catalog.tsx:183` `disabled={editing !== null}`；删除是整行过滤（`:99-101`）；apiKey write-only（`/api/models` 只给 `apiKeySet`，`internal/gateway/read_models.go:742,776`），删完密钥无法读回。
- 后果：id 拼错（`deepseek-chat` → `deepsek-chat`）只能删掉重建 + 重打密钥；无"复制模型"。
- 对照：DSH 的模型 id 来自 provider 目录，用户不手写；自定义项也有独立编辑表单。
- 修法：id 可编辑（保存时校验唯一 + 是否被 `chat.model`/会话覆盖引用）或提供"复制为新模型"；至少明示"ID 创建后不可改"。

#### M2 没有"测试连接 / 拉取模型列表 / 从 provider 导入" [P1]
- 操作：模型页全量走查（1440 + 390），逐词搜索入口。
- 证据：`grep -rn "测试连接|拉取模型|models/list|importModels|verify" web/src/features/settings web/src/api/read-models.ts` → 0 命中；后端没有 probe 端点，也没有对上游 `GET /v1/models` 的调用。
- 后果：填错 URL/密钥只能"发一条消息看炸不炸"——实测报 `dial tcp … connection refused`（`r2-model-dead-provider.png`），密钥错要等 401；添加 3 个模型 = 3 次盲填。
- 对照：DSH 目录由 Host 从适配器提供（`llm/models-updated` 重拉），客户端不手填 URL/密钥。
- 修法：行内"测试"（该行 baseURL+key 打 `/v1/models` 或 1-token），三态结果 + "上次验证 N 分钟前"。

#### M3 没有"当前生效的 provider 与来源" [P1]
- 证据：行副标题只有 `id · 服务地址|跟随全局服务地址 · 密钥已设置|未设置 · 128k · 系列`；`ModelInfo` 数据模型**没有 provider 字段**（`internal/contracts/modelcatalog.go:11-40`）；`/api/models` DTO 也没有（`read_models.go:736-753`）。
- 后果：一行可能指向完全不同的网关，界面不区分；"跟随全局服务地址"只说了落空时的情况，没说"本行地址覆盖全局"——而这正是实测中最容易踩的坑（流程 17）。
- 修法：行内显示"生效地址（来源：本行/全局）""生效密钥（来源：本行/全局/环境变量）"；DTO 增 `effectiveBaseURL` + `baseURLSource`。

#### M4 没有会话级模型（协议未接线） [P1]
- 证据：后端**已有** `SetSessionModel`（`internal/modules/agent/agent.go:1715-1737`）+ `session.<id>.model` 行（`internal/contracts/chat.go:471-474`），但 RPC `session.model.set` 在 `docs/TRANSPORT.md:550` 状态为"延后"；composer 的 `onChange` 直接写全局（`composer-status.tsx:145-152`）。
- 后果：用户无法"这个会话用慢模型、那个会话用快模型"，只能全局切。
- 对照：DSH 模型选择是会话级的（`session.selectModel`），composer 与 `/model` 共用同一目录。
- 修法：接通 `session.model.set`；composer 写会话覆盖并显示"本会话"角标 + "恢复默认"。

#### M5 消息不记录产生它的模型 [P1]
- 证据：`SnapshotMessage` 只有 `role/text/reasoning/attachments/steps/usage/stopReason`（`web/src/api/gateway.ts:65-77`）；`grep -n model web/src/chat/transcript.tsx` = 0。
- 后果：切换默认后回看历史无法判断哪条回复来自哪个模型；成本/质量对比做不了。实测：用 `slow-a` 发的一轮，运行中把默认切走，跑完后该轮**没有任何模型标记**。
- 修法：`session.snapshot` 的 message 增 `model` + `thinkingMode`（运行记录里本来有），usage 行显示角标。

#### M6 没有搜索 / 分组 / 批量 / 启用禁用 [P2]
- 证据：模型区只有 `添加模型` 一个按钮（`r2-model-1440-final-3rows.png`）；无搜索框、无 provider 分组、无"全部删除/导出"、无 per-model 启用开关；唯一的搜索框是左侧"搜索设置页"（搜的是设置页名与字段名）。
- 后果：模型一多（>8）只能靠肉眼扫；无法临时停用某模型而不删除（会话引用会悬空）。
- 对照：DSH 目录 >4 个模型即出现搜索框，按 provider 分组，支持模糊匹配与键盘导航。
- 修法：列表加搜索 + 分组 + 行内启用开关（禁用的行不出现在选择器里，但保留配置）。

### 静默失败（S）

#### S1 校验全空：坏 baseURL / 怪 id / 负数 / 跨字段矛盾全部放行 [P0]
- 操作：`not-a-url:8080/v1`；` http://127.0.0.1:9882/v1 `（前后空格）；id = `my model` / `我的模型` / `DeepSeek-Chat` / `x`×300；`contextWindow=-5`；`contextWindow=1000 & maxTokens=900000`。
- 网络：每次 `settings.set modelcatalog.models` 直接成功（无 `error` 帧、无 400），值原样落盘。
- 代码：`model-catalog.tsx:185-206`（baseURL 无 `rules`；两个 InputNumber 只有 `min={0}`、无上限、无交叉校验）；id 只有 `{required, whitespace}`（`:182`）；后端 `SanitizeModelInfoList` 只 trim/去重（`contracts/modelcatalog.go:158-198`），`handleModels` 甚至不走它（`read_models.go:768-788`）。
- 后果：`contextWindow:0` 与"未设置"不可区分；`maxTokens>contextWindow` 会在真实 provider 上 400，而这里要等发消息才炸；300 字符 id 把行布局与 `session.<id>.model.<model>.thinking` 键一起拉长。
- 修法：字段级校验（baseURL 必须 `http(s)://host[:port][/path]` 且无空白；id `^[A-Za-z0-9._:/-]{1,120}$` 且唯一；contextWindow 空或 1024..2_000_000；maxTokens 空或 ≤ contextWindow）。

#### S2 0 行目录被误报为"JSON 损坏" [P0]
- 操作：把 3 个模型全删光。
- 网络：落盘 `"value":"[]"`（合法 JSON）。
- DOM：`模型目录不是合法 JSON，已停止渲染。`（`r2-model-1440-empty-list.png`）。
- 代码：`model-catalog.tsx:118-122` 用 `raw.trim() && catalog.length === 0` 判定损坏；`parseRows`（`:35-44`）对 `[]` 返回空数组，所以这条提示 100% 误报。
- 后果：删完最后一个模型被告知"配置损坏"，会把用户推去手改 JSON；"未配置 / 已清空 / 真损坏"三态不可区分。
- 修法：三态分开渲染（无行 → "还没有模型"；`[]` → "已删除全部模型"；解析失败 → 才报损坏并给"恢复为空/导出原文"）。

#### S3 删空后仍能用"幽灵模型"跑（后端口径 ≠ 界面口径）[P0]
- 操作：目录 0 行、选择器空、`chat.model` 仍是已删的 `plain-a` → 直接发消息。
- 网络/DOM：消息成功并流式返回；页脚 `plain-a`；`/api/models.defaultModel` 仍是 `plain-a`。
- 代码：`agent.go:1426-1444` `defaultModelLocked` —— 只要 `chat.model` 非空就照用；"已保存空列表 → 不复活默认"的分支只在 `chat.model` 也为空时生效。
- 后果：用户以为删掉就不能用了，实际仍在用界面不存在的模型（以及它对应的旧地址/旧密钥）跑。
- 修法：目录已保存且为空时 `Send` 报可读错误（"未配置模型，请到 设置→模型 添加"）；`/api/models.defaultModel` 同规则派生（悬空返回空）。

#### S4 `Agent 预设` 是裸 JSON textarea，非法输入保存成功 [P1]
- 操作：textarea 改成 `{ not json ]` → 保存。
- 网络：`settings.set presets.agents={ not json ]` 成功；提示"已保存"。
- 代码：`web/src/features/settings/ia.ts:117-119`（`json(...)` 就是普通 textarea）；`internal/modules/agent/preset.go:54-63` 解析失败 → 一行日志 → 当无预设。
- 后果：一个逗号出错，预设全丢，界面说"已保存"，没有任何字段级错误。
- 对照：DSH 用卡片分组 + 只读 YAML 查看器，页面不编辑 JSON；"读取失败时默认值保持不变并显示错误"。
- 修法：保存前 `JSON.parse` 并把错误显示在字段下（改动最小）；正解是列表 + 表单。

#### S5 重复 id 的语义分裂（UI 两条 / 后端两条 / agent 一条 / 大小写还合并）[P2]
- 证据：`/api/models` 返回两条 `{"id":"min-1"}`（`handleModels` 直接用 `ParseModelInfoList`）；`SanitizeModelInfoList` 只留首个（`contracts/modelcatalog.go:170-173`）；`FindModelInfo` **忽略大小写**（`:272-274`），`DeepSeek-Chat` 与 `deepseek-chat` 会互认；删除按 id 过滤 → 一次删两条（实测）。
- 后果：选择器里两个同值选项；选第二个实际用第一个的地址/密钥；"设为默认"分不清是谁。
- 修法：添加/编辑时按 trim+lower 做唯一性校验；写入端做归一化去重，让两个读模型同源。

#### S6 选择器首屏/刷新有"模型未选"的假状态 [P2]
- 证据：300ms 采样 `选择模型`，1000ms 后回填为 `ID带中文`；`composer-status.tsx:107-119` 只有一个 mount 期 `loadModels()`，没有 loading 态。
- 后果：慢一点的环境里用户会以为"模型丢了/没配置"，而此时 `chat.model` 明明是好的。
- 修法：加载中渲染 skeleton/占位（"加载模型…"），与"未配置"分开。

### 不可恢复（U）

#### U1 390 窄屏：内容列宽 0，模型列表完全不可见 [P1]
- 操作：390×844 打开 `/settings/model`。
- DOM/截图：`r2-model-390-list3.png`（左侧导航占满宽度，右侧内容被挤成一条线，列表一行都看不到）；实测 `.zk-model-row__main` `clientWidth=0 / scrollWidth=60`；无折叠导航的入口。
- 代码：`.zk-settings__nav` 有样式，但 `.zk-model-catalog / .zk-model-list / .zk-model-row` 在 `web/src/design/global.css` 里 **0 条规则**（`grep -c` 全 0）。
- 后果：手机上无法查看/编辑/删除模型（只能靠"添加模型"盲加，弹窗本身正常 374px）。
- 修法：窄屏导航改抽屉或 `.zk-settings` 在 `max-width:720px` 变单列；列表补最小宽度与换行。

### 半接线（H）

#### H1 删除默认模型 → 悬空 `chat.model`，三处界面互相打脸 [P0]
- 操作：把 `full-1` 设为默认 → 删除该行。
- 网络：一次 `settings.set modelcatalog.models`（只删数组项），**没有任何 `chat.model` 修正**；`/api/models` 返回 `"defaultModel":"full-1"` 而 `models[]` 里没有它。
- DOM：设置页无警告、无行带"默认"标签、`模型服务→模型` 仍写 `full-1`（`r2-model-after-delete-default.png`）；输入框选择器显示**裸 id** `full-1`，下拉里没有这一项（`r2-model-composer-dangling.png`）。
- 代码：`model-catalog.tsx:99-101`（删除不管引用）、`:103-109`、`read_models.go:772`（原样回显）。
- 后果：实际运行落到"目录第一行"（`contracts/modelcatalog.go:246-260`）——**静默换模型**，可能换到另一个网关/另一个价格。
- 对照：DSH "模型/提供方被删除时保留已保存的 id，但**不展示其 ID**"（显示"请选择模型"），目录恢复后自动恢复名称。
- 修法：删除前检查引用（是默认 → "默认模型将改为 X，继续？"）；删除后同步写 `chat.model`；`/api/models` 的 `defaultModel` 只在命中目录时返回，否则返回空 + `defaultModelStale`。

#### H2 同一行 `chat.model` 两个控件，脏值静默吃掉对方的写入 [P0]
- 操作：`模型服务→模型` 输入 `par-a`（不保存）→ 点目录另一行"设为默认"（网关 `par-b`）→ 点页脚"保存"。
- 网络：`settings.set chat.model=par-b`，随后 `settings.set chat.model=par-a`；两次都提示"已保存"。
- DOM：`r2-model-default-vs-textfield.png`（Tag 在 `par-b`，文本框写 `par-a`，页脚"有未保存的更改"）。
- 代码：`settings-screen.tsx:196-207`（只在 `!dirty` 时采纳网关值）+ `model-catalog.tsx:103-109`（写同一行）。
- 后果：结果取决于点击顺序，没有冲突提示；脏状态下"设为默认"看着生效了，一保存就回退。
- 修法：`chat.model` 只留一个控件（说清它是"默认模型"，用目录的选择器渲染、禁止手打 id）。

#### H3 输入框切换模型 = 改全局默认（所有会话同步）[P0]
- 操作：会话 A 切到"我的模型" → 看会话 B、刷新、设置页。
- 网络：`settings.set {"key":"chat.model","value":"我的模型"}`，全程没有 `session.model.set`。
- 后果：在某个会话里换模型会静默改掉所有人的默认；新会话也跟着变。草稿不丢（实测 `DRAFT-1` 原样），运行中的轮次也不受影响（流程 24）——但"我选的模型只对这个会话生效"这个用户预期彻底落空。
- 修法：见 M4（会话覆盖 + 全局默认两条写入路径，UI 必须可区分）。

#### H4 同一页两套保存语义，"放弃更改"对目录无效 [P1]
- 操作：加/删/改任意模型 → 看页脚。
- DOM：页脚始终 `已与网关快照一致`，`放弃更改`/`保存` 都 disabled（`r2-model-1440-final-3rows.png`）；而目录的每次增删改都已在 WS 落盘（流程 1/12/13 的帧）。
- 代码：`model-catalog.tsx:60-67` 直接 `setSetting`；页脚 Save 只处理 `changedRows(page, values, rows)`（`settings-screen.tsx:241-258`）。
- 后果：改错没有撤销路径；"添加模型"与"保存"的关系没人讲得清。
- 修法：目录也进 draft + 页脚统一保存（推荐），或保留即时保存但给行内撤销并在页脚明说"模型列表即时生效"。

#### H5 选择器只在 mount 拉一次 `/api/models`（跨页/跨标签页不刷新）[P1]
- 操作：A 页在设置里加 `xpage-a`（目录 4 行）→ B 页（已开的会话页）点开模型下拉。
- 网络/DOM：B 页下拉仍是 3 项（`DeepSeek 对话/本地 Qwen/工具模型`）；`reload` 后才出现 `跨页新模型`。
- 代码：`composer-status.tsx:107-119` `useEffect(..., [])`。
- 后果：多标签页/后台改配置后，选择器与真实目录不一致；用户会选到"刚删掉的模型"或看不到"刚加的模型"。
- 修法：订阅 settings 域的 `chat.model` / `modelcatalog.models` 变化重拉，或把 `/api/models` 并入 settings 快照。

#### H6 无差别写入 `contextWindow:0, maxTokens:0` [P2]
- 操作：只填 name+id 添加。
- 网络：落盘 `{"id":"min-1","name":"极简模型","contextWindow":0,"maxTokens":0}`。
- 代码：`model-catalog.tsx:71`（`openAdd` 预置 0）+ `:84-91`（只删 `''/undefined`，`0` 会写进去）+ `:136`（`row.contextWindow ? … : ''` 因此永不显示）。
- 后果：所有新行都带 0，"0"与"未知"语义碰运气。
- 修法：0 当"未填"处理（提交时删键），或把"未知"显式渲染成 `窗口未知`。

#### H7 `chat.apiKey` 明文可读 + 清空即清空（与目录行"留空=保留"相反）[P1]
- 操作：模型服务 → API Key（已回填真值）→ 清空 → 保存。
- 网络：`settings.set {"key":"chat.apiKey","value":""}`；落盘 `'chat.apiKey': ''`；"已保存"。
- 代码：`settings-screen.tsx:425-433`（`Input.Password value={value}` 绑真实值）；`model-catalog.tsx:86` 则是 `if (key === 'apiKey') continue; // 空白=保留`。
- 后果：①点"眼睛"就能看全密钥；②误清空无确认、无回滚，下次发消息才 401；③两套语义学不会。
- 修法：统一 write-only（`sk-…abcd` + "更换/清除"两个显式动作，"清除"二次确认）；WS `settings.snapshot` 不下发 `chat.apiKey` 明文（需后端配合）。

### 视觉 / 规范（V）

#### V1 模型列表没有任何样式（浏览器默认 `<ul>/<li>`）[P2]
- 证据：`r2-model-1440-final-3rows.png` —— 可见 `•` 项目符号、按钮折行到文字下方且缩进不一致、行间无分隔、`128k`/`密钥已设置` 挤成一行；`grep -c "\.zk-model-" web/src/design/global.css` = 0（`zk-msg-error`/`zk-settings__nav` 都有，唯独模型列表没有）。
- 修法：`Table`/`List` 或自定义 `grid` 行（名称+角标 / 元信息 / 操作），行高 32–36px；`删除` 改 hover 才出现的文本按钮（当前 3 行 = 3 个红块，违反强调色预算）。

#### V2 术语与错误呈现 [P2]
- 同一概念两套叫法：`接口地址`（`settings.ia.baseUrl`）vs `服务地址`（`settings.model.field.baseURL`）；`系列: qwen` vs `思考系列`。
- 运行失败同一条错误出现 3 处（行内 + alert + "会话「标题」运行失败" toast），并把 `provider: openai / rig: provider "openai" stream:` 内部链路名抛给用户。
- `对话模式`（`chat.mode`）是自由文本，只有 `standard/minimal/creator` 有效，填 `foo` 静默落回 standard（`internal/modules/agent/plan_mode.go:184` 的 switch default）。
- 修法：术语表统一；错误分层（用户句 + 折叠详情）；`chat.mode` 改单选。

---

## 两份真源的关系（实测 + 代码）

| 问题 | 结论 | 证据 |
|---|---|---|
| 往目录加一条模型，输入框选择器会不会出现？ | 会，但要**重新加载页面**（选择器只 mount 拉一次） | 流程 25：另一页下拉 3 项 → reload 后 4 项 |
| 默认模型以哪个为准？ | 运行侧：`chat.model` **在目录里**就用它，否则用**目录第一行**；展示侧：`/api/models.defaultModel` 原样回显 `chat.model` | `agent.go:1426-1444` + `contracts/modelcatalog.go:246-260`；删默认后上报 `full-1`、实际跑 `my model` |
| 目录行自带 baseURL/apiKey vs 全局 | **行内赢**，行内空则回落全局，再回落 assembly/env | `contracts/modelcatalog.go:208-220`；流程 17：全局=死端口 9999，行内 9882 的模型跑通、无行内地址的 `connection refused` |
| 只有 `chat.*`、没有目录行时 | 设计上有一次性迁移（`migrateLegacyConnection`），但本实例**没发生**：`chat.model=acceptance-tool` 且 `modelcatalog.models` 行不存在，`/api/models` 初始 = `{"models":[],"defaultModel":"acceptance-tool"}` | `internal/modules/modelcatalog/catalog.go:200-235`（只在模块激活时迁移） |

---

## 与 DSH 的差距（`dsh-client-ui-model-selection` / `dsh-client-ui-agent-preset`）

DSH 行为契约（`README.zh.md`）+ 具体尺寸（`ModelSelect.module.css`）：

1. **一份目录，两个入口**：`/model` 弹窗与 composer 模型位共享同一个 `ModelDirectoryResolver`，"任一入口所做的选择正是另一个入口接下来显示的"。ZeoKeel 相反：设置页读 `modelcatalog.models` 原始 JSON、composer 读派生 `/api/models` 且只拉一次 → 两份真源 + 缓存（H2/H5）。
2. **会话级选择**：`session.selectModel`，每会话持久化；"完整选择从下一次请求开始生效；运行中的步骤保留其启动时的模型与推理强度"。ZeoKeel 写全局 `chat.model`，`session.model.set` 标"延后"（M4/H3）。
3. **按 provider 分组 + 搜索**：账号 provider 第一 → DeepSeek → 第三方；>4 个模型才显示搜索框（`/model` 始终有），模糊匹配、前缀优先；空目录/无匹配都有状态区文案。ZeoKeel：数据模型层面就没有 provider（M3），无搜索/分组（M6）。
4. **未选中 / 已删除的显示**：未选择 → 常规字重"请选择模型"并隐藏推理文案；已保存选择不在目录 → 保留 id 但**不展示 id**，目录恢复后自动恢复名称。ZeoKeel 直接显示裸 id（H1）。
5. **尺寸契约**：chip `height:28px; padding:0 4px 0 8px; radius:sm; font-size:13px/20px`；菜单 `max-height:min(360px,100dvh-96px); padding:4px`；行 `min-height:34px; padding:5px 7px; radius:md`；搜索行 `height:34px; padding:0 8px`。ZeoKeel 的模型列表**一条样式都没有**（V1）。
6. **失败与刷新**：选择被拒 → 菜单保持打开并报错；刷新期间保留上次显示；凭据/URL 从不进 label slot（最小暴露）。ZeoKeel 的失败要等发消息才暴露，且 `chat.apiKey` 明文进浏览器（H7）。
7. **预设**：DSH 卡片分组 + 默认高亮 + 只读 YAML 查看器，创建走创造流程，"读取失败时默认值保持不变并显示错误"。ZeoKeel 是裸 JSON textarea（S4）。

---

## 最该先修的 3 条

1. **H1 + S3（悬空/幽灵默认）**：删除默认行必须同步 `chat.model`（或先拦截）；`/api/models.defaultModel` 与 `agent.defaultModelLocked` 用**同一个派生函数**；目录为空时 `Send` 报"未配置模型"而不是拿旧 `chat.model` 硬跑。——会静默换模型/换地址/换价格。
2. **H2 + H4（同页两套保存语义 + 同一行两个控件）**：`chat.model` 收成唯一入口，目录改动进统一 draft（或明确即时保存并给撤销），让"设为默认 / 保存 / 放弃更改"三者语义闭合。——用户按不同顺序点会得到不同结果。
3. **S1 + S2（零校验 + 误报损坏）**：baseURL/id/contextWindow/maxTokens 字段级校验 + id 唯一性（与 `FindModelInfo` 同规则，trim+lower）；把"空列表"从"JSON 损坏"里拆出来。——这是"模型配置里一堆问题"的观感来源。

## 模型配置应该怎么设计（5 条）

1. **一份真源、一个派生函数**：目录是唯一写入口；`/api/models`、agent 配置、输入框选择器全部由同一个纯函数派生（含"默认是否悬空""生效地址/密钥来自本行还是全局"），任何界面都不得自己再推一遍规则。
2. **模型 = 身份 + 连接 + 能力三段，分离编辑**：`id` 创建后不可改（要改就"复制为新模型"）；连接（baseURL/key）单独可测、可替换、write-only 脱敏（`sk-…abcd` + 更换/清除）；能力（contextWindow/maxTokens/family/thinking）带区间与交叉校验。密钥永不出服务端。
3. **会话级选择 + 全局默认**：composer 写 `session.model`（"本会话"角标 + "恢复默认"），设置页写全局默认；两者共享同一份目录与搜索；每条消息记录产生它的 `model + reasoningEffort`，历史可追溯。
4. **每一行都能被验证、能一键修好**：行内"测试"（`/v1/models` 或 1-token）→ 三态结果 + "上次验证时间"；失败行显示原因；空目录/悬空默认/密钥缺失都渲染成可操作的引导态（"添加模型 / 测试连接 / 恢复默认"），而不是错误文案。
5. **列表即真相**：真正的行组件（名称+来源角标 / 生效地址+来源 / 窗口与 maxTokens / 状态点 / 操作），支持搜索与 provider 分组；危险操作收敛（删除 hover 才出现、删默认要确认并说明后果）。"这是什么、从哪来、现在生效的是它吗"必须一屏答完。

---

### 附：截图（`/data/zeokeel/.cache/ui-audit/shots/`，34 张）

1440：`r2-model-1440-list3.png`、`r2-model-1440-final-3rows.png`、`r2-model-1440-empty-list.png`（0 行误报）、`r2-model-1440-add-modal.png`、`r2-model-1440-edit-modal.png`
390：`r2-model-390-list3.png`（内容列宽 0）、`r2-model-390-add-modal.png`、`r2-model-390-edit-modal.png`、`r2-model-390-probe.png`
校验：`r2-model-add-modal-required.png`、`r2-model-empty-name.png`、`r2-model-neg-context.png`、`r2-model-duplicate-id.png`
默认/删除：`r2-model-default-tag.png`、`r2-model-default-vs-textfield.png`、`r2-model-after-delete-default.png`、`r2-model-after-delete-duplicate.png`
输入框：`r2-model-composer-dangling.png`（裸 id）、`r2-model-composer-dropdown-1440.png`、`r2-model-composer-no-models.png`、`r2-model-composer-after-switch.png`、`r2-model-switch-during-run.png`、`r2-model-switch-during-run-done.png`
运行：`r2-model-send-with-empty-catalog.png`、`r2-model-run-own-baseurl.png`、`r2-model-run-inherit-baseurl.png`、`r2-model-precedence-own.png`、`r2-model-dead-provider.png`
其它：`r2-model-preset-invalid-json.png`、`r2-model-list-1-row.png`、`r2-model-list-many-rows.png`、`r2-model-add-modal-empty.png`、`r2-model-edit-modal.png`、`r2-model-composer-dropdown.png`