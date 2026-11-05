# ZeoKeel 审计修复计划（v1，Lead 维护）

> 目标：修复 `docs/audit/MASTER-AUDIT.md` 的 P0/P1 全部 + 可行 P2/P3，并逐项验证；不可在本轮完成的 L 项写清延期理由。
> 基线：`HEAD=3100af0`（22:18，并发开发最后一个提交）。`go build/vet/test ./...` 基线全绿（Lead 已跑，见下）。
> 审计报告仍是问题清单真源：`docs/audit/MASTER-AUDIT.md` + 分报告 + 06/07 验证报告。修复时先读对应条目证据，修完把"修复前/后"写进 `FIX-REPORT.md`（Lead 汇总）。

## 0. 已知状态（先核对再修，避免重复工）

| 审计项 | 当前状态 @3100af0 | 处理 |
| --- | --- | --- |
| RT-06 模型流 60s 总超时 | **已由并发提交 3111614 修复**（默认无整体超时 + `provider.timeout_seconds`） | 只补慢流/配置回归测试（若缺） |
| RT-08 无重试/退避 | **已由 6bd0720 修复**（`internal/provider/openai/retry.go` 等）；另 3100af0 空响应重试 | 复核覆盖 + 失败回合落盘仍要做（RT-08 后半） |
| RT-01 workspace mcp.json 无审批执行 | 仍存在（`services.go:1441 connectMCP` 读 `<root>/mcp.json`，无信任门） | **P0 第一优先** |
| RT-02/03/04/05/07/09/10/11/12/13/14/15 | 仍存在（09 UTC、11 无 Chmod、12 固定端口、07 无 Lstat 等） | 按下方分配修 |
| UX-01/03 键盘发送静默 | 仍存在（`home.go`/`session_live.go` 直调 c.Toast） | 前端 P1 第一优先 |
| 其余 P1/P2/P3 | 见 master 附录 B 与各分报告 | 按分配修 |

## 1. 工作流与文件所有权（禁止跨范围写；需要交叉改动先发消息协调）

| 工作流 | 负责人 | 独占文件/目录范围 |
| --- | --- | --- |
| 后端 | fix-backend | `internal/app/**`、`internal/session/**`、`internal/runtime/**`、`internal/provider/**`、`internal/store/**`、`internal/tools/**`、`internal/mcp/**`、`internal/automation/**`、`internal/agentteam/**`、`internal/skills/**`、`internal/websearch/**`、`internal/prompt/**`、`internal/state/**`、`internal/winproc/**`、`internal/design/**`（仅必要时） |
| 前端 | fix-frontend | `internal/shell/**`、`internal/brand/**`、`internal/icons/**`、`internal/design/**`（样式令牌，与后端冲突时先消息） |
| 平台 | fix-platform | `cmd/**`、`scripts/**`、`.github/**`、`Makefile`、`packaging/**`、`docs/**`（**除** `docs/audit/**`，该目录 Lead 独占）、`.gitignore` |
| Lead | lead | `docs/audit/**`、集成/冲突文件裁决、最终验证与提交 |

规则：不改他人范围；不改 `docs/audit/` 既有报告；不 `git add/commit/stash/reset/checkout`（Lead 统一提交）；并发的 HEAD 移动不要回滚；停进程用精确 PID。

## 2. 后端（fix-backend）任务清单（按顺序，标 ✅ 表示完成验收）

