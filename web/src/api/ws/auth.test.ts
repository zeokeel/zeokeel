import { afterEach, describe, expect, it, vi } from 'vitest';
import { exchangeFragmentToken, probeAuthorization, tokenFromHash } from './auth';
import { GatewayRpc, setGatewayRpc, type SocketLike } from './rpc';

/**
 * The fragment → cookie exchange (docs/TRANSPORT.md §3.1).
 *
 * Ordering is the whole point: `POST /auth` must land before the WebSocket is
 * opened, because the socket authenticates by cookie. A client that connects
 * first is a guaranteed 401 against the real gateway — which is exactly what
 * happened before this test existed.
 */

const view = (hash: string) => ({ hash, pathname: '/', search: '' });

describe('tokenFromHash', () => {
  it('reads the token from a plain fragment', () => {
    expect(tokenFromHash('#token=abc123')).toBe('abc123');
    expect(tokenFromHash('#token=abc123&other=1')).toBe('abc123');
  });

  it('reads the token from a path-shaped fragment', () => {
    expect(tokenFromHash('#/c/s-1?token=abc123')).toBe('abc123');
  });

  it('returns null when there is nothing to exchange', () => {
    expect(tokenFromHash('')).toBeNull();
    expect(tokenFromHash('#')).toBeNull();
    expect(tokenFromHash('#token=')).toBeNull();
    expect(tokenFromHash('#other=1')).toBeNull();
  });
});

describe('exchangeFragmentToken', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts the token, keeps the cookie, then erases the fragment', async () => {
    const calls: string[] = [];
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(`${String(input)} ${init?.method ?? 'GET'} credentials=${init?.credentials}`);
      return new Response(null, { status: 204 });
    });
    const replaceState = vi.fn();

    const outcome = await exchangeFragmentToken({
      fetchImpl: fetchImpl as unknown as typeof fetch,
      location: view('#token=abc123'),
      replaceState,
    });

    expect(outcome).toBe('exchanged');
    // The exchange happened first, with credentials so the cookie is stored.
    expect(calls).toEqual(['/auth POST credentials=same-origin']);
    const body = JSON.parse(String(fetchImpl.mock.calls[0][1]?.body));
    expect(body).toEqual({ token: 'abc123' });
    // Only then is the fragment removed, and the URL keeps path + query.
    expect(replaceState).toHaveBeenCalledWith('/');
  });

  it('does nothing when the URL has no token', async () => {
    const fetchImpl = vi.fn();
    const replaceState = vi.fn();
    const outcome = await exchangeFragmentToken({
      fetchImpl: fetchImpl as unknown as typeof fetch,
      location: view(''),
      replaceState,
    });
    expect(outcome).toBe('none');
    expect(fetchImpl).not.toHaveBeenCalled();
    expect(replaceState).not.toHaveBeenCalled();
  });

  it('reports a stale link instead of throwing on 401', async () => {
    const fetchImpl = vi.fn(async () => new Response(null, { status: 401 }));
    const replaceState = vi.fn();
    const outcome = await exchangeFragmentToken({
      fetchImpl: fetchImpl as unknown as typeof fetch,
      location: view('#token=stale'),
      replaceState,
    });
    expect(outcome).toBe('invalid');
    // A rejected token is not worth rewriting history for.
    expect(replaceState).not.toHaveBeenCalled();
  });

  it('propagates a network failure so the caller can retry later', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    });
    await expect(
      exchangeFragmentToken({
        fetchImpl: fetchImpl as unknown as typeof fetch,
        location: view('#token=abc'),
        replaceState: vi.fn(),
      }),
    ).rejects.toThrow('Failed to fetch');
  });
});

describe('probeAuthorization', () => {
  it('separates "cookie rejected" from "route missing"', async () => {
    const probe = (status: number) => probeAuthorization((async () => new Response(null, { status })) as typeof fetch);
    await expect(probe(401)).resolves.toBe('unauthorized');
    await expect(probe(403)).resolves.toBe('unauthorized');
    // 404 = the request got past auth (unknown route), which proves the cookie
    // was accepted; 400 is also past auth (params, not credentials).
    await expect(probe(404)).resolves.toBe('authorized');
    await expect(probe(400)).resolves.toBe('authorized');
    await expect(probe(200)).resolves.toBe('authorized');
    await expect(probe(500)).resolves.toBe('unknown');
  });
});

/** A socket that never opens: the gateway refused the upgrade. */
class RefusedSocket implements SocketLike {
  readyState = 0;
  private listeners = new Map<string, Array<(event: unknown) => void>>();

  send(): void {}

  close(): void {}

  addEventListener(type: string, listener: (event: unknown) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  /** Browser behaviour for a 401 upgrade: open never fires, close does. */
  refuse(): void {
    this.readyState = 3;
    for (const listener of this.listeners.get('close') ?? []) listener({ code: 1006 });
  }
}

describe('a rejected cookie stops the retry loop', () => {
  it('settles on "unauthorized" and never reconnects', async () => {
    const sockets: RefusedSocket[] = [];
    const rpc = new GatewayRpc({
      url: () => 'ws://test/ws',
      createSocket: () => {
        const socket = new RefusedSocket();
        sockets.push(socket);
        return socket;
      },
      setTimeout: (handler) => {
        const timer = setTimeout(handler, 0);
        return timer;
      },
    });
    setGatewayRpc(rpc);
    const probe = vi.fn(async () => 'unauthorized' as const);
    rpc.onAuthProbe(probe);
    rpc.start();
    sockets[0].refuse();

    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(probe).toHaveBeenCalledTimes(1);
    expect(rpc.getStatus()).toBe('unauthorized');
    // The loop is over: no second socket, even after the old backoff would fire.
    expect(sockets).toHaveLength(1);
    rpc.start();
    expect(sockets).toHaveLength(1);
    setGatewayRpc(null);
  });

  it('keeps reconnecting when the probe says the cookie is fine', async () => {
    const sockets: RefusedSocket[] = [];
    const rpc = new GatewayRpc({
      url: () => 'ws://test/ws',
      createSocket: () => {
        const socket = new RefusedSocket();
        sockets.push(socket);
        return socket;
      },
      setTimeout: (handler) => {
        const timer = setTimeout(handler, 0);
        return timer;
      },
    });
    setGatewayRpc(rpc);
    rpc.onAuthProbe(async () => 'authorized');
    rpc.start();
    sockets[0].refuse();

    await new Promise((resolve) => setTimeout(resolve, 30));
    expect(sockets.length).toBeGreaterThan(1);
    expect(rpc.getStatus()).not.toBe('unauthorized');
    rpc.stop();
    setGatewayRpc(null);
  });
});