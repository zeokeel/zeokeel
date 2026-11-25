# 输入框（composer）功能可用性深审

- 实例：`http://127.0.0.1:9873`，会话 `sess-1795524825566673035` / `sess-1795524830628257589` / 新建 `sess-1795525734005566211`
- 覆盖流程：**18 个**；截图：**43 张**（`shots/r2-input-*.png`）；记录的操作：**≈190 次**（16 个 Playwright 脚本，键盘 60+ / 指针 40+ / WS·HTTP 帧 90+）
- 代码基线：`/data/zeokeel` master @ `cd29938`
- 结论一句话：**输入框"长得很像"、主要输入路径能用（Enter/Cmd+Enter 发送、Shift+Enter 换行、运行中排队、slash 菜单、附件上传都能工作），但有 4 个 P0：Stop 是假的（不取消后端）、队列消息在 409 后丢失、一条待审批会锁死所有会话的输入框、390 窄屏下输入区宽度为 0 无法输入。**

---

## A. DSH 输入框的解剖图（源码 + 具体数字）

来源（只读）：
`…/@deepseek-ai/dsh-client-ui-conversation/src/client/skeleton/InputBar.tsx`（604 行）、`InputBar.module.css`（472 行）、`…/input/editor/keymap.ts`（300 行）、`…/input/submission-policy.ts`、`ConversationRoot.module.css`、`dsh-client-ui-theme/src/styles/{base,gradient-shadow-text,focus}.css`、`dsh-client-ui-input-trigger/src/client/MenuView.module.css`、`dsh-client-ui-attachment/src/client/ComposerAttachments.module.css`、`dsh-client-ui-conversation/src/client/queue/QueueDock.module.css`、`…/dsh-client-ui-chat/README.zh.md`、`…/dsh-client-ui-conversation/README.zh.md`。

### A.1 结构与垂直顺序（卡片内从上到下）

```
.root           padding: 0 16px 4px            ← 侧净空 16px(嵌入式 8)，底部 4px
 ├ .notice      (可选) 4px 8px / 12px-18px / 6px 下边距
 ├ .card        max-width ≤ 952px  radius 28  bg input-major  shadow soft
 │   ├ gap: 12px（图注：文本区↔按钮行 12px），padding-top: 8px
 │   ├ .overlayAnchor   height: 0, absolute inset 0 0 auto   ← 菜单/选择器的定位锚（=卡片宽）
 │   ├ .accessory       padding: 10px 12px 0, gap 8          ← 附件/引用等 accessory 槽
 │   ├ slot:conversation.input.attachments  padding 2px 10px 0；缩略图 64×64 radius 20
 │   ├ .scroll    max-height: 336px (=14 行 × 24px)，margin-right 4px  ← 唯一滚动盒
 │   │   └ .grow > .input (contenteditable)
 │   │        min-height 36px(docked) / 52px(hero)
 │   │        padding 4px 8px 0 14px；font 14px / line-height 24px
 │   │      .placeholder  absolute inset 4px 8px auto 14px，单行省略(hero 两行)
 │   └ .row      工具栏  padding 2px 8px 6px, gap 12px(容器<560 → 8px)
 │        ├ 左 .tools(gap 12)  = [+] 28×28 圆  +  .modes(gap 12) = 权限 chip 28 + 计划 chip 28
 │        └ 右 .trailing(margin-left auto, gap 12) = 自定义右槽 + 模型 chip 28 + 活动区 + 发送/停止 34×34
 └ .dock        统计行 padding-top 4px, gap 12px, min-height 22px（先占位，读数到达不跳高）
```

### A.2 尺寸表（DSH）

