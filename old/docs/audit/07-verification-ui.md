# UI/产品审计独立验证报告（verifier-ui）

> 验证者：verifier-ui · 日期：2026-11-05 · 验证时 `HEAD = 3111614`（审计期间 7eebe47→27d5d5f→20f1399→3111614）
> 方法：**独立复现**，不采信原报告结论。自建二进制 `.cache/audit/verify-ui/tmp/zeokeel-verify`（`go build ./cmd/zeokeel` @3111614，sha256 `681df21f…`；`dist/` 保持 21:15 原样未动）；Xvfb **:98 1600×1000x24**（防底部裁切假象）+ `unshare -n` 独立 netns + `ZEOKEEL_DATA_DIR=/tmp/zk-verify-ui/data`；xdotool/import 驱动 35 张截图（`.cache/audit/verify-ui/shots/`，引用路径 `../../.cache/audit/verify-ui/shots/`）；sqlite3 直查；代码核验 `internal/shell`、`internal/app`、mygo v0.7.4 源码。
> 限制：未用真实模型（未复现 provider 失败的运行时错误态，PD-09 只做代码+原报告截图复核）；未驱动审批/破坏性确认弹层；HighContrast/TextScale/IME/DPI/多显示器未验证；PD-06 未等真实定时触发；Windows GUI 的「无 stderr 静默」只能代码推断。
> 对原报告证据的抽查：`03` 引用 90 张、`05` 引用 85 张截图均存在且可读，引用的关键文件（12/14/22/42、77/63/43）齐全，**未发现伪造/缺失证据**。

## 0. 结论摘要

- 两份报告**无 P0**。P1 主体成立：UX-01/02/03、PD-01/03/04/05/06/07/09/10/12/13 均可复核（CONFIRMED 23、PARTIAL 3、子项 REFUTED 2）。
- **两处需纠正（会影响 master 措辞/定级）**：
  1. **PD-01/UX-03 的「按钮路径也被丢弃」不成立**：真跑证据——Session composer 点 **Send 按钮**会显示 toast「尚未配置 Agent。请在 设置 > 模型 中添加模型。」（`32-sendbutton-0.35s.png`）；静默只发生在**键盘 Enter（TextArea `OnTextKey` 回调，在 mygo frame 外执行）**路径。点击回调在 `runFrame` 内执行，`c.Toast` 有效。=> UX-01 全 CONFIRMED；UX-03 PARTIAL；PD-01 PARTIAL。
  2. **PD-08「Add plugin 死 CTA」不成立**：点击后弹出诚实的边界说明（「所有插件均内置于 ZeoKeel…没有第三方插件市场」，`25-add-plugin-dialog.png`），不是死路。但「4 vs 6 两套列表不一致」「4 个实验开关无实现」成立 → 建议降级 **P2**。
- UX-06 底部裁切**不是截图方法学假象**：直接抓取 900×720 独立设置窗口（`17`）同样裁切；22 张金标同款裁切，说明预览集未捕获该布局问题。
- 其余「状态残留/空态缺失」类结论均为**真 bug**，非 Xvfb/无 WM 造成；1600×1000 下 composer/footer 完整可见。
- 计数：**CONFIRMED 23 · PARTIAL 3（UX-03/PD-01/PD-08）· REFUTED 子项 2（PD-01 点 Send、PD-08 死 CTA）**。

## 1. 复现环境与命令（可重跑）

