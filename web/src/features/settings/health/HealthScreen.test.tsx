import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nProvider } from '@/i18n';

/**
 * R27b · the run-status screen (heartbeat + browser).
 *
 * These tests pin the parts that make a status page trustworthy:
 *  1. every number comes from the API — nothing is computed or accumulated here;
 *  2. 立即查一次 really is `heartbeat.tick` **then** a re-read, in that order —
 *     one write, one read of the state it produced;
 *  3. a failed read names its cause and 重试 is a real second read;
 *  4. a disconnected browser is an honest empty state, not an error and not a
 *     zeroed endpoint;
 *  5. the loop controls send **only the field that changed** and apply the
 *     answer they get back, and a refused write leaves the controls on the
 *     gateway's last known value instead of showing a state nobody accepted.
 *
 * No timers are faked: the screen has no polling, so every assertion is about
 * one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  loadSandboxStatus: vi.fn(),
  loadHeartbeatStatus: vi.fn(),
  tickHeartbeat: vi.fn(),
  configureHeartbeat: vi.fn(),
  loadBrowserStatus: vi.fn(),
  planNotification: vi.fn(),
  deliverNotification: vi.fn(),
}));

vi.mock('@/api/health', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/health')>();
  return { ...actual, ...api };
});

vi.mock('@/api/sandbox', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/sandbox')>();
  return { ...actual, loadSandboxStatus: api.loadSandboxStatus };
});

import { HealthScreen } from './HealthScreen';

function renderScreen() {
  return render(
    <I18nProvider>
      <HealthScreen />
    </I18nProvider>,
  );
}

/** A tick that stays in flight until the test resolves it (the in-flight state). */
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

const CLEAN_REPORT = {
  leadRuns: 1,
  dispatched: 2,
  polls: 3,
  newMessages: 4,
  errors: undefined,
};

/** The status a read *or* a `heartbeat.configure` answers with (same DTO). */
type HeartbeatView = {
  running: boolean;
  leadIntervalMs: number;
  channelIntervalMs: number;
  lastTick?: string;
  leadRuns: number;
  dispatched: number;
  polls: number;
  newMessages: number;
};

function heartbeatStatus(overrides: Partial<HeartbeatView> = {}): HeartbeatView {
  return {
    running: true,
    leadIntervalMs: 30_000,
    channelIntervalMs: 60_000,
    leadRuns: 1,
    dispatched: 2,
    polls: 3,
    newMessages: 4,
    ...overrides,
  };
}

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  api.loadHeartbeatStatus.mockResolvedValue({
    running: true,
    leadIntervalMs: 30_000,
    channelIntervalMs: 60_000,
    lastTick: new Date(Date.now() - 125_000).toISOString(),
    leadRuns: 1,
    dispatched: 2,
    polls: 3,
    newMessages: 4,
  });
  api.tickHeartbeat.mockResolvedValue(CLEAN_REPORT);
  api.configureHeartbeat.mockResolvedValue(heartbeatStatus());
  api.loadBrowserStatus.mockResolvedValue({
    connected: true,
    endpoint: 'http://127.0.0.1:9222',
    pages: 3,
  });
  // Default: a host WITH an OS runner (the quiet state). The unconfined cases
  // below override it explicitly.
  api.loadSandboxStatus.mockResolvedValue({
    available: true,
    platform: 'linux',
    confinement: 'os',
    commandsUnconfined: false,
  });
  // The delivery section starts from a clean plan/receipt: each test that cares
  // gives the answer it wants to pin.
  api.planNotification.mockResolvedValue({ channels: ['inapp'], deferred: false });
  api.deliverNotification.mockResolvedValue({ channel: 'inapp', deferred: false });
});

