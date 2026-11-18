# A4 逐控件交互清点（2026-11-18）

> 方法：对 `internal/{uikit,ui,uiroot}` 的**每一个交互构造点**做静态清点（图标按钮/按钮/开关/下拉/复选/单选/输入/搜索/页签/列表/树/表格/网格/菜单/折叠/浮层/自绘命中区），
> 再用 `ui-drive` 的驱动步骤与 Tab 走查做**运行时证据**交叉标注。全量明细见 [A4-control-inventory.tsv](A4-control-inventory.tsv)（398 行）。

## 0. 总量

| 项 | 数 |
|---|---|
| 交互构造点合计 | **398** |
| 其中真正可点的控件 | **358** |
| 其中浮层容器（Modal/Overlay/Popover，本身只承接焦点） | 40 |
| 有驱动/键盘证据（PASS） | **144**（40%） |
| 尚未被驱动断言（待覆盖） | **214** |
| 驱动中判 FAIL 的产品缺陷 | **0**（另有 2 条已知限制 WARN，见 §5） |

## 1. 按屏分布

| 屏 | 可点控件 | 已有证据 | 覆盖率 |
|---|---|---|---|
| 壳层 | 21 | 9 | 43% |
| 侧栏 | 16 | 2 | 12% |
| 会话屏 | 81 | 28 | 35% |
| 首页 | 11 | 2 | 18% |
| 自动化 | 22 | 11 | 50% |
| 洞察 | 8 | 4 | 50% |
| 能力 | 52 | 25 | 48% |
| 右栏 | 14 | 6 | 43% |
| 设置 | 91 | 49 | 54% |
| 共享组合 | 42 | 8 | 19% |

> 说明：侧栏/壳层覆盖率偏低是**静态标签提取**的局限（这些控件的 Label 大多在闭包里，如 `menuLabel := …`），并非未验证——Tab 走查已逐个见过它们：

> 侧栏 `折叠侧边栏 · 主导航 · 新建会话 · 添加工作区 · 搜索会话与消息 · 设置 · 分隔条 · 会话行`；壳层 `智能体团队 · 子智能体 · 快捷操作 · 打开右侧栏 · 对话/轨迹/记忆/上下文`。

## 2. 五个动态面（用户点名）的新增驱动

| 面 | 步骤 | 断言 | 结果 |
|---|---|---|---|
| AI 回复流式 | `a4qa-stream-partial` | 运行中可见部分文本、`停止` 在、输入后 Enter 变为排队（不打断本轮）、Esc 收尾 | PASS |
| 工具调用 | `a4qa-tool-row-toggle` | 脚本化工具调用完成后行显示完成态；**点击行**展开详情、再点收起 | PASS |
| MCP 连接器 | `a4qa-mcp-add-http` | 添加面板（传输/名称/URL/认证/超时）→ 空 URL 测试有反馈 → 不可达 URL 测试给出失败原因 → 添加成功入列表；**重复 URL 被拒（「该 URL 已存在」）** | PASS |
| MCP 连接器 | `a4qa-mcp-edit-toggle-remove` | 卡片菜单→编辑→保存、启停开关翻转（已停用可见）、移除确认框取消后卡片仍在、确认后消失 | PASS |
| Skill 技能 | `a4qa-skill-create-edit-view-delete` | 新建（名称/描述/内容）→ 列表出现 → 查看内容显示正文 → 行菜单（复制路径/禁用/编辑/删除）→ 禁用标记 → 删除需确认 | PASS |
| 设置逐页 | `a4qa-settings-inputs` | 模型页系统提示词输入+保存、行为页运行限额输入、通知开关翻转并复位 | PASS |
| 设置默认值 | `a4qa-settings-restore-defaults` | 「恢复默认」→ 确认框 → 取消后快捷键值不变 | PASS |
| 键盘可达 | `a4qa-tabwalk-{session,landing,automation,insights,capabilities,settings}` | 每屏连续 Tab：焦点每步移动、可达控件数 ≥ 阈值、循环闭合 | 6/6 PASS |

## 3. 每屏已覆盖代表（证据来自驱动/Tab 走查）

