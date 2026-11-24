/**
 * The gateway client (docs/TRANSPORT.md): one WS control plane plus the SSE run
 * stream.
 *
 * This module is the only place in `web/src` that speaks to the gateway. It
 * owns the transport (`ws/rpc.ts`), the per-domain snapshot+delta stores
 * (`ws/domains.ts`) and the small set of commands the UI issues.
 *
 * Merge rule for the transcript (§4.6): SSE is the live fast lane, the WS
 * `session:<id>` domain is authoritative. Deltas are not folded into the chat
 * store while a run is streaming (that would double the text); the store is
 * rebuilt from the fresh snapshot when the run settles, which is also how tool
 * outputs the SSE stream did not carry become visible.
 */
import { GatewayRpc, type ConnectionStatus, gatewayRpc } from './ws/rpc';
import { exchangeFragmentToken, probeAuthorization } from './ws/auth';
import { ApiError, isMethodUnsupported } from './errors';
import { applyPromptsDelta, type QueuedPrompt, type QueuedPromptWire } from './prompt-queue';
import {
  applyContextDelta,
  EMPTY_CONTEXT,
  normalizeContext,
  type ContextSnapshotResult,
  type ContextState,
  type ContextView,
} from './context';
import {
  applyTasksDelta,
  normalizeTask,
  type GatewayTask,
  type TaskStatus,
  type TasksSnapshot,
  type TaskWire,
} from './tasks';
import {
  SyncedDomain,
  applyItemSet,
  applyListDelta,
  applySettingsDelta,
  type DomainDelta,
} from './ws/domains';

// ------------------------------------------------------------------- types --

export interface SessionSummary {
  id: string;
  title: string;
  updatedAt?: string;
  running?: boolean;
  waiting?: boolean;
  unread?: boolean;
  archived?: boolean;
  pinned?: boolean;
  messageCount?: number;
  workspaceId?: string;
  effectiveApprovalMode?: string;
}

export interface SnapshotStep {
  callId: string;
  name: string;
  status?: string;
  input?: unknown;
  output?: unknown;
  error?: string;
}

export interface SnapshotMessage {
  id: string;
  role: string;
  text: string;
  reasoning?: string;
  steps?: SnapshotStep[];
  usage?: { inputTokens?: number; outputTokens?: number };
  stopReason?: string;
  remoteError?: string;
  streaming?: boolean;
}

export interface SessionSnapshot {
  id: string;
  title: string;
  messages: SnapshotMessage[];
  running: boolean;
  interrupted: boolean;
  rev: number;
  epoch: string;
  truncated: boolean;
  oldestMessageId?: string;
}

export interface ApprovalRequest {
  id: string;
  tool: string;
  summary: string;
  /** Raw arguments are a JSON string on this wire (§5.1.3). */
  arguments?: string;
  sessionId?: string;
  source?: string;
  createdAt?: string;
  expiresAt?: string;
}

export interface ApprovalSnapshot {
  mode: string;
  permission: string;
  approvalTimeoutSeconds: number;
  pending: ApprovalRequest[];
}

export interface AskPending {
  id: string;
  text: string;
  options?: string[];
  createdAt?: string;
}

export interface PlanState {
  state: string;
  content: string;
}

export interface ArtifactItem {
  id: string;
  title: string;
  body: string;
  createdAt?: string;
}

export interface WorkspaceInfo {
  id: string;
  name: string;
  root: string;
  color?: string;
}

export interface WorkspaceSnapshot {
  workspaces: WorkspaceInfo[];
  currentId?: string;
}

export type SettingsRows = Record<string, string>;

// --------------------------------------------------------------- transport --

let routedFor: GatewayRpc | null = null;
let capabilities = new Set<string>();
const readyListeners = new Set<() => void>();

/**
 * The singleton lives in `ws/rpc.ts`, so replacing it (tests) takes effect.
 *
 * The auth gate is installed **here**, on first access, and not in an effect:
 * child effects (the providers) run before the App's effect, so a gate installed
 * by `startGateway()` would arrive after the first `start()` had already opened
 * a socket — a guaranteed 401 on the real gateway. That is exactly the bug this
 * file shipped once; the ordering now lives at the transport's front door.
 */
let gateState: GatewayAuthState | null = null;
let gatePromise: Promise<boolean> | null = null;
let gatedFor: GatewayRpc | null = null;

function authGate(): Promise<boolean> {
  if (!gatePromise) {
    gatePromise = (async () => {
      try {
        const outcome = await exchangeFragmentToken();
        if (outcome === 'invalid') {
          gateState = 'stale-link';
          return false;
        }
        gateState = 'ok';
        return true;
      } catch {
        // A network fault is not a rejected cookie: let the transport retry.
        gateState = 'unreachable';
        return true;
      }
    })();
  }
  return gatePromise;
}

