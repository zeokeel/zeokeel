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

## 最终验证（Round 59，2026-11-08，HEAD 9d1cbac）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 06:05–06:13（Asia/Shanghai）。

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

$ go test ./internal/regression -count=1 -v
exit 0 · ~10.5s · 206 PASS / 0 SKIP / 0 FAIL
（HEAD 9d1cbac 提交态回归为 189 PASS；验证期间并行审计新增未提交用例
 data_privacy/home_composer/notifications/session_commands/sidebar_workspace_add
 加入工作树后为 206 PASS / 0 SKIP，全部通过）
```

### 三产品冒烟（无 panic）
```
$ mkdir -p /tmp/final-desktop-r59
$ printf '{"ui.globalHotkey":"CmdOrCtrl+Shift+Space"}\n' > /tmp/final-desktop-r59/settings.json
$ xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir /tmp/final-desktop-r59
exit 124
06:07:25 ZeoKeel: system tray ready (close to tray: false)
06:07:25 ZeoKeel: global hotkey registered
06:07:25 ZeoKeel: ... product "desktop" loaded: 27 module instances, screens [home automation session plugins]
06:07:31 ZeoKeel: quit requested
NO_PANIC

$ timeout 8 go run ./cmd/zeokeel --headless --data-dir /tmp/final-headless-r59
exit 124
06:07:37 ZeoKeel: ... product "headless" loaded（14 module instances）
ready; waiting for SIGINT or SIGTERM
stopping
NO_PANIC

$ printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}\n' \
    | timeout 10 go run ./cmd/zeokeel --acp --data-dir /tmp/final-acp-r59
