# AGENTS.md

> 给 AI coding agent 的项目说明（"README for agents"，格式参考 [agents.md](https://agents.md)）。
> **最近的 AGENTS.md 优先；用户对话中的明确指令优先于本文件。**
> 本文件是**约束**，不是介绍；与它冲突的实现一律拒绝——先改文档，再改代码。

---

## 0. 形态（迁移已完成，2026-11-23）

**网关进程（gateway）+ 浏览器 Web 客户端（主体）+ 极小原生托盘壳。**

原生桌面 UI（`internal/ui`、`internal/uiroot`、`internal/uikit`，约 16 万行）已于
`S5: retire the native UI` 删除，代码保存在 `pre-s4` tag。三个部分各自的规则不同：

| 部分 | 位置 | 规则 |
|---|---|---|
| **Web 客户端**（默认改这里） | `web/`（React + TypeScript + Vite + antd 6 + antd X 2.0） | [WEB-CLIENT.md](docs/WEB-CLIENT.md) + [DESIGN.md](docs/DESIGN.md) |
| **网关进程** | `internal/gateway`（WS 控制面、SSE 运行流、静态资源） | [TRANSPORT.md](docs/TRANSPORT.md) |
| **托盘壳** | `internal/host/` 的托盘/热键/通知（约 0.8k 行 mygo：`tray.go`/`tray_shell.go`/`hotkey.go`/`notify.go`/`framework_locale.go`） | mygo 规则仅此处适用（原生 UI 已退役；托盘壳规则见 `internal/host` 注释） |

- 决策记录：[docs/plans/web-ui-framework.md](docs/plans/web-ui-framework.md)、[docs/plans/web-runtime-adapter.md](docs/plans/web-runtime-adapter.md)；退役清单与执行记录（已归档）：[docs/archive/s4-retirement-inventory.md](docs/archive/s4-retirement-inventory.md)、[docs/archive/s4-execution-checklist.md](docs/archive/s4-execution-checklist.md)
- **归档文档**（不是契约）：[docs/archive/README.md](docs/archive/README.md) 有索引。打开归档文件前先确认它是否描述现状。

---

## 1. 项目概览

**ZeoKeel**：Go 编码的常驻 AI Agent。网关承载全部后端能力，浏览器是主客户端，
托盘壳只负责常驻图标 / 全局快捷键 / 系统通知。

```
cmd/zeokeel             入口（默认 --product serve；--headless；--acp 是标志；
                        --list-products 输出 headless 与 serve 两个产品）
internal/gateway        serve 产品：WS 控制面（快照+差值）、SSE 运行流、静态资源、端口协商
web/                    浏览器客户端：路由 / /inbox /team /skills/drafts /settings/*
internal/host           进程宿主 + 托盘壳：托盘/热键/通知/设置存储/日志/退出
internal/contracts      服务契约（chat/approval/plan/team/task/...）——叶子层
internal/modules/*      后端能力模块（35 个）：acp agent appinfo approvals automation bot browser
                        channeldispatch creds dingtalk email feishu goal heartbeat
                        hosttools importer inbox insights lead memorystore modelcatalog
                        notifyroute replyrelay sandbox sessionstore skilldraft skills
                        tasklease teamnotes toolkit triage update webhook wechat
                        workspace
internal/assembly       装配根：编译期 registry + assemblies/headless.json 预检（serve 复用其 entries）
internal/design         设计 token（→ 生成 web/src/design/tokens.css）
internal/brand          品牌层：唯一 hex 来源、主题、字体栈
internal/icons          34 个 SVG 图标（→ 生成 web 图标组件）
internal/devtools       fakemodel / gwprobe 等开发夹具（不进产品二进制）
docs/plans              迁移计划与执行清单（活）  docs/archive 已退役文档（证据）
```

### 边界（机器断言，违反即 CI 红）

- **原生引擎隔离**：`github.com/egoist/mygo` 只允许出现在 **4 个包**——
  `internal/host`、`internal/lib/openurl`、`internal/brand`、`internal/design`。
  其它任何包 import mygo 都会被 `TestEngineIsQuarantinedToTheShell` 拒绝。
- **后端引擎无关**：`internal/gateway`、`internal/contracts`、`internal/modules/**`
  对 mygo 的**传递**依赖必须为 0（`TestBackendIsTransitivelyEngineFree`）。
- **test 依赖单列**：`internal/gateway` 的 e2e 允许（且要求）拖进 `internal/modules/*`
  ——没有真模块就没有端到端价值；但**不得拖进 mygo**（`TestTestDependenciesStayEngineFree`）。
- **模块边界**：后端模块只 import `contracts`/`design`/`lib/*` 与共享库
  `internal/{hosttools,websearch}`；`host` 不得直连 `internal/modules/*`
  （[ARCHITECTURE.md](docs/ARCHITECTURE.md) §3；`TestImportBoundaries`）。
- **Web 侧**：`web/src` 只能经 `web/src/api/` 访问网关，**禁止**手写 `fetch`/`WebSocket`；
  `web/src/design/tokens.css` 是唯一颜色/字号/间距来源（生成物，手改即违规）。

---

## 2. 必读顺序（写 Web 客户端时）

1. 本文件。
2. [docs/WEB-CLIENT.md](docs/WEB-CLIENT.md)：路由、api 层、状态模型、验收清单。
3. [docs/TRANSPORT.md](docs/TRANSPORT.md)：方法表（§5.1，带实现状态列）、域/快照/差值、错误码、SSE 事件。
4. [docs/DESIGN.md](docs/DESIGN.md)：组件目录 / 场景映射 / 反模式 / token。
5. [docs/APPROVAL-SURFACES.md](docs/APPROVAL-SURFACES.md)：**哪些工具会停审批**（改审批行为前必读，含机器可读清单）。
6. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)：进程结构、传输层、边界表。

