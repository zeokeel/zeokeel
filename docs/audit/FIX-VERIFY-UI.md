# wave 1 UI/UX 修复独立复核（FIX-VERIFY-UI）

> 复核者：verifier-ui · 日期：2026-11-05 · 复核窗口 23:30–23:52
> 代码基线：`HEAD = 6da09e3`（`1da4f12` wave1 + docs）+ **未提交的 wave2 工作树**（复核期间持续变动，见 §4）
> 验证二进制（自建，均带版本注入）：
> - `verify2` @23:31 ≈ wave1 提交态（UX-04 修复前），用于大部分 UI 项；
> - `verify3` @23:36 `sha256 dd24e2d4…`（含 UX-04 refreshWindows 修复）；
> - `verify4` @~23:45 `sha256 70d1a4d9…`（含修复；`internal/app/settings_window.go` mtime 23:34 < build，确认编入）→ 专门复验 UX-04。
> 环境：Xvfb **1600×1000x24**（:98，被外部杀后改 :97）+ `unshare -n` + `ZEOKEEL_DATA_DIR=/tmp/zk-fix-verify/data` + xdotool/import；截图 76 张：`.cache/audit/verify-ui-fix/shots/`（引用 `../../.cache/audit/verify-ui-fix/shots/`）。
> 限制：UX-04 的「关闭设置窗」路径未能在被杀前复测；真实 WM 下的行为未验证（本环境无 WM）；PD-02 预设下拉只验证存在/默认自定义，未逐项点选；未用真实模型。

## 0. 结论摘要

- 15 项复核结果：**CONFIRMED 13 · PARTIAL 2 · REFUTED 0**。wave1 UI/UX 批次主体**真实落地**，键盘路径静默/空会话污染两个核心缺陷已修复（截图+DB 直查证实）。
- **UX-04 仍为 PARTIAL（未真正修好）**：state 清理生效（主窗口下一次输入后 popover 消失），但**打开/移动设置窗后主窗口不重绘**，残留 popover 仍可见（本环境可复现）；`refreshWindows()` 的 `win.Invalidate()` 不足以强制绘制（疑似 mygo `holdFrame`/窗口遮挡时不出一帧）。详见 §2.1，已上报 lead。
- **About 版本 PARTIAL**：已显示真实 build 串，但其上一行仍保留硬编码 `version 0.1.0`（`i18n.go:45`），两行版本自相矛盾，建议删旧行。
- 其余 CONFIRMED 项均有真跑截图：UX-01/03/PD-01（toast+回滚+保留 draft）、UX-02、PD-02、PD-09、PD-11、PD-17/UX-05、PD-18、UX-06（840 不切行）、UX-08、UX-09、UX-10、UX-11、UX-13、PD-08。
- 无「截图方法学假象」误报；1600×1000 下 composer/footer 完整；设置窗底部裁切修复用独立 X 窗口直抓验证。

## 1. 判定矩阵

