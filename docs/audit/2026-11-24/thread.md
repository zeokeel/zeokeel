# 会话流（transcript / thread）审计

- 覆盖：`/c/:id` 对话页（完成态工具轮、待审批/被拒轮、长会话 15 轮 30 行、慢流式中、停止后、provider 失败、失败工具卡、md 富文本、超宽代码块、长段落、390px 窄窗）、`/c/:id/trajectory`、`/c/:id/memory`（该会话无记忆条目，只有空态一句「这个会话还没有记忆条目。」）；悬停操作条、工具卡展开/收起/原始参数、Ctrl+F 查找条。
- 截图 28 张（`shots/thread-*.png`）。实例：9860 只读基线、9864 自建（本地 md 夹具，可控慢流/失败/宽代码）、9872 由 lead 提供（未使用，9862 出现 429 后改用自建实例，未影响共享实例）。
- 量测方法：`page.evaluate` 取 `getBoundingClientRect()` / `getComputedStyle()`，见 `.cache/ui-audit/m-*.json` 与脚本 `thread-m*.mjs`。

## 结论

1. **会话流在 1440 下读不动**：整列 1128px 宽、正文单行 1120px ≈ 80 个汉字，产品自己的 `TranscriptWidth 960 / ProseMeasure 640`（≈45 字/行）根本没进 CSS，`global.css` 里没有任何 max-width。
2. **三种状态里只有两种有视觉**：完成有灰字「完成」，失败有红十字，但**流式中完全没有"正在生成"的提示**，**停止后完全没有"已停止"**（刷新后反而长出「16 入 · 81 出」，把被截断的回答伪装成完整一轮）。
3. 工具卡是"能用的半成品"：状态只有灰字无 tint、无耗时、卡片宽度随内容在 236→321px 之间跳、错误行直接打印协议枚举 `other`。
4. 虚拟化与跟随是**做对了**的部分：30 行/13680px 的会话只渲染 9–19 行，「回到最新」出现在 24px 阈值之外，点击后 `distanceFromBottom = 0`；1440 下 `document.scrollWidth = 1440`（宽代码块横滚不破版，`pre.scrollWidth 3557 / clientWidth 1126`）。
5. 390px 下整个会话面不可用（正文列 78px，逐字换行），属壳层与 thread 共同的窄窗缺口。

## 发现

### T-01 390px 下会话列只剩 78px，正文逐字换行 [P0]
- 现象：`thread-mobile-390.png` 中「工具」被拆成两行竖排，URL `http://127.0.0.1:9861/health` 折成 3 行，一条 29 字的消息占 284px 高。
- 证据：`shots/thread-mobile-390.png`；`page.evaluate` → `.zk-sidebar` x0 w200、`.zk-content` x200 **w126**、`.zk-thread__body` x200 w126 `padding:16px 24px 0`、`.zk-transcript` x224 **w78**（14px 字 → 每行 5 个汉字）、`.zk-composer` `padding:12px 8px 16px`。`web/src/design/global.css:155-159`（`.zk-thread__body` 内距写死 24）与 `global.css:1248-1264`（只有 `.zk-composer` / `.zk-composer__hint` 有 <480 的规则，thread body 没有）。
- 规范：BRIEF §5 P0「移动端不可用」；DESIGN §5.4「窄窗规则」要求主列不得低于 `ContentMinWidth 520`（`internal/design/components.go:79`），否则降级侧栏；WEB-CLIENT §9 视觉规则。
- 建议：`.zk-thread__body { padding-inline: var(--zk-space-4) }` 加 `@media (max-width:480px){ .zk-thread__body{ padding-inline: var(--zk-space-2) } }`；侧栏 <900 改为 overlay 抽屉（壳层修复，见 shell 区域），并给 `.zk-transcript__row` 一个 `min-width: 0` + 内容 `overflow-wrap:anywhere` 兜底。

