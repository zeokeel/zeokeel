# FIX-REPORT — 1.0 功能缺口「统一收口」轮（2026-11-06）

> 范围：用户指令「除了反馈联网上送、模型配置剩余限制不需要管，其他功能都补齐（TDD）」，并在本轮追加「流式输出、显示思考内容、不必要的折叠」（先调研 DSH 处理方式）。
> 方法：Agent Teams 并行 + 每项 TDD（先写失败测试记录 RED，再实现到 GREEN）+ 3 组独立审查（REVIEW/REVIEW-2/REVIEW-3）+ P2 修复 + 全量门禁预跑。
> 关联：[BUGS.md](../BUGS.md)（缺陷状态）、[STATUS.md](../STATUS.md)（状态真源）、[TDD-RED-EVIDENCE.md](TDD-RED-EVIDENCE.md)（RED→GREEN 证据）、[FUNCTIONAL-PARITY.md](../FUNCTIONAL-PARITY.md)。

## 0. 范围与排除项

### 本轮交付（13 项）
| # | 交付 | DSH 参考 | 备注 |
| --- | --- | --- | --- |
| 1 | 工作区变更 inline diff（捕获/统一 diff/事件持久化/可展开 UI/重开回放） | `dsh-workspace-changes` | 比 DSH 多持久化（重开仍可见） |
| 2 | `/goal` 斜杠命令（status/create/edit/pause/resume/complete/abandon/clear） | `dsh-command-goal` | 命令不进模型；状态/错误稳定文案 |
| 3 | `/compact` 手动压缩命令 + **会话级持久替换** | `dsh-command-compact` | 早期历史被 summary 替换，重开/后续回合不回退 |
| 4 | `/goal edit` 保持同一 goal 身份（id/phase/rounds 不变） | `dsh-command-goal` | 不再映射为 set→新 id |
| 5 | `@` 提及细节：`@"带空格路径"`（含 `"`/`\` 转义与往返解析）、chevron 指针下钻、面包屑多级点击、会话行 workspace/时间详情、跨工作区候选 | `dsh-client-ui-reference` | 插入 canonical mention（mygo TextArea 无内联芯片，记为 UI 差异） |
| 6 | 会话引用原子化：canonical `@[label](dsh-session:<id>)` + 受限只读快照作为**第二条 user 消息**注入（≤3 来源/单源 ≤8KiB/不可信警告/omitted 计数/幂等/可回放） | `dsh-session-reference` | 注入行在 Chat 折叠为紧凑 disclosure |
| 7 | 崩溃中断回合修复：消费 requested/tool_pending checkpoint，幂等收尾（补 unknown-outcome 工具结果 + 中断锚点，不重放副作用） | `dsh-agent-loop` interrupted-turn closers | 明确不做副作用重放 |
| 8 | ACP 完整面：`session/list`、`session/load`、`authenticate`、server→client `fs/*`/`terminal/*` seam、`session/request_permission` 四选项映射 | `dsh-acp` / `dsh-acp-app` | 无 client 能力 fail-closed，不 panic |
| 9 | LSP 工具：stdio JSON-RPC client + `lsp` 工具（definition/references/implementation/hover，1-based UTF-16，输出有界）+ 安全门 + 内置插件页开关 + inspect 设置键 | `dsh-lsp`/`dsh-lsp-stdio`/`dsh-tool-lsp`（可选包） | 冷启动 server、无连接池（已记录） |
| 10 | Wayland 全局快捷键：纯 Go D-Bus + xdg-desktop-portal `GlobalShortcuts`（CreateSession/BindShortcuts/Activated，fake portal 测试，X11 回退） | ZeoKeel 平台缺口（非 DSH 对齐项） | 真实 GNOME/KDE portal 需真机 |
| 11 | 导入器收尾：运行中实例检测（默认拒绝 + `--force`）、图片/附件对象迁移（25MiB 帽/计数跳过/Lstat 拒绝 symlink）、manifest v2 旧版迁移+损坏容错、失败会话补偿回滚 | ZeoKeel 自身功能（DSH 无迁移器） | 仍为「文本+附件导入」，工具/轨迹不迁移（设计不变） |
| 12 | **Chat 流式思考展示与折叠**（用户追加）：每轮「思考」disclosure，running 单行实时 preview（最后完成段落首行）+ 透明度脉冲、完成后「已思考」默认折叠、点击/Enter 展开 mono 全文、展开态按消息保持、无 reasoning 不占位、答案永远可见 | DSH `ReasoningRow`（`dsh-client-ui-chat`） | 完成态无 settled preview；shimmer 为透明度脉冲；正文 mono 非 compact Markdown（均已记录） |
| 13 | 独立审查 P2 修复（7 项）：LSP rootURI 生效/parse 先截断、Wayland 拒绝后 Close session、ACP server→client 同步路径守卫、导入器失败回滚+symlink Lstat、workspacechanges 死代码删除、思考行 streaming 预览缓存、packaging README 口径 | — | 见 §4 |

### 明确排除（用户指定/产品决定/框架核实不存在）
- 反馈联网上送、模型配置剩余限制（用户指定不做）。
- PTC 代码运行时、语音输入（2026-11-06 产品决定不做）。
- **auto-memory**：核实 DSH 0.2.1-rc.2 安装包内无任何 memory/auto-memory 包或能力（`ls node_modules/@deepseek-ai | grep -i mem` 为空；系统提示无 memory 指令）；ZeoKeel 反而已有 memory 工具 + Settings › Memory 页。故不作为对齐缺口。
- **computer-use**：核实 DSH 安装包内无 computer-use 工具包（工具包清单见 `dsh-agent-preset/skills/cordis-composition-reference/references/packages.md`），非对齐项。
- 迁移器完整化：仍按产品定位「最小导入（文本+附件，技术预览）」，不导入工具调用/轨迹（messages 表无 tool_call 列，强行合成会破坏后续模型调用）。

## 1. 关键契约（用户可见行为）

- **工作区 diff**：每回合开始保留受控文件基线、结束生成统一 diff（3 行上下文；单文件 ≤1MiB/≤2000 行/≤64KiB；二进制/NUL→binary；超限→oversized/coarse；每回合 ≤100 文件/≤256KiB，超限 truncated）；payload 随 `workspace/changes` 事件持久化，重开可回放；UI 默认折叠、可展开、+/- 着色、点击仍打开文件。
- **/compact**：无参数；忙碌/无历史/无压缩器/保存失败给稳定文案；成功「Compacted N history items, about T tokens saved」；**持久替换**：store 事务删除被替换前缀+清理其 tool_calls+summary 落库+ordinal 重排+tail 保留；`state.Compaction` 事件 fold 幂等，重开/后续回合读压缩后历史；不改模型最终答案可见性。
- **/goal**：命令不进模型；`/goal` 显示 objective/phase/rounds/下一步；控制词仅整词识别；`edit` 同 id 不重置续轮预算。
- **会话引用**：`@[...](dsh-session:...)` 精确往返；每轮最多 3 来源、单源 8KiB、固定不可信警告、`<referenced-sessions>` tag-safe JSON；引用的会话内容作为 prompt 后第二条 user 消息进入模型历史（持久化、重开回放、下一轮不重复）；Chat 中以紧凑引用 disclosure 呈现，不显示原始 JSON、不产生幻影 turn。
- **崩溃中断修复**：requested/tool_pending checkpoint → 已派发无结果的工具补 unknown-outcome（不重放）、assistant 中断锚点、按 turn 去重 marker、checkpoint 落 completed；再次恢复 no-op；用户 Stop 不误判。
- **ACP**：list 按 updatedAt 倒序+cwd 过滤；load 回放 user/assistant 文本；authenticate 默认 no-op、可配门禁；fs/terminal 请求有超时/能力门控；request_permission 四种选项映射到 ApprovalQueue；同步控制 handler 内发起 server→client 请求返回明确错误不阻塞。
- **LSP**：`lsp.servers`（语言→{command,args,rootURI?,env?}）或工作区 `.zeokeel/lsp.json`（默认 off，需 `lsp.workspace_config=allow`）；无 server 给稳定提示；输出 ≤16KiB、位置 ≤50、UTF-16 列；`plugin.lsp.enabled` 默认开，Plugins 页可关。
- **思考行**：running=「思考」+单行实时 preview（最后完成段落首行，省略号、透明度脉冲，ReduceMotion 静止）；有答案/回合结束→「已思考」默认折叠；点击/Enter 展开 mono 全文（长文本换行不溢出）；展开态按 assistant 消息 id 保存；无 reasoning 不渲染不占行；顺序=思考→工具→答案。
- **Wayland**：Wayland 会话优先 portal（CreateSession→BindShortcuts→Activated 回调，Session.Close 释放），失败回退 X11/XWayland，再失败 ErrUnsupported；非 Wayland 不碰 D-Bus。
- **导入器**：运行中默认拒绝（`--force` 覆盖；dry-run 不探测）；附件迁移计数（images_migrated/attachments_migrated/attachments_dropped + skip 原因）；失败会话补偿回滚且不写 manifest（重试不重复）；manifest v2 升级不重导、损坏从 .bak 恢复/隔离后重建、>v2 fail-closed。

