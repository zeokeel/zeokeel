# composer / 审批 / 计划·提问 / 队列 审计

- 覆盖路由与状态：`/`（现在：空闲 / 待审批禁用 / 390 窄屏）、`/c/:id`（会话内审批、原始参数展开、运行中慢流、运行中排队、完成态用量行、390 窄屏）、`/inbox`（审批行单条 / 两条）、`/settings/model`（改模型以触发 slow 场景）；斜杠菜单（有结果 / 空结果）、`@` 提及（空工作区 / 有文件 / Tab·Enter 补齐 / Esc 关闭）、范围下拉、Tab 焦点序、Esc 语义（普通 / 下拉内 / IME 合成中）、多审批排队。
- 实例：`9860`（只读基线，token 只用于截图）、`9873`（lead 分配的专属沙箱，可造数据）。两实例的几何测量**逐像素一致**（duplicate evidence）。
- 截图：41 张（`shots/composer-*.png`），关键 12 张已逐张目视核对。
- 量测脚本与原始 JSON：`c1-now.mjs`/`c2-thread.mjs`/`c3-cross-narrow.mjs`/`c5-composer.mjs`/`c7-approval.mjs`/`c8-enabled.mjs`/`c9c-slow.mjs`/`c10-two.mjs`/`c11-race.mjs`/`c12-esc390.mjs`/`c13-scope.mjs`/`logs/*.json`。

## 结论

1. **审批把 composer 变成了全局单例的受害者**：任何会话只要有 1 条待审批，`/` 和**所有**会话的输入框同时禁用（`approvals` 是全局数组，不分 session），用户既不能开新对话也不能在别的会话继续工作，而卡片只显示"最老的那一条"。这是本区域最严重的功能缺陷。
2. **Esc 在审批存在时有四种含义、界面同时印出相反的两种**：同一屏上卡片写「Esc 拒绝」、下方 hint 写「Esc 清空草稿」或「Esc 停止本轮」，实际生效的是拒绝；在「仅此一次」下拉里按 Esc 关下拉会**顺带把审批拒掉**；中文输入法按 Esc 取消候选词也会**把审批拒掉**（实测）。
3. **输入列被 prefix 挤到内容区 24% 处**：placeholder 与刚输入的文字都从 x=561.8 起（`+` 在 x=313.1），因为 253px 宽的权限/模型选择器被塞进 antd X `Sender` 的 `prefix`（同一 flex 行的左侧）。空 composer 就 96px 高（minRows=2），三件套竖排成左栏 —— 与 ChatGPT / DSH（工具在文本下方一行）都相反，而代码注释声称这是"DeepSeek 客户端的做法"（与 DSH 源码不符）。
4. **审批三动作没有语义层级**：`拒绝` 与 `允许一次` 默认态、hover 态**完全相同**（hover 都变主色蓝 rgb(54,107,255)），危险动作用户只能靠位置区分；范围下拉与允许按钮会重复同一句「本会话内始终允许」。DSH 的做法相反：拒绝 hover 变**红**（`interactive-bg-hover-danger`），允许用 primary 填充。
5. **窄屏（390）composer 与审批卡完全不可用**：textarea 宽度 0、发送键在 x=489.8（视口 390）、composer 底边 922 > 844 落到视口外；审批卡被压成 78px 竖条，逐字换行，"原始参数"竖排。
6. 大量控件高度（24 / 32）既不接 antd 默认规格之外的自有 token（28 / 36 / 40），composer 还完全没有可见焦点环；无障碍名是 `arrow-up` / 英文 `Stop loading`。

## 发现

### C-01 一条待审批锁死所有会话与「现在」屏的 composer（跨会话污染） [P0]
- 现象：任一会话出现待审批后，`/` 与**其它无关会话**的输入框同时变灰、placeholder 变成「先处理上面的审批…」，且无法通过任何入口开始新一轮；新开会话同样立刻被禁用。界面上只有一句"先处理上面的审批…"，但那张卡并不属于当前会话。
- 证据：
  - `shots/composer-two-pending-card.png`、`shots/composer-crosssession-other-thread-1440.png`（9860 普通会话 `sess-1795521631279059167` 里出现别人的审批卡 + 输入框禁用）、`shots/composer-tab-order.png`（`/` 屏同样禁用）。
  - 并发实验 `logs/c11.json`：两个独立 context 各开会话并发发送 → 两个页面 `taDisabled: true`、`cards: 1`、`queueText: "还有 1 条待审批"`。新开会话的输入框同样 disabled（`P1/P2` 输出一致）。
  - 代码：`web/src/features/thread/thread-screen.tsx:484-485` `disabled={approvals.length > 0}` / `placeholder={approvals.length > 0 ? …}`；`web/src/features/now/now-screen.tsx:27,125`；`web/src/features/approvals/approval-dock.tsx:18`（`const oldest = approvals[0]` 是全局队列，不按 session 过滤）；队列过滤只有过期（`approvals-context.tsx:46`）。
  - 反证：`logs/c11.json` 证明第 2 条审批**只能靠并发竞速**才造得出来 —— 因为第 1 条已经锁死了所有发送入口。