describe('HealthScreen — both sections render what their API returned', () => {
  it('shows the heartbeat in human terms', async () => {
    renderScreen();

    const running = await screen.findByTestId('health-heartbeat-running');
    expect(running).toHaveTextContent(/运行中|Running/);

    // Intervals are read in seconds/minutes, not raw milliseconds.
    expect(screen.getByTestId('health-heartbeat-leadInterval')).toHaveTextContent(/30 秒|every 30 s/);
    expect(screen.getByTestId('health-heartbeat-channelInterval')).toHaveTextContent(/1 分钟|every 1 min/);
    // The last tick is a relative stamp, not a timestamp the reader must decode.
    expect(screen.getByTestId('health-heartbeat-lastTick')).toHaveTextContent(/2 分钟前|2 min ago/);

    // The last pass's counts, exactly as the gateway reported them.
    expect(screen.getByTestId('health-count-leadRuns')).toHaveTextContent('1');
    expect(screen.getByTestId('health-count-dispatched')).toHaveTextContent('2');
    expect(screen.getByTestId('health-count-polls')).toHaveTextContent('3');
    expect(screen.getByTestId('health-count-newMessages')).toHaveTextContent('4');

    // One read each, no polling.
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(1);
    expect(api.loadBrowserStatus).toHaveBeenCalledTimes(1);
  });

  it('shows the browser endpoint and page count when connected', async () => {
    renderScreen();

    expect(await screen.findByTestId('health-browser-state')).toHaveTextContent(/已连接|Connected/);
    expect(screen.getByTestId('health-browser-endpoint')).toHaveTextContent(
      'http://127.0.0.1:9222',
    );
    expect(screen.getByTestId('health-browser-pages')).toHaveTextContent('3');
    expect(screen.queryByTestId('health-browser-empty')).toBeNull();
  });

  it('lists the errors of the last pass instead of hiding them', async () => {
    api.loadHeartbeatStatus.mockResolvedValue({
      running: true,
      leadIntervalMs: 30_000,
      channelIntervalMs: 60_000,
      leadRuns: 0,
      dispatched: 0,
      polls: 0,
      newMessages: 0,
      errors: ['lead: no provider', 'poll: feishu timeout'],
    });
    renderScreen();

    const errors = await screen.findByTestId('health-heartbeat-errors');
    expect(within(errors).getAllByTestId('health-heartbeat-error')).toHaveLength(2);
    expect(errors).toHaveTextContent('lead: no provider');
    expect(errors).toHaveTextContent('poll: feishu timeout');
  });
});

describe('HealthScreen — 立即查一次 ticks, then re-reads', () => {
  it('calls heartbeat.tick and then reads the status again, in that order', async () => {
    const user = userEvent.setup();
    renderScreen();

    await screen.findByTestId('health-heartbeat-running');
    await user.click(screen.getByTestId('health-heartbeat-tick'));

    await waitFor(() => expect(api.tickHeartbeat).toHaveBeenCalledTimes(1));
    // The write is followed by a read of the state it produced — the screen
    // shows the gateway's answer, not the tick response.
    await waitFor(() => expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(2));

    const firstRead = api.loadHeartbeatStatus.mock.invocationCallOrder[0];
    const tick = api.tickHeartbeat.mock.invocationCallOrder[0];
    const secondRead = api.loadHeartbeatStatus.mock.invocationCallOrder[1];
    expect(firstRead).toBeLessThan(tick);
    expect(tick).toBeLessThan(secondRead);
  });

  it('is disabled while a tick is in flight', async () => {
    const user = userEvent.setup();
    const pending = deferred<typeof CLEAN_REPORT>();
    api.tickHeartbeat.mockReturnValue(pending.promise);
    renderScreen();

    const button = await screen.findByTestId('health-heartbeat-tick');
    await user.click(button);
    // Second click while the first pass is running is not a second pass.
    expect(button).toBeDisabled();
    await user.click(button);
    expect(api.tickHeartbeat).toHaveBeenCalledTimes(1);

    pending.resolve(CLEAN_REPORT);
    await waitFor(() => expect(button).not.toBeDisabled());
    await waitFor(() => expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(2));
  });

  it('reports a failed tick without dropping the status it already has', async () => {
    const user = userEvent.setup();
    api.tickHeartbeat.mockRejectedValueOnce(new Error('tick refused'));
    renderScreen();

    await screen.findByTestId('health-heartbeat-running');
    await user.click(screen.getByTestId('health-heartbeat-tick'));

    expect(await screen.findByText('tick refused')).toBeInTheDocument();
    // Nothing changed, so nothing was re-read.
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('health-heartbeat-running')).toBeInTheDocument();
  });
});

/* ------------------------------------------------------------ loop controls -- */

/** Open an antd Select and return its options, so a label can be picked by text. */
function optionsOf(select: HTMLElement): HTMLElement[] {
  fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
  return Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option'));
}

