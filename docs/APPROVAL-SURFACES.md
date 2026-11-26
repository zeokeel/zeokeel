# 审批表面（APPROVAL-SURFACES）

> **活文档**。原生 UI 的 12 个 surface 规格已随 `S5: retire the native UI` 退役
> （归档见 [archive/AGENT-SURFACES.md](archive/AGENT-SURFACES.md)），但"哪些工具会
> 停审批"是**浏览器客户端与托盘壳都还在依赖的行为契约**，因此单独成文。
>
> 协议侧：`approvals.snapshot` / `approvals.delta` / `approval.resolve`
> （[TRANSPORT](TRANSPORT.md) §5.1.3）；客户端渲染见 [WEB-CLIENT](WEB-CLIENT.md)。

#### S4.1 哪些工具会停审批（批准卡的真实触发面）

> 结论先行：**这是个个人助手，跑在用户自己的电脑上，只有一个真人。审批卡挡不住真正的
> 风险（读本机数据本来就不弹卡），却会打断每一次正常操作；真正的边界是"能不能写 /
> 能不能外发"**。因此（2026-11-26 产品决策）**MCP 工具与命令执行都不弹审批**，文件
> 读取放开到整台机器，写入仍然只限当前工作目录**与私有临时目录**（§S4.3）。今天会停卡的
> 只剩：网络工具、设置读写、沙箱授权申请、任务/调度/工作树/后台作业控制，以及不认识的
> 工具。判断顺序（`internal/modules/agent/plan_mode.go` 的 `gate()`）：

| 优先级 | 层 | 命中即 | 条件 | 来源 |
|---|---|---|---|---|
| 0 | **会话角色（agent preset）的工具白名单** + 任务工具绑定 | **deny**（不给审批机会） | 会话选了带 `Tools` 的角色（如内置 `lead`），或绑定了某个任务（`session.bindtask`） | `internal/modules/agent/preset.go` `presetToolsDenial` / `taskbinding.go`（两者都在 `plan_mode.go` 的 `gate()` 里，位于下面所有层之前） |
| 1 | plan / minimal / creator 模式的工具白名单 | allow 或 **deny**（不给审批机会） | 模式开启时（plan 模式下 MCP 与命令都不在白名单里，因此直接拒绝） | `plan_mode.go` 门 `agentModePlan()` / `chatMode()` |
| 2 | 会话级 permission 覆盖 | full=全部 allow；read-only=只读集 allow、其余 **deny**（含 MCP）；workspace=共享自动放行集（含 MCP 前缀家族）allow，未命中继续 | 会话设置了 `session.permission.set` | `contracts.ReadOnlyAutoAllowedTools` / `WorkspaceAutoAllowed` |
| 3 | **OS 沙箱已约束**（SDK 认证） | allow（**不弹卡，静默**；无 approvals 服务的装配也放行——这次调用本来就不需要审批） | sandbox 模块 active + runner 报告真实约束 + policy ≠ danger-full-access | `internal/modules/agent/sandbox.go` → `gate.AllowSandboxedCommands` |
| 4 | **命令家族但 SDK 未认证约束** | allow（**不弹卡**；无 OS 约束这件事在 `sandbox.status` / 设置页可见，模型侧文案也如实写） | 沙箱模块在场（`contracts.KeySandbox` 已装配）**且**该 run 的 root 有命令策略（`policySource` 即 `contracts.KeyToolPolicy` 给出 `commandPolicy(root) != nil`；只有模块没有策略源时本条不成立，命令落回第 8 层弹卡）而 runner 缺失 / runner 报告无强制 / policy 为 danger-full-access | `internal/modules/agent/sandbox.go` `shellCommandExempt` + `recordUnconfinedShell` |
| 5 | **MCP 家族** | allow（**不弹卡**） | 工具名以 `mcp__` 开头（`contracts.IsMCPTool`） | `plan_mode.go` 的 MCP 分支 |
| 6 | 交互工具 | allow（它们自己的 overlay 才是交互） | 见下方 interaction 清单 | `plan_mode.go` `interactionTools` |
| 7 | 记住的「本会话允许」 | allow | 用户曾点过"始终允许" | `session.allowedTools` |
| 8 | 以上都没命中 | **弹审批卡**（`approvals.Request` 阻塞） | 默认 workspace 模式 | `internal/modules/approvals` |

