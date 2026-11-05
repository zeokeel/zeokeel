# 独立复核报告（verifier-core）：Round 3 核心侧验证

> 复核者：verifier-core · 日期：2026-11-06 · 基线：**`20e98d4`（Round 3, HEAD，工作树干净）**；验证在 `git archive HEAD` 干净快照 `/tmp/verify-round3` 内进行（不改代码/不 commit）。
> 方法：干净快照 `go build/vet/test ./...` + **7 个自写独立探针**（AR-22 随机等价、AR-07/09 迁移、presets 端到端、FP-16 app/desc、updater/ACP/hotkey 运行级）+ 作者测试交叉复跑 + 真二进制 CLI/ACP stdio e2e + AR-22 benchmark。
> 证据：`.cache/audit/verify-round3/`（`build-test.log`、`ar07-09-probe.log`、`presets-fp16-probes.log`、`author-tests-bench.log`、`updater-cli-e2e.log`、`acp2.*`、探针源码）。环境：Go 1.27.3、`CGO_ENABLED=0`。

## 0. 结论摘要

| 领域 | 判定 | 关键证据 |
| --- | --- | --- |
| 门禁 | **CONFIRMED** | 干净快照 `go build ./...`/`go vet ./...` OK；`go test -count=1 ./...` **26 包全绿**（exit 0） |
| presets 端到端 | **CONFIRMED** | 独立探针：用户 JSON 加载→tool/persona/model 解析、unknown ID 拒绝、坏 JSON/未知工具 reload fail-closed 且旧 registry 保留、未知持久化 ID→fallback+warning、DeleteSession 清理选择；作者 app e2e 6/6 PASS（含 zero-match 空集 fail-closed、reload 生效） |
| FP-16 | **CONFIRMED** | 独立 app e2e（真实 SSE）：第二轮 provider 请求逐字段断言 `role=tool`+`tool_call_id`+`{"status":"answered","answer":"postgres"}`、同回合 idle、无 user 消息泄漏；描述契约探针 PASS；作者测试 9/9 PASS |
| AR-07/AR-09 | **CONFIRMED（附 1 个 P3）** | 独立迁移探针：v1→v2 升级、0600 备份、ordinal 回填/排序/续号、幂等重开、失败步骤回滚到 v1 且有备份、`schema_version` 0/99 拒绝（TooOld/TooNew）。**P3：同 `created_at` 消息的 tie-break 由 legacy `rowid` 变成 `id`**（见 §3.1） |
| AR-22/AR-20 | **CONFIRMED** | ApplyBatch vs 顺序 Apply：400 组随机序列 + 随机分块 + nil/单事件/错误溢出/不可变性全部 `reflect.DeepEqual`；benchmark（1 iteration）：apply_loop **1,371.9ms/op，30,066 allocs** vs batch **1.84ms/op，35 allocs**（≈746x，与 commit "~2ms vs ~1.2s、35 vs 30k" 相符）；AR-20：`agent.run` 48 行（声称 48）、`cmd/zeokeel/cli.go` 72 行（声称 72），runtime 全测试绿无回归 |
| updater CLI | **CONFIRMED** | 真二进制 e2e：无 feed→exit 1；http feed 默认被 https-only 拒绝（exit 1，JSON error 含 "must use https"）；`ZEOKEEL_UPDATE_ALLOW_HTTP=1` → `update_available` exit 0；`--download` → `downloaded`+staged 文件落 `<data>/updates/`；`--help` exit 0、未知 flag exit 2 |
| ACP e2e | **CONFIRMED** | 无模型时优雅报错 exit 1（stderr 清晰，无 stdout 污染）；配置 provider env 后真进程 stdio e2e：`initialize`+`session/new` 均返回合法 JSON-RPC（stdout 2 行合法 JSON、exit 0、诊断只在 stderr） |
| hotkey 接线 | **CONFIRMED** | `TestGlobalHotkeySettingWiring`、`TestGlobalHotkeyUnsupportedPlatformDegrades`、`TestHotkeyString` 全 PASS；Linux/macOS unsupported 降级路径有测试与文档 |

**未发现 P0/P1 缺陷**；1 个 P3（AR-07 同时间戳 tie-break）见 §3.1，1 个观察项见 §6.2。

## 1. presets 端到端（探针 `zz_verify_round3_presets_test.go`，pass）

在 controller 级独立构造：`session.New(Config{PresetKnownTools: {read_file,bash,write_file}})` + 临时用户预设文件（tools 子集/persona/model）：

