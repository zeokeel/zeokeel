# 屏面（Now / 自动化 / 能力 / 洞察 / 团队 / 轨迹 / 记忆 / 检查器 / 子会话 / 404）审计

- 覆盖：`/`、`/automation`、`/capabilities`（技能 + 连接器）、`/insights`（近 7 天）、`/team`、`/c/:id/trajectory`、`/c/:id/memory`、`/c/:id/files`、`/c/:id/guide`、`/c/:id/child/:childId`、`/no-such-route`；视口 1440×900、1600×1000、390×844；数据态：自动化 2 条任务 + 编辑/历史/删除确认/开关/400 错误，能力 1 条技能（实例本地 fixture `.agents/skills/release-notes`）。
- 实例：审计用专属实例 `http://127.0.0.1:9875`（`sess-1795522277595631438` 完成态、`sess-1795522282701399082` 待审批）；9860 只读基线。量测原始数据：`.cache/ui-audit/surfaces-1440.json`、`surfaces-viewports.json`。
- 截图：35 张（`shots/surfaces-*.png`）。

## 结论

- 这 10 个屏共用一套 `zk-screen` 骨架，因此缺陷高度同构：**顶栏 h1 与页面 h1 同文重复**、**内距三套（0 / 16 / 24）**、**没有内容最大宽度**、**空态是"默认插画 + 一句文案"且与主操作分居两处**。逐屏修会返工，应改骨架。
- 最严重的三处不是审美：**390 下内容列仅 126px（洞察表格 31px、相互重叠）**、**/files 与 /guide 在 390 下检查器全宽浮层无关闭路径**、**Now 因别的会话的待审批卡把整个 composer 禁用**（落地页无法开始任何工作）。自动化还有一个**主流程直接失败**的缺陷（保存按界面自带帮助文案写必 400，错误又渲染在弹窗后面）。
- 数据密集的两屏（洞察、自动化）问题在"数字没有网格"：洞察统计条 label 在上、值在下且 5 个数字不等宽左对齐；两表并排却列宽/高度都不同，数字列左对齐。
- 检查器与轨迹暴露的是"右栏是二等公民"：右栏 tab 与左栏 tab 不在同一条基线（y=48 vs y=91）、两套 tab 语言、`/files` 空选择时高亮"开始"；轨迹把消息序号当时间、只有 dot+✓ 两个图标、没有耗时与参数。

## 发现

### S-01 390 视口下内容列只剩 126px，洞察页表格互相重叠、范围切换溢出列外 [P0]
- 现象：390 宽时侧栏仍占 200px，`#zk-main` 只剩 126px。洞察页「按天」「按模型」两张表各被压到 **31px** 宽，表体（82 / 183px）溢出并与邻表文字叠在一起；`近 7 天/近 30 天` 分段控件从 x=262 一路顶到 389（列右边界 326），"近 30 天" 被切掉；标题"洞察"折成两行、说明逐字换行。
- 证据：`shots/surfaces-insights-390.png`、`shots/surfaces-now-390.png`、`shots/surfaces-team-390.png`；`surfaces-viewports.json → narrow.insights`：`main {x:200,w:126}`、`tables[0] {x:224,w:31,innerW:82}`、`tables[1] {x:271,w:31,innerW:183}`、`seg {x:262,w:126.9,right:389}`；`#zk-main` 126 → 内容两表并排（`web/src/design/global.css:1084` `.zk-insights__tables{grid-template-columns:minmax(0,1fr) minmax(0,1fr)}`）在 126px 内各分 47px。
- 规范：BRIEF §5「移动端不可用 = P0」；WEB-CLIENT §9 视觉规则要求窄屏可用；antd 6 `Table` 的 `scroll={{ x }}` 是表格窄屏的官方做法（`bash scripts/antd-cli.sh demo Table --version 6` 有 `fixed-columns`/`responsive` 示例）。
- 建议：≤768 时侧栏改为抽屉（`@media` 已有 200px 固定宽度，改为 0 宽 + overlay）；`.zk-screen` 加 `min-width:0`；`.zk-insights__tables` 在 ≤768 变 `grid-template-columns:1fr`；两张 `Table` 传 `scroll={{ x: 420 }}`；`header--row` 在 ≤768 改 `flex-direction:column; align-items:stretch`。

### S-02 /files 与 /guide 在 390 下变成全屏浮层且没有任何关闭路径，左侧会话列宽度为 0 [P0]
- 现象：390 宽打开 `/c/:id/files`，检查器 `position:fixed` 铺满（x=31.2 w=358.8），左侧会话列 `.zk-thread` 宽度被压到 **0**，composer 内容跑到 x=489（视口外）。浮层里只有「开始 / 文件」两个 tab，没有关闭/返回按钮，Esc 也不关；用户被困在检查器里。
- 证据：`shots/surfaces-files-390.png`、`shots/surfaces-guide-390.png`；`surfaces-viewports.json → narrow.files`：`insp {x:31.2,w:358.8,pos:"fixed"}`、`scroller.zk-thread {w:0,clientW:0,scrollW:322}`、`composer {x:200,w:16}`；`web/src/design/global.css:1231-1239`（`.zk-inspector{position:fixed;...width:min(420px,92vw)}`）；`web/src/features/app-routes.tsx:34-42`（Splitter 左 panel `min={360}`、右 panel `defaultSize={380} min={280}`）。
- 规范：DSH 的浮层契约要求可关闭（BRIEF §4.4 `dsh-client-ui-sidebar-right`）；antd 6 `Drawer` 自带关闭按钮与 Esc；WEB-CLIENT §1.3「检查器是路由组」在窄屏应降级为可关闭抽屉。
- 建议：窄屏用 antd `Drawer`（`closable`、`onClose → navigate(/c/:id)`）承载 `InspectorPanel`；或至少给 `.zk-inspector` 加一个固定在右上角的关闭按钮；Splitter 左 panel 在 ≤768 用 `min={0}`，并把 `.zk-thread` 的 `min-width:0` 补齐。

