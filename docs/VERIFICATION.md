# Verification log (final)

Date: 2026-11-08 (Asia/Shanghai) · commit: c2b2f92

## Build, vet, tests
```
go version go1.27.3 linux/amd64

$ go build ./...
OK

$ go vet ./...
OK

$ go test ./... -count=1
packages ok: 32

$ go test ./... -count=1 -race
packages ok (race): 32

$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/uiroot ./internal/design ./internal/uikit
packages ok (CGO_ENABLED=0): 14
```

## Product smokes
```
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir <tmp>
2026/11/08 02:23:36 ZeoKeel: ZeoKeel dev (commit unknown, built unknown, linux/amd64, go1.27.3) product "desktop" loaded: 26 module instances, screens [home automation session plugins]

$ go run ./cmd/zeokeel --headless --data-dir <tmp> (SIGTERM after 6s)
2026/11/08 02:23:26 ZeoKeel: ZeoKeel dev (commit unknown, built unknown, linux/amd64, go1.27.3) product "headless" loaded
stopping

$ printf '{initialize,session/new,session/prompt unknown}' | go run ./cmd/zeokeel --acp --data-dir <tmp>
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"promptCapabilities":{"audio":false,"embeddedContext":false,"image":false}},"protocolVersion":1}}
{"id":2,"jsonrpc":"2.0","result":{"sessionId":"sess-1794075824617415992"}}
{"error":{"code":-32603,"message":"session/prompt: send: agent: unknown chat session \"missing\""},"id":3,"jsonrpc":"2.0"}
```

## Assembly
```
desktop instances: 26 · headless instances: 14 · registry modules: 28
screens: [home automation session plugins]
```

## Screenshots
```
snapshot renderer PNG steps: 47
docs/screenshots files: 48
```

## Known remaining gaps (from docs/COVERAGE.md §5)

```
1. 编排执行：真实 subagent/agentteam runner、Team UI/steer、goal 自动驱动
2. Composer/plan/modes 高级控制：permission 访问模式、推理强度、plan mode keep-planning/feedback/plan 卡、
   /plan /goal /compact /memory 命令
3. presets/persona + @session 引用（modelcatalog 缺 context/effort/添加编辑/连接检查）
4. 更新安装 + 桌面宿主：updater download/SHA256/stage/install、autostart/single-instance/窗口几何/通知/代理
5. MCP/插件/artifact/todo 管理深度：stdio/workspace mcp.json/工具级开关、内置插件开关、artifact 历史、todo 面板
```

Everything above is tracked in `docs/COVERAGE.md`; the core conversation product,
settings, inspector, search, MCP configuration, web search, ask_user/present/plan
review and goal tracking are implemented and covered by tests/snapshots.

## Re-verification (round 39, current HEAD)

```
$ go build ./... && go vet ./...                                  # OK
$ go test ./... -count=1                                          # 32 packages ok
$ go test ./... -count=1 -race                                    # 32 packages ok
$ xvfb-run -a timeout -s TERM 7 go run ./cmd/zeokeel --data-dir <tmp>
  ZeoKeel ... product "desktop" loaded: 26 module instances, screens [home automation session plugins]
$ timeout -s TERM 5 go run ./cmd/zeokeel --headless --data-dir <tmp>
  product: headless (no window) ... ready; waiting for SIGINT or SIGTERM / stopping
$ printf initialize+session/new | go run ./cmd/zeokeel --acp --data-dir <tmp>
  {"id":1,...,"result":{"agentCapabilities":{...},"protocolVersion":1}}
  {"id":2,...,"result":{"sessionId":"sess-..."}}                  # exit 0
$ go run ./internal/devtools/ui-snapshot <tmp>                    # 47 PNG steps, no errors
```

## Adversarial regression suite (after TDD fixes)

```
$ go test ./internal/regression/... -count=1 -race        # ok (~10s)
$ go test ./internal/regression/... -count=1 -v | grep -c SKIP   # 0
$ go test ./internal/regression/... -count=1 -v | grep -c PASS   # 92
$ go test ./... -count=1                                  # 33 packages ok
$ go test ./... -count=1 -race                            # 33 packages ok
```

Audit report: docs/ADVERSARIAL-AUDIT.md (78 top-level tests; 4×P2 + 4×P3 found
and fixed via RED→GREEN; probes now assert instead of skip).

## 最终验证（Round 56，2026-11-08，HEAD 132e386）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 05:46–05:49（Asia/Shanghai）。

### Build / vet / tests
```
$ go build ./...
exit 0（4s）

$ go vet ./...
exit 0（1s）

$ go test ./... -count=1
exit 0 · 13s · 42 个包中 34 个测试包 ok、8 个 no test files、0 FAIL

$ go test ./... -count=1 -race
exit 0 · 47s · 34 个测试包 ok、8 个 no test files、0 FAIL / 0 DATA RACE

$ go test ./internal/regression/... -count=1 -v
exit 0 · 9.7s · 187 PASS / 0 SKIP / 0 FAIL
```

### 三产品冒烟（无 panic）
```
$ mkdir -p /tmp/final-desktop
$ printf '{"ui.globalHotkey":"CmdOrCtrl+Shift+Space"}\n' > /tmp/final-desktop/settings.json
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir /tmp/final-desktop
exit 124
05:48:12 ZeoKeel: system tray ready (close to tray: false)
05:48:12 ZeoKeel: global hotkey registered
05:48:12 ZeoKeel: ... product "desktop" loaded: 27 module instances, screens [home automation session plugins]
05:48:18 ZeoKeel: quit requested
NO_PANIC

$ timeout 8 go run ./cmd/zeokeel --headless --data-dir /tmp/final-headless
exit 124
05:48:22 ZeoKeel: ... product "headless" loaded
ready; waiting for SIGINT or SIGTERM
stopping
NO_PANIC（14 module instances）

$ printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}\n' \
    | timeout 10 go run ./cmd/zeokeel --acp --data-dir /tmp/final-acp
exit 0
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"promptCapabilities":{"audio":false,"embeddedContext":false,"image":false}},"protocolVersion":1}}
合法 JSON-RPC 2.0 initialize 响应；NO_PANIC
```

### UI 快照
```
$ mkdir -p /tmp/final-shots
$ go run ./internal/devtools/ui-snapshot /tmp/final-shots
exit 0 · 4s
"wrote ..." 渲染步数: 60
唯一 PNG 文件: 58
同名重复写入: p1-chat.png、p2-mentions.png（各 2 次，最终保留最后一次）
NO_PANIC
```
注：ui-snapshot 要求输出目录已存在；首次未建目录时 exit 1（`create /tmp/final-shots/...: no such file or directory`），预先 `mkdir -p` 后通过。

### 结论
- 构建/vet/测试（含 -race）与反证回归套件全绿；三产品均正常启动/退出，桌面托盘与预置全局热键就绪；
- 快照渲染 60 步（58 个唯一 PNG，2 个目标名重复）；README 状态行已按本结果同步。

## 最终验证（Round 59，2026-11-08，HEAD 51e8e59）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 08:18–08:23（Asia/Shanghai）。
命令：`./scripts/verify.sh`（完整）。

### 一键验证结果（首次即 PASS，无需重跑）
```
$ ./scripts/verify.sh
==> gofmt ... PASS (0s)
==> go build ./... ... PASS (4s)
==> go vet ./... ... PASS (0s)
==> go test ./... -count=1 ... PASS (15s)
==> go test ./... -count=1 -race ... PASS (53s)
==> regression (-v) ... (regression: 245 PASS / 0 SKIP / 0 FAIL) PASS (13s)
==> ui-snapshot ... (snapshot: 65 steps / 63 PNGs) PASS (9s)
==> desktop smoke (xvfb) ... (desktop exit 124, tray ready: 1) PASS (8s)
==> headless smoke ... (headless exit 124) PASS (8s)
==> ACP smoke ... (ACP exit 0) PASS (0s)
verify summary: 10 passed, 0 failed, 110s total
verify: PASS
```
regression **245 PASS / 0 SKIP / 0 FAIL**；UI 快照 **65 步渲染 / 63 个唯一 PNG**；三产品冒烟无 panic：desktop exit 124、headless exit 124、ACP exit 0。本轮无失败步骤。

### xvfb 桌面冒烟（模块实例数确认）
```
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir <tmp>
exit 124（允许）
08:20:47 ZeoKeel: system tray ready (close to tray: false)
08:20:47 ZeoKeel: ... product "desktop" loaded: 27 module instances, screens [home automation session plugins]
08:20:54 ZeoKeel: quit requested
NO_PANIC
```
`grep -c '27 module instances'` = 1；日志无 panic/runtime error/SIGSEGV。

### 新功能冒烟
- **内置模块清单（诊断页 / 插件页，静态画面）**
  - 诊断页：`已加载模块 N 个` + `展开模块列表`（每行 `entryID  moduleID  state`，active 成功色；40 条上限后「…」）；截图证据 `/tmp/r59-evidence/diagnostics-modules.png`（fake ModuleLister 4 模块）。
  - Plugins 页：`内置模块` section + `N 个模块` Badge + `编译期装配，只读。` + 默认折叠的 `展开模块列表`（ModuleID mono + State Badge）；截图证据 `/tmp/r59-evidence/plugins-modules.png`。
  - 说明：截图为单测 harness 注入 fake `KeyModuleLister` 的静态画面；产品运行时该服务由 host 在 assembly 加载前提供，xvfb 日志的 27 module instances 即 host 实际填充来源。ui-snapshot 自建裸 kernel root 不注入 ModuleLister，故 p2-diagnostics/p2-plugins 快照不显示该区（非缺陷）。
- **技能编辑**：`go test ./internal/ui/settings/ -run TestSkills -count=1` → **18 PASS**（新建/编辑/校验/错误/无 editor 隐藏等）。
- **记忆整理**：`go test ./internal/ui/settings/ -run TestMemoryPageTidy -count=1` → **3 PASS**（去重保留最早/无重复/错误）；记忆页整体 `TestMemoryPage` **16 PASS**。
- 宿主模块清单映射/服务：`go test ./internal/host -run 'TestModuleInfo|TestModuleLister|TestProvideModuleLister'` → 3 PASS（加载顺序 EntryID/ModuleID/State、快照副本、provide/require 同实例）。

### 文档核对
- docs/COVERAGE.md 矩阵（§2 28 项 + §3 34 项 = 62）重算 **✅39 / 🟡22 / ❌1**；摘要与统计表一致（62.9% / 35.5% / 1.6%），本轮无差异。
- README 状态行更新为 Round 59（HEAD 51e8e59）。

## 最终验证（Round 58，2026-11-08，HEAD 364a988）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 09:03–09:08（Asia/Shanghai）。
命令：`./scripts/verify.sh`（完整）。验证时工作树含第十四轮审计已落地的未提交文件
（`internal/regression/{goal_pause,mcp_connection_config}_test.go`），计数以该工作树为准。

### 一键验证结果（两次均 PASS，无失败步骤）
```
$ ./scripts/verify.sh
==> gofmt ... PASS (1s)
==> go build ./... ... PASS (3s)
==> go vet ./... ... PASS (1s)
==> go test ./... -count=1 ... PASS (15s)
==> go test ./... -count=1 -race ... PASS (60s)
==> regression (-v) ... (regression: 263 PASS / 0 SKIP / 0 FAIL) PASS (16s)
==> ui-snapshot ... (snapshot: 66 steps / 64 PNGs) PASS (8s)
==> desktop smoke (xvfb) ... (desktop exit 124, tray ready: 1) PASS (8s)
==> headless smoke ... (headless exit 124) PASS (8s)
==> ACP smoke ... (ACP exit 0) PASS (1s)
verify summary: 10 passed, 0 failed, 121s total
verify: PASS
```
第 1 次 PASS（118s，HEAD 提交态）；第十四轮两个审计文件落到工作树后第 2 次复跑 PASS（121s，计数同为 263），
判定：验证过程无偶发失败，复跑仅为纳入新落地的审计用例。
regression **263 PASS / 0 SKIP / 0 FAIL**（含第十四轮 goal_pause 3 项 + mcp_connection_config 2 项；第十四轮其余用例尚未落地）；
UI 快照 **66 步渲染 / 64 个唯一 PNG**；三产品冒烟无 panic。

### xvfb 桌面冒烟（模块实例数/无 panic）
```
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir <tmp>
exit 124（允许）
09:05:59 ZeoKeel: system tray ready (close to tray: false)
09:05:59 ZeoKeel: ... product "desktop" loaded: 27 module instances, screens [home automation session plugins]
09:06:07 ZeoKeel: quit requested
NO_PANIC
```
`grep -c '27 module instances'` = 1；无 panic/runtime error/SIGSEGV。