- 规范：DSH 审批面板接管的是"该请求所属会话"的 composer（`dsh-client-ui-approval/README.zh.md`：「接管 Conversation composer」），并有"待处理请求锁 + 较早请求的失败不解锁替代请求"的语义；主流做法（ChatGPT/Claude Code）里审批是**会话内**阻塞，从不阻止你开新对话。
- 建议：`ApprovalDock` 与 composer 的 disabled 只认 `approvals.filter(r => r.sessionId === currentSessionId)`；其余会话的审批只以徽标/收件箱提示（`+N`）呈现，不参与 disabled 判定。`/` 屏不因别的会话有审批而禁用（若确实要拦，文案必须写清"哪个会话在等审批"并给跳转）。

### C-02 同一屏印出两条互相矛盾的 Esc 快捷键说明 [P0]
- 现象：审批在屏时，卡片 footer 写「Esc 拒绝 · Cmd/Ctrl+Enter 允许」，而其下方 100px 处的 composer hint 同时写「… Esc 清空草稿」或「Esc 停止本轮」。实际按 Esc 是**拒绝审批**（approval 级 30 > stopRun 20 > clearDraft 10）。用户按 hint 的指引"清空草稿/停止本轮"，换来的是把工具调用拒掉。
- 证据：
  - `shots/composer-seeded-now-approval.png`（卡片写 Esc 拒绝；hint 写 `Enter 或 Cmd/Ctrl+Enter 发送 · Shift+Enter 换行 · Esc 清空草稿 · / 命令`）
  - `shots/composer-scope-session.png`（运行中被审批阻塞时，hint 变成 `Enter 排队发送 · Esc 停止本轮 · Shift+Enter 换行 · / 命令`，与卡片「Esc 拒绝」再次冲突；而此状态下输入框本身就是 disabled，两条提示都无从执行）
  - `logs/c12.json` `ESC-DENY`：cardsBefore 1 → after 0，`taDisabled: false`，收件箱归零 = 确实拒绝。
  - 代码：`web/src/keyboard/esc-arbiter.ts:23-30` 的级别表；`approvals-context.tsx:90-99`（level `approval`）；`composer.tsx:396-403`（hint 只按 phase 取文案，完全不知道审批状态）。
- 规范：DSH 只在"焦点在审批详情区内部"时才把 Enter/Esc 当决定（`ApprovalCard.tsx`：`if (event.defaultPrevented || !event.currentTarget.contains(document.activeElement)) return`），Esc 用于关闭警告卡时明确"不会拒绝请求"。ZeoKeel 自己也不许两处给同一按键两套承诺（DESIGN.md §2 状态语义不可降级）。
- 建议：① composer hint 增加 disabled/approval 分支（`composer.hint.blocked`：「先处理上面的审批 · Esc 拒绝」）；② 或让 composer 的 Esc 文案在审批在屏时整段隐藏；③ 更彻底：Esc 只在审批卡内有焦点时拒绝，其余场景保留"清空草稿"。

### C-03 在「仅此一次」下拉里按 Esc 关下拉，会顺带把审批拒掉 [P0]
- 现象：点开范围下拉后按 Esc，下拉关闭，**审批同时被拒绝**，队列自动前进到下一条。操作者以为只是关了个下拉。
- 证据：`logs/c12.json` `ESC-IN-DROPDOWN`：`before {dropdown:true, cards:1, queue:"还有 1 条待审批"}` → `after {dropdown:false, cards:1, queue:null}`（第一条被拒、第二条顶上，所以计数没变但队列文案消失）；`shots/composer-esc-in-dropdown.png`。后续再按一次 Esc → `cards 0`。
- 代码：`esc-arbiter.ts:115` 用 `document` + `capture: true` 监听，先于 antd Select 自己的 Esc 处理；`esc-arbiter.ts:84-95` 命中第一个候选就 `preventDefault + stopPropagation`，没有"浮层已消费"的判断。
- 规范：DSH 在决定动作前检查 `event.defaultPrevented`，并让输入控件/下拉自己保留键盘处理（README.zh.md：「输入控件与输入法候选保留各自的按键」）。
- 建议：arbiter 在 `handleKey` 开头加 `if (key.defaultPrevented) return false`，并在审批卡打开浮层（Select/Popover）时临时停用 `deny-oldest-approval` claim（或用 `active` 条件把它关掉）。