### T-02 助手正文与整个会话列没有宽度上限：1440 下单行 80 汉字，1920 下更长 [P1]
- 现象：助手正文左对齐、无气泡，宽度随容器 100% 铺满；短回答时收缩到 318px，长回答时直接 1128px。中文长段落实测单行 1120px ≈ 80 字，远超舒适阅读区间。1920 视口下同一规则会到 ~1600px（`.zk-thread__body` 宽度 = 视口 − 264 − 48）。
- 证据：`shots/thread-md-full.png`、`shots/thread-long-1440.png`；量测：`.zk-transcript__row` x288 **w1128**、`.zk-markdown` x288 **w1128**；长段落 476 字 / 8 行，满行宽 **1120.1 / 1120.5 / 1116.5px**，line-height 22px（1.57）；用户气泡上限同为 1128（`maxWidth:100%`，antd X Bubble content 默认）。`web/src/design/global.css:564-587`（`.zk-transcript`/`__inner`/`__row` 均无 max-width）；`web/src/chat/transcript.tsx:219-234` 行容器 `inset-inline:0`。
- 规范：DESIGN §5.4 `TranscriptWidth 960`（"conversation column … bounds blocks (code fences, tables, tool cards)"）与 `ProseMeasure 640`（"~45 Chinese characters or 90 Latin characters per line"）——`internal/design/components.go:88-98`；但 `web/scripts/gen-tokens.mjs:145-146` 只导出 spacing/radius/control height，两个宽度 token **从未生成**，`tokens.css` 里搜不到 → web 侧根本无法引用。对照 DSH：`.column { max-width: var(--dsh-chat-content-width) }`，该变量 `clamp(680px, 64% of column, 920px)`（`dsh-client-ui-conversation/src/client/skeleton/ConversationRoot.module.css:468-472`）；ChatGPT 助手正文 48rem≈768px。判定：规范已写死、实现整段缺失。
- 建议：`gen-tokens.mjs` 的 `HEIGHT_ALLOWLIST` 同款白名单里加 `TranscriptWidth` / `ProseMeasure` 两个长度 token → `--zk-transcript-width:960px; --zk-prose-measure:640px`；`.zk-transcript__inner{ max-width:var(--zk-transcript-width); margin-inline:auto }`；`.zk-markdown p, .zk-markdown li, .zk-markdown blockquote{ max-width:var(--zk-prose-measure) }`，代码块/表格/工具卡保持整列宽（这正是 `components.go:89-91` 注释的原意）。

### T-03 用户气泡无上限且对齐到列右缘，长消息会横跨 1128px [P1]
- 现象：用户气泡右对齐，宽度=内容宽度，上限是整列；实测一句 29 字 → w452；输入长段落时按 T-02 可到 1128px（≈80 字/行）。
- 证据：`shots/thread-desktop-1440.png`；`.ant-bubble-content`（user）x964 **w452** h74 `bg rgba(0,0,0,0.06)` `radius 12px` `padding 12px 16px`；`.ant-bubble-body` `max-width:100%`（antd X 默认，`@ant-design/x/es/bubble/style/bubble.js:63-73`）。
- 规范：DSH 用户气泡 `max-width: min(calc(var(--dsh-chat-content-width,748px)*0.702), 82%)`（`dsh-client-ui-chat/src/client/chat/MessageItem.module.css:20`）→ 920 列下约 646px；ChatGPT 用户气泡上限约列宽 70%。BRIEF §4.5「消息气泡宽度约束」。
- 建议：`.ant-bubble-end .ant-bubble-body { max-width: min(calc(var(--zk-transcript-width) * 0.7), 82%) }`（在 `global.css` 里针对 `zk-transcript` 作用域覆盖，不动 antd token）。

