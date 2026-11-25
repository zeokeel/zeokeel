/**
 * M6 · 成员 (the roster and the role policy).
 *
 * Three things shape this screen, and the first one is a matter of not lying:
 *
 *  1. **A role is a policy table, not sign-in.** The product has no login, no
 *     password and no session; the gateway trusts the browser on this machine
 *     and `users.act` merely records *who this browser is acting as* in a
 *     settings row. A roster of people with roles looks like an auth system, so
 *     the header says outright that it is not one, and the legend says what a
 *     role actually decides (dispatch / approve / manage bots / review memory /
 *     decide budget — the five rows of `contracts.Allowed`, mirrored in
 *     `./roles.ts`).
 *  2. **A change sends the whole member.** `users.upsert` requires `id`, `name`
 *     and `role` (there is no patch shape, see `api/users.ts`), so a role change
 *     echoes the stored row with one field changed — see {@link wholeRow}.
 *  3. **No timer.** None of the three methods has a delta stream, so the roster
 *     is read on mount and again after this screen's own write. A poll would be
 *     the only way to notice another client's change, and that is the polling
 *     loop AGENTS.md §3 forbids. The write's own answer is not trusted as the
 *     new roster either — `users.act` answers with an id, and the gateway
 *     resolves the *effective* identity (it falls back to the first owner), so
 *     only a re-read says who is acting now.
 *
 * One read means one failure surface: the page is skeleton, or the page is the
 * error with 重试 — a section cannot claim "nobody" when the snapshot never
 * arrived. A failed *write* is different: nothing changed, so the list stays as
 * it was and the gateway's own words (including the last-owner refusal) are
 * printed above it.
 */
import { useCallback, useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Empty, Flex, Input, Select, Skeleton, Tag } from 'antd';
import {
  actAsUser,
  loadUsers,
  upsertUser,
  USER_ROLES,
  type User,
  type UsersSnapshot,
} from '@/api/users';
import { useMediaQuery } from '@/design/use-system-appearance';
import { describeError } from '@/features/describe-error';
import { formatRelativeTime } from '@/features/inbox/state';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';
import { rolesFor, USER_ACTIONS, type UserAction } from './roles';

type Translate = (key: MessageKey, params?: Record<string, string | number>) => string;

/** A long id must wrap inside its row instead of widening the page. */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** A flex child allowed to shrink below its content (`min-width: auto` refuses). */
const SHRINKABLE: CSSProperties = { flex: 1, minWidth: 0 };

/**
 * Below this width 新增成员 — the page's only filled primary — gets antd's
 * `large` (40px): `middle`'s 32px is not a thumb target on a phone, and this is
 * the same full web client over the LAN (the bots and channels screens make the
 * same call).
 */
const NARROW_QUERY = '(max-width: 768px)';

/**
 * Role → its own words. These are protocol values, so the *table* is the only
 * place the mapping lives; a role this build does not know is shown verbatim
 * rather than folded into a default (which would read as a permission claim).
 */
const ROLE_KEYS: Record<string, MessageKey> = {
  owner: 'users.roles.owner',
  manager: 'users.roles.manager',
  collaborator: 'users.roles.collaborator',
};

/**
 * A role tag's colour. Owner and manager get one each because they are the two
 * roles with powers a reader must be able to spot at a glance; a collaborator
 * (dispatch only) wears the neutral default, and an unknown role is never
 * coloured — a colour would claim a rank this build cannot vouch for.
 */
const ROLE_COLORS: Record<string, string> = { owner: 'gold', manager: 'blue' };

/** The five capabilities, in the contract's own order, with their own words. */
const ACTION_KEYS: Record<UserAction, MessageKey> = {
  dispatch: 'users.action.dispatch',
  approve: 'users.action.approve',
  'manage-bots': 'users.action.manageBots',
  'review-memory': 'users.action.reviewMemory',
  'decide-budget': 'users.action.decideBudget',
};

/**
 * The role a new member starts with: the least power the product has. A form
 * that opened on `owner` would hand out the account's powers on a stray submit.
 */
const DEFAULT_ROLE = 'collaborator';

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

type Loaded<T> =
  | { kind: 'loading' }
  | { kind: 'ready'; value: T }
  | { kind: 'error'; message: string };

/** One snapshot plus the instant it landed: the created stamps read against it. */
interface Reading {
  snapshot: UsersSnapshot;
  at: number;
}

/**
 * The one read, as a state. `reload` is the 重试 path (it shows the skeleton
 * again); `refresh` is the re-read after a write (it keeps the page in place, so
 * a save does not blink the roster away).
 */