改托盘壳时额外读 mygo 源码（`go list -m -f '{{.Dir}}' github.com/egoist/mygo`）。
**原生 UI 已退役**，没有 `.agents/skills/` 之类的技能目录可读；托盘壳规则写在 `internal/host` 的
注释里（`tray.go` / `tray_shell.go` / `hotkey.go` / `notify.go` / `framework_locale.go`）。

---

## 3. 常驻规则（违反即拒绝）

**Web 客户端**（细则见 DESIGN.md）：

1. **颜色/字号/间距只来自 `web/src/design/tokens.css`**：禁止 hex、禁止裸数值度量。
2. **官方件优先**：antd 6 有组件就用（`Table`/`Modal`/`Form`/`Select`/`Tabs`/`Alert`…）；antd X 覆盖会话面（Bubble/Sender/Conversations/ThoughtChain…）。自造前先查 [antd-x-component-map](docs/plans/antd-x-component-map.md)。
3. **antd/X 的 API 必须查，不许凭记忆写**（见 §4 的离线 CLI 与 `*.d.ts`）。
4. **状态语义不可降级**：`error` 用语义色 + 文案；`running ≠ warning`（进行中 = 进行态；warning 只给"需要用户动作"）。
5. **审批动词不中性化**：允许/始终允许/拒绝三值；无默认动作、Esc=拒绝（[APPROVAL-SURFACES](docs/APPROVAL-SURFACES.md)）。
6. **协议只经 `web/src/api/`**；新增方法先改 TRANSPORT §5.1 并让 `TestTransport*` 变绿。
7. **强调色预算**：一屏 ≤2 个主色填充块、≤1 个 primary 按钮；红色元素 ≤2。
8. **不做未请求的功能**：不加装饰性面板/统计来"看起来完整"。

**托盘壳**（`internal/host`）：tray / 热键 / 通知都是**尽力而为**——没有显示环境、没有托盘宿主时只警告并继续；网关先起、托盘后起（`internal/host/serve.go`）。

---

## 4. 命令（离线环境已配好）

