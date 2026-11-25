# 设置项接线深审（wire）— 「存进去了，功能到底受不受影响」

- 实例：`http://127.0.0.1:9875`（data-dir `.cache/ui-audit/data-wire`），另加一个**"已重启的对等实例"** `:9899`（data-dir `data-wire2`，用于验证"只在 activation/启动时读一次"的设置）
- 覆盖流程：**28 个设置项**（写入 → 实测 → 刷新/重启 → 再实测）
- 截图：**72 张**（`shots/r2-wire-*.png`）；其中 26 张是本报告的关键证据
- 记录的操作：约 190 次点击/键入 + 34 个会话的完整轮次（含审批允许/拒绝 5 次）
- 模型侧证据：自建脚本模型 `wire-stub.py`（`:9911`，SSE，OpenAI 兼容 + `/search` 端点）；它把每次请求体落盘到 `wire-stub-requests.jsonl`，因此"发给模型的内容是否变"是**直接读数**，不是推断
- 存储侧证据：网关自己的 `data-wire/settings.json`（每行 `key → string`）+ WebSocket 帧 `{"jsonrpc":"2.0","id":10,"method":"settings.set","params":{"key":"…","value":"…"}}`
- 只读源码，未改产品代码；实验脚本全部在 `.cache/ui-audit/wire-*.mjs`

**判定口径**：写入成功但没有任何真实行为变化 = **H（半接线）**；"有消费者但需要重启/只对某类会话生效，而界面没写" 也归 H（用户视角等价于不生效）。OK = 改设置后能观察到行为变化。

---

## 0. 流程总表（26 行）