function client(): GatewayRpc {
  const connection = gatewayRpc();
  if (gatedFor !== connection) {
    gatedFor = connection;
    connection.setAuthGate(authGate);
  }
  return connection;
}

/**
 * Resolves once the fragment→cookie exchange finished (immediately when there
 * was no fragment). REST bridges — the prompt queue and the mock-only surfaces —
 * must await this before their first request, or they race the exchange and
 * collect 401s that look like a broken feature.
 */
export function authorizationReady(): Promise<boolean> {
  client();
  return gatePromise ?? Promise.resolve(true);
}

/** True when the gateway advertised a capability (§4.2: never assume). */
export function hasCapability(name: string): boolean {
  return capabilities.has(name);
}

export function connectionStatus(): ConnectionStatus {
  return client().getStatus();
}

export function onConnectionStatus(handler: (status: ConnectionStatus) => void): () => void {
  return client().onStatus(handler);
}

/** Why the control plane is unavailable, in the user's words (§3.1). */
export type GatewayAuthState = 'ok' | 'stale-link' | 'unreachable';

/**
 * Start the control plane and load every global domain. Idempotent.
 *
 * Order matters (§3.1/§4.2): the fragment token is exchanged for the cookie
 * **before** the WebSocket is opened — the socket authenticates by cookie, so
 * connecting first would be a guaranteed 401.
 */
export async function startGateway(): Promise<GatewayAuthState> {
  const began = beginConnection();
  await gatePromise;
  return gateState ?? began;
}

function beginConnection(): GatewayAuthState {
  const connection = client();
  connection.onEpoch(() => {
    // A new gateway process (or a reconnect with a new epoch) invalidates every
    // cached revision: re-snapshot instead of trusting local state (§6.2).
    void reloadAll();
  });
  connection.onReady(() => {
    capabilities = new Set(connection.capabilities());
    void reloadAll();
    readyListeners.forEach((listener) => listener());
  });
  connection.onAuthProbe(probeAuthorization);
  connection.start();
  return connection.isUnauthorized() ? 'stale-link' : 'ok';
}

export function stopGateway(): void {
  client().stop();
}

/**
 * Drop every per-connection domain cache.
 *
 * A domain holds a snapshot from *one* gateway; after the client points at a
 * different one (re-auth to another port, or a new fake gateway in a test) the
 * cached value describes a board that no longer exists. Reconnect alone must not
 * call this: same gateway, same state.
 */
export function clearConnectionCaches(): void {
  sessionDomains.clear();
  promptsDomains.clear();
  tasksDomains.clear();
  contextDomains.clear();
}

/** Fires after each successful handshake (used to re-subscribe). */
export function onGatewayReady(handler: () => void): () => void {
  readyListeners.add(handler);
  return () => readyListeners.delete(handler);
}

export function gatewayEpoch(): string | null {
  return client().getEpoch();
}

// ----------------------------------------------------------------- domains --

const sessionsDomain = new SyncedDomain<SessionSummary[]>(
  'session.list',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      sessions: SessionSummary[];
    }>('session.list.snapshot');
    return { value: sortSessions(result.sessions ?? []), rev: result.rev, epoch: result.epoch };
  },
  (list, delta) => sortSessions(applyListDelta(list, delta)),
);

const approvalsDomain = new SyncedDomain<ApprovalSnapshot>(
  'approvals',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      mode: string;
      permission: string;
      approvalTimeoutSeconds: number;
      pending: ApprovalRequest[];
    }>('approvals.snapshot');
    return {
      value: {
        mode: result.mode ?? 'workspace',
        permission: result.permission ?? 'workspace',
        approvalTimeoutSeconds: result.approvalTimeoutSeconds ?? 0,
        pending: result.pending ?? [],
      },
      rev: result.rev,
      epoch: result.epoch,
    };
  },
  (current, delta) => ({ ...current, pending: applyItemSet(current.pending, delta) }),
);

const settingsDomain = new SyncedDomain<SettingsRows>(
  'settings',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      rows: SettingsRows;
    }>('settings.snapshot');
    return { value: result.rows ?? {}, rev: result.rev, epoch: result.epoch };
  },
  (rows, delta) => applySettingsDelta(rows, delta),
);