```bash
export PATH=/usr/local/go/bin:$PATH
export GOPATH=/data/mygo/.cache/gopath        # 本沙箱；普通环境可省略
export GOCACHE=/data/mygo/.cache/go-build
export GOTMPDIR=/tmp/zeokeel-tmp TMPDIR=/tmp/zeokeel-tmp   # 必须在仓库外
mkdir -p /tmp/zeokeel-tmp
export GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off

# Go（显式 internal/cmd，不要 ./... —— web/node_modules 里可能有第三方 Go 文件）
gofmt -l internal cmd
go build ./internal/... ./cmd/...
go vet   ./internal/... ./cmd/...
go test  ./internal/... ./cmd/... -count=1

# 一键验证
scripts/verify.sh --quick     # fmt/build/vet/协议一致性/test（快速内环）
scripts/verify.sh             # 全量：再加 -race 与 headless/ACP/serve smoke
make ci                       # build+vet+test

# 协议级端到端（无需模型 key、无需浏览器）
make acceptance-check         # 纯文本一轮：create → run → SSE → done
make acceptance-approval      # 审批链：run → approvals.delta → resolve → tool.result → done
make acceptance-slow          # 断连 ≠ 停止
make acceptance-update        # 更新面：update.status/check 真的接到后端（不要求有新版本）
# 想在浏览器里手动验：make acceptance / make acceptance-tool（打印带 token 的 URL）
```

**Web 客户端（`web/`）**

```bash
cd web
npm ci                 # 锁定依赖（package-lock.json 必须提交）

# CI 也跑的项（.github/workflows/ci.yml 的 "Web client checks" + "Generated artefacts"）
npm run typecheck      # tsc --noEmit
npm run lint           # eslint
npm run test           # vitest
npm run gen:verify     # 复跑生成器并与基线比对（生成物被手改即红）

npm run e2e            # 真浏览器（playwright chromium）冒烟：中英各一轮
npm run e2e:cold       # 一条命令自包含：真网关 + fake model + 全新数据目录，跑完收尾
npm run gen:tokens     # internal/design + internal/brand → tokens.css / antd-theme.ts（生成物）
npm run gen:icons      # internal/icons/svg → 图标组件（生成物）
npm run gen:settings-keys  # 设置键清单 → src/design/settings-keys.ts（生成物）
npm run gen:ws-contract    # internal/gateway/contract.json → src/design/ws-contract.ts（生成物）
npm run gen:baseline   # 采生成链基线（改了上游 Go 先跑，再改生成物）
npm run build          # 产物由 scripts/build-web-client.sh 复制进 go:embed 目录
```

- **antd/X 知识必须查**：离线 CLI（`bash scripts/antd-cli.sh info Button --version 6 --format json`、`demo`、`token`、`semantic`、`lint ./web/src`）；antd X 的确切 props 读 `web/node_modules/@ant-design/x/es/**/*.d.ts`。包装脚本把 `HOME`/`XDG_*` 重定向到 `.cache/antd-cli/`。仓库里**没有** `.agents/skills/` 目录——上面这两处是唯一的知识源。

---

## 5. Web 改动固定流程

1. **查**：WEB-CLIENT 的页面/验收清单 → DESIGN 场景表 → antd/X 是否有现成件。
2. **读**：协议（TRANSPORT §4/§5）与现有 `web/src/api/` 的调用形态。
3. **实现**：沿用现有状态模式；禁止自造 fetch/WebSocket；**域的第一帧不发 delta，客户端必须先 `.snapshot` 再吃差值**。
4. **测**：vitest 覆盖组件与协议适配；端到端用 mock server 或 `make acceptance-*`。
5. **验**：`npm run typecheck && npm run lint && npm run test`；改到生成链上游（design/brand/icons）时跑 `gen:verify`。
6. **同步**：改协议 → TRANSPORT §5.1（状态列）+ `TestTransport*`；改 token/图标 → 生成链基线。

改托盘壳时：`go test ./internal/host -count=1` + `make acceptance`（看托盘能否打开客户端）。