**安全 P0/P1**
1. **RT-01（P0）**：workspace `mcp.json` 默认不执行。实现 `mcp.workspace_config` 设置（默认 `off`；`ask`/`allow` 可选）+ 在 README/known-limitations 记录；`connectMCP` 仅在允许时读取；记录来源与命令日志；补测试（默认配置下 workspace mcp.json 不执行；allow 时执行）。**必须跑通探针反向用例**。
2. **RT-02**：`Controller.submit` 在 `bindWorkspace` 后按目标会话权限重建（复用 `rebuildForPermission`，持 bindMu）；补单测（Full→新会话默认 workspace，submit 前重建；0 次 rebuild 断言改为有效）。
3. **RT-03**：Read Only 注册表收敛：`schedule_*`、`memory`、`goal` 在只读模式要求审批或移除（与 prompt 政策一致）；补测试断言只读下无持久副作用工具（或 `needsApproval=true`）。
4. **RT-07**：`tool_spill.go` 写前逐级 `Lstat` 拒 symlink + `O_CREATE|O_EXCL`；失败回退原文；补测试（symlink 场景不写出）。
5. **RT-11/PD-14**：DB/wal/shm 显式 `Chmod(0600)`；数据目录存在时 `Chmod(0700)`；补测试。
6. **RT-12/PD-03**：单实例锁改数据目录锁文件（`flock`/`O_EXCL`+PID，Windows 用命名 mutex 或文件锁 fallback）+ 保留端口作为兼容；二次启动提示带 data dir；如 mygo 支持，激活已有窗口（研究 `mygo.App`/窗口 API；不支持则文档记录 + stderr 提示）。
7. **RT-14**：auto-review 不再把 workspace `AGENTS.md` 作为授权证据（从 Constraints 移除或标记 untrusted + 长度上限）。
8. **RT-15**：`scrubEnv`/`scrubMCPEnv` 扩展 secret 名单（`*_KEY/*_TOKEN/*_SECRET/*_PASSWORD`、AWS/GITHUB/OPENAI/ANTHROPIC 等）或白名单；补测试。
9. **RT-04（P1，高难）**：并发模型——优先 `Controller` 持有 per-session `runtime.Agent`（工厂注入，`services.go` 构建每个会话的 agent），至少做到"审批等待不阻塞其它会话"。若风险过高，先实现 `SetAgentFactory` seam + 单会话回退 + 文档；补测试（两会话并行模型调用不再严格串行）。
10. **FP-08**：`write_file` 原子写（tmp+rename+fsync）+ `edit_file` read-before-write 强制（会话级已读文件表）；补测试。
11. **FP-13**：MCP 支持 streamable-http（`ServerConfig` 加 transport/url/headers；go-sdk 支持则接）；配置解析测试。
12. **FP-19**：统一代理设置 `network.proxy`（web_fetch 可配置；provider/websearch 已有默认代理）；保留 SSRF 校验。
13. **AR-18/PD-15**：窗口创建/恢复时按当前屏幕 WorkArea clamp（`app.go` + `window_state.go`）；补 headless 几何测试。
14. **RT-13**：终态 status/ToolEnd 不走可丢通道；审批 early 加 TTL/上限；补测试。
15. 其余后端 P2/P3（可实现部分）：AR-07 消息序（加 messages.ordinal 列 + 迁移）、AR-09 schema 迁移框架、AR-05 projector 收敛（删或 per-session）、AR-06/RT-10 批量持久化、AR-21 重复代码、AR-22 快照拷贝。**优先实现低风险项；高风险重构先列方案，等 Lead 决定**。

