/**
 * R27a · 渠道 settings — the three things a channel needs before it can reach
 * the Lead: a credential, a webhook source, and a notification route
 * (`credentials.*`, `webhook.source*`, `notify.route.*`, TRANSPORT §5.1).
 *
 * Three honest sections, in that order, each with its own read:
 *
 *   渠道凭据    identity list + a write-only secret form (never echoes a value)
 *   Webhook 来源 id/channel list + add (secret) + delete
 *   通知路由    one route per category: primary / fallback / enabled / quiet hours
 *
 * The surface is request/response — none of these methods has a delta stream —
 * so every section reads on mount and **re-reads after its own write**. There is
 * no open-ended timer on purpose: a poll would be the only way to notice another
 * client's change, and inventing one is the polling loop the project forbids.
 *
 * The one exception is 扫码接入 (`wechat.login.start` / `wechat.login.state`):
 * a QR login is inherently a start-then-poll flow with a human in the middle, so
 * it polls on a fixed interval, stops at a fixed number of tries, and dies with
 * its block. Bounded by construction, not by discipline.
 *
 * A failed *read* replaces its own section with an error that names the cause
 * and offers 重试 (the other two sections stay usable). A failed *write* is
 * reported above the form it came from, because nothing changed.
 */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';
import {
  Alert,
  Button,
  Empty,
  Flex,
  Input,
  Popconfirm,
  Select,
  Skeleton,
  Switch,
  Tag,
  Typography,
} from 'antd';
import {
  CREDENTIAL_FIELD_NAMES,
  CREDENTIAL_KINDS,
  NOTIFY_CHANNELS,
  deleteCredential,
  listCredentials,
  listWebhookSources,
  loadNotifyRoutes,
  pollWechatLoginState,
  removeWebhookSource,
  setCredential,
  setWebhookSource,
  startWechatLogin,
  upsertNotifyRoute,
  type CredentialFieldName,
  type CredentialInfo,
  type CredentialKind,
  type NotifyRoute,
  type WechatLoginStart,
  type WebhookSourceInfo,
} from '@/api/channels';
import { useMediaQuery } from '@/design/use-system-appearance';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';

/** One credential field: the wire name, its label, and whether it is a secret. */
interface CredentialFieldSpec {
  name: CredentialFieldName;
  labelKey: MessageKey;
  secret: boolean;
}

/**
 * Label and masking for every field name the credential store may hold. The
 * *names* come from the api layer (`CREDENTIAL_FIELD_NAMES`) — they are the keys
 * of the `credentials.set` payload, not a UI choice — so the form and the
 * request cannot drift apart; the `Record<CredentialFieldName, …>` type makes a
 * missing label a compile error.
 */
const FIELD_COPY: Record<CredentialFieldName, { labelKey: MessageKey; secret: boolean }> = {
  token: { labelKey: 'channels.field.token', secret: true },
  // The robot code is an identity the platform matches the token against, not a
  // secret: it is shown in clear, and it is as required as the token because
  // every send body carries it.
  robotCode: { labelKey: 'channels.field.robotCode', secret: false },
  appId: { labelKey: 'channels.field.appId', secret: false },
  appSecret: { labelKey: 'channels.field.appSecret', secret: true },
  username: { labelKey: 'channels.field.username', secret: false },
  password: { labelKey: 'channels.field.password', secret: true },
};

/** The form's fields, in the order each kind stores them. */
const CREDENTIAL_FIELDS: Record<CredentialKind, readonly CredentialFieldSpec[]> = {
  wechat: fieldsFor('wechat'),
  dingtalk: fieldsFor('dingtalk'),
  feishu: fieldsFor('feishu'),
  email: fieldsFor('email'),
};

function fieldsFor(kind: CredentialKind): readonly CredentialFieldSpec[] {
  return CREDENTIAL_FIELD_NAMES[kind].map((name) => ({ name, ...FIELD_COPY[name] }));
}

const KIND_OPTIONS = CREDENTIAL_KINDS.map((kind) => ({ value: kind, label: kind }));
const CHANNEL_OPTIONS = NOTIFY_CHANNELS.map((channel) => ({ value: channel, label: channel }));