### 新功能冒烟（单测证据 + ACP 真实帧）
- **目标暂停/恢复**：`go test ./internal/uiroot -run TestGoalPause` → **2 PASS**（暂停/恢复状态机、并发）；
  `go test ./internal/ui/session -run 'TestGoalPause|TestGoalAutoContinueSkipsPausedGoal'` → **5 PASS**
  （chip 与动作、失败 Danger、无 GoalPauser 隐藏、暂停目标跳过自动续跑、en）。
- **插件连接配置**：`go test ./internal/ui/plugins -run TestMCPConfig` → **5 PASS**
  （stdio command/args/env 展示、env 键值含 KEY/TOKEN/SECRET/PASSWORD/AUTHORIZATION 脱敏、HTTP 仅 URL、
  无匹配 settings 行隐藏、坏 JSON 可读报错、en）。
- **ACP session/list + session/load 真实帧**（同一 data-dir 两次 `--acp`）：
```
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"loadSession":true,...},"protocolVersion":1}}
{"id":2,"jsonrpc":"2.0","result":{"sessionId":"sess-1794099982300810356"}}
{"id":3,"jsonrpc":"2.0","result":{"sessions":[{"sessionId":"sess-1794099982300810356","title":"","updatedAt":"2026-11-08T09:06:22+08:00"}]}}
{"id":2,"jsonrpc":"2.0","result":{"messages":[],"model":"gpt-4o-mini","sessionId":"sess-1794099982300810356","title":""}}   # session/load
```

### 文档核对
- docs/COVERAGE.md 矩阵重算（§2 28 + §3 34 = 62）：**✅40 / 🟡21 / ❌1**；摘要与统计表一致（64.5% / 33.9% / 1.6%），本轮无差异。
- README 状态行更新为 Round 58（HEAD 364a988）。

## 最终验证（Round 59：干净环境一键验证）

- 命令：`./scripts/verify.sh`（无并行 agent 修改工作树时）
- 结果：**10/10 PASS，108s**：gofmt / go build / go vet / go test / go test -race / regression / ui-snapshot /
  desktop smoke / headless smoke / ACP smoke。
- regression 计数：227 PASS / 0 SKIP；ui-snapshot 64 步渲染 / 62 唯一 PNG。
- 本轮唯一修复：automation 测试 helper 的等待预算 3s→10s（并行载入下偶发超时，非产品回归）；修复后
  `go test ./internal/modules/automation -count=2` 与完整 verify.sh 均 PASS。

## 最终验证（Round 58 补充：第十一轮反证后）

- `./scripts/verify.sh`：**10/10 PASS，111s**；regression **241 PASS / 0 SKIP**（第十一轮新增 14 例），
  ui-snapshot **65 步渲染 / 63 唯一 PNG**，desktop/headless/ACP 冒烟同前。
- regression 第十一轮覆盖：运行中排队发送、MCP server 开关、自动化搜索筛选、消息字号、AGENTS.md 注入、
  设置记忆页、模型连接检查、上下文预算、反馈导出统计；无新缺陷。

## 最终验证（Round 59 收尾：第十二轮反证后）

- `./scripts/verify.sh`：**10/10 PASS，115s**；regression **251 PASS / 0 SKIP**（第十二轮新增 10 例），
  ui-snapshot **65 步渲染 / 63 唯一 PNG**，desktop / headless / ACP 冒烟通过。
- 第十二轮覆盖：技能编辑（UpdateSkill 并发/拒绝/设置页入口）、设置记忆整理/清空（真实后端与故障注入）、
  内置模块清单（host 映射/lister 快照/诊断页截断/plugins 只读段）；第十一轮关键链路复跑 9 PASS 0 回退；无新缺陷。
- xvfb 桌面日志：`product "desktop" loaded: 27 module instances`、tray ready、quit requested、无 panic。

## 最终验证（Round 57，2026-11-08，HEAD 02f6612）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 08:40–08:55（Asia/Shanghai）。
命令：`./scripts/verify.sh`（完整）。验证时工作树含并行进行的第十三轮审计未提交文件（`internal/regression/{goal_autocontinue,reasoning_preview,provider_presets}_test.go` 与 provider_presets 相关改动），regression 计数以该工作树为准。

### 一键验证结果（第 4 次尝试 PASS）
```
$ ./scripts/verify.sh
==> gofmt ... PASS (1s)
==> go build ./... ... PASS (3s)
==> go vet ./... ... PASS (1s)
==> go test ./... -count=1 ... PASS (16s)
==> go test ./... -count=1 -race ... PASS (60s)
==> regression (-v) ... (regression: 258 PASS / 0 SKIP / 0 FAIL) PASS (14s)
==> ui-snapshot ... (snapshot: 65 steps / 63 PNGs) PASS (10s)
==> desktop smoke (xvfb) ... (desktop exit 124, tray ready: 1) PASS (8s)
==> headless smoke ... (headless exit 124) PASS (8s)
==> ACP smoke ... (ACP exit 0) PASS (1s)
verify summary: 10 passed, 0 failed, 122s total
verify: PASS
```
regression **258 PASS / 0 SKIP / 0 FAIL**（含工作树第十三轮新增审计用例；HEAD 02f6612 提交态约 251）;UI 快照 **65 步 / 63 唯一 PNG**；三产品冒烟无 panic。

### 尝试记录与判定（前 3 次为并行审计写入冲突，非产品回归）
1. 第 1 次：regression 步骤 FAIL（89s）；包内用例输出全 PASS（失败用例在截断窗口之外），单独复跑 `go test ./internal/regression -count=1 -v` → **254 PASS / 0 SKIP**，判定并行负载/写入下偶发。
2. 第 2 次：`go vet ./...` FAIL——工作树中并行写入的 `internal/regression/provider_presets_test.go` 编译错误（`harness.h undefined`）；该文件属第十三轮审计 WIP，随后被修复，vet 恢复 PASS。
3. 第 3 次：`-race` FAIL build——并行写入的 `internal/ui/settings/provider_presets_test.go` 缺少 test helper（`applyProviderPreset` 等未定义）；WIP 补齐后恢复。
4. 第 4 次（上表）**10/10 PASS，122s**。

### xvfb 桌面冒烟（模块实例数/无 panic）
```
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir <tmp>
exit 124（允许）
08:54:00 ZeoKeel: system tray ready (close to tray: false)
08:54:00 ZeoKeel: ... product "desktop" loaded: 27 module instances, screens [home automation session plugins]
08:54:08 ZeoKeel: quit requested
NO_PANIC
```
`grep -c '27 module instances'` = 1；无 panic/runtime error/SIGSEGV。

### 新功能冒烟（单测证据 + ACP 真实帧）
- **goal 自动续跑**：`go test ./internal/ui/settings -run TestBehaviorPageGoalAutoContinue` → **2 PASS**；第十三轮 regression `TestGoalAutoContinue*` → **3 PASS**（默认关闭/坏值、次数上限与重置、抑制清除与失败）。
- **实时思考预览**：`go test ./internal/ui/session -run TestReasoningPreview` → **3 PASS**（流式中显示、完成消失、空/英文隐藏）；regression `TestReasoningPreview*` → **2 PASS**。COVERAGE 的 `Reasoning 展示` 因此升为 ✅。
- **服务商预设**：`go test ./internal/ui/settings -run TestProviderPreset` → **8 PASS**（DeepSeek/Ollama/自定义、匹配高亮、写失败、en、model 失败回滚）；regression `TestProviderPreset*` → **2 PASS**。
- **ACP session/list + session/load 真实帧**（同一 data-dir 两次 `--acp`）：
```
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"loadSession":true,...},"protocolVersion":1}}
{"id":2,"jsonrpc":"2.0","result":{"sessionId":"sess-1794099265027788239"}}
{"id":3,"jsonrpc":"2.0","result":{"sessions":[{"sessionId":"sess-1794099265027788239","title":"","updatedAt":"2026-11-08T08:54:25+08:00"}]}}
{"id":2,"jsonrpc":"2.0","result":{"messages":[],"model":"gpt-4o-mini","sessionId":"sess-1794099265027788239","title":""}}
```

### 文档核对
- docs/COVERAGE.md 矩阵重算（§2 28 + §3 34 = 62）：**✅40 / 🟡21 / ❌1**（Reasoning 展示升 ✅）；摘要与统计表已同步为 40/21/1（64.5% / 33.9% / 1.6%）。
- README 状态行更新为 Round 57（HEAD 02f6612）。

## 最终验证（Round 60：覆盖收口后）

- `./scripts/verify.sh`：**10/10 PASS，120s**；regression **263 PASS / 0 SKIP / 0 FAIL**；
  ui-snapshot **66 步 / 64 唯一 PNG**；desktop（27 module instances、tray ready）/headless/ACP 冒烟通过，无 panic。
- COVERAGE 终版：62 项 **✅48 / 🟡13（已实现·深度限制，§5.1）/ ❌1（winproc 平台非目标，§5.2）**——功能面覆盖完成。
- 已知缺陷：历轮 P3（含 P3-9..P3-15）均已修复，回归探针静默；无已知未修复缺陷。

## 最终验证（Round 8，2026-11-08，HEAD a7fe576）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 11:12–11:23（Asia/Shanghai）。
命令：`./scripts/verify.sh`（完整）。验证时工作树含第十六轮审计未提交文件（`internal/regression/{agent_presets,memory_extract,plugin_params_lsp}_test.go` 与 goal_lifecycle 调整），计数以该工作树为准。

### 一键验证结果（第 2 次尝试 PASS）
```
$ ./scripts/verify.sh
==> gofmt ... PASS (0s)
==> go build ./... ... PASS (4s)
==> go vet ./... ... PASS (0s)
==> go test ./... -count=1 ... PASS (16s)
==> go test ./... -count=1 -race ... PASS (66s)
==> regression (-v) ... (regression: 283 PASS / 0 SKIP / 0 FAIL) PASS (18s)
==> ui-snapshot ... (snapshot: 66 steps / 64 PNGs) PASS (8s)
==> desktop smoke (xvfb) ... (desktop exit 124, tray ready: 1) PASS (8s)
==> headless smoke ... (headless exit 124) PASS (8s)
==> ACP smoke ... (ACP exit 0) PASS (0s)
verify summary: 10 passed, 0 failed, 128s total
verify: PASS
```
regression **283 PASS / 0 SKIP / 0 FAIL**（含第十六轮新增审计；HEAD 提交态约 278）；UI 快照 **66 步 / 64 个唯一 PNG**；三产品冒烟无 panic。

### 尝试记录与判定
1. 第 1 次：前 5 步 PASS，`regression` FAIL（91s）——当时并行写入的未提交 `internal/regression/agent_presets_test.go` 中
   `TestAgentPresetSelectionAndRunApplication` 报 `clear session model: agent: session model must not be empty`
   （该文件属第十六轮审计 WIP，且 `TestAgentPresetPickerUI` 同批）。判定：并行审计 WIP 冲突，非产品回归。
2. 等待该审计 WIP 修复后单独复跑 `go test ./internal/regression -run TestAgentPreset...` → PASS；第 2 次完整 verify 全绿（上表，128s）。

### xvfb 桌面冒烟（模块实例数/无 panic）
```
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir <tmp>
exit 124（允许）
11:21:44 ZeoKeel: system tray ready (close to tray: false)
11:21:44 ZeoKeel: ... product "desktop" loaded: 27 module instances, screens [home automation session plugins]
11:21:52 ZeoKeel: quit requested
NO_PANIC
```
`grep -c '27 module instances'` = 1；无 panic/runtime error/SIGSEGV。