| 设置项 | 写入是否成功 | 有消费者吗（文件:行） | 实测是否生效 | 结论 |
|---|---|---|---|---|
| `ui.chatFontSize` | ✅ `settings.set` → `"12"/"20"`，store 落盘 | `web/src/shell/app-shell.tsx:44-55`（写 `--zk-chat-font-size`）→ `web/src/design/global.css:566-569` | ✅ `.zk-transcript` 计算值 **12px → 20px**（每次另开新会话测，刷新后仍在） | **OK** |
| `ui.workSteps.mode`（compact/standard/detailed/verbose） | ✅ 四个值依次落盘 | ❌ 只有 `ia.ts:211-219`（写）+ `settings-keys.ts`；无任何读 | ✅ 四档下工具卡 DOM **完全相同**：`h=60px / 文本 29 字 / 展开块 0`（`r2-wire-ws-mode-*.png`） | **H** |
| `ui.workSteps.collapse`（completion/next-input） | ✅ `"completion"/"next-input"` | ❌ 同上，零消费者；展开逻辑写死在 `web/src/chat/thought-steps.tsx:78-88`（running/error 才自动展开） | ✅ 两档工具卡完全相同（`h=60px`） | **H** |
| `ui.shortcut.commandPalette`（改 `Mod+J`） | ✅ 落盘 `"Mod+J"` | ❌ 零消费者；命令面板写死 `mod:true,key:'k'`（`web/src/features/palette/command-palette.tsx:109`） | ✅ **Ctrl+J 无反应**（面板未开）；**Ctrl+K 仍能打开**（`r2-wire-shortcut-ctrlk.png`） | **H** |
| `ui.shortcut.toggleSidebar`（改 `Mod+J`） | ✅ 落盘 `"Mod+J"` | ❌ 零消费者；侧栏写死 `mod:true,key:'b'`（`web/src/shell/app-shell.tsx:76-86`） | ✅ Ctrl+J 侧栏宽度 264 不变；**Ctrl+B 仍生效**（264 → 64，`r2-wire-shortcut-ctrlb.png`） | **H** |
| `ui.theme.mode` | ✅ `"system"/"light"/"dark"` 落盘 | `web/src/design/use-system-appearance.ts:44-58` → `web/src/app.tsx:30,54`（**只喂 antd ConfigProvider**） | ⚠️ `dark` 只把 antd 组件切成深色，**应用自身 surface 不变**：`body` 仍 `rgb(255,255,255)`，而正文颜色变成 `rgba(255,255,255,0.85)` → **白底白字不可读**（`r2-wire-theme-dark-thread.png`、`r2-wire-theme-mode-dark.png`） | **H（严重）** |
| `ui.theme.dark`（legacy 开关「偏好深色」） | ✅ `true` 落盘 | 仅在 mode 为空时计数：`use-system-appearance.ts:51-57`；宿主同规则 `internal/host/theme_mode.go:36-52` | ⚠️ 设置页的「主题」没有"空"选项 → 用户一碰主题，mode 就非空，**该开关永久失效**；实测 `mode=system + dark=true` 仍是浅色；`mode=dark` 与 legacy-only 的可见结果一致 | **H（残留）** |
| `agent.permission` | ✅ `"read-only"/"workspace"/"full"` | `internal/modules/toolkit/toolkit.go:439-476`（→ 沙箱模式，**每次 build 读**）、`internal/modules/approvals/approvals.go:230`、`web/src/chat/composer-status.tsx:80-140`（chip） | ✅ chip 文案跟着变（只读 / 工作区内修改）；read-only 下 `write_file` 走审批卡、`read_file` 照常完成（`r2-wire-perm-readonly-*.png`） | **OK** |
| `agent.mode`（填 `plan`） | ✅ `"plan"` 落盘 | 后端 `internal/modules/agent/plan_mode.go:179-205`（读）；**web 端零消费**（只 `ia.ts:157` 一个裸文本框） | ✅ 工具面 **51 → 20**（模型请求体的 `tools` 直接缩到只读白名单）；❌ 界面上**没有任何"计划模式"指示**，用户不知道自己进了 plan 模式 | **H（UI 半接线）** |
| `chat.mode`（`minimal`/`creator`） | ✅ 落盘 | `internal/modules/agent/plan_mode.go:48-90,179-205` | ✅ 系统提示 8034 → **5312** 字符、工具 51 → **20**（minimal）；→ **3719** 字符、工具 **11**（creator） | **OK**（UI 只有裸文本框，无选项/无说明 → P2） |
| `chat.systemPrompt` | ✅ 落盘 | `internal/modules/agent/agent.go:1464-1475`（每轮解析） | ✅ 请求体 system 消息里出现哨兵 `WIRE-SENTINEL-42`（sysLen 7998 → 8034） | **OK** |
| `chat.baseURL` / `chat.model` | ✅ 落盘 | `agent.go:1432-1500`（`configForModelLocked`，每轮） | ✅ 改到 `:9911` 后下一轮请求直接打到脚本模型（无需刷新/重启） | **OK** |
| `chat.contextBudget` | ✅ `"200"` 落盘 | `internal/modules/agent/context_budget.go:73-98`、`agent.go:1495` | ⚠️ 后端进 rig 的上下文保护/压缩器；**UI 零可见度**：上下文 Tab 与底部 `1 轮 1 步 · 上下文 0%` 都只按模型 window 算，预算改 200 与 200000 界面上看不出差别 | **H（无反馈）** |
| `agent.approvalTimeoutSeconds` | ✅ `"5"` 落盘 | `internal/modules/approvals/approvals.go:277-290`（**仅 `Source=automation` 才起超时**）、`internal/gateway/domains.go:1161` | ✅ 交互会话里挂起审批等 17s 仍然挂着（超时不生效）；❌ 设置页只写「审批超时（秒）」，没说"只对自动化会话"；web 端拿到 `approvalTimeoutSeconds` 后**没有任何组件渲染**（`features/approvals/approvals-context.tsx:49,120` → 无人消费） | **H** |
| `agent.subagentMaxConcurrent` | ✅ | `internal/modules/agent/agent.go:122`（`parseConcurrency`） | 未做端到端（需要真子代理跑并发）→ 有消费者，行为未实测 | OK（代码级） |
| `agent.teamMaxConcurrent` | ✅ | `internal/modules/agent/agent.go:124` | 同上 | OK（代码级） |
| `toolkit.shellTimeoutSeconds` | ✅ `"5"` 落盘 | `internal/modules/toolkit/toolkit.go:133,504-521`（**activation 时读一次**） | ❌ 未重启：`sleep 12` 正常跑完（输出 `SHOULD-NOT-PRINT`）；✅ 重启后同一设置：**5000ms 就把命令转后台**（`[still running after 5000ms; moved to background job …]`） | **H（需重启，界面没说）** |
| `toolkit.shellMaxOutputKiB` | ✅ `"1"` 落盘 | `toolkit.go:135,523-540`（activation 一次） | ❌ 未重启：`seq 1 200000` 输出 32872 B 全量；✅ 重启后：结果 **1125 B**，开头 `[truncated: 240319 earlier bytes discarded]` | **H（需重启）** |
| `toolkit.deniedGlobs` | ✅ `["secret/**"]` 落盘 | `internal/modules/toolkit/denied_globs.go:31-58`（activation 一次） | ❌ 未重启：`read_file secret/pass.txt` 成功；✅ 重启后：`sandbox: denied: path "secret/pass.txt" is hidden by pattern "secret/**"`，对照 `notes.txt` 正常 | **H（需重启）** |
| `sandbox.writableDirs` | ✅ | `toolkit.go:137,545-560`（activation 一次） | 同族（需重启）；未单独做写穿透实验 | OK（代码级，需重启） |
| `net.proxy` / `net.noProxy` | ✅ `http://127.0.0.1:9` 落盘 | `internal/host/serve.go:50`（**只在启动时**）、`internal/host/proxy.go:3-6` 注释明说 "A restart applies changes"；`internal/host/settings.go:105-107` 只有 autostart 有 watcher | ❌ 未重启：模型请求照常直达（session 里看不出任何代理痕迹）；✅ 重启后 `bash: env \| grep -i proxy` 输出 `HTTP_PROXY=http://127.0.0.1:9` 等 4 个变量 | **H（需重启，界面没说）** |
| `ui.language` | ✅ `"en"` 落盘 | `web/src/i18n/index.tsx:70-80` | ⚠️ 字段 label/help/按钮/侧栏（Inbox/Capabilities…）切了；**设置页自己的二级导航（模型/行为与限额/外观/通知/网络与搜索/LSP/归档/数据与隐私/关于）仍是中文**，跳转链接「跳到主内容」也仍是中文（`r2-wire-lang-en-appearance.png`） | **H（部分）** |
| `ui.notifications`（应用内通知） | ✅ | ❌ **零消费者**（全仓只有 `contracts/interaction.go:98` 的常量） | ✅ 关掉后 toast 一切照旧（`web/src/api/gateway.ts:602-625` 无条件渲染） | **H** |
| `ui.notifications.system`（系统通知） | ✅ | 仅托盘壳 `internal/host/notify_sink.go:41` | 浏览器客户端（serve 产品）没有 OS 通知通道 → 开关无效；文案没说"仅托盘壳" | **H** |
| `ui.autostart` / `ui.closeToTray` / `ui.globalHotkey` | ✅ | autostart：`host/serve.go:44-53`（启动 + watcher，serve 下也生效）；closeToTray：`host/tray.go:81-88`；globalHotkey：`host/hotkey.go:60-69` | 仅托盘壳进程消费；设置页对 hotkey 有「托盘壳使用；浏览器端不生效」提示（`ia.ts:225-231`），对 autostart/closeToTray 也写了「（托盘壳）」，**只有 hotkey 之外两项的提示藏在 label 里** | OK（受形态限制，文案基本诚实） |
| `ui.sessions.showArchived` | ✅ | ❌ **零消费者**（`internal/contracts/chat.go:541` 注释自认是"old sidebar view option"） | ✅ 开关后侧栏没有任何归档筛选变化 | **H** |
| `web.search.endpoint` / `web.search.apiKey` | ✅ | `internal/modules/toolkit/toolkit.go:232-250`（每次调用读） | ✅ 批准 `web_search` 后，stub 的 `/search` 收到 `Authorization: Bearer stub-search-key` + `{"query":"wiring probe","count":5}`，工具卡返回 stub 的结果 | **OK** |
| `presets.agents` / `tools.disabled` / `skills.disabled` / `mcp.*` / `lsp.*` | ✅ | `agent/preset.go:54,218`、`toolkit/mcp_disabled.go:41`、`toolkit/skills_filter.go:25`、`toolkit/lsp.go:44-50` | 有真实消费者（本轮未逐个端到端） | OK（代码级） |

