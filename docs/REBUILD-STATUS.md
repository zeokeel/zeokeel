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