## 2. 独立审查（三轮）

- **REVIEW（task-12，runtime/session/state/store/shell）**：P2×2（workspacechanges 死代码、引号 mention 不可解析 token）→ 已修。
- **REVIEW-2（task-13，ACP/LSP/导入器/Wayland/跨平台）**：仅 P2×6（LSP rootURI、Wayland session 泄漏、ACP seam 契约、导入器回滚/symlink、LSP 解析放大）→ 已修（task-16）。
- **REVIEW-3（task-18，最后落地改动）**：P1×2 + P2×4（goal driver Idle 竞态（race flaky）、会话引用注入行显示为原始 JSON 气泡+幻影 turn、Candidates 未接线、turn_context 全局 map 泄漏、compact summary 角色语义、思考行每帧 O(n)）→ 已修（task-21/22/23）。

## 3. 门禁（最终）

```
（见 §6 最终结果；本文件在提交前由 Lead 更新）
```

## 4. TDD RED→GREEN

逐项 RED 命令/失败输出与 GREEN 结果见 [TDD-RED-EVIDENCE.md](TDD-RED-EVIDENCE.md) §4（本轮 13 项 + P2 修复）。摘要：

| 交付 | RED | GREEN |
| --- | --- | --- |
| 工作区 diff | workspacechanges `undefined: Turn`；session 无 Diff/Added；shell `unknown field Diff` | 四包 + shell 全绿；事件回放测试 |
| /goal + /compact | palette 无 /goal//compact、命令到达模型（4/1 provider calls）、runtime/session undefined | 六包全绿；命令 0 provider calls |
| compact 持久化 | store `ReplaceCompactedMessages undefined`、state `undefined: Compaction`、runtime 签名不符 | 六包全绿；重开/二次压缩不回退 |
| 会话引用 | 包 stub 全失败、注入无第二条消息/无持久化、shell `#live` 旧 token | sessionreference/session/runtime/shell 全绿；幂等/回放测试 |
| 崩溃恢复 | runtime/session 恢复测试 not implemented | runtime/session/app 定向全绿；二次恢复 no-op |
| ACP 完整面 | `./internal/acp` 编译失败（undefined Client/ListSessionsResponse） | acp/app 全绿；-race PASS |
| LSP | `internal/lsp` not implemented、tools 17 fail | lsp/tools/app 全绿；-race PASS |
| Wayland | `undefined: dbusMessage` | 57 pass + 1 skip；-race PASS |
| 导入器 | 14 FAIL（附件/manifest） | importer/cmd 全绿；真实 ~/.dsh dry-run/导入验证 |
| 思考行 | `-run Reasoning` 8 FAIL | Reasoning + shell 全包全绿 |
| P2 修复 | 各项 RED 见 task-16 报告 | 定向 + -race 全绿 |