function useReading(): {
  state: Loaded<Reading>;
  reload: () => void;
  refresh: () => Promise<void>;
} {
  const read = useCallback(async (): Promise<Reading> => {
    const snapshot = await loadUsers();
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

/** A protocol value with no display word of its own: verbatim, or an em dash. */
function orDash(value: string | undefined): string {
  return value !== undefined && value.trim() !== '' ? value : '—';
}

/** A role as a person reads it (unknown roles verbatim — never a default). */
function roleWord(role: string, t: Translate): string {
  const key = ROLE_KEYS[role];
  return key ? t(key) : orDash(role);
}

function roleOptions(t: Translate): Array<{ value: string; label: string }> {
  return USER_ROLES.map((role) => ({ value: role, label: roleWord(role, t) }));
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

export function UsersScreen() {
  const t = useT();
  const narrow = useMediaQuery(NARROW_QUERY);
  const { state, reload, refresh } = useReading();
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [draft, setDraft] = useState({ id: '', name: '', role: DEFAULT_ROLE });

  /**
   * One write, one re-read. Only one write may be in flight: all three verbs
   * rewrite the same roster (a role change is an upsert, and a re-read after a
   * switch must not race another write's answer). A failed write changes
   * nothing, so the roster stays exactly as it was and no read is issued.
   */
  const write = async (
    key: string,
    run: () => Promise<unknown>,
    failureKey: MessageKey,
  ): Promise<boolean> => {
    if (busy !== null) return false;
    setBusy(key);
    setActionError(null);
    try {
      await run();
      await refresh();
      return true;
    } catch (cause) {
      setActionError(describeError(cause, t, failureKey));
      return false;
    } finally {
      setBusy(null);
    }
  };

  const users = state.kind === 'ready' ? state.value.snapshot.users : [];
  const actingUserId = state.kind === 'ready' ? state.value.snapshot.actingUserId : undefined;
  const actingRole = state.kind === 'ready' ? state.value.snapshot.actingRole : undefined;
  const acting = users.find((user) => user.id === actingUserId);
  const canCreate = draft.id.trim() !== '' && draft.name.trim() !== '' && busy === null;

  const create = async () => {
    if (!canCreate) return;
    const created = await write(
      'create',
      () =>
        upsertUser({
          id: draft.id.trim(),
          name: draft.name.trim(),
          role: draft.role,
        }),
      'users.createFailed',
    );
    if (created) setDraft({ id: '', name: '', role: DEFAULT_ROLE });
  };

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title" style={WRAP_LONG}>
          {t('users.title')}
        </h1>
        {/* The honest line the whole screen rests on: a roster with roles is
            not a login system, and saying so is part of the feature. */}
        <p className="zk-screen__note" style={WRAP_LONG} data-testid="users-note">
          {t('users.note')}
        </p>
        {acting ? (
          <p className="zk-screen__note" style={WRAP_LONG} data-testid="users-acting-summary">
            {t('users.actingAs', {
              name: acting.name !== '' ? acting.name : acting.id,
              role: roleWord(actingRole ?? acting.role, t),
            })}
          </p>
        ) : null}
      </header>

      {state.kind === 'loading' ? <Skeleton active title={false} paragraph={{ rows: 3 }} /> : null}

      {state.kind === 'error' ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          title={t('users.loadFailed')}
          description={state.message}
          action={
            <Button size="small" onClick={reload} data-testid="users-retry">
              {t('users.retry')}
            </Button>
          }
        />
      ) : null}

      {state.kind === 'ready' ? (
        <Flex vertical gap="large">
          <Section
            title={t('users.legend.title')}
            note={t('users.legend.note')}
            testId="users-legend"
          >
            <ul className="zk-inbox__list">
              {USER_ACTIONS.map((action) => (
                <li
                  key={action}
                  className="zk-inbox__row"
                  data-testid={`users-capability-${action}`}
                >
                  <div className="zk-inbox__body" style={SHRINKABLE}>
                    <div className="zk-inbox__title" style={WRAP_LONG}>
                      {t(ACTION_KEYS[action])}
                    </div>
                    <div className="zk-inbox__meta" style={WRAP_LONG}>
                      {legendRoles(action, t)}
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          </Section>

          <Section title={t('users.roster.title')} note={t('users.roster.note')} testId="users-roster">
            <Flex gap="middle" wrap align="end">
              <Field id="users-create-id" label={t('users.field.id')}>
                <Input
                  id="users-create-id"
                  value={draft.id}
                  autoComplete="off"
                  onChange={(event) => setDraft((current) => ({ ...current, id: event.target.value }))}
                  data-testid="users-create-id"
                />
              </Field>
              <Field id="users-create-name" label={t('users.field.name')}>
                <Input
                  id="users-create-name"
                  value={draft.name}
                  autoComplete="off"
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, name: event.target.value }))
                  }
                  data-testid="users-create-name"
                />
              </Field>
              <Field id="users-create-role" label={t('users.field.role')}>
                <Select
                  id="users-create-role"
                  value={draft.role}
                  options={roleOptions(t)}
                  onChange={(next: string) => setDraft((current) => ({ ...current, role: next }))}
                  aria-label={t('users.field.role')}
                  data-testid="users-create-role"
                />
              </Field>
              <Button
                type="primary"
                size={narrow ? 'large' : undefined}
                onClick={() => void create()}
                loading={busy === 'create'}
                disabled={!canCreate}
                data-testid="users-create-submit"
              >
                {t('users.create')}
              </Button>
            </Flex>

            {actionError ? (
              <Alert type="error" showIcon role="alert" closable title={actionError} />
            ) : null}

            {users.length === 0 ? (
              <Empty description={t('users.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : (
              <ul className="zk-inbox__list">
                {users.map((user) => (
                  <MemberRow
                    key={user.id}
                    user={user}
                    now={state.value.at}
                    busy={busy}
                    acting={user.id === actingUserId}
                    onChangeRole={(role) =>
                      // Whole row, one field changed: `users.upsert` requires
                      // id, name *and* role, so this echoes the stored name the
                      // user is looking at (see `wholeRow`).
                      void write(
                        `role:${user.id}`,
                        () => upsertUser(wholeRow(user, role)),
                        'users.writeFailed',
                      )
                    }
                    onAct={() =>
                      void write(`act:${user.id}`, () => actAsUser(user.id), 'users.actFailed')
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

/**
 * The roles the policy table allows for one capability.
 *
 * Read from `./roles.ts` rather than hand-written per row, so the legend cannot
 * drift from the matrix the tests pin against `contracts.Allowed`.
 */
function legendRoles(action: UserAction, t: Translate): string {
  const roles = rolesFor(action);
  if (roles.length === USER_ROLES.length) return t('users.legend.allRoles');
  return roles.map((role) => roleWord(role, t)).join(t('users.roles.separator'));
}

function RoleTag({ user }: { user: User }) {
  const t = useT();
  return (
    <Tag color={ROLE_COLORS[user.role]} data-testid={`users-role-tag-${user.id}`}>
      {roleWord(user.role, t)}
    </Tag>
  );
}

function MemberRow({
  user,
  now,
  busy,
  acting,
  onChangeRole,
  onAct,
}: {
  user: User;
  now: number;
  /** The key of the write currently in flight, if any. */
  busy: string | null;
  /** This is the member the gateway says this browser is acting as. */
  acting: boolean;
  onChangeRole: (role: string) => void;
  onAct: () => void;
}) {
  const t = useT();
  // `createdAt` is a fact the gateway stamps; a record without one says 未知
  // instead of stamping "just now" onto a member that carries no time.
  const stamp = formatRelativeTime(user.createdAt, now, t) || t('users.timeUnknown');
  // A write in flight disables every verb on the page: they all rewrite the same
  // roster, so a second one would race this one's re-read.
  const blocked = busy !== null;

  return (
    <li className="zk-inbox__row" data-testid={`users-row-${user.id}`}>
      <div className="zk-inbox__body" style={SHRINKABLE}>
        <div className="zk-inbox__title" style={WRAP_LONG}>
          <RoleTag user={user} />
          <span>{user.name !== '' ? user.name : user.id}</span>
          {acting ? (
            <Tag color="success" data-testid={`users-acting-${user.id}`}>
              {t('users.acting.mark')}
            </Tag>
          ) : null}
        </div>
        <div className="zk-inbox__meta" style={WRAP_LONG}>
          {user.id} · {t('users.field.created')} {stamp}
        </div>
      </div>
      <div className="zk-inbox__actions">
        <Select
          size="small"
          value={ROLE_KEYS[user.role] ? user.role : undefined}
          placeholder={orDash(user.role)}
          options={roleOptions(t)}
          disabled={blocked}
          loading={busy === `role:${user.id}`}
          onChange={(next: string) => onChangeRole(next)}
          aria-label={t('users.field.role')}
          data-testid={`users-role-${user.id}`}
        />
        {/* The acting member shows the marker instead of the verb: "act as the
            person you already are" is a request with no meaning. */}
        {acting ? null : (
          <Button
            size="small"
            disabled={blocked}
            loading={busy === `act:${user.id}`}
            onClick={onAct}
            data-testid={`users-act-${user.id}`}
          >
            {t('users.actAs')}
          </Button>
        )}
      </div>
    </li>
  );
}

/**
 * The whole stored member with one field changed — the only safe shape for a
 * `users.upsert` against an existing member.
 *
 * The contract requires `id`, `name` and `role` on every upsert (there is no
 * patch shape and no optional field), so a role change has to carry the name the
 * row is showing. Sending `{id, role}` would be rejected as incomplete; and a
 * call that carried an empty name would be invalid on its own, because a
 * nameless member cannot be shown or attributed to (`contracts.User.Name`).
 * Snapshot → modify one field → upsert is the flow the gateway documents, and
 * this function is that "modify one field".
 */
function wholeRow(user: User, role: string): { id: string; name: string; role: string } {
  return { id: user.id, name: user.name, role };
}