/**
 * The two writes besides 立即查一次: on/off and the cadence. Both send **only the
 * field that changed** (`heartbeat.configure` treats an absent field as "leave
 * it alone") and apply the answer they get back — that answer is the new state,
 * so no second read is issued.
 */
describe('HealthScreen — the loop controls configure the heartbeat', () => {
  it('sends exactly {enabled:false} and renders 未运行 from the response', async () => {
    const user = userEvent.setup();
    api.configureHeartbeat.mockResolvedValue(heartbeatStatus({ running: false }));
    renderScreen();

    const toggle = await screen.findByTestId('health-heartbeat-enabled');
    expect(toggle).toHaveAttribute('aria-checked', 'true');
    await user.click(toggle);

    await waitFor(() => expect(api.configureHeartbeat).toHaveBeenCalledTimes(1));
    // Exactly the field the user touched — no cadence echo, no empty padding.
    expect(api.configureHeartbeat).toHaveBeenCalledWith({ enabled: false });

    // The response *is* the new state: the tag flips without a second read.
    await waitFor(() =>
      expect(screen.getByTestId('health-heartbeat-running')).toHaveTextContent(/未运行|Not running/),
    );
    expect(screen.getByTestId('health-heartbeat-enabled')).toHaveAttribute('aria-checked', 'false');
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(1);
  });

  it('sends exactly {leadIntervalMs:300000} when a cadence is chosen', async () => {
    api.configureHeartbeat.mockResolvedValue(heartbeatStatus({ leadIntervalMs: 300_000 }));
    renderScreen();

    const select = await screen.findByTestId('health-heartbeat-cadence');
    const fiveMinutes = optionsOf(select).find((option) =>
      /^每 5 分钟$|^every 5 min$/.test(option.getAttribute('title') ?? option.textContent ?? ''),
    );
    expect(fiveMinutes).toBeDefined();
    fireEvent.click(fiveMinutes as HTMLElement);

    await waitFor(() => expect(api.configureHeartbeat).toHaveBeenCalledTimes(1));
    expect(api.configureHeartbeat).toHaveBeenCalledWith({ leadIntervalMs: 300_000 });

    await waitFor(() =>
      expect(screen.getByTestId('health-heartbeat-leadInterval')).toHaveTextContent(
        /每 5 分钟|every 5 min/,
      ),
    );
    // The switch was not part of this write, and nothing was re-read.
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(1);
  });

  it('offers 30 秒 / 1 分钟 / 5 分钟 / 15 分钟 as millisecond values', async () => {
    renderScreen();

    const select = await screen.findByTestId('health-heartbeat-cadence');
    const titles = optionsOf(select).map((option) => option.getAttribute('title') ?? '');
    expect(titles).toEqual([
      expect.stringMatching(/^(每 |every )?30/),
      expect.stringMatching(/^(每 |every )?1 (分钟|min)/),
      expect.stringMatching(/^(每 |every )?5 (分钟|min)/),
      expect.stringMatching(/^(每 |every )?15 (分钟|min)/),
    ]);
  });

  it('keeps the last known value and shows the gateway text when the write fails', async () => {
    const user = userEvent.setup();
    api.configureHeartbeat.mockRejectedValueOnce(new Error('heartbeat is locked'));
    renderScreen();

    const toggle = await screen.findByTestId('health-heartbeat-enabled');
    await user.click(toggle);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('heartbeat is locked');

    // Nothing changed on the gateway, so nothing changed here either: the switch
    // stays on the value the last real answer carried.
    expect(screen.getByTestId('health-heartbeat-enabled')).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByTestId('health-heartbeat-running')).toHaveTextContent(/运行中|Running/);
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(1);
  });

  it('disables both controls while a write is in flight', async () => {
    const user = userEvent.setup();
    const pending = deferred<HeartbeatView>();
    api.configureHeartbeat.mockReturnValue(pending.promise);
    renderScreen();

    const toggle = await screen.findByTestId('health-heartbeat-enabled');
    const select = screen.getByTestId('health-heartbeat-cadence');
    await user.click(toggle);

    expect(toggle).toBeDisabled();
    // antd puts the disabled state on the Select's own root element.
    expect(select).toHaveClass('ant-select-disabled');

    // A second click while the first write is unanswered is not a second write.
    await user.click(toggle);
    expect(api.configureHeartbeat).toHaveBeenCalledTimes(1);

    pending.resolve(heartbeatStatus({ running: false, leadIntervalMs: 900_000 }));
    await waitFor(() => expect(toggle).not.toBeDisabled());
    expect(select).not.toHaveClass('ant-select-disabled');
    expect(screen.getByTestId('health-heartbeat-leadInterval')).toHaveTextContent(
      /每 15 分钟|every 15 min/,
    );
  });
});