## 5. 已知差异 / 未验证项（发布说明候选）

1. 会话引用 UI：composer 显示 canonical mention 明文（mygo TextArea 无内联芯片）；Chat 注入行以紧凑 disclosure 呈现（非 DSH 专用上下文渲染）。
2. 思考行：完成态仅「已思考」无 settled preview；shimmer 为透明度脉冲；展开正文 mono 纯文本（非 compact Markdown）。
3. `/compact` summary 以 assistant 行持久化（app storeHistory 只认 user/assistant；见 REVIEW-3 P2-3 的识别语义修复）。
4. LSP：每次调用冷启动语言服务器（无连接池）；无专属插件图标（fallback info）；未真机 gopls 验证。
5. Wayland 全局快捷键：真实 GNOME/KDE portal（同意对话框、Activated 投递、shortcut id 持久化）需真机验证；X11 回退/非 Wayland 不连 D-Bus 已测。
6. 崩溃恢复：坏 checkpoint 静默跳过；合成工具结果缺 Args 回落 "{}"；明确不做副作用重放。
7. 导入器：仍不导入工具调用/轨迹/usage（设计不变）；损坏 manifest 重建可能重导一次（备份恢复成功则不重导）；macOS plist 在本地非数字版本下回退 0.1.0。
8. 既有环境限制不变：无真机 Windows/macOS（IME/a11y/DPI/通知/安装包）；签名/公证与托管 update feed DEFERRED；wayland-only 真实 portal 未验。
9. 性能：思考行 preview 已缓存（task-23），长思考流式仍为整帧重绘框架限制。
10. go1.27 + sonic 依赖会在 stderr 打印 fallback 警告（第三方依赖，构建不受影响）。