### S-03 Now（落地页）渲染「其他会话」的待审批卡，并因此禁用整个 composer [P0]
- 现象：`/` 上出现一条来自另一会话的审批卡（含"来自会话：把 README 里的安装步骤补充完整…"、"Esc 拒绝 · Cmd/Ctrl+Enter 允许"、三个按钮），composer `disabled=true`、placeholder 变成"先处理上面的审批…"。落地页唯一职责是"开始一段新对话"，却被别人的审批阻塞。
- 证据：`shots/surfaces-now-1440.png`；`surfaces-1440.json → now`：`approval {x:288,y:573,w:1128,h:169}`、`sender {disabled:true}`、`textareaDisabled:true`；代码 `web/src/features/now/now-screen.tsx:26-27`（`approvals.length>0`）、`:76`（`<ApprovalDock/>`）、`:125`（`disabled={creating || approvalPending}`）、`:127`（placeholder 切换）；i18n `web/src/i18n/zh.ts:135`。
- 规范：DESIGN.md §2 状态语义（页面主操作不应被非本页状态置灰）；APPROVAL-SURFACES 要求审批出现在**其所属会话**上下文；主流做法（ChatGPT / Cursor）新会话入口永不被其他会话的审批阻塞，待审批以收件箱/角标提示。
- 建议：Now 屏不挂 `ApprovalDock`（移除 `now-screen.tsx:76`），composer 的 `disabled` 只保留 `creating`；审批在收件箱与所属会话内处理，落地页最多在顶栏给一个"1 项待审批"入口。

### S-04 自动化「新建任务」按界面自带帮助文案保存必定 400，且失败信息渲染在弹窗背后、表单无字段错误 [P0]
- 现象：表单帮助文案写"五段 cron（如 `0 8 * * *`）或 **every 24h / daily 09:00**"。照它填 `every 24h` 或 `daily 09:00` 保存 → 请求 400，弹窗不关、没有任何字段错误、用户看不到任何提示（错误 `automation create failed: 400 Bad Request` 被画在**页面层** x=288 y=173，被弹窗遮罩压住，只有左侧 200px 露出）。同一屏用 `0 8 * * *` 或 `cron …` 则成功——说明是"帮助文案与解析器不一致"，不是网络问题。
- 证据：`shots/surfaces-automation-error-1440.png`（错误文字被弹窗压住）、`shots/surfaces-automation-data-1440.png`（cron 任务成功）；`surfaces-1440.json`/实测：`ERR {msg:"automation create failed: 400 Bad Request", x:288, y:173, modalX:460, fieldErr:[]}`；接口层实测（同一实例、浏览器内 fetch）：
  - `every 24h` → 400 `automation: schedule "every 24h": every needs a whole number of seconds like "30s"`
  - `daily 09:00` → 400 `automation: schedule "daily 09:00": time "09:00" must be HH:MM:SS…`
  - `0 * * * *` → 200（`nextRun 2026-11-24T21:00:00+08:00`）
  代码：`web/src/i18n/zh.ts:340`（帮助文案）、`internal/contracts/automation.go:391-404`（`daily` 必须 `HH:MM:SS`）、`:343-355`（`every` 只收秒）、`web/src/features/automation/automation-screen.tsx:113-116`（错误只进页面级 `setError`）、`web/src/api/read-models.ts:98-106`（`readJson` 抛 `automation create failed: 400`，丢弃服务端 message）。
- 规范：DESIGN.md §3 反模式（表单错误必须落到字段）；antd 6 `Form` 的 `rules`/`validateFields` 与 `Modal` 的 `onOk` 约定（`bash scripts/antd-cli.sh demo Form --version 6`）；BRIEF §5「功能不可用 = P0」。
- 建议：① 把 `scheduleHelp` 改成解析器真正接受的语法（`0 8 * * *` / `cron 0 8 * * * Asia/Shanghai` / `daily 09:00:00`），或让 contracts 的 `daily`/`every` 解析器接受 `HH:MM` 与 `24h`；② 提交失败时把服务端 message 写进 `form.setFields([{name:'schedule',errors:[…]}])`，并把错误区移到弹窗内（`Alert` 置于表单顶部）；③ `readJson` 保留响应体的 `error.message`，别只留状态码。

### S-05 顶栏 h1 与页面 h1 同文重复：每屏把标题渲染两遍 [P1]
- 现象：顶栏（`.zk-topband__title`，16px/600）与页面 `<h1 class="zk-screen__title">`（20px/600）文字完全相同、垂直相距 46px：自动化/能力/洞察/团队各两遍「自动化/能力/洞察/团队」；轨迹页更是「轨迹」出现在 tab（y=48）→ H1（y=111）两处；记忆页 tab+标题两遍。
- 证据：`shots/surfaces-automation-1440.png`、`surfaces-insights-1440.png`、`surfaces-team-1440.png`、`surfaces-capabilities-1440.png`、`surfaces-trajectory-1440.png`、`surfaces-memory-1440.png`；`surfaces-1440.json`：`headings[0]{cls:"zk-topband__title",text:"自动化",y:12,font:"16px"}`、`headings[1]{cls:"zk-screen__title",text:"自动化",y:73,font:"20px"}`；代码 `web/src/shell/top-band.tsx:39`（h1）+ `automation-screen.tsx:140` / `capabilities-screen.tsx:33` / `insights-screen.tsx:41` / `team-screen.tsx:127` / `trajectory-screen.tsx:45` / `memory-screen.tsx:33`。
- 规范：GitHub / Cursor 的页面骨架是"一条标题行（标题 + 右侧主操作）"，不重复；DSH 的 `ConversationRoot` 顶栏只放会话标题与视图 tab（`ConversationRoot.module.css:29-32` 头部 76px 与侧栏对齐），页面体内不再重复标题；WEB-CLIENT §9（一屏一个 H1）。
- 建议：二选一——顶栏只做窗口级信息（品牌/连接状态/主操作），页面内保留 `<h1>`；或页面内去掉 `<h1>`，把 `.zk-screen__note` 与主操作并入顶栏行。语义上全站只保留一个 `<h1>`（顶栏那个改成 `<div>`/`<span>` 或反之）。

