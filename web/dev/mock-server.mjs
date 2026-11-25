#!/usr/bin/env node
/**
 * Local gateway stand-in for the web client (no Go required).
 *
 * Two kinds of surface live here:
 *
 *  1. The real contract: `POST /api/chat/run` → text/event-stream with the
 *     seven event types of docs/TRANSPORT.md §4.6, including a run that parks
 *     on a tool approval and resumes when the client resolves it.
 *  2. A temporary read/write bridge for everything the WS control plane will
 *     own (TRANSPORT §6): sessions, approvals, inbox, settings, files,
 *     capabilities, automation, insights. The client only reaches these
 *     through `web/src/api/*`, so swapping them for WS changes no component.
 *
 * Run: node dev/mock-server.mjs        (PORT=8788 by default)
 */
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { validateParams } from './ws-params.mjs';
import {
  CONDITIONAL_CAPABILITIES,
  STATIC_CAPABILITIES,
} from '../src/design/ws-contract.ts';
import { createHash } from 'node:crypto';

const PORT = Number(process.env.PORT ?? 8788);
const HOST = process.env.HOST ?? '127.0.0.1';

const nowIso = () => new Date().toISOString();

/** Which session the client is looking at (`session.active.get`). */
let activeSessionId = null;

/** Toast actions the mock can actually run (keyed by toast id, like the gateway). */
const toastActions = new Map();
let toastSequence = 0;

/** Ask/plan state the mock really mutates (their commands are not no-ops). */
let askState = null;
let planState = { state: 'idle', content: '' };

/** Cold store: no persisted settings rows, the way a fresh data dir starts. */
const COLD_STORE = process.env.MOCK_COLD_STORE === '1';
const shortId = (prefix) => `${prefix}-${randomUUID().slice(0, 8)}`;

// ---------------------------------------------------------------- sessions --
/** @type {Map<string, {id:string,title:string,updatedAt:string,messages:any[]}>} */
const sessions = new Map();

const session = (title) => {
  const value = { id: shortId('s'), title, updatedAt: nowIso(), messages: [] };
  sessions.set(value.id, value);
  return value;
};

const msg = (role, text, extra = {}) => ({ id: shortId('m'), role, text, ...extra });

const seeded = session('重构 transport 层');
seeded.messages.push(
  msg('user', '把 SSE 解析从组件里挪到 api 层。'),
  msg('assistant', '已经挪好了：`src/api/run-stream.ts` 负责创建请求，组件只消费事件。', {
    reasoning: '先确认现在的解析在哪里，再决定迁移边界。',
    steps: [
      {
        callId: 'call-seed-1',
        name: 'read_file',
        input: { path: 'src/chat/markdown.tsx' },
        output: 'export function MarkdownBody(…)',
        status: 'ok',
      },
      {
        callId: 'call-seed-2',
        name: 'grep',
        input: { pattern: 'new EventSource', path: 'src' },
        output: 'no matches',
        status: 'ok',
      },
    ],
    usage: { inputTokens: 980, outputTokens: 210 },
    stopReason: 'end_turn',
  }),
);

const other = session('写 WEB-CLIENT 验收');

// A 600-message conversation: the browser checks the virtualised transcript
// (no blank window, no jump) against it in `dev/browser-check.mjs`.
const longSession = session('长会话 · 600 条');
for (let index = 0; index < 600; index += 1) {
  longSession.messages.push(
    index % 2 === 0
      ? msg('user', `第 ${index} 条提问：这一段用来撑开滚动高度。`)
      : msg('assistant', `第 ${index} 条回答：虚拟列表只渲染视口附近的行。`, {
          reasoning: index % 40 === 1 ? '先确认滚动位置，再决定是否跟随。' : undefined,
          usage: { inputTokens: 120 + index, outputTokens: 30 },
          stopReason: 'end_turn',
        }),
  );
}

other.messages.push(
  msg('user', 'transcript 虚拟化的验收项怎么定？'),
  msg('assistant', '按 §7.1：变高行测量、稳定 key、流式原地增长、首屏 80 条。', {
    reasoning: '对照原生 ui.List 的既有约束。',
    usage: { inputTokens: 640, outputTokens: 96 },
    stopReason: 'end_turn',
  }),
);

const childOf = new Map();
childOf.set(seeded.id, [
  {
    id: 'child-3f4a1b2c',
    parentId: seeded.id,
    title: '子代理 · 梳理 SSE 调用点',
    messages: [
      msg('user', '列出所有订阅 SSE 的地方。'),
      msg('assistant', '共 2 处：`run-stream.ts`（创建）与 `use-gateway-chat.ts`（消费）。'),
    ],
  },
]);

// --------------------------------------------------------------- approvals --
const approvals = new Map();
const approvalWaiters = new Map();
/** Assigned by the WS section; no-ops until then (seeded data runs first). */
let publishApprovals = () => {};
const permission = { mode: 'workspace' };

const createApproval = (input) => {
  const approval = {
    id: shortId('appr'),
    createdAt: nowIso(),
    expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
    source: 'user',
    ...input,
  };
  approvals.set(approval.id, approval);
  publishApprovals();
  return approval;
};

const resolveApproval = (id, decision) => {
  const approval = approvals.get(id);
  if (!approval) return false;
  approvals.delete(id);
  publishApprovals();
  const waiter = approvalWaiters.get(id);
  if (waiter) {
    approvalWaiters.delete(id);
    waiter(decision);
  }
  return true;
};

// One approval waits on a cold load so the card and the Inbox are visible
// without first starting a run.
createApproval({
  tool: 'bash',
  summary: '运行构建脚本',
  risk: 'exec',
  sessionId: other.id,
  target: { kind: 'command', text: 'npm run build' },
  arguments: { command: 'npm run build', cwd: '/data/mygo/web' },
});

// ------------------------------------------------------------------ inbox --
const inbox = new Map();
const addInbox = (item) => {
  inbox.set(item.id, { createdAt: nowIso(), ...item });
  return item.id;
};

addInbox({
  id: 'ask-1',
  kind: 'ask',
  sessionId: other.id,
  title: '需要你的确认：发布前要不要跑全量回归？',
  detail: '选项：跑全量 / 只跑 web / 跳过',
  expiresAt: new Date(Date.now() + 30 * 60_000).toISOString(),
});
addInbox({
  id: 'plan-1',
  kind: 'plan',
  sessionId: seeded.id,
  title: '计划待审阅：把 transport 抽象为三层',
  detail: '3 个步骤，等待批准或反馈',
});
addInbox({
  id: 'artifact-1',
  kind: 'artifact',
  sessionId: seeded.id,
  title: '产物待预览：transport-notes.md',
  detail: '1.8 KB · Markdown',
});
addInbox({
  id: 'done-1',
  kind: 'completion',
  sessionId: seeded.id,
  title: '子代理完成：梳理 SSE 调用点',
  detail: '2 处调用点，已回到父会话',
});

// ------------------------------------------------------------ prompt queue --
/** @type {Map<string, Array<{id:string,sessionId:string,text:string,createdAt:string}>>} */
const promptQueues = new Map();
/** Per-session inbox revisions, so `prompts.delta` carries a real baseRev. */
const promptRevs = new Map();
const publishedPrompts = new Map();
const promptRev = (sessionId) => promptRevs.get(sessionId) ?? 0;

/** Bump one session's inbox revision and push the delta to every ready client. */
const publishPrompts = (sessionId) => {
  const next = queueOf(sessionId).map((item) => ({
    id: item.id,
    text: item.text,
    createdAt: item.createdAt,
  }));
  const previous = publishedPrompts.get(sessionId) ?? [];
  const previousIds = new Set(previous.map((item) => item.id));
  const nextIds = new Set(next.map((item) => item.id));
  const added = next.filter((item) => !previousIds.has(item.id));
  const removed = previous.filter((item) => !nextIds.has(item.id)).map((item) => item.id);
  publishedPrompts.set(sessionId, next);
  const rev = promptRev(sessionId) + 1;
  promptRevs.set(sessionId, rev);
  wsNotify('prompts.delta', { sessionId, baseRev: rev - 1, rev, added, removed });
};

const queueOf = (sessionId) => {
  if (!promptQueues.has(sessionId)) promptQueues.set(sessionId, []);
  return promptQueues.get(sessionId);
};

// -------------------------------------------------------------- context --
/**
 * Folded context views. `found:false` for a session with no runs yet — the
 * honest empty state (a cold session has nothing to fold). The delta replaces
 * the whole view, never a field patch (§5.1.9).
 * @type {Map<string, {rev:number, found:boolean, context?:any}>}
 */
const contextViews = new Map();
const contextView = (sessionId) => {
  if (contextViews.has(sessionId)) return contextViews.get(sessionId);
  const session = sessions.get(sessionId);
  const hasRun = (session?.messages ?? []).some((message) => message.role === 'assistant');
  const view = hasRun
    ? {
        rev: 1,
        found: true,
        context: {
          revision: 1,
          snapshot: {
            model: 'fakemodel/tier-1',
            window: 200000,
            systemPromptTokens: 1820,
            systemPromptBytes: 7280,
            sections: [
              { id: 'agents-md', label: 'AGENTS.md', source: 'file', bytes: 5120, tokens: 1280 },
              { id: 'tools', label: '工具目录', source: 'builtin', bytes: 2160, tokens: 540 },
            ],
            tools: [{ name: 'bash', group: 'shell', tokens: 96 }],
            history: { messages: 2, user: 1, assistant: 1, tool: 0 },
            injected: { agentsMd: true, skills: true },
            estimatedPrompt: 1820,
            seq: 1,
          },
          requests: [
            {
              seq: 1,
              iteration: 1,
              model: 'fakemodel/tier-1',
              prompt: 1820,
              completion: 320,
              total: 2140,
              contextTokens: 1820,
              costUsd: 0.0032,
              costKnown: true,
            },
          ],
          events: [
            {
              seq: 1,
              kind: 'spill',
              detail: '超大工具输出落盘',
              path: '.zk/spill/tool-1.txt',
              bytes: 40960,
              saved: 9200,
            },
          ],
          used: 2140,
          window: 200000,
          totalCostUsd: 0.0032,
          costKnown: true,
        },
      }
    : { rev: 1, found: false };
  contextViews.set(sessionId, view);
  return view;
};

/** Push a whole-history reset (`msg.reset`, §6.6) for one session. */
const broadcastSessionReset = (session) => {
  if (!session) return;
  const rev = (session.rev = (session.rev ?? 0) + 1);
  wsNotify('session.delta', {
    sessionId: session.id,
    baseRev: rev - 1,
    rev,
    ops: [{ op: 'msg.reset', messages: session.messages.slice() }],
  });
};

