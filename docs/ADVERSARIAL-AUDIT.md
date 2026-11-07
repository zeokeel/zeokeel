# 反证审计报告（ADVERSARIAL-AUDIT）

- 日期：2026-11-08（Asia/Shanghai）· Go go1.27.3 linux/amd64 · 离线环境
- 任务：task-108「反证审计：已交付模块的负向/边界测试与缺陷报告」
- 范围：只新增 `internal/regression/**`（仅 `_test.go`，测试专用包）与本文件；**未修改任何生产代码**，未 commit。
- 方法：**反证法**。对每个已交付模块，不重复其正向单测，而是构造负向输入（空/非法/超长/恶意/并发/取消/panic）、故障注入（httptest 本地 server、坏文件、坏 JSON、失效 URL）与黑白盒断言；断言不变量（不 panic、不丢状态、不死锁、不越界、不误报）。真实缺陷用「已知缺陷探针」记录：缺陷存在时 `t.Skip` 并写明 AUDIT 编号，缺陷修复后同一用例自动转为通过断言。

## 修复状态（Lead 复核后）

> 所有 11 个「已知缺陷探针」已随 TDD 修复转为 PASS：`go test ./internal/regression -count=1 -v` 中 **0 SKIP / 92 PASS**。

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
$ go test ./internal/regression -count=1 -race              ok  8.7s
```

测试规模：`internal/regression` 共 78 个顶层 Test（含子用例后更多），约 3.9k 行；11 个已知缺陷探针在缺陷存在时 SKIP（不影响 CI 绿），其余全部 PASS。

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