/**
 * A credential id or webhook source id is printed verbatim and can be one long
 * token: `overflowWrap: anywhere` keeps it inside the row at 390px instead of
 * widening the page (`break-word` leaves the intrinsic width at the whole id).
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** A flex child allowed to shrink below its content (`min-width: auto` refuses). */
const SHRINKABLE: CSSProperties = { flex: 1, minWidth: 0 };

/**
 * Below this width 保存 — the screen's only filled primary — gets antd's
 * `large` (40px): `middle`'s 32px is not a thumb target on a phone, and this
 * screen is the same full web client over the LAN (prototype screen 05).
 */
const NARROW_QUERY = '(max-width: 768px)';

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

type Loaded<T> =
  | { kind: 'loading' }
  | { kind: 'ready'; value: T }
  | { kind: 'error'; message: string };

/**
 * One read, as a state. `reload` is the 重试 path (it shows the skeleton again);
 * `refresh` is the re-read after a write (it keeps the section in place, so a
 * save does not blink the list away).
 *
 * `read` must be stable (`useCallback`): it is the effect's dependency, and an
 * unstable function would re-read on every render — the polling loop by another
 * name. `identity` names *what* was read (the credential kind); when it changes,
 * the previous answer is stale by definition and the section shows its skeleton
 * instead of the other kind's rows.
 */
function useSectionValue<T>(
  read: () => Promise<T>,
  identity = '',
): {
  state: Loaded<T>;
  reload: () => void;
  refresh: () => Promise<void>;
} {
  const [snapshot, setSnapshot] = useState<{ identity: string; loaded: Loaded<T> }>(() => ({
    identity,
    loaded: { kind: 'loading' },
  }));
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void read().then(
      (value) => {
        if (!cancelled) setSnapshot({ identity, loaded: { kind: 'ready', value } });
      },
      (cause: unknown) => {
        if (!cancelled) {
          setSnapshot({ identity, loaded: { kind: 'error', message: messageOf(cause) } });
        }
      },
    );
    return () => {
      cancelled = true;
    };
  }, [read, identity, attempt]);

  const reload = useCallback(() => {
    setSnapshot({ identity, loaded: { kind: 'loading' } });
    setAttempt((value) => value + 1);
  }, [identity]);

  const refresh = useCallback(async () => {
    try {
      setSnapshot({ identity, loaded: { kind: 'ready', value: await read() } });
    } catch (cause) {
      setSnapshot({ identity, loaded: { kind: 'error', message: messageOf(cause) } });
    }
  }, [read, identity]);

  // A snapshot taken for another identity is not this section's answer.
  const state: Loaded<T> =
    snapshot.identity === identity ? snapshot.loaded : { kind: 'loading' };

  return { state, reload, refresh };
}

function Section({
  titleKey,
  noteKey,
  children,
}: {
  titleKey: MessageKey;
  noteKey: MessageKey;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <section className="zk-settings__section">
      <h2 className="zk-settings__section-title">{t(titleKey)}</h2>
      <p className="zk-screen__note">{t(noteKey)}</p>
      {children}
    </section>
  );
}

/** Label → control → help, the same anatomy the settings pages use (§FIELD-DESIGN). */
function Field({
  id,
  label,
  kind = 'text',
  help,
  children,
}: {
  id?: string;
  label: string;
  kind?: 'text' | 'secret';
  help?: string;
  children: ReactNode;
}) {
  return (
    <div className="zk-field" data-kind={kind}>
      <div className="zk-field__label-row">
        <label className="zk-field__label" htmlFor={id}>
          {label}
        </label>
      </div>
      <div className="zk-field__control">{children}</div>
      {help ? <p className="zk-field__help">{help}</p> : null}
    </div>
  );
}

/** Loading and failure are per section, so one dead read cannot blank the rest. */
function SectionState({
  state,
  onRetry,
}: {
  state: Loaded<unknown>;
  onRetry: () => void;
}) {
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
        title={t('channels.loadFailed')}
        description={state.message}
        action={
          <Button size="small" onClick={onRetry}>
            {t('channels.retry')}
          </Button>
        }
      />
    );
  }
  return null;
}