### C-04 中文输入法按 Esc 取消候选词 = 拒绝审批（缺 `isComposing` 守卫） [P0]
- 现象：拼音候选窗口开着时按 Esc（最常见的"取消本次候选"），事件被 document capture 的 arbiter 吃掉并**拒绝审批**。
- 证据：`logs/c15-ime.mjs` 结果：派发 `keydown{Escape, isComposing:true, keyCode:229}` → `defaultPrevented: true`，`cards 1 → 0`，`taDisabled false`（=已拒绝并解锁）。截图 `shots/composer-ime-esc.png`。
- 代码：`esc-arbiter.ts:84-90` 只判断 `key.key === 'Escape'` 与 `key.repeat`，没有 `isComposing` / `keyCode === 229` 判断（`EscKey` 类型里也根本没有这两个字段）；`shortcut-arbiter` 同理。
- 规范：DSH `ApprovalCard.tsx:50-54` 守卫 `event.repeat || composing.current || event.nativeEvent.isComposing || event.keyCode === 229` 才放行；这是中文环境的硬要求。
- 建议：`EscKey` 增加 `isComposing` / `keyCode`，`handleKey` 首行丢弃合成中的按键；顺带修 C-03 的 `defaultPrevented` 判断（同一处）。

### C-05 输入列被 prefix 挤到内容区 24% 处，placeholder 与 `+` 差 249px [P1]
- 现象：placeholder「给 ZeoKeel 发消息，/ 调用指令，@ 文件或对话」与用户输入的每个字都从 **x=561.8** 开始，而左下 `+` 的圆心在 **x=313.1**；两者相差 248.7px，视线要横跳 1/4 屏。原因是权限选择器（118px）+ 模型选择器（132.8px）被塞进 `Sender.prefix`（与 textarea 同一 flex 行、`align-items: flex-end`），把 input 压成 801px 并右移 260.8px。
- 证据：
  - `shots/composer-idle-1440.png`、`shots/composer-seeded-now-approval.png`、`shots/composer-running-1440.png`
  - `logs/c8.json` `IDLE`：`sender {x:288,w:1128,h:96}`、`prefix {x:301,y:785,w:252.8,h:48}`、`ta {x:561.8,w:801.2}`、`placeholderX: 561.8`、`attachCenterX: 313.1`、`send {x:1371,w:32,h:32}`
  - 空 composer 高度 **96px**（`minRows: 2` → textarea 54px + 上下 12px padding + footer 16px），1 行文本时仍是 96px（`logs/c8.json` `GROW` 第 1/2 行均为 96）；6 行封顶 184px。
  - 代码：`web/src/chat/composer.tsx:355-387`（`autoSize={{minRows:2,maxRows:6}}`，`prefix` = `+` + `ComposerPickers`）；`web/src/chat/composer-status.tsx:128`（`<Space size={2} wrap>` 里三个 `size="small"` Select）。
- 规范：antd X `Sender` 的 `prefix` 是"输入框左侧的小前缀"（`es/sender/style/index.js`：`-content{display:flex;gap:paddingXS;align-items:flex-end}`、`-prefix{flex:none}`），承载 253px 的控件群必然挤压输入；DSH `InputBar.module.css` 的做法是卡片 `flex-direction: column; gap:12px`，文本在上、`.row` 工具条在下（注释：「Toolbar: attach + Plan + Read-only on the left; model + send on the right」），ChatGPT 同样是"工具在底部一行、占位文字左对齐"。
  - 另：`composer-status.tsx:12-18` 的注释断言「The DeepSeek client puts the mode, model and thinking chips in the prefix」——与 DSH 源码不符（DSH 三个 chip 全在 `.row` 里）。按错误前提做的取舍，应更正。
- 建议：把 `ComposerPickers` 从 `prefix` 移到 `footer`，与 `+`、用量行、发送键同处底部一行（左：`+` + 权限 + 模型；右：发送）；`prefix` 只留给真正的行内小前缀（或直接不传）。`autoSize.minRows` 降到 1，让空态回到 ~52–56px。

### C-06 审批三动作没有危险/安全的视觉层级，拒绝的 hover 是"主色蓝" [P1]
- 现象：`拒绝`（63.9×32）与 `允许一次`（88×32）默认态同色同边（`#fff` 底、`1px #d9d9d9`、`rgba(0,0,0,.88)` 字），hover 态也**完全相同**：文字与边框一起变主色 `rgb(54,107,255)`。危险动作没有任何红/警示语气，用户只能靠"右黄左白"位置区分。
- 证据：`shots/composer-seeded-hover-deny.png` / `composer-seeded-hover-allow.png`（两张图除了按钮位置外完全一致）；`logs/c7.json` `HOVER` 两行均为 `{"color":"rgb(54, 107, 255)","bg":"rgb(255,255,255)","border":"rgb(54, 107, 255)"}`；`logs/c13.json` `SIZES.cardControls`（拒绝 63.9×32 / 允许 144×32 / 选择器 146×24）。
- 规范：DSH `ApprovalPanel.module.css:171-176`：`.reject:hover { background: var(--dsw-alias-interactive-bg-hover-danger); color: var(--dsw-alias-state-error-primary) }`；动作组合是三个按钮（拒绝 outline / 允许一次 primary / 始终允许 primary），**没有**范围下拉。AGENTS.md §3.5「审批动词不中性化」、WEB-CLIENT.md A9「无默认动作」。
- 建议：`拒绝` 用 antd 6 `color="danger"`（默认态即可带危险语气，或只做 hover 危险底色 + `--zk-danger-text` 字），`允许` 用 `type="primary"` 独占唯一主色填充（一屏 ≤1 个 primary 仍成立，因为发送键在禁用态）；把范围下拉换成第三个按钮 `本会话内始终允许`，彻底避免 C-06b 的重复文案。