- 构造时加载用户 preset；`SetSessionPreset` 持久化选择；`ResolvePreset` 返回该 preset 且无 warning；`SessionPresetModel`="vc3-model"。
- `SetSessionPreset("no-such")` → `presets.ErrNotFound`，原选择不变。
- 写入坏 JSON → `ReloadPresets` 返回 `ErrInvalidJSON`，**旧 registry 仍可用**（fail-closed），`LastPresetLoadError` 非 nil；修复后 reload 成功并清错误。
- 写入含未知工具 → `ErrUnknownTool`（KnownTools 校验）。
- `DeleteSession` 后 `SessionPresetID` 为空、setting 清理；持久化一个不存在的 preset ID → `ResolvePreset` 返回 `standard` + 非空 warning。
- 作者 app e2e 交叉复跑：`TestBuildAgentAppliesSessionPreset`、`TestBuildAgentUnknownPresetFallsBackToStandard`、`TestPresetReloadAppliesToNextTurn`、`TestFilterRegistryByPresetFailsClosedOnZeroMatch`、`TestBuildAgentPresetZeroMatchFailsClosedAndWarns`、`TestBuildServicesRejectsBrokenUserPreset` → **6/6 PASS**。

## 2. FP-16（探针 `zz_verify_fp16_*.go` + 作者测试，pass）

- 独立 app e2e：ask_user 阻塞→问题卡发布→`AnswerQuestion("call-ask","postgres")`→第二轮 provider 请求含 `role:"tool"`、`tool_call_id:"call-ask"`、content `{"status":"answered","answer":"postgres"}`；无 user 消息泄漏；卡片清空；同一回合 2 次 provider 调用后 idle。
- 描述契约：真实 `Description()` 同时含 answered（interactive/same-turn）与 pending（headless/ACP/subagent/timeout degrade），无旧 "stop the turn" 断言；pending 条款与工具真实返回（`kind=question,status=pending`）一致。
- 作者测试：`TestQuestionGateAnswersWithinTurn`、`TestAskQuestionBlocksUntilAnswered/CancelsOnContext/DeadlineTimeout`、`TestApproveAlwaysWithPerSessionGatesOnly`、`TestAskUserDescriptionMatchesBothModes` → **9/9 PASS**。

## 3. AR-07 / AR-09（探针 `zz_verify_round3_ar0709_test.go`）

独立构造 v2 索引→降级为 v1（保留数据）→ `Open` 升级，验证：`schema_version=2`、`schema_migrations` v2 行=1、`backups/index-v1-to-v2-*.sqlite` 存在且 0600、ordinal 按 legacy 顺序回填且密集、升级后新消息续号、重开幂等（不重复迁移/不新增备份）、注入迁移失败→回滚到 v1 且备份仍在、`schema_version=99`→`ErrSchemaTooNew`、`0`→`ErrSchemaTooOld`。legacy 排序从 `8e2ba5b:internal/store/message.go:80` 确认是 `ORDER BY created_at, rowid`。

### 3.1 [P3] AR-07 迁移对同 `created_at` 消息的 tie-break 与 legacy 不一致

- 证据：`internal/store/migrations.go:189` 回填用 `ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at, id)`；legacy 排序是 `ORDER BY created_at, rowid`（旧 build 的 ListMessages）。独立探针：插入同 `created_at` 的两条消息（插入序 `zzz-message` → `aaa-message`），升级后顺序变为 `[aaa-message zzz-message]`（id 序），而 v1 显示序应为插入序。
- 影响：仅当同一会话存在完全相同的 `created_at`（时钟精度内批量写入等）时，升级后这两条消息的显示顺序可能变化；不影响内容/归属/持久化，属显示顺序 P3。
- 建议（owner parity-auditor，S）：回填改为 `ORDER BY created_at, rowid`（window 函数内可用 `rowid`）；`ListMessages` 的 `ORDER BY ordinal, id`（v2 路径）保持不变即可。探针 `TestVC3AR07SameTimestampTieBreak` 保留为回归；修复后该探针应转绿。
- **2026-11-06 01:41 更新：已按建议修复（未提交 worktree）。** verifier-core 在「HEAD `20e98d4` + 仅 `internal/store/migrations.go`/`migrations_test.go` 改动」隔离快照复验：diff 为 `ORDER BY created_at, rowid`+注释；我的 tie-break 探针 **PASS**（修复前 FAIL，证据 `ar07-09-probe.log`）、完整 AR-07/09 探针无回归、parity 回归测试 PASS、`internal/store` 全绿 1.084s、`go build` OK → **P3 CONFIRMED FIXED（待提交，提交时两文件一起 add）**。残余：已被 round3 旧构建（id tie-break）升级过的开发库不会自动重排；该版本未发布，无用户影响。

## 4. AR-22 / AR-20

