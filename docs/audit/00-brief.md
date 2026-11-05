# ZeoKeel 全量审计 · 团队共享简报（v1）

> 日期：2026-11-05 · 审计基线：`HEAD = 7eebe47`（`feat: per-turn turn_end markers, tool-result spill, budget settings`）
> 审计对象：`/data/mygo`（ZeoKeel：纯 Go 桌面 AI Agent，mygo 原生 UI + eino 运行时，功能对齐 DSH）
> 产品目标：**做一个全功能的 AI Agent 桌面端工具**，参考 DSH（已安装 0.2.1-rc.2），但视觉/交互自有。
> 本文件由 Lead 维护，**审计成员只读**。

---

## 1. 目标与非目标

**目标**：用独立、可复核的证据回答五个问题，并给出可执行的优先级建议：

1. **功能缺失**：DSH 有、ZeoKeel 没有（或只有半成品/未接 UI）的 agent 能力；
2. **设计不合理**：架构、数据模型、状态管理、安全/权限、性能、可扩展性上的结构性问题；
3. **UI bug**：视觉/布局/渲染/主题/缩放/裁剪/状态残留等可观察缺陷；
4. **UX bug**：交互流程、反馈、空态/错误态、键盘/焦点、信息架构、桌面惯例、i18n 等体验缺陷；
5. **项目不合理**：工程/文档/测试/构建/发布/流程层面的结构性问题。

**非目标（本轮不做）**：

- ❌ 不修改任何被跟踪的代码/文档/预览图；不提交 commit；不执行 `git checkout/clean/reset/stash`。
- ❌ 不做修复实现。审计的终点是**证据充分的报告 + 修复建议**，不是 PR。
- ❌ 不重复造轮子：先读已有审计/已知限制，已知问题只做「状态复核」，重点找**新问题**（NEW）。
- ❌ 不把「与 DSH 像素对齐」当目标（已退役，ADR 0001）。

---

## 2. 交付物与写入边界（重要）

只允许写自己的报告与本目录下的证据目录；**禁止**改他人文件、禁止 `git add/commit`。

| 成员 | 报告（唯一写入的文件） | 临时证据目录 |
| --- | --- | --- |
| parity-auditor | `docs/audit/01-functional-parity.md` | `.cache/audit/parity/` |
| runtime-auditor | `docs/audit/02-runtime-correctness.md` | `.cache/audit/runtime/` |
| uiux-auditor | `docs/audit/03-ui-ux.md` | `.cache/audit/uiux/` |
| arch-auditor | `docs/audit/04-architecture-quality.md` | `.cache/audit/arch/` |
| product-auditor | `docs/audit/05-product-gaps.md` | `.cache/audit/product/` |
| Lead | `docs/audit/00-brief.md`、`docs/audit/MASTER-AUDIT.md`、`docs/audit/06-verification.md` | `.cache/audit/lead/` |
| verifier（第二轮） | `docs/audit/06-verification.md` | `.cache/audit/verify/` |

- `.cache/` 在 `.gitignore` 中，证据（截图、日志、探针代码、临时副本）放这里。
- 报告是给用户读的**正式产物**，放 `docs/audit/`；图片引用用相对路径 `../../.cache/audit/<member>/shots/xxx.png`。
- **工作区可能有并发写入**：审计期间可能有其他进程/会话改动未跟踪文件（如 Windows 资源文件）。以 `git rev-parse HEAD` 与 `git status --short` 记录方法学，不要清理、不要回滚任何东西；你的结论以代码内容为准并给出行号。

---

## 3. 严重度定义（全团队统一）

| 级别 | 定义 | 例子 |
| --- | --- | --- |
| **P0** | 数据丢失/损坏、安全漏洞、核心流程不可用、崩溃/卡死、隐私泄露 | API key 明文泄漏、路径沙箱可逃逸、会话数据丢写、审批被绕过 |
| **P1** | 重要能力缺失或半成品、高频 UX 缺陷、明显视觉错误、可复现的交互失效 | 功能存在但 UI 未接线、错误态无反馈、中文截断、焦点丢失 |
| **P2** | 质量/一致性/可维护性问题，影响体验或长期成本 | 命名混乱、重复代码、文案不一致、边界未处理 |
| **P3** | 打磨项、想法、低优先级改进 | 动效细节、可选增强 |

**标记**：每条发现标注 `NEW`（此前审计未记录）或 `KNOWN`（已知限制/旧审计记录，注明来源并复核现状）。

---

## 4. 证据标准（会被独立验证）