### S-06 页面内距三套并存，主操作按钮掉到说明文字下方（标题行结构未启用） [P1]
- 现象：`.zk-screen` 统一 `padding:24px`（自动化/能力/洞察/团队/轨迹/记忆）；`/` 的正文块是 `.zk-thread__body{padding:16px 24px 0}`（顶边 16，其余 24）；`/inbox` 为 0（BRIEF §3 已记）。另外自动化与团队的 `<header class="zk-screen__header">` **没有** `--row` 修饰类，于是"新建任务/新建队友"按钮落在说明文字正下方（x=288 y=125），而不是标题行右侧——同一代码库里只有洞察用了 `--row`。
- 证据：`surfaces-1440.json`：`main.first{cls:"zk-screen",pad:"24px",y:49}` vs `now.first{cls:"zk-thread",pad:"0px"}`、`now.empty{y:65}`（49+16）；`automation.header{display:"block",y:73,h:84}`、`automation.primaries[0]{text:"新建任务",x:288,y:125}`；`team.primaries[0]{x:288,y:125}`；`insights.header{display:"flex",align:"flex-start"}`。代码：`web/src/design/global.css:781-796`（`.zk-screen` / `.zk-screen__header` / `--row`）、`:155`（`.zk-thread__body`）、`automation-screen.tsx:138`、`team-screen.tsx:125`。
- 规范：BRIEF §4.5 主流做法"页面标题行 + 右侧主操作"（GitHub / Cursor / Linear 一致）；DESIGN.md §5 间距 token（`--zk-space-6`）。
- 建议：自动化/团队改用 `<header className="zk-screen__header zk-screen__header--row">`（与洞察一致）；新增 `.zk-page{padding:var(--zk-space-6)}` 作为唯一页面内距，`/` 与 `/inbox` 都套用；`--row` 加 `align-items:flex-start` 已足够，按钮加 `.zk-screen__action` 固定右上。

### S-07 没有内容最大宽度：1600 以上表格/统计条/输入框被拉成一片 [P1]
- 现象：1600×1000 下 `#zk-main` 1336px，全部内容撑满：洞察两表各 636px、统计条 5 个数字只占 285px 而右侧空 1000px；Now 的 composer 宽 1288px（一行能塞 ~120 字），hero 与 composer 相距 ~500px；团队/自动化表格 1288px 宽、最后一列离内容 800px。
- 证据：`shots/surfaces-insights-1600.png`、`shots/surfaces-now-1600.png`、`shots/surfaces-team-1600.png`；`surfaces-viewports.json → wide`：`main{w:1336}`、`insights.tables[{w:636},{w:636}]`、`totals` 最右 573.5、`now.sender{w:1288}`；`global.css` 中 `.zk-screen`/`.zk-thread`/`.zk-empty` 均无 `max-width`。
- 规范：DSH 给所有会话面一条共享宽度轴 `--dsh-chat-content-width: clamp(680px, 64%, 920px)`、输入卡 `+32px`（`dsh-client-ui-conversation/src/client/skeleton/ConversationRoot.module.css:465-476`、`ChatView.module.css:66-71`）；antd 6 栅格/`Card` 的常规内容宽度约束。
- 建议：在 `.zk-screen` 内加一层 `.zk-page__inner{max-width:1120px;margin-inline:auto}`；composer 与 `.zk-thread__body` 用同一个 `--zk-content-max`（建议 880–960），保证"标题行 / 内容 / 输入卡"同一条轴。

### S-08 空态用 antd 默认插画 + 一句文案，位置在页面中上部，与标题和主操作脱节，且不告诉下一步 [P1]
- 现象：自动化/能力/团队/记忆的空态都是 `Empty.PRESENTED_IMAGE_DEFAULT`（184×152 svg，渲染 184×100）+ 一行 14px 灰字（"还没有定时任务。"/"还没有队友或子任务"），盒子 y=173（顶栏下 24 + 标题块 84），横向居中；而"新建任务/新建队友"主操作在 x=288 y=125 左上角——两者相隔 470px 且不在同一视觉组。空态里没有 `children`（没有"创建第一个"按钮），用户读完不知道该点什么。
- 证据：`shots/surfaces-automation-1440.png`、`surfaces-team-1440.png`、`surfaces-capabilities-1440.png`、`surfaces-memory-1440.png`；`surfaces-1440.json`：`team.antEmpty{x:296,y:173,w:1112,h:130,desc:"还没有队友或子任务",img:{w:1112,h:100}}`、`automation.antEmpty{y:173}`、`memory.antEmpty{y:179}`；代码 `automation-screen.tsx:156`、`team-screen.tsx:144`、`capabilities-screen.tsx:54/79`、`memory-screen.tsx:45`。
- 规范：antd 6 `Empty` 的官方"引导 + 主操作"形态是 `description` + `children`（`bash scripts/antd-cli.sh demo Empty customize --version 6`：`<Empty description={…}><Button type="primary">Create Now</Button></Empty>`），并可用 `styles={{image:{height:60}}}` 缩小插画；主流：Linear 空列表=一句说明+一个主操作、无插画，ChatGPT 项目空态=居中一句引导+就近的创建按钮。
- 建议：统一 `<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} styles={{image:{height:48}}} description=…>` + `<Button type="primary">` 作为 `children`；空态区域整体垂直居中（`align-self:center`、`margin-block:auto`），并与标题块同轴；或直接删掉插画、用 `Typography.Paragraph` 一句 + 主操作。