第 2 层与第 8 层之外还有一条**硬性关闭**：`approvals == nil`（装配里没有审批服务）时，
凡是要审批才能执行的调用一律 deny 并附可读错误。第 4、5 层（命令豁免、MCP 豁免）在第 8 层
**之前**，所以它们各自也实现了这条 fail-closed（2026-11-26 审计：此前它们只判断了
`Mode()=="never"`，组合"有 sandbox 模块 + 无 approvals 服务"会把两个家族全部静默放行）。
headless 的 deny-all 策略（`Mode()=="never"`）同样在每一层前置生效。

**第 0 层（角色边界，2026-11-26）**：角色就是 `AgentPreset`，"lead 不干活"因此是**硬边界**而不是提示词。
会话用 `session.preset.set` 选到带 `Tools` 允许列表的角色后，列表外的工具在 `gate()` 里直接 `DecisionDeny`
（拒绝理由走 SDK 的可选 `DenialExplainer` 通道，模型看到"角色 lead 只允许这些工具：…"）；它排在下面
所有"可以放行"的分支之前，所以**记住的"始终允许"、会话 permission 覆盖、沙箱豁免都**不能把角色撑大。
`Tools` 为空 = 全部工具（执行者角色就是这样）；没选角色的会话完全不受影响（默认行为不变）。
`spill_read` / `tool_search` 是运行时读回能力，任何允许列表都移除不了它们。
委派出去的**子 run 用执行者角色**（`delegatedToolsForSession` / `delegatedSystemPrompt`），不继承编排者的
只读人设——否则被派去做事的智能体会因为"你不能改文件"而拒绝工作。

**"留空 = 授予全部工具"是产品语义，因此内置角色的覆盖必须显式清空（2026-11-26 审计，中高）**：
`presets.agents` 里同 ID 的用户行覆盖内置角色时，**省略** `tools` 字段表示"沿用内置允许列表"，
只有显式 `"tools": []` 才是"清空 = 全部工具"。原因很直接：空 allowlist 等于全权限，而"只改人设"
的编辑最常省略 `tools`——那一瞬间内置 lead 会静默获得全部工具，而它的人设文本还写着"你的工具集里
没有这些工具"。两句话必有一句是假的；让门禁赢。用户在设置 → Agent 预设里编辑这个 JSON 时看到的就是
这条语义（`settings.ia.help.presets`）。

**未知角色 = 拒绝，不是"没有角色"（2026-11-26 审计，中）**：`session.<id>.preset` 行存在但解析不到
角色（预设被删、或 `settings.set` 直接写坏了这一行）时，`gate()` 按"未知角色"拒绝每一次工具调用
（理由写明行名与两条出路：`session.preset.set` 选一个存在的角色，或清空该行），`toolsForSession`
也不再提供任何工具。它**绝不**退回"没有角色 ⇒ 全部工具"。`settings.set` 写 `session.<id>.preset`
时也已改为走 `SetSessionPreset` 的校验（`contracts.ParseSessionPresetSettingKey`），所以坏值只可能
来自旧文档——运行时仍然 fail-closed。

**模型侧文案与门禁同源（2026-11-26 审计，第三处同类分叉）**：审批策略那句话
（`approvalPolicyLine`）过去在**无会话覆盖**时是三段手写字符串，只读预设的名字，不读
`ApprovalsAvailable`、`DenyAll`、以及"沙箱装配了但没有命令策略"。于是三种组合下模型读到的是**假话**：
(i) 装配里没有 approvals 服务 → 文案说"shell/MCP 免审批"，门禁在第 8 层 fail-closed 全拒；
(ii) headless deny-all（`Mode()=="never"` 归一成 read-only）→ 文案说只读豁免，门禁每层都拒；
(iii) 沙箱模块在场但 `commandPolicy(root)==nil` → claim 判命令家族豁免，`shellCommandExempt` 却要求策略存在，调用落到弹卡。
现在**两个全局三支与覆盖层一样，从同一张 claim 表 `approvalPolicyClaims` 渲染**（tier 里新增
`CommandPolicyAvailable`，`effectiveApprovalPolicy` 用本 run 的 root 求值），并且
`internal/modules/agent/permission_policy_test.go` 的 `TestApprovalSentenceAgreesWithTheGate` 把**句子文本**
逐家族读出来，与**真实 `gate()`** 对同一代表工具的回答逐一比对（不是钉字面量：旧的 `TestApprovalPolicyLines`
那种字面量断言正是假话能绿的原因）。改任一侧都会红。

