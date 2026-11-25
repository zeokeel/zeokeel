#!/usr/bin/env node
// Live checks against a REAL running product (no mocks, no fake gateway).
//
// `run.sh` boots `--product serve` on a fresh data dir, seeds one webhook source
// and then calls this script. Everything here goes over the product's own two
// transports:
//
//   * the WebSocket control plane (JSON-RPC 2.0, one message per line, the
//     subset of RFC 6455 the gateway implements — see internal/gateway/ws.go),
//     authenticated by the `zk` cookie minted with POST /auth;
//   * the signed inbound HTTP path (X-Zeokeel-Signature), which takes no token.
//
// Every check prints `PASS <name> <raw result>` or `FAIL <name> <raw result>`.
// The "raw result" is the actual answer, not a summary: the point of this file is
// to print what the product said, so a reader can disagree with the verdict.
//
// Zero dependencies: node's net/crypto only. Node's global WebSocket cannot set
// a cookie or an Origin header, which the gateway's auth requires, so the
// handshake is done on a raw socket — the same technique internal/devtools/gwprobe
// uses on the Go side.
//
// Usage: node scripts/verify-live/checks.mjs --port 9932 --token <token> --dir <dataDir>
// Exit 0 only if every check PASSed.

import net from 'node:net';
import crypto from 'node:crypto';

// --------------------------------------------------------------- arguments --

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 2) {
    const name = argv[i];
    const value = argv[i + 1];
    if (!name?.startsWith('--') || value === undefined) {
      throw new Error(`bad argument ${JSON.stringify(name)}; want --port N --token T --dir D`);
    }
    out[name.slice(2)] = value;
  }
  const port = Number(out.port);
  if (!Number.isInteger(port) || port <= 0) throw new Error('--port is required');
  if (!out.token) throw new Error('--token is required');
  if (!out.dir) throw new Error('--dir is required');
  return { port, token: out.token, dir: out.dir };
}

// ------------------------------------------------------------- ws framing --