### S-09 加载态、空态、错误态三种形态三种位置：loading 居中、empty 顶部、error 一条红字 [P1]
- 现象：同一屏的 loading 用 `<div className="zk-empty"><Spin/></div>`（`.zk-empty` 是 `height:100%;align-items:center`），实测 spinner 在 y=564（垂直居中）；数据到达后换成 `<Empty>`，跳到 y=173（顶部）。团队更把 loading 写成 `<Empty description="正在读取团队…">`——**用空态组件表达加载**。错误态则是一条 `.zk-msg-error` 行（红字、无边框、无重试按钮）。
- 证据：`shots/surfaces-team-1440.png`（空态）、`shots/surfaces-automation-error-1440.png`（错误行 x=288 y=173 h=20.4）；`surfaces-h.mjs` 帧序列：`{"spin":{x:842,y:564.5,w:20,h:20}}` → 60ms 后 `empty:null` → 最终 `antEmpty{y:173}`；代码 `automation-screen.tsx:151-156`、`capabilities-screen.tsx:40-44`、`insights-screen.tsx:61-64`、`memory-screen.tsx:40-45`、`trajectory-screen.tsx:52-57`、`team-screen.tsx:141-144`（`<Empty description={t('team.loading')}/>`）、`global.css:177-182`（`.zk-empty` 被复用为 loading 容器）。
- 规范：DESIGN.md §2 状态语义（loading ≠ empty ≠ error，各自形态固定）；antd 6 `Spin`（`tip`/`size`）+ `Alert`/`Result` 的官方用法；`Empty` 的语义只用于"确实没有数据"。
- 建议：新增 `.zk-loading`（居中 `Spin`，与空态同位置）、`.zk-error`（antd `Alert type="error" showIcon` + 重试 `Button`）；空态/加载态都放在同一个内容槽位，避免位置跳变；team 的 loading 换成 `Spin`。

### S-10 团队屏一屏两个空态，活动表空行横跨整宽 [P1]
- 现象：成员为空时显示插画空态（y=173），下面"活动"小节仍然渲染一张表，表里只有一行居中的"还没有活动。"（td x=288 w=1128，y=364）；右侧"新建队友"按钮在左上。同一屏两处"什么都没有"，第二处还是空表格，并占掉首屏一半。
- 证据：`shots/surfaces-team-1440.png`；`surfaces-1440.json → team.tables[0]{x:288,y:325,w:1128,h:78,tds:[{text:"还没有活动。",x:288,w:1128,align:"center"}]}`；代码 `team-screen.tsx:215-244`（活动节无条件渲染）、`:222`（`locale={{emptyText:t('team.activityEmpty')}}`）。
- 规范：antd 6 `Table` 的 `locale.emptyText`（`demo Table custom-empty`）用于"表有结构但无行"，但一个整屏级空态已足够；DESIGN.md §3 反模式（不要用空面板"看起来完整"）；AGENTS §3.8「不做未请求的功能」。
- 建议：`members.length===0 && activities.length===0` 时只渲染一个空态（含"新建队友"按钮），活动节整体隐藏；活动为空但成员存在时，活动节用一行 `Typography.Text type="secondary"` 而不是空表。

### S-11 洞察统计条：label 在上、数字在下，5 个数字不等宽左对齐，没落在网格上 [P1]
- 现象：`<dl class="zk-insights__totals">` 5 组 `dt`（12px 灰）在上、`dd`（20px/600）在下，横向 gap 16px 紧密排开，总宽仅 285px（会话 3 / 轮次 3 / 输入 tokens 48 / 输出 tokens 27 / 工具调用 3）；因为每组按文字宽度自适应，5 个数字的 x 分别是 300/340/399.4/478.1/549.4——**间距不相等，数字不在同一网格**；1600 宽时右侧空出 1000px。
- 证据：`shots/surfaces-insights-1440.png`、`surfaces-insights-1600.png`；`surfaces-1440.json → insights.totals`：`[{label:"会话",value:"3",x:288,w:24,ddFont:"20px",ddAlign:"start"},…,{label:"工具调用",x:525.4,w:48}]`；代码 `insights-screen.tsx:66-80`、`global.css:1065-1082`。
- 规范：antd 6 `Statistic`（`title`/`value`/`formatter`/`groupSeparator`/`precision`/`loading`）就是为统计数字而设（`bash scripts/antd-cli.sh info Statistic --version 6`、`demo Statistic basic`），数值在上、标题在下；AGENTS §3.2「官方件优先」。
- 建议：改用 `<Row gutter={[16,16]}><Col flex="1 1 160px"><Statistic title=… value=…/></Col>…`，每格等宽；数字右对齐或等宽字体（`font-variant-numeric: tabular-nums`）。

### S-12 洞察两张表并排却不等高、列宽不对齐，数字列左对齐 [P1]
- 现象：「按天」556×312（日期列 456 + 轮次 100，7 行），「按模型」556×78（模型 336 + 调用 100 + tokens 120，1 行）。两表数值列的 x 分别是 744 / 1196 / 1356，互不对齐；三个数值列 `text-align: start`，所以 "0"、"2"、"50" 都是左对齐、末位不成列；左侧日期列 456px 只放 10 字符，右侧模型列 336px 只放 15 字符，中间大片空白；两表底部相差 234px。
- 证据：`shots/surfaces-insights-1440.png`；`surfaces-1440.json → insights.tables`：`[{w:556,rows:7,ths:[日期:456@288, 轮次:100@744], tds:[2026-11-18@288, "0"@744,align:"start"]},{w:556,rows:1,ths:[模型:336@860, 调用:100@1196, tokens:120@1296]}]`；代码 `insights-screen.tsx:85-108`（列未声明 `align:'right'`，无 `scroll`），`global.css:1084`（两列 1fr）。
- 规范：antd 6 `Table` 的 `column.align`（数字列惯例 `right`）；主流数据表（GitHub Insights / Linear 报表）数字列右对齐、单位列窄；对比同项目内 `dsh-client-ui-chat` 的用量面板用等宽数字。
- 建议：数值列 `align:'right'` + `f-number`（`font-variant-numeric: tabular-nums`）；日期列 `width:120`；两表改为上下堆叠（`grid-template-columns:1fr`）或让「按模型」补齐行数不齐的视觉（加 `Table` 的 `locale.emptyText` 或干脆用一张合并表：日期 / 模型 / 轮次 / tokens）。