**机器可读清单**（测试 `internal/modules/agent/approval_surface_test.go` 逐条对照代码，改规则不改文档会红；
"每个已注册工具都必须落在某个家族里"的双向检查在 `internal/modules/toolkit/approval_surface_registry_test.go`，
因为工具注册表只在 toolkit 里）：

<!-- approval-exemptions:begin -->
sandbox-shell: bash, pwsh
mcp-family: mcp__
workspace-auto: apply_patch, edit_file, glob, grep, inspect, job_list, job_output, job_wait, list_dir, lsp_definition, lsp_diagnostics, lsp_hover, lsp_implementation, lsp_references, lsp_symbols, memory, read_file, read_image, sandbox_status, search, session_event_read, session_event_search, session_event_trace, session_search, session_trace, skill, spill_read, str_replace_editor, team_task_get, team_task_list, terminal_list, terminal_read, todo_write, tool_search, undo, working_directory, write_file
interaction: ask_user, present, plan_review, exit_plan_mode, get_goal, create_goal, update_goal
role-deny-lead: apply_patch, bash, edit_file, pwsh, write_file
parks: enter_worktree, exit_worktree, job_kill, list_worktrees, request_sandbox_access, schedule_create, schedule_delete, schedule_list, schedule_update, settings_get, settings_set, team_task_create, team_task_update, web_fetch, web_search
<!-- approval-exemptions:end -->

- `sandbox-shell` 是**命令执行面**（`sandboxShellTools`，`internal/modules/agent/sandbox.go`）。它在**沙箱模块在场时就免审批**，分两种情况：SDK 认证该调用已被 OS 约束 → 静默放行（第 3 层）；SDK 不认证（无 runner / runner 报告 `none` / danger-full-access 策略）→ 仍然放行，但按"会话 + 工具"各记一条 `[gate] <tool> ran without an approval prompt: … no OS confinement …` 日志（第 4 层）。**不留隐式退回**：没有"沙箱不可用就退回弹卡"的第三条路径。装配里根本没有 sandbox 模块时（`s.sandboxService == nil`，也就没有工具策略源 `contracts.KeyToolPolicy`），命令照旧走审批——那是一个被裁剪过的装配，不是本产品的默认；两个出货产品（`serve` / `headless`）都装 sandbox 模块。
- **"无 OS 约束"是用户可见状态，不是一行日志**（2026-11-26 审计，严重 1）。三层披露：
  1. **网关状态面**：`sandbox.status`（TRANSPORT §5.1.5）返回 `confinement ∈ {os, none}`、`commandsUnconfined`、`note`；设置 → 运行状态页把它渲染成一行明示状态。
  2. **模型侧文案**：`system_prompt.go` 的 approval policy 段不再写"sandboxed shell commands"（对未约束的命令是假话），改由 `unconfinedCommandsLine` 在无 runner 时补一句 "OS confinement: none — … the working-directory write rule is a rule you must follow, not a boundary the OS enforces"；`sandbox_status` 工具在无 runner 时也报告 `runner: {name:"none", enforcement:"none"}` 而不是省略该字段。
  3. **日志**：`recordUnconfinedShell` 仍按"会话 + 工具"记一次——但要清楚它**只是 `logger.Printf`**，客户端看不到，不能当成披露渠道。前两条才是。