1. **代码证据**：`path/file.go:LINE`（必要时给 2-5 行关键代码）。行号必须对应当前 HEAD；大文件请引用符号名而非仅行号。
2. **运行证据**：命令 + 输出摘要；测试证据给 `go test -run ...` 的结果；UI 证据给截图路径 + 观察到的事实。
3. **禁止臆测**：区分「已确认」「高度可疑」「待验证」三种状态；没有证据的猜测最多放「可疑项/未验证」章节。
4. 每条发现必须有：**现象 → 证据 → 影响 → 复现/验证方式 → 建议修复（含大致工作量 S/M/L）**。
5. 假阳性会被 verifier 打回；宁可少而准，不要多而虚。

---

## 5. 环境速查（已验证可用）

### Go 工具链

```sh
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/data/mygo/.cache/go-build GOMODCACHE=/data/mygo/.cache/gomod
export GOSUMDB=off CGO_ENABLED=0
go build ./... && go vet ./...
go test -count=1 ./internal/<pkg>   # 定向测试；不要全仓跑（Lead 已跑过基线，见 §7）
```

### 运行中的 GUI（无显示器环境已打通）

```sh
BIN=/data/mygo/dist/zeokeel-linux-amd64      # 已构建；需要新构建用 make build（落 dist/）
export ZEOKEEL_DATA_DIR=/tmp/zk-audit-<member>   # 隔离数据目录，别污染 ~/.zeokeel
xvfb-run -a -s '-screen 0 1280x800x24' bash -c '
  '"$BIN"' >/tmp/zk-audit.log 2>&1 & APP=$!
  sleep 4
  import -window root /tmp/shot.png          # 截图（ImageMagick）
  xdotool search --name ZeoKeel               # 窗口 id
  # xdotool mousemove X Y click 1 / type / key ctrl+comma ...
  kill $APP'
```

- 可观察到应用真的渲染（CPU llvmpipe）。应用日志在 stderr。
- 窗口无系统装饰（Xvfb 无 WM），截图即应用内容；`xdotool windowsize <id> 900 600` 可测窄窗响应式。
- **中文输入**经 IME 不便模拟，键盘输入用 ASCII 文案；需要中文文案的显示效果时改数据/设置或直接看预览图。
- **真实模型（可选，成本受限）**：key 在 `/root/.dsh/.credentials.yaml`（`DEEPSEEK_API_KEY`，**只读取、绝不打印/写入报告**）。app 支持 env：`DEEPSEEK_API_KEY=... ZEOKEEL_PROVIDER_BASE_URL=... ZEOKEEL_PROVIDER_MODEL=...`。每个成员最多 3-4 个短回合（prompt 用英文、短句），避免无谓开销。模型端点在网络可用时才能用；失败就退回静态审计并注明。
- UI 预览金标（22 张）在 `docs/design/preview/`，直接 `read_image` 审查；不要用 `ZEOKEEL_UPDATE_PREVIEWS=1` 覆写金标。

### DSH 参考（本地只读，最权威的功能清单）

```
/usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/@deepseek-ai/
  <package>/README.zh.md      # 305 个包，每个都有中文文档
```

- 快捷方式：`ls` 包名即可看到能力地图（dsh-agent-loop / dsh-tool-* / dsh-plan-mode / dsh-compaction-* / dsh-goal* / dsh-session-* / dsh-skill* / dsh-mcp* / dsh-client-ui-* / dsh-desktop 等）。
- 需要确认某能力行为时读对应 README.zh.md（例如 `dsh-tool-fs` 的 edit 语义、`dsh-permission-presets` 的权限模型）。
- 不要启动/重启 DSH 服务（本机已有 DSH web 在运行，端口 3080/8099 属于宿主环境）。

### 项目内既有审计（先读，避免重复）

- `docs/design/known-limitations.md`（已知限制，最全）
- `docs/FUNCTIONAL-PARITY.md`（上一轮功能对齐矩阵）
- `docs/design/review-round3.md`、`review-round4.md`（UI/UX 前两轮评审）
- `docs/research/05-final-audit.md`、`07-final-acceptance.md`、`08-final-recheck.md`
- `.cache/team/bug-backlog.md`（最近一轮用户反馈 bug 与修复状态）
- `docs/STATUS.md`、`README.md`、`docs/PLAN.md`（可能互相矛盾，arch-auditor 重点查）

---

## 6. 报告结构（所有维度统一）

