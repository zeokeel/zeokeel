# ZeoKeel

编译期模块化的 Go 桌面 AI agent：**薄总入口 + 内核装配 + 模块贡献点**。
UI 是 mygo 原生控件（无浏览器/webview）；所有模块一起编译进同一二进制，`assembly.json`
只决定启用/裁剪哪些模块。

```
cmd/zeokeel           薄入口：解析参数 → host.Run
internal/host         进程宿主：窗口/托盘/单实例/日志/退出
internal/uiroot       UI 总入口（帧根）：布局、导航、overlay/toast、主题/语言、五张贡献点注册表
internal/assembly     装配根：编译期 registry + assembly.json 预检 + 产品装配
internal/contracts    契约：服务键 + 接口 + 贡献值类型（模块唯一共享面）
internal/modules/*    后端模块（store/session/tools/...）
internal/ui/*         UI 模块（home/session/settings/...）
internal/uikit        共享 UI 库（优先 mygo 原生控件，不手搓）
internal/design       设计 tokens
old/                  重构前的 ZeoKeel（归档参考，独立 Go module）
```

规则（详见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)）：

1. **mygo 有的控件优先用控件**，不手搓控件/布局图元树。
2. 模块只依赖 `internal/contracts`、`internal/uikit`、`internal/design`、`internal/lib/*`；
   禁止 import 其他模块实现、`internal/uiroot`、`internal/host`。
3. UI 状态归模块实例；禁止包级可变状态；UI 渲染只在帧内，跨线程更新走 `UIRuntime.Post`。
4. 注册/注销必须走 `ctx.Effect`（kernel 事务回滚依赖它）。
5. 配置变更 = 重启进程（编译期注册表，无热更新/运行时插件安装）。

构建（离线可用）：

```sh
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
go build ./...
go test ./...
```

归档的旧工程：`cd old && go build ./...`（独立 module，仅参考）。

## 状态（2026-11-07 · Phase-1 可聊天产品已可运行）

- ✅ 编译期装配：`internal/assembly`（显式 registry + 内嵌 `assemblies/desktop.json`、`headless.json`
  + kernel 预检 + required 失败整树回滚 + UI 子组级联卸载）
- ✅ UI 总入口 `internal/uiroot`：帧根、布局区域、五张贡献点注册表、Post 队列、导航、设置面、
  槽位渲染（`RenderSlot`）与工具卡注入（`RenderTool`）
- ✅ 进程宿主 `internal/host` + 薄 `cmd/zeokeel`（17 行）+ `Makefile`
- ✅ 示范模块：`appinfo`、`toolkit`（把 ZeoKeel Rig 工具集 + 沙盒策略组装成产品服务：
  fs/shell/pwsh、editor、image、PTY terminal、jobs、web、远程 MCP over HTTP、
  memory/schedule/worktree/skill/team_task/settings 等）、
  `hosttools`（产品侧 host 工具：ask_user/present/plan/goal/team，注册进共享工具注册表）、
  `ui/home`、`ui/about`、`ui/welcome`、`ui/toolcards`、`uikit`
- ✅ SDK 依赖：`github.com/zeokeel/zeokeel-rig v0.13.0`（纯工具集：agent loop / subagent / provider /
  sandbox / 81 个静态工具 + MCP/LSP / gate / session+compact / checkpoint；零第三方依赖；
  旧 agent 61 个工具名 + 现代工具面 55 个名称 100% 覆盖，`TestToolCatalogCoverage` 与
  `TestModernToolSurface` 双重门禁）
- ✅ Phase-1 聊天产品（用 rig 内核重建 old 的核心体验，UI 全部 mygo 原生控件）：
  - 后端模块 `zeokeel/agent`：ChatService（流式事件 → 会话读模型、订阅、取消、会话管理；
    provider/tools/history 全部来自 rig）
  - UI 模块 `zeokeel-ui/session`（对话页：Tabs/Scroll/TextArea/Collapsible/Badge/RenderTool）、
    `zeokeel-ui/sidebar`（新对话/SidebarItem 会话列表/设置入口）、
    `zeokeel-ui/settings`（模型配置页：TextInput/TextArea，API key 打码）
  - 端到端验证：真实 desktop assembly + 假 OpenAI SSE 服务 → 流式聊天 → 帧断言；Xvfb 真窗口冒烟
    （12 实例、screens [home session]）；截图见 `docs/screenshots/phase-1-*.png`
- ⬜ 后续阶段见 [docs/PRODUCT-REBUILD-PLAN.md](docs/PRODUCT-REBUILD-PLAN.md)：
  P1 会话持久化/工具卡/轨迹/审批 → P2 composer 增强/设置扩展 → P3 memory/skills/automation/插件 →
  P4 ACP/热键/导入/更新/诊断
- ✅ `make ci` 全绿；uiroot 29 个 headless 测试（覆盖率 99.3%）；desktop/headless 装配集成测试；
  Xvfb 真窗口冒烟通过
- ⬜ 逐模块迁移（后端 20 + UI 16，见 `docs/research/kernel-split/05-ui-module-map.md`）：
  `ui/session` 后续接管 `session.*` 注入点（轨迹、轨迹旁横条、页头横条、右栏、输入区）

## 常用命令

```sh
make ci            # build + vet + test
make run           # 桌面产品（需要桌面会话）
make run-headless  # headless 产品（无窗口，等 SIGINT/SIGTERM）
make build ARGS=   # 仅构建
```

注入点清单与灵活性规则：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) §4/§5；
已接线的模块清单：[docs/INJECTION-POINTS.md](docs/INJECTION-POINTS.md)。