- **壳层**（9 条证据）：`折叠侧边栏`、`搜索会话`、`更多操作`、`快捷操作`、`打开右侧栏`、`关闭右侧栏`、`搜索命令`、`继续编辑`、`放弃修改`
- **侧栏**（2 条证据）：`在“%s”中新建会话`、`会话操作：`
- **会话屏**（28 条证据）：`归档原文列表`、`返回主会话`、`「研究员」已完成：…`、`发送给`、`发送消息给`、`停止成员`、`关闭成员`、`会话操作`、`打开子会话`、`会话轨迹`、`打开设置`、`搜索记忆`、`记忆列表`、`新建记忆`
- **首页**（2 条证据）：`打开模型设置…`、`打开模型设置`
- **自动化**（11 条证据）：`创建第一个任务`、`自动化任务`、`搜索任务`、`启用任务`、`任务操作`、`编辑任务`、`每次新建会话`、`跟随默认模型`、`自定义模型 ID`、`打开会话`
- **洞察**（4 条证据）：`未知模型`、`筛选会话`、`会话排行列表`、`未命名会话`
- **能力**（25 条证据）：`Markdown 预览`、`技能操作`、`用默认应用打开`、`MCP 连接器列表`、`连接器选项`、`设置存储未启用，无法保存连接器`、`连接器操作`、`启用连接器`、`按名称、说明或路径搜索…`、`技能列表`、`刷新技能`、`新建技能`、`技能目录与额外目录`、`打开目录`
- **右栏**（6 条证据）：`关闭`、`重新读取文件`、`用系统应用打开`、`重新读取目录`、`工作区文件`、`查看改动`
- **设置**（49 条证据）：`DSH 数据目录`、`只读扫描该目录，不写入任何数据`、`搜索记忆`、`记忆列表`、`添加记忆`、`记忆内容`、`整理策略`、`记忆操作`、`编辑记忆`、`代理地址`、`输入价格 / 1M`、`缓存输入 / 1M`、`输出价格 / 1M`、`模型名称`
- **共享组合**（8 条证据）：`approval.arguments.`、`继续规划反馈`、`重新投递队列中的消息`、`队列中的消息`、`发送队列中的消息`、`.usage`、`对话消息`

## 4. 待覆盖清单（按屏，供下一轮补驱动）

> 「未覆盖」= 尚无驱动步骤/断言触及该调用点，**不是产品缺陷**。变量标签条目（24 条）需要按 AccessAll 名逐个核对。

### 壳层（12 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 图标按钮 | 展开侧边栏 | `internal/uiroot/frame.go:330` |
| 图标按钮 | 展开侧边栏 | `internal/uiroot/frame.go:346` |
| 图标按钮 | 新建会话 | `internal/uiroot/frame.go:362` |
| 图标按钮 | label（变量） | `internal/uiroot/frame.go:391` |
| 图标按钮 | settingsLabel（变量） | `internal/uiroot/frame.go:409` |
| 图标按钮 | 会话 | `internal/uiroot/frame.go:434` |
| 图标按钮 | 关闭检查器 | `internal/uiroot/frame.go:870` |
| 按钮 | )   if chord != | `internal/uiroot/frame.go:1032` |
| 列表 | 命令 | `internal/uiroot/frame.go:1412` |
| 图标按钮 | 关闭设置 | `internal/uiroot/frame.go:2078` |
| 搜索框 | 搜索设置 | `internal/uiroot/frame.go:2135` |
| … | 另 1 条见 TSV | |

### 侧栏（14 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 列表 | 消息搜索 | `internal/ui/sidebar/sidebar.go:833` |
| 图标按钮 | 添加工作区 | `internal/ui/sidebar/sidebar.go:1189` |
| 图标按钮 | 工作区操作： | `internal/ui/sidebar/sidebar.go:1702` |
| 输入框 | T（变量） | `internal/ui/sidebar/sidebar.go:2588` |
| 输入框 | T（变量） | `internal/ui/sidebar/sidebar.go:2651` |
| … | 另 9 条见 TSV | |

### 会话屏（53 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 按钮 | 正在压缩上下文… | `internal/ui/session/context_pane.go:236` |
| 表格 | %d | `internal/ui/session/context_pane.go:621` |
| 列表 | 上下文事件列表 | `internal/ui/session/context_pane.go:690` |
| 按钮 | 打开工作台 | `internal/ui/session/agentsurface.go:829` |
| 按钮 | 关闭 | `internal/ui/session/agentsurface.go:1134` |
| 列表 | 选择后在当前面板打开 · Esc 关闭 | `internal/ui/session/agentsurface.go:1293` |
| 图标按钮 | tr（变量） | `internal/ui/session/agentsurface.go:2466` |
| 图标按钮 | tr（变量） | `internal/ui/session/agentsurface.go:2806` |
| 图标按钮 | label（变量） | `internal/ui/session/view.go:2254` |
| 图标按钮 | tr（变量） | `internal/ui/session/view.go:2948` |
| 菜单按钮 | 打开工作区 | `internal/ui/session/view.go:5456` |
| 按钮 | 新建工作区 | `internal/ui/session/view.go:5467` |
| 图标按钮 | tr（变量） | `internal/ui/session/interrupted.go:70` |
| 图标按钮 | tr（变量） | `internal/ui/session/goal.go:373` |
| 按钮 | 全部清空 | `internal/ui/session/memory_tools.go:71` |
| 图标按钮 | tr（变量） | `internal/ui/session/memory_tools.go:333` |
| … | 另 37 条见 TSV | |

