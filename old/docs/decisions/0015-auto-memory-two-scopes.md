# ADR 0015：双层 auto-memory（Global / Workspace）

- 日期：2026-11-06
- 状态：**已采纳**
- 决策者：用户（2026-11-06：「auto-memory 这个业内有比较好的实现么，另外这个应该是跟着工作空间走，和整体设置两种，你看要怎么编排」→ 批准推荐默认值「可以可以，非常完美，搞吧」）；实施：Lead + Agent Teams
- 关联：[auto-memory 设计契约](../design/auto-memory.md)、[FUNCTIONAL-PARITY](../FUNCTIONAL-PARITY.md)、`internal/store/memory.go`、`internal/tools/memory.go`、`internal/prompt/memory.go`、`internal/memory/`、`internal/app/memory_auto.go`、`internal/shell/memory_ui.go`

## 1. 背景

ZeoKeel 此前的 memory 只有一层：`memories` 表按 `workspace_id` 存，模型通过显式 `memory` 工具写入，Settings › Memory 列出/删除；`internal/app/services.go` 里有一条明确的产品决定「不做 post-turn 自动抽取」。用户要求补齐 auto-memory，并明确要**两层作用域**：跟随工作空间 + 整体设置（全局）。

DSH 0.2.1-rc.2 没有任何 memory 包/能力（已核实安装包目录与系统提示），因此这不是 DSH 对齐项，而是 ZeoKeel 产品能力；实现参考业内的成熟做法：

- **Claude Code**（[memory 文档](https://code.claude.com/docs/en/memory)）：CLAUDE.md（人写指令）与 auto memory（模型在会话中自己写文件）双系统；auto memory 分 user/feedback/project/reference 四类，按仓库存储，`MEMORY.md` 索引每次加载前 200 行/25KB、主题文件按需读取；`/memory` 可浏览/编辑/删除与总开关；**零额外模型调用**。
- **OpenAI ChatGPT**（[memory dreaming](https://openai.com/zh-Hans-CN/index/chatgpt-memory-dreaming/)）：保存式记忆 + 后台记忆整合。
- **Mem0 v3**（[算法迁移文档](https://docs.mem0.ai/migration/platform-v2-to-v3)）：记忆累积而非覆盖、agent 生成事实也捕获、混合检索、Dream 式后台整合。
- **LangMem**（[SDK 发布博客](https://www.langchain.com/blog/langmem-sdk-launch)）：semantic/episodic/procedural 记忆分类 + namespace 划分隐私域 + memory manager/prompt optimizer。

## 2. 决策

1. **两个持久作用域**：
   - `global`（整体设置/用户级）：跨工作区的偏好、习惯、身份；`workspace_id = ''`；默认模式 **suggest**（候选进待确认 inbox）。
   - `workspace`（跟随工作空间）：项目约定、构建/测试命令、架构决策、坑、外部资源；默认模式 **auto**（agent 通过 memory 工具即时写入 active、立即可见可删）。
   - 注入顺序 Global → Workspace（越具体越靠后，冲突时 workspace 优先），两段独立限额（global 12 条/1500 字符；workspace 沿用 20 条/4000 字符），并保留「参考数据、非指令」警告。
2. **两类写入通路**：
   - A. 会话内 agent 自写（Claude Code 式）：memory 工具加 `scope` 参数，写入模式由 Settings 决定（off/suggest/auto）；**零额外模型调用**。
   - B. 可选后台抽取器（默认关）：每 N 个成功回合异步跑一次（默认 6，范围 1–50），JSON schema 输出候选（≤3 条），经 secret scrub/去重/相似合并后按 scope 模式落库；失败静默、不影响会话。
3. **确定性整合（C）**：无额外模型调用；合并精确重复、清理超过 30 天的 superseded、按每 scope active 上限（global 100 / workspace 300）按 importance+last_used_at 淘汰为 superseded。
4. **DB 为唯一真源**（SQLite schema v3：scope/kind/source/status/session_id/turn_id/content_hash/confidence/importance/last_used_at/superseded_by + scope 索引）；markdown 仅提供导出（不做自动镜像/文件真源），避免与事务、工具、UI 双写冲突。
5. **隐私与控制面**：自动候选先过 secret scrub（api key/token/password/secret/bearer/sk-/AKIA/私钥块等不落库）；全局写入只能经 review（除非用户显式把 global 设为 auto）；Settings › Memory 双 tab（Global / Workspace）可查看/编辑/删除/接受建议/清空/导出；agent 对 global 的写入在 suggest 模式下返回「已保存为待审核建议」；Read Only 会话仍完全不注册 memory 工具（RT-03）。

## 3. 后果与限制

- 正面：记忆跟随工作空间（项目上下文）与整体设置（个人偏好）分层生效；自动记忆可完全关闭；全局默认不静默污染；无额外模型调用即可获得 Claude Code 式自动记忆（抽器为可选增强）。
- 取舍：不做自动 markdown 镜像（用户可导出）；后台抽取器默认关（成本/行为可预期）；不做跨机器同步（后续可选）；subagent 会话不自动捕获；被引用会话的上下文行不进入自动记忆分析（避免递归）。
- 已知差异：global 的 auto 模式允许用户显式开启（默认 suggest）；确定性整合不做 LLM 语义合并（后续可选增强）。
- **UI 布局（2026-11-06 用户补充决定）**：全局记忆在 Settings›Memory（Global 单页，可 Add/Edit/Delete/Accept/Discard/清空/导出）；工作区记忆在会话头部 `轨迹` 旁的 **Memory tab**（同样可编辑/新增/审核/清空/导出），`/memory` 在会话中切到该 tab、在 Home 打开 Settings；两处共享按 scope 参数化组件。理由是全局=整体设置、项目上下文=聊天现场，避免把所有记忆塞进设置页或把全局项误放到单个会话。

## 4. 验证

- store：v2→v3 迁移保留旧行/默认值/hash 回填；scope 隔离；旧 API 语义不变；去重/计数/last_used/supersede。
- settings：四键默认/Normalize/Load-Save 往返/权限白名单。
- prompt/tool：双段注入顺序与限额；scope×模式矩阵；read-only 不注册。
- UI：双 tab、模式持久化、pending accept/discard、scope 隔离、/memory 命令。
- pipeline：抽取触发/节流/关闭/无 key/失败静默；scrub/相似/解析容错；整合幂等。
- 门禁：`sh scripts/ci.sh`、`CGO_ENABLED=1 go test -race -timeout 20m ./internal/shell`、`GOOS=windows/darwin go build ./...`、`check-clean-checkout.sh`。