const workspacesDomain = new SyncedDomain<WorkspaceSnapshot>(
  'workspace.list',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      workspaces: WorkspaceInfo[];
      currentId?: string;
    }>('workspace.list.snapshot');
    return {
      value: { workspaces: result.workspaces ?? [], currentId: result.currentId },
      rev: result.rev,
      epoch: result.epoch,
    };
  },
  (current, delta) => {
    const list = applyListDelta(current.workspaces, delta);
    const currentId = typeof delta.currentId === 'string' ? delta.currentId : current.currentId;
    return { workspaces: list, currentId };
  },
);

const askDomain = new SyncedDomain<AskPending | null>(
  'ask',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      pending: AskPending | null;
    }>('ask.snapshot');
    return { value: result.pending ?? null, rev: result.rev, epoch: result.epoch };
  },
  (_current, delta) => (delta.pending as AskPending | null | undefined) ?? null,
);

const planDomain = new SyncedDomain<PlanState>(
  'plan',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      state: string;
      content: string;
    }>('plan.snapshot');
    return {
      value: { state: result.state ?? '', content: result.content ?? '' },
      rev: result.rev,
      epoch: result.epoch,
    };
  },
  (_current, delta) => ({
    state: String(delta.state ?? ''),
    content: String(delta.content ?? ''),
  }),
);

const artifactsDomain = new SyncedDomain<ArtifactItem[]>(
  'artifacts',
  async () => {
    const result = await client().request<{
      epoch: string;
      rev: number;
      items: ArtifactItem[];
    }>('artifacts.snapshot');
    return { value: result.items ?? [], rev: result.rev, epoch: result.epoch };
  },
  (items, delta) => applyItemSet(items, delta),
);

/** sessionId → its synced session domain (created on subscribe). */
const sessionDomains = new Map<string, SyncedDomain<SessionSnapshot>>();

function sessionDomain(sessionId: string): SyncedDomain<SessionSnapshot> {
  const existing = sessionDomains.get(sessionId);
  if (existing) return existing;
  const domain = new SyncedDomain<SessionSnapshot>(
    `session:${sessionId}`,
    async () => {
      const snapshot = await loadSnapshot(sessionId);
      return { value: snapshot, rev: snapshot.rev, epoch: snapshot.epoch };
    },
    (session, delta) => applySessionDelta(session, delta),
  );
  sessionDomains.set(sessionId, domain);
  return domain;
}

function sortSessions(list: SessionSummary[]): SessionSummary[] {
  return [...list].sort((a, b) => {
    const left = a.updatedAt ? Date.parse(a.updatedAt) : 0;
    const right = b.updatedAt ? Date.parse(b.updatedAt) : 0;
    return right - left;
  });
}

async function reloadAll(): Promise<void> {
  await Promise.all([
    sessionsDomain.reload().catch(() => undefined),
    approvalsDomain.reload().catch(() => undefined),
    settingsDomain.reload().catch(() => undefined),
    workspacesDomain.reload().catch(() => undefined),
    askDomain.reload().catch(() => undefined),
    planDomain.reload().catch(() => undefined),
    artifactsDomain.reload().catch(() => undefined),
  ]);
  for (const [sessionId, domain] of sessionDomains) {
    await subscribeSessionDomain(sessionId, domain).catch(() => undefined);
  }
}

/** Route one delta notification to its domain (§6.3). */
export function handleDelta(method: string, params: unknown): void {
  const delta = (params ?? {}) as DomainDelta;
  switch (method) {
    case 'session.list.delta':
      sessionsDomain.applyDelta(delta);
      return;
    case 'approvals.delta':
      approvalsDomain.applyDelta(delta);
      return;
    case 'settings.delta':
      settingsDomain.applyDelta(delta);
      return;
    case 'workspace.list.delta':
      workspacesDomain.applyDelta(delta);
      return;
    case 'ask.delta':
      askDomain.applyDelta(delta);
      return;
    case 'plan.delta':
      planDomain.applyDelta(delta);
      return;
    case 'artifacts.delta':
      artifactsDomain.applyDelta(delta);
      return;
    case 'prompts.delta': {
      const sessionId = typeof delta.sessionId === 'string' ? delta.sessionId : '';
      const domain = sessionId ? promptsDomains.get(sessionId) : undefined;
      domain?.applyDelta(delta);
      return;
    }
    case 'session.delta': {
      const sessionId = typeof delta.sessionId === 'string' ? delta.sessionId : '';
      const domain = sessionId ? sessionDomains.get(sessionId) : undefined;
      domain?.applyDelta(delta);
      return;
    }
    case 'context.delta': {
      const sessionId = typeof delta.sessionId === 'string' ? delta.sessionId : '';
      contextDomains.get(sessionId)?.applyDelta(delta);
      return;
    }
    case 'tasks.delta': {
      // One board per session, plus the process-wide board (`tasks`). The delta
      // carries no sessionId, so both are offered the row: a row that does not
      // belong to a board is simply absent from its snapshot set.
      const sessionId = typeof delta.sessionId === 'string' ? delta.sessionId : '';
      if (sessionId) tasksDomains.get(sessionId)?.applyDelta(delta);
      tasksDomains.get(PROCESS_TASKS)?.applyDelta(delta);
      return;
    }
    default:
      return;
  }
}

