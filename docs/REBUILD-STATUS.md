# ZeoKeel 重建状态（Rig v0.13.0 · old/ 对照）

> 截至 2026-11-07（goal round 10）。目标：按 old/ 功能面重建，UI 全部 mygo 原生控件。

## 已落地（可运行 / 可验证）

**内核与宿主**
- kernel 编译期模块装配（`internal/assembly/registry.go` + `assemblies/{desktop,headless}.json`）
- 产品布局：`cmd/zeokeel` 薄 main → `internal/host` → 模块；rig v0.13.0 作为 agent 内核
- desktop 15 / headless 6 实例；`go test ./...` + `-race` 18 包全绿；xvfb 真窗口冒烟通过

**设计体系（从 old 原样移植）**
- `internal/brand`（palette/theme/logo）、`internal/design`（tokens/字阶/间距/组件样式/motion）、
  `internal/icons`（Lucide SVG）
- 宿主 chrome：280px 侧栏 + 64px brand row + Cmd/Ctrl+B 64px 图标 rail；无全局顶栏，屏幕自带头部；
  右侧检查栏为窗口级 docked 面板

**P3 工作区 + Files 检查器（2026-11-07 晚）**
- `zeokeel/workspace`：workspaces.json 注册表（Create/Open/Delete/current 持久化）、沙箱 Files/ReadFile/
  Search（绝对路径/`..`/symlink 逃逸拒绝，深度与 limit）、原子写；desktop 17 / headless 8 实例
- 右侧栏「文件」Tab：打开文件夹（mygo 原生 Directory picker）、目录进入/面包屑、文本/Markdown/图片预览、
  刷新与错误状态；E2E 覆盖工作区创建 + 列表 + 预览帧

**P3 插件页（2026-11-08）**
- toolkit 提供 `KeyPlugins`：MCP servers 快照（Name/URL/ServerName/Version/Protocol/Tools/Status）、Refresh Ping +
  ListTools 刷新；连接失败保留条目
- `zeokeel-ui/plugins`：MCP 插件状态卡 + 工具目录分类/搜索/计数；侧栏通用导航自动出现「插件」；
  E2E 覆盖工具目录（48 工具）与搜索过滤、MCP 空态

**P3 自动化（2026-11-08）**
- `zeokeel/automation`：cron 子集（*/N、M H、@hourly/@daily）+ every/after，JSON 持久化，ticker 扫描执行
  （新会话 + prompt）、RunNow/CRUD/启停/NextRun/LastError/Subscribe；toolkit schedule_* 绑定持久 scheduler
- `zeokeel-ui/automation`：自动化页（任务卡/启停 Toggle/立即运行/删除确认/内联新建表单）+ 侧栏通用屏幕导航；
  E2E 覆盖 RunNow 新建会话执行、schedule_create/list 持久化、页面渲染

**P3 会话 fork / 导出（2026-11-08）**
- agent `SessionForker`：深拷贝读模型（新消息 ID/附件/Tools/Reasoning），标题「副本 - …」，active 切换，
  store 持久化（Create/Rename/ReplaceHistory/fork 事件），重启恢复
- sidebar 行菜单「创建副本」；session 头部「导出」→ mygo.Dialog.Save 写 Markdown（转写+元信息），
  成功 Badge/失败 DangerText；E2E 覆盖 fork→持久化重启、导出文件内容

**P3 技能（2026-11-08）**
- `zeokeel/skills`：rig skill.FileStore 组合多根（config/dataDir/workspace，动态生效），List/Read + Roots；
  body 去 frontmatter；toolkit skill 工具绑定持久 store
- 设置「技能」页：roots 列表、技能卡（名称/描述/路径）、Collapsible markdown 预览、刷新/错误/空态；
  E2E 覆盖工作区 SKILL.md 发现 + skill 工具 list + 设置页展开

**P3 终端（2026-11-07 晚）**
- `zeokeel/terminal`：运行器（workspace cwd、bash/sh/zsh/pwsh 探测与选择、CombinedOutput、退出码、
  timeout/ctx 取消、输出截断）；rightpanel「终端」Tab 全 native（cwd/shell/清空/输出/输入/运行/停止）；
  E2E 真跑 `echo` 命令并断言帧输出

**P3 会话↔工作区（2026-11-07 晚）**
- sessionstore：sessions 表幂等迁移 workspace_id（旧库兼容、重启不重复）+ SessionWorkspaceStore.SetSessionWorkspace
- agent：NewSession/首次 Send 绑定当前工作区并持久化，ChatSessionInfo.WorkspaceID/restore 恢复
- sidebar：workspace→session 树（组头折叠/会话数/当前工作区高亮/未分组/跨组搜索），E2E 覆盖关联与树渲染