| 项 | wave1 声称 | 判定 | 复核证据（本次真跑） | 备注/代码 |
| --- | --- | --- | --- | --- |
| UX-01/UX-03/PD-01 | 键盘 Enter 失败 → deferred toast；回滚空会话；保留 draft/附件 | **CONFIRMED** | 无模型 Home Enter：`19`（1.0s toast「尚未配置 Agent…」）、`18/20`（0.2/3.5s）；停留 Home、draft `hello fix verify` 仍在；DB `sessions=0 messages=0`（修复前 sessions=1）。Session 内 Enter：`25` toast + draft `session draft` 保留。附件路径未独立跑通（`/file` 键盘路径未产生可见 chip，见 §4） | `home.go:395-403 rollbackHomeSession`、`toastDeferred`（`composer_menus.go:384-402`）、`session_live.go:879-886` |
| UX-02 | 会话空态 + 无模型 CTA | **CONFIRMED** | `24-new-session-empty-state.png`：图标+「开始对话」+提示+红字「尚未配置模型，添加后才能发送第一条消息。」+按钮「在设置中添加模型」 | `session_live.go:345-353`、`i18n.go:348` |
| PD-02 | Provider 预设 + 测试连接 | **CONFIRMED** | `39`（服务商下拉「自定义」+ 测试连接按钮）、`42`（失败态红字「连接失败：Get http://127.0.0.1:9/v1/models … connection refused」）、`43`（保存成功）；DB `model.default` 落库 | `models_ui.go:55-69`（providerPresets/probe）、`:453+`（modelProviderField） |
| PD-09 | 失败回合分类卡/重试/技术细节 | **CONFIRMED** | `55`：卡片「无法连接模型服务」+原因「请求未能到达服务商。请检查网络、代理与基础 URL，然后重试。」+「重试」「技术细节」；`56`展开后显示原始 NodeRunError 栈 | `session_errors.go:32+`（classify）、`:70+`（copy/actions） |
| PD-11 | Home 返回入口 | **CONFIRMED** | 侧栏新增「首页」`01/27`；折叠 rail 的 Home 图标点击后回到 Home `61` | `workspaces.go:174-189 goHome`、`chrome.go:161` |
| PD-17/UX-05 | 工作区 root 校验；对话框可见 label | **CONFIRMED** | `14`：可见 label「工作区名称」「根目录」；`16`：不存在目录 → 红字「该目录不存在。勾选「创建该目录」即可创建。」+「创建该目录」勾选框，勾选后可创建（`17`，workspaces=1） | `workspaces.go:1254/1356` |
| PD-18 | 删除模型二次确认 | **CONFIRMED** | `59`：弹窗「删除模型？ 删除「Dummy」？正在使用它的会话会回退到下一个默认模型。」取消/删除模型；点取消未删除 | `models_ui.go:618 modelDeleteDialog` |
| UX-04 | 打开设置窗清理 popover、关窗后无残留 | **PARTIAL（仍复现）** | `73` popover 开 → Ctrl+, → 把设置窗移开 `74`：popover **仍在**；点击主窗口空白后 `75` 才消失（说明 state 已清、只是不重绘）。修复前同序列行为相同（`44-47`/`48-50`）；隐藏路径未复测 | `settings_window.go:261-286`（Open/Hide→`refreshWindows()`）；`settings_window_test.go:321 TestSettingsWindowShowHideRepaints`（仅断言 fake 窗口 Invalidate 计数） |
| UX-06 | 设置窗 840 首屏不切行 | **CONFIRMED** | 独立窗口直抓 `04-settings-window-900x840.png`（900×840）：末行「性能与用量」完整、下方「高级」标题可见，无半行裁切 | `settings_window.go:218-224`（Height 840） |
| UX-08 | 折叠 rail 会话 flyout | **CONFIRMED** | `30`：rail 点工作区图标 → flyout 显示 workspace 名、会话行「新建会话 刚才」、动作「打开工作区/在此新建会话」 | `chrome.go:178/207 railWorkspacePopover/railWorkspaceMenu` |
| UX-09 | 侧栏工作区空态 | **CONFIRMED** | `01`：「暂无工作区。添加一个工作区来归档会话。」+「添加工作区」按钮 | 侧栏空态 |
| UX-10 | @ 会话行本地化 | **CONFIRMED** | `26`：行标签「会话 · 新建会话」，尾部相对时间「刚才」（同名会话可区分） | `composer_menus.go:618-625` 行 label 用 `menuText("Session · ","会话 · ")` |
| UX-11 | 窄窗权限 chip tooltip | **CONFIRMED** | 900×600（`31/32`）：chip 退化为图标+箭头；hover 显示 tooltip「访问模式：工作区写入」（`34b` 放大） | `home_live_menus.go:184` |
| UX-13 | 右栏 Start 页整治 | **CONFIRMED** | `35`：内容顶部对齐、无装饰圆形 sparkle，直接是「工作区工具」+3 卡片 | `start_tab.go` 相关 |
| PD-08 | 插件页 6 项一致 + 实验标注 | **CONFIRMED** | `36`：Plugins 页内置 6 项（子代理/网络搜索/代理循环/Shell 工具/技能/MCP 服务）与 Settings 一致；实验 4 项带「实验性」「未实现」标签 | `plugins_ui.go` |
| About 版本 | 版本显示接线 | **PARTIAL** | `60`：已显示 `0.2.0-w2verify2 (commit 6da09e3, built …)`；但上一行仍为硬编码「version 0.1.0」，同一页面两个版本号 | `settings_extra.go:246` 用 `BuildVersionString()`；`i18n.go:45 about.version` 未删 |

## 2. 关键复现细节

### 2.1 UX-04（唯一未修好的项，附可复现步骤）

