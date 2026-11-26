import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConnectorSummary, SkillSummary } from '@/api/read-models';
import type { BoardTask, TaskLease, TaskReview } from '@/api/task';
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
  // 验收结论 reads its own row (`tasks.reviews`): the durable review log, which
  // outlives the lease that the review deletes.
  loadTaskReviews: vi.fn(),
  claimTask: vi.fn(),
  heartbeatTask: vi.fn(),
  submitTask: vi.fn(),
  reviewTask: vi.fn(),
  // 工具绑定 reads and writes its own row on the task (`tasks.tools.get/set`).
  loadTaskTools: vi.fn(),
  setTaskTools: vi.fn(),
  // 预算 reads its own row on the task (`tasks.budget.get`) and writes it
  // (`tasks.budget.set`): its failure is never the task's failure, so it gets a
  // mock of its own.
  loadTaskBudget: vi.fn(),
  setTaskBudget: vi.fn(),
  // 待发送消息 reads (`tasks.send.pending`) and releases (`tasks.send.approve`)
  // a parked outbound message; both are its own, like the budget's read.
  loadPendingSend: vi.fn(),
  approveSend: vi.fn(),
  // 提出人 reads its own row on the task (`tasks.requestedby.get`), in the header
  // rather than in a section: its failure is silent, never the task's failure.
  loadTaskRequester: vi.fn(),
  // The 执行手段 block reads this on its own; a failure here is not a failure
  // of the task, so it is a mock of its own.
  loadCapabilities: vi.fn(),
}));

vi.mock('@/api/task', () => ({
  LEASE_STATES: ['claimed', 'review', 'done', 'rejected'],
  EVIDENCE_KINDS: ['file', 'link', 'output', 'screenshot'],
  loadTaskBoard: api.loadTaskBoard,
  loadTaskLease: api.loadTaskLease,
  loadTaskReviews: api.loadTaskReviews,
  claimTask: api.claimTask,
  heartbeatTask: api.heartbeatTask,
  submitTask: api.submitTask,
  reviewTask: api.reviewTask,
  loadTaskTools: api.loadTaskTools,
  setTaskTools: api.setTaskTools,
  loadTaskBudget: api.loadTaskBudget,
  setTaskBudget: api.setTaskBudget,
  loadPendingSend: api.loadPendingSend,
  approveSend: api.approveSend,
  loadTaskRequester: api.loadTaskRequester,
}));

vi.mock('@/api/read-models', () => ({
  loadCapabilities: api.loadCapabilities,
}));

/** One skill row as `GET /api/capabilities` answers it. */
function skill(name: string, description = `${name} 能做的事`): SkillSummary {
  return { id: name, name, description, source: 'workspace' };
}

/** One MCP connector row; the state is what the gateway derived, not a guess. */
function connector(name: string, overrides: Partial<ConnectorSummary> = {}): ConnectorSummary {
  return {
    id: name,
    name,
    transport: 'http',
    tools: 1,
    status: '已连接',
    state: 'connected',
    ...overrides,
  };
}

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

