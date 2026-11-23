/**
 * A fake gateway for tests: the same JSON-RPC surface the real gateway serves
 * (docs/TRANSPORT.md §4.1/§6.3), in memory, behind a fake socket.
 *
 * The point is that tests exercise the *real* client code — `GatewayRpc`, the
 * domain stores, the reducers — instead of a stubbed `fetch`. Only the socket
 * is fake; every frame is newline-framed JSON-RPC like the wire.
 */
import { GatewayRpc, setGatewayRpc, type SocketLike } from '@/api/ws/rpc';
// The mock and this fake share one params table, so a client that sends an
// unknown field fails here too instead of only against the real gateway.
import { validateParams } from '../../dev/ws-params.mjs';
import { SETTING_KEYS } from '@/design/settings-keys';
import { clearConnectionCaches } from '@/api/gateway';
import { CONDITIONAL_CAPABILITIES, STATIC_CAPABILITIES } from '@/design/ws-contract';

export interface FakeSessionInfo {
  id: string;
  title: string;
  updatedAt?: string;
  running?: boolean;
}

export interface FakeMessage {
  id: string;
  role: string;
  text: string;
  reasoning?: string;
  usage?: { promptTokens?: number; completionTokens?: number };
  tools?: Array<{
    id: string;
    name: string;
    status?: string;
    summary?: string;
    arguments?: string;
  }>;
}

export interface FakeApproval {
  id: string;
  tool: string;
  summary?: string;
  arguments?: string;
  sessionId?: string;
  createdAt?: string;
  expiresAt?: string;
}

export interface FakeQueuedPrompt {
  id: string;
  text: string;
  createdAt?: string;
}

/**
 * Conditional capabilities this fixture provides. Named **by the contract's own
 * `conditional[].name`**, not by a written-out list, so a new conditional half
 * is a deliberate choice here rather than a silent gap.
 */
export const FAKED_CONDITIONAL: string[] = ['context', 'tasks', 'regenerate'].filter((name) =>
  CONDITIONAL_CAPABILITIES.some((item) => item.name === name),
);

export interface FakeState {
  sessions: FakeSessionInfo[];
  messages: Record<string, FakeMessage[]>;
  approvals: FakeApproval[];
  approvalMode: string;
  settings: Record<string, string>;
  ask: { id: string; text: string; options?: string[]; answer?: string } | null;
  /** The session the client is looking at; `session.active.get` reports it. */
  activeSessionId?: string | null;
  plan: { state: string; content: string };
  artifacts: Array<{ id: string; title: string; body: string }>;
  workspaces: Array<{ id: string; name: string; root: string }>;
  /** sessionId → queued prompts, oldest first. */
  prompts: Record<string, FakeQueuedPrompt[]>;
  /**
   * Task boards: `''` is the process-wide board, a session id is that session's
   * board (§5.1.8). `added` deltas upsert by id.
   */
  tasks: Record<string, Array<Record<string, unknown>>>;
  taskRevs: Record<string, number>;
  /** sessionId → folded context view, or `null` for `found:false`. */
  contexts: Record<string, unknown | null>;
  currentWorkspaceId?: string;
  capabilities: string[];
}

export function defaultState(): FakeState {
  return {
    sessions: [{ id: 's-1', title: '冒烟会话', updatedAt: '2026-11-23T00:00:00Z' }],
    messages: { 's-1': [] },
    approvals: [],
    approvalMode: 'workspace',
    // The language row decides the UI locale; tests that assert Chinese copy
    // pin it here instead of depending on the machine's navigator.language.
    settings: {
      [SETTING_KEYS.themeMode]: 'system',
      [SETTING_KEYS.agentPermission]: 'workspace',
      [SETTING_KEYS.uiLanguage]: 'zh',
    },
    ask: null,
    plan: { state: 'idle', content: '' },
    artifacts: [],
    workspaces: [{ id: 'ws-1', name: 'mygo', root: '/data/mygo' }],
    prompts: {},
    tasks: { '': [] },
    taskRevs: { '': 1 },
    contexts: {},
    currentWorkspaceId: 'ws-1',
    // Every static capability the contract declares, plus the conditional ones
    // this fixture actually fakes. Hand-writing this list is how `approvals`
    // was once served without being declared.
    capabilities: [...STATIC_CAPABILITIES, ...FAKED_CONDITIONAL],
  };
}