describe('HealthScreen — a failed read is reported, never hidden', () => {
  it('shows the cause and re-reads on 重试', async () => {
    const user = userEvent.setup();
    api.loadHeartbeatStatus
      .mockRejectedValueOnce(new Error('gateway said no'))
      .mockResolvedValue({
        running: false,
        leadIntervalMs: 30_000,
        channelIntervalMs: 60_000,
        leadRuns: 0,
        dispatched: 0,
        polls: 0,
        newMessages: 0,
      });
    renderScreen();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('gateway said no');
    // The other section still renders its own answer.
    expect(await screen.findByTestId('health-browser-pages')).toBeInTheDocument();

    await user.click(within(alert).getByRole('button', { name: /重试|Retry/ }));

    expect(await screen.findByTestId('health-heartbeat-running')).toHaveTextContent(/未运行|Not running/);
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(2);
  });
});

describe('HealthScreen — a disconnected browser is an empty state, not an error', () => {
  it('says so and offers no endpoint', async () => {
    api.loadBrowserStatus.mockResolvedValue({ connected: false, pages: 0 });
    renderScreen();

    const empty = await screen.findByTestId('health-browser-empty');
    expect(empty).toHaveTextContent(/还没连接浏览器|No browser is connected yet/);
    // The hint says where a connection comes from, not "retry harder".
    expect(empty).toHaveTextContent(/设置|[Ss]ettings/);
    expect(screen.queryByTestId('health-browser-endpoint')).toBeNull();
    expect(screen.getByTestId('health-browser-state')).toHaveTextContent(/未连接|Not connected/);
  });

  it('re-reads the browser on 刷新 without touching the heartbeat', async () => {
    const user = userEvent.setup();
    api.loadBrowserStatus.mockResolvedValue({ connected: false, pages: 0 });
    renderScreen();

    await screen.findByTestId('health-browser-empty');
    await user.click(within(screen.getByTestId('health-browser')).getByRole('button', { name: /刷新|Refresh/ }));
    await waitFor(() => expect(api.loadBrowserStatus).toHaveBeenCalledTimes(2));
    expect(api.loadHeartbeatStatus).toHaveBeenCalledTimes(1);
    expect(api.tickHeartbeat).not.toHaveBeenCalled();
  });
});
/* --------------------------------------------------------------- delivery -- */

/**
 * 投递 — the one section that asks the gateway to *send* something.
 *
 * Two properties are the whole point, and both are about not inventing anything:
 *  1. the plan is shown as the channel order the gateway answered with, plus its
 *     deferred flag and reason — no local "will probably go to…" guess;
 *  2. the receipt is shown verbatim (channel, attempts in order, every error in
 *     the gateway's own words) because a delivery failure is the one place where
 *     its text is the only true description of what happened.
 *
 * Plan and receipt are alternatives, not a log: the newest answer wins, so the
 * section never mixes "where it would go" with "where it went".
 */

const PLAN = { channels: ['feishu', 'wechat', 'inapp'], deferred: false };

const RECEIPT = {
  channel: 'wechat',
  attempts: ['feishu', 'wechat'],
  deferred: false,
  errors: ['feishu: 401 token expired'],
};