/** One masked client frame (RFC 6455 §5.2): control frames and text both. */
function encodeFrame(opcode, payload) {
  const data = Buffer.isBuffer(payload) ? payload : Buffer.from(payload, 'utf8');
  const mask = crypto.randomBytes(4);
  let header;
  if (data.length < 126) {
    header = Buffer.alloc(2);
    header[1] = 0x80 | data.length;
  } else if (data.length < 65536) {
    header = Buffer.alloc(4);
    header[1] = 0x80 | 126;
    header.writeUInt16BE(data.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[1] = 0x80 | 127;
    header.writeBigUInt64BE(BigInt(data.length), 2);
  }
  header[0] = 0x80 | opcode; // FIN + opcode
  for (let i = 0; i < data.length; i += 1) data[i] ^= mask[i % 4];
  return Buffer.concat([header, mask, data]);
}

/** Drains complete frames out of `state.buffer`; returns the leftover bytes. */
function takeFrames(state, onFrame) {
  let buffer = state.buffer;
  for (;;) {
    if (buffer.length < 2) break;
    const fin = (buffer[0] & 0x80) !== 0;
    const opcode = buffer[0] & 0x0f;
    const masked = (buffer[1] & 0x80) !== 0;
    let length = buffer[1] & 0x7f;
    let offset = 2;
    if (length === 126) {
      if (buffer.length < 4) break;
      length = buffer.readUInt16BE(2);
      offset = 4;
    } else if (length === 127) {
      if (buffer.length < 10) break;
      length = Number(buffer.readBigUInt64BE(2));
      offset = 10;
    }
    const maskBytes = masked ? 4 : 0;
    if (buffer.length < offset + maskBytes + length) break;
    let payload = buffer.subarray(offset + maskBytes, offset + maskBytes + length);
    if (masked) {
      const mask = buffer.subarray(offset, offset + 4);
      payload = Buffer.from(payload);
      for (let i = 0; i < payload.length; i += 1) payload[i] ^= mask[i % 4];
    }
    buffer = buffer.subarray(offset + maskBytes + length);
    onFrame({ fin, opcode, payload: Buffer.from(payload) });
  }
  state.buffer = buffer;
}

/**
 * connect opens the control plane: POST /auth mints the HttpOnly `zk` cookie,
 * then the raw socket performs the upgrade with that cookie plus the Origin the
 * gateway demands (§3.2 C3). Resolves once the 101 handshake is verified.
 */
async function connect(port, token) {
  const origin = `http://127.0.0.1:${port}`;
  let authRes;
  try {
    authRes = await fetch(`${origin}/auth`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ token }),
    });
  } catch (error) {
    const cause = error?.cause?.code ?? error?.cause?.message ?? '';
    throw new Error(`POST ${origin}/auth failed${cause ? ` (${cause})` : ''}: is the product running on this port?`);
  }
  if (authRes.status !== 204) {
    throw new Error(`POST /auth -> ${authRes.status} (want 204)`);
  }
  const setCookies = authRes.headers.getSetCookie?.() ?? [];
  const cookie = setCookies.map((value) => value.split(';')[0]).join('; ');
  if (!cookie.includes('zk=')) throw new Error(`POST /auth set no zk cookie: ${setCookies.join(' | ')}`);

  const key = crypto.randomBytes(16).toString('base64');
  const socket = net.connect(port, '127.0.0.1');
  const pending = new Map();
  const lines = [];
  let lineBuffer = '';
  let nextId = 1;
  let closed = false;
  let wsHandshake = null;

  const state = { buffer: Buffer.alloc(0) };

  const onFrame = ({ opcode, payload }) => {
    if (opcode === 0x9) {
      // ping -> pong with the same body
      socket.write(encodeFrame(0xa, payload));
      return;
    }
    if (opcode === 0xa) return; // pong
    if (opcode === 0x8) {
      closed = true;
      for (const [, resolve] of pending) resolve({ error: { message: 'socket closed' } });
      pending.clear();
      return;
    }
    if (opcode === 0x1 || opcode === 0x0) {
      // Server->client frames carry one JSON document per frame and no
      // trailing newline (internal/gateway/client.go writeLoop); the newline
      // rule of §4.1 is about batching several *client* messages into a frame.
      // Handle both: split complete lines, then try the tail as a document.
      lineBuffer += payload.toString('utf8');
      drainLines();
    }
  };

  const dispatch = (message) => {
    lines.push(message);
    if (message.id !== undefined && message.id !== null) {
      const waiter = pending.get(String(message.id));
      if (waiter) {
        pending.delete(String(message.id));
        waiter.resolve(message);
      }
    }
  };

  const drainLines = () => {
    for (;;) {
      const index = lineBuffer.indexOf('\n');
      if (index >= 0) {
        const line = lineBuffer.slice(0, index).trim();
        lineBuffer = lineBuffer.slice(index + 1);
        if (!line) continue;
        try {
          dispatch(JSON.parse(line));
        } catch {
          lines.push({ unparsed: line });
        }
        continue;
      }
      const tail = lineBuffer.trim();
      if (!tail) {
        lineBuffer = '';
        break;
      }
      let message;
      try {
        message = JSON.parse(tail);
      } catch {
        break; // an incomplete document: wait for the rest of the frame
      }
      lineBuffer = '';
      dispatch(message);
    }
  };

  await new Promise((resolve, reject) => {
    socket.once('error', reject);
    socket.once('connect', resolve);
  });

  socket.on('data', (chunk) => {
    if (wsHandshake === null) {
      state.buffer = Buffer.concat([state.buffer, chunk]);
      const end = state.buffer.indexOf('\r\n\r\n');
      if (end < 0) return;
      wsHandshake = state.buffer.subarray(0, end + 4).toString('utf8');
      state.buffer = state.buffer.subarray(end + 4);
      resolveHandshake(wsHandshake);
      if (state.buffer.length > 0) takeFrames(state, onFrame);
      return;
    }
    state.buffer = Buffer.concat([state.buffer, chunk]);
    takeFrames(state, onFrame);
  });
  socket.on('close', () => {
    closed = true;
    for (const [, waiter] of pending) waiter.resolve({ error: { message: 'socket closed' } });
    pending.clear();
  });

  let resolveHandshake;
  let rejectHandshake;
  const handshakeSettled = new Promise((resolve, reject) => {
    resolveHandshake = resolve;
    rejectHandshake = reject;
  });

  socket.write(
    `GET /ws HTTP/1.1\r\n` +
      `Host: 127.0.0.1:${port}\r\n` +
      `Upgrade: websocket\r\n` +
      `Connection: Upgrade\r\n` +
      `Sec-WebSocket-Key: ${key}\r\n` +
      `Sec-WebSocket-Version: 13\r\n` +
      `Origin: ${origin}\r\n` +
      `Cookie: ${cookie}\r\n` +
      `\r\n`,
  );

  const timer = setTimeout(() => rejectHandshake(new Error('websocket handshake timed out')), 10_000);
  try {
    await handshakeSettled;
  } finally {
    clearTimeout(timer);
  }

  const expectedAccept = crypto
    .createHash('sha1')
    .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
    .digest('base64');
  const statusLine = wsHandshake.split('\r\n')[0];
  if (!statusLine.includes('101')) throw new Error(`upgrade refused: ${statusLine}`);
  if (!wsHandshake.includes(expectedAccept)) throw new Error('Sec-WebSocket-Accept mismatch');

  /** call sends one JSON-RPC request and resolves the matching response frame. */
  const call = (method, params, timeoutMs = 15_000) => {
    if (closed) return Promise.resolve({ error: { message: 'socket closed' } });
    const id = nextId++;
    const frame = JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n';
    socket.write(encodeFrame(0x1, frame));
    return new Promise((resolve) => {
      const expire = setTimeout(() => {
        pending.delete(String(id));
        resolve({ __timeout: true, error: { message: `no answer within ${timeoutMs}ms` } });
      }, timeoutMs);
      pending.set(String(id), {
        resolve: (message) => {
          clearTimeout(expire);
          resolve(message);
        },
      });
    });
  };

  const close = () => {
    try {
      socket.write(encodeFrame(0x8, Buffer.alloc(0)));
    } catch {
      /* socket already gone */
    }
    socket.destroy();
  };

  return { call, close, notifications: lines, socket };
}

