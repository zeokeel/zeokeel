import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Composer } from './composer';

/**
 * v0.3.1 ③: while a turn is submitted but nothing has come back yet — the 30 s
 * blackhole a user hit in v0.3.0 — the hint says so instead of showing the idle
 * text as if nothing were happening.
 *
 * Three states, and the boundary is a **first frame of any kind**: a tool call is
 * a frame too, so the hint must leave `connecting` for it as well.
 */
function renderComposer(phase: 'idle' | 'connecting' | 'running') {
  render(<Composer value="" onChange={() => undefined} onSubmit={() => undefined} phase={phase} />);
}

describe('composer hint states', () => {
  it('idle: the send hint', () => {
    renderComposer('idle');
    expect(screen.getByTestId('composer-hint').textContent).toContain('Enter 或 Cmd/Ctrl+Enter 发送');
  });

  it('connecting: says it is connecting to the model', () => {
    renderComposer('connecting');
    expect(screen.getByTestId('composer-hint').textContent).toContain('正在连接模型');
  });

  it('running: the queue/stop hint, never the connecting text', () => {
    renderComposer('running');
    const text = screen.getByTestId('composer-hint').textContent ?? '';
    expect(text).toContain('Enter 排队发送');
    expect(text).not.toContain('正在连接模型');
  });

  /**
   * The phase is also machine-readable. e2e and any future assertion must be able
   * to check the state without matching a translated sentence — and the attribute
   * must not drift from the copy it describes.
   */
  it('carries the phase as data-phase, in step with the copy', () => {
    for (const phase of ['idle', 'connecting', 'running'] as const) {
      const view = render(
        <Composer value="" onChange={() => undefined} onSubmit={() => undefined} phase={phase} />,
      );
      const hint = screen.getByTestId('composer-hint');
      expect(hint.dataset.phase).toBe(phase);
      expect(hint.textContent).not.toBe('');
      view.unmount();
    }
  });
});
