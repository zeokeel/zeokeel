/**
 * The WS state plane client (docs/TRANSPORT.md §4.1, §4.2, §4.5, §6).
 *
 * One JSON-RPC 2.0 message per line, inside WebSocket text frames — the same
 * framing ACP uses over stdio, so the gateway's framing code is shared. This
 * module owns: the handshake (`initialize` → `ready`), request/response
 * correlation, notifications, the per-connection `epoch`, and reconnect with
 * exponential backoff.
 *
 * Nothing above `src/api/` may touch a WebSocket; components subscribe through
 * the typed helpers in `gateway.ts`.
 */

export const PROTOCOL_VERSION = 1;
export const CLIENT_NAME = 'zeokeel-web';
export const CLIENT_VERSION = '0.0.0';

/**
 * `unauthorized` is terminal: the gateway rejected the cookie, so retrying is
 * pointless (and the gateway rate-limits the source). Only a new `#token=` from
 * the tray or the terminal can recover it.
 */
export type ConnectionStatus = 'connecting' | 'online' | 'offline' | 'unauthorized';

export interface RpcFailure extends Error {
  code: number;
  /** Stable application name (`session_not_found`, …), never English prose. */
  codeName?: string;
  data?: Record<string, unknown>;
}

export interface InitializeResult {
  epoch: string;
  protocolVersion: number;
  serverVersion: string;
  capabilities: string[];
  limits: {
    maxFrameBytes: number;
    maxAttachmentBytes: number;
    snapshotTailDefault: number;
    deltaRingBytes: number;
  };
}

interface Pending {
  resolve: (value: unknown) => void;
  reject: (error: RpcFailure) => void;
  timer: ReturnType<typeof setTimeout>;
}

const REQUEST_TIMEOUT_MS = 15_000;
const PING_INTERVAL_MS = 20_000;
const BACKOFF_BASE_MS = 500;
const BACKOFF_MAX_MS = 8_000;

/** Transport surface the client needs; a fake implements it in tests. */
export interface SocketLike {
  readyState: number;
  send(data: string): void;
  close(): void;
  addEventListener(type: string, listener: (event: unknown) => void): void;
  removeEventListener?(type: string, listener: (event: unknown) => void): void;
}

export interface RpcOptions {
  /** Overridable for tests and for the mock gateway's port. */
  url?: () => string;
  createSocket?: (url: string) => SocketLike;
  /** Injected for deterministic backoff in tests. */
  setTimeout?: (handler: () => void, ms: number) => unknown;
}

function defaultUrl(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${scheme}://${window.location.host}/ws`;
}

