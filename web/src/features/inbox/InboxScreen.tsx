/**
 * The inbound work inbox — everything a channel (WeChat / DingTalk / Feishu /
 * email / webhook / cron) handed to the Lead and nobody has decided on yet
 * (`inbox.*`, docs/TRANSPORT.md §5.1.13).
 *
 * Two sections, in the order the interface promises the reader:
 *
 *   现在必须做  status `new` — still waiting, in the gateway's own order
 *   已分诊      task / ignored / archived — decided, kept for reference
 *
 * The surface is read-once: `inbox.*` is request/response, so the snapshot is
 * taken on mount and taken again after each write. There is no timer here on
 * purpose — a poll would be the only way to see another client's change, and
 * inventing one is the polling loop the project forbids.
 *
 * Only the read that produced the rows stays on screen while a write is in the
 * air; the write's own failure is reported next to the queue instead of
 * replacing it, because a failed triage changed nothing.
 */
import { useCallback, useEffect, useState, type CSSProperties } from 'react';
import { Alert, Button, Empty, Flex, Result, Select, Skeleton, Tag } from 'antd';
import {
  ALL_INBOX_STATUSES,
  dispatchInboxItem,
  loadInboxSnapshot,
  triageInboxItem,
  type InboxItem,
  type InboxSnapshot,
} from '@/api/inbox';
import { useMediaQuery } from '@/design/use-system-appearance';
import { useT } from '@/i18n';
import { DISPATCH_ASSIGNEES } from './assignees';
import { formatRelativeTime, groupInboxItems, type InboxSectionKey, type Translate } from './state';

/**
 * A subject is whatever the channel handed over — often a URL or an id with no
 * space in it. `overflowWrap: anywhere` is what keeps such a token inside its
 * row instead of widening the whole screen on a 390px phone; `break-word` is not
 * enough because it leaves the intrinsic min-content width at the full token.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** A flex child allowed to shrink below its content (`min-width: auto` refuses). */
const SHRINKABLE: CSSProperties = { flex: 1, minWidth: 0 };

/**
 * The tap-target breakpoint. Matches the CSS breakpoints global.css already
 * uses for narrow layouts (899 / 768 / 480); below it the row verbs grow to
 * antd's `large` (40px), because a 24px button is not hittable with a thumb.
 */
const NARROW_QUERY = '(max-width: 768px)';

/** Section copy. `groupInboxItems` deals in keys so the words stay in the catalogue. */
function sectionTitle(t: Translate, key: InboxSectionKey): string {
  return key === 'now' ? t('inbox.work.now') : t('inbox.work.triaged');
}

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; snapshot: InboxSnapshot; at: number }
  | { kind: 'error'; message: string };

/**
 * What the last dispatch said about itself.
 *
 * A dispatch can succeed as a *write* and still wake nobody: the gateway creates
 * the task and then reports `dispatch.skipped` (no budget headroom, the
 * concurrency ceiling) or `dispatch.error` (the run could not start). Both mean
 * "the task has an owner and nothing is working on it", which is the state a user
 * mistakes for progress — so it is shown, verbatim, and it is *not* an error of
 * the write (`skipped` is a decision, `error` is a failure).
 */
type DispatchNotice = {
  tone: 'warning' | 'error';
  message: string;
};

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/**
 * One read, as a state. Every value here is either the gateway's answer or the
 * failure it produced — nothing is assumed while the request is in the air.
 *
 * `at` is the clock the rows are measured against, captured with the snapshot,
 * so a row's "2 分钟前" does not change between two renders for no reason.
 */
async function readInbox(): Promise<LoadState> {
  try {
    // The default filter is "still actionable"; the 已分诊 half needs the
    // decided statuses named explicitly (contracts.InboxFilter).
    const snapshot = await loadInboxSnapshot({ statuses: ALL_INBOX_STATUSES });
    return { kind: 'ready', snapshot, at: Date.now() };
  } catch (cause) {
    return { kind: 'error', message: messageOf(cause) };
  }
}

