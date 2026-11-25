import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { BoardTask, TaskLease } from '@/api/task';
import { translate, type MessageKey } from '@/i18n';
import { TaskDetailScreen } from './TaskDetailScreen';

/**
 * The screen renders outside `I18nProvider` here, which is exactly what
 * `useT()` falls back to: the Chinese catalogue. Assertions therefore use the
 * real catalogue strings — the copy under test is the copy that ships.
 */
const T = (key: MessageKey, params?: Record<string, string | number>) =>
  translate('zh', key, params);

/**
 * The api module is the gateway boundary, so the screen tests mock it: what is
 * asserted here is what the screen shows and what it asks the gateway for, not
 * the wire framing (`task-api.test.ts` pins method names and parameters).
 */
const api = vi.hoisted(() => ({
  loadTaskBoard: vi.fn(),
  claimTask: vi.fn(),
  heartbeatTask: vi.fn(),
  submitTask: vi.fn(),
  reviewTask: vi.fn(),
}));

vi.mock('@/api/task', () => ({
  LEASE_STATES: ['claimed', 'review', 'done', 'rejected'],
  EVIDENCE_KINDS: ['file', 'link', 'output', 'screenshot'],
  loadTaskBoard: api.loadTaskBoard,
  claimTask: api.claimTask,
  heartbeatTask: api.heartbeatTask,
  submitTask: api.submitTask,
  reviewTask: api.reviewTask,
}));

function task(overrides: Partial<BoardTask> = {}): BoardTask {
  return {
    id: 'task-1',
    subject: '查退款率',
    description: '对比三家竞品的退款政策',
    status: 'pending',
    ...overrides,
  };
}

function lease(overrides: Partial<TaskLease> = {}): TaskLease {
  return { taskId: 'task-1', owner: 'lead', state: 'claimed', ...overrides };
}

function renderScreen(taskId = 'task-1') {
  return render(
    <MemoryRouter initialEntries={[`/t/${taskId}`]}>
      <Routes>
        <Route path="/t/:taskId" element={<TaskDetailScreen />} />
      </Routes>
    </MemoryRouter>,
  );
}

/**
 * antd inserts a space between two CJK characters (「重试」renders as「重 试」),
 * so a label is matched character by character. Same convention as
 * `features/inbox/InboxScreen.test.tsx`.
 */
function label(text: string): RegExp {
  return new RegExp(`^${[...text].join('\\s*')}$`);
}

/** The five section headings, level 2, in document order. */
function sectionHeadings(): string[] {
  return screen
    .getAllByRole('heading', { level: 2 })
    .map((heading) => heading.textContent ?? '');
}

beforeEach(() => {
  vi.clearAllMocks();
  api.claimTask.mockResolvedValue(lease());
  api.submitTask.mockResolvedValue(lease({ state: 'review' }));
  api.reviewTask.mockResolvedValue(lease({ state: 'done' }));
});