// ---------------------------------------------------------------- tasks --
/**
 * Task boards: the process-wide one (`''`) and one per session. `added` in
 * `tasks.delta` is an upsert — a whole changed row — so the publisher sends the
 * changed rows, not field patches (§5.1.8).
 * @type {Map<string, {rev:number, tasks:Array<any>}>}
 */
const taskBoards = new Map();
/** Team fixture: the browser's team screen reads these. */
const mockTeam = { members: [], activities: [] };
/** Last interjections, so the fixture behaves like the gateway without inventing a run. */
const intercepted = [];
const taskBoard = (sessionId) => {
  if (!taskBoards.has(sessionId)) taskBoards.set(sessionId, { rev: 1, tasks: [] });
  return taskBoards.get(sessionId);
};

const publishTasks = (sessionId) => {
  const board = taskBoard(sessionId);
  const rev = board.rev + 1;
  board.rev = rev;
  // The delta carries no sessionId: the client routes it to the board it holds.
  wsNotify('tasks.delta', {
    baseRev: rev - 1,
    rev,
    added: board.tasks.map((task) => ({ ...task })),
    removed: [],
  });
};

// Seed one board so the chip has something to show in the mock.
taskBoard('').tasks.push(
  {
    id: 'task-1',
    subject: '复核 B5 的英文覆盖',
    status: 'in_progress',
    owner: 'web-engineer',
    createdAt: nowIso(),
    updatedAt: nowIso(),
  },
  {
    id: 'task-2',
    subject: '给 tasks 域补 mock 形状',
    status: 'pending',
    createdAt: nowIso(),
    updatedAt: nowIso(),
  },
);

// ------------------------------------------------ orchestration fixtures --
// §5.1.13. The inbound work queue is request/response (no delta follows a
// triage), so the screen reads again after each write; the fixture keeps a
// small queue so `npm run mock` can drive the screen by hand.
const mockInbox = {
  items: [
    {
      id: 'inbox-000001',
      source: 'wechat',
      channel: 'dm-1',
      sender: 'owner',
      title: '查退款率',
      body: '帮我看一下上周的退款率',
      ref: 'wechat://dm-1/7788',
      receivedAt: nowIso(),
      lastSeenAt: nowIso(),
      status: 'new',
      count: 3,
    },
    {
      id: 'inbox-000002',
      source: 'dingtalk',
      channel: 'group-42',
      sender: 'lead',
      title: '整理周报',
      receivedAt: nowIso(),
      status: 'task',
      count: 1,
      taskIds: ['task-2'],
    },
  ],
};

/** Leases handed out by `tasks.claim`, keyed by task id. */
const mockLeases = new Map();
/** Channel credentials, kind/id -> {kind,id,fields}. Values are never echoed. */
const mockCredentials = new Map();
/** Task -> allowed tool names. */
const mockTaskTools = new Map();
/** Task -> approval policy ("none" | "before_start"). */
const mockTaskApproval = new Map();
/** Task -> budget meter. */
const mockTaskBudgets = new Map();
/** Shared team notes, newest first. */
const mockTeamNotes = [];
/** QR logins started by the settings screen. */
const mockWechatLogins = [];
/** Heartbeat loop state the configure method mutates. */
const mockHeartbeat = { enabled: true, leadIntervalMs: 30000, channelIntervalMs: 60000 };
const leaseFor = (taskId, owner, state) => {
  const lease = {
    taskId,
    owner,
    until: new Date(Date.now() + 10 * 60_000).toISOString(),
    heartbeat: nowIso(),
    state,
    evidence: [],
    reviewNote: '',
  };
  mockLeases.set(taskId, lease);
  return { ...lease };
};

/** One-shot `approval.token` values; `approval.action` consumes them. */
const mockApprovalTokens = new Set();

/** Notify routes and webhook sources (both default to empty). */
const mockNotifyRoutes = [];
const mockWebhookSources = [];

// ---------------------------------------------------------------- settings --
const settingsCatalog = {
  groups: [
    {
      key: 'common',
      label: '常用',
      rank: 12,
      pages: [
        {
          key: 'model',
          label: '模型',
          order: 10,
          sections: [
            {
              title: '默认模型',
              fields: [
                {
                  key: 'defaultModel',
                  label: '默认模型',
                  kind: 'select',
                  value: 'deepseek-v4.1-flash',
                  options: ['deepseek-v4.1-flash', 'deepseek-v4.1-pro', 'zeokeel-local'],
                  help: '新会话默认使用的模型。',
                },
                {
                  key: 'contextBudget',
                  label: '上下文预算',
                  kind: 'number',
                  value: 0,
                  min: 0,
                  max: 1000000,
                  help: '0 表示跟随模型默认值。',
                },
                {
                  key: 'reasoningEffort',
                  label: '推理等级',
                  kind: 'radio',
                  value: 'medium',
                  options: ['low', 'medium', 'high'],
                },
              ],
            },
          ],
        },
        {
          key: 'behavior',
          label: '行为与限额',
          order: 20,
          sections: [
            {
              title: '权限',
              fields: [
                {
                  key: 'permissionMode',
                  label: '权限模式',
                  kind: 'radio',
                  value: 'workspace',
                  options: ['read-only', 'workspace', 'auto'],
                  help: '只能看 / 可以改文件 / 什么都能做。审批与操作系统沙箱都跟随它。',
                },
                {
                  key: 'workFolder',
                  label: '工作文件夹',
                  kind: 'text',
                  value: '/data/mygo',
                  placeholder: '/path/to/workspace',
                },
                {
                  key: 'maxIterations',
                  label: '单轮最大迭代',
                  kind: 'number',
                  value: 0,
                  min: 0,
                  max: 200,
                  help: '0 表示使用模块默认值。',
                },
              ],
            },
          ],
        },
      ],
    },
    {
      key: 'interface',
      label: '界面与通知',
      rank: 30,
      pages: [
        {
          key: 'appearance',
          label: '外观',
          order: 10,
          sections: [
            {
              title: '主题',
              fields: [
                {
                  key: 'theme',
                  label: '主题',
                  kind: 'radio',
                  value: 'system',
                  options: ['system', 'light', 'dark'],
                },
                {
                  key: 'language',
                  label: '语言',
                  kind: 'radio',
                  value: 'zh-CN',
                  options: ['zh-CN', 'en-US'],
                },
                {
                  key: 'textScale',
                  label: '字号',
                  kind: 'select',
                  value: '100',
                  options: ['90', '100', '110', '120'],
                },
                { key: 'reduceMotion', label: '减少动效', kind: 'switch', value: false },
              ],
            },
          ],
        },
        {
          key: 'notifications',
          label: '通知',
          order: 10,
          sections: [
            {
              title: '分级',
              fields: [
                {
                  key: 'level',
                  label: '通知级别',
                  kind: 'radio',
                  value: 'needs-action',
                  options: ['all', 'needs-action', 'off'],
                  help: '只有「需要你处理」会打断你；完成回执仍在收件箱。',
                },
                { key: 'sound', label: '提示音', kind: 'switch', value: false },
              ],
            },
          ],
        },
      ],
    },
    {
      key: 'connections',
      label: '连接',
      rank: 50,
      pages: [
        {
          key: 'network',
          label: '网络与搜索',
          order: 30,
          sections: [
            {
              title: '代理',
              fields: [
                {
                  key: 'proxy',
                  label: 'HTTP 代理',
                  kind: 'text',
                  value: '',
                  placeholder: 'http://127.0.0.1:7890',
                },
                {
                  key: 'searchProvider',
                  label: '搜索提供方',
                  kind: 'select',
                  value: 'builtin',
                  options: ['builtin', 'tavily', 'brave'],
                },
              ],
            },
          ],
        },
        {
          key: 'lsp',
          label: 'LSP',
          order: 50,
          sections: [
            {
              title: '服务器',
              fields: [
                {
                  key: 'servers',
                  label: '语言服务器',
                  kind: 'records',
                  value: [
                    {
                      id: 'lsp-ts',
                      name: 'typescript-language-server',
                      command: 'typescript-language-server --stdio',
                    },
                  ],
                  columns: [
                    { key: 'name', label: '名称' },
                    { key: 'command', label: '命令' },
                  ],
                },
              ],
            },
          ],
        },
      ],
    },
    {
      key: 'data',
      label: '数据',
      rank: 70,
      pages: [
        {
          key: 'archive',
          label: '归档',
          order: 30,
          sections: [
            {
              title: '已归档会话',
              fields: [
                {
                  key: 'archived',
                  label: '会话',
                  kind: 'records',
                  value: [{ id: 'arc-1', title: '旧版 transport 讨论', archivedAt: '2026-11-02' }],
                  columns: [
                    { key: 'title', label: '标题' },
                    { key: 'archivedAt', label: '归档时间' },
                  ],
                },
              ],
            },
          ],
        },
        {
          key: 'data-privacy',
          label: '数据与隐私',
          order: 40,
          sections: [
            {
              title: '导入',
              fields: [
                {
                  key: 'importPath',
                  label: '从归档文件导入',
                  kind: 'text',
                  value: '',
                  placeholder: '/path/to/archive.zip',
                },
                {
                  key: 'hiddenFiles',
                  label: '对 agent 隐藏的文件',
                  kind: 'records',
                  value: [{ id: 'hide-1', pattern: '.env' }],
                  columns: [{ key: 'pattern', label: '模式' }],
                },
                {
                  key: 'clearAll',
                  label: '清除全部本地数据',
                  kind: 'danger',
                  help: '不可撤销。会话、归档与上传都会删除。',
                },
              ],
            },
          ],
        },
      ],
    },
    {
      key: 'about',
      label: '关于',
      rank: 90,
      pages: [
        {
          key: 'about',
          label: '关于',
          order: 10,
          sections: [
            {
              title: '版本',
              fields: [
                { key: 'version', label: '版本', kind: 'readonly', value: '0.0.0-web (mock)' },
                { key: 'gateway', label: '网关', kind: 'readonly', value: 'mock-server.mjs' },
                { key: 'checkUpdate', label: '检查更新', kind: 'action', value: 'check-update' },
              ],
            },
          ],
        },
      ],
    },
  ],
};

const findPage = (key) =>
  settingsCatalog.groups.flatMap((group) => group.pages).find((page) => page.key === key);

// ------------------------------------------------------------------- files --
const workspaceFiles = [
  { path: 'src', dir: true },
  { path: 'src/api', dir: true },
  { path: 'src/api/run-stream.ts', changed: true },
  { path: 'src/api/protocol.ts' },
  { path: 'src/chat/transcript.tsx', changed: true },
  { path: 'src/app.tsx' },
  { path: 'package.json' },
  { path: 'README.md' },
];