/** Install the notification router once per connection instance. */
function routeNotifications(): void {
  const connection = client();
  if (routedFor === connection) return;
  routedFor = connection;
  connection.onNotification((method, params) => handleDelta(method, params));
}

// ------------------------------------------------------------- subscribing --

export function subscribeSessions(listener: (list: SessionSummary[]) => void): () => void {
  routeNotifications();
  client().start();
  return sessionsDomain.subscribe(listener);
}

export function subscribeApprovals(listener: (value: ApprovalSnapshot) => void): () => void {
  routeNotifications();
  client().start();
  return approvalsDomain.subscribe(listener);
}

export function subscribeSettings(listener: (rows: SettingsRows) => void): () => void {
  routeNotifications();
  client().start();
  return settingsDomain.subscribe(listener);
}

export function subscribeWorkspaces(listener: (value: WorkspaceSnapshot) => void): () => void {
  routeNotifications();
  client().start();
  return workspacesDomain.subscribe(listener);
}

/**
 * Answer a pending ask (§5.1.5). The gateway resolves the tool call with the text,
 * so the model sees exactly what the user picked — including a custom answer.
 */
export async function answerAsk(id: string, answer: string): Promise<void> {
  await client().request('ask.answer', { id, ...(answer ? { answer } : {}) });
}

/** Cancel a pending ask: the tool resolves as cancelled, the run keeps going. */
export async function cancelAsk(id: string): Promise<void> {
  await client().request('ask.cancel', { id });
}

/**
 * Exit one plan review (§5.1.6). `action` is "approve", "cancel" or "status";
 * "continue with feedback" is not a plan action — it is a normal message that
 * starts the revision run.
 */
export async function exitPlan(action: 'approve' | 'cancel' | 'status'): Promise<PlanState> {
  const result = await client().request<{ state?: string }>('plan.exit', { action });
  return { state: result?.state ?? '', content: '' };
}

export function subscribeAsk(listener: (pending: AskPending | null) => void): () => void {
  routeNotifications();
  client().start();
  return askDomain.subscribe(listener);
}

export function subscribePlan(listener: (state: PlanState) => void): () => void {
  routeNotifications();
  client().start();
  return planDomain.subscribe(listener);
}

export function subscribeArtifacts(listener: (items: ArtifactItem[]) => void): () => void {
  routeNotifications();
  client().start();
  return artifactsDomain.subscribe(listener);
}

/**
 * Toasts (§6.3): `toast.posted {id, level, text, sessionId?, actionLabel?,
 * actionId?}` is a domain-less one-way event, so it is not part of a snapshot or
 * a delta — it is subscribed straight off the notification stream.
 */
export interface ToastEvent {
  id: string;
  level: string;
  text: string;
  sessionId?: string;
  actionLabel?: string;
  actionId?: string;
  /** The gateway asks the tray to show a system notification too. */
  wantSystem?: boolean;
}

export function subscribeToasts(listener: (toast: ToastEvent) => void): () => void {
  client().start();
  return client().onNotification((method, params) => {
    if (method !== 'toast.posted') return;
    const payload = (params ?? {}) as Partial<ToastEvent>;
    if (typeof payload.id !== 'string' || typeof payload.text !== 'string') return;
    listener({
      id: payload.id,
      level: typeof payload.level === 'string' ? payload.level : 'info',
      text: payload.text,
      sessionId: payload.sessionId,
      actionLabel: payload.actionLabel,
      actionId: payload.actionId,
      wantSystem: payload.wantSystem,
    });
  });
}

/** A toast's action button: `notify.action {id, actionId}` (§6.3). */
export async function notifyAction(id: string, actionId: string): Promise<void> {
  await client().request('notify.action', { id, actionId });
}

/** Read the current workspace value without subscribing. */
export function currentWorkspaces(): WorkspaceSnapshot | null {
  return workspacesDomain.value();
}

/**
 * Subscribe to one conversation: `session.subscribe` replays what this client
 * missed, then every `session.delta` updates the cached session.
 */
export async function subscribeSession(
  sessionId: string,
  listener: (session: SessionSnapshot) => void,
): Promise<() => void> {
  routeNotifications();
  const domain = sessionDomain(sessionId);
  await domain.reload();
  const unsubscribe = domain.subscribe(listener);
  await subscribeSessionDomain(sessionId, domain);
  return () => {
    unsubscribe();
    void client().request('session.unsubscribe', { sessionId }).catch(() => undefined);
  };
}