### S-13 检查器右栏与左栏顶边错位 43px，且两栏是两套 tab 语言 [P1]
- 现象：`/c/:id/files` 左栏是顶栏内的 antd `Tabs`（对话/轨迹/记忆/上下文，**y=48，高 38，14px 带下划线**）；右栏检查器自己的 pill tab（开始/文件，**y=91，高 27，13px**）。两条 tab 行既不同高也不同基线，右栏顶部凭空多出一条 4px 内距的船头；左栏 tab 下划线在 y=86，右栏 tab 底边在 y=118。
- 证据：`shots/surfaces-files-1440.png`、`surfaces-guide-1440.png`；`surfaces-1440.json → files`：`tabs[0]{text:"对话",y:48,h:38}`、`inspTabs[0]{text:"开始",y:91,h:27,font:"13px"}`、`insp{x:1060,y:87,w:380}`；代码 `web/src/design/global.css:935-955`（`.zk-inspector__tabs{padding:4px 8px}`、`.zk-inspector__tab{font-size:var(--zk-type-xs)}`）、`inspector-panel.tsx:125-144`、`top-band.tsx:42-59`。
- 规范：DSH 的 `ConversationRoot` 头部固定 76px 并明确"两侧规则在列边相接"（`ConversationRoot.module.css:29-32`）；antd 6 同层级导航应使用同一 `Tabs` 变体（`demo Tabs` 的 `card`/`editable` 等）。
- 建议：右栏顶部留出与顶栏等高（`--zk-band-height` + tabs 高）的占位，或让检查器 tab 与左栏 tab 同基线；两栏都用 antd `Tabs`（右栏 `size="small"` 同一变体），去掉自造 pill。

### S-14 `/files` 未选文件时渲染"开始"内容并高亮"开始"tab，`文件` tab 永远无法处于选中态 [P1]
- 现象：URL 是 `/c/:id/files`，但页面显示引导文案、且"开始"tab 高亮（`aria-selected=true`）、"文件"不高亮。点"文件"又回到 `/files`，`guide` 判定再次为真 → 仍然是"开始"高亮。用户无法判断当前在哪个页面，且"文件"页面的空状态（应显示"选择文件查看内容"）被"开始"页的文案取代。
- 证据：`shots/surfaces-files-1440.png`（右栏文案是引导语，tab"开始"带灰底）、`surfaces-guide-1440.png`（完全相同）；`surfaces-1440.json → files.inspTabs`：`[{text:"开始",active:true},{text:"文件",active:false}]`、`files.inspPreview: null`（预览区根本没渲染）；代码 `inspector-panel.tsx:121`（`const guide = relative === '' && !searchParams.get('view')`）、`:129-143`、`:156-179`。
- 规范：WEB-CLIENT §1.2「tab 状态在 URL 里，刷新保持面」；antd 6 `Tabs` 的 `activeKey` 必须由路由推导。
- 建议：`guide` 只看路由（`location.pathname.endsWith('/guide')`），`/files` 无论是否选中文件都渲染树 + 预览（空预览显示 `inspector.pickFile`），tab 高亮跟随路由。

### S-15 404：顶栏标题退化成品牌名 `ZeoKeel`，内容贴在左上，页内标题用了 h4 [P1]
- 现象：`/no-such-route` 的顶栏 h1 变成 `ZeoKeel`（品牌名，因为 `routeForPath` 无匹配时回落 `app.name`），页面标题"这个地址没有页面"是 `Typography.Title level={4}`（20px h4），整块 `.zk-stub` 在 x=264 y=49 起、内距 32/24、`max-width:720px` —— 内容挤在左上角，与任何屏的骨架（24px 内距、h1 20px、标题行）都不同；只有"回到现在"一个按钮（这点是对的，层级有主操作）。
- 证据：`shots/surfaces-notfound-1440.png`、`surfaces-notfound-390.png`；`surfaces-1440.json → notfound`：`headings[0]{cls:"zk-topband__title",text:"ZeoKeel"}`、`first{cls:"zk-stub",x:264,y:49,w:720,pad:"32px 24px"}`、`primaries[0]{text:"回到现在",x:288,y:191.6}`；代码 `top-band.tsx:25-29`、`not-found-screen.tsx:17-26`、`global.css:264-269`（`.zk-stub`）。
- 规范：全站页面骨架一致性（WEB-CLIENT §9）；主流（GitHub 404 / Linear）是"顶栏保留应用名 + 居中的 404 标题 + 返回主操作"或"标题行给页面名"。
- 建议：顶栏为 404 给专用 key（如 `notFound.bandTitle`: "页面不存在"）；`.zk-stub` 换成 `.zk-screen` + `.zk-screen__header`，正文居中（`margin-block:auto`），标题用 `<h1 class="zk-screen__title">`。

### S-16 轨迹屏信息量低于对话内的工具行：时间线只有圆点+对勾，时间列是消息序号，没有耗时与参数 [P1]
- 现象：时间线每行 = antd Timeline 圆点（灰色空心）+ 行内 `check` 图标（双图标）+"工具 · web_fetch" + 右侧 `#2`（实为 `messageIndex+1` 的消息序号，被当作时间位）+ 一块 `ok` 结果块。没有耗时、没有参数（URL/请求体）、没有状态文字、不能展开；而同一会话在对话里那一行显示的是"工具 web_fetch › / http://127.0.0.1:9861/health · 完成 / 16 入 · 9 出"。
- 证据：`shots/surfaces-trajectory-1440.png`；`surfaces-1440.json → trajectory`：`timeline[0]{text:"工具 · web_fetch#2ok",y:179,h:59.4}`、`toolIo[0]{x:314,y:204.4,w:1102,margin:"4px 0 0",pad:"8px"}`；代码 `trajectory-screen.tsx:62`（`color:"gray"`）、`:66`（`<ZkIcon name="check"/>` 与 Timeline 圆点重复）、`:87`（`time: "#" + (messageIndex+1)`）、`:70`（`<pre class="zk-tool-io">` 直接贴内容）、`global.css:1048-1057`（`.zk-trajectory__time` 只有颜色，没有对齐/等宽）。
- 规范：antd 6 `Timeline` 的 `items[].dot` 用于自定义图标（要么用 dot 要么给 `color`，不要两套）；DSH 的工具行契约是"单行折叠 + 状态点 + 耗时 + 可展开原始参数"（BRIEF §4.5）；DESIGN.md §3 反模式（不要把序号伪装成时间）。
- 建议：去掉 Timeline 圆点（`dot={null}` 或直接改 `Timeline` 为 `items[].dot` 用状态图标），或用 `Badge` 状态点；把 `#2` 换成真实耗时（`step.duration` 若协议有）或删掉该列；结果块左侧与标题文字对齐（当前 x=314 vs 标题 335）、加 `margin-inline-start` 与折叠（`Typography.Paragraph ellipsis` + 展开）；补参数摘要（`toolMeta` 已有 label，可从 `step.args` 取 url/command）。

