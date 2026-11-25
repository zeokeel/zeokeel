import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
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
 *     zeroed endpoint.
 *
 * No timers are faked: the screen has no polling, so every assertion is about
 * one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  loadHeartbeatStatus: vi.fn(),
  tickHeartbeat: vi.fn(),
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