### 新功能冒烟（单测证据）
- **memory extract**：backend `TestMemoryExtract*` **7 PASS**（默认关闭、缺会话/短对话拒绝、成功去重、坏 JSON/空、忙、Add 失败、上限）；UI `TestMemoryExtract*` **8 PASS**（默认关、坏值、无 extractor 隐藏、成功添加、失败 Danger、重复点击、无活动会话禁用、en）；regression `TestMemoryExtract*` **2 PASS**。
- **presets**：agent `TestSetSessionPreset/TestPreset/TestNoPreset` **4 PASS**；regression `TestAgentPreset*` **2 PASS**（应用/覆盖/清除与 picker UI）。
- **spill（大工具结果落盘）**：agent `TestSpill*/TestPrune*` **8 PASS**；regression `TestToolSpill*` **4 PASS**。
- **compact**：`TestCompactNow*` **5 PASS**（替换历史/拒绝运行中/不可用/无需压缩/保存失败）。
- **goal lifecycle**：regression `TestGoalLifecycle*` **2 PASS**（状态机、命令与终态自动续跑）。
- **i18n 错误**：agent `TestClassifyErrorTable/TestRunFailureSetsErrorKind/TestSuccessfulRunClearsErrorKind` **3 PASS** + UI `TestAssistantErrorKind*` **3 PASS**。
- **插件参数**：`TestPluginParams*` **8 PASS**；regression `TestPluginParamsSection` **1 PASS**。
- **ACP session/list + session/load 真实帧**（同一 data-dir 两次 `--acp`）：
```
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"loadSession":true,...},"protocolVersion":1}}
{"id":2,"jsonrpc":"2.0","result":{"sessionId":"sess-1794108146301265621"}}
{"id":3,"jsonrpc":"2.0","result":{"sessions":[{"sessionId":"sess-1794108146301265621","title":"","updatedAt":"2026-11-08T11:22:26+08:00"}]}}
{"id":2,"jsonrpc":"2.0","result":{"messages":[],"model":"gpt-4o-mini","sessionId":"sess-1794108146301265621","title":""}}   # session/load
```

### 文档核对
- docs/COVERAGE.md 矩阵重算（§2 28 + §3 34 = 62）：**✅61 / 🟡0 / ❌1**（backend 27✅/0🟡/1❌ winproc，shell 34✅）；摘要与统计表一致（98.4% / 0% / 1.6%），覆盖清单全部关闭。
- README 状态行更新为 Round 8（HEAD a7fe576）。

## 模型配置重构（2026-11-10，多服务商模型列表 + 每模型思考强度）

HEAD cf0c3d8 + 工作区改动。需求：删除模型页「快速预设」与独立「连接」表单；「添加模型」支持常用服务商；
所有模型进入模型列表；删除内置默认模型；对话内模型菜单可给每个模型单独设置思考强度（按模型保存，会话可覆盖）。

### 关键行为
- **模型列表是唯一模型来源**：`modelcatalog.models` 显式存在（含 `"[]"`）即以用户列表为准；产品不再有内置默认模型
  （`contracts.DefaultModelCatalog` 删除）；模型页无「恢复默认」。
- **每模型独立连接**：`ModelInfo` 新增 `baseURL`/`apiKey`；agent 解析顺序为 模型行 → `chat.*` → assembly/env，
  `Config()`/每会话模型/subagent/team 模型覆盖都按行解析各自连接（`contracts.ModelConnection`）。
- **旧配置迁移**：desktop 启动时 `chat.model/baseURL/apiKey` 在用户从未保存过列表的情况下一次性迁移为一条模型行
  （`internal/modules/modelcatalog/migration_test.go`；已有行/空列表/malformed 不迁移，写失败不阻塞激活）。
- **默认模型**：列表中的默认行（`chat.model` 指向行，否则首行）镜像写 `chat.model/baseURL/apiKey`；
  镜像三步写入失败逐键回滚（AUDIT-P3-15 纪律），删除默认行自动提升剩余首行，删空清空镜像。
- **添加/编辑弹窗**：服务商预设（DeepSeek/OpenAI/Ollama/通义千问/Kimi/智谱/硅基流动/OpenRouter/Gemini/自定义，
  `contracts.ModelProviderPresets`）预填 Base URL 与示例模型 ID；API Key 密码输入（新增留空继承同 Base URL 行、
  编辑留空保留、可清除）；弹窗内「测试连接」沿用 `ModelListGetter` seam；编辑弹窗保留思考系列/默认思考/
  生成参数/自定义思考模式（有界滚动 + 高级折叠）。
- **对话框每模型思考强度（按模型单独配置）**：composer 模型菜单在模型切换行下方为**每个模型单独一行**
  `思考强度：<模型名>`（Subtitle `默认：<该模型默认强度/系列默认>`），子菜单写该模型
  `modelcatalog.models` 行的 `DefaultThinking`（部分模型无思考模式时不显示该行）；会话菜单
  `本会话思考强度（<模型名>）：<有效值>` 的覆盖按 **（会话×模型）** 保存
  （`session.<id>.thinking.<modelID>`），切换模型不会串用其他模型的强度，首项「跟随模型默认」清空按模型键
  与旧会话级 thinking/effort 行（旧键仅作旧会话兼容回落）；Home 新对话 composer 同样每模型一行；
  automation 模型选择兜底改为空列表并更新文案。
- **空列表语义**：显式空列表 + 无 `chat.model` 时 `Config().Model` 为空、UI 显示「未配置」；
  `Send`/`SendWithAttachments`/`Regenerate` 返回 `ErrNoModelConfigured`（不发 provider 请求、不改会话）。

### 验证命令与结果
`scripts/verify.sh --no-smoke`（含 -race，410s）：**10/10 PASS**
```
gofmt PASS · go build ./... PASS(4s) · go vet ./... PASS(1s)
designlint --strict: 241 files / 155 violations / 0 error / 0 warn / 0 new（baseline 156）
design-check（6 项 grep）：6 passed, 0 failed
go tool mygo vet PASS
go test ./... -count=1 PASS(41s)
go test ./... -count=1 -race PASS(321s)
regression (-v): 289 PASS / 0 SKIP / 0 FAIL
ui-snapshot: 91 steps / 89 PNGs PASS
verify: PASS（product smokes 见下轮全量）
```
`scripts/verify.sh --quick` 另跑一轮：9/9 PASS（88s，回归 289 PASS）。

### 定向测试（新增/重写）
- `internal/modules/modelcatalog`：迁移（缺失行迁移、已有行/`"[]"`/malformed 不迁移、写失败不阻塞激活、幂等）、
  显式空列表、每模型连接透传。
- `internal/modules/agent`：`model_connection_test.go`（默认/会话/subagent 连接的 providerFactory seam、回落顺序）、
  `no_model_test.go`（显式空列表不发 provider、Send/Regenerate 守卫）、`subagent_model_test.go`。
- `internal/ui/settings`：模型列表空态/行/默认徽章、添加（服务商预填/密钥继承/默认模型）、编辑（连接/清除密钥/
  测试连接/高级折叠）、删除提升默认、镜像写失败回滚、malformed 重写、i18n/en。
- `internal/ui/session`：`model_thinking_test.go`（模型级默认显示/写入、会话跟随模型清除覆盖、空列表菜单）。
- `internal/ui/home`：`model_thinking_test.go`（Home 模型菜单同样语义、空目录深链 model 页）。
- `internal/ui/uimodtest`：desktop E2E 模型页断言、未保存守卫改用系统提示词草稿、P1 场景种子两行模型列表。
- `internal/regression`：`TestModelConnectionCheckDialog*`（弹窗内连接检查 seam/失败/无地址禁用/存 key 不泄漏）、
  `TestProviderPresetAddDialog/CustomKeepsTypedEndpoint/TrailingSlashMatch/MirrorWriteFailureRollsBack`。
- 上述定向包本次合计 **949 PASS / 0 FAIL**（contracts 18 / modelcatalog 26 / agent 231 / settings 292 /
  session 304 / home 33 / automation 45），另 `internal/ui/uimodtest` desktop E2E 6.8s 全绿。

### 快照（人工查看）
`go run ./internal/devtools/ui-snapshot .cache/ui-conformance-model`：
`p2-model-settings.png`（模型列表 + 默认徽章 + 每行服务商/地址/密钥状态）、`p2-model-add.png`（服务商/Base URL/
API Key/测试连接/设为默认）、`p2-model-edit.png`（只读 ID + 连接 + 思考系列/默认思考 + 高级折叠）、
`p2-model-empty.png`（无内置默认模型空态 + 默认模型未配置）、`p2-modelmenu.png`（模型菜单含「模型思考强度」子菜单）。

### 已知限制
- `modelcatalog.models` 行内的 `apiKey` 与旧 `chat.apiKey` 同为本地明文设置（诊断页/导出报告已按 JSON 凭据键掩码）；
- malformed 列表按「未保存过列表」处理（走旧回落链），模型页同时显示可修复提示；
- 显式空列表但仍有 `chat.model` 时按旧语义回落（兼容旧安装），模型页显示该回退默认模型。

### 补充（同日）：模型/推理等级改为两级菜单（按模型独立）
用户两次反馈后的最终形态：① 思考强度按模型独立（不是会话级统一值）；② composer 里只用**二级菜单**选择，
不把模型列表和强度堆在同一个菜单。

- **数据层**：会话内推理等级覆盖按（会话×模型）保存（`contracts.SessionModelThinkingSettingKey` /
  `SessionThinkingOverride` / `SessionThinkingOverrideKeys`；旧 `session.<id>.thinking`/`.effort` 仅作旧会话
  兼容回落）；agent 按（会话×模型）解析，切换模型不会串用其他模型的覆盖；模型级默认 `DefaultThinking` 仍在
  `modelcatalog.models` 行中。
- **会话 composer 模型 chip 一级菜单**（紧凑两行，各自 Trailing 当前值）：
  `模型 → <当前模型>`、`推理等级 → <当前等级>`、分隔线、`打开模型设置…`。
  - `模型` 二级按服务商分组（`MatchProviderPreset`；未匹配“自定义服务/Custom service”，组头禁用，catalog 顺序），
    模型项 radio 当前会话模型、Subtitle 备注、Trailing 上下文窗口，Chosen 切换会话模型；
  - `推理等级` 二级：`跟随模型默认（<该模型默认名/系列默认>）` + 该模型模式（radio 反映显式覆盖；
    选模式写按（会话×模型）键并清旧 thinking/effort，跟随默认清全部覆盖键）；无模式/无 settings/无模型时隐藏该行；
  - chip Tooltip 为「切换本会话模型与推理等级：<model> · <level>」。
- **会话菜单**：宽布局移除思考强度段（避免重复）；veryCompact（模型 chip 折叠）时保留「模型：… / 推理等级」回退入口。
- **Home composer**：一级同样为 `模型`/`推理等级`；模型二级按服务商分组写默认模型（`chat.model`），
  推理二级写该模型 `DefaultThinking`（新会话默认）；空目录保留「打开模型设置…」→ model 页。

验证：`scripts/verify.sh --no-smoke` **10/10 PASS（410s）**：gofmt/build/vet/designlint（0 new）/
design-check 6/6/mygo vet/`go test ./...`/`go test ./... -race`（317s）/regression **289 PASS / 0 SKIP / 0 FAIL**/
ui-snapshot **93 steps / 91 PNGs**。定向包：session 306 PASS、home 35 PASS、automation 45 PASS、agent 234 PASS、
contracts 20 PASS。快照（人工查看）：
`.cache/ui-conformance-model3/p2-modelmenu.png`（一级两行）、`p2-modelmenu-models.png`（按服务商分组的模型二级菜单）、
`p2-modelmenu-thinking.png`（推理等级二级菜单：跟随模型默认/默认/低/中/高强度）。
（`go test ./... -race` 曾出现一次与本改动无关的既有 flaky：regression
`TestFeedbackWriteFailureLeavesReadModelUnchanged` 在满负载下偶发订阅通知计数 1≠0；单独 `-race -count=5` 通过，
重跑全量通过。）

## 会话线程密度重构（2026-11-10，A+B：元信息行 + 过程带/折叠 + 标记列）

用户反馈截图：transcript 里多轮消息之间大量空行、右侧一列动作按钮；要求“时间和用量要显示、用量用紧凑单位（K/M/B，如 100M）、过程行前面有能区分思考/工具调用的标记”。

- **动作区收敛（A）**：删除 32px 整行动作行与独立 usage footer；每条助手消息末尾改为一条右对齐紧凑元信息行
  `时间 · 用时 X · N tokens · ⋯`（时间/用量常显；所有数字走 `uikit.FormatCompactCount`，不展示原始计数）。
  24×24 ⋯ 打开原生菜单：复制消息（写剪贴板 + toast）、重新生成、有帮助/没帮助（radio，按 `SessionFeedbacker`）、
  用量明细子菜单（时长/输入/输出/缓存/上下文预算，全部紧凑值）。用户消息 meta 同样收敛为 `时间 · ⋯`
  （复制原文/编辑消息；无障碍名 `用户消息操作` 与助手区分）。流式无元信息行；失败轮只保留时间/用量 + 错误行 + 重试。
