# Web 运行时适配：x-sdk 接管状态机

> 决定（2026-11-23）：**采用 `@ant-design/x-sdk` 的 `useXChat` 作为前端会话状态运行时，不自建 store**；MCP 的增删仍走网关设置，`XMCPClient` 只用于"测试连接/预览工具"。
> 依据：`@ant-design/x-sdk@2.10.0` 的 `.d.ts`（CDN 实测，非记忆）。

---

## 1. x-sdk 实际提供了什么

`es/index.d.ts` 的导出：

```ts
export { default as useXChat } from './x-chat';
export { default as useXConversations } from './x-conversations';
export { default as XRequest, AbstractXRequestClass } from './x-request';
export { default as XStream } from './x-stream';
export { AbstractChatProvider, DefaultChatProvider, OpenAIChatProvider, DeepSeekChatProvider } from './chat-providers';
```

三块与我们直接相关：

| 组件 | 作用 | 我们用它替掉什么 |
|---|---|---|
| `useXChat(config)` | 会话状态 hook；每 `conversationKey` 一个 store | 自建的 messages reducer + 流式拼接 + 自动滚动状态 |
| `ChatMessagesStore` | `subscribe/getSnapshot/setMessages/addMessage/setMessage/removeMessage`，带 throttle | 自建的 zustand store 与会话级缓存 |
| `AbstractChatProvider` + `AbstractXRequestClass` | 传输扩展点 | 自建的"网关协议 → 消息流"转换层 |

**结论：省掉的是最难写对的那部分**（流式拼接、状态流转、每会话隔离、节流重渲染），留下的是两个小类。

## 2. 必须实现的接口（全部成员，实测）

### 2.1 `AbstractXRequestClass` —— 7 个成员

```ts
abstract class AbstractXRequestClass<Input, Output, ChatMessage> {
  constructor(baseURL: string, options?: XRequestOptions<Input, Output, ChatMessage>);
  abstract get asyncHandler(): Promise<any>;
  abstract get isTimeout(): boolean;
  abstract get isStreamTimeout(): boolean;
  abstract get isRequesting(): boolean;
  abstract get manual(): boolean;
  abstract run(params?: Input): void;
  abstract abort(): void;
}
```

`XRequestOptions` 携带回调：`onUpdate(chunk, headers, msg)` / `onSuccess(chunks, headers, msg)` / `onError(err, info, headers, fallback)`。

### 2.2 `AbstractChatProvider` —— 3 个抽象方法

```ts
abstract transformParams(requestParams, options): Input;
abstract transformLocalMessage(requestParams): ChatMessage | ChatMessage[];
abstract transformMessage(info: { originMessage, chunk, chunks, status, responseHeaders }): ChatMessage;
```

外加 `config: { request: AbstractXRequestClass | () => AbstractXRequestClass }`、`injectRequest({onUpdate,onSuccess,onError})`、`getMessages()`。

### 2.3 `useXChat` 的配置

```ts
{
  provider?          // 我们的 GatewayChatProvider
  conversationKey?   // = 会话 id
  defaultMessages?   // 支持 () => Promise<Msg[]>  ← 正好接快照
  parser?            // agent message → bubble message 投影
  requestPlaceholder? // 发送后立刻显示的占位消息
  requestFallback?    // 失败时的兜底消息
}
```

`defaultMessages` 支持异步是关键——**我们的 `session.snapshot` 可以直接接在这里**，不用另写加载态。

---

## 3. 传输分叉：已定 B（2026-11-23）

`XRequest`（库存实现）是**SSE 专用**：`SSEOutput = Partial<Record<'data'|'event'|'id'|'retry', any>>`。原 `TRANSPORT.md` 是纯 WS JSON-RPC，两者需要收口。候选：

| 方案 | 手搓量 | 代价 |
|---|---|---|
| A. 纯 WS + 自写 request 类 | 实现 7 个成员（~250 行） | 单一传输，协议一致；但流式解析要自己写 |
| **B. WS 管状态 + POST+SSE 管单轮运行** ✅ | **0 行**（直接用 `XRequest`） | 两条传输；但这是 OpenAI/Anthropic 的行业常态 |

**已定 B**（2026-11-23 决策，依据"能用框架尽量用框架"）。协议细节已落到 `TRANSPORT.md` §4.6（端点、事件表、与 WS 状态的关系、断连语义、鉴权、背压）。理由：
1. "能用框架就用框架"的最彻底落点——`XRequest` 开箱即用，连 abort 都由 `AbortController` 负责。
2. 单轮运行的语义本来就是"请求 → 流式响应 → 结束"，天生是 SSE 形状；WS 的双向性在这里没用上。
3. 快照/差值/审批/通知仍在 WS 上（它们才是双向的），分工清晰而非重复。