1. Home composer 点「标准模式」chip → popover 打开（`73`，同一位置可复现于 `67`）；
2. `Ctrl+,` 打开独立 Settings 窗口（900×840）；
3. `xdotool windowmove <settings_wid> 1300 80` 把设置窗移开 → 主窗口 popover 仍绘制在原位（`74`）；
4. 在主窗口空白处点一下 → popover 消失（`75`），且权限 chip 未变（说明 closeComposerPopovers 的 state 清理是生效的，缺的是重绘）。

**判断**：刷新请求已发出但主窗口那一帧没有绘制。最可能原因：设置窗口在上层/主窗口失焦时 mygo 的 `holdFrame` 跳过帧（`rt.input==false` 且窗口被认为遮挡），`win.Invalidate()` 等不到出帧；只有主窗口收到输入事件（`rt.input=true`）才强制出一帧。**建议修复方向**：在 show/hide 后触发一次「不可被 hold」的重绘（例如 `win.Update(func(){})`/显式 input 触发的 repaint，或评估 mygo 是否有 `RequestFrame`/取消遮挡的公开 API）；并在 Xvfb+无 WM 环境用上述 xdotool 序列做回归；补的测试若只断言 fake 窗口 Invalidate 计数，无法覆盖本 bug（当前 `TestSettingsWindowShowHideRepaints` 即如此）。

### 2.2 其他复现要点

- 无模型发送：`xdotool type 'hello fix verify'` → `Return`（首次弹添加工作区，见 `14`）；建 workspace 后 `Return` → `19` toast、保持 Home、draft 保留；`sqlite3 … "select count(*) from sessions; select count(*) from messages;"` → `0/0`。
- PD-09：Settings>模型 填 `Dummy / http://127.0.0.1:9/v1 / dummy` 保存（`43`）→ 新会话发送 → provider 重试 5 次后失败（日志可见 retry 5/5）→ `55/56` 分类卡与折叠技术细节。
- 设置窗计量：`xwininfo -root -tree` → `"设置" 900x840+350+80`（修复前 900×720 末行被切半）。

## 3. 发现的问题/建议（按严重度）

1. **UX-04 PARTIAL（P2，未修好）**：如 §2.1；wave1 报告「popover 生命周期」应改为 PARTIAL/OPEN。已上报 lead。
2. **About 版本自相矛盾（P3）**：`i18n.go:45` 的 `"about.version": {"version 0.1.0",...}` 仍渲染，与真实 build info 并存；建议删除该 i18n 行或改用 `BuildVersionString()`。
3. **环境备注（非修复缺陷）**：复核期间 Xvfb :98 与 app 被外部进程**连续杀掉两次**（无 crash 日志），疑为他人清理时误杀；建议 lead 提醒所有成员只用精确 PID 停自己的进程。复核改用持久终端持有的 Xvfb :97 后稳定。
4. 观察（不在本轮范围）：Settings>模型空态仍有**两个「添加模型」主按钮**（`38`），UX-07 双主按钮未修，保持 master 的 P2。

## 4. 未验证 / 限制

1. UX-04 的隐藏（关设置窗）路径：修复后未复测（杀掉前只验证了显示路径）；真实 WM（有合成器/焦点管理）下是否也复现未验证——本环境无 WM，但这是 team 约定的审计环境。
2. 附件保留：键盘 `/file` 路径未跑出可见 attachment chip（命令被拼进已有 draft 导致「没有匹配的命令」）；仅有代码+单测（`send_feedback_test.go TestKeyboardSendFailureRestoresHomeAttachments`），未独立复现。
3. PD-02 预设下拉仅确认存在且默认「自定义」，未逐项选择 DeepSeek/OpenAI/Ollama 验证自动填充。
4. 未用真实模型端点；PD-09 分类只验证了 network 一类（auth/429/context/model 分类仅代码）。
5. 工作树在复核期间持续变动（wave2 并行），`verify4` 为某一时刻快照；UX-04 结论基于该快照 + 修复文件 mtime/内容确认编入。

## 5. 对 master 的建议

- **接受**：UX-01/02/03、PD-01/02/08/09/11/17/18、UX-05/06/08/09/10/11/13 → CONFIRMED，可在 FIX-REPORT 标记完成（UX-05 并入 PD-17）。
- **保持 OPEN**：UX-04（PARTIAL，需真重绘修复 + 真跑回归）；About 版本去重（P3，顺带修）。
- wave1 的「UX-04/UX-06/UX-08/UX-10/UX-11/UX-13/PD-08」打包声明中仅 UX-04 需回退状态，其余成立。
