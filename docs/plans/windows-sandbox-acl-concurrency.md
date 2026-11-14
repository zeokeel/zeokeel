# Windows 沙盒并发写修复（rig v0.16.3）

> 状态：2026-11-15。修复在 `/data/zeokeel-rig` tag `v0.16.3`（commit `0515026`，基于 v0.16.2）；
> 产品以 `third_party/zeokeel-rig` 内树 fork + `replace` 消费，保持干净 checkout 可构建。
> Windows 实机运行时验证待做（Linux 只能交叉编译与纯状态机测试）。

## 1. 根因

Windows ACL runner 每个 sandbox helper 都：

1. `GetNamedSecurityInfoW` 保存 workspace 目录的**整份 DACL/SACL**；
2. 追加自己的 capability SID 写+删 ACE（和 Everyone `FILE_DELETE_CHILD` deny、Low 标签）；
3. 子进程退出时用保存的快照**整体恢复**。

并发两个命令 A/B 时：B 的快照包含 A 的授权；A 先退出恢复自己的旧快照，把 B 的 ACE 一并抹掉；
仍在运行的 B（或之后启动的命令）写 workspace 就失败。反向顺序则留下 A 的 ACE 残留。
这不是安全策略拒绝，而是跨进程 ACL 快照恢复互相覆盖。

## 2. 修复（v0.16.3）

- **跨进程命名 mutex** `Local\ZeoKeelSandboxACL-<sha256(workspace)>` 串行化 ACL 状态读改写。
- **per-grant marker mutex** `Local\ZeoKeelSandboxGrant-<pathHash>-<sidHash>`：helper 运行期间持有；
  其他 helper 用 `OpenMutexW` 判断该 grant 是否仍存活（进程崩溃 → 句柄消失 → 可清理）。
- **状态文件** `%LOCALAPPDATA%\ZeoKeel\sandbox-acl\<pathHash>.json`（不可用退回 `%TEMP%`）：
  - 原始 self-relative security descriptor（base64）；
  - deny/label 是否由本状态添加；
  - active grants（capability SID）；
  - 原子写（temp + rename）。
- **Grant**（mutex 内）：purge 已死 grants → 若 purge 后无 active 且有残留状态，先恢复原始 descriptor
  并删状态 → 首个 grant 保存原始 descriptor、加 deny/Low label（仅一次；label 失败降级）→
  给自己 capability SID 加 ACE → 记录 own grant → 保存。
- **Cleanup**（mutex 内）：只 `REVOKE_ACCESS` 删除**自己的** capability ACE → 移除 own grant →
  purge dead → 无 active 时恢复原始 descriptor（DACL+LABEL；仅 DACL 回退）并删状态，否则保留状态；
  最后关闭 marker mutex。幂等；restore 失败保留状态供重试，绝不放宽授权。
- **安全语义不变**：restricted token / restricting SIDs / Low integrity / private temp / fail-closed
  probe / helper argv 与结果格式不变；read-only 仍不修改 workspace ACL。
- 状态文件损坏 → fail closed（拒绝运行，不改写安全描述符）。

## 3. 消费方式

- `/data/zeokeel-rig`：分支 `fix/windows-acl-concurrency`，tag `v0.16.3`（已推送 GitHub；`goproxy.cn` 尚未索引该版本）。
- 产品：`go.mod` 将 rig 提升到 `v0.16.3`，并 `replace github.com/zeokeel/zeokeel-rig => ./third_party/zeokeel-rig`
  （`v0.16.3` 的完整源码快照）。这样干净 checkout 无需 sibling rig 或已发布代理。
- goproxy.cn 索引 v0.16.3 后：删除 `third_party/zeokeel-rig`、去掉 `replace`、保留 `require v0.16.3+`。

## 4. 验证

- Linux：`go test ./sandbox/osrunner`（纯状态机 9 项：并发 grant、部分退出、最后 restore、死 grant purge、
  崩溃残留、损坏 fail-closed、label 降级、命名/哈希、4 并发串行化）通过；rig 全量测试通过。
- Windows 交叉：`GOOS=windows GOARCH=amd64 go build ./...`、`go vet ./...`、`go test -c ./sandbox/osrunner` 通过。
- 产品：`go build ./...`、`go test ./...` 通过；`GOOS=windows GOARCH=amd64 go build ./...` 通过；
  designlint 0 new；干净目录（无 sibling）构建通过。
- **待 Windows 实机验证**：命名 mutex 的真实跨进程行为、`SetFileSecurityW` 恢复 self-relative descriptor
  （含 LABEL）、`OpenMutexW` 死 grant 判定、真实并发 helper ACL 表现。
  运行：`cd /data/zeokeel-rig && RIG_WINDOWS_SANDBOX_TEST=1 go test ./sandbox/osrunner -run TestWindowsRuntimeConcurrentWorkspaceWrites -count=1`。

## 5. 已知边界

- `Local\` 为同一 Windows 会话命名空间；不同用户/会话同时操作同一 workspace 时协调仅靠各自状态文件，
  跨用户并发仍是罕见边界。
- helper 被硬杀后状态文件保留：下一个 helper 会 purge 死 grant；若所有 helper 崩溃且状态残留，
  下次 grant 会先恢复原始 descriptor 再开始。
- restore 失败保留状态文件供后续重试，helper 仍以失败码退出（沿用旧行为），不放宽授权。