### C-06b 范围下拉与允许按钮印出同一句话 [P1]
- 现象：把范围切到"本会话内始终允许"后，下拉框显示「本会话内始终允许」，右边 8px 处的按钮也显示「本会话内始终允许」——同一个字符串并排出现两次（`shots/composer-scope-session.png`；`logs/c13.json` `select {w:146,text:"本会话内始终允许"}` / `allow {w:144,text:"本会话内始终允许"}`）。
- 规范：antd 6 `Select` + 动词按钮的组合，按钮文案应表达"动作"而非重复"范围"；DSH 用两个独立按钮表达两种范围，避免"选择范围→再点一个含义相同的按钮"的二次确认歧义。
- 建议：见 C-06；若保留下拉，允许按钮文案固定为「允许」（范围由下拉表达），或下拉项改为「仅此一次 / 本会话内始终允许」而按钮写「允许」。

### C-07 `Cmd/Ctrl+Enter` 忽略范围选择，永远只允许一次 [P1]
- 现象：卡片提示「Cmd/Ctrl+Enter 允许」，但该快捷键写死 `resolve(id,'allow')`，与下拉里已选的"本会话内始终允许"无关 —— 用户以为快捷键会按他选的范围授权，实际只授权一次（安全上的"少授权"方向，但与提示不符，且用户会以为已长期授权而不再处理同类请求）。
- 证据：`web/src/features/approvals/approvals-context.tsx:103-113`（`run: () => void resolve(oldest.id, 'allow')`，没有读 `scope`）；`approval-card.tsx:115-136`（`scope` 只是卡片本地 state，不进入 context）。
- 规范：DSH「在详情区域按 Enter 始终授予有限范围的许可……持久授权需要显式操作『始终允许』按钮」——即快捷键=单次、且界面要说清这一点；ZeoKeel 的提示词必须与之对齐。
- 建议：文案改成「Esc 拒绝 · Cmd/Ctrl+Enter 允许一次」；或把 `scope` 提进 context 让快捷键跟随（不推荐，容易误授权）。

### C-08 审批卡右侧「来自会话：… · 用户」语义含糊、右对齐吞标题、「现在」屏没有跳转 [P1]
- 现象：卡片第一行最右是灰字 `来自会话：把 README 里的安装步骤补充完整，需要先看现有的内容。 · 用户`。「· 用户」是把 `request.source`（automation/user，即**谁发起的请求**）直接拼在会话名后面，读起来像"会话·用户"这种归属，而不是"由你发起"。该行 `margin-inline-start:auto` 右对齐、`flex-wrap:nowrap`，会话标题一长就吃掉整行右侧（9860 实测宽度 416.7px，标题被 DSH 的 12px 灰字撑满到卡片右边距）。
  - 在 `/`（现在屏）上，卡片的 `otherSession` 判定需要 `currentSessionId`，为空 → **不显示「打开该会话」**。也就是说"审批来自别的会话、你又不在那个会话里"这个最需要跳转的场景，恰恰没有跳转入口；只有在另一个会话里才出现链接（`logs/c3.json` `OTHER-THREAD.link {x:1313,w:86}`）。
- 证据：`shots/composer-now-approval-1440.png`（无链接）、`shots/composer-crosssession-other-thread-1440.png`（有链接 `打开该会话`）；`logs/c1.json` `origin {x:982.3,w:416.7}`、`originText`；`logs/c7.json` `B-BEFORE.openLink: null`（本会话内无链接，正确）。
- 代码：`approval-card.tsx:52-53`（`otherSession = originSessionId && currentSessionId && originSessionId !== currentSessionId`）、`:66-79`（origin 行 + `· 用户`）；`approval-dock.tsx:49`（`onOpenSession` 只按 `sessionId` 存在与否传入）；`global.css:361-376`（`.zk-approval__origin{margin-inline-start:auto;flex-wrap:nowrap}`）。
- 规范：DSH 卡片用"来源对象（图标+名称）"独立成块（`ApprovalCard.tsx` `subject` 区，`.subject{display:flex;gap:12px;font-size:16px}`），不把来源塞进标题右侧；WEB-CLIENT.md 要求审批卡可追溯所属会话。
- 建议：把来源行移到标题下方独立一行（12px，`可换行`），文案例外化：`来自「会话标题」 · 由你发起` / `由自动化任务发起`；`/` 屏也要渲染「打开该会话」（`onOpenSession` 与 `otherSession` 解耦：只要 `originSessionId !== currentSessionId`（含 undefined）就给链接）。

