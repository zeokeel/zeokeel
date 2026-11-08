# 反证审计报告（ADVERSARIAL-AUDIT）

- 日期：2026-11-08（Asia/Shanghai）· Go go1.27.3 linux/amd64 · 离线环境
- 任务：task-108「反证审计：已交付模块的负向/边界测试与缺陷报告」
- 范围：只新增 `internal/regression/**`（仅 `_test.go`，测试专用包）与本文件；**未修改任何生产代码**，未 commit。
- 方法：**反证法**。对每个已交付模块，不重复其正向单测，而是构造负向输入（空/非法/超长/恶意/并发/取消/panic）、故障注入（httptest 本地 server、坏文件、坏 JSON、失效 URL）与黑白盒断言；断言不变量（不 panic、不丢状态、不死锁、不越界、不误报）。真实缺陷用「已知缺陷探针」记录：缺陷存在时 `t.Skip` 并写明 AUDIT 编号，缺陷修复后同一用例自动转为通过断言。

## 修复状态（Lead 复核后）

> 首轮 11 个「已知缺陷探针」与第二轮 4 个 P3 均已随 TDD 修复转为 PASS；第三轮 P3-9/10 已修复、P3-11 重开后于 ca2d24c 真正修复；第四轮 P3-12 已修复（ca2d24c）；第五~七轮无新缺陷，第八轮发现 1 个 P3（AUDIT-P3-13）并已修复：诊断报告现在对 apiKey/token/secret/password/authorization 类设置整值隐藏，并对 JSON 结构化设置（mcp.servers）递归掩码 Bearer token 与凭证字段；新增单测断言 web.search.apiKey 与 mcp Bearer 不泄漏。第九轮发现 1 个 P3（AUDIT-P3-14：agent.permission=never 已存储但会话权限 chip 显示「询问」，待修）。第十轮无新缺陷；第十一轮无新缺陷。`go test ./internal/regression -count=1 -race` 当前 **241 PASS / 0 SKIP**（历史：首轮修复后 92，第二轮后 98，第三轮后 133，第四轮后 153，第五轮后 172，第六轮后 180，第七轮后 189，第八轮后 208，第九轮后 220，第十轮后 227）。

| ID | 优先级 | 状态 | 修复摘要 |
|----|--------|------|----------|
| AUDIT-P2-1 | P2 | ✅ 已修复 | sessionstore 新增 text/search_text 列（幂等迁移+回填），SearchMessages/事件搜索改为解码文本匹配，snippet 用纯文本 |
| AUDIT-P2-2 | P2 | ✅ 已修复 | importer WalkDir 跳过 symlink 并记 Note，真实会话照常导入 |
| AUDIT-P2-3 | P2 | ✅ 已修复 | update.Check(nil) → context.Background()，不再 panic |
| AUDIT-P2-4 | P2 | ✅ 已修复 | uiroot 四桥 handler 调用统一 runHandlerSafely（recover+日志）；Ask panic 清理 pending 并返回可读错误 |
| AUDIT-P3-4 | P3 | ✅ 已修复 | JSONL 导出错误只存裸 err，前缀渲染一次 |
| AUDIT-P3-2 | P3 | ✅ 已修复 | JSONL 导出前按 locale 设置 ExportJSONLDialogTitle |
| AUDIT-P3-1 | P3 | ✅ 已修复 | MCP URL 去重按小写（assembly 优先不变） |
| AUDIT-P3-3 | P3 | ✅ 已修复 | importer Scan 按 source session ID 去重计数 |
| AUDIT-P3-额外 | P3 | ✅ 已修复 | 上述修复均有先失败后通过的单测（RED→GREEN 记录见各任务摘要） |

## 验证命令与结果

```
$ go build ./...                                            OK
$ go vet ./...                                              OK
$ go test ./... -count=1                                    全部包 ok（含 internal/regression）
$ go test ./internal/regression -count=1                    ok  1.2s
$ go test ./internal/regression -count=1 -race              ok 12.8s（98 PASS / 0 SKIP）
```

测试规模：`internal/regression` 共 241 个顶层 Test（首轮 78 + 第二轮 20 + 第三轮 35 + 第四轮 20 + 第五轮 19 + 第六轮 8 + 第七轮 9 + 第八轮 17 + 特性提交补充 2 + 第九轮 12 + 第十轮 7 + 第十一轮 14；含子用例后更多；另有 `internal/ui/uimodtest` 的 E2E 1 个，不计入本包）。第三轮 P3-9/10 已修复，P3-11 重开后已真正修复，第四轮 P3-12 已修复，第五~七轮无新缺陷，第八轮 P3-13 已修复（da654e3），第九轮新增 P3-14（待修），第十轮、第十一轮无新缺陷；当前 **241 PASS / 0 SKIP**。

## 用例矩阵

| # | 模块 | 反证点 | 用例数 | 结果 |
|---|------|--------|-------|------|
| 1 | approvals | Resolve 未知/空 ID、非法 decision 不改队列；重复 Resolve 首答生效；取消中的 Request 清队列且 ID 变未知；已取消 ctx 的 Request 不泄漏；订阅回调 panic 被遏制；Mode never/auto 不排队；非法 Mode 拒绝激活；并发 Request+Resolve 每人恰一答、无死锁（-race） | 7 | PASS |
| 2 | sessionstore / store | LIKE 通配符 `%` `_` 按字面匹配；SQL 片段注入不改结果集；空/空白 query 不返回全量；limit 0/负数→默认 50、超大→硬上限 200、1→1；并发 CreateSession/ReplaceHistory/SearchMessages（-race）；WAL 重开数据保持；重复 Open/迁移幂等、双连接互见；未知/空白 session ID 的读写语义；64KiB 标题+1.3MiB 消息往返且 snippet 有界；事件日志 LIKE/逆向 range | 10 | PASS + **AUDIT-P2-1**（3 个 skip 探针） |
| 3 | importer | 目录不存在/是文件/sessions 是文件；坏 JSON/空文件/版本 3/`.zstd` 全部跳过且不中断，好会话仍导入；2MiB 单行与 3MiB 垃圾行不崩；重复 Import 幂等、新增会话只导新增；路径穿越 symlink；取消 ctx；默认 sourceDir；cwd 工作区注册；import 事件；配置 NUL 拒绝；重复 DSH session ID | 11 | PASS + **AUDIT-P2-2**（1 个 skip）+ **AUDIT-P3-3**（log 记录） |
| 4 | websearch / update | 非法 endpoint（空/相对/ftp/无 host/file）；空 query 与 nil Searcher；count 0/超大 clamp 与服务端实收值；非 2xx（长错误体 snippet 有界）/204/坏 JSON/错误类型/null results；1MiB 响应边界（超限拒绝、恰近限接受）；取消与客户端超时；请求头/体形状（Bearer 仅配置时、trim）；update 配置校验（负超时/NUL）、disabled、http/ftp 拒绝、500/坏 JSON/缺版本/坏版本/超大 manifest/404、无匹配 asset、https TLS 不信任、取消 ctx、版本比较与可用性 | 16 | PASS + **AUDIT-P2-3**（1 个 skip） |
| 5 | uiroot 交互桥 + ui/interaction | Ask 无 handler 快速失败；未知/重复 Cancel/Answer 均为 no-op；同步 Answer；已取消/进行中取消；Answer+Cancel 竞争恰一结果（20 轮 -race）；8 并发问题 ID 唯一且各答各问；Artifact 空 title/body、无 handler、取消 ctx、并发唯一 ID；Plan 空内容、非法 action、无计划 approve/cancel、status 不改状态、二次 approve 拒绝；Goal 空文本、重复 Clear、nil ctx、并发 Set/Clear/Get；handler panic 遏制探针 ×4；UI 帧：无桥 no-op、空队列、空 artifact 回退文案、空 question 可取消、Escape 取消、FIFO、非法/空 plan 状态忽略、Exit 失败错误可见且对话框不关、卸载时 Cancel question+Exit cancel plan | 20 | PASS + **AUDIT-P2-4**（4 个 skip） |
| 6 | 导出 JSONL + 附件 | 引号/反斜杠/换行/NUL/HTML/emoji 文本、空消息、20 万 rune reasoning、tool/reasoning/error 扁平化——逐行 `json.Unmarshal` 校验；空会话仅 1 行 header；取消保存无状态；保存失败可见；附件恰好 10MiB 接受、10MiB+1 拒绝且不显示 chip、目录/不存在路径拒绝、后续合法 drop 清除旧错误；目标空文本 chip 不渲染 | 7 | PASS + **AUDIT-P3-4**、**AUDIT-P3-2**（2 个 skip，其余断言仍执行） |
| 7 | MCP settings 合并 | 坏 JSON/NUL 行/空 name/url/非数组 JSON：激活不失败、全部跳过并记日志；assembly URL 冲突时 assembly 胜出且顺序在前；用户重复 URL 首条胜出；空 settings/无 settings 服务；缺 URL 保留失败条目（可见诊断）；Refresh(nil/取消 ctx)；URL 大小写不敏感去重缺口 | 7 | PASS + **AUDIT-P3-1**（log 记录） |

合计 78 个顶层 Test；失败（P0/P1）0 个。

## 发现的问题

> 未发现 P0（数据损坏/必现崩溃/安全问题可远程触发）与 P1（正常产品路径崩溃/丢数据）。
> 4 个 P2，4 个 P3（ID 为测试内稳定编号；优先级列独立标注）。

### AUDIT-P2-1（P2）会话搜索按原始 JSON 载荷匹配：引号/反斜杠文本搜不到，结构引号搜全部

- 位置：`internal/modules/sessionstore/search.go:59`（`CAST(m.payload AS TEXT) LIKE ...`）、`internal/modules/sessionstore/store.go:750` 事件搜索同源；契约 `internal/contracts/search.go:22-29` 承诺 “finds messages whose text contains query”。
- 复现：`go test ./internal/regression -run 'TestStoreSearchMessagesFindsQuotedText|TestStoreSearchMessagesQuoteDoesNotMatchEverything|TestStoreEventSearchFindsQuotedPayloadText' -v` → 三例均命中缺陷而 skip。
  - 写入消息 `the "quoted" answer` 后搜 `"quoted"` → **0 命中**（payload 里是 `\"quoted\"`，用户须输入 JSON 转义形式）。
  - 写入 `path C:\tmp\file` 后搜该字面量 → 0 命中（payload 里 `\\`）。
  - 任意消息 payload 都含结构引号，搜 `"` → **命中全部消息**（假阳性）；反斜杠查询会命中所有含引号/换行/非 ASCII（`\uXXXX` 转义）的消息。
  - 事件日志 `Store.Search` 同样按原始 payload 匹配。
- 影响：用户搜索含引号/反斜杠的代码、路径、引用文本时静默失联；搜 `"` 时全表命中，结果噪声。危害功能正确性，非崩溃/数据损坏。
- 建议（供 Lead 修复）：`WHERE` 改为对解码后的文本（或其去转义版本）建匹配列/使用 FTS，或在读取路径过滤：先按 LIKE 粗筛 `session_id`，再在 Go 里对解码 `rig.Message.Text()` 精确匹配并回填 snippet；事件 Search 对 `Payload` 解码后匹配 JSON 字符串值。

### AUDIT-P2-2（P2）importer 跟随 sessions 内 symlink 读出 DSH home 之外的文件