## 6. 最终门禁与提交

- **代码提交**：`f204755`（feat: 1.0 functional closure — diff view, commands, references, ACP, LSP, Wayland, recovery, importer, reasoning UI，含全部实现/测试/打包 README）。
- **最终门禁**（同一内容树，提交前）：
  - `sh scripts/ci.sh` → **PASS**：gofmt/vet/build 全绿；`go test ./...` 全包绿；零 cgo 四目标交叉编译 PASS；app 工具可调用 PASS；coverage total **80.3%**；race 7 包含 `internal/session` 233.6s。
  - `CGO_ENABLED=1 go test -race -timeout 20m ./internal/shell` → **PASS**（528.1s）。
  - `GOOS=windows go build ./...`、`GOOS=darwin go build ./...` → **PASS**。
  - `go vet ./...`、`gofmt -l cmd internal` → 干净。
- **独立验证**：REVIEW（task-12）/REVIEW-2（task-13）均 PASS（仅 P2，已修）；REVIEW-3（task-18）报 P1×2 + P2×4，**全部修复并复验 PASS**（goal driver race 10/10 连续绿；引用注入折叠/候选排序/per-agent 队列；compact summary 识别；思考行 preview 缓存；packaging 文案）；PRE-GATE（task-17）GREEN；VERIFY-FIXES（task-24）PASS。
- **TDD**：每项先 RED 后 GREEN，证据见 [TDD-RED-EVIDENCE.md](TDD-RED-EVIDENCE.md) §4；无 `tdd_red` 标签残留。
- **提交后终验**：`sh scripts/check-clean-checkout.sh`（干净 HEAD 构建）与 `sh scripts/ci.sh --fast` 在 docs 提交后的 HEAD 上复跑，结果记录于最终提交信息/会话报告（若复跑失败以该结果为准）。