/** True when `text` parses as JSON (i.e. the frame is not truncated). */
function isCompleteJson(text: string): boolean {
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

function failure(code: number, message: string, codeName?: string): RpcFailure {
  const error = new Error(message) as RpcFailure;
  error.code = code;
  if (codeName) error.codeName = codeName;
  return error;
}

export class GatewayRpc {
  private socket: SocketLike | null = null;
  private status: ConnectionStatus = 'offline';
  private ready = false;
  private closedByUser = false;
  private epoch: string | null = null;
  private nextId = 1;
  private pending = new Map<number, Pending>();
  private notificationHandlers = new Set<(method: string, params: unknown) => void>();
  private statusHandlers = new Set<(status: ConnectionStatus) => void>();
  private epochHandlers = new Set<(epoch: string, changed: boolean) => void>();
  private readyHandlers = new Set<() => void>();
  private reconnectAttempt = 0;
  private advertised: string[] = [];
  /** Set when the gateway answered 401/403: stop the retry loop for good. */
  private unauthorized = false;
  /** Called before a close schedules a retry, so the caller can probe /auth. */
  private authProbe: (() => Promise<'authorized' | 'unauthorized' | 'unknown'>) | null = null;
  /**
   * Runs before the *first* socket is opened and resolves to whether connecting
   * is allowed at all. This is what keeps the fragment→cookie exchange ahead of
   * the WebSocket: providers subscribe during mount, so "connect only after
   * authorising" has to live in the transport, not in call order.
   */
  private authGate: (() => Promise<boolean>) | null = null;
  private gatePromise: Promise<boolean> | null = null;
  private reconnectTimer: unknown = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private buffer = '';

  constructor(private options: RpcOptions = {}) {}

  // ---------------------------------------------------------------- status --

  getStatus(): ConnectionStatus {
    return this.status;
  }

  getEpoch(): string | null {
    return this.epoch;
  }

  /** Capabilities the gateway advertised in `initialize` (§4.2). */
  capabilities(): string[] {
    return this.advertised;
  }

  /** Probe used to tell "network dropped" from "cookie rejected" (§3.1). */
  onAuthProbe(probe: () => Promise<'authorized' | 'unauthorized' | 'unknown'>): void {
    this.authProbe = probe;
  }

  /** Gate the first connection behind the fragment→cookie exchange (§3.1). */
  setAuthGate(gate: () => Promise<boolean>): void {
    this.authGate = gate;
  }

  /**
   * Terminal auth failure: show the stale-link message and never reconnect
   * (each retry would be another 401 against a gateway that rate-limits).
   */
  markUnauthorized(): void {
    this.unauthorized = true;
    this.closedByUser = true;
    this.clearTimers();
    const socket = this.socket;
    this.socket = null;
    socket?.close();
    this.rejectAll(failure(1020, 'unauthorized', 'unauthorized'));
    this.setStatus('unauthorized');
  }

  isUnauthorized(): boolean {
    return this.unauthorized;
  }

  isOnline(): boolean {
    return this.status === 'online' && this.ready;
  }

  onStatus(handler: (status: ConnectionStatus) => void): () => void {
    this.statusHandlers.add(handler);
    return () => this.statusHandlers.delete(handler);
  }

  onNotification(handler: (method: string, params: unknown) => void): () => void {
    this.notificationHandlers.add(handler);
    return () => this.notificationHandlers.delete(handler);
  }

  /** Fires after every successful handshake, with whether the epoch changed. */
  onEpoch(handler: (epoch: string, changed: boolean) => void): () => void {
    this.epochHandlers.add(handler);
    return () => this.epochHandlers.delete(handler);
  }

  /** Fires once the handshake finished and deltas may flow. */
  onReady(handler: () => void): () => void {
    this.readyHandlers.add(handler);
    return () => this.readyHandlers.delete(handler);
  }

  // ------------------------------------------------------------ lifecycle --

  start(): void {
    if (this.unauthorized) return;
    this.closedByUser = false;
    if (this.socket) return;
    this.connect();
  }

  stop(): void {
    this.closedByUser = true;
    this.clearTimers();
    // Detach first: the close event must not look like a dropped connection.
    const socket = this.socket;
    this.socket = null;
    socket?.close();
    this.setStatus('offline');
    this.rejectAll(failure(1026, 'connection closed by the client', 'closed'));
  }

  /** Send one request and wait for its result. */
  async request<T = unknown>(
    method: string,
    params: unknown = {},
    timeoutMs = REQUEST_TIMEOUT_MS,
  ): Promise<T> {
    await this.whenReady();
    const socket = this.socket;
    if (!socket) throw failure(1024, 'not connected', 'not_ready');
    const id = this.nextId++;
    const message = JSON.stringify({ jsonrpc: '2.0', id, method, params });
    const promise = new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(failure(1025, `${method} timed out`, 'timeout'));
      }, timeoutMs);
      this.pending.set(id, {
        resolve: resolve as (value: unknown) => void,
        reject,
        timer,
      });
    });
    socket.send(`${message}\n`);
    return promise;
  }

  /** Fire one notification (no response expected). */
  notify(method: string, params: unknown = {}): void {
    const socket = this.socket;
    if (!socket) return;
    socket.send(`${JSON.stringify({ jsonrpc: '2.0', method, params })}\n`);
  }

  private whenReady(): Promise<void> {
    if (this.isOnline()) return Promise.resolve();
    this.start();
    return new Promise((resolve, reject) => {
      const deadline = Date.now() + REQUEST_TIMEOUT_MS;
      const tick = () => {
        if (this.isOnline()) {
          resolve();
          return;
        }
        if (Date.now() > deadline) {
          reject(failure(1024, 'gateway is not reachable', 'not_ready'));
          return;
        }
        setTimeout(tick, 50);
      };
      tick();
    });
  }

  private connect(): void {
    // §3.1: the socket authenticates by cookie, so it must not be opened until
    // the exchange ran. Everyone calls `start()` (providers, retries), so the
    // ordering is enforced here rather than at call sites.
    if (!this.authGate) {
      this.open();
      return;
    }
    void this.ensureGate().then((allowed) => {
      if (!allowed || this.unauthorized) return;
      this.open();
    });
  }

  private ensureGate(): Promise<boolean> {
    if (!this.authGate) return Promise.resolve(true);
    if (!this.gatePromise) {
      this.gatePromise = this.authGate().then((allowed) => {
        if (!allowed) this.markUnauthorized();
        return allowed;
      });
    }
    return this.gatePromise;
  }

  private open(): void {
    const create =
      this.options.createSocket ??
      ((url: string) => new WebSocket(url) as unknown as SocketLike);
    const url = this.options.url ? this.options.url() : defaultUrl();
    this.setStatus('connecting');
    let socket: SocketLike;
    try {
      socket = create(url);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.socket = socket;
    this.ready = false;
    this.buffer = '';

    // Events from a socket this client already replaced (React Strict Mode
    // mounts start→stop→start) must not tear down the current connection.
    socket.addEventListener('open', () => {
      if (this.socket !== socket) return;
      void this.handshake();
    });
    socket.addEventListener('message', (event) => {
      if (this.socket !== socket) return;
      this.onFrame(event);
    });
    socket.addEventListener('close', () => {
      if (this.socket !== socket) return;
      this.onClose();
    });
    socket.addEventListener('error', () => {
      if (this.socket !== socket) return;
      this.onClose();
    });
  }

  private async handshake(): Promise<void> {
    try {
      const result = await this.rawRequest<InitializeResult>('initialize', {
        clientName: CLIENT_NAME,
        clientVersion: CLIENT_VERSION,
        protocolVersion: PROTOCOL_VERSION,
        capabilities: [
          'session.list',
          'session.snapshot',
          'session.delta',
          'session.subscribe',
          'approvals',
          'ask',
          'plan',
          'artifacts',
          'settings',
          'workspace',
          'run.cancel',
        ],
      });
      const previous = this.epoch;
      const changed = previous !== null && previous !== result.epoch;
      this.epoch = result.epoch;
      this.advertised = result.capabilities ?? [];
      this.epochHandlers.forEach((handler) => handler(result.epoch, changed));
      await this.rawRequest('ready', {});
      this.ready = true;
      this.reconnectAttempt = 0;
      this.setStatus('online');
      this.startPing();
      this.readyHandlers.forEach((handler) => handler());
    } catch {
      // A refused handshake is not worth retrying in a tight loop; the close
      // handler schedules the (backed-off) retry.
      this.socket?.close();
    }
  }

  private rawRequest<T>(method: string, params: unknown): Promise<T> {
    const socket = this.socket;
    if (!socket) return Promise.reject(failure(1024, 'not connected', 'not_ready'));
    const id = this.nextId++;
    const promise = new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(failure(1025, `${method} timed out`, 'timeout'));
      }, REQUEST_TIMEOUT_MS);
      this.pending.set(id, { resolve: resolve as (value: unknown) => void, reject, timer });
    });
    socket.send(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
    return promise;
  }

  private onFrame(event: unknown): void {
    const data = (event as { data?: unknown }).data;
    if (typeof data !== 'string') return;
    this.buffer += data;
    // §4.1: one JSON-RPC message per line; a frame may carry several.
    let newline = this.buffer.indexOf('\n');
    while (newline >= 0) {
      const line = this.buffer.slice(0, newline).trim();
      this.buffer = this.buffer.slice(newline + 1);
      if (line !== '') this.handleMessage(line);
      newline = this.buffer.indexOf('\n');
    }
    // The gateway writes one message per WebSocket frame **without** a trailing
    // newline (`encodeFrame` is a bare json.Marshal), so the remainder must be
    // consumed as soon as it is a complete JSON value. Requiring `\n` here left
    // every response unparsed against the real gateway while the mock — which
    // appends `\n` — passed.
    const rest = this.buffer.trim();
    if (rest === '') return;
    if (isCompleteJson(rest)) {
      this.buffer = '';
      this.handleMessage(rest);
    }
  }

  private handleMessage(line: string): void {
    let message: Record<string, unknown>;
    try {
      message = JSON.parse(line) as Record<string, unknown>;
    } catch {
      return;
    }
    const id = typeof message.id === 'number' ? message.id : null;
    if (id !== null) {
      const pending = this.pending.get(id);
      if (!pending) return;
      this.pending.delete(id);
      clearTimeout(pending.timer);
      const error = message.error as
        | { code?: number; message?: string; data?: Record<string, unknown> }
        | undefined;
      if (error) {
        const failureError = failure(
          Number(error.code ?? -32603),
          String(error.message ?? 'gateway error'),
          typeof error.data?.name === 'string' ? error.data.name : undefined,
        );
        failureError.data = error.data;
        pending.reject(failureError);
      } else {
        pending.resolve(message.result);
      }
      return;
    }
    const method = typeof message.method === 'string' ? message.method : '';
    if (!method) return;
    const params = message.params;
    this.notificationHandlers.forEach((handler) => handler(method, params));
  }

  private onClose(): void {
    if (this.socket === null && this.status === 'offline') return;
    this.socket = null;
    this.ready = false;
    this.clearTimers();
    this.rejectAll(failure(1024, 'connection lost', 'disconnected'));
    this.setStatus('offline');
    if (this.closedByUser || this.unauthorized) return;
    // A close before the handshake ever completed may be a 401 (the browser
    // hides the HTTP status from WebSocket). Probe once, then decide: a
    // rejected cookie stops the loop instead of hammering the gateway.
    if (this.epoch === null && this.authProbe) {
      void this.authProbe().then((outcome) => {
        if (outcome === 'unauthorized') {
          this.markUnauthorized();
          return;
        }
        this.scheduleReconnect();
      });
      return;
    }
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (this.closedByUser || this.reconnectTimer) return;
    this.reconnectAttempt += 1;
    const exponential = Math.min(
      BACKOFF_MAX_MS,
      BACKOFF_BASE_MS * 2 ** Math.min(this.reconnectAttempt - 1, 6),
    );
    // Jitter keeps several tabs from reconnecting in lockstep.
    const delay = exponential * (0.8 + Math.random() * 0.4);
    const schedule: (handler: () => void, ms: number) => unknown =
      this.options.setTimeout ?? ((handler, ms) => setTimeout(handler, ms));
    this.reconnectTimer = schedule(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private startPing(): void {
    this.clearPing();
    this.pingTimer = setInterval(() => {
      if (!this.isOnline()) return;
      void this.request('ping', {}, 5_000).catch(() => undefined);
    }, PING_INTERVAL_MS);
  }

  private clearPing(): void {
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.pingTimer = null;
  }

  private clearTimers(): void {
    this.clearPing();
  }

  private setStatus(status: ConnectionStatus): void {
    if (this.status === status) return;
    this.status = status;
    this.statusHandlers.forEach((handler) => handler(status));
  }

  private rejectAll(error: RpcFailure): void {
    for (const [id, pending] of this.pending) {
      clearTimeout(pending.timer);
      pending.reject(error);
      this.pending.delete(id);
    }
  }
}

let shared: GatewayRpc | null = null;

/** The app's single control-plane connection. */
export function gatewayRpc(options: RpcOptions = {}): GatewayRpc {
  if (!shared) shared = new GatewayRpc(options);
  return shared;
}

/** Replace the singleton (tests only). */
export function setGatewayRpc(rpc: GatewayRpc | null): void {
  shared = rpc;
}