### T-04 流式中的回答没有任何"正在生成"的可视信号 [P1]
- 现象：慢流式时助手区只有不断变长的纯文本：没有闪烁光标、没有 dot、没有 shimmer、没有「正在生成」行；整屏唯一运行提示在 composer（圆形停止键 + `~9.0 tok/s`）。首帧前的 `Bubble loading`（三个点）只在 requestPlaceholder 阶段出现，一旦有文本就消失。
- 证据：`shots/thread-streaming-1440.png`（采样 10 次，`bubbleLoading:false`、`loadingDots:0`、`think:false`、`.zk-msg-meta:null`）；`.ant-bubble` className 只有 `ant-bubble-start`（无 `-loading`）；`web/src/chat/transcript.tsx:239` `loading={info.status === 'loading'}`，`web/src/chat/assistant-body.tsx:25-36` 流式时只渲染 `MarkdownBody`。
- 规范：DESIGN §5.2 状态矩阵 `running → 文本色 LabelSecondary / 填充 Accent（spinner·点）`；DSH 运行中的分组标题/工具行用 `TextShimmer`（`dsh-client-ui-chat/README.zh.md:130`）并在尾行给出运行鲸鱼；Claude/ChatGPT 在正文尾部显示光标或三点。
- 建议：在 `assistant-body.tsx` 的 markdown 之后加一个 `streaming` 分支：`<span className="zk-streaming-dot" aria-live="polite" />`（1.5s 呼吸，`prefers-reduced-motion` 关闭），或给最后一段文字加 DSH 式 mask-image shimmer；`aria-busy` 挂到 `.zk-transcript__row`。

### T-05 点停止后「已停止」不出现；刷新后该轮显示 token 用量，像是完整回答 [P1]
- 现象：Esc / 停止键中止后，行内只留下半截文本，**没有任何中止标记**；页面刷新后同一行出现「16 入 · 81 出」，看起来与正常完成的一轮完全一样。composer 统计同时从 `~9 tok/s` 变成 `1 轮 0 步 · 0 tok`（数据不一致）。
- 证据：`shots/thread-stopped-1440.png`（停止后 100ms×40 次轮询，`.zk-msg-stopped` 从未出现，`hasStopped:false`）；`shots/thread-stopped-reload.png`（重新打开该会话：`lastText:"… slow stream 16 入 · 81 出"`、`meta:"16 入 · 81 出"`、`hasStopped:false`）。代码路径：`web/src/chat/assistant-body.tsx:26` `stopped = !streaming && message.stopReason === 'aborted' && !message.error`；`web/src/api/gateway.ts:988-994` 快照里 `stopReason` 仅当 `session.interrupted` 才给 `'aborted'`，否则末条助手消息一律 `'end_turn'`；`web/src/features/thread/thread-screen.tsx:224-244` settle 后 `chat.refresh()` 用快照覆盖了带 `stopReason:'aborted'` 的乐观行（`web/src/runtime/use-gateway-chat.ts:88-91` 里本来有正确的 aborted 标记）。
- 规范：DESIGN §5.2「cancelled → LabelTertiary / LabelTertiary，中性，不画成失败」与 §9.4「状态只能语义 tint + 必填文字标签」；WEB-CLIENT §7.3 要求失败/运行默认展开可见 → 中止同样必须可见；BRIEF §5 P1「状态表达错误」。
- 建议：两条路选一——(a) 网关在 aborted 时把 `interrupted` 持久化到该 session 快照（首选，客户端零改动）；(b) 客户端在 `refresh()` 前把 `stopReason:'aborted'` 写进 sessionStorage 的 per-session 标记，快照失败时回填。同时中止轮必须**隐藏 usage**（`assistant-body.tsx:113` 已有 `status==='loading'` 早退，需补 `message.stopReason==='aborted'`）。

