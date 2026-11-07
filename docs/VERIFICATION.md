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