const fileContents = {
  'src/api/run-stream.ts': `import { XRequest } from '@ant-design/x-sdk';

export const RUN_ENDPOINT = '/api/chat/run';

export function createRunRequest(baseURL = RUN_ENDPOINT) {
  return XRequest(baseURL, { manual: true });
}
`,
  'src/api/protocol.ts': `export const RUN_EVENTS = ['message.delta', 'done'] as const;\n`,
  'src/chat/transcript.tsx': `import { Bubble } from '@ant-design/x';\n\nexport function Transcript({ messages }) {\n  return <Bubble.List items={messages} />;\n}\n`,
  'src/app.tsx': `export function App() {\n  return <Shell />;\n}\n`,
  'package.json': `{\n  "name": "zeokeel-web",\n  "private": true\n}\n`,
  'README.md': `# web/\n\nZeoKeel 浏览器客户端。\n`,
};

const fileDiffs = {
  'src/api/run-stream.ts': `--- a/src/api/run-stream.ts
+++ b/src/api/run-stream.ts
@@ -1,6 +1,9 @@
 import { XRequest } from '@ant-design/x-sdk';
 
 export const RUN_ENDPOINT = '/api/chat/run';
+
+// The stream timeout catches a stalled gateway; no request timeout.
+const STREAM_TIMEOUT = 60_000;
 
-export function createRunRequest(baseURL = RUN_ENDPOINT) {
-  return XRequest(baseURL, { manual: true });
+export function createRunRequest(baseURL = RUN_ENDPOINT) {
+  return XRequest(baseURL, { manual: true, streamTimeout: STREAM_TIMEOUT });
 }
`,
  'src/chat/transcript.tsx': `--- a/src/chat/transcript.tsx
+++ b/src/chat/transcript.tsx
@@ -1,6 +1,10 @@
 import { Bubble } from '@ant-design/x';
 
 export function Transcript({ messages }) {
-  return <Bubble.List items={messages} />;
+  return (
+    <Bubble.List items={messages} autoScroll />
+  );
 }
`,
};

// -------------------------------------------------------- other read models --
const memoryEntries = [
  { id: 'mem-1', text: '这个仓库的 Go 侧 UI 正在退役，只有 web/ 继续演进。', createdAt: '2026-11-22' },
  { id: 'mem-2', text: 'SSE 只负责单轮增量，权威状态在 WS 的 session:<id> 域。', createdAt: '2026-11-23' },
];

const capabilities = {
  skills: [
    { id: 'antd', name: 'antd', description: 'antd v6 组件查询与陷阱', source: '内置' },
    { id: 'antd-x', name: 'antd-x', description: '@ant-design/x 2.10 对话面规格', source: '内置' },
  ],
  connectors: [
    { id: 'mcp-fs', name: 'filesystem', transport: 'stdio', tools: 6, status: 'connected' },
    { id: 'mcp-gh', name: 'github', transport: 'http', tools: 12, status: 'idle' },
  ],
};

const automationTasks = [
  {
    id: 'task-1',
    title: '每天 09:00 汇总仓库改动',
    schedule: '0 9 * * *',
    enabled: true,
    lastRun: '2026-11-23 09:00',
  },
  {
    id: 'task-2',
    title: '每周一扫描依赖更新',
    schedule: '0 8 * * 1',
    enabled: false,
    lastRun: '2026-11-17 08:00',
  },
];

const insights = {
  7: {
    totals: { sessions: 12, turns: 86, inputTokens: 482000, outputTokens: 96000, toolCalls: 214 },
    byDay: [
      { day: '2026-11-17', turns: 9 },
      { day: '2026-11-18', turns: 14 },
      { day: '2026-11-19', turns: 11 },
      { day: '2026-11-20', turns: 18 },
      { day: '2026-11-21', turns: 7 },
      { day: '2026-11-22', turns: 15 },
      { day: '2026-11-23', turns: 12 },
    ],
    byTool: [
      { tool: 'read_file', calls: 96 },
      { tool: 'bash', calls: 48 },
      { tool: 'grep', calls: 39 },
      { tool: 'edit_file', calls: 31 },
    ],
  },
  30: {
    totals: { sessions: 41, turns: 302, inputTokens: 1810000, outputTokens: 372000, toolCalls: 812 },
    byDay: [
      { day: '2026-11-01', turns: 12 },
      { day: '2026-11-08', turns: 21 },
      { day: '2026-11-15', turns: 18 },
      { day: '2026-11-22', turns: 15 },
    ],
    byTool: [
      { tool: 'read_file', calls: 341 },
      { tool: 'bash', calls: 168 },
      { tool: 'grep', calls: 122 },
      { tool: 'edit_file', calls: 96 },
    ],
  },
};

// ------------------------------------------------------------------ server --
function sendJson(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(payload),
    'Cache-Control': 'no-store',
  });
  res.end(payload);
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let raw = '';
    req.on('data', (chunk) => {
      raw += chunk;
      if (raw.length > 1_000_000) reject(new Error('body too large'));
    });
    req.on('end', () => {
      if (!raw) return resolve({});
      try {
        resolve(JSON.parse(raw));
      } catch (error) {
        reject(error);
      }
    });
    req.on('error', reject);
  });
}

function sse(res, event, data) {
  res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

function chunkText(text, words) {
  const pieces = text.match(/\S+\s*/g) ?? [text];
  const out = [];
  for (let index = 0; index < pieces.length; index += words) {
    out.push(pieces.slice(index, index + words).join(''));
  }
  return out;
}

/** The run parks on this tool call until a human answers. */
const PARKED_TOOL = {
  name: 'bash',
  input: { command: 'npm run test -- --watch=false', cwd: '/data/mygo/web' },
  summary: '运行测试',
  risk: 'exec',
};

async function handleRun(req, res) {
  let body;
  try {
    body = await readBody(req);
  } catch {
    return sendJson(res, 400, { error: 'invalid json' });
  }

  const { sessionId, text, regenerate, editMessageId } = body ?? {};
  // §4.6: regenerate is a *mode* of this endpoint. `text` must be absent — the
  // gateway reruns the user message it already holds, and a stray text here is
  // exactly the invalid_params the real gateway answers with.
  if (!sessionId) return sendJson(res, 400, { error: 'sessionId is required' });
  // §4.6 (edit mode, 2026-11-23): the two rewrites are mutually exclusive.
  if (regenerate === true && editMessageId !== undefined && editMessageId !== '') {
    return sendJson(res, 400, {
      // Same shape as run.go: the field is named, no `data.name` sentinel.
      error: { code: 'invalid_params', data: { field: 'editMessageId' } },
      message: 'editMessageId is mutually exclusive with regenerate',
    });
  }
  if (regenerate === true && text !== undefined) {
    return sendJson(res, 400, {
      error: { code: 'invalid_params', data: { field: 'text' } },
      message: 'regenerate must not carry text',
    });
  }
  if (editMessageId !== undefined && editMessageId !== '') {
    // The edit's text IS the new content; empty would mean "delete", which the
    // protocol does not offer.
    if (typeof text !== 'string' || text.trim() === '') {
      return sendJson(res, 400, {
        error: { code: 'invalid_params', data: { field: 'text' } },
        message: 'text is required when editMessageId is set',
      });
    }
  } else if (regenerate !== true && (typeof text !== 'string' || text.trim() === '')) {
    return sendJson(res, 400, { error: 'text is required' });
  }
  const target = sessions.get(sessionId);
  if (!target) return sendJson(res, 404, { error: 'unknown session' });

  const turnId = shortId('t');
  if (editMessageId !== undefined && editMessageId !== '') {
    // Edit = replace that user message's text and drop everything after it, then
    // rerun. The refusals mirror `editRefusal` in run.go.
    const editIndex = target.messages.findIndex((message) => message.id === editMessageId);
    const refuse = (name, reason) =>
      sendJson(res, 400, {
        error: { code: 'invalid_params', data: { field: 'editMessageId', name } },
        message: reason,
      });
    if (editIndex < 0) {
      return refuse('edit_target_not_found', 'no such user message in this session');
    }
    const row = target.messages[editIndex];
    if (row.role !== 'user') return refuse('edit_target_not_user', 'only user messages can be edited');
    if (Array.isArray(row.attachments) && row.attachments.length > 0) {
      return refuse('edit_target_has_attachments', 'messages with attachments cannot be edited');
    }
    if (!row.durable) return refuse('edit_target_compacted', 'the message is no longer in the durable history window');
    if (row.moved) return refuse('edit_target_moved', 'the conversation changed while the edit was being applied');
    // Replace the text and truncate the tail: the edit rewrites history.
    row.text = text;
    target.messages.splice(editIndex + 1);
    broadcastSessionReset(target.messages);
  } else if (regenerate === true) {
    // Roll the tail back exactly like SessionRegenerator: drop the trailing
    // assistant reply and the user message it answered, then rerun that prompt.
    const lastAssistant = [...target.messages].reverse().findIndex((m) => m.role === 'assistant');
    if (lastAssistant >= 0) target.messages.splice(target.messages.length - 1 - lastAssistant, 1);
    const lastUser = [...target.messages].reverse().findIndex((m) => m.role === 'user');
    if (lastUser >= 0) target.messages.splice(target.messages.length - 1 - lastUser, 1);
    target.messages.push(msg('user', target.lastPrompt ?? '（重生成）'));
    broadcastSessionReset(null);
  } else {
    target.messages.push(msg('user', text));
    target.lastPrompt = text;
  }
  if (target.title === '新会话' && typeof text === 'string') target.title = text.trim().slice(0, 24);
  target.updatedAt = nowIso();

  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });

  const run = { aborted: false, turnId, timers: new Set() };
  const wait = (ms) =>
    new Promise((resolve) => {
      const timer = setTimeout(() => {
        run.timers.delete(timer);
        resolve();
      }, ms);
      run.timers.add(timer);
    });

  res.on('close', () => {
    if (res.writableEnded) return;
    run.aborted = true;
    for (const timer of run.timers) clearTimeout(timer);
    run.timers.clear();
    console.log(`[mock] run ${turnId} aborted by client (not a stop request)`);
  });

  const assistant = msg('assistant', '');
  const steps = [];
  let assistantText = '';

  try {
    if (/error/i.test(text)) {
      await wait(60);
      if (run.aborted) return;
      sse(res, 'error', { code: 'provider_unavailable', message: '模型服务暂时不可用（mock 触发）' });
      return res.end();
    }

    const reasoning = '先确认这一轮需要什么信息，再决定要不要调用工具。';
    for (const piece of chunkText(reasoning, 6)) {
      if (run.aborted) return;
      sse(res, 'reasoning.delta', { turnId, text: piece });
      await wait(35);
    }
    assistant.reasoning = reasoning;

    // ---- the approval gate -------------------------------------------------
    if (permission.mode === 'workspace' || permission.mode === 'read-only') {
      const approval = createApproval({
        tool: PARKED_TOOL.name,
        summary: PARKED_TOOL.summary,
        risk: PARKED_TOOL.risk,
        sessionId,
        target: { kind: 'command', text: PARKED_TOOL.input.command },
        arguments: PARKED_TOOL.input,
      });
      const callId = shortId('call');
      sse(res, 'tool.start', { callId, name: PARKED_TOOL.name, input: PARKED_TOOL.input });

      const decision = await new Promise((resolve) => {
        approvalWaiters.set(approval.id, resolve);
        const timer = setTimeout(() => {
          approvalWaiters.delete(approval.id);
          approvals.delete(approval.id);
          resolve('deny');
        }, 120_000);
        run.timers.add(timer);
      });
      if (run.aborted) return;

      if (decision === 'deny') {
        const error = '用户拒绝了这次调用';
        sse(res, 'tool.result', { callId, ok: false, error });
        steps.push({ callId, name: PARKED_TOOL.name, input: PARKED_TOOL.input, error, status: 'error' });
      } else {
        const output = 'Tests  7 passed (7)';
        sse(res, 'tool.result', { callId, ok: true, output });
        steps.push({ callId, name: PARKED_TOOL.name, input: PARKED_TOOL.input, output, status: 'ok' });
      }
      await wait(120);
    }

    const answer = [
      '这一轮由 **mock 网关** 回答，用来验证 SSE 的七个事件按序到达。',
      '',
      '1. `reasoning.delta` 写入推理区',
      '2. `tool.start` / `tool.result` 生成工具卡',
      '3. `message.delta` 流式写入正文',
      '',
      '```ts',
      "const event = parseRunEvent({ event: 'message.delta', data: '{\"text\":\"hi\"}' });",
      '```',
      '',
      '发送含 `error` 的文字可以看到终止性错误；点停止可以看到中性「已停止」。',
    ].join('\n');

    for (const piece of chunkText(answer, 5)) {
      if (run.aborted) return;
      assistantText += piece;
      sse(res, 'message.delta', { turnId, text: piece });
      await wait(45);
    }
    if (run.aborted) return;

    sse(res, 'usage', { inputTokens: 1280, outputTokens: 320 });
    target.messages.push({
      ...assistant,
      text: assistantText,
      steps,
      usage: { inputTokens: 1280, outputTokens: 320 },
      stopReason: 'end_turn',
    });
    target.updatedAt = nowIso();
    sse(res, 'done', { turnId, stopReason: 'end_turn' });
    // toast.posted is a real gateway broadcast (§6.3); the mock emits one when a
    // turn settles so the client's toast path is exercised outside vitest too.
    wsNotify('toast.posted', {
      id: shortId('toast'),
      level: 'info',
      text: '这一轮已完成',
      sessionId: target.id,
      actionLabel: '查看会话',
      actionId: 'open-session',
    });
    res.end();
    console.log(`[mock] run ${turnId} done (${assistantText.length} chars)`);
  } catch (error) {
    if (!res.writableEnded) {
      sse(res, 'error', { code: 'internal', message: String(error) });
      res.end();
    }
  }
}

