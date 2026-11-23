import { describe, expect, it } from 'vitest';
import { GatewayRpc, type SocketLike } from './rpc';

/** A socket that records what the client sent and can be driven by the test. */
class ScriptedSocket implements SocketLike {
  readyState = 1;
  /** Raw payloads, exactly as the client wrote them (newline included). */
  raw: string[] = [];
  sent: string[] = [];
  private listeners = new Map<string, Array<(event: unknown) => void>>();
  closed = false;

  send(data: string): void {
    this.raw.push(data);
    for (const line of data.split('\n')) if (line.trim()) this.sent.push(line);
  }

  close(): void {
    this.closed = true;
    this.emit('close', {});
  }

  addEventListener(type: string, listener: (event: unknown) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  /** Feed one server frame (raw text, may hold several lines). */
  push(text: string): void {
    this.emit('message', { data: text });
  }

  open(): void {
    this.emit('open', {});
  }

  messages(): Array<Record<string, unknown>> {
    return this.sent.map((line) => JSON.parse(line) as Record<string, unknown>);
  }

  private emit(type: string, event: unknown): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

/** Let queued microtasks (the handshake's awaits) run. */
async function settle(): Promise<void> {
  for (let index = 0; index < 8; index += 1) await Promise.resolve();
}

function harness() {
  const sockets: ScriptedSocket[] = [];
  const rpc = new GatewayRpc({
    url: () => 'ws://test/ws',
    createSocket: () => {
      const socket = new ScriptedSocket();
      sockets.push(socket);
      return socket;
    },
    setTimeout: (handler) => {
      const timer = setTimeout(handler, 0);
      return timer;
    },
  });
  return { rpc, sockets, latest: () => sockets[sockets.length - 1] };
}

/** Answer `initialize` + `ready` like the gateway does (§4.2). */
async function completeHandshake(
  socket: ScriptedSocket,
  epoch = 'epoch-1',
  capabilities?: string[],
) {
  const initialize = socket.messages().find((message) => message.method === 'initialize');
  socket.push(
    `${JSON.stringify({
      jsonrpc: '2.0',
      id: initialize?.id,
      result: {
        epoch,
        protocolVersion: 1,
        serverVersion: 'test',
        capabilities: capabilities ?? ['session.list', 'settings'],
        limits: {
          maxFrameBytes: 1,
          maxAttachmentBytes: 1,
          snapshotTailDefault: 60,
          deltaRingBytes: 1,
        },
      },
    })}\n`,
  );
  // `ready` is only sent once the client's initialize promise resolves.
  await settle();
  const ready = socket.messages().find((message) => message.method === 'ready');
  socket.push(`${JSON.stringify({ jsonrpc: '2.0', id: ready?.id, result: {} })}\n`);
}

describe('GatewayRpc', () => {
  it('handshakes with initialize then ready and records the epoch', async () => {
    const { rpc, latest } = harness();
    rpc.start();
    const socket = latest();
    socket.open();
    await settle();
    await completeHandshake(socket, 'epoch-abc', ['session.list', 'session.create']);
    await settle();
    expect(rpc.getStatus()).toBe('online');
    expect(rpc.getEpoch()).toBe('epoch-abc');
    expect(rpc.capabilities()).toContain('session.create');
    const methods = socket.messages().map((message) => message.method);
    expect(methods).toEqual(['initialize', 'ready']);
  });

  it('frames every request as one newline-terminated JSON-RPC line', async () => {
    const { rpc, latest } = harness();
    rpc.start();
    const socket = latest();
    socket.open();
    await settle();
    await completeHandshake(socket);
    await settle();

    const pending = rpc.request('session.snapshot', { sessionId: 's-1' });
    // `request` waits for `isOnline()` first, so the frame is sent a tick later.
    await settle();
    const payload = socket.raw[socket.raw.length - 1];
    expect(payload.endsWith('\n')).toBe(true);
    const sent = payload;
    const message = JSON.parse(sent) as Record<string, unknown>;
    expect(message).toMatchObject({
      jsonrpc: '2.0',
      method: 'session.snapshot',
      params: { sessionId: 's-1' },
    });
    socket.push(
      `${JSON.stringify({ jsonrpc: '2.0', id: message.id, result: { session: { id: 's-1' } } })}\n`,
    );
    await expect(pending).resolves.toEqual({ session: { id: 's-1' } });
  });

  it('parses a frame with no trailing newline (the real gateway writes these)', async () => {
    const { rpc, latest } = harness();
    const seen: string[] = [];
    rpc.onNotification((method) => seen.push(method));
    rpc.start();
    const socket = latest();
    socket.open();
    await settle();
    // No `\n` at the end: `encodeFrame` in the gateway is a bare json.Marshal.
    socket.push(JSON.stringify({ jsonrpc: '2.0', method: 'approvals.delta', params: {} }));
    expect(seen).toEqual(['approvals.delta']);
  });

  it('splits a frame that carries several messages', async () => {
    const { rpc, latest } = harness();
    const seen: string[] = [];
    rpc.onNotification((method) => seen.push(method));
    rpc.start();
    const socket = latest();
    socket.open();
    await settle();
    await completeHandshake(socket);
    await settle();

    socket.push(
      `${JSON.stringify({ jsonrpc: '2.0', method: 'approvals.delta', params: {} })}\n` +
        `${JSON.stringify({ jsonrpc: '2.0', method: 'settings.delta', params: {} })}\n`,
    );
    expect(seen).toEqual(['approvals.delta', 'settings.delta']);
  });

  it('rejects a request with the gateway code, not English prose', async () => {
    const { rpc, latest } = harness();
    rpc.start();
    const socket = latest();
    socket.open();
    await settle();
    await completeHandshake(socket);
    await settle();

    const pending = rpc.request('session.snapshot', { sessionId: 'nope' });
    await settle();
    const sent = socket.messages().find((message) => message.method === 'session.snapshot');
    const id = sent?.id as number;
    socket.push(
      `${JSON.stringify({
        jsonrpc: '2.0',
        id,
        error: { code: 1001, message: 'session_not_found', data: { name: 'session_not_found' } },
      })}\n`,
    );
    await expect(pending).rejects.toMatchObject({ code: 1001, codeName: 'session_not_found' });
  });

  it('reports a changed epoch on reconnect and re-connects with backoff', async () => {
    const { rpc, sockets, latest } = harness();
    const epochs: Array<{ epoch: string; changed: boolean }> = [];
    rpc.onEpoch((epoch, changed) => epochs.push({ epoch, changed }));
    rpc.start();
    const first = latest();
    first.open();
    await settle();
    await completeHandshake(first, 'epoch-1');
    await settle();

    // The gateway goes away.
    first.close();
    expect(rpc.getStatus()).toBe('offline');

    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(sockets.length).toBeGreaterThan(1);
    const second = latest();
    second.open();
    await settle();
    await completeHandshake(second, 'epoch-2');
    await settle();

    expect(epochs).toEqual([
      { epoch: 'epoch-1', changed: false },
      { epoch: 'epoch-2', changed: true },
    ]);
    expect(rpc.getStatus()).toBe('online');
  });

  it('rejects in-flight requests when the socket closes', async () => {
    const { rpc, latest } = harness();
    rpc.start();
    const socket = latest();
    socket.open();
    await settle();
    await completeHandshake(socket);
    await settle();

    const pending = rpc.request('session.list.snapshot', {});
    socket.close();
    await expect(pending).rejects.toMatchObject({ code: 1024 });
  });
});