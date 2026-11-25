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
  loadHeartbeatStatus: vi.fn(),
  tickHeartbeat: vi.fn(),
  configureHeartbeat: vi.fn(),
  loadBrowserStatus: vi.fn(),
}));

vi.mock('@/api/health', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/health')>();
  return { ...actual, ...api };
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