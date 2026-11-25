/**
 * R27d · 机器人 settings — the two halves of the bot entity (plan G1):
 * `RoleTemplate` (what a岗位 *is*) and `BotInstance` (who does it, with which
 * memory scope, tool grant and default model). One read feeds both
 * (`bots.snapshot`), and the only write is `bots.upsert` (TRANSPORT §5.1).
 *
 * Three properties shape the screen:
 *
 *  1. **模板 is read-only, honestly.** Templates are seeded by the backend and
 *     this build has no method that writes one — so the section shows the five
 *     facts it has and says so, instead of offering an edit that would 404.
 *  2. **A status change sends the whole row.** `bots.upsert` on an existing bot
 *     is replacement-with-rules (see {@link wholeRow}), so the screen sends the
 *     snapshot's row with exactly one field changed. Sending only `status` would
 *     blank the model (`""` = "follow the session") and the owner (`""` =
 *     "unowned"), which the user never touched.
 *  3. **No timer.** Neither method has a delta stream, so a read happens on
 *     mount and again after this screen's own write. A poll would be the only
 *     way to notice another client's change, and that is the polling loop
 *     AGENTS.md §3 forbids.
 *
 * One read means one failure surface: the whole page is skeleton, or the whole
 * page is the error with 重试 — a section cannot claim "no bots" when the
 * snapshot never arrived.
 */
import { useCallback, useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Empty, Flex, Input, Select, Skeleton, Tag } from 'antd';
import {
  loadBots,
  upsertBot,
  type BotInstance,
  type BotTemplate,
  type BotsSnapshot,
  type BotUpsertInput,
} from '@/api/bots';
import { useMediaQuery } from '@/design/use-system-appearance';
import { formatRelativeTime } from '@/features/inbox/state';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';

type Translate = (key: MessageKey, params?: Record<string, string | number>) => string;

/** A long id or model name must wrap inside its row instead of widening the page. */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** A flex child allowed to shrink below its content (`min-width: auto` refuses). */
const SHRINKABLE: CSSProperties = { flex: 1, minWidth: 0 };

/**
 * Below this width 新建 Bot — the page's only filled primary — gets antd's
 * `large` (40px): `middle`'s 32px is not a thumb target on a phone, and this is
 * the same full web client over the LAN (the channels screen makes the same
 * call for its 保存).
 */
const NARROW_QUERY = '(max-width: 768px)';

/**
 * Status → its own words. These are protocol values, so the *table* is the only
 * place the mapping lives; a value this build does not know is shown verbatim
 * rather than being folded into a default (which would read as "dispatchable").
 */
const STATUS_KEYS: Record<string, MessageKey> = {
  active: 'bots.status.active',
  paused: 'bots.status.paused',
  retired: 'bots.status.retired',
};

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

type Loaded<T> =
  | { kind: 'loading' }
  | { kind: 'ready'; value: T }
  | { kind: 'error'; message: string };

/** One snapshot plus the instant it landed: relative stamps are read against it. */
interface Reading {
  snapshot: BotsSnapshot;
  at: number;
}

/**
 * The one read, as a state. `reload` is the 重试 path (it shows the skeleton
 * again); `refresh` is the re-read after a write (it keeps the page in place, so
 * a save does not blink the lists away).
 */