- 位置：`internal/modules/importer/importer.go:177`（`filepath.WalkDir` 不解析符号链接，但 `:198 parseSessionFile(path)` 会 `os.Open` 跟随）；攻击点是 `sessions/**/session.v4.jsonl` 为 symlink。
- 复现：`go test ./internal/regression -run TestImporterSymlinkEscape -v` → 在 DSH home 的 sessions 内建 symlink 指向 home 外部的合法 session.v4.jsonl，`preview.Sessions == 1`（缺陷存在时 skip；安全不变量应为 0）。
- 影响：本地数据导入边界被绕过——只要攻击者可写 DSH data home 的 sessions 目录，即可让导入器读取 home 之外、格式恰好为 v4 JSONL 的文件内容并入档。属本地路径穿越读（P2；非远程可利用，不写文件）。
- 建议：WalkDir 回调对 `entry.Type()&fs.ModeSymlink != 0` 直接跳过并记 note；或对 `filepath.EvalSymlinks(path)` 断言仍在 `sessionsRoot` 之下。

### AUDIT-P2-3（P2）update.Check(nil) 空指针 panic；其他宿主服务把 nil ctx 当 Background

- 位置：`internal/modules/update/update.go:147-149`（`Check(ctx)` 直接 `ctx.Err()`，无 nil 保护）。
- 复现：`go test ./internal/regression -run TestUpdateCheckNilContext -v` → `panic: runtime error: invalid memory address or nil pointer dereference`（缺陷存在时 skip）。
- 影响：调用方（工具/测试/未来 CLI）传 nil ctx 时 panic 而非可读错误；同仓 approvals/interaction/artifact/plan/goal 服务都显式 `if ctx == nil { ctx = context.Background() }`，行为不一致。
- 建议：`Check` 开头加 nil→Background（或返回 `errors.New("ctx is required")` 的可读错误），并同步 websearch 之类已带保护的调用风格。

### AUDIT-P2-4（P2）uiroot 交互桥的 handler panic 未被遏制，且泄漏 pending 状态

- 位置：`internal/uiroot/interaction.go:82`（Ask 同步调 handler）、`internal/uiroot/artifacts.go:57`（Present）、`internal/uiroot/plan.go:61,94`（Review/Exit）、`internal/uiroot/goal.go:60`（Set）；作为对照，`internal/modules/approvals/approvals.go:365 notify` 明确 recover 并记录。
- 复现：`go test ./internal/regression -run 'TestInteractionHandlerPanicIsNotContained|TestPlanReviewerHandlerPanic|TestGoalTrackerHandlerPanic|TestArtifactPresenterNegatives' -v` → panic 从 Ask/Present/Review/Set 传播到调用者（4 个 skip 探针）。
- 影响：UI 模块 handler 的 bug（或 handler 内错误）会跨模块边界击穿 agent 工具调用协程；interaction Ask 场景下 pending 条目还会残留在 map（无 UI 可再关闭它）。与「宿主遏制模块 panic」的既有约定（frame.safeBuild、approvals.notify）不一致。
- 建议：四个桥在调用 handler 处套 `runSafelyErr` 式 recover：Ask 记录日志并返回可读错误且清理 pending；Present/Review/Exit/Set 记录日志后继续返回确定状态（Present 仍返回 id）。uiroot 包内已有 `safeBuild` 的 recover 工具可复用。

### AUDIT-P3-4（P3，文案）JSONL 导出失败时 `导出失败：` 前缀重复

- 位置：`internal/ui/session/view.go:129,135`（写 `st.exportErr = "导出失败：" + err`）与 `view.go:110`（渲染时再拼 `st.tr("导出失败：")`）；对照 `view.go:179` Markdown 路径只存裸 `err.Error()`。
- 复现：`go test ./internal/regression -run TestSessionExportJSONLCancelAndError -v` → 用户看到 `"导出失败：导出失败：disk full"`（缺陷存在时 skip）。
- 影响：纯文案重复，功能正常。
- 建议：JSONL 路径与 Markdown 路径一致：只存 `err.Error()`，前缀只在渲染处拼一次。

### AUDIT-P3-2（P3，i18n）直接导出 JSONL 时保存对话框标题停留中文

- 位置：`internal/ui/session/export.go:21,27`（`ExportJSONLDialogTitle` 默认中文）+ `internal/ui/session/view.go:173-174`（只有 `exportSession`/Markdown 路径按帧语言重设两个标题；`exportSessionJSONL` 路径不设置）。
- 复现：`go test ./internal/regression -run TestSessionExportJSONLDialogTitleFollowsLocale -v` → en 界面直接「Export JSONL」，标题仍为 `导出会话`（缺陷存在时 skip）。
- 影响：英文界面下 JSONL 直接导出时原生保存对话框标题未本地化（若先导出过一次 Markdown 则被顺带修正）。
- 建议：`exportSessionJSONL` 内先 `ExportJSONLDialogTitle = st.tr("导出会话", "Export conversation")`（与 exportSession 对称）。

### AUDIT-P3-1（P3，去重口径）MCP URL 去重区分大小写

- 位置：`internal/modules/toolkit/mcp_settings.go:41,51,55`（`seen[url]` 用 trim 后原始字符串做 key）。
- 复现：`go test ./internal/regression -run TestMCPMergeURLCaseSensitivity -v`（log 记录）→ assembly `http://host/x` + settings `HTTP://host/x` 被判为不同 URL，同一端点被注册两次（第二个是坏连接）。
- 影响：用户配置同端点但大小写不同 scheme/host 时出现重复插件条目；非安全问题。
- 建议：key 用 `url.Parse` 后的规范化（scheme/host 小写、去默认端口、去尾 `/`），解析失败则退回原串。

### AUDIT-P3-3（P3，预览口径）重复 DSH session ID 时 Scan 预览计数偏大

- 位置：`internal/modules/importer/importer.go:219`（`preview.Sessions++` 对每个可解析文件计数）与 `:273`（Import 时才按 `existing` map 去重跳过）。
- 复现：`go test ./internal/regression -run TestImporterDuplicateSessionIDs -v`（log 记录）→ 两个目录携带同一 DSH session ID：`preview.Sessions=2`，实际 `ImportedSessions=1, Skipped=1`。
- 影响：导入预览显示“将导入 2 个会话”，实际只导入 1 个；无数据错误。
- 建议：scan 阶段按 `dsh-<id>` 去重（后出现的计 Skipped/Note），或 preview 明确标注“含重复源 ID”。

## 关键不变量：未被证伪的通过项

以下反证用例断言的不变量**全部成立**（PASS）：

1. **approvals 队列完整性**：未知/空 ID、非法 decision、重复 Resolve 均不改变 pending 集合与已交付答案；取消中的 Request 恰好清理自己的条目；订阅回调 panic 被遏制且不阻断后续订阅者；并发 Resolve 每个请求恰好一个答案、队列最终清空、无死锁（-race）。
2. **store 数据安全与边界**：LIKE 元字符按字面匹配（无注入放大）；空 query 不返回全量；limit 三段边界（默认/负/超大）与硬上限 200 精确；未知/空白 session ID 读为空、写为可读错误；超长标题/消息完整往返且搜索 snippet 有界；WAL 重开后数据保持、重复打开/迁移幂等、双连接互见；并发读写搜索 -race 干净。
3. **importer 容错与幂等**：坏 JSON/空/版本不符/`.zstd`/超大行均跳过并出 note，其余会话照常导入（部分失败不中断）；重复 Import 与新增会话均幂等；取消 ctx 立即返回；缺失/非目录路径可读报错；import 事件与工作区注册正确；配置 NUL 拒绝。
4. **websearch / update 网络边界**：非法 URL/配置在发请求前拒绝；非 2xx/坏 JSON/超大响应分类可读；1MiB 边界严格（恰好低于接受、超出拒绝）；query trim、Bearer 仅在配置时发送、count clamp 后服务端实收值正确；取消/客户端超时均不挂起；update 的 disabled/https-only/500/坏 manifest/无 asset 分类正确；版本比较边界正确。
5. **交互桥与 UI 帧健壮性**：nil handler/未知 ID/重复 Cancel/空 payload/非法 plan action 均不 panic 不泄露状态；Answer/Cancel 竞争恰一结果；plan 状态机不因非法输入迁移；UI 帧在空队列、空 artifact（回退文案）、空 question、Escape、未知 plan 状态、Exit 失败、模块卸载等场景均不 panic，且卸载会 Cancel pending question / Exit reviewing plan（agent 工具不会悬挂）。
6. **导出与附件**：含引号/反斜杠/换行/NUL/HTML/emoji/空消息/20 万 rune reasoning 的会话导出后**逐行 JSON 合法**且字段往返；空会话仅 header 行；附件 10MiB 边界精确（恰好接受、+1 拒绝、拒绝不留 chip）；坏路径拒绝且不污染后续合法 drop。
7. **MCP 设置合并**：坏 JSON/NUL/空行/非数组不失败激活且有日志；assembly URL 冲突时 assembly 胜出且顺序在前；用户重复 URL 首条胜出；缺 settings 服务时保持 assembly 集合；缺 URL 服务器保留可见失败条目；Refresh(nil/取消) 安全。

## 未覆盖项与局限

- update 的 **asset https-only 默认**无法纯黑盒验证：需要可信 TLS feed；本地 `httptest.NewTLSServer` 不被默认 client 信任，因此只验证了「http feed 默认拒绝」「insecure 开关语义」与 transport 拒绝路径（`TestUpdateSecureDefaults` 注释说明）。
- MCP 的**成功握手/工具列表**未覆盖：本审计用不可达 URL 只打失败路径与合并/去重读模型；真实 Streamable HTTP 会话属集成测试范围。
- update 的下载/SHA256/安装阶段不在 `internal/modules/update`（仅 check phase），未覆盖。
- importer 的 `.zstd` 仅验证拒绝/跳过路径（模块按设计不支持解压）。
- websearch 的 10s 默认超时未真实等待（用 ctx 取消与 25ms 客户端超时验证同一边界行为，避免慢测试）。
- 会话 composer 的**附件发送**链路（`AttachmentSender`）未打全链路；审计只覆盖 FileDrop→staging 的 10MiB 边界与错误展示。
- 并发约束按产品架构：UI 帧为单 UI 线程；跨 goroutine 只验证服务/桥的并发安全（-race），不并发跑两个 mygo Tester。
- 11 个已知缺陷用例以 SKIP 记录（修复后自动转 PASS）；因此 CI 绿不代表缺陷已修，需按上方编号跟踪。

## 结论

- **P0/P1 未被证伪**：未发现必现崩溃、数据损坏或远程可利用的问题；核心不变量（审批队列完整性、存储并发与迁移、导入幂等与容错、HTTP 故障分类、客户端/UI 空数据不 panic、模块卸载清理）均通过反证。
- **被证伪的断言集中在三处语义边界**：搜索的「文本」语义（AUDIT-P2-1）、导入的「源树边界」语义（AUDIT-P2-2），以及宿主对模块回调的「panic 遏制」约定在 uiroot 四桥上的缺失（AUDIT-P2-4）；另有 update 的空 ctx panic（P2-3）与 4 个 P3 文案/口径问题。建议按 ID 修复后，删除对应探针的 SKIP 分支即可获得回归锁。
- 审计测试可独立运行：`go test ./internal/regression/... -count=1 -race`（当前全绿，11 skip 即缺陷台账）。

