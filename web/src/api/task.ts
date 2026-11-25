/**
 * One task on the work board: the read plus the claim/review verbs
 * (`tasks.*`, docs/TRANSPORT.md §5.1.8 and §5.1.13).
 *
 * Two halves live here because a task detail screen needs both:
 *
 *   read    `tasks.snapshot {sessionId?}` → the board. There is **no per-task
 *           read method**, so the screen picks the row it was opened for out of
 *           the board it fetched (see `features/task/TaskDetailScreen.tsx`).
 *   write   `tasks.claim` / `tasks.heartbeat` / `tasks.submit` / `tasks.review`
 *           — the claim layer above the board, each one answering with the live
 *           `TaskLease`.
 *
 * **The lease is only readable through a write.** The evidence a task carries
 * and the reviewer's note arrive on the `TaskLease` those four methods return;
 * `tasks.snapshot` never contains them. So a screen that has just been opened
 * knows the board row and nothing else, and must not render an empty "证据"
 * section as if it had checked — this module does not invent a read for it.
 *
 * Everything here is request/response: no delta follows, so the screen reads
 * once and re-reads after its own write (`inbox.ts` has the same shape).
 *
 * Parameters are exactly the ones the generated contract allows
 * (`src/design/ws-contract.ts`: `tasks.claim`/`tasks.heartbeat` take
 * `{id,owner}`, `tasks.submit` takes `{id,owner,evidence}`, `tasks.review` takes
 * `{id,decision,reviewer,note?}`), so a stray field fails here instead of on the
 * wire.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/** The four states a lease can be in (`contracts.LeaseState*`). */
export const LEASE_STATES = ['claimed', 'review', 'done', 'rejected'] as const;

/** The four kinds of proof a submit may carry (`contracts.Evidence.Kind`). */
export const EVIDENCE_KINDS = ['file', 'link', 'output', 'screenshot'] as const;

/**
 * One board row, normalized.
 *
 * `status` stays a plain string on purpose: an unknown protocol status must not
 * make the row disappear, and the screen — not this layer — decides which of
 * the six human status words it becomes (`features/task/copy.ts`).
 */
export interface BoardTask {
  /** Gateway-assigned id; the `/t/:taskId` segment. */
  id: string;
  /** One line a human can scan. */
  subject: string;
  /** The goal in prose, when the dispatcher wrote one. */
  description?: string;
  /** Protocol status: pending / in_progress / completed (kept raw, see above). */
  status: string;
  /** Current owner, when the board has one. */
  owner?: string;
  /** Tasks that must finish first. */
  blockedBy?: string[];
  /** Files this task is allowed to write — the dispatch-time promise. */
  writeScopes?: string[];
}

/** One attached proof. `ref` is a path, URL or command-output reference. */
export interface TaskEvidence {
  /** One of {@link EVIDENCE_KINDS}; kept raw for the same reason as `status`. */
  kind: string;
  ref: string;
  /** One line of context. */
  note?: string;
}

/** The live claim on one board task (`contracts.TaskLease`). */
export interface TaskLease {
  taskId: string;
  owner: string;
  /** One of {@link LEASE_STATES}; kept raw (unknown values stay visible). */
  state: string;
  /** When the claim expires without a heartbeat. RFC 3339. */
  until?: string;
  /** The last time the owner proved it was alive. RFC 3339. */
  heartbeat?: string;
  /** What the owner submitted; kept through accept/reject. */
  evidence?: TaskEvidence[];
  /** The reviewer's reason on accept/reject. */
  reviewNote?: string;
}

/** Rows as they arrive on the wire (`omitempty` on every optional field). */
interface BoardTaskWire {
  id?: string;
  subject?: string;
  description?: string;
  status?: string;
  owner?: string;
  blockedBy?: string[];
  writeScopes?: string[];
}

interface LeaseWire {
  taskId?: string;
  owner?: string;
  state?: string;
  until?: string;
  heartbeat?: string;
  evidence?: Array<{ kind?: string; ref?: string; note?: string }>;
  reviewNote?: string;
}

/** Who a claim/submit acts as. */
export interface LeaseTarget {
  id: string;
  owner: string;
}

export interface SubmitTaskInput extends LeaseTarget {
  evidence: readonly TaskEvidence[];
}

/** `tasks.review`'s vocabulary. */
export type ReviewDecision = 'accept' | 'reject';