### T-06 工具卡里直接打印协议枚举当错误文案（红字 "other"）[P1]
- 现象：一次失败的工具调用，卡片顶部红字只写 `other`；审批被拒绝的那一轮同样的位置也是 `other`，真正的信息（`rig: tool not found: bogus_tool` / `rig: tool call denied by approval gate: web_fetch`）埋在下面「输出」段里。同时**审批被拒**被画成红色失败卡，与"工具报错"无法区分。
- 证据：`shots/thread-tool-failed-top.png`（红字 `other`，输入/输出/原始参数在其下）；`shots/thread-approval-pending-1440.png`（9860 `sess-1795521636269061347`：行内 `工具 web_fetch … · 失败 | other | 输出 rig: tool call denied by approval gate: web_fetch`，图标 `rgb(255,77,79)`）。映射代码：`web/src/api/gateway.ts` 快照段 `error: tool.errorKind || undefined`，渲染在 `web/src/chat/tool-card.tsx:22-29`。
- 规范：`web/src/i18n/zh.ts:1-8` 与 `web/src/i18n/index.tsx:22` 明写"protocol values never live here — stopReason/errorKind/tool names"；DESIGN §5.2「error 不得用普通文本…cancelled 不得画成红色失败」；`features/provider-error.ts:1-12` 的既有原则（原生态英文 JSON 不该进中文会话）。
- 建议：`tool-card.tsx` 把 `step.error`（kind）映射成文案：`errorKind==='approval_denied' → 已拒绝（t('thread.toolDenied')，中性 tertiary + 灰色图标）`，其余 kind →「工具执行失败」+ 把 message 提到第一行；`api/gateway.ts` 的 `SnapshotMessage.steps[].error` 改为同时携带 `errorKind` 与 `errorMessage`。

### T-07 工具行没有耗时、状态只有灰字，无状态 tint [P1]
- 现象：完成的工具行是「✓ 工具 web_fetch >」+「http://…/health · 完成」，"完成"是 13px `rgb(74,81,96)` 普通文本，和摘要同色同权重；**没有耗时**（`formatDuration` 只用于推理行的 `desc`）；失败行的红色来自 antd X ThoughtChain 的图标色，而不是产品 token。
- 证据：`shots/thread-desktop-1440.png`、`shots/thread-row-assistant-clip.png`；量测：`.zk-tool-text` 13px/20px `rgb(74,81,96)`，ThoughtChain 成功图标 `rgb(82,196,26)`、失败 `rgb(255,77,79)`、运行中 `rgb(13,68,246)`（`getComputedStyle` 三态实测），而 token 是 `--zk-success:#16a34a`/`--zk-danger:#dc2626`/`--zk-accent:#0d44f6`。`web/src/chat/thought-steps.tsx:96-128`（`state` 只是拼接字符串）、`thought-steps.tsx:183-189`（`formatDuration` 已实现但只给 reasoning 用）。
- 规范：DESIGN §5.2「状态展示三层：①对象头/工具卡头 = StatusBadge（语义 tint + AA 文字 + 必填标签）」、§9.4「状态只能"语义 tint + 必填文字标签"，不得中性底 + 状态字」；DSH 工具行在运行中走 TextShimmer、错误保留语义色（`dsh-client-ui-chat/README.zh.md:130,142`），并且耗时放在 composer 统计而非行内（同文件 83 行）。参考实现：Claude 工具 chip 形态为 `△ 读取 3 个文件 · 1.2s`。
- 建议：`toolItem()` 的 `description` 改成结构化子组件：`<StatusDot status>` + 摘要 + `<span className="zk-tool-dur">{formatDuration(step.durationMs)}</span>`；状态 tint 用 `--zk-success/--zk-danger/--zk-accent` 覆盖 `ant-thought-chain-status-*` 的颜色（`global.css` 里 `.zk-thought .ant-thought-chain-status-success{color:var(--zk-success)}` 等三条），并要求后端在 `ToolStep` 上补 `durationMs`。

