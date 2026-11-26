/**
 * R3b · `/t/:taskId` — one task, in the five sections a person asks for in this
 * order (docs/plans/grokbot-interaction-design.md §5.4):
 *
 *   1 在做什么     the title (page heading) plus the goal in one line, owner, deps
 *   2 到哪一步了   the state word plus what happened, newest three, expandable
 *   3 证据         the proof attached to the submission
 *   4 产物         the files that proof produced
 *   5 要不要你     "不需要你做什么", or the four verbs that move it on
 *
 * Reading it: two reads, one loading state. `tasks.snapshot` answers with the
 * whole board and the screen picks its row out of it (there is no per-task read);
 * `tasks.lease` answers with the live claim on that row, which is where the
 * evidence and the reviewer's note live. The second read is allowed to fail on
 * its own: a task whose proof did not load still shows its task, with §3 saying
 * why and offering a retry, because "the evidence did not load" and "this task
 * has no evidence" are different statements.
 *
 * Every write ends in exactly one re-read (the api is request/response, so a
 * re-read is the only way to observe what the write did to the board); a failed
 * write keeps the row on screen and reports itself next to the buttons, because
 * a failed write changed nothing.
 *
 * Copy comes from the catalogue (`task.detail.*`, and the existing
 * `inbox.time.*` for relative stamps) — this file carries no user-visible
 * string of its own.
 *
 * §2 also carries the 执行手段 block (`ExecutionMeans`): what the product can
 * actually reach (skills, MCP connectors) is the other half of "how is this
 * getting done", so it is read — and allowed to fail — on its own. Next to it
 * sits 预算 (`TaskBudget`): what the work cost against its ceiling, and whether
 * that ceiling stopped it. Three separate reads, three separate failures — none
 * of them can take the task down.
 */
import { useCallback, useEffect, useState, type CSSProperties } from 'react';
import { useParams } from 'react-router';
import {
  Alert,
  Button,
  Descriptions,
  Empty,
  Input,
  Result,
  Skeleton,
  Space,
  Tag,
  Timeline,
  Listy,
  Typography,
} from 'antd';
import {
  claimTask,
  loadTaskBoard,
  loadTaskLease,
  loadTaskReviews,
  reviewTask,
  submitTask,
  type BoardTask,
  type TaskEvidence,
  type TaskLease,
  type TaskReview,
} from '@/api/task';
import { useMediaQuery } from '@/design/use-system-appearance';
import { useT } from '@/i18n';
import { ExecutionMeans } from './ExecutionMeans';
import { PendingSend } from './PendingSend';
import { TaskBudget } from './TaskBudget';
import { TaskRequester } from './TaskRequester';

/**
 * The acting identities — roles, not people.
 *
 * This product has exactly one human, so "somebody else must review it" cannot
 * mean a second person: it means a *different role*. The gateway enforces
 * exactly that — `tasks.review` refuses a reviewer equal to the lease owner
 * (`lease: reviewer is the executor`, `contracts.ErrLeaseReviewerIsOwner`) —
 * which is why doing the work and auditing it use two different identities
 * here. Claim/submit act as the executor role; accept/reject act as the
 * auditor role. Using one constant for both would be self-review.
 */
const EXECUTOR = 'executor';
const AUDITOR = 'auditor';

/**
 * A subject, a goal or an evidence ref is often one unbroken token (a URL, a
 * commit id). `overflowWrap: anywhere` keeps it inside the column at 390px —
 * `break-word` would not, because it leaves the intrinsic min-content width at
 * the whole token, which is what widened the screen to 2255px before this.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/**
 * Below this width the four verbs get antd's `large` (40px): `middle` is 32px,
 * which a thumb misses on a phone — the same full web on a LAN (prototype
 * screen 05), not a cut-down app. Matches the CSS breakpoints global.css uses.
 */
const NARROW_QUERY = '(max-width: 768px)';

/** `useT()`'s translator, threaded into the pure helpers below. */
type Translate = ReturnType<typeof useT>;