const server = createServer(async (req, res) => {
  // Fragment → cookie exchange (§3.1). Returns 204 with `Set-Cookie: zk=…`.
  if (req.url === '/auth' && req.method === 'POST') {
    let body;
    try {
      body = await readBody(req);
    } catch {
      return sendJson(res, 400, { error: { code: -32600, message: 'invalid_request' } });
    }
    if (!body || body.token !== AUTH_TOKEN) {
      return unauthorized(res, { 'Retry-After': '1' });
    }
    res.setHeader(
      'Set-Cookie',
      `${AUTH_COOKIE}=${AUTH_TOKEN}; HttpOnly; SameSite=Strict; Path=/`,
    );
    res.writeHead(204);
    return res.end();
  }

  const url = new URL(req.url ?? '/', `http://${req.headers.host ?? HOST}`);
  const path = url.pathname;
  const method = req.method ?? 'GET';

  // ------------------------------------------------------------ sessions --
  if (path === '/api/sessions' && method === 'GET') {
    return sendJson(res, 200, {
      sessions: [...sessions.values()]
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
        .map(({ id, title, updatedAt }) => ({ id, title, updatedAt })),
    });
  }
  if (path === '/api/sessions' && method === 'POST') {
    const created = session('新会话');
    return sendJson(res, 200, {
      session: { id: created.id, title: created.title, updatedAt: created.updatedAt },
    });
  }

  const childMatch = path.match(/^\/api\/sessions\/([\w-]+)\/children\/([\w-]+)$/);
  if (childMatch && method === 'GET') {
    const children = childOf.get(childMatch[1]) ?? [];
    const child = children.find((item) => item.id === childMatch[2]);
    if (!child) return sendJson(res, 404, { error: 'unknown child session' });
    return sendJson(res, 200, child);
  }

  const memoryMatch = path.match(/^\/api\/sessions\/([\w-]+)\/memory$/);
  if (memoryMatch && method === 'GET') {
    return sendJson(res, 200, { entries: memoryEntries });
  }

  const snapshotMatch = path.match(/^\/api\/sessions\/([\w-]+)$/);
  if (snapshotMatch && method === 'GET') {
    const found = sessions.get(snapshotMatch[1]);
    if (!found) return sendJson(res, 404, { error: 'unknown session' });
    return sendJson(res, 200, {
      id: found.id,
      title: found.title,
      messages: found.messages,
      children: (childOf.get(found.id) ?? []).map(({ id, title }) => ({ id, title })),
    });
  }

  // ----------------------------------------------------------- approvals --
  if (path === '/api/approvals' && method === 'GET') {
    return sendJson(res, 200, {
      mode: permission.mode,
      approvals: [...approvals.values()].sort((a, b) => a.createdAt.localeCompare(b.createdAt)),
    });
  }
  const resolveMatch = path.match(/^\/api\/approvals\/([\w-]+)\/resolve$/);
  if (resolveMatch && method === 'POST') {
    const body = await readBody(req).catch(() => ({}));
    const decision = body?.decision;
    if (!['allow', 'allow_always', 'deny'].includes(decision)) {
      return sendJson(res, 400, { error: 'decision must be allow|allow_always|deny' });
    }
    if (!resolveApproval(resolveMatch[1], decision)) {
      return sendJson(res, 404, { error: 'unknown or expired approval' });
    }
    return sendJson(res, 200, { ok: true });
  }
  if (path === '/api/approval-mode' && method === 'GET') {
    return sendJson(res, 200, permission);
  }

  // --------------------------------------------------------------- inbox --
  if (path === '/api/inbox' && method === 'GET') {
    const items = [];
    // An approval is the same entity the thread card shows, not a copy.
    for (const approval of approvals.values()) {
      const source = sessions.get(approval.sessionId);
      items.push({
        id: approval.id,
        kind: 'approval',
        sessionId: approval.sessionId,
        sessionTitle: source?.title ?? '',
        title: `${approval.summary} · ${approval.tool}`,
        detail: approval.target?.text ?? '',
        createdAt: approval.createdAt,
        expiresAt: approval.expiresAt,
        decision: 'pending',
      });
    }
    for (const item of inbox.values()) {
      const source = sessions.get(item.sessionId);
      items.push({ ...item, sessionTitle: source?.title ?? '' });
    }
    items.sort((a, b) => String(a.createdAt).localeCompare(String(b.createdAt)));
    return sendJson(res, 200, { items });
  }
  const inboxAct = path.match(/^\/api\/inbox\/([\w-]+)\/act$/);
  if (inboxAct && method === 'POST') {
    const body = await readBody(req).catch(() => ({}));
    const id = inboxAct[1];
    if (body?.action === 'deny') resolveApproval(id, 'deny');
    else if (body?.action === 'allow') resolveApproval(id, 'allow');
    else inbox.delete(id);
    return sendJson(res, 200, { ok: true });
  }

  // ----------------------------------------------------------------- run --
  // The single-turn token stream (§4.6). Authenticated like everything else.
  if (path === '/api/chat/run' && method === 'POST') {
    return handleRun(req, res);
  }

  // -------------------------------------------------------- prompt queue --
  // Note: the prompt inbox is a WS domain (`prompts.*`) — it deliberately has
  // no REST shape any more, so the client cannot keep a second path alive.

  sendJson(res, 404, { error: 'not found' });
});


// ---------------------------------------------------------------- auth --
// The mock authenticates like the gateway (docs/TRANSPORT.md §3.1), because a
// laxer mock hides exactly this class of bug: the client "worked" here while it
// could not connect to the real gateway at all.
//
// What is NOT modelled: the Host and Origin checks (C2/C3). Under `npm run dev`
// the page's origin is the Vite port while the proxy connects to us, so a
// faithful Origin check would reject every dev request — modelling it would
// mean modelling the proxy, not the gateway.
const AUTH_TOKEN = process.env.ZK_TOKEN ?? 'dev-token';
const AUTH_COOKIE = 'zk';

const cookieToken = (req) => {
  const header = req.headers.cookie ?? '';
  for (const part of header.split(';')) {
    const [name, ...rest] = part.trim().split('=');
    if (name === AUTH_COOKIE) return rest.join('=');
  }
  return null;
};

const authorized = (req) => {
  if (cookieToken(req) === AUTH_TOKEN) return true;
  // **MOCK-ONLY convenience, not the contract.** The real gateway accepts a
  // cookie or `Authorization: Bearer` and has no query-token path (`auth.go`).
  // This exists because a browser cannot set headers on a WebSocket handshake
  // and the integration tests need to drive the WS plane; product code must
  // never send it — asserted by `src/api/token-query.test.ts`.
  const url = new URL(req.url ?? '/', `http://${req.headers.host ?? '127.0.0.1'}`);
  const queryToken = url.searchParams.get('token');
  if (queryToken !== null && queryToken === AUTH_TOKEN) return true;
  // §3.1: non-browser clients (tests, curl, scripts) use a Bearer token.
  const header = String(req.headers.authorization ?? '');
  return header.toLowerCase().startsWith('bearer ') && header.slice(7).trim() === AUTH_TOKEN;
};

const unauthorized = (res, extraHeaders = {}) => {
  for (const [name, value] of Object.entries(extraHeaders)) res.setHeader(name, value);
  sendJson(res, 401, { error: { code: 1020, message: 'unauthorized', data: { name: 'unauthorized' } } });
};