### 首页（9 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 按钮 | 配置模型 | `internal/ui/home/home.go:249` |
| 按钮 | 配置模型 | `internal/ui/home/home.go:451` |
| 按钮 | 新建会话 | `internal/ui/home/home.go:469` |
| 按钮 | 添加工作区 | `internal/ui/home/home.go:477` |
| 菜单按钮 | 还没有工作区 | `internal/ui/home/composer.go:296` |
| 图标按钮 | tr（变量） | `internal/ui/home/composer.go:775` |
| … | 另 3 条见 TSV | |

### 自动化（11 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 按钮 | 取消 | `internal/ui/automation/view.go:141` |
| 菜单按钮 | 筛选 | `internal/ui/automation/view.go:208` |
| 按钮 | 立即运行 | `internal/ui/automation/view.go:309` |
| 按钮 | 修复 | `internal/ui/automation/view.go:351` |
| 折叠区 | 运行历史 | `internal/ui/automation/view.go:530` |
| 输入框 | 例如：每日依赖巡检 | `internal/ui/automation/view.go:593` |
| 输入框 | */5 * * * * | `internal/ui/automation/view.go:611` |
| 按钮 | 取消 | `internal/ui/automation/view.go:651` |
| 菜单按钮 | 计划模板 | `internal/ui/automation/view.go:846` |
| 菜单按钮 | 跟随当前工作区 | `internal/ui/automation/view.go:1142` |
| 菜单按钮 | 默认全局 | `internal/ui/automation/view.go:1180` |

### 洞察（4 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 图标按钮 | 刷新 | `internal/ui/insights/view.go:42` |
| 按钮 | 重试 | `internal/ui/insights/view.go:176` |
| … | 另 2 条见 TSV | |

### 能力（27 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 输入框 | 名称 | `internal/ui/plugins/skills_edit.go:221` |
| 输入框 | 描述 | `internal/ui/plugins/skills_edit.go:226` |
| 输入框 | 描述 | `internal/ui/plugins/skills_edit.go:287` |
| 图标按钮 | 刷新连接 | `internal/ui/plugins/connectors_section.go:347` |
| 菜单按钮 | 详情 | `internal/ui/plugins/connectors_section.go:653` |
| 按钮 | 查看详情 | `internal/ui/plugins/connectors_section.go:729` |
| 按钮 | 查看详情 | `internal/ui/plugins/connectors_section.go:733` |
| 按钮 | 查看报错详情 | `internal/ui/plugins/connectors_section.go:745` |
| 按钮 | 重试 | `internal/ui/plugins/connectors_section.go:752` |
| 图标按钮 | 查看内容 | `internal/ui/plugins/skills_section.go:395` |
| 输入框 | 例如：~/.zeokeel/skills | `internal/ui/plugins/skills_section.go:467` |
| 按钮 | 应用 | `internal/ui/plugins/skills_section.go:477` |
| 按钮 | 重新打开浏览器 | `internal/ui/plugins/connectors_auth.go:203` |
| 按钮 | 复制授权链接 | `internal/ui/plugins/connectors_auth.go:210` |
| 下拉 | 传输 | `internal/ui/plugins/connectors_edit.go:216` |
| 输入框 | 名称 | `internal/ui/plugins/connectors_edit.go:224` |
| … | 另 11 条见 TSV | |

### 右栏（8 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 图标按钮 | 复制路径 | `internal/ui/rightpanel/view.go:344` |
| 右键菜单 | 打开 | `internal/ui/rightpanel/view.go:863` |
| 列表 | 交付物列表 | `internal/ui/rightpanel/files.go:295` |
| 按钮 | 下载 | `internal/ui/rightpanel/files.go:358` |
| 按钮 | 下载 | `internal/ui/rightpanel/files.go:365` |
| … | 另 3 条见 TSV | |