**汇总：28 行里 OK 11 行（含 5 行仅代码级确认），H 17 行。** H 分四类：
- **零消费者（6 行）**：`ui.workSteps.mode`、`ui.workSteps.collapse`、`ui.shortcut.commandPalette`、`ui.shortcut.toggleSidebar`、`ui.notifications`、`ui.sessions.showArchived`
- **需重启/只在激活时读，界面没说（4 行 H）**：`net.proxy`(+noProxy)、`toolkit.shellTimeoutSeconds`、`toolkit.shellMaxOutputKiB`、`toolkit.deniedGlobs`、`sandbox.writableDirs`（后两项 OK 但同族需重启）
- **半接线/只接一半（4 行）**：`ui.theme.mode`（只接 antd）、`ui.theme.dark`（残留）、`agent.mode`（后端接、UI 不接）、`ui.language`（字段接、设置导航不接）
- **生效范围与文案不符 / 无反馈（2 行）**：`agent.approvalTimeoutSeconds`（只对自动化会话）、`chat.contextBudget`（UI 零可见度）
- **只对托盘壳生效（1 行）**：`ui.notifications.system`（浏览器客户端没有 OS 通知通道，文案未标"仅托盘壳"）

（6+4+4+2+1 = 17）

---

## 发现

### W-1 `ui.theme.mode=dark` 把正文变成白底白字 [P0]
- 操作：设置 → 外观 → 主题 → 深色 → 保存；打开 `/c/<会话>`。
- 网络：WS `{"method":"settings.set","params":{"key":"ui.theme.mode","value":"dark"}}`，随后 `settings.delta` 回显。
- DOM/截图：`body` background `rgb(255,255,255)`，`.zk-transcript` 文字 `rgba(255,255,255,0.85)`；侧栏 `rgb(0,21,41)` 变暗（antd 生效），主区完全没变。见 `shots/r2-wire-theme-dark-thread.png`、`shots/r2-wire-theme-mode-dark.png`（设置页 label 直接消失）。
- 代码：`web/src/design/use-system-appearance.ts:44-58` 只产出 `'light'|'dark'` 交给 `web/src/app.tsx:54` 的 antd `ConfigProvider`；`web/src/design/global.css` **没有** `[data-theme]`/`prefers-color-scheme` 分支，`web/src/design/tokens.css`（生成物）也只有浅色一套。也就是说：深色只接线到 antd 组件，没接线到应用自身的 surface/token。
- 对照（DSH）：`dsh-client-ui-theme/src/client/*.tsx` 把 appearance 同时写进 `document.documentElement[data-theme]` 与 antd algorithm，token 两套（`design-platform.css` 有 `:root[data-theme=dark]`）。
- 修法：Appearance 变化时 `document.documentElement.dataset.theme = appearance`；`tokens.css` 生成两套（`internal/design` 加 dark 变体）；`global.css` 全部改用变量；加一条 vitest 断言 `.zk-transcript` 的前景/背景对比度 ≥ 4.5。