async function subscribeSessionDomain(
  sessionId: string,
  domain: SyncedDomain<SessionSnapshot>,
): Promise<void> {
  const reply = await client().request<{ rev: number; resync?: boolean }>('session.subscribe', {
    sessionId,
    sinceRev: domain.rev(),
  });
  if (reply.resync) await domain.reload();
}

// ---------------------------------------------------------------- commands --

export async function resolveApproval(
  id: string,
  decision: 'allow' | 'allow_always' | 'deny',
): Promise<void> {
  await client().request('approval.resolve', { id, decision });
}

export async function cancelRun(sessionId: string): Promise<void> {
  await client().request('run.cancel', { sessionId });
}

/** `session.create` — the gateway ships it in task-13. */
export async function createSession(title = ''): Promise<SessionSummary> {
  try {
    const result = await client().request<{ sessionId: string }>('session.create', { title });
    await sessionsDomain.reload();
    return { id: result.sessionId, title };
  } catch (cause) {
    // A gateway without the method is a capability gap, not a transport fault:
    // it gets a code so the UI can phrase it in the user's language.
    if (isMethodUnsupported(cause)) {
      throw new ApiError('method_unsupported', 'session.create');
    }
    throw new ApiError('session_create_failed', messageOf(cause));
  }
}

/** A failure's raw detail for logs — never shown to the user as-is. */
function messageOf(cause: unknown): string {
  if (cause instanceof Error) return cause.message;
  if (typeof cause === 'string') return cause;
  return 'unknown';
}

/** `settings.set` — one row per call; the gateway pushes the delta back. */
export async function setSetting(key: string, value: string): Promise<void> {
  try {
    await client().request('settings.set', { key, value });
  } catch (cause) {
    if (isMethodUnsupported(cause)) {
      throw new ApiError('method_unsupported', 'settings.set');
    }
    throw new ApiError('settings_write_failed', messageOf(cause));
  }
}

// --------------------------------------------------------------- snapshots --

/** sessionId → its prompt inbox domain (`prompts:<sessionID>`). */
const contextDomains = new Map<string, SyncedDomain<ContextState>>();

function contextDomain(sessionId: string): SyncedDomain<ContextState> {
  const existing = contextDomains.get(sessionId);
  if (existing) return existing;
  const domain = new SyncedDomain<ContextState>(
    `context:${sessionId}`,
    async () => {
      const result = await client().request<ContextSnapshotResult>('context.snapshot', {
        sessionId,
      });
      return {
        value: normalizeContext(result),
        rev: result.rev ?? 0,
        epoch: result.epoch ?? '',
      };
    },
    // Whole-view replacement (§6.5): there is nothing to merge.
    (value, delta) => applyContextDelta(value, delta as { found?: boolean; context?: ContextView }),
  );
  contextDomains.set(sessionId, domain);
  return domain;
}

/**
 * Subscribe to one session's folded context view.
 *
 * The gateway re-folds every ~2 seconds, so a listener is a stream of the
 * backend's snapshots — not a live cursor (§5.1.9).
 */
export async function subscribeContext(
  sessionId: string,
  listener: (state: ContextState) => void,
): Promise<() => void> {
  routeNotifications();
  const domain = contextDomain(sessionId);
  await domain.reload();
  const unsubscribe = domain.subscribe(listener);
  const reply = await client().request<{ rev: number; resync?: boolean }>('context.subscribe', {
    sessionId,
    sinceRev: domain.rev(),
  });
  if (reply.resync) await domain.reload();
  return unsubscribe;
}

/** One-off read (the tab's first paint uses the subscription instead). */
export async function refreshContext(sessionId: string): Promise<ContextState> {
  const domain = contextDomain(sessionId);
  await domain.reload();
  return domain.value() ?? EMPTY_CONTEXT;
}

/** The process-wide task board is keyed like a session so one code path serves both. */
const PROCESS_TASKS = '';

const tasksDomains = new Map<string, SyncedDomain<GatewayTask[]>>();

/** `tasks:<id>` when a session is given, `tasks` otherwise (§6.5). */
function tasksDomain(sessionId: string | undefined): SyncedDomain<GatewayTask[]> {
  const key = sessionId ?? PROCESS_TASKS;
  const existing = tasksDomains.get(key);
  if (existing) return existing;
  const domain = new SyncedDomain<GatewayTask[]>(
    sessionId ? `tasks:${sessionId}` : 'tasks',
    async () => {
      const result = await client().request<TasksSnapshot>(
        'tasks.snapshot',
        sessionId ? { sessionId } : {},
      );
      return {
        value: (result.tasks ?? []).map(normalizeTask),
        rev: result.rev ?? 0,
        epoch: result.epoch ?? '',
      };
    },
    (value, delta) => applyTasksDelta(value, delta as { added?: TaskWire[]; removed?: string[] }),
  );
  tasksDomains.set(key, domain);
  return domain;
}

