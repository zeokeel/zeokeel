/**
 * Background tasks (`tasks` / `tasks:<sessionId>`, docs/TRANSPORT.md §5.1.8).
 *
 * The board is written from several places — the agent's own team-task tools,
 * another client, this one — and only the gateway's own writes are pushed
 * immediately: everything else is polled, with a **2 second ceiling**. So the
 * UI must neither pretend to be instant nor hard-code a delay; it renders
 * whatever the domain currently holds.
 *
 * `added` in `tasks.delta` is an **upsert** (a whole changed row), not a
 * field-level patch: merge by `id`, never merge properties.
 */

export type TaskStatus = 'pending' | 'in_progress' | 'completed';

export interface GatewayTask {
  id: string;
  subject: string;
  description?: string;
  status: TaskStatus;
  owner?: string;
  blockedBy?: string[];
  writeScopes?: string[];
  createdAt?: string;
  updatedAt?: string;
}

/** Rows as they arrive on the wire: `status` is the protocol value. */
export interface TaskWire {
  id: string;
  subject?: string;
  description?: string;
  status?: string;
  owner?: string;
  blockedBy?: string[];
  writeScopes?: string[];
  createdAt?: string;
  updatedAt?: string;
}

export interface TasksSnapshot {
  epoch: string;
  rev: number;
  tasks: TaskWire[];
}

/**
 * Protocol status → ours. An unknown status is `pending`: the row exists and is
 * not running, and inventing a fourth state would put a protocol value in the
 * UI (README 硬约束）。
 */
export function toTaskStatus(value: string | undefined): TaskStatus {
  if (value === 'in_progress') return 'in_progress';
  if (value === 'completed') return 'completed';
  return 'pending';
}

export function normalizeTask(row: TaskWire): GatewayTask {
  return {
    id: row.id,
    subject: row.subject ?? row.id,
    status: toTaskStatus(row.status),
    ...(row.description ? { description: row.description } : {}),
    ...(row.owner ? { owner: row.owner } : {}),
    ...(row.blockedBy && row.blockedBy.length > 0 ? { blockedBy: row.blockedBy } : {}),
    ...(row.writeScopes && row.writeScopes.length > 0 ? { writeScopes: row.writeScopes } : {}),
    ...(row.createdAt ? { createdAt: row.createdAt } : {}),
    ...(row.updatedAt ? { updatedAt: row.updatedAt } : {}),
  };
}

/**
 * Fold a delta: remove by id, then **upsert** every `added` row by id. New rows
 * append so the board keeps the order the gateway gave it.
 */
export function applyTasksDelta(
  items: GatewayTask[],
  delta: { added?: TaskWire[]; removed?: string[] },
): GatewayTask[] {
  const removed = new Set(delta.removed ?? []);
  const kept = items.filter((item) => !removed.has(item.id));
  const index = new Map(kept.map((item, position) => [item.id, position]));
  const next = kept.slice();
  for (const row of delta.added ?? []) {
    const task = normalizeTask(row);
    const at = index.get(task.id);
    if (at === undefined) {
      index.set(task.id, next.length);
      next.push(task);
    } else {
      next[at] = task;
    }
  }
  return next;
}

/** The chip's number: work that is not finished yet. */
export function activeTaskCount(items: GatewayTask[]): number {
  return items.filter((item) => item.status !== 'completed').length;
}