// ----------------------------------------------------------------- checks --

const results = [];

/** Reports one check. `pass` is the verdict; `raw` is what the product said. */
function report(name, pass, raw) {
  const verdict = pass ? 'PASS' : 'FAIL';
  results.push({ name, pass });
  console.log(`${verdict} ${name} ${raw}`);
}

const clip = (value, max = 700) => {
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  return text.length > max ? `${text.slice(0, max)}…` : text;
};

const REQUIRED_CAPABILITIES = [
  'inbox',
  'triage',
  'tasklease',
  'approvalaction',
  'notifyroute',
  'webhook',
  'lead',
  'credentials',
  'heartbeat',
  'browser',
  'teamnotes',
  'wechatlogin',
  'channeldispatch',
];

const WEBHOOK_SECRET = Buffer.from('verify-secret-0123456789');

function signBody(secret, timestamp, body) {
  const mac = crypto
    .createHmac('sha256', secret)
    .update(`${timestamp}.`)
    .update(body)
    .digest('hex');
  return `t=${timestamp},v1=${mac}`;
}

async function main() {
  const { port, token, dir } = parseArgs(process.argv.slice(2));
  const origin = `http://127.0.0.1:${port}`;
  // The data dir is printed, not checked: the checks assert what the product
  // says, and the reader needs to know which instance said it.
  console.error(`# target ${origin}  data dir ${dir}`);

  let client;
  try {
    client = await connect(port, token);
  } catch (error) {
    report('ws.connect', false, clip(String(error?.message ?? error)));
    return finish();
  }
  const { call, close } = client;

  try {
    // a. the handshake the browser client must be able to complete.
    const init = await call('initialize', { clientName: 'verify-live', clientVersion: '1', protocolVersion: 1 });
    if (init.error) {
      report('initialize.capabilities', false, `error ${clip(init.error)}`);
    } else {
      const capabilities = Array.isArray(init.result?.capabilities) ? init.result.capabilities : [];
      const missing = REQUIRED_CAPABILITIES.filter((name) => !capabilities.includes(name));
      report(
        'initialize.capabilities',
        missing.length === 0,
        `missing=[${missing.join(',')}] capabilities=${clip(capabilities, 400)}`,
      );
    }

    // §4.2: initialize must be the first request, and every other method is
    // answered with not_ready until the client confirms with `ready`. Not a
    // check of its own — it is the mechanical gate the client has to pass.
    const ready = await call('ready', {});
    if (ready.error) {
      report('ready', false, `error ${clip(ready.error)}`);
    }

    // b. approval-category notifications must fall back to the in-app channel.
    const plan = await call('notify.plan', { category: 'approval' });
    if (plan.error) {
      report('notify.plan.approval', false, `error ${clip(plan.error)}`);
    } else {
      const channels = Array.isArray(plan.result?.channels) ? plan.result.channels : [];
      const last = channels[channels.length - 1];
      report(
        'notify.plan.approval',
        last === 'inapp',
        `channels=${clip(channels)} last=${JSON.stringify(last)} deferred=${JSON.stringify(plan.result?.deferred)} reason=${JSON.stringify(plan.result?.reason)}`,
      );
    }

    // c. an unknown task id must be answered honestly, either way. This check
    //    records the answer and only fails on a non-answer (crash, no frame).
    const tools = await call('tasks.tools.get', { id: 'nope' });
    if (tools.error) {
      const typed = typeof tools.error.code === 'number' && tools.error.message;
      report('tasks.tools.get.unknown', Boolean(typed), `typed error ${clip(tools.error)}`);
    } else if (tools.__timeout) {
      report('tasks.tools.get.unknown', false, 'no answer');
    } else {
      const honest = tools.result !== null && typeof tools.result === 'object' && 'tools' in tools.result;
      report('tasks.tools.get.unknown', honest, `result ${clip(tools.result)}`);
    }

    // d. the autonomous loop is actually running with a sane interval.
    const heartbeat = await call('heartbeat.status', {});
    if (heartbeat.error) {
      report('heartbeat.status', false, `error ${clip(heartbeat.error)}`);
    } else {
      const running = heartbeat.result?.running === true;
      const interval = Number(heartbeat.result?.leadIntervalMs ?? 0);
      report(
        'heartbeat.status',
        running && interval > 0,
        `running=${JSON.stringify(heartbeat.result?.running)} leadIntervalMs=${JSON.stringify(heartbeat.result?.leadIntervalMs)} channelIntervalMs=${JSON.stringify(heartbeat.result?.channelIntervalMs)}`,
      );
    }

    // e. the inbox read model answers with an array (empty on a fresh dir).
    const inbox = await call('inbox.snapshot', {});
    if (inbox.error) {
      report('inbox.snapshot', false, `error ${clip(inbox.error)}`);
    } else {
      const items = inbox.result?.items;
      report('inbox.snapshot', Array.isArray(items), `items=${clip(items, 400)} count=${Array.isArray(items) ? items.length : 'n/a'}`);
    }

    // f. a stored credential is listed, and the secret never comes back.
    const set = await call('credentials.set', {
      kind: 'wechat',
      id: 'live',
      fields: { token: 'live-secret' },
    });
    const list = set.error ? { error: { message: 'skipped: set failed' } } : await call('credentials.list', { kind: 'wechat' });
    const raw = JSON.stringify([set, list]);
    if (set.error || list.error) {
      report('credentials.list', false, `set=${clip(set.error ?? set.result)} list=${clip(list.error ?? list.result)}`);
    } else {
      const rows = Array.isArray(list.result?.credentials) ? list.result.credentials : [];
      const found = rows.some((row) => row?.kind === 'wechat' && row?.id === 'live');
      const leaked = raw.includes('live-secret');
      report(
        'credentials.list',
        found && !leaked,
        `set=${clip(set.result)} list=${clip(list.result)} containsLive=${found} leakedSecret=${leaked}`,
      );
    }

    // g. the signed inbound path: real HMAC over "<ts>.<body>", no token.
    const body = JSON.stringify({
      title: 'verify-live',
      body: 'from scripts/verify-live/checks.mjs',
      ref: 'verify',
    });
    const timestamp = Math.floor(Date.now() / 1000);
    const post = (signature) =>
      fetch(`${origin}/api/inbound/verify`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', 'X-Zeokeel-Signature': signature },
        body,
      });

    const good = await post(signBody(WEBHOOK_SECRET, timestamp, body));
    const goodBody = (await good.text()).trim();
    report(
      'inbound.webhook.signed',
      good.status === 202,
      `HTTP ${good.status} (want 202) body=${clip(goodBody, 300)}`,
    );

    // h. the whole M0 chain over the real wire: an inbox row becomes a task, the
    // task is claimed with evidence and accepted, and the note lands in team
    // memory. This is the part that a unit test cannot prove — it is the product,
    // assembled, answering its own protocol.
    const dispatch = await call('inbox.dispatch', { id: 'inbox-000001', subject: 'live chain task', assignee: 'bot-live' });
    const taskId = dispatch.result?.taskIds?.[0] ?? dispatch.result?.id ?? '';
    report('chain.dispatch', Array.isArray(dispatch.result?.taskIds) || Boolean(dispatch.error) === false && Boolean(taskId), `taskIds=${clip(JSON.stringify(dispatch.result?.taskIds ?? dispatch.error ?? null), 200)}`);

    let chainOk = Boolean(taskId);
    let chainDetail = 'no task id';
    if (taskId) {
      const claim = await call('tasks.claim', { id: taskId, owner: 'bot-live' });
      const submit = claim.error ? claim : await call('tasks.submit', {
        id: taskId, owner: 'bot-live', evidence: [{ kind: 'link', ref: 'verify-live://evidence' }],
      });
      const review = submit.error ? submit : await call('tasks.review', {
        id: taskId, decision: 'accept', reviewer: 'verify-live', note: 'live harness',
      });
      chainOk = !claim.error && !submit.error && !review.error;
      chainDetail = claim.error
        ? `claim: ${JSON.stringify(claim.error)}`
        : submit.error
          ? `submit: ${JSON.stringify(submit.error)}`
          : review.error
            ? `review: ${JSON.stringify(review.error)}`
            : `state=${review.result?.state}`;
    }
    report('chain.claim-submit-review', chainOk, clip(chainDetail, 300));

    // i. the accepted task became team memory (acceptance records a note).
    const notes = await call('team.notes', { limit: 10 });
    const noted = Array.isArray(notes.result?.notes) && notes.result.notes.length > 0;
    report('chain.team-note-recorded', noted, `notes=${clip(JSON.stringify(notes.result?.notes ?? notes.error ?? null), 400)}`);

    // j. a task can name the tools it may use, and the binding comes back.
    const bound = taskId
      ? await call('tasks.tools.set', { id: taskId, tools: ['browser_open'] })
      : { error: { message: 'no task' } };
    const readBack = taskId ? await call('tasks.tools.get', { id: taskId }) : { error: { message: 'no task' } };
    const bindingOk = !bound.error && JSON.stringify(readBack.result?.tools ?? []) === JSON.stringify(['browser_open']);
    report('tasks.tools.roundtrip', bindingOk, `set=${clip(JSON.stringify(bound.error ?? 'ok'), 120)} get=${clip(JSON.stringify(readBack.result ?? readBack.error ?? null), 200)}`);

    const wrong = await post(signBody(Buffer.from('verify-secret-wrong'), timestamp, body));
    const wrongBody = (await wrong.text()).trim();
    report(
      'inbound.webhook.bad-signature',
      wrong.status === 401,
      `HTTP ${wrong.status} (want 401) body=${clip(wrongBody, 300)}`,
    );
  } catch (error) {
    report('harness', false, clip(String(error?.stack ?? error), 1200));
  } finally {
    close();
  }

  return finish();
}

function finish() {
  const failed = results.filter((row) => !row.pass);
  console.log(
    `SUMMARY ${results.length - failed.length}/${results.length} passed` +
      (failed.length ? ` failed=[${failed.map((row) => row.name).join(',')}]` : ''),
  );
  process.exit(failed.length === 0 && results.length > 0 ? 0 : 1);
}

await main();