### W-2 工作步骤 4 档 + 收起时机：两个控件零消费者 [P1]
- 操作：外观页把「工作步骤展示」依次设 compact/standard/detailed/verbose（每档保存 → 新会话 → 让模型调一次 `read_file`）；「收起时机」设 completion/next-input。
- 网络：6 次 `settings.set` 全部成功（`ui.workSteps.mode=compact…verbose`、`ui.workSteps.collapse=completion/next-input`），store 里逐档可见。
- DOM/截图：四档工具卡尺寸/文本完全一致 —— `height=60px`、文本 `读取文件 read_file notes.txt · 完成`（29 字）、展开内容块 0、`expandedRows=0`；收起时机两档同样一致。见 `shots/r2-wire-ws-mode-compact|standard|detailed|verbose.png`、`shots/r2-wire-ws-collapse-*.png`。
- 代码：`web/src/features/settings/ia.ts:211-223` 只写不读；`web/src/chat/thought-steps.tsx:78-88` 的展开规则是硬编码 `items.filter(status==='loading'||'error')`，组件连 `settings` 都没订阅。
- 应对照 DSH：`dsh-client-ui-chat` 的 tool-step 渲染读 settings 的 verbosity（compact 隐藏参数与输出、verbose 默认全展开）。
- 修法：`ThoughtSteps` 接 `subscribeSettings`；`mode` 控制"卡片显示字段集"，`collapse` 控制 turn 结束/下次输入时的 `expandedKeys` 复位。