**B 的协议增量**（加到 `TRANSPORT.md`）：
- `POST /api/chat/run`（body: `{sessionId, text, attachments}`）→ `text/event-stream`
- 事件：`message.delta` / `tool.start` / `tool.result` / `reasoning.delta` / `usage` / `done` / `error`
- 取消：`AbortController` → 服务端在连接断开时中止该轮
- 状态一致性仍由 WS 的 `session:<id>` 域 rev 保证；SSE 只负责"这一轮的增量"

---

## 4. 映射表

### 4.1 消息状态

`MessageStatusEnum = local | loading | updating | success | error | abort`

| 我们的语义 | x-chat status | 渲染（规则 7） |
|---|---|---|
| 已发送、未开始 | `local` | 正文 + 时间 |
| 运行中 | `loading` / `updating` | **Accent** 指示 + `LabelSecondary` 词；**不是 warning** |
| 完成 | `success` | 无状态色 |
| 失败 | `error` | `semantic.error` + 错误行 |
| 用户中止 | `abort` | **中性**（`cancelled` 不许当错误） |

### 4.2 快照/差值 → store 操作

| TRANSPORT 的域/ops | store 调用 |
|---|---|
| `domain: session:<id>` 首帧快照 | `defaultMessages()` 返回值 |
| `msg.append` | `addMessage` |
| `msg.patch`（流式 token / 工具状态） | `setMessage(id, partial)` |
| `msg.reset`（regenerate / compact） | `setMessages(next)` |
| `msg.remove` | `removeMessage` |
| `epoch` 变化 | 重建 store（`destroy()` 后重新挂载） |
| `baseRev != localRev` | 丢弃本地并 `defaultMessages` 重取 |

### 4.3 其它契约不由 x-chat 承担

审批、计划、提问、产物、运行态、Inbox —— 这些**不是消息**，继续由 WS 的服务契约驱动，不进 `ChatMessagesStore`。x-chat 只管 transcript。

---

## 5. MCP：用框架的地方用，但边界要说清

`XMCPClient` 的实际接口只有一个方法：

```ts
XMCPClient(baseURL, options?).tools(): Promise<XMCPTool[]>
// XMCPTool = { name, description, inputSchema, annotations? }
```

也就是说它是**只读的工具发现客户端**，**不含**增删服务器、不含持久连接、不含凭据管理。

所以：

| 操作 | 归属 | 用什么 |
|---|---|---|
| 增加/删除 MCP 服务器 | 网关设置（`SettingMCPServers` 写入 + toolkit 重连） | 我们的 settings 契约 |
| **填完 URL 立刻"测试连接 / 预览工具"** | Web 客户端 | **`XMCPClient`（用它）** |
| 调用 MCP 工具执行 | 网关 `internal/modules/toolkit` | 服务端 |

"测试连接"这条是真实收益：用户粘上 URL 就能看到工具数量与名字，再决定保存，省掉一次"存了才发现连不上"的往返。

**不建议把 MCP 连接搬到浏览器**，两个硬约束：
1. **CORS**：绝大多数 MCP server 不发 `Access-Control-Allow-Origin`，浏览器直连会全线失败（`XMCPClient` 走 `fetch`）。
2. **凭据**：MCP 常用 `headers` 带 token；放浏览器意味着 token 进 JS 上下文与 devtools。

如果确实要浏览器直连（例如本地 stdio 转 HTTP 的场景），再单独评估，届时 token 必须走一次性短期凭据。

---

## 6. 不做的事

- 不引 `DefaultChatProvider` / `OpenAIChatProvider` / `DeepSeekChatProvider`：它们是直连模型厂商的，我们的模型调用在网关里。
- 不引 `XStream` 做传输：它面向 `ReadableStream` 解析，`XRequest` 已经封好。
- 不自建 messages store：这是本次决定的核心。
- 不把 `x-chat` 当作审批/计划的状态机。

---

## 7. 落地步骤（取代原 Stage 2 的一部分）

1. `web/` 脚手架 + `npm i antd@6 @ant-design/x@2.10.0 @ant-design/x-sdk@2.10.0 @ant-design/x-markdown@2.10.0`
2. 网关加 `POST /api/chat/run`（SSE），复用 `chat` 契约的流式事件
3. `GatewayChatProvider extends AbstractChatProvider`（3 方法）+ 用库存 `XRequest`
4. `useXChat({ provider, conversationKey, defaultMessages: fetchSnapshot })`
5. 用 `BubbleList` + `Sender` 接上，先跑通"发送 → 流式 → 完成 → 中止"
6. 再补 `ThoughtChain`（推理/工具步骤）与审批卡（自建，走 WS）

验收：`WEB-CLIENT.md` 里与 transcript/composer 相关的 P0 条目全部可跑。