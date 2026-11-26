# ZeoKeel UI/UX 重构方案（从后端功能反推）

> 口径：不参考旧前端骨架，纯从后端运行形态反推 IA 与交互。
> 视觉：克制工具风（Linear/Vercel 级 Operate 控制台）。技术：保留 antd，`tokens.css` 即 antd re-skin 层。
> 字体：全 SIL OFL 1.1，可商用、可内嵌打包——Inter（UI）/ 思源黑体 Noto Sans SC（中文）/ JetBrains Mono（数据）。

## 一、三条核心论点（都来自后端事实）

1. **浏览器是"自治机器的观察窗 + 控制台"，不是聊天框。**
   依据：heartbeat 每 30s 跑 triage、60s 轮询渠道；`inbox→lead→triage→board→dispatch→agent run→audit→report` 没人看也在转。
   → 首页是**指挥台**（OPS），回答"什么在等我 / 现在在发生什么 / 我该看什么"，不是"说点什么"。

2. **后端六类人类闸门是同一个概念"机器卡住等我"，必须合并为单一决策队列。**
   依据：`approval.*` / `ask.*` / `plan.*` / `tasks.send.*` / `tasks.rework.reset` / `notify:needs_decision`，共用 notify 类别与 approvals 队列。旧 UI 把它散成 7 处。
   → 指挥台右栏 + 独立「决策」页就是这一条队列；闸门也内联出现在它所属的 run 里（RUN 图标注"也会出现在决策队列"）。

3. **工作单位是任务，不是会话。**
   依据：`session.bindtask` + `session.preset.set` 说明会话只是"某角色执行某任务"的运行载体；持久对象是任务板上的任务（租约 / 证据 / 返工数 / 审计结论）。
   → 主导航以任务板（BOARD）为中心；会话列表降级为「会话历史」。RUN 图顶部横幅展示的是绑定任务而非"未命名会话"。

## 二、信息架构（反推版，替换旧的 13 项平铺）

```
指挥台              ← 自治循环的观察窗 + 决策队列入口
决策 ⦿5            ← 六类闸门合并的单一队列（唯一带紧迫性 badge）
工作  任务板 / 工作流
团队  成员 / 角色与预设
产出  报告 / 会话历史 / 洞察 / 自动化
配置  能力与连接 / 渠道 / 设置
```
旧问题对照：两个撞名「收件箱」→ 收敛为「决策」+「任务板·排队」；5 个 `settings/*` 被当顶级导航 → 按对象模型归位；占位方块图标 → 统一 1.6 描边图标族。

## 三、7 个关键面（截图在 shots/）

| 屏 | 补上的后端能力 / 论点 | 文件 |
|---|---|---|
| OPS 指挥台 | 自治循环可见化 + 决策队列中枢 | shots/OPS.png |
| LAUNCH 启动器(⌘K) | 发起=三选一(问一下/派任务 task.submit/跑工作流) | shots/LAUNCH.png |
| BOARD 任务板 | 6 列对应 `task.status`；证明"工作单位=任务" | shots/BOARD.png |
| RUN 会话执行视图 | 绑定任务+角色+工具上限+证据+提交验收；证明"会话=载体" | shots/RUN.png |
| FLOW 工作流编排台 | **M2 从零补入口**：阶段图+依赖+运行态 | shots/FLOW.png |
| ROLES 角色预设编辑器 | **取代裸 JSON**：allow/deny + MCP/技能域(M3/M3b) | shots/ROLES.png |
| REPORT 报告中心 | **M4 报告归档 + M1 返工到顶的拍板入口** | shots/REPORT.png |

## 四、craft 自检（impeccable detect，UI 定稿后一次）

全部 `warning`，无 error / P0。性质说明：
- `tiny-text / undersized-ui-text (11.5px, 10.5px)`：高密度控制台的刻意取舍，功能文本主体 12.5–13.5px，仅 meta/脚注低于 11px。可在实现期把正文底线提到 12px、meta 到 11px 收掉。
- `cramped-padding(band)`：顶栏 49px 紧凑是控制台惯例，非缺陷。
- `overused-font(Inter)`：**brief 钉死项**——需求是"开源可商用可内嵌"，Inter 是 OFL 里最稳的工具字面；个性可后续换 OFL 的 Geist 替代族，不影响方案。

## 五、从截图到代码的落地路线（低风险优先）

1. **token 层**：把 `tokens.css` 的变量映射进 `web/src/design/antd-theme.ts` 的 `theme.token` + 少量 `components` 覆盖。零结构改动，先让全站换肤。
2. **IA 外壳**：按第二节重写 `shell/sidebar-nav.tsx` 分组 + 收敛双收件箱；路由表 `features/routes.ts` 对应调整。
3. **决策队列**：新建 `features/decisions/`，聚合 `approvals + ask + plan + tasks.send + tasks.rework + notify:needs_decision` 六个已存在的 WS 域为一个读模型。
4. **补齐缺口**（后端已就绪、前端零引用）：`api/workflow.ts`(FLOW) / `presets` 可视化(ROLES) / `api/reports.ts`(REPORT) / `tasks.rework.reset` 按钮接线。
5. **任务板 & RUN**：BOARD 接 `tasks.snapshot/delta`；RUN 在现有 thread 上加任务横幅 + 证据栏 + 提交验收。

渲染可复现：`node assemble-ops.mjs && node render.mjs`（playwright-core 自带 chromium，字体已 self-host）。