### W-3 两个快捷键设置是死开关（真实键位写死） [P1]
- 操作：外观页把「命令面板快捷键」「折叠侧栏快捷键」都改成 `Mod+J` → 保存 → 在会话页按 Ctrl+J / Ctrl+K / Ctrl+B。
- 网络：两次 `settings.set` 成功，store 为 `Mod+J`。
- DOM/截图：Ctrl+J（面板开=`false`，侧栏 264 不变）；**Ctrl+K 打开了命令面板**（innerText 出现「命令面板 新建会话…」）；**Ctrl+B 折叠侧栏 264 → 64**。见 `r2-wire-shortcut-ctrlj.png` / `-ctrlk.png` / `-ctrlb.png`。
- 代码：`web/src/features/palette/command-palette.tsx:109`（`mod:true,key:'k'`）、`web/src/shell/app-shell.tsx:76-86`（`mod:true,key:'b'`）；`ui.shortcut.*` 只在 `internal/contracts/shortcuts.go` 与生成清单里出现。
- 修法：要么接入（解析 `Mod+K`/`CmdOrCtrl+Shift+P` 到 `useShortcut`，含冲突检测与回显），要么**从设置页删掉这两行**——现在是"存了个没人看的字符串"。

### W-4 `ui.theme.dark`（偏好深色）对任何碰过主题的用户都已失效 [P1]
- 操作：主题设 `system` + 「偏好深色」打开 → 保存；再在一台**全新数据目录、只有 `ui.theme.dark=true`、没有 mode 行**的实例（`:9899`）上冷启动。
- 网络：`settings.set {ui.theme.dark:true}`、`{ui.theme.mode:system}` 均成功。
- DOM/截图：`mode=system & dark=true` → `body rgb(255,255,255)`（浅色）；legacy-only 实例同样 `rgb(255,255,255)`，与 `mode=dark` 的 body 一致（`r2-wire-restart-legacy-dark.png`、`r2-wire-theme-legacy-dark.png`）。
- 代码：`use-system-appearance.ts:44-58`（legacy 只在 `mode===''` 时读）、`internal/host/theme_mode.go:36-52` 同规则；而「主题」单选组**没有"空/未设置"项**（`ia.ts:201-212`），一旦用户点过主题，mode 永远非空。
- 修法：删掉「偏好深色」；迁移时把遗留 `ui.theme.dark=true` 一次性转成 `ui.theme.mode=dark`。