/**
 * Subscribe to a task board. `sessionId` omitted = the process-wide board.
 *
 * The board is polled by the gateway for changes made elsewhere (§6.3, 2s
 * ceiling), so a listener is never a promise of "instant".
 */
export async function subscribeTasks(
  sessionId: string | undefined,
  listener: (tasks: GatewayTask[]) => void,
): Promise<() => void> {
  routeNotifications();
  const domain = tasksDomain(sessionId);
  await domain.reload();
  const unsubscribe = domain.subscribe(listener);
  const reply = await client().request<{ rev: number; resync?: boolean }>('tasks.subscribe', {
    ...(sessionId ? { sessionId } : {}),
    sinceRev: domain.rev(),
  });
  if (reply.resync) await domain.reload();
  return unsubscribe;
}

/** Re-read the board once (the chip refreshes on demand, never on a timer). */
export async function refreshTasks(sessionId?: string): Promise<GatewayTask[]> {
  const domain = tasksDomain(sessionId);
  await domain.reload();
  return domain.value() ?? [];
}

/**
 * Stop or resume one task. `pending` **is** the stop state (§5.1.8), so stopping
 * and parking are the same command — there is no `task.stop`.
 */
export async function setTaskStatus(
  id: string,
  status: TaskStatus,
  sessionId?: string,
): Promise<void> {
  await client().request('task.status', {
    id,
    status,
    ...(sessionId ? { sessionId } : {}),
  });
}

/** Cancel = delete on the backend (§5.1.8). Idempotent. */
export async function deleteTask(id: string, sessionId?: string): Promise<void> {
  await client().request('task.delete', { id, ...(sessionId ? { sessionId } : {}) });
}

const promptsDomains = new Map<string, SyncedDomain<QueuedPrompt[]>>();

function promptsDomain(sessionId: string): SyncedDomain<QueuedPrompt[]> {
  const existing = promptsDomains.get(sessionId);
  if (existing) return existing;
  const domain = new SyncedDomain<QueuedPrompt[]>(
    `prompts:${sessionId}`,
    async () => {
      const result = await client().request<{
        epoch: string;
        rev: number;
        prompts: QueuedPromptWire[];
      }>('prompts.snapshot', { sessionId });
      return {
        value: (result.prompts ?? []).map((item) => ({ ...item, sessionId })),
        rev: result.rev,
        epoch: result.epoch,
      };
    },
    (items, delta) => applyPromptsDelta(items, delta as never),
  );
  promptsDomains.set(sessionId, domain);
  return domain;
}

/**
 * Subscribe to one session's inbox: snapshot first, then `prompts.delta`. A
 * delta that cannot be applied (missed frames, resync) is answered with a fresh
 * snapshot by `SyncedDomain`, never with a guessed append.
 */
export async function subscribePrompts(
  sessionId: string,
  listener: (items: QueuedPrompt[]) => void,
): Promise<() => void> {
  routeNotifications();
  const domain = promptsDomain(sessionId);
  await domain.reload();
  const unsubscribe = domain.subscribe(listener);
  const reply = await client().request<{ rev: number; resync?: boolean }>('prompts.subscribe', {
    sessionId,
    sinceRev: domain.rev(),
  });
  if (reply.resync) await domain.reload();
  return unsubscribe;
}

/** Enqueue one prompt into the session's inbox (command, §5.1.1). */
export async function enqueuePrompt(sessionId: string, text: string): Promise<{ id: string }> {
  // The wire field is `prompt` (gateway promptsEnqueueParams), not `text`.
  const result = await client().request<{ id: string }>('prompts.enqueue', { sessionId, prompt: text });
  // The pushed delta is the truth; the domain folds it in.
  return { id: result.id };
}

/** Remove one queued prompt. Idempotent on the backend (repeat removes succeed). */
export async function removeQueuedPrompt(sessionId: string, id: string): Promise<void> {
  await client().request('prompts.remove', { sessionId, id });
}

/** Read the inbox once (used when claiming the head after a run settles). */
export async function refreshPrompts(sessionId: string): Promise<QueuedPrompt[]> {
  const domain = promptsDomain(sessionId);
  await domain.reload();
  return domain.value() ?? [];
}