**功能 P1/P2（后端，按投入产出）**
16. FP-04：goal 工具增加 id/revision/phase/pause/blocked + Controller API（供 `/goal` 用；shell 由前端接）。
17. FP-07：schedule 任务记录 origin session；触发时投递回原会话（存在则 Submit，否则新建并标注）；automation 表加列/迁移。
18. FP-09：SQLite FTS5 会话搜索（虚表 + 触发器/重建 + 中文 trigram 或 unicode61；排序 + 降级 LIKE）。
19. FP-10：token 估算区分 CJK（rune-based）+ 模型 context budget 配置（settings）+ 旧图片 offload（压缩前替换占位）。
20. FP-11：checkpoint 三屏障（模型请求前/工具副作用前/pre-step）——至少模型请求前 + 工具前两个 checkpoint；恢复路径测试。
21. FP-15：job 完成通知（in-turn queue/step reminder）；team 任务板（复用 todo 表 + owner/revision）——后者可延后。
22. FP-16：ask_user 阻塞式（工具等待 answer channel；超时降级 pending）；UI 由前端配合。
23. FP-17：skills 多 root（workspace + `~/.zeokeel/skills` + project `.zeokeel/skills`）+ 手动 refresh；`/` 面板由前端加。
24. FP-18：headless CLI（`zeokeel run "prompt" [--json]`）：在 `internal/app` 提供 `RunHeadless`（新文件），cmd 由平台 worker 接；支持一次性会话 + 输出结果/退出码。
25. FP-05：workspace changes/回合 diff——新建 `internal/workspacechanges` 包（git status 快照 + 文件 hash），session 事件 `workspace/changes`，UI 由前端渲染。
26. FP-12：provider 预设/多 provider/模型发现：至少 provider preset 元数据 + `/models` 探测（前端接线）；重试已在 RT-08 修复后补齐。
27. FP-01/02/03/06/10 等 L 项：能降级的先降级实现（workflow 增加 parallel/pipeline JSON 语义；PTC 增加 filter/dedup 受限 DSL；subagent 支持异步 childId+通知；preset 改为 JSON 定义注册）——**按时间取舍，先做 workflow parallel + subagent 异步**。

每项要求：`gofmt`；定向 `go test ./internal/<pkg>`；关键项加回归测试；修完给 Lead 一行状态（ID + 文件 + 测试命令）。

## 3. 前端（fix-frontend）任务清单

**P1（先做）**
1. **UX-01/UX-03/PD-01**：键盘 Enter 发送失败统一走 `toastText`（`home.go`、`session_live.go`）；失败时回滚空会话/不产生空会话；补测试（toast queue 可见、session 不残留）。点 Send 已有 toast 保持。
2. **UX-02**：会话「对话」空态（图标+引导+无模型 CTA），复用 trajectory 空态语法；补渲染测试。
3. **PD-02**：添加模型对话框加 provider 预设（DeepSeek/OpenAI/Ollama 等）/「测试连接」（调后端新增 API 或 `/models`）——后端接口由 fix-backend 提供，先做 UI + 本地校验，接口缺失时用 provider 现有字段模拟。
4. **PD-09**：失败回合错误产品化（分类文案 + 打开模型设置/重试按钮；技术细节折叠/details）；补测试。
5. **PD-11**：Home 可返回（品牌行/侧栏入口）；补导航测试。
6. **PD-18**：删除模型二次确认；PD-17：添加工作区校验存在/可写（`os.Stat`）+ 不存在时「创建目录」选项。
7. **UX-05**：添加工作区对话框可见 label/placeholder；**UX-09**：侧栏工作区空态 + 添加按钮。
8. **UX-06**：Settings 独立窗口高度按内容/加底部 padding，确保首屏不切行；更新预览金标。
9. **UX-04**：打开设置/切换屏幕时统一关闭 popover（`closeAllPopovers`）；补测试。
10. **PD-05**：Settings 增加 Notifications（开关/免打扰）、Data & Privacy（key 存储说明/清空数据/导出全部）、Diagnostics（导出日志/版本/环境）、Network（proxy）、Updates（检查更新占位）、About 版本信息；持久化 settings 键；补 UI 测试。
11. **PD-06**：Automation 面加执行历史/失败原因/「仅前台运行」提示 + 创建/编辑表单加 workspace/权限/模型选择（后端表/接口由 fix-backend 加；先接上已有字段）。
12. **PD-07**：MCP 服务器管理（列表/添加/状态/连通测试）、Skills 列表（名称/描述/路径/校验错误/打开文件夹）、AGENTS.md 查看/编辑（模板）；需要后端 API（与 fix-backend 约定接口）。
13. **PD-08**：插件页统一（复用 `builtinPluginDefs` 6 项）；实验开关加「未实现」标注或禁用；「添加插件」保留边界说明。
14. **PD-10**：重启恢复上次会话 + 草稿持久化：shell 侧启动打开 `ui.last_session`、草稿落 store（后端加 store 键/接口，与 fix-backend 约定）。
15. **PD-12（可拆）**：托盘菜单加 New Session/Settings、关闭行为偏好、开机自启开关（平台层支持）、全局快捷键（mygo 支持则做，否则文档）。
16. **UX-07/08/10/11/13/14**：双主按钮收口、rail 会话 flyout、@ 菜单中文化、窄窗权限 chip 可见/tooltip、Start 页布局、toast 位置；补测试/更新预览。