/** The read, as a state. Every value is either the gateway's answer or the failure it produced. */
type LoadState =
  | { kind: 'loading' }
  | {
      kind: 'ready';
      task: BoardTask;
      at: number;
      /** The live claim, or `null` when nobody holds the task. */
      lease: TaskLease | null;
      /** `tasks.lease`'s failure, when the task loaded but the proof did not. */
      leaseError: string | null;
      /** The durable review log (`tasks.reviews`), oldest first. */
      reviews: TaskReview[];
      /** `tasks.reviews`'s failure — its own read, its own failure. */
      reviewsError: string | null;
    }
  /** The board answered and this id is not on it — not an error. */
  | { kind: 'missing' }
  | { kind: 'error'; message: string };

/** Which write is in the air. All of them disable every button. */
type Pending = 'claim' | 'submit' | 'accept' | 'reject' | null;

/** Which inline form is open. Only one at a time — they are alternatives. */
type Form = 'none' | 'evidence' | 'reject';

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/**
 * The two reads behind one screen state: the board row, then its lease.
 *
 * Only the first may fail the screen. The second returns the row with
 * `leaseError` instead, and `keep` is what the caller already proved — the lease
 * a write just returned — so a failed re-read cannot throw away evidence the
 * gateway already acknowledged.
 */
async function readTask(taskId: string, keep: TaskLease | null = null): Promise<LoadState> {
  let board: BoardTask[];
  try {
    // The process-wide board is the one `/t/:taskId` links into.
    board = await loadTaskBoard();
  } catch (cause) {
    return { kind: 'error', message: messageOf(cause) };
  }
  const task = board.find((row) => row.id === taskId);
  if (!task) return { kind: 'missing' };
  // The clock the relative stamps are measured against, captured with the read
  // so two renders of the same row do not disagree about "2 分钟前".
  const at = Date.now();
  // Two independent reads behind the same section: the live claim (evidence and
  // the note) and the durable review log. Each is allowed to fail on its own —
  // "the proof did not load" and "the audit trail did not load" are different
  // statements, and neither may take the task down.
  let lease: TaskLease | null = keep;
  let leaseError: string | null = null;
  try {
    lease = await loadTaskLease({ id: taskId });
  } catch (cause) {
    leaseError = messageOf(cause);
  }
  let reviews: TaskReview[] = [];
  let reviewsError: string | null = null;
  try {
    reviews = await loadTaskReviews({ id: taskId });
  } catch (cause) {
    reviewsError = messageOf(cause);
  }
  return { kind: 'ready', task, at, lease, leaseError, reviews, reviewsError };
}

/** Board status + lease state → one of the six status words (design §6.1). */
function describeStatus(
  task: BoardTask,
  lease: TaskLease | null,
  t: Translate,
): { label: string; color: 'default' | 'processing' | 'warning' | 'error' } {
  switch (lease?.state) {
    case 'claimed':
      return { label: t('task.status.running'), color: 'processing' };
    case 'review':
      // Waiting on a human decision — the only state allowed to use the
      // attention colour (design §6.2).
      return { label: t('task.status.waiting'), color: 'warning' };
    case 'done':
      return { label: t('task.status.done'), color: 'default' };
    case 'rejected':
      // 卡住 must carry a next step; the ways out are in section 5.
      return { label: t('task.status.stuck'), color: 'warning' };
    default:
      break;
  }
  switch (task.status) {
    case 'in_progress':
      return { label: t('task.status.running'), color: 'processing' };
    case 'completed':
      return { label: t('task.status.done'), color: 'default' };
    case 'failed':
      // 失败 always comes with a next step; section 5 owns the ways out.
      return { label: t('task.status.failed'), color: 'error' };
    // An unknown status is 排队中: the row exists and is not running, and a
    // protocol value must never reach the screen (design §11.1).
    default:
      return { label: t('task.status.queued'), color: 'default' };
  }
}

function evidenceKindLabel(kind: string, t: Translate): string {
  switch (kind) {
    case 'file':
      return t('task.detail.evidence.kind.file');
    case 'link':
      return t('task.detail.evidence.kind.link');
    case 'output':
      return t('task.detail.evidence.kind.output');
    case 'screenshot':
      return t('task.detail.evidence.kind.screenshot');
    default:
      return t('task.detail.evidence.kind.other');
  }
}

/** Two kinds are inferable from the ref itself; the form has one field on purpose. */
function evidenceKindFor(ref: string): TaskEvidence['kind'] {
  return /^https?:\/\//i.test(ref) ? 'link' : 'file';
}

function isLink(ref: string): boolean {
  return /^https?:\/\//i.test(ref);
}

