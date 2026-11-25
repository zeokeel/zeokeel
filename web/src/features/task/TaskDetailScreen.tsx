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
 */
import { useCallback, useEffect, useState } from 'react';
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
  reviewTask,
  submitTask,
  type BoardTask,
  type TaskEvidence,
  type TaskLease,
} from '@/api/task';
import { useT } from '@/i18n';

/**
 * The acting identity.
 *
 * Hard-coded until the shell knows who is signed in: `/t/:taskId` is the Lead's
 * own screen, so the claim it makes is the Lead's. Submitting or reviewing as
 * anybody else would need an identity the gateway does not hand out yet.
 */
const OWNER = 'lead';

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
  try {
    const lease = await loadTaskLease({ id: taskId });
    return { kind: 'ready', task, at, lease, leaseError: null };
  } catch (cause) {
    return { kind: 'ready', task, at, lease: keep, leaseError: messageOf(cause) };
  }
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
}

/**
 * What we can honestly say about the way here.
 *
 * With no lease — nobody holds the task, or the lease read itself failed — every
 * step below it is unknown, so the timeline degrades to the one thing the board
 * does tell us: the state word.
 */
function timelineSteps(lease: TaskLease | null, t: Translate): Step[] {
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
    if (lease.state === 'done') {
      steps.push({ label: t('task.detail.progress.accepted'), detail: lease.reviewNote });
    }
    if (lease.state === 'rejected') {
      steps.push({
        label: t('task.detail.progress.rejected'),
        detail: lease.reviewNote,
        failed: true,
      });
    }
  }
  steps.push({ label: t('task.detail.progress.now') });
  return steps;
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
    <Space size={4} wrap>
      <Tag>{evidenceKindLabel(row.kind, t)}</Tag>
      {isLink(row.ref) ? (
        // External proof opens in a new tab with no opener.
        <a href={row.ref} target="_blank" rel="noreferrer">
          {row.ref}
        </a>
      ) : (
        <Typography.Text code>{row.ref}</Typography.Text>
      )}
      {row.note ? <Typography.Text type="secondary">{row.note}</Typography.Text> : null}
    </Space>
  );
}

export function TaskDetailScreen() {
  const { taskId } = useParams<{ taskId: string }>();
  const id = taskId ?? '';
  const t = useT();

  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches.
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<Pending>(null);
  /** The evidence retry is a read of its own; it only turns that button. */
  const [leaseBusy, setLeaseBusy] = useState(false);
  /** A failed write, shown without discarding the row. */
  const [actionError, setActionError] = useState<string | null>(null);
  const [form, setForm] = useState<Form>('none');
  const [ref, setRef] = useState('');
  const [reason, setReason] = useState('');
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    // No id means the route was reached without a parameter; the render below
    // answers that with the same honest empty state, without a fetch.
    if (!id) return;
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
    await runWrite(() => claimTask({ id, owner: OWNER }));
  };

  /**
   * Re-read **only** the lease.
   *
   * The task is on screen and its row is fine; what failed was the proof. A
   * retry that also re-read the board would flash the whole screen for a second
   * failure in a different half (§3's retry promises exactly one thing).
   */
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
      submitTask({ id, owner: OWNER, evidence: [{ kind: evidenceKindFor(value), ref: value }] }),
    );
    if (ok) {
      setForm('none');
      setRef('');
    }
  };

  const accept = async () => {
    setBusy('accept');
    await runWrite(() => reviewTask({ id, decision: 'accept', reviewer: OWNER }));
  };

  const reject = async () => {
    const note = reason.trim();
    if (!note) return;
    setBusy('reject');
    const ok = await runWrite(() => reviewTask({ id, decision: 'reject', reviewer: OWNER, note }));
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

  const { task, at, lease, leaseError } = state;
  const status = describeStatus(task, lease, t);
  const steps = timelineSteps(lease, t);
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
        <h1 className="zk-screen__title">{task.subject}</h1>
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
                children: task.description || (
                  <Typography.Text type="secondary">{t('task.detail.goalMissing')}</Typography.Text>
                ),
              },
              {
                key: 'owner',
                label: t('task.detail.field.owner'),
                children: task.owner || (
                  <Typography.Text type="secondary">{t('task.detail.ownerMissing')}</Typography.Text>
                ),
              },
              {
                key: 'blockedBy',
                label: t('task.detail.field.blockedBy'),
                children: task.blockedBy?.join('、') || (
                  <Typography.Text type="secondary">{t('task.detail.blockedNone')}</Typography.Text>
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
                color: step.failed ? 'red' : undefined,
                content:
                  step.detail || step.at ? (
                    <Space orientation="vertical" size={0}>
                      {step.detail ? <span>{step.detail}</span> : null}
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
                <Space size={4} wrap>
                  <Typography.Text code>{row.ref}</Typography.Text>
                  {row.note ? (
                    <Typography.Text type="secondary">{row.note}</Typography.Text>
                  ) : null}
                </Space>
              )}
            />
          )}
        </section>

        {/* 5 要不要你 */}
        <section aria-label={t('task.detail.section.needYou')}>
          <Typography.Title level={2}>{t('task.detail.section.needYou')}</Typography.Title>
          {settled ? (
            <Typography.Text type="secondary">{t('task.detail.needYou.none')}</Typography.Text>
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
                  onClick={() => void claim()}
                  loading={busy === 'claim'}
                  disabled={busy !== null && busy !== 'claim'}
                >
                  {t('task.detail.claim')}
                </Button>
                <Button
                  onClick={() => {
                    setActionError(null);
                    setForm((value) => (value === 'evidence' ? 'none' : 'evidence'));
                  }}
                  disabled={busy !== null}
                >
                  {t('task.detail.submitEvidence')}
                </Button>
                <Button
                  onClick={() => void accept()}
                  loading={busy === 'accept'}
                  disabled={busy !== null && busy !== 'accept'}
                >
                  {t('task.detail.accept')}
                </Button>
                <Button
                  danger
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
        </section>
      </Space>
    </div>
  );
}