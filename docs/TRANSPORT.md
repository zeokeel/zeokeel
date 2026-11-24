# TRANSPORT：网关 ↔ 浏览器 控制面协议

> 状态：**Stage 0 草案**。本文件定义 `--product serve` 网关与浏览器客户端之间的线协议。
> 决策记录：[plans/web-ui-framework.md](plans/web-ui-framework.md)；客户端规范：[docs/WEB-CLIENT.md](WEB-CLIENT.md)。
> 盘点依据：`.cache/pivot/contracts-projection.md`（51 个服务键 / ~194 接口方法 / 7,085 行非测试契约）。
> 约定：`【决策】`= 已定，不再讨论；`【提案】`= 本文提出、待评审；`【估算】`= 未实测。

---

## 0. 一页速览

| 项 | 值 |
|---|---|
| 传输 | **双传输**：状态面 WebSocket + JSON-RPC 2.0（换行分帧，与 ACP 同构）；单轮运行流 `POST /api/chat/run` → SSE（§4.6，2026-11-23 决策） |
| 端点 | `ws://127.0.0.1:<port>/ws`；其余 HTTP 端点见 §3.0，二进制附件走 `GET /api/blobs/{id}` |
| 默认端口 | 9932，占用时顺延，实际端口写 `<data-dir>/gateway.json` |
| 数据形状 | **快照（`<domain>.snapshot`）+ 差值（`<domain>.delta`）**；禁止每帧拉取 |
| 版本 | `epoch`（网关实例随机 ID）+ 每域 `rev`（单调 uint64） |
| 覆盖 | T1 13 键 + T2 22 键 ≈ **100–108 个方法**；T3 16 键永不上线 |
| 单实例 | 数据目录 `flock`；托盘壳只读 `gateway.json`，不猜端口 |
| 鉴权 | 每次启动随机 token → `#token=` fragment → HttpOnly cookie；`Origin`/`Host` 严格校验 |

---

## 1. 目标与非目标

### 1.1 目标

| # | 目标 | 验收口径 |
|---|---|---|
| G1 | **完全等价**于现有桌面端 | 桌面今天能做的每一件事，浏览器里都能做（T1+T2 全覆盖，`contracts` 的 ~100 个可投影方法全部上线） |
| G2 | 网关是唯一状态持有者 | 浏览器刷新/换标签页/断线重连不丢任何进行中的 run；状态全部由网关重建 |
| G3 | 不每帧拉取 | 稳态下客户端一帧 0 次请求；只有变更才有一帧 `delta` |
| G4 | 托盘壳极薄 | 托盘壳只读 `gateway.json` + 一个只读状态接口，不含任何会话逻辑 |
| G5 | 复用现有后端 | 27 个 backend module 一行不改（`internal/assembly/registry.go:74` 等注册保持）；复用 headless 装配 |

### 1.2 非目标（第一版明确不做）

| # | 非目标 | 说明 |
|---|---|---|
| N1 | **不做公网暴露** | 第一版只 bind `127.0.0.1`。不监听 `0.0.0.0`、不做 TLS、不做反向代理场景（`AGENTS.md` §0 的形态即此）。 |
| N2 | **不做多用户** | 一个网关 = 一个 OS 用户 = 一份数据目录。没有用户表、没有 per-user 隔离；所有连接看到同一份状态。 |
| N3 | **不做鉴权体系** | 没有账号/密码/角色/会话有效期管理。只有一枚**进程级随机 token**（§3），用于阻止"同机其它网页/进程顺手连上来"。 |
| N4 | 不做插件市场/远程客户端/移动端 | 无 CORS 开放、无 `Access-Control-Allow-Origin: *`。 |
| N5 | 不做服务端渲染 | 客户端是 SPA（Vite 产物），网关只发 JSON，不发 HTML 片段。 |
| N6 | 不把 T3 上线 | `UIRuntime`/`Navigator`/`ScreenLister`/`ToastAnchor`/`SessionFocus`/`FilePreview`/`Theme`(客户端)/`Logger`/`DataDir`/`FileDrop`/`ACP`/`Undo`/`SpillStore`/`Subagents`/`ToolPolicy`/`Update` 共 16 键永不上线（分级依据见盘点报告 §2）。 |

> N3 的威胁模型必须写清楚：**防的是跨源网页与误连，不是同用户的本机恶意进程**。后者能读 `gateway.json`（同用户可读），因此 token 对它无效——这是 N2/N3 的直接后果，不是漏洞。

---

## 2. 进程与生命周期

### 2.1 三个进程角色

| 角色 | 进程 | 职责 | 现有代码 |
|---|---|---|---|
| **网关** | `zeokeel --product serve` | 装配 + WS 控制面 + 唯一状态 | 新增 `internal/gateway`；装配复用 headless |
| **浏览器客户端** | 用户浏览器 | 渲染 + 输入 | 新增 `web/` |
| **托盘壳** | `zeokeel --tray`（极薄 mygo 应用） | 托盘图标、全局快捷键、系统通知、`打开界面`/`退出` | 复用 `internal/host/tray.go` |

**关键点**：网关**不链接 mygo 窗口/事件循环**，因此不能沿用 `mygo.App` 系列 API；`internal/host/tray.go` 的托盘能力由托盘壳独占（它自己是一个最小 mygo app）。

### 2.2 `--product serve` 需要的代码改动（实施清单）

| 位置 | 现状 | 需要的改动 |
|---|---|---|
| `internal/assembly/assembly.go:31-38` | 常量只有 `ProductDesktop`/`ProductHeadless` | 加 `ProductServe = "serve"`；`Products()`（`:47-61`）从 `assemblies/*.json` 读，所以要么加 `assemblies/serve.json`，要么让 serve 复用 headless 文件（推荐后者，见下） |
| `internal/assembly/assembly.go:133-141` | `registryFor` 只给需要 UI 服务的装配声明 uiroot 服务（`usesUIServices` `:163`） | serve 无 `KeyUIRuntime` 消费者 → 天然不声明，无需改动 |
| `internal/host/host.go:271-284` | `switch opts.Product` 只有 desktop/headless，default 报错 | 加 `case assembly.ProductServe: return runServe(...)` |
| `internal/host/host.go:183-235` | flag 解析 + `--acp`/`--headless` 互斥 | 加 `--port`（默认 9932）；`--product serve` 与 `--acp`/`--headless` 互斥，沿用现有冲突检查写法 |
| `internal/host/host.go:347-385` | `RunACP` 模式：provide 服务 → 加载 headless 装配 → `Serve` | `runServe` 照抄这条骨架，只是最后一步换成 `gateway.Serve(ctx, ...)` |

**装配选择【决策】**：`serve` 不是新的 JSON 装配文件，而是"headless 装配 + 网关服务层"。理由：
1. headless 已经是"无窗口、全套后端模块"的形态（`assemblies/headless.json`，approvals 被钉成 `auto` 除外，见 §2.6）；
2. `registryFor` 只给需要 UI 服务的装配声明 uiroot 服务（`internal/assembly/assembly.go:133-141`），serve 没有 `KeyUIRuntime` 消费者，天然不声明；
3. 少一个需要长期同步的 JSON 文件。

### 2.3 启动序列

```
1. 解析 flag（--product serve --port 9932 --data-dir ...）        host.go:183-235
2. defaultDataDir: $ZEOKEEL_DATA_DIR 或 ~/.zeokeel               host.go:145-147
3. NewLogger(dataDir) → <data-dir>/logs/<LogFileName>            logger.go:45-56
4. NewSettings(dataDir)                                          host.go:293
5. applyProxySettings                                            host.go:297
6. 单实例 flock(<data-dir>/gateway.lock)                         §2.4
7. 生成 token(32B) + epoch(8B)                                   §3
8. bind 127.0.0.1:port（顺延，§2.5）→ 确定实际端口
9. 写 gateway.json（0600，原子 rename）                          §2.4
10. cordis.Root() + provideServices(dataDir, settings, logger)   host.go:813-827
11. assembly.LoadWithBuildInfo(root, assembly.ProductHeadless)   host.go:317
12. 网关注册订阅（chat/approvals/automation/goal/notifier）      §6.1
13. ensureDefaultWorkspace                                      host.go:323
14. 打印一行 URL（含 token fragment）到 stdout + 日志（不含 token）
15. 等待 SIGINT/SIGTERM（signals_unix.go:11）或全部客户端关闭且 --exit-with-last-client
```

> ⚠️ **provideServices 的现有分叉**：`internal/host/host.go:825-827` 在 `uiRoot == nil` 时**提前 return**，只 provide `DataDir`/`Settings`/`Logger`。而 `UserInteraction`/`ArtifactPresenter`/`PlanReviewer`/`Notifier`/`ForegroundSession` 全部由 uiroot 提供（`host.go:839-864`）。
> **这正是 ACP 里批准/提问/计划"消失"的机械原因**（不只是取舍）：headless 装配根本不 provide 这 5 个键，`hosttools` 的 `ctx.Get` 落空（`internal/modules/hosttools/module.go:77-88`），工具注册时就没接上。
> **【决策】网关必须自己 provide 这 5 个键**，实现放在 `internal/gateway`（§7.2），且**不能**复用 `uiroot` 的实现（会拉起整个冻结区）。

### 2.4 单实例协同

| 问题 | 方案 |
|---|---|
| 桌面用的 `mygo.App.RequestSingleInstanceLock`（`host.go:415-420`）能否复用？ | **不能**。代码注释已说明它依赖 GUI 事件循环（`host.go:411-414`："The handshake needs the GUI event loop"），网关无窗口。 |
| 复用它的**策略** | 可以。`singleinstance.go:13-22` 的结论是"永远单实例、不读设置"，serve 沿用这个策略（不是那段代码）。 |
| 实现 | `<data-dir>/gateway.lock` 上的 `flock(LOCK_EX|LOCK_NB)`。拿不到锁 → 读 `gateway.json` → stdout 打印 `already running: <url>` → `exit 0`（对齐 desktop 第二实例行为 `host.go:417-419`）。 |
| `gateway.json` 形状 | `{schemaVersion:1, epoch, pid, port, token, startedAt, product:"serve", version}`，权限 `0600` |
| 原子性 | 写 `gateway.json.tmp` → `fsync` → `rename`；退出时 `os.Remove` |
| 僵尸文件 | 启动时读旧文件：`pid` 不存在 → 直接覆盖；pid 存在但 `flock` 可拿 → 覆盖（防止 pid 复用误判） |
| 托盘壳如何发现网关 | **只读 `gateway.json`**，绝不猜端口、绝不扫端口。文件不在/解析失败 → 托盘菜单显示"网关未运行 · 启动"，点击执行 `spawn zeokeel --product serve` |
| 托盘壳是否单实例 | 否。托盘壳可被重启/重复拉起；它无状态，重复实例只多一个图标（可加 `trayID` 去重，`tray.go:22`）。**网关才是单例。** |

### 2.5 端口选择

| 规则 | 值 |
|---|---|
| 默认 | `9932` |
| 占用回退 | 依次试 `9932..9941`（10 个）；全占 → `:0`（内核分配） |
| 绑定地址 | 硬编码 `127.0.0.1`，不接受配置（N1） |
| 实际端口发布 | 只通过 `gateway.json` 的 `port` 字段 + 首次打印的 URL |
| 换端口的后果 | 已打开的浏览器标签页失效（WS 断开且无法重连）→ 客户端收到 close 后进入"网关已迁移"空态，提示从托盘重开。**不**自动猜测新端口（避免端口扫描 + DNS rebinding 面） |

### 2.6 装配差异：批准模式

| 装配 | approvals 配置 | 位置 |
|---|---|---|
| desktop | `{"mode": "workspace"}` | `internal/assembly/assemblies/desktop.json:22-27` |
| headless（含 ACP） | `{"mode": "auto", "ignorePermissionSetting": true}` | `internal/assembly/assemblies/headless.json:22-27` |
| **serve** | **必须同 desktop（`workspace`，读设置）** | 需要 `serve.json` 或加载期覆盖 |

**【提案】** 若坚持"serve 复用 headless 文件"，则 `runServe` 在 `LoadWithBuildInfo` 之前注入一份 override（把 approvals 条目改成 `{"mode":"workspace"}`）。**否则浏览器客户端永远收不到审批请求**——因为 `auto` 模式在 `Request` 里直接返回 allow（`internal/contracts/store.go:131-133` 注释 + `internal/modules/approvals/approvals.go:247-255`）。这是全文档里最容易漏掉的一条。

### 2.7 优雅退出

```
SIGINT/SIGTERM（signals_unix.go:11）
 → 停止 accept（close listener）
 → 广播 gateway.shutdown {reason:"signal"}，等 ≤2s 让客户端落盘
 → 取消所有 in-flight run? 否：按 desktop 语义，进程消失 = run 中断
 → loader.Close()            host.go:337-339
 → root.DisposeAll()         host.go:306-310
 → 删 gateway.json、释放 flock
```

| 场景 | 行为 |
|---|---|
| 浏览器全部关闭 | 网关继续运行（等价 desktop 的 close-to-tray 语义，`contracts.SettingCloseToTray` `interaction.go:96`） |
| 托盘"退出" | 托盘壳发 `POST /quit`（带 cookie）→ 走上面的退出路径 |
| 崩溃 | `gateway.json` 残留 → 下次启动按 §2.4 覆盖；客户端读到 `epoch` 变化 → 全量 resync |
| 更新安装 | 沿用 `applyPendingUpdateOnStartup`（`host.go:300-303`）；网关退出重启 → `epoch` 变 → 客户端 resync |

---

## 2.6 HTTP 端点（状态面之外的全部入口）

