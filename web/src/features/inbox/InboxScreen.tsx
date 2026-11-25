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
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Empty, Result, Skeleton, Tag } from 'antd';
import {
  ALL_INBOX_STATUSES,
  dispatchInboxItem,
  loadInboxSnapshot,
  triageInboxItem,
  type InboxItem,
  type InboxSnapshot,
} from '@/api/inbox';
import { useT } from '@/i18n';
import { formatRelativeTime, groupInboxItems, type InboxSectionKey, type Translate } from './state';

/** Section copy. `groupInboxItems` deals in keys so the words stay in the catalogue. */
function sectionTitle(t: Translate, key: InboxSectionKey): string {
  return key === 'now' ? t('inbox.work.now') : t('inbox.work.triaged');
}

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; snapshot: InboxSnapshot; at: number }
  | { kind: 'error'; message: string };

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
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches.
  const [attempt, setAttempt] = useState(0);
  /** The row whose write is in the air — its own buttons are disabled. */
  const [busyId, setBusyId] = useState<string | null>(null);
  /** A failed write (dispatch/triage), shown without discarding the list. */
  const [actionError, setActionError] = useState<string | null>(null);

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
                      busy={busyId === item.id}
                      decided={section.key === 'triaged'}
                      onDispatch={() =>
                        void decide(item, () =>
                          // The item's own line is the subject: the queue is a
                          // triage surface, so the reader confirms, not retypes.
                          dispatchInboxItem({ id: item.id, subject: item.title }),
                        )
                      }
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
  /** This row's write is in the air. */
  busy: boolean;
  /** Already decided — no write actions on it any more. */
  decided: boolean;
  onDispatch: () => void;
  onIgnore: () => void;
}

function InboxRow({ item, now, busy, decided, onDispatch, onIgnore }: InboxRowProps) {
  const t = useT();
  const stamp = formatRelativeTime(item.receivedAt, now, t);
  return (
    <li className="zk-inbox__row">
      <div className="zk-inbox__body">
        <div className="zk-inbox__title">
          {/* The channel family, not a severity: one tag per row, neutral. */}
          <Tag>{item.source}</Tag>
          <span>{item.title}</span>
          {/* Merged arrivals are one row carrying a count, not N rows. A filled
              Tag rather than a red Badge: a screen of red dots would read as an
              alarm, and the accent budget allows at most two red elements. */}
          {item.count > 1 ? <Tag>{t('inbox.work.mergedCount', { count: item.count })}</Tag> : null}
        </div>
        {item.sender || stamp ? (
          <div className="zk-inbox__meta">
            {item.sender ? <span>{item.sender}</span> : null}
            {stamp ? <span>{stamp}</span> : null}
          </div>
        ) : null}
      </div>
      {decided ? null : (
        <div className="zk-inbox__actions">
          {/* Neither action is filled: the inbox never suggests a default
              (WEB-CLIENT §8.1 A9's principle for decisions). */}
          <Button size="small" disabled={busy} onClick={onDispatch}>
            {t('inbox.work.dispatch')}
          </Button>
          <Button size="small" disabled={busy} onClick={onIgnore}>
            {t('inbox.work.ignore')}
          </Button>
        </div>
      )}
    </li>
  );
}