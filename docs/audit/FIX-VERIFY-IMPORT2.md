# 独立验证 H：PD-04 导入器 wave 2 再验收（真实数据 / 幂等 / 质量）

> 验证者：product-auditor · 日期：2026-11-06 00:04–00:20 · 基线：HEAD `84c5b1f`（wave 2 提交；工作树仅 `docs/BUGS.md` 未提交）
> 方法：只读代码审查 + `go test ./internal/importer` + 真实 `/root/.dsh` 只读 dry-run / 隔离导入 + 受控 fixture（正常目录 / v5 header / 损坏 manifest）+ Xvfb GUI 复核导入会话
> 构建：`/tmp/zkv3/zeokeel`（`go build ./cmd/zeokeel` @84c5b1f）；所有导入写 `/tmp/zkv3/*` 隔离数据目录
> 证据：`.cache/audit/product-verify2/logs/`（dry-run/导入 JSON、测试日志）、`.cache/audit/product-verify2/shots/`；**未改代码、未 commit、未修改 `/root/.dsh`、未打印任何凭据值**。
> 注：DSH 服务在验证期间仍在写盘，因此两次 dry-run 的少量计数（tool calls/step_assistant ±2）会有微小波动，属数据源实时变化。

## 0. 结论摘要

- **修复主体验收 PASS**：真实 `/root/.dsh` dry-run 得到 **27 sessions / 1,248 messages**（修复前 17,219 messages，-92.8%），`images_dropped=25`，skips 分解 `{system_injected:2633, step_assistant:13409–13413, subagent:285, empty_session:13, images_dropped:25}`，与预期 ~1248 / ~2622 / ~13388 / 285 / 13 / 25 基本一致；0 failed；凭据仍只列键名。
- **质量验收 PASS**：隔离导入后 DB 中**系统注入上下文 = 0 条**、**合成 `[tool call]` 行 = 0 条**；会话标题保留、用户气泡为真实用户请求、assistant 为最终回复（GUI `shots/05` 可见干净 transcript）；图片丢失在报告/摘要中可见。
- **P1 BLOCKER（新发现）**：**幂等 manifest 在真实 DSH 目录布局上失效**。真实会话目录有两种命名：`<uuid>/`（285 个）与 `session-<uuid>/`（40 个）；`sessionDirName()`（`internal/importer/manifest.go:77-83`）把 `session-<uuid>` 目录误判为「session 文件直接在 bucket 下」，返回文件名 `session.v4.jsonl.zstd`，导致 27 个导入会话**全部映射到同一个 manifest key**。实测：首次导入后 manifest 只有 **1** 个条目；第二次导入仍 imported=27 → DB 变成 54 sessions / 2496 messages（**重复导入**）。控制组 fixture（目录 `sess-1/sess-2`）幂等正常（第二次 imported=0、`idempotent=2`、manifest 2 条）→ 根因可定位。
- **其余 PASS**：损坏 manifest fail-closed（exit 1、0 写入）；header v5 版本守卫（`unsupported_version=1`、exit 1、显式错误）；CLI `--manifest/--no-manifest` 互斥（exit 2）、`--no-manifest` 摘要；`go test ./internal/importer` 16/16 PASS（但测试 fixture 未覆盖真实 `session-<uuid>` 布局，正是漏掉该 bug 的原因）。
- **判定**：**「最小导入 CLI（技术预览）」的导入质量已达标**，可以按技术预览发布；但 **「重复运行跳过未变会话」的幂等声明在真实 DSH 数据上不成立**，`docs/BUGS.md` PD-04 的 ✅ DONE 与 README「默认幂等」需在修复 X1 后再确认。

## 1. 真实数据 dry-run 复跑（只读）

```sh
/tmp/zkv3/zeokeel import --from /root/.dsh --dry-run --json
/tmp/zkv3/zeokeel import --from /root/.dsh --dry-run
```