### W-5 6 个设置「只在启动/激活时读一次」，界面从不提"重启" [P1]
- 涉及：`net.proxy`/`net.noProxy`、`toolkit.shellTimeoutSeconds`、`toolkit.shellMaxOutputKiB`、`toolkit.deniedGlobs`、`sandbox.writableDirs`。
- 操作：先在运行中的 9875 上改这 5 项并实测（全部**无变化**）；再把同样的值写进一台进程启动时就带着它们的数据目录（`:9899`），冷启动后实测。
- 网络/输出证据（重启后）：
  - `bash: env | grep -i proxy` → `HTTP_PROXY=http://127.0.0.1:9`、`HTTPS_PROXY=…`、`http_proxy`、`https_proxy`（`r2-wire-restart-env-proxy.png`）
  - `sleep 12` → `[still running after 5000ms; moved to background job …]`（timeout=5 生效）
  - `seq 1 200000` → 结果 1125 B，`[truncated: 240319 earlier bytes discarded]`
  - `read_file secret/pass.txt` → `sandbox: denied: path "secret/pass.txt" is hidden by pattern "secret/**"`（`r2-wire-restart-denied-glob.png`）
  - 未重启时四项全部"照旧"（`r2-wire-shell-timeout.png`、`r2-wire-shell-output-card.png`、`r2-wire-denied-glob-secret.png`）
- 代码：`toolkit.go:126-137`（同一处注释还写着 "the settings page says a restart applies them"——**界面并没有这句话**，注释与事实不符）、`denied_globs.go:31`、`internal/host/serve.go:50`、`internal/host/settings.go:105-107`（只有 autostart 有 watcher）。
- 修法：给这 5 行加 `helpKey`「修改后需重启网关生效」并在保存后 toast 提示"已保存，重启后生效"；或补 watcher 热应用（toolkit 的 policyCfg 每次 build 重算即可，`sandboxOf` 已经是这个模式）。

### W-6 `agent.approvalTimeoutSeconds` 只对自动化会话生效，界面写成通用 [P1]
- 操作：行为页设「审批超时」= 5 秒 → 在交互会话里触发 `bash`（需审批）→ 等 18 秒（6 秒时卡片已在，18 秒仍在）。
- DOM/截图：`web_fetch`（parks 家族）触发审批卡；设置「审批超时」= 5 秒，6 秒时卡在、**18 秒时卡仍在**，无倒计时、无自动拒绝（`r2-wire-approval-timeout.png`）。
- 代码：`internal/modules/approvals/approvals.go:277-290` 只在 `request.Source == automation` 时把该值变成 `AskTimeout`；注释 `RG-04` 明说交互审批"keep waiting"。web 端把 `approvalTimeoutSeconds` 塞进 `features/approvals/approvals-context.tsx:49,120` 后**没有组件使用**。
- 修法：label 改「自动化审批超时（秒）」+ help「交互会话的审批永不超时」；或让 UI 在自动化来源的审批卡上显示倒计时（当前有值无渲染）。

### W-7 `agent.mode` 是"裸文本框"，plan 模式没有任何界面指示 [P1]
- 操作：行为页「Agent 模式」填 `plan` → 保存 → 新会话里让模型调 `bash`。
- 网络：`settings.set {agent.mode:plan}` 成功；下一轮请求体的 `tools` 从 **51 个变成 20 个**（只剩 `read_file/glob/grep/web_fetch/…`），模型仍尝试 `write_file`，被拒。
- DOM/截图：composer 前缀只显示 `工作区内修改 acceptance-tool`，全页 `计划模式|plan mode` 匹配 **false**；用户只会看到工具"莫名失败"（`r2-wire-agent-mode-plan.png`）。
- 代码：`internal/modules/agent/plan_mode.go:179-205`；`web/src/features/settings/ia.ts:157` 是 `text(...)`，没有选项枚举，也没有 chip。
- 对照 DSH：`dsh-client-ui-plan` 有显式的 plan 徽标 + `exit_plan_mode` 按钮。
- 修法：改成 radio（normal/plan）+ composer 上显示模式徽标；plan 下把 `exit_plan_mode` 的入口提到 chip。

