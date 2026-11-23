import { describe, expect, it } from 'vitest';
import { resolveComposerPhase } from './composer';

/**
 * v0.3.1 ③: idle → connecting → running, where the connecting window ends at the
 * **first frame of any kind**. Waiting for the first token would leave the hint
 * saying "connecting" long after a tool call had visibly started.
 */
describe('resolveComposerPhase', () => {
  it('is idle when nothing is running', () => {
    expect(resolveComposerPhase(false, { text: '' })).toBe('idle');
    expect(resolveComposerPhase(false, undefined)).toBe('idle');
  });

  it('is connecting while a turn is in flight with no frame yet', () => {
    expect(resolveComposerPhase(true, { text: '', reasoning: '', steps: [] })).toBe('connecting');
    expect(resolveComposerPhase(true, undefined)).toBe('connecting');
  });

  it('leaves connecting on the first text', () => {
    expect(resolveComposerPhase(true, { text: '你' })).toBe('running');
  });

  it('leaves connecting on a tool call, before any answer text exists', () => {
    // This is the case a "first token" rule gets wrong.
    expect(resolveComposerPhase(true, { text: '', reasoning: '', steps: [{ id: 'call-1' }] })).toBe(
      'running',
    );
  });

  it('leaves connecting on reasoning alone', () => {
    expect(resolveComposerPhase(true, { text: '', reasoning: '先看目录' })).toBe('running');
  });
});