### S-17 子会话屏：面包屑没有父会话标题与子会话身份，顶栏仍是父会话标题 + 父会话 tabs [P1]
- 现象：`/c/:id/child/:childId` 的头部是一条 41px 高的窄行："← 返回父会话　子会话　Esc 返回"——三处都不是面包屑（没有父会话标题、没有子会话名/id）；顶栏仍是父会话标题（h1）+ 对话/轨迹/记忆/上下文 tab，点 tab 会跳到父会话的页面，看起来像"当前就在父会话"。正文只有一个成员详情组件，未找到时显示"找不到这个队友（可能已关闭）。"
- 证据：`shots/surfaces-child-1440.png`；`surfaces-1440.json → child`：`childHeader{x:264,y:87,w:1176,h:41,text:"← 返回父会话子会话Esc 返回"}`、`tabs[0]{text:"对话",selected:true}`、`headings[0]{text:"帮我确认一下网关的健康检查是否能通…"}`；代码 `child-session-screen.tsx:34-44`、`global.css:1021-1027`、`top-band.tsx:20-29`。
- 规范：antd 6 `Breadcrumb`（项目内检查器已用 `Breadcrumb`，见 `inspector-panel.tsx:187-206`）；主流（Cursor/Claude 的子任务视图）都是"父会话 › 子会话名"面包屑。
- 建议：`zk-child-header` 换成 `Breadcrumb`（父会话标题可点 + 子会话名）；顶栏在 child 路由下显示子会话名并隐藏父会话的 THREAD_TABS（`top-band.tsx` 里按路径判断）。

### S-18 能力页"来源"列直接打印绝对路径，不省略 [P2]
- 现象：一条技能行的来源是 `/data/zeokeel/.cache/ui-audit/data-surfaces/workspace/.agents/skills/release-notes`（90 字符，塞在 469px 的 `Tag variant="filled"` 里）。它把本机路径泄露到 UI，且窄屏必然溢出。
- 证据：`shots/surfaces-capabilities-data-1440.png`；`surfaces-d.mjs → CAPS`：`ths[来源 w:469.6]`、`rows[0]` 含该绝对路径；代码 `capabilities-screen.tsx:62-70`（`dataIndex:'source'` → `<Tag variant="filled">{value}</Tag>`）。
- 规范：DESIGN.md §5 token/文案（UI 不出现实现路径）；antd `Typography.Text ellipsis` + `Tooltip`。
- 建议：映射成类别标签（工作区 / 全局 / 插件）+ `Tooltip` 显示完整路径；或 `ellipsis` + `title`。

### S-19 自动化表格：时间表列 code 块换行、日期格式与洞察页不一致、操作列 4 个按钮 [P2]
- 现象：`时间表` 用 `<Typography.Text code>` 显示 canonical 值 `cron 0 * * * * Asia/Shanghai`，在 170px 列里折成两行，把行高撑到 61px；`下次运行` 用 `new Date(...).toLocaleString()` → `2026/11/24 21:00:00`，而洞察页日期是 `2026-11-18`（ISO）；操作列 250px 塞 4 个 `size=small` 按钮（立即运行/编辑/历史/删除）。
- 证据：`shots/surfaces-automation-data-1440.png`；`surfaces-d.mjs → AUTO`：`rows[2].h:61`、cell 时间表 `cron 0 * * * *\nAsia/Shanghai`、`cells[2]{x:742,t:"2026/11/24 21:00:00"}`、`headings/ths`：任务 284 / 时间表 170 / 下次 170 / 上次 170 / 启用 84 / 操作 250；代码 `automation-screen.tsx:183`（`Typography.Text code`）、`:189/195`（`toLocaleString`）；对比 `insights-screen.tsx:91`（日期直接用后端 ISO 字符串）。
- 规范：DESIGN.md §5（日期格式统一，建议 `Intl.DateTimeFormat` 单一入口）；antd 6 `Table` 的 `column.ellipsis`。
- 建议：时间表列显示人类可读摘要（模块已有 `automation.Describe` → "Cron 0 * * * *"）或 `ellipsis`+`Tooltip`；日期统一走一个 `formatDateTime` helper；操作列把低频的"历史/删除"收进 `Dropdown`（保留"立即运行"为主操作）。