function useReading(): {
  state: Loaded<Reading>;
  reload: () => void;
  refresh: () => Promise<void>;
} {
  const read = useCallback(async (): Promise<Reading> => {
    const snapshot = await loadBots();
    return { snapshot, at: Date.now() };
  }, []);
  const [state, setState] = useState<Loaded<Reading>>({ kind: 'loading' });
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

/**
 * The tool list as a person reads it.
 *
 * An empty grant is **not** "zero tools": the wire type documents it as the
 * role's read-only default set. Printing "0 个工具" would be a false reading of
 * a real value, so the empty case says 默认只读 instead.
 */
function toolSummary(tools: string[] | undefined, t: Translate): string {
  const names = tools ?? [];
  return names.length === 0
    ? t('bots.tools.default')
    : t('bots.toolCount', { count: names.length });
}

/** A protocol value with no display word of its own: verbatim, or an em dash. */
function orDash(value: string | undefined): string {
  return value !== undefined && value.trim() !== '' ? value : '—';
}

function Section({
  title,
  note,
  testId,
  children,
}: {
  title: string;
  note: string;
  testId: string;
  children: ReactNode;
}) {
  return (
    <section className="zk-settings__section" data-testid={testId}>
      <h2 className="zk-settings__section-title">{title}</h2>
      <p className="zk-screen__note">{note}</p>
      {children}
    </section>
  );
}

/** Label → control, the anatomy the other settings surfaces use. */
function Field({
  id,
  label,
  children,
}: {
  id: string;
  label: string;
  children: ReactNode;
}) {
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

export function BotsScreen() {
  const t = useT();
  const narrow = useMediaQuery(NARROW_QUERY);
  const { state, reload, refresh } = useReading();
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [draft, setDraft] = useState({ id: '', templateId: '', name: '', owner: '' });

  /**
   * One write, one re-read. Only one write may be in flight: the verbs all
   * rewrite the same record set, and two overlapping writes would race each
   * other's snapshot. A failed write changes nothing, so the lists stay exactly
   * as they were and no read is issued.
   */
  const write = async (
    key: string,
    input: BotUpsertInput,
    failureKey: MessageKey,
  ): Promise<boolean> => {
    if (busy !== null) return false;
    setBusy(key);
    setActionError(null);
    try {
      await upsertBot(input);
      await refresh();
      return true;
    } catch (cause) {
      setActionError(describeError(cause, t, failureKey));
      return false;
    } finally {
      setBusy(null);
    }
  };

  const templates = state.kind === 'ready' ? state.value.snapshot.templates : [];
  const instances = state.kind === 'ready' ? state.value.snapshot.instances : [];
  // The first template is the default pick: a form that opens with an empty
  // Select makes the user hunt for the only list they can choose from.
  const templateId = draft.templateId !== '' ? draft.templateId : (templates[0]?.id ?? '');
  const canCreate = draft.id.trim() !== '' && templateId !== '' && busy === null;

  const create = async () => {
    if (!canCreate) return;
    const created = await write(
      'create',
      {
        // Only the fields the form actually holds: a name/owner left blank is
        // the user saying "follow the template / unowned", and the contract
        // reads an absent optional value that way too.
        id: draft.id.trim(),
        templateId,
        ...(draft.name.trim() !== '' ? { name: draft.name.trim() } : {}),
        ...(draft.owner.trim() !== '' ? { owner: draft.owner.trim() } : {}),
      },
      'bots.createFailed',
    );
    if (created) setDraft({ id: '', templateId: '', name: '', owner: '' });
  };

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title" style={WRAP_LONG}>
          {t('bots.title')}
        </h1>
        <p className="zk-screen__note" style={WRAP_LONG}>
          {t('bots.note')}
        </p>
      </header>

      {state.kind === 'loading' ? <Skeleton active title={false} paragraph={{ rows: 3 }} /> : null}

      {state.kind === 'error' ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          title={t('bots.loadFailed')}
          description={state.message}
          action={
            <Button size="small" onClick={reload} data-testid="bots-retry">
              {t('bots.retry')}
            </Button>
          }
        />
      ) : null}

      {state.kind === 'ready' ? (
        <Flex vertical gap="large">
          <Section
            title={t('bots.templates.title')}
            note={t('bots.templates.note')}
            testId="bots-templates"
          >
            {templates.length === 0 ? (
              <Empty
                description={t('bots.templates.empty')}
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ) : (
              <ul className="zk-inbox__list">
                {templates.map((template) => (
                  <TemplateRow key={template.id} template={template} />
                ))}
              </ul>
            )}
          </Section>

          <Section
            title={t('bots.instances.title')}
            note={t('bots.instances.note')}
            testId="bots-instances"
          >
            <Flex gap="middle" wrap align="end">
              <Field id="bots-create-id" label={t('bots.field.id')}>
                <Input
                  id="bots-create-id"
                  value={draft.id}
                  autoComplete="off"
                  onChange={(event) => setDraft((current) => ({ ...current, id: event.target.value }))}
                  data-testid="bots-create-id"
                />
              </Field>
              <Field id="bots-create-template" label={t('bots.field.template')}>
                <Select
                  id="bots-create-template"
                  value={templateId === '' ? undefined : templateId}
                  placeholder="—"
                  options={templates.map((template) => ({
                    value: template.id,
                    label: `${template.name} · ${template.id}`,
                  }))}
                  onChange={(next: string) =>
                    setDraft((current) => ({ ...current, templateId: next }))
                  }
                  aria-label={t('bots.field.template')}
                  data-testid="bots-create-template"
                />
              </Field>
              <Field id="bots-create-name" label={t('bots.field.name')}>
                <Input
                  id="bots-create-name"
                  value={draft.name}
                  autoComplete="off"
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, name: event.target.value }))
                  }
                  data-testid="bots-create-name"
                />
              </Field>
              <Field id="bots-create-owner" label={t('bots.field.owner')}>
                <Input
                  id="bots-create-owner"
                  value={draft.owner}
                  autoComplete="off"
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, owner: event.target.value }))
                  }
                  data-testid="bots-create-owner"
                />
              </Field>
              <Button
                type="primary"
                size={narrow ? 'large' : undefined}
                onClick={() => void create()}
                loading={busy === 'create'}
                disabled={!canCreate}
                data-testid="bots-create-submit"
              >
                {t('bots.create')}
              </Button>
            </Flex>

            {actionError ? (
              <Alert type="error" showIcon role="alert" closable title={actionError} />
            ) : null}

            {instances.length === 0 ? (
              <Empty description={t('bots.instances.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : (
              <ul className="zk-inbox__list">
                {instances.map((bot) => (
                  <InstanceRow
                    key={bot.id}
                    bot={bot}
                    now={state.value.at}
                    busy={busy}
                    onStatus={(status) =>
                      void write(`status:${bot.id}`, wholeRow(bot, status), 'bots.writeFailed')
                    }
                  />
                ))}
              </ul>
            )}
          </Section>
        </Flex>
      ) : null}
    </div>
  );
}