/** Load one conversation (tail window) — the `defaultMessages` seam. */
export async function loadSnapshot(sessionId: string): Promise<SessionSnapshot> {
  const result = await client().request<{
    epoch: string;
    rev: number;
    session: {
      id: string;
      title: string;
      messages: Array<{
        id: string;
        role: string;
        text: string;
        reasoning?: string;
        streaming?: boolean;
        error?: string;
        usage?: { promptTokens?: number; completionTokens?: number };
        tools?: Array<{
          id: string;
          name: string;
          status?: string;
          summary?: string;
          arguments?: string;
          path?: string;
          errorKind?: string;
        }>;
      }>;
      running: boolean;
      interrupted: boolean;
    };
    truncated: boolean;
    oldestMessageId?: string;
  }>('session.snapshot', { sessionId });

  const session = result.session;
  const messages: SnapshotMessage[] = (session.messages ?? []).map((message, index, all) => {
    const last = index === all.length - 1;
    return {
      id: message.id,
      role: message.role,
      text: message.text ?? '',
      reasoning: message.reasoning ?? '',
      steps: (message.tools ?? []).map((tool) => ({
        callId: tool.id,
        name: tool.name,
        status: mapToolStatus(tool.status),
        input: parseArguments(tool.arguments) ?? (tool.path ? { path: tool.path } : undefined),
        output: tool.summary || undefined,
        error: tool.errorKind || undefined,
      })),
      usage: message.usage
        ? { inputTokens: message.usage.promptTokens, outputTokens: message.usage.completionTokens }
        : undefined,
      stopReason:
        last && session.interrupted
          ? 'aborted'
          : last && message.role === 'assistant'
            ? 'end_turn'
            : undefined,
      remoteError: message.error,
      streaming: message.streaming,
    };
  });

  return {
    id: session.id,
    title: session.title,
    messages,
    running: session.running,
    interrupted: session.interrupted,
    rev: result.rev,
    epoch: result.epoch,
    truncated: result.truncated,
    oldestMessageId: result.oldestMessageId,
  };
}

function mapToolStatus(status: string | undefined): string {
  switch (status) {
    case 'running':
    case 'in_progress':
    case 'active':
      return 'running';
    case 'error':
    case 'failed':
      return 'error';
    default:
      return 'ok';
  }
}

function parseArguments(raw: string | undefined): unknown {
  if (!raw) return undefined;
  try {
    return JSON.parse(raw) as unknown;
  } catch {
    return raw;
  }
}

// ---------------------------------------------------- session delta folding --

export function applySessionDelta(
  session: SessionSnapshot,
  delta: DomainDelta,
): SessionSnapshot {
  const ops = (delta.ops as Array<Record<string, unknown>> | undefined) ?? [];
  let messages = [...session.messages];
  let running = session.running;
  let interrupted = session.interrupted;
  let title = session.title;

  for (const op of ops) {
    switch (op.op) {
      case 'msg.add': {
        const message = mapDeltaMessage(op.message);
        if (!message) break;
        const index = typeof op.index === 'number' ? op.index : messages.length;
        messages = [...messages.slice(0, index), message, ...messages.slice(index)];
        break;
      }
      case 'msg.delta': {
        const messageId = String(op.messageId ?? '');
        const textAppend = typeof op.textAppend === 'string' ? op.textAppend : '';
        const reasoningAppend = op.reasoningAppend as { textAppend?: string } | undefined;
        messages = messages.map((message) =>
          message.id === messageId
            ? {
                ...message,
                text: message.text + textAppend,
                reasoning: (message.reasoning ?? '') + (reasoningAppend?.textAppend ?? ''),
              }
            : message,
        );
        break;
      }
      case 'msg.tool.upsert': {
        const messageId = String(op.messageId ?? '');
        const tool = op.tool as Record<string, unknown> | undefined;
        if (!tool || typeof tool.id !== 'string') break;
        messages = messages.map((message) => {
          if (message.id !== messageId) return message;
          const step: SnapshotStep = {
            callId: tool.id as string,
            name: String(tool.name ?? ''),
            status: mapToolStatus(tool.status as string | undefined),
            input: parseArguments(tool.arguments as string | undefined),
            output: typeof tool.summary === 'string' ? tool.summary : undefined,
            error: typeof tool.errorKind === 'string' ? tool.errorKind : undefined,
          };
          const existing = message.steps ?? [];
          const index = existing.findIndex((item) => item.callId === step.callId);
          const steps =
            index >= 0
              ? existing.map((item, at) => (at === index ? { ...item, ...step } : item))
              : [...existing, step];
          return { ...message, steps };
        });
        break;
      }
      case 'msg.patch': {
        const messageId = String(op.messageId ?? '');
        const fields = (op.fields as Record<string, unknown> | undefined) ?? {};
        messages = messages.map((message) =>
          message.id === messageId
            ? {
                ...message,
                text: typeof fields.text === 'string' ? fields.text : message.text,
                reasoning:
                  typeof fields.reasoning === 'string' ? fields.reasoning : message.reasoning,
                streaming:
                  typeof fields.streaming === 'boolean' ? fields.streaming : message.streaming,
                remoteError:
                  typeof fields.error === 'string' ? fields.error : message.remoteError,
              }
            : message,
        );
        break;
      }
      case 'msg.reset': {
        messages = ((op.messages as Array<Record<string, unknown>> | undefined) ?? [])
          .map((message) => mapDeltaMessage(message))
          .filter((message): message is SnapshotMessage => message !== null);
        break;
      }
      case 'session.patch': {
        const fields = (op.fields as Record<string, unknown> | undefined) ?? {};
        if (typeof fields.running === 'boolean') running = fields.running;
        if (typeof fields.interrupted === 'boolean') interrupted = fields.interrupted;
        if (typeof fields.title === 'string') title = fields.title;
        break;
      }
      default:
        break;
    }
  }

  return { ...session, title, messages, running, interrupted, rev: delta.rev ?? session.rev };
}

