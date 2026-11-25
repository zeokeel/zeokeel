import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConnectorSummary, SkillSummary } from '@/api/read-models';
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
  // 工具绑定 reads and writes its own row on the task (`tasks.tools.get/set`).
  loadTaskTools: vi.fn(),
  setTaskTools: vi.fn(),
  // The 执行手段 block reads this on its own; a failure here is not a failure
  // of the task, so it is a mock of its own.
  loadCapabilities: vi.fn(),
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
  loadTaskTools: api.loadTaskTools,
  setTaskTools: api.setTaskTools,
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
        expect(await means.findByText(BINDING_COPY.unbound)).toBeInTheDocument();
        // Unbound is not "nothing to offer": the boxes are there to bind with.
        expect(means.getByRole('checkbox', { name: 'github' })).not.toBeChecked();
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
      api.loadCapabilities
        .mockRejectedValueOnce(new Error('能力服务连不上'))
        .mockResolvedValue({ skills: [skill('pdf')], connectors: [], disabled: [] });
      const user = userEvent.setup();

      renderScreen();

      const region = await progress();
      await user.click(within(region).getByRole('button', { name: label(T('task.detail.retry')) }));

      expect(await within(region).findByText('pdf')).toBeInTheDocument();
      // The retry promises one thing: this read happens again.
      expect(api.loadCapabilities).toHaveBeenCalledTimes(2);
      expect(api.loadTaskBoard).toHaveBeenCalledTimes(1);
      // The failure is no longer on screen.
      expect(within(region).queryByText('能力服务连不上')).toBeNull();
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
});
