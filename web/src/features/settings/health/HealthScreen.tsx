/**
 * R27b · run status — the two things that move on their own: the Lead's
 * heartbeat (`heartbeat.status` / `heartbeat.tick`) and the automation browser
 * (`browser.status`). See docs/TRANSPORT.md §5.1.
 *
 * Two honest sections, each with its own read:
 *
 *   自转      running state, the two cadences, the last pass and its counts
 *   浏览器     connected state, endpoint and page count — or an empty state
 *
 * **There is no timer on purpose.** Neither method has a delta stream, so the
 * only way to notice a change made elsewhere would be a poll, and a status page
 * that polls is a page that cannot be trusted to mean "what you are looking at
 * is now". A button is honest — it says when the read happened; a timer is not.
 * The section's own write (`heartbeat.tick`) re-reads afterwards, so the numbers
 * on screen are always the gateway's answer and never a local guess.
 *
 * A failed *read* replaces its own section with an error that names the cause
 * and offers 重试 (the other section keeps working). A failed *write* is
 * reported as-is, because nothing changed and the status on screen is still the
 * last true answer.
 */
import { useCallback, useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Descriptions, Empty, Flex, Skeleton, Tag, Typography } from 'antd';
import {
  loadBrowserStatus,
  loadHeartbeatStatus,
  tickHeartbeat,
  type BrowserStatus,
  type HeartbeatStatus,
} from '@/api/health';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';

type Translate = (key: MessageKey, params?: Record<string, string | number>) => string;

/**
 * An endpoint or an error string can be one long token: `overflowWrap: anywhere`
 * keeps it inside the row at 390px instead of widening the page.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;
/** Past this age "N 天前" stops being readable, and the date itself is honest. */
const ABSOLUTE_AFTER_MS = 30 * DAY_MS;

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

function two(value: number): string {
  return value < 10 ? `0${value}` : String(value);
}

/** Local calendar date — language-neutral, so it needs no catalogue key. */
function calendarDate(epochMs: number): string {
  const date = new Date(epochMs);
  return `${date.getFullYear()}-${two(date.getMonth() + 1)}-${two(date.getDate())}`;
}

/**
 * A cadence in the terms a person thinks in ("every 30 s"), not raw
 * milliseconds. A non-finite value renders as `—`: a cadence the gateway did not
 * report is not zero.
 */
function describeInterval(ms: number, t: Translate): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  if (ms < 1_000) return t('health.interval.everyMillis', { count: Math.round(ms) });
  if (ms < MINUTE_MS) return t('health.interval.everySeconds', { count: Math.round(ms / 1_000) });
  if (ms < HOUR_MS) return t('health.interval.everyMinutes', { count: Math.round(ms / MINUTE_MS) });
  return t('health.interval.everyHours', { count: Math.round(ms / HOUR_MS) });
}

/**
 * When the last pass happened, as a relative stamp. `lastTick` is absent before
 * the first pass — the caller decides what to print instead (the gateway did not
 * say "now", so neither do we). Unparseable input degrades to the raw string.
 */
function describeAge(iso: string | undefined, now: number, t: Translate): string | null {
  if (!iso) return null;
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return iso;

  // A future stamp is clock skew, not an event: read it as "just now" rather
  // than printing negative minutes.
  const delta = Math.max(0, now - then);
  if (delta < MINUTE_MS) return t('health.age.justNow');
  if (delta < HOUR_MS) return t('health.age.minutes', { count: Math.floor(delta / MINUTE_MS) });
  if (delta < DAY_MS) return t('health.age.hours', { count: Math.floor(delta / HOUR_MS) });
  if (delta < ABSOLUTE_AFTER_MS) return t('health.age.days', { count: Math.floor(delta / DAY_MS) });
  return calendarDate(then);
}

type Loaded<T> =
  | { kind: 'loading' }
  | { kind: 'ready'; value: T }
  | { kind: 'error'; message: string };

/**
 * One read, as a state. `reload` is the 重试 path (it shows the skeleton again);
 * `refresh` is the re-read after a write or after the user asks for a new
 * reading (it keeps the section in place, so a tick does not blink the numbers
 * away).
 *
 * `read` must be stable (`useCallback`): it is the effect's dependency, and an
 * unstable function would re-read on every render — a polling loop by another
 * name.
 */