### C-09 「影响对象」既不在卡片正文里，也不在收件箱行里 [P1]
- 现象：web_fetch 的审批卡正文只有一句「这会向外发出网络请求。」——**URL 不在卡片上**，必须先点开「原始参数」才能看到 `{"url":"http://127.0.0.1:9883/health"}`。收件箱行则相反：标题把原始 JSON 硬拼进句子 `待审批{"url":"http://127.0.0.1:9883/health"}`（标签与 JSON 之间无空格、无引号处理），副行只有 `web_fetch`，同样没有会话名、没有影响面句子、没有范围选择、按钮是 24px 的 `size="small"`。
- 证据：`shots/composer-seeded-thread-approval.png`（卡片无 URL）→ `shots/composer-seeded-raw-open.png`（展开后才见 URL）；`shots/composer-inbox-approval.png` / `composer-inbox-metrics.png`；`logs/c7.json` `A-NOW.target: null`、`B-RAW.rawText`；`logs/c13.json` `INBOX {title:"待审批{\"url\":\"…\"}", detail:"web_fetch", btns:[{w:47.9,h:24},{w:72,h:24}]}`。
- 代码：`web/src/api/approvals.ts:56-67`（`approvalTarget` 只认 `command/cmd/input/text` 与 `path`，**不认 `url`**，所以网络类工具永远 `undefined`）；`approval-card.tsx:84-91`（target 为空则不渲染）；`api/inbox.ts:56-65`（`title: approval.summary || approval.tool`，摘要里就是原始参数）；`inbox-screen.tsx:135-146`（`size="small"` 两个按钮，无范围）。
- 规范：DSH 卡片有专门的 `.command` 行（`word-break:break-all`，13px 等宽）直接展示作用对象；GitHub Copilot 的确认弹窗同样把"操作对象（文件/命令）"放在正文而非折叠里。两条进入路径必须给出**同一套**信息与动作（WEB-CLIENT.md：审批面单一真相）。
- 建议：① `approvalTarget` 支持 `url`/`query`/`host` 等键，并对网络类工具渲染「请求地址」行（等宽、可换行）；② 收件箱行标题改为「工具名 · 目标」，原始参数不再进标题；③ 收件箱行提升到与卡片相同的 32px 按钮 + 同样的三种决策（或用同一个 `ApprovalCard` 的紧凑变体）。

### C-10 composer 完全没有可见焦点环，而卡片是 antd 3px 蓝框、`.zk-field` 是 2px 主色 [P1]
- 现象：点进输入框后，`Sender` 的 outline 仍为 `none`、边框仍是 `rgba(0,0,0,0.1)`，没有任何视觉变化；而卡片内 Tab 到按钮时是 antd 的 `rgb(135,175,255) solid 3px`，设置页的 `.zk-field` 是 `rgba(13,68,246,0.8) solid 2px`。同一个应用三套焦点语言，其中最重要的输入框**没有**反馈。
- 证据：`logs/c8.json` `FOCUS {"senderOutline":"rgba(0, 0, 0, 0.88) none 3px","senderBorder":"rgba(0, 0, 0, 0.1)"}`；`logs/c12.json` `TAB-FROM`（skip-link 2px 主色、按钮 3px `rgb(135,175,255)`）；`shots/composer-focus-1440.png`、`shots/composer-tab-order.png`。
- 代码：`chat/composer.tsx:346-358`（`Sender` 没有 `classNames.root`/focus 处理）；antd X `es/sender/style/index.js` 里 `-input{ &:focus-visible { outline:none } }`；`design/global.css:1281-1285` 的 `.zk-field[data-focused='true']` 规则 composer 用不上。
- 规范：WCAG 2.4.7 焦点可见；DSH 主题包 focus ring 2px（`design-platform.css`）；WEB-CLIENT.md §9 视觉规则要求焦点态可见。
- 建议：给 `Sender` 传 `classNames={{ root: 'zk-sender' }}` 并在 `global.css` 加 `.zk-sender:focus-within { box-shadow: 0 0 0 2px var(--zk-focus); border-color: var(--zk-accent) }`，与 `.zk-field` 统一为 2px 主色；同时把所有 antd 默认焦点色替换为该 token。

### C-11 发送/停止按钮没有可读的无障碍名：`arrow-up` / 英文 `Stop loading` [P1]
- 现象：空闲态发送按钮的 accessible name 来自图标的 `aria-label="arrow-up"`（读屏会念"arrow-up"而不是"发送"）；运行中的停止按钮名字是英文 `Stop loading`，且没有任何中文标签。
- 证据：`logs/c5.json` `send.icon: "arrow-up"`；`logs/c13.json` `A11Y-NAMES` 最后一项 `{tag:"BUTTON", ariaLabel:null, text:"Stop loading"}`；`logs/c14-meter.mjs` `send {aria:null, iconAria:"arrow-up"}`。
- 代码：`chat/composer.tsx:346-392`（`Sender` 的按钮由组件内部渲染，没有传 `components`/`actions` 覆盖；`loading` 由 antd X 的 `LoadingButton` 渲染）。
- 规范：WCAG 4.1.2 可访问名；DSH 的发送/停止按钮是自绘的（`InputBar.module.css` `.primary` + `.sendingSpinner`，注释「an arc turns where the arrow was」），名字由调用方给出中文。
- 建议：用 antd X `Sender` 的 `actions`/`components` 覆写 `SendButton`/`LoadingButton`，显式 `aria-label={t('composer.send')}` / `{t('composer.stop')}`；顺带把结束态的图标从"加载转圈"改成方形停止符（现在运行中就是一个浅蓝 spinner，语义是"加载"不是"停止"）。