// ------------------------------------------------------ WS control plane --
// The browser client speaks only JSON-RPC over WS (docs/TRANSPORT.md §4.1), so
// the mock does too: one client code path for mock and real gateway. The REST
// routes above stay for the surfaces that are still mock-only (prompt queue,
// files, capabilities, automation, insights, memory, child sessions).

const WS_EPOCH = randomUUID().replace(/-/g, '').slice(0, 16);
/** Conditional capabilities this mock actually serves. */
const MOCK_CONDITIONAL = ['context', 'tasks', 'regenerate'];

// Derived from the generated contract: every static capability, plus the
// conditional halves this mock implements (named by `conditional[].name`).
const WS_CAPABILITIES = [
  ...STATIC_CAPABILITIES,
  ...CONDITIONAL_CAPABILITIES.filter((item) => MOCK_CONDITIONAL.includes(item.name)).map(
    (item) => item.name,
  ),
];
const wsClients = new Set();

/** Real settings rows (contracts/settingskeys.go names only). */
const settingsRows = new Map([
  ['agent.permission', 'workspace'],
  ['agent.approvalTimeoutSeconds', '900'],
  ['agent.subagentMaxConcurrent', '4'],
  ['agent.teamMaxConcurrent', '3'],
  ['toolkit.shellTimeoutSeconds', '120'],
  ['toolkit.shellMaxOutputKiB', '256'],
  ['ui.theme.mode', 'system'],
  // The language row is **not** seeded when the caller asks for a cold store:
  // `ACCEPTANCE_DATA_DIR` pointing at an empty directory is exactly what CI
  // starts from, and the client must fall back to `navigator.language` there.
  ...(COLD_STORE ? [] : [['ui.language', 'zh']]),
  ['ui.chatFontSize', '14'],
  ['ui.workSteps.mode', 'standard'],
  ['ui.workSteps.collapse', 'completion'],
  ['ui.notifications', 'true'],
  ['ui.notifications.system', 'false'],
  ['ui.sessions.showArchived', 'false'],
  ['net.proxy', ''],
  ['net.noProxy', 'localhost,127.0.0.1'],
]);

const workspaces = [
  { id: 'ws-mygo', name: 'mygo', root: '/data/mygo', color: '#5b8def' },
  { id: 'ws-web', name: 'web', root: '/data/mygo/web', color: '#7a5bf0' },
];
let currentWorkspaceId = 'ws-mygo';

const wsRevs = { list: 0, approvals: 0, settings: 0, workspace: 0, ask: 0, plan: 0, artifacts: 0 };
const lastPublished = { list: [], approvals: [], settings: {}, workspace: [] };

const wsFrame = (payload) => {
  const data = Buffer.from(payload, 'utf8');
  const length = data.length;
  let header;
  if (length < 126) header = Buffer.from([0x81, length]);
  else if (length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x81;
    header[1] = 126;
    header.writeUInt16BE(length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x81;
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(length), 2);
  }
  return Buffer.concat([header, data]);
};

const wsSend = (client, message) => {
  try {
    client.socket.write(wsFrame(`${JSON.stringify(message)}\n`));
  } catch {
    wsClients.delete(client);
  }
};

const wsResult = (client, id, result) => wsSend(client, { jsonrpc: '2.0', id, result });
const wsError = (client, id, code, message, name) =>
  wsSend(client, {
    jsonrpc: '2.0',
    id,
    error: { code, message, data: name ? { name } : {} },
  });
const wsNotify = (method, params) => {
  for (const client of wsClients) {
    if (client.ready) wsSend(client, { jsonrpc: '2.0', method, params });
  }
};

const approvalWire = (approval) => ({
  id: approval.id,
  tool: approval.tool,
  summary: approval.summary,
  arguments:
    typeof approval.arguments === 'string'
      ? approval.arguments
      : approval.arguments
        ? JSON.stringify(approval.arguments)
        : undefined,
  sessionId: approval.sessionId,
  source: approval.source,
  createdAt: approval.createdAt,
  expiresAt: approval.expiresAt,
});

const sessionInfoWire = (value) => ({
  id: value.id,
  title: value.title,
  updatedAt: value.updatedAt,
  running: Boolean(value.running),
  unread: false,
  messageCount: value.messages.length,
});

const sessionWire = (value) => ({
  id: value.id,
  title: value.title,
  model: 'deepseek-v4.1-flash',
  running: Boolean(value.running),
  interrupted: false,
  messages: value.messages.map((message) => ({
    id: message.id,
    role: message.role,
    text: message.text,
    reasoning: message.reasoning,
    streaming: message.streaming,
    usage: message.usage
      ? { promptTokens: message.usage.inputTokens, completionTokens: message.usage.outputTokens }
      : undefined,
    tools: (message.steps ?? []).map((step) => ({
      id: step.callId,
      name: step.name,
      status: step.status === 'running' ? 'running' : step.status === 'error' ? 'error' : 'succeeded',
      summary: typeof step.output === 'string' ? step.output : undefined,
      arguments: step.input ? JSON.stringify(step.input) : undefined,
    })),
    createdAt: message.createdAt,
  })),
});

const publishList = () => {
  const next = [...sessions.values()].map(sessionInfoWire);
  const previous = lastPublished.list;
  const byId = new Map(previous.map((item) => [item.id, item]));
  const added = next.filter((item) => !byId.has(item.id));
  const changed = next.filter((item) => {
    const before = byId.get(item.id);
    return before && JSON.stringify(before) !== JSON.stringify(item);
  });
  const removed = previous.filter((item) => !next.some((now) => now.id === item.id)).map((item) => item.id);
  lastPublished.list = next;
  const rev = ++wsRevs.list;
  wsNotify('session.list.delta', { baseRev: rev - 1, rev, added, changed, removed });
};

publishApprovals = () => {
  const next = [...approvals.values()].map(approvalWire);
  const previous = lastPublished.approvals;
  const previousIds = new Set(previous.map((item) => item.id));
  const nextIds = new Set(next.map((item) => item.id));
  const added = next.filter((item) => !previousIds.has(item.id));
  const removed = previous.filter((item) => !nextIds.has(item.id)).map((item) => item.id);
  lastPublished.approvals = next;
  const rev = ++wsRevs.approvals;
  wsNotify('approvals.delta', { baseRev: rev - 1, rev, added, removed });
};

const publishSettings = (rows) => {
  const changed = {};
  for (const [key, value] of rows) {
    if (lastPublished.settings[key] !== value) changed[key] = value;
  }
  const removed = Object.keys(lastPublished.settings).filter((key) => !rows.has(key));
  lastPublished.settings = Object.fromEntries(rows);
  const rev = ++wsRevs.settings;
  wsNotify('settings.delta', { baseRev: rev - 1, rev, rows: changed, removed });
};

const publishWorkspaces = () => {
  const next = [...workspaces];
  const previous = lastPublished.workspace;
  const byId = new Map(previous.map((item) => [item.id, item]));
  const added = next.filter((item) => !byId.has(item.id));
  const changed = next.filter((item) => {
    const before = byId.get(item.id);
    return before && JSON.stringify(before) !== JSON.stringify(item);
  });
  const removed = previous.filter((item) => !next.some((now) => now.id === item.id)).map((item) => item.id);
  lastPublished.workspace = next;
  const rev = ++wsRevs.workspace;
  wsNotify('workspace.list.delta', {
    baseRev: rev - 1,
    rev,
    added,
    changed,
    removed,
    currentId: currentWorkspaceId,
  });
};