状态面是 WS；下面这些是**普通 HTTP**，全部要求 cookie 或 bearer（§3.2），并且都只 bind `127.0.0.1`。它们是 Web 客户端各屏的读模型与附件通道——此前文档只写了 WS 方法与一个不存在的 `/blob/{id}`，读模型端点完全没记（2026-11-24 补齐）。

| 方法 | 路径 | 用途 | 实现 |
|---|---|---|---|
| POST | `/auth` | `#token=` fragment → HttpOnly cookie（§3.1） | `auth.go` |
| GET | `/ws` | WebSocket 升级 = 状态面 | `server.go` |
| POST | `/quit` | 托盘"退出" | `server.go` |
| POST | `/api/chat/run` | 一轮对话的 SSE 流（§4.6），body 见 §5 的 `run` 条目 | `run.go` |
| POST | `/api/blobs` | 上传附件（原始二进制体，`?name=`，≤10 MiB）→ `{id,mime,bytes,name}` | `blobs.go` |
| GET | `/api/blobs/{id}` | 取回附件字节（**不支持 Range**） | `blobs.go` |
| GET | `/api/capabilities` | 技能 + MCP 连接器（R23） | `read_models.go` |
| GET | `/api/insights?range=7\|30` | 用量统计（R24） | `read_models.go` |
| GET | `/api/models` | 模型目录（含每个模型的思考模式与可编辑字段） | `read_models.go` |
| GET/POST/DELETE | `/api/automation`、`/api/automation/{id}`、`/api/automation/{id}/run` | 自动化任务 CRUD 与立即运行（R22） | `read_models.go` |
| GET | `/api/sessions/{id}/memory` | 会话记忆（R5） | `read_models.go` |
| GET | `/api/files/tree`、`/api/files/content`、`/api/files/diff` | 工作区文件/预览/差异（R8–R11） | `files.go` |
| GET | `/` 及其余 GET | 内嵌客户端静态资源（未内嵌时 503） | `server.go` |

**约定**：`/api/*` 未知路径先鉴权再 404（未鉴权的探测无法测绘端点面，`handleUnknownAPI`）。

---

## 3. 安全模型

四条约束，缺一不可。**防的是跨源网页与误连（N1/N3），不是同用户恶意进程。**

### 3.1 Token 生命周期

| 阶段 | 行为 |
|---|---|
| 生成 | 启动时 `crypto/rand` 32 字节 → `base64url`（43 字符）；`epoch` 另取 8 字节 hex（16 字符） |
| 存储 | 只在内存 + `gateway.json`（0600）。**永不写日志** |
| 首次打开 | 网关打印 `http://127.0.0.1:<port>/#token=<T>`；用户点开（或托盘"打开界面"） |
| 换 cookie | 客户端读 `location.hash` → `POST /auth {token}` → 服务端回 `Set-Cookie: zk=<T>; HttpOnly; SameSite=Strict; Path=/`；然后 `history.replaceState(null,'','/')` 抹掉 fragment |
| 为什么用 fragment | 浏览器**不会**把 `#...` 发给服务器 → 不进访问日志、不进 `Referer`、不进 HTTP 请求行 |
| 后续请求 | WS/HTTP 只认 cookie；`Authorization: Bearer <T>` 作为非浏览器客户端（curl/脚本）的替代 |
| 轮换 | 每次进程启动换一枚；无过期时间（进程级），无刷新机制 |
| 撤销 | 只能重启网关 |

### 3.2 四项检查与失败行为

| # | 检查 | 何时 | 失败行为 |
|---|---|---|---|
| C1 | **只 bind loopback** | 启动 | 不用检查——绑不上就换端口（§2.5）；不存在被外部访问的可能 |
| C2 | **`Host` 头**必须是 `127.0.0.1:<port>` 或 `localhost:<port>` | 每个 HTTP 请求 + WS 握手 | `403 {"error":"bad host"}`，立即关连接，记一条日志（防 DNS rebinding：攻击者域名解析到 127.0.0.1 时 `Host` 是攻击者域名） |
| C3 | **`Origin`** 必须是 `http://127.0.0.1:<port>` / `http://localhost:<port>`；**缺失** `Origin` 时要求 `Authorization: Bearer` | 每个 WS 握手 + 每个 `POST` | 有 Origin 但不符 → `403`，**不升级** WS；无 Origin 且无 Bearer → `401` |
| C4 | **token**：cookie `zk` 或 `Authorization: Bearer` | WS 握手 + 所有 `/api/*` | `401`，`Retry-After: 1`（限速，防枚举）；连续 20 次失败 → 该源 IP 冷却 60s |

补充：
- `GET /`（静态资源）与 `GET /assets/*` **不要求** token（否则首屏无法加载 JS）；它们不含数据，且 C2 已经把 rebinding 挡住。
- `GET /api/blobs/{id}` **要求** token，且 `id` 是网关生成的随机 128-bit，不可猜、不可枚举目录。
- 所有写方法都要求 cookie（`SameSite=Strict` 已阻断跨站表单/`fetch` 携带）。
- 响应一律带 `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、CSP `default-src 'self'`。

---

## 4. 协议总览

### 4.1 帧格式【决策】

与 ACP 完全同构：**一行一个 JSON-RPC 2.0 消息，`\n` 结束**。

| 项 | 值 | 依据 |
|---|---|---|
| 版本 | `"jsonrpc":"2.0"` | rig `acp/acp.go:21` |
| 帧上限 | 16 MiB | rig `acp/acp.go:26` `DefaultMaxFrameBytes` |
| 请求 | `{"jsonrpc":"2.0","id":N,"method":"session.send","params":{...}}` | 同 ACP |
| 响应 | `{"jsonrpc":"2.0","id":N,"result":{...}}` / `{"id":N,"error":{"code":..,"message":..,"data":..}}` | 同上 |
| 通知（服务端→客户端） | `{"jsonrpc":"2.0","method":"session.delta","params":{...}}`（无 `id`） | 同上 |
| 通知（客户端→服务端） | 仅 `$/cancel`、`foreground.set` 等少数 | 新增（ACP 无此需要，它单客户端） |

**为什么换行分帧而不是 SSE/自定义二进制**：ACP 的传输层是 `Serve(ctx, in io.Reader, out io.Writer)`（`internal/modules/acp/acp.go:162-175`，rig `acp/acp.go:1-8`），把 stdio 换成 WS 的 `io.ReadWriter` 后**分帧/校验/错误码/分发全部复用**，只需换掉"连接管理"这一层。JSON 文本也让浏览器 `JSON.parse` 直接可用，无需 proto codegen。

### 4.2 握手

```
客户端                                         网关
  |  GET /            (静态页，无 token)          |
  |  POST /auth {token}  → Set-Cookie            |
  |  GET /ws (Upgrade, Cookie: zk=..., Origin)   |
  |                                              |
  |  → initialize {clientName, clientVersion,     |
  |                protocolVersion:1,             |
  |                capabilities:[...]}            |
  |  ← result {epoch, protocolVersion:1,          |
  |            serverVersion, capabilities:[...], |
  |            limits:{maxFrameBytes,             |
  |                    maxAttachmentBytes,        |
  |                    snapshotTailDefault,       |
  |                    deltaRingBytes}}           |
  |  → ready {}                                   |   ← 客户端确认后网关才开始推 delta
```

| 规则 | 说明 |
|---|---|
| `initialize` 必须第一个发 | 之前只允许 `ping` |
| `protocolVersion` 不符 | 服务端回 `error.code=1021 unsupported_protocol` 并附自身版本，**关闭连接** |
| `capabilities` 双向 | 服务端列出它支持的可选半（清单见 §4.2.1），客户端列出它认识的 UI schema 版本。缺失能力 → 相关 UI 入口不渲染（对齐现有"键缺失即隐藏"逻辑，如 `workspace.go:72-75`） |

#### 4.2.1 能力清单（服务端 `initialize.capabilities`）

**规则**：每个**会改变 UI 渲染**的域或运行模式，都必须在能力表里有条目。否则客户端只有两条
烂路——总是猜测（能力不存在也渲染，运行时才炸），或者试错（点一次、失败再禁用）。能力探测
**只走这张表**，不新增探测方法。

**静态项**（`serverCapabilities`，每个网关都报）：

| 能力 | 门控的 UI / 行为 |
|---|---|
| `session.list` / `session.snapshot` / `session.delta` / `session.subscribe` | 会话侧栏与对话页（读模型） |
| `session.create` | 「新建会话」入口（客户端据此禁用按钮） |
| `run.cancel` | 运行中的「停止」 |
| `approvals` | 审批卡（`approvals.snapshot/subscribe`、`approval.resolve`） |
| `ask` | 提问卡与回答 |
| `plan` | 计划审阅卡与 `plan.exit` |
| `artifacts` | 产物面板（`present` 的落点） |
| `settings` / `settings.set` | 设置页；**`settings.set` 决定表单是否可写**（缺失时页面显示只读并禁用输入） |
| `workspace` | 工作区列表与切换 |
| `prompts` | 提示队列（发送队列、队首/取消） |
| `notify` | 应用内 toast 与 `notify.action` |
| `foreground` | 前台会话上报（后台完成是否静音的判断依据） |

**条件项**（装配真的提供对应后端时才出现；缺失即相关入口不渲染）：

| 能力 | 出现条件 | 门控的 UI |
|---|---|---|
| `context` | 装配提供 `contracts.KeyContext` | 「上下文」Tab（`context.snapshot/subscribe`，§5.1.8） |
| `regenerate` | 聊天后端实现 `contracts.SessionRegenerator` | 「⋯」菜单里的「重新生成」（§4.6 regenerate 模式） |
| `edit` | 聊天后端实现 `contracts.SessionEditor` | 「⋯」菜单里的「编辑并重发」（§4.6 editMessageId 模式） |
| `tasks` | 装配提供 `contracts.KeyTaskBoard` | 后台任务 chip（`tasks.snapshot/subscribe`、`task.status/delete`，§5.1.8） |
| `team` | 装配提供 `contracts.KeyTeam` | 团队屏（`team.*`，§5.1.11） |
| `update` | 装配提供 `contracts.KeyUpdate` | 设置 → 关于 的更新面（`update.status/check/download/install`，§5.1.12） |

> 能力名**全小写、无前缀**，与 `context`/`plan`/`ask` 同风格。`regenerate` 是**运行模式**而不是
> 方法名——客户端读它决定菜单项的禁用态与 tooltip（文案 `thread.regenerateUnsupported`），
> 而不是靠"点一次看它是否返回 `regenerate_not_supported`"。
>
> 客户端已用到的两项：`session.create`（新建按钮）、`settings.set`（设置可写）。新增能力时
> 先在这张表登记，再让客户端读——`TestInitializeAdvertisesCapabilities` 会同时钉住静态清单
> 与三个条件项的双向行为。

#### 4.2.2 机器可读的契约：`internal/gateway/contract.json`【生成物】

`go generate ./internal/gateway` 从 Go 声明生成 `internal/gateway/contract.json`（提交进版本库）：

```json
{
  "protocolVersion": 1,
  "params": { "<method>": { "required": [...], "optional": [...] }, … },
  "capabilities": {
    "static": [ … ],
    "conditional": [ { "name": "context", "when": "KeyContext" }, … ]
  }
}
```

- `params`：字段名来自各 `xxxParams` struct 的 **json tag**；**不带 `,omitempty` = 必填**
  （handler 会拒绝空值的那一类）。所以"必填"不是另写一份清单，而是 tag 本身的事实。
  **副作用（调用方须知）**：可空值字段会被记为**可选**——`settings.set` 的 `value` 就是这样
  （空串 = 清除该行，服务端照写不误）。空值代表什么由调用方决定，服务端不会拒绝它。
- `capabilities.static` 与本节上表**同源**（`serverCapabilities`），`conditional[].when` 是
  **机器可读标记**（`KeyContext` / `KeyTaskBoard` / `SessionRegenerator`），人话说明在 §4.2.1。
- **谁读它**：web 侧的三处替身（`dev/ws-params.mjs`、`dev/mock-server.mjs`、
  `src/test/fake-gateway.ts`）**只读这一份**，禁止手写方法或能力清单。起因是第 9 次
  "替身比生产宽松"：fake 手写了 `settings.set` 而真网关没有，171 条测试全绿、设置页永远只读。
- **过期即红**：`TestContractIsUpToDate` 比对生成物与源码，不一致就指出具体方法/能力；
  生成是确定性的（键排序），**连跑两次 `git diff` 为空**。忘了重生成时的修法就是那一行命令。
- `TestContractCoversEveryMethod` 断言 contract 的方法集合 == `SupportedMethods()`（数量以测试为准，当前 48 个；这里刻意不写死数字），
  所以"新方法忘了进契约"也会红。
- **`endpoints` 段**：非 JSON-RPC 的端点也在契约里。为什么单独一段而不是塞进 `params`：`params`
  以 JSON-RPC 方法名为键、且 `TestContractCoversEveryMethod` 把它的键集合钉死为
  `SupportedMethods()`——把 `POST /api/chat/run` 混进去，等于让契约声明一个网关并不 dispatch
  的"方法"，正是这个文件要防的那类 bug。所以端点自带 `transport/method/path`。

  目前只有一个端点，它是协议里最复杂的请求体（三种 run 模式 + 互斥字段）：

  ```json
  {
    "name": "chat.run", "transport": "sse", "method": "POST", "path": "/api/chat/run",
    "fields": { "required": ["clientRunId","sessionId"],
                "optional": ["attachments","editMessageId","regenerate","text"] },
    "rules": [
      { "kind": "exclusive", "fields": ["regenerate","editMessageId"] },
      { "kind": "requires", "field": "text", "when": "editMessageId", "nonEmpty": true },
      { "kind": "forbidden", "field": "text", "when": "regenerate" },
      { "kind": "requiredUnless", "field": "text", "unless": ["regenerate"], "nonEmpty": true }
    ]
  }
  ```

  规则只有四种、都是扁平字段（JS 侧一个 switch 就能校验，无需表达式解析）：`exclusive`（列出的
  字段不得同时出现）、`requires`（`when` 出现时 `field` 必填，`nonEmpty` 则不得为空白）、
  `forbidden`（`when` 出现时 `field` 必须**缺席**）、`requiredUnless`（`field` 必填，除非列出了
  `unless` 中的字段）。**替身不该再各写一遍 if**：`web/dev/mock-server.mjs` 曾手写
  `const {sessionId, text, regenerate} = body` 并**静默丢掉 `editMessageId`**，于是 mock 接受
  编辑请求却按普通 run 处理（第 12 次"替身比生产宽松"，且发生在最复杂的请求体上）。
  `TestContractCoversTheRunEndpoint` 钉住字段来自 `runRequest` 的 json tag、且四条规则逐条相等。
| `epoch` | 客户端记录；重连后 `epoch` 变 → 丢弃全部本地状态（§6.2） |
| 多标签页 | 每个标签页 = 一条 WS = 一个 `clientId`。网关不做跨标签页状态同步（各自 `session.subscribe`），但**读模型共享**（同一个 rev 序列） |

### 4.3 错误模型

基础码复用 JSON-RPC/ACP（rig `acp/acp.go:28-37`）：

| code | 名称 | 何时 |
|---|---|---|
| -32700 | parse_error | 帧不是合法 JSON |
| -32600 | invalid_request | 缺 `method` / 缺 `id`（非通知） |
| -32601 | method_not_found | 未实现 |
| -32602 | invalid_params | 参数校验失败（`data: {field, reason}`） |
| -32603 | internal_error | 网关内部故障 |

应用码（`data` 里带 `retryable` 布尔）：

| code | name | 来源契约（file:line） |
|---|---|---|
| 1001 | session_not_found | `chat.go:560` 返回 ok=false |
| 1002 | session_running | `ErrSessionRunning` `chat.go:269` |
| 1003 | compact_conflict | `ErrCompactConflict` `chat.go:255` |
| 1004 | compacting | `ErrChatCompacting` `chat.go:260` |
| 1005 | compact_unavailable / 1006 compact_nothing | `chat.go:248,249` |
| 1007 | goal_not_found / 1008 goal_stale_ref / 1009 goal_conflict / 1010 goal_round_limit | `goal.go:72-76` |
| 1011 | goal_direct_human_required | `goal.go:77` |
| 1012 | job_not_found / 1013 job_unavailable | `agentsurface.go:140,143` |
| 1014 | session_log_too_large | `sessionlog.go:42` |
| 1015 | workspace_name_empty / 1016 workspace_name_taken | `workspace.go:98,101` |
| 1017 | automation_locked | `automation.go:260` |
| 1018 | undo_unavailable | `undo.go:13` |
| 1019 | revision_gone（需 resync） | §6.2 |
| 1020 | unauthorized / 1021 unsupported_protocol / 1022 bad_origin | §3 |
| 1023 | capability_not_supported | §1.3 可选半缺失 |
| 1024 | not_ready | 装配加载中/关闭中 |
| 1025 | backpressure | 客户端消费太慢（§4.5） |

**翻译层职责**：契约里满是 `errors.New` 哨兵、UI 用 `errors.Is` 分支（`chat.go:246-269`、`workspace.go:96-102`、`sessionlog.go:36-43`、`goal.go:71-78`、`agentsurface.go:136-144`）。网关**必须**把这些映射成上表稳定码，**禁止**把 `err.Error()` 英文原文塞进 `message` 给 UI 判断（现有桌面 UI 就是这么做的，浏览器侧没有 `errors.Is`）。

### 4.4 取消语义

| 层 | 消息 | 语义 | 依据 |
|---|---|---|---|
| **run 取消** | `run.cancel {sessionId}` | 停掉该会话正在跑的 agent run（幂等） | `chat.go:574` |
| **请求取消** | `$/cancel {id}`（客户端→服务端通知） | 中止某个在途 RPC 的服务端工作，回 `error 1026 cancelled` | ACP 用 `ctx.Done()` 等价实现（`chat_adapter.go:92-94`） |
| **提问取消** | `ask.cancel {id}` | 关掉一个 pending question | `interaction.go:33` |
| **OAuth 取消** | `mcp.auth.cancel {cfg, flow}` | 关本地回环监听 | `plugins.go:192` |
| **断线** | WS close | **不取消任何 run**（网关继续跑，等价 desktop 最小化）；客户端重连后 `session.subscribe{sinceRev}` 补齐 | §6.2 |
| **退出** | `gateway.shutdown` 通知 | 客户端停止重连，显示"网关已停止" | §2.7 |

关键区分：**"用户点了停止"是业务事件（`run.cancel`），"网络断了"不是**。现有桌面代码在这里有先例：`session.go:620-628` 处理"请求停止但订阅漏了 settle"的情况，逻辑是"从读模型确认 run 已停，才宣布已停止"——网关要保留这个诚实性：`run.cancel` 只是请求，真正结束由 `session.delta` 的 `running:false` 表达。

### 4.5 背压

| 场景 | 策略 |
|---|---|
| 客户端消费慢（WS send buffer 满） | 丢弃该客户端的**中间** delta，只保留最后一帧（每个域合并成一个"这里已经到 rev N"的 delta），并补发一次全量快照；超阈值 → close(1013 try again later) |
| 流式文本 | 服务端按 50–100ms 合并 `textAppend`（估算值，需实测调） |
| 大 payload | 一律走 `/api/blobs/{id}`，不走 WS（§6.4） |

### 4.6 流式运行走 SSE（双传输）【决策 2026-11-23】

**决策**：状态面（快照 / 差值 / 审批 / 交互 / 通知）继续走 WS；**单轮运行的 token 流走 `POST /api/chat/run` 的 `text/event-stream`**。

**理由（框架事实，不是偏好）**：前端采用 `@ant-design/x-sdk` 的 `useXChat`，其库存 `XRequest` 是 **SSE 专用**实现（`SSEOutput = Partial<Record<'data'|'event'|'id'|'retry', any>>`）。走 SSE 则适配代码为 0；纯 WS 则需自实现 `AbstractXRequestClass` 的 7 个成员（约 250 行）。单轮运行本来就是"请求 → 流式响应 → 结束"的形状，WS 的双向性在这里用不上。

#### 端点与请求

```
POST /api/chat/run
Content-Type: application/json
Accept: text/event-stream

