# Round 3 UI 独立验证（FIX-VERIFY-ROUND3-UI）

> 验证者：verifier-ui · 日期：2026-11-06 01:36–02:30 · 基线 `HEAD = 20e98d4`（干净）
> 自建二进制：`git archive HEAD` → /tmp 纯源码树构建，`version=0.2.0-r3verify commit=20e98d4`，sha256 `49ba8cbb…`；Xvfb **1600×1000x24** + `unshare -n` + `ZEOKEEL_DATA_DIR=/tmp/zk-r3/data`；xdotool/import；python/Go fake OpenAI SSE provider（`/tmp/zk-r3-fake/fake-sse`，std 库，纯本机 127.0.0.1:18081）。
> 截图：`.cache/audit/verify-round3-ui/shots/`（19 张）。
> 限制：本轮 Xvfb 被**外部进程连续杀掉 2 次**（无 crash 日志），`xvfb-run` 兜底也因 display 竞争失败，导致 updater 页/FP-16 问题卡/GUI 删除回归的**第二次交互截图缺失**；FP-16 改为「运行官方 fake-provider e2e 测试 + UI 代码核验」，未独立驱动 GUI 问题卡。

## 0. 结论摘要

| 项 | 判定 | 说明 |
| --- | --- | --- |
| presets 设置页（列表/覆盖/Reload/坏文件 banner） | **CONFIRMED** | `02`：坏 `broken.json` → 红 banner「预设文件被忽略：…invalid character…」；Reload 后顶部 toast「预设重新加载失败…」；移除坏文件再 Reload →「预设已重新加载」、内置 3 个 + 用户预设 **Reviewer（用户文件 badge、工具 2、Model: review-model）**（`05`）；每行「设为默认」；管理预设文件三按钮（重新加载/打开预设目录/创建示例）（`02/03/05`） |
| presets 会话 header picker | **CONFIRMED**（未知回退 warning 未真跑） | `06` 1600 下 header 出现预设 chip「Standard」；点击 → `07` popover「会话预设」列 Standard(勾)/Minimal/Creator/Reviewer，Reviewer 行带「人设」摘要与 id「reviewer」；点 Reviewer → `08` chip 变「Reviewer」+ 顶部 toast「预设已设置，下一回合生效」；`14` 900×600 下 chip 隐藏（符合 `<1000px` 隐藏约定）。未知/删除预设回退 Standard 的**橙色 warning** 未构造真跑（代码 `presets_ui.go` 有 warning 渲染，属 PARTIAL 子项） |
| UX-14 主窗 toast 顶部带 | **CONFIRMED** | `08`/`13` 1600×1000：toast 在窗口**顶部带**（y≈10–50），composer 完全不被遮挡；`15` 900×600：同样顶部带、不遮 composer；`02/05/18` 设置窗保存/toast 也在设置窗顶部带。代码 `internal/shell/app.go` View 调用 `toastViewport`（Top+AlignCenter+`PassThrough()`）——Round2 我报告的「主窗压 composer」已修复 |
| FP-16 问题卡同回合回答/超时 | **PARTIAL** | 后端已实现并**测试通过**：`go test -run 'TestQuestionGate|TestAskUser' ./internal/app ./internal/runtime` → **ok**（2 个 gate e2e：问题卡→回答→同回合第二次模型调用收到 answered；timeout 降级 pending）；UI 代码 `todo_ui.go:142-230`（问题卡/选项 chips/输入/空回答本地拒绝）。但 GUI 端「真实 fake-provider 回合 → 问题卡渲染/点击 chips/同回合继续」未独立截图驱动（Xvfb 被杀 + 5min 超时超时盒）；标 PARTIAL |
| updater GUI | **PARTIAL** | 代码已接线：设置>更新页含「检查更新」，`defaultUpdateCheck` 无 feed 返回 `updater.ErrDisabled` 并在页面解释（`settings_update_hotkey.go:40-115`），异步检查状态文案存在；本环境未成功截到本轮更新页/点击态（Xvfb 被杀；上一轮 wave2 截图只有按钮）。Round4 若有独立 GUI 验收则以此为准 |
| 删除回归（模式 3 / 无语音） | **CONFIRMED（代码+前轮 UI 证据）** | `modes_ui.go` 恰 3 个 Mode；`plugins_ui.go experimentalPluginDefs` 3 项（无 voice_input），`20e98d4` 未回潮；`06` header 显示「标准模式」；本轮 GUI 抽查截图缺失（同 Xvfb 故障），沿用 Round1/2 真跑截图 |

## 1. presets 复现步骤（可重跑）

```sh
# <dataDir>/presets/reviewer.json = {id,name,tools:["read_file","grep"],persona,prompt,model}
# <dataDir>/presets/broken.json = 非法 JSON → 设置>代理预设 红 banner；Reload → 顶部失败 toast
# 删除 broken.json → Reload → 「预设已重新加载」+ Reviewer 行（用户文件）
# 会话 header 预设 chip（1600 可见；900×600 隐藏）→ 选 Reviewer → toast「预设已设置，下一回合生效」
```

## 2. UX-14 复现

```sh
# Session 输入 /clear → 主窗顶部带 toast；1600 与 900×600 均不遮 composer（08/13/15）
```

## 3. FP-16 证据

- 运行时 gate：`internal/app/question_gate.go`（5min 超时 → `ErrQuestionTimeout` 降级；回答作为同一 tool call 结果注入）。
- 官方 fake-provider e2e（本轮实跑）：`go test -count=1 -run 'TestQuestionGate' ./internal/app` → **ok 0.135s**；`go test -run 'TestAskUser' ./internal/runtime` → **ok 0.012s**（含 blocking 同回合继续 / timeout 降级）。
- UI：`internal/shell/todo_ui.go:142-230`；问题卡渲染于 composer 上方（`liveComposerArea` 先于 composer）。
- 未做：GUI 内真实回合触发 ask_user → 点选项 chips → 同回合继续 的端到端截图（见 §0 限制）；建议 Round4 在稳定 X 下补一次。

## 4. 发现/建议

1. **环境风险（非产品缺陷）**：本窗口内 Xvfb :98 被外部进程连杀 2 次（`/tmp/.X11-unix` 仅剩他人 X99）；建议 Lead 统一约束「只 kill 自己 PID」并给 Xvfb 加唯一 display/命名，否则后续 verifier 截图不可靠。
2. presets 未知回退的橙色 warning、updater GUI 检查更新点击态、FP-16 GUI 卡片交互 → 建议 Round4 补真跑（均已具备代码/测试基础）。
3. 其余无新缺陷；Round3 宣称的「UX-14 主窗顶部带 pass-through」「presets 端到端」「FP-16 阻塞 ask_user」在可验证范围内成立。

## 5. 未验证 / 限制

1. GUI 问题卡交互与超时撤回（5min 默认，超时盒；且未驱动真实回合）。
2. updater GUI 检查更新（无 feed 禁用提示/有 mock feed 可更新态）未截图；代码路径已核。
3. presets 未知/删除预设的橙色回退 warning 未真跑，仅代码核验。
4. 删除回归本轮 GUI 截图缺失（沿用 Round1/2）。
5. 真实模型未用；fake provider 已就绪但 GUI 回合未跑通（X 故障）。