/** One recorded review decision (`tasks.reviews` answers these oldest first). */
function review(overrides: Partial<TaskReview> = {}): TaskReview {
  return {
    taskId: 'task-1',
    reviewer: 'auditor',
    role: 'auditor',
    decision: 'accept',
    verdict: 'pass',
    at: '2026-11-26T02:00:00Z',
    ...overrides,
  };
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
  // Same reason: the audit-trail read is its own queue. "Never reviewed" is the
  // honest default and the gateway's answer for a task with no decisions.
  api.loadTaskReviews.mockReset();
  api.loadTaskReviews.mockResolvedValue([]);
  // Same reason as the lease: a queued answer must not steer the next test.
  api.loadTaskTools.mockReset();
  // No binding unless a test says so: a task nobody bound tools to is the
  // honest default, and it is what the gateway answers with `{tools:null}`.
  api.loadTaskTools.mockResolvedValue(null);
  api.setTaskTools.mockReset();
  // The gateway echoes what it stored; a clear answers without a list.
  api.setTaskTools.mockImplementation(async (input: { id: string; tools: string[] }) => ({
    taskId: input.id,
    ...(input.tools.length > 0 ? { tools: [...input.tools] } : {}),
  }));
  // No claim unless a test says so: "no lease" is the honest default for a task
  // nobody has opened.
  api.loadTaskLease.mockResolvedValue(null);
  // Same reason again: a queued budget answer must not steer the next test. A
  // task with no ceiling is the honest default (`{budget:null}`).
  api.loadTaskBudget.mockReset();
  api.loadTaskBudget.mockResolvedValue(null);
  // Nothing is parked unless a test says so: a task with no held message is the
  // honest default (`{held:false}`), and it is what the block must stay silent on.
  api.loadPendingSend.mockReset();
  api.loadPendingSend.mockResolvedValue(null);
  api.approveSend.mockReset();
  // The gateway's answer to a release: the channel that took the message.
  api.approveSend.mockResolvedValue({ channel: 'feishu' });
  // Nobody is on record as the requester unless a test says so: that is what the
  // gateway answers with `{requestedBy:null}`, and it is the honest default for
  // a task dispatched before requesters were recorded.
  api.loadTaskRequester.mockReset();
  api.loadTaskRequester.mockResolvedValue(null);
  api.claimTask.mockResolvedValue(lease());
  api.submitTask.mockResolvedValue(lease({ state: 'review' }));
  api.reviewTask.mockResolvedValue(lease({ state: 'done' }));
  // An installed product with nothing installed yet: the block still answers,
  // and no unrelated test has to know about capabilities.
  api.loadCapabilities.mockResolvedValue({ skills: [], connectors: [], disabled: [] });
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
    // The line waits for the 待发送消息 read: while that answer is unknown the
    // screen must not claim there is nothing for this person to do.
    expect(await within(needYou).findByText(T('task.detail.needYou.none'))).toBeInTheDocument();
    expect(within(needYou).queryByRole('button', { name: label(T('task.detail.claim')) })).toBeNull();
  });

  it('claims with the task id as the executor role, then re-reads the board', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    // Opening the task: nobody holds it. After the claim the lease read is what
    // reports it — the same two reads, now with a claim in the second one.
    api.loadTaskLease.mockResolvedValueOnce(null).mockResolvedValue(lease());
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: label(T('task.detail.claim')) }));

    // Claiming acts as the executor role — the role that does the work.
    expect(api.claimTask).toHaveBeenCalledWith({ id: 'task-1', owner: 'executor' });
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
      owner: 'executor',
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
      owner: 'executor',
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

  it('accepts through the gateway with the auditor identity', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    const user = userEvent.setup();

    renderScreen();

    await user.click(await screen.findByRole('button', { name: label(T('task.detail.accept')) }));

    expect(api.reviewTask).toHaveBeenCalledWith({
      id: 'task-1',
      decision: 'accept',
      // Reviewing acts as the auditor role: in a single-user product the
      // reviewer≠executor rule can only mean "a different role".
      reviewer: 'auditor',
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
      reviewer: 'auditor',
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

  /**
   * 执行手段 (M5 visibility).
   *
   * The task screen is where a person asks "can this team actually do it?", so
   * the answer — skills and MCP connectors — belongs on it. What is asserted
   * here: the block reads `GET /api/capabilities` on its own, names what is
   * installed, keeps the gateway's own words for a broken connector, and never
   * takes the task down with it. The block lives inside §2, so the five-section
   * contract above is unchanged.
   */
  describe('执行手段', () => {
    /** §2, which is where the block nests (see the screen's own note there). */
    async function progress(): Promise<HTMLElement> {
      return screen.findByRole('region', { name: T('task.detail.section.progress') });
    }

    it('lists the skills and connectors this product can use', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadCapabilities.mockResolvedValue({
        skills: [skill('pdf', '读 PDF 里的表格'), skill('git', '查提交历史')],
        connectors: [connector('local-mcp', { tools: 2 })],
        disabled: [],
      });

      renderScreen();

      const means = within(await progress());
      // Name plus the one line that says what it does.
      expect(await means.findByText('pdf')).toBeInTheDocument();
      expect(means.getByText('读 PDF 里的表格')).toBeInTheDocument();
      expect(means.getByText('git')).toBeInTheDocument();
      // The connector and its state, which the gateway derived (round-2 C-04).
      // Scoped to the connector rows: the name is also a box in the binding
      // picker below (a connector row is "connected?", the box is "allowed here?").
      expect(within(means.getByTestId('means-connectors')).getByText('local-mcp')).toBeInTheDocument();
      expect(means.getByText(T('screen.capabilities.state.connected'))).toBeInTheDocument();
      // One read, and it is the capabilities read — not the board again.
      expect(api.loadCapabilities).toHaveBeenCalledTimes(1);
      expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
    });

    it('shows five skills and reveals the rest with 展开', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadCapabilities.mockResolvedValue({
        skills: Array.from({ length: 7 }, (_, index) => skill(`s${index + 1}`)),
        connectors: [],
        disabled: [],
      });
      const user = userEvent.setup();

      renderScreen();

      const means = within(await progress());
      // Five is the budget: enough to know what is there, not a wall of text.
      expect(await means.findByText('s5')).toBeInTheDocument();
      expect(means.queryByText('s6')).toBeNull();
      expect(means.queryByText('s7')).toBeNull();

      await user.click(means.getByRole('button', { name: label(T('task.detail.progress.more')) }));

      expect(means.getByText('s6')).toBeInTheDocument();
      expect(means.getByText('s7')).toBeInTheDocument();
    });

    it('shows an unavailable connector with the gateway’s own error text', async () => {
      const reason = 'dial tcp 127.0.0.1:9919: connect: connection refused';
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadCapabilities.mockResolvedValue({
        skills: [],
        connectors: [
          connector('broken', { state: 'failed', tools: 0, status: `连接失败: ${reason}`, error: reason }),
        ],
        disabled: [],
      });

      renderScreen();

      const means = within(await progress());
      // 不可用 is the state word; the reason is the gateway's sentence, verbatim.
      expect(await means.findByText('不可用')).toBeInTheDocument();
      expect(means.getByText(reason)).toBeInTheDocument();
      // A broken connector is never dressed up as a working one.
      expect(means.queryByText(T('screen.capabilities.state.connected'))).toBeNull();
    });

    it('lists disabled connectors on a line of their own', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadCapabilities.mockResolvedValue({
        skills: [],
        connectors: [connector('live')],
        disabled: ['old-mcp'],
      });

      renderScreen();

      const means = within(await progress());
      const disabledRow = await means.findByTestId('means-disabled');
      expect(within(disabledRow).getByText(T('screen.capabilities.state.disabled'))).toBeInTheDocument();
      expect(within(disabledRow).getByText('old-mcp')).toBeInTheDocument();
      // Two different statements: what is connected, and what is switched off.
      expect(within(disabledRow).queryByText('live')).toBeNull();
      // Connected, on the connector list (and, separately, offered for binding).
      expect(within(means.getByTestId('means-connectors')).getByText('live')).toBeInTheDocument();
    });

    it('says the product has no execution means at all when the API answers empty', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadCapabilities.mockResolvedValue({ skills: [], connectors: [], disabled: [] });

      renderScreen();

      const means = within(await progress());
      expect(await means.findByText('这个产品还没装任何执行手段')).toBeInTheDocument();
    });

    it('keeps the task on screen when capabilities fail to load', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadCapabilities.mockRejectedValue(new Error('能力服务连不上'));

      renderScreen();

      // The task itself is still there …
      expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
      expect(screen.getByText('对比三家竞品的退款政策')).toBeInTheDocument();
      // … and §2 says which half failed, with a way to ask again.
      const means = within(await progress());
      const alert = await means.findByRole('alert');
      expect(within(alert).getByText(T('screen.capabilities.loadFailed'))).toBeInTheDocument();
      expect(within(alert).getByText('能力服务连不上')).toBeInTheDocument();
      // The panel header carries the same failure, so a collapsed panel cannot
      // look like an installed product with nothing in it.
      expect(means.getAllByText(T('screen.capabilities.loadFailed')).length).toBe(2);
      expect(within(alert).getByRole('button', { name: label(T('task.detail.retry')) })).toBeInTheDocument();
      // Not the whole-screen failure.
      expect(screen.queryByText(T('task.detail.loadFailed'))).toBeNull();
    });

    /**
     * 工具绑定 — the execution policy of *this* task (`tasks.tools.get/set`, M5).
     *
     * What is offered: connector names only. `GET /api/capabilities` answers the
     * MCP servers it knows and a *count* of their tools, never the individual
     * tool names, so a picker that showed `mcp__<connector>__*` would be
     * inventing names the gateway never printed. The block says so in one line
     * instead. A name the task is already bound to is always offered, even when
     * capabilities does not list it, so 保存 can never silently drop it.
     */
    describe('工具绑定', () => {
      const BINDING_COPY = {
        unbound: '还没绑定工具',
      };
      const SAVE = T('settings.save');
      const TOOLS = 'browser_open';

      /** A product whose capabilities name two connectors and their counts. */
      function twoConnectors() {
        api.loadCapabilities.mockResolvedValue({
          skills: [],
          connectors: [connector('local-mcp', { tools: 2 }), connector('github', { tools: 7 })],
          disabled: [],
        });
      }

      it('renders the current binding as checked boxes', async () => {
        api.loadTaskBoard.mockResolvedValue([task()]);
        twoConnectors();
        api.loadTaskTools.mockResolvedValue({ taskId: 'task-1', tools: ['github'] });

        renderScreen();

        const means = within(await progress());
        expect(await means.findByRole('checkbox', { name: 'github' })).toBeChecked();
        expect(means.getByRole('checkbox', { name: 'local-mcp' })).not.toBeChecked();
        // The binding is this task's policy, read by id — not guessed from
        // capabilities, and not the whole board read again.
        expect(api.loadTaskTools).toHaveBeenCalledWith('task-1');
        expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
        // The connector-name note is there because names are all we have.
        expect(
          means.getByText('只能按连接器绑定：网关还没在 capabilities 里给出单个工具名'),
        ).toBeInTheDocument();
      });

      it('sends the exact full list on 保存, then re-reads the binding', async () => {
        api.loadTaskBoard.mockResolvedValue([task()]);
        twoConnectors();
        api.loadTaskTools
          .mockResolvedValueOnce({ taskId: 'task-1', tools: ['local-mcp'] })
          .mockResolvedValue({ taskId: 'task-1', tools: ['local-mcp', 'github'] });
        const user = userEvent.setup();

        renderScreen();

        const means = within(await progress());
        await user.click(await means.findByRole('checkbox', { name: 'github' }));
        await user.click(means.getByRole('button', { name: label(SAVE) }));

        // The whole list, in the order the boxes are shown — a rebind replaces,
        // it does not merge, so a partial list would drop what is not in it.
        expect(api.setTaskTools).toHaveBeenCalledWith({
          id: 'task-1',
          tools: ['local-mcp', 'github'],
        });
        expect(api.setTaskTools).toHaveBeenCalledTimes(1);
        // One re-read of the binding after the write: the boxes show what the
        // gateway stored, not what this component hoped it stored.
        await vi.waitFor(() => expect(api.loadTaskTools).toHaveBeenCalledTimes(2));
        expect(means.getByRole('checkbox', { name: 'github' })).toBeChecked();
      });

      it('sends [] on 清除 and reports the task as unbound again', async () => {
        api.loadTaskBoard.mockResolvedValue([task()]);
        twoConnectors();
        api.loadTaskTools
          .mockResolvedValueOnce({ taskId: 'task-1', tools: [TOOLS] })
          .mockResolvedValue(null);
        const user = userEvent.setup();

        renderScreen();

        const means = within(await progress());
        // A bound name capabilities never listed (individual tool names are not
        // in that payload) is still on the box list while it is bound.
        expect(await means.findByRole('checkbox', { name: TOOLS })).toBeChecked();

        await user.click(means.getByRole('button', { name: label('清除') }));

        expect(api.setTaskTools).toHaveBeenCalledWith({ id: 'task-1', tools: [] });
        await vi.waitFor(() => expect(api.loadTaskTools).toHaveBeenCalledTimes(2));
        expect(await means.findByText(BINDING_COPY.unbound)).toBeInTheDocument();
      });

      it('keeps the previous binding when the write fails', async () => {
        api.loadTaskBoard.mockResolvedValue([task()]);
        twoConnectors();
        api.loadTaskTools.mockResolvedValue({ taskId: 'task-1', tools: ['local-mcp'] });
        api.setTaskTools.mockRejectedValue(new Error('tasklease: task tools invalid'));
        const user = userEvent.setup();

        renderScreen();

        const means = within(await progress());
        const box = await means.findByRole('checkbox', { name: 'local-mcp' });
        await user.click(box);
        // The edit is in the boxes …
        expect(box).not.toBeChecked();

        await user.click(means.getByRole('button', { name: label(SAVE) }));

        // … the failure is reported in the gateway's own words …
        expect(await means.findByText('tasklease: task tools invalid')).toBeInTheDocument();
        // … and the boxes go back to the binding the gateway still holds: a
        // policy the gateway rejected must not look applied.
        await vi.waitFor(() => expect(box).toBeChecked());
        // No re-read: a failed write changed nothing.
        expect(api.loadTaskTools).toHaveBeenCalledTimes(1);
      });

      it('says 还没绑定工具 for a task with no policy', async () => {
        api.loadTaskBoard.mockResolvedValue([task()]);
        twoConnectors();

        renderScreen();

        const means = within(await progress());
        // Scoped to the binding block: 还没绑定工具 is about this task's policy,
        // not about the product having nothing installed.
        const binding = within(means.getByTestId('tools-binding'));
        expect(await binding.findByText(BINDING_COPY.unbound)).toBeInTheDocument();
        // Unbound is not "nothing to offer": the boxes are there to bind with.
        expect(binding.getByRole('checkbox', { name: 'github' })).not.toBeChecked();
      });

      it('allows one write in flight at a time', async () => {
        api.loadTaskBoard.mockResolvedValue([task()]);
        twoConnectors();
        api.loadTaskTools.mockResolvedValue({ taskId: 'task-1', tools: ['local-mcp'] });
        // Never settles: the point is what the buttons do *while* it is in the air.
        api.setTaskTools.mockReturnValue(new Promise(() => {}));
        const user = userEvent.setup();

        renderScreen();

        const means = within(await progress());
        await user.click(await means.findByRole('checkbox', { name: 'github' }));
        await user.click(means.getByRole('button', { name: label(SAVE) }));

        // antd renders a loading icon with `aria-label="loading"`, which joins the
        // button's accessible name — so the button is matched without anchors.
        expect(
          means.getByRole('button', { name: new RegExp([...SAVE].join('\\s*')) }),
        ).toBeDisabled();
        expect(means.getByRole('button', { name: label('清除') })).toBeDisabled();
        expect(means.getByRole('checkbox', { name: 'github' })).toBeDisabled();
        expect(api.setTaskTools).toHaveBeenCalledTimes(1);
      });
    });

    it('re-reads the capabilities on 重试 without re-reading the board', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      // A flag rather than a single-shot rejection: under load the component may
      // read twice before the click, and a queue-based mock then hands the second
      // read a success — leaving no alert to click. The flag makes the failure
      // last exactly as long as the test says it does.
      let failing = true;
      api.loadCapabilities.mockImplementation(() =>
        failing
          ? Promise.reject(new Error('能力服务连不上'))
          : Promise.resolve({ skills: [skill('pdf')], connectors: [], disabled: [] }),
      );
      const user = userEvent.setup();

      renderScreen();

      const region = within(await progress()).getByTestId('means-capabilities');
      await within(region).findByText('能力服务连不上');
      failing = false;
      // Count before the click: under load React may mount twice, and asserting an
      // absolute number made this test depend on how many mounts happened.
      const readsBeforeRetry = api.loadCapabilities.mock.calls.length;
      await user.click(within(region).getByRole('button', { name: label(T('task.detail.retry')) }));

      expect(await within(region).findByText('pdf')).toBeInTheDocument();
      // The retry promises one thing: exactly one more read.
      expect(api.loadCapabilities.mock.calls.length).toBe(readsBeforeRetry + 1);
      expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
      // The failure is no longer on screen.
      expect(within(region).queryByText('能力服务连不上')).toBeNull();
    });
  });

  /**
   * 预算 (G11 cost ceiling).
   *
   * A bound session reports real token usage and the task stops when it crosses
   * its ceiling, so the screen owes the reader two facts: how much of the
   * ceiling is spent, and — when it is spent — that the task has stopped and
   * what unblocks it. It also owns the ceiling's own write (`tasks.budget.set`):
   * a dispatched task is created with a 200k/30min row, and until 2026-11-26 no
   * client could change it. There is still no 继续 button — resuming is the
   * gate's job once the ceiling allows it — but "adjust the limit" is not the
   * same move as "skip the stop", and only the first one belongs here.
   *
   * It is a read of its own (`tasks.budget.get`), with its own failure: a budget
   * that did not load must not take the task around it down, and "no ceiling"
   * (`{budget:null}`) is an answer, not a failure.
   */
  describe('预算', () => {
    /** The block's own testid, so every assertion is scoped to it. */
    async function budget(): Promise<HTMLElement> {
      return screen.findByTestId('task-budget');
    }

    /** The catalogue's own number formatting, so the assertion is locale-free. */
    const n = (value: number) => value.toLocaleString();

    it('says 这个任务没有预算上限 when the task has no budget row', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget.mockResolvedValue(null);

      renderScreen();

      const block = within(await budget());
      expect(await block.findByText('这个任务没有预算上限。')).toBeInTheDocument();
      // Nothing was measured, so nothing is drawn: no bar, no alert, no retry.
      expect(block.queryByRole('progressbar')).toBeNull();
      expect(block.queryByRole('alert')).toBeNull();
      // No 继续: a stop is cleared through the ceiling and the gate, not by a
      // button here. The ceiling itself IS writable from this block.
      expect(block.queryByRole('button', { name: /继\s*续/ })).toBeNull();
      expect(block.getByRole('button', { name: label(T('task.detail.budget.adjust')) })).toBeInTheDocument();
      expect(api.loadTaskBudget).toHaveBeenCalledWith('task-1');
      // The budget is its own read; the board is not read again for it.
      expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
    });

    /**
     * 追加预算 (2026-11-26 audit, 高): the read-only block told the reader that
     * topping up "lives on the surface that owns that decision" — and no such
     * surface existed. `tasks.budget.set` had zero callers in `web/src`, so the
     * 200k/30min row a dispatched task gets automatically could be seen and
     * never changed.
     *
     * The entry is deliberately two-step (保存 then a confirmation): raising a
     * ceiling changes when the task stops, and that is a decision, not a field
     * edit. A refusal (the gateway guards this write with a permission action) is
     * shown inside the block, in the gateway's own words.
     */
    it('adjusts the ceiling through tasks.budget.set after an explicit confirmation', async () => {
      api.loadTaskBoard.mockResolvedValue([task({ status: 'in_progress' })]);
      api.loadTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 200000,
        maxMinutes: 30,
        usedTokens: 1000,
        exhausted: false,
      });
      api.setTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 500000,
        maxMinutes: 30,
        usedTokens: 1000,
        exhausted: false,
      });
      const user = userEvent.setup();

      renderScreen();

      const block = within(await budget());
      await block.findByText(`已用 ${n(1000)} / 上限 ${n(200000)} tokens`);

      await user.click(block.getByRole('button', { name: label(T('task.detail.budget.adjust')) }));
      // The form starts from what the row says, not from blanks.
      const tokens = block.getByLabelText(T('task.detail.budget.maxTokens')) as HTMLInputElement;
      expect(tokens.value).toBe('200000');
      await user.clear(tokens);
      await user.type(tokens, '500000');
      await user.click(block.getByRole('button', { name: label(T('task.detail.budget.save')) }));

      // Nothing is written yet: the confirmation is the decision.
      expect(api.setTaskBudget).not.toHaveBeenCalled();
      await user.click(
        await screen.findByRole('button', { name: label(T('task.detail.budget.confirmAdjust')) }),
      );

      await waitFor(() =>
        expect(api.setTaskBudget).toHaveBeenCalledWith({ id: 'task-1', maxTokens: 500000, maxMinutes: 30 }),
      );
      // The block redraws from the write's own answer, so the new ceiling is the
      // one on screen — no stale 200k under a fresh 500k.
      expect(await block.findByText(`已用 ${n(1000)} / 上限 ${n(500000)} tokens`)).toBeInTheDocument();
    });

    it('refuses an empty or negative ceiling instead of writing 0', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 1000,
        maxMinutes: 0,
        usedTokens: 10,
        exhausted: false,
      });
      const user = userEvent.setup();

      renderScreen();

      const block = within(await budget());
      await user.click(await block.findByRole('button', { name: label(T('task.detail.budget.adjust')) }));
      const tokens = block.getByLabelText(T('task.detail.budget.maxTokens')) as HTMLInputElement;
      await user.clear(tokens);

      // An empty box is "I have not decided", never a ceiling of zero.
      const save = block.getByRole('button', { name: label(T('task.detail.budget.save')) });
      expect(save).toBeDisabled();
      await waitFor(() => expect(block.getByText(T('task.detail.budget.invalid'))).toBeInTheDocument());
      expect(api.setTaskBudget).not.toHaveBeenCalled();
    });

    it('shows a refused budget write inside the block, in the gateway words', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 200000,
        maxMinutes: 30,
        usedTokens: 1000,
        exhausted: true,
      });
      api.setTaskBudget.mockRejectedValue(new Error('没有权限调整预算'));
      const user = userEvent.setup();

      renderScreen();

      const block = within(await budget());
      await user.click(await block.findByRole('button', { name: label(T('task.detail.budget.adjust')) }));
      const minutes = block.getByLabelText(T('task.detail.budget.maxMinutes')) as HTMLInputElement;
      await user.clear(minutes);
      await user.type(minutes, '60');
      await user.click(block.getByRole('button', { name: label(T('task.detail.budget.save')) }));
      await user.click(
        await screen.findByRole('button', { name: label(T('task.detail.budget.confirmAdjust')) }),
      );

      const alert = await block.findByRole('alert');
      expect(within(alert).getByText(T('task.detail.budget.saveFailed'))).toBeInTheDocument();
      // The gateway's refusal, not a generic "failed": the user has to know it
      // was not allowed, not that the network went away.
      expect(within(alert).getByText('没有权限调整预算')).toBeInTheDocument();
      // The old ceiling is still the one in force.
      expect(await block.findByText(`已用 ${n(1000)} / 上限 ${n(200000)} tokens`)).toBeInTheDocument();
    });

    it('shows used over limit and a bar whose aria-valuenow is the used percentage', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 1000,
        maxMinutes: 0,
        usedTokens: 440,
        exhausted: false,
      });

      renderScreen();

      const block = within(await budget());
      expect(
        await block.findByText(`已用 ${n(440)} / 上限 ${n(1000)} tokens`),
      ).toBeInTheDocument();
      const bar = block.getByRole('progressbar');
      expect(bar).toHaveAttribute('aria-valuenow', '44');
      // Still going: the bar keeps the in-progress semantic, not the error one.
      expect(bar.className).toContain('ant-progress-status-normal');
      expect(bar.className).not.toContain('ant-progress-status-exception');
      // The stop line belongs to an exhausted budget only.
      expect(block.queryByText('任务已停止，需要追加预算才能继续')).toBeNull();
    });

    it('also shows the minutes when the budget has a time ceiling', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 1000,
        maxMinutes: 60,
        usedTokens: 10,
        // The clock a minute ceiling runs on: 25 minutes spent so far.
        startedAt: new Date(Date.now() - 25 * 60_000).toISOString(),
        exhausted: false,
      });

      renderScreen();

      const block = within(await budget());
      expect(await block.findByText(`已用 25 分钟 / 上限 60 分钟`)).toBeInTheDocument();
    });

    it('says the stop in one line and turns the bar to the error semantic once exhausted', async () => {
      api.loadTaskBoard.mockResolvedValue([task({ status: 'in_progress' })]);
      api.loadTaskBudget.mockResolvedValue({
        taskId: 'task-1',
        maxTokens: 1000,
        maxMinutes: 0,
        usedTokens: 1000,
        exhausted: true,
      });

      renderScreen();

      const block = within(await budget());
      expect(await block.findByText('任务已停止，需要追加预算才能继续')).toBeInTheDocument();
      const bar = block.getByRole('progressbar');
      expect(bar).toHaveAttribute('aria-valuenow', '100');
      // The stop is the error semantic (the same colour an error uses
      // everywhere else) — not a decorative red.
      expect(bar.className).toContain('ant-progress-status-exception');
      // The stop is not a button to press: raising the ceiling is
      // (`tasks.budget.set`), resuming is the gate's decision afterwards.
      expect(block.queryByRole('button', { name: /继\s*续/ })).toBeNull();
      expect(block.getByRole('button', { name: label(T('task.detail.budget.adjust')) })).toBeInTheDocument();
    });

    it('keeps the failure inside the block and re-reads only the budget on 重试', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget
        .mockRejectedValueOnce(new Error('预算服务连不上'))
        .mockResolvedValue(null);
      const user = userEvent.setup();

      renderScreen();

      const block = within(await budget());
      const alert = await block.findByRole('alert');
      expect(within(alert).getByText(T('task.detail.budget.loadFailed'))).toBeInTheDocument();
      // The gateway's own words, not a guess of ours.
      expect(within(alert).getByText('预算服务连不上')).toBeInTheDocument();

      const readsBeforeRetry = api.loadTaskBudget.mock.calls.length;
      await user.click(within(alert).getByRole('button', { name: label(T('task.detail.retry')) }));

      expect(await block.findByText('这个任务没有预算上限。')).toBeInTheDocument();
      // The retry promises one thing: exactly one more budget read.
      expect(api.loadTaskBudget.mock.calls.length).toBe(readsBeforeRetry + 1);
      expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
      expect(block.queryByRole('alert')).toBeNull();
    });

    it('keeps the whole task on screen when only the budget read fails', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskBudget.mockRejectedValue(new Error('预算服务连不上'));

      renderScreen();

      // The task itself rendered: its title, its goal and its five sections …
      expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
      expect(screen.getByText('对比三家竞品的退款政策')).toBeInTheDocument();
      expect(sectionHeadings()).toEqual([
        T('task.detail.section.doing'),
        T('task.detail.section.progress'),
        T('task.detail.section.evidence'),
        T('task.detail.section.artifacts'),
        T('task.detail.section.needYou'),
      ]);
      // … the four verbs still work …
      expect(screen.getByRole('button', { name: label(T('task.detail.claim')) })).toBeEnabled();
      // … the failure is the budget's own, inside the block …
      const element = await budget();
      const block = within(element);
      expect(within(block.getByRole('alert')).getByText('预算服务连不上')).toBeInTheDocument();
      // … and the evidence half is untouched: not loaded ≠ failed.
      const evidence = screen.getByRole('region', { name: T('task.detail.section.evidence') });
      expect(within(evidence).queryByRole('alert')).toBeNull();
      // Not the whole-screen failure, and no false "no ceiling".
      expect(screen.queryByText(T('task.detail.loadFailed'))).toBeNull();
      expect(within(element).queryByText('这个任务没有预算上限。')).toBeNull();
    });
  });

  /**
   * 待发送消息 (the outbound half of `approval=before_send`).
   *
   * A settled task parks the message it wants to send instead of sending it, so
   * the screen owes the person two things: the **exact words** that would go out
   * (it is what would be said in public — never paraphrased, never truncated) and
   * one verb that lets them leave. The approval is one write of exactly
   * `{id,approver}` followed by a re-read, because the hold is what the read
   * answers and the write is what empties it.
   *
   * It is a read of its own (`tasks.send.pending`), with its own failure: a read
   * that failed must not take the task down, and it must not be confused with
   * "nothing is waiting". Nothing held is the **absence** of the block — no empty
   * scaffolding, because a box that says "no message waiting" on every task is
   * noise on every task.
   */
  describe('待发送消息', () => {
    /** The words the gateway parked — with a newline, so verbatim means something. */
    const HELD = '退款率 3.1%，比上季度低 0.4 个点。\n报告：https://example.test/r/1';

    /** The block's own testid: every assertion below is scoped to it. */
    async function block(): Promise<HTMLElement> {
      return screen.findByTestId('task-pending-send');
    }

    /** A write that has not answered yet, so "in flight" can be observed. */
    function deferred<T>() {
      let resolve!: (value: T) => void;
      const promise = new Promise<T>((done) => {
        resolve = done;
      });
      return { promise, resolve };
    }

    it('is not on screen at all when nothing is held', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadPendingSend.mockResolvedValue(null);

      renderScreen();

      // The task rendered, and the block's own read answered "nothing" …
      expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
      await waitFor(() => expect(api.loadPendingSend).toHaveBeenCalledWith('task-1'));
      // … so there is no title, no box and no verb: no empty scaffolding.
      expect(screen.queryByTestId('task-pending-send')).toBeNull();
      expect(screen.queryByText(T('task.detail.send.title'))).toBeNull();
      expect(
        screen.queryByRole('button', { name: label(T('task.detail.send.approve')) }),
      ).toBeNull();
    });

    it('shows the held words verbatim, newlines included', async () => {
      api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
      api.loadPendingSend.mockResolvedValue({ text: HELD });

      renderScreen();

      const element = await block();
      expect(within(element).getByText(T('task.detail.send.title'))).toBeInTheDocument();
      // The words themselves, exactly — not a summary, not a trimmed version.
      expect(within(element).getByTestId('task-send-text').textContent).toBe(HELD);
      // The newline is preserved by the style the block sets, so the words read
      // as the gateway wrote them.
      const body = within(element).getByTestId('task-send-text');
      expect(body.style.whiteSpace).toBe('pre-wrap');
    });

    it('says nothing of 不需要你做什么 while a message is waiting', async () => {
      api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
      api.loadPendingSend.mockResolvedValue({ text: HELD });

      renderScreen();

      await block();
      expect(screen.queryByText(T('task.detail.needYou.none'))).toBeNull();
    });

    it('sends exactly {id, approver:"lead"} after the confirm, then re-reads and drops the text', async () => {
      const user = userEvent.setup();
      api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
      api.loadPendingSend.mockResolvedValueOnce({ text: HELD }).mockResolvedValueOnce(null);
      api.approveSend.mockResolvedValue({ channel: 'feishu' });

      renderScreen();

      const element = await block();
      await user.click(within(element).getByTestId('task-send-approve'));
      // Opening the question is not an answer.
      expect(api.approveSend).not.toHaveBeenCalled();
      // The question states that the message will be sent.
      expect(await screen.findByText(T('task.detail.send.confirm'))).toBeInTheDocument();

      await user.click(
        await screen.findByRole('button', { name: label(T('task.detail.send.confirmOk')) }),
      );

      await waitFor(() => expect(api.approveSend).toHaveBeenCalledTimes(1));
      expect(api.approveSend.mock.calls[0][0]).toEqual({ id: 'task-1', approver: 'lead' });

      // The write is followed by a read of the hold it emptied.
      await waitFor(() => expect(api.loadPendingSend).toHaveBeenCalledTimes(2));
      // The held text is gone …
      await waitFor(() => expect(screen.queryByTestId('task-pending-send')).toBeNull());
      // … and the receipt says which channel took it.
      expect(screen.getByTestId('task-send-receipt')).toHaveTextContent(
        T('task.detail.send.sent', { channel: 'feishu' }),
      );
      // The task is settled and nothing waits, so §5 may now say so.
      expect(screen.getByText(T('task.detail.needYou.none'))).toBeInTheDocument();
    });

    it('keeps the text and prints the gateway own words when the send fails', async () => {
      const user = userEvent.setup();
      api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
      api.loadPendingSend.mockResolvedValue({ text: HELD });
      api.approveSend.mockRejectedValue(new Error('channel dispatch: no channel configured'));

      renderScreen();

      const element = await block();
      await user.click(within(element).getByTestId('task-send-approve'));
      await user.click(
        await screen.findByRole('button', { name: label(T('task.detail.send.confirmOk')) }),
      );

      const alert = await within(element).findByRole('alert');
      // The gateway's own words, not a guess of ours.
      expect(within(alert).getByText('channel dispatch: no channel configured')).toBeInTheDocument();
      // The words are still on screen, unchanged: the message did not leave.
      expect(within(element).getByTestId('task-send-text').textContent).toBe(HELD);
      // A failed write changed nothing, so nothing is read again …
      expect(api.loadPendingSend).toHaveBeenCalledTimes(1);
      // … there is no receipt to show …
      expect(screen.queryByTestId('task-send-receipt')).toBeNull();
      // … and the verb is offered again.
      expect(within(element).getByTestId('task-send-approve')).toBeEnabled();
    });

    it('allows one send in flight: the verb is disabled and a second click sends nothing', async () => {
      const user = userEvent.setup();
      const inFlight = deferred<{ channel: string }>();
      api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
      api.loadPendingSend.mockResolvedValueOnce({ text: HELD }).mockResolvedValueOnce(null);
      api.approveSend.mockReturnValue(inFlight.promise);

      renderScreen();

      const element = await block();
      await user.click(within(element).getByTestId('task-send-approve'));
      await user.click(
        await screen.findByRole('button', { name: label(T('task.detail.send.confirmOk')) }),
      );

      await waitFor(() => expect(api.approveSend).toHaveBeenCalledTimes(1));
      const approve = within(element).getByTestId('task-send-approve');
      expect(approve).toBeDisabled();
      await user.click(approve);
      expect(api.approveSend).toHaveBeenCalledTimes(1);

      inFlight.resolve({ channel: 'feishu' });
      await waitFor(() => expect(api.loadPendingSend).toHaveBeenCalledTimes(2));
    });

    it('keeps a failed read inside the block and re-reads only the held message on 重试', async () => {
      const user = userEvent.setup();
      api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
      api.loadPendingSend
        .mockRejectedValueOnce(new Error('held sends unavailable'))
        .mockResolvedValue({ text: HELD });

      renderScreen();

      const element = await block();
      const alert = await within(element).findByRole('alert');
      expect(within(alert).getByText(T('task.detail.send.loadFailed'))).toBeInTheDocument();
      // "It did not load" is not "nothing is waiting": no false silence.
      expect(within(alert).getByText('held sends unavailable')).toBeInTheDocument();

      await user.click(within(alert).getByRole('button', { name: label(T('task.detail.retry')) }));

      expect(within(await block()).getByTestId('task-send-text').textContent).toBe(HELD);
      // The retry promises one thing: exactly one more read of the hold.
      expect(api.loadPendingSend).toHaveBeenCalledTimes(2);
      expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
    });
  });

  /**
   * 提出人 — which human asked for the task, in one line of the header.
   *
   * The board records the work and the lease records the executor; the gateway
   * now records the person who asked for it (`tasks.requestedby.get`), and until
   * this line existed the screen never said who. Three answers, and they are not
   * interchangeable: a name (`提出人 alice`), nobody on record (`提出人未知`) and a
   * read that failed (**no line at all** — a failed attribution must never read
   * as "unknown", because that would be a claim this read could not make).
   *
   * The line lives in the header, not in a sixth section: the five sections are a
   * contract (design §5.4). It appears only once the read has answered, so there
   * is no placeholder line for real copy to shift down.
   */
  describe('提出人', () => {
    it('names the human who asked for it', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskRequester.mockResolvedValue('alice');

      renderScreen();

      const line = await screen.findByTestId('task-requester');
      expect(line).toHaveTextContent(T('task.detail.requester.known', { name: 'alice' }));
      expect(api.loadTaskRequester).toHaveBeenCalledWith('task-1');
      // It belongs to the header: an attribution is not a section of its own.
      const header = document.querySelector('.zk-screen__header');
      expect(header).not.toBeNull();
      expect(header).toContainElement(line);
    });

    it('says 提出人未知 when the gateway has nobody on record', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskRequester.mockResolvedValue(null);

      renderScreen();

      expect(await screen.findByTestId('task-requester')).toHaveTextContent(
        T('task.detail.requester.unknown'),
      );
    });

    it('renders no line at all when the read fails — never 提出人未知', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskRequester.mockRejectedValue(new Error('requestedby unavailable'));
      const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
      try {
        renderScreen();

        // The task is untouched: this read cannot take the screen down, and it
        // throws nothing at the person.
        expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
        expect(screen.queryByTestId('task-requester')).toBeNull();
        expect(screen.queryByText(T('task.detail.requester.unknown'))).toBeNull();
        // Silent on screen, but the reason is not swallowed.
        const logged = debug.mock.calls
          .map((call) => call.map((part) => String(part)).join(' '))
          .join('\n');
        expect(logged).toContain('requestedby unavailable');
      } finally {
        debug.mockRestore();
      }
    });

    it('renders nothing while the read is in the air — no placeholder line', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      // Never answers: whatever is on screen now is what the resolved read has
      // not yet earned.
      api.loadTaskRequester.mockReturnValue(new Promise(() => {}));

      renderScreen();

      expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('查退款率');
      expect(screen.queryByTestId('task-requester')).toBeNull();
      expect(document.querySelectorAll('.zk-screen__header p')).toHaveLength(0);
    });

    it('keeps the five-section contract exactly five with the line present', async () => {
      api.loadTaskBoard.mockResolvedValue([task()]);
      api.loadTaskRequester.mockResolvedValue('alice');

      renderScreen();

      // The line landed …
      expect(await screen.findByTestId('task-requester')).toBeInTheDocument();
      // … and the page is still the five sections, in the contract order: the
      // attribution did not become a sixth one.
      const headings = sectionHeadings();
      expect(headings).toHaveLength(5);
      expect(headings).toEqual([
        T('task.detail.section.doing'),
        T('task.detail.section.progress'),
        T('task.detail.section.evidence'),
        T('task.detail.section.artifacts'),
        T('task.detail.section.needYou'),
      ]);
    });
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

  it('shows the recorded audit conclusion after the review deleted the lease', async () => {
    // The state a review leaves behind: the board says completed, `tasks.lease`
    // answers null (the decision deleted the claim), and the audit trail is the
    // only record left. Before the review log existed the screen showed an
    // accepted task as if nobody had ever looked at it.
    api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
    api.loadTaskLease.mockResolvedValue(null);
    api.loadTaskReviews.mockResolvedValue([
      review({ decision: 'reject', verdict: 'fail', findings: '少了第三家' }),
      review({ findings: '证据齐全' }),
    ]);

    renderScreen();

    expect(
      await screen.findByText(
        T('task.detail.progress.acceptedBy', { reviewer: T('task.detail.role.auditor') }),
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        T('task.detail.progress.rejectedBy', { reviewer: T('task.detail.role.auditor') }),
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('少了第三家')).toBeInTheDocument();
    expect(screen.getByText('证据齐全')).toBeInTheDocument();
  });

  it('renders a direct close as unaudited instead of borrowing the accepted wording', async () => {
    // The gateway records `direct_close` + verdict `unaudited` when a task was
    // completed without a lease review (`task.status` or `team_task_update`).
    // Showing that as "验收通过" would claim an audit that never happened.
    api.loadTaskBoard.mockResolvedValue([task({ status: 'completed' })]);
    api.loadTaskLease.mockResolvedValue(null);
    api.loadTaskReviews.mockResolvedValue([
      review({
        reviewer: 'user',
        role: undefined,
        decision: 'direct_close',
        verdict: 'unaudited',
        findings: 'closed via tasks.status, no lease review (lease state: review, evidence: 1)',
      }),
    ]);

    renderScreen();

    const progress = await screen.findByRole('region', { name: T('task.detail.section.progress') });
    expect(await within(progress).findByText(T('task.detail.progress.directClose'))).toBeInTheDocument();
    expect(
      within(progress).getByText(/closed via tasks\.status, no lease review/),
    ).toBeInTheDocument();
    // The reviewed wording must not appear: nobody accepted this task.
    expect(
      within(progress).queryByText(
        T('task.detail.progress.acceptedBy', { reviewer: T('task.detail.role.auditor') }),
      ),
    ).toBeNull();
  });

  it('says the audit trail failed to load instead of implying nobody reviewed it', async () => {
    api.loadTaskBoard.mockResolvedValue([task()]);
    api.loadTaskReviews.mockRejectedValue(new Error('tasks.reviews: unavailable'));

    renderScreen();

    const progress = await screen.findByRole('region', { name: T('task.detail.section.progress') });
    expect(await within(progress).findByText(T('task.detail.reviews.failed'))).toBeInTheDocument();
    expect(within(progress).getByText('tasks.reviews: unavailable')).toBeInTheDocument();
  });
});