{ "sessionId": "<id>", "text": "...", "attachments": ["<blob-id>"], "clientRunId": "<uuid>" }
```

响应 `200 text/event-stream`。`clientRunId` 由客户端生成，用于把 SSE 与 WS 上同一轮的状态变更对齐（幂等重试时复用同一个 id）。

#### 重新生成（`regenerate` 模式，2026-11-23）

"重试上一轮"是**同一个端点的模式**，不是第二条发送路径：

```json
{ "sessionId": "<id>", "clientRunId": "<uuid>", "regenerate": true }
```

- `text` 必须为空（带上 `text` → `invalid_params {field:"text"}`）：后端重跑的是它**已经持有的**最后一条用户消息，客户端再发一遍就等于追加第二条 prompt。
- 后端语义（`SessionRegenerator.Regenerate`，`chat.go:480`）：**回退最后一轮**（丢掉尾部助手回复与其用户消息）**并立刻重跑**——所以它不是"先重置、再自己发一轮"的两步式。
- 事件流、`clientRunId` 幂等、断连≠停止，全部与普通 run 相同：网关先挂 SSE 观察器再调 `Regenerate`，因此 `tool.start`/审批/用量/`done` 一个都不缺。
- 后端不支持该半边（旧装配）→ `1023 capability_unsupported`，`data.name = "regenerate_not_supported"`；客户端据此禁用菜单项，而不是显示"请求失败"。
- 与历史的交互：回退会整体替换历史，`session:<id>` 域据此发 `msg.reset`（§6.6）；客户端不需要额外做什么，但**要以 WS 的 rev 为准**（§4.6 上一节）。
- **为什么不做一个独立的 `message.regenerate` WS 方法**：那样协议里会出现"WS 先重置 + SSE 再发送"的两步式，客户端漏发一次就留下"历史已回退、但没有 run"的半状态；而 `message.send` 当初正是因为第二条发送路径被删除的（见 §5.1）。发送路径只有一条：`POST /api/chat/run`。

#### 事件类型

| event | data | 说明 |
|---|---|---|
| `message.delta` | `{ turnId, text }` | 正文增量（合并窗口与服务端其他流一致） |
| `reasoning.delta` | `{ turnId, text }` | 推理增量 |
| `tool.start` | `{ callId, name, input }` | 工具开始（对应 `ThoughtChain` 的节点创建） |
| `tool.result` | `{ callId, name?, ok, output?, error? }` | 工具结束（`name` 供客户端给结果行命名，见下方顺序保证） |
| `usage` | `{ inputTokens, outputTokens, ... }` | 用量（状态条使用） |
| `done` | `{ turnId, stopReason }` | 正常结束；随后关闭连接 |
| `error` | `{ code, message }` | 终止性错误；随后关闭连接 |

事件顺序保证：`tool.start` 必在同一 `callId` 的 `tool.result` 之前；`done`/`error` 必为最后一个事件。

**一个调用若在流第一次看到它时已经结束**（合窗内完成），网关仍先补发它的 `tool.start`（带 `name` 与 `input`）再发 `tool.result`——客户端按 `callId` 建节点，缺了 start 就只能拿 callId 当工具名显示。

#### 编辑并重发（`editMessageId` 模式，2026-11-23）

编辑一条**更早的**用户消息并重发，同样是这个端点的模式：

```json
{ "sessionId": "<id>", "clientRunId": "<uuid>", "editMessageId": "<msg-id>", "text": "<新文本>" }
```

- 语义：**截断到该消息之前**（该消息及其之后的一切都被丢弃），再用 `text` 起一轮——标准聊天语义。
- `editMessageId` 与 `regenerate` **互斥**（同时出现 → `invalid_params {field:"editMessageId"}`）；
  带 `editMessageId` 时 `text` **必填非空**（空文本等于"删除消息"，协议不提供）。
- 事件流、`clientRunId` 幂等、断连≠停止，与普通 run 相同；同一 `clientRunId` **只截断一次**。
- **目标定位**：durable 历史（rig）**没有消息 ID**，读模型的 `msg-N` 是内存生成、重启后按序重分配。
  因此服务端用**从尾部倒数第 k 条用户消息**作锚点（k = 读模型里目标之后还有几条用户消息），
  并用 (role, text) 校验；对不上就拒绝。助手消息在 durable 里是一次迭代一条、读模型里折叠成一条，
  所以**按消息条数倒推是错的**（`TestEditAnchorCountsUserMessages` 钉住这一点）。
- **拒绝清单**（每个都是 `invalid_params {field:"editMessageId"}`，`data.name` 见括注），
  **绝不会静默从头跑**（那等于悄悄丢掉用户历史）：
  | 情况 | `data.name` |
  |---|---|
  | 会话或消息不存在 | `edit_target_not_found` |
  | 目标不是用户消息（编辑助手 = "从这里重生成"，另一个功能） | `edit_target_not_user` |
  | 目标带附件（run 路径不支持附件，允许编辑等于静默丢图） | `edit_target_has_attachments` |
  | 目标已被 compact 掉（durable 窗口外，无法安全定位） | `edit_target_compacted` |
  | 锚点在替换前复核时对不上（期间历史被改写） | `edit_target_moved` |
- **替换前复核**：算完锚点后、写入前**重新加载并校验前缀仍然是那个锚点**（照 compaction 的做法，
  `compact_now.go`）——客户端点击到服务端执行之间有时间窗，校验不过就拒绝。
- 能力探测：装配提供 `contracts.SessionEditor` 时 `initialize.capabilities` 含 `edit`（§4.2.1），
  客户端据此启用菜单项；不支持时 `1023 capability_unsupported` + `data.name="edit_not_supported"`。
- **为什么不做独立 WS 方法**：同 regenerate——两步式会留下"历史已截断但没有 run"的半状态。

#### 与 WS 状态的关系（关键）

**权威状态仍在 WS 的 `session:<id>` 域里**，SSE 只是"这一轮的增量快车道"：

- 客户端渲染以 `ChatMessagesStore` 为单一真相，SSE 事件通过 provider 的 `transformMessage` 写入；WS 的 `session.delta` 到达时按 §6.2 的 rev 规则合并，二者冲突时**以 WS 的 rev 为准**。
- 因此 SSE 丢包/中断不会造成状态错误，只会造成"这一轮看起来停住"，WS 的差值会补上。

#### 断连与取消

| 场景 | 行为 |
|---|---|
| 用户点"停止" | 客户端 `abort()`（`AbortController`）+ 发 WS `run.cancel`；与 §4.4 一致，**以 WS 的 `running:false` 为准**宣布已停止 |
| 浏览器标签关掉 / 网络断 | **不中止运行**（与 §4.4"网络断了不是业务事件"一致）。agent 继续跑，客户端回来靠 `session.snapshot` 补齐 |
| 服务端中止（超时/网关退出） | 发 `error` 事件后关闭；WS 侧广播 `running:false` |

#### 鉴权与 CORS

- 复用 §3.1 的 cookie；因为客户端用 `fetch` + `ReadableStream`（非 `EventSource`），必要时可带自定义头，但**不以 header 鉴权为准**，cookie 是唯一权威。
- 同源（客户端由网关自己吐出），因此不需要 CORS 头；`Origin` 校验按 §3.2 执行。

#### 背压

SSE 由 TCP 自然背压，不需要像 WS 那样丢中间帧；但**渲染侧必须节流**（`ChatMessagesStore` 自带 throttle），否则每个 token 一次 setState 会让长会话掉帧。

---

## 5. 方法清单

命名：`<domain>.<verb>`。快照类 `*.snapshot`，变更类 `*.delta`（服务端→客户端通知），命令类动词式。
`幂等` 列：Y = 重复调用结果相同；N = 每次都有副作用；Y* = 服务层幂等但会触发一次广播。
**所有方法都对应 `contracts` 里已有的接口**——本协议不新增后端能力，只新增形状。

### 5.1 T1：可用浏览器客户端所必需（13 键 / 约 58 方法）

#### 5.1.1 会话与消息（`chat`，`chat.go:554`）

> **向前分页的终止语义**（2026-11-24 明确）：把上一页的 `oldestMessageId` 作为
> `beforeMessageId` 再取一页，当游标正好是会话的第一条消息时，返回 **`messages: []` +
> `truncated: false` + 无 `oldestMessageId`** —— 这就是"历史已到最前"。客户端以
> **空 `messages` 停止翻页**，不要用 `truncated` 判断；非法/不属于该会话的游标是参数错误
> （`invalid_params`），不是"从最新开始"。

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `session.list.snapshot` | — | `{epoch,rev,sessions:[ChatSessionInfo]}` | Y | `ChatService.Sessions()` `chat.go:558` | ✅ 已实现 |
| `session.snapshot` | `{sessionId, tail?, beforeMessageId?}` | `{epoch,rev,session:ChatSession, truncated, oldestMessageId}` | Y | `ChatService.Session()` `chat.go:560` | ✅ 已实现 |
| `session.active.get` | — | `{sessionId}` | Y | `chat.go:562` | ✅ 已实现 |
| `session.activate` | `{sessionId}` | `{sessionId}` | Y* | `chat.go:564` | 延后 |
| `session.create` | `{title}` | `{sessionId}` | N | `chat.go:566` | ✅ 已实现 |
| `session.delete` | `{sessionId}` | `{}` | Y | `chat.go:569` | 延后 |
| `session.rename` | `{sessionId,title}` | `{}` | Y | `SessionRenamer` `chat.go:585` | 延后 |
| `session.pin` | `{sessionId,pinned}` | `{}` | Y | `SessionPinner` `chat.go:516` | 延后 |
| `session.archive` | `{sessionId,archived}` | `{}` | Y | `SessionArchiver` `chat.go:501` | 延后 |
| `session.fork` | `{sessionId}` | `{sessionId}` | N | `SessionForker` `chat.go:544` | 延后 |
| `session.model.set` | `{sessionId,model}` | `{}` | Y | `SessionModelSwitcher` `chat.go:467` | 延后 |
| `session.thinking.set` | `{sessionId,modelId,modeId}` | `{}` | Y | 组合 settings 行 `thinking.go:306-313` | 延后 |
| `session.preset.get/set` | `{sessionId}` / `{sessionId,presetId}` | `{presetId}` | Y/Y | `SessionPresetSetter` `presets.go:138,139` | 延后 |
| `session.permission.set` | `{sessionId,mode}` | `{}` | Y | `SessionPermissionSwitcher` `automation.go:73` | 延后 |
| `session.workspace.set` | `{sessionId,workspaceId}` | `{}` | Y | `SessionWorkspaceSwitcher` `automation.go:67` | 延后 |
| ~~`message.send`~~ | — | — | — | **已移除（2026-11-23）**：与 §4.6 的 `POST /api/chat/run` 完全重复（后者已带 text + attachments 且是流式入口）。两条发送路径并存必然漂移，只保留 SSE 那一条。原实现参考 `ChatService.Send` `chat.go:572`。 | 已移除 |
| ~~`message.regenerate`~~ | — | — | — | **已移除（2026-11-23）**：改为 `POST /api/chat/run` 的 `regenerate:true` 模式（§4.6），保持单一发送路径。后端仍是 `SessionRegenerator` `chat.go:480`。 | 已移除 |
| `run.cancel` | `{sessionId}` | `{}` | Y | `chat.go:574` | ✅ 已实现 |
| `session.compact` | `{sessionId}` | `CompactResult` | N | `SessionCompactor` `chat.go:284` | 延后 |
| `session.allowedTools` | `{sessionId}` | `{tools:[]}` | Y | `SessionToolPermissions` `chat.go:534` | 延后 |
| `session.allowedTools.clear` | `{sessionId}` | `{}` | Y | `chat.go:536` | 延后 |
| `prompts.snapshot` | `{sessionId}` | `{prompts:[QueuedPrompt]}` | Y | `PromptQueue` `promptqueue.go:38` | ✅ 已实现 |
| `prompts.enqueue` | `{sessionId,prompt}` | `{id}` | N | `promptqueue.go:36` | ✅ 已实现 |
| `prompts.remove` | `{sessionId,id}` | `{}` | Y | `promptqueue.go:40` | ✅ 已实现 |
| `prompts.subscribe` | `{sessionId, sinceRev}` | `{rev, resync:bool}` | Y | 域订阅（`prompts:<sessionId>`） | ✅ 已实现 |
| `prompts.unsubscribe` | `{sessionId}` | `{}` | Y | 同上（取消；不调用则网关一直为该会话轮询重建域） | ✅ 已实现 |
| `session.subscribe` | `{sessionId, sinceRev}` | `{rev, resync:bool}` | Y | `ChatService.Subscribe` `chat.go:577` | ✅ 已实现 |
| `session.unsubscribe` | `{sessionId}` | `{}` | Y | 同上（取消） | ✅ 已实现 |
| ↳ 通知 |  | `session.list.delta`、`session.delta` |  |  | — |

#### 5.1.2 检索与持久读（`store`，`store.go:48`）

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `search.messages` | `{query,limit}` | `{hits:[MessageSearchHit]}` | Y | `MessageSearcher` `search.go:40` | 延后 |
| `session.meta` | `{sessionId}` | `{meta,found}` | Y | `Store.GetSessionMeta` `store.go:72` | 延后 |
| `session.events` | `{sessionId,from,to}` | `{events:[SessionEvent]}` | Y | `rig.SessionLog.Events` rig `extensions.go:158` | 延后 |
| `session.events.last` | `{sessionId,limit}` | `{events}` | Y | `extensions.go:162` | 延后 |
| `session.events.search` | `{query,limit}` | `{events}` | Y | `extensions.go:160` | 延后 |

> `Store.History()/CheckpointStore()/ReplaceHistory()`（`store.go:53-59`）**不上线**：返回 `rig.History` 接口值与 `[]rig.Message`（Go 类型，见盘点报告 R5），且只被 agent 与 compaction 使用。

#### 5.1.3 审批（`approvals`，`store.go:123`）

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `approvals.snapshot` | — | `{epoch,rev,mode,pending:[ApprovalRequest]}` | Y | `Mode()` `store.go:130` + `Pending()` `:135` | ✅ 已实现 |
| `approval.resolve` | `{id,decision}` | `{}` | Y* | `Resolve` `store.go:137`；`decision∈{allow,allow_always,deny}` `:81-92` | ✅ 已实现 |
| `approvals.subscribe` | — | — | Y | `Subscribe` `:141` | ✅ 已实现 |
| ↳ 通知 |  | `approvals.delta {rev, added[], removed[]}` |  |  | — |

> **必须上线**：`Request` 阻塞在 agent goroutine 上（`store.go:131-133`），不投影就永远 allow/deny 不了。这是与 ACP 最大的分叉（ACP 直接把它钉成 auto，§9）。
> `ApprovalRequest.SessionID/Source`（`store.go:106-113`）必须透传：多会话并发时对话框要说明来源，这是契约里明确写过的要求。

#### 5.1.4 阻塞式交互：提问 / 计划 / 产物（`interaction`，`interaction.go`）

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `ask.snapshot` | — | `{pending:{id,text,options}?}` | Y | `UserInteraction` `interaction.go:25`（`Ask` 的等待态） | ✅ 已实现 |
| `ask.answer` | `{id,answer}` | `{}` | Y* | `Answer` `:31` | ✅ 已实现 |
| `ask.cancel` | `{id}` | `{}` | Y | `Cancel` `:33` | ✅ 已实现 |
| `ask.subscribe` | — | — | Y | `SetHandler` `:27`（改为事件流） | ✅ 已实现 |
| `plan.snapshot` | — | `{state,content}` | Y | `PlanReviewer.State()` `interaction.go:80` | ✅ 已实现 |
| `plan.exit` | `{action}` | `{}` | N | `Exit` `:79` | ✅ 已实现 |
| `plan.subscribe` | — | — | Y | `SetPlanHandler` `:77` | ✅ 已实现 |
| `artifacts.snapshot` | `{limit}` | `{items:[Artifact]}` | Y | `ArtifactLister` `:54` | ✅ 已实现 |
| `artifact.get` | `{id}` | `{artifact}` | Y | `:54` 索引后按 id 取 | 延后 |
| ↳ 通知 |  | `ask.opened`、`plan.reviewing`、`artifact.presented` |  |  | — |

> `Ask`/`Review`/`Present` 三个方法本身**不上线**——它们是 agent 侧调用（阻塞等待）。上线的是它们的**等待态**与**答复命令**，方向相反。

#### 5.1.5 设置 / 语言 / 主题 / 应用信息 / 模型目录

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `settings.snapshot` | — | `{epoch,rev,rows:{k:v}}` | Y | `SettingsService.All()` `contracts.go:349` | ✅ 已实现 |
| `settings.get` | `{key}` | `{value}` | Y | `:347` | 延后 |
| `settings.set` | `{key,value}` | `{}` | Y | `:348` | ✅ 已实现 |
| `settings.delete` | `{key}` | `{}` | Y | `SettingsDeleter` `:356` | 延后 |
| `settings.subscribe` | — | — | Y | **新增**（契约无通知，§6.3） | ✅ 已实现 |
| `locale.get` | — | `{language,strings:{k:v}}` | Y | `LocaleService.Language/T` `contracts.go:340,342` | 延后 |
| `locale.set` | `{language}` | `{}` | Y | `:341` | 延后 |
| `theme.get` | — | `{dark}` | Y | `ThemeService.Dark()` `contracts.go:333` | 延后 |
| `theme.set` | `{dark}` | `{}` | Y | `:334` | 延后 |
| `appinfo.get` | — | `AppInfo` | Y | `contracts.go:185-191` | 延后 |
| `models.snapshot` | — | `{models:[{ModelInfo,thinkingModes[]}]}` | Y | `ModelCatalogService.Models()` `modelcatalog.go:143` + `ThinkingModesForModel` `thinking.go:164` | 延后 |

> `theme` 在盘点里归 T3（客户端 CSS 主题），但**主题选择必须同步到设置行**（`ui.theme.mode`，`interaction.go:120`），所以保留 `theme.get/set` 两个薄方法；渲染完全在客户端。
> `locale.get` 一次发全文案表（估算 8–20 KiB，按模块分片更稳），之后 `locale.set` 触发一次重取。**不做** `locale.t(key)` 逐条 RPC。

#### 5.1.6 工作区与文件（`workspace`，`workspace.go:37`）

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `workspace.list.snapshot` | — | `{epoch,rev,workspaces:[], currentId?}` | Y | `List` `workspace.go:39` + `Current` `:41` | ✅ 已实现 |
| `workspace.create` | `{name,root}` | `{workspace}` | N | `:44` | 延后 |
| `workspace.open` | `{id}` | `{workspace}` | Y* | `:46` | 延后 |
| `workspace.delete` | `{id}` | `{}` | Y | `:48` | 延后 |
| `workspace.rename` | `{id,name}` | `{workspace}` | Y | `WorkspaceRenamer` `:84` | 延后 |
| `workspace.color` | `{id,color}` | `{workspace}` | Y | `WorkspaceColorer` `:93` | 延后 |
| `files.list` | `{dir}` | `{entries:[FileEntry]}` | Y | `Files` `:52` | 延后 |
| `files.search` | `{query,limit}` | `{entries}` | Y | `Search` `:58` | 延后 |
| `file.read` | `{path,limit,offset?}` | `{blobId,size,mime}` 或文本内联 | Y | `ReadFile` `:55` | 延后 |
| `workspace.changes` | — | `{changes:[FileChange]}` | Y | `WorkspaceDiffer` `:110` | 延后 |
| `file.openNative` | `{path}` | `{}` | Y | `FilePreviewer.OpenNative` `agentsurface.go:176` | 延后 |

> `file.read` 走 blob（`[]byte` 见 `workspace.go:55`）。`FileEntry`/`FileChange` 全字段可 JSON（`workspace.go:22-33,63-70`），无 Go-only 类型。
> `file.openNative` 是**唯一**需要宿主动作的文件能力（在系统默认应用打开）；`PreviewFile`/`OpenFiles`（`agentsurface.go:171-173`）是客户端侧栏行为，不上线。

#### 5.1.7 通知与前台（`notifier` / `foreground-session`）

| 方法 / 通知 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `notify.dismiss` | `{id}` | `{}` | Y | `Notifier.Dismiss` `notify.go:51` | 延后 |
| `notify.action` | `{id,actionId}` | `{}` | Y | 替代 `Notification.Action func()` `notify.go:40`（§6.3） | ✅ 已实现 |
| ↳ 通知 `toast.posted` | `{Notification 无 Action}` | — | — | `SetHandler` `notify.go:47` | — |
| `foreground.set` | `{sessionId}` | `{}` | Y | `ForegroundSession` `foreground.go:24`（**方向反转**） | ✅ 已实现 |

> **第一个真实用例（2026-11-23）**：`task.delete` 成功后会发一条带 `撤销` 动作的通知
> （`actionLabel:"撤销"`、**稳定的 `actionId:"restore"`**——每个 toast 的 id 不同，所以两个待撤销
> 的删除不会互相串；实现上动作按 **toast id** 存放，不按 actionId 存放）。点撤销把**同一行**原样
> 恢复（id/status/owner/时间戳都在——任务板内部是墓碑隐藏而非物理删除，撤销即清墓碑），而不是
> 新建一条。后端若未实现 `contracts.TaskBoardRestorer`，只发普通通知、不承诺撤销。

> **`notify.action` 的语义（2026-11-23 补死）**：`ActionNotifier.NotifyAction` 注册的回调在
> 收到本方法时**同步执行一次**（回 `{}` 即代表"撤销已发生"），随后该动作的两份登记都被删除，
> 所以同一 `id` 的第二次点击、未知 `id`、`actionId` 不匹配一律回
> `invalid_params`（`data.field` 指 `id` 或 `actionId`，`data.name="action_unavailable"`）——
> 而不是过去的 `1023 action_unavailable` 或静默 `{}`。**幂等性**体现在"只执行一次"，不是
> "重复调用都回成功"。

> `foreground.set` 是唯一"客户端→服务端的状态上报"。用途见契约注释：后台 run 完成时，只有"用户正看着的那个会话"才静音（`foreground.go:7-19`）。不实现它，所有后台完成通知都会响。

#### 5.1.8 上下文时间线（context，§6.3 降级）

| 方法 / 通知 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `context.snapshot` | `{sessionId}` | `{epoch,rev,sessionId,found,context?}` | Y | `ContextInspector.SessionContext` `contextinspect.go:256` | ✅ 已实现 |
| `context.subscribe` | `{sessionId, sinceRev}` | `{epoch,rev,resync}` | Y | 同域机制（§6.5） | ✅ 已实现 |
| `context.unsubscribe` | `{sessionId}` | `{}` | Y | 同上（取消） | ✅ 已实现 |
| ↳ 通知 `context.delta` | `{sessionId, baseRev, rev, found, context?}` | — | — | **整段替换**（见下） | — |
| `context.archive` | `{sessionId,seq}` | `{messages[],truncated}` | Y | `ContextArchiveService` `contextinspect.go:270+` | 延后 |

> **快照永远是全量，没有 `sinceRev`**（2026-11-23 决定）：增量只由 `context.subscribe` 协商
> （`sinceRev` 在订阅上有意义）。契约里已把 `sinceRev` 从 `context.snapshot` / `tasks.snapshot`
> 移除——**声明一个不生效的参数比没有更糟**，它会让替身和客户端都以为存在该能力。

> **域是每会话的**：`context:<sessionId>`。`context?` 与 `found` 一起读：`found:false` 表示
> 该会话还没有任何上下文记录（冷会话/从未运行），客户端显示空态，**不要编造数字**。
>
> **delta 是整段替换，不是字段级 diff**：后端每次读取都从 durable 事件日志**重新折叠**整个视图
> （`contracts.SessionContext`），只暴露一个 `Revision`（"内容没变就别重读"），而 `requests`/
> `events` 是"追加 + 截断"的有界窗口（各 200 条）。所以 delta 直接带**完整的 `context` 对象**，
> 客户端整体替换；变化判定用后端的 `Revision`——两者都为 `found:false`，或 `Revision` 未变，
> 就不发 delta。最坏情况（200 请求 + 200 审计条目）约几十 KB，仍在 `deltaRingBytes` 预算内，
> 超出时走既有 `resync` 机制。
>
> **能力探测走 `initialize` 的 capabilities**：装配里没有 `KeyContext` 时，`initialize` 的
> capabilities **不含** `context`，客户端据此隐藏「上下文」Tab。**不要**自己发明探测方法。
>
> **延迟上限 2s（降级，不是推送）**：`ContextInspector` 没有变更通知。网关只对**已订阅**的
> 会话轮询（`refreshWatchedPrefix("context:")`），无人看的会话零成本。客户端不能假设"运行中
> 上下文读数实时刷新"——它在一次 run 期间会随 `model_call` 事件变化，但到达延迟上限是 2s。

#### 5.1.9 任务板（taskboard，§6.3 降级）

| 方法 / 通知 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `tasks.snapshot` | `{sessionId?}` | `{epoch,rev,tasks:[Task]}` | Y | `TaskBoard.List` `tasks.go:43` / `SessionTaskBoard` `:69` | ✅ 已实现 |
| `tasks.subscribe` | `{sessionId?, sinceRev}` | `{epoch,rev,resync}` | Y | 同域机制（§6.5） | ✅ 已实现 |
| `tasks.unsubscribe` | `{sessionId?}` | `{}` | Y | 同上（取消；省略 `sessionId` = 进程级任务板） | ✅ 已实现 |
| ↳ 通知 `tasks.delta` | `{baseRev, rev, added:[Task], removed:[id]}` | — | — | `diffTasks`（**按 id upsert**） | — |
| `task.status` | `{id,status,sessionId?}` | `{epoch,rev,task}` | Y | `TaskBoard.SetStatus` `:45` | ✅ 已实现 |
| `task.delete` | `{id,sessionId?}` | `{epoch,rev}` | Y | `TaskBoard.Delete` `:46` | ✅ 已实现 |
| `task.create` | `{subject,description}` | `{task}` | N | `:44` | 延后 |
| `task.assign` | `{id,owner}` | `{task}` | Y | `TaskAssigner` `:55` | 延后 |

> **域**：`tasks`（进程级看板）与 `tasks:<sessionId>`（后端实现 `SessionTaskBoard` 时按会话隔离；
> 否则回落进程级）。客户端带 `sessionId` 时落在后者。
>
> **delta 形状是 upsert，不是字段级 diff**：看板的 `status`/`owner`/`updatedAt` 会就地变，
> 而后端没有字段级变更日志，所以**变了的任务整行放进 `added`**，客户端按 `id` upsert；
> 消失的 id 进 `removed`。这样 `snapshot → 逐个应用 delta == 下一个快照` 的等价性仍然成立
> （`domains_test.go` 的 `TestTasksDomainReplayMatchesNextSnapshot`）。
>
> **延迟上限 2s（降级，不是推送）**：`TaskBoard` 没有变更通知。网关自己的写路径
> （`task.status`/`task.delete`）成功后会**立即** `refreshNow` 该域并 bump rev，所以调用方
> 自己的改动是即刻回声的；但**别处**的改动（agent 的 `team_task_*` 工具、另一个客户端）
> 只能靠与 settings 相同的 2s 轮询发现（§6.3）。客户端不得假设"任务刚变就一定有 delta"。
>
> **状态语汇**取契约常量（`pending` / `in_progress` / `completed`），未知值 → `invalid_params`
> （不是静默 no-op）。`task.delete` 是"取消"，`task.status` 回 `pending` 是"停止"。

#### 5.1.10 运行控制（`run`）

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `run.cancel` | `{sessionId}` | `{}` | Y | `ChatService.Cancel` `chat.go:611` | ✅ 已实现 |
| `run.interject` | `{sessionId, text, attachments?}` | `{ok}` | N | 取消 + 立即新开一轮 | ✅ 已实现 |

> `run.interject` 是**插话**（"steering"）：rig 的 agent 每次运行只加载一次历史，
> `TurnHook` 只有 BeforeRun/AfterRun/BeforeTool/AfterTool，没有逐轮注入的接缝，所以
> 无法把消息塞进正在跑的对话。这里的语义是**取消当前轮并立刻用新消息开一轮**——模型能看到
> 已经产生的那半段回答和新的指令，且整个过程在网关内一次完成，客户端不会与 409 抢跑。

#### 5.1.11 团队（`team`，`team.go:134`）

浏览器客户端的团队屏。后端 `contracts.TeamService` 早已齐备，此前没有任何传输方法：
成员列表、派发、发消息、等待、打断、关闭与活动流都取不到。快照由客户端在每次操作后刷新，
并在团队屏打开期间轮询（服务没有变更订阅钩子）。

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `team.snapshot` | — | `{members:[TeamMember], activities:[TeamActivity]}` | Y | `TeamService.Members()` `team.go:138` | ✅ 已实现 |
| `team.spawn` | `{name, prompt, model?}` | `{member}` | N | `:140` | ✅ 已实现 |
| `team.send` | `{id, message, async?}` | `{answer}` / `{ok}` | N | `SendAsync` `:142` / `Send` `:144` | ✅ 已实现 |
| `team.wait` | `{id}` | `{answer}` | Y | `:146` | ✅ 已实现 |
| `team.interrupt` | `{id}` | `{ok}` | Y | `:148` | ✅ 已实现 |
| `team.close` | `{id}` | `{ok}` | Y | `:150` | ✅ 已实现 |
| `team.activity` | `{limit?}` | `{items:[TeamActivity]}` | Y | `TeamActivityLister` `team.go:127` | ✅ 已实现 |

#### 5.1.12 更新（`update`，GitHub Releases）

更新后端（`internal/modules/update`）从模块化时代就有：`contracts.UpdateService.Check` 读 GitHub Releases、
比对版本、选本平台资产，`UpdateDownloader` 校验 SHA-256/大小后暂存，`UpdateInstaller` 写"下次启动安装"
的计划，宿主 `internal/host/install*.go` 负责真正的替换与重启。**缺的一直是浏览器到它的这段线**：
没有方法、没有 capability、也没有自动检查，所以从客户端看这个产品"不会更新"。

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `update.status` | — | `UpdateStatus`（见下） | Y | 网关内存中的最近一次结果 + `PendingInstall()` | ✅ 已实现 |
| `update.check` | — | `UpdateStatus` | Y | `UpdateService.Check` `update.go:194` | ✅ 已实现 |
| `update.download` | — | `UpdateStatus`（带 `staged`） | Y | `UpdateDownloader.Download` + `UpdateInstaller.ScheduleInstall` | ✅ 已实现 |
| `update.install` | `{restart?}` | `UpdateStatus` | N | `UpdateInstaller.PendingInstall` + 宿主安装 | ✅ 已实现 |

`UpdateStatus`：

```jsonc
{
  "currentVersion": "0.3.9",
  "latestVersion": "0.3.10",   // 空 = 还没检查过
  "available": true,
  "notes": "…",                 // release notes（Markdown）
  "url": "https://…",           // 本平台资产的下载地址
  "sha256": "…", "size": 12345,
  "checkedAt": "2026-11-24T10:00:00Z",
  "error": "…",                 // 最近一次检查失败的原因；**不**会把失败说成"已是最新"
  "staged": { "version": "0.3.10", "sha256": "…", "kind": "appimage" }
}
```

规则：

- **`update.check` / `update.download` / `update.install` 属于会阻塞的少数方法**（网络、磁盘），它们跑在各自
  的 goroutine 上，`$/cancel` 可取消（§4.4）。
- 检查失败**不是**协议错误：`error` 字段带原文，界面照实说"连不上 GitHub"。
- `update.install` 在没有暂存包时返回 `1024 update_not_downloaded`，不会重启到同一个版本。
- `restart: true` 时网关写 `<dataDir>/updates/install-now` 标记后关闭自己；宿主在此后才执行安装
  （此时可执行文件句柄已释放），成功则拉起新版本。`restart` 省略 = 保持"下次启动安装"。
- **自动检查**由产品开启（`gateway.Options.AutoCheckUpdates`，serve 产品打开）：启动 30s 后一次，此后每 6h
  一次；发现新版本时发一条通知（`发现新版本 x.y.z（当前 a.b.c）…`），同一版本只通知一次。e2e 夹具不开启，
  以免每个测试网关都去访问 GitHub。
- capability：`update`（来自 `KeyUpdate`，headless 与 serve 都装配了 update 模块）。

#### 5.1.9 连接器（`mcp`，能力页）

| 方法 | 参数 | 返回 | 幂等 | 契约 | 状态 |
| --- | --- | --- | --- | --- | --- |
| `mcp.reload` | — | `{connectors:[{id,name,transport,tools,status,state,error}]}` | N | `MCPReloader.Reload` `plugins.go:56`；连接器页保存 `mcp.servers` 后调用，免重启生效 | ✅ 已实现 |

> 连接器页是**唯一**写 `mcp.servers` 的地方，而 toolkit 只在装配时建连，所以保存后必须
> 显式 `mcp.reload`——否则界面显示「已保存」而连接器永远不出现（round-2 C-03）。
> 返回行里的 `state` 是 `connected|failed|idle`，`error` 是失败原因；客户端不再按中文字符串比较。

### 5.2 T2：对齐桌面所需（22 键 / 约 48 方法）

#### 5.2.1 编排（team / childagents / taskboard / todo）

| 方法 | 参数 | 幂等 | 契约 |
|---|---|---|---|
| `team.snapshot` | — → `{members:[TeamMember]}` | Y | `Members()` `team.go:138` |
| `team.spawn` | `{name,prompt,model}` → `{member}` | N | `:140` |
| `team.send` | `{id,message,async}` | N | `SendAsync` `:142` / `Send` `:144` |
| `team.wait` | `{id}` → `{answer}` | Y | `:146`（长等待，走 `$/cancel`） |
| `team.interrupt` | `{id}` | Y | `:148` |
| `team.close` | `{id}` | Y | `:150` |
| `team.activity` | `{limit}` → `{items}` | Y | `TeamActivityLister` `:128` |
| `agent.children` | `{parentSessionId}` | Y | `ChildAgents` `agentsurface.go:91` |
| `agent.child.get` | `{id}` | Y | `:93` |
| `agent.child.send` | `{id,message}` → `{answer}` | N | `:96` |
| `agent.subagents` | — → `{items}` | Y | `SubagentActivityLister` `agent_activity.go:65` |
| `tasks.snapshot` | `{sessionId?}` | Y | `TaskBoard.List` `tasks.go:43` / `SessionTaskBoard` `:69` — **已实现，见 §5.1.8** |
| `task.create` | `{subject,description}` | N | `:44` — 延后 |
| `task.status` | `{id,status}` | Y | `:45` — **已实现，见 §5.1.8** |
| `task.assign` | `{id,owner}` | Y | `TaskAssigner` `:55` — 延后 |
| `task.delete` | `{id}` | Y | `:46` — **已实现，见 §5.1.8** |
| `todo.snapshot` | — → `{items}` | Y | `TodoService.Todos()` `todo.go:44` |

> `todo` 的诚实限制必须写进客户端：这是**进程级单列表、非按会话**（`todo.go:35-40` 明示）。多标签页看到同一份。

#### 5.2.2 目标（goal，`goal.go:80`）

| 方法 | 参数 | 幂等 | 契约 |
|---|---|---|---|
| `goal.get` | `{sessionId}` | Y | `:82` |
| `goal.create` | `{sessionId,objective,maxGoalRounds}` | N | `:86` |
| `goal.update` | `{sessionId,ref,update}` | N | `:90`（`ref.revision` 是 CAS 令牌，`goal.go:5-9`） |
| `goal.clear` | `{sessionId,ref}` | Y* | `:96` |
| `goal.disarm` | `{sessionId}` | Y | `:103` |
| `goal.subscribe` | `{sessionId}` | Y | `:106` |
| ↳ 通知 | `goal.delta {sessionId, goal}`（整快照，小） | | |

> `AdmitRound`（`goal.go:100`）**不上线**：引擎侧续跑计数，不是用户动作。
> **重启后 goal 自动 disarm**（`goal.go:11-14`）→ 客户端不能缓存"armed"跨 `epoch`。

#### 5.2.3 自动化与统计

| 方法 | 参数 | 幂等 | 契约 |
|---|---|---|---|
| `automation.snapshot` | — → `{tasks:[AutomationTask]}` | Y | `List` `automation.go:202` |
| `automation.create` / `.update` / `.delete` | `{task}` / `{task}` / `{id}` | N/N/Y | `:204,206,209` |
| `automation.runNow` | `{id}` | N | `:211` |
| `automation.subscribe` | — | Y | `:214` |
| `automation.status` | — → `AutomationStatus` | Y | `:244` |
| `automation.retry` | — | N | `:255` |
| `insights.get` | `{days,sessionLimit,forceRefresh}` → `{Insights}` | Y | `insights.go:231` |

> `AutomationTask.Schedule` 是**字符串**（`automation.go:28-30`），线上只传它；`AutomationSchedule` 那套未导出字段（`:291-305`）留在网关内解析，绝不序列化。
> `insights.get` 是"重活"（契约明示可在后台跑，`insights.go:227-230`）→ 支持 `$/cancel`，客户端不阻塞首屏。

#### 5.2.4 扩展：插件 / MCP / 技能 / 记忆

| 方法 | 参数 | 幂等 | 契约 |
|---|---|---|---|
| `plugins.snapshot` | — → `{servers:[MCPPlugin]}` | Y | `MCP()` `plugins.go:37` |
| `plugins.refresh` | — | N | `:40` |
| `mcp.reload` | — → `{connectors:[…]}` | N | `MCPReloader.Reload` `:56`；**已接线**（网关 `reloadConnectors`，连接器页保存后调用，免重启生效） |
| `mcp.tools` | `{server}` → `{tools:[MCPTool]}` | Y | `MCPToolCatalog.Tools` `:74` |
| `mcp.tool.enable` | `{server,tool,enabled}` | Y | `:75` |
| `mcp.auth.status` | `{cfg}` | Y | `MCPAuthService.Status` `:182`（同步、无 I/O） |
| `mcp.auth.prepare` | `{cfg}` → `{flow:{authUrl,redirectUri,...}}` | N | `:186` |
| `mcp.auth.wait` | `{cfg,flow}` | N | `:189` |
| `mcp.auth.cancel` | `{cfg,flow}` | Y | `:192` |
| `mcp.auth.revoke` | `{cfg}` | Y | `:195` |
| `skills.snapshot` | — → `{skills,roots,warnings}` | Y | `SkillsService` `skills.go:16`（=`rig.SkillStore.List` rig `extensions.go:82`）+ `Roots` `:20` + `SkillWarnings` `:27` |
| `skill.get` | `{name}` | Y | rig `extensions.go:83` |
| `skill.create` / `.update` / `.delete` | `{draft}` / `{name,draft}` / `{name}` | N/N/Y | `SkillsEditor` `skills.go:72,76,79` |
| `memory.list` | `{workspaceRoot,query,types,limit}` | Y | `memoryv2.go:44` |
| `memory.get` | `{workspaceRoot,uri}` | Y | `:46` |
| `memory.write` | `{workspaceRoot,draft}` | N | `:48` |
| `memory.delete` | `{workspaceRoot,uri}` | Y | `:50` |

> MCP OAuth 在浏览器模型下**变简单**：`Prepare` 返回的 `AuthURL`（`plugins.go:166-174`）直接 `window.open`，回环回调仍在网关侧（`Wait`）。契约原本"UI 开浏览器"的要求（`plugins.go:167`）现在天然满足。

#### 5.2.5 会话附属读模型

| 方法 | 参数 | 幂等 | 契约 |
|---|---|---|---|
| `context.snapshot` | `{sessionId}` → `{SessionContext,found}` | Y | `SessionContext` `contextinspect.go:259` |
| `context.archive` | `{sessionId,seq}` | Y | `ContextArchiveService` `:344` |
| `executions.history` | `{sessionId,limit}` | Y | `execution.go:113` |
| `jobs.snapshot` | — → `{jobs:[BackgroundJob]}` | Y | `JobService.Jobs()` `agentsurface.go:154` |
| `job.output` | `{id}` → `{text,truncated}` | Y | `:158`（64 KiB 上限 `:129`） |
| `job.kill` | `{id}` | Y | `:156` |
| `session.log.export` | `{sessionId}` → `{blobId}` | Y | `SessionLogExporter` `sessionlog.go:27` |

#### 5.2.6 运维/诊断

| 方法 | 参数 | 幂等 | 契约 |
|---|---|---|---|
| `import.scan` | `{dir}` → `{ImportPreview}` | Y | `import.go:42` |
| `import.run` | `{dir}` → `{ImportResult}` | N | `:45` |
| `modules.snapshot` | — → `{modules:[ModuleInfo]}` | Y | `ModuleLister` `modules.go:19` |
| `tools.snapshot` | — → `{names,total}` | Y | `ToolCatalog` `toolcatalog.go:19,22` |
| `sandbox.status` | — → `SandboxStatus` | Y | `SandboxService.Status` `sandbox.go:44` |

> `SandboxService.Runner()`（`sandbox.go:40`）返回 `rigsandbox.Runner` 接口值——**永不上线**；只有 `Status/Available` 的数据面上线。

### 5.3 统计

| 层 | 键 | 方法数【估算】 | 其中通知 |
|---|---|---|---|
| T1 | 13 | 58 | 8 |
| T2 | 22 | 48 | 4 |
| **合计** | **35** | **≈106** | **12** |
| 不上线 | 16 (T3) | — | — |

对照：`contracts` 非测试有 ~194 个接口方法签名；上线的 ~106 个 = **55%**。差额主要是"可选半被合并"（如 13 个 chat 可选半合成同一 `session.*` 命名空间）与"阻塞方法只取其等待态"（`Ask`/`Review`/`Present`）。

---

## 6. 快照与差值协议（核心章节）

### 6.1 三个版本概念

| 概念 | 类型 | 生命周期 | 谁维护 | 变化时客户端做什么 |
|---|---|---|---|---|
| `epoch` | 16 hex | 一次网关进程 | 启动时随机 | **丢弃全部本地状态，全量 resync** |
| `rev`（每域） | uint64 单调 | 一次 epoch 内 | 网关 hub 单 goroutine | 用于 delta 拼接校验 |
| `seq`（会话事件日志） | int64 | 持久（SQLite） | sessionstore | 用于 trajectory/归档的**事件**分页与断点续读 |

`seq` 是既有概念：`rig.SessionEvent.Seq`（rig `extensions.go:148`）+ `SessionLog.Events(from,to)`（`:158`）+ `ContextArchive.Seq`（`contextinspect.go:304`，注释明确"durable sequence number"）。**不要**用它当协议 revision（它是持久事件序号，跨重启连续，但和"读模型版本"不是一回事）。**`rev` 是协议层新造的内存计数器。**

### 6.2 域（domain）划分与 rev 维护

| domain | 快照内容 | rev 何时 +1 | 数据源（订阅点） |
|---|---|---|---|
| `session.list` | `[]ChatSessionInfo` | 任意会话变更（含标题/running/unread/pin/archive） | `chat.Subscribe` `session.go:612` |
| `session:<id>` | 一个 `ChatSession`（含 messages） | 该会话任何变更 | 同上（过滤 sessionID） |
| `approvals` | `{mode, pending[]}` | 待批集合变化 | `approvals.Subscribe` `approval.go:128` |
| `ask` | 待回答的提问 | 提问/回答/取消 | `interaction`（真推送） |
| `plan` | 计划审阅状态 | 进入/提交/退出计划 | `interaction`（真推送） |
| `artifacts` | 产物列表（append-only） | 新产物 | `ArtifactPresenter`（真推送） |
| `prompts:<id>` | `{prompts:[QueuedPrompt]}`（该会话的队列） | 入队/移除/队首派发 | **无订阅**（`PromptQueue` 无通知，轮询 + 写路径立即刷新） |
| `settings` | `{rows}` | 任一行写入 | **无订阅**（§6.3：按 2s 轮询落地，延迟上限 2s） |
| `tasks` / `tasks:<id>` | `{tasks:[Task]}`（进程级 / 按会话） | 看板内容真的变了（网关写路径立即、其余靠轮询） | **无订阅**（`tasks.go`，§6.3 降级，延迟上限 2s） |
| `context:<id>` | `{found, context}`（折叠的上下文时间线） | 后端的 `Revision` 变了 | **无订阅**（`contextinspect.go`，只轮询已订阅会话，延迟上限 2s） |
| `workspace.list` | `[]Workspace` + `currentId` | 增删改/切换 | **无订阅**（按需快照 + 客户端显式 invalidate） |
| 其余（team/jobs/plugins/insights/skills/memory/automation/goal） | 各自列表 | 网关在每次写方法成功后 +1；读模型本身无订阅 | 请求—响应 + 懒快照（automation 另有 `GET /api/automation` 读模型，见 §2.6） |

> **没有** `messages`、`plans`（复数是笔误）与 `team` 域：消息走 `session:<id>` 的 ops，计划是单数 `plan`，团队走 `team.snapshot` 请求—响应 + 活动流。

**维护规则（hub 单 goroutine 独占）**：

```
onServiceChange(domain):
  cache = caches[domain]            // 上一次的快照副本
  next  = buildSnapshot(domain)     // 调服务（在 worker goroutine 上，见 §7）
  rev++
  delta = diff(cache, next)         // 通用 diff 或 §6.6 的 session 专用 diff
  ring[domain].push({baseRev:rev-1, rev, delta})   // 有界环，默认 256 条 / 256 KiB
  cache = next
  broadcast(delta) to subscribers[domain]