### S-20 删除确认用裸 `Popconfirm`：确定是蓝色 primary，焦点留在触发按钮，项目自带 `ConfirmDanger` 未使用 [P2]
- 现象：点行内红色"删除"弹出 antd `Popconfirm`（"删除「每小时的检查」？ 取消 / 确定"），"确定"是**蓝色实心 primary**、删除语义没有危险色；弹层出现在 x=1240 y=141，压在表头上方；打开后焦点仍在触发的"删除"按钮上（实测 `document.activeElement` 是 `ant-btn-dangerous`），要按 Tab 才能到"确定"。
- 证据：`shots/surfaces-automation-delete-confirm-1440.png`；`surfaces-e.mjs` 输出 `CONFIRM {x:1240,y:141,w:200,h:78,text:"删除「每小时的检查」？ | 取 消 | 确 定"}`、焦点探针 `{cls:"…ant-btn-dangerous…ant-popover-open",text:"删 除"}`；随后按 Enter 未删除（`after` 与 `before` 相同）；代码 `automation-screen.tsx:225-232`（`Popconfirm` 无 `okButtonProps={{danger:true}}`），项目已有 `web/src/ui/confirm-danger.tsx:41-58`（cancel 自动聚焦 + 确认 `danger`）。
- 规范：项目自身契约 `docs/DESIGN.md`/`confirm-danger.tsx:6-10`（"the destructive action is never the default"）+ AGENTS §3（审批/危险动词不中性化）；antd `Popconfirm` 的 `okButtonProps`。
- 建议：改用 `ConfirmDanger`（与全站一致），或至少 `okButtonProps={{ danger: true }}` + `okText="删除"`。

### S-21 运行历史弹窗 720px 只放一个默认空插画 [P2]
- 现象：`width={720}` 的历史弹窗在没有记录时只有一个默认空插画 + "还没有运行记录。"，白框 720×190，插件尺寸与内容完全不成比例；而团队活动表同类空状态用的是一行文字。
- 证据：`shots/surfaces-automation-history-1440.png`；`surfaces-e.mjs → HISTORY {w:720, body:"还没有运行记录。"}`；代码 `automation-screen.tsx:278-308`（`width={720}`、`<Empty>`）。
- 规范：antd 6 `Empty` 的 `styles={{image:{height:60}}}` 或 `PRESENTED_IMAGE_SIMPLE`；同类空状态在项目内应统一。
- 建议：历史弹窗按内容自适应宽度（去掉固定 720，或 `width={520}`），空态用 simple image + 一句说明。

### S-22 Now hero 与 composer 在同一轴上（cx 均为 852），但 hero 悬在空态区中心、与主操作相距 500px [P2]
- 现象：实测 hero 标题 `cx=852`、composer `cx=852`——**水平是居中的**（与任务假设不同，我复核了 1440/1600 两个视口都一致）。真正的问题是垂直：`.zk-empty` 占满剩余区（y=65..573），hero 被垂直居中到 y=288，而 composer 在 y=754，两者相距 ~500px；且审批卡出现时 `.zk-empty` 高度从 789 缩到 508，hero 随之上移 60px 以上（1600 下 y=444 → 1440+审批下 y=288）。
- 证据：`shots/surfaces-now-1440.png`（hero y=288、composer y=754、审批卡 y=573）、`shots/surfaces-now-1600.png`（hero 无审批卡、y≈444）；`surfaces-1440.json → now`：`welcomeTitle{cx:852,y:288,font:"24px",lh:"32px"}`、`composer{cx:852,y:754}`、`empty{y:65,h:508}`；`surfaces-viewports.json → wide.now`：`empty{h:789}`；`global.css:177-182`（`.zk-empty{height:100%;align-items:center}`）、`now-screen.tsx:66-69`。
- 规范：DSH 的新会话 hero 是"标题块 + 工作区选择 + 输入卡"**同一个 centered stack**（`gap:12px`、共享 `max-width: var(--dsh-composer-card-max-width)`，`HeroShell.module.css:9-24`），标题 26/32 wt500 紧贴输入卡；ChatGPT 也是 hero 与输入框组成一个整体。DESIGN.md §5 垂直节奏。
- 建议：把 hero 从"占满剩余空间的居中容器"改为"与 composer 同一列、间距 12–16px 的 stack"（`.zk-thread__body` 内 `margin-block:auto` 的下半部分），或用 `grid-template-rows: 1fr auto` 让 hero 与 composer 成为一个块；这样审批卡出现时 hero 不会跳。

### S-23 hero 字号 24/32 与页面标题 20/28、Empty 文案 14 三档并存 [P2]
- 现象：Now 的 hero 是 `Welcome variant="borderless"`，标题 24px/32px（antd `fontSizeHeading3`），描述 14px/22；而所有 `zk-screen__title` 是 20px/28；antd `Empty` 描述 14px `rgba(0,0,0,.45)`。同一产品里"页面标题"有两种字号，且 hero 比页面标题更大。
- 证据：`surfaces-1440.json → now.welcomeTitle{font:"24px",lh:"32px"}` vs `automation.headings[1]{font:"20px",lh:"28px"}`；`surfaces-h.mjs → TEAM.antEmpty.desc{font:"14px",color:"rgba(0,0,0,.45)"}`；`web/node_modules/@ant-design/x/es/welcome/style/index.js`（borderless title 用 `fontSizeHeading3`）；`global.css:798-803`（`.zk-screen__title` = `--zk-type-l` 20/28）。
- 规范：DSH hero 标题 26/32 wt500 且**只有 hero 一处**用大字号（`HeroShell.module.css:34-38`）；DESIGN.md §5 字号 token（一屏只用 3 档以内）。
- 建议：hero 用 `--zk-type-xl`（24/32）作为唯一"首屏标题"档并在 DESIGN.md 标注；页面标题保持 20/28；空态描述与 `--zk-type-xs` 对齐。

### S-24 顶栏标题（16px）比页面标题（20px）小，信息层级倒挂 [P2]
- 现象：`.zk-topband__title` 是 `--zk-type-m`（16px/600），页内 `.zk-screen__title` 是 `--zk-type-l`（20px/600）。屏幕上从上到下是"小标题 → 大标题"，配合 S-05 的重复，读者会以为上面一行是面包屑。
- 证据：同 S-05 的数字（y=12 font 16px vs y=73 font 20px）；代码 `global.css:123-131`（`.zk-topband__title`）、`:798`（`.zk-screen__title`）。
- 规范：DESIGN.md §5 字号层级；WEB-CLIENT §9。
- 建议：与 S-05 一并解决——顶栏只留一行 14/16px 的窗口级标题，页面唯一的 h1 用 20/28。