## 新链路缺陷修复（Lead 复核，2026-11-08）

| ID | 状态 | 修复摘要 |
|----|------|----------|
| AUDIT-P3-5 | ✅ 已修复 | TeamService.Wait 在 manager.Wait 后优先返回 Manager.Last 的最新答案（后续 SendAsync 不再返回首次结果） |
| AUDIT-P3-6 | ✅ 已修复 | ParseSessionReferences 按分隔符集解析（补齐 ] } " ' > 」），尾点裁剪、内嵌 `team/a.b` 保留 |
| AUDIT-P3-7 | ✅ 已修复 | 引用前缀无 token 边界问题：`,`/`;` 等分隔符截断，`@session:abc,@session:def` 正确解析两个 ID |
| AUDIT-P3-8 | ✅ 已修复 | settings `agent.permission="never"` 映射到 ModeNever（拒绝而非排队）；恶意/未知值仍回退 ask |

回归套件复跑：`internal/regression` **98 PASS / 0 SKIP**（新增 20 个反证用例覆盖 team/@session/effort/permission）。

## 反证扩展：新链路（2026-11-08 第二轮）

同一包内追加的负向/边界测试，覆盖新交付的 4 条链路；仍只写测试与本报告，未改生产代码、未 commit。

```
$ go test ./internal/regression -count=1 -race              ok  13.5s（98 顶层 Test；11 SKIP 为缺陷台账）
$ go build ./... / go vet ./internal/regression              OK
```

| # | 链路 | 反证点 | 新增用例 | 结果 |
|---|------|--------|---------|------|
| 8 | team 编排（contracts.TeamService over rig team.Manager） | 未知/空成员 ID 上 SendAsync/Send/Wait/Interrupt/Close；空 name/prompt；nil ctx；6 并发 Spawn（唯一 ID、各自答案，-race）；provider 400 的 spawn 后错误从 Wait 传播且成员可 Close；Interrupt 中断在飞行中的 run；kernel dispose 触发 CloseAll 并回收团队工具（保留调用方预置工具）；工具注册去重：预置 `spawn_teammate`/`team_spawn` 后 agent 记日志跳过而非失败，`subagent`/`subagent_fork` 各只注册一次 | 7 | PASS（发现 AUDIT-P3-5） |
| 9 | @session 引用 | ParseSessionReferences：空/纯文本/仅前缀/前缀+空格/尾随标点/括号/CJK 标点/重复折叠/首次出现顺序/大写与相似词不匹配/内嵌标点保留/10 万 rune token/Format↔Parse 往返；尾随 `] } " ' > 」` 未裁剪、无 token 边界（两块文档化缺口）；E2E：引用块注入模型输入且截断 ≤8000 rune（含省略号/结束标记），自引用/未知/重复不注入，read model+SQLite 历史只保留原始 prompt | 4 | PASS（发现 AUDIT-P3-6/7） |
| 10 | reasoning effort | 非法 module config→请求体省略 reasoning_effort；非法 per-session setting→省略；无配置无 setting→省略；config low/medium/high/大小写空格→请求体顶层字段精确；session setting 覆盖 config（含 trim/大小写）；其他 session 不继承覆盖（setting 作用域） | 4 | PASS |
| 11 | permission（settings agent.permission） | workspace 模式仅 read_file/list_dir/glob/grep 自动 allow（不排队/不通知），write/edit/bash/terminal/web_*/MCP/空名与大写仍排队；ask/workspace/full/auto/大小写/空值回退映射；恶意/未知值（`root`、`full;drop table`、`never\n`、NUL、4096 rune）回退 ask；pending 期间切换模式不扰动在途请求、新请求按新模式；settings 写入 300 次 + 60 次请求/解析并发（-race，无死锁，队列清空）；无 settings 服务时以 assembly mode 为准 | 5 | PASS（发现 AUDIT-P3-8） |

新增缺陷（均只记录，未改生产代码；仍无 P0/P1）：

### AUDIT-P3-5（P3，契约措辞/语义）TeamService.Wait 在后续 Send/SendAsync 之后返回首次运行结果

- 位置：`internal/contracts/team.go:37`（“Wait blocks until the teammate finishes its current run”）vs 适配器 `internal/modules/agent/team.go:206` → rig `team.Manager.Wait` → rig `agent/subagent.go:411-421`（`<-firstDone` 后返回 `firstResult`，后续 run 不改写）。
- 复现：`go test ./internal/regression -run TestTeamSpawnWaitSendAndMembers -v`（log 记录）→ spawn+Wait→SendAsync 跟进→`Wait` 仍返回首次答案，最新答案只在 `Members().Last`。
- 影响：团队工具/UI 若用 Wait 读“当前运行结果”，在持续对话后会拿到过期答案；无数据损坏。建议契约措辞改为“首次/当前在飞 run，后续轮次用 Send+Last”，或适配器改为轮询成员 Last。
- 无独立失败断言（测试已按现状断言并记录）。

### AUDIT-P3-6（P3，解析器）@session 引用尾随标点裁剪集不全

- 位置：`internal/contracts/sessionref.go:28`（`TrimRight` 集合 `.,;:!?，。；：！？)`）。
- 复现：`go test ./internal/regression -run TestParseSessionReferencesMissedTrailingPunctuation -v`（log）→ `@session:abc]`、`...}`、`..."`、`...'`、`...>`、`...」` 均返回带标点的 ID，随后查不到会话被当未知引用跳过（Markdown 链接/引号粘贴场景失效）。
- 建议：裁剪集补 `] } > " ' 」 ” ’`（或用 unicode.IsPunct 从尾部剥离非 ID 字符）。

### AUDIT-P3-7（P3，解析器）前缀无 token 边界，畸形文本产生拼接伪 ID

- 位置：`internal/contracts/sessionref.go:23`（`strings.Index(field, prefix)` 允许 token 中间匹配）。
- 复现：`go test ./internal/regression -run TestParseSessionReferencesNoTokenBoundary -v`（log）→ `@session:abc,@session:def` → `["abc,@session:def"]`；`@session:a@session:b` → `["a@session:b"]`；`mail@session:abc` → `["abc"]`。
- 影响：多引用紧邻（缺空格）时只解析出一个伪 ID，其余静默丢失；被当作未知引用跳过，无崩溃。建议按空白 token 后要求前缀位于 token 起点（或 field==prefix+id 完全匹配）。

### AUDIT-P3-8（P3，权限语义）settings `agent.permission=never` 未被 effectiveMode 映射，退化为 ask

- 位置：`internal/modules/approvals/approvals.go:198-206`（switch 仅映射 ask/workspace/full(auto)，无 never 分支）。
- 复现：`go test ./internal/regression -run TestPermissionSettingModes -v`（log）→ 设置 `"never"`/`"NEVER"` 时 `Mode()` 返回 `ask`，Request 排队而非立即 deny；never 只能在 assembly Config 中生效。
- 影响：把 never 写进 settings 的自动化/宿主期望“全部拒绝、不阻塞”，实际会挂起等待人工审批；属语义不一致（当前唯一方式是 assembly 配置）。建议显式映射 never→ModeNever（或让 settings 只接受 ask/workspace/full 并在写入/读取处给日志）。

### 回归复核：首轮缺陷已全部修复

- 首轮 8 个缺陷（P2-1..4、P3-1..4）已随提交 `e9ae184`（"adversarial-audit fixes"）修复：搜索改为解码文本列、importer 跳过 symlink、update nil ctx、uiroot 四桥 panic 遏制、JSONL 前缀/标题、MCP URL 小写去重、importer preview 去重计数。
- 本次复跑 `go test ./internal/regression -count=1 -race`：**98 PASS / 0 SKIP**——所有遗留探针按设计自动转为断言并通过，无需再跳过。
- 本小节新链路的 4 个 P3（AUDIT-P3-5..8）在后续提交 `024b84f`（“P3 fixes from round-2 audit”）中全部修复：TeamService.Wait 优先 Manager.Last、ParseSessionReferences 改为分隔符解析、settings permission never 映射 ModeNever；回归用例同步改为断言修复后的行为（见该提交的修复表）。

### 新链路未被证伪的通过项

1. **team**：未知/空 ID 与空 name/prompt 全部可读报错且不 panic；并发 Spawn 无重复 ID/丢成员（-race）；provider 失败的成员仍可列出并关闭；Interrupt 使成员变 interrupted 且 Wait 报中断；kernel dispose 关闭所有成员并反注册团队工具（调用方预置工具保留）；工具注册冲突走“日志+跳过”而非激活失败，`subagent` 名字始终唯一。
2. **@session**：解析器对空/重复/顺序/大小写/相似词/CJK 标点/超大 token 行为正确；注入只进模型输入，read model 与 SQLite 持久化保留原始 prompt（自引用/未知/重复均不注入，未知有日志）；8000 rune 上下文上限严格且带截断标记。
3. **reasoning effort**：settings > config > 空；非法值（config 与 session）都省略字段；大小写/空白归一；字段确实出现在 httptest 请求体顶层 `reasoning_effort`；覆盖按 session 隔离不串。
4. **permission**：workspace 只读白名单精确、写/网络/未知工具仍排队且不通知；full/auto 立即 allow，ask 排队，未知/恶意值回退 ask；切换模式不影响在途请求；并发写设置+请求无死锁/无竞态（-race）；无 settings 时 assembly mode 生效。

### 新链路未覆盖/局限

- team：未打满 Manager 成员上限（`ErrTeamFull`）与 runner 并发上限；Interrupt 与 Send 的细粒度竞争只覆盖“在飞行 run”；agent 模块成功 spawn 依赖本地 httptest OpenAI 兼容服务（SSE），未接真实 provider。
- @session：8000 rune 上限用单条引用验证；多引用共享预算与“剩余不足 footer”分支未逐一压测；每会话 20 条消息截取只做间接覆盖。
- reasoning effort：只验证 `Extra`/请求体路径；未验证非 openai provider 的等价字段（rig 内其他 provider 不在本仓）。
- permission：workspace 白名单是代码常量 4 个工具，与 toolkit 实际注册名的交叉核对未做（若未来改名需同步）；never 的 settings 语义见 AUDIT-P3-8。

## 反证审计第三轮：install-on-restart / toast / workspace mcp.json / references 补充（2026-11-08）

