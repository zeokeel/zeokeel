/**
 * Channel configuration: credential identities, webhook sources and the
 * notification router (docs/TRANSPORT.md §5.1 — `credentials.*`,
 * `webhook.source*`, `notify.route.*`).
 *
 * Two properties of this surface shape the screen above it:
 *
 *  1. **Secrets are write-only.** `credentials.set` and `webhook.source.set`
 *     take the secret in the request body; no read ever hands it back —
 *     `credentials.list` returns identities (`{kind,id}`) and
 *     `webhook.sources` returns `{id,channel?}`. A UI that could show a stored
 *     secret would be showing something this layer never received.
 *  2. **There is no pushed domain.** None of these methods has a snapshot/
 *     delta stream, so a screen reads once and reads again after its own
 *     write. A poll would be the only way to notice another client's change,
 *     and inventing one is the polling loop AGENTS.md §3 forbids.
 *
 * Parameters are exactly the ones the generated contract allows
 * (`src/design/ws-contract.ts`); optional values are omitted rather than sent
 * as empty strings, because "absent" and "cleared" are different requests.
 *
 * The one exception is `wechat.login.*`: a QR login is a **human-in-the-loop**
 * flow, so the pair is start-then-poll by design (the gateway answers one
 * attempt per call and never holds a request open). That poll is bounded — the
 * screen stops after a fixed number of tries — so it is not the open-ended
 * polling loop AGENTS.md §3 forbids.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/** The four channel families a credential can belong to. */
export type CredentialKind = 'wechat' | 'dingtalk' | 'feishu' | 'email';

export const CREDENTIAL_KINDS: readonly CredentialKind[] = [
  'wechat',
  'dingtalk',
  'feishu',
  'email',
];

/**
 * The channels a notification route can name. These are protocol values, not
 * copy: the Select shows them verbatim for the same reason the inbox shows a
 * row's `source` verbatim (README hard constraint: protocol values never
 * become UI strings).
 */
export const NOTIFY_CHANNELS: readonly string[] = [
  'inapp',
  'system',
  'wechat',
  'dingtalk',
  'feishu',
  'email',
];

/** The field keys a credential bundle may carry (see `Credential.field` in Go). */
export type CredentialFieldName =
  | 'token'
  | 'robotCode'
  | 'appId'
  | 'appSecret'
  | 'username'
  | 'password';

/**
 * The `fields` keys each kind's adapter reads.
 *
 * The store validates the *shape* (`fields` is a non-empty map of bounded
 * strings), not a per-kind schema, so this table is the client's half of the
 * contract: the form sends exactly these names under `credentials.set`, and a
 * missing one is a credential the adapter cannot use.
 */
export const CREDENTIAL_FIELD_NAMES: Record<CredentialKind, readonly CredentialFieldName[]> = {
  wechat: ['token'],
  // DingTalk is outbound only and needs TWO values: the robot access token and
  // the robot code. The platform's send body always carries `robotCode`
  // (`internal/modules/dingtalk/adapter.go`), so a row with only a token is a
  // credential no send can use — the request goes out with an empty robotCode
  // and comes back 400. Both are therefore form fields, and a half-filled form
  // is not saveable. The robot code is an identity, not a secret (the form shows
  // it in clear); no `signSecret` field exists, that belonged to the retired
  // callback signature, which no adapter reads any more.
  dingtalk: ['token', 'robotCode'],
  feishu: ['appId', 'appSecret'],
  email: ['username', 'password'],
};

/** One stored credential, as the list reports it — identity only, never a value. */
export interface CredentialInfo {
  kind: string;
  id: string;
}

/** One inbound webhook source. `secret` is not part of any read (TRANSPORT §5.1). */
export interface WebhookSourceInfo {
  id: string;
  channel?: string;
}

/** One category → channel route, exactly as the router holds it. */
export interface NotifyRoute {
  category: string;
  primary: string;
  fallback?: string;
  quietHours?: string;
  escalationMs?: number;
  enabled: boolean;
}