function useSectionValue<T>(read: () => Promise<T>): {
  state: Loaded<T>;
  reload: () => void;
  refresh: () => Promise<void>;
} {
  const [state, setState] = useState<Loaded<T>>({ kind: 'loading' });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void read().then(
      (value) => {
        if (!cancelled) setState({ kind: 'ready', value });
      },
      (cause: unknown) => {
        if (!cancelled) setState({ kind: 'error', message: messageOf(cause) });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [read, attempt]);

  const reload = useCallback(() => {
    setState({ kind: 'loading' });
    setAttempt((value) => value + 1);
  }, []);

  const refresh = useCallback(async () => {
    try {
      setState({ kind: 'ready', value: await read() });
    } catch (cause) {
      setState({ kind: 'error', message: messageOf(cause) });
    }
  }, [read]);

  return { state, reload, refresh };
}

/** Loading and failure are per section, so one dead read cannot blank the other. */
function SectionState({ state, onRetry }: { state: Loaded<unknown>; onRetry: () => void }) {
  const t = useT();
  if (state.kind === 'loading') {
    return <Skeleton active title={false} paragraph={{ rows: 2 }} />;
  }
  if (state.kind === 'error') {
    return (
      <Alert
        type="error"
        showIcon
        role="alert"
        title={t('health.loadFailed')}
        description={state.message}
        action={
          <Button size="small" onClick={onRetry}>
            {t('health.retry')}
          </Button>
        }
      />
    );
  }
  return null;
}

function Section({
  titleKey,
  noteKey,
  action,
  testId,
  children,
}: {
  titleKey: MessageKey;
  noteKey: MessageKey;
  action?: ReactNode;
  testId: string;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <section className="zk-settings__section" data-testid={testId}>
      <div className="zk-screen__header--row">
        <div>
          <h2 className="zk-settings__section-title">{t(titleKey)}</h2>
          <p className="zk-screen__note">{t(noteKey)}</p>
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

export function HealthScreen() {
  const t = useT();
  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('health.title')}</h1>
        <p className="zk-screen__note">{t('health.note')}</p>
      </header>
      <Flex vertical gap="large">
        <HeartbeatSection />
        <BrowserSection />
      </Flex>
    </div>
  );
}

/* -------------------------------------------------------------- heartbeat -- */

function HeartbeatSection() {
  const t = useT();
  const [ticking, setTicking] = useState(false);
  const [tickError, setTickError] = useState<string | null>(null);

  // The reading carries the moment it landed: the relative stamp is computed
  // against that instant, so a re-read after a tick makes "刚刚" true again
  // without the screen holding a clock of its own.
  const read = useCallback(async () => {
    const status = await loadHeartbeatStatus();
    return { status, at: Date.now() };
  }, []);
  const { state, reload, refresh } = useSectionValue(read);

  const tick = async () => {
    if (ticking) return;
    setTicking(true);
    setTickError(null);
    try {
      // The write, then a read of the state it produced: the screen shows the
      // gateway's answer, not the tick's own response.
      await tickHeartbeat();
      await refresh();
    } catch (cause) {
      setTickError(describeError(cause, t, 'health.tickFailed'));
    } finally {
      setTicking(false);
    }
  };

  return (
    <Section
      titleKey="health.heartbeat.title"
      noteKey="health.heartbeat.note"
      testId="health-heartbeat"
      action={
        <Button
          type="primary"
          onClick={() => void tick()}
          loading={ticking}
          disabled={ticking}
          data-testid="health-heartbeat-tick"
        >
          {t('health.heartbeat.tick')}
        </Button>
      }
    >
      <SectionState state={state} onRetry={reload} />

      {tickError ? <Alert type="error" showIcon role="alert" closable title={tickError} /> : null}

      {state.kind === 'ready' ? <HeartbeatFacts reading={state.value} /> : null}
    </Section>
  );
}

/** One heartbeat reading, plus the instant it was taken. */
interface HeartbeatReading {
  status: HeartbeatStatus;
  at: number;
}

function HeartbeatFacts({ reading }: { reading: HeartbeatReading }) {
  const t = useT();
  const { status } = reading;
  const lastTick = describeAge(status.lastTick, reading.at, t);

  return (
    <>
      <Flex gap="small" align="center" wrap>
        {status.running ? (
          <Tag color="success" data-testid="health-heartbeat-running">
            {t('health.heartbeat.running')}
          </Tag>
        ) : (
          <Tag data-testid="health-heartbeat-running">{t('health.heartbeat.stopped')}</Tag>
        )}
      </Flex>

      <Descriptions size="small" column={1}>
        <Descriptions.Item label={t('health.heartbeat.lastTick')}>
          <span data-testid="health-heartbeat-lastTick">
            {lastTick ?? t('health.heartbeat.never')}
          </span>
        </Descriptions.Item>
        <Descriptions.Item label={t('health.heartbeat.leadInterval')}>
          <span data-testid="health-heartbeat-leadInterval">
            {describeInterval(status.leadIntervalMs, t)}
          </span>
        </Descriptions.Item>
        <Descriptions.Item label={t('health.heartbeat.channelInterval')}>
          <span data-testid="health-heartbeat-channelInterval">
            {describeInterval(status.channelIntervalMs, t)}
          </span>
        </Descriptions.Item>
        {/* Each count is one fact about the *last pass*, printed as reported —
            nothing here accumulates a total of its own. */}
        <Descriptions.Item label={t('health.heartbeat.leadRuns')}>
          <span data-testid="health-count-leadRuns">{status.leadRuns}</span>
        </Descriptions.Item>
        <Descriptions.Item label={t('health.heartbeat.dispatched')}>
          <span data-testid="health-count-dispatched">{status.dispatched}</span>
        </Descriptions.Item>
        <Descriptions.Item label={t('health.heartbeat.polls')}>
          <span data-testid="health-count-polls">{status.polls}</span>
        </Descriptions.Item>
        <Descriptions.Item label={t('health.heartbeat.newMessages')}>
          <span data-testid="health-count-newMessages">{status.newMessages}</span>
        </Descriptions.Item>
      </Descriptions>

      {status.errors && status.errors.length > 0 ? (
        <div data-testid="health-heartbeat-errors">
          <Typography.Text type="danger" strong>
            {t('health.heartbeat.errorCount', { count: status.errors.length })}
          </Typography.Text>
          <ul className="zk-inbox__list">
            {status.errors.map((error, index) => (
              <li key={`${index}:${error}`} data-testid="health-heartbeat-error">
                {/* The gateway's own words: not translated, but selectable. */}
                <Typography.Text type="danger" style={WRAP_LONG}>
                  {error}
                </Typography.Text>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </>
  );
}

/* ---------------------------------------------------------------- browser -- */

function BrowserSection() {
  const t = useT();
  const [refreshing, setRefreshing] = useState(false);

  const read = useCallback(() => loadBrowserStatus(), []);
  const { state, reload, refresh } = useSectionValue(read);

  const reread = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      await refresh();
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <Section
      titleKey="health.browser.title"
      noteKey="health.browser.note"
      testId="health-browser"
      action={
        <Button
          onClick={() => void reread()}
          loading={refreshing}
          disabled={refreshing}
          data-testid="health-browser-refresh"
        >
          {t('health.refresh')}
        </Button>
      }
    >
      <SectionState state={state} onRetry={reload} />
      {state.kind === 'ready' ? <BrowserFacts status={state.value} /> : null}
    </Section>
  );
}

function BrowserFacts({ status }: { status: BrowserStatus }) {
  const t = useT();
  return (
    <>
      <Flex gap="small" align="center" wrap>
        {status.connected ? (
          <Tag color="success" data-testid="health-browser-state">
            {t('health.browser.connected')}
          </Tag>
        ) : (
          <Tag data-testid="health-browser-state">{t('health.browser.disconnected')}</Tag>
        )}
      </Flex>

      {status.connected ? (
        <Descriptions size="small" column={1}>
          <Descriptions.Item label={t('health.browser.endpoint')}>
            <span data-testid="health-browser-endpoint" style={WRAP_LONG}>
              {status.endpoint ?? '—'}
            </span>
          </Descriptions.Item>
          <Descriptions.Item label={t('health.browser.pages')}>
            <span data-testid="health-browser-pages">{status.pages}</span>
          </Descriptions.Item>
        </Descriptions>
      ) : (
        // Disconnected is not an error: nothing failed, there is just no
        // browser attached. It says where a connection comes from instead of
        // offering a "retry" that could not change anything.
        <div data-testid="health-browser-empty">
          <Empty description={t('health.browser.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
          <Typography.Text type="secondary">{t('health.browser.emptyHint')}</Typography.Text>
        </div>
      )}
    </>
  );
}