/** "2 分钟前", measured against the read's clock. `null` when the stamp is unusable. */
function relativeStamp(value: string, now: number, t: Translate): string | null {
  const at = Date.parse(value);
  if (Number.isNaN(at)) return null;
  const minutes = Math.floor(Math.max(0, now - at) / 60_000);
  if (minutes < 1) return t('inbox.time.justNow');
  if (minutes < 60) return t('inbox.time.minutesAgo', { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t('inbox.time.hoursAgo', { count: hours });
  return t('inbox.time.daysAgo', { count: Math.floor(hours / 24) });
}

interface Step {
  label: string;
  detail?: string;
  /** RFC 3339 stamp, when the step has one. */
  at?: string;
  /** The one step that is a failure carries the error colour, not the accent. */
  failed?: boolean;
  /**
   * A step nobody reviewed: the task was closed directly (`task.status` or
   * `team_task_update`), so the timeline must say so instead of borrowing the
   * "accepted" wording. It is not a failure — the work may be fine — but it is
   * the one step that tells the reader the audit trail has a hole.
   */
  unaudited?: boolean;
}

/**
 * What we can honestly say about the way here.
 *
 * With no lease — nobody holds the task, or the lease read itself failed — every
 * step below it is unknown, so the timeline degrades to the one thing the board
 * does tell us: the state word.
 */
function timelineSteps(lease: TaskLease | null, reviews: readonly TaskReview[], t: Translate): Step[] {
  const steps: Step[] = [];
  if (lease) {
    if (lease.heartbeat) {
      steps.push({
        label: t('task.detail.progress.claimed'),
        detail: lease.owner,
        at: lease.heartbeat,
      });
    }
    const count = lease.evidence?.length ?? 0;
    if (count > 0) {
      steps.push({
        label: t('task.detail.progress.submitted'),
        detail: t('task.detail.progress.evidenceCount', { count }),
      });
    }
    // A live lease may still carry the decision (the write answered with it
    // before the row was re-read); the log below is what makes it survive.
    if (lease.state === 'done' && reviews.length === 0) {
      steps.push({ label: t('task.detail.progress.accepted'), detail: lease.reviewNote });
    }
    if (lease.state === 'rejected' && reviews.length === 0) {
      steps.push({
        label: t('task.detail.progress.rejected'),
        detail: lease.reviewNote,
        failed: true,
      });
    }
  }
  // The durable half: every recorded decision, oldest first. This is the only
  // place a review still shows after `tasks.review` deleted the lease it
  // described — without it an accepted task looked like it was never reviewed.
  for (const review of reviews) {
    const failed = review.decision === 'reject';
    // `direct_close` / verdict `unaudited` is a close that skipped the review —
    // the gateway records it so a terminal state cannot be silent. Rendering it
    // as "accepted" would turn "nobody looked at this" into "somebody approved
    // it", which is the exact lie the audit row exists to prevent.
    const unaudited = review.decision === 'direct_close' || review.verdict === 'unaudited';
    if (unaudited) {
      steps.push({
        label: t('task.detail.progress.directClose'),
        detail: review.findings,
        at: review.at,
        unaudited: true,
      });
      continue;
    }
    const who = roleLabel(review.reviewer, t);
    steps.push({
      label: failed
        ? t('task.detail.progress.rejectedBy', { reviewer: who })
        : t('task.detail.progress.acceptedBy', { reviewer: who }),
      detail: review.findings,
      at: review.at,
      ...(failed ? { failed: true } : {}),
    });
  }
  steps.push({ label: t('task.detail.progress.now') });
  return steps;
}

/** The role word for a reviewer identity (roles are preset ids). */
function roleLabel(reviewer: string, t: Translate): string {
  switch (reviewer) {
    case 'lead':
      return t('task.detail.role.lead');
    case 'executor':
      return t('task.detail.role.executor');
    case 'auditor':
      return t('task.detail.role.auditor');
    default:
      return reviewer;
  }
}

/**
 * One row of §3/§4.
 *
 * `Listy` replaces the deprecated antd `List` (6.7 warns on every render and
 * removes it in the next major). It is a plain row renderer with no empty
 * state of its own, so the caller keeps the `Empty` and this stays one line of
 * markup: kind tag, the ref (a link when it is a URL), one line of context.
 */
function EvidenceRow({ row, t }: { row: TaskEvidence; t: Translate }) {
  return (
    <Space size={4} wrap style={WRAP_LONG}>
      <Tag>{evidenceKindLabel(row.kind, t)}</Tag>
      {isLink(row.ref) ? (
        // External proof opens in a new tab with no opener.
        <a href={row.ref} target="_blank" rel="noreferrer" style={WRAP_LONG}>
          {row.ref}
        </a>
      ) : (
        <Typography.Text code style={WRAP_LONG}>
          {row.ref}
        </Typography.Text>
      )}
      {row.note ? (
        <Typography.Text type="secondary" style={WRAP_LONG}>
          {row.note}
        </Typography.Text>
      ) : null}
    </Space>
  );
}

export function TaskDetailScreen() {
  const { taskId } = useParams<{ taskId: string }>();
  const id = taskId ?? '';
  const t = useT();
  const narrow = useMediaQuery(NARROW_QUERY);

  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches.
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<Pending>(null);
  /** The evidence retry is a read of its own; it only turns that button. */
  const [leaseBusy, setLeaseBusy] = useState(false);
  /** The audit-trail retry is a third independent read. */
  const [reviewsBusy, setReviewsBusy] = useState(false);
  /** A failed write, shown without discarding the row. */
  const [actionError, setActionError] = useState<string | null>(null);
  const [form, setForm] = useState<Form>('none');
  const [ref, setRef] = useState('');
  const [reason, setReason] = useState('');
  const [expanded, setExpanded] = useState(false);
  /**
   * Whether a `before_send` task is holding a message: `null` until the
   * 待发送消息 block has read that (it owns the read, its failure and its write).
   * The mirror exists for one line of copy — §5 must not say 不需要你做什么 while
   * the answer is unknown, because a waiting message is exactly what that line
   * would contradict. A failed hold read therefore keeps the line away too.
   */
  const [sendHeld, setSendHeld] = useState<boolean | null>(null);

  useEffect(() => {
    // No id means the route was reached without a parameter; the render below
    // answers that with the same honest empty state, without a fetch.
    if (!id) return;
    // Another task (or a 重试 of this one) is another hold: what the last read
    // proved about it no longer answers for this one.
    setSendHeld(null);
    let cancelled = false;
    void readTask(id).then((next) => {
      if (!cancelled) setState(next);
    });
    return () => {
      cancelled = true;
    };
  }, [id, attempt]);

  /** Run one write, keep its lease, then re-read the board exactly once. */
  const runWrite = useCallback(
    async (work: () => Promise<TaskLease>): Promise<boolean> => {
      setActionError(null);
      try {
        // The write proves the lease; `readTask` keeps it if the re-read's own
        // lease call fails (the board may still answer).
        const written = await work();
        setState(await readTask(id, written));
        return true;
      } catch (cause) {
        // The write failed: the row is unchanged, so keep showing it and say why.
        setActionError(messageOf(cause));
        return false;
      } finally {
        setBusy(null);
      }
    },
    [id],
  );

  const claim = async () => {
    setBusy('claim');
    await runWrite(() => claimTask({ id, owner: EXECUTOR }));
  };

  /**
   * Re-read **only** the lease.
   *
   * The task is on screen and its row is fine; what failed was the proof. A
   * retry that also re-read the board would flash the whole screen for a second
   * failure in a different half (§3's retry promises exactly one thing).
   */
  const retryReviews = async () => {
    setReviewsBusy(true);
    try {
      const reviews = await loadTaskReviews({ id });
      setState((prev) =>
        prev.kind === 'ready' ? { ...prev, reviews, reviewsError: null } : prev,
      );
    } catch (cause) {
      setState((prev) =>
        prev.kind === 'ready' ? { ...prev, reviewsError: messageOf(cause) } : prev,
      );
    } finally {
      setReviewsBusy(false);
    }
  };

  const retryLease = async () => {
    setLeaseBusy(true);
    try {
      const lease = await loadTaskLease({ id });
      setState((prev) => (prev.kind === 'ready' ? { ...prev, lease, leaseError: null } : prev));
    } catch (cause) {
      setState((prev) =>
        prev.kind === 'ready' ? { ...prev, leaseError: messageOf(cause) } : prev,
      );
    } finally {
      setLeaseBusy(false);
    }
  };

  const submit = async () => {
    const value = ref.trim();
    // The button is disabled below this, but a submit event can still arrive
    // through the form; the gateway answers 1010 for an empty list.
    if (!value) return;
    setBusy('submit');
    const ok = await runWrite(() =>
      submitTask({ id, owner: EXECUTOR, evidence: [{ kind: evidenceKindFor(value), ref: value }] }),
    );
    if (ok) {
      setForm('none');
      setRef('');
    }
  };

  const accept = async () => {
    setBusy('accept');
    await runWrite(() => reviewTask({ id, decision: 'accept', reviewer: AUDITOR }));
  };

  const reject = async () => {
    const note = reason.trim();
    if (!note) return;
    setBusy('reject');
    const ok = await runWrite(() => reviewTask({ id, decision: 'reject', reviewer: AUDITOR, note }));
    if (ok) {
      setForm('none');
      setReason('');
    }
  };

  const notFound = (
    <div className="zk-screen">
      <Result
        status="404"
        title={t('task.detail.notFound')}
        subTitle={t('task.detail.notFoundHint')}
      />
    </div>
  );

  if (!id) return notFound;

  if (state.kind === 'loading') {
    return (
      <div className="zk-screen" aria-busy="true">
        <Skeleton active title paragraph={{ rows: 6 }} />
      </div>
    );
  }

  if (state.kind === 'missing') return notFound;

  if (state.kind === 'error') {
    return (
      <div className="zk-screen">
        <Result
          status="error"
          title={t('task.detail.loadFailed')}
          subTitle={state.message}
          extra={
            <Button
              onClick={() => {
                // Back to the honest loading state, then read again.
                setState({ kind: 'loading' });
                setAttempt((value) => value + 1);
              }}
            >
              {t('task.detail.retry')}
            </Button>
          }
        />
      </div>
    );
  }

  const { task, at, lease, leaseError, reviews, reviewsError } = state;
  const status = describeStatus(task, lease, t);
  const steps = timelineSteps(lease, reviews, t);
  const visible = expanded ? steps : steps.slice(-3);
  const evidence = lease?.evidence ?? [];
  // 产物 are the submitted files: the lease is the only thing that carries
  // evidence, and a `file` entry is a product, not a citation.
  const artifacts = evidence.filter((row) => row.kind === 'file');
  // Nothing to ask when the task is finished; otherwise section 5 offers the moves.
  const settled = task.status === 'completed' || lease?.state === 'done';

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        {/* The subject is free-form and can be one long token: it wraps rather
            than widening the column (390px phone, prototype screen 05). */}
        <h1 className="zk-screen__title" style={WRAP_LONG}>
          {task.subject}
        </h1>
        {/* 提出人: who asked for this, as one caption on the subject — not a
            sixth section (the five are a contract). It owns its read and writes
            nothing at all when that read fails: "unknown" is an answer, a
            failure is not. */}
        <TaskRequester taskId={id} />
      </header>

      <Space orientation="vertical" size="large" style={{ width: '100%' }}>
        {/* 1 在做什么 */}
        <section aria-label={t('task.detail.section.doing')}>
          <Typography.Title level={2}>{t('task.detail.section.doing')}</Typography.Title>
          <Descriptions
            size="small"
            column={1}
            items={[
              {
                key: 'goal',
                label: t('task.detail.field.goal'),
                children: (
                  <div style={WRAP_LONG}>
                    {task.description || (
                      <Typography.Text type="secondary">{t('task.detail.goalMissing')}</Typography.Text>
                    )}
                  </div>
                ),
              },
              {
                key: 'owner',
                label: t('task.detail.field.owner'),
                children: (
                  <div style={WRAP_LONG}>
                    {task.owner || (
                      <Typography.Text type="secondary">{t('task.detail.ownerMissing')}</Typography.Text>
                    )}
                  </div>
                ),
              },
              {
                key: 'blockedBy',
                label: t('task.detail.field.blockedBy'),
                children: (
                  <div style={WRAP_LONG}>
                    {task.blockedBy?.join('、') || (
                      <Typography.Text type="secondary">{t('task.detail.blockedNone')}</Typography.Text>
                    )}
                  </div>
                ),
              },
            ]}
          />
        </section>

        {/* 2 到哪一步了 */}
        <section aria-label={t('task.detail.section.progress')}>
          <Typography.Title level={2}>{t('task.detail.section.progress')}</Typography.Title>
          <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
            <Tag color={status.color}>{status.label}</Tag>
            <Timeline
              items={visible.map((step, index) => ({
                key: `${step.label}-${index}`,
                title: step.label,
                // Failure is the error colour; an unaudited close is a warning
                // (the log has a hole a human may want to fill), never the
                // accent an accepted review gets.
                color: step.failed ? 'red' : step.unaudited ? 'orange' : undefined,
                content:
                  step.detail || step.at ? (
                    <Space orientation="vertical" size={0}>
                      {step.detail ? <span style={WRAP_LONG}>{step.detail}</span> : null}
                      {step.at ? (
                        // Relative time; the absolute stamp (with its zone) is
                        // the hover title (design §12.30).
                        <Typography.Text type="secondary" title={step.at}>
                          {relativeStamp(step.at, at, t) ?? step.at}
                        </Typography.Text>
                      ) : null}
                    </Space>
                  ) : undefined,
              }))}
            />
            {steps.length > 3 ? (
              <Button size="small" onClick={() => setExpanded((value) => !value)}>
                {expanded ? t('task.detail.progress.less') : t('task.detail.progress.more')}
              </Button>
            ) : null}

            {/* The audit trail is its own read with its own failure: an empty
                timeline under a since-deleted lease must not read as "nobody
                reviewed this". */}
            {reviewsError ? (
              <Alert
                type="error"
                showIcon
                role="alert"
                title={t('task.detail.reviews.failed')}
                description={reviewsError}
                action={
                  <Button size="small" loading={reviewsBusy} onClick={() => void retryReviews()}>
                    {t('task.detail.retry')}
                  </Button>
                }
              />
            ) : null}

            {/* 执行手段: the other half of "how is this getting done" — what the
                team can actually use (skills + MCP connectors, M5). It sits in
                §2 rather than §5 because it is not a move for the reader to
                make; §5 is the four verbs, and a settled task shows §5 as
                "不需要你做什么", which is exactly when a finished task still owes
                an answer about what did the work. */}
            <ExecutionMeans taskId={id} />

            {/* 预算: the cost side of the same question. A task stops when a bound
                session spends past its ceiling, so the reader learns it here,
                next to what did the work — as a read of its own, which is why a
                failed budget read leaves everything above untouched. Topping up
                is a Lead action on another surface; this block only reports. */}
            <TaskBudget taskId={id} />
          </Space>
        </section>

        {/* 3 证据 */}
        <section aria-label={t('task.detail.section.evidence')}>
          <Typography.Title level={2}>{t('task.detail.section.evidence')}</Typography.Title>
          {leaseError ? (
            // "The proof did not load" is not "there is no proof": the task stays
            // on screen and this half says what actually happened.
            <Alert
              type="error"
              showIcon
              role="alert"
              title={t('task.detail.evidence.failed')}
              description={leaseError}
              action={
                <Button size="small" loading={leaseBusy} onClick={() => void retryLease()}>
                  {t('task.detail.retry')}
                </Button>
              }
            />
          ) : evidence.length === 0 ? (
            <Empty
              description={t('task.detail.evidence.empty')}
              image={Empty.PRESENTED_IMAGE_SIMPLE}
            />
          ) : (
            <Listy
              virtual={false}
              items={evidence}
              rowKey={(row, index) => `${index}:${row.ref}`}
              itemRender={(row) => <EvidenceRow row={row} t={t} />}
            />
          )}
        </section>

        {/* 4 产物 */}
        <section aria-label={t('task.detail.section.artifacts')}>
          <Typography.Title level={2}>{t('task.detail.section.artifacts')}</Typography.Title>
          {artifacts.length === 0 ? (
            <Empty
              description={t('task.detail.artifacts.empty')}
              image={Empty.PRESENTED_IMAGE_SIMPLE}
            />
          ) : (
            <Listy
              virtual={false}
              items={artifacts}
              rowKey={(row, index) => `${index}:${row.ref}`}
              itemRender={(row) => (
                <Space size={4} wrap style={WRAP_LONG}>
                  <Typography.Text code style={WRAP_LONG}>
                    {row.ref}
                  </Typography.Text>
                  {row.note ? (
                    <Typography.Text type="secondary" style={WRAP_LONG}>
                      {row.note}
                    </Typography.Text>
                  ) : null}
                </Space>
              )}
            />
          )}
        </section>

        {/* 5 要不要你 */}
        <section aria-label={t('task.detail.section.needYou')}>
          <Typography.Title level={2}>{t('task.detail.section.needYou')}</Typography.Title>
          <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
            {/* 待发送消息: with approval=before_send the message this task wants to
                send is parked instead of sent, and this is the only place the
                person can learn that — so it sits in §5, the section about what
                they have to do. The block owns its read and its write; it reports
                whether something is waiting so the settled line below never
                contradicts it. */}
            <PendingSend taskId={id} onHeld={setSendHeld} />
            {settled ? (
              // Only a *read* answer may produce this line: while the hold is
              // unknown there is nothing honest to say about it.
              sendHeld === false ? (
                <Typography.Text type="secondary">{t('task.detail.needYou.none')}</Typography.Text>
              ) : null
            ) : (
              <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
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

                <Space wrap>
                  {/* The screen's one primary: taking the task is the move that
                      makes the rest of this section possible. */}
                  <Button
                    type="primary"
                    size={narrow ? 'large' : undefined}
                    onClick={() => void claim()}
                    loading={busy === 'claim'}
                    disabled={busy !== null && busy !== 'claim'}
                  >
                    {t('task.detail.claim')}
                  </Button>
                  <Button
                    size={narrow ? 'large' : undefined}
                    onClick={() => {
                      setActionError(null);
                      setForm((value) => (value === 'evidence' ? 'none' : 'evidence'));
                    }}
                    disabled={busy !== null}
                  >
                    {t('task.detail.submitEvidence')}
                  </Button>
                  <Button
                    size={narrow ? 'large' : undefined}
                    onClick={() => void accept()}
                    loading={busy === 'accept'}
                    disabled={busy !== null && busy !== 'accept'}
                  >
                    {t('task.detail.accept')}
                  </Button>
                  <Button
                    danger
                    size={narrow ? 'large' : undefined}
                    onClick={() => {
                      setActionError(null);
                      setForm((value) => (value === 'reject' ? 'none' : 'reject'));
                    }}
                    disabled={busy !== null}
                  >
                    {t('task.detail.reject')}
                  </Button>
                </Space>

                {form === 'evidence' ? (
                  <form
                    onSubmit={(event) => {
                      event.preventDefault();
                      void submit();
                    }}
                  >
                    <Space.Compact style={{ width: '100%' }}>
                      <Input
                        value={ref}
                        autoFocus
                        aria-label={t('task.detail.evidenceRef')}
                        placeholder={t('task.detail.evidenceRefPlaceholder')}
                        onChange={(event) => setRef(event.target.value)}
                      />
                      <Button
                        htmlType="submit"
                        disabled={ref.trim() === '' || busy !== null}
                        loading={busy === 'submit'}
                      >
                        {t('task.detail.submit')}
                      </Button>
                      <Button
                        onClick={() => {
                          setForm('none');
                          setRef('');
                        }}
                      >
                        {t('task.detail.cancel')}
                      </Button>
                    </Space.Compact>
                  </form>
                ) : null}

                {form === 'reject' ? (
                  <form
                    onSubmit={(event) => {
                      event.preventDefault();
                      void reject();
                    }}
                  >
                    <Space orientation="vertical" size="small" style={{ width: '100%' }}>
                      <Input.TextArea
                        value={reason}
                        autoFocus
                        rows={2}
                        aria-label={t('task.detail.rejectReason')}
                        placeholder={t('task.detail.rejectReasonPlaceholder')}
                        onChange={(event) => setReason(event.target.value)}
                      />
                      <Space>
                        {/* A reject without a reason tells the teammate nothing,
                            so the button stays off until there is one. */}
                        <Button
                          danger
                          htmlType="submit"
                          disabled={reason.trim() === '' || busy !== null}
                          loading={busy === 'reject'}
                        >
                          {t('task.detail.rejectSubmit')}
                        </Button>
                        <Button
                          onClick={() => {
                            setForm('none');
                            setReason('');
                          }}
                        >
                          {t('task.detail.cancel')}
                        </Button>
                      </Space>
                    </Space>
                  </form>
                ) : null}
              </Space>
            )}
          </Space>
        </section>
      </Space>
    </div>
  );
}