/* ------------------------------------------------------- wechat QR login -- */

/**
 * The copy this block shows, spelled out as keys and nothing else.
 *
 * Note for the maintainer: these lived in a local `COPY` table first, because
 * the task that added the block could not edit the catalogue. `src/i18n`
 * already owns every user-visible string, and the B5 guard
 * (`src/i18n/i18n.test.tsx`) fails on any Chinese literal outside it, so the
 * keys were added to `channels.wechat.*` instead — a local table would have
 * been a second source of copy that the guard exists to prevent.
 */
const WECHAT_LOGIN_KEYS = {
  title: 'channels.wechat.title',
  note: 'channels.wechat.note',
  generate: 'channels.wechat.generate',
  regenerate: 'channels.wechat.regenerate',
  scanHint: 'channels.wechat.scanHint',
  pending: 'channels.wechat.pending',
  waiting: 'channels.wechat.waiting',
  connected: 'channels.wechat.connected',
  expired: 'channels.wechat.expired',
  exhausted: 'channels.wechat.exhausted',
  startFailed: 'channels.wechat.startFailed',
  pollFailed: 'channels.wechat.pollFailed',
} as const satisfies Record<string, MessageKey>;

/** How often one attempt is checked, and how many checks before giving up. */
const WECHAT_POLL_INTERVAL_MS = 2000;
const WECHAT_POLL_MAX_TRIES = 10;

/**
 * One QR attempt, as a state. `idle` is the state the block starts in; every
 * other value is an answer from the gateway (or from the request that tried to
 * reach it), so the button label follows from it and nothing is inferred.
 */
type WechatLoginPhase =
  | { kind: 'idle' }
  | { kind: 'starting' }
  | { kind: 'polling'; qrUrl: string; state: string }
  | { kind: 'confirmed' }
  | { kind: 'expired' }
  | { kind: 'exhausted' }
  | { kind: 'failed'; title: MessageKey; message: string };

/**
 * 扫码接入 — a WeChat login starts with a code the human scans, so this is the
 * one place on the surface that polls. Two properties keep it honest:
 *
 *  1. **Bounded**: at most {@link WECHAT_POLL_MAX_TRIES} checks, one per
 *     {@link WECHAT_POLL_INTERVAL_MS}; a code nobody scans dies here instead of
 *     leaving a request loop behind.
 *  2. **Bounded in duration too**: the timer is cleared when the attempt ends,
 *     when this block unmounts (another kind, another screen) and on unmount of
 *     the page — and a reply that lands afterwards is dropped, not rendered.
 *
 * The in-flight ref is what stops the timer from stacking requests: a slow
 * `wechat.login.state` means the next tick does nothing, so the interval can
 * never have two requests in the air for one attempt.
 */
