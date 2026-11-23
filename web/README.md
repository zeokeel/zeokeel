<!-- @format -->

# web/ — ZeoKeel 浏览器客户端

网关（Go，`internal/gateway`）的消费者。协议见 [`docs/TRANSPORT.md`](../docs/TRANSPORT.md)，
客户端契约见 [`docs/WEB-CLIENT.md`](../docs/WEB-CLIENT.md)，组件映射见
[`docs/plans/antd-x-component-map.md`](../docs/plans/antd-x-component-map.md)。

交付状态：**S2 骨架 + S3 功能对齐（mock 驱动）**。审批 / Inbox / 工具卡 / 设置 / 检查器 /
分层 Esc 已落地；唯一未接线的是 WS 快照/差值（隔离在 `src/api/`，等 task-3）。

## 启动（不需要 Go）

```bash
cd web
npm ci

# 终端 A：mock 网关（SSE + 审批闸门 + 全部读模型）
npm run mock            # http://127.0.0.1:8788

# 终端 B：dev server（/api 代理到 mock）
npm run dev             # http://127.0.0.1:5178
```

对着真实网关跑：`ZK_API_URL=http://127.0.0.1:9932 npm run dev`。

### mock 与真网关的差异（只有这一条）

mock 的 WebSocket 接受 `?token=`（供脚本驱动控制面：浏览器无法给握手加请求头）。
**真网关不接受**——`internal/gateway/auth.go` 只认 cookie 与 `Authorization: Bearer`。
这是**测试夹具特权**，`src/api/token-query.test.ts` 断言产品代码里不出现 query token。

### mock 可触发的路径

| 操作 | 观察 |
| --- | --- |
| 随便发一句 | 推理 → 工具调用**停在审批** → 允许后继续 → 流式正文（代码高亮）→ 用量 → 完成 |
| 停在审批时按 `Esc`（焦点在任何地方，含侧栏/顶栏） | 拒绝最旧那条；本轮继续跑完（工具结果是失败行） |
| 停在审批时按 `Cmd/Ctrl+Enter` | 允许最旧那条；裸 `Enter` 永远不做决定 |
| 审批卡「范围」选「本会话内始终允许」 | 主按钮变为该动作（`allow_always`） |
| 点「拒绝」/`Esc` 后 | 卡片消失，下一条（若有）出现 |
| 文字含 `error` | 终止性 `error` 事件 → 红色错误行（不是 warning） |
| 点停止 / 运行中按 `Esc` | 连接中断，保留已收到内容 + 中性「已停止」 |
| `/inbox` | 5 类打断源（审批/提问/计划/产物/完成回执）；审批可直接处置；空态无 0 值统计 |
| `/c/:id` 工具卡 | 26 个渲染器按工具名分发；未注册工具（MCP）走通用卡（输入+输出） |
| `/c/:id/files` | 工作区树 + 预览 + 「改动」逐文件 diff（`?view=diff`） |
| `/settings/*` | 9 页目录；改一项后 `Esc` 弹丢弃守卫且页面不关；页脚唯一 primary |
| 输入 `/` | 命令菜单（↑↓ + Enter 执行；Tab 只补全；Esc 关闭且保留草稿） |
| 输入 `@` | 文件候选（Enter/Tab 补全路径；Esc 关闭） |
| `Mod+K` / `Mod+Shift+P` | 命令面板（打开即可输入；Esc 关闭且下次清空） |
| `Mod+F` | 会话内查找条（Enter 下一个 / Shift+Enter 上一个；Esc 关闭并把焦点还给 composer） |
| `Mod+Alt+F` | 打开右侧文件检查器 |
| 刷新 `/c/:id` | 快照恢复（含推理/工具步骤/用量） |
| 切走再切回会话 | 回到该会话上次的阅读位置；原来贴底则继续跟随，原来上滚则显示「回到最新」 |
| 运行中再发一条 | 进入网关队列（WS `prompts:*` 域，「N 条待发送」，可单条取消）；本轮 `done` 后自动发队首；`Esc` 停止时队列保留 |

## 命令

```bash
npm run dev        # vite dev server
npm run typecheck  # tsc --noEmit
npm run lint       # eslint（含"禁止颜色字面量"规则）
npm run test       # vitest run（E1–E9、SSE 集成、520 条虚拟化、整树冒烟）
npm run build      # tsc + vite build → dist/（由 go:embed 打进二进制）
npm run gen:tokens # 从 internal/{brand,design} 重新生成 tokens.css 与 antd-theme.ts
npm run gen:icons  # 从 internal/icons/svg 重新生成 src/design/icons.tsx（34 个）

# 端到端（真浏览器，headless chromium）
npm run mock       # 一个终端：mock 网关（127.0.0.1:8788，token 固定 dev-token）
npm run dev        # 另一个终端：vite（127.0.0.1:5178，代理 /api /auth /ws）
npm run e2e        # 默认打 mock：新建会话 → 发一轮 → 审批 → 完成 → 刷新恢复

# 打真网关时两个变量是必须的：
#   E2E_BASE=http://127.0.0.1:9932 E2E_TOKEN=<启动行里的 token> npm run e2e
```

`npm run e2e` 只依赖界面文本/角色（不依赖任何 fixture），失败会写
`.cache/ui-review/smoke-failed.png` 与 `smoke-report.json`。mock 专用的大盘脚本
（600 条虚拟化 + 帧率）是 `node dev/browser-check.mjs`，它依赖 mock fixture，别指向真网关。

## 目录