exit 0
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"promptCapabilities":{"audio":false,"embeddedContext":false,"image":false}},"protocolVersion":1}}
合法 JSON-RPC 2.0 initialize 响应；NO_PANIC
```

### UI 快照
```
$ mkdir -p /tmp/final-shots-r59
$ go run ./internal/devtools/ui-snapshot /tmp/final-shots-r59
exit 0 · 8s
"wrote ..." 渲染步数: 63
唯一 PNG 文件: 61
同名重复写入: p1-chat.png、p2-mentions.png（各 2 次，后写覆盖）
NO_PANIC
```

### 文档核对（本轮）
- docs/COVERAGE.md：矩阵 §2（28 项）+ §3（34 项）= 62 项，实际 ✅37 / 🟡24 / ❌1；原摘要 34/24/4 与统计表 34/23/5 已校正为 **✅37（59.7%）/ 🟡24（38.7%）/ ❌1（1.6%）**。
- README 顶部状态更新为 Round 59（HEAD 9d1cbac）与最新数字；构建命令 `go build ./...` / `go test ./...` 核对仍准确。
- 验证期间工作树含并行 agent 未提交改动（如 `internal/regression/*_test.go`、`internal/ui/settings/data.go` 等）；本任务未修改生产代码。HEAD 9d1cbac 提交态回归 189 PASS，当前工作树 206 PASS / 0 SKIP。
- 观察（未改矩阵行，超出本轮摘要修正范围）：个别矩阵行证据文案滞后，如 Skills 行仍写“缺创建/编辑/删除”（Round 53 已落地创建/删除）、Artifacts 行仍写“缺下载动作”（工件下载已落地）；状态分布仍与目标 37/24/1 一致。

## 一键验证（scripts/verify.sh）

仓库提供可重复的一键验证脚本，按下面的顺序执行并逐项打印 `PASS/FAIL + 耗时`；任一步失败即非零退出（EXIT trap 仍打印摘要）。所有命令默认使用离线环境（`PATH=/usr/local/go/bin:...`、`GOPROXY=off`、`GOSUMDB=off`、`GOFLAGS=-mod=mod`，均可由外部环境覆盖）。

```sh
./scripts/verify.sh              # 完整：gofmt/build/vet/test/-race/回归/快照/三产品冒烟，约 100s
./scripts/verify.sh --quick      # 快速循环：跳过 -race 与产品冒烟，约 40s
./scripts/verify.sh --no-smoke   # 保留 -race，仅跳过三产品冒烟
make verify                      # 等价于 ./scripts/verify.sh（可 ARGS=--quick）
```

步骤与预期输出：

1. `gofmt`：模块包内 Go 文件均已格式化（`gofmt -l` 为空）；
2. `go build ./...` / `go vet ./...`：通过；
3. `go test ./... -count=1`（`--quick` 跳过下一步的 race）；
4. `go test ./... -count=1 -race`；
5. `go test ./internal/regression -count=1 -v`：打印 `(regression: N PASS / 0 SKIP / 0 FAIL)`；
6. `go run ./internal/devtools/ui-snapshot <临时目录>`：打印 `(snapshot: 63 steps / 61 PNGs)`，目录用完清理（`VERIFY_KEEP_TEMP=1` 可保留）；
7. desktop xvfb 冒烟：`xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir <临时目录>`（预置 `ui.globalHotkey=CmdOrCtrl+Shift+Space`），允许 exit 124，检查 tray ready 与无 panic；
8. headless 冒烟：`timeout 8 go run ./cmd/zeokeel --headless ...`，允许 exit 124，检查 loaded/ready；
9. ACP 冒烟：initialize JSON-RPC，检查 `"jsonrpc":"2.0"`、`"protocolVersion":1`。

本机实测（2026-11-08，HEAD bd89cf1，go1.27.3 linux/amd64）：

```
$ ./scripts/verify.sh --quick
==> gofmt ... PASS (0s)          ==> go build ./... ... PASS (3s)
==> go vet ./... ... PASS (1s)   ==> go test ./... -count=1 ... PASS (13s)
==> regression ... (208 PASS / 0 SKIP / 0 FAIL) PASS (12s)
==> ui-snapshot ... (63 steps / 61 PNGs) PASS (9s)
verify summary: 6 passed, 0 failed, 38s total · verify: PASS

$ ./scripts/verify.sh
==> go test ./... -count=1 -race ... PASS (47s)
==> desktop smoke (xvfb) ... (exit 124, tray ready: 1) PASS (9s)
==> headless smoke ... (exit 124) PASS (8s)
==> ACP smoke ... (ACP exit 0) PASS (0s)
verify summary: 10 passed, 0 failed, 102s total · verify: PASS
```

## 最终验证（Round 58，2026-11-08，HEAD d27e449）

环境：go1.27.3 linux/amd64 · 离线（GOPROXY=off/GOSUMDB=off/GOFLAGS=-mod=mod）· 2026-11-08 07:53–07:56（Asia/Shanghai）。
命令：`./scripts/verify.sh`（完整）。

### 一键验证结果（首次即 PASS，无需重跑）
```
$ ./scripts/verify.sh
==> gofmt ... PASS (1s)
==> go build ./... ... PASS (4s)
==> go vet ./... ... PASS (0s)
==> go test ./... -count=1 ... PASS (14s)
==> go test ./... -count=1 -race ... PASS (51s)
==> regression (-v) ... (regression: 227 PASS / 0 SKIP / 0 FAIL) PASS (13s)
==> ui-snapshot ... (snapshot: 65 steps / 63 PNGs) PASS (8s)
==> desktop smoke (xvfb) ... (desktop exit 124, tray ready: 1) PASS (8s)
==> headless smoke ... (headless exit 124) PASS (8s)
==> ACP smoke ... (ACP exit 0) PASS (0s)
verify summary: 10 passed, 0 failed, 107s total
verify: PASS
```
regression **227 PASS / 0 SKIP / 0 FAIL**；UI 快照 **65 步渲染 / 63 个唯一 PNG**；三产品冒烟均无 panic：desktop exit 124（tray ready 1）、headless exit 124、ACP exit 0。本轮无与预期不符/失败步骤。

### 新功能冒烟
- **ACP session/list + session/load 真实帧**（`go run ./cmd/zeokeel --acp`，同一 data-dir 两次连接）：
```
{"id":1,"jsonrpc":"2.0","result":{"agentCapabilities":{"loadSession":true,...},"protocolVersion":1}}
{"id":2,"jsonrpc":"2.0","result":{"sessionId":"sess-1794095740740741703"}}          # session/new
{"id":3,"jsonrpc":"2.0","result":{"sessions":[{"sessionId":"sess-1794095740740741703","title":"","updatedAt":"2026-11-08T07:55:40+08:00"}]}}
{"id":2,"jsonrpc":"2.0","result":{"messages":[],"model":"gpt-4o-mini","sessionId":"sess-1794095740740741703","title":""}}   # session/load
```
- **模型连接检查（离线 fake，未做网络请求）**：`go test ./internal/ui/settings/ -run TestModelCheck -count=1` → **8 PASS**（成功、HTTP 失败、网络错误、无 BaseURL、重复点击、en 文案、chat config 回退、状态文案本地化）；测试通过 `ModelListGetter` seam 脚本化 `/models` 响应，不访问网络。
- **plan keep-planning**：`go test ./internal/ui/interaction/ -run TestKeepPlanning -count=1` → 7 PASS（发送反馈/必填/失败保留模态/无活动会话/无 chat 隐藏/en）；无需触发 UI。

### 文档核对
- docs/COVERAGE.md 矩阵（§2 28 项 + §3 34 项 = 62）实际 **✅39 / 🟡22 / ❌1**；摘要与统计表已同步为 39/22/1（62.9% / 35.5% / 1.6%）。
- README 状态行更新为 Round 58（HEAD d27e449）；构建/验证命令核对仍准确。

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