### W-8 `chat.mode` / `chat.contextBudget`：后端都生效，但界面无法确认 [P2]
- 操作：模型页把「对话模式」填 `minimal`/`creator`，把「上下文预算」填 200。
- 网络：脚本模型收到的请求 —— system 8034 → 5312 → 3719 字符，tools 51 → 20 → 11；`chat.contextBudget` 停在配置里。
- DOM/截图：`chat.mode` 与 `chat.systemPrompt` 只有裸文本框（无选项、无当前模式回显）；`chat.contextBudget` 的数值在「上下文」Tab 与底部 `上下文 0%` 上都**读不出来**（`api/context.ts` 只有 `window/used`，没有 budget）。
- 代码：`agent/context_budget.go:73-98`、`agent/plan_mode.go:179`；`web/src/features/thread/context-screen.tsx` 无 budget 字段。
- 修法：`chat.mode` 用 radio（standard/minimal/creator）；上下文 Tab 增加一行「预算 200（显式设置）」与"压缩已触发"N 次。

### W-9 切到英文后，设置页自己的导航仍是中文 [P2]
- 操作：外观页「界面语言」→ 英文 → 保存 → 刷新 → 进 `/settings/appearance`。
- 网络：`settings.set {ui.language:'en'}` + `settings.delta`。
- DOM/截图：字段 label 全英文（`Language / Theme / Prefer dark / Conversation font size…`），主侧栏英文（`Inbox / Capabilities / …`），**但设置二级导航仍是「模型 / 行为与限额 / 外观 / 通知 / 网络与搜索 / LSP / 归档 / 数据与隐私 / 关于」**；跳转链接仍是「跳到主内容」（`r2-wire-lang-en-appearance.png`、`r2-wire-lang-en-thread.png`）。
- 代码：`web/src/features/settings/settings-screen.tsx:88-114` 的 `navItems = useMemo(..., [pages, matches])` **依赖里没有 `t`/`locale`**：首帧按默认语言算完就冻结；`web/src/shell/app-shell.tsx:91` 的 `跳到主内容` 直接写死中文。
- 修法：把 `locale`（或 `t`）加进依赖；跳过链接走 `t('shell.skipToContent')`（键已存在）。

### W-10 通知 4 个开关：一个零消费者、一个只对托盘壳 [P2]
- 操作：通知页逐个开关并保存；触发同样的事件（会话完成 toast）。
- 网络：4 次 `settings.set` 成功。
- DOM/截图：应用内 toast 一切照旧（`docs` 与代码里 `subscribeToasts` 无条件渲染）。
- 代码：`ui.notifications` 全仓只有 `internal/contracts/interaction.go:97-99` 的常量，**没有任何读点**；`ui.notifications.system` 只在 `internal/host/notify_sink.go:41`（托盘壳）被读。
- 修法：`ui.notifications` 接进 `web/src/features/toasts`（关掉就不渲染），或删掉；`ui.notifications.system` 加「仅托盘壳」提示（与 hotkey 行同款）。

### W-11 `ui.sessions.showArchived` 是纯死开关 [P2]
- 操作：归档页打开「在侧栏显示已归档会话」→ 保存 → 回侧栏看。
- DOM/截图：侧栏无任何变化，也没有归档筛选器。
- 代码：只有 `ia.ts:297` 写；`internal/contracts/chat.go:541` 自己写着这是 "old sidebar view option"，没有读点。
- 修法：删掉，或真的实现侧栏归档分组。

### W-12 「其它设置（网关有、界面未接线）」是好事，但位置误导 [P2]
- 模型页底部渲染 `session.*.unread` 这类界面未接线的行（`settings-screen.tsx` 的 extra rows），语气诚实；但它们被放在**模型页**而不是一个「未接线」专页，用户会以为它们属于模型配置。建议单独成页或折叠在"高级/原始行"。

---

## 与 DSH 的差距（该功能应该长什么样）

