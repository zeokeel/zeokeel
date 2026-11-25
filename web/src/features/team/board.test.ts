import { describe, expect, it } from 'vitest';
import type { BoardTask } from '@/api/task';
import { boardStatusOf, groupTasksByStatus, statusOrder } from './board';

function task(id: string, status: string): BoardTask {
  return { id, subject: `任务 ${id}`, status };
}

/** The six words, written out once: these are the design's fixed vocabulary. */
const WORDS = ['queued', 'running', 'waiting', 'stuck', 'done', 'failed'] as const;

describe('statusOrder', () => {
  it('is the six words in the one fixed order', () => {
    expect(statusOrder()).toEqual([...WORDS]);
  });

  it('hands out a copy, so a caller cannot reorder every section on the next render', () => {
    statusOrder().reverse();
    expect(statusOrder()).toEqual([...WORDS]);
  });
});

describe('groupTasksByStatus', () => {
  it('always returns all six sections, in order, even when the board is empty', () => {
    const groups = groupTasksByStatus([]);

    expect(groups.map((group) => group.status)).toEqual([...WORDS]);
    expect(groups.map((group) => group.count)).toEqual([0, 0, 0, 0, 0, 0]);
    // The label is a catalogue key, never a baked-in string: the screen owns the words.
    expect(groups.map((group) => group.label)).toEqual([
      'task.status.queued',
      'task.status.running',
      'task.status.waiting',
      'task.status.stuck',
      'task.status.done',
      'task.status.failed',
    ]);
  });

  it('maps the protocol statuses to the six words and counts each section', () => {
    const groups = groupTasksByStatus([
      task('a', 'pending'),
      task('b', 'in_progress'),
      task('c', 'completed'),
      task('d', 'pending'),
      task('e', 'failed'),
      task('f', 'waiting'),
      task('g', 'stuck'),
      task('h', 'done'),
    ]);
    const byStatus = Object.fromEntries(groups.map((group) => [group.status, group]));

    expect(byStatus.queued.count).toBe(2);
    expect(byStatus.running.count).toBe(1);
    expect(byStatus.waiting.count).toBe(1);
    expect(byStatus.stuck.count).toBe(1);
    // `completed` and `done` are the same word, so they land in the same section.
    expect(byStatus.done.count).toBe(2);
    expect(byStatus.failed.count).toBe(1);
  });

  it('lands an unknown status in 进行中 instead of dropping the row', () => {
    const groups = groupTasksByStatus([task('a', 'paused_by_the_gateway')]);
    const running = groups.find((group) => group.status === 'running');

    // Not dropped, and not silently reordered into 排队中 either.
    expect(running?.tasks.map((row) => row.id)).toEqual(['a']);
    expect(groups.reduce((sum, group) => sum + group.count, 0)).toBe(1);
    expect(boardStatusOf('paused_by_the_gateway')).toBe('running');
  });

  it('keeps the gateway order inside a section and never re-sorts rows', () => {
    const groups = groupTasksByStatus([
      task('second', 'in_progress'),
      task('first', 'in_progress'),
    ]);

    expect(groups.find((group) => group.status === 'running')?.tasks.map((row) => row.id)).toEqual([
      'second',
      'first',
    ]);
  });
});