- **过程带与折叠（B）**：思考与非决策工具行缩进 Space4、行距 Space1，组成一条“活动带”；每行左端固定 16px
  **标记列**（sparkles=思考、`uikit.ToolIcon` 分类图标=运行命令/读取文件/搜索/团队任务…、运行中=官方 Spinner），
  行内结构 `[标记] 动作名 [状态] · 预览/详情 [内联 chevron]`，chevron 不再贴阅读列右缘；`uikit.ReasoningTrace`
  与 `uikit.ToolRow` 新增 `MarkerColumn`/`InlineChevron` 选项（默认 false，其他调用方外观不变）。
  完成且过程 ≥2 行时默认折叠为一行摘要 `过程 · 思考 · N 个工具调用 · 用时`（用 `ui.CollapsibleBase` 自建 trigger，
  前置 chevron 标记），下方只显示正文与决策卡；点击展开恢复完整时间线（正文不重复）；流式永远展开；
  状态 `st.processOpen` 与 HoldPlace/prune 同步。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS（426s）**：gofmt/build/vet/designlint（0 new）/
  design-check 6/6/mygo vet/`go test ./...`/`go test ./... -race`（327s）/regression **289 PASS / 0 SKIP / 0 FAIL**/
  ui-snapshot **97 steps / 95 PNGs**。定向包 session/uikit 全绿（新增 process_fold_test 覆盖折叠/展开/持久化/流式/
  决策卡/标记列对齐/英文/失败无动作；uikit 组件 InlineChevron/MarkerColumn 测试）。
- **快照（人工查看）**：`.cache/ui-conformance-density/p2-process-fold.png`（折叠摘要 + 元信息行）、
  `p2-process-expanded.png`（展开时间线：标记列 + 动作名 + 状态 + `·` 预览 + 内联 chevron）、
  `p2-message-actions.png`（⋯ 菜单：复制/重生成/有帮助/没帮助/用量明细）、
  `p2-message-usage.png`（用量明细：2.5s / 42K / 120 / 36K / 128k，无原始长数字）。
- 文档同步：AGENT-SURFACES S1/S8、COVERAGE 消息级操作、DESIGN.md §7.1 自绘白名单条目；
  ui-drive `message-actions-copy` 步骤与 veryCompact/uimodtest 断言同步；`go build ./...`/全量测试通过。

## 会话壳层视觉重构（2026-11-10，B3 顶栏 + composer dock + 通知右上角 + 审批内联）

用户多轮截图反馈后的最终视觉形态：会话标题/Tabs 上提到宿主顶层，去掉返回与 Alt+←，更多改 ⋯；
目标与审批从 header/居中 Modal 移到输入框上方；通知改右上角；审批卡改为 V3 强强调内联卡。

- **B3 顶层顶栏**（`internal/uiroot/frame.go`）：当前屏向 `screen.header` / `screen.actions` /
  `screen.tabs` 有贡献时，宿主渲染一块浅色 band（`BgSidebar` + 仅底边 1px `BorderL2`、行间无线）：
  40px 标题行（标题/工作区 Grow + 屏幕动作 + palette + inspector）+ 可选 32px Tabs 行；无贡献屏
  不渲染任何顶部工具栏（Home/Automation/Plugins 不再有空条）。可见返回按钮与 `Alt+←` 历史回退删除
  （`Navigator.Back` API 保留内部使用）。屏幕级槽位按 item ID 前缀作用域：`session.title` /
  `session.actions` / `session.tabs` 只在 session 屏渲染。
- **session**：Module 注册三个屏幕槽；屏幕 body 删除旧 header（标题/Tabs/更多/目标/团队）；「更多」
  文字按钮 → 24×24 ⋯ 图标（Label「会话操作/Conversation actions」，菜单=复制会话 ID/复制对话/导出
  Markdown/JSONL/归档）。composer 上方新增 dock（`TranscriptWidth` 居中）：目标条（Badge + 目标文本 +
  ⋯「目标操作」）→ `RenderSlot(session.banner)` 审批卡 → composer；Esc 在存在 pending 审批时
  `Resolve(deny)` 最早一条并保持本轮运行，不中断生成。
- **审批 V3 内联卡**（`internal/uikit/approval.go` `Inline`；`internal/ui/approval`）：外层警示描边 +
  `Warning.Alpha(0.22)` 顶部横幅（Shield/加粗「需要审批」/工具友好名/待审批计数/「Esc 拒绝」）+
  中性卡体（影响说明/命令或摘要 mono 块/参数折叠/会话级提示）+ 右对齐「拒绝/本会话内始终允许/允许」；
  无 Default/autofocus；注册到 `session.banner`，非 session 屏收到 pending 时自动 `Push("session")`。
  旧 Modal 路径保留（`Inline=false`，embedder/兼容测试）。
- **通知右上角**（`internal/ui/toasts`）：viewport 改 top-right，`top=design.Space8*2+design.Space5`
  （80 DIP = 72 顶栏 + 8 gap）、右对齐、从顶部向下堆叠；最多 2 条/FIFO/合并 ×N/级别过滤/关闭/超时/
  hover 暂停等行为不变；不遮挡 B3 顶栏与 composer。

验证：`scripts/verify.sh --no-smoke` **10/10 PASS（约 415s）**：gofmt/build/vet/designlint（0 new，baseline
156 条 0 新增）/design-check 6/6（raw metric 45 ≤ 51）/mygo vet/`go test ./...`（42s）/`go test ./... -race`
（310s）/regression **289 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **97 steps / 95 PNGs**。
定向包：uiroot、session、uikit、approval、toasts、uimodtest、regression 全绿；新增测试
`internal/uiroot/b3_header_test.go`（无贡献无顶栏/无返回/命名空间作用域/两行 band 顺序/tabs 位于 body 上方）、
`internal/ui/session/chrome_b3_test.go`（⋯ 菜单/目标条在输入框上方/session.banner 渲染/Esc 审批优先且不中断）、
`internal/uikit/approval_test.go` 与 `internal/ui/approval/*_test.go`（Inline 渲染/决定/队列计数/自动切屏）、
`internal/ui/toasts/toasts_test.go`（右上角定位/向下堆叠）。
快照（人工查看）：`.cache/ui-conformance-final/p1-chat.png`（B3 顶栏 + 无返回 + ⋯/搜索/右栏 + 单条底边线）、
`p1-approval.png`（composer 上方 V3 审批内联卡：警示横幅/描边/命令块/三按钮）、`p2-toast.png`（右上角两条
通知自上而下、位于顶栏下方）、`p2-home.png`（无空工具栏）、`narrow/narrow-session.png`（窄窗标题/Tabs/目标条）、
`narrow/dark-session.png`（深色主题 header band 与右上角通知）。
文档同步：AGENT-SURFACES S1/S4/S7/S8 与 §2 跨 surface 规则、ARCHITECTURE §5.1（B3 band + 槽位作用域）、
INJECTION-POINTS（B3/命名空间/session.banner）、COVERAGE（Approval 内联卡、Chrome、Team chip）、DESIGN.md
（§2 场景表与 §6.3 registry Inline）、本文档。

### 补充（同日）：视觉尺度归一（用户反馈“整体 DPI 偏大/不协调”）

排查结论：不是渲染缩放或 DPI bug——mygo 以 DIP 绘制、快照就是 1280×800 逻辑像素，正文基准仍是
14px。真正的原因是 B3 顶栏把原来的**页面标题**（`TypeL` 20px）搬到了窗口 chrome 位置：它紧邻的
Tabs 是 13px、badge 12px、图标 16px，而标题行 44px 偏高，导致顶栏在整窗里“上重下轻”、像被放大。

归一处理（只改 chrome 尺度，正文/间距体系不动）：
- 会话标题 `TypeL`(20/28, 600) → `TypeM`(16/24, 600)，与 13px Tabs、12px badge、16px band 图标同一尺度；
- B3 标题行 44 → 40 DIP（`Space8+Space2`，Tabs 行仍 32），band 76 → 72 DIP；默认不再用页面标题级字号做窗口标题；
- 右上角 toast 顶部 inset 84 → 80 DIP（72 band + 8 gap），其余通知行为不变；
- 内容区、composer、消息/元信息/过程带字号保持 14/13/12/11 原层级。

验证（归一后重跑）：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/
design-check 6/6（raw metric 45 ≤ 51）/mygo vet/`go test ./...`（45s）/`-race`（321s）/regression
**289 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **97 steps / 95 PNGs**。
快照（人工对比）：归一前 `.cache/ui-conformance-final/p1-chat.png`（标题 20px、band 76） vs 归一后
`.cache/ui-conformance-scale/p1-chat.png`（标题 16px、band 72）；审批卡/通知位置同步复查
`.cache/ui-conformance-scale/p1-approval.png`、`p2-toast.png`，窄窗/深色同前一致。
文档同步：AGENT-SURFACES S1、ARCHITECTURE §5.1、INJECTION-POINTS、COVERAGE（band 40+32）。

### 补充（同日）：显示区域扩大（用户反馈“显示区域太小”，明确不动字体）

用户澄清：字号不是问题、不要动字体；问题是窗口内可用的显示区域偏小。处理：
- 桌面默认窗口 **1400×900 → 1512×944**（最小窗仍 900×600；`internal/host/host.go`
  `mainWindowWidth/Height`）。
- `mainWindowStateKey` 由 `"main"` 升为 `"main.v2"`：mygo 会按旧 StateKey 恢复上次几何，
  旧默认尺寸会“粘住”新安装；升 key 使更大的默认窗口在升级后首次启动生效一次，之后用户
  自行拖拽的尺寸仍按新 key 持久化。
- `ui-snapshot` 的 p1/p2 基准帧同步改为 **1512×944**（窄窗 1024×720 / 900×600 档不变），
  快照证据与新默认窗口一致；字体、token、间距全部未改。

验证：`scripts/verify.sh --no-smoke` **10/10 PASS**（含 `go test ./...` 与 `-race`、regression
289 PASS、ui-snapshot 97 steps/95 PNGs；快照尺寸变为 1512×944）。证据：新默认尺寸整帧
`.cache/ui-conformance-display/p1-chat.png`、`p1-approval.png`、`p2-toast.png`。

### 补充（同日）：正文显示宽度加宽（阅读列 840 → 960）

用户反馈“正文显示宽度再宽一点，两边减少一点空白”。处理：`design.TranscriptWidth`
**840 → 960 DIP**（单一 token，transcript / composer / composer dock / approval 卡同时对齐
变宽）；窄窗仍按 `MaxWidthPercent(92)` 收缩，不产生横向溢出。默认窗口已在上一条扩到
1512×944：内容列 1232 时两侧空白由 (1232−840)/2 = **196 DIP** 降到 (1232−960)/2 = **136 DIP**，
正文列加宽 120 DIP。字号、字重、间距 token 均未改（按用户「不要动字体」）。

验证：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check
6/6（raw metric 45 ≤ 51）/mygo vet/`go test ./...`（43s）/`-race`（320s）/regression
**289 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **97 steps / 95 PNGs**（基准帧 1512×944）。
快照（人工查看）：`.cache/ui-conformance-wide/p1-chat.png`（正文列 960、两侧空白 136）、
`p1-approval.png`（审批卡/composer 同步对齐 960）、`p2-toast.png`；对照上一版 840：
`.cache/ui-conformance-display/p1-chat.png`。
文档同步：AGENT-SURFACES S1 布局、DESIGN.md 布局 token 表、host 窗口注释。

## 0.2.1 补丁（2026-11-10：消息元信息上移 + 移除消息评分）

用户反馈两处：① 每条 AI 回复下面的独立「时间 · 用时 · tokens · ⋯」行在只有过程折叠的回复里像一行空白，
应移到上面更紧凑；② 消息的「有帮助 / 没帮助」评分功能不需要，全部删除。

- **元信息上移**：助手消息的 meta（时间/紧凑用量/⋯）改为**消息顶部**；可折叠过程的消息把 meta
  放在「过程 · 思考 · N 个工具调用」摘要行右端（同一行，省掉原来的一行）；不可折叠的消息 meta
  是回复上方一条右对齐紧凑行；流式无 meta；用户消息 meta 保持在气泡下方。实现：`buildAssistantMetaContent`
  与折叠摘要同行，`buildAssistantMetaRow` 只作顶部包装，折叠触发器在行内创建；`CollapsibleBase`
  触发器与 meta 为同一 Row 的兄弟，⋯ 点击不会触发折叠。
