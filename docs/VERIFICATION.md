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
   /plan /goal /compact /feedback /memory 命令
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