const wsDispatch = (client, message) => {
  const { id, method } = message;
  const params = message.params ?? {};
  const reply = (result) => wsResult(client, id, result);
  const fail = (code, name, text) => wsError(client, id, code, text ?? name, name);

  if (method !== 'initialize' && method !== 'ready' && !client.ready) {
    return fail(1024, 'not_ready');
  }
  // Strict like the gateway: an unknown or missing field is invalid_params, not
  // something to ignore. This is what keeps a `{text}` vs `{prompt}` mismatch
  // from surviving into the real gateway.
  const paramsCheck = validateParams(method, params);
  if (!paramsCheck.ok) {
    return wsSend(client, {
      jsonrpc: '2.0',
      id,
      error: {
        code: -32602,
        message: `invalid params: ${paramsCheck.field} ${paramsCheck.reason}`,
        data: { name: 'invalid_params', field: paramsCheck.field, reason: paramsCheck.reason },
      },
    });
  }

  switch (method) {
    case 'initialize':
      return reply({
        epoch: WS_EPOCH,
        protocolVersion: 1,
        serverVersion: 'mock',
        capabilities: WS_CAPABILITIES,
        limits: {
          maxFrameBytes: 16 * 1024 * 1024,
          maxAttachmentBytes: 10 * 1024 * 1024,
          snapshotTailDefault: 60,
          deltaRingBytes: 256 * 1024,
        },
      });
    case 'ready':
      client.ready = true;
      return reply({});
    case 'ping':
      return reply({ pong: true, epoch: WS_EPOCH });
    case 'session.list.snapshot': {
      const sessionsNow = [...sessions.values()].map(sessionInfoWire);
      lastPublished.list = sessionsNow;
      return reply({ epoch: WS_EPOCH, rev: wsRevs.list, sessions: sessionsNow });
    }
    case 'session.snapshot': {
      const value = sessions.get(params.sessionId);
      if (!value) return fail(1001, 'session_not_found');
      return reply({
        epoch: WS_EPOCH,
        rev: wsRevs.list,
        session: sessionWire(value),
        truncated: false,
      });
    }
    case 'session.subscribe':
      // Subscribing is how the client says which session it is looking at, so
      // that is what `session.active.get` must report (no invented method).
      activeSessionId = params.sessionId === undefined ? null : String(params.sessionId);
      // `sinceRev` decides whether a replay is needed; the mock keeps no buffer,
      // so it answers resync:false only when the client is already current.
      const since = params.sinceRev === undefined ? wsRevs.list : Number(params.sinceRev);
      return reply({ epoch: WS_EPOCH, rev: wsRevs.list, resync: since < wsRevs.list });
    case 'session.unsubscribe':
      return reply({});
    case 'session.rename': {
      const target = sessions.get(String(params.sessionId ?? ''));
      if (target) target.title = String(params.title ?? target.title);
      publishList();
      return reply({});
    }
    case 'session.delete': {
      const sessionId = String(params.sessionId ?? '');
      sessions.delete(sessionId);
      publishList();
      return reply({});
    }
    case 'session.archive': {
      const target = sessions.get(String(params.sessionId ?? ''));
      if (target) target.archived = Boolean(params.archived);
      publishList();
      return reply({});
    }
    case 'session.pin': {
      const target = sessions.get(String(params.sessionId ?? ''));
      if (target) target.pinned = Boolean(params.pinned);
      publishList();
      return reply({});
    }
    case 'mcp.reload': {
      const disabled = Array.isArray(params.disabled) ? params.disabled.map(String) : [];
      return reply({ connectors: [], disabled });
    }
    case 'session.create': {
      const created = session(params.title || '新会话');
      publishList();
      return reply({ sessionId: created.id });
    }
    case 'approvals.snapshot': {
      const pending = [...approvals.values()].map(approvalWire);
      lastPublished.approvals = pending;
      return reply({
        epoch: WS_EPOCH,
        rev: wsRevs.approvals,
        mode: permission.mode,
        permission: permission.mode,
        approvalTimeoutSeconds: 900,
        pending,
      });
    }
    case 'approval.resolve': {
      if (!['allow', 'allow_always', 'deny'].includes(params.decision)) {
        return fail(-32602, 'invalid_params');
      }
      if (!resolveApproval(params.id, params.decision)) {
        return fail(1019, 'approval_not_pending');
      }
      publishApprovals();
      return reply({ resolved: true });
    }
    case 'settings.snapshot': {
      const rows = Object.fromEntries(settingsRows);
      lastPublished.settings = rows;
      return reply({ epoch: WS_EPOCH, rev: wsRevs.settings, rows });
    }
    case 'settings.set': {
      if (typeof params.key !== 'string' || params.key === '') {
        return fail(-32602, 'invalid_params');
      }
      settingsRows.set(params.key, String(params.value ?? ''));
      publishSettings(settingsRows);
      return reply({});
    }
    case 'workspace.list.snapshot': {
      const list = [...workspaces];
      lastPublished.workspace = list;
      return reply({
        epoch: WS_EPOCH,
        rev: wsRevs.workspace,
        workspaces: list,
        currentId: currentWorkspaceId,
      });
    }
    case 'prompts.snapshot': {
      const sessionId = String(params.sessionId ?? '');
      if (!sessions.has(sessionId)) return fail(1001, 'session_not_found');
      const prompts = queueOf(sessionId).map((item) => ({
        id: item.id,
        text: item.text,
        createdAt: item.createdAt,
      }));
      publishedPrompts.set(sessionId, prompts);
      return reply({ epoch: WS_EPOCH, rev: promptRev(sessionId), prompts });
    }
    case 'prompts.subscribe': {
      const sessionId = String(params.sessionId ?? '');
      const since = Number(params.sinceRev ?? 0);
      // The client took a snapshot first; anything it missed can only be
      // honoured when the revision is the one we hold. Otherwise resync.
      return reply({ epoch: WS_EPOCH, rev: promptRev(sessionId), resync: since !== promptRev(sessionId) });
    }
    case 'prompts.unsubscribe':
      return reply({});
    case 'context.snapshot': {
      const sessionId = String(params.sessionId ?? '');
      if (!sessions.has(sessionId)) return fail(1001, 'session_not_found');
      const view = contextView(sessionId);
      return reply({
        epoch: WS_EPOCH,
        rev: view.rev,
        sessionId,
        found: view.found,
        ...(view.found ? { context: view.context } : {}),
      });
    }
    case 'context.subscribe': {
      const sessionId = String(params.sessionId ?? '');
      if (!sessions.has(sessionId)) return fail(1001, 'session_not_found');
      const since = Number(params.sinceRev ?? 0);
      const view = contextView(sessionId);
      return reply({ epoch: WS_EPOCH, rev: view.rev, resync: since !== view.rev });
    }
    case 'context.unsubscribe':
      // Dropping a subscription is not an error for a conversation this fixture
      // is not tracking: the real gateway answers {} either way.
      return reply({});
    case 'run.interject': {
      // Offline fixture: no real run to steer, so acknowledge and record the request.
      const sessionId = String(params.sessionId ?? '').trim();
      const text = String(params.text ?? '').trim();
      if (!sessionId) return fail(1003, 'sessionId is required');
      if (!text) return fail(1003, 'text is required');
      const attachments = Array.isArray(params.attachments) ? params.attachments.length : 0;
      intercepted.push({ sessionId, text, attachments });
      return reply({ ok: true });
    }
    // The update surface (§5.1.12). The mock cannot reach GitHub, so it
    // reports a fixed "you are current" — the point is that the client's four
    // calls have a dispatcher, not that the fixture invents releases.
    case 'update.status':
      return reply({
        currentVersion: '0.0.0-mock',
        checkedAt: new Date().toISOString(),
      });
    case 'update.check':
      return reply({
        currentVersion: '0.0.0-mock',
        checkedAt: new Date().toISOString(),
      });
    case 'update.download':
      return reply({ currentVersion: '0.0.0-mock', error: 'mock 不下载更新包' });
    case 'update.install':
      // `restart` selects install-now vs install-on-next-start; the mock has
      // nothing staged, so it only acknowledges the shape.
      return reply({
        currentVersion: '0.0.0-mock',
        ...(params.restart === true ? { error: 'mock 没有已下载的更新' } : {}),
      });
    case 'team.snapshot': {
      return reply({ members: mockTeam.members.slice(), activities: mockTeam.activities.slice() });
    }
    case 'team.spawn': {
      const name = String(params.name ?? '').trim();
      const prompt = String(params.prompt ?? '').trim();
      if (!name || !prompt) return fail(1003, 'name and prompt are required');
      const member = {
        id: `member-${mockTeam.members.length + 1}`,
        name,
        status: 'active',
        runState: 'idle',
        model: String(params.model ?? ''),
        created: new Date().toISOString(),
        last: '',
        error: '',
      };
      mockTeam.members.push(member);
      mockTeam.activities.push({ id: `act-${mockTeam.activities.length + 1}`, memberId: member.id, kind: 'spawn', text: `spawned ${name}`, at: member.created });
      return reply({ member });
    }
    case 'team.send': {
      const id = String(params.id ?? '');
      const message = String(params.message ?? '').trim();
      if (!message) return fail(1003, 'message is required');
      if (!mockTeam.members.some((m) => m.id === id)) return fail(1001, 'member_not_found');
      mockTeam.activities.push({
        id: `act-${mockTeam.activities.length + 1}`,
        memberId: id,
        kind: 'message',
        text: message,
        at: new Date().toISOString(),
      });
      return reply(params.async ? { ok: true } : { answer: '' });
    }
    case 'team.wait': {
      return reply({ answer: '' });
    }
    case 'team.interrupt':
    case 'team.close': {
      const id = String(params.id ?? '');
      if (!mockTeam.members.some((m) => m.id === id)) return fail(1001, 'member_not_found');
      return reply({ ok: true });
    }
    case 'team.activity': {
      const limit = Number(params.limit ?? 50);
      const items = mockTeam.activities.slice(-Math.max(1, limit));
      return reply({ items });
    }
    case 'tasks.snapshot': {
      // Process-wide board when sessionId is absent, per-session otherwise
      // (§5.1.8). Same shape either way, which is why one handler serves both.
      const sessionId = params.sessionId === undefined ? '' : String(params.sessionId);
      const board = taskBoard(sessionId);
      return reply({ epoch: WS_EPOCH, rev: board.rev, tasks: board.tasks.slice() });
    }
    case 'tasks.subscribe': {
      const sessionId = params.sessionId === undefined ? '' : String(params.sessionId);
      const since = Number(params.sinceRev ?? 0);
      const board = taskBoard(sessionId);
      return reply({ epoch: WS_EPOCH, rev: board.rev, resync: since !== board.rev });
    }
    case 'tasks.unsubscribe':
      return reply({});
    case 'task.status': {
      const sessionId = params.sessionId === undefined ? '' : String(params.sessionId);
      const status = String(params.status ?? '');
      if (!['pending', 'in_progress', 'completed'].includes(status)) {
        return fail(-32602, 'invalid_params');
      }
      const board = taskBoard(sessionId);
      const row = board.tasks.find((task) => task.id === params.id);
      if (!row) return fail(1001, 'task_not_found');
      row.status = status;
      row.updatedAt = nowIso();
      publishTasks(sessionId);
      return reply({});
    }
    case 'task.delete': {
      const sessionId = params.sessionId === undefined ? '' : String(params.sessionId);
      const board = taskBoard(sessionId);
      const index = board.tasks.findIndex((task) => task.id === params.id);
      if (index >= 0) board.tasks.splice(index, 1);
      publishTasks(sessionId);
      return reply({});
    }
    // ------------------------------------------- inbound work inbox (§5.1.13) --
    case 'inbox.snapshot': {
      const statuses = Array.isArray(params.statuses) ? params.statuses.map(String) : null;
      const source = params.source === undefined ? '' : String(params.source);
      const includeExpired = params.includeExpired === true;
      const limit = params.limit === undefined ? 0 : Math.max(0, Number(params.limit) || 0);
      const nowMs = Date.now();
      // The zero filter means "still actionable" (new + triaged); a caller that
      // also wants decided rows names the statuses, exactly like the gateway.
      const items = mockInbox.items.filter((item) => {
        if (statuses && !statuses.includes(item.status)) return false;
        if (source !== '' && item.source !== source) return false;
        if (!includeExpired && item.expiresAt && Date.parse(item.expiresAt) <= nowMs) return false;
        return true;
      });
      return reply({
        items: limit > 0 ? items.slice(0, limit) : items.map((item) => ({ ...item })),
        stats: {
          total: mockInbox.items.length,
          new: mockInbox.items.filter((item) => item.status === 'new').length,
          triaged: mockInbox.items.filter((item) => item.status !== 'new').length,
        },
      });
    }
    case 'inbox.dispatch': {
      const item = mockInbox.items.find((row) => row.id === params.id);
      if (!item) return fail(-32600, 'inbox: unknown item');
      const subject = String(params.subject ?? '').trim();
      if (subject === '') return fail(-32602, 'invalid_params');
      const blockedBy = Array.isArray(params.blockedBy) ? params.blockedBy.map(String) : [];
      const writeScopes = Array.isArray(params.writeScopes) ? params.writeScopes.map(String) : [];
      const board = taskBoard('');
      const taskId = shortId('task');
      board.tasks.push({
        id: taskId,
        subject,
        status: 'pending',
        ...(params.detail ? { description: String(params.detail) } : {}),
        ...(params.assignee ? { owner: String(params.assignee) } : {}),
        ...(blockedBy.length > 0 ? { blockedBy } : {}),
        ...(writeScopes.length > 0 ? { writeScopes } : {}),
        createdAt: nowIso(),
        updatedAt: nowIso(),
      });
      item.status = 'task';
      item.taskIds = [...(item.taskIds ?? []), taskId];
      publishTasks('');
      return reply({ taskIds: [taskId], created: true, item: { ...item } });
    }
    case 'inbox.triage': {
      const item = mockInbox.items.find((row) => row.id === params.id);
      if (!item) return fail(-32600, 'inbox: unknown item');
      const action = String(params.action ?? '');
      if (!['triaged', 'task', 'ignored', 'archived'].includes(action)) {
        return fail(-32602, 'invalid_params');
      }
      item.status = action;
      return reply({ ...item });
    }
    case 'tasks.budget.set': {
      const id = String(params.id ?? '');
      if (!id) return fail(-32602, 'invalid_params');
      const previous = mockTaskBudgets.get(id) ?? { taskId: id, maxTokens: 0, maxMinutes: 0, usedTokens: 0, exhausted: false };
      const maxTokens = params.maxTokens === undefined ? previous.maxTokens : Number(params.maxTokens);
      const maxMinutes = params.maxMinutes === undefined ? previous.maxMinutes : Number(params.maxMinutes);
      if (maxTokens < 0 || maxMinutes < 0) return fail(-32600, 'lease: negative budget');
      const budget = { ...previous, maxTokens, maxMinutes, exhausted: maxTokens > 0 && previous.usedTokens > maxTokens };
      mockTaskBudgets.set(id, budget);
      return reply(budget);
    }
    case 'tasks.budget.get': {
      const id = String(params.id ?? '');
      const budget = mockTaskBudgets.get(id);
      return reply(budget ? budget : { budget: null });
    }
    case 'tasks.budget.consume': {
      const id = String(params.id ?? '');
      const tokens = Number(params.tokens ?? 0);
      const previous = mockTaskBudgets.get(id);
      if (!previous) return fail(-32600, 'lease: no budget');
      const used = previous.usedTokens + tokens;
      const exhausted = previous.maxTokens > 0 && used > previous.maxTokens;
      const budget = { ...previous, usedTokens: used, exhausted };
      mockTaskBudgets.set(id, budget);
      if (exhausted) {
        const held = mockLeases.get(id);
        if (held) held.state = 'blocked';
        return fail(-32600, 'lease: task budget exhausted');
      }
      return reply(budget);
    }
    case 'tasks.approval.set': {
      const id = String(params.id ?? '');
      const policy = String(params.policy ?? '');
      if (!id) return fail(-32602, 'invalid_params');
      if (policy !== 'none' && policy !== 'before_start') return fail(-32600, 'lease: unknown approval policy');
      mockTaskApproval.set(id, policy);
      return reply({ policy });
    }
    case 'tasks.approval.get': {
      const id = String(params.id ?? '');
      const policy = mockTaskApproval.get(id);
      return reply({ policy: policy ?? 'none', set: policy !== undefined });
    }
    case 'tasks.approve': {
      const id = String(params.id ?? '');
      // The approver is who said yes; the mock records it on the lease so a
      // test can tell an approval apart from a plain claim.
      const approver = String(params.approver ?? '');
      const held = mockLeases.get(id);
      if (!held || held.state !== 'awaiting_approval') return fail(-32600, 'lease: not awaiting approval');
      held.state = 'claimed';
      held.reviewer = approver;
      return reply({ ...held, evidence: held.evidence.slice() });
    }
    case 'tasks.reject-start': {
      const id = String(params.id ?? '');
      const approver = String(params.approver ?? '');
      const note = String(params.note ?? '');
      if (!approver || !note) return fail(-32602, 'invalid_params');
      const held = mockLeases.get(id);
      if (!held) return fail(-32600, 'lease: not held');
      mockLeases.delete(id);
      return reply({ ok: true });
    }
    case 'tasks.tools.set': {
      const id = String(params.id ?? '');
      const tools = Array.isArray(params.tools) ? params.tools.map(String) : null;
      if (!id || tools === null) return fail(-32602, 'invalid_params');
      if (tools.length > 32) return fail(-32600, 'lease: too many tools');
      mockTaskTools.set(id, { taskId: id, tools, updatedAt: nowIso() });
      return reply(mockTaskTools.get(id));
    }
    case 'tasks.tools.get': {
      const id = String(params.id ?? '');
      const bound = mockTaskTools.get(id);
      return reply(bound ? bound : { tools: null });
    }
    case 'tasks.lease': {
      const held = mockLeases.get(String(params.id ?? ''));
      if (!held) return reply({ lease: null });
      return reply({ ...held, evidence: held.evidence.slice() });
    }
    case 'tasks.claim': {
      const taskId = String(params.id ?? '');
      const owner = String(params.owner ?? '');
      const held = mockLeases.get(taskId);
      if (held && held.owner !== owner) return fail(-32600, 'lease: task already claimed');
      return reply(leaseFor(taskId, owner, 'claimed'));
    }
    case 'tasks.heartbeat': {
      const held = mockLeases.get(String(params.id ?? ''));
      if (!held || held.owner !== String(params.owner ?? '')) {
        return fail(-32600, 'lease: task not held');
      }
      held.heartbeat = nowIso();
      return reply({ ...held, evidence: held.evidence.slice() });
    }
    case 'tasks.submit': {
      const held = mockLeases.get(String(params.id ?? ''));
      if (!held || held.owner !== String(params.owner ?? '')) {
        return fail(-32600, 'lease: task not held');
      }
      const evidence = Array.isArray(params.evidence) ? params.evidence : [];
      if (evidence.length === 0) return fail(1010, 'evidence_required');
      held.evidence = evidence.map((row) => ({
        kind: String(row.kind ?? ''),
        ref: String(row.ref ?? ''),
        ...(row.note ? { note: String(row.note) } : {}),
      }));
      held.state = 'review';
      return reply({ ...held, evidence: held.evidence.slice() });
    }
    case 'tasks.review': {
      const held = mockLeases.get(String(params.id ?? ''));
      if (!held) return fail(-32600, 'lease: task not held');
      const decision = String(params.decision ?? '');
      if (decision !== 'accept' && decision !== 'reject') return fail(-32602, 'invalid_params');
      // The contract requires a reviewer; the mock reads it but does not echo it
      // in the lease (the wire DTO has no reviewer field).
      const reviewer = String(params.reviewer ?? '');
      void reviewer;
      held.state = decision === 'accept' ? 'done' : 'rejected';
      held.reviewNote = String(params.note ?? '');
      return reply({ ...held, evidence: held.evidence.slice() });
    }
    case 'approval.token': {
      const approvalId = String(params.approvalId ?? '');
      if (!approvals.has(approvalId)) return fail(1019, 'approval_not_pending');
      const token = `mock-token-${approvalId}`;
      mockApprovalTokens.add(token);
      return reply({ token, expiresAt: new Date(Date.now() + 60_000).toISOString() });
    }
    case 'approval.action': {
      const token = String(params.token ?? '');
      if (!mockApprovalTokens.has(token)) return fail(-32600, 'approval: token expired');
      const decision = String(params.decision ?? '');
      if (decision !== 'allow' && decision !== 'deny') return fail(-32602, 'invalid_params');
      mockApprovalTokens.delete(token);
      if (!resolveApproval(String(params.approvalId ?? ''), decision)) {
        return fail(1019, 'approval_not_pending');
      }
      publishApprovals();
      return reply({ ok: true });
    }
    case 'notify.route.snapshot':
      return reply({ routes: mockNotifyRoutes.map((route) => ({ ...route })) });
    case 'notify.route.upsert': {
      const category = String(params.category ?? '');
      const primary = String(params.primary ?? '');
      if (category === '' || primary === '') return fail(-32602, 'invalid_params');
      const route = {
        category,
        primary,
        fallback: Array.isArray(params.fallback) ? params.fallback.map(String) : [],
        ...(params.quietHours ? { quietHours: params.quietHours } : {}),
        ...(params.escalationMs === undefined ? {} : { escalationMs: Number(params.escalationMs) }),
        enabled: params.enabled !== false,
      };
      const at = mockNotifyRoutes.findIndex((row) => row.category === category);
      if (at >= 0) mockNotifyRoutes[at] = route;
      else mockNotifyRoutes.push(route);
      return reply({ ...route });
    }
    case 'wechat.login.start': {
      const qrId = `qr-${mockWechatLogins.length + 1}`;
      mockWechatLogins.push({ qrId, polls: 0 });
      return reply({ qrId, qrUrl: `https://open.weixin.qq.com/qr/mock-${qrId}`, expiresAt: new Date(Date.now() + 300000).toISOString() });
    }
    case 'wechat.login.state': {
      const qrId = String(params.qrId ?? '');
      const entry = mockWechatLogins.find((row) => row.qrId === qrId);
      if (!entry) return reply({ state: 'expired', connected: false });
      entry.polls += 1;
      // The mock walks pending -> waiting -> confirmed so the UI can be driven.
      const state = entry.polls === 1 ? 'pending' : entry.polls === 2 ? 'waiting' : 'confirmed';
      return reply({ state, connected: state === 'confirmed' });
    }
    case 'team.note.review': {
      const id = String(params.id ?? '');
      if (!id) return fail(-32602, 'invalid_params');
      if (params.accept === undefined) return fail(-32602, 'invalid_params');
      const note = mockTeamNotes.find((row) => row.id === id);
      if (!note) return fail(-32600, 'team notes: unknown note');
      note.reviewed = true;
      note.reviewedBy = String(params.reviewer ?? '');
      note.reviewedAt = params.at ? String(params.at) : nowIso();
      return reply(note);
    }
    case 'notify.plan': {
      const category = String(params.category ?? '');
      return reply({ channels: ['inapp'], deferred: false, ...(category ? {} : {}) });
    }
    case 'notify.deliver': {
      const category = String(params.category ?? '');
      const text = String(params.text ?? '');
      if (!category || !text) return fail(-32602, 'invalid_params');
      return reply({ channel: 'inapp', attempts: ['inapp'], deferred: false });
    }
    case 'team.notes': {
      return reply({ notes: mockTeamNotes.slice(0, Number(params.limit ?? 50)) });
    }
    case 'team.note.record': {
      const title = String(params.title ?? '');
      if (!title) return fail(-32602, 'invalid_params');
      const note = {
        id: String(params.id ?? `note-${mockTeamNotes.length + 1}`),
        ...(params.taskId ? { taskId: String(params.taskId) } : {}),
        title,
        ...(params.body ? { body: String(params.body) } : {}),
        ...(params.author ? { author: String(params.author) } : {}),
        at: nowIso(),
      };
      mockTeamNotes.unshift(note);
      return reply(note);
    }
    case 'heartbeat.configure': {
      // The contract declares enabled; the mock must read it, not just intervals.
      if (params.enabled !== undefined) mockHeartbeat.enabled = Boolean(params.enabled);
      if (params.leadIntervalMs !== undefined) mockHeartbeat.leadIntervalMs = Number(params.leadIntervalMs);
      if (params.channelIntervalMs !== undefined) mockHeartbeat.channelIntervalMs = Number(params.channelIntervalMs);
      if (params.leadIntervalMs !== undefined && params.leadIntervalMs < 5000) {
        return fail(-32602, 'invalid_params');
      }
      return reply({
        running: mockHeartbeat.enabled, leadIntervalMs: Number(params.leadIntervalMs ?? mockHeartbeat.leadIntervalMs),
        channelIntervalMs: Number(params.channelIntervalMs ?? mockHeartbeat.channelIntervalMs),
        lastTick: nowIso(), leadRuns: 0, dispatched: 0, polls: 0, newMessages: 0,
      });
    }
    case 'heartbeat.status': {
      return reply({
        running: mockHeartbeat.enabled, leadIntervalMs: 30000, channelIntervalMs: 60000,
        lastTick: nowIso(), leadRuns: 1, dispatched: 0, polls: 0, newMessages: 0,
      });
    }
    case 'heartbeat.tick': {
      return reply({ leadRuns: 1, dispatched: 0, polls: 0, newMessages: 0 });
    }
    case 'browser.status': {
      return reply({ connected: false, pages: 0 });
    }
    case 'credentials.list': {
      const kind = String(params.kind ?? '');
      const rows = [...mockCredentials.entries()]
        .filter(([, row]) => !kind || row.kind === kind)
        .map(([, row]) => ({ kind: row.kind, id: row.id }));
      return reply({ credentials: rows });
    }
    case 'credentials.set': {
      const kind = String(params.kind ?? '');
      const id = String(params.id ?? '');
      if (!kind || !id) return fail(-32602, 'invalid_params');
      mockCredentials.set(`${kind}/${id}`, { kind, id, fields: { ...(params.fields ?? {}) } });
      return reply({ ok: true });
    }
    case 'credentials.delete': {
      const kind = String(params.kind ?? '');
      const id = String(params.id ?? '');
      if (!mockCredentials.delete(`${kind}/${id}`)) return fail(-32600, 'credentials: not found');
      return reply({ ok: true });
    }
    case 'webhook.sources':
      // The secret never leaves the gateway: no echo here either.
      return reply({
        sources: mockWebhookSources.map((row) => ({
          id: row.id,
          ...(row.channel ? { channel: row.channel } : {}),
        })),
      });
    case 'webhook.source.set': {
      const id = String(params.id ?? '');
      const secret = String(params.secret ?? '');
      if (!/^[a-z0-9-]+$/.test(id)) return fail(-32602, 'invalid_params');
      if (secret.length < 16) return fail(-32602, 'invalid_params');
      const row = { id, ...(params.channel ? { channel: String(params.channel) } : {}), secret };
      const at = mockWebhookSources.findIndex((item) => item.id === id);
      if (at >= 0) mockWebhookSources[at] = row;
      else mockWebhookSources.push(row);
      return reply({ id: row.id, ...(row.channel ? { channel: row.channel } : {}) });
    }
    case 'webhook.source.remove': {
      const at = mockWebhookSources.findIndex((item) => item.id === String(params.id ?? ''));
      if (at >= 0) mockWebhookSources.splice(at, 1);
      return reply({ ok: true });
    }
    case 'lead.run':
      // One bounded pass. The fixture has nothing to scan on its own, so the
      // honest answer is a run that touched nothing rather than invented counts.
      return reply({ scanned: 0, dispatched: 0, ignored: 0, skipped: 0, failed: 0 });
    case 'prompts.enqueue': {
      const sessionId = String(params.sessionId ?? '');
      const text = String(params.prompt ?? params.text ?? '').trim();
      if (!sessions.has(sessionId)) return fail(1001, 'session_not_found');
      if (text === '') return fail(-32602, 'invalid_params');
      const prompt = { id: shortId('q'), sessionId, text };
      queueOf(sessionId).push(prompt);
      publishPrompts(sessionId);
      return reply({ id: prompt.id });
    }
    case 'prompts.move': {
      const sessionId = String(params.sessionId ?? '');
      const queue = queueOf(sessionId);
      const from = queue.findIndex((item) => item.id === params.id);
      if (from >= 0) {
        const [moved] = queue.splice(from, 1);
        const target = Math.max(0, Math.min(Number(params.index ?? 0), queue.length));
        queue.splice(target, 0, moved);
      }
      publishPrompts(sessionId);
      return reply({});
    }
    case 'prompts.remove': {
      const sessionId = String(params.sessionId ?? '');
      const queue = queueOf(sessionId);
      const index = queue.findIndex((item) => item.id === params.id);
      if (index >= 0) queue.splice(index, 1);
      publishPrompts(sessionId);
      return reply({});
    }
    case 'foreground.set':
      return reply({});
    case 'session.active.get': {
      // The client tells the gateway which session it is looking at; the answer
      // must follow that, not a constant or "the first session in the map".
      return reply({ sessionId: activeSessionId ?? '' });
    }
    case 'approvals.subscribe':
    case 'ask.subscribe':
    case 'plan.subscribe':
    case 'settings.subscribe':
    case 'artifacts.subscribe':
      // Global domains are broadcast to every ready client; the subscribe call
      // exists for replay after a reconnect, which the mock does not buffer.
      return reply({
        epoch: WS_EPOCH,
        rev: params.sinceRev === undefined ? 0 : Number(params.sinceRev),
        resync: false,
      });
    case 'notify.action': {
      const toastId = String(params.id ?? '');
      const actionId = String(params.actionId ?? '');
      const entry = toastActions.get(toastId);
      if (!entry || entry.actionId !== actionId) return fail(1021, 'action_unavailable');
      toastActions.delete(toastId);
      entry.run();
      return reply({});
    }
    case 'ask.snapshot':
      return reply({ epoch: WS_EPOCH, rev: wsRevs.ask, pending: askState });
    case 'ask.answer': {
      if (!askState || String(params.id) !== askState.id) return fail(1023, 'ask_not_pending');
      askState = { ...askState, answer: String(params.answer ?? '') };
      wsRevs.ask += 1;
      wsNotify('ask.delta', { rev: wsRevs.ask, pending: askState });
      return reply({});
    }
    case 'ask.cancel': {
      if (askState && String(params.id) !== askState.id) return fail(1023, 'ask_not_pending');
      askState = null;
      wsRevs.ask += 1;
      wsNotify('ask.delta', { rev: wsRevs.ask, pending: null });
      return reply({});
    }
    case 'plan.exit': {
      planState = { state: String(params.action ?? 'idle'), content: planState.content };
      wsRevs.plan += 1;
      wsNotify('plan.delta', { rev: wsRevs.plan, state: planState.state });
      return reply({});
    }
    case 'plan.snapshot':
      return reply({
        epoch: WS_EPOCH,
        rev: wsRevs.plan,
        state: planState.state,
        content: planState.content,
      });
    case 'artifacts.snapshot':
      return reply({ epoch: WS_EPOCH, rev: wsRevs.artifacts, items: [] });
    case 'run.cancel': {
      const value = sessions.get(params.sessionId);
      if (value) value.running = false;
      publishList();
      return reply({});
    }
    default:
      return fail(-32601, 'method_not_found');
  }
};