- **评分功能整体移除**：删 `contracts` 的 `ChatMessage.Feedback`、`FeedbackGood/Bad`、
  `SessionFeedbacker`、`MessageFeedbackSettingKey`；agent 的 `SetMessageFeedback`/读模型注入；
  session 的 `/反馈` 命令、评分对话框、⋯ 菜单评分项与相关状态；导出（Markdown/JSONL）与
  诊断报告里的评分字段；`ui/settings` 数据与隐私导出/诊断的评分统计；配套测试与文档。
  旧版本遗留的 `feedback.<session>.<message>` 设置行不再被读取/导出，不影响会话数据。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/
  design-check 6/6（raw metric 45 ≤ 51）/mygo vet/`go test ./...`（42s，47 包）/`-race`（310s）/
  regression **276 PASS / 0 SKIP / 0 FAIL**（移除 13 个评分相关回归用例）/ui-snapshot
  **97 steps / 95 PNGs**。
- **快照（人工查看）**：`.cache/ui-conformance-meta2/p1-chat.png`（meta 在消息顶部）、
  `p2-process-fold.png`（折叠摘要与 meta 同一行）、`p2-message-actions.png`（菜单只剩
  复制消息/重新生成/用量明细）、`p2-message-usage.png`。

## 0.2.2 补丁（2026-11-10：同回合多步过程合并 + 最终回复底部复制）

用户反馈：同一回合连续多个「过程 · 思考 · 1 个工具调用」行内容相同，应合并；并且最终回复底部要有复制按钮。

- **同回合多步过程合并**：`uikit.SessionThread` 新增 `SameTurn` / `GroupRow` 两个可选钩子（未设置时行为
  不变）：连续且 `SameTurn` 接受的窗口消息共享一个 List 行，行名用步骤范围（「助手 1–3」）。session
  的 `assistantTurnContinues` 把「已结束、无错误、带工具调用的助手步骤」串成一回合；`buildAssistantTurnGroup`
  汇总工具数与思考（「过程 · 思考 · 合计工具数」），默认折叠，折叠时只渲染**最终结果**（最后一步回复 +
  必要的决策卡），中间步骤的正文/工具行收进折叠区；展开恢复每一步的完整时间线。折叠状态按首条消息 ID
  保存；meta 用量按回合汇总（时长/tokens/迭代求和，上下文取最后一次请求）。
- **最终回复底部复制**：最后一条已结束回复正文下方新增常显「复制」按钮（Label「复制回复」），点击写剪贴板
  并 toast「已复制」；与 ⋯ 菜单「复制消息」复制同一文本。流式/失败轮不显示。
- **测试**：uikit `TestSessionThreadGroupsConsecutiveRuns`（分组行/单行/行名范围）；session
  `TestAssistantStepsFoldIntoOneTurn`（默认只显示最终结果、合计摘要、展开恢复三步）与
  `TestLatestReplyHasBottomCopyButton`（按钮可见、点击复制、Label）；原 `TestTrajectoryTimelineShowsToolCalls`
  改为在聊天页先展开合并摘要再断言工具行。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check 6/6
  （raw metric 45 ≤ 51）/mygo vet/`go test ./...`（41s，47 包）/`-race`（307s）/regression
  **276 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **97 steps / 95 PNGs**。
- **快照（人工查看）**：`.cache/ui-conformance-turn/p1-chat.png`（最终回复底部「复制」按钮、meta 顶部）、
  `p2-process-fold.png`、`p2-message-actions.png`。

### 补充（同日）：CI 稳定性修复（不涉及产品二进制）

- GitHub CI 在 v0.2.1/v0.2.2 的 master 推送上报了 3 个异步时序 flake（`internal/ui/plugins` 的
  `TestMCPRefreshUpdates`/`TestMCPRefreshRunsOffUIThread`，`internal/ui/rightpanel` 的
  `TestFilesListingIsCachedBetweenFrames`）：断言在后台刷新 goroutine 真正开始/回投之前就读取了计数。
  修复为按 deadline 轮询（`pumpFrames`/等待 refreshStart、等待 listing count 与「已刷新」文本），
  `-count=10` 与 `-race -count=3` 本地全绿。
- 两个 workflow 的 `runs-on` 从 `ubuntu-latest` 固定为 **`ubuntu-24.04`**：GitHub 通告
  ubuntu-latest 将于 2026-10 迁移到 Ubuntu 26，产品快照/工具链对运行镜像敏感，固定 LTS 避免
  CI 无声漂移（升级镜像需单独测试后修改）。

## 0.2.4（2026-11-10：工作步骤对齐 DSH，默认标准 + 回答结束后折叠）

用户反馈：运行中每个读取文件/执行命令都实时展开，整段内容不断滚动；DSH 的「工作步骤」默认是标准，
运行中只有一行原地更新，完成后折叠。设计评审通过：方案 A（transcript 内单行）+ 完整四档 + 收起时机。

- **设置（对齐 DSH）**：新增 `ui.workSteps.mode`（compact / **standard（默认）** / detailed / verbose）与
  `ui.workSteps.collapse`（**completion（默认）** / next-input），设置 → 行为页新增可见「工作步骤」区
  （工作步骤展示 4 档 Segmented + 每档说明；工作步骤收起时机 2 档），写行立即生效；空/非法值归一为默认。
- **运行中**：标准/简洁渲染一行实时步骤（活动图标 + 状态词 + 标准档实时细节 + 用时；状态词按工具族：
  正在分析请求/正在读取文件/正在运行命令/正在搜索网页/正在协调子智能体/等待你的操作…；用时在满 1s 后出现），
  不再把思考披露和每个工具行铺进 transcript；流式正文与决策卡（ask_user/plan_review/exit_plan_mode）仍显示。
  详细/完全展开保留完整实时时间线。旧行为 = 完全展开档。
- **完成后**：简洁/标准/详细把可折叠回合收成一条摘要（有工具或有思考才折叠；纯思考摘要为「过程 · 思考」）；
  完全展开永不折叠、不分组、直接显示完整时间线；next-input 时最新一轮完成后保持展开，下一条用户消息到来后回落折叠；
  用户手动展开/折叠始终优先。同一回合多步合并（沿用 `SameTurn/GroupRow`）在非 verbose 档保留。
- **测试**：session 新增 `worksteps_test.go`（四档流式/完成态、活动词 19 个 tool id、中英、用时省略、next-input、
  显式优先、分组/不分组、未知值归一、逐帧读设置）；settings 新增 `work_steps_test.go`（默认/写入/非法值/
  写失败/英文/区块顺序）；uimodtest/regression 的旧流式断言改为展开摘要或 verbose 设置。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check 6/6
  （raw metric 45 ≤ 51）/mygo vet/`go test ./...`（43s，47 包）/`-race`（316s）/regression
  **276 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **97 steps / 95 PNGs**。
- **快照（人工查看）**：`.cache/ui-conformance-worksteps3/p1-approval.png`（运行中单行「正在协调子智能体」，
  不再铺时间线）、`p2-behavior.png`（设置 → 行为 → 工作步骤：标准/回答结束后默认选中）、
  `p2-process-fold.png`（完成后的摘要 + 元信息 + 复制）。
- **文档同步**：AGENT-SURFACES S1（工作步骤展示）与 S8、COVERAGE 消息级操作、ui-drive 行为页检查。

## 0.2.6（2026-11-10：主框架 DSH 对齐，M1a–M3）

用户要求按 DSH 重做主框架并"一点一点累加"到 M3：顶部三入口、左栏减负、右栏默认收起多标签、团队/子代理进主内容区、生成文件可侧栏预览、删除旧检查器代码、TDD 开发、发布小版本。

- **顶部（M1a/M1c）**：会话标题行顺序 = 标题/面包屑 → 智能体团队 → 子智能体 N → N 个后台任务 → spacer → ⋯（运行中 badge 在 spacer 后）。团队浮层 = 成员卡（状态点/名/模型/最新活动/打开）+ 共享任务卡（status/owner/blockedBy/write scopes），`ui.PopoverBase` 原生浮层、Esc/点外关闭/焦点归还；子代理 switcher 选中后主栏切到子会话，标题变 `父标题 › 子智能体名` 面包屑，点父标题返回；一次性只读、可继续队友有 composer（`SendChildAgent`）；后台任务浮层显示状态/命令/用时/退出码并可停止（stoped 负退出码显示 "—"）。新增 `ChildAgentService` 统一读模型（agent）、`JobService`（toolkit）、`FilePreviewer`（rightpanel）。
- **右栏（M1a/M1b）**：默认收起；多标签 = 开始 guide（工作区文件 / 新建终端，等宽卡片、无说明文字、Ctrl+Alt+P / Ctrl+`）+ 文件 + 终端 + 每个打开文件一个可关闭标签；文件页含工作区树（懒加载/400 行上限）、交付区、文本/Markdown/图片预览、文件内折叠 diff；终端页复用命令运行器；所有页面滚动安全。删除旧 8 页签与概览/工具/日志/变更/团队/任务 pane、pane 选择器/切换命令/快捷键。
- **生成文件**：回合尾部「交付文件」（write_file）与「已编辑 N 个文件」（edit_file/str_replace_editor）卡片；每行「在侧边栏预览（PreviewFile）」「查看改动（文件标签内折叠 diff）」「用系统应用打开（OpenNative，internal/lib/openpath）」；正文文件卡打开右栏新标签。
- **左栏（M1d）**：删除首页导航（启动落在会话/新对话 hero；rail 同步去掉首页）；会话树/搜索/归档均隐藏 `自动化: ` 运行会话（运行历史留在自动化页）；保留 会话/自动化任务/插件/设置。
- **删除**：`internal/uikit/team.go` + tests（1875+842 行）、rightpanel agents/tasks/changes/team_* pane 与旧测试、旧团队/任务命令、toolcards 团队 footer；diff 为 +9.5k/−12.3k（净减约 2.9k 行，含测试与文档）。
- **TDD 证据**：session `agentsurface_test.go`（19 帧测试）、rightpanel 60 帧测试（guide/tabs/files/filepreview/terminal/deliverables/diff）、agent `childagents_test.go`（6）、toolkit `jobs_service_test.go`（11）、sidebar/home/uiroot 更新、uimodtest 8 个 E2E 重写；devtools ui-drive **91/91 PASS（156 shots）**、ui-snapshot **96 PNGs**。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check 6/6（raw metric 42 ≤ 51、ButtonBase 9 ≤ 10）/mygo vet/`go test ./...`（48 包，43s）/`-race`（314s）/regression **276 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **98 steps / 96 PNGs**。
- **快照**：`.cache/m1-devtools/snapshot/`（p2-guide、p2-files、p2-file-preview、p2-file-diff、p2-terminal、p2-team-overlay、p2-team-child、p2-child-switcher、p2-team-tasks、p2-jobs、p2-jobs-stopped、p2-landing、narrow/*）、`.cache/m1c-shots/*`、`.cache/m1-rightpanel/*`。
- **限制**：P2 在本版是"统一子会话呈现层"（ChildAgent 读模型来自现有 subagent/团队运行记录 + 主栏面包屑导航），尚未持久化真正独立的子会话 `ChatSession` 存储；后续可把 `ChildAgentService` 换成持久子会话实现而不改 UI。

## 0.2.7（2026-11-10：顶栏长标题与三入口补丁）

用户截图反馈：会话标题过长占据顶栏；且顶栏只剩「智能体团队」，另外两个入口（子智能体/后台任务）不见了。

- **标题截断（DSH crumb 规则）**：主会话标题 `MaxWidth(design.Space8*7 = 224 DIP)` + 单行省略，完整标题进 Tooltip；不再挤压 agent 入口与右上角工具。新增帧测试 `TestHeaderLongTitleIsTruncatedLikeDSH`（标题宽度 ≤ 224 且团队入口在其右侧）。
- **三入口常显**：`buildSubagentEntry` / `buildJobsEntry` 在对应后端存在时始终渲染，计数 0 也显示（「子智能体 0」「0 个后台任务」）；子智能体 switcher 与后台任务浮层新增空态文案（「还没有子智能体…」「暂无后台任务…」）；只有后端缺失时才隐藏。新增 `TestSubagentChipShownAtZero`、`TestJobsChipShownAtZero`；旧 `TestJobsChipHiddenWhenNoJobs` 改为零态可见断言。
- **退出码**：后台任务被停止/超时后 manager 返回负值哨兵，显示「—」而不是「-1」（`backgroundJobExitLabel`）。
- **证据更新**：ui-drive `jobs-chip` 步骤改为断言 0 任务 chip + 空态浮层后重新跑通；`go run ./internal/devtools/ui-snapshot .cache/m1-devtools/snapshot-fix` 96 PNG，人工确认 p2-jobs / p2-team-child 顶栏三入口与截断标题。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check 6/6/mygo vet/`go test ./...`（40s，48 包）/`-race`（315s）/regression **276 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **98 steps / 96 PNGs**。

## 0.2.8（2026-11-10：更新器修复 + 独立对抗审计）

用户反馈：Windows 提示"重启后升级"但实际不升级；更新应下载安装包直接运行；macOS 需要静默升级方法；darwin/windows 的 tar.gz 更新资产无用。随后进行了 3 个独立 subagent 的对抗审计（更新器/壳层 UI/跨切面），报告在 `.cache/audit/{updater,shell-ui,cross-cutting}.md`。

- **更新器**：GitHub 资产选择改为 Windows setup exe、macOS dmg、Linux AppImage（tar.gz 仅旧 release 兼容回退）；install kinds 全链路 Kind；下载后「安装并重启」通过 host 命令 `uiroot.install-update` 立即执行。Windows 不再用 `cmd.exe /C "start ..."`（os/exec 的 CommandLineToArgvW 转义与 cmd /C 规则冲突，setup 实际不会启动）：改为把**本可执行文件**以隐藏 helper 模式（`--zeokeel-install-after-exit` / `--zeokeel-open-after-exit`）启动，普通 argv 由 Go 无损传参，helper 等到应用退出后用 `exec.Command` 启动 setup/新 app，无 shell 拼字符串；安装器窗口保持可见（HideWindow 只用于重启）。macOS 修复 `singleAppInDMG`（真实 dmg 含 `.DS_Store`/`.VolumeIcon.icns`/`Applications` 符号链接，旧逻辑要求唯一顶层项导致静默安装永远不执行）；dm​g 替换保留旧 bundle 到下次启动、失败恢复旧 bundle；AppImage 用稳定系统 shell 延迟重启（helper 在 AppImage 临时挂载里可能随卸载消失）。host 启动对 installer/dmg 不再自动应用（等待显式安装），按 `opts.AppVersion` 清理已安装计划，installer/dmg 启动时 0 次哈希、应用类 plan 仅 1 次；清理顺序改为先删 staged 再删 pending。设置页支持失败后「重试安装并重启」，命令按存在性查找（忽略 Enabled，排程后 Enabled 变 true），平台文案按 GOOS 区分。
- **壳层审计修复**：折叠侧栏启动时 nav.Current 为空导致标题/tab/入口/右栏全被过滤（默认屏在任何 chrome 读取前解析）；子会话中子智能体 switcher 双 owner（header chip + breadcrumb）导致 mygo panic，改为 breadcrumb 独占并独立 ScrollState；子会话保留父级审批 banner，Esc 先返回父会话不盲拒；生成文件卡片只收成功调用、Path 优先、Arguments 回退、每卡 8 行；FilePreviewer 的 PreviewFile/OpenNative 统一工作区包含检查（拒绝 `../`、工作区外绝对路径、符号链接逃逸）；工作区切换后编辑器/预览按 (workspace,path) 隔离并丢弃旧缓存；自动化会话改为显式 `Origin` 元数据（auto 写 setting、agent 读模型带出、侧栏隐藏非用户 origin），去掉标题前缀启发式；标题按 DSH crumb 宽度截断、三入口常显（0.2.7）；图像预览有 16MiB/24MP 边界与诚实错误。
- **发布资产**：release workflow 不再构建/上传 `darwin-arm64.tar.gz`、`windows-amd64.tar.gz`、`windows-arm64.tar.gz`；只发布 Windows setup、macOS dmg、Linux AppImage 与 `SHA256SUMS.txt`。README/RELEASE/INJECTION-POINTS/AGENT-SURFACES/DESIGN registry 同步；删除旧 TeamSurface registry 条目。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check 6/6/mygo vet/`go test ./...`（48 包，45s）/-race（329s）/regression **276 PASS / 0 SKIP / 0 FAIL**/ui-snapshot **98 steps / 96 PNGs**；新增审计修复测试（session `audit_fix_test.go` 9 个、rightpanel 73 PASS、host 164 PASS/update 47 PASS、agent/sidebar/automation provenance 与 Path、uiroot collapsed 启动）。
- **审计结论**：3 份报告无遗留 P0；已修 P0/P1（Windows setup 不启动、macOS 静默安装死代码、安装计划/UI 流程、子会话 panic、折叠启动、生成文件真实性、路径越权、缓存串工作区、标题前缀误伤）。**仍有 P2 已知风险**：旧版（≤0.2.7）无 origin 的自动化会话会重新出现（显式-only 的迁移代价）；无宿主命令的嵌入构建对 installer/dmg 的"下次启动自动安装"文案仍不成立；header 每帧读取 team/child/jobs 后端（jobs 会解析 job_list 文本）；立即安装仍在 UI 线程同步执行（macOS 大 dmg 会卡数秒，spinner 可能来不及绘制）；「查看改动」当前等价于打开文件标签（差异在文件标签内折叠显示），文案与实现需再对齐；Windows/macOS 安装路径仅有 seam 单测，发版前需真机 smoke。