**P3 工作区变更 diff（2026-11-07 晚）**
- `WorkspaceDiffer.Changes`：git status --porcelain=v1 -z 解析（M/A/D/R/??、rename 新路径、嵌套 workspace
  路径翻译）、`git diff HEAD` 生成 unified diff（二进制/untracked 空 diff、maxChanges=100 / maxDiffBytes=256KiB、
  ctx 取消）；右侧栏「变更」Tab：状态 Badge、±统计、Collapsible 展开 diff 行着色、刷新；E2E 用真实 git 仓库验证

**P2 @ 提及（2026-11-07 晚）**
- composer `@` 建议面板：读 WorkspaceService（空查询 Files / 非空 Search），native 行插入 `@path `；
  E2E 覆盖真实 desktop assembly 的提及选择 → 发送 → 气泡文本

**P2 附件/图片（2026-11-07 晚）**
- contracts.Attachment / AttachmentSender / ChatMessage.Attachments；agent 图片→rig image part（data URL），
  非图片→文本 part（text/* 内容 ≤32KiB）；重启从 rig parts 恢复 chips
- composer：mygo.Dialog.Open 原生多选、48×48 图片缩略图、chips 移除、10MB 限制、MIME 映射；
  用户气泡只读 chips/缩略图；E2E 验证 provider 请求含 image part 与 UI 帧 chips

**P3 记忆 + Markdown（2026-11-07 晚）**
- `zeokeel/memory`：JSON 原子持久化 rig.MemoryStore（Add/List/Remove/Search、scope、重启恢复），
  toolkit 用持久 store 注册 memory 工具；desktop 16 / headless 7 实例
- 对话页「记忆」Tab：列表/搜索/添加/删除/错误提示/空态（native 控件 + design 样式）
- 助手正文 mygo `ui/markdown`：GFM 表格/任务列表/代码块（语言标签+复制）/流式增量

**垂直切片**
1. agent 宿主：`zeokeel/agent`（ChatService：provider/agent/gate/流式读模型、compaction、checkpoint）
2. 会话/持久化：`zeokeel/sessionstore`（SQLite：History/SessionLog/CheckpointStore/HistoryReplacer + 会话元数据），
   agent 重启恢复、标题/消息/事件持久化
3. 聊天主界面：`zeokeel-ui/session`（品牌消息气泡/思考折叠/工具卡/轨迹 Tab/记忆占位、composer 工具栏
   [模型 chip、`/` 命令、附件占位]、Enter 发送、复制 ID/对话、slash 命令菜单）、`zeokeel-ui/home`
   （品牌 hero + 初始设置卡 + composer 入口）、`zeokeel-ui/sidebar`（新对话/首页/会话搜索/行内
   PopoverMenu 重命名/删除/设为当前/footer 设置）
4. 工具/审批：`zeokeel/approvals` + `zeokeel-ui/approval`（native Modal）、`zeokeel-ui/toolcards`
   （24 真实工具卡）、`zeokeel-ui/rightpanel`（概览/工具/模型/日志；Log 读 SQLite 事件）
5. 设置：模型页（预设）、服务商页（DeepSeek/OpenAI/Ollama）、外观页（深色模式/语言，持久化）、
   快捷键页；about 页

## 未完成（按 old 功能面）

- P2 剩余：附件/图片（rig image ContentPart）、`@` 提及、Markdown 渲染、流式光标/动效、快捷键→命令注册表链路
- P3：memory 页/记忆工具接线、skills、automation、插件、workspace→session 树与 workspace diff、
  会话搜索（全文）、fork/export、右侧栏 Files/Terminal 页
- P4：ACP、全局热键、导入、更新、诊断
- 已知边界：MCP 动态工具名（`mcp__*`）无专用卡（走 session 兜底）；审批队列内存态；SQLite 单连接串行化

## 验证命令

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod
go build ./... && go vet ./...
go test ./... -count=1 && go test ./... -count=1 -race
go run ./cmd/zeokeel --headless --data-dir /tmp/zk   # 6 实例
xvfb-run -a timeout -s TERM 8 go run ./cmd/zeokeel --data-dir /tmp/zk2  # 15 实例
go run ./internal/devtools/ui-snapshot /tmp          # 刷新 docs/screenshots/p2-*.png
```