function WechatQrLogin({ onConnected }: { onConnected: () => void }) {
  const t = useT();
  const [phase, setPhase] = useState<WechatLoginPhase>({ kind: 'idle' });

  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const inFlightRef = useRef(false);
  const triesRef = useRef(0);
  // A poll reply must not touch a component that is already gone.
  const aliveRef = useRef(true);

  const stopPolling = useCallback(() => {
    if (timerRef.current !== null) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
      stopPolling();
    };
  }, [stopPolling]);

  const pollOnce = useCallback(
    async (qrId: string, qrUrl: string) => {
      // One request per slot: a slow answer skips the ticks that land during it.
      if (inFlightRef.current) return;
      inFlightRef.current = true;
      triesRef.current += 1;
      try {
        const next = await pollWechatLoginState(qrId);
        if (!aliveRef.current) return;
        if (next.connected || next.state === 'confirmed') {
          stopPolling();
          setPhase({ kind: 'confirmed' });
          onConnected();
          return;
        }
        if (next.state === 'expired') {
          stopPolling();
          setPhase({ kind: 'expired' });
          return;
        }
        if (triesRef.current >= WECHAT_POLL_MAX_TRIES) {
          stopPolling();
          setPhase({ kind: 'exhausted' });
          return;
        }
        setPhase({ kind: 'polling', qrUrl, state: next.state });
      } catch (cause) {
        if (!aliveRef.current) return;
        stopPolling();
        setPhase({ kind: 'failed', title: WECHAT_LOGIN_KEYS.pollFailed, message: messageOf(cause) });
      } finally {
        inFlightRef.current = false;
      }
    },
    [onConnected, stopPolling],
  );

  const start = useCallback(async () => {
    if (phase.kind === 'starting' || phase.kind === 'polling') return;
    stopPolling();
    setPhase({ kind: 'starting' });
    let login: WechatLoginStart;
    try {
      login = await startWechatLogin();
    } catch (cause) {
      if (aliveRef.current) {
        setPhase({ kind: 'failed', title: WECHAT_LOGIN_KEYS.startFailed, message: messageOf(cause) });
      }
      return;
    }
    // The block may have unmounted while the request was out: a timer nobody
    // can see, polling a code nobody can read, is exactly the leak to avoid.
    if (!aliveRef.current) return;
    triesRef.current = 0;
    inFlightRef.current = false;
    setPhase({ kind: 'polling', qrUrl: login.qrUrl, state: 'pending' });
    timerRef.current = setInterval(
      () => void pollOnce(login.qrId, login.qrUrl),
      WECHAT_POLL_INTERVAL_MS,
    );
  }, [phase.kind, pollOnce, stopPolling]);

  const inFlight = phase.kind === 'starting' || phase.kind === 'polling';
  const qrUrl = phase.kind === 'polling' ? phase.qrUrl : null;
  const stateWord =
    phase.kind === 'polling'
      ? phase.state === 'waiting'
        ? t(WECHAT_LOGIN_KEYS.waiting)
        : phase.state === 'pending'
          ? t(WECHAT_LOGIN_KEYS.pending)
          : // An unknown state is a protocol value: shown verbatim rather than
            // translated, because a state this build does not know is a fact.
            phase.state
      : null;

  return (
    <div className="zk-field" data-testid="channels-wechat-login">
      <div className="zk-field__label-row">
        <span className="zk-field__label">{t(WECHAT_LOGIN_KEYS.title)}</span>
      </div>
      <div className="zk-field__control">
        <Flex vertical gap="small">
          <p className="zk-field__help">{t(WECHAT_LOGIN_KEYS.note)}</p>
          <Flex gap="small" align="center" wrap>
            {/* Not a primary: 保存 stays the screen's only filled button. */}
            <Button
              onClick={() => void start()}
              loading={phase.kind === 'starting'}
              disabled={inFlight}
              data-testid="channels-wechat-login-start"
            >
              {phase.kind === 'idle'
                ? t(WECHAT_LOGIN_KEYS.generate)
                : t(WECHAT_LOGIN_KEYS.regenerate)}
            </Button>
            {phase.kind === 'polling' && stateWord ? (
              <Typography.Text type="secondary" role="status" data-testid="channels-wechat-login-state">
                {stateWord}
              </Typography.Text>
            ) : null}
            {phase.kind === 'confirmed' ? (
              <Typography.Text className="zk-notice" role="status" data-testid="channels-wechat-login-state">
                {t(WECHAT_LOGIN_KEYS.connected)}
              </Typography.Text>
            ) : null}
            {phase.kind === 'expired' || phase.kind === 'exhausted' ? (
              <Typography.Text type="warning" role="status" data-testid="channels-wechat-login-state">
                {phase.kind === 'expired'
                  ? t(WECHAT_LOGIN_KEYS.expired)
                  : t(WECHAT_LOGIN_KEYS.exhausted)}
              </Typography.Text>
            ) : null}
          </Flex>
          {qrUrl ? (
            <>
              {/* No QR renderer ships with this client, and adding one is not
                  this screen's call: the code's own URL is shown as selectable
                  text with the note that says what to do with it. */}
              <Typography.Paragraph
                copyable
                style={WRAP_LONG}
                data-testid="channels-wechat-login-url"
              >
                {qrUrl}
              </Typography.Paragraph>
              <Typography.Text type="secondary">{t(WECHAT_LOGIN_KEYS.scanHint)}</Typography.Text>
            </>
          ) : null}
          {phase.kind === 'failed' ? (
            <Alert
              type="error"
              showIcon
              role="alert"
              title={t(phase.title)}
              description={phase.message}
            />
          ) : null}
        </Flex>
      </div>
    </div>
  );
}