---

## 6. 文档同步义务（与代码同 PR）

| 改动 | 必须同步 |
|---|---|
| 新增/删除协议方法 | `docs/TRANSPORT.md` §5.1 方法表（状态列）+ `internal/gateway` dispatch（`TestTransport*` 会红） |
| 新增域/通知 | TRANSPORT §5/§6 的形状 + `internal/gateway` 的 snapshot/delta 测试 |
| 改审批触发面 | [docs/APPROVAL-SURFACES.md](docs/APPROVAL-SURFACES.md) 的机器可读块 + `internal/modules/agent/approval_surface_test.go` |
| 改设计 token / 品牌 / 图标 | `web` 的 `gen:tokens` / `gen:icons` 重跑并提交生成物 |
| 新增页面/入口 | WEB-CLIENT 路由与验收清单 + DESIGN 场景表 |
| 改进程/产品形态 | ARCHITECTURE + 本文件 §0/§1 + `cmd/zeokeel --list-products` 的期望 |

---

## 7. 硬边界（违反 = 拒绝合并）

- **引擎隔离**：只有 §1 那 4 个包可以 import mygo；后端与网关必须引擎无关（含传递依赖）。
- **不自造协议**：客户端只能用 TRANSPORT 里声明的方法；要新方法就先进文档 + dispatch + 一致性测试。
- **不绕过 api 层**：`web/src` 里手写 `fetch`/`WebSocket` 一律拒绝。
- **不手改生成物**：`web/src/design/tokens.css`、`antd-theme.ts`、图标组件都是 `gen:*` 的产物。
- **不给测试开产品后门**：不给 gateway/agent 加"仅测试"开关；夹具放 `internal/devtools` 或 `scripts/acceptance`。
- **不做未请求的功能**：不加导航/统计/装饰性面板。

---

## 8. 汇报与验证要求

- 汇报必须给**证据**：命令与结果原文、测试名、真实端口/URL、失败与限制说明。
- 没跑验证不说"完成"；只改文档则明确写"未改代码"。
- 交付前自检：`gofmt` / `go build` / `go vet` / 相关包 `go test`；涉及协议或前端时加 `scripts/verify.sh --quick` 与 web 三连（typecheck/lint/test）。
- 提交代码时：显式路径 `git add`（**不要 `git add -A`**——工作区里可能有别人的改动），提交后 `git show --stat` 自检，并在提交信息里写清"为什么"。
- 遇到规则冲突：先按本文件做，并在回复里指出冲突点；不要静默偏离。

---

## 9. 常用速查

| 需求 | 去哪 |
|---|---|
| 协议方法/事件/错误码 | [docs/TRANSPORT.md](docs/TRANSPORT.md)（§5.1 带实现状态列） |
| 哪些工具会停审批 | [docs/APPROVAL-SURFACES.md](docs/APPROVAL-SURFACES.md) |
| 页面/路由/验收清单 | [docs/WEB-CLIENT.md](docs/WEB-CLIENT.md) |
| 设计 token 取值 | `internal/design/{tokens,components}.go` → `web/src/design/tokens.css` |
| 品牌色 / 图标 | `internal/brand`、`internal/icons/svg/*.svg`（都由 web 生成器消费） |
| antd 组件用法 | `bash scripts/antd-cli.sh info <Component> --version 6` + `demo` |
| antd X 组件 props | `web/node_modules/@ant-design/x/es/**/*.d.ts` |
| 协议级端到端 | `make acceptance-check / acceptance-approval / acceptance-slow` |
| 现有测试保护网 | `internal/gateway/*_test.go`（含 15 条 `TestE2E*`）、`internal/assembly/boundary_test.go` |
| 迁移决策与退役记录 | `docs/plans/web-runtime-adapter.md`、`docs/archive/s4-retirement-inventory.md`、`docs/archive/s4-execution-checklist.md` |
| 已退役的文档 | [docs/archive/README.md](docs/archive/README.md) |