```sh
cd /data/mygo && export PATH=/usr/local/go/bin:$PATH GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod CGO_ENABLED=0
go build -o .cache/audit/verify-ui/tmp/zeokeel-verify ./cmd/zeokeel
Xvfb :98 -screen 0 1600x1000x24 -nolisten tcp &          # PID 3239969（结束后已精确 kill）
rm -rf /tmp/zk-verify-ui && mkdir -p /tmp/zk-verify-ui/ws /tmp/zk-verify-ui/data
DISPLAY=:98 ZEOKEEL_DATA_DIR=/tmp/zk-verify-ui/data nohup unshare -n -- sh -c \
  'ip link set lo up; exec /data/mygo/.cache/audit/verify-ui/tmp/zeokeel-verify' >/tmp/zk-verify-ui/app.log 2>&1 &
DISPLAY=:98 xdotool mousemove 800 540 click 1; xdotool type 'hello verify'; xdotool key Return   # 首次 Enter → 弹「添加工作区」
DISPLAY=:98 xdotool type 'Audit WS'; xdotool key Tab; xdotool type '/tmp/zk-verify-ui/ws'; xdotool key Return
DISPLAY=:98 xdotool mousemove 800 540 click 1; xdotool key Return   # 无模型发送
DISPLAY=:98 import -window root shots/06-send-0.2s.png              # 再 1.0s / 3.5s 各一帧
sqlite3 /tmp/zk-verify-ui/data/index.sqlite "select count(*) from sessions; select count(*) from messages;"
```
设置窗口是独立 X 窗口（`xwininfo -root -tree` → `0x2001dd "设置" 900x720+350+140`），用 `import -window <id>` 直接取窗口图，排除「截图裁切」怀疑。

## 2. 核验矩阵（来源 ID | 报告定级 | 判定 | 复核证据 | 备注）