```

**为什么 rev 由网关造而不复用后端**：后端根本没有 read model revision（`ChatService` 只给"变了"信号 `chat.go:575-577`）。网关必须自己持有"上一版快照"才能算差值——这也是 `cache` 存在的原因。

### 6.3 五条无载荷失效信号 → 带差值的通知

| 现状（桌面） | 网关升级 | 差值怎么算 |
|---|---|---|
| `chat.Subscribe(func(sessionID))` `chat.go:577`，UI 收到后 `Post→Invalidate→下一帧全读`（`session.go:612-640`） | `session.delta {sessionId, baseRev, rev, ops[]}` + `session.list.delta {baseRev, rev, list.diff}` | **逐条消息 diff**：见 §6.6（复用 ACP `seed/observe` 算法） |
| `approvals.Subscribe(func())` `store.go:141`（无参） | `approvals.delta {baseRev, rev, added[], removed[]}` | 集合小（通常 0–2 条），直接推**全量 pending** 也可接受；用 `added/removed` 以 id 为键 |
| `automation.Subscribe(func())` `automation.go:214`（无参） | `automation.delta {baseRev, rev, added[], removed[], changed[{id,fields}]}` | 以 task.ID 为键 |
| `goal.Subscribe(func(sessionID))` `goal.go:106` | `goal.delta {sessionId, goal\|null}` | 整快照（一个 goal 就几百字节），不算 diff |
| `notifier.SetHandler(func(Notification))` `notify.go:47` | `toast.posted {id, level, text, sessionId, actionLabel?, actionId?}` | 本来就是带载荷的单向事件；**唯一改动**：`Action func()`（`notify.go:40`）序列化成 `actionId`，客户端点按钮回 `notify.action{id,actionId}` |

**两处必须新增订阅（契约缺口）**：

| 缺口 | 位置 | 方案 |
|---|---|---|
| `SettingsService` 无变更通知 | `contracts.go:346-350`（只有 Get/Set/All） | 网关在**自己的**写路径上 bump `settings` rev，并对 `Set/Delete` 加进程内 `sync.RWMutex` + 变更回调注册表（`internal/gateway/settings_watch.go`）。**不改 contracts**：多个客户端 + 网关内部写入都能被它看到；外部改文件（用户手改 settings.json）不感知——记为已知限制 |
| `WorkspaceService` 无变更通知 | `workspace.go:37-59` | 同上：网关包装写方法后 bump；`Current()` 变化频率低，客户端切工作区时主动 `workspace.snapshot` 即可 |

> **实现状态（S1e，internal/gateway）：写命令 `session.create` 与 `settings.set` 上线。**
> `session.create {title}` → `{epoch, sessionId, rev}`：调用 `ChatService.NewSession`，
> **返回前**同步刷新 `session.list` 域（`refreshNow`），所以侧栏 delta 不会晚一拍；标题
> 由传输层设护栏（≤200 字符、不含控制字符；空标题合法，后端自行 trim）。`settings.set
> {key, value}` → `{epoch, rev}`：调用 `SettingsService.Set` 后**立即**重建 `settings` 域
> 并发 delta（不等 §6.3 的 2s 轮询——写入方就在网关进程内）；写 `agent.permission` 时还会
> 立刻刷新 `approvals` 域（审批模式由该行派生）。key 的**格式**被校验（非空、≤128 字节、
> 首字符字母数字、仅 `[A-Za-z0-9._-]`），值 ≤64 KiB；"未知 key"无法判定（设置存储是通用
> KV，模块各自读写自己的行），因此不拒绝——这是契约本身的性质，不是遗漏。
> 本期明确不实现 `message.send`（与 `POST /api/chat/run` 重复）与 workspace 写方法
> （检查器只读）。

> **实现状态（S1d，internal/gateway）：prompt queue 上线为 `prompts:<sessionID>` 域。**
> `prompts.snapshot {sessionId}` 返回该会话的 inbox（oldest first），`prompts.delta`
> 为 `{sessionId, baseRev, rev, added[], removed[], reordered?, order?}`；命令是
> `prompts.enqueue {sessionId, prompt}` 与 `prompts.remove {sessionId, id}`。**队列真相
> 在后端**（`contracts.PromptQueue`，agent 模块实现）：网关只读它、不另存副本，所以
> 重启、第二个标签页和桌面端看到的是同一个 inbox。写路径（enqueue/remove 成功）立即
> bump 该域 rev 并发 delta；因为 `PromptQueue` **没有变更通知**，后端自身的改动（一次
> run 结束后自动开始队首 prompt）靠与 settings 相同的轮询发现，**延迟上限同为 2s**。
> 错误：空 prompt → `invalid_params`，未知会话 → `1001 session_not_found`，重复 remove
> 幂等（成功）。原 web mock 里自造的 REST `/api/sessions/:id/prompts` 不是网关路由，
> 客户端应改用本域。

> **实现状态（S1c，internal/gateway）**：`settings` 与 `workspace.list` 两个域按"轮询 + 差值"落地，**没有假装是推送**：网关每 2s 读一次 `SettingsService.All()` / `WorkspaceService.List()+Current()`，内容真的变了才 +1 该域 `rev` 并广播 `settings.delta` / `workspace.list.delta`；不变则零流量。因此这两域的**延迟上限是 2s**，客户端不能假设一次设置写入会立刻回声——这是契约没有通知能力的直接后果，不是实现偷懒。副作用是**用户手改 `settings.json` 也能在 2s 内被看到**（比上面"不感知"的原始限制更强）。`approvals` 走真推送（`Approvals.Subscribe`），另外每 2s 也刷新一次，因为审批模式（`mode`）来自 settings 行：模式变化会让该域发 `resync`，客户端重新取快照。**上下文时间线（`context:<id>`）也是轮询**：`ContextInspector` 无通知，网关对已订阅会话每 2s 重读一次折叠视图，用后端 `Revision` 判断是否真的变了（不变则零流量）。**任务板（`tasks`）同样按"轮询 + 内容变才 bump"落地**：`task.status`/`task.delete` 这类网关写路径立即刷新并回带新 rev，其余改动（agent 的 `team_task_*` 工具、第二个客户端）延迟上限 **2s**。

### 6.4 长会话与大数据：分页与裁剪【提案】

**问题**：`ChatSession` 返回**整个会话**（`chat.go:128-144`），`ChatMessage.Text` 是完整正文，`Attachment.Data` 是 `[]byte`（`chat.go:415`）。长会话 + 图片 = 数 MB/帧。

| 手段 | 规则 | 依据 |
|---|---|---|
| **快照尾窗口** | `session.snapshot` 默认只回最近 **60 条消息** + `truncated:true` + `oldestMessageId` | 虚拟列表本来就只渲染视口；桌面 `ui.List` 也是虚拟化 |
| **向前分页** | `session.messages.before {sessionId, beforeMessageId, limit≤100}` | 复用 `MessageOrdinalLocator`（`search.go:47-56`）作为稳定游标：`ordinal:N` 跨重启可用 |
| **附件一律 blob 化** | 快照里 `Attachment` 只留 `{id,name,mime,bytes,blobId}`；`Data` 永不上快照 | `chat.go:414-422`：`Bytes` 字段本来就是为"未加载内容的恢复芯片"准备的，正好复用 |
| **blob 端点** | `POST /api/blobs?name=<文件名>` 上传原始二进制体（≤10 MiB），`GET /api/blobs/{id}` 取回；实现不支持 Range（不要依赖分段读） | `Attachment.DataURL()`（`chat.go:440-445`）已有 base64 先例，但 base64 膨胀 33%，二进制端点更省 |
| **blob 上限** | 单 blob ≤ 10 MiB（图片），与 ACP 的 `MaxPromptImageBytes` 对齐 | rig `acp/server/server.go:55`；`acp.go:43` 注释 |
| **工具载荷裁剪** | `ToolCall.Arguments` 已"bounded in the transcript"（`contracts.go:116-118`）；`Summary` 保持后端边界 | 现成边界，不额外裁 |
| **job 输出** | 64 KiB + 截断标记（`agentsurface.go:126-133`） | 现成 |
| **归档** | 8 KiB/条、200 条、1 KiB/参数（`contextinspect.go:327-334`） | 现成 |
| **压缩** | WS 开启 `permessage-deflate`；不得压缩 `/api/blobs/*`（已压缩的图） | — |
| **frame 上限** | 16 MiB 沿用（rig `acp/acp.go:26`）；超限 → `error 1025 backpressure` | — |

**裁剪的铁律**：截断必须显式（`truncated:true` + 缺失量），不能静默。现有代码已经贯彻这条（`contextinspect.go:320-322` "never a silent loss"、`insights.go:214-219` "The UI must say so instead of presenting a complete total"），客户端照抄这个语义。

### 6.5 断线续传

```
客户端重连
 → initialize（拿新 epoch）
 → epoch 变? 是 → 丢弃全部本地状态，逐域 snapshot（走 §6.4 尾窗口）
              否 → 对每个已订阅域：
                    session.subscribe {sessionId, sinceRev: localRev}
                    → 网关查 ring[domain]:
                       sinceRev 在环内 → 回放 [sinceRev+1 .. rev] 的 delta 序列（合并成一条或按序多帧）
                       sinceRev 太旧/不在环内 → {resync:true} → 客户端改发 session.snapshot
 → ready
```

| 情形 | 行为 |
|---|---|
| 断线 < 环深度 | 增量补齐，零全量 |
| 断线较久 | 全量快照（尾窗口），已加载的向前分页保留 |
| 网关重启 | `epoch` 变 → 全量 |
| 浏览器刷新 | 同上（内存状态丢），但**run 没停**——重连后 `running:true` 自然反映 |
| 环的设计 | 每域独立环；`session:<id>` 环只在有订阅者时保留（无订阅者 → 不缓存、不 bump，省内存） |

### 6.6 会话差值的具体算法（复用 ACP）

**这是全协议唯一需要"逐条 diff"的地方**，因为流式文本每分钟可能变几百次。

复用 rig `acp/server/stream.go` 的 `promptState.seed/observe`（`stream.go:45-77`）思想：

| 变化 | op | 判据 |
|---|---|---|
| 助手文本增长 | `{op:"msg.delta", messageId, textAppend:"..."}` | 新旧 `Text` 前缀相等且更长（ACP 同款 `stream.go:79-85`） |
| 推理分段增长 | `{op:"msg.delta", messageId, reasoningAppend:{iteration, textAppend}}` | 按 `ReasoningSegment.Iteration` + `TextOffset`（`chat.go:112-125`）定位 |
| 工具出现 | `{op:"msg.tool.upsert", messageId, tool:{...}}` | 以 `ToolCall.ID` 为键（`contracts.go:112`） |
| 工具状态/结果变化 | `{op:"msg.tool.upsert", ...}`（同键覆盖；仅 Status/Summary/EndedAt 变） | 同 ACP 的 `toolStates` 映射（`stream.go:34-36`） |
| usage / truncated / error 变化 | `{op:"msg.patch", messageId, fields:{...}}` | JSON Merge Patch |
| 新消息 | `{op:"msg.add", index, message}` | — |
| 消息删除（regenerate/compact 重写历史） | `{op:"msg.reset", messages:[...]}` | 一次性全量替换 |
| 会话级字段（title/model/running/interrupted） | `{op:"session.patch", fields:{...}}` | — |

**为什么不用"整条消息重发"**：一条 8 KB 的回复，流式 400 个 token → 重发 ≈ 1.6 MB vs 增量 ≈ 8 KB。这就是 ACP 已经在做的事。

**为什么保留 `reset`**：`Regenerate`（`chat.go:480`）和 `CompactNow`（`:284`）会**整体替换历史**——契约里 `ReplaceHistory` 就是这个语义（`store.go:57-59`）。此时 diff 没有意义。

---

## 7. 主线程亲和的重定义

### 7.1 结论【决策】

**网关没有"UI 线程"。** 采用 **每请求 goroutine（RPC handler 池）+ 服务自身同步 + 单一 hub 广播 goroutine**。

理由（三条，都是契约里写死的）：

| # | 证据 | 含义 |
|---|---|---|
| 1 | `ChatService`：「every method is safe to call from any goroutine. Subscribers are called synchronously from the agent goroutine」`chat.go:551-553` | 服务层本来就线程安全 |
| 2 | `Store`：「All methods are safe for concurrent use」`store.go:47`；`Approvals.Subscribe`：「Callbacks run synchronously from any goroutine」`store.go:139-140` | 同上 |
| 3 | `InsightsService`：「safe to call from any goroutine … a UI calls it from a background goroutine and posts the result back」`insights.go:227-230` | 契约预设"调用方有 UI 线程需要 post 回来"——网关把这个角色换成了 hub |

**"UI thread only" 的四处约束，本质是 uiroot 单线程帧循环的消费者约束，不是服务约束**，逐条处理：

| 约束（原文） | 位置 | 网关处理 |
|---|---|---|
| 「`Enabled` and `Run` may be called from the UI thread only」 | `contracts.go:159-160` | `command.invoke` 在 worker goroutine 执行，带 30s 超时（估）；`Enabled` 预计算成 `commands.snapshot` 里的 `enabled:bool`，运行期不再调 |
| 「Call it on the UI thread」（`SessionFocus.FocusMessage`） | `search.go:76` | **不上线**：纯客户端滚动定位 |
| 「`Reveal` … called on the UI thread in the frame the page is built」 | `contracts.go:71-77` | **删除该方法**：客户端自己在 `settings.pages` 快照里按 `anchorKey` 展开/滚动（`SettingsSearchItem.AnchorKey` `:95`） |
| 「`Status` may be called from the UI thread」/「`Prepare` … must run off the UI thread」 | `plugins.go:181-185` | 区别保留为**协议形态**：`mcp.auth.status` 同步内联返回（无 I/O）；`mcp.auth.prepare/wait` 异步 + `flowId` + `$/cancel` |

### 7.2 五条必须由网关 provide 的服务（新实现）

| 键 | 桌面提供者 | 网关提供者 | 语义映射 |
|---|---|---|---|
| `KeyUserInteraction` `interaction.go:25` | uiroot `host.go:854` | `gateway/interaction.go` | `Ask` 发布 `ask.opened` → 等 `ask.answer`/`ask.cancel`（或 ctx 结束）；`SetHandler` 不再需要（事件由 hub 广播） |
| `KeyArtifactPresenter` `:60` | uiroot `host.go:857` | `gateway/artifacts.go` | `Present` 记录 + 广播 `artifact.presented`，**立即返回**（契约要求立即返回 id，`:57-59`） |
| `KeyPlanReviewer` `:76` | uiroot `host.go:860` | `gateway/plan.go` | `Review` 发布 `plan.reviewing` 并阻塞等 `plan.exit`；`State` 从内存读 |
| `KeyNotifier` `notify.go:45` | uiroot `host.go:863` | `gateway/notify.go` | `Notify` → `toast.posted` 广播；`NotifySystem`/`NotifySystemFor` 转给**托盘壳**（IPC 或直接 `osascript`/`notify-send`？→ 提案：托盘壳轮询状态接口，网关只标记 `wantSystem:true`） |
| `KeyForegroundSession` `foreground.go:20` | uiroot `host.go:839`（读当前屏幕） | `gateway/foreground.go` | 最近一次 `foreground.set` 的 sessionId；多标签页时取"最近上报且仍连接"的那个 |

> **这五条是 headless 装配今天彻底缺失的部分**（`host.go:825-827` 提前 return）。ACP 的"批准/提问/计划缺席"有一半原因在这里——不是设计取舍，是没 provide。写进本文是为了避免 serve 重犯。

### 7.3 唯一保留的线程规则

| 规则 | 理由 |
|---|---|
| **广播写入只能发生在 hub 的单一 goroutine** | 等价于桌面 `UIRuntime.Post` 的队列语义（`internal/uiroot/runtime.go:51-59`：入队 + 请求一帧）。多 goroutine 直接写 WS 会乱序，导致客户端 `baseRev` 校验失败 |
| **任何服务调用不得发生在写路径上** | 写路径只做"取快照 → diff → 入队"；服务调用在 worker 完成后再投递结果。否则一个慢服务（`insights`/`import`）会卡死全场广播 |
| **`rev` 只在 hub 内递增** | 否则 delta 序列有洞 |

---

## 8. 扩展点的数据化（`BuildFunc` 的替代）【提案 · 待 Stage 0 定稿】

### 8.1 问题

`contracts` 的 UI 贡献模型是**闭包**：

| 类型 | 定义 | 不能上网的原因 |
|---|---|---|
| `BuildFunc` | `contracts.go:17` `func(c *ui.Context)` | 要 `*ui.Context` |
| `ScreenSpec` | `:20-26`（`Build BuildFunc`） | 同上 |
| `SlotItem` | `:30-35` | 同上 |
| `SettingsPageSpec` | `:38-78`（`Build`/`Footer`/`Dirty`/`Discard`/`Reveal`） | 5 个函数字段 |
| `ToolRenderer` | `:152-157`（`Build func(c, call)`） | 同上 |
| `CommandSpec` | `:161-182`（`Enabled`/`Run`） | 同上 |

### 8.2 替代方案：**白名单组件 + 数据描述**

**决策原则**：不追求"任意 Go 闭包都能数据化"（那等于重新发明一个 UI 框架）。改为：
1. 客户端**预置**一组组件（`session`/`automation`/`insights`/`settings` 等页面骨架 + 卡片类型）；
2. 网关只发**结构性数据 + 引用键**（`kind` + `payload`）；
3. 未知 `kind` → 客户端降级渲染（纯文本行），不报错。

### 8.3 五类 schema

**① 屏幕（`ScreenSpec` `:20-26`）**

```json
{"id":"automation","titleKey":"screen.automation","order":30,
 "visible":true,"route":"/automation","client":"automation"}
```
> `client` 指向客户端已实现的组件；网关不发渲染逻辑。`Visible func()` 变成 `visible` 布尔，由网关按"能力是否具备"预计算（例：无 `KeyAutomation` → `visible:false`）。

**② 槽位（`SlotItem` `:30-35`）**

```json
{"slot":"session.status","id":"approval.count","order":10,
 "kind":"badge","payload":{"labelKey":"slot.approvals","value":2,"tone":"warning"}}
```
> 槽名沿用现有常量（`contracts.go:201-252`，约 30 个）。`kind` 是受控词表：`badge`/`text`/`button`/`menu`/`banner`/`card`。

**③ 设置页（`SettingsPageSpec` `:38-78`）**

```json
{"id":"appearance","section":"settings.section.ui","titleKey":"settings.appearance","order":10,
 "page":"appearance","keywords":["主题","theme","字号"],
 "searchItems":[{"id":"font","labelKey":"settings.chatFont","keywords":["字号"],"anchorKey":"appearance.font"}],
 "footer":{"kind":"actions","items":[{"id":"discard","labelKey":"action.discard","tone":"neutral"},
                                     {"id":"save","labelKey":"action.save","tone":"primary"}]},
 "dirty":false}
```
> `Dirty func()` → `dirty` 布尔（网关从"草稿 vs 已保存"算）；`Discard func()` → `settings.page.discard {pageId}`；`Reveal` 删除（客户端本地滚动）。

**④ 工具卡（`ToolRenderer` `:152-157`）**

```json
{"tool":"write_file","order":10,"card":"file",
 "fields":{"path":"arguments.path","content":"arguments.content"},
 "summary":{"kind":"lines","from":"result"}}
```
> 工具卡从"Go 渲染函数"变成"**卡片类型 + 字段取值路径**"。受控卡片类型首批（对齐 `internal/ui/toolcards` 的现有分类）：`file`/`diff`/`shell`/`search`/`todo`/`team`/`generic`。
> `ToolCall` 数据本身已经是 JSON 友好结构（`contracts.go:111-148`），无需改造。

**⑤ 命令面板（`CommandSpec` `:161-182`）**

```json
{"id":"session.new","titleKey":"command.newSession","hintKey":"command.newSession.hint",
 "order":10,"enabled":true,"shortcut":"Cmd/Ctrl+N","category":"actions",
 "keywords":["新建","new"],"invoke":"session.create","args":{}}
```
> `Run func() error` → `invoke` + `args`，由客户端发标准 RPC。`Enabled func() bool` → `enabled` 预计算（状态变化时由 `commands.delta` 更新）。
> 现有宿主自动生成的两类命令（`goto`/`settings` 分组，见 `commands.go:12-19`）由网关按屏幕/设置页清单生成，与桌面一致。

### 8.4 版本化与降级

| 机制 | 规则 |
|---|---|
| `uiSchemaVersion` | 在 `initialize.capabilities` 里双向声明（`uiSchemaVersion:1`） |
| 未知 `kind`/`card` | 客户端渲染 `<Unsupported kind=...>` 占位（一行灰字），**不抛错、不空白** |
| 缺失能力 | `capabilities` 不含该能力 → 对应槽位/命令不下发（网关侧过滤），与桌面"键缺失即隐藏"一致（`workspace.go:72-75`） |
| 迁移期 | 保留 `internal/ui` 作为回退路径（AGENTS.md §0 冻结区），两套并存直到 Stage 3 |

### 8.5 与 §5 方法表的关系

本节五类 schema **尚未落到 §5 的方法清单**：是否新增 `ui.snapshot`（一次下发 screens+slots）与 `commands.snapshot`，取决于 Stage 0 对本节的裁决。
若裁决是"UI 结构全部写在客户端 TS、网关只发 domain 数据"，则本节整体删除，§5 方法数不变（≈106）；若保留，§5 增 2 个方法（≈108）。**这是本文档唯一一处有意保留的悬空。**

---

## 9. 与 ACP 的关系

### 9.1 定位差异

| 维度 | ACP | 网关 |
|---|---|---|
| 传输 | stdio（`acp.go:162-175`） | WS（`io.ReadWriter`） |
| 客户端数 | **1**（进程独占） | N（多标签页） |
| 方法数 | 6（`acp.go:59-65`）+ 3 通知（`:46-48`） | ~106 + 12 通知 |
| 契约覆盖 | `chat` 的一个窄子集（~15%） | T1+T2（~55% 方法面） |
| 批准 | **不投影**（装配钉 `auto`，`headless.json:22-27`） | 一等公民（§5.1.3） |
| 读模型 | `session/list` 3 字段（`acp.go:311-315`）、`session/load` 只有文本（`:377-387`） | 全字段 + 尾窗口 + blob |
| 无载荷信号 | 无（`Prompt` 内自订阅，`chat_adapter.go:63-68`） | 升级为带 delta（§6.3） |

### 9.2 可共用

| 资产 | 位置 | 复用方式 |
|---|---|---|
| 换行分帧 + 校验 + 错误码 + 分发 | rig `acp/acp.go:1-8,26-37` | 直接复用：把 `io.Reader/Writer` 换成 WS 连接包装 |
| **快照差值引擎**（`seed`/`observe`） | rig `acp/server/stream.go:45-77` | **算法照抄**，DTO 换成我们的（`stream.go:22-36` 的 `toolStates`/`toolIDs` 键控策略完全适用） |
| 图像块解码 + 10 MiB 上限 | rig `acp/server/images.go`（124 行）、`server.go:55` | 复用上限常量与校验 |
| 方法映射骨架（switch + 错误构造） | `acp.go:180-191`、`:396-404` | 形状照抄 |
| **已知语义**：请求返回即 run 结束 | `chat_adapter.go:87-95` | 网关**不同**：`message.send` 立即返回 `messageId`，run 状态走 delta。这是有意的分叉（浏览器要能"发完就继续操作"） |

### 9.3 必须分叉

| 点 | 原因 |
|---|---|
| `message.send` 的返回语义 | ACP `Prompt` 阻塞到 run 结束（`chat_adapter.go:89-95`）；网关不阻塞 |
| 批准 | ACP 靠装配 `auto` 绕开；网关必须真投影 |
| 单会话假设 | `chatAdapter.Prompt` 一次只跟一个 session（`chat_adapter.go:63-68`）；网关要 N 个并发订阅 |
| 客户端状态上报 | ACP 无（`foreground.set`、`settings.subscribe` 是网关新增） |
| `session/load` 的形状 | ACP 只回 user/assistant 纯文本（`acp.go:377-387`）；网关要完整读模型 |

### 9.4 代码组织建议【提案】

```
internal/gateway/
  server.go       HTTP + WS 装配、静态资源、/auth、/blob
  hub.go          订阅表、per-domain rev、delta ring、单写 goroutine
  proto.go        JSON-RPC 帧、错误码、能力协商
  methods_*.go    按域分组的方法表（session/approvals/settings/workspace/...）
  delta.go        通用 list.diff / patch；session 专用 msg.* ops
  services.go     provide 五条宿主服务（interaction/artifacts/plan/notify/foreground）
  port.go         端口协商 + gateway.json + flock
  auth.go         token、cookie、Origin/Host 校验
```
不在 `internal/gateway` 复用 ACP 的模块描述符（ACP 仍是独立产品入口 `--acp`，`host.go:347-385`）。

---

## 10. 实施顺序【提案】

| Stage | 内容 | 验收 | 规模【估算】 |
|---|---|---|---|
| **0** | 本文件定稿 + §8 五类 schema 定稿 | 评审通过 | — |
| **1** | 骨架：port/gateway.json/flock/token/auth + WS + `initialize` + `session.list.snapshot` + `session.snapshot` + `session.delta`（只 text/tool） | 浏览器能看到会话列表与流式回复 | 1,200–1,600 行 |
| **2** | T1 剩余：批准/提问/计划/产物/设置/工作区/文件/notifier/foreground | 桌面能做的 T1 全能做 | 2,800–4,400 行 |
| **3** | T2 全量 + 五类扩展点数据化落地 | 完整等价 | 3,000–4,000 行 |
| **4** | `internal/ui` 退役 | AGENTS.md §0 的 Stage 3 | — |

（Stage 1–3 合计与盘点报告的 T1+T2 估算 8k–10k 行一致。）

---

## 附录 A：证据索引（本文引用的代码事实）

| 事实 | 位置 |
|---|---|
| 51 个服务键 / ~194 方法 / 7,085 行非测试契约 | `.cache/pivot/contracts-projection.md` §1 |
| `ChatService` 线程安全 + 订阅语义 | `internal/contracts/chat.go:547-578` |
| `chat.Session()` 返回整会话 | `internal/contracts/chat.go:128-144` |
| `Attachment.Data []byte` | `internal/contracts/chat.go:414-422` |
| `Approvals.Request` 阻塞 | `internal/contracts/store.go:131-137` |
| `PlanReview` 状态机 | `internal/contracts/interaction.go:65-81` |
| `Notification.Action func()` | `internal/contracts/notify.go:35-41` |
| 订阅点（五个后端源） | `contracts.ChatService.Subscribe`、`Approvals.Subscribe`、`SettingsService`（轮询）、`WorkspaceService.List/Current`（轮询）、`ContextInspector.SessionContext`（轮询） |
| 域快照/差值/合并 | `internal/gateway/domains.go`、`internal/gateway/delta.go`、`internal/gateway/hub.go` |
| 装配产品清单 | `internal/assembly/assembly.go:48-70`（`Products()` / `AllProducts()`） |
| headless 批准被钉 auto | `internal/assembly/assemblies/headless.json` |
| 单实例（数据目录 flock） | `internal/gateway/lock_unix.go` / `lock_windows.go`（原生的 `singleinstance.go` 已随 S5 删除） |
| 托盘实现（尽力而为） | `internal/host/tray.go`、`tray_shell.go` |
| 数据目录解析 | `internal/host/host.go:145-153` |
| 日志路径 | `internal/host/logger.go:45-56` |
| 终止信号 | `internal/host/signals_unix.go:11` |
| ACP 传输（分帧/错误码/帧上限） | rig `acp/acp.go:1-8,26-37`、`internal/modules/acp/acp.go:162-175` |
| ACP 差值引擎 | rig `acp/server/stream.go:1-9,45-77` |
| ACP 适配器（订阅+拉快照） | `internal/modules/acp/chat_adapter.go:46-96` |
| ACP 图像上限 10 MiB | `internal/modules/acp/acp.go:43`、rig `acp/server/server.go:55` |
| 持久事件序号 `Seq` | rig `extensions.go:147-163` |
| 现有各域上限（job 64 KiB / 归档 8 KiB / 图片 10 MiB / 帧 16 MiB） | `agentsurface.go:126-133`、`contextinspect.go:327-334`、`acp.go:43`、rig `acp/acp.go:26` |

## 附录 B：待定项（需要 Stage 0 决策）

| # | 待定 | 本文的倾向 |
|---|---|---|
| Q1 | serve 用独立 `assemblies/serve.json` 还是复用 headless + 运行期覆盖？ | 复用 + 覆盖（§2.6），但**必须**覆盖批准模式 |
| Q2 | §8 五类 schema 的受控词表（`kind`/`card`）首批包含哪些？ | `badge`/`text`/`button`/`menu`/`banner`/`card`；卡片 `file`/`diff`/`shell`/`search`/`todo`/`team`/`generic` |
| Q3 | 托盘壳与网关之间是否要 IPC（还是托盘壳只做"打开浏览器 + 读 gateway.json"）？ | 先只做只读发现 + `POST /quit`；系统通知经网关的 `notify` 标记 + 托盘壳轮询 |
| Q4 | `settings` 变更是否需要感知"用户手改 settings 文件"？ | 第一版不感知（§6.3 已记为限制） |
| Q5 | 是否需要 `--exit-with-last-client`（无客户端即退出）？ | 不需要；default 常驻（对齐 desktop close-to-tray） |