function mapDeltaMessage(raw: unknown): SnapshotMessage | null {
  if (!raw || typeof raw !== 'object') return null;
  const message = raw as Record<string, unknown>;
  if (typeof message.id !== 'string') return null;
  return {
    id: message.id,
    role: String(message.role ?? 'assistant'),
    text: String(message.text ?? ''),
    reasoning: typeof message.reasoning === 'string' ? message.reasoning : '',
    steps: Array.isArray(message.tools)
      ? (message.tools as Array<Record<string, unknown>>).map((tool) => ({
          callId: String(tool.id ?? ''),
          name: String(tool.name ?? ''),
          status: mapToolStatus(tool.status as string | undefined),
          input: parseArguments(tool.arguments as string | undefined),
          output: typeof tool.summary === 'string' ? tool.summary : undefined,
          error: typeof tool.errorKind === 'string' ? tool.errorKind : undefined,
        }))
      : [],
    usage: message.usage
      ? {
          inputTokens: (message.usage as { promptTokens?: number }).promptTokens,
          outputTokens: (message.usage as { completionTokens?: number }).completionTokens,
        }
      : undefined,
    streaming: Boolean(message.streaming),
    remoteError: typeof message.error === 'string' ? message.error : undefined,
  };
}

// --- team (TRANSPORT §5.1.9) --------------------------------------------------
//
// A teammate runs in the backend manager, not in this client, and the service has
// no change hook: the screen refreshes this snapshot after every action and polls
// while it is open.

export interface TeamMember {
  id: string;
  name: string;
  status: string;
  runState: string;
  model?: string;
  created?: string;
  last?: string;
  error?: string;
}

export interface TeamActivityRow {
  id: string;
  memberId?: string;
  kind: string;
  text?: string;
  at?: string;
}

export interface TeamSnapshot {
  members: TeamMember[];
  activities: TeamActivityRow[];
}

export async function loadTeam(): Promise<TeamSnapshot> {
  const result = await client().request<Partial<TeamSnapshot>>('team.snapshot', {});
  return {
    members: Array.isArray(result?.members) ? result.members : [],
    activities: Array.isArray(result?.activities) ? result.activities : [],
  };
}

export async function spawnTeammate(input: {
  name: string;
  prompt: string;
  model?: string;
}): Promise<TeamMember> {
  const result = await client().request<{ member: TeamMember }>('team.spawn', input);
  return result.member;
}

export async function sendToTeammate(
  id: string,
  message: string,
  options: { async?: boolean } = {},
): Promise<string | undefined> {
  const result = await client().request<{ answer?: string; ok?: boolean }>('team.send', {
    id,
    message,
    ...(options.async ? { async: true } : {}),
  });
  return result?.answer;
}

export async function waitTeammate(id: string): Promise<string | undefined> {
  const result = await client().request<{ answer?: string }>('team.wait', { id });
  return result?.answer;
}

export async function interruptTeammate(id: string): Promise<void> {
  await client().request('team.interrupt', { id });
}

export async function closeTeammate(id: string): Promise<void> {
  await client().request('team.close', { id });
}

export async function loadTeamActivity(limit = 50): Promise<TeamActivityRow[]> {
  const result = await client().request<{ items?: TeamActivityRow[] }>('team.activity', { limit });
  return Array.isArray(result?.items) ? result.items : [];
}