| 来源ID | 定级 | 判定 | 复核证据（截图均为本次独立复现） | 备注 |
| --- | --- | --- | --- | --- |
| UX-01 | P1 | **CONFIRMED** | `06/07/08`（0.2s/1.0s/3.5s）：Session 空 transcript、草稿回填、无 toast；`sessions=1 messages=0 events=0`；再 Enter 后像素级无变化（与发送前 AE=0/17，仅光标闪烁）；`09/10/11` | 键盘 Enter 路径完全成立 |
| UX-02 | P1 | **CONFIRMED** | `14`（对话 tab 整片空白）vs `13`（轨迹 tab「暂无事件。发送消息以开始追踪。」）；`trajectory.go:164-167`、`i18n.go:306`；`session_live.go` 无空 transcript 分支（grep 无命中） | 真 bug |
| UX-03 | P1 | **PARTIAL** | 静默机制成立：mygo `Context.Toast` 仅在 `inFrame` 内生效（mygo `ui/api_gen.go:559-565`、`ui/frame.go:29-38`），键盘回调在 `runFrame` 外执行；`toastText` 有 queueToast 兜底（`composer_menus.go:361-372`、`composer_slash.go:971-991`、`shell/app.go:279-281`）。对照实验 `/clear` toast 可见（`12-slash-clear-toast.png`）。**但「按钮回调也被丢弃」REFUTED**：点 Send 可见 toast（`32`） | 建议改写为「键盘触发的失败反馈被丢弃」；按钮路径无需修 |
| UX-04 | P2 | **CONFIRMED** | `15`（菜单开）→`16`（Ctrl+, 后菜单位于设置窗口上方/半遮）→`18`（关闭设置后菜单完整残留）；`shell/app.go:373-384` 只置 `moreOpen=false`+`closeSelectors()`（`settings_ui.go:946-955` 只清设置页下拉），未清 `liveMenus()` popover | 真状态残留 |
| UX-05 | P2 | **CONFIRMED** | `03-send-no-ws-dialog.png`/`04`：两个输入无可见 label、无 placeholder，仅下方解释小字；`workspaces.go:1185-1204` 仅 `ui.TextInput(...).Label()`（a11y-only） | 真 bug |
| UX-06 | P2 | **CONFIRMED** | 独立窗口直抓 `17`（900×720，末行「忙碌时的发送行为」被切半、初始无滚动提示）；`settings_window.go:218-224` 常量 900/720；金标 `settings-window-native-{light,dark}.png` 同款裁切 | 非截图假象；「滚动条 hover overlay」属已知框架限制，裁切本身可修 |
| UX-07 | P2(KNOWN) | **CONFIRMED** | `20`（侧栏「新建会话」实心 + 页头「添加插件」实心）、`24`（「新建」+「新建自动化任务」）；`44`/`27` 设置页双「添加模型」原报告有图 | round4 P1-8 仍未修 |
| UX-08 | P2 | **CONFIRMED** | `19`：Ctrl+B 后 56px rail 仅 brand/expand/new/plugins/automation/workspace/add，无会话入口；`chrome.go:146-181` | 真信息架构缺口 |
| UX-10 | P2 | **CONFIRMED**（代码） | `composer_menus.go:25-29` `sessionMentionPrefix = "session · "` 硬编码，`:460` 拼接；中文界面混排 | 未逐帧复现截图，代码充分 |
| UX-14 | P3 | **CONFIRMED**（旁证） | `12`/`32`：toast 固定在底部居中，压住 composer 工具栏（「工作区写入」chip/Send 按钮区域） | 顺带确认 |
| PD-01 | P1 | **PARTIAL** | 主链 CONFIRMED：`03`（首 Enter 弹添加工作区）→创建后 `06/07/08`（切 Session、空会话、无错误、草稿回填、无消息）；`sessions=1 messages=0`。**「点 Send 同样毫无反应」REFUTED**：点 Send 有 toast（`32`），但同样 `messages=0`、空会话残留 | 静默仅键盘路径；空会话污染两者皆有 |
| PD-02 | P1 | **CONFIRMED**（代码） | `models_ui.go:225-266` 对话框仅 5 个文本字段；`confirmModel`（`:276-320`）只校验 name 非空；无 Test 按钮/`/models` 探测/预设（grep 无命中） | 未真跑填表；代码与原报告截图一致 |
| PD-03 | P1 | **CONFIRMED** | 代码 `app.go:109-122` 固定 `127.0.0.1:49731`；`cmd/zeokeel/main.go:12-17` 仅 `log.Println` 后 return。实测：进入运行中实例 netns 二次启动 → `second_exit=0`、stderr `ZeoKeel is already running`、无新窗口/无激活（另用占位 listener 实测：任何进程占用 49731 即阻断启动）；换 `ZEOKEEL_DATA_DIR` 仍被阻断 | 真 bug；Windows GUI 无 stderr → 双击无反应成立（代码推断） |
| PD-04 | P1 | **CONFIRMED**（代码） | 全仓 grep 无 DSH 导入/迁移实现（仅注释提及 dsh-* 组件）；定位文案 `docs/STATUS.md:7`、`docs/PLAN.md:363`（「DSH 桌面替代/迁移器」） | 定位与能力矛盾成立 |
| PD-05 | P1 | **CONFIRMED** | `26b/27` + `settings_ui.go:340-393`：仅 6 页（通用/模型/记忆/内置插件/代理预设/关于）；无通知/隐私与数据/诊断/更新/重置页 | 真缺口 |
| PD-06 | P1 | **CONFIRMED** | `21`（列表页无历史/失败列，仅时间）；`24`（新建对话框仅 标题/提示词/计划/启用，无 workspace/权限/模型/时区）；`automation_ui.go:317-318` 只显示 LastRun；`app.go:589-621` 失败仅 `log.Printf`、30s 轮询；`store/automation.go:14-25` 无 history/status 字段 | 真缺口 |
| PD-07 | P1 | **CONFIRMED** | `27`：设置>内置插件中「技能」「MCP 服务」只有开关、无「配置」按钮（其余 4 项有）；`plugins_ui.go:55` 仅插件定义；`internal/shell` 无 MCP/Skills/AGENTS.md 管理页（grep 无命中） | 真缺口 |
| PD-08 | P1 | **PARTIAL → 建议 P2** | 4 vs 6 CONFIRMED：`plugins_ui.go:21-70` 六项 vs `:242-251` 仅 4 行；`20`（4 行）vs `27`（6 行）。实验开关无功能 CONFIRMED（`:62-73` 注释「features do not exist yet」）。**「死 CTA」REFUTED**：`25` 弹窗为边界说明，非死路 | 描述需改写；定级建议 P2 |
| PD-09 | P1 | **CONFIRMED** | 失败回合直接渲染 provider 原文（`session_live.go:401-406` + 原报告 `shots/63`），无分类建议/无「打开模型设置」；`liveMessageActions` 只在**已完成 assistant 消息**下（`message_actions.go:32-47`），失败回合本身无 Retry 入口 | 措辞可保留；注：全局存在 Retry（已完成消息底部） |
| PD-10 | P1 | **CONFIRMED**（代码） | `draft_scope.go:8-40` `drafts map` 纯内存；grep 无 `last_session`/恢复逻辑；窗口几何却有持久化（证明非框架限制） | 未做重启真跑（时间盒），代码充分 |
| PD-12 | P1 | **CONFIRMED**（代码） | `app.go:565-586` 托盘仅 Show/Quit 且英文硬编码；grep 无 GlobalShortcut/autostart；`mygo.NewWindow` 仅 2 处（主窗口 `app.go:180`、设置窗口 `settings_window.go:182`） | 真缺口 |
| PD-13 | P1(KNOWN) | **CONFIRMED** | `Makefile` 仅 windows/package-windows；`dist/` 仅 Windows exe/zip（+linux 裸二进制，无安装器）；`packaging/windows/README-WINDOWS.txt` 自述 unsigned、无 installer/auto-update | KNOWN 仍成立；原报告「dist 仅 Windows zip」措辞可微调（还有 linux 裸二进制） |
| PD-14 | P2 | **CONFIRMED**（代码） | `store/model.go:20/28/48` `api_key` 明文列 + `ProviderAPIKeyKey`；无 keychain 代码 | 未真读本机 DB；代码充分 |
| PD-15 | P2 | **CONFIRMED** | `app.go:183-187` 1600×1000、Min 900×600，无屏幕 clamp；原报告 1280×800 裁剪有图 | 真 |
| PD-16 | P2 | **CONFIRMED**（代码） | `app.go:570/577` 托盘英文；会话标题创建时按当时语言落库（本机 DB 标题「新建会话」） | 真 |
| PD-17 | P2 | **CONFIRMED** | `workspaces.go:1275-1311` 只校验 name/root 非空；`store/workspace.go:21-47` root 任意字符串落库 | 真 |

