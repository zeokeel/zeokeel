# 反证审计报告（ADVERSARIAL-AUDIT）

- 日期：2026-11-08（Asia/Shanghai）· Go go1.27.3 linux/amd64 · 离线环境
- 任务：task-108「反证审计：已交付模块的负向/边界测试与缺陷报告」
- 范围：只新增 `internal/regression/**`（仅 `_test.go`，测试专用包）与本文件；**未修改任何生产代码**，未 commit。
- 方法：**反证法**。对每个已交付模块，不重复其正向单测，而是构造负向输入（空/非法/超长/恶意/并发/取消/panic）、故障注入（httptest 本地 server、坏文件、坏 JSON、失效 URL）与黑白盒断言；断言不变量（不 panic、不丢状态、不死锁、不越界、不误报）。真实缺陷用「已知缺陷探针」记录：缺陷存在时 `t.Skip` 并写明 AUDIT 编号，缺陷修复后同一用例自动转为通过断言。

## 修复状态（Lead 复核后）

> 首轮 11 个「已知缺陷探针」与第二轮 4 个 P3 均已随 TDD 修复转为 PASS；第三轮 3 个 P3 中 P3-9/10 已修复、P3-11 复核发现修复未落地（见第四轮）；第四轮新增 P3-12。`go test ./internal/regression -count=1 -race` 当前 **153 PASS / 0 SKIP**（历史：首轮修复后 92，第二轮后 98，第三轮后 133）。

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

测试规模：`internal/regression` 共 153 个顶层 Test（首轮 78 + 第二轮 20 + 第三轮 35 + 第四轮 20；含子用例后更多）。首轮/第二轮缺陷已修复；第三轮 P3-9/10 已修复，P3-11 复核为**声明修复但代码未落地**；第四轮新增 P3-12；当前 **153 PASS / 0 SKIP**。

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

第四轮回归：`internal/regression` **153 PASS / 0 SKIP**。仍有文档化未落地项（/feedback 斜杠命令、plan keep-planning 反馈循环、
会话级 feedback 汇总/导出）。