export function ChannelsScreen() {
  const t = useT();
  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title" style={WRAP_LONG}>
          {t('channels.title')}
        </h1>
        <p className="zk-screen__note" style={WRAP_LONG}>
          {t('channels.note')}
        </p>
      </header>
      <Flex vertical gap="large">
        <CredentialsSection />
        <WebhookSourcesSection />
        <NotifyRoutesSection />
      </Flex>
    </div>
  );
}

/* ------------------------------------------------------------ credentials -- */

function CredentialsSection() {
  const t = useT();
  const narrow = useMediaQuery(NARROW_QUERY);
  const [kind, setKind] = useState<CredentialKind>('wechat');
  const [id, setId] = useState('default');
  const [values, setValues] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [savedId, setSavedId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const read = useCallback(() => listCredentials(kind), [kind]);
  const { state, reload, refresh } = useSectionValue(read, kind);

  // A finished login stored a credential the adapter can use, so the list this
  // section is showing is stale — re-read it instead of assuming the row.
  const onWechatConnected = useCallback(() => {
    void refresh();
  }, [refresh]);

  const spec = CREDENTIAL_FIELDS[kind];
  // Every field of the chosen kind is required by `credentials.set`; a form that
  // submitted half of them would store a credential nobody can use.
  const complete = spec.every((field) => (values[field.name] ?? '').trim() !== '');

  const save = async () => {
    if (!complete || saving) return;
    const credentialId = id.trim() === '' ? 'default' : id.trim();
    const fields: Record<string, string> = {};
    for (const field of spec) fields[field.name] = values[field.name] ?? '';

    setSaving(true);
    setActionError(null);
    setSavedId(null);
    try {
      await setCredential({ kind, id: credentialId, fields });
      // The secret is gone from the form the moment it is stored: the page can
      // never show it again, so it must not keep a copy in its own state.
      setValues({});
      setSavedId(credentialId);
      await refresh();
    } catch (cause) {
      setActionError(describeError(cause, t, 'channels.saveFailed'));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (row: CredentialInfo) => {
    setBusyId(row.id);
    setActionError(null);
    setSavedId(null);
    try {
      await deleteCredential({ kind: row.kind, id: row.id });
      await refresh();
    } catch (cause) {
      setActionError(describeError(cause, t, 'channels.deleteFailed'));
    } finally {
      setBusyId(null);
    }
  };

  return (
    <Section titleKey="channels.credentials.title" noteKey="channels.credentials.note">
      <SectionState state={state} onRetry={reload} />

      <Field id="channels-credential-kind" label={t('channels.credentials.kind')}>
        <Select
          id="channels-credential-kind"
          value={kind}
          options={KIND_OPTIONS}
          onChange={(next: CredentialKind) => {
            setKind(next);
            // The field names belong to the kind, so the old values would be
            // sent under names the new kind does not own.
            setValues({});
            setSavedId(null);
            setActionError(null);
          }}
          aria-label={t('channels.credentials.kind')}
          data-testid="channels-credential-kind-select"
        />
      </Field>

      {/* WeChat is the one kind whose credential a human can obtain here by
          scanning a code; the other three are typed in below. */}
      {kind === 'wechat' ? <WechatQrLogin onConnected={onWechatConnected} /> : null}

      <Field
        id="channels-credential-id"
        label={t('channels.credentials.id')}
        help={t('channels.credentials.idHelp')}
      >
        <Input
          id="channels-credential-id"
          value={id}
          placeholder="default"
          autoComplete="off"
          onChange={(event) => setId(event.target.value)}
          data-testid="channels-credential-id-input"
        />
      </Field>

      {spec.map((field) => (
        <Field
          key={field.name}
          id={`channels-credential-${field.name}`}
          label={t(field.labelKey)}
          kind={field.secret ? 'secret' : 'text'}
        >
          {field.secret ? (
            <Input.Password
              id={`channels-credential-${field.name}`}
              value={values[field.name] ?? ''}
              autoComplete="off"
              onChange={(event) =>
                setValues((current) => ({ ...current, [field.name]: event.target.value }))
              }
              data-testid={`channels-credential-field-${field.name}`}
            />
          ) : (
            <Input
              id={`channels-credential-${field.name}`}
              value={values[field.name] ?? ''}
              autoComplete="off"
              onChange={(event) =>
                setValues((current) => ({ ...current, [field.name]: event.target.value }))
              }
              data-testid={`channels-credential-field-${field.name}`}
            />
          )}
        </Field>
      ))}

      <Flex gap="small" align="center" wrap>
        <Button
          type="primary"
          size={narrow ? 'large' : undefined}
          onClick={() => void save()}
          loading={saving}
          disabled={!complete}
          data-testid="channels-credential-save"
        >
          {t('channels.credentials.save')}
        </Button>
        {!complete ? (
          <Typography.Text type="secondary">
            {t('channels.credentials.incomplete')}
          </Typography.Text>
        ) : null}
        {savedId ? (
          <Typography.Text className="zk-notice" role="status" data-testid="channels-credential-saved">
            {t('channels.savedId', { id: savedId })}
          </Typography.Text>
        ) : null}
      </Flex>

      {actionError ? (
        <Alert type="error" showIcon role="alert" closable title={actionError} />
      ) : null}

      <Typography.Text type="secondary">{t('channels.credentials.stored')}</Typography.Text>
      {state.kind === 'ready' && state.value.length === 0 ? (
        <Empty
          description={t('channels.credentials.empty')}
          image={Empty.PRESENTED_IMAGE_SIMPLE}
        />
      ) : null}
      {state.kind === 'ready' && state.value.length > 0 ? (
        <ul className="zk-inbox__list">
          {state.value.map((row) => (
            <li
              key={`${row.kind}:${row.id}`}
              className="zk-inbox__row"
              data-testid={`channels-credential-${row.kind}-${row.id}`}
            >
              <div className="zk-inbox__body" style={SHRINKABLE}>
                <div className="zk-inbox__title" style={WRAP_LONG}>
                  <Tag>{row.kind}</Tag>
                  <span>{row.id}</span>
                </div>
                {/* Identity only: the list API has no value to render, and the
                    page never kept one (see `save` above). */}
                <div className="zk-inbox__meta">{t('channels.credentials.writeOnly')}</div>
              </div>
              <div className="zk-inbox__actions">
                <Popconfirm
                  title={t('channels.credentials.deleteConfirm', { kind: row.kind, id: row.id })}
                  okText={t('channels.confirmDelete')}
                  cancelText={t('ui.cancel')}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void remove(row)}
                >
                  <Button
                    size="small"
                    disabled={busyId === row.id}
                    data-testid={`channels-credential-delete-${row.kind}-${row.id}`}
                  >
                    {t('channels.credentials.delete')}
                  </Button>
                </Popconfirm>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </Section>
  );
}

/* --------------------------------------------------------- webhook sources -- */

function WebhookSourcesSection() {
  const t = useT();
  const [id, setId] = useState('');
  const [secret, setSecret] = useState('');
  const [channel, setChannel] = useState('');
  const [adding, setAdding] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const read = useCallback(() => listWebhookSources(), []);
  const { state, reload, refresh } = useSectionValue(read);

  const ready = id.trim() !== '' && secret.trim() !== '';

  const add = async () => {
    if (!ready || adding) return;
    setAdding(true);
    setActionError(null);
    try {
      await setWebhookSource({ id: id.trim(), secret, channel: channel.trim() });
      // The secret must not survive its own request.
      setSecret('');
      setId('');
      setChannel('');
      await refresh();
    } catch (cause) {
      setActionError(describeError(cause, t, 'channels.saveFailed'));
    } finally {
      setAdding(false);
    }
  };

  const remove = async (row: WebhookSourceInfo) => {
    setBusyId(row.id);
    setActionError(null);
    try {
      await removeWebhookSource(row.id);
      await refresh();
    } catch (cause) {
      setActionError(describeError(cause, t, 'channels.deleteFailed'));
    } finally {
      setBusyId(null);
    }
  };

  return (
    <Section titleKey="channels.webhooks.title" noteKey="channels.webhooks.note">
      <SectionState state={state} onRetry={reload} />

      <Field
        id="channels-webhook-id"
        label={t('channels.webhooks.id')}
        help={t('channels.webhooks.idHelp')}
      >
        <Input
          id="channels-webhook-id"
          value={id}
          autoComplete="off"
          onChange={(event) => setId(event.target.value)}
          data-testid="channels-webhook-id-input"
        />
      </Field>

      <Field id="channels-webhook-secret" label={t('channels.webhooks.secret')} kind="secret">
        <Input.Password
          id="channels-webhook-secret"
          value={secret}
          autoComplete="off"
          onChange={(event) => setSecret(event.target.value)}
          data-testid="channels-webhook-secret-input"
        />
      </Field>

      <Field id="channels-webhook-channel" label={t('channels.webhooks.channel')}>
        <Input
          id="channels-webhook-channel"
          value={channel}
          autoComplete="off"
          onChange={(event) => setChannel(event.target.value)}
          data-testid="channels-webhook-channel-input"
        />
      </Field>

      <Flex gap="small" align="center" wrap>
        <Button
          onClick={() => void add()}
          loading={adding}
          disabled={!ready}
          data-testid="channels-webhook-add"
        >
          {t('channels.webhooks.add')}
        </Button>
      </Flex>

      {actionError ? (
        <Alert type="error" showIcon role="alert" closable title={actionError} />
      ) : null}

      <Typography.Text type="secondary">{t('channels.webhooks.stored')}</Typography.Text>
      {state.kind === 'ready' && state.value.length === 0 ? (
        <Empty description={t('channels.webhooks.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : null}
      {state.kind === 'ready' && state.value.length > 0 ? (
        <ul className="zk-inbox__list">
          {state.value.map((row) => (
            <li
              key={row.id}
              className="zk-inbox__row"
              data-testid={`channels-webhook-${row.id}`}
            >
              <div className="zk-inbox__body" style={SHRINKABLE}>
                <div className="zk-inbox__title" style={WRAP_LONG}>
                  <Tag>{row.id}</Tag>
                  <span>{row.channel ?? t('channels.webhooks.noChannel')}</span>
                </div>
              </div>
              <div className="zk-inbox__actions">
                <Popconfirm
                  title={t('channels.webhooks.deleteConfirm', { id: row.id })}
                  okText={t('channels.confirmDelete')}
                  cancelText={t('ui.cancel')}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void remove(row)}
                >
                  <Button
                    size="small"
                    disabled={busyId === row.id}
                    data-testid={`channels-webhook-delete-${row.id}`}
                  >
                    {t('channels.webhooks.delete')}
                  </Button>
                </Popconfirm>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </Section>
  );
}

/* ------------------------------------------------------- notification routes -- */

interface RouteDraft {
  primary: string;
  fallback: string;
  quietHours: string;
  enabled: boolean;
}

function draftFrom(route: NotifyRoute): RouteDraft {
  return {
    primary: route.primary,
    fallback: route.fallback ?? '',
    quietHours: route.quietHours ?? '',
    enabled: route.enabled,
  };
}

function NotifyRoutesSection() {
  const t = useT();
  const [drafts, setDrafts] = useState<Record<string, RouteDraft>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const read = useCallback(() => loadNotifyRoutes(), []);
  const { state, reload, refresh } = useSectionValue(read);

  const draftFor = (route: NotifyRoute): RouteDraft => drafts[route.category] ?? draftFrom(route);

  const edit = (route: NotifyRoute, patch: Partial<RouteDraft>) => {
    setDrafts((current) => ({
      ...current,
      [route.category]: { ...(current[route.category] ?? draftFrom(route)), ...patch },
    }));
    setSaved(null);
  };

  const save = async (route: NotifyRoute) => {
    const draft = draftFor(route);
    setBusy(route.category);
    setActionError(null);
    setSaved(null);
    try {
      await upsertNotifyRoute({
        category: route.category,
        primary: draft.primary,
        enabled: draft.enabled,
        // Absent means "unchanged by this write"; nothing here invents an
        // empty string for a field the user did not fill in.
        ...(draft.fallback.trim() === '' ? {} : { fallback: draft.fallback }),
        ...(draft.quietHours.trim() === '' ? {} : { quietHours: draft.quietHours }),
      });
      // Drop the draft so the row renders the gateway's answer, not our hope.
      setDrafts((current) => {
        const next = { ...current };
        delete next[route.category];
        return next;
      });
      setSaved(route.category);
      await refresh();
    } catch (cause) {
      setActionError(describeError(cause, t, 'channels.saveFailed'));
    } finally {
      setBusy(null);
    }
  };

  return (
    <Section titleKey="channels.notify.title" noteKey="channels.notify.note">
      <SectionState state={state} onRetry={reload} />

      {actionError ? (
        <Alert type="error" showIcon role="alert" closable title={actionError} />
      ) : null}

      {state.kind === 'ready' && state.value.length === 0 ? (
        <Empty description={t('channels.notify.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : null}

      {state.kind === 'ready' && state.value.length > 0 ? (
        <ul className="zk-inbox__list">
          {state.value.map((route) => {
            const draft = draftFor(route);
            const primaryId = `channels-notify-primary-${route.category}`;
            const fallbackId = `channels-notify-fallback-${route.category}`;
            const quietId = `channels-notify-quiet-${route.category}`;
            const enabledId = `channels-notify-enabled-${route.category}`;
            return (
              <li
                key={route.category}
                className="zk-inbox__row"
                data-testid={`channels-notify-${route.category}`}
              >
                <div className="zk-inbox__body" style={SHRINKABLE}>
                  <div className="zk-inbox__title" style={WRAP_LONG}>
                    {/* The category is a protocol value: shown verbatim, never
                        translated (README hard constraint). */}
                    <Tag>{route.category}</Tag>
                  </div>
                  <Flex gap="middle" wrap align="end">
                    <Field id={primaryId} label={t('channels.notify.primary')}>
                      <Select
                        id={primaryId}
                        size="small"
                        value={draft.primary}
                        options={CHANNEL_OPTIONS}
                        onChange={(next: string) => edit(route, { primary: next })}
                        aria-label={t('channels.notify.primary')}
                        data-testid={`channels-notify-primary-select-${route.category}`}
                      />
                    </Field>
                    <Field id={fallbackId} label={t('channels.notify.fallback')}>
                      <Select
                        id={fallbackId}
                        size="small"
                        allowClear
                        value={draft.fallback === '' ? undefined : draft.fallback}
                        placeholder={t('channels.notify.fallbackNone')}
                        options={CHANNEL_OPTIONS}
                        onChange={(next?: string) => edit(route, { fallback: next ?? '' })}
                        aria-label={t('channels.notify.fallback')}
                        data-testid={`channels-notify-fallback-select-${route.category}`}
                      />
                    </Field>
                    <Field id={quietId} label={t('channels.notify.quietHours')}>
                      <Input
                        id={quietId}
                        size="small"
                        value={draft.quietHours}
                        placeholder={t('channels.notify.quietHoursPlaceholder')}
                        autoComplete="off"
                        onChange={(event) => edit(route, { quietHours: event.target.value })}
                        data-testid={`channels-notify-quiet-input-${route.category}`}
                      />
                    </Field>
                    <Field id={enabledId} label={t('channels.notify.enabled')}>
                      <Switch
                        id={enabledId}
                        size="small"
                        checked={draft.enabled}
                        onChange={(next) => edit(route, { enabled: next })}
                        aria-label={t('channels.notify.enabled')}
                        data-testid={`channels-notify-enabled-switch-${route.category}`}
                      />
                    </Field>
                  </Flex>
                  {saved === route.category ? (
                    <span className="zk-notice" role="status">
                      {t('channels.notify.saved')}
                    </span>
                  ) : null}
                </div>
                <div className="zk-inbox__actions">
                  <Button
                    size="small"
                    onClick={() => void save(route)}
                    loading={busy === route.category}
                    data-testid={`channels-notify-save-${route.category}`}
                  >
                    {t('channels.notify.save')}
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      ) : null}
    </Section>
  );
}