| 元素 | 数值 | 出处 |
|---|---|---|
| 卡片宽上限 | `clamp(680px, 64%列宽, 920px) + 32px` → **≤ 952px** | `ConversationRoot.module.css:472` |
| 卡片圆角 | **28px**（`--dsw-radius-panel`） | `base.css:23` |
| 卡片边框 | `border: 0` + 内阴影描边 **0.5px** `border-l2`(rgba(0,0,0,.1)) | `InputBar.module.css:68-69` |
| 卡片阴影 | `0 4px 16px rgba(0,0,0,.03), 0 0 24px rgba(0,0,0,.03)` | `gradient-shadow-text.css:38` |
| 卡片底色 | `--dsw-specific-input-major`（浅色 `neutral-bluish-00`，深色 850） | `design-platform.css:314` |
| 卡片内 padding | 上 8 / 左右 0（由 `.input` 的 14/8 提供）/ 下 0；块间距 **12** | `:64,:61` |
| 文本字号/行高 | **14px / 24px**（粗指针设备 ≥16px） | `:77-78,:88-92` |
| 文本区最小高 | docked **36px**（1 行+4px），hero **52px**（2 行） | `:186-191,:257` |
| 文本区最大高 | **336px = 14 行**，超出内滚 | `:146` + `ConversationRoot:441` |
| 文本区内边 | `4px 8px 0 14px` | `:191` |
| 占位符 | 绝对定位 `inset 4px 8px auto 14px`，caption 色，单行省略 | `:235-244` |
| 工具栏行 | `padding 2px 8px 6px`，gap **12**（<560px → 8） | `:273-289,:327` |
| 附件键 `+` | **28×28** 圆形，`--dsw-specific-selector` 底 | `:336-348` |
| 权限/计划/模型 chip | 高 **28px**，圆角 8，字 **13/20 500**，右侧 12px 折角；模型 max-width **360px** | `PermissionSelect.module.css:7-16`、`PlanModeControl.module.css:13-22`、`ModelSelection:19-29` |
| 发送键 | **34×34** 圆，`button-info-fill`(#3964FE/#679EFE)，白图标，`translateY(-2px)` | `:398-417` |
| 发送禁用态 | `opacity: .4`；发送中 → 圆弧 spinner（不褪色，`cursor: progress`） | `:430-444` |
| 停止态 | 同位置换成方形（`rect rx=3`），`aria-label=停止` | `InputBar.tsx:546-560` |
| 统计行 | min-height **22px**（20px 读数 + 1px padding） | `:27-29` |
| 菜单（`/`、`@`、`+`） | `bottom: calc(100% + 4px)`、`left/right: 0`（**贴卡片宽**）、padding 4、max-height **400px**（运行时按上方空间钳制）、行 min-height **34px**、圆角 12、13/20；小节标题 11px | `MenuView.module.css:1-13,49-67` |
| 队列坞 | 卡片上方，`width = 卡宽 − 2×8`，上圆角 16；标题行高 **36**；行 min-height **36**；列表 max-height **180** 内滚 | `QueueDock.module.css:7-33,62-70,114-130` |
| 附件缩略图 | **64×64**，圆角 20，0.5px 描边；删除钮 18 圆 | `ComposerAttachments:11-21,91-95` |
| 聚焦 | `.input{outline:none}`；全局 `:focus-visible` 环 **2px** 业务蓝（可编辑控件点击不画环）；卡片 `tabIndex=-1`，点卡片任意空白 → `preventDefault` 后把光标还给编辑器 | `focus.css:3-20`、`InputBar.tsx:332-344` |
| 打字光标 | `caret-color: business-primary` | `:201` |

### A.3 键盘语义（`keymap.ts`，CRITICAL 优先级）

| 手势 | 行为 | 行号 |
|---|---|---|
| Enter | 提交；模式由「繁忙时 Enter」偏好决定（queue/steer）；`event.repeat` 直接吞掉（长按不机关枪） | `:258-281` |
| Shift+Enter | 保留原生换行（**组合输入期间也换行**） | `:264` |
| Ctrl/Cmd+Enter | **互补模式**（偏好是 queue 就 steer，反之亦然） | `:279` + `submission-policy.ts:35-40` |
| Alt / AltGraph / Ctrl+Cmd / Shift+Cmd-Enter | 一律 `return true` 但不提交，把 DOM 事件让给应用快捷键 | `:261-262` |
| IME | `isComposing \|\| keyCode===229 \|\| compositionend 后 10ms 窗口` → 既不提交也不换行 | `:9-15,52-56,186-197,265-269` |
| Esc | 分层：先关浮层（菜单/选择器）→ 命令 chip 需删除才能退出；**Esc-Esc（默认 500ms）** 才停止本轮 | `:239-248`、`conversation/README.zh.md:145` |
| Tab / Shift+Tab | Tab 接受高亮补全（可下钻项下钻）且不吃焦点；Shift+Tab 等同 Esc 离开菜单 | `:234-238` |
| ↑/↓ | 菜单内移动高亮；组合输入期间不拦截 | `:228-229` |
| Space | 命令「认领」裁决（`/goal` 之类插入 chip） | `:249-257` |
| 粘贴 | 文件→入库（图片走上传）；文本→清洗后插入；纯文件时 `preventDefault` | `:282-298` |
| 拖放 | `registerFileIntake({card, occurrence, accepts, add})`：文档级粘贴/拖放**按焦点+主次选举**唯一输入框接收 | `InputBar.tsx:316-322` |
| 发送后 | Enter 提交 → 进入 `sending`（编辑器与附件栏只读、按钮 spinner）→ Host 接收后才清草稿；被拒则草稿与附件**原样保留** | `conversation/README.zh.md:133` |
| 运行中 | 主键位随草稿切换：空/被阻塞时同一位置是 **Stop**，有草稿时是 **Send**（标签按 Enter 解析出的模式写「排队发送/插话发送」） | `InputBar.tsx:366-407` |
| 队列 | 卡片上方 QueueDock：行内 Edit / Remove / Steer，空草稿还有 steer-all 组合键；**abort 不清队列** | `:139` + `QueueDock` |
| 附件限制 | 只对**图片**做前端预检（张数/单张/总量），整批拒绝并立即提示；通用文件无客户端上限，选中即上传；发送会等全部上传就绪 | `:241-275,:99-104` |
| 无权限 | 缺 `session/prompt` → **不渲染发送键**（而非禁用）；缺 `session/cancel` → 不渲染停止；缺 `fileUploads/upload` → 不收文件 | `:61-66,562` |
| 无 Session | 同一棵 DOM 变 inert，整张卡片当 Workspace 选择器（虚线内描边） | `:169-175,:97-119` |

**DSH 输入框的 6 个关键设计点**（详见末节）：① 单卡片三行分带（文本 / 工具栏 / 统计）② 14 行 336px 自增高+内滚 ③ 左键位固定、右键位随草稿变 Send/Stop ④ IME 三重护栏 + repeat 护栏 ⑤ 菜单贴卡片宽、下缘 4px ⑥ 失败不清草稿、队列独立于 abort。

---

## B. ZeoKeel 输入框逐项实测（数字 + 截图）

实现：`web/src/chat/composer.tsx`（406 行，antd X `Sender`）+ `web/src/chat/composer-status.tsx`（prefix=选择器、footer=统计/插话）+ `web/src/design/global.css`（`.zk-composer` L166-175、`.zk-slash` L597-613、`.zk-queue-chip` L1334-1338）。

### B.1 结构与视觉（1440×900，deviceScaleFactor 2）

| 元素 | 矩形（CSS px） | 样式 |
|---|---|---|
| `.zk-composer` | x=**264** y=754 w=**1176** h=**146**（空）/168（1-3 行）/190/256（6 行） | `padding: 12px 24px 16px`；无底色/边框/圆角/阴影 |
| `.ant-sender` | x=**288** y=766 w=**1128** h=96/118 | `radius 12px`、`border 1px solid transparent`、`background rgba(0,0,0,0.04)`、antd 三层阴影 |
| `.ant-sender-prefix` | x=**301** y=763 w=**210–252.8** h=**48** | 两行：`+`(24×24) + 权限/模型两个 `ant-select`(24 高) |
| `textarea` | x=**505.8–567.3** y=757 w=**795.7–857.2** h=**54** → 142（6 行封顶） | `padding 5px 0`、**14px/22px**、`min-height 54`、`max-height 142` |
| 发送/停止 | **32×32** 圆 @ (1371, 779)（有草稿）/ (1371, 801)（空） | `ant-btn-circle ant-btn-primary`；`aria-label` **null**，图标 aria-label `arrow-up`/`Stop` |
| footer（统计/插话） | x=289 y=823 w=1126 h=**16**（空）/ **38** | 「N 轮 N 步 · tok · 缓存 · 上下文」22px |
| `.zk-composer__hint` | x=**288** y=**866** w=1128 h=**18** | **在卡片外**、12px/18px、常驻一行 |
| slash 菜单 | x=**264** y=519.6 w=**1176** h=**208.4**；6 行，行高 **28.4** | `position:absolute; inset-inline:0; bottom:calc(100%+4px)`；radius 12；`max-height 280` |
| 队列 chip | x=264 y=704 w=**1176** h=**28** | 整行纯文本按钮「3 条待发送」，popover 278×102 |
| 附件 Tag | x=288（贴卡片左边缘）h=**24** | 类名 `.zk-composer__attachments` / `.zk-attach__thumb` **没有任何 CSS 定义** |

**视觉断裂（实测）**：文本区左边缘 x=561.8，`+` 在 x=301（`+` 字形 309）→ **260.8px 空白带**；占位符也被推到同一列（screenshot `r2-input-idle-1440.png` 里「给 ZeoKeel 发消息…」从 x≈761 开始）。6 行时文本块顶部 y=669，而 `+` 在 763、模型 chip 在 787 → 左侧控件组**垂直落在文本块的中下部**，与首行基线完全不对齐（`r2-input-lines-6.png`）。390 窄屏：文本区 **width=0、x=431.3**（超出 390 视口 41px），卡片只剩 126px 宽（`r2-input-390.png`）。

六状态截图：`r2-input-idle-1440.png` / `r2-input-lines-3.png` / `r2-input-lines-6.png` / `r2-input-attachment-big.png` / `r2-input-running.png` / `r2-input-approval-new.png`。

**文本列漂移**：`textarea.x` 实测 5 个值 —— 505.8（只读+tool 审批态）、511.3（权限=只读）、519（空草稿）、561.8（默认）、567.3（模型名更长）→ **同一条消息的不同状态间，正文左边缘漂 61.5px**，因为 prefix 宽度 = 「+」行与「选择器」行的自然宽度（210→252.8）。

### B.2 行为实测

| # | 操作 | 结果 | 判定 |
|---|---|---|---|
| 1 | 1/3/6/9 行输入 | 高 54→76→142→**142**（`scrollHeight 208` 内滚）；DSH 是 14 行/336px | 6 行封顶（P2） |
| 2 | Enter（空闲） | `POST /api/chat/run` 200 ✔ | ✔ |
| 3 | Shift+Enter | 插入 `\n`、**无请求** ✔ | ✔ |
| 4 | Ctrl+Enter | `POST /api/chat/run` ✔ | ✔ |
| 5 | Enter（运行中） | `WS prompts.enqueue` + `prompts.delta added` ✔ 队列工作 | ✔ |
| 6 | IME Enter（空闲，草稿"你好"） | `defaultPrevented=false`、不发送 ✔（antd X Sender 自带 `isCompositionRef`） | ✔ |
| 7 | **IME Enter（运行中，草稿"中文候选"）** | **`prompts.enqueue {"prompt":"中文候选"}`** → 候选词被当消息入队 | **P1 / S** |
| 8 | Esc（空闲有草稿） | 清空草稿 ✔ | ✔ |
| 9 | **Esc（运行中 + 草稿）** | 第一次：**直接停轮**（phase→idle），草稿保留；第二次才清草稿 | **P1 / H** |
| 10 | 空白草稿 Enter | 不发（`submit` 早退）✔ | ✔ |
| 11 | 点发送键（空草稿） | 按钮 **enabled**，点击**无任何请求**、无提示 | **P2 / F** |
| 12 | `/` 菜单 | 6 条（new/stop/files/inbox/settings/clear），↑↓ 高亮、Enter 执行、Esc 关且**保留草稿** ✔ | ✔（无「无结果」态） |
| 13 | `@` 菜单 | 触发 `GET /api/files/tree` 200，但工作区为空 → 无列表；`@nope` 静默无反馈 | 半可用 |
| 14 | 附件（`+`→选择器） | `FILECHOOSER-opened` → `POST /api/blobs?name=dot.png` 200 → Tag ✔ | ✔ |
| 15 | 附件（大图 400×300） | Tag 变 579×308、**图片按原尺寸渲染**、composer 高 476px | **P1 / S** |
| 16 | 附件（.exe） | `POST /api/blobs?name=evil.exe` **200 接受**（无类型/大小前端校验） | P2 |
| 17 | **粘贴图片** | `paste` 事件 `defaultPrevented=false`，**无 Tag、无 /api/blobs** | **P0/P1 / M** |
| 18 | **拖入文件** | `drop` 后无 Tag、无请求、无报错 | **P1 / M** |
| 19 | 带附件发送 | `POST /api/chat/run {attachments:["450089…"]}` 200 ✔，但气泡里只有文字 `shot.png带附件的消息`（`imgCount 0`） | P2 / S |
| 20 | 队列 chip | 「3 条待发送」整行 1176×28；popover 列出 #1/#2/#3 + ×；运行中无「发送队首」✔ | ✔ |
| 21 | **点「发送队首」** | `prompts.remove` → `POST /api/chat/run` → **409** → 队列项已销毁、无回滚 | **P0 / S** |
| 22 | 权限选择器 | 三档（只读/工作区内修改/完全访问）；切换 → `WS settings.set {agent.permission:"read-only"}` + `approvals.delta resync`；**刷新后保持** ✔ | ✔（真生效） |
| 23 | **模型选择器** | `GET /api/models` → `{"models":[],"defaultModel":"acceptance-tool"}` → 下拉「**暂无数据**」（且重复渲染两次「暂无数据」）；chip 却显示当前模型名 | **P1 / F** |
| 24 | 审批在屏 | 输入框 `disabled`，placeholder「先处理上面的审批…」，发送键变暗方形；提示行仍写「Enter 排队发送 · Esc 停止本轮」 | 见 F-3 |
| 25 | **跨会话审批** | 会话 S 有审批 → 打开空闲会话 A：A 的输入框同样 `disabled` + 「先处理上面的审批…」（卡片多一个「打开该会话」） | **P0 / U** |
| 26 | 草稿持久化 | 输入文字 → 刷新 → `""`；附件也丢 | **P1 / M** |
| 27 | 打字区焦点 | 载入后 `activeElement=BODY`（无 autofocus）；`aria-label="消息输入框"` ✔；`:focus-visible` 为真但 `outline:none`、卡片无任何聚焦反馈 | P2 |
| 28 | Tab 进输入框 | 首次 Tab 直接落在 textarea ✔ | ✔ |
| 29 | 390 窄屏 | composer 126×300、textarea **w=0**、点击 30s 超时、无法输入、提示撑成 5 行、统计撑成 4 行 | **P0 / U** |

---

## 流程总表

| 流程 | 结论 | 类型 | 证据 |
|---|---|---|---|
| 空闲 Enter 发送 | 可用 | — | `POST /api/chat/run` 200（i5） |
| Shift+Enter 换行 | 可用 | — | `value="第一行\n第二行"`，无请求（i5） |
| Cmd/Ctrl+Enter 发送 | 可用 | — | `POST /api/chat/run` 200（i5/i7） |
| 多行自增高 | 6 行封顶（142px） | — | `r2-input-lines-9.png`，`max-height:142px`（i2） |
| 运行中 Enter 排队 | 可用 | — | `WS prompts.enqueue` + `prompts.delta`（i7b） |
| 队列在轮次自然结束后自动续跑 | 可用（略有延迟） | — | 轮次结束后 `msgCount 32→34`、新 run 起来（i17） |
| 运行中再发（停止后是否续） | 停止后队列**不自动续跑**（符合设计），但队列项在 409 时被销毁 | S | i10/i11 |
| IME 合成中 Enter（空闲） | 不发送 | — | `defaultPrevented=false`（i11） |
| IME 合成中 Enter（运行中） | 把候选词入队 | **S** | `prompts.enqueue {"prompt":"中文候选"}`（i11） |
| Esc 清空草稿 | 可用（空闲） | — | `AFTER-ESC-1 value:""`（i14） |
| Esc 停止本轮 | 单次 Esc 即停，且**后端没停** | **S/H** | `STOP-NET` 空、`session.list.running:true`（i16） |
| 停止按停止键 | 同上：只断前端流 | **S/H** | i16 |
| 停止后再发 | 409「Fetch failed with status 409详情…」 | **S** | i16 |
| `/` 菜单 | 可用（↑↓/Enter/Esc/Tab） | — | i2 |
| `@` 菜单 | 依赖文件树；无结果无提示 | 半接线 | `GET /api/files/tree` 200（i2） |
| 附件添加（选择器） | 可用 | — | `POST /api/blobs` 200（i8） |
| 附件删除 | 可用（Tag ×） | — | i8 |
| 附件随消息发送 | 发出去了，气泡不显示图片 | H/S | `attachments:["450089…"]`（i10） |
| 粘贴图片/文件 | 无反应 | **M** | `paste` 未 preventDefault、无 /api/blobs（i8） |
| 拖入文件 | 无反应 | **M** | `drop` 后 `DROP-NET []`（i8） |
| 队列 chip / popover | 可用 | — | i10 |
| 「发送队首」 | 409 后消息丢失 | **S** | `prompts.remove` → 409（i11） |
| 权限选择器切换 | 生效且持久 | — | `settings.set` → reload 仍是「只读」（i9） |
| 模型选择器 | 空下拉（假入口） | **F** | `{"models":[]}` → 「暂无数据」（i8） |
| 待审批 → 输入框 | 同会话禁用 | 设计如此 | i12 |
| 待审批 → **其它会话输入框** | 一起禁用 | **U** | i12 |
| 空草稿点发送 | 无反应 | **F** | `EMPTY-SEND-NET []`（i9） |
| 草稿跨刷新 | 丢失 | **M** | `DRAFT-AFTER-RELOAD {"value":""}`（i10） |
| 390 窄屏输入 | 不可输入 | **U** | `ta.w=0`、click timeout（i10） |
| 焦点/读屏名 | 有 aria-label，无聚焦反馈，发送键无名字 | P2 | i2/i9 |

---

## 发现

### F-1 Stop 是"前端假装"：只断本地流，后端继续跑，随后所有发送 409 [P0]
- **操作**：模型切 `acceptance-slow`（8 秒流）→ 输入「慢跑一条用于测停止」→ Enter → 运行中点 **停止键**（也试过 Esc）。
- **网络**：`STOP-NET` **完全为空**（没有任何 `run.cancel` 帧、没有 HTTP）；停止后 `session.list.running` 仍为 **true**。
- **关键证据（后端继续生成）**：停止后每 2s 采样 `session.snapshot` 里最后一条 assistant 文本长度：**47 → 107 → 196 字符**（`running:true` 全程），最终自然跑完停在 **359 字符**。也就是说 Stop 之后模型仍然在跑、token 仍在写入，只是前端不再显示。
- **DOM/截图**：UI 立刻 `phase=idle`、停止键变回 `arrow-up`（`r2-input-stop-after.png`）——**界面说停了，服务端没停**。
- **代码**：`web/src/features/thread/thread-screen.tsx:147-151` `stopRun()` 只调 `chat.abort()` → `@ant-design/x-sdk` 的流 abort；`web/src/api/gateway.ts:669` 的 `cancelRun()`（`run.cancel`）**全仓 0 个调用点**（`grep -rn cancelRun src` 只命中定义）。网关 capabilities 里明明有 `run.cancel`。
- **对照**：DSH 的 Stop 与 `Esc Esc` 走同一 `session/cancel` 作用域取消（`conversation/README.zh.md:145`：「快捷键与 Stop 按钮调用同一作用域取消操作」），并且 Stop 是否可渲染由 `session/cancel` 访问答案决定（`InputBar.tsx:64`）。ZeoKeel 自己的 `make acceptance-slow` 也写明"断连 ≠ 停止"。
- **后果链**：幽灵轮次 → 下一条消息 `POST /api/chat/run` → **409** → 队列项已被销毁（F-2）→ 用户看到 `Fetch failed with status 409详情Fetch failed with status 409`。
- **修法**：`stopRun()` 里 `void cancelRun(sessionId)`（先 cancel 再 abort 流，或 cancel 成功再断）；`chat.abort()` 只作为兜底。用 `run.cancel` 的 `result` 决定是否把 UI 切到 idle（否则保持"停止中"），并给超时。

### F-2 队列消息在 409 后静默消失（remove-then-send 无回滚）[P0]
- **操作**：运行中入队 3 条 → 停止 → 点队列 chip → 点「发送队首」。
- **网络**：`WS> prompts.remove {"id":"prompt-…-5"}` → `WS< prompts.delta removed:[…]` → `REQ POST /api/chat/run` → **RES 409** → `prompts.snapshot` 里该项已不存在。
- **DOM**：操作后 `queue: null`（chip 消失），草稿不恢复，无任何错误提示；同一次点击还顺带 `prompts.remove` 了另一条（id `…-4`）。
- **代码**：`thread-screen.tsx:313-320`
  ```ts
  const sendQueueHead = async () => {
    const head = queue.items[0];
    if (!head || chat.isRequesting) return;
    await queue.remove(head.id);          // 先删
    chat.onRequest({ sessionId, text: head.text }); // 再发，无 try/catch
  };
  ```
  收尾 effect（`:222-240`）是同一个"先 remove 再 onRequest"的形状，同样没有回滚。
- **对照**：DSH 的 QueueDock 行有独立的 Edit/Remove/Steer，投递由 Host 的投递窗口裁决，被拒时**保留队列行**（`conversation/README.zh.md:137`「被拒绝或失败的提交不会公开回显，草稿留在 composer 里并显示错误提示」）。
- **修法**：`await chat.onRequest(...)` 走完再 remove（或失败时 `queue.enqueue(text)` 回滚 + toast）；`onRequest` 需要返回可 await 的 Promise。

### F-3 一条待审批会锁死**所有会话**的输入框 [P0]
- **操作**：在新建会话 S（`sess-1795525734005566211`）用 tool 模型发「用 web_fetch 检查一下 health」→ 出现审批 → 切到空闲会话 A（`sess-1795524825566673035`）。
- **DOM**：A 的 `textarea.disabled=true`、placeholder「先处理上面的审批…」、`phase=idle`（A 根本没在跑），审批卡片带「打开该会话」按钮（`r2-input-approval-other-session.png`；同会话变体只有「拒绝/允许一次」，见 `r2-input-approval-new.png`）。测量：卡片 x=288 w=1128 h=169（同会话）/171（跨会话）。
- **代码**：`thread-screen.tsx:484` `disabled={approvals.length > 0}` + `:485` placeholder 覆盖 —— 判据是**全局** `useApprovals()` 列表，不是"本会话的审批"。
- **对照**：DSH 的审批是**该 Session 的 composer seat 接管**（`ApprovalPanel.module.css:5` 用 `--dsh-composer-side-clearance` 对齐自己那张卡），别的会话的输入框照常可写；跨会话只在收件箱/角标提示。
- **附带问题**：审批在屏时提示行仍写「Enter 排队发送 · Esc 停止本轮」（`r2-input-approval-new.png` 底部），而实际 Enter 无反应（`ENTER-WHILE-APPROVAL-NET []`）、Esc 是"拒绝"。
- **修法**：`disabled` 判据改为 `approvals.some(a => a.sessionId === sessionId)`；跨会话审批用角标/收件箱，不动输入框；审批期间提示行切「审批等待中：Esc 拒绝 · Cmd/Ctrl+Enter 允许」。

### F-4 390 窄屏下输入框不可用（文本区宽 0）[P0]
- **操作**：viewport 390×844 打开会话，点输入区输入。
- **DOM**：`.zk-composer` x=200 **w=126** h=300；`.ant-sender` w=110；`.ant-sender-prefix` w=202.3（**比卡片还宽**）；`textarea` x=431.3 **w=0**、`clientWidth 0 / scrollWidth 14`；`.zk-composer__hint` 110×**90（5 行）**；footer 108×**104**；`page.click('textarea')` **30s 超时**、输入后 `value=""`、`activeElement=BODY`。侧栏 `position:static`、w=200、`data-collapsed=false` 未折叠。
- **截图**：`r2-input-390.png`、`r2-input-390-typed.png`（只剩 `+`、「只读 ⌄」、「accep…」、4 行统计、5 行提示）。
- **代码**：`global.css:1249-1259` 窄屏只改了 `padding-inline` 和 `--ant-sender-padding`，没有处理「prefix 是横向列」的结构（prefix 内容最小宽 202px 撑住 flex 行），`.zk-composer` 也没有 `min-width:0`/换行策略。
- **对照**：DSH 的工具栏行 `flex-wrap: wrap` + `container-type: inline-size` + `@container (max-width:560px)` 收窄间距，卡片宽 `min(calc(100% - 16px), 952px)`，侧净空降 8px；文本区永远独占整行，永远不会被左侧控件挤成 0。
- **修法**：见"重排方案"（把选择器从 prefix 移到工具栏行），并给 `.zk-composer` 与 `.ant-sender` 加 `min-width: 0`。

### F-5 运行中按 Enter 选中文候选词 → 候选被当消息入队 [P1]
- **操作**：`acceptance-slow` 运行中，草稿「中文候选」，派发 `compositionstart` + `keydown Enter{isComposing:true,keyCode:229}`。
- **网络**：`WS> prompts.enqueue {"sessionId":"…","prompt":"中文候选"}` + `prompts.delta added`。
- **DOM**：`defaultPrevented=true`（ZeoKeel 自己吞了键），草稿仍在，队列多一条 `1 条待发送`，`r2-input-ime-running.png`。
- **代码**：`composer.tsx:268-272`
  ```ts
  if (event.key === 'Enter' && running && !event.shiftKey && !event.altKey) {
    event.preventDefault(); submit(); return false;
  }
  ```
  没有 `isComposing` 判断；同样的 `event.key === 'Enter' && (metaKey||ctrlKey)`（`:259-263`）也没有。空闲路径之所以安全，是因为 antd X `Sender` 自己有一层 `isCompositionRef`（`node_modules/@ant-design/x/es/sender/components/TextArea.js:97`）——运行中这条分支**先于**它执行并 `return false`，把它短路了。
- **对照**：DSH `keymap.ts:52-56` 三重判定 + `compositionend` 后 10ms 窗口（`:186-197`），且组合期间 Shift+Enter 仍换行、Tab/↑↓ 不被拦截。
- **修法**：`handleKeyDown` 开头统一 `if ((event.nativeEvent as any).isComposing || event.keyCode === 229) return;`（并保留 antd 那层）。

### F-6 模型选择器是空下拉（假入口）[P1]
- **操作**：点第二个 picker。
- **网络**：`GET /api/models` → `{"models":[],"defaultModel":"acceptance-tool"}`（两次）。
- **DOM**：`ant-select-item-option` **0 个**，空态文案「暂无数据」并**渲染两次**（`r2-input-model-open.png`）；但 chip 上显示 `acceptance-slow`，看起来"能选"。
- **代码**：`composer-status.tsx:150` `options={models.map(...)}`，`models` 来自空目录；`:143-152` 没给"无可选模型"的说明或跳转设置页的入口。
- **对照**：DSH 的模型 chip 是 `role/aria-expanded` + 分组列表（含「更多模型」入口），无模型时提示去设置（`dsh-client-ui-model-selection` + `settings-models`）。
- **修法**：空列表时渲染「尚未配置模型 → 打开设置」的入口（可点击跳 `/settings`），或隐藏 chip 只留文字。

### F-7 附件缩略图完全没有样式：400×300 的图按原尺寸铺开 [P1]
- **操作**：`setInputFiles` 一个 400×300 PNG。
- **DOM**：`.zk-attach__thumb` **naturalWidth 400 / 渲染 400×300**；Tag 由 24 高变 **579×308**；`.zk-composer` 由 146 变 **476** 高（y=424）；两个类名 `.zk-composer__attachments`、`.zk-attach__thumb` 在 `web/src/**/*.css` 里**零定义**（`grep` 只命中 `composer.tsx:325/337`）。
- **截图**：`r2-input-attachment-big.png`（大蓝块压住整个输入区，文件名挤在右下角）。
- **对照**：DSH 64×64 缩略图、圆角 20、0.5px 描边、`padding 2px 10px 0`、删除钮 18 圆（`ComposerAttachments.module.css:11-21,91-95`）。
- **修法**：补 `global.css`：`.zk-attach__thumb{width:48px;height:48px;object-fit:cover;border-radius:var(--zk-radius-sm)}`、`.zk-composer__attachments{display:flex;flex-wrap:wrap;gap:var(--zk-space-2);padding:var(--zk-space-2) 0 0}`。

### F-8 粘贴图片/文件没有任何入口 [P1]
- **操作**：在 textarea 派发带 `Files` 的 `ClipboardEvent('paste')`（模拟截图后 Ctrl+V）。
- **结果**：`defaultPrevented=false`、**无 Tag、无 `POST /api/blobs`**（`PASTE-NET` 空）。
- **代码**：`composer.tsx:346-392` 未给 `Sender` 传 `onPaste`/`onPasteFile`，也没有自定义 paste 监听；antd X 支持 `onPasteFile`（`Sender.d.ts`）。
- **对照**：DSH `keymap.ts:282-298` 粘贴先 `clipboardFiles()` 吸文件、再取 `text/plain`；文档级粘贴还有 `registerFileIntake` 选举（`InputBar.tsx:316`）。
- **修法**：接 `Sender` 的 `onPasteFile={(files)=>onAttachFiles(files)}`。

### F-9 拖入文件无任何反应 [P1]
- **操作**：向 `.zk-composer` 派发 `dragenter/dragover/drop`（含 1 个文件）。
- **结果**：`DROP-NET []`、无 Tag、**无提示、无 dragover 高亮**（连"不支持"都没说）。
- **代码**：composer 无 `onDrop/onDragOver`；也没有 DSH 的 `registerFileIntake` 对应物。
- **对照**：DSH `InputBar.tsx:277,316-322`：`canAcceptDrop` 决定是否接收，整卡是投放区，文档级拖放按焦点选举唯一接收者。
- **修法**：在 `.zk-composer` 上加 drop 区（dragover `preventDefault` + 视觉高亮 + drop → `onAttachFiles`）。

### F-10 草稿不跨刷新保存 [P1]
- **操作**：输入「草稿要不要活过刷新」→ F5 刷新。
- **结果**：`{"value":"","tags":[]}`（`r2-input-draft-reload.png`）。附件同理（i9 `AFTER-RELOAD tags:[]`）。
- **代码**：`composer.tsx` 的 `value` 由 `thread-screen.tsx` 的 `useState` 持有，没有落到 session 存储；`GET` 侧也没有读回。
- **对照**：DSH 草稿与引用 chip 按 Session 持久化（`conversation/README.zh.md:123`「草稿文字和引用语义字段由逐 Session Conversation store 持久化」），刷新/切会话回来仍在。
- **修法**：草稿写进 per-session 存储（现有的 settings/会话快照都可挂），或先做 `sessionStorage` 级兜底。

### F-11 单次 Esc 直接中止本轮，且运行中无法用 Esc 清草稿 [P1]
- **操作**：运行中（草稿「还没写完的草稿」）按一次 Esc。
- **结果**：`AFTER-ESC-1 phase=idle`（轮次被停）、**草稿还在**；再按一次才清空（`AFTER-ESC-2 value:""`）。也就是运行中"Esc 清空草稿"这条提示永远不可达。
- **代码**：`keyboard/esc-arbiter.ts:23-31` 层级 `stopRun:20 > clearDraft:10`；`thread-screen.tsx:152-160` 的 `stop-run` claim 无条件 `active: chat.isRequesting`。
- **对照**：DSH 要 **Esc-Esc（默认 500ms 内两次）** 才停轮（`conversation/README.zh.md:145`），单次 Esc 只做"关浮层/清草稿"，误触代价低；且它的 Stop 提示是 `Esc Esc` 两键的 tooltip（`InputBar.tsx:547`）。
- **修法**：`stop-run` 改成"两击序列"（记住上次 Esc 时间戳，500ms 内第二次才 stop）；提示行同步写 `Esc Esc 停止`。

### F-12 空草稿时发送键可点但什么都不发生；发送键没有可访问名 [P2]
- **操作**：空草稿点 `.ant-sender-actions-btn`。
- **结果**：`EMPTY-SEND-NET []`（无请求、无提示）；按钮 `disabled=false`；`aria-label` 为 **null**，内部图标 `aria-label="arrow-up"`/`"Stop"`（读屏会念 "arrow-up"）；无 tooltip。
- **代码**：`composer.tsx:205-208` `submit()` 里 `if (value.trim()==='') return;`；`Sender` 的 `onSendDisabled` 只对 ctx 生效（`Sender.js:184`），实际按钮没被禁用。
- **对照**：DSH 空草稿时发送键 `opacity:.4` + 真正 disabled，`aria-label={t('input.send')}`，并带 `Tooltip` + 快捷键提示（`InputBar.tsx:563-572`）。
- **修法**：`disabled={value.trim()==='' && attachments.length===0 && !running}`，`aria-label="发送"`，加 tooltip。

### F-13 停止后重发：409 以英文 SDK 原文冒出来（且重复两遍）[P2]
- **操作**：F-1 之后马上再发一条「停止后再发一条」。
- **网络**：`POST /api/chat/run` → **409**。
- **DOM**：`zk-msg-error` 文案 `Fetch failed with status 409详情Fetch failed with status 409`（`r2-input-send-after-stop.png`），`messageCount` 不增。
- **代码**：错误直出 SDK message（`api/` 层未按 409→"本轮仍在运行"映射）。
- **对照**：DSH 的 `session/writer-held` 这类冲突有专门文案（`InputBar.tsx:134-137` → `t('error.sessionInUse')`）。
- **修法**：409 映射成「本轮仍在运行，请稍候或先停止」并带重试；顺带去掉重复拼接。

### F-14 文本列随状态漂移 61.5px（视觉断裂的结构性原因）[P2]
- 实测 `textarea.x` = 505.8 / 511.3 / 519 / 561.8 / 567.3（对应"审批禁用/权限=只读/空草稿/默认/模型名更长"），`prefix.w` = 202.3 / 210 / 252.8。
- 原因：`composer.tsx:357-387` 把 `+` 和两个 `ant-select` 一起塞进 `Sender` 的 `prefix`，prefix 是**横排 flex 的一项**，其宽度由「选择器标签文字」自然宽决定，于是文本列被推着左右移动。
- 对照：DSH 的 `+` 与 chip 在**卡片底部的工具栏行**（`.row`），文本区独占整宽，任何 chip 文字变化都不影响文本列（`InputBar.module.css:186-202` vs `:273-324`）。
- 修法：见"重排方案"。

### F-15 其它（P2 汇总）
- 只 6 行上限（`autoSize maxRows 6`，142px）vs DSH 14 行/336px：长 prompt 只能内滚 6 行视野。
- 提示行常驻卡片外，同一行承载三种互斥语义（「Enter 发送 / Esc 清空草稿 / Esc 停止本轮」按状态切换），审批时还写错。
- 附件无前端类型/大小校验（`.exe` 也 `POST /api/blobs` 200）。
- 无 autofocus（DSH 桌面端 mount 即聚焦编辑器）。
- `+` 缺 `aria-haspopup="listbox"` / `aria-expanded`（DSH `InputBar.tsx:511-512` 有）。
- slash 菜单只在**行首**且无「无结果」提示；DSH 的 trigger 是编辑器内的 inline 触发，并有空结果态。
- 队列 chip 是 1176×28 的整行纯文本按钮（点击热区大得离谱、视觉上不像 chip），DSH 是卡片上方的独立坞面板（宽 = 卡宽 −16、上圆角 16、标题行 36）。

---

## 对照表：维度 / DSH（数字）/ ZeoKeel（数字）/ 差距

| # | 维度 | DSH | ZeoKeel | 差距 |
|---|---|---|---|---|
| 1 | 卡片宽 | `clamp(680,64%列宽,920)+32` → ≤**952px** | `.ant-sender` **1128px**（视口 1440，`1176−48`） | 上限大 176px，无 920 上限语义，长行可读性差 |
| 2 | 卡片内边距 | 上 8 / 左右 0（文本 14/8）/ 下 0；块间 12 | `12px 24px 16px` 在外层，卡片内无统一步进 | 双层 padding，视觉上"卡中卡" |
| 3 | 圆角 | **28px** | **12px** | 差 16px；卡片显得像输入域不像"面板" |
| 4 | 边框/阴影 | 0.5px 内描边 `border-l2` + 极轻双层阴影 | `1px solid transparent` + antd 三层阴影 | 无描边，边界靠底色区分 |
| 5 | 底色 | `--dsw-specific-input-major`（浅色纯白） | `rgba(0,0,0,0.04)` 灰底 | 反了：DSH 输入域比页面亮，ZeoKeel 更暗 |
| 6 | 垂直顺序 | accessory → 附件 → 文本 → **工具栏行** → (dock 统计) | 附件 → **prefix(选择器) 与文本同行** → 文本 → footer 统计 → hint | 关键结构差异：选择器跑到文本左边 |
| 7 | 文本区左边缘 | 卡片内 14px（x = 卡宽起点 +14） | **x=561.8**，距卡片左 273.8px，`+` 在 301 | 260.8px 空白带，占位符同列被推右 |
| 8 | 文本字号/行高 | 14 / **24** | 14 / **22** | 行高差 2px，6 行 = 12px |
| 9 | 自增高上限 | **336px = 14 行** | **142px = 6 行** | 少 8 行；长 prompt 视野只有 43% |
| 10 | 最小高 | 36px（1 行）/ hero 52px | **54px**（minRows 2） | 空态多占 18px |
| 11 | 工具栏位置 | 卡片**底部**整行（左 `+`/权限/计划，右 模型/发送） | `+` + 两个选择器在**文本左侧竖排两行**；统计在底部 | 结构错位，导致 7/14 号缺陷 |
| 12 | `+` 附件键 | 28×28 圆，`selector` 底 | 24.2×24 文本按钮（无底色） | 小 4px、无形状，不像可点控件 |
| 13 | 模型/权限 chip | 28 高、13/20 **500**、右侧 12px 折角、圆角 8 | 24 高、13/20、ant-select borderless | 高 4px；无"chip"轮廓 |
| 14 | 发送键 | **34×34** 圆、info 蓝填充、白图标、`aria-label=发送`、tooltip+快捷键、空草稿 disabled(opacity .4) | **32×32** 圆、primary 蓝、`aria-label` **null**、无 tooltip、空草稿**可点但无效** | 小 2px；可访问名缺失；空态语义假 |
| 15 | 运行中键位 | 同一位置按草稿切换 Send/Stop；Send 标签写「排队发送/插话发送」 | 切换 Send/Stop ✔，但无标签、无 tooltip | 行为对，告知缺 |
| 16 | 停止真伪 | `session/cancel` 真取消（Stop 与 Esc-Esc 同一动作） | 只 abort 本地流，**后端仍 running**；`run.cancel` 无调用点 | **功能性缺失（P0）** |
| 17 | 停止手势 | Esc **两次**（500ms） | Esc **一次**即停，且抢占"清草稿" | 误触代价高（P1） |
| 18 | IME Enter | `isComposing`/229/10ms 三重护栏，运行中也不提交 | 空闲安全（antd 兜住）；**运行中会入队** | 组合输入漏判（P1） |
| 19 | Enter 模式 | 繁忙时按偏好 queue/steer，Mod+Enter 用**互补**模式 | 运行中 Enter 入队、Mod+Enter 也入队（无互补、无偏好设置） | 少一个"插话"档 |
| 20 | 菜单锚定 | `bottom: calc(100%+4px)` + `left/right:0`（**= 卡片宽**），padding 4，行 34 | `inset-inline:0` 相对 `.zk-composer`（**含 24px padding → 1176 宽**），行 **28.4** | 比卡片宽 48px、行矮 5.6px、贴边更远 |
| 21 | 菜单空结果 | 有 section/空态处理，运行中按上方空间钳高 | 无结果=菜单直接消失（`/zzzz`、`@nope` 都静默） | 无反馈 |
| 22 | 附件缩略图 | 64×64、圆角 20、0.5px 边、删除 18 圆 | Tag 24 高 + **图片原尺寸**（400×300 撑到 579×308） | 无样式，严重视觉破坏（P1） |
| 23 | 粘贴/拖放 | 图片入库、文本清洗插入、文件夹成 `@` chip | **都没有**（paste 未 preventDefault、drop 无监听） | 缺两条最常用的入口（M） |
| 24 | 草稿持久化 | 按 Session 存草稿+引用，刷新仍在 | 刷新即丢（文字与附件都丢） | M |
| 25 | 队列 | 卡片上方坞面板（宽=卡宽−16、上圆角 16、标题 36、行 36、列表 ≤180），行内 Edit/Remove/Steer | 整行 28px 纯文本 chip + popover（278×102）+ ×；无 Edit、无 Steer | 能力少一半，热区夸张 |
| 26 | 队列与停止 | abort 不清队列，投递由 Host 裁决，被拒保留 | abort 不清队列 ✔，但「发送队首」409 后**丢消息** | 数据丢失（P0） |
| 27 | 审批与输入框 | 审批接管**本会话**的 composer seat，其他会话输入框照常 | 任何一条审批 → **所有会话** `disabled` + placeholder 覆盖 | 跨会话锁死（P0/U） |
| 28 | 窄屏 | `min(calc(100%−16px),952)`、工具栏 `flex-wrap`、`@container <560` 收间距、侧净空 8 | 390 下卡片 126px、textarea **w=0**、无法输入 | 不可用（P0/U） |
| 29 | 聚焦 | 文本区 `outline:none` 但点卡片任意处回焦、caret 业务蓝；控件 `:focus-visible` 2px 环 | 无聚焦反馈、无 caret 取色；控件靠 antd 自带环 | 部分差（P2） |
| 30 | 读屏名 | 文本区 `aria-label`＝placeholder 文案；role=textbox + aria-multiline | `aria-label=消息输入框` ✔（textarea 原生 role）；发送键无名 | 发送键缺陷（P2） |
| 31 | 发送后失败 | Host 拒绝 → 草稿与附件**原样保留** + toast | 草稿先清空再发；409 时草稿已丢（仅气泡可「编辑」找回） | 失败恢复差 |
| 32 | 附件校验 | 图片张数/单张/总量前端预检，整批拒绝并提示 | 无校验（`.exe` 也上传成功） | 后端兜底未知 |
| 33 | 空/无障碍态 | 无 Session → 整卡当 Workspace 选择器（虚线描边、`aria-haspopup=menu`） | 无此态 | — |

---

## 重排方案（文本在上、工具条在下的一行布局）

目标：把 33 行差距里的 **7/11/14/28**（结构类，一次改完一起消失）作为第一批，其余按需。

```
.zk-composer                       ← 只留横向 padding（tokens），不要再 12/24/16 双层
└ .ant-sender (card)             radius 16→28? 最低 16；bg surface；0.5px 描边；无灰底
   ├ 附件行（flex-wrap, gap 8, 缩略图 48×48 object-fit:cover, radius 8）
   ├ 文本区（独占整行，min-height 44=1 行, max-height 336=14 行, 14/24, padding 12/16）
   └ 工具栏行（一行 flex, space-between, gap 8, padding 8, flex-wrap:wrap）
        ├ 左：+ 28×28 圆 · 权限 chip 28 · 计划 chip 28
        └ 右：模型 chip 28 · 统计(次要, 12/18) · 发送/停止 34×34 圆
   └ 提示行：不再常驻；挪进 placeholder 或按状态单行 12px（仅剩一行真实所需）
```

要改的文件与要点：

1. **`web/src/chat/composer.tsx`**
   - `prefix` 只留 `+`（`<AttachButton/>`）；把 `prefix` 里的 `ComposerPickers` 挪到 `footer` 的第一行（或直接用 `footer` 承载工具栏）。
   - `autoSize={{ minRows: 1, maxRows: 14 }}`（配合 CSS `max-height:336px`）。
   - 发送键：`disabled` 正确化 + `aria-label="发送"` + tooltip；运行中 `aria-label="停止"`。
   - `handleKeyDown` 首行加 IME 护栏（`isComposing || keyCode===229`）。
   - 传 `onPasteFile`；容器加 `onDrop/onDragOver` → `onAttachFiles`。
   - 提示行改为按状态一行（或在审批/运行态隐藏），不再拼三条互斥语义。
2. **`web/src/chat/composer-status.tsx`**
   - `ComposerPickers` 拆成「权限 chip / 模型 chip」两个受控小组件，模型空列表时渲染「去设置配置模型」入口。
3. **`web/src/design/global.css`**
   - `.zk-composer`：去 `12px 24px 16px`，改 tokens 化的 `padding: var(--zk-space-3) var(--zk-space-6) var(--zk-space-4)` 只负责外边距；加 `min-width: 0`。
   - `.zk-composer .ant-sender`：`--ant-sender-padding`、`min-width:0`、`flex-wrap` 策略，容器查询 `@container (max-width:560px)` 收间距。
   - **新增**（当前完全缺失）：`.zk-composer__attachments`、`.zk-attach__thumb` 的样式。
   - `.zk-slash`：`inset-inline: 0` → 改到卡片宽（用一个内层定位容器），行高 28.4 → 34，补空结果态。
   - `.zk-queue-chip`：整行文本 → 卡片上方 chip（右对齐、自宽、hover 底）。
4. **`web/src/features/thread/thread-screen.tsx`**
   - `stopRun()`：`void cancelRun(sessionId)`（`api/gateway.ts:669` 已有，接入即可）。
   - `disabled`：`approvals.some(a => a.sessionId === sessionId)`。
   - `sendQueueHead` / 收尾 effect：先 `await` 发送成功再 `queue.remove`，失败回滚 + toast。
   - `stop-run` 的 Esc claim：改两击序列（500ms）。
5. **`web/src/features/approvals/*`**：跨会话审批改成「另一会话有审批」的提示条（不动输入框），点击「打开该会话」再跳。
6. **草稿持久化**：`useState` → per-session 存储（写 `sessionStorage` 兜底，或挂到现有 settings/快照通道）。

---

## 与 DSH 的差距（该功能应该长什么样）

一句话：**DSH 的输入框是"一张卡片、三段分带、键位固定"；ZeoKeel 现在把两组选择器塞进了文本左侧，于是文本列漂移、窄屏塌成 0 宽，并在"停止/队列/审批"三条链路上把状态真相当成了本地 UI 状态。**

**DSH 输入框的 6 个关键设计点：**

1. **一张卡片、三段式分带**：附件（顶）/ 文本（中，独占整宽）/ 工具栏（底，左 `+`+权限+计划、右 模型+发送）。文本列的位置与任何控件文字宽度无关——这是防"文本列漂移"的结构性前提。
2. **14 行 / 336px 的自增高 + 唯一滚动盒**：文本区 `min-height 36`（hero 52），`max-height 336`，只有 `.scroll` 滚，卡片内 `gap 12`、统计行提前占位 22px，所以在 1~14 行之间**按钮位置固定、卡片高度可预测**。
3. **同一位置的键随草稿变 Send/Stop**：空草稿或被阻塞时该位置是 Stop；有草稿时变 Send，标签直接写「排队发送 / 插话发送」；`Mod+Enter` 用**互补**投递模式。停止是**真的取消**（`session/cancel`，与 Esc-Esc 同一动作）。
4. **三重 IME 护栏 + repeat 护栏**：`isComposing || keyCode 229 || compositionend 后 10ms`，组合期间 Enter 不提交、Shift+Enter 仍换行、Tab/↑↓ 不被拦截；长按 Enter 不机关枪。
5. **浮层贴卡片**：`/`、`@`、`+` 的菜单 `bottom: calc(100%+4px)`、`left/right: 0`，宽度**等于卡片**（不含侧净空），padding 4、行 34、按上方空间钳高；队列是卡片上方独立坞（宽 = 卡宽 −16、上圆角 16）。
6. **失败不吞状态**：发送进入 `sending` 只读态，Host 接收后才清草稿与附件，被拒则**原样保留** + 定位文案（`session/writer-held`→"会话被占用"）；队列投递由 Host 的投递窗口裁决，被拒保留队列行。附属两条：图片前端预检（张数/单张/总量，整批拒绝）、粘贴/拖放都进同一条入库管道。