## 4. 平台（fix-platform）任务清单

1. **AR-01**：崩溃诊断——`cmd/zeokeel/main.go` 设 `log.SetOutput(MultiWriter(stderr, DataDir()/logs/app-YYYYMMDD.log))`、`defer recover` 写 crash 文件、`runtime/debug.SetCrashOutput`（Go 1.27 可用则用）；日志轮转（按大小/天数）；补测试（日志文件创建）。
2. **AR-03/版本**：`-ldflags "-X main.version=... -X main.commit=... -X main.buildTime=..."`，About 显示版本/commit；`build.sh` 默认 `-trimpath -s -w`（体积 -31.8%）；版本号从 git tag/常量。
3. **AR-02/CI**：新增 `.github/workflows/ci.yml`（gofmt/vet/build/test/check-nocgo；race 至少 app/state/session/tools/runtime/store/agentteam，shell 单独 job `-timeout 20m`）；coverage 基线记录；禁止 flaky 重跑（改 `scripts/evolve/gates.sh` 或至少 CI 不用）。
4. **AR-16/文档**：建 `docs/BUGS.md`（把 master 附录 B 的 P0/P1 + 未修项迁入）；更新 known-limitations/FUNCTIONAL-PARITY 三处反向结论（auto-review/goal driver/plan mode）；STATUS 收敛（≤100 行 + commit 锚）；修 README/PLAN 文档矛盾；README 增加当前状态/构建说明。
5. **FP-18 CLI**：`cmd/zeokeel/main.go` 增加子命令 `run`（调用 internal/app.RunHeadless；后端提供后接）、`--version`、`--help`；platform worker 与 fix-backend 约定接口。
6. **打包**：新增 Linux tar.gz 脚本 + macOS .app（可选脚本，无签名说明）；Windows 安装器可评估 NSIS/zip 说明；`make package-linux`/`package-macos` 目标 + README。
7. **docs/audit/FIX-REPORT.md 模板**（Lead 汇总用，平台 worker 不写）。

## 5. 延期 L 项（本轮不做，写进 known-limitations/roadmap）

- workflow 完整 JS 引擎、PTC Node/TS 运行时（需引入新运行时，与"纯 Go/no-cgo"约束冲突；先做受限 DSL/文档说明）。
- ACP/SDK 完整协议、三平台签名/公证/自动更新基础设施（需证书/发布账号）。
- `internal/shell` 41k 行整体分层重构、schema v2 大迁移、JSONL 真源重写（风险高，分批）。
- 真机 IME/屏幕阅读器/DPI/通知矩阵（无真机）。

## 6. 验证与交付

- Lead 每完成一波：`gofmt`/`go vet ./...`/`go build ./...`/`go test -count=1 ./...`/`sh scripts/check-nocgo.sh`；关键 P0/P1 跑回归测试 + Xvfb 真跑冒烟（1600×1000，隔离 data dir，unshare -n）。
- 修完 → Lead 生成 `docs/audit/FIX-REPORT.md`：ID | 修复前 | 修复内容 | 文件 | 验证证据 | 状态（FIXED/PARTIAL/DEFERRED）；再交独立验证员抽查 P0/P1（可复用 verifier-core/ui 思路）。
- 提交策略：Lead 在每波验证绿后统一 `git commit`（用户可见的分批提交），不 push。