### T-08 悬停操作条藏在气泡内部，且常驻 28px 死区把气泡撑高 [P1]
- 现象：用户气泡里文字下面永远空着一块灰（1 行消息 h=74 而非 46），因为 `.zk-msg-actions` 用 `opacity:0` 占位；悬停时「复制」是图标、「编辑」是**文字按钮**，两者混在一行贴着气泡右下角。助手侧的操作条右对齐于**回答文字的右缘**（不是列的右缘），短回答时两个图标飘在列中间。
- 证据：`shots/thread-hover-actions.png`（气泡内右下）；量测：user `.ant-bubble-content` h74 = 12+22+4+24+12；`.zk-msg-actions` x980 w420 h24 `margin-top:4px` `opacity:1`（悬停）/ `0`（非悬停）；assistant `.zk-msg-actions` x288 w318.3，按钮 x554.3 / x582.3（右缘 606 = 文字右缘）。代码：`global.css:1399-1411`（opacity 方案）、`global.css:1516-1518`（`justify-content:flex-end`），`web/src/chat/message-actions.tsx:64-80`（user 用文字「编辑」、assistant 用两个 icon-only 按钮）。
- 规范：DSH 把操作行放在气泡**外面**（`.userRow { flex-direction: column; align-items: flex-end; gap: 6px }`，`MessageItem.module.css:3-8`），操作行高 28、gap 8、icon 与时间戳同色（`MessageIconActions.module.css:6-10`）；ChatGPT 的悬停条也在气泡下方外侧。
- 建议：`transcript.tsx` 的 user 分支把 `<MessageActions/>` 移出 `.ant-bubble-content`（放进 row 的兄弟容器，`justify-content:flex-end`，`margin-top:6px`，`position:absolute` 或 `height:0 + translateY` 均可，但**不要**占位）；「编辑」改成 icon（`EditOutlined`）或与复制一起收进 `⋯` 菜单；assistant 的 `justify-content` 改成 `flex-start`（贴回答左缘）或整列左缘，避免与正文右缘耦合。

### T-09 展开后的工具卡宽度随内容收缩（236→321px 跳变），信息挤成一列 [P1]
- 现象：展开 `web_fetch` 卡片后，卡片只有 236px 宽（因为最宽子项是那条 mono URL），再加上 `原始参数` 展开，卡片跳到 321px；`地址 / 输出 / 原始参数` 竖排三段各占一行，横向 800px 空着。失败卡同理（239px 宽）。
- 证据：`shots/thread-tool-expanded-1440.png`、`shots/thread-tool-raw-args.png`；量测：`.zk-tool-card` w236.4 → 321（打开原始参数后 `.zk-tool-io` x314 w320.9）；`.zk-tool-section__label` 12px/18px `rgb(90,99,113)`（"地址"/"输出"），`.zk-tool-mono` x314 w236.4 h30 13px/20px bg `#f2f4f7` radius 6；`.ant-bubble-body` w318.3（整行只有 318px，因为内容是 shrink-to-fit）。代码：`web/src/chat/tool-card.tsx:21-44` + `web/src/chat/tools/sections.tsx:14-22`，容器没有 `width:100%`。
- 规范：DESIGN §5.4「LG 卡/工具卡」+ T-02 里 `TranscriptWidth` 注释"bounds blocks (code fences, tables, tool cards)"——工具卡本应吃满阅读列；DSH 的过程组用固定 `div` 布局盒 + 滚动正文（`README.zh.md:128`），宽度不随内容跳。
- 建议：`.zk-tool-card { width: 100%; }`、`.zk-tool-section { display:grid; grid-template-columns: 56px 1fr; gap: var(--zk-space-2) }`（label 与内容同排，像 DSH 的 key/value 行），`pre` 一律 `overflow:auto`；`.zk-thought__content` 给 `max-width: var(--zk-prose-measure)` 或宽度随列。

