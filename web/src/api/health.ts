/**
 * Runtime status: the Lead's heartbeat and the automation browser
 * (`heartbeat.status`, `heartbeat.tick`, `browser.status` — docs/TRANSPORT.md
 * §5.1).
 *
 * Two properties of this surface shape the screen above it:
 *
 *  1. **It is request/response, not a pushed domain.** None of the three methods
 *     has a snapshot/delta stream, so the screen reads once, and reads again
 *     after its own write (`heartbeat.tick`). A timer would be the only way to
 *     notice a change made elsewhere, and inventing one is the polling loop
 *     AGENTS.md §3 forbids.
 *  2. **A count is a historical fact.** `leadRuns`/`dispatched`/`polls`/
 *     `newMessages` describe the *last pass*, not a live counter, so they are
 *     passed through as reported and never accumulated on this side.
 *
 * Parameters are exactly what the generated contract allows
 * (`src/design/ws-contract.ts`): all three methods take `{}`, which is why no
 * helper here accepts arguments.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/** One heartbeat pass, as the gateway counts it. */
export interface HeartbeatReport {
  /** Lead passes taken in this tick. */
  leadRuns: number;
  /** Tasks/items the Lead dispatched in this tick. */
  dispatched: number;
  /** Channel polls taken in this tick. */
  polls: number;
  /** Messages the polls brought in. */
  newMessages: number;
  /** Failures of the pass, in the gateway's own words. Absent when clean. */
  errors?: string[];
}

/** `heartbeat.status` — the loop's state plus the last pass's report. */
export interface HeartbeatStatus extends HeartbeatReport {
  running: boolean;
  /** Milliseconds between Lead passes (the gateway's configured cadence). */
  leadIntervalMs: number;
  /** Milliseconds between channel polls. */
  channelIntervalMs: number;
  /**
   * RFC 3339 of the last pass. Absent before the first pass — a screen that
   * invented "just now" for a loop that never ran would be lying.
   */
  lastTick?: string;
}

/** `browser.status` — the automation browser, if one is attached. */
export interface BrowserStatus {
  connected: boolean;
  /** The browser's own endpoint (CDP URL). Absent while disconnected. */
  endpoint?: string;
  /** Pages the browser is currently holding. */
  pages: number;
}

/**
 * One WS request, gated like every other bridge in this layer: the
 * fragment→cookie exchange must have run, or the socket's first frame is a
 * guaranteed 401 that looks like a broken feature (see `api/http.ts`).
 */
async function request<T>(method: string): Promise<T> {
  await authorizationReady();
  // All three methods declare no parameters, so an empty object is the whole
  // request — a stray field would be rejected by the same contract table.
  return gatewayRpc().request<T>(method, {});
}

/** A number from the wire, or `0` — a `NaN` in a count row reads as a bug. */
function count(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/** The error strings a pass reported, if any (the DTO omits an empty list). */
function errors(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const rows = value.filter((row): row is string => typeof row === 'string' && row !== '');
  return rows.length > 0 ? rows : undefined;
}

/** Read the loop's status once. No delta will follow — call again to re-read. */
export async function loadHeartbeatStatus(): Promise<HeartbeatStatus> {
  const result = await request<Partial<HeartbeatStatus>>('heartbeat.status');
  const lastTick = typeof result.lastTick === 'string' && result.lastTick !== '' ? result.lastTick : undefined;
  const reported = errors(result.errors);
  return {
    running: result.running === true,
    leadIntervalMs: count(result.leadIntervalMs),
    channelIntervalMs: count(result.channelIntervalMs),
    ...(lastTick ? { lastTick } : {}),
    leadRuns: count(result.leadRuns),
    dispatched: count(result.dispatched),
    polls: count(result.polls),
    newMessages: count(result.newMessages),
    ...(reported ? { errors: reported } : {}),
  };
}

/**
 * Run one pass now (`heartbeat.tick`). The result is this pass's own report;
 * the caller re-reads `heartbeat.status` afterwards so the screen shows the
 * gateway's state, not the response of the write it just made.
 */
export async function tickHeartbeat(): Promise<HeartbeatReport> {
  const result = await request<Partial<HeartbeatReport>>('heartbeat.tick');
  const reported = errors(result.errors);
  return {
    leadRuns: count(result.leadRuns),
    dispatched: count(result.dispatched),
    polls: count(result.polls),
    newMessages: count(result.newMessages),
    ...(reported ? { errors: reported } : {}),
  };
}

/** Read the browser's status once. */
export async function loadBrowserStatus(): Promise<BrowserStatus> {
  const result = await request<Partial<BrowserStatus>>('browser.status');
  const endpoint = typeof result.endpoint === 'string' && result.endpoint !== '' ? result.endpoint : undefined;
  return {
    connected: result.connected === true,
    ...(endpoint ? { endpoint } : {}),
    pages: count(result.pages),
  };
}