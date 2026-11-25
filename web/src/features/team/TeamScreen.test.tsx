import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { BoardTask } from '@/api/task';
import { TeamScreen } from './TeamScreen';

/**
 * The api module is the gateway boundary, so the screen tests mock it: what is
 * asserted here is what the screen shows and what it asks the gateway for, not
 * the wire framing (`features/task/task-api.test.ts` pins the method names).
 *
 * The screen renders outside `I18nProvider`, which is what `useT()` falls back
 * to: the Chinese catalogue. Assertions therefore use the real shipping copy.
 */
const api = vi.hoisted(() => ({ loadTaskBoard: vi.fn() }));

vi.mock('@/api/task', () => ({ loadTaskBoard: api.loadTaskBoard }));

function task(id: string, overrides: Partial<BoardTask> = {}): BoardTask {
  return { id, subject: `任务 ${id}`, status: 'pending', ...overrides };
}

function renderScreen() {
  return render(
    <MemoryRouter initialEntries={['/team']}>
      <TeamScreen />
    </MemoryRouter>,
  );
}

/** antd inserts a space between two CJK characters (「重试」renders as「重 试」). */
function label(text: string): RegExp {
  return new RegExp(`^${[...text].join('\\s*')}$`);
}

/** The section headings, level 2, in document order. */
function sectionHeadings(): string[] {
  return screen
    .getAllByRole('heading', { level: 2 })
    .map((heading) => heading.textContent ?? '');
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('TeamScreen', () => {
  it('shows a skeleton, not an empty state, while the first read is in the air', () => {
    api.loadTaskBoard.mockReturnValue(new Promise(() => {}));

    renderScreen();

    expect(document.querySelector('.ant-skeleton')).not.toBeNull();
    // The honest loading state claims nothing about the board.
    expect(screen.queryByText('还没有任务。')).toBeNull();
    // Read once: no timer means exactly one request while the screen is open.
    expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
  });

  it('says 还没有任务。 when the board is empty', async () => {
    api.loadTaskBoard.mockResolvedValue([]);

    renderScreen();

    expect(await screen.findByText('还没有任务。')).toBeInTheDocument();
    // No zero-count headings either: a `0` answers nothing (WEB-CLIENT A13).
    expect(screen.queryAllByRole('heading', { level: 2 })).toHaveLength(0);
    expect(screen.queryByTestId('team-count-queued')).toBeNull();
  });

  it('groups rows into the six words in the fixed order', async () => {
    api.loadTaskBoard.mockResolvedValue([
      task('d', { status: 'failed' }),
      task('a', { status: 'pending' }),
      task('b', { status: 'in_progress' }),
      task('c', { status: 'completed' }),
    ]);

    renderScreen();

    await screen.findByRole('region', { name: '排队中' });
    expect(sectionHeadings()).toEqual(['排队中', '进行中', '完成', '失败']);
    expect(within(screen.getByRole('region', { name: '排队中' })).getByText('任务 a')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: '进行中' })).getByText('任务 b')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: '完成' })).getByText('任务 c')).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: '失败' })).getByText('任务 d')).toBeInTheDocument();
  });

  it('shows a per-group count in the header', async () => {
    api.loadTaskBoard.mockResolvedValue([
      task('a', { status: 'pending' }),
      task('b', { status: 'pending' }),
      task('c', { status: 'in_progress' }),
    ]);

    renderScreen();

    await screen.findByTestId('team-count-queued');
    expect(screen.getByTestId('team-count-queued')).toHaveTextContent('排队中 2');
    expect(screen.getByTestId('team-count-running')).toHaveTextContent('进行中 1');
    // A word with no rows gets no chip.
    expect(screen.queryByTestId('team-count-failed')).toBeNull();
  });

  it('puts an unknown status on the board under 进行中', async () => {
    api.loadTaskBoard.mockResolvedValue([task('a', { status: 'paused_by_the_gateway' })]);

    renderScreen();

    const running = await screen.findByRole('region', { name: '进行中' });
    // Visible, not dropped — the row exists and the gateway returned it.
    expect(within(running).getByText('任务 a')).toBeInTheDocument();
    expect(screen.getByTestId('team-count-running')).toHaveTextContent('进行中 1');
  });

  it('gives each row its subject, owner (or 没人认领), relative time and deep link', async () => {
    const twoMinutesAgo = new Date(Date.now() - 2 * 60_000).toISOString();
    api.loadTaskBoard.mockResolvedValue([
      task('task-1', { subject: '查退款率', owner: 'lead', updatedAt: twoMinutesAgo }),
      task('task-2', { subject: '整理周报' }),
    ]);

    renderScreen();

    const row = await screen.findByTestId('team-row-task-1');
    // The subject is the link to the task's own screen.
    expect(within(row).getByRole('link', { name: '查退款率' })).toHaveAttribute('href', '/t/task-1');
    expect(within(row).getByText('lead')).toBeInTheDocument();
    expect(within(row).getByText('2 分钟前')).toBeInTheDocument();

    // A row nobody claimed says so, and a row with no stamp shows no invented time.
    const unclaimed = screen.getByTestId('team-row-task-2');
    expect(within(unclaimed).getByText('没人认领')).toBeInTheDocument();
    expect(within(unclaimed).queryByText(/分钟前|小时前|天前|刚刚/)).toBeNull();
    expect(within(unclaimed).getByRole('link', { name: '整理周报' })).toHaveAttribute(
      'href',
      '/t/task-2',
    );
  });

  it('shows an error Result and refetches when 重试 is pressed', async () => {
    api.loadTaskBoard.mockRejectedValueOnce(new Error('socket closed'));

    renderScreen();

    expect(await screen.findByText('任务板加载失败')).toBeInTheDocument();
    expect(screen.getByText('socket closed')).toBeInTheDocument();

    api.loadTaskBoard.mockResolvedValueOnce([task('task-1', { subject: '查退款率' })]);
    await userEvent.setup().click(screen.getByRole('button', { name: label('重试') }));

    expect(await screen.findByText('查退款率')).toBeInTheDocument();
    await waitFor(() => expect(api.loadTaskBoard).toHaveBeenCalledTimes(2));
    expect(screen.queryByText('任务板加载失败')).toBeNull();
  });
});