### C-12 斜杠 / `@` 菜单不锚定输入位置且空结果沉默 [P1]
- 现象：斜杠菜单是**整栏**浮层（x=264, w=1176, h=180），左边缘比输入框左边缘（288）还靠外 24px，而用户输入的 `/` 在 x=561.8 —— 菜单与光标/输入列没有任何视觉连接。`/zzz` 无匹配时菜单**直接消失**，没有任何"无匹配"提示（`thread.noMatch` 文案存在但没被这里使用）；`@` 在空工作区同样毫无反馈。
- 证据：`shots/composer-slash-open.png`（`logs/c8.json` `SLASH.menu{x:264,w:1176}`、`taX:561.8`）、`shots/composer-slash-empty.png`（`SLASH-EMPTY {menu:false, matchesText:null}`）、`shots/composer-mention-open.png`（`MENTION {menu:false}`）；补文件后 `logs/c16` 菜单正常（`rows: ["@README.md", …]`，仍 x=264 w=1176）。
- 代码：`design/global.css:601-613`（`.zk-slash{position:absolute;inset-inline:0;bottom:calc(100%+4px)}` 挂在 `.zk-composer` 上 → 整栏）；`chat/composer.tsx:127`（`menuOpen = slashQuery !== null && matches.length > 0 && …`，无匹配即关闭且无提示）；`:137-140`（mention 同理）。
- 规范：主流做法（ChatGPT 的 `/` 面板、Cursor 的 `@` 列表）是贴住输入区的窄浮层/列表，且空结果给"没有匹配的 X"。antd 6 的 `Select`/`AutoComplete` 也都会渲染"无数据"空态。
- 建议：`.zk-slash` 改为 `inset-inline-start: var(--zk-space-4); width: min(560px, 100% - 32px)`（或直接跟随 textarea 的 x/宽度），并渲染 `无匹配命令` / `无匹配文件` 空态行；mention 提示词也要区分（现在是共用 `composer.slashHint`，导致 `@` 菜单写着"Enter 执行"——Enter 只是插入路径）。

### C-13 控件高度有四套，且全都不接自己的 token（24 / 32 vs 28 / 36 / 40）[P1]
- 现象：同一个 composer 里：`+` 24px、权限选择器 24px、模型选择器 24px、发送键 32px；审批卡里：范围下拉 24px、两个动作按钮 32px；收件箱里：同样的动作按钮 24px。而项目自己的 token 是 `--zk-control-compact-height: 28px` / `--zk-control-height: 36px` / `--zk-page-action-height: 40px`，**一个都没用上**（全部来自 antd 默认 `small=24` / `middle=32`）。同一条"拒绝"动作在卡片里 32、在收件箱里 24。
- 证据：`logs/c13.json` `SIZES {composerControls:[24.2×24, 118×24, 132.8×24, 32×32], cardControls:[146×24, 63.9×32, 144×32], tokenHeights:{control:"36px",compact:"28px",pageAction:"40px"}}`；`logs/c13.json` `INBOX.btns [{w:47.9,h:24},{w:72,h:24}]`。
- 规范：AGENTS.md §3.1「颜色/字号/间距只来自 tokens.css，禁止裸数值」；`design/tokens.css` 定义的四档高度是有意的设计尺度；antd 6 `Button` 的 `size` 只有 `large|middle|small`（无自定义高度档），所以要么用 `styles.root.height` 接 token，要么接受 32/24 并同步 token。
- 建议：统一为两档：次要控件 28px（`--zk-control-compact-height`）、主行动 36px（`--zk-control-height`）；审批动作与发送键用 36px（或都保持 32 但把 token 改成 32，二选一并写进 DESIGN.md）。`拒绝` 的 `拒 绝` 两字间距也应 `autoInsertSpace={false}`（antd 6 的 `content` 语义里有"Chinese character spacing optimization"，导致两字标签被撑成 `拒 绝`，与四字标签「允许一次」不齐）。

