# ADR 0012：构建产物统一 `dist/` + 稳定文件名

- 日期：2026-11-05
- 状态：**已采纳**
- 决策者：用户（2026-11-05：「清理一下编译，搞的到处都是，固定编译到一个位置，并且编译名称要稳定」）；实施：Lead
- 关联：`scripts/build.sh`、`Makefile`、`packaging/windows/README-WINDOWS.txt`、`README.md`、`docs/PLAN.md` §3.4

## 1. 背景与问题

历史上构建产物散落且命名不稳定：

- `scripts/check-nocgo.sh` 早期版本交叉编译不指定 `-o`，把 `zeokeel`/`zeokeel.exe` 写回仓库根（`.evolve/research/01-audit.md` 记录过该污染）；
- 手动 `go build ./cmd/zeokeel` 也会在仓库根生成 `zeokeel(.exe)`；
- `dist/` 里 Windows 产物有 `zeokeel-windows-amd64.exe`、`-console.exe`、zip 等，但缺少统一的 Linux/macOS 命名与重建入口；
- `make build` 只是 `go build ./...`（compile check），`make clean` 还会 `rm -rf .cache`（清掉 Go 模块/构建缓存，破坏本地开发状态）。

用户要求：**固定一个位置 + 名称稳定**；产物不要散落在仓库各处。

## 2. 决策

### 2.1 唯一输出目录：`dist/`

- 所有二进制只写 `dist/`（已 gitignore）。
- 命名固定为 `dist/zeokeel-<goos>-<goarch>[.exe]`；Windows 额外 `dist/zeokeel-windows-amd64-console.exe`（带控制台窗口的排障版）。
- Windows 发布包固定为 `dist/zeokeel-windows-amd64.zip`（内含上面两个 exe、`README-WINDOWS.txt`、`SHA256SUMS.txt`），外层 `dist/SHA256SUMS.txt` 覆盖三个产物。
- 仓库根 `/zeokeel`、`/zeokeel.exe`、包内 `*.test` 全部列为构建污染，由 `make clean` 清除；不再提交/保留。

### 2.2 唯一入口：`scripts/build.sh`（Makefile 同名目标）

| 命令 | 产物 |
| --- | --- |
| `make build` / `build.sh native` | `dist/zeokeel-<host goos>-<host goarch>` |
| `build.sh linux-amd64` / `linux-arm64` / `darwin-arm64` | 对应 `dist/zeokeel-…` |
| `make windows` / `build.sh windows` | Windows GUI + console |
| `make build-all` | 四目标 |
| `make package-windows` | Windows 双产物 + zip + SHA256SUMS |
| `make build-check` | `go build ./...`（只编译检查，不产出文件） |
| `make clean` | 只清 `dist/`、根目录遗留 `zeokeel`/`zeokeel.exe`、`*.test`；保留 `.cache/`、`.probe/`、`.evolve/`（本地状态不是构建产物） |

`scripts/build.sh check`/`help` 也可用。脚本设置 `CGO_ENABLED=0`、`-trimpath`；Windows GUI 用 `-ldflags "-H windowsgui"`。

### 2.3 发布 README 纳入仓库

Windows 包内的 `README-WINDOWS.txt` 从 `dist/`（gitignored、会被 clean 删除）移到仓库跟踪的 `packaging/windows/README-WINDOWS.txt`，由 `package-windows` 复制进包，保证打包可复现。

## 3. 后果与验证

- 正面：任何一次构建的输出路径/名称可预测；CI/用户脚本可以固定引用 `dist/zeokeel-<goos>-<goarch>`；仓库根不再出现二进制；`make clean` 不再误删缓存。
- 兼容性：旧脚本如引用根目录 `zeokeel` 需改为 `dist/…`（README/PLAN 已更新）；`dist/` 仍 gitignored，发布资产另行分发。
- 验证：`make clean && make build && make package-windows` 产物齐全、zip `unzip -t` 通过；`git status` 无二进制；`check-nocgo.sh` 仍用临时目录不受影响。