只新增/修改 `internal/regression/**` 与本文件；未改生产代码、未 commit。新增 35 个顶层 Test（包内 98 → 133）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 18.0s（133 PASS / 0 SKIP）
```

| # | 链路 | 反证点 | 新增用例 | 结果 |
|---|------|--------|---------|------|
| 12 | install-on-restart（模块半） | ScheduleInstall 成功/重排替换旧 plan、pending.json 字段与 PendingInstall 往返；非法 plan（空版本/空 sha/文件缺失/越界绝对路径/相对路径/目录/校验和不匹配）不写 plan 且 ErrInstall；被篡改的 pending.json（坏 JSON/缺字段/traversal/绝对外部路径）PendingInstall=false；8 并发调度（-race）原子写、无临时文件泄漏；symlink 绕过包含检查；无 dataDir → ErrNoDataDir | 6 | PASS（发现 AUDIT-P3-9） |
| 13 | install-on-restart（宿主半） | fake replacer：成功替换后清理 plan+staged；替换失败保留 plan+staged 且重试成功；traversal/绝对外部路径/坏 JSON/缺字段/staged 缺失/目录/CurrentExecutable 失败/nil replacer/空 dataDir 全部不调用 Replace 且保留 plan；无 plan 时无操作；staged 被篡改后宿主不重校验 sha（AUDIT-P3-10） | 5 | PASS（发现 AUDIT-P3-10） |
| 14 | toast 宿主桥 + 模块帧 | Notifier：空文本有 ID 不投递、Dismiss 幂等（重复/未知/空 ID）、handler panic 遏制、nil handler、20 并发 Notify/Dismiss（-race，唯一 ID）；toasts 帧：6 条只显示最新 4 条（4 个关闭按钮）、关闭按钮移除对应 toast 并 Dismiss 对应 ID、真实 4s TTL 自动消失（auto-expiry 未 Dismiss → AUDIT-P3-11）、空文本不渲染、无 notifier 时 no-op 不注册槽位、dispose 清 handler+清栈、真实 uiroot.Notifier 端到端 | 11 | PASS（发现 AUDIT-P3-11） |
| 15 | session 导出 Notify 参数 | 成功：Markdown/JSONL、zh/en 各恰一条 success toast 且文案精确（对话已导出 / Conversation exported）；失败：恰一条 error toast，含底层错误且「导出失败：」只出现一次；取消保存不 toast；无 notifier 时导出仍成功且不 panic | 3 | PASS |
| 16 | workspace mcp.json opt-in | opt-in 归一：仅 "true"/"TRUE"/" true " 开启，false/0/1/yes/on/garbage/空白关闭；对象 {"servers":[]} 与顶层数组两种形状；坏 JSON/含 NUL 行/缺 URL 行记日志跳过、读错误不失败激活；优先级 assembly > settings > workspace 且 URL 大小写归一（文件内重复也去重）；stdio 按 command+args 去重；无 workspace 服务/无 current/空 root/current 报错均安全；opt-in 关闭时即使文件存在也不读 | 8 | PASS |
| 17 | @session references 补充 | session_refs 事件恰好一条且 ids 只含真正展开的引用（自引用/未知/重复被过滤），无引用 prompt 不记事件；引用块仅注入被引用会话最后 20 条消息（26 条会话中最早 3 轮不出现、块未触发 8000 截断），且持久化仍为原始 prompt | 2 | PASS |

### 第三轮新增缺陷（均只记录，未改生产代码；无 P0/P1/P2）

#### AUDIT-P3-9（P3，边界）ScheduleInstall 的路径包含检查是词法判断，updates 内 symlink 可指向外部文件

- 位置：`internal/modules/update/install.go:45`（ScheduleInstall）→ `:58 os.Stat(staged)`（跟随 symlink）与 `:150 stagedPathBelowUpdates`（仅 filepath.Abs/Rel 词法包含）；宿主同构：`internal/host/install.go:137 applyPendingUpdate` → `:191 stagingPathBelowUpdates`。
- 复现：`go test ./internal/regression -run TestInstallSymlinkStagedPath -v` → 在 `<dataDir>/updates/linked-asset` 建 symlink 指向 dataDir 外文件，等值 SHA 的 ScheduleInstall 成功并记录该路径（log 记录）。
- 影响：数据目录归用户所有，攻击者已可写 pending.json/staged 文件，因此无权限提升；但“staged 必须位于 updates 下”的不变量被绕过，宿主随后会把外部文件内容当作更新覆盖可执行文件。建议 `filepath.EvalSymlinks` 后再次做包含检查（并选择拒绝 symlink）。

#### AUDIT-P3-10（P3，完整性）宿主 applyPendingUpdate 不重校验 pending.json 的 sha256

- 位置：`internal/host/install.go:33 pendingInstallFile.SHA256`（解码但全程未使用）与 `:137 applyPendingUpdate`→`:176 replacer.Replace(staged)`；模块侧 `internal/modules/update/install.go` 在 schedule 时校验一次。
- 复现：`go test ./internal/regression -run TestHostApplyDoesNotReverifyChecksum -v` → schedule 后篡改 staged 文件内容，宿主仍 Replace 成功（log 记录）。
- 影响：下载校验与安装之间（或 pending.json 被改后）文件可被替换而无检测；仍是本地用户权限范围，属纵深防御缺口。建议宿主在 Replace 前对 staged 重新计算 SHA-256 并与 plan 比对，不匹配则保留 plan 报错。

#### AUDIT-P3-11（P3，宿主桥簿记）toast 自动过期不调用 Notifier.Dismiss，宿主桥 active 表条目滞留

- 位置：`internal/ui/toasts/toasts.go:109 enqueue`（TTL goroutine）→ `:130 removeToast`（只删模块栈，不 Dismiss）；对照关闭按钮 `:184 st.notifier.Dismiss(notification.ID)`；`internal/uiroot/notifier.go:51` Notify 把条目写入 `active` map，只有 `:66 Dismiss` 删除。
- 复现：`go test ./internal/regression -run TestToastsTTLExpiry -v` → TTL 后 toast 消失但 Dismiss 调用数为 0（log 记录）。
- 影响：长时间运行且通知频繁的桌面会话中，`active` map 每次通知增长一条（元素很小、无读者），属缓慢内存滞留；不影响 UI/功能。建议自动过期路径也调用 `Notifier.Dismiss(notification.ID)`，或 notifier 的 active 表仅作为 handler 未装时的 pending 缓存并在投递后清除。

### 第三轮未被证伪的通过项

1. **install-on-restart 模块**：合法调度原子写 plan 并可替换；七类非法调度全部拒绝且不产生残留 plan；被篡改/不完整的 pending.json 安全降级为“无安装”；8 并发调度无竞态、无临时文件泄漏、最终 plan 完整；无 dataDir 给可读错误。
2. **install-on-restart 宿主**：fake replacer 下成功清理 plan+staged、失败保留并可重试；traversal/坏 JSON/缺 staged/目录/CurrentExecutable 失败等所有恶意 plan 都不会调用 Replace；无 plan 时完全 no-op。
3. **toast**：宿主桥对空文本/未知 Dismiss/handler panic/nil handler/并发均安全；模块帧严格 4 条上限、关闭按钮精确移除并通知宿主、真实 TTL 生效、空 payload 不渲染、无 notifier 时优雅降级、dispose 清理干净、真实桥端到端可达。
4. **session 导出 toast**：成功/失败各恰一条、level 与中英文文案精确、失败文案无重复前缀、取消不通知、无 notifier 不失败。
5. **workspace mcp.json**：opt-in 仅显式 true 生效；两种文件形状；任何坏文件/坏行都只记日志不失败激活；三源优先级与 URL/stdio 去重正确；workspace 缺失或异常安全；opt-out 完全不读文件。
6. **references 补充**：`session_refs` 事件只记真正展开的 ID；无引用不记事件；仅注入尾部 20 条且 8000 预算下不误截断；持久化仍是原始 prompt。

### 第三轮局限

- 宿主 `applyPendingUpdate` 未导出，审计在**测试侧**用 `//go:linkname`（见 `internal/regression/host_install_test.go` 头注释）调用它以遵守“只改 internal/regression”的约束；若宿主重命名/内联需同步调整。未覆盖默认 `defaultExecutableReplacer` 的真实替换（测试用 fake，绝不替换测试二进制）。
- toasts 的真实 TTL 用生产 4s 等待（`toastTTL` 未导出，模块内测试才能覆写）；TTL 测试单独约 4s。
- workspace mcp.json 未测试 symlink 文件、成功 MCP 握手（死 URL 只覆盖失败条目与合并顺序）；opt-in 仅启动时读取（重启生效）符合文档。
- 第三轮无 P0/P1/P2 新发现；AUDIT-P3-9/10 属本地纵深防御，P3-11 属桥簿记滞留。

## 第三轮缺陷修复（Lead 复核，2026-11-08）

| ID | 状态 | 修复摘要 |
|----|------|----------|
| AUDIT-P3-9 | ✅ 已修复 | update/host 的 staged 路径包含检查在词法判断后再 `EvalSymlinks` 双侧复检（缺失文件交由宿主校验；指向 dataDir 外的 symlink 拒绝） |
| AUDIT-P3-10 | ✅ 已修复 | host `applyPendingUpdate` 在 Replace 前重算 staged SHA-256 与 plan 比对（缺失/不匹配保留 plan），新增篡改/缺校验和单测 |
| AUDIT-P3-11 | ⚠️ 声称已修复，复核未落地 | d026c30 提交说明称自动过期调用 `Notifier.Dismiss`，但 toasts.go enqueue/removeToast 与 uiroot/notifier.go 实际未改；第四轮探针仍记录（见文末第四轮） |

第三轮回归：`internal/regression` **133 PASS / 0 SKIP**（新增 35 例：install 模块/宿主、toast、workspace mcp.json、引用补充）。

## 反证审计第四轮：turn usage / plan mode / 消息反馈（2026-11-08）