```markdown
# <维度>审计报告（ZeoKeel）

> 审计者：<name> · 日期：2026-11-05 · 基线：HEAD 7eebe47 · 方法：<代码阅读/运行/截图>
> 限制：<没做到什么>

## 0. 结论摘要
（≤10 行 + Top 发现表：ID | 严重度 | 标题 | NEW/KNOWN | 修复量 S/M/L）

## 1. 范围与方法（含实际执行的命令、覆盖的文件/屏）

## 2. 详细发现
### [<前缀>-01] <标题> · P0 · NEW
- 现象：
- 证据：`path:line` / 命令输出 / 截图
- 影响：
- 复现/验证：
- 建议修复：

（按 P0 → P3 排序；ID 前缀：FP=功能、RT=运行时、UX=UI/UX、AR=架构、PD=产品）

## 3. 已验证良好的方面（避免只列问题；3-6 条）

## 4. 已知问题复核（KNOWN 状态是否仍成立；表格）

## 5. 未验证/可疑项（明确写「未验证」）

## 6. 优先级建议（本维度 Top 5-10，按投入产出排序）
```

- 篇幅：150–400 行；**信息密度优先**，Top 10-15 深挖 > 50 条泛泛而谈。
- 每条发现都要能独立看懂（不要「同上」「见前文」）。
- 所有表格用中文表头；代码/命令/路径保留英文原文。

---

## 6.5 运行/截图方法学修正（Lead 2026-11-05 21:29 实测）

- **默认窗口是 1600×1000（`internal/app/app.go:182-184`），MinWidth/MinHeight = 900×600。** Xvfb 屏幕高度若只有 800，窗口底部（**Session composer/footer 在 y≈860-965**）会被裁掉，截图会显示"没有 composer"的假象——这是截图裁剪，不是 UI bug。**请用 `-screen 0 1600x1000x24`**，或 `xdotool windowsize <wid> 1600 1000`。测窄窗再用 `xdotool windowsize <wid> 900 600`（此时窗口区=应用真实最小尺寸）。
- 窗口状态会持久化在 `DataDir()/window.json`；不同数据目录必须各自隔离（`ZEOKEEL_DATA_DIR`）。窗口状态恢复不做屏幕边界校验，可作 P2 发现（小屏/多显示器场景）待验证。
- **单实例锁**：固定回环端口 `127.0.0.1:49731`（`internal/app/app.go:112-121`），全局唯一、与 data dir/display 无关；多人/多实例并行要用 `unshare -n`（uiux-auditor 已验证）。
- **HEAD 在审计期间会移动**：开始时 `7eebe47`，21:26 已到 `27d5d5f`（另一个并发开发进程在提交 Windows 图标/构建改动，工作树保持干净）。**每条发现请记录你实际验证时的 `git rev-parse HEAD`**；行号以验证时 HEAD 为准。不要清理/回滚/提交任何东西。

## 7. 基线事实（Lead 已执行，免重复）

- `go version go1.27.3 linux/amd64`；`CGO_ENABLED=0`。
- `go build ./...` ✅、`go vet ./...` ✅。
- `go test -count=1 ./...` ✅ 19 包全绿（耗时：shell 58s / session 33s / tools 7.3s / app 1.5s；其余 <1s）。
- `dist/zeokeel-linux-amd64`（49.5MB）可在 Xvfb 下启动并渲染；Home 首屏（未配置模型）可截图。
- 工作树在审计开始时干净，但 21:20 左右出现**另一个进程**生成的未跟踪文件（`cmd/zeokeel/resource_windows_amd64.{rc,syso}`、`logo/zeokeel.ico`、`scripts/gen-windows-resource.sh`）——与本次审计无关，不要动。

### 已知工作假设（可被证伪）

- 单 Agent 全局 workspace root 设计（per-session root 需大改）仍是架构遗留。
- 插件只做内置（无第三方市场）、无内核级沙箱是**既定产品边界**，不算缺陷；但要评估这个边界对「全功能桌面 Agent」目标的影响。
- 自动更新/签名/三平台安装包目前缺失（仅 Windows zip）。
- `/goal`、`/compact` 命令行有意未做；goal round driver 缺失。

---

## 8. 协作

- 每完成一个维度方向若发现 **P0**（安全/数据丢失/核心流程不可用），**立即** `send_message` 给 `lead`（≤10 行，带证据）。
- 完成时：写报告 → 确认文件存在 → `send_message` 给 `lead`：报告路径 + Top 5 发现（每条一行）+ 未验证项。
- 不要等别人，不要修改他人报告；跨维度重复发现各写各的，verifier/Lead 会去重。
- 时间盒：约 60–90 分钟有效工作；先广后深，优先保证 Top 发现的证据质量。
