/**
 * R26 · 团队任务 — the team's task board, grouped by the six status words
 * (`tasks.snapshot`, docs/TRANSPORT.md §5.1.8).
 *
 * Read-once on purpose: `tasks.*` is request/response, so the snapshot is taken
 * when the screen opens and taken again only after 重试. There is no timer here
 * — a poll would invent a freshness the board cannot promise, and the six words
 * are already the honest picture of what the gateway last said.
 *
 * The grouping lives in `./board` (pure, unit tested); this file only renders
 * it. Copy comes from the catalogue, including the relative stamp, which reuses
 * the `inbox.time.*` keys rather than growing a second vocabulary for "2 分钟前".
 */
import { useEffect, useState, type CSSProperties } from 'react';
import { Button, Empty, Flex, Listy, Result, Skeleton, Space, Tag, Typography } from 'antd';
import { Link } from 'react-router';
import { loadTaskBoard, type BoardTask } from '@/api/task';
import { formatRelativeTime } from '@/features/inbox/state';
import { useT } from '@/i18n';
import { groupTasksByStatus } from './board';

/**
 * A subject is free-form and often one unbroken token (a URL, an id). At 390px
 * that token used to widen the board to 1440px: `Space` renders each child in an
 * `.ant-space-item` with `flex: 0 1 auto` **and `min-width: auto`**, so the link
 * refused to shrink below the token. `overflowWrap: anywhere` also lowers the
 * intrinsic min-content width to one character, which `break-word` does not.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere', minWidth: 0 };

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; tasks: BoardTask[]; at: number }
  | { kind: 'error'; message: string };

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/**
 * One read, as a state. `at` is the clock the relative stamps are measured
 * against, captured with the answer so a row's "2 分钟前" cannot change between
 * two renders for no reason.
 */
async function readBoard(): Promise<LoadState> {
  try {
    const tasks = await loadTaskBoard();
    return { kind: 'ready', tasks, at: Date.now() };
  } catch (cause) {
    return { kind: 'error', message: messageOf(cause) };
  }
}

export function TeamScreen() {
  const t = useT();
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches.
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void readBoard().then((next) => {
      if (!cancelled) setState(next);
    });
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  if (state.kind === 'loading') {
    return (
      <div className="zk-screen" aria-busy="true">
        <Skeleton active title={false} paragraph={{ rows: 4 }} />
      </div>
    );
  }

  if (state.kind === 'error') {
    return (
      <div className="zk-screen">
        <Result
          status="error"
          title={t('team.board.loadFailed')}
          subTitle={state.message}
          extra={
            <Button
              onClick={() => {
                // Back to the honest loading state, then read again.
                setState({ kind: 'loading' });
                setAttempt((value) => value + 1);
              }}
            >
              {t('team.board.retry')}
            </Button>
          }
        />
      </div>
    );
  }

  const groups = groupTasksByStatus(state.tasks);
  // A `0` is not a count worth printing (WEB-CLIENT §8.1 A13): the header only
  // speaks about words that actually have rows, and an empty section is not
  // rendered at all.
  const filled = groups.filter((group) => group.count > 0);
  const empty = state.tasks.length === 0;
  const at = state.at;

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title" style={{ overflowWrap: 'anywhere' }}>
          {t('team.board.title')}
        </h1>
        {empty ? null : (
          <Space size={4} wrap>
            {filled.map((group) => (
              <Tag key={group.status} data-testid={`team-count-${group.status}`}>
                {t(group.label)} {group.count}
              </Tag>
            ))}
          </Space>
        )}
      </header>

      {empty ? (
        <Empty description={t('team.board.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : (
        filled.map((group) => (
          <section key={group.status} aria-label={t(group.label)}>
            <Typography.Title level={2}>{t(group.label)}</Typography.Title>
            <Listy
              virtual={false}
              items={group.tasks}
              rowKey="id"
              itemRender={(task) => <BoardRow task={task} now={at} />}
            />
          </section>
        ))
      )}
    </div>
  );
}

interface BoardRowProps {
  task: BoardTask;
  /** The clock the relative stamp is measured against. */
  now: number;
}

/** Subject → `/t/:taskId`; owner or 没人认领; how long since the gateway touched it. */
function BoardRow({ task, now }: BoardRowProps) {
  const t = useT();
  // `updatedAt` is what a reader means by "when did this move"; `createdAt` is
  // the fallback for a row that has never been touched since it was created.
  const stamp = formatRelativeTime(task.updatedAt ?? task.createdAt, now, t);
  return (
    <div data-testid={`team-row-${task.id}`}>
      {/* `Flex` rather than `Space`: its children are the links themselves (no
          `.ant-space-item` wrapper with `min-width: auto`), so the subject can
          shrink and wrap at 390px instead of pushing the row out. */}
      <Flex wrap gap="small" align="baseline">
        {/* The subject is the link: a row with a separate "open" button would
            need two targets for one action. */}
        <Link to={`/t/${task.id}`} style={WRAP_LONG}>
          {task.subject}
        </Link>
        <Typography.Text type="secondary">
          {task.owner || t('team.board.unassigned')}
        </Typography.Text>
        {stamp ? <Typography.Text type="secondary">{stamp}</Typography.Text> : null}
      </Flex>
    </div>
  );
}