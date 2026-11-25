/**
 * Shaping for the team task board (`tasks.snapshot`, docs/TRANSPORT.md §5.1.8).
 *
 * Pure functions only: no gateway, no DOM, no clock. The screen owns the words
 * (they come from the catalogue through `t`), so a grouping bug cannot be
 * hidden behind a copy change and vice versa.
 *
 * The six status words are fixed by the interaction design (§6.1, `docs/plans/
 * grokbot-interaction-design.md`): 排队中 / 进行中 / 等你 / 卡住 / 完成 / 失败 —
 * in that order, always. A board that reorders itself by "importance" makes the
 * same work jump around between two glances.
 */
import type { BoardTask } from '@/api/task';
import type { MessageKey } from '@/i18n';

/**
 * One of the six words. Named after the word, not after a protocol value: the
 * wire speaks `pending`/`in_progress`/`completed`, the reader reads Chinese or
 * English (README 硬约束「协议值不进 UI」).
 */
export type BoardStatus = 'queued' | 'running' | 'waiting' | 'stuck' | 'done' | 'failed';

/** The fixed reading order of the six words. */
const ORDER: readonly BoardStatus[] = ['queued', 'running', 'waiting', 'stuck', 'done', 'failed'];

/** Catalogue key per word — the same keys `/t/:taskId` uses, so the two screens cannot disagree. */
const LABELS: Record<BoardStatus, MessageKey> = {
  queued: 'task.status.queued',
  running: 'task.status.running',
  waiting: 'task.status.waiting',
  stuck: 'task.status.stuck',
  done: 'task.status.done',
  failed: 'task.status.failed',
};

/**
 * The six words, in the one order the board renders them.
 *
 * Returns a fresh array so a caller cannot reorder the shared constant and move
 * every section on the next render.
 */
export function statusOrder(): BoardStatus[] {
  return [...ORDER];
}

/**
 * A board row's protocol status → one of the six words.
 *
 * Both vocabularies are accepted where they mean the same thing: `pending` and
 * `queued` are 排队中, `completed` and `done` are 完成. The board serves the
 * tool-written tasks (`pending`/`in_progress`/`completed`), while 等你/卡住 are
 * lease-shaped words the wire may start sending; naming all six here means a
 * new value fills a real section instead of being folded into a wrong one.
 */
export function boardStatusOf(status: string): BoardStatus {
  switch (status) {
    case 'pending':
    case 'queued':
      return 'queued';
    case 'in_progress':
    case 'running':
      return 'running';
    case 'waiting':
      return 'waiting';
    case 'stuck':
      return 'stuck';
    case 'completed':
    case 'done':
      return 'done';
    case 'failed':
      return 'failed';
    default:
      // An unknown status is **进行中**, never dropped: the row exists and the
      // gateway returned it, so it is live work; hiding it would silently
      // remove a task from the board, which is the one failure mode a board
      // must not have. 进行中 (not 排队中) because "waiting for a slot" is a
      // claim the client cannot make — it does not know the queue.
      return 'running';
  }
}

export interface BoardGroup {
  /** Which of the six words this section is, and where it sits in the order. */
  status: BoardStatus;
  /** The word, as a catalogue key — the screen resolves it through `t`. */
  label: MessageKey;
  /** Rows, in the order the gateway returned them (never re-sorted here). */
  tasks: BoardTask[];
  /** Shown in the header and on the section heading; `tasks.length` by construction. */
  count: number;
}

/**
 * Group the board into the six sections, always all six, always in order.
 *
 * Empty sections are returned rather than filtered: the header's per-word
 * counts need all six, and the screen — not this function — decides that an
 * empty section is not worth rendering (WEB-CLIENT §8.1 A13: a `0` heading
 * answers nothing).
 */
export function groupTasksByStatus(tasks: readonly BoardTask[]): BoardGroup[] {
  const buckets = new Map<BoardStatus, BoardTask[]>(ORDER.map((status) => [status, []]));
  for (const task of tasks) {
    // `boardStatusOf` is total: every status has a bucket, so no row can be lost.
    buckets.get(boardStatusOf(task.status))?.push(task);
  }
  return ORDER.map((status) => {
    const rows = buckets.get(status) ?? [];
    return { status, label: LABELS[status], tasks: rows, count: rows.length };
  });
}