/* --------------------------------------------------------------- templates -- */

function TemplateRow({ template }: { template: BotTemplate }) {
  const t = useT();
  const facts = [
    t('bots.field.role') + ' ' + orDash(template.role),
    t('bots.field.defaultModel') + ' ' + orDash(template.defaultModel),
    t('bots.field.memoryScope') + ' ' + orDash(template.memoryScope),
    toolSummary(template.allowedTools, t),
  ].join(' · ');

  return (
    <li className="zk-inbox__row" data-testid={`bots-template-${template.id}`}>
      <div className="zk-inbox__body" style={SHRINKABLE}>
        <div className="zk-inbox__title" style={WRAP_LONG}>
          <Tag>{template.id}</Tag>
          <span>{template.name}</span>
        </div>
        <div className="zk-inbox__meta" style={WRAP_LONG}>
          {facts}
        </div>
      </div>
    </li>
  );
}

/* --------------------------------------------------------------- instances -- */

function StatusTag({ bot }: { bot: BotInstance }) {
  const t = useT();
  const key = STATUS_KEYS[bot.status];
  // `success` is the only colour on this page: active is the one state that
  // means "may be given work". Paused needs nothing from the user (a warning
  // would be a lie) and retired is a fact, not a failure.
  return (
    <Tag
      color={bot.status === 'active' ? 'success' : undefined}
      data-testid={`bots-instance-status-${bot.id}`}
    >
      {key ? t(key) : orDash(bot.status)}
    </Tag>
  );
}

function InstanceRow({
  bot,
  now,
  busy,
  onStatus,
}: {
  bot: BotInstance;
  now: number;
  /** The key of the write currently in flight, if any. */
  busy: string | null;
  onStatus: (status: string) => void;
}) {
  const t = useT();
  // A bot nobody has seen yet has no age: the row says 从未 instead of stamping
  // "just now" onto a record that carries no time.
  const stamp = formatRelativeTime(bot.lastSeenAt, now, t) || t('bots.never');
  const facts = [
    bot.id,
    t('bots.field.template') + ' ' + orDash(bot.templateId),
    t('bots.field.owner') + ' ' + (bot.owner && bot.owner !== '' ? bot.owner : t('bots.unowned')),
    t('bots.field.memoryScope') + ' ' + orDash(bot.memoryScope),
    toolSummary(bot.allowedTools, t),
    t('bots.field.lastSeen') + ' ' + stamp,
  ].join(' · ');
  // A write in flight disables every verb on the page: they all rewrite the same
  // record set, so a second one would race this one's re-read.
  const blocked = busy !== null;

  return (
    <li className="zk-inbox__row" data-testid={`bots-instance-${bot.id}`}>
      <div className="zk-inbox__body" style={SHRINKABLE}>
        <div className="zk-inbox__title" style={WRAP_LONG}>
          <StatusTag bot={bot} />
          <span>{bot.name || bot.id}</span>
        </div>
        <div className="zk-inbox__meta" style={WRAP_LONG}>
          {facts}
        </div>
      </div>
      <div className="zk-inbox__actions">
        {bot.status === 'active' ? (
          <Button
            size="small"
            disabled={blocked}
            loading={busy === `status:${bot.id}`}
            onClick={() => onStatus('paused')}
            data-testid={`bots-pause-${bot.id}`}
          >
            {t('bots.pause')}
          </Button>
        ) : null}
        {bot.status === 'paused' ? (
          <Button
            size="small"
            disabled={blocked}
            loading={busy === `status:${bot.id}`}
            onClick={() => onStatus('active')}
            data-testid={`bots-resume-${bot.id}`}
          >
            {t('bots.resume')}
          </Button>
        ) : null}
        {/* A retired bot shows no verb at all: retiring is terminal, so a
            reactivate button would be a request that must be refused. */}
      </div>
    </li>
  );
}

/**
 * The whole stored row with one field changed — the only safe shape for a
 * `bots.upsert` against an existing bot.
 *
 * Update is replacement-with-rules (`contracts.BotService.Update`): `owner: ""`
 * means "unowned" and `defaultModel: ""` means "follow the session", both
 * written verbatim, while `name` / `memoryScope` / `status: ""` keep the stored
 * value and an absent tool list keeps the grant. So a write that carried only
 * `status` would silently clear the model and the owner of a bot whose row the
 * user was looking at. Snapshot → modify one field → upsert is the documented
 * flow, and this function is that "modify one field".
 */
function wholeRow(bot: BotInstance, status: string): BotUpsertInput {
  return {
    id: bot.id,
    templateId: bot.templateId,
    name: bot.name,
    owner: bot.owner ?? '',
    defaultModel: bot.defaultModel ?? '',
    // A copy: the row's grant is echoed as-is so an update cannot clear it.
    allowedTools: [...(bot.allowedTools ?? [])],
    memoryScope: bot.memoryScope ?? '',
    status,
  };
}