/** One fake WebSocket: `send` is the client → gateway direction. */
class FakeSocket implements SocketLike {
  readyState = 0;
  private listeners = new Map<string, Set<(event: unknown) => void>>();

  constructor(private readonly gateway: FakeGateway) {
    queueMicrotask(() => {
      this.readyState = 1;
      this.emit('open', {});
      this.gateway.opened(this);
    });
  }

  send(data: string): void {
    for (const line of data.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      this.gateway.receive(this, JSON.parse(trimmed) as Record<string, unknown>);
    }
  }

  close(): void {
    if (this.readyState === 3) return;
    this.readyState = 3;
    this.emit('close', {});
  }

  addEventListener(type: string, listener: (event: unknown) => void): void {
    const set = this.listeners.get(type) ?? new Set();
    set.add(listener);
    this.listeners.set(type, set);
  }

  removeEventListener(type: string, listener: (event: unknown) => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  /** gateway → client */
  push(message: unknown): void {
    this.emit('message', { data: `${JSON.stringify(message)}\n` });
  }

  private emit(type: string, event: unknown): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

export class FakeGateway {
  /** Toast id → the action it offers (the gateway keys by toast, not action). */
  private readonly toastActions = new Map<string, { actionId: string; run: () => void }>();
  private toastSequence = 0;
  /** Toasts pushed at the client, newest last — assertable like approvals. */
  readonly toasts: Array<{ id: string; actionLabel?: string; actionId?: string }> = [];

  /** Push a toast at connected clients and keep it for assertions. */
  private emitToast(toast: { id: string; actionLabel?: string; actionId?: string }): void {
    this.toasts.push(toast);
    for (const socket of this.sockets) {
      socket.push({ jsonrpc: '2.0', method: 'toast.posted', params: toast });
    }
  }

  /** Every decision the client sent, in order — `{id, tool, decision}`. */
  readonly decisions: Array<{ id: string; tool: string; decision: string }> = [];
  /** Tools whose approval was denied: they must never execute. */
  readonly deniedTools: string[] = [];
  /** Tools whose approval was granted. */
  readonly allowedTools: string[] = [];

  readonly state: FakeState;
  readonly calls: Array<{ method: string; params: Record<string, unknown> }> = [];
  readonly sockets: FakeSocket[] = [];
  private revs = {
    list: 0,
    approvals: 0,
    settings: 0,
    workspace: 0,
    ask: 0,
    plan: 0,
    artifacts: 0,
    prompts: {} as Record<string, number>,
  };
  private epoch = 'epoch-test';
  /** Simulate a gateway restart: next connect answers with a new epoch. */
  nextEpoch: string | null = null;

  constructor(state: FakeState = defaultState()) {
    this.state = state;
  }

  createSocket = (_url: string): SocketLike => {
    const socket = new FakeSocket(this);
    this.sockets.push(socket);
    return socket;
  };

  opened(_socket: FakeSocket): void {
    // nothing: the handshake starts with `initialize`
  }

  /** Client → gateway dispatch. */
  /** A socket for tests that drive the dispatch directly (no DOM involved). */
  openTestSocket(): FakeSocket {
    const socket = { push: () => undefined, closed: false };
    this.sockets.push(socket as never);
    return socket as unknown as FakeSocket;
  }

  receive(socket: FakeSocket, message: Record<string, unknown>): void {
    const method = String(message.method ?? '');
    const params = (message.params ?? {}) as Record<string, unknown>;
    const id = message.id as number | undefined;
    this.calls.push({ method, params });

    const paramsCheck = validateParams(method, params);
    if (!paramsCheck.ok) {
      if (id !== undefined) {
        socket.push({
          jsonrpc: '2.0',
          id,
          error: {
            code: -32602,
            message: `invalid params: ${paramsCheck.field} ${paramsCheck.reason}`,
            data: { name: 'invalid_params', field: paramsCheck.field },
          },
        });
      }
      return;
    }

    const reply = (result: unknown) => {
      if (id !== undefined) socket.push({ jsonrpc: '2.0', id, result });
    };
    const fail = (code: number, name: string) => {
      if (id !== undefined) {
        socket.push({ jsonrpc: '2.0', id, error: { code, message: name, data: { name } } });
      }
    };

    if (method === 'initialize') {
      if (this.nextEpoch) {
        this.epoch = this.nextEpoch;
        this.nextEpoch = null;
      }
      reply({
        epoch: this.epoch,
        protocolVersion: 1,
        serverVersion: 'test',
        capabilities: this.state.capabilities,
        limits: {
          maxFrameBytes: 16 * 1024 * 1024,
          maxAttachmentBytes: 10 * 1024 * 1024,
          snapshotTailDefault: 60,
          deltaRingBytes: 256 * 1024,
        },
      });
      return;
    }
    if (method === 'ready') {
      reply({});
      return;
    }
    if (method === 'ping') {
      reply({ pong: true, epoch: this.epoch });
      return;
    }
    if (method === 'session.list.snapshot') {
      reply({ epoch: this.epoch, rev: this.revs.list, sessions: this.state.sessions });
      return;
    }
    if (method === 'session.snapshot') {
      const sessionId = String(params.sessionId ?? '');
      const info = this.state.sessions.find((item) => item.id === sessionId);
      if (!info) {
        fail(1001, 'session_not_found');
        return;
      }
      reply({
        epoch: this.epoch,
        rev: this.revs.list,
        session: {
          id: info.id,
          title: info.title,
          running: Boolean(info.running),
          interrupted: false,
          messages: this.state.messages[sessionId] ?? [],
        },
        truncated: false,
      });
      return;
    }
    if (method === 'session.subscribe') {
      reply({ epoch: this.epoch, rev: this.revs.list, resync: false });
      return;
    }
    if (method === 'session.unsubscribe') {
      reply({});
      return;
    }
    if (method === 'session.create') {
      const created: FakeSessionInfo = {
        id: `s-${this.state.sessions.length + 1}`,
        title: String(params.title || '新会话'),
        updatedAt: new Date().toISOString(),
      };
      this.state.sessions.push(created);
      this.state.messages[created.id] = [];
      this.broadcastList([created], []);
      reply({ sessionId: created.id });
      return;
    }
    if (method === 'approvals.snapshot') {
      reply({
        epoch: this.epoch,
        rev: this.revs.approvals,
        mode: this.state.approvalMode,
        permission: this.state.approvalMode,
        approvalTimeoutSeconds: 900,
        pending: this.state.approvals,
      });
      return;
    }
    if (method === 'approval.resolve') {
      const approvalId = String(params.id ?? '');
      const decision = String(params.decision ?? '');
      // The contract's vocabulary (§5.1.3). A fixture that ignores it makes allow
      // and deny the same code path, which silently removes half the semantics
      // from every approval test: "the card went away" cannot tell you whether
      // the tool ran.
      if (decision !== 'allow' && decision !== 'allow_always' && decision !== 'deny') {
        fail(-32602, 'invalid_params');
        return;
      }
      const index = this.state.approvals.findIndex((item) => item.id === approvalId);
      if (index < 0) {
        fail(1019, 'approval_not_pending');
        return;
      }
      const [resolved] = this.state.approvals.splice(index, 1);
      // Recorded so a test can assert *which* decision was applied, not merely
      // that the list shrank.
      this.decisions.push({ id: approvalId, tool: resolved?.tool ?? '', decision });
      if (decision === 'deny') {
        // A denial is a result for the tool, not silence: the call must not run.
        this.deniedTools.push(resolved?.tool ?? '');
      } else {
        this.allowedTools.push(resolved?.tool ?? '');
      }
      this.broadcastApprovals([], [approvalId]);
      reply({ resolved: true });
      return;
    }
    // Global domains: the subscribe call is a replay request; the deltas that
    // follow arrive through `notify()` on every connected socket (task-38 A).
    if (
      method === 'approvals.subscribe' ||
      method === 'ask.subscribe' ||
      method === 'plan.subscribe' ||
      method === 'settings.subscribe'
    ) {
      const revKey = method.split('.')[0] as 'approvals' | 'ask' | 'plan' | 'settings';
      reply({ epoch: this.epoch, rev: this.revs[revKey], resync: false });
      return;
    }
    // B group: these three are state changes, so the assertion can be on state
    // rather than "the command returned {}".
    if (method === 'ask.answer') {
      if (!this.state.ask) {
        fail(1023, 'ask_not_pending');
        return;
      }
      this.state.ask = { ...this.state.ask, answer: String(params.answer ?? '') };
      this.revs.ask += 1;
      this.notify('ask.delta', { rev: this.revs.ask, pending: this.state.ask });
      reply({});
      return;
    }
    if (method === 'ask.cancel') {
      this.state.ask = null;
      this.revs.ask += 1;
      this.notify('ask.delta', { rev: this.revs.ask, pending: null });
      reply({});
      return;
    }
    if (method === 'plan.exit') {
      this.state.plan = { ...this.state.plan, state: String(params.action ?? 'idle') };
      this.revs.plan += 1;
      this.notify('plan.delta', { rev: this.revs.plan, state: this.state.plan.state });
      reply({});
      return;
    }
    if (method === 'settings.snapshot') {
      reply({ epoch: this.epoch, rev: this.revs.settings, rows: this.state.settings });
      return;
    }
    if (method === 'settings.set') {
      const key = String(params.key ?? '');
      if (!key) {
        fail(-32602, 'invalid_params');
        return;
      }
      this.state.settings[key] = String(params.value ?? '');
      const rev = ++this.revs.settings;
      this.notify('settings.delta', {
        baseRev: rev - 1,
        rev,
        rows: { [key]: this.state.settings[key] },
        removed: [],
      });
      reply({});
      return;
    }
    if (method === 'workspace.list.snapshot') {
      reply({
        epoch: this.epoch,
        rev: this.revs.workspace,
        workspaces: this.state.workspaces,
        currentId: this.state.currentWorkspaceId,
      });
      return;
    }
    if (method === 'prompts.snapshot') {
      const sessionId = String(params.sessionId ?? '');
      if (!this.state.sessions.some((item) => item.id === sessionId)) {
        fail(1001, 'session_not_found');
        return;
      }
      reply({
        epoch: this.epoch,
        rev: this.promptsRev(sessionId),
        prompts: this.state.prompts[sessionId] ?? [],
      });
      return;
    }
    if (method === 'prompts.subscribe') {
      const sessionId = String(params.sessionId ?? '');
      reply({
        epoch: this.epoch,
        rev: this.promptsRev(sessionId),
        resync: Number(params.sinceRev ?? 0) !== this.promptsRev(sessionId),
      });
      return;
    }
    if (method === 'prompts.enqueue') {
      const sessionId = String(params.sessionId ?? '');
      const text = String(params.prompt ?? params.text ?? '');
      if (!this.state.sessions.some((item) => item.id === sessionId)) {
        fail(1001, 'session_not_found');
        return;
      }
      if (text.trim() === '') {
        fail(-32602, 'invalid_params');
        return;
      }
      const queue = this.state.prompts[sessionId] ?? [];
      const prompt = { id: `q-${queue.length + 1}-${Date.now()}`, text };
      queue.push(prompt);
      this.state.prompts[sessionId] = queue;
      this.broadcastPrompts(sessionId, [prompt], []);
      reply({ id: prompt.id });
      return;
    }
    if (method === 'prompts.remove') {
      const sessionId = String(params.sessionId ?? '');
      const queue = this.state.prompts[sessionId] ?? [];
      const index = queue.findIndex((item) => item.id === params.id);
      if (index >= 0) queue.splice(index, 1);
      this.broadcastPrompts(sessionId, [], [String(params.id ?? '')]);
      reply({});
      return;
    }
    if (method === 'context.snapshot') {
      const sessionId = String(params.sessionId ?? '');
      const context = this.state.contexts[sessionId] ?? null;
      reply({
        epoch: 'fake',
        rev: 1,
        sessionId,
        found: context !== null,
        ...(context ? { context } : {}),
      });
      return;
    }
    if (method === 'context.subscribe') {
      const since = Number(params.sinceRev ?? 0);
      reply({ epoch: 'fake', rev: 1, resync: since !== 1 });
      return;
    }
    if (method === 'tasks.snapshot') {
      const key = this.taskKey(params);
      reply({ epoch: 'fake', rev: this.state.taskRevs[key] ?? 1, tasks: this.state.tasks[key] ?? [] });
      return;
    }
    if (method === 'tasks.subscribe') {
      const key = this.taskKey(params);
      const since = Number(params.sinceRev ?? 0);
      reply({
        epoch: 'fake',
        rev: this.state.taskRevs[key] ?? 1,
        resync: since !== (this.state.taskRevs[key] ?? 1),
      });
      return;
    }
    if (method === 'task.status') {
      const key = this.taskKey(params);
      const board = this.state.tasks[key] ?? [];
      const row = board.find((task) => task.id === params.id);
      if (!row) {
        fail(1001, 'task_not_found');
        return;
      }
      row.status = String(params.status ?? 'pending');
      this.broadcastTasks(key, [row], []);
      reply({});
      return;
    }
    if (method === 'task.delete') {
      const key = this.taskKey(params);
      const board = this.state.tasks[key] ?? [];
      const index = board.findIndex((task) => task.id === params.id);
      const removed = index >= 0 ? board[index] : null;
      if (removed) board.splice(index, 1);
      this.broadcastTasks(key, [], [String(params.id ?? '')]);
      // The gateway offers an undo for every delete: a toast whose action runs
      // the closure that puts **this** row back. Registered under the toast id,
      // not under `actionId` — every undo is called `"restore"`, so an actionId
      // key would make two deletes overwrite each other and undo the wrong task.
      if (removed) {
        const toastId = `toast-${this.toastSequence += 1}`;
        this.toastActions.set(toastId, {
          actionId: 'restore',
          run: () => {
            const current = this.state.tasks[key] ?? [];
            if (!current.some((task) => task.id === removed.id)) current.push(removed);
            this.broadcastTasks(key, [removed], []);
          },
        });
        this.emitToast({ id: toastId, actionLabel: '撤销', actionId: 'restore' });
      }
      reply({});
      return;
    }
    if (method === 'notify.action') {
      const toastId = String(params.id ?? '');
      const actionId = String(params.actionId ?? '');
      const entry = this.toastActions.get(toastId);
      // Same vocabulary as the gateway: an action that is gone answers
      // `action_unavailable` rather than pretending to succeed.
      if (!entry || entry.actionId !== actionId) {
        fail(1021, 'action_unavailable');
        return;
      }
      this.toastActions.delete(toastId);
      entry.run();
      reply({});
      return;
    }
    if (method === 'session.active.get') {
      // C group: the active session is real state, not a constant — switching the
      // selection must change this answer.
      reply({ sessionId: this.state.activeSessionId ?? null });
      return;
    }
    if (method === 'foreground.set') {
      reply({});
      return;
    }
    if (method === 'ask.snapshot') {
      reply({ epoch: this.epoch, rev: this.revs.ask, pending: this.state.ask });
      return;
    }
    if (method === 'plan.snapshot') {
      reply({ epoch: this.epoch, rev: this.revs.plan, ...this.state.plan });
      return;
    }
    if (method === 'artifacts.snapshot') {
      reply({ epoch: this.epoch, rev: this.revs.artifacts, items: this.state.artifacts });
      return;
    }
    if (method === 'run.cancel') {
      const session = this.state.sessions.find((item) => item.id === params.sessionId);
      if (session) session.running = false;
      reply({});
      return;
    }
    fail(-32601, 'method_not_found');
  }

  // ------------------------------------------------------------- notifying --

  broadcastList(
    added: FakeSessionInfo[] = [],
    removed: string[] = [],
    changed: FakeSessionInfo[] = [],
  ): void {
    const rev = ++this.revs.list;
    this.notify('session.list.delta', { baseRev: rev - 1, rev, added, changed, removed });
  }

  private promptsRev(sessionId: string): number {
    return this.revs.prompts[sessionId] ?? 0;
  }

  broadcastPrompts(
    sessionId: string,
    added: FakeQueuedPrompt[] = [],
    removed: string[] = [],
  ): void {
    const rev = this.promptsRev(sessionId) + 1;
    this.revs.prompts[sessionId] = rev;
    this.notify('prompts.delta', { sessionId, baseRev: rev - 1, rev, added, removed });
  }

  /** `''` = the process-wide board (§5.1.8). */
  private taskKey(params: Record<string, unknown>): string {
    return params.sessionId === undefined ? '' : String(params.sessionId);
  }

  broadcastTasks(
    key: string,
    added: Array<Record<string, unknown>> = [],
    removed: string[] = [],
  ): void {
    const rev = (this.state.taskRevs[key] ?? 1) + 1;
    this.state.taskRevs[key] = rev;
    // The delta carries no sessionId; the client routes it to the board it holds.
    this.notify('tasks.delta', { baseRev: rev - 1, rev, added, removed });
  }

  broadcastApprovals(added: FakeApproval[] = [], removed: string[] = []): void {
    const rev = ++this.revs.approvals;
    this.notify('approvals.delta', { baseRev: rev - 1, rev, added, removed });
  }

  notify(method: string, params: unknown): void {
    for (const socket of this.sockets) socket.push({ jsonrpc: '2.0', method, params });
  }

  /** Push a frame that contradicts the client's revision (resync path). */
  notifyStale(method: string, params: Record<string, unknown> = {}): void {
    this.notify(method, { baseRev: 999, rev: 1000, ...params });
  }

  methodsCalled(): string[] {
    return this.calls.map((call) => call.method);
  }

  /** Drop every connection (simulates the gateway going away). */
  dropConnections(): void {
    for (const socket of this.sockets) socket.close();
    this.sockets.length = 0;
  }
}

export interface FakeGatewayHandle {
  gateway: FakeGateway;
  dispose: () => void;
}

/** Install a fake gateway as the app's control plane. */
export function installFakeGateway(state: FakeState = defaultState()): FakeGatewayHandle {
  // A new fake gateway is a new gateway: cached boards from the previous one
  // would otherwise describe state that never existed here.
  clearConnectionCaches();
  const gateway = new FakeGateway(state);
  setGatewayRpc(
    new GatewayRpc({
      url: () => 'ws://test/ws',
      createSocket: gateway.createSocket,
      setTimeout: (handler) => {
        // Reconnect promptly in tests instead of waiting out the backoff.
        const timer = setTimeout(handler, 0);
        return timer;
      },
    }),
  );
  return {
    gateway,
    dispose: () => {
      clearConnectionCaches();
      setGatewayRpc(null);
    },
  };
}