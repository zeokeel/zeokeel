/**
 * R27b · run status — the two things that move on their own: the Lead's
 * heartbeat (`heartbeat.status` / `heartbeat.tick`) and the automation browser
 * (`browser.status`). See docs/TRANSPORT.md §5.1.
 *
 * Three honest sections:
 *
 *   自转      running state, on/off + cadence controls, the two cadences, the
 *             last pass and its counts
 *   投递      one category's delivery plan, and a test send with its receipt
 *   浏览器     connected state, endpoint and page count — or an empty state
 *
 * 投递 is the only section that sends something outside the product, so it is
 * built to show the gateway's answer and nothing else: the plan is the router's
 * channel order (with its deferred flag and reason), the receipt is the
 * dispatcher's report (which channel took it, what was tried, every error in its
 * own words). Planning sends nothing; sending replaces the plan with the receipt,
 * because one answer at a time is the only unambiguous reading.
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
 *
 * The 自转 controls (`heartbeat.configure`) are the one place this screen sends a
 * value the user chose. They send **only the field that changed**, and they are
 * driven by the status the gateway returned: no optimistic flip, so a refused
 * write leaves the switch where the last real answer put it.
 */
import { useCallback, useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Descriptions, Empty, Flex, Input, Select, Skeleton, Switch, Tag, Typography } from 'antd';
import {
  NOTIFY_CATEGORIES,
  configureHeartbeat,
  deliverNotification,
  loadBrowserStatus,
  loadHeartbeatStatus,
  planNotification,
  tickHeartbeat,
  type BrowserStatus,
  type DeliveryPlan,
  type DeliveryReceipt,
  type HeartbeatConfig,
  type HeartbeatStatus,
  type NotifyCategory,
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

/** A field that may grow: `minWidth: 0` lets it shrink instead of the page. */
const SHRINKABLE: CSSProperties = { flex: 1, minWidth: 0 };

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
  /** Adopt a value the gateway just returned from a write — no second read. */
  apply: (value: T) => void;
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

  const apply = useCallback((value: T) => {
    setState({ kind: 'ready', value });
  }, []);

  return { state, reload, refresh, apply };
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
  title,
  note,
  action,
  testId,
  children,
}: {
  /** Already-translated copy: the catalogue owns the strings, this owns the box. */
  title: string;
  note: string;
  action?: ReactNode;
  testId: string;
  children: ReactNode;
}) {
  return (
    <section className="zk-settings__section" data-testid={testId}>
      <div className="zk-screen__header--row">
        <div>
          <h2 className="zk-settings__section-title">{title}</h2>
          <p className="zk-screen__note">{note}</p>
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
        <DeliverySection />
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
  const [saving, setSaving] = useState(false);
  const [configError, setConfigError] = useState<string | null>(null);

  // The reading carries the moment it landed: the relative stamp is computed
  // against that instant, so a re-read after a tick makes "刚刚" true again
  // without the screen holding a clock of its own.
  const read = useCallback(async () => {
    const status = await loadHeartbeatStatus();
    return { status, at: Date.now() };
  }, []);
  const { state, reload, refresh, apply } = useSectionValue(read);

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

  /**
   * The one write the user aims at a field: send that field, and adopt the
   * status that comes back. Nothing is applied when the write fails, so both
   * controls keep showing the last value the gateway actually reported.
   */
  const configure = async (patch: HeartbeatConfig) => {
    if (saving) return;
    setSaving(true);
    setConfigError(null);
    try {
      apply({ status: await configureHeartbeat(patch), at: Date.now() });
    } catch (cause) {
      setConfigError(describeError(cause, t, 'settings.saveFailed'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Section
      title={t('health.heartbeat.title')}
      note={t('health.heartbeat.note')}
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

      {state.kind === 'ready' ? (
        <HeartbeatControls
          status={state.value.status}
          saving={saving}
          onChange={(patch) => void configure(patch)}
        />
      ) : null}

      {configError ? (
        <Alert type="error" showIcon role="alert" closable title={configError} />
      ) : null}

      {state.kind === 'ready' ? <HeartbeatFacts reading={state.value} /> : null}
    </Section>
  );
}

/** The Lead cadences this screen offers, in milliseconds: 30 s … 15 min. */
const CADENCES_MS = [30_000, 60_000, 300_000, 900_000];

/**
 * On/off and cadence for the loop.
 *
 * Both controls are *fully controlled by the gateway's status*: they never move
 * on a click, only on the answer. A switch that flipped first and rolled back
 * would show a state for as long as the request took — this one cannot.
 */
function HeartbeatControls({
  status,
  saving,
  onChange,
}: {
  status: HeartbeatStatus;
  saving: boolean;
  onChange: (patch: HeartbeatConfig) => void;
}) {
  const t = useT();
  const configured = status.leadIntervalMs;
  const known = Number.isFinite(configured) && configured > 0;

  // A cadence the gateway reports but this screen does not offer is still
  // offered *for that value*: a Select whose value matches no option renders a
  // bare number, which is not a reading of the configured cadence.
  const cadences =
    known && !CADENCES_MS.includes(configured)
      ? [...CADENCES_MS, configured].sort((left, right) => left - right)
      : CADENCES_MS;

  return (
    <Flex gap="middle" align="center" wrap data-testid="health-heartbeat-controls">
      <Flex gap="small" align="center">
        <Typography.Text>{t('health.heartbeat.title')}</Typography.Text>
        <Switch
          checked={status.running}
          aria-label={t('health.heartbeat.title')}
          onChange={(enabled) => onChange({ enabled })}
          // While a write is unanswered neither control accepts another one.
          disabled={saving}
          loading={saving}
          data-testid="health-heartbeat-enabled"
        />
      </Flex>
      <Flex gap="small" align="center">
        <Typography.Text>{t('health.heartbeat.leadInterval')}</Typography.Text>
        <Select<number>
          value={known ? configured : undefined}
          placeholder="—"
          options={cadences.map((ms) => ({ value: ms, label: describeInterval(ms, t) }))}
          onChange={(ms) => onChange({ leadIntervalMs: ms })}
          disabled={saving}
          data-testid="health-heartbeat-cadence"
        />
      </Flex>
    </Flex>
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

/* --------------------------------------------------------------- delivery -- */

/** Label → control, the same anatomy the settings pages use. */
function Field({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="zk-field">
      <div className="zk-field__label-row">
        <label className="zk-field__label" htmlFor={id}>
          {label}
        </label>
      </div>
      <div className="zk-field__control">{children}</div>
    </div>
  );
}

/**
 * 投递 — one category's plan, and a test send with its receipt.
 *
 * The two actions are alternatives and both talk to the gateway for real:
 * 看计划 calls `notify.plan` (router only, nothing leaves the product) and
 * 发送测试 calls `notify.deliver` (the dispatcher tries the plan in order). Each
 * answer replaces the other, because "where it would go" and "where it went" are
 * different claims and showing them together is how a status page lies.
 *
 * One action is in flight at a time: the buttons disable for the duration, and a
 * second click is not a second send. A failure is the gateway's text in an
 * Alert — never a paraphrase, because a delivery error names the thing that
 * refused.
 *
 * The category and the channel names are protocol values (`notifyroute.go`),
 * so they are printed verbatim; the copy around them is in the catalogue.
 */
function DeliverySection() {
  const t = useT();

  // `null` = "still the default sentence"; once the user types, the text is
  // theirs and the default (which follows the language) no longer applies.
  const [draft, setDraft] = useState<string | null>(null);
  const [category, setCategory] = useState<NotifyCategory>('approval');
  const [busy, setBusy] = useState<'plan' | 'send' | null>(null);
  const [plan, setPlan] = useState<DeliveryPlan | null>(null);
  const [receipt, setReceipt] = useState<DeliveryReceipt | null>(null);
  const [error, setError] = useState<string | null>(null);

  const text = draft ?? t('health.delivery.textDefault');
  // A blank text is not a message: the gateway refuses it, so the button does too.
  const canSend = busy === null && text.trim() !== '';

  const runPlan = async () => {
    if (busy !== null) return;
    setBusy('plan');
    setError(null);
    try {
      const answer = await planNotification(category);
      // The newest answer wins.
      setReceipt(null);
      setPlan(answer);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(null);
    }
  };

  const send = async () => {
    if (!canSend) return;
    setBusy('send');
    setError(null);
    try {
      const answer = await deliverNotification({ category, text });
      setPlan(null);
      setReceipt(answer);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusy(null);
    }
  };

  return (
    <Section
      title={t('health.delivery.title')}
      note={t('health.delivery.note')}
      testId="health-delivery"
      action={
        <Flex gap="small">
          <Button
            onClick={() => void runPlan()}
            loading={busy === 'plan'}
            disabled={busy !== null}
            data-testid="health-delivery-plan"
          >
            {t('health.delivery.plan')}
          </Button>
          <Button
            onClick={() => void send()}
            loading={busy === 'send'}
            disabled={!canSend}
            data-testid="health-delivery-send"
          >
            {t('health.delivery.send')}
          </Button>
        </Flex>
      }
    >
      {error ? <Alert type="error" showIcon role="alert" closable title={error} /> : null}

      <Flex gap="middle" wrap align="flex-start">
        <Field id="health-delivery-category-field" label={t('health.delivery.category')}>
          {/* The category is a protocol value: the option label is the value. */}
          <Select<NotifyCategory>
            id="health-delivery-category-field"
            value={category}
            options={NOTIFY_CATEGORIES.map((value) => ({ value, label: value }))}
            onChange={(next) => {
              setCategory(next);
              // A plan/receipt belongs to the category it was answered for.
              setPlan(null);
              setReceipt(null);
            }}
            disabled={busy !== null}
            data-testid="health-delivery-category"
          />
        </Field>
        <div style={SHRINKABLE}>
          <Field id="health-delivery-text-field" label={t('health.delivery.text')}>
            <Input
              id="health-delivery-text-field"
              value={text}
              onChange={(event) => setDraft(event.target.value)}
              disabled={busy !== null}
              data-testid="health-delivery-text"
            />
          </Field>
        </div>
      </Flex>

      {plan ? <DeliveryPlanView plan={plan} /> : null}
      {receipt ? <DeliveryReceiptView receipt={receipt} /> : null}
    </Section>
  );
}

/** The router's answer: the channels in its order, plus deferred and why. */
function DeliveryPlanView({ plan }: { plan: DeliveryPlan }) {
  const t = useT();
  return (
    <div data-testid="health-delivery-plan-result">
      <Typography.Text strong>{t('health.delivery.planTitle')}</Typography.Text>
      {plan.channels.length > 0 ? (
        <ol className="zk-inbox__list">
          {plan.channels.map((channel, index) => (
            <li key={`${index}:${channel}`}>
              <Tag data-testid="health-delivery-plan-channel">{channel}</Tag>
            </li>
          ))}
        </ol>
      ) : (
        <p className="zk-screen__note">{t('health.delivery.noChannels')}</p>
      )}
      {plan.deferred ? (
        <Flex gap="small" align="center" wrap data-testid="health-delivery-deferred">
          {/* Deferred is a fact about the plan, not a warning: quiet hours held
              the message back on purpose, so the tag stays neutral. */}
          <Tag>{t('health.delivery.deferred')}</Tag>
          {/* The router's sentence, not ours to rewrite. */}
          {plan.reason ? (
            <Typography.Text type="secondary" style={WRAP_LONG}>
              {plan.reason}
            </Typography.Text>
          ) : null}
        </Flex>
      ) : plan.reason ? (
        <Typography.Text type="secondary" style={WRAP_LONG}>
          {plan.reason}
        </Typography.Text>
      ) : null}
    </div>
  );
}

/** The dispatcher's answer: who took it, what was tried, what failed. */
function DeliveryReceiptView({ receipt }: { receipt: DeliveryReceipt }) {
  const t = useT();
  return (
    <div data-testid="health-delivery-receipt">
      <Typography.Text strong>{t('health.delivery.receiptTitle')}</Typography.Text>
      <Descriptions size="small" column={1}>
        <Descriptions.Item label={t('health.delivery.channel')}>
          <Tag color="success" data-testid="health-delivery-receipt-channel">
            {receipt.channel || '—'}
          </Tag>
        </Descriptions.Item>
        {receipt.attempts ? (
          <Descriptions.Item label={t('health.delivery.attempts')}>
            <Flex gap="small" wrap>
              {receipt.attempts.map((channel, index) => (
                <Tag key={`${index}:${channel}`} data-testid="health-delivery-attempt">
                  {channel}
                </Tag>
              ))}
            </Flex>
          </Descriptions.Item>
        ) : null}
        {receipt.deferred ? (
          <Descriptions.Item label={t('health.delivery.deferred')}>
            <span data-testid="health-delivery-receipt-deferred">{receipt.reason ?? '—'}</span>
          </Descriptions.Item>
        ) : null}
      </Descriptions>

      {receipt.errors ? (
        <div data-testid="health-delivery-receipt-errors">
          <Typography.Text type="danger" strong>
            {t('health.delivery.errors')}
          </Typography.Text>
          <ul className="zk-inbox__list">
            {receipt.errors.map((message, index) => (
              <li key={`${index}:${message}`} data-testid="health-delivery-receipt-error">
                {/* The gateway's own words: not translated, but selectable. */}
                <Typography.Text type="danger" style={WRAP_LONG}>
                  {message}
                </Typography.Text>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
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
      title={t('health.browser.title')}
      note={t('health.browser.note')}
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