- `mcp-family` 是**前缀家族**（`contracts.MCPToolPrefix` / `contracts.IsMCPTool`）：`mcp__<server>__<tool>` 任何具体名字都免审批，因为它是运行时数据、冻结清单永远追不上。会话级 read-only 覆盖仍然**直接拒绝** MCP（显式收紧优先），plan/chat 白名单同理；headless 的 deny-all 策略（`approvals` 的 `never`）仍然拒绝；无 approvals 服务时也 deny（fail-closed）。
  **注意一个后果**：存量的 `agent.permission = read-only` 预设下 MCP 也**不再弹卡**（该分支在 `approvals.Request` 之前）——这与命令家族一致，但**只读预设因此不再是"MCP 也会问一声"**。只读预设真正的围栏仍是文件策略（写工具与 shell 的文件效果）。若要"只读时连 MCP 也拒"，那是另一个产品决定（改成会话级覆盖或加一条策略判定），不是现在的实现。
- `workspace-auto` 是 workspace 模式自动放行集（`contracts.WorkspaceAutoAllowedTools`，含只读子集与 `memory` 等应用状态工具）。**MCP 不在这个 map 里**（它按前缀判定），所以判"会不会弹卡"要用 `contracts.WorkspaceAutoAllowed(name)`，不要直接查 map。
- `interaction` 不弹审批，因为工具本身就是一次用户交互（DTO 形状见 S5/S6）。
- `parks` 是**保证会停审批**的家族（2026-11-26 审计补齐了 7 类真正会停卡却漏写的工具：`settings_get`、`request_sandbox_access`、`job_kill`、`schedule_create/list/update/delete`、`enter_worktree/exit_worktree/list_worktrees`、`team_task_create/team_task_update`）。浏览器验收里的"审批停住 → 允许 → 流式完成"仍然用 `web_search` / `web_fetch`。
- **模型可见的策略文案跟随生效权限档，不跟随 approval mode（2026-11-26 审计，高）**：
  `system_prompt.go` 的 `Approval policy:` 段由 `approvalPolicy`（全局预设 + 会话覆盖 + 是否有 approvals
  服务 + 装配里是否有 sandbox 模块 + 宿主 OS 约束）推出，其中会话覆盖那一层直接调用 `gate()` 用的同一个
  `overridePermission`。因此**会话 read-only 覆盖下，提示明说 shell（bash/pwsh）与 MCP 会被直接拒绝、
  不会弹卡**——而不是旧文案里那句"shell 与 MCP 不经审批运行"。一致性的门禁测试是
  `internal/modules/agent/permission_policy_test.go` 的 `TestApprovalPolicyClaimsMatchTheGate`：
  它把同一批代表工具真的交给 `gate()`，逐条比对"文案说会放行/会拒/会弹卡"与门禁的实际决定。
- **两个 "read-only" 指的不是同一件事**（2026-11-26 审计）：
  - `agent.permission = read-only`（**全局预设**，`docs/TRANSPORT` 的 approvals 模式）：它同时是文件策略档（写工具被策略拒绝）与审批档（除只读集外一律弹卡）。
  - 会话级 `session.permission.set` 的 `read-only`（**会话覆盖**）：它只改审批门禁——只读集之外的调用直接 deny（不给审批机会），**不改 OS 文件策略**（OS 沙箱永远跟全局预设）。
  - 反过来也要说清：**会话级 `workspace` 覆盖对命令与 MCP 不构成任何限制**。它只把 workspace 自动放行集加进 allow；命令家族本来就走第 3/4 层（与覆盖无关），MCP 走第 5 层。也就是说把一个会话设成 workspace 不会让命令"更受限"，也不会让它"刚开始弹卡"。

#### S4.2 MCP 工具注解（destructiveHint / readOnlyHint）与审批

> 结论先行：**注解仍然不参与任何判定**。而"危险工具必须停审批"这一层今天也**不存在**了——MCP 家族整体免审批（§S4.1 第 5 层），所以也没有"靠注解把它补回来"的余地。

