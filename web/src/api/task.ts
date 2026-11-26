/**
 * One task on the work board: the read plus the claim/review verbs
 * (`tasks.*`, docs/TRANSPORT.md §5.1.8 and §5.1.13).
 *
 * Two reads and one write live here because a task detail screen needs all
 * three:
 *
 *   read    `tasks.snapshot {sessionId?}` → the board. There is **no per-task
 *           read method**, so the screen picks the row it was opened for out of
 *           the board it fetched (see `features/task/TaskDetailScreen.tsx`).
 *   read    `tasks.lease {id}` → the live `TaskLease`, or `null` when nobody
 *           holds the task. Its failure is *not* the task's failure: a task you
 *           can see but whose proof did not load is still a task.
 *   write   `tasks.claim` / `tasks.heartbeat` / `tasks.submit` / `tasks.review`
 *           — the claim layer above the board, each one answering with the live
 *           `TaskLease`.
 *
 * **The lease has its own read** (`tasks.lease`): the evidence a task carries
 * and the reviewer's note live on the `TaskLease`, never on the board row, so a
 * screen that has just been opened reads both — the row from the board and the
 * proof from the lease — instead of waiting for its first write to see anything.
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
  /**
   * When the gateway created / last touched the row, RFC 3339. The board screen
   * renders "2 分钟前" from these (`features/team/TeamScreen.tsx`); a board with
   * no time is a board whose rows cannot be told apart after a day.
   *
   * Added for the team board (R26) — additive, both optional, so every existing
   * caller is unaffected.
   */
  createdAt?: string;
  updatedAt?: string;
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

/**
 * One recorded review decision (`contracts.TaskReview`, `tasks.reviews`).
 *
 * This is the durable half of a review: `tasks.review` deletes the lease it
 * describes, so `tasks.lease` answers `null` exactly when the audit trail
 * starts mattering. `verdict` is the machine-readable outcome (`pass`/`fail`)
 * and `decision` the verb, so a screen never has to infer one from the other.
 */
export interface TaskReview {
  taskId: string;
  reviewer: string;
  /** The reviewer's role (a preset id such as `lead` / `auditor`), when it has one. */
  role?: string;
  /** `accept` / `reject`, kept raw for the same reason as the lease state. */
  decision: string;
  /** `pass` / `fail`. */
  verdict: string;
  /** The reviewer's note (the reject reason, or an accept note). */
  findings?: string;
  /** RFC 3339. */
  at?: string;
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
  createdAt?: string;
  updatedAt?: string;
}

interface ReviewWire {
  taskId?: string;
  reviewer?: string;
  role?: string;
  decision?: string;
  verdict?: string;
  findings?: string;
  at?: string;
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

/**
 * Reading a lease is read-only, so the only parameter is the task.
 *
 * `docs/TRANSPORT.md` §5.1.13 and the generated table both say `{id}` for
 * `tasks.lease` (the read has its own `leaseReadParams` on the Go side); unlike
 * claim/submit/review there is no identity in play, so none is sent.
 */
export interface LeaseReadInput {
  id: string;
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
    // Kept as the wire's own RFC 3339 strings: the board measures them against
    // the read's clock, and a `Date` here would freeze the timezone choice.
    ...(row.createdAt ? { createdAt: row.createdAt } : {}),
    ...(row.updatedAt ? { updatedAt: row.updatedAt } : {}),
  };
}

/**
 * A lease answer, with the request's own identity as the fallback: a gateway
 * that omits `taskId` (or `owner`) still answered about *this* task, and
 * rendering `undefined` in the timeline helps nobody.
 */
