/**
 * The inbound work inbox (`inbox.*`, docs/TRANSPORT.md §5.1.13).
 *
 * Every channel (WeChat, DingTalk, Feishu, email, webhooks, cron, a teammate's
 * proposal) lands in the gateway's inbox first, and only the Lead turns an item
 * into work. This module is the client's only reader/writer of that queue.
 *
 * It is a **request/response** surface, not a pushed domain: there is no
 * `inbox.snapshot`/`inbox.delta` stream, so the screen reads once and re-reads
 * after its own write. A poll would be the only way to notice someone else's
 * change, and the screen does not invent one (AGENTS.md §3: no polling loops).
 *
 * Parameters are exactly the ones the generated contract allows
 * (`src/design/ws-contract.ts` — `inbox.dispatch` requires `{id, subject}`,
 * `inbox.triage` requires `{id, action}`); the mock and the fake gateway
 * validate with that same table, so a stray field fails here instead of on the
 * wire.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/** The five states the backend reports. Unknown values stay on the wire as-is. */
export type InboxStatus = 'new' | 'triaged' | 'task' | 'ignored' | 'archived';

export const INBOX_STATUSES: readonly InboxStatus[] = [
  'new',
  'triaged',
  'task',
  'ignored',
  'archived',
];

/**
 * Everything, spelled out.
 *
 * `inbox.snapshot`'s zero filter means "still actionable" (`new` + `triaged`
 * only, contracts.InboxFilter), so a screen that also shows decided rows must
 * name the statuses it wants — otherwise the "已分诊" half is empty for a reason
 * nobody can see in the UI.
 */
export const ALL_INBOX_STATUSES: readonly string[] = INBOX_STATUSES;

/** One inbound event, normalized across channels. `body` is data, never instructions. */
export interface InboxItem {
  /** Gateway-assigned identity, stable across merges of the same event. */
  id: string;
  /** Channel family: wechat, dingtalk, feishu, email, webhook, cron, teammate. */
  source: string;
  /** Platform-side conversation identity (chat id, thread, repo, mailbox). */
  channel?: string;
  /** Platform-side sender identity. */
  sender?: string;
  /** One line a human can scan. */
  title: string;
  body?: string;
  /** Original link/id for jumping back to the platform. */
  ref?: string;
  /** First arrival — merges keep it. RFC 3339. */
  receivedAt?: string;
  /** Latest arrival — merges update it. RFC 3339. */
  lastSeenAt?: string;
  /** Past this instant the item is stale and hidden from the default list. */
  expiresAt?: string;
  /**
   * One of {@link INBOX_STATUSES}. Kept as a plain string because the client
   * must not drop a row whose status it does not recognise yet — see
   * `features/inbox/state.ts` for how an unknown value is grouped.
   */
  status: string;
  /** How many arrivals this item represents (merges increment it). */
  count: number;
  /** Tasks the Lead created or appended from this item. */
  taskIds?: string[];
}

/** The "N waiting" numbers. */
export interface InboxStats {
  total: number;
  new: number;
  triaged: number;
}

export interface InboxSnapshot {
  items: InboxItem[];
  stats: InboxStats;
}

/** `inbox.snapshot`'s filter. All fields are optional on the wire. */
export interface InboxFilter {
  statuses?: readonly string[];
  source?: string;
  limit?: number;
  includeExpired?: boolean;
}

export interface DispatchInboxInput {
  /** The item to turn into work. */
  id: string;
  /** The task subject — what the Work board will show. */
  subject: string;
  /** Optional teammate/owner to assign it to. */
  assignee?: string;
}

export interface DispatchInboxResult {
  taskIds: string[];
  /** False when the subject matched an open task and the item was appended. */
  created: boolean;
  /** The item after triage. Absent only if the gateway omitted it. */
  item?: InboxItem;
}

/** `inbox.triage`'s vocabulary (contracts.InboxAction*). */
export type TriageAction = 'triaged' | 'task' | 'ignored' | 'archived';

export interface TriageInboxInput {
  id: string;
  action: TriageAction;
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

/** Read the queue once. No delta will follow — call again after a write. */
export async function loadInboxSnapshot(filter: InboxFilter = {}): Promise<InboxSnapshot> {
  const params: Record<string, unknown> = {};
  if (filter.statuses && filter.statuses.length > 0) params.statuses = [...filter.statuses];
  if (filter.source) params.source = filter.source;
  if (filter.limit !== undefined) params.limit = filter.limit;
  if (filter.includeExpired !== undefined) params.includeExpired = filter.includeExpired;

  const result = await request<{ items?: InboxItem[]; stats?: Partial<InboxStats> }>(
    'inbox.snapshot',
    params,
  );
  // The DTO always carries both halves; the fallbacks only keep a malformed
  // answer from rendering `undefined` in the list or `NaN` in a count.
  return {
    items: result.items ?? [],
    stats: {
      total: result.stats?.total ?? 0,
      new: result.stats?.new ?? 0,
      triaged: result.stats?.triaged ?? 0,
    },
  };
}

/** Turn one item into a task (`inbox.dispatch`, not idempotent). */
export async function dispatchInboxItem(input: DispatchInboxInput): Promise<DispatchInboxResult> {
  const params: Record<string, unknown> = { id: input.id, subject: input.subject };
  if (input.assignee) params.assignee = input.assignee;

  const result = await request<{
    taskIds?: string[];
    created?: boolean;
    item?: InboxItem;
  }>('inbox.dispatch', params);
  return {
    taskIds: result.taskIds ?? [],
    created: result.created ?? false,
    ...(result.item ? { item: result.item } : {}),
  };
}

/** Record a decision without creating a task (`inbox.triage`) — e.g. 忽略. */
export async function triageInboxItem(input: TriageInboxInput): Promise<InboxItem> {
  return request<InboxItem>('inbox.triage', { id: input.id, action: input.action });
}