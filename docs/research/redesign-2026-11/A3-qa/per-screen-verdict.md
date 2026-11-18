# 逐屏复核结论（2026-11-18 终审）

> 基准：`.cache/ui-conformance-final/` 128 张。**人工逐张看过约 100 张**（含所有"会不同"的屏）；
> 其余为同屏的明/暗、窄窗、状态变体，由 `full-sweep.md` 的 119 张程序化度量 + 变体差分覆盖。
> 判据：顶带 48+36=84、圆角 4/6/8/12/16、接缝 1px `BorderL2`、正文 14/22、元数据 12、accent 填充 ≤2 / primary ≤1。

## 0. 结论

**通过。** 128 张无未解释缺陷；本轮共发现并修复 26 个问题（其中 2 个是正确性问题、1 个 P0 暗色可读性），
全部有重出的 PNG 或单测/帧测证据。构建、测试、designlint、design-check、mygo vet 全绿。

## 1. 正确性问题（非审美）

| # | 问题 | 证据 | 状态 |
|---|---|---|---|
| C1 | **非零退出码被画成成功**：bash `[exit code: 2]` 显示 ✓ 无失败词，与后台任务面板规则不一致 | `TestToolExitCode`、`TestToolRowShellExitCodeIsAFailure`、`TestToolCallCardShellExitCodeIsAFailure`；`p2-tool-row-states` 首行 | 已修（`ToolCallStatusKind/Label` 全链路同源；仅 shell 类重判；cancelled 保持中性；非 shell 不重判） |
| C2 | **暗色下 composer/能力卡/设置面白底白字**（P0，可读性失败） | 复验采样 `#1B1B26`/`#23232F`；`themeGen` 重绘 | 已修（根因是 mygo 对 Scroll 子树复用绘制层：换主题时给根内容换 key） |

## 2. 动态面（用户点名）

| 面 | 结论 | 证据 |
|---|---|---|
| AI 回复流式 | 部分文本 + 「正在回复」+ 运行态状态条 + 排队/停止；Enter 变排队不打断本轮 | `p2-streaming-reply(-dark)`、`a4qa-stream-partial` |
| 工具调用 | 四态字色/词/可访问名一致；宽行与轨迹行尾部同序；点击行展开/收起；失败 stderr 与 exit 码可见 | `p2-tool-row-states(-dark)`、`p1-trajectory`、`a4qa-tool-row-toggle`、`a4qa-tool-fail-state` |
| MCP 连接器 | 表单头部固定/唯一滚动体/预留状态行/动作行一行；校验错误内联在字段下；STDIO 分支 URL 消失、空命令被拒；OAuth 分支有作用域/客户端 ID/授权服务器；重复 URL 被拒 | `p2-capabilities-connector-{form,form-stdio,test,details}`、`a4qa-mcp-*` |
| Skill | 新建/编辑表单同构（有滚动体）；查看内容显示正文；复制路径/禁用/删除确认 | `p2-capabilities-skill-{editor,content}`、`a4qa-skill-create-edit-view-delete` |
| 队列 | 运行中由 composer `×N` chip 报数（贴排队左侧、弹出层右对齐、有标题）；停止后由状态条徽章接管 | `p2-inbox(-popover)`、`a4qa-send-note-approval` |

## 3. 逐屏清单（按屏幕族）

| 屏幕族 | 结论 | 备注 |
|---|---|---|
| 会话（对话/轨迹/记忆/上下文） | ✅ | 气泡 `BgStrong` 无描边；元信息底部 hover 显形；轨迹/工具行尾部同序；上下文表格无竖线 |
| 审批（内联卡 + diff） | ✅ | 无默认动作、Enter 不决策、Esc 拒绝；混合 diff 行级染色；暗色 cyan primary 达标 |
| 计划/提问（决策闸门） | ✅ | 刻意无 ✕（规则已入 `DESIGN.md §7.4`）；两卡内距逐 token 一致 |
| composer | ✅ | 平铺 r16、焦点仅键盘 1px accent、禁用实心灰 5.9:1、旁注解释禁用原因 |
| 侧栏（展开/折叠/未读/置顶/搜索） | ✅ | 选中 `BgSelected` + 2px 左条；未读点右对齐；英文 placeholder 完整 |
| 右栏检查器（开始/文件/预览/diff/交付区） | ✅ | 页面 tab 保留下划线、文档 tab 浅底 chip；collapsible 全局裸 trigger |
| 能力（技能/连接器） | ✅ | 卡路径列 105/105px 对齐；scope chip 定宽；开关进 640 measure |
| 设置十页 | ✅ | S4 浮层 + `SurfaceSunken()` 导航；可访问名统一 `<标题> 输入`；系统通知不支持时只读 |
| 自动化 | ✅ | 每行 3 个可见目标；`修正运行计划`/`打开最近运行的会话` 进 ⋯ |
| 洞察 | ✅ | 1024 下全部列在内容区内 |
| 落地/空态 | ✅ | 单 primary（`配置 API Key`）；建议 chip 三行居中 |
| 通知/toast | ✅ | 右下锚点避 composer 与检查器；有 pending 审批时让到顶带下方；同文案合并 `×N`；暗色浅卡（mygo 反转，已登记） |
| 团队/子代理 | ✅ | 状态合并成 `已结束 · 可继续`；子会话 composer 与主会话同解剖；子会话输入名唯一 |
| 恢复/分叉/中断 | ✅ | 中断横幅中性；fork 场景名副其实；恢复后状态正确 |
| 窄窗（1024） | ✅ | 顶带整簇收进 `⋯`、距接缝 20；检查器抽屉；设置面单栏 |
| 英文（全站） | ✅ | 日期/计划/时间本地化；会话标题保留中文（用户数据） |
| 暗色（全站） | ✅ | S0–S4 同向；无白面残留；cyan accent 对比达标 |

## 4. 已知限制（3 条，均已登记）

1. 危险确认关闭后**焦点归还**：整装里被 `ui.Sidebar` 焦点组覆写（接线与包级测试齐全）；
2. 暗色 toast 是**浅卡**（mygo 浮层反转语义，`DESIGN.md §7.4.1`）；
3. 1024 窄窗 + 空会话 hero：toast 可能压住「配置 API Key」上部 25px（瞬时、可关闭）。

## 5. 方法学备注

- 评审图默认**静息态**（`renderRest()` 移开指针后再拍）；hover/focus 单独出图，见 `full-sweep.md` 顶部说明与 `p2-file-diff-hover.png`。
- 「第一行是哪个文件」「面板高度固定」这类**不是契约**，测试不应依赖；已在相关测试注释中标注。