server.on('upgrade', (req, socket) => {
  if (!req.url?.startsWith('/ws')) {
    socket.destroy();
    return;
  }
  if (!authorized(req)) {
    // §3.1/C4: the upgrade is refused with a real HTTP response, not a socket
    // that silently closes — the browser surfaces it as a failed handshake.
    socket.write(
      'HTTP/1.1 401 Unauthorized\r\n' +
        'Content-Type: application/json\r\n' +
        'Retry-After: 1\r\n' +
        'Connection: close\r\n' +
        'Content-Length: 74\r\n\r\n' +
        '{"error":{"code":1020,"message":"unauthorized","data":{"name":"unauthorized"}}}',
    );
    socket.destroy();
    return;
  }
  const key = req.headers['sec-websocket-key'];
  if (!key) {
    socket.destroy();
    return;
  }
  const accept = createHash('sha1')
    .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
    .digest('base64');
  socket.write(
    'HTTP/1.1 101 Switching Protocols\r\n' +
      'Upgrade: websocket\r\n' +
      'Connection: Upgrade\r\n' +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
  );
  const client = { socket, ready: false, buffer: Buffer.alloc(0) };
  wsClients.add(client);

  socket.on('data', (chunk) => {
    client.buffer = Buffer.concat([client.buffer, chunk]);
    for (;;) {
      const frame = readFrame(client.buffer);
      if (!frame) break;
      client.buffer = client.buffer.slice(frame.bytes);
      if (frame.opcode === 0x8) {
        socket.end();
        wsClients.delete(client);
        return;
      }
      if (frame.opcode === 0x9) {
        socket.write(Buffer.from([0x8a, 0x00]));
        continue;
      }
      if (frame.opcode !== 0x1) continue;
      const text = frame.payload.toString('utf8');
      for (const line of text.split('\n')) {
        if (line.trim() === '') continue;
        try {
          wsDispatch(client, JSON.parse(line));
        } catch (error) {
          console.error('[mock] ws dispatch failed', error);
        }
      }
    }
  });

  socket.on('close', () => wsClients.delete(client));
  socket.on('error', () => wsClients.delete(client));
});