- **为什么免审批**：审批卡挡不住真正的风险（模型本来就能读本机数据、能跑命令），却要为每一次 MCP 调用打断会话。个人助手的边界应该是"能不能写 / 能不能外发"（§S4.3 与网络工具），不是"每次调用都问一遍"。
- **注解尚未被读取**：`tools/list` 返回的 `annotations.readOnlyHint` / `destructiveHint` / `idempotentHint` 目前不参与任何判定。`internal/modules/toolkit/mcp_control.go` 只把工具名、描述与参数 schema 交给注册表，审批侧只按名字家族判定（§S4.1 `mcp-family` 行）；工具卡片上也不会出现"只读/破坏性"标记。
- **这条产品决策的代价，说清楚**：`destructiveHint: true` 的 MCP 工具现在**不会**停审批。`docs/plans/grokbot-open-source-plan.md` §8 M2.5 批次 A 的验收条件"destructive 注解的工具一定停审批"因此对这个家族**不再成立**（它靠的是"全部都停"，而全部都不停了）。要恢复那一层，只能重新引入"按注解分流"——而注解是服务端自述、可撒谎且缺省语义含糊，需要先有服务端信任等级与 UI 明示，否则等于把安全面换成第三方自我声明。**未实现，且本次刻意不实现。**
- 相关条目：`docs/plans/grokbot-open-source-plan.md` §7.5 G14 #1（注解→审批）、§8 M2.5 批次 A。"单 server 超时不拖住整轮"已实现且同样**不触碰审批面**：超时只让一次调用以 `*MCPCallTimeoutError` 失败（`internal/modules/toolkit/mcp_call_timeout.go`）。

#### S4.3 文件读写范围：读全系统，只写工作目录与私有临时目录

> 结论先行：**读不设围栏；写的围栏是"当前工作目录 + 私有临时目录"**（不是只有工作目录：
> `/tmp` 与 `$TMPDIR` 恒在可写面里，见下）。这需要一个产品侧的拆分，因为上游 rig v0.38.1
> 的 `sandbox.Policy` **把读根与写根焊在一起**：`ResolvePath` 把所有路径（读和写）都限制在
> `Roots()` 内，而 `WritableRoots()` **就是** `Roots()` 加上追加的可写根——把 `Roots` 放开到
> `/`（读全系统）会同时把可写面、以及从 `WritableRoots()` 渲染出来的 OS 沙箱 profile 一起
> 放开到 `/`。rig 没有 `readRoots` / `writeRoots` 之分，也没有任何"只读根"选项。

因此拆分在**产品侧**做（`internal/modules/toolkit`），一个 build 里两个策略：

| 策略 | 根 | 模式 | 绑给谁 |
|---|---|---|---|
| 读策略 `newReadPolicy` | `[工作目录, 文件系统根]`（第一个根仍是相对路径基准） | `ModeReadOnly`（构造上写不了） | `read_file`、`list_dir`、`glob`、`grep`、`read_image` |
| 写策略 `newPolicy`（未改动） | `[工作目录]` + 追加可写根 | 用户的权限档 | `write_file`、`edit_file`、`str_replace_editor`、`apply_patch`、`undo`、`bash`/`pwsh`、`job_*`、`worktree`、`sandbox_status` |

- **围栏是真的（在有 OS runner 的主机上）**：写工具落盘前问 `Policy.CheckWritablePath`（根围栏 + 只读子路径），`apply_patch` 走同一个策略；shell 的文件效果由 OS runner 按**写策略**渲染的 profile 限制。
- **可写面的准确表述**（2026-11-26 审计）：`workspace-write` 的实际可写集合是
  **工作目录 ∪ 追加可写根 ∪ 平台临时目录 ∪ 私有 scratch**。平台临时目录两项开关
  （`sandbox.Policy` 的 `IncludeSlashTmp` / `IncludeTmpdirEnv`）**默认都是开的**
  （rig `sandbox/sandbox.go:335`，渲染在 `sandbox/scratch.go:105-125`），所以
  **`/tmp` 与 `$TMPDIR` 恒在可写面**。任何"只写工作目录"的说法都必须写成
  "只写工作目录与私有临时目录"，否则是过度承诺。
