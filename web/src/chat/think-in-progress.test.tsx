import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ThoughtSteps } from './thought-steps';

vi.mock('@/i18n', () => ({
  useT: () => (key: string) => key,
}));

/**
 * task-37 #5 — the **in-progress** reasoning is X's `Think`; the completed step
 * list stays `ThoughtChain`.
 *
 * Three assertions, because "we render Think" alone would not notice the two
 * regressions that matter:
 *  1. while streaming: `.ant-think` exists **and the reasoning text is visible**
 *     (a collapsed Think would hide what the user is waiting on);
 *  2. once done: no `.ant-think`, and the chain carries the step;
 *  3. the text survives the hand-over between the two components — the classic
 *     bug when two renderers take turns.
 */

describe('reasoning: Think while streaming, ThoughtChain when settled', () => {
  it('shows the live reasoning in Think, expanded, with its text', () => {
    const { container } = render(
      <ThoughtSteps reasoning="正在比较两个方案" steps={[]} streaming />,
    );

    const think = container.querySelector('.ant-think');
    expect(think, '进行中应由 antd X 的 Think 承载').not.toBeNull();
    expect(screen.getByText('正在比较两个方案')).toBeVisible();

    // The hand-written in-progress marker is gone.
    expect(container.querySelector('.zk-reasoning-text')).toBeNull();
    expect(container.querySelector('.ant-thought-chain') ?? container.querySelector('.zk-thought__header')).toBeNull();
  });

  it('hands the settled step to ThoughtChain and keeps the text', async () => {
    const { container, rerender } = render(
      <ThoughtSteps reasoning="正在比较两个方案" steps={[]} streaming />,
    );
    expect(container.querySelector('.ant-think')).not.toBeNull();

    rerender(<ThoughtSteps reasoning="正在比较两个方案" steps={[]} streaming={false} />);

    await waitFor(() => expect(container.querySelector('.ant-think')).toBeNull());
    expect(container.querySelector('.zk-thought'), '完成态仍由 ThoughtChain 承载').not.toBeNull();
    // 3. no content is lost at the hand-over: settled steps collapse by design,
    //    so the text must come back when the user opens the step again.
    const user = userEvent.setup();
    await user.click(screen.getByText('thread.reasoning'));
    await waitFor(() => expect(screen.getByText('正在比较两个方案')).toBeInTheDocument());
  });
});