```
src/api/        唯一的网关入口：SSE 传输（库存 XRequest）、协议类型、会话/审批/收件箱/设置/文件/读模型
src/runtime/    GatewayChatProvider（3 个抽象方法）+ useXChat 接线
src/chat/       transcript（虚拟化）/ composer（斜杠、@）/ markdown / ThoughtChain / 工具卡（26 渲染器）
src/keyboard/   分层 Esc 仲裁器 + 全局 chord 仲裁器（单一 document capture 监听）
src/shell/      Layout + Sider + Splitter 壳层、顶带、会话列表、侧栏导航
src/features/   now / thread（含 trajectory、memory、child、find）/ approvals / inbox / palette
                settings（9 页）/ inspector / automation / capabilities / insights
src/design/     tokens.css + antd-theme.ts + icons.tsx（**三者皆为生成物**）+ 三行主题
dev/            mock-server.mjs
scripts/        gen-tokens.mjs · gen-icons.mjs
```

## 硬约束（不要偏离）

- **`src/api/` 之外不写 `fetch`**（库存 `XRequest` 在 `src/api/run-stream.ts` 创建）。
- **`tokens.css` / `antd-theme.ts` / `icons.tsx` 是生成物**：改色/字号改 `internal/{brand,design}`；
  改图标改 `internal/icons/svg`，然后跑对应 `gen:*`。手改视为违规（WEB-CLIENT §9.1 T1）。
- **不许颜色字面量**：`src/` 内 hex/rgb/hsl 由 ESLint 拦（白名单只有生成文件）。
- **主题只有三行**：`algorithm` + `colorPrimary` + `fontFamily`；**不用** `compactAlgorithm`。
- **键盘**：不绑 `Cmd/Ctrl+N/T/W/.` 与 `Tab`（Tab 只做焦点移动；菜单里只补全文本）；
  `Esc` 只经 `esc-arbiter.ts` 的单一监听 + 声明式 claim 栈，组件内不得自己绑 keydown。
- **审批无默认动作**：不用 `Modal.confirm`；裸 `Enter` 不决定；拒绝不是 primary。
- **不引第二套设计系统**；markdown 只用 `@ant-design/x-markdown`；图表不引库（洞察用表格）。
- **antd API 先查不猜**：`bash scripts/antd-cli.sh info <Component> --version 6 --format json`。
- **协议值不进 UI**：`stopReason: "stop"`、`end_turn`、`max_tokens`、`errorKind` 这类线上值只用于
  分支判断，用户看到的永远是自己的词表（「已停止」「失败」）。渲染原始枚举会把实现细节漏到界面上。
- **mock 必须和网关同等严格**：mock 宽松一次，就会藏一个只在真网关炸的集成 bug（已发生两次：
  帧尾 `\n`、`prompts.enqueue` 的 `text` vs `prompt`）。WS 方法的参数契约在
  `dev/ws-params.mjs`，mock、测试用的 fake 网关、`alignment.test.ts` 共用它——未知/缺失字段一律
  `invalid_params`，不再静默接受。

### 生成链基线（改 Go 侧设计源之前先做）

```
npm run gen:baseline   # 采基线：树必须干净（有未提交改动会拒绝），记录生成器真实读到的输入
npm run gen:verify     # 复跑生成器并比对：输入*消失*才红，数量变化不红
```

基线存在 `web/.cache/gen-baseline.json`（已 gitignore，不进版本库）。两条纪律：**基线必须在不可逆动作
开始之前采**，且**采集时树必须冻结**——在删除动作已经落到工作树之后再采，等于拿结果跟结果比。
输入清单是**从生成器源码解析**出来的（`read('internal/design/tokens.go')`、`readdirSync` 的目录），
不是按目录名猜的：猜目录会把同一个错误换个地方再犯。

## 订阅源版本（精确锁定，不要加 `^`）

`antd 6.7.1` · `@ant-design/x` / `x-sdk` / `x-markdown` `2.10.0` · `@ant-design/icons 6.3.4` ·
`react 19.3.1` · `react-router 8.5.2` · `@tanstack/react-virtual 3.14.28` · `vite 8.3.8`。

## 本期有意延后（不是遗漏）

| 项 | 原因 / 归属 |
| --- | --- |
| WS 快照/差值接线 | 权威状态在 WS `session:<id>` 域；当前由 `src/api/{gateway,approvals,inbox,settings,files}.ts` 的 HTTP 桥提供，task-3 落地后只改这些文件。审批队列用 1.5s 轮询桥接（`ApprovalsProvider`），同样只换实现。 |
| ~~会话切换的滚动位置恢复（A3）~~ | 已完成（task-8）：消息 id + 行内偏移 + 贴底布尔。 |
| ~~运行中排队（A5/B13）~~ | 已完成：队列是网关的 WS `prompts:<sessionID>` 域（快照 + 差值 + enqueue/remove 命令），客户端无本地副本、无 REST 路径。 |
| 消息 `⋯` 菜单（B11）、后台任务 chip（B12） | 本轮范围外。 |
| 计划/提问/产物三类浮层（S5/S6） | Inbox 能看见并跳回会话；浮层本身属下一轮。 |
| 设置页的动态记录新增（LSP 服务器添加、归档恢复） | 目录驱动的删除已实现；新增表单未做。 |
| Context Tab（R6） | 后端无 `KeyContext` 时该 Tab 不出现的规则已遵守；mock 不提供，故为占位路由。 |
| 暗色/语言切换的真实生效（B5） | 当前跟随系统 `prefers-color-scheme`；设置值已能保存，尚未驱动外观。 |
| 终端、图表、插件市场 | 任务书明确不做。 |
| Playwright / 截图回归 | 环境无 headless 浏览器；以 vitest（E1–E9、真 SSE 集成、520 条虚拟化）替代，**不声称**已在真实浏览器验证滚动帧率。 |