## 3. 七项必查复现记录（摘要）

1. **UX-01/PD-01（无模型首启发送）**：`06-send-0.2s` / `07-send-1.0s` / `08-send-3.5s` 三帧：0.2s 已切到 Session（对话 tab、空 transcript、composer 回填 `hello verify`、无 toast/错误行），3.5s 完全一致（sha256 与 1.0s 相同）；DB `sessions=1 sessions_title=新建会话 messages=0 events=0`。再次 Enter：`09/10/11` 与 `08` 的像素差 AE=0/17（光标闪烁），即**零可见变化**。补测：点 Send 按钮 → `32` 显示 toast（见 §0-1）。
2. **UX-03 根因**：`home.go:328/331`、`session_live.go:820/823` 在发送失败时直调 `c.Toast`；mygo `Context.Toast` 在非 build pass（`rt.inFrame=false`）直接 return（`ui/api_gen.go:559-565`、`ui/frame.go:29-38`），键盘回调 `OnTextKey`（mygo `ui/input.go:719-726`）在 frame 外执行 → 被丢弃；`toastText`→`queueToast`（`composer_slash.go:971-978`）→下一帧 `View` 开头 `flushDeferredToast`（`shell/app.go:279-281`）可见。对照：`/clear` → `12` toast「已清空输入框草稿」可见（键盘路径，走 toastText）。
3. **UX-02**：`13` 轨迹 tab 空态文案 vs `14` 对话 tab 全空白；代码 `trajectory.go:164-167` 有 `session.trajectory_empty`，`session_live.go` 无对应分支。
4. **UX-04**：`15`→`16`→`18` 三步复现（菜单位于设置窗口上方半遮 → 关闭设置后完整残留），`openSettings()` 未清理 `liveMenus()`。
5. **PD-03**：同 netns 二次启动（`nsenter -t <app_pid> -n`）→ `ZeoKeel is already running`、exit 0、无窗口激活；外部 listener 占用 49731 同样阻断；换 data dir 无效。
6. **PD-06**：`21`/`24` 界面证据 + 代码（见矩阵）；无历史/失败原因/workspace/权限/模型字段成立。
7. **PD-04**：grep 无迁移实现，定位文案在 `docs/STATUS.md:7`、`docs/PLAN.md:363`。

## 4. 最终可信 P0/P1 清单与 master 建议

**无 P0。** P1 建议合并/处置如下（按修复收益排序）：