### S-25 轨迹/记忆页 tab 与页面标题同名，出现"轨迹→轨迹"的连续重复 [P2]
- 现象：轨迹页自上而下是 tab「轨迹」（选中，y=48）→ h1「轨迹」（y=111）→ 说明；记忆页 tab「记忆」→ h1「记忆」。顶栏那一行还是会话标题，等于同一视线内三次同义。
- 证据：`shots/surfaces-trajectory-1440.png`、`shots/surfaces-memory-1440.png`；`surfaces-1440.json → trajectory.tabs[1]{text:"轨迹",selected:true}` 与 `headings[1]{text:"轨迹",y:111}`。
- 规范：同 S-05；页面内的子面（tab）已表达位置，不需要再来一个同名 h1。
- 建议：轨迹/记忆这类"会话内子面"去掉页内 h1，把说明文字放到顶栏 tab 行下方或作为 `Tooltip`/`aria-description`。

### S-26 390 下自动化/团队的表单与表格仍按桌面列宽绘制（未做响应式） [P1]
- 现象：390 下自动化的"新建任务"按钮仍在（x=224），但一旦有数据，6 列表格会挤进 126px；团队活动表实测 78px 宽、表头"时间/类型/内容"逐字换行（截图里"时/间"竖排）。`Table` 未传 `scroll={{x}}`，也没有 `responsive` 列定义。
- 证据：`shots/surfaces-team-390.png`、`shots/surfaces-automation-390.png`；`surfaces-viewports.json → narrow.team.tables[0]{x:224,w:78,h:122,innerW:90,clientW:78}`；代码 `team-screen.tsx:217-243`、`automation-screen.tsx:158-237`（均无 `scroll`）。
- 规范：antd 6 `Table` 的 `scroll={{x:'max-content'}}` 是窄屏官方解法（`demo Table fixed-columns`）；BRIEF §5 移动端可用性。
- 建议：两表加 `scroll={{ x: 720 }}`；`size="small"` 保留；窄屏隐藏"上次运行/来源"等次要列（`responsive:['md']`）。

### S-27 `.zk-empty` 被同时用作"空态容器"和"加载容器"，语义与位置都错 [P2]
- 现象：`<div className="zk-empty"><Spin/></div>` 在 automation/capabilities/insights/memory/trajectory 五处出现，而真正的空态用的是 antd `<Empty>`（无容器类）。于是 CSS 里"空态"这个类实际控制的是 loading 的居中。
- 证据：`automation-screen.tsx:152`、`capabilities-screen.tsx:41`、`insights-screen.tsx:62`、`memory-screen.tsx:41`、`trajectory-screen.tsx:53`；`global.css:177-182`；实测 loading spinner y=564 vs empty y=173（S-09）。
- 规范：DESIGN.md §3 命名/语义（类名即契约）。
- 建议：拆成 `.zk-state{display:grid;place-items:center;height:100%}` 供 loading/empty/error 共用，保留 `.zk-empty` 只给空态。

### S-28 洞察范围切换 `Segmented` 只有 24px 高、贴标题顶部，1600 下离标题 1100px [P2]
- 现象：`近 7 天/近 30 天` 的 `Segmented size="small"` 在 header 的 `flex-start` 对齐下与 H1 顶边平齐（y=73、高 24），不像与标题块垂直居中；1440 下 x=1289（内容右边界 1416），1600 下 x=1449——离标题 1000+px，中间只剩空白。
- 证据：`shots/surfaces-insights-1440.png`、`surfaces-insights-1600.png`；`surfaces-1440.json → insights.seg{x:1289.1,y:73,w:126.9,value:"近 7 天"}`、`surfaces-viewports.json → wide.insights.seg{x:1449.1}`；代码 `insights-screen.tsx:44-54`、`global.css:791-796`（`align-items:flex-start`）。
- 规范：antd 6 `Segmented` 与标题行的常规组合（`demo Segmented`）；BRIEF §4.5「页面标题行 + 右侧主操作」——右元素应与标题块居中对齐，且随内容宽度收敛。
- 建议：`header--row` 用 `align-items:center`（或把 Segmented 包一层 `align-self:center`），并与 S-07 的内容最大宽度一起收敛。

## 跨区域共性问题

1. **骨架未参数化**：`zk-screen` 这一层同时承担"内距 / 滚动 / 宽度 / 标题块"四种职责，于是每屏各自补丁（`/` 用 `zk-thread`、`/inbox` 0、`.zk-stub` 32/24、`.zk-child-header` 41px）。需要一套 `Page`（head：标题行+主操作；body：max-width 内容槽；state：loading/empty/error 同槽位）。
2. **标题存在性与层级混乱**：顶栏 h1（16）+ 页内 h1（20）+ tab 同名（14）+ 404 退化品牌名，四个来源共同产生 S-05/S-24/S-25/S-15。全站应只有一个 `<h1>`，且由路由表（`routes.ts` 的 `titleKey`）唯一决定。
3. **状态三态（loading/empty/error）没有统一形态**：S-08/S-09/S-10/S-27 是同一个根因——空态用 antd 默认插画且无下一步、加载态借用空态类、错误态是一条无边框红字（还常被弹窗/遮罩吞掉，S-04）。
4. **"数字"没有网格**：洞察统计条与两张表（S-11/S-12）、自动化表格的时间列（S-19）都缺 `Statistic` / `align:'right'` / `tabular-nums` / 统一日期格式。
5. **窄屏没有第二套布局**：`@media(max-width:…)` 只在 1024 区间改了侧栏宽度与检查器定位，没有任何一屏做列重排（S-01/S-02/S-26）。侧栏 200px 在 390 下占 51% 是全局病根（shell 审计已记，我这边表现为内容列 126px）。
6. **右栏/子面是二等公民**：检查器 tab 与主 tab 不同基线（S-13）、`/files` 高亮错（S-14）、子会话没有面包屑（S-17）、轨迹没有参数与耗时（S-16）——都是"主视图用官方件、副视图自己拼 div"的结果。