### T-10 用量行没有单位、没有标签，紧贴正文 4px，且与 composer 用两套词汇 [P2]
- 现象：助手回答下方一行 12px 三级灰「16 入 · 9 出」，无「token」字样、无图标、`margin-top:4px`，视觉上仍属正文块；同一屏 composer 底部却写「1 轮 1 步 · 25 tok」（另一套词）。停止后还出现「1 轮 0 步 · 0 tok」。
- 证据：`shots/thread-desktop-1440.png`、`shots/thread-md-full.png`；`.zk-msg-meta` x288 w1128 h18 12px/18px `rgb(90,99,113)` `margin:4px 0 0`；文案 `web/src/i18n/zh.ts:99` `'{in} 入 · {out} 出'` vs `zh.ts:552` `'{turns} 轮 {steps} 步 · {tokens} tok · 缓存 {cached}% · 上下文 {context}%'`；渲染 `web/src/chat/assistant-body.tsx:113-126`。
- 规范：DESIGN §5.3 `TypeXXS 12` 用于"元数据/消息时间/用量"（字号对，**语义标注**缺）；DSH 的轮次用量是**可展开的行**且带图标（`TurnUsagePanel.module.css:16-38` `padding:6px 8px; gap:4px; font-variant-numeric:tabular-nums`），记账不完整时整行不显示（`dsh-client-ui-chat/README.zh.md:81`）。
- 建议：文案改成「入 16 · 出 9 tok」或加图标 + `title`/`aria-label`「本轮用量：输入 16 tokens，输出 9 tokens」；`margin-top` 提到 `--zk-space-2`(8px) 或加一条 1px 分隔；composer 与消息行共用同一个 `formatTokens`。

### T-11 代码块没有复制按钮、没有横向可滚动的暗示 [P2]
- 现象：超宽代码块在卡片右缘被"截断"，没有滚动条视觉、没有渐变/「↔」提示，也没有复制按钮；`pre` 实际可滚（`scrollLeft` 生效）。
- 证据：`shots/thread-wide-1440.png`、`shots/thread-wide-scrolled.png`；量测 `pre.scrollWidth 3557 / clientWidth 1126`、`overflow-x:auto`、`document.scrollWidth` 恒 1440（**不破版，做对了**）；`web/src/chat/markdown.tsx:11-18` 只用 `CodeHighlighter header={false}`。
- 规范：Cursor/Copilot/ChatGPT 的代码块均有右上角复制 + 语言标签；antd X `CodeHighlighter` 自带 `header`（这里被显式关掉）。
- 建议：`header` 打开或自绘一个 `zk-code__copy`（复用 `MessageActions` 的 clipboard 逻辑，`navigator.clipboard.writeText`，成功 1.5s「已复制」），并在 `pre` 上挂 `.zk-code[data-scrollable]` 显示右侧 24px 渐变。

### T-12 表格/引用/标题的排版没有产品级样式 [P2]
- 现象：Markdown 表格只有内容没有框线、表头无底色（`border:0px`），5 行表格占 230px 高；引用块只是 40px 缩进（`margin:14px 40px`、无左边条、无 tint），看起来像普通段落；`h2` 是 21px/700（1.5em 的浏览器默认比例），不在 20/24 的 type scale 上。
- 证据：`shots/thread-md-full.png`；`.zk-markdown table` w374 `overflow-x:auto`；`th` `padding:11.9px 14px`、`border-top-width:0px`、`background:rgba(0,0,0,0)`；`blockquote` `margin:14px 40px`、无 border-left；`h2` `font-size:21px`、`margin:17.43px 0`。
- 规范：DESIGN §5.3 字号只走 type role（20/28/600 或 24/32/600），§5.4 `RadiusXS/SM`；`web/src/design/global.css:213-232` 只给了 `.zk-markdown` 与 `.zk-inline-code` 两条规则，表格/引用/标题无规则（`XMarkdown` 默认样式直接透出）。
- 建议：在 `global.css` 补 `.zk-markdown` 的 `table/th/td/blockquote/h1..h4` 规则：表头 `background:var(--zk-bg-strong)`、单元格 `border-bottom:1px solid var(--zk-border-l1)`、`blockquote{border-inline-start:2px solid var(--zk-border-l3); padding-inline-start:var(--zk-space-3); color:var(--zk-label-secondary)}`、标题改用 `--zk-type-l-line` 等 token。

