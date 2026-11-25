import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BoardTask, TaskLease } from '@/api/task';
import { translate, type MessageKey } from '@/i18n';
import {
  expectNoHorizontalOverflow,
  expectTapTarget,
  expectWrapsOrEllipsis,
  installViewport,
  PHONE_WIDTH,
  uninstallViewport,
} from '@/test/viewport';
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
  loadTaskLease: vi.fn(),
  claimTask: vi.fn(),
  heartbeatTask: vi.fn(),
  submitTask: vi.fn(),
  reviewTask: vi.fn(),
}));

vi.mock('@/api/task', () => ({
  LEASE_STATES: ['claimed', 'review', 'done', 'rejected'],
  EVIDENCE_KINDS: ['file', 'link', 'output', 'screenshot'],
  loadTaskBoard: api.loadTaskBoard,
  loadTaskLease: api.loadTaskLease,
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
  // `mockResolvedValueOnce` queues survive `clearAllMocks`, so the lease read is
  // reset outright: a leftover answer from one test must not steer the next.
  api.loadTaskLease.mockReset();
  // No claim unless a test says so: "no lease" is the honest default for a task
  // nobody has opened.
  api.loadTaskLease.mockResolvedValue(null);
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
    // Opening the task: nobody holds it. After the claim the lease read is what
    // reports it — the same two reads, now with a claim in the second one.
    api.loadTaskLease.mockResolvedValueOnce(null).mockResolvedValue(lease());
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
    api.loadTaskLease
      .mockResolvedValueOnce(null)
      .mockResolvedValue(lease({ state: 'review', evidence: [{ kind: 'file', ref: 'out/report.md' }] }));
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
    api.submitTask.mockResolvedValue(lease({ state: 'review' }));
    // The re-read's lease is what fills §3/§4 — the same shape the gateway
    // would answer with after a submission.
    api.loadTaskLease.mockResolvedValueOnce(null).mockResolvedValue(
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

  it('shows the evidence a teammate already submitted, on first open', async () => {
    api.loadTaskBoard.mockResolvedValue([task({ owner: 'bot-1' })]);
    // The lease read is the whole point of this surface: no write happens in
    // this test, so everything §2/§3/§4 show came from `tasks.lease`.
    api.loadTaskLease.mockResolvedValue(
      lease({
        owner: 'bot-1',
        state: 'review',
        heartbeat: new Date(Date.now() - 120_000).toISOString(),
        evidence: [
          { kind: 'link', ref: 'https://example.com/r', note: '竞品 A' },
          { kind: 'file', ref: 'out/report.md' },
        ],
      }),
    );

    renderScreen();

    expect(await screen.findByText('https://example.com/r')).toBeInTheDocument();
    const evidence = screen.getByRole('region', { name: T('task.detail.section.evidence') });
    expect(within(evidence).queryByText(T('task.detail.evidence.empty'))).toBeNull();
    expect(within(evidence).queryByRole('alert')).toBeNull();
    // §2 reports the lease's state, and the timeline knows someone took it.
    expect(screen.getByText(T('task.status.waiting'))).toBeInTheDocument();
    expect(screen.getByText(T('task.detail.progress.claimed'))).toBeInTheDocument();
    // §4 shows the file the submission produced.
    const artifacts = screen.getByRole('region', { name: T('task.detail.section.artifacts') });
    expect(within(artifacts).getByText('out/report.md')).toBeInTheDocument();
    // The read asks for this task and carries no identity: it is read-only.
    expect(api.loadTaskLease).toHaveBeenCalledWith({ id: 'task-1' });
  });

  it('says there is no evidence when the lease read answers null', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    api.loadTaskLease.mockResolvedValue(null);

    renderScreen();

    const evidence = await screen.findByRole('region', { name: T('task.detail.section.evidence') });
    expect(within(evidence).getByText(T('task.detail.evidence.empty'))).toBeInTheDocument();
    // "No lease" is an answer, not a failure: no alert, no retry.
    expect(within(evidence).queryByRole('alert')).toBeNull();
    expect(within(evidence).queryByRole('button', { name: label(T('task.detail.retry')) })).toBeNull();
    const artifacts = screen.getByRole('region', { name: T('task.detail.section.artifacts') });
    expect(within(artifacts).getByText(T('task.detail.artifacts.empty'))).toBeInTheDocument();
  });

  it('keeps the task on screen when only the lease read fails', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    api.loadTaskLease.mockRejectedValue(new Error('证据服务连不上'));

    renderScreen();

    // The task itself rendered: the row and its goal are there …
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
    expect(screen.getByText('对比三家竞品的退款政策')).toBeInTheDocument();
    // … and §3 says which half failed, in the gateway's own words.
    const evidence = screen.getByRole('region', { name: T('task.detail.section.evidence') });
    expect(within(evidence).getByText(T('task.detail.evidence.failed'))).toBeInTheDocument();
    expect(within(evidence).getByText('证据服务连不上')).toBeInTheDocument();
    // Not the whole-screen failure, and not a false "there is no evidence".
    expect(screen.queryByText(T('task.detail.loadFailed'))).toBeNull();
    expect(within(evidence).queryByText(T('task.detail.evidence.empty'))).toBeNull();
  });

  it('re-reads only the lease when 证据没读出来 offers a retry', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    api.loadTaskLease
      .mockRejectedValueOnce(new Error('证据服务连不上'))
      .mockResolvedValue(
        lease({ state: 'review', evidence: [{ kind: 'link', ref: 'https://example.com/r' }] }),
      );
    const user = userEvent.setup();

    renderScreen();

    const evidence = await screen.findByRole('region', { name: T('task.detail.section.evidence') });
    await user.click(within(evidence).getByRole('button', { name: label(T('task.detail.retry')) }));

    expect(await screen.findByText('https://example.com/r')).toBeInTheDocument();
    // The retry promises one thing: the proof is read again. The board is not.
    expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
    expect(api.loadTaskLease).toHaveBeenCalledTimes(2);
    expect(
      within(screen.getByRole('region', { name: T('task.detail.section.evidence') })).queryByRole(
        'alert',
      ),
    ).toBeNull();
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
/**
 * Screen 05 of docs/design/grokbot-ui/README.md: the phone opens `/t/:taskId`
 * over the LAN at ~390px. What used to break: an unbroken subject or evidence
 * ref made the container 2255px wide (measured in Chromium before the fix), and
 * the four verbs stayed 32px tall after antd's `middle` size.
 */
describe('TaskDetailScreen at 390px', () => {
  // A subject / ref that is one long token, which is the case that overflowed.
  const LONG_REF = `https://example.test/evidence/${'b'.repeat(150)}`;

  beforeEach(() => {
    installViewport(PHONE_WIDTH);
  });

  afterEach(() => {
    uninstallViewport();
  });

  it('does not widen the screen and wraps the subject and the evidence ref', async () => {
    api.loadTaskBoard.mockResolvedValue([task({ subject: LONG_REF, description: LONG_REF })]);
    api.loadTaskLease.mockResolvedValue(
      lease({
        state: 'review',
        evidence: [
          { kind: 'link', ref: LONG_REF, note: LONG_REF },
          { kind: 'file', ref: LONG_REF },
        ],
      }),
    );

    renderScreen();

    const title = await screen.findByRole('heading', { level: 1 });
    expect(title).toHaveTextContent(LONG_REF);

    expectNoHorizontalOverflow(document.querySelector('.zk-screen') as Element);
    expectWrapsOrEllipsis(title, 'the task subject');
    // §1's goal line and §3/§4's refs can be the same unbroken token.
    expectWrapsOrEllipsis(document.querySelector('.ant-descriptions-item-content') as Element, 'the goal');
    const refs = [...document.querySelectorAll('.ant-listy a, .ant-typography code')];
    expect(refs.length).toBeGreaterThan(0);
    for (const ref of refs) expectWrapsOrEllipsis(ref, 'an evidence ref');
  });

  it('keeps the four verbs as tap targets', async () => {
    api.loadTaskBoard.mockResolvedValue([task({ subject: LONG_REF })]);

    renderScreen();

    const claim = await screen.findByRole('button', { name: label(T('task.detail.claim')) });
    expect(claim).toBeVisible();
    expect(claim).toBeEnabled();
    expectTapTarget(claim);
    // The other three stay reachable next to it (they wrap, they do not vanish).
    for (const verb of [
      T('task.detail.submitEvidence'),
      T('task.detail.accept'),
      T('task.detail.reject'),
    ]) {
      expectTapTarget(screen.getByRole('button', { name: label(verb) }));
    }
  });
});