/** The credential payload: field name → secret value, per kind. */
export type CredentialFields = Record<string, string>;

export interface CredentialSetInput {
  kind: string;
  id: string;
  fields: CredentialFields;
}

export interface CredentialDeleteInput {
  kind: string;
  id: string;
}

export interface WebhookSourceSetInput {
  id: string;
  secret: string;
  channel?: string;
}

export interface NotifyRouteInput {
  category: string;
  primary: string;
  fallback?: string;
  quietHours?: string;
  escalationMs?: number;
  enabled?: boolean;
}

/**
 * One WS request, gated like every other bridge in this layer: the
 * fragment→cookie exchange must have run, or the socket's first frame is a
 * guaranteed 401 that looks like a broken feature (see `api/http.ts`).
 */
async function request<T>(method: string, params: Record<string, unknown>): Promise<T> {
  await authorizationReady();
  return gatewayRpc().request<T>(method, params);
}

/** Trimmed non-empty value, or `undefined` so the key is omitted from the request. */
function optional(value: string | undefined): string | undefined {
  if (value === undefined) return undefined;
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

/** Identities for one kind (`credentials.list`), or every kind when omitted. */
export async function listCredentials(kind?: string): Promise<CredentialInfo[]> {
  const params: Record<string, unknown> = {};
  if (kind) params.kind = kind;
  const result = await request<{ credentials?: Array<Partial<CredentialInfo>> }>(
    'credentials.list',
    params,
  );
  // The wire type has no values to drop: a row is `{kind,id}` by construction.
  return (result.credentials ?? []).map((row) => ({
    kind: String(row.kind ?? ''),
    id: String(row.id ?? ''),
  }));
}

/** Store one credential (`credentials.set`). The secret lives only in this call. */
export async function setCredential(input: CredentialSetInput): Promise<void> {
  await request<unknown>('credentials.set', {
    kind: input.kind,
    id: input.id,
    fields: { ...input.fields },
  });
}

/** Remove one credential identity (`credentials.delete`). */
export async function deleteCredential(input: CredentialDeleteInput): Promise<void> {
  await request<unknown>('credentials.delete', { kind: input.kind, id: input.id });
}

/** Registered webhook sources (`webhook.sources`) — id/channel only. */
export async function listWebhookSources(): Promise<WebhookSourceInfo[]> {
  const result = await request<{ sources?: Array<Partial<WebhookSourceInfo>> }>(
    'webhook.sources',
    {},
  );
  return (result.sources ?? []).map((row) => ({
    id: String(row.id ?? ''),
    ...(row.channel ? { channel: String(row.channel) } : {}),
  }));
}

/** Register or re-key one source (`webhook.source.set`). */
export async function setWebhookSource(input: WebhookSourceSetInput): Promise<void> {
  const params: Record<string, unknown> = { id: input.id, secret: input.secret };
  const channel = optional(input.channel);
  if (channel !== undefined) params.channel = channel;
  await request<unknown>('webhook.source.set', params);
}

/** Forget one source (`webhook.source.remove`). */
export async function removeWebhookSource(id: string): Promise<void> {
  await request<unknown>('webhook.source.remove', { id });
}

/** Normalize one wire route; `enabled` is true unless the gateway says otherwise. */
function normalizeRoute(row: Partial<NotifyRoute>): NotifyRoute {
  return {
    category: String(row.category ?? ''),
    primary: String(row.primary ?? ''),
    enabled: row.enabled !== false,
    ...(row.fallback ? { fallback: String(row.fallback) } : {}),
    ...(row.quietHours ? { quietHours: String(row.quietHours) } : {}),
    ...(typeof row.escalationMs === 'number' ? { escalationMs: row.escalationMs } : {}),
  };
}

/** Every category the router knows (`notify.route.snapshot`). */
export async function loadNotifyRoutes(): Promise<NotifyRoute[]> {
  const result = await request<{ routes?: Array<Partial<NotifyRoute>> }>(
    'notify.route.snapshot',
    {},
  );
  return (result.routes ?? []).map(normalizeRoute);
}

/** Write one category's route (`notify.route.upsert`), returning the stored row. */
export async function upsertNotifyRoute(input: NotifyRouteInput): Promise<NotifyRoute> {
  const params: Record<string, unknown> = {
    category: input.category,
    primary: input.primary,
  };
  const fallback = optional(input.fallback);
  if (fallback !== undefined) params.fallback = fallback;
  const quietHours = optional(input.quietHours);
  if (quietHours !== undefined) params.quietHours = quietHours;
  if (input.escalationMs !== undefined) params.escalationMs = input.escalationMs;
  if (input.enabled !== undefined) params.enabled = input.enabled;

  const result = await request<Partial<NotifyRoute>>('notify.route.upsert', params);
  // The category and primary are ours; the rest is the router's answer.
  return normalizeRoute({ ...input, ...result, category: input.category, primary: input.primary });
}

/* --------------------------------------------------------- wechat QR login -- */

/**
 * The lifecycle of one QR-login attempt (`contracts.WeChatLoginState`).
 *
 * These are **protocol values**, not copy: the block compares them and shows
 * its own words. An unknown value is not an error — it keeps polling, because a
 * gateway that learns a new intermediate state must not freeze this screen.
 */
export const WECHAT_LOGIN_STATES = ['pending', 'waiting', 'confirmed', 'expired'] as const;

export type WechatLoginStateName = (typeof WECHAT_LOGIN_STATES)[number];

/**
 * One QR-login attempt (`wechat.login.start`: `{}` → `{qrId,qrUrl,expiresAt?}`).
 *
 * The flow is two calls on purpose: a settings page must first render the code
 * and then poll what the human did with it — a blocking call would hold one
 * request open for minutes. `qrUrl` is what the code encodes; without a QR
 * renderer the page shows it as a scannable link.
 */
export interface WechatLoginStart {
  qrId: string;
  qrUrl: string;
  expiresAt?: string;
}

/** What `wechat.login.state` reports for one `qrId` (`{qrId}` → `{state,connected}`). */
export interface WechatLoginState {
  /** One of `WECHAT_LOGIN_STATES`; unknown values pass through verbatim. */
  state: string;
  /** Whether the account is logged in — the adapter's own answer, not `state`'s. */
  connected: boolean;
  expiresAt?: string;
}

/**
 * Ask the platform for a fresh QR code (`wechat.login.start`).
 *
 * A reply with no `qrId`/`qrUrl` cannot be rendered *or* polled, so it fails
 * here instead of leaving the screen waiting on an attempt that does not exist.
 */
export async function startWechatLogin(): Promise<WechatLoginStart> {
  const result = await request<{ qrId?: string; qrUrl?: string; expiresAt?: string }>(
    'wechat.login.start',
    {},
  );
  const qrId = String(result.qrId ?? '');
  const qrUrl = String(result.qrUrl ?? '');
  if (qrId === '' || qrUrl === '') {
    throw new Error('wechat.login.start: the gateway returned no QR code');
  }
  return {
    qrId,
    qrUrl,
    ...(result.expiresAt ? { expiresAt: String(result.expiresAt) } : {}),
  };
}

/**
 * Poll one attempt (`wechat.login.state`).
 *
 * `expired` is an **answer, not a failure**: the gateway reports it as a state
 * so a polling loop can stop and offer a new code instead of showing an error.
 */
export async function pollWechatLoginState(qrId: string): Promise<WechatLoginState> {
  const result = await request<{ state?: string; connected?: boolean; expiresAt?: string }>(
    'wechat.login.state',
    { qrId },
  );
  return {
    state: String(result.state ?? ''),
    connected: result.connected === true,
    ...(result.expiresAt ? { expiresAt: String(result.expiresAt) } : {}),
  };
}