### C-14 状态行文案在运行中与两个 Text 粘连、颜色对比度不足 [P2]
- 现象：运行中状态行渲染成 `~11 tok/s1 轮 0 步 · 0 tok · 缓存 0% · 上下文 0%` —— 速率与用量之间**没有分隔**（`~11 tok/s1`）；且运行中 `0 tok / 缓存 0% / 上下文 0%` 全是 0，看起来像"什么都没发生"。完成态是 `1 轮 1 步 · 25 tok · 缓存 0% · 上下文 0%`，14px `rgba(0,0,0,0.45)`（对比度 **3.36:1**，低于 4.5:1）。
- 证据：`shots/composer-running-queued.png`（"~11 tok/s1 轮 0 步"）、`shots/composer-usage-meter.png`；`logs/c9c` `meters: "~12 tok/s1 轮 0 步 · 0 tok · 缓存 0% · 上下文 0%"`；`logs/c14-meter.mjs` `usage {font:14px, color:"rgba(0,0,0,0.45)"}`。
- 代码：`chat/composer-status.tsx:182-199`（`.zk-composer-meters` 里两个 `Typography.Text` 相邻，`global.css` 没有给 `.zk-composer-meters` 定义 `display:flex; gap`）；`i18n/zh.ts:552` 的模板。
- 建议：`.zk-composer-meters{display:flex;gap:var(--zk-space-2);align-items:baseline}`；颜色改用 `--zk-label-tertiary`（6.07:1）；运行中把未知的用量项渲染成 `—` 而不是 `0`。

### C-15 禁用态说明文字对比度 1.82:1，几乎读不出"为什么不能输入" [P2]
- 现象：审批阻塞时唯一的原因说明是 placeholder「先处理上面的审批…」，其颜色是 `rgba(0,0,0,0.25)` 压在 `rgba(0,0,0,0.04)` 的灰底上 → 实测合成后对比度约 **1.82:1**；禁用的发送键 `rgba(13,68,246,0.45)` 上的白箭头约 **2.22:1**（且色相偏紫，与蓝色的"可用"态差别只在透明度）。
- 证据：`logs/c1.json` `ta {color:"rgba(0, 0, 0, 0.25)"}`、`sender {bg:"rgba(0, 0, 0, 0.04)"}`；`shots/composer-seeded-now-approval.png`。
- 规范：WCAG 1.4.3（4.5:1）；DSH 的禁用发送键是"保持满色 + `opacity: .4`"（`InputBar.module.css` `.primary:disabled`），至少不改变色相；更重要的是 DSH 把"为什么禁用"放在单独的 `notice`（12px，`--dsw-alias-label-secondary`）里，而不是靠 placeholder。
- 建议：禁用原因升级为卡片上方的一条 `notice`（`--zk-label-secondary`），placeholder 保持常规色；禁用发送键改为满色 + `opacity:.4`。

### C-16 hint 三态文案不一致（命令键漏项） [P2]
- 现象：空闲态写「Enter 或 Cmd/Ctrl+Enter 发送」，运行态写「Enter 排队发送」但**不提** Cmd/Ctrl+Enter（代码里 `Cmd/Ctrl+Enter` 在运行态依然会入队，`composer.tsx:259-263`），连接态只剩「正在连接模型…」把换行/清空提示全丢了。
- 证据：`logs/c8.json` `hintText`、`logs/c9c` `RUNNING.hint`、`shots/composer-running-1440.png`；代码 `i18n/zh.ts:136-138`。
- 建议：三态并列同一套键位（只换动词）：`Enter 发送 / Enter 排队发送`、`Cmd/Ctrl+Enter 同上`、`Shift+Enter 换行`、`Esc …`；连接态保留后两条。

### C-17 多条待审批只给一个计数，看不到第 2 条是什么 [P2]
- 现象：两条并发审批时，卡片右侧只多出 `还有 1 条待审批`（`shots/composer-two-pending-card.png`），无法查看/切换/定位第二条；必须先拒/允第一条才能看到第二条。收件箱能看到两条但两条**完全一样**（标题都是同一串 JSON），无法分辨属于哪个会话。
- 证据：`logs/c11.json` `{"cards":1,"queueText":"还有 1 条待审批"}`；`shots/composer-inbox-two.png`（两行文本完全相同）。
- 建议：计数做成可点（展开队列摘要：会话名 + 工具 + 目标，逐条跳转）；收件箱行必须带会话名。

### C-18 队列 chip / 面板细节 [P2]
- 现象：运行中排队后，chip「1 条待发送」浮在 composer 右上、与输入框无连接；弹出面板每行是 `#1第二条：…`（序号与文本无空格）；面板无"发送队首"（运行中，符合设计）。
- 证据：`shots/composer-running-queued.png`、`shots/composer-queue-popover.png`；`logs/c9c` `POPOVER rows:["#1第二条：运行中再发一条，应该进入队列"]`。
- 代码：`features/thread/prompt-queue-panel.tsx:42-54`（`<span class="zk-queue__index">#{index+1}</span>` 紧跟 `<span class="zk-queue__text">`）、`global.css:1354-1364`。
- 建议：`.zk-queue__row` 加 `gap: var(--zk-space-2)`；chip 移到 composer footer 内（与用量行同一行左侧），避免两块"悬空"信息。

