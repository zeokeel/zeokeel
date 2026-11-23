import { describe, expect, it } from 'vitest';
import { resolveComposerPhase, type ComposerFrame } from './composer';

/**
 * v0.3.1 ③: idle → connecting → running, where the connecting window ends at the
 * **first frame of any kind**. Waiting for the first token would leave the hint
 * saying "connecting" long after a tool call had visibly started.
 *
 * The phase is computed from the turn's **assistant** frame, not from whatever
 * message happens to be last: sending appends the user's echo immediately, and
 * reading that as "a frame arrived" made `connecting` unreachable in the browser
 * (found by running the v0.3.1 build — the unit tests passed while every real
 * turn skipped straight to `running`).
 */
const user = (text: string): ComposerFrame => ({ role: 'user', text });
const assistant = (frame: Partial<ComposerFrame> = {}): ComposerFrame => ({
  role: 'assistant',
  text: '',
  reasoning: '',
  steps: [],
  ...frame,
});

describe('resolveComposerPhase', () => {
  it('is idle when nothing is running', () => {
    expect(resolveComposerPhase(false, [])).toBe('idle');
    expect(resolveComposerPhase(false, [user('hi'), assistant({ text: '你' })])).toBe('idle');
  });

  it('is connecting while a turn is in flight and only the user echo exists', () => {
    // The bug: the user's own message is not an assistant frame.
    expect(resolveComposerPhase(true, [user('hi')])).toBe('connecting');
    expect(resolveComposerPhase(true, [])).toBe('connecting');
  });

  it('is still connecting while the assistant placeholder is empty', () => {
    expect(resolveComposerPhase(true, [user('hi'), assistant()])).toBe('connecting');
  });

  it('leaves connecting on the first text', () => {
    expect(resolveComposerPhase(true, [user('hi'), assistant({ text: '你' })])).toBe('running');
  });

  it('leaves connecting on a tool call, before any answer text exists', () => {
    // This is the case a "first token" rule gets wrong.
    expect(
      resolveComposerPhase(true, [user('hi'), assistant({ steps: [{ id: 'call-1' }] })]),
    ).toBe('running');
  });

  it('leaves connecting on reasoning alone', () => {
    expect(resolveComposerPhase(true, [user('hi'), assistant({ reasoning: '先看目录' })])).toBe(
      'running',
    );
  });

  it('ignores a later user message, such as a queued follow-up', () => {
    expect(resolveComposerPhase(true, [user('hi'), assistant(), user('second')])).toBe('connecting');
  });
});