# 执行域持久化（job / terminal）设计稿

> 状态：2026-11-15 已实现（toolkit execution_routing.go / contracts/execution.go；跨 app 重启不重连为明确边界）。目标：后台 job 与模型 terminal 会话不再因工作区切换、权限/沙盒模式重建被
> `CloseAll` 杀死；跨 build 按 ID 路由；生命周期写入 durable 事件审计。明确不承诺 OS 进程跨应用重启重连。

## 1. 现状与问题

- `internal/modules/toolkit/rebind.go` 用 `rootScoped` 表示一个 `(root, mode, sandboxOn)` 构建；构建拥有
  `jobs.Manager` 与 `terminal.Manager`。
- `rebindGroup.forRoot` / `forRootValue` 在 root、权限 preset、OS runner 变化时创建新 build，旧 build
  `retire()`，无 inflight 即 `runClose()` → 两个 manager 的 `CloseAll()`。
- 后果：切工作区或切权限/沙盒时，后台 job 被 kill、terminal PTY 被关闭；旧 build 的 job 也无法再由
  `job_output/job_kill` 访问（代理只按当前 root 分发）。
- `drainNotices` 只 drain 当前 build，旧 build 完成通知会丢。

## 2. 设计

### 2.1 稳定持有

- `rebindGroup` 保留所有 build 直至模块 unload；`retire()` 不再销毁 manager（只标记非当前）。
- 同一 `(root, mode, sandboxOn)` 仍复用（`overrides`/current 逻辑不变）。
- 模块 unload 时统一对所有 build 执行 `CloseAll()`（job/terminal），幂等。

### 2.2 按 ID 跨 build 路由

- group 维护 `jobOwners map[string]*rootScoped`、`terminalOwners map[string]*rootScoped`。
- `boundTool.Invoke` 对执行工具走 `invokeExecutionTool`：
  - `job_start` / `terminal_open`：分发到 ctx root/当前 build（使用最新策略），成功后从结果
    （`job_id: …` / `session_id: …`）记录 owner。
  - `job_output` / `job_wait` / `job_kill`：从 arguments 的 `job_id` 找 owner；terminal 的
    `terminal_send/read/signal/close` 从 `session_id`/`sessionId` 找 owner；找不到回退当前 build
    （保留 rig 的 unknown 错误）。
  - `job_list` / `terminal_list`：合并 root 与调用 root 相同的所有保留 build（含空 root），
    保持 rig 文本格式并修正计数；模型只看到自己 root 的作业。
- 非执行工具仍按 root 分发（文件/shell 工具继续遵循当前策略，不跨 build 泄漏）。
- host-facing `jobService` 的 `job_list`/`job_kill` 也走同一路由；`drainNotices` 遍历所有保留 build。

### 2.3 durable 生命周期事件

- toolkit 模块可选消费 `contracts.KeyStore`；无 store 时 no-op（测试/embedder）。
- 工具包装在 `contracts.AgentSessionFromContext(ctx)` 有会话时追加事件：
  - `execution/job_started`：job id、命令、cwd、root、policy mode；
  - `execution/job_finished`（notice drain）：job id、state/exit code；
  - `execution/terminal_opened`：session id、cwd、transport；
  - `execution/terminal_closed`：session id。
- 不持久化完整输出（可择机加 output tail + truncated 标记）；应用退出/崩溃未写 finished 的记录，
  读侧视为 interrupted（不伪造结束）。

## 3. 明确边界

- OS 进程的生命周期随应用退出结束；重启后无法重新持有 PTY/job 管道。DSH 的 jobs 同样不跨进程重启。
- 本轮不做「重启后 UI 展示历史 job/terminal」；durable 事件先供审计与后续 UI/read model。

## 4. 验收

- 切工作区后 job 仍 running，且新 build 的 `job_output/job_wait/job_kill` 按 ID 可用；terminal 同理。
- 切权限 preset/沙盒 runner 重建后旧 job/terminal 可控；新任务使用新策略。
- 多 root 并发不串；`job_list` 只列当前 root；旧 build 完成 notice 不丢。
- 模块 unload 关闭全部；fake store 断言 durable 事件。
- `go test ./internal/modules/toolkit -count=1`、相关 `-race`、build/vet/gofmt 通过。