- **AR-22 等价性（独立探针 pass）**：400 组随机事件序列（Token/Reasoning/ToolStart/ToolEnd/Status/TurnEnd/Error/Usage/ApprovalRequest/WorkspaceChanges/nil/未知事件）+ 3 种 base + 随机 chunk 切分，`ApplyBatch` 与顺序 `Apply` 逐字段 `reflect.DeepEqual`；另覆盖 `ApplyBatch(prev,nil)==prev`、全 nil、nil prev 空批、>100 错误溢出、prev 不可变性。
- **Benchmark（1 iteration，`BenchmarkApplyTokenStream`）**：`apply_loop_reference` 1,371,910,097 ns/op、30,066 allocs；`batch_10k` 1,838,228 ns/op、35 allocs（≈746x 加速，与 commit "700x / 35 vs 30k allocs" 相符）；`projector_bursts_256` 15.65ms/op、875 allocs（与 Projector 256 批一致）。
- **AR-20**：`agent.run` 拆分后 48 行；`cmd/zeokeel/cli.go` 72 行（+`commands.go` 37、`usage.go` 26），与 commit 声明一致；runtime 包全测试绿 → 拆分无行为回归（全量 26 包绿）。

## 5. updater CLI / ACP e2e / hotkey 接线（真二进制 + 作者测试）

- 构建 `go build -o /tmp/zk3/zeokeel ./cmd/zeokeel`（命令中用 `/tmp/zk3/zeokeel`；此前 `/tmp/zk3` 目录名导致一次误报，已修正）。
- **updater CLI**（`ZEOKEEL_DATA_DIR` 隔离 + 本地 HTTP feed/asset 含正确 SHA-256）：见 §0 行；`--json` 输出均为单个合法 JSON（stderr 另有日志）；`updates/` staged 文件内容与 asset 一致（24B）。
- **ACP e2e**：无模型时 exit 1、错误"headless runtime: no model is configured…"清晰且 stdout 无输出（protocol-only 纪律保持）；设 `DEEPSEEK_API_KEY/ZEOKEEL_PROVIDER_BASE_URL/ZEOKEEL_PROVIDER_MODEL` 后，`initialize` 与 `session/new` 返回 2 行合法 JSON-RPC（result 含 protocolVersion/agentInfo/sessionId），exit 0，诊断只在 stderr。
- **hotkey**：作者测试 PASS；Linux/macOS unsupported 降级有实现与文档（Windows 真实现仅静态/交叉编译验证，见 §7）。

## 6. 发现与观察

1. **[P3] AR-07 tie-break**（§3.1，owner parity-auditor）——建议顺手修：`ORDER BY created_at, rowid`。
2. **观察（非缺陷）**：ACP 无模型时错误链为 `app: acp adapter: app: headless runtime: no model…`（多层前缀略啰嗦，但 exit 1 且不污染 stdout，符合"无 UI 无模型"降级预期）。
3. **观察（非缺陷，历史遗留）**：CLI 启动时 Go 日志会打印 sonic/Go1.27 兼容 WARNING 到 stderr（不影响 stdout/JSON，既有 AR-10 依赖风险）。

## 7. 未验证 / 残余

1. presets picker 的**视觉/交互**（UI 侧）归 verifier-ui；本报告只做代码/测试级与 session/app API 级。
2. Windows 真机 hotkey 注册（RegisterHotKey）未运行（无 Windows）；仅 `GOOS=windows build/vet` 属作者声明，本报告未重跑交叉编译。
3. updater `--install` 真替换运行中二进制未执行（staged 已验证；replacer 由 updater 单测与 Round2 对抗探针覆盖）；真实 HTTPS 证书链/签名未验证（发布基础设施外）。
4. ACP prompt 真跑需模型 endpoint；本报告只验证 initialize/session-new stdio 与无模型降级。
5. 迁移备份只验证 v1→v2 单步；未来多步迁移路径按框架设计推断（当前只有 1 步）。

## 8. 证据索引（`.cache/audit/verify-round3/`）

- `build-test.log`：干净 20e98d4 快照 build/vet/26 包测试。
- `ar07-09-probe.log`：AR-07/09 探针（主 PASS + tie-break P3 FAIL 证据）。
- `presets-fp16-probes.log`：presets 端到端 + FP-16 两个独立探针。
- `author-tests-bench.log`：presets/hotkey 作者测试、AR-22 benchmark、AR-20 行数。
- `fp16-author-tests.log`：committed FP-16 作者测试 9 条。
- `updater-cli-e2e.log`、`acp2.out`/`acp2.err`：真二进制 e2e。
- 探针源码：`zz_verify_round3_{ar22,ar0709,presets}_test.go`、`zz_verify_fp16_{spotcheck,desc}_test.go`（可拷回对应包复跑）。
- `README-evidence.txt`：索引与基线说明。
