import { describe, expect, it } from 'vitest';
import { applyPromptsDelta, type QueuedPrompt } from './prompt-queue';

describe('applyPromptsDelta', () => {
  const rows: QueuedPrompt[] = [
    { id: 'a', text: 'a' },
    { id: 'b', text: 'b' },
    { id: 'c', text: 'c' },
  ];

  it('appends new prompts to the end', () => {
    const next = applyPromptsDelta(rows, { added: [{ id: 'd', text: 'd' }] });
    expect(next.map((item) => item.id)).toEqual(['a', 'b', 'c', 'd']);
  });

  it('drops removed prompts', () => {
    const next = applyPromptsDelta(rows, { removed: ['b'] });
    expect(next.map((item) => item.id)).toEqual(['a', 'c']);
  });

  it('applies a reorder that carries no add or remove', () => {
    // The gateway reorders with `{reordered:true, order:[…]}`; a client that only
    // handled add/remove kept the old order while the backend had changed it.
    const next = applyPromptsDelta(rows, { reordered: true, order: ['c', 'a', 'b'] });
    expect(next.map((item) => item.id)).toEqual(['c', 'a', 'b']);
  });

  it('keeps rows the delta does not name, after the named ones', () => {
    const next = applyPromptsDelta(rows, { reordered: true, order: ['c'] });
    expect(next.map((item) => item.id)).toEqual(['c', 'a', 'b']);
  });

  it('removes and appends in the same delta', () => {
    const next = applyPromptsDelta(rows, { removed: ['a'], added: [{ id: 'd', text: 'd' }] });
    expect(next.map((item) => item.id)).toEqual(['b', 'c', 'd']);
  });
});