| 指标 | 本次（84c5b1f） | 上次（da12d44，FIX-VERIFY-PRODUCT §2） | 预期 | 结论 |
| --- | --- | --- | --- | --- |
| sessions_found / imported / skipped / failed | 325 / 27 / 298 / 0 | 325 / 27 / 298 / 0 | 27 | ✅ |
| messages_imported | **1,248** | 17,219 | ~1248 | ✅ |
| tool_calls / results seen | 16,883 / 17,094 | 16,815 / 17,026 | — | 实时数据波动 |
| images_dropped | **25** | 无字段 | 25 | ✅ 可见 |
| skips.system_injected | 2,633 | 无字段 | ~2622 | ✅ |
| skips.step_assistant | 13,409–13,413 | 无字段 | ~13388 | ✅ |
| skips.subagent / empty_session / images_dropped | 285 / 13 / 25 | 无分解 | 285 / 13 / 25 | ✅ |
| credentials | `DEEPSEEK_API_KEY`（仅键名） | 同 | — | ✅ 无值泄漏 |
| workspaces | 5（messages 59/509/98/351/231） | 5 | — | ✅ |

- 人类摘要包含 `manifest:` / `images:` / `skips:` 行（`logs/dryrun-human.txt`），JSON 含 `skips`/`images_dropped`/`workspaces`/`credentials`。
- dry-run 不写 manifest、不写库（对照 `TestImportManifestDryRunDoesNotWrite`）。

## 2. CLI 行为

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| 人类摘要展示 manifest 默认路径 | ✅ `manifest: <data dir>/dsh-import-manifest.json (repeated runs skip unchanged sessions)` | `logs/dryrun-human.txt` |
| `--no-manifest` | ✅ `manifest: disabled (--no-manifest): every run re-imports all sessions` | 同 |
| `--manifest` + `--no-manifest` 互斥 | ✅ exit 2，`--manifest and --no-manifest are mutually exclusive` | `logs/flags-conflict.err` |
| `--json` 字段 | ✅ skips/images/manifest 相关字段齐全（manifest 路径仅在人类输出） | `logs/dryrun-full.json` |
| manifest 文件权限/原子写 | ✅ 0600 文件、0700 目录、tmp+fsync+rename（代码审查 `manifest.go:save`） | `stat` 输出 600 |
| 凭据值泄漏 | ✅ 全程未出现 `sk-`/值（grep 断言） | 同 |

## 3. Manifest / 版本守卫（受控测试）

| 用例 | 结果 | 结论 |
| --- | --- | --- |
| **真实布局幂等**（`/tmp/zkv3/data2`） | 第一次导入 27 → manifest **1** 条；第二次导入 **27**（应为 0）→ DB 54 sessions / 2496 messages | ❌ **P1 BUG**：key 碰撞（见 §3.1） |
| 控制组 fixture `sess-1/2` | 第一次 2 imported、manifest 2 条；第二次 **0 imported**，skips `idempotent=2` | ✅ 幂等逻辑本身正确 |
| 损坏 manifest（非法 JSON） | exit 1，`importer: import manifest is invalid: … invalid character`；导入前后 sessions 均 54（fail-closed，0 写入） | ✅ |
| header v5（`session.v4.jsonl` 内 `"version":5`） | `skips.unsupported_version=1`、exit 1、错误含 `unsupported DSH session version … header version 5` | ✅ |
| v5 文件名 `session.v5.jsonl` | 未被发现（`sessionFileIn` 只认 v3/v4/通用名）→ sessions_found=0 | 可接受（DSH 当前仍写 v4 名） |
| dry-run 不写 manifest | ✅（单测 `TestImportManifestDryRunDoesNotWrite`） | ✅ |

### 3.1 根因与修复建议（幂等 P1）

- 真实 DSH 目录（325 个）：`<uuid>/` 285 个、`session-<uuid>/` 40 个（`find … | sed` 统计）。
- `internal/importer/manifest.go:77-83`：

```go
func sessionDirName(sessionFile string) string {
    dir := filepath.Base(filepath.Dir(sessionFile))
    if strings.HasPrefix(dir, "session.") || strings.HasPrefix(dir, "session-") {
        return filepath.Base(sessionFile)   // 真实 DSH 的 session-<uuid> 目录也命中这里
    }
    return dir
}
```