范围：HEAD `0769ad3`（turn usage footer、per-message copy、plan mode）+ 工作树 task-133 消息反馈（未提交）。
只新增 `internal/regression/**` 测试并改本文件；未改生产代码、未 commit。新增 **20** 个顶层 Test（包内 133 → 153）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 27.9s（153 PASS / 0 SKIP）
```

| # | 链路 | 反证点 | 新增用例 | 结果 |
|---|------|--------|---------|------|
| 18 | turn usage（agent） | 无 usage/全零 usage → Usage nil 且无事件；有 usage → assistant 消息字段+恰一个 usage 事件；OpenAI 嵌套 cached 与 DeepSeek `prompt_cache_hit_tokens` 都进 CachedTokens；tool-calling 两轮迭代 token/cached 累加、Iterations=2、事件为累计值；并发两个会话各自记账（-race）；重启读模型 Usage 丢失（按设计）但事件保留；provider 失败无记账 | 8 | PASS |
| 19 | plan mode（agent gate） | 15 个白名单工具（read_file/list_dir/glob/grep/ask_user/present/plan_review/exit_plan_mode/goal/team_task_list/team_task_get/memory/skill/skills/web_fetch）全部放行且执行、不排队；9 个写/exec/编排工具拒绝、未执行、状态 error/summary 含 denied；单轮多工具全拒；mode 值 `plan/PLAN/ Plan /空白`→plan，`normal/空/未知`→走 approvals 队列；切回 normal 后同调用进入审批队列（allow 才执行），再切 plan 立即拒绝；与 permission 组合：plan 优先于 full/workspace，normal+full 自动放行、normal+never 直接拒绝 | 5 | PASS |
| 20 | 消息反馈（task-133 工作树） | 非法值（great/GOOD/带空白/换行）拒绝且不写 settings/不改读模型；未知/空白 session/message 拒绝；good/bad/清空 的读模型+settings 行一致；settings 写失败→错误上抛、读模型不变、不通知订阅者，恢复后可写；重启从持久 settings 水合 rating；20 并发写后读模型与 settings 一致、可清空；契约 key 形状/常量；**非 assistant 消息也可被评分（AUDIT-P3-12）** | 7 | PASS + AUDIT-P3-12 |

### 第四轮发现

#### AUDIT-P3-12（P3，校验缺口）SetMessageFeedback 接受任意已存在消息（含 user/tool），契约只针对 assistant

- 位置：`internal/modules/agent/feedback.go:31`（SetMessageFeedback）：校验 feedback 值 → 找 session → 仅检查 `state.messages` 中存在该 ID，未校验 `message.Role == ChatRoleAssistant`；契约 `internal/contracts/chat.go` 写的是 “user's rating of one **assistant** message”。
- 复现：`go test ./internal/regression -run TestFeedbackOnNonAssistantMessagesProbe -v` → 对 user 消息 `SetMessageFeedback(...,"good")` 返回 nil 并写入 settings（探针 log 记录，不 fail）。
- 影响：API 误用可产生“评分了用户消息”的持久数据；当前 UI 只在 assistant 行渲染按钮，用户不可见，属低危一致性缺口。建议增加 role 校验（或把契约措辞改为任意消息）。

#### AUDIT-P3-11 复核：提交声明与代码不一致，自动过期仍未 Dismiss（P3 重开）

- 位置：`internal/ui/toasts/toasts.go:110 enqueue/130 removeToast`（TTL 到期只删栈）；`internal/uiroot/notifier.go:51` active map 仅由 `Dismiss`（:66）删除。
- 复现：`go test ./internal/regression -run TestToastsTTLExpiry -v` → 4s 后 toast 消失但 `Notifier.Dismiss` 调用数为 0（log 记录）；d026c30 提交说明称“toast expiry calls Notifier.Dismiss”，但该提交未修改 toasts.go/notifier.go（`git show d026c30 --stat` 无此二文件）。
- 影响：同第三轮（宿主桥 active 表随通知缓慢增长，无 UI 影响）。建议实际在 removeToast/过期路径调用 `st.notifier.Dismiss(notification.ID)`，并补单测防回归。

#### 第三轮缺陷复核结果
- AUDIT-P3-9（staged symlink 包含检查）：已修复（module/host 双侧 `EvalSymlinks` 复检）；探针不再记录。
- AUDIT-P3-10（宿主重校验 SHA-256）：已修复（Replace 前重算比对，缺失/不匹配保留 plan）；探针不再记录。
- AUDIT-P3-11：见上，仍未修复。第四轮无 P0/P1/P2 新发现。

### /feedback 相关：未落地项标注

- **斜杠命令 `/feedback [good|bad]` 未迁移**：`internal/ui/session/session.go` 的 sessionCommands 仍只有 8 个命令（新会话/创建副本/导出对话/设置目标/审阅计划/添加记忆/权限模式/打开设置），无 /feedback；旧 shell 的 `/feedback` 可给最新 assistant 打分或写会话级 feedback 行。
- **消息级 good/bad + 清空** 已在 task-133 工作树落地（`internal/ui/session/view.go` 有帮助/没帮助/已反馈 + `internal/modules/agent/feedback.go`），但**未提交**，本轮按工作树测试并通过。
- **plan 审阅的 keep-planning + feedback 修订循环**仍未落地（plan Modal 只有批准/取消；旧 shell 有 Keep planning + 反馈续跑）。
- **会话级 feedback 行 / 反馈汇总/导出**未迁移（旧 store 的 feedback 表语义）。

### 第四轮未证伪的关键不变量

1. **usage**：nil/全零不写、非零恰好一条事件且字段一致；cached 两种 provider 拼写均归集；多迭代累加（含 cached/iterations）；并发会话隔离；失败运行不记账；重启按设计丢 per-message usage 但保留审计事件。
2. **plan mode**：白名单精确放行（不产生审批）、非白名单 fail-closed（未执行、错误可见、不排队）；多工具轮次全部拒绝；mode 归一化只认 plan 变体，其它值回退审批；切换恢复审批语义；permission full/workspace/never 组合下 plan 优先级正确。
3. **feedback（WIP）**：非法值/未知目标零副作用；写失败不污染读模型且不通知；成功后读模型与持久 settings 一致；重启水合；并发一致。

### 第四轮局限

- 消息反馈实现为 task-133 工作树未提交代码；若该实现被回滚/重写，`feedback_test.go` 需同步（测试依赖 `contracts.SessionFeedbacker` 与 Feedback 字段）。
- feedback 的 nil-settings 分支（`ErrSettingsUnavailable`）在公共装配不可达（agent 模块 Requires KeySettings），仅有模块内单测覆盖。
- plan-mode 白名单由测试固定为 15 项（期望值）；产品改名单时需同步（这是有意的行为锁）。
- usage 的 restore 丢失为文档化设计（rig history 无 usage 字段），测试按设计断言；未覆盖 UI footer 的像素/布局（模块内测试覆盖文案）。

## 第四轮缺陷修复（Lead 复核，2026-11-08）

| ID | 状态 | 修复摘要 |
|----|------|----------|
| AUDIT-P3-11 | ✅ 已修复（重开→本次真正修复） | toasts TTL 到期路径现在调用 `Notifier.Dismiss(notification.ID)` 再移除栈项；新增断言证明过期后 dismiss 被调用；并修正此前提交说明与代码不一致的问题 |
| AUDIT-P3-12 | ✅ 已修复 | agent `SetMessageFeedback` 增加角色校验（仅 assistant 可评分；user/tool 返回可读错误且零副作用），新增单测 |
| AUDIT-P3-13 | ✅ 已修复 | 诊断报告改为按凭据后缀掩码并对 JSON 值递归 redact（Bearer token/secret/apiKey 等），第八轮泄漏探针复跑不再记录 |

第四轮回归：`internal/regression` **153 PASS / 0 SKIP**。第五轮复跑确认 P3-11/P3-12 修复未回退；`/feedback` 斜杠命令已在本轮落地。
第六轮复跑扩张到 **180 PASS / 0 SKIP**（托盘/关闭到托盘、工件下载说明、chat modes 四模式 gate 表），并新增 desktop assembly 的模式菜单 E2E。
第七轮复跑扩张到 **189 PASS / 0 SKIP**（全局热键、可选 LSP、automation Close 关机语义），并复跑第四~六轮关键探针 104 项确认 0 回退。
第八轮扩张到 **206 PASS / 0 SKIP**（Home composer、/停止 /附加文件 /帮助、数据与隐私页、通知开关、侧栏添加工作区），发现 AUDIT-P3-13（已修复）。
第九轮扩张到 **220 PASS / 0 SKIP**（ACP session/list/load、三态主题、侧栏视图、行为默认权限、通知级别过滤、自动化模型覆盖），发现 AUDIT-P3-14。
第十轮扩张到 **227 PASS / 0 SKIP**（plan keep-planning、slash 命令 i18n、窗口快捷键），并复跑第九轮关键链路 17 项确认 0 回退。
第十一轮扩张到 **241 PASS / 0 SKIP**（排队发送、MCP 服务器开关、自动化筛选、消息字号、AGENTS.md/记忆页/连接检查/上下文预算/反馈导出）。
仍有文档化未落地项（plan keep-planning 反馈循环、会话级 feedback 汇总、旧 `/feedback [good|bad]` 参数式直接打分、自动化 runs 之外的历史查询）。

## 反证审计第五轮：/feedback 命令 / 自动化运行历史 / 反馈复跑 / 记忆与技能编辑（2026-11-08）

范围：HEAD `8074fb2`（/feedback 命令、自动化运行历史）+ `37190ed`（记忆整理/清空、技能创建/删除；本轮并行落地，审计先按
工作树 WIP 写用例，随后随该提交落地）。只新增 `internal/regression/**` 并改本文件；未改生产代码、未 commit。
新增 **19** 个顶层 Test（包内 153 → 172）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 30.6s（172 PASS / 0 SKIP）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 21 | `/feedback` 命令与导出 | Enabled 条件（无 SessionFeedbacker / 无 active session / 无 assistant 回答均禁用）；禁用时 Run 无对话框且不 panic；对话框给最新 assistant 打分/清空并同步读模型；后端写失败保持对话框+本地化错误、读模型不变；导出 JSONL 仅含 good/bad（非法读模型值不导出）、Markdown zh/en 输出“用户反馈/User feedback”行且不导出非法值 | 5 | PASS |
| 22 | 自动化运行历史 | RunNow 成功记录（时间戳有序+摘要+持久化）；失败记录（status=error、摘要含 send prompt、≤200 runes、LastError、持久化）；ticker 自动运行记录；12 次运行后 cap 10 且 newest-first；旧 registry JSON 无 `runHistory` 字段正常加载并继续追加；并发 RunNow（多任务+同任务 in-flight 折叠，-race）每任务至少一条记录 | 6 | PASS |
| 23 | 第四轮消息反馈/P3 修复复跑 | `go test -run 'TestFeedback|TestToasts' -race` → 19 PASS：P3-12 角色校验拒绝 user 消息评分（错误含 assistant/unknown）；P3-11 TTL 过期调用 Notifier.Dismiss 且断言 dismiss；第四轮 13 个反馈用例全部通过 | 复跑 | PASS（未回退） |
| 24 | 记忆整理/清空 + 技能编辑（本轮落地） | memory：tidy 按 trim/空白折叠/小写归一保留最早一条并报告删除数；无重复 no-op；清空确认（取消保留/确认清空/空 store 安全/持久化为空）；List、Remove 故障显示错误行不 panic；skills：draft 校验（空名/空 body/空格·点·斜杠·换行/超长名）、frontmatter 折行、重复名拒绝且不覆盖、中文 slug 可创建、删除边界（未知/穿越/配置根只读/无 SKILL.md 目录/symlink 拒绝）、嵌套文件随技能删除、ctx 取消零副作用 | 8 | PASS |

### 第五轮未证伪的关键不变量

1. **/feedback**：命令可见性完全由（feedbacker、active session、最新 assistant）三者决定；对话框只写最新 assistant；写失败不改读模型且可重试；导出层对畸形 Feedback 值做白名单归一（非法值静默忽略，不污染 JSONL/Markdown）。
2. **自动化历史**：每次执行（RunNow/ticker）恰一条记录且同步落盘；失败摘要截断到 200 runes；历史上限 10 且 newest-first；旧 JSON 无新字段向后兼容；并发调用不死锁、不重复记录（in-flight 折叠）、-race 干净。
3. **P3 修复未回退**：P3-12 角色守卫与 P3-11 dismiss 行为均被既有用例锁定并复跑通过。
4. **记忆/技能（WIP→37190ed）**：tidy 的归一化键与保留策略确定；清空有确认且空 store 安全；故障注入只产生可读错误；技能写路径仅 `<dataDir>/skills`，名称白名单阻断路径穿越，重复创建 O_EXCL 不覆盖，删除前做包含+realpath+symlink 检查，配置/工作区根只读，取消上下文零副作用。

### 第五轮发现

- **无新缺陷**（无 P0/P1/P2/P3）。P3-11 与 P3-12 经 ca2d24c 修复后本轮复跑确认仍未回退。
- 观察（非缺陷）：记忆 tidy/clear 在 UI 层用 List+逐条 Remove 实现，单 UI 线程下正确；不是原子批量 API，大量条目时写放大明显，未来若做后端批量接口可再评估。
- 观察（非缺陷）：技能描述的 frontmatter 未做 YAML 引号转义，但实现会折叠空白（阻断换行注入），实测 `inject: value`/`#hash`/`[list]` 等值可原样往返，未构成注入。