export interface ReviewTaskInput {
  id: string;
  decision: ReviewDecision;
  reviewer: string;
  /** Required by the screen for a reject; the gateway keeps it as the note. */
  note?: string;
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

/**
 * A row without an id is dropped: it cannot be claimed, reviewed or linked to,
 * so keeping it would only make the screen show work nobody can act on.
 */
function boardTaskFromWire(row: BoardTaskWire): BoardTask | null {
  if (!row.id) return null;
  return {
    id: row.id,
    // The subject is what the row is *called*; falling back to the id keeps a
    // nameless row clickable instead of blank.
    subject: row.subject || row.id,
    status: row.status || 'pending',
    ...(row.description ? { description: row.description } : {}),
    ...(row.owner ? { owner: row.owner } : {}),
    ...(row.blockedBy && row.blockedBy.length > 0 ? { blockedBy: row.blockedBy } : {}),
    ...(row.writeScopes && row.writeScopes.length > 0 ? { writeScopes: row.writeScopes } : {}),
  };
}

/**
 * A lease answer, with the request's own identity as the fallback: a gateway
 * that omits `taskId`/`owner` still produced a lease for *this* task and *this*
 * owner, and rendering `undefined` in the timeline helps nobody.
 */
function leaseFromWire(row: LeaseWire, fallback: { id: string; owner: string }): TaskLease {
  const evidence = (row.evidence ?? [])
    .filter((item) => Boolean(item.ref))
    .map((item) => ({
      kind: item.kind || 'file',
      ref: item.ref as string,
      ...(item.note ? { note: item.note } : {}),
    }));
  return {
    taskId: row.taskId || fallback.id,
    owner: row.owner || fallback.owner,
    state: row.state || '',
    ...(row.until ? { until: row.until } : {}),
    ...(row.heartbeat ? { heartbeat: row.heartbeat } : {}),
    ...(evidence.length > 0 ? { evidence } : {}),
    ...(row.reviewNote ? { reviewNote: row.reviewNote } : {}),
  };
}

/**
 * Read the board once (`tasks.snapshot`). Omitting `sessionId` reads the
 * process-wide board, which is the one `/t/:taskId` links into.
 *
 * No delta will follow; call again after a write.
 */
export async function loadTaskBoard(sessionId?: string): Promise<BoardTask[]> {
  const params: Record<string, unknown> = {};
  if (sessionId) params.sessionId = sessionId;

  const result = await request<{ tasks?: BoardTaskWire[] }>('tasks.snapshot', params);
  const tasks: BoardTask[] = [];
  for (const row of result.tasks ?? []) {
    const task = boardTaskFromWire(row);
    if (task) tasks.push(task);
  }
  return tasks;
}

/** Take the task for `owner` (`tasks.claim`, not idempotent). */
export async function claimTask(target: LeaseTarget): Promise<TaskLease> {
  const result = await request<LeaseWire>('tasks.claim', {
    id: target.id,
    owner: target.owner,
  });
  return leaseFromWire(result, target);
}

/** Extend a claim the caller still owns (`tasks.heartbeat`, idempotent). */
export async function heartbeatTask(target: LeaseTarget): Promise<TaskLease> {
  const result = await request<LeaseWire>('tasks.heartbeat', {
    id: target.id,
    owner: target.owner,
  });
  return leaseFromWire(result, target);
}

/**
 * Attach evidence and move the task to review (`tasks.submit`, not idempotent).
 *
 * The gateway answers `1010 evidence_required` for an empty list, so the screen
 * must never call this with nothing — the api layer stays faithful and does not
 * silently pad the list.
 */
export async function submitTask(input: SubmitTaskInput): Promise<TaskLease> {
  const result = await request<LeaseWire>('tasks.submit', {
    id: input.id,
    owner: input.owner,
    evidence: input.evidence.map((row) => ({
      kind: row.kind,
      ref: row.ref,
      ...(row.note ? { note: row.note } : {}),
    })),
  });
  return leaseFromWire(result, { id: input.id, owner: input.owner });
}

/** Accept (done) or reject (back for another attempt) one submission. */
export async function reviewTask(input: ReviewTaskInput): Promise<TaskLease> {
  const params: Record<string, unknown> = {
    id: input.id,
    decision: input.decision,
    reviewer: input.reviewer,
  };
  // Omitted rather than sent as `''`: an empty note is "no reason given", and
  // `reject` requires a real one before the request is made.
  if (input.note) params.note = input.note;

  const result = await request<LeaseWire>('tasks.review', params);
  return leaseFromWire(result, { id: input.id, owner: input.reviewer });
}