function leaseFromWire(row: LeaseWire, fallback: { id: string; owner?: string }): TaskLease {
  const evidence = (row.evidence ?? [])
    .filter((item) => Boolean(item.ref))
    .map((item) => ({
      kind: item.kind || 'file',
      ref: item.ref as string,
      ...(item.note ? { note: item.note } : {}),
    }));
  return {
    taskId: row.taskId || fallback.id,
    owner: row.owner || fallback.owner || '',
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

/**
 * Read the live lease for one task (`tasks.lease`).
 *
 * Two wire shapes are legal and both mean the same thing here: a bare
 * `TaskLease`, or `{lease: null}` when nobody holds the task. `null` is an
 * answer — "no claim, no evidence" — and must not be turned into an error;
 * a thrown error means the read itself failed.
 */
export async function loadTaskLease(input: LeaseReadInput): Promise<TaskLease | null> {
  const result = await request<LeaseWire & { lease?: LeaseWire | null }>('tasks.lease', {
    id: input.id,
  });
  const row = Object.prototype.hasOwnProperty.call(result, 'lease') ? result.lease : result;
  if (!row) return null;
  return leaseFromWire(row, { id: input.id });
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

/**
 * Read the recorded review decisions of one task (`tasks.reviews`).
 *
 * Oldest first. An empty array is an answer ("never reviewed"); a thrown error
 * means the read itself failed. This is a separate read from `tasks.lease`
 * because the two cannot answer the same question: the lease is deleted by the
 * very decision this list records.
 */
export async function loadTaskReviews(input: LeaseReadInput): Promise<TaskReview[]> {
  const result = await request<{ reviews?: ReviewWire[] | null }>('tasks.reviews', {
    id: input.id,
  });
  const reviews: TaskReview[] = [];
  for (const row of result.reviews ?? []) {
    if (!row.decision || !row.verdict) continue;
    reviews.push({
      taskId: row.taskId || input.id,
      reviewer: row.reviewer || '',
      decision: row.decision,
      verdict: row.verdict,
      ...(row.role ? { role: row.role } : {}),
      ...(row.findings ? { findings: row.findings } : {}),
      ...(row.at ? { at: row.at } : {}),
    });
  }
  return reviews;
}

/**
 * The tool binding of one task (`contracts.TaskTools`, M5 execution policy).
 *
 * A lease says *who* is working on a task; this says *what that work may touch*.
 * They are separate records on purpose (a task can be released and re-claimed
 * without losing its policy), which is why the screen reads both.
 *
 * `tools` is absent, never `[]`, in a real answer: the gateway deletes a binding
 * rather than storing an empty one, and a read of a task with no binding answers
 * `{tools:null}` — the same absent shape as `tasks.lease`'s `{lease:null}`, so
 * this layer has one "nothing there" case.
 */
export interface TaskTools {
  taskId: string;
  /** Model-visible tool names the owner may run. Absent means "no binding". */
  tools?: string[];
  /** When the policy was last written, RFC 3339. */
  updatedAt?: string;
}

/** Rows as they arrive on the wire: `tools` is null when nothing is bound. */
interface TaskToolsWire {
  taskId?: string;
  tools?: string[] | null;
  updatedAt?: string;
}

export interface TaskToolsSetInput {
  id: string;
  /** The full policy. `[]` clears it — the gateway never merges two writes. */
  tools: readonly string[];
}

/**
 * A binding, with the requested id as the fallback: a gateway that omits
 * `taskId` still answered about *this* task, and the screen keys its state on it.
 *
 * Blank names are dropped: they are not tool names, they cannot be run, and a
 * checkbox for "" would be a checkbox nobody can act on. Everything else is kept
 * verbatim and in the order the gateway stored it, so a rebind that changes
 * nothing writes back exactly what was read.
 */
function taskToolsFromWire(row: TaskToolsWire, fallbackId: string): TaskTools {
  const tools = (row.tools ?? []).filter((name) => typeof name === 'string' && name.trim() !== '');
  const binding: TaskTools = { taskId: row.taskId || fallbackId };
  if (tools.length > 0) binding.tools = tools;
  if (row.updatedAt) binding.updatedAt = row.updatedAt;
  return binding;
}

/**
 * Read the policy a task was dispatched with (`tasks.tools.get`).
 *
 * `null` is an answer — "no binding" — and must not be turned into an error;
 * a thrown error means the read itself failed. Both the absent shape the gateway
 * documents (`{tools:null}`) and an empty list normalize to `null`, because a
 * zero-item policy means exactly what no policy means: nothing is restricted.
 */
export async function loadTaskTools(id: string): Promise<TaskTools | null> {
  const result = await request<TaskToolsWire>('tasks.tools.get', { id });
  const binding = taskToolsFromWire(result, id);
  return binding.tools && binding.tools.length > 0 ? binding : null;
}

/**
 * Replace the task's policy (`tasks.tools.set`, not idempotent in intent: the
 * previous set is replaced, never merged).
 *
 * `tools` is always sent, and a clear is an explicit `[]` rather than a missing
 * field: the gateway rejects a missing/null list with `1010` so that a policy can
 * never be removed by a malformed request.
 */
export async function setTaskTools(input: TaskToolsSetInput): Promise<TaskTools> {
  const result = await request<TaskToolsWire>('tasks.tools.set', {
    id: input.id,
    tools: [...input.tools],
  });
  return taskToolsFromWire(result, input.id);
}

/**
 * The per-task cost ceiling (`contracts.TaskBudget`, G11).
 *
 * A bound session reports real token usage and the task stops when it crosses
 * this ceiling, so the three numbers here are the whole story a person needs:
 * how much was spent, what the limit was, and whether the stop has happened.
 *
 * `maxTokens` / `maxMinutes` are `0` for "unlimited" — the gateway does not use
 * `omitempty` on them precisely so this layer can tell that from a missing
 * field, and this layer keeps that meaning verbatim instead of guessing a
 * ceiling the gateway never set.
 */
export interface TaskBudget {
  taskId: string;
  /** Token ceiling. `0` means unlimited. */
  maxTokens: number;
  /** Wall-clock ceiling in minutes, measured from {@link startedAt}. `0` means unlimited. */
  maxMinutes: number;
  /** What the accounting source has reported so far. Never derived here. */
  usedTokens: number;
  /** When the first usage was reported — the clock `maxMinutes` runs on. */
  startedAt?: string;
  /** The stop itself. Sticky until somebody re-budgets the task. */
  exhausted: boolean;
}

/** Rows as they arrive on the wire: `exhausted` is always present, the limits are 0 when unset. */
interface TaskBudgetWire {
  taskId?: string;
  maxTokens?: number;
  maxMinutes?: number;
  usedTokens?: number;
  startedAt?: string;
  exhausted?: boolean;
}

/** A count the wire claims to carry; anything else is 0 rather than `NaN` on screen. */
function count(value: number | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

/**
 * A budget row, with the requested id as the fallback: a gateway that omits
 * `taskId` still answered about *this* task, and the screen keys its render on it.
 *
 * `exhausted` is `true` only when the gateway says so: a missing field is "no
 * stop", never an inferred one — a task that was stopped and an older gateway
 * that never reported stops must not look the same.
 */
function taskBudgetFromWire(row: TaskBudgetWire, fallbackId: string): TaskBudget {
  const budget: TaskBudget = {
    taskId: row.taskId || fallbackId,
    maxTokens: count(row.maxTokens),
    maxMinutes: count(row.maxMinutes),
    usedTokens: count(row.usedTokens),
    exhausted: row.exhausted === true,
  };
  if (row.startedAt) budget.startedAt = row.startedAt;
  return budget;
}

/**
 * Read the ceiling a task was dispatched with (`tasks.budget.get`).
 *
 * Two wire shapes are legal and both mean the same thing here: a bare
 * `TaskBudget`, or `{budget:null}` when the task has none. `null` is an answer —
 * "no ceiling" — and must not be turned into an error; a thrown error means the
 * read itself failed. That distinction is the whole reason this is a separate
 * call: a task you can see but whose budget did not load is still a task.
 */
export async function loadTaskBudget(id: string): Promise<TaskBudget | null> {
  const result = await request<TaskBudgetWire & { budget?: TaskBudgetWire | null }>(
    'tasks.budget.get',
    { id },
  );
  const row = Object.prototype.hasOwnProperty.call(result, 'budget') ? result.budget : result;
  if (!row) return null;
  return taskBudgetFromWire(row, id);
}

/** What a ceiling write carries: the two limits, in the gateway's own units. */
export interface TaskBudgetSetInput {
  id: string;
  /** Token ceiling; `0` clears it (unlimited), it is not an absence. */
  maxTokens: number;
  /** Wall-clock ceiling in minutes; `0` clears it. */
  maxMinutes: number;
}

/**
 * Set the ceiling of one task (`tasks.budget.set`).
 *
 * Before this existed the budget block could read a ceiling and never change it:
 * a dispatched task gets a 200k/30min row automatically, and `tasks.budget.set`
 * had no client at all. The gateway answers with the row in force (usage kept,
 * `exhausted` re-derived from usage versus the new limits), so the block redraws
 * from the answer rather than from a guess.
 *
 * The gateway guards this write with a permission action, so a refusal is a real
 * answer: it is thrown, and the block shows the gateway's own words instead of
 * inventing a reason (see `features/task/TaskBudget.tsx`).
 */
export async function setTaskBudget(input: TaskBudgetSetInput): Promise<TaskBudget> {
  const result = await request<TaskBudgetWire>('tasks.budget.set', {
    id: input.id,
    maxTokens: input.maxTokens,
    maxMinutes: input.maxMinutes,
  });
  return taskBudgetFromWire(result, input.id);
}

/**
 * 待发送消息 — the message a `before_send` task is holding
 * (`tasks.send.pending`, docs/TRANSPORT.md §5.1).
 *
 * A task with `approval=before_send` may start, but the message it wants to send
 * when it settles does not leave the product: it is parked with the task until a
 * human approves it (`tasks.send.approve`). Until this read existed nobody could
 * find that out — the task simply looked finished and the words never arrived.
 *
 * The answer is `{held:true,text}` while a message waits and `{held:false}`
 * otherwise. Only the first shape is a hold; `held` that is `false`, missing, or
 * wrapped in anything else normalizes to `null` here, because "nothing is
 * waiting" is a real answer and not a failure. A thrown error is the read itself
 * having failed, and the block that calls this reports it as such.
 *
 * `text` is kept **verbatim** — no trim, no re-wrap, no truncation: it is the
 * words that would be said in public, and any edit here would be a different
 * message on the wire.
 */
export interface HeldSend {
  /** The exact words that would go out. */
  text: string;
}

/** Rows as they arrive on the wire: `held:false` carries no text at all. */
interface HeldSendWire {
  held?: boolean;
  text?: string;
}

/**
 * Read the parked message for one task (`tasks.send.pending`).
 *
 * `null` is an answer — nothing is held — and must not be turned into an error.
 * `{held:true}` without text is still a hold (a message with nothing in it), so
 * it is reported rather than swallowed: hiding it would hide something waiting
 * to go out.
 */
export async function loadPendingSend(id: string): Promise<HeldSend | null> {
  const result = await request<HeldSendWire>('tasks.send.pending', { id });
  if (result.held !== true) return null;
  return { text: result.text ?? '' };
}

/**
 * What the channel dispatcher reported back about an approved send
 * (`contracts.ChannelReceipt`).
 */
export interface SendReceipt {
  /** The channel that took the message (`feishu`, `qq`, …); the gateway's own word. */
  channel: string;
  /**
   * How many delivery attempts it took, when the gateway reports it. Kept out of
   * the type when absent, so "one attempt" (the usual case) is not invented.
   */
  attempts?: number;
}

/** Exactly what the gateway accepts for a release: who, and on which task. */
export interface ApproveSendInput {
  id: string;
  /**
   * Who approves it. Required by the gateway — a blank approver is refused
   * (`1010 approver is required`) rather than read as consent.
   */
  approver: string;
}

interface SendReceiptWire {
  sent?: boolean;
  channel?: string;
  attempts?: number;
}

/**
 * Let a held message leave (`tasks.send.approve`, not idempotent: an approved
 * message leaves exactly once, and a second approval is an error).
 *
 * A failed call is a message that did **not** go out; the gateway reports it as a
 * protocol error carrying its own words, which reach the screen unchanged.
 */
export async function approveSend(input: ApproveSendInput): Promise<SendReceipt> {
  const result = await request<SendReceiptWire>('tasks.send.approve', {
    id: input.id,
    approver: input.approver,
  });
  const receipt: SendReceipt = { channel: result.channel ?? '' };
  if (typeof result.attempts === 'number') receipt.attempts = result.attempts;
  return receipt;
}

/**
 * 提出人 — which human asked for this task (`tasks.requestedby.get`).
 *
 * The board records the work, the lease records the executor, and this records
 * the person who asked for it. It is a read of its own for the same reason the
 * budget is: the screen can show the task without it, so its failure must not
 * become the task's failure.
 *
 * The answer is `{requestedBy: string|null}`, and the two meanings must never be
 * blurred: a name is "somebody asked for it", while `null` is "the gateway has
 * nobody on record" — an answer, not a failure. A thrown error is the read
 * itself having failed, and the caller keeps that distinct from "unknown".
 *
 * Absent, `null`, empty and whitespace-only all normalize to `null` here: a name
 * that is only spaces is not a name, and `提出人 ` with nothing after it would be
 * an attribution to a person who is not on the row.
 */
export async function loadTaskRequester(id: string): Promise<string | null> {
  const result = await request<{ requestedBy?: string | null }>('tasks.requestedby.get', { id });
  const name = typeof result.requestedBy === 'string' ? result.requestedBy.trim() : '';
  return name === '' ? null : name;
}