- 结果：所有 `session-<uuid>` 会话的 key 都是 `<home>|session.v4.jsonl.zstd`；`manifest.Sessions` 是 map，后写覆盖前写 → 27 个导入会话只留 1 条；下次运行除最后一条外的会话 hash 都不匹配 → 全部重新导入。
- 建议修复（S/M）：key 改为 **`filepath.Rel(dshHome, sessionFile)`**（或 session 文件相对 `sessions/` 的路径），与目录命名约定解耦；补一个真实布局 fixture（`session-<uuid>/session.v4.jsonl.zstd`）的幂等测试；`TestRunImportCLIManifestIdempotency` / `TestImportManifestIdempotent` 当前只覆盖 `sess-*` 目录，是本次漏检原因。
- 顺带：`--include-subagents` 的 312 会话同样受影响（同一 key 规则）。

## 4. 导入后会话质量（隔离导入 + GUI）

- 隔离导入 `/tmp/zkv3/datagui`（`--max 6`）：2 sessions / 59 messages（27 assistant + 32 user）/ images_dropped=2；对比修复前同批数据 704 messages。
- DB 质量断言：
  - `content LIKE '%Current runtime context%' OR '%Time sampled%' OR '%The approval policy changed%'` → **0**（修复前会话可见假用户气泡）；
  - `content LIKE '%[tool call]%'` → **0**（修复前 610/704）；
  - 标题保留（「部署 Plot 并替换 AI 服务」「移动优先的AI小说创作工具」）。
- GUI（Xvfb 1600×1000，`shots/05-imported-session.png`）：标题/workspace 正确，用户气泡=真实请求，assistant=完整 Markdown 最终回复（含代码/加粗/标题渲染），无系统注入与 step 噪音；`shots/06` 为滚动后内容。
- 已知残余（非本轮新问题）：导入会话无 events → Trajectory/Usage/turn 终态/tool 卡仍为空；images/attachments/trajectory 不导入（已在 CLI 帮助与 README 声明）。

## 5. 剩余缺口 / 发布判定

| 项 | 状态 | 建议 |
| --- | --- | --- |
| **X1 manifest key 碰撞（真实 DSH 布局幂等失效）** | ❌ P1 未修 | 按 §3.1 改 key + 真实布局回归测试；修复前不要在 README/BUGS 声明「默认幂等/重复运行跳过未变会话」 |
| X2 文档数字/状态同步 | ⚠️ 文档过时 | `docs/BUGS.md` PD-04 写「325 sessions/17223 messages」（旧数字，现 1,248）；README 称「设置页 UI 入口待 wave3」，但 `settings_extra` 已有导入入口（`TestImportFromDSHDialog`）；需按实际状态更新 |
| X3 运行中实例检测 | ❌ 未做 | CLI import 仍只有帮助文案提示；建议检测 `instance.lock` 并拒绝/警告（P2，任务已列为已知剩余） |
| X4 真机验证 | 未验证 | macOS/Windows 文件锁/路径（P2，无真机） |
| X5 版本守卫/损坏 manifest | ✅ | 已 fail-closed；v5 文件名暂不识别可接受 |
| 质量（注入/step/图片统计） | ✅ | 系统注入与 step 噪音清零；图片丢失可见 |

**判定**：
- **「最小导入 CLI（技术预览）」：质量达标、可发布**（文档需按 X2 更新实际数字与 UI 状态）。
- **「默认幂等 manifest」：当前在真实 DSH 布局上不成立（X1）**，`docs/BUGS.md` PD-04 的 ✅ DONE 应改为「DONE（幂等修复中/待复核）」或修复后再确认；这是本次再验收唯一阻断性发现。
- 「迁移器」定位：仍不成立（按 FIX-VERIFY-PRODUCT §2.6 的 X1–X5 处理），文档口径已降级，维持。

## 6. 证据索引

- `.cache/audit/product-verify2/logs/`：`dryrun-full.json`、`dryrun-human.txt`、`import1/2`、`import-idempotent.json`、`idem-run1/2.json`、`import-corrupt-manifest.err`、`version-guard.json|.err`、`fixture-run1/2.json`、`gui-import.json`、`importer-tests.txt`、`flags-conflict.err`
- `.cache/audit/product-verify2/shots/`：`01-imported-home.png`、`05-imported-session.png`、`06-imported-session-scrolled.png`
- 补充方法/命令：`.cache/audit/product-verify2/evidence-notes.md`
