import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { MessageInfo } from '@ant-design/x-sdk';
import { Transcript, shouldFollow } from './transcript';
import { assistantMessage, userMessage, type ZeoMessage } from './types';

function manyMessages(count: number): MessageInfo<ZeoMessage>[] {
  return Array.from({ length: count }, (_, index) => ({
    id: `m-${index}`,
    status: 'success' as const,
    message:
      index % 2 === 0
        ? userMessage(`问题 ${index}`)
        : { ...assistantMessage(), text: `答案 ${index}` },
  }));
}

describe('shouldFollow (§7.2 threshold)', () => {
  it('sticks within 24px of the bottom and lets go above it', () => {
    expect(shouldFollow(0)).toBe(true);
    expect(shouldFollow(23)).toBe(true);
    expect(shouldFollow(24)).toBe(false);
    expect(shouldFollow(1400)).toBe(false);
  });
});

describe('Transcript virtualisation', () => {
  it('renders a bounded window for a 520-message session, not every row', () => {
    render(<Transcript messages={manyMessages(520)} />);
    const rows = document.querySelectorAll('.zk-transcript__row');
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.length).toBeLessThanOrEqual(40);
    // The window is the tail: the newest message is on screen.
    expect(screen.getByText('答案 519')).toBeInTheDocument();
    expect(screen.queryByText('问题 0')).not.toBeInTheDocument();
  });

  it('stops following after the reader scrolls up, and 回到最新 resumes at the bottom', async () => {
    const user = userEvent.setup();
    render(<Transcript messages={manyMessages(120)} />);
    const scroller = document.querySelector('.zk-transcript__scroll') as HTMLElement;
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 4000 });
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 600 });

    // At the bottom: no button.
    scroller.scrollTop = 3400;
    fireEvent.scroll(scroller);
    expect(screen.queryByRole('button', { name: '回到最新' })).not.toBeInTheDocument();

    // Reader scrolls up: following stops and the action appears.
    scroller.scrollTop = 1000;
    fireEvent.scroll(scroller);
    const jump = await screen.findByRole('button', { name: '回到最新' });

    // Clicking goes back to the bottom and removes the action immediately.
    await user.click(jump);
    expect(screen.queryByRole('button', { name: '回到最新' })).not.toBeInTheDocument();
  });
});