| 能力 | ZeoKeel 现状 | DSH |
|---|---|---|
| 主题 | 只接 antd，应用 surface 不变 → 深色白底白字 | `dsh-client-ui-theme` 同时写 `data-theme` 与 token 双套，README 明确"两处必须一致" |
| 快捷键 | 设置项存在但键位写死 | 快捷键来自 settings，改键即时生效并显示冲突 |
| 工作步骤详略 | 4 档 + 收起时机全无消费 | verbosity 决定每步展示字段，collapse 决定结束态展开 |
| 需重启的设置 | 静默保存，无提示 | 明确"restart required"提示（DSH 的 plugin/setting 变更走 apply/restart 提示） |
| 计划模式 | 裸文本框 + 无指示 | plan 徽标 + exit 按钮 + 审批面差异 |

---

## 建议直接从 UI 移除（12 → 精简）

移除（零消费者或永久失效，留着只会让用户以为有效）：

1. `ui.workSteps.mode` — 4 档全无消费
2. `ui.workSteps.collapse` — 同上
3. `ui.shortcut.commandPalette` — 键位写死 Mod+K
4. `ui.shortcut.toggleSidebar` — 键位写死 Mod+B
5. `ui.theme.dark`（「偏好深色」） — 只在 mode 为空时有效，而 UI 无法让 mode 变空
6. `ui.notifications`（「应用内通知」） — 全仓零消费者
7. `ui.sessions.showArchived`（「在侧栏显示已归档会话」） — 零消费者

保留但必须改文案/控件（不是删）：

8. `agent.mode` → radio + 模式徽标
9. `chat.mode` → radio（standard/minimal/creator）
10. `agent.approvalTimeoutSeconds` → 改称「自动化审批超时」
11. `toolkit.shellTimeoutSeconds` / `toolkit.shellMaxOutputKiB` / `toolkit.deniedGlobs` / `net.proxy`（+`noProxy`）/ `sandbox.writableDirs` → 加「重启后生效」
12. `ui.notifications.system` / `ui.closeToTray` / `ui.autostart` → 统一标「仅托盘壳」
13. `ui.theme.mode` → 先修 W-1 再考虑是否保留 `system` 选项

---

## 实验脚本与原始数据（可复跑）

| 文件 | 用途 |
|---|---|
| `wire-stub.py` | 脚本模型 + `/search` 端点（`:9911`），请求体落盘 `wire-stub-requests.jsonl`、`wire-stub-search.jsonl` |
| `wire-lib.mjs` | Playwright 帮助函数（登录/改字段/保存/等待 store/新会话/发消息/审批） |
| `wire-e2-appearance.mjs` → `wire-e2.json` | 字号/工作步骤 4 档/收起时机/主题 legacy |
| `wire-e3a-permission.mjs` | agent.permission（read-only vs workspace） |
| `wire-e5-keys-lang.mjs` | 快捷键 + ui.language |
| `wire-e6-sysprompt.mjs` → `wire-e6.json` | chat.systemPrompt / chat.mode（system 8034→5312→3719，tools 51→20→11） |
| `wire-e7-net-limits.mjs` / `wire-e8-tools.mjs` | search 端点、未重启的限额实验 |
| `wire-e10-restart.mjs` → `wire-e10.json` | **重启后**：proxy env / timeout / 输出上限 / deniedGlobs |
| `wire-e11-theme.mjs` / `wire-e12-dark.mjs` / `wire-e13-darkchat.mjs` | 主题（含暗色可读性） |

**限制**：
- 9875 的网关进程运行在另一个 pid 命名空间，本会话无法停它，因此"重启"用**同配置的一份数据目录冷启动到 `:9899`** 来验证（等价于用户重启网关）。
- `agent.subagentMaxConcurrent` / `teamMaxConcurrent` 只做了代码级确认，没有跑真实并发子代理。
- 代理只验证到"进程环境变量已生效"，没有走真实上游域名（沙箱里 Go 的 `ProxyFromEnvironment` 对 127.0.0.1 永远直连，无法用本地回环模拟被代理的出站请求）。