| # | 条目（合并后） | 判定 | master 建议 |
| --- | --- | --- | --- |
| 1 | **首启/键盘发送失败静默**（UX-01 + UX-03 键盘路径 + PD-01）：空会话污染侧栏、草稿回填、无错误反馈；点 Send 有 toast 但不落消息 | CONFIRMED（UX-03/PD-01 部分措辞修正） | **采纳 P1**；失败统一 `toastText` + 失败回滚空会话/保留 Home 草稿（S/M）。UX-03 降为根因条目挂在 #1 下 |
| 2 | PD-03 单实例固定端口、二次启动不激活 | CONFIRMED | 采纳 P1（数据目录锁 + 激活已有窗口，S） |
| 3 | PD-06 Automation 无历史/失败可见、不绑 workspace/权限/模型、仅前台 | CONFIRMED | 采纳 P1（M） |
| 4 | PD-09 失败错误产品化 | CONFIRMED | 采纳 P1（错误分类 + 失败回合 CTA/Retry，S） |
| 5 | PD-07 MCP/Skills/AGENTS.md 无管理 UI | CONFIRMED | 采纳 P1（M） |
| 6 | PD-02 + PD-05 首启/设置面（连通性测试、通知/数据/诊断/更新页） | CONFIRMED | 采纳 P1，建议打包成「配置与支持面」一条（M） |
| 7 | PD-10 会话/草稿不恢复 | CONFIRMED（代码） | 采纳 P1（M） |
| 8 | PD-12 桌面生命周期（全局键/自启/托盘动作/多窗口） | CONFIRMED（代码） | 采纳 P1（M/L，可拆批次） |
| 9 | PD-04 迁移定位 vs 零迁移实现 | CONFIRMED（代码） | 采纳 P1；建议 master 先要求**降级文案**（S）再做导入（L） |
| 10 | PD-13 安装包/签名/更新缺失 | CONFIRMED（KNOWN） | 保持 P1/KNOWN |
| — | UX-02 新会话对话空态 | CONFIRMED | 采纳；P1/P2 之间（首启体验关键，可 P1） |
| — | PD-08 插件双界面/假开关 | PARTIAL | **降级 P2**，删除「死 CTA」表述 |
| — | UX-04/05/06/08/10 与 PD-14/15/16/17 等 P2/P3 | CONFIRMED | 维持原级，纳入 master 的 P2 汇总 |

## 5. 未验证 / 证据不足

1. **PD-09 运行时错误态**：未配置可达/不可达 endpoint 真跑，只有代码 + 原报告 `shots/63`（构建自 7eebe47）。「失败回合无 Retry」按代码结构成立，但未截图。
2. **PD-10 重启恢复**：未做跨进程重启真跑（代码证据充分）。
3. **PD-06 定时触发失败链路**：未建任务等 30s 轮询；「失败只进日志」为代码结论。
4. **审批卡鼠标点击、破坏性确认弹层视觉**：未驱动（原报告亦标注未验证）。
5. **HighContrast/TextScale/真机 IME/高 DPI/多显示器窗口 clamp**：headless 无法验证。
6. **Windows GUI 二次启动完全静默**：Linux 下只能确认 stderr 文案；Windows 无 console 属代码推断。
7. UX-11/12/13/15/16（P2/P3）未逐条复跑，未在本报告下结论。
8. 结论不受审计期间 HEAD 继续移动影响：本文行号以 `3111614` 为准；`20f1399..3111614` 仅动 provider/services，UI 发送路径代码未变。

## 6. 附：截图索引（`.cache/audit/verify-ui/shots/`）

`01` 首启 Home · `03/04` 添加工作区对话框（无可见 label）· `05` 工作区创建 toast · `06/07/08` 无模型发送 0.2/1.0/3.5s · `09/10/11` 会话内再次 Enter · `12` `/clear` toast 对照 · `13` 轨迹空态 vs `14` 对话空白 · `15/16/18` popover 残留三步 · `17` 设置窗口 900×720 底部裁切 · `19` 折叠 rail · `20` Plugins 4 项 · `21` Automation 列表 · `24` Automation 新建对话框 · `25` Add plugin 弹窗 · `26b/27` 设置「内置插件」6 项（技能/MCP 无配置）· `32/33` 点 Send 按钮出现 toast（纠正 PD-01/UX-03 的关键证据）。