### 第五轮局限

- 记忆整理/清空为 UI 层实现：本轮通过真实 memory 后端与故障注入 store 覆盖，未覆盖“外部进程同时写 memory.json”的并发语义（store 自身有互斥与原子写，模块内单测覆盖）。
- 自动化历史仅验证“缺少 `runHistory` 字段”的旧 JSON；`runHistory` 类型损坏的 registry 会按模块启动错误处理，未作为兼容目标。
- `/feedback` 旧命令的参数式用法（`/feedback good|bad` 直接给最新回答打分）与 session 级 feedback 行仍未迁移；当前为交互式对话框。
- 技能编辑器设置页的按钮/模态交互由 settings 包内单测覆盖；本轮反证聚焦模块接口、文件边界与文件系统副作用。

## 反证审计第六轮：系统托盘 / 工件下载 / chat modes（2026-11-08）

范围：`12021a0`（系统托盘 + close-to-tray、工件下载）+ `03c57d7`（chat modes 四模式，本轮并行落地后提交）。
只新增 `internal/regression/**` 与 `internal/ui/uimodtest`（test-only 包）的 E2E，并改本文件；未改生产代码。
`internal/regression` 新增 **8** 个顶层 Test（172 → 180），另加 1 个 uimodtest E2E。

```
$ gofmt -l internal/regression/ internal/ui/uimodtest/      （空）
$ go vet ./internal/regression ./internal/ui/uimodtest      OK
$ go test ./internal/regression -count=1 -race               ok 39.7s（180 PASS / 0 SKIP）
$ go test ./internal/ui/uimodtest -count=1 -race             ok 10.5s（含模式菜单 E2E）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 25 | 系统托盘 / 关闭到托盘（host，regression 层） | closeToTray 归一化表（true/TRUE/1/yes/空白=on；空/false/no/0/未知/nil=off）；createTray 选项（ID zeokeel、tooltip ZeoKeel、PNG icon、菜单）；托盘 click 与「显示 ZeoKeel」→ show、「退出」→ quit；工厂缺失/报错/nil tray → 可读错误、不注册 click/close、不隐藏窗口；setupTray nil window 拒绝；close-to-tray on 时关闭 PreventDefault+Hint Hide，keepAlive false（shutdown）放行，keepAlive nil 仍拦截，off 不注册 close handler | 4 | PASS |
| 26 | 工件下载（rightpanel） | 说明：`internal/ui/rightpanel/artifacts_download_test.go` 已用真实 frame 覆盖保存成功/取消/对话框错误/写入错误/Unicode slug 表/英文文案（6 用例）；下载处理是 rightpanel 包内未导出函数，导出层（Session Markdown/JSONL）与工件下载是两条独立路径，不应合并断言；保存对话框依赖原生 Dialog，headless 无法触发，故 regression 层不重复新增用例 | 0（不重复） | 说明 |
| 27 | chat modes 四模式 gate 表（agent） | minimal：15 白名单放行且不排队、写/exec/编排 deny 未执行+error/denied；creator：8 白名单放行、read_file/grep/写/exec deny；plan：15 白名单+plan 优先级；standard：写/读均走审批（allow 执行/deny 不执行）；mode 归一（大小写/空白/未知回退 standard）；plan+creator/minimal 组合下 plan 优先（team_task_list/read_file 放行、write deny）；bypass 模式每轮 approvals pending 0 | 4 | PASS |
| 28 | 模式菜单 E2E（真实 desktop assembly，uimodtest） | 初始「普通」且无模式徽标；切「精简模式」→ `chat.mode=minimal` 且 `agent.mode=normal`、header 徽标「精简模式」、工具栏标签「精简」；切「创作模式」→ `chat.mode=creator`、徽标替换且无旧徽标；切回「普通模式」→ `chat.mode=standard`、所有徽标消失。E2E 合适：assembly 装配→composer popover→settings store→header 渲染的整链只有 E2E 能证明 | 1 E2E | PASS |

### 第六轮未证伪的关键不变量

1. **托盘**：关闭到托盘只在设置归一为真且托盘创建成功时生效；创建失败零副作用（不注册监听、仍可正常退出）；shutdown guard 让 teardown 能真正关窗；菜单/点击动作映射到 show/quit。
2. **chat modes**：标准=审批流、精简=只读、创作=非编码写作面、计划=最高优先；白名单精确（不误放、不误拒），拒绝的工具不执行且错误可见，切换模式对后续调用立即生效。
3. **E2E 模式菜单**：菜单项→双 settings 写入→header 徽标/工具栏标签的可见结果与存储值一致，切回普通完全清除徽标。

### 第六轮发现

- **无新缺陷**（无 P0/P1/P2/P3）。
- 观察（非缺陷）：`createTray` 自身接受 nil window（`setupTray` 才校验）；调用方只有 host 内部，regression 以 setupTray 的 nil-window 拒绝作为对外约束。
- 观察（非缺陷）：托盘 show 是 window.Show/Restore/Focus 的组合，本层用 handler 计数验证 wiring；真实 window 调用由 host 包既有单测覆盖，真实原生 tray 由 xvfb smoke 覆盖（无 desktop session 时仅告警）。

### 第六轮局限

- 原生托盘图标/菜单需要桌面会话：regression 层只测 seam wiring 与关闭策略，xvfb 下 NewTray 失败路径由 host smoke 覆盖。
- close-to-tray 的窗口 Show/Restore/Focus 实际调用由 host 单测断言；本层以 handler 计数避免依赖真实 mygo Window。
- 工件下载未加 regression 层（理由见 #26）；若未来把保存动作抽成 contracts 级 seam，可在导出层补跨层用例。
- 模式菜单 E2E 使用内存 settings；宿主 settings 文件持久化由 host 设置单测覆盖。
- chat modes 白名单在 regression 中以期望表锁定（contracts 政策变化时需同步，属有意行为锁）。

## 反证审计第七轮（终审）：全局热键 / 可选 LSP / automation Close（2026-11-08）

范围：`132e386`（全局热键 + 可选 LSP 工具）。只新增 `internal/regression/**` 并改本文件；未改生产代码。
`internal/regression` 新增 **9** 个顶层 Test（180 → 189）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go test ./internal/regression -count=1 -race               ok 41.3s（189 PASS / 0 SKIP）
$ go test ./internal/regression -race -run '第四~六轮关键探针'   104 PASS / 0 SKIP（0 回退）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 29 | 全局热键（host） | normalizeHotkey 表：空/空白=关闭；别名（commandorcontrol/option/return/esc/del/ins/win/meta）与大小写/trim/顺序归一；F1-F12、字母、数字、命名键；拒绝：仅修饰符、双修饰符无键、多键、未知修饰符/键、重复修饰符（含别名重复）、空 token、F13、超长（>64 rune 及超长组合）。注册成功：归一化 accelerator 注册、回调 Show/Restore/Focus 各 1、shutdown 返回的 off 调用注销；空/nil settings 不注册。失败零副作用：非法值不触达 registrar；注册失败返回可读包装错误、无 off、窗口零调用；nil window/registrar 拒绝 | 3 | PASS |
| 30 | 可选 LSP | contracts.Parse：空/空白=关闭零配置不报错；坏 JSON/数组文档/null/缺 command/空白 command/command 非字符串/args、env 非数组均报错；标量/args/env trim、空项丢弃、args 逐项 verbatim 不按空格拆分、未知字段忽略。Encode：先归一化再编码；Encode→Parse 往返一致；nil args/env 省略；零配置 Encode 不校验而 Parse 报 command required（记录该不对称）。toolkit：坏 JSON/缺 command/不存在命令/空/空白 5 类配置均激活成功、无 lsp_* 工具、常规工具仍在、日志含 ignoring/start 警告；seam 注入 fake server：lsp_hover/lsp_definition 恰注册一次、与内建 read_file 冲突的名字跳过并记日志、模块卸载 Close 恰一次 | 4 | PASS |
| 31 | automation Close（关机语义） | RunNow 后立即 DisposeAll（5 轮）：Close 等待 in-flight 完成后返回，automations.json 合法且 runHistory 恰 1 条 success；无 `.automations-*.tmp` 残留；Close 返回后 20ms 内文件字节不变（无关闭后写盘）。3 任务并发 RunNow 后立即 DisposeAll：每任务恰 1 条 success、registry 合法、-race 干净 | 2 | PASS |
| 32 | 第四~六轮关键探针复跑 | 按名称复跑 usage/plan mode/feedback/toasts/install/host apply/automation/memory/skills/tray/chat modes/references/effort/permission/team/mcp/jobs/approvals 等既有反证：104 PASS / 0 SKIP，无回退 | 复跑 | PASS |

### 第七轮未证伪的关键不变量

1. **热键**：accelerator 语法严格且归一化确定；注册失败是纯无副作用失败（启动不中断）；成功注册的回调只做 show/restore/focus；shutdown 钩子可注销。
2. **LSP**：配置解析拒绝一切畸形输入、空值安全表示“关闭”；toolkit 的任何 LSP 问题都只降级（激活成功、无 lsp_* 工具、日志可诊断），成功启动的工具恰注册一次且与内建工具冲突时跳过；卸载关闭且只关闭一次。
3. **automation Close**：Close 等待 in-flight 写盘完成，关闭后 registry 稳定、无临时文件、无关闭后写入；并发运行同样被 flush。
4. **0 回退**：第四~六轮 104 项关键探针复跑全绿。

### 第七轮发现

- **无新缺陷**（无 P0/P1/P2/P3）。
- 观察（非缺陷）：`EncodeLSPServerConfig` 不校验空 command（编码层只归一化），由 `ParseLSPServerConfig` 拒绝；UI 保存路径应先 Parse 再 Encode（settings 页现状如此），文档记录该不对称。
- 观察（非缺陷）：热键 shutdown 注销由 host `runDesktop` teardown 调用 `hotkeyOff`；regression 层验证 off 闭包语义，`runDesktop` 的调用顺序属 host 装配层，未在 regression 重复。

### 第七轮局限

- 热键只测注入 registrar/window；真实 `mygo.GlobalShortcut` 注册需桌面会话（xvfb smoke 属 host 层），系统级占用冲突的真实错误文案未覆盖。
- LSP 用 seam 注入 fake server 验证注册/关闭所有权；真实语言服务器进程（离线环境无可用 LSP binary）未做进程级集成，`lsp.Start` 的真实 stdio 握手由 rig 单测覆盖。
- automation Close 用快速 fake chat 的 in-flight 写盘窗口；真实 provider 慢网络下的 Close 阻塞时长未做性能断言（语义是等待完成）。
- 第四~六轮复跑按测试名前缀选择 104 项，未逐条复跑全部 180 项（全量 -race 已覆盖）。

## 反证审计第八轮：Home composer / 会话命令 / 数据与隐私 / 通知开关 / 侧栏工作区（2026-11-08）

范围：`a3ef7ec`（Home composer、/停止 /附加文件 /帮助、数据与隐私页）+ `9d1cbac`（侧栏添加工作区、ui.notifications 开关）。
只新增 `internal/regression/**` 并改本文件；未改生产代码。新增 **17** 个顶层 Test（189 → 206）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 44.5s（206 PASS / 0 SKIP）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 33 | Home composer | 空草稿点击发送 / Enter 均不建会话不发送；无 chat 后端仍安全渲染且发送禁用；Enter 发送 trim 后建会话+Send+导航 session，Shift+Enter 不发送；附件：picker 成功暂存 chip、无 AttachmentSender 时回退 plain prompt 并提示「附件需在会话中发送」、picker 失败可读错误；NewSession / Send 失败可读错误且不导航、不丢状态 | 4 | PASS |
| 34 | /停止 /附加文件 /帮助 | /stop：非运行禁用、运行启用、调用 Cancel + 「已停止」徽标、Cancel 失败错误行；/附加文件：无 AttachmentSender →「当前后端不支持附件」、picker 成功暂存 chips、取消 no-op、错误可读；/帮助：模态含帮助/斜杠命令/快捷键/Esc 关闭/Cmd/Ctrl+K 与关键命令标题、注册命令 ≥12，Esc 关闭且注册量不变 | 3 | PASS |
| 35 | 数据与隐私页 | 导出全部：Markdown 含 3 个会话标题与 role/text、默认文件名、成功徽标、取消静默、保存失败错误行；清除全部：确认文案带会话数、取消保留、确认清空、部分失败「已清除 2 个会话，1 个失败」；诊断报告含应用/Commit/会话数/设置项且 chat.apiKey 掩码；打开数据目录 seam 失败错误行；无会话空态；报告取消/失败 | 4 | PASS（发现 AUDIT-P3-13） |
| 36 | 通知开关 ui.notifications | 归一化行为矩阵：false/FALSE/空白/0/no 抑制；缺失/空/true/1/yes/banana/2/off/disabled 显示（缺失默认开）；热切换：禁用时清空已显示栈、禁用期投递忽略、重新启用后可显示；设置页 toggle 写 false/true、坏值起始为开 | 3 | PASS |
| 37 | 侧栏添加工作区 | 空名称回退目录 basename（含尾斜杠）、自定义名优先、Create 收到 trim 后参数、Open 新 ID、成功徽标、模态关闭；空目录本地拒绝不调用 Create、picker 失败/取消、Create 失败保留模态+可读错误；无 workspace 后端隐藏入口 | 3 | PASS |

### 第八轮发现

#### AUDIT-P3-13（P3，隐私）诊断报告只掩码 chat.apiKey，其他含密钥设置明文导出

- 位置：`internal/ui/settings/data.go` 的 `diagnosticReportText`：`if key == contracts.SettingsChatAPIKey && value != "" { value = "已设置(隐藏)" }`，其余 key 原样输出。
- 复现：`go test ./internal/regression -run TestDataPrivacyDiagnosticReportMasking -v` → 设置 `web.search.apiKey="sk-search-secret-456"` 与 `mcp.servers` 含 `Authorization: Bearer sk-mcp-secret-789`，导出的诊断报告包含两处明文（探针 t.Logf 记录，不 fail）。
- 影响：用户按页面引导导出诊断报告用于支持/排障时，会把搜索服务 key、MCP server headers 内的 token 等敏感值写入磁盘文件；属于隐私泄露（需用户主动导出，故记 P3）。
- 建议：按 key 后缀（`apiKey`/`api_key`/`token`/`secret` 等）或显式敏感清单掩码；对 JSON 值（如 `mcp.servers`）递归 redact 敏感字段；并补一条报告内容白名单测试。

### 第八轮未证伪的关键不变量

1. **Home composer**：空草稿与修饰键组合不发送；发送成功才清草稿/附件并导航；失败保留状态并展示可读错误；附件回退不静默丢提示；无 chat 后端不 panic 且禁用发送。
2. **会话命令**：/stop 的 Enabled 与运行态一致且 Cancel 结果可见；/file 需要 AttachmentSender，picker 三种结果（成功/取消/错误）各自正确；/help 列出真实注册命令与快捷键，Esc 关闭不改变注册表。
3. **数据与隐私**：批量导出/清除的非取消路径可见结果、取消静默、错误可读；清除部分失败有计数；诊断报告只掩码 chat.apiKey（见 P3-13）。
4. **通知开关**：只有 false/0/no 关闭，缺失/坏值保持开启；热切换清栈且不重启生效。
5. **侧栏工作区**：名称回退与 Create 参数确定；失败保留模态；无后端不显示入口。

### 第八轮观察（非缺陷）

- Home 附件回退在设置 notice 后立即导航到会话，提示可能不可见；是否改为 toast 属 UX 决策，已在文档记录行为。
- /stop 的 Enabled 只在调色板路径生效；直接程序化 Run 仍会调用 Cancel（不可达路径，测试按 UI 约束断言）。
- 通知设置页对坏值显示为「开」并在首次 toggle 写入规范值；符合“缺失/坏值默认开”的约定。

### 第八轮局限

- Home/settings/sidebar 测试用内存 settings 与 fake chat/workspace；宿主 settings 文件持久化、真实 provider 网络错误语义由模块/宿主单测覆盖。
- 原生 picker/save/open 对话框均以 seam 替换（headless）；真实桌面对话框路径由桌面 smoke 覆盖，CI 不可触发。
- 数据页导出内容在 seam 处捕获；默认 seam 的磁盘写入错误未在本轮覆盖（seam 错误路径已覆盖）。
- 通知 TTL 的 4s 真实过期未在本轮重复（既有 TestToastsTTLExpiry 覆盖）。
- Home 附件大小上限（10MB）与非法文件仅按实现路径推断，未在本轮构造超大文件（避免 CI 磁盘压力）。

## 第八轮缺陷修复（Lead 复核，2026-11-08）

| ID | 状态 | 修复摘要 |
|----|------|----------|
| AUDIT-P3-13 | ✅ 已修复 | `internal/ui/settings/data.go` 诊断报告：敏感键（apikey/api_key/token/secret/password/authorization）整值「已设置(隐藏)」；JSON 设置递归 redact（Bearer → `Bearer ***`、凭证键 → `***`）；新增 settings 单测覆盖 `web.search.apiKey` 与 `mcp.servers` Bearer 泄漏 |

## 反证审计第九轮：ACP session 列表/加载、三态主题、侧栏视图、默认权限、通知级别与自动化模型（2026-11-08）

范围：`bf4618a`（全工具级开关、托盘会话/设置动作）+ `d40d7b3`（三态主题、侧栏视图选项）+ `bd89cf1`（plan transcript 卡、
自动化模型覆盖、toast 级别过滤）；`session/list`/`session/load` 为工作树并行落地的 ACP 扩展（未提交，审计先按其实现）。
只新增 `internal/regression/**` 并改本文件；未改生产代码。新增 **12** 个顶层 Test（208 → 220）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 46.7s（220 PASS / 0 SKIP）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 38 | ACP `session/list` / `session/load`（真实 JSON-RPC 帧） | initialize 后 list：newest-updated first、空列表返回 `[]`、无 id 通知不产生响应、`params:[]` → -32602（-32602）；load：只回放 user/assistant 非空文本并 trim、顺序不变、model 回退 ChatConfig、未知/缺失/空白/非对象参数 → -32602、**成功与失败都不改变 active session**；initialize 的 `agentCapabilities.loadSession=true` | 2 | PASS（依赖工作树 WIP） |
| 39 | 三态主题（host 回归层） | `resolveThemeDark` 表：light/dark 强制、system/空/未知跟随、大小写/空白归一（× systemDark 两值）；`initialThemeDark`：nil settings 跟随、空 mode 无 legacy 跟随、legacy true/false/坏值→forced（坏值→light）、mode 优先 legacy、system/未知仍跟随；`applyThemeMode`：system 注册监听并应用更新、强制模式不注册、shutdown hook 注销恰一次、nil theme/system 安全 | 3 | PASS |
| 40 | 侧栏视图排序/分组 | 默认 recent 顺序；视图菜单选 名称→`sidebar.sort=name` 且大小写不敏感排序；平铺→`sidebar.group=flat`；切回写 recent/workspace；workspace 树显示分组标题、flat 隐藏标题与未分组且会话全在；坏值 `bogus` 回退默认（recent/workspace）不崩溃、不被 frame 覆写 | 2 | PASS |
| 41 | 行为页默认权限 | 默认 ask 且不写入；选择 完全访问/从不/询问 写同一 `agent.permission` 行 + 「已保存」；坏值显示 ask 且不被覆写；与会话权限 chip 共用 setting：full→chip「全部允许」；never 已存储但 chip 显示「询问」（AUDIT-P3-14） | 2 | PASS（发现 AUDIT-P3-14） |
| 42 | 通知级别过滤 + 自动化模型覆盖 | toast `ui.notifications.hidden`：缺失/空/坏 JSON 不过滤、四级别过滤且大小写空白归一、未知级别永不过滤、隐藏级别热清栈、行不被 frame 改写；automation model：trim 持久化、RunNow 先 SetSessionModel 后 Send、registry 持久化、无 switcher/switch 失败仍 success 仅告警、无 model 不调用 | 2 | PASS |

### 第九轮发现

#### AUDIT-P3-14（P3，UI 一致性）agent.permission=never 存储但会话权限 chip 显示「询问」

- 位置：`internal/ui/session/session.go` 的 `permissionMode`/`permissionLabel`：只识别 ask/workspace/full，unknown（含 `never`）回退 ask → chip 显示「询问」；而 approvals gate 已正确执行 never（拒绝全部）。行为页（`internal/ui/settings/behavior.go`）提供「从不」选项并写入同一 `agent.permission` 行。
- 复现：`go test ./internal/regression -run TestBehaviorPermissionSharedWithSessionChip -v` → 存储 `never` 后会话工具栏徽标显示「询问」（log-only 探针）。
- 影响：用户在行为页选择「从不」后，会话工具栏仍显示「询问」，与真实 fail-closed 行为不一致，可能造成“为何工具全被拒”的困惑；无数据/安全影响（审批本身正确），记 P3。
- 建议：`permissionLabel` 增加 never→「从不」，或行为页的 never 与会话 chip 的三态 cycle 统一（chip 加第四态）。
- 另：第八轮 AUDIT-P3-13（诊断报告掩码）已由 da654e3 修复，第八轮泄漏探针本轮复跑不再记录。

### 第九轮未证伪的关键不变量

1. **ACP session 方法**：真实帧往返的 JSON-RPC 形状正确（id/result/error code）；列表排序/空列表/坏参数稳定；load 过滤与 trim 规则确定；load 是只读操作（不改变 active session）；initialize 能力声明与新方法一致。
2. **主题**：三态归一与 legacy 回退优先级确定；system 的监听只在 system/未知模式注册、更新可传导、shutdown 注销一次；强制模式完全静态。
3. **侧栏视图**：sort/group 仅两个合法值生效，坏值回退且不写回；name 排序大小写不敏感；flat 真正隐藏分组且不丢会话；选择持久化。
4. **默认权限**：页面写的就是 approvals/会话 chip 共用的同一 row；ask 默认与坏值归一；full/never 的审批语义（既有 gate 测试）不受页面影响。
5. **通知级别**：坏配置永不误过滤（fail-open 显示）；已知级别精确过滤并热清栈；行不被渲染路径改写。
6. **自动化模型**：先设模型再发送；无能力/失败时 best-effort 仍执行；无覆盖不调用。

### 第九轮观察与局限

- ACP `session/list`/`session/load` 为工作树未提交实现；若被回滚/重写，本组测试需同步（依赖导出的 MethodSessionList/MethodSessionLoad 常量与 loadSession 能力）。
- 主题回归通过 test-only `//go:linkname` 访问 host 未导出 helpers（host 单测已覆盖同样分支，regression 只补关键导出层断言）。
- 侧栏视图用 scripted `ChatSessionInfo`（fakeChat 的 Sessions 不携带 WorkspaceID），workspace 后端为 recording fake；真实 workspace 模块的创建/打开由既有单测覆盖。
- toast 级别过滤复用既有 fake-notifier harness；真实 Notifier→toast 链路已有 host bridge 测试，本轮不重复。
- 自动化模型覆盖用 recording switcher；真实 provider 的每会话模型切换由 agent 模块单测覆盖。
- 任务要求的「托盘动作 / plan 卡 / 全工具级开关」本轮按“选 2-3 个补导出层断言”选择通知级别过滤 + 自动化模型覆盖；托盘/plan 卡/全工具开关已有模块内单测（host/toolcards/plugins），未在本轮重复。

## 第九轮缺陷修复（Lead 复核，2026-11-08）

| ID | 状态 | 修复摘要 |
|----|------|----------|
| AUDIT-P3-14 | ✅ 已修复 | `contracts.PermissionNever` 常量补入；会话 composer 权限 chip 识别 never 并显示「从不允许/Never」（此前回落为「询问」），新增单测；实际 fail-closed 行为与显示一致 |

## 反证审计第十轮：plan keep-planning / slash 命令 i18n / 窗口快捷键（2026-11-08）

范围：`f2dff99`（plan keep-planning 反馈回路 + UI i18n 清理）+ `6563a1a`（ACP session list/load、行为权限页、窗口快捷键）。
只新增 `internal/regression/**` 并改本文件；未改生产代码。新增 **7** 个顶层 Test（220 → 227）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 47.2s（227 PASS / 0 SKIP）
$ go test ./internal/regression -race -run '第九轮关键链路'      17 PASS / 0 SKIP（0 回退）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 43 | plan keep-planning（interaction 叠加层） | 成功：Exit("cancel") 恰一次 + Send(active, 固定前缀+反馈) 恰一次 + 叠加层关闭 + Notifier 成功 toast 恰一次；空反馈时按钮禁用/点击无动作（无 Exit/Send）；Send 失败保留 Modal+错误行、仅一次尝试（无重复发送）；Exit 失败保留 Modal 且不 Send；无 active session → 「没有活动会话」错误、无 Send、Modal 保留；无 chat → 隐藏「继续规划/继续规划反馈」而批准/取消仍在 | 3 | PASS |
| 44 | slash 命令 i18n | en 下 12 个 session 命令（含反馈）Title/Hint 全英文且无汉字；zh→en 切换后下一帧重注册（stop/help 变英文）；/help Modal 渲染英文（Help/Slash commands/New conversation/Stop/Attach files/Permission mode/Esc to close）且无中文标题；Esc 关闭后切回 zh 恢复中文；命令注册不漂移 | 2 | PASS |
| 45 | 窗口快捷键（回归层） | Cmd/Ctrl+N 经 kernel KeyChat 建会话并导航 session（连续两次建两个）；Cmd/Ctrl+, 打开设置（关闭后可再开）；Cmd+K 打开命令面板（Esc 关闭）；Cmd+B 折叠侧栏；K/B 不建会话、不开设置（与 N/, 不冲突）；无 chat：Cmd+N 安全（不导航不 panic）、Cmd+, 仍可开设置；NewSession 失败静默不导航 | 2 | PASS |
| 46 | 第九轮关键链路复跑 | 按前缀复跑 theme / sidebar view / behavior permission / toast level / ACP session / automation model / sidebar add workspace：17 PASS / 0 SKIP，0 回退 | 复跑 | PASS |

### 第十轮未证伪的关键不变量

1. **keep-planning**：反馈非空才可提交；提交先 Exit(cancel)（失败则原样保留、不发送），再向 active session 发送带前缀反馈（失败保留 Modal 防丢失）；成功才关叠加层并 toast；无 chat 时该交互整体隐藏。
2. **i18n**：命令注册表的 Title/Hint 随宿主语言在激活时与切换后的下一帧更新；帮助 Modal 直接读取注册表，因此同步刷新；切回中文恢复。
3. **快捷键**：N 创建+导航（kernel 绑定与失败静默）、, 切换设置、K/B 各自独立；无 chat 与失败的 NewSession 均安全。

### 第十轮发现

- **无新缺陷**（无 P0/P1/P2/P3）。
- 观察（非缺陷）：headless Tester 中当 Modal（设置面/命令面板）已打开时，再次按 Cmd/Ctrl+, 或 Cmd/Ctrl+K 不会关闭它（按键被 Modal/焦点消费）；关闭走 Esc/按钮。真实桌面焦点链路由 mygo 处理，uiroot 单测只断言“打开”。回归按可达路径（Esc/CloseSettings）断言。
- 观察：plan keep-planning 的英文文案由模块内测试覆盖；本层回归覆盖中文成功/失败分支与无 chat 隐藏。

### 第十轮局限

- plan 回路用 fake PlanReviewer/Notifier 桥接（宿主真实桥由 uiroot 测试覆盖）；未驱动真实 agent 的 plan_review 工具回合。
- 快捷键测试手工 `SetKernelContext` 绑定 kernel（真实 host 在装配前绑定）；Modal 打开时的按键消费行为与桌面焦点可能不同（见观察）。
- i18n 断言注册表值 + 帮助 Modal 显示；斜杠菜单/命令面板的渲染顺序由内部测试覆盖，本轮不重复。
- 第九轮复跑按前缀选择 17 项（全量 227 项 -race 已单独跑绿）。

## 反证审计第十一轮：排队发送 / MCP 服务器开关 / 自动化筛选 / 消息字号 / 管理深度（2026-11-08）

范围：`3cc1495`（queue-during-run、MCP server switches、automation filters）+ `4f57a2b`（transcript font size、feedback in
exports/reports）+ `217d0dc`（AGENTS.md 注入、settings Memory 页）+ `d27e449`（模型上下文预算、连接检查）。
只新增 `internal/regression/**` 并改本文件；未改生产代码。新增 **14** 个顶层 Test（227 → 241）。

```
$ gofmt -l internal/regression/                              （空）
$ go vet ./internal/regression                               OK
$ go build ./...                                             OK
$ go test ./internal/regression -count=1 -race               ok 51.7s（241 PASS / 0 SKIP）
```

| # | 链路 | 反证点 | 用例 | 结果 |
|---|------|--------|------|------|
| 47 | 运行中排队发送（ui.sendWhileRunning） | off/缺失/坏值：运行中仅「停止」、无输入/发送、无队列；on：可输入，提交后「已排队 N 条」且运行中不发；运行结束 FIFO 逐条 flush（会话/内容正确）；flush 失败保留队列+错误行（尝试被记录、队列不清）；切换会话清空队列且切回不重放 | 3 | PASS |
| 48 | MCP 服务器开关（mcp.disabledServers） | toolkit filter 表：大小写/空白归一、空/null/空数组/空串、坏 JSON 与错类型不误过滤且记日志、未知名、skipped 计数；plugins 页：settings 源被禁行还原 URL+「已停用（重启生效）」、启用写 `[]`、live 服务器禁用写排序合并 JSON、坏配置 status 错误但页面仍渲染 | 2 | PASS |
| 49 | 自动化搜索/筛选 | 名称/提示词子串、大小写、清空恢复；0 匹配空态与 0 计数；仅启用/仅停用筛选与计数；搜索+筛选组合；恢复全部 | 2 | PASS |
| 50 | 消息字号（ui.chatFontSize） | 行为页 selector：默认「标准」不写入；大/小/标准写入 settings；坏值显示「标准」且不被 frame 覆写；会话屏在两个帧之间切换字号后 Markdown 重建无错误、正文仍渲染 | 2 | PASS |
| 51 | AGENTS.md / 记忆页 / 连接检查 / 上下文预算 / 反馈导出 | AGENTS：默认文件注入标题+内容、自定义名 trim、缺失忽略、空设置禁用、>32KiB 截断（尾部不达 provider + 日志）；记忆页：无后端空态、真实后端添加/列表/搜索无匹配/删除与计数；连接检查：seam 200→「连接正常」+3 个模型+URL/key 传参、401→HTTP 错误、传输错误、未配置 URL 禁用且不调 seam；上下文预算：匹配模型 128k/32.8k、未知模型/无 catalogue 无后缀；反馈导出：会话 Markdown 有汇总与逐条引文、诊断报告有全局计数、非法值不导出 | 5 | PASS |

### 第十一轮未证伪的关键不变量

1. **排队发送**：仅字面 "true" 启用；运行中绝不直接 Send；队列归属创建它的会话；FIFO 一次一条；失败保留并可重试；切换会话清空。
2. **MCP 开关**：filter 对坏配置 fail-open（不误禁用），名字匹配 trim+大小写不敏感；插件页只重建 settings 源的行、写回排序 JSON、失败回滚/报错；assembly/workspace 行不可重建。
3. **自动化筛选**：纯 UI 会话态（不持久化）；子串匹配 name/prompt 大小写不敏感；筛选与搜索可组合；空态与计数一致。
4. **消息字号**：三档映射与坏值回退由包内测试锁定；设置落盘与重建路径无错误。
5. **管理深度**：AGENTS.md 只影响该 run 的 system prompt（缺失/空设置/超限安全）；记忆页真实后端 CRUD；连接检查 seam 的四类结果；上下文预算只对匹配 catalogue 的模型出现；导出/报告含反馈且非法值被忽略。

### 第十一轮发现

- **无新缺陷**（无 P0/P1/P2/P3）。
- 观察（非缺陷）：排队发送失败后队列保留，而 per-frame fallback（代码注释明确 "next notification (or frame check) retries it"）会在后续重建帧重试 Send；本层用帧 pump 观察到一次以上失败尝试即放宽（≥1 次）。真实桌面帧是事件驱动的，影响是失败期间可能重复尝试；建议未来加冷却/人工重试，不记缺陷。
- 观察（非缺陷）：消息字号的 px 映射与 Markdown 缓存键属 session 包内部（无导出 seam），本层只验证 selector 写值与重建路径无错误；映射/缓存失效由 internal 测试锁定。

### 第十一轮局限

- 队列 flush 经 `runtime.Post`，测试需多帧 pump；fakeChat 的 Send 不自动启动 run，测试手动翻转 Running。
- MCP filter 经 test-only `//go:linkname` 访问 toolkit 未导出函数（参数为导出类型 `toolkit.MCPServer`）；plugins 页用 fake `PluginsService` + 真实设置 store。
- 自动化筛选用真实 automation 后端（TickMs 设大避免调度干扰）与真实 UI 模块。
- 连接检查替换 `ModelListGetter` seam（不发真实网络），异步结果用帧 pump 等待。
- AGENTS.md 注入用 fake WorkspaceService（Root 指向临时目录），实际读文件路径已覆盖。
- 上下文预算用 fake `ModelCatalogService`；真实 catalog 加载/配置由模块测试覆盖。
- 反馈导出复用 data 页 save seam 捕获内容（不写真实文件）。