### C-19 计划 / 提问对话框（本环境无法触发）——代码层问题 [P1]
- 触发条件（供 lead 复现）：`AskDialog` 依赖 `ask` 域（`subscribeAsk`），只有 agent 调用交互工具 `ask_user` 才产生（`internal/modules/agent/plan_mode.go` 的 `interactionTools`，`docs/APPROVAL-SURFACES.md:28`）；`PlanDialog` 依赖 `plan` 域 `state === 'reviewing'`（`features/thread/plan-dialog.tsx:40`），由 `plan_review` / 计划模式产生。确定性假模型只实现 `plain*/tool*/slow*`（`internal/devtools/fakemodel/main.go:120-146`），所以两个对话框在本环境**不可达**，只做代码审查。
- 代码层发现：
  1. **两个"审批面"的风险语言相反**：审批卡坚持"无主色填充"（`approval-card.tsx:126-127` 注释），而 `plan-dialog.tsx:81-83` 的「批准」是 `type="primary"` 实心主色，且顺序为 `取消计划 / 继续规划 / 批准(primary)`；同一产品里"批准执行"有主色、"允许工具"没有。
  2. `plan-dialog.tsx:68-72` `closable={false}` + `mask={{closable:false}}` + Esc 无 claim（刻意），于是 Esc 会**穿透到 composer 的 `clearDraft` claim**（level 10）——模态框后面看不见的草稿被清空，而提示词写着「Esc 不会取消计划」，没提它仍会清草稿。
  3. `ask-dialog.tsx:42-52` 用 `level: 'approval'`，与 `approvals-context.tsx:92` 的 `deny-oldest-approval` **同级**；同级 tie-break 是"最后 push 的赢"（`esc-arbiter.ts:70-73`），而 claim 在 mount 时 push（`keyboard-provider.tsx:74-81`）→ ask（在路由里挂载更晚）永远赢。两者同时存在时 Esc 打谁与用户看哪儿无关。
  4. `ask-dialog` 的选项按钮是 `block` 普通按钮，footer 只有「取消」；无默认焦点、无主按钮，键盘用户按 Enter 不做任何事（可接受，但需要在 hint 里说清，现在 hint 写「Esc = 取消 · 选择选项即可提交 · 「其他…」可自定义回答」）。
  5. 两个对话框都**不显示所属会话/工具**（ask 只有问题文本，plan 只有计划正文），与 C-09 的"影响对象缺席"是同一个毛病。
- 建议：plan 的「批准」改为与审批卡一致的次级/主色策略（二选一并全局统一）；`plan-dialog` 也注册一个 `level:'overlay'` 的 Esc claim 并 `return false`，把 Esc 明确"吃掉"；`ask-dialog` 提到独立级别（如 `ask: 35`）避免与审批抢同一 key；两个对话框都加一行来源（会话名 + 工具）。

## 未能覆盖 / 环境限制

- `plan-dialog` / `ask-dialog` 无法在假模型下触发（见 C-19 的触发条件），未截图。
- 9860 的待审批在其等待期间**自行消失**（我在 20:07 截到卡，20:09 再访问只剩一条"会话…已完成"的 toast；`agent.approvalTimeoutSeconds` 默认 0 且只对 automation 源生效，`internal/modules/approvals/approvals.go:276-282`，所以更像是 run 结束而非超时）。**审批在等待中消失时界面没有任何说明**（`approvals-context.tsx:46` 静默过滤），我把这条并入 C-01 的建议（状态变化需要可解释）。9860 的 390 截图因此不含卡片，390 的卡片证据改用 9873（`composer-approval-390-now.png`）。
- `@` 提及的空态与有候选态分别用空工作区 / 手工放 3 个文件（`data-composer/workspace`）两种条件下的结果。
- 未测移动端真实触摸（仅 390×844 视口）。

## 跨区域共性问题（供 lead 汇总）

1. **antd 默认视觉语言越过了 tokens**：`#d9d9d9`（按钮/选择器边框）、`rgba(0,0,0,.88/.45/.25)`（三级文字）、`rgb(135,175,255)`（焦点环）全部是 antd 默认值，不是 `--zk-*`；对比度因此不受控（3.36:1、1.82:1）。
2. **三套焦点语言**：`.zk-field` 2px 主色 / antd 3px 淡蓝 / composer 无 —— 需要一条全局焦点规则。
3. **四档控件高度都没落地**：token 是 28/36/40，实际是 24/32。
4. **全局单例状态耦合跨区域行为**：`approvals` 数组、`esc` claim、`mod+Enter` shortcut 都是全局的，导致"别的会话的审批"能改本会话的输入框、能吃掉任意页面的 Esc/Enter。
5. **右对齐元信息 + nowrap**：审批卡的来源行、状态行的 rate/usage 都是 typography 的"次要信息"被摆到最右并禁止换行，标题一长就被压。
6. **12px 承担了太多可读信息**（影响面句子、来源、快捷提示），且用 tertiary 灰；建议把"必须读懂才能安全决策"的句子提到 13/14px 并升一档灰度。