### T-13 `.zk-markdown > :first-child` 归零规则失效，首块上边距泄漏 17px [P2]
- 现象：助手消息顶部多出 17.4px（`h2`）或 14px（`p`）空白，与用户气泡之间的 12px 间距被吃掉，看起来像"回答比提问远"。
- 证据：`shots/thread-md-full.png`；`.zk-transcript__row` y189 → `.zk-markdown` y206.4（差 17.4 = h2 的 `margin-top`，因为 `.zk-markdown > :first-child` 命中的是 `div.x-markdown` 而不是 `h2`）；`web/src/design/global.css:218-224` 的两条 `> :first-child/:last-child` 规则。
- 规范：DESIGN §5.4 间距档位（外边距必须可预期）；`markdown.tsx:28-36` 用 `<div class="zk-markdown"><XMarkdown/></div>` 包了一层。
- 建议：改成 `.zk-markdown .x-markdown > :first-child{margin-top:0}` / `:last-child{margin-bottom:0}`（两行），或给 `.zk-markdown > .x-markdown{display:contents}`。

### T-14 工具卡默认折叠与 WEB-CLIENT §7.3 的"本轮默认展开"不符 [P2]
- 现象：最新一轮里已完成的工具卡默认是收起的，只有"运行中/失败"才自动展开；用户需要点两下（点标题、再点原始参数）才能看到参数。
- 证据：`shots/thread-desktop-1440.png`（9860 最新轮，工具卡收起）；`web/src/chat/thought-steps.tsx:78-81` `autoExpanded` 只收 `status==='loading' || 'error'`。
- 规范：WEB-CLIENT §7.3「默认态：运行中展开、完成后可折叠；**本轮（最后一条 user 之后）默认展开**，出现更新的 user 消息后折叠」——原生 `collapse=next-input` 策略；`docs/WEB-CLIENT.md:491-499`。
- 建议：`autoExpanded` 增加"最后一个 assistant 行"判定（`isLastTurn` prop 由 transcript 传入），并把它与 `userExpanded` 的优先级写进注释。

### T-15 Ctrl+F 查找条搜不到工具参数/输出，输入框是一条 916px 的原生 input [P2]
- 现象：会话里明明显示 `http://127.0.0.1:9861/health`，在查找条输入 `health` 得到「无匹配」；查找条是一个通栏无样式 `input`（只带浏览器 focus 边框，916.2×22），占满整行并挤压正文（37px 高）。
- 证据：`shots/thread-findbar.png`；`.zk-find` x264 w1176 h37 `padding:4px 24px`、`input` x317.4 **w916.2 h22**、右侧 `无匹配` + ↑↓×；代码 `web/src/features/thread/find-bar.tsx` + `thread-screen.tsx:336-348`（只搜 `message.text` 与 `reasoning`）。
- 规范：DESIGN §1.2「官方组件目录」要求输入走 antd `Input`（圆角/边框/高度 token），而不是裸 `<input>`；DSH/浏览器查找都能命中工具输出。
- 建议：改用 `Input size="small" allowClear prefix={<SearchOutlined/>}` 并限宽 `min(420px, 60%)` 靠右；搜索范围加上 `steps[].input/output/summary`（`matchIndexes` 里对 `info.message.steps` 做同样 includes）。