describe('HealthScreen — 投递 shows the plan and the receipt the gateway returned', () => {
  it('shows the plan as the ordered channels, with deferred and its reason', async () => {
    const user = userEvent.setup();
    api.planNotification.mockResolvedValue({
      channels: ['feishu', 'wechat', 'inapp'],
      deferred: true,
      reason: 'quiet hours 22:00-08:00',
    });
    renderScreen();

    await user.click(await screen.findByTestId('health-delivery-plan'));

    await waitFor(() => expect(api.planNotification).toHaveBeenCalledWith('approval'));
    const plan = await screen.findByTestId('health-delivery-plan-result');
    expect(
      within(plan)
        .getAllByTestId('health-delivery-plan-channel')
        .map((node) => node.textContent),
    ).toEqual(['feishu', 'wechat', 'inapp']);
    // Deferred is a fact about the plan, and its reason is the router's sentence
    // — printed as given.
    expect(within(plan).getByTestId('health-delivery-deferred')).toBeInTheDocument();
    expect(plan).toHaveTextContent('quiet hours 22:00-08:00');
    // Planning sends nothing, so there is no receipt.
    expect(screen.queryByTestId('health-delivery-receipt')).toBeNull();
    expect(api.deliverNotification).not.toHaveBeenCalled();
  });

  it('shows the receipt: which channel took it, the attempts in order, every error verbatim', async () => {
    const user = userEvent.setup();
    api.deliverNotification.mockResolvedValue(RECEIPT);
    renderScreen();

    const text = await screen.findByTestId('health-delivery-text');
    await user.click(screen.getByTestId('health-delivery-send'));

    await waitFor(() => expect(api.deliverNotification).toHaveBeenCalledTimes(1));
    // The selected category and the text the user sees are what is sent.
    expect(api.deliverNotification).toHaveBeenCalledWith({
      category: 'approval',
      text: (text as HTMLInputElement).value,
    });

    const receipt = await screen.findByTestId('health-delivery-receipt');
    expect(within(receipt).getByTestId('health-delivery-receipt-channel')).toHaveTextContent(
      'wechat',
    );
    expect(
      within(receipt)
        .getAllByTestId('health-delivery-attempt')
        .map((node) => node.textContent),
    ).toEqual(['feishu', 'wechat']);
    const errors = within(receipt).getAllByTestId('health-delivery-receipt-error');
    expect(errors).toHaveLength(1);
    // Verbatim: the gateway's words, not a translation of them.
    expect(errors[0].textContent).toBe('feishu: 401 token expired');
    // The newest answer replaces the older one.
    expect(screen.queryByTestId('health-delivery-plan-result')).toBeNull();
  });

  it('replaces a receipt with a newer plan (the newest action wins)', async () => {
    const user = userEvent.setup();
    api.deliverNotification.mockResolvedValue(RECEIPT);
    api.planNotification.mockResolvedValue(PLAN);
    renderScreen();

    await user.click(await screen.findByTestId('health-delivery-send'));
    await screen.findByTestId('health-delivery-receipt');

    await user.click(screen.getByTestId('health-delivery-plan'));

    await screen.findByTestId('health-delivery-plan-result');
    expect(screen.queryByTestId('health-delivery-receipt')).toBeNull();
  });

  it('disables 发送测试 while the text is blank and never calls the gateway', async () => {
    const user = userEvent.setup();
    renderScreen();

    const text = await screen.findByTestId('health-delivery-text');
    const send = screen.getByTestId('health-delivery-send');
    expect(send).not.toBeDisabled();

    await user.clear(text);
    expect(send).toBeDisabled();
    await user.click(send);
    expect(api.deliverNotification).not.toHaveBeenCalled();
  });

  it('keeps one action in flight at a time: both buttons are disabled until it lands', async () => {
    const user = userEvent.setup();
    const pending = deferred<typeof PLAN>();
    api.planNotification.mockReturnValue(pending.promise);
    renderScreen();

    const plan = await screen.findByTestId('health-delivery-plan');
    const send = screen.getByTestId('health-delivery-send');
    await user.click(plan);

    expect(plan).toBeDisabled();
    expect(send).toBeDisabled();
    // A second action while the first is unanswered is not a second action.
    await user.click(send);
    expect(api.deliverNotification).not.toHaveBeenCalled();

    pending.resolve(PLAN);
    await waitFor(() => expect(plan).not.toBeDisabled());
    expect(send).not.toBeDisabled();
  });

  it('offers the six categories and plans the one that is selected', async () => {
    const user = userEvent.setup();
    renderScreen();

    const select = await screen.findByTestId('health-delivery-category');
    // Categories are protocol values: shown verbatim, in the gateway's order.
    expect(optionsOf(select).map((option) => option.getAttribute('title') ?? option.textContent)).toEqual([
      'approval',
      'needs_decision',
      'task_done',
      'task_failed',
      'inbox_digest',
      'daily_summary',
    ]);

    const taskFailed = optionsOf(select).find(
      (option) => (option.getAttribute('title') ?? '') === 'task_failed',
    );
    fireEvent.click(taskFailed as HTMLElement);
    await waitFor(() => expect(select).toHaveTextContent('task_failed'));

    await user.click(screen.getByTestId('health-delivery-plan'));
    await waitFor(() => expect(api.planNotification).toHaveBeenCalledWith('task_failed'));
  });

  it('shows the gateway text in an Alert when the action fails', async () => {
    const user = userEvent.setup();
    api.deliverNotification.mockRejectedValueOnce(
      new Error('channeldispatch: every channel failed: feishu 500'),
    );
    renderScreen();

    await user.click(await screen.findByTestId('health-delivery-send'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('channeldispatch: every channel failed: feishu 500');
    // Nothing was delivered, so no receipt is invented.
    expect(screen.queryByTestId('health-delivery-receipt')).toBeNull();
  });
});

describe('HealthScreen — the shell-confinement row is the command fence made visible', () => {
  /**
   * Commands never ask for approval in this product (2026-11-26 permission
   * model), so a host without an OS runner runs them unconfined. The audit's
   * finding was that this difference lived only in a log line the client cannot
   * see; these tests pin that the status page now shows it — and that the two
   * other states are not rounded to the same tag.
   */
  it('says OS-confined, with the scope sentence, when a runner reports enforcement', async () => {
    renderScreen();

    expect(await screen.findByTestId('health-sandbox-state')).toHaveTextContent(/OS 已约束|OS-confined/);
    expect(screen.getByTestId('health-sandbox-scope')).toHaveTextContent(
      /工作目录与私有临时目录|working directory and private temporary directories/,
    );
    expect(screen.getByTestId('health-sandbox-platform')).toHaveTextContent('linux');
    // Nothing to warn about, so no warning card is invented.
    expect(screen.queryByTestId('health-sandbox-warning')).toBeNull();
    expect(api.loadSandboxStatus).toHaveBeenCalledTimes(1);
  });

  it('shows the unconfined warning, quoting the gateway, instead of a quiet tag', async () => {
    api.loadSandboxStatus.mockResolvedValue({
      available: false,
      platform: 'linux',
      reason: 'bwrap: not installed',
      confinement: 'none',
      commandsUnconfined: true,
      note:
        'Shell commands run without OS confinement (bwrap: not installed): the working-directory write rule is an application rule, not an enforced boundary.',
    });
    renderScreen();

    expect(await screen.findByTestId('health-sandbox-state')).toHaveTextContent(/无 OS 约束|No OS confinement/);
    const warning = screen.getByTestId('health-sandbox-warning');
    // The gateway's own sentence is what the user reads; the screen does not
    // paraphrase "which host has no runner".
    expect(warning).toHaveTextContent('bwrap: not installed');
    expect(warning).toHaveTextContent(/not an enforced boundary|不是 OS 强制执行的边界/);
    expect(screen.getByTestId('health-sandbox-scope')).toHaveTextContent(
      /任何路径都可写|any path the process can write/,
    );
    expect(screen.getByTestId('health-sandbox-reason')).toHaveTextContent('bwrap: not installed');
  });

  it('does not call an assembly without a sandbox module "confined"', async () => {
    // Empty confinement = unknown (no sandbox module): commands stay behind the
    // approval gate there, so neither "confined" nor "unconfined" is true.
    api.loadSandboxStatus.mockResolvedValue({
      available: false,
      platform: 'linux',
      confinement: '',
      commandsUnconfined: false,
    });
    renderScreen();

    expect(await screen.findByTestId('health-sandbox-state')).toHaveTextContent(
      /未装配沙箱模块|No sandbox module/,
    );
    expect(screen.queryByTestId('health-sandbox-warning')).toBeNull();
  });

  it('a failed read stays inside its own section', async () => {
    api.loadSandboxStatus.mockRejectedValueOnce(new Error('unknown method: sandbox.status'));
    renderScreen();

    // Its own section reports the failure…
    await waitFor(() => expect(screen.getByTestId('health-sandbox')).toHaveTextContent(
      /sandbox.status/,
    ));
    // …and the heartbeat keeps working.
    expect(await screen.findByTestId('health-heartbeat-running')).toBeInTheDocument();
  });
});