export function InboxScreen() {
  const t = useT();
  const narrow = useMediaQuery(NARROW_QUERY);
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches.
  const [attempt, setAttempt] = useState(0);
  /** The row whose write is in the air — its own buttons are disabled. */
  const [busyId, setBusyId] = useState<string | null>(null);
  /** A failed write (dispatch/triage), shown without discarding the list. */
  const [actionError, setActionError] = useState<string | null>(null);
  /** A dispatch that woke nobody, or failed to: the task exists either way. */
  const [dispatchNotice, setDispatchNotice] = useState<DispatchNotice | null>(null);

  useEffect(() => {
    let cancelled = false;
    void readInbox().then((next) => {
      if (!cancelled) setState(next);
    });
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const decide = useCallback(async (item: InboxItem, work: () => Promise<unknown>) => {
    setBusyId(item.id);
    setActionError(null);
    setDispatchNotice(null);
    try {
      await work();
      // Exactly one re-read after the write — the only refresh this surface does.
      setState(await readInbox());
    } catch (cause) {
      // The write failed: the queue is unchanged, so keep showing it and say why.
      setActionError(messageOf(cause));
    } finally {
      setBusyId(null);
    }
  }, []);

  /**
   * Dispatch one item, optionally to a role, and keep what the gateway answered.
   *
   * The assignee is what makes a wake-up possible at all (only the lead role
   * starts a run), and `dispatch` is the only way to learn whether one started.
   */
  const dispatch = useCallback(
    (item: InboxItem, assignee?: string) =>
      void decide(item, async () => {
        const result = await dispatchInboxItem({
          id: item.id,
          subject: item.title,
          ...(assignee ? { assignee } : {}),
        });
        const outcome = result.dispatch;
        if (outcome?.error) {
          setDispatchNotice({ tone: 'error', message: outcome.error });
        } else if (outcome?.skipped) {
          setDispatchNotice({ tone: 'warning', message: outcome.skipped });
        }
      }),
    [decide],
  );

  if (state.kind === 'loading') {
    return (
      <div className="zk-screen" aria-busy="true">
        <Skeleton active title={false} paragraph={{ rows: 3 }} />
      </div>
    );
  }

  if (state.kind === 'error') {
    return (
      <div className="zk-screen">
        <Result
          status="error"
          title={t('inbox.work.loadFailed')}
          subTitle={state.message}
          extra={
            <Button
              onClick={() => {
                // Back to the honest loading state, then read again.
                setState({ kind: 'loading' });
                setAttempt((value) => value + 1);
              }}
            >
              {t('inbox.work.retry')}
            </Button>
          }
        />
      </div>
    );
  }

  const sections = groupInboxItems(state.snapshot.items);
  const empty = state.snapshot.items.length === 0;

  return (
    <div className="zk-screen">
      <div className="zk-inbox">
        {actionError ? (
          <Alert
            type="error"
            showIcon
            role="alert"
            closable
            title={actionError}
            onClose={() => setActionError(null)}
          />
        ) : null}

        {dispatchNotice ? (
          // `warning`, not `error`: a skipped dispatch is a policy decision the
          // user may want to act on (add budget, wait for a slot), and the task
          // really was created. The reason is the gateway's own sentence.
          <Alert
            type={dispatchNotice.tone}
            showIcon
            role="alert"
            closable
            title={dispatchNotice.message}
            onClose={() => setDispatchNotice(null)}
          />
        ) : null}

        {empty ? (
          <Empty
            description={t('inbox.work.empty')}
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
        ) : (
          sections
            // An empty half is not rendered: "0 项已分诊" is a number that
            // answers nothing (WEB-CLIENT §8.1 A13).
            .filter((section) => section.items.length > 0)
            .map((section) => (
              <section key={section.key} aria-label={sectionTitle(t, section.key)}>
                <h2 className="zk-inbox__label">{sectionTitle(t, section.key)}</h2>
                <ul className="zk-inbox__list">
                  {section.items.map((item) => (
                    <InboxRow
                      key={item.id}
                      item={item}
                      now={state.at}
                      narrow={narrow}
                      busy={busyId === item.id}
                      decided={section.key === 'triaged'}
                      // The item's own line is the subject: the queue is a
                      // triage surface, so the reader confirms, not retypes. The
                      // role is the one thing it cannot confirm for them.
                      onDispatch={(assignee) => dispatch(item, assignee)}
                      onIgnore={() =>
                        void decide(item, () =>
                          triageInboxItem({ id: item.id, action: 'ignored' }),
                        )
                      }
                    />
                  ))}
                </ul>
              </section>
            ))
        )}
      </div>
    </div>
  );
}

interface InboxRowProps {
  item: InboxItem;
  /** The clock the relative stamp is measured against. */
  now: number;
  /** Narrow viewport: stack the actions under the subject and grow the verbs. */
  narrow: boolean;
  /** This row's write is in the air. */
  busy: boolean;
  /** Already decided — no write actions on it any more. */
  decided: boolean;
  /** Dispatch, optionally to a role the reader picked for this row. */
  onDispatch: (assignee?: string) => void;
  onIgnore: () => void;
}

function InboxRow({ item, now, narrow, busy, decided, onDispatch, onIgnore }: InboxRowProps) {
  const t = useT();
  const stamp = formatRelativeTime(item.receivedAt, now, t);
  /**
   * The role this row is dispatched to, per row and not per screen: two items
   * pulled from the same channel routinely belong to different roles, and a
   * screen-wide selection would silently hand the second one to the first's owner.
   *
   * Nothing is preselected. Dispatch is a decision, and the screen does not take
   * it for the reader (the same reason neither verb here is a filled button).
   */
  const [assignee, setAssignee] = useState<string | undefined>(undefined);
  return (
    <li
      className="zk-inbox__row"
      // At 390px the actions move under the subject: side by side, a long title
      // and two verbs cannot both keep their width.
      style={narrow ? { flexDirection: 'column', alignItems: 'stretch' } : undefined}
    >
      <div className="zk-inbox__body" style={SHRINKABLE}>
        <div className="zk-inbox__title" style={WRAP_LONG}>
          {/* The channel family, not a severity: one tag per row, neutral. */}
          <Tag>{item.source}</Tag>
          <span>{item.title}</span>
          {/* Merged arrivals are one row carrying a count, not N rows. A filled
              Tag rather than a red Badge: a screen of red dots would read as an
              alarm, and the accent budget allows at most two red elements. */}
          {item.count > 1 ? <Tag>{t('inbox.work.mergedCount', { count: item.count })}</Tag> : null}
        </div>
        {item.sender || stamp ? (
          // `Flex` rather than two bare spans: `.zk-inbox__meta` has no gap of
          // its own, so "owner" and "10 分钟前" rendered as the single token
          // "owner10 分钟前" at 390px (seen in the Chromium pass).
          <Flex className="zk-inbox__meta" gap="small" wrap>
            {item.sender ? <span>{item.sender}</span> : null}
            {stamp ? <span>{stamp}</span> : null}
          </Flex>
        ) : null}
      </div>
      {decided ? null : (
        <div
          className="zk-inbox__actions"
          // Same width as the subject at 390px, so both verbs stay on screen.
          style={narrow ? { width: '100%', justifyContent: 'stretch' } : undefined}
        >
          {/* Who takes it. A value here is a preset id: the backend wakes the
              lead role and only that one, so "派给谁" is the difference between a
              task somebody runs and a row on a board. */}
          <Select
            data-testid={`inbox-assignee-${item.id}`}
            aria-label={t('task.detail.field.owner')}
            placeholder={t('task.detail.ownerMissing')}
            value={assignee}
            allowClear
            size={narrow ? 'large' : 'small'}
            disabled={busy}
            style={narrow ? { flex: 1 } : undefined}
            onChange={(value: string | undefined) => setAssignee(value || undefined)}
            options={DISPATCH_ASSIGNEES.map(({ value, key }) => ({ value, label: t(key) }))}
          />
          {/* Neither action is filled: the inbox never suggests a default
              (WEB-CLIENT §8.1 A9's principle for decisions). */}
          <Button
            size={narrow ? 'large' : 'small'}
            style={narrow ? { flex: 1 } : undefined}
            disabled={busy}
            onClick={() => onDispatch(assignee)}
          >
            {t('inbox.work.dispatch')}
          </Button>
          <Button
            size={narrow ? 'large' : 'small'}
            style={narrow ? { flex: 1 } : undefined}
            disabled={busy}
            onClick={onIgnore}
          >
            {t('inbox.work.ignore')}
          </Button>
        </div>
      )}
    </li>
  );
}