- **这份"读策略"绝不能交给 runner，也不能交给 `sandbox_status`**（硬约束）：
  读策略的 `Roots()` 是 `[工作目录, "/"]`，而 `Policy.WritableRoots()` 不按模式过滤、
  直接返回 `Roots()` + 追加根 + 授权——把它交给 runner 会渲染出"整个文件系统可写"的
  profile，交给 `sandbox_status` 会让模型看见一份不存在的可写面。两者只接写策略
  （`rebind.go` 的 `policy`）。断言测试：
  `internal/modules/toolkit/policy_split_test.go` 的
  `TestReadPolicyNeverReachesTheRunnerOrTheStatus`。
- **诚实说明的边界**（这一半做不到的，写在这里）：
  - **没有读的拒绝清单**（也不打算有）。`~/.ssh`、`~/.aws`、浏览器 profile、本产品自己的
    凭据存储都在读范围内；`/proc/**/mem`、设备文件也不会被额外拦截：rig 唯一的读侧过滤是
    `WithHidden`（用户设置 `toolkit.deniedGlobs` 的隐藏清单，仍然生效），它按 glob 隐藏路径，
    不是按"危险设备/凭据"分类。之所以不加一层假清单：shell 的读本来就不受围栏约束（见下一条），
    工具层再拦只会是表演。
  - **组合风险要说清**：读全机 + 命令免审批 + `network: true`，意味着**唯一剩下的外发闸门
    是 `web_fetch` / `web_search` 的审批卡**。模型不需要它们也能外发：`bash` 里 `curl`、
    `git push`、任意进程都行（命令免审批，网络由进程权限决定）。所以这张卡挡的是"模型主动
    抓/搜网页"这一条路，不是"数据出机器"。用户若要真正的外发边界，得关掉命令工具或加
    OS 层网络隔离（rig 的 network 覆盖度因后端而异：`NetworkReporter` 未实现的 runner 一律按 "none" 报告，见 §S4.1 第 3/4 层）。
  - **硬链接绕过**：rig 从不做 inode 级判定（`sandbox` 包注释自陈；`dsh_parity_test.go`
    把"硬链接守卫已摘掉"钉成契约）。工作目录内一个指向目录外的硬链接，策略层既不拒绝也不
    特殊处理——写它等于写目录外那个文件。这是路径前缀判定的固有缺口，不是本产品的 bug；
    唯一能做的是提示用户别在有硬链接的目录里放开 agent。
  - **shell 的读、进程可见性、设备访问都不在承诺内**（rig 的沙箱承诺本来就只治理文件效果）。
    `bash -c 'cat /etc/shadow'` 能读到什么，取决于进程权限，不取决于这里的拆分。
  - **`search`（BM25 检索）仍然是工作目录范围**：它的遍历按构建期 `root` 走（`search.New(root, …)`），不经过 `Policy.OpenPath`。要跨全机检索得另做一次产品决定（遍历成本与结果质量都会变）。
  - **`lsp_*` 工具仍然是工作目录范围**：`sandboxedLSPTool` 用写策略的 `ResolvePath` 校验 `path` 参数（LSP 服务本身也是按工作区配置启动的）。要让 LSP 看工作区外的文件，得连服务启动范围一起改。
  - **Windows 只放开进程所在卷**（`filesystemReadRoot()` 返回 `C:\` 之类）：其他盘符需要各自的策略根，产品目前没有这个入口。
  - **`sandbox_status` 报告的是写策略**（可写面与模式），它不会说"读无围栏"；但它**会**
    报告 runner 与 enforcement（无 runner 时 `runner: {name:"none", enforcement:"none"}`），
    即"命令有没有 OS 约束"这一半现在可查。模型侧的运行时上下文里由 `workingDirectoryLine`
    说清读的一半，由 `unconfinedCommandsLine` 说清"无 OS 约束"的一半。
  - **要做到"上游原生支持读写分离"**，需要 rig 增加独立的读根（例如 `WithReadRoots`，让 `ResolvePath` 用读根、`CheckWritablePath`/`WritableRoots` 用写根）。那是上游改动，本次没有 fork。
