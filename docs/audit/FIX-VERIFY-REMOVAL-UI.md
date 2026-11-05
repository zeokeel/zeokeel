# 删除 PTC / 语音输入 · UI 侧独立验证（FIX-VERIFY-REMOVAL-UI）

> 验证者：verifier-ui · 日期：2026-11-06 01:05–01:15 · 基线 `HEAD = d69d7d9`（工作树干净）
> 自建二进制：`-ldflags version=0.2.0-removal commit=d69d7d9`，sha256 `ef4b6898…`；Xvfb **:98 1600×1000x24** + `unshare -n` + `ZEOKEEL_DATA_DIR=/tmp/zk-rm/data`；xdotool/import；sqlite3 直查。
> 截图：`.cache/audit/verify-removal/shots/`（8 张）；grep 原始输出：`.cache/audit/verify-removal/evidence-grep.txt`。
> 说明：`task-32` 为 verifier-ui/verifier-core 共享任务，领取时已被 verifier-core 持有（claim 被拒），本文件是 UI 侧独立交付。

## 0. 结论摘要

UI 侧 4 项全部 **CONFIRMED**，无 PARTIAL/REFUTED：
1. **模式选择器只剩 Standard/Minimal/Creator**（会话 header 的「标准模式」菜单 3 项，无 PTC）；设置>代理预设页同样 3 项。
2. **插件页无语音输入**：插件页实验区仅 代理团队/自动授权审核/开发者工具 3 项；设置>内置插件实验区同样 3 项，无「语音输入」行/详情。
3. **旧 ptc 会话回退 Standard**：DB 注入 `session.<id>.mode=ptc` → 重启 → header 显示「标准模式」，菜单勾选「标准模式」，UI 不出现 PTC。
4. **无 PTC/voice 文案**：模式菜单/代理预设页/插件页/内置插件页截图均无 PTC/语音文案；`internal/shell` 全量 grep 无命中。
5. 代码级：`agentModeOptions()` 恰好 3 项；`controller.sessionModeFor` 对无法 `NormalizeMode` 的存量值回退 `ModeStandard`；`experimentalPluginDefs` 已无 voice_input。
6. 门禁：`go test -count=1 ./internal/shell ./internal/app` → **ok（shell 62.9s / app 2.6s）**。
- 残留检查：exact-word grep（`ptc|PTC|voice|Voice|语音`）仅命中守卫测试 `internal/app/ptc_voice_removal_test.go`；`internal/prompt/removed_preset_test.go` 按约定以字面量拼接 `"p"+"tc"`；宽泛 `-i ptc` 的其余 19 处非测试命中均为 `attempt*`/`prompt*` 子串假阳性（见 evidence-grep.txt）。

## 1. 核验矩阵

| 检查项 | 判定 | 真跑证据 | 代码/DB 证据 |
| --- | --- | --- | --- |
| 模式选择器 = Standard/Minimal/Creator | **CONFIRMED** | `02-mode-menu.png`：菜单恰 3 项「标准模式（勾选）/极简模式/创造模式」；`07` 为 ptc 回退后同一菜单 | `internal/shell/modes_ui.go:20-40 agentModeOptions()` 3 项；`i18n.go:478-482` 仅 3 组 modes.*.label |
| 插件页无语音输入 | **CONFIRMED** | `03-plugins-page.png` 实验区仅 3 行（代理团队/自动授权审核/开发者工具）；`04-settings-builtin-plugins.png` 实验区同样 3 行、内置 6 行无语音 | `internal/shell/plugins_ui.go:62-69 experimentalPluginDefs` 3 项（voice_input 已删） |
| 设置页无 PTC/voice 文案 | **CONFIRMED** | `05-settings-agent-presets.png`：仅 标准/极简/创造 三个预设卡片；模式菜单/插件页截图均无旧文案 | `grep -rniE "ptc|voice|语音" internal/shell --include=*.go` → 无命中 |
| 旧 ptc 会话回退 Standard 且 UI 不显示 | **CONFIRMED** | 注入 `session.e59584f9….mode=ptc` → 干净退出重启 → `06-ptc-fallback-restored.png` header 显示「标准模式」；`07` 菜单勾选标准模式、无 PTC 行 | `internal/session/controller.go:2661-2692`：`NormalizeMode(stored)` 失败 → `ModeStandard`；`runtime.NormalizeMode("ptc")` 返回错误 |
| grep 仅守卫测试残留 | **CONFIRMED** | evidence-grep.txt：exact-word 命中仅 `internal/app/ptc_voice_removal_test.go`；prompt 守卫用 `"p"+"tc"` 拼接（`internal/prompt/removed_preset_test.go:11`） | 宽泛 `-i` 命中 19 处非测试均为子串假阳性 |
| 回归门禁 | **CONFIRMED** | `go test -count=1 ./internal/shell ./internal/app` → `ok 62.926s` / `ok 2.560s` | — |

## 2. 复现步骤（可重跑）

```sh
# 建工作区/会话（UI），会话 header 点「标准模式」→ 截图 02（3 项）
# 插件页 → 03；Ctrl+, → 内置插件 → 04；代理预设 → 05
SID=$(sqlite3 /tmp/zk-rm/data/index.sqlite "select id from sessions limit 1;")
sqlite3 /tmp/zk-rm/data/index.sqlite "insert or replace into settings(key,value)
  values('session.$SID.mode','ptc');"
# 干净退出（更多→退出）→ 同 data dir 重启 → 06（header 标准模式）/07（菜单 3 项勾选标准）
grep -rnwE 'ptc|PTC|voice|Voice|语音' internal cmd --include='*.go'
go test -count=1 ./internal/shell ./internal/app
```

## 3. 观察（不阻塞）

1. **存量脏值保留**：回退是「读时归一」，DB 中 `session.<id>.mode=ptc` 不会被改写（重启后仍可查到）。对用户无可见影响（所有读取路径都会归一为 Standard），但建议后续用一次性迁移清掉旧值，避免长期脏数据。
2. 已验证范围：UI 层（菜单/页面/文案/存量会话回退）。rest/test 的运行时行为（registry 无 ptc、prompt 无 mode:ptc 等）属 verifier-core 的报告范围，未在本文重复。

## 4. 未验证 / 限制

1. 未用真实模型发起回合验证「ptc 回退后运行时无 ptc 工具」——属核心侧行为；本报告只验证 UI/持久化读取路径。
2. 未覆盖旧数据里其他潜在残留键（如 `agent.preset=ptc`）的 UI 默认高亮；已验证代理预设页只渲染 3 项，注入值不会显示旧文案。
3. 无 WM 的 Xvfb 环境；纯 UI 文案/菜单验证不受影响。