### T-16 轨迹页信息密度过低，结果块无标签 / 编号可疑 [P2]
- 现象：`/c/:id/trajectory` 整页只有一条 "✓ 工具 · web_fetch #2" 和一块通栏灰色 `ok`；"#2" 在本轮只有一次调用时出现，输出块没有「输出」标签、没有耗时、没有参数（要回对话页才能看）。
- 证据：`shots/thread-trajectory-1440.png`；页面文本 `轨迹 / 这一轮里 agent 调用了什么、结果如何。 / 工具 · web_fetch #2 / ok`；输出块 x312 w1112 通栏 `bg #f2f4f7`；代码 `web/src/features/thread/trajectory-screen.tsx`。
- 规范：DESIGN §5.3（`TypeXS 13` 用于检查器正文）、§5.4（检查器宽度 `InspectorWidth 320` 而非通栏）；DSH 的工具节点自带 Definition 描述与参数（`README.zh.md:126`）。
- 建议：一行一个工具：`状态点 + 工具名 + 摘要（args 首字段）+ 耗时 + 展开`；输出块加「输出」小标签与 `max-height:240px; overflow:auto`；编号用 1-based（或去掉）。

### T-17 状态色双轨：ThoughtChain 用 antd 默认绿/红，产品 token 未生效 [P2]
- 现象：成功图标 `rgb(82,196,26)`、失败 `rgb(255,77,79)`、运行中 `rgb(13,68,246)`；产品 token 是 `#16a34a / #dc2626 / #0d44f6`。同一屏上"工具成功绿"与 `.zk-badge--success`（`color-mix(--zk-success)`）是两种绿。
- 证据：`getComputedStyle('.ant-thought-chain-status').color` 三态；`web/src/design/tokens.css:30-35`；`documents/DESIGN §9.2`（禁止颜色字面量、只走 token）。
- 建议：`global.css` 加三条覆盖（`.zk-thought .ant-thought-chain-status-success{color:var(--zk-success)}` 等），或在 `global.css:234-236` 的 `.zk-thought` 作用域内用 `color-mix` 染 tint。

### T-18 长会话：虚拟化与跟随正确，但中段 DOM 略超预算 [P2]（同时是正面验证）
- 现象/证据：15 轮 = 30 行、`scrollHeight 13680px`；底部只渲染 9 行（`domNodes 1166`），中段渲染 19 行（`domNodes 2077`）；顶部/中段都出现「回到最新」，点击后 `scrollTop 13051`、`distanceFromBottom 0`、按钮消失（`shots/thread-long-session-top.png` / `-middle.png` / `-bottom.png`）。
- 规范：WEB-CLIENT §7.4「单帧 DOM 节点 ≤ 2000、滚动 55fps」——中段 2077 略超；§7.2 跟随阈值 24px（`shouldFollow` 实现正确）。
- 建议：`overscan` 从 8 降到 4，或对已折叠的工具卡用 `contain: content`；「回到最新」按钮加上下浮起阴影/12px 边距（当前 `bottom:16px` 直接压在最后一行内容上，见 `-top.png` 里压住代码块右缘）。

## 跨区域共性问题

1. **设计 token 与实现脱节**：`TranscriptWidth/ProseMeasure` 写在 `internal/design/components.go` 却不在 `gen-tokens.mjs` 的导出白名单里 → `tokens.css` 无此变量 → 会话列没有宽度上限（T-02）。同类风险：任何"设计里有、生成器不导"的常量都只能靠人肉补 CSS。
2. **协议值直接当文案**：`errorKind` 打印成 `other`（T-06）、`stopReason` 曾打印 `stop`（`assistant-body.tsx:107-112` 的注释就是在修这个），说明缺一个"枚举→文案"的统一映射层，和 `features/provider-error.ts` 一样应该只有一个出口。
3. **状态表达不成体系**：运行中无提示（T-04）、中止不可见（T-05）、工具状态用灰字（T-07）、状态色走 antd 默认（T-17）——四处四个答案，而 DESIGN §5.2 已有三层矩阵，需要一次"状态语汇"总收口。
4. **窄窗只有 composer 适配了**（T-01）：`.zk-composer` 有 <480 规则，`.zk-thread__body`、`.zk-transcript__row`、工具行都没有，说明响应式是按"谁看起来动不了"逐个补的，缺全局断点约定（900/1024/480 三档已有定义，thread 未接入）。