## 0.2.9（2026-11-10：P2 继续修 + Windows cmd 弹窗）

用户反馈：Windows 某些工具调用仍弹 cmd 窗口；继续修审计 P2。

- **Windows 隐藏控制台**：桌面版是 GUI 子系统，rig 的 pwsh/bash/后台任务/终端在无控制台父进程中启动时，Windows 会为每个子进程新建控制台窗口。新增 `internal/host/console_windows.go`：启动时若无控制台则 `AllocConsole`+`ShowWindow(SW_HIDE)`，子进程继承隐藏控制台，不再逐个弹 cmd；从终端启动（已有控制台）保持原样。非 Windows 为 no-op；`GOOS=windows` 交叉构建通过。
- **job 列表解析**：status/command 自由文本包含 ` exit=`/` duration_ms=`/` command=` 时会切错字段；改为右侧锚定正则 ` exit=(-?\d+) duration_ms=(\d+) command=`，新增自由文本测试。
- **mac 快捷键标签**：右栏文件/终端命令 shortcut 在 darwin 显示 Cmd+Alt+P / Cmd+`（host 实际绑定 Cmd），Linux/Windows 仍 Ctrl。
- **文档**：COVERAGE/UI-CONFORMANCE/DESIGN registry 清除旧右栏团队/任务 pane 描述；INJECTION-POINTS 记录新命令。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**：gofmt/build/vet/designlint 0 new/design-check 6/6/mygo vet/`go test ./...`（48 包，46s）/-race（325s）/regression 276 PASS/ui-snapshot 98 steps / 96 PNGs。
- **发布说明**：v0.2.8 的 macOS dmg 因 GitHub macOS arm64 runner 容量连续 3 次排队失败未发布；Windows/Linux 安装包已可用。macOS 用户需等待 dmg 补传或源码构建；≤0.2.7 需手动安装一次。

## 0.2.12（2026-11-10：全 Linux 生成/签名/公证 macOS dmg）

用户要求不用 macOS CI 机器也能出 dmg + 签名。mygo 官方只支持在 macOS 上 `codesign`/`hdiutil`，因此改用 Mozilla/Firefox 同款 Linux 工具链：

- **dmg 生成**：`libdmg-hfsplus`（`mkfs.hfsplus` + `hfsplus` + `dmg`）在 `ubuntu-24.04` 生成 UDIF dmg；Firefox CI 的 `mozpack/dmg.py` 正是这套工具。
- **签名/公证**：`rcodesign`（apple-codesign 0.29.0）在 Linux 上签 `.app`/dmg、`notary-submit --staple` 提交 Apple 并 staple；Mozilla RelEng 文档与 `signingscript/rcodesign.py` 使用同一工具做 Linux 公证。
- **流水线**：`package-macos` 改为 `ubuntu-24.04`，与 Windows/Linux 编译 job 并行；`build-macos` 仍负责 Go 交叉编译并上传 `.app` artifact（以及 tar.gz 回退）。所有 release job 均为 `ubuntu-24.04`，无 macOS runner。
- **本地验证（未提交的测试产物在 `.cache/dmg-*`）**：Linux 构建的 `ZeoKeel.app` → `libdmg-hfsplus` 生成 dmg → `dmg extract` 回读确认 `ZeoKeel.app` + `/Applications` 链接；`rcodesign generate-self-signed-certificate` + `sign`（app 和 dmg）成功，`print-signature-info` 显示 DMG CodeDirectory/CMS；打包脚本 `scripts/package-macos-dmg.sh` 签名/未签名两条路径均跑通。Apple 公证需真实 secrets，未在本地提交（CI 用 `APPLE_API_*`）。
- **降级**：`package-macos` optional；secrets/工具链失败时 dmg 缺失，Windows/Linux 和未签名 tar.gz 仍发布；updater 优先 dmg，回退 archive。
- **验证**：`scripts/verify.sh --no-smoke` **10/10 PASS**（gofmt/build/vet/designlint 0 new/design-check 6/6/mygo vet/`go test ./...` 48 包/-race/regression 276 PASS/ui-snapshot 98 steps / 96 PNGs）。
- **限制**：Linux 生成的 dmg 仍需在真 Mac 上手工验证一次（挂载、安装、Gatekeeper、公证/签名）；dmg 内 `.app` 未 staple ticket 时依赖 dmg 公证；`rcodesign` 属第三方工具，需要信任其处理 `.p12`/API key。

## 0.2.13（2026-11-10：修复 Linux dmg 构建）

v0.2.12 的 package-macos job 在 GitHub Ubuntu runner 上构建 libdmg-hfsplus 失败：runner 的 OpenSSL 3 头文件与 libdmg FileVault 的 OpenSSL 1.x HMAC/AES API 不兼容（`HMAC_CTX` 不完整类型）。本地用 `-DOPENSSL_FOUND=OFF` 曾成功，但 CI 的 CMake 仍找到 OpenSSL；改为构建前 sed 关闭 `dmg/CMakeLists.txt` 的 OpenSSL 分支（`IF(OPENSSL_FOUND)` → `IF(FALSE)`），并在本地用同一补丁重新构建 `dmg`/`hfsplus` 成功。其余与 v0.2.12 相同，待 v0.2.13 release 验证真实 dmg 资产。

## 0.2.14（2026-11-10：现成 action + 预编译 dmg 工具）

- **签名/公证**：改用 `indygreg/apple-code-sign-action@v1.2`（rcodesign 作者维护，支持 Linux runner），分别签名 `.app`、签名 dmg、notarize+staple；不再手写 rcodesign 调用。
- **dmg 工具**：预编译 `JuliaBinaryWrappers/libdmg_hfsplus_jll.jl` 的 `libdmg_hfsplus.v0.5.0.x86_64-linux-gnu`（`dmg`/`hfsplus`），不再 cmake 编译；Ubuntu 依赖 `hfsprogs`（mkfs.hfsplus）与 `liblzfse-dev`。
- **Actions 版本**：`actions/upload-artifact@v7`、`actions/download-artifact@v8`、checkout/setup-go 最新 major。
- **真实凭据本地验证**：使用 `/root/apple_key` 的 Developer ID `.p12` 与 App Store Connect key，在 Linux 上跑通 dmg 生成 → 签名 → Apple 公证 **Accepted** → staple（`.cache/dmg-real/run.log`，未提交私钥）；公证日志显示 app 与 dmg 的 cdhash。签名前用 `openssl pkcs12 -clcerts` 提取叶子证书，避免 Keychain p12 中 CA 在前的错误签名；app 显式 `--code-signature-flags runtime`。
- 全 Linux release jobs；`package-macos` 仍是 optional，secrets/工具异常时回退 tar.gz。

## 0.2.18（2026-11-10：合并 macOS 编译与打包，去掉 tar.gz 回退）

- 删除单独的 `build-macos` job 和 upload/download artifact 往返：`package-macos`（`ubuntu-24.04`）现在先执行 `mygo build -platform darwin/arm64`，再在同一 job 内生成 dmg、签名、公证、staple；同一系统版本没必要拆两个 runner。
- 不再发布 `zeokeel-<version>-darwin-arm64.tar.gz`；macOS 资产只有签名+公证的 `zeokeel-<version>-arm64.dmg`。更新器的 archive 兼容逻辑保留用于旧 release。
- `publish` 需要 `package-macos`/`build-windows`/`build-linux`；macOS job 仍 optional（失败时 Windows/Linux 正常发布，只是没有 macOS 资产）。

## 0.2.19（2026-11-11：交互与视觉审计全量落地）

- **来源**：[docs/UI-AUDIT-2026-11-11.md](UI-AUDIT-2026-11-11.md)（审计 v0.2.18：P1×3 / P2×15 / P3×16 + 统一设计语言 v2）。用户接受全部建议后，按 P1 → P2 → P3 → v2 顺序实现；审计报告新增 §10 修复状态与验收闭环。
- **实现要点**：
  - 安全：`uikit.ConfirmDanger`（取消 Default、危险非 Default）迁移 14 处危险确认；审批 Esc 提升到窗口作用域；审批摘要 fallback。
  - 交互：设置搜索别名（`settingsPageKeywords`）、窄窗抽屉让宽 + elevation、运行态键位提示、粘贴图片附件（session/Home）、palette 一次 Esc、侧栏折叠展开按钮、搜索命中高亮、导入三态、热键录入首屏、模型页保存语义拆分、固定「会话设置」chip、零值入口隐藏、团队任务短 ID/owner、@ 错误本地化恢复。
  - 视觉 v2：选择态两级（tint + 2px Accent 条 / 瞬态 accent 填充）、强调色预算、StatusBadge 统一、高度 32/36/40/44（删 38）、圆角语义、暗色 surface ramp（导航下沉）、Segmented 选中、MarkdownDoc、tab 下划线、列表去卡墙、顶带统一（automation/plugins）、属性表、toast 骨架、ControlChip、DisabledPrimary。
  - 契约：`AGENTS.md`（危险默认/预算/选择态/字号度量/主操作落点）、`docs/DESIGN.md`（§5.1 surface ramp、§5.3 字号规则、§5.4 度量、§5.5 预算、§6 预设/组合、AP-19..AP-25）、`docs/DESIGN-LINT.md`（DS-11..DS-16）、`internal/design/registry.json`（新组件 + AP）。
- **验证**：`go test ./... -count=1` 全绿（含 regression / uimodtest / design 契约测试）；`gofmt -l internal cmd` 空；`go build ./...`、`go vet ./...`、`go tool mygo vet .` 干净；`designlint --strict --baseline` **0 error / 0 warn / 0 new / 0 blocking**；`scripts/design-check.sh` 6/6 PASS；`ui-snapshot` 96 PNG（`.cache/fix-final/shots/`）；`ui-drive` **91/91 PASS**（`.cache/fix-final/drive/drive-report.md`）。
- **已知残余**：toast 堆叠时第二条仍可能短暂覆盖 transcript 右上 meta（4s、指针穿透）；会话记忆行与设置记忆行仍两套行尾操作（V22，下一轮抽 `uikit.ListRow`）；全仓 11px 静态审计（DS-15 AST）、设置对话框标题全量统一（V9）、1024+抽屉顶带重排（V27）列为后续增量；640×480 低于产品最小窗，设置 footer 仅记录；暗色会话 transcript/工具卡未逐帧覆盖，依赖 token 测试与浅色对照；Home 粘贴图片钩子已接，真实桌面剪贴板未真机验证。

## 0.2.20（2026-11-11：签名表面第二阶段 + 过减法修正）

- **Agent Run Band**：`session.buildAgentRunBand` 在 `SlotScreenHeader` 中把运行中/智能体团队/子智能体/后台任务/待审批收进一条 hairline 状态带；团队与子代理零值常显（关键功能可发现），后台任务/待审批只在有内容时出现，全空时不渲染。旧 `buildAgentHeaderEntries` 与 actions slot 的运行中 badge 合并进 band。
- **State-first Tool Row**：`uikit.ToolRowOptions.State` + 3px 状态 gutter（Running=Warning/Error=Danger/Cancelled=neutral；Success 安静），折叠摘要改等宽输入摘要；toolcards 与 session fallback 都传 `State`；新增 `TestToolRowStateGutter`（24 子测试，像素断言）与组件标本帧。
- **过减法修正**：恢复团队/子代理零值入口、状态条 6 项（轮/步/tok/s/上下文/缓存/剩余）、逐条消息「用时 · tokens」；保留的减法只限重复/装饰（设置眉标、卡外复制、卡片墙）。
- **契约**：AGENTS 规则 17；DESIGN §6.3 `session.buildAgentRunBand`；AGENT-SURFACES S9；registry `session.agent-run-band`；DESIGN-LINT DS-17。
- **验证**：`go test ./... -count=1` 全绿；gofmt/build/vet/mygo vet 干净；designlint **0 new / 0 blocking**；design-check 6/6；`ui-snapshot` **101 次渲染 / 100 唯一 PNG**（`.cache/phase2/shots/`）；`ui-drive` **91/91 PASS**（`.cache/phase2/drive/`）。
- **残余**：Tool Row 完成态不保留 gutter lane，running→success 有 11px 瞬态左移（为保持完成行与相邻 reasoning 行同一 inset；若要求零 reflow 再改为透明 lane）；窄带 band 只显示图标+计数（Tooltip/可及名保留全称）。

## 0.2.21（2026-11-11：Markdown 阅读面整体升级）

- **`uikit.ChatMarkdown`**：会话正文 Markdown 主题（14px 正文 + 1.55 比例行高；H1 17–18/H2≤16/H3≤15 且低于屏幕标题；标题上下距收紧；行内代码 `BgStrong`+等宽；引用/分隔线 `BorderL2`；表格头 `BgStrong`、单元格 0.7/0.45；链接 Accent+500；暗色同构）。
- **`uikit.MarkdownCodeBlock`**：官方 `markdown.DefaultCodeBlock` 产品覆盖（`BgStrong` 填充、无边框、`RadiusSM`；语言只在真实命名时显示；官方 Copy/流式/高亮/横向滚动保留），接入会话、推理、检查器文档。
- **fixture**：`ui-snapshot`/`ui-drive` 的脚本答案不再输出 `text /data/mygo`，改为 `bash` + `go test ./... -count=1`；新增 `p2-markdown-specimen[-dark]` 覆盖所有 Markdown 块。
- **验证**：`go test ./...` 全绿；gofmt/build/vet/mygo vet 干净；designlint 0 new/0 blocking；design-check 6/6；`ui-snapshot` **103 次渲染 / 102 唯一 PNG**（`.cache/phase3/shots/`）；`ui-drive` **91/91 PASS**（`.cache/phase3/drive/`）。
- **残余**：Markdown 表格按内容宽度（未强制铺满阅读列）；代码围栏在短代码时仍占满整行（为横向滚动与对齐保留）。

## 0.2.22（2026-11-11：短代码围栏收缩）

- `uikit.MarkdownCodeBlock`：测量最长代码行（`design.MonoFontStack` + `TypeXS` 13px），围栏宽度 = 最长行 + `Space3*2`，最小 128 DIP（`Space8*4`）；`MaxWidthPercent(100)` 保证长行不越出阅读列并继续横向滚动。每帧只测量最长一行（按字节长度选取，CJK 偏宽取上界）。
- 证据：`p1-chat` / `p2-markdown-specimen[-dark]` 短围栏已收缩；`go test ./...` 全绿；designlint 0 new/0 blocking；design-check 6/6；`ui-snapshot` **103 次渲染 / 102 唯一 PNG**；`ui-drive` **91/91 PASS**。

## 0.2.23（2026-11-11：Tool Row 零 reflow + 遗留项）

- **状态 gutter 零 reflow**：`uikit.ToolRow` 的 gutter 改为固定宽度的左边框（active 染 Warning/Danger/LabelTertiary；success/idle/unknown 透明），宽度始终占位；running→success 只换色。`uikit.ReasoningTrace` 在 marker-column band 补同宽透明边，保持工具/推理 glyph 对齐。新增 `TestToolRowStateGutterDoesNotReflow`（running/success 名字矩形一致）。
- **运行中工具行产品帧**：新增 `TestRunningToolRowRendersInProductSession`（真实 session surface 渲染 `正在运行命令 · go test ...`）。
- **「回到最新」叠压**：短围栏收缩后复制按钮回到内容宽度内，`p2-tool-row-complete` 复核无叠压；浮动按钮仍为 bottom-right 覆盖层。
- **验证**：`go test ./...` 全绿；designlint 0 new/0 blocking；design-check 6/6；`ui-snapshot` **103 次渲染 / 102 唯一 PNG**；`ui-drive` **91/91 PASS**。

## 0.2.24（2026-11-11：产品级缺陷评审首轮 TDD 修复）

- **范围**：`docs/PRODUCT-BUG-REVIEW-2026-11-11.md` 的 P0/P1 与全部 P2（FR-28 明确 Deferred）+ 高价值 P3；每项先补失败回归测试再实现。覆盖安全/权限（MCP 多 server、settings_get、read-only override、LSP Initialize/PathPolicy、损坏数据隔离、tools.disabled）、loop/压缩/会话删除、prompt 注入（模式/指引/AGENTS/@session/子代理预设/记忆技能注入）、MCP 生命周期、记忆 scope、技能根与 frontmatter、工作区 per-run 根、ACP `cwd`、首启/错误恢复/审批归属等。
- **验证**（`./scripts/verify.sh` 全量，2026-11-11 13:2x）：
  - `gofmt` / `go build ./...` / `go vet ./...` / `go tool mygo vet` 全部 PASS；
  - `designlint --strict` 0 error / 0 warn / 0 new / 0 blocking；`scripts/design-check.sh` **6/6 PASS**；
  - `go test ./... -count=1` PASS；`go test ./... -race` PASS；
  - regression **276 PASS / 0 SKIP / 0 FAIL**；`ui-snapshot` **103 steps / 102 PNGs**；
  - 三产品冒烟：desktop（xvfb，exit 124，tray ready）/ headless（exit 124）/ ACP（exit 0）全部 PASS；
  - **verify summary: 13 passed, 0 failed（529s）→ verify: PASS**。
- **已知限制**：见 CHANGELOG `[0.2.24]`「已知限制」与评审报告 §0.1（goal 会话级、后台通知、LSP 重绑、workspace mcp.json 信任、headless 审批文案、记忆跨进程锁等明确 Deferred）。

## 0.2.25（2026-11-11：会话头 Run Band 视觉补丁）

- **变更**：`session.buildAgentRunBand` 去掉外层 hairline 容器（描边/底色/内边距），只保留一行原生 `ui.Button` 入口 + `Space2` 间距；团队/子智能体零值仍常显，但不再显示 `0`（精确计数保留在 tooltip 与无障碍名）。`ui.PopoverBase` 浮层、入口顺序与点击行为不变。`docs/DESIGN.md` §6.3、`docs/AGENT-SURFACES.md` S9/壳层同步为“一行原生入口按钮、间距分组”。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression 276 PASS / 0 FAIL；`ui-snapshot` 103 steps / 102 PNGs；desktop/headless/ACP smoke PASS；**13/13 PASS**（530s）。人工复核 `p1-chat` 快照：标题后无双重描边、无 0 计数。

## 0.2.26（2026-11-11：OS 沙箱始终开启 + A 系列固定行为）

- **变更**：OS 沙箱始终开启、不可关闭（Linux bwrap/Landlock、macOS Seatbelt + 拒绝自检、Windows 受限令牌 ACL；不可用时 fail closed），权限模式联动 OS profile，沙箱可用时 shell 族工具自动放行；A 系列固定行为并从设置删除开关：流完整性校验、单实例运行、运行中排队发送、目标自动续跑（5 次上限）、通知级别过滤；模型高级选项的最大迭代/上下文预算改为“留空 = 模块默认”的可选输入。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression 276 PASS / 0 FAIL；`ui-snapshot` 101 steps / 100 PNGs；desktop/headless/ACP smoke PASS；**13/13 PASS**（507s）。人工复核截图：行为页展开无排队/目标开关、模型高级选项无流完整性/默认开关、外观页无单实例、通知页无级别过滤。

## 0.2.27（2026-11-11：Windows 沙箱命令执行 + rig v0.15.0）

- **变更**：升级 `zeokeel-rig v0.14.1 → v0.15.0`，修复 Windows ACL runner 私有临时目录回退到 `C:\WINDOWS` 导致所有命令退出码 127 的 P0；修复 `write_file` 父目录、`glob`/`grep` 的 Windows 路径分隔符；`terminal_*` Windows 默认 `powershell.exe`；`job_start` 支持 Windows PowerShell；`working_directory` 与文件工具共享 session cwd。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression 276 PASS / 0 FAIL；`ui-snapshot` 101 steps / 100 PNGs；desktop/headless/ACP smoke PASS；**13/13 PASS**（519s）。

## 0.2.28（2026-11-11：web_fetch 不再拦截任何地址）

- **变更**：ZeoKeel 的 `web_fetch` 不再安装 sandbox URL 策略，回环、私网、链路本地、benchmark/保留段均可抓取；仍只接受 http/https，并保留超时、重定向、响应体/输出上限。这是产品决策，等于关闭 SSRF 地址防护（rig 库默认策略不变）。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression 276 PASS / 0 FAIL；`ui-snapshot` 101 steps / 100 PNGs；desktop/headless/ACP smoke PASS；**13/13 PASS**（506s）。新增 `TestWebFetchAllowsPrivateAddresses`（loopback 抓取成功）。

## 0.2.29（2026-11-11：Windows 沙箱使用系统自带 temp + rig v0.15.1）

- **变更**：升级 `zeokeel-rig v0.15.0 → v0.15.1`；Windows ACL helper 改用系统自带 temp（`GetTempPath2W`/`GetTempPathW` → 用户 Known Folder `LocalAppData\Temp` → `os.TempDir()`，Win32 过滤 Windows 目录），修复 helper 环境被裁剪时候选列表为空、所有命令退出码 127；`CommandEnv` 放行 Windows 非敏感定位变量；`job_start` 按 shell 裸基名校验允许列表。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression 276 PASS / 0 FAIL；`ui-snapshot` 101 steps / 100 PNGs；desktop/headless/ACP smoke PASS；**13/13 PASS**（510s）。

## 0.2.30（2026-11-11：已结束团队/子智能体持久历史）

- **变更**：跑完的队友与同步子代理写入 `<数据目录>/child-agents.json`（有界 24/48 条 + 14 天 TTL、原子写、损坏隔离为 `.corrupt-<ts>`；退出时仍在运行的成员记为已中断）。重启后作为 `Historical` 只读行恢复：团队浮层新增「已结束」分区（历史标记/状态/输出/活动），子智能体切换器与主栏子会话可打开但无输入框，`SendChildAgent` 拒绝历史行；历史按父会话归属，页头计数与浮层只列当前会话历史，运行中队友保持进程级可见。`ui-snapshot` 设置桩改为带锁（修复自动化场景并发 map 读写导致的快照偶发崩溃）。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression **276 PASS / 0 SKIP / 0 FAIL**；`ui-snapshot` **103 steps / 102 PNGs**；desktop/headless/ACP smoke PASS；**13/13 PASS（502s）**。`ui-drive --only team` **7/7 PASS**（新增 `team-turn-persist`、`team-history`）；人工复核 `p2-team-history.png`、`p2-team-history-child.png`（已结束分区/历史标记/只读提示/无输入框）。

## 0.2.31（2026-11-11：DSH 对齐的子会话架构）

- **变更**：teammate 与同步子代理统一使用稳定 child-session ID（团队 member ID == 子智能体 ID == 历史行 ID，`member-N`/注册表 ID 只在后端解析）；`child-sessions.json` v2 替换 v0.2.30 的 `child-agents.json`（有界 64、14 天 TTL、原子写、损坏隔离、v1 自动迁移），spawn/完成/失败/中断/关闭/退出持续落盘；共享任务板持久化到 `team-tasks.json`（v0.2.32 起按会话；启动回放连续编号/依赖/owner/写入范围/墓碑；模型工具成功后写入；损坏隔离；无 dataDir 时纯内存）。修复 tracker 用过期成员快照释放 claim 导致并发 spawn 丢 claim 的竞态。
- **验证**（`./scripts/verify.sh` 全量）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression **276 PASS / 0 SKIP / 0 FAIL**；`ui-snapshot` **103 steps / 102 PNGs**；desktop/headless/ACP smoke PASS；**13/13 PASS（502s）**。`ui-drive --only team` **7/7 PASS**；人工复核 `p2-team-history.png`（历史行稳定 ID、已完成 badge、历史标记）。
- **已知后续**：完成回执注入 transcript、任务板按会话归属（两项均已在 0.2.32 交付）、writeScopes 仍仅提示（与 DSH 一致）。

## 0.2.32（2026-11-12：rig v0.16.0 + 会话级任务板 + 完成回执）

- **变更**：zeokeel-rig v0.15.1 → v0.16.0（Windows `terminal_*` 真实 ConPTY，不可用时回退 pipe；公共 API 无破坏，`GOOS=windows` 交叉构建通过）。共享任务板按会话归属：`team_task_*` 携带 run-scoped 会话上下文，每会话持久化 `<数据目录>/team-tasks/<session>.json`；启动预载（128 文件/30 天修剪、缓存 192）；v0.2.31 的全局板懒迁移（`.migrating` + target journal，失败阻塞变更类工具并重试，按内容多重集合幂等合并，回滚/旧版文件重新出现不丢不重；旧墓碑不会误隐藏新任务）。UI 任务板缓存优先，miss 只做一次只读加载（不迁移/不写/不隔离），无活动会话不回退全局板；删除会话按路径删文件+墓碑。子代理/队友完成回执：队友完成以 user 消息注入下一轮模型上下文（每队友一次、成功才提交、失败重试、teardown 先等 run 收尾），transcript 新增 `ChatRoleNotice` 只读行（live 按时间合入/流式上方，失败 danger 文本+告警图标且同屏最多 2 条 danger，重启后按 Finished 追加）；同步一次性子代理答案已在工具结果中不重复注入；rig manager 团队通知排空但不注入。委派优先 run-scoped 会话；关闭运行中队友持久化「已中断 + 结束时间」；`WithAgentSession(nil)` 防护；测试 teardown 顺序修复（模块先于临时目录释放）。
- **验证**（`./scripts/verify.sh` 全量，CGO 开启以支持 `-race`）：gofmt/build/vet/designlint/design-check/mygo vet PASS；`go test ./...` 与 `-race` PASS；regression **276 PASS / 0 SKIP / 0 FAIL**；`ui-snapshot` **104 steps / 103 PNGs**；desktop/headless/ACP smoke PASS；**13/13 PASS（502s）**。`ui-drive --only team` **7/7 PASS**；人工复核 `.cache/r2-snapshot/p2-team-tasks.png`（按会话任务卡）、`.cache/r2-snapshot/p2-team-history.png`（完成回执行）。
- **独立审计**：4 轮 fresh-context 独立 agent 审计（backend/surface 分组，逐轮「审计→修复→复审计」；覆盖身份/持久化、任务板缓存/迁移/删除、回执一次性、并发/teardown、UI 语义与文档），最终确认 **NO P0/P1/P2 FINDINGS**。保留的 P3 限制：目标会话任务板已满（256）时迁移保持 pending，需删除该会话释放容量；`stores` 注册表与损坏备份的清理为有界近似；`writeScopes` 仍仅提示（与 DSH 一致）。

## 0.2.33（2026-11-12：rig v0.16.1 修复 Windows 沙箱探测崩溃）

- **变更**：`zeokeel-rig v0.16.0 → v0.16.1`。修复 Windows ACL 探测 `0xC0000005`：`winWindowsDirectory` 把 `GetWindowsDirectoryW(lpBuffer, uSize)` 参数传反，容量 260（0x104）被当成输出缓冲区指针并写入，probe helper 以 exit status 2 崩溃，沙箱 runner fail closed —— Windows 上 `pwsh`/`job_start`/`terminal_*` 全部不可用（v0.2.29 起）。同一 rig 版本还修复：Windows `terminal_*` 的遗留早退（v0.16.0 的 ConPTY 实现不可达，删除后走 ConPTY/pipe 回退）、Windows 10 上缺失 `GetTempPath2W` 导致的探测 panic（先 `Find` 再 `Call`，回退 `GetTempPathW`）。ZeoKeel 侧无代码改动，公共 API 无破坏性变更。
- **验证**：本仓库 `gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`（CGO_ENABLED=0）PASS；`GOOS=windows/amd64`、`GOOS=windows/arm64` 交叉构建 PASS。rig v0.16.1 tag 工作流全量 CI（含 clean-room `go get` + `go mod verify`）PASS，新增 Windows 运行时回归测试 `TestWindowsRuntimeKnownFolders`。本次无 UI/行为改动，未跑 `scripts/verify.sh` 全量。

## 0.2.34 草案（2026-11-14：mygo v0.9.0 + rig v0.16.2 + 官方化替换）

- **依赖升级**：`mygo v0.8.3 → v0.9.0`（唯一 breaking 是 `sqlite.LibraryPath`/`MYGO_SQLITE_LIBRARY` 移除，产品未使用；新增 6 个传递依赖：`ProtonMail/go-crypto`、`cloudflare/circl`、`klauspost/compress`、`ulikunitz/xz`、`digitalxero.dev/rpm`、`x/crypto`，均为 mygo 传递依赖，`CGO_ENABLED=0` 构建通过）；`zeokeel-rig v0.16.1 → v0.16.2`（新增 `internal/procattr.HideWindow`：所有子进程 spawn 隐藏 Windows 控制台窗口，修复 Windows 每次工具调用弹黑窗；ConPTY `STARTUPINFOEX` 同步隐藏；无公共 API / 模型可见变更；补 rig 源级覆盖测试，任何未来 spawn 忘记准备都会失败）。
- **官方化替换**（能不自己写就不自己写；逐项先写测试再实现）：
  - **spill**：自写 `internal/modules/agent/spill.go`（258 行：32 KiB 阈值、2 KiB 头、中文 marker、100 文件清理、History 包装）→ `rig.SpillPolicy`（产品只保留落盘目录求值，`AgentSpec.Spill`；文件 258→64 行）。行为变化：模型看到 rig 摘要（`[tool result spilled to file]` + 绝对路径 + 字节数 + 240 B preview），同轮后续请求不再携带全文；旧会话里的中文指针不再被 rig 识别（文件仍可人工打开）。
  - **hosttools**：自写 `ask_user`/`plan_review`/`exit_plan_mode`/`goal` → `rig/tools/hosttools`（工具名与排序不变；`present` 是产品内容 artifact，保留；`plan_review` 参数 `plan`→`content`；`goal` 增加 rig 生命周期 `update`，经 `contracts.GoalPauser`/`GoalLifecycler` 适配；headless 仍不注册 plan 对；删除旧 `plan` 参数兼容 shim 与仓库内种子改用 `content`）。
  - **手动压缩**：补 `Fallback: compact.WindowCompactor{}`，预算扣除 system prompt（与自动路径一致）。
  - **openpath**：两处手写 `xdg-open`/`open`/`explorer` → `mygo.Shell.OpenPath`（保留测试 seam）。
  - **退出**：`OnBeforeQuit` 处理 `QuitEvent.SessionEnding`（OS 注销/关机），同步收尾并 flush 日志。
  - **mygo v0.9 能力接入**：命令面板 `ListState.Choose`；壳层 a11y landmark + 右栏 tabpanel（`Controls(panel)`）；`editor.LanguageForText`；runstatus `c.Key`；菜单键/Shift+F10 打开选中行菜单测试；Focus 自动 reveal 回归测试（composer 重聚焦、检视器关闭回归焦点两处证明滚动不变，不需要 `PreventScroll`）。
- **测试（TDD）**：每项先补/改失败测试再实现。新增：rig `internal/procattr` 覆盖率 + 平台测试；`TestPaletteFilterChangeChoosesFirstVisibleRow`、`TestPaletteReopenForgetsPreviousChosenCommand`、`TestShellLandmarks`、`TestTabPanelIsNamedAndLinkedToItsTab`、`TestEditorForReadsLanguageFromShebang`、`TestComposerRefocusKeepsScroll`、`TestInspectorCloseFocusReturnKeepsScroll`、`TestWorkspaceFileRowMenuKeyOpensRowMenu`、`TestSessionListMenuKeyOpensChosenRowMenu`、`TestRunStatusStatusRowsHaveDistinctKeys`、`TestGoalStoreUpdateMapsLifecycle`；重写 spill/compact/regression spill 测试。
- **验证**：`gofmt`、`go build ./...`（`CGO_ENABLED=0` 与 `GOOS=windows/amd64`）、`go vet ./...`、`go tool mygo vet .`、`go test ./... -count=1`、`designlint --strict --baseline` 0 new、`scripts/design-check.sh` 6/6、`go mod verify`、`go tool mygo migrate-ui`（干净副本，无产品文件需要迁移）PASS。
- **残余**：Windows 控制台修复需真机复核（本环境 Linux；rig 侧用 Windows-only 测试 + `GOOS=windows` 交叉 vet 验证）；`goal update=blocked` 在 rig schema 中仍暴露但产品无 blocked 生命周期，返回可读错误；旧 spill 指针兼容性如上。