describe('TaskDetailScreen', () => {
  it('shows a skeleton, not an empty five-section page, while the board is in the air', () => {
    api.loadTaskBoard.mockReturnValue(new Promise(() => {}));

    renderScreen();

    expect(document.querySelector('.ant-skeleton')).not.toBeNull();
    // The honest loading state claims nothing about the task.
    expect(screen.queryByText(T('task.detail.section.doing'))).toBeNull();
    expect(screen.queryByText(T('task.detail.notFound'))).toBeNull();
  });

  it('says 找不到这个任务 when the board has no such row', async () => {
    api.loadTaskBoard.mockResolvedValue([task({ id: 'task-2' })]);

    renderScreen();

    expect(await screen.findByText(T('task.detail.notFound'))).toBeInTheDocument();
  });

  it('renders the five sections in the contract order', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);

    renderScreen();

    await screen.findByRole('heading', { name: T('task.detail.section.doing') });
    expect(sectionHeadings()).toEqual([
      T('task.detail.section.doing'),
      T('task.detail.section.progress'),
      T('task.detail.section.evidence'),
      T('task.detail.section.artifacts'),
      T('task.detail.section.needYou'),
    ]);
    // §1 carries the title (page heading) and the one-line goal.
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
    expect(screen.getByText('对比三家竞品的退款政策')).toBeInTheDocument();
    // No lease has been produced yet, so the proof sections are honestly empty.
    expect(within(screen.getByRole('region', { name: T('task.detail.section.evidence') })).getByText(T('task.detail.evidence.empty'))).toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: T('task.detail.section.artifacts') })).getByText(T('task.detail.artifacts.empty'))).toBeInTheDocument();
  });

  it('says 不需要你做什么 once the task is done', async () => {
    api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);

    renderScreen();

    const needYou = await screen.findByRole('region', { name: T('task.detail.section.needYou') });
    expect(within(needYou).getByText(T('task.detail.needYou.none'))).toBeInTheDocument();
    expect(within(needYou).queryByRole('button', { name: label(T('task.detail.claim')) })).toBeNull();
  });

  it('claims with the task id as the Lead, then re-reads the board', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: label(T('task.detail.claim')) }));

    expect(api.claimTask).toHaveBeenCalledWith({ id: 'task-1', owner: 'lead' });
    // One re-read after the write — the api is request/response, not a stream.
    await vi.waitFor(() => expect(api.loadTaskBoard).toHaveBeenCalledTimes(2));
    // The lease's state is what the screen now reports.
    expect(await screen.findByText(T('task.status.running'))).toBeInTheDocument();
  });

  it('keeps 提交证据 disabled until the ref is non-empty, then submits it as evidence', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: T('task.detail.submitEvidence') }));

    const submit = screen.getByRole('button', { name: label(T('task.detail.submit')) });
    expect(submit).toBeDisabled();
    // Whitespace is not evidence.
    await user.type(screen.getByLabelText(T('task.detail.evidenceRef')), '   ');
    expect(screen.getByRole('button', { name: label(T('task.detail.submit')) })).toBeDisabled();

    await user.type(screen.getByLabelText(T('task.detail.evidenceRef')), 'out/report.md');
    expect(screen.getByRole('button', { name: label(T('task.detail.submit')) })).toBeEnabled();

    await user.click(screen.getByRole('button', { name: label(T('task.detail.submit')) }));

    expect(api.submitTask).toHaveBeenCalledWith({
      id: 'task-1',
      owner: 'lead',
      evidence: [{ kind: 'file', ref: 'out/report.md' }],
    });
    await vi.waitFor(() => expect(api.loadTaskBoard).toHaveBeenCalledTimes(2));
    // The lease the submission produced is what §2 now reports.
    expect(await screen.findByText(T('task.status.waiting'))).toBeInTheDocument();
  });

  it('reads a URL ref as a link rather than a file', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: T('task.detail.submitEvidence') }));
    await user.type(screen.getByLabelText(T('task.detail.evidenceRef')), 'https://example.com/r');
    await user.click(screen.getByRole('button', { name: label(T('task.detail.submit')) }));

    expect(api.submitTask).toHaveBeenCalledWith({
      id: 'task-1',
      owner: 'lead',
      evidence: [{ kind: 'link', ref: 'https://example.com/r' }],
    });
  });

  it('shows the submitted evidence and the files it produced', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    api.submitTask.mockResolvedValue(
      lease({
        state: 'review',
        heartbeat: new Date(Date.now() - 120_000).toISOString(),
        evidence: [
          { kind: 'link', ref: 'https://example.com/r', note: '竞品 A' },
          { kind: 'file', ref: 'out/report.md' },
        ],
      }),
    );
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: T('task.detail.submitEvidence') }));
    await user.type(screen.getByLabelText(T('task.detail.evidenceRef')), 'out/report.md');
    await user.click(screen.getByRole('button', { name: label(T('task.detail.submit')) }));

    await screen.findByText('https://example.com/r');
    const artifacts = screen.getByRole('region', { name: T('task.detail.section.artifacts') });
    expect(within(artifacts).getByText('out/report.md')).toBeInTheDocument();
    // The link is proof, not a product: it stays in §3 only.
    expect(within(artifacts).queryByText('https://example.com/r')).toBeNull();
    // Relative stamp, absolute on hover (design §12.30).
    expect(screen.getByText('2 分钟前')).toHaveAttribute(
      'title',
      expect.stringContaining('T'),
    );
  });

  it('accepts through the gateway with the reviewer identity', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: label(T('task.detail.accept')) }));

    expect(api.reviewTask).toHaveBeenCalledWith({
      id: 'task-1',
      decision: 'accept',
      reviewer: 'lead',
    });
    await vi.waitFor(() => expect(api.loadTaskBoard).toHaveBeenCalledTimes(2));
  });

  it('requires a reason before 打回, then sends it as the review note', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: label(T('task.detail.reject')) }));

    const confirm = screen.getByRole('button', { name: label(T('task.detail.rejectSubmit')) });
    expect(confirm).toBeDisabled();
    expect(api.reviewTask).not.toHaveBeenCalled();

    await user.type(screen.getByLabelText(T('task.detail.rejectReason')), '少了第三家');
    expect(screen.getByRole('button', { name: label(T('task.detail.rejectSubmit')) })).toBeEnabled();
    await user.click(screen.getByRole('button', { name: label(T('task.detail.rejectSubmit')) }));

    expect(api.reviewTask).toHaveBeenCalledWith({
      id: 'task-1',
      decision: 'reject',
      reviewer: 'lead',
      note: '少了第三家',
    });
    await vi.waitFor(() => expect(api.loadTaskBoard).toHaveBeenCalledTimes(2));
  });

  it('reports a failed write next to the buttons instead of dropping the task', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    api.claimTask.mockRejectedValue(new Error('lease: task already claimed'));
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: label(T('task.detail.claim')) }));

    expect(await screen.findByText('lease: task already claimed')).toBeInTheDocument();
    // The row is still there — the failed write changed nothing.
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
  });

  it('shows a retry on a read failure and refetches when it is pressed', async () => {
    api.loadTaskBoard
      .mockRejectedValueOnce(new Error('网关连不上'))
      .mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    expect(await screen.findByText(T('task.detail.loadFailed'))).toBeInTheDocument();
    expect(screen.getByText('网关连不上')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: label(T('task.detail.retry')) }));

    expect(await screen.findByRole('heading', { name: T('task.detail.section.doing') })).toBeInTheDocument();
    expect(api.loadTaskBoard).toHaveBeenCalledTimes(2);
  });
});