### 设置（42 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 按钮 | 浏览… | `internal/ui/settings/import.go:86` |
| 输入框 | 录制全局热键 | `internal/ui/settings/hotkey_settings.go:230` |
| 输入框 | 组合键 | `internal/ui/settings/hotkey_settings.go:243` |
| 按钮 | 按键录入组合键 | `internal/ui/settings/hotkey_settings.go:254` |
| 折叠区 | 展开模块列表 | `internal/ui/settings/diagnostics_modules.go:44` |
| 列表 | 已加载模块列表 | `internal/ui/settings/diagnostics_modules.go:60` |
| 按钮 | 去重并合并 | `internal/ui/settings/memory.go:190` |
| 按钮 | 全部清空 | `internal/ui/settings/memory.go:198` |
| 按钮 | 添加 | `internal/ui/settings/memory.go:249` |
| 按钮 | 按策略整理 | `internal/ui/settings/memory.go:449` |
| 输入框 | 逗号分隔的直连主机列表。 | `internal/ui/settings/network.go:79` |
| 输入框 | 输入新的 API Key；留空保持不变 | `internal/ui/settings/modelcatalog_editor.go:678` |
| 下拉 | 思考系列菜单 | `internal/ui/settings/modelcatalog_editor.go:704` |
| 下拉 | 默认思考菜单 | `internal/ui/settings/modelcatalog_editor.go:736` |
| 折叠区 | 生成、价格与自定义模式 | `internal/ui/settings/modelcatalog_editor.go:761` |
| 列表 | 自定义思考模式 | `internal/ui/settings/modelcatalog_editor.go:874` |
| … | 另 26 条见 TSV | |

### 共享组合（34 条）

| 类型 | 控件 | 位置 |
|---|---|---|
| 折叠区 | 详情 | `internal/uikit/toolcall.go:172` |
| 单选 | 其他… | `internal/uikit/ask.go:158` |
| 单选 | 其他… | `internal/uikit/ask.go:162` |
| 输入框 | 其他回答 | `internal/uikit/ask.go:171` |
| 按钮 | 取消 | `internal/uikit/ask.go:210` |
| 输入框 | 回答 | `internal/uikit/ask.go:247` |
| 按钮 | 取消 | `internal/uikit/ask.go:254` |
| 按钮 | .detail | `internal/uikit/runstatus.go:213` |
| 按钮 | 重新运行上一次请求 | `internal/uikit/runstatus.go:345` |
| 按钮 | 清空全部 | `internal/uikit/runstatus.go:597` |
| 按钮 | 重试 | `internal/uikit/session.go:183` |
| 按钮 | these（变量） | `internal/uikit/settingsform.go:83` |
| 按钮 | these（变量） | `internal/uikit/settingsform.go:87` |
| 按钮 | these（变量） | `internal/uikit/settingsform.go:89` |
| … | 另 20 条见 TSV | |

## 5. 已知限制与判读

- `a4qa-known-limit-focus-return-esc|enter`：危险确认关闭后焦点不落地（WARN，U/A4-QA-REPORT §7.5 A）。
- 浮层容器（Modal/Overlay/Popover，40 个构造点）本身不判「点击/hover」，它们的 Esc 行为已由 Esc 矩阵与 `esc-*` 用例覆盖。
- 「hover 有反馈」列为「是（框架 hover）」表示该控件走 mygo 原生 hover（`uikit.IconButton`/官方 Button/Switch/Select/List 行），框架级证据见 `TestIconButton*` 与 `a4qa-hover-vs-selected`；**像素级逐控件证据仍待补**（见 §6）。

## 6. 下一轮建议（按性价比）

1. **逐控件 hover 像素探针**：对每屏 10–15 个代表控件做「hover 前后同一矩形像素差」断言（机制已在 `a4qa-hover-vs-selected` 里跑通，可复用）。
2. **侧栏/壳层图标按钮逐个点开**：折叠/新建会话/添加工作区/搜索/设置/顶带 5 个按钮逐个点击+Esc 复原（现在只覆盖了其中 3 个）。
3. **设置剩余页**：LSP（新增服务器/删除）、记忆（添加/删除条目）、归档（恢复/删除）、数据与隐私（清除全部会话的确认路径）。
4. **工具三态的失败分支**：需要服务端脚本加一个「工具返回错误」模式（`server.go` 不在我的写集，需 Lead 授权一行改动）。
5. **MCP 的 STDIO 分支与鉴权分支**：STDIO 表单（命令/参数/环境变量）与 Bearer/OAuth 高级选项的必填校验。