/** Read one masked client frame; null until the whole frame is buffered. */
function readFrame(buffer) {
  if (buffer.length < 2) return null;
  const opcode = buffer[0] & 0x0f;
  const masked = (buffer[1] & 0x80) !== 0;
  let length = buffer[1] & 0x7f;
  let offset = 2;
  if (length === 126) {
    if (buffer.length < 4) return null;
    length = buffer.readUInt16BE(2);
    offset = 4;
  } else if (length === 127) {
    if (buffer.length < 10) return null;
    length = Number(buffer.readBigUInt64BE(2));
    offset = 10;
  }
  const maskSize = masked ? 4 : 0;
  if (buffer.length < offset + maskSize + length) return null;
  const mask = masked ? buffer.subarray(offset, offset + 4) : null;
  const payload = Buffer.from(buffer.subarray(offset + maskSize, offset + maskSize + length));
  if (mask) {
    for (let i = 0; i < payload.length; i += 1) payload[i] ^= mask[i % 4];
  }
  return { opcode, payload, bytes: offset + maskSize + length };
}

server.listen(PORT, HOST, () => {
  console.log(
    `[mock] listening on http://${HOST}:${PORT}  (${sessions.size} sessions, ${approvals.size} approval)`,
  );
  // The gateway prints the tokenised URL; the fragment is the only way in.
  console.log(`[mock] open: http://${HOST}:${PORT}/#token=${AUTH_TOKEN}`);
  console.log(`[mock] dev client (vite): http://127.0.0.1:5178/#token=${AUTH_TOKEN}`);
});