/**
 * R28 · 技能草稿 — the queue of skill proposals a finished task leaves behind
 * (`skilldrafts.snapshot`, `skilldrafts.decide`; docs/TRANSPORT.md §5.1).
 *
 * A task that is accepted now proposes a six-section SKILL.md draft by itself.
 * This screen is where a human reads those proposals: it lists them newest
 * first with their status (待确认 / 已采纳 / 已驳回), the skill name, the
 * one-line summary, a deep link back to the task the draft came from, and the
 * body clamped to three lines until 展开.
 *
 * **采纳 records the decision only.** Nothing installs a skill from here —
 * writing `<dataDir>/skills/<slug>/SKILL.md` stays the skills editor's job, fed
 * by a human — and the screen says so in one line instead of letting "accepted"
 * imply "installed".
 *
 * The surface is request/response — `skilldrafts` has no delta stream — so the
 * queue is read once on mount and **read again after the screen's own write**.
 * There is no timer on purpose: a poll would be the only way to notice a draft
 * proposed elsewhere, and inventing one is the polling loop AGENTS.md §3
 * forbids. The write's own answer is not trusted as the new queue either: a
 * decision may have been recorded elsewhere between the two calls, so only a
 * re-read says what the queue now holds.
 *
 * A failed *read* replaces the list with an error that names the cause and
 * offers 重试. A failed *decision* is reported above the list, because nothing
 * was recorded and the rows on screen are still the last true answer: the row
 * keeps its verbs and nothing is re-read.
 *
 * Only a `pending` row carries 采纳 / 驳回 — a decision is final (a rejection is
 * terminal in the store), so a decided row offers neither verb again. 驳回 asks
 * first (a Popconfirm: the verdict cannot be taken back), and both verbs are
 * disabled while one decision is on the wire so a second cannot race the first.
 *
 * Copy comes from the catalogue (`skills.drafts.*`) — this file carries no
 * user-visible string of its own.
 */
import { useCallback, useEffect, useState, type CSSProperties } from 'react';
import { Alert, Button, Empty, Flex, Listy, Popconfirm, Skeleton, Tag, Typography } from 'antd';
import { Link } from 'react-router';
import {
  decideSkillDraft,
  loadSkillDrafts,
  SKILL_DRAFT_ACCEPTED,
  SKILL_DRAFT_PENDING,
  SKILL_DRAFT_REJECTED,
  type SkillDraft,
} from '@/api/skilldrafts';
import { describeError } from '@/features/describe-error';
import { formatRelativeTime } from '@/features/inbox/state';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';

/**
 * The reviewer a decision is filed under. The Lead is the one who reads this
 * queue (the product proposes for the Lead's tasks) and the gateway takes the
 * reviewer as data — there is no ambient identity on this surface — so the
 * client states the name it is filing under rather than leaving it blank.
 */
const REVIEWER = 'lead';

/** Lines the body is clamped to before offering 展开. */
const CLAMP_ROWS = 3;

/**
 * Rough glyphs per line at the narrowest viewport the client supports (390px,
 * the phone audit's width) — see {@link bodyOverflows}.
 */
const NARROW_LINE_GLYPHS = 28;

/**
 * A body, a skill name or a task id can be one unbroken token (a path, a URL).
 * `overflowWrap: anywhere` keeps it inside the row at 390px instead of widening
 * the page (`break-word` leaves the intrinsic width at the whole token).
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere', minWidth: 0 };

/** The body as written: line breaks survive, long tokens wrap. */
const BODY_BASE: CSSProperties = {
  overflowWrap: 'anywhere',
  whiteSpace: 'pre-wrap',
};

/** The same body, cut to {@link CLAMP_ROWS} lines by CSS. */
const BODY_CLAMPED: CSSProperties = {
  ...BODY_BASE,
  display: '-webkit-box',
  WebkitBoxOrient: 'vertical',
  WebkitLineClamp: CLAMP_ROWS,
  overflow: 'hidden',
};

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; drafts: SkillDraft[]; at: number }
  | { kind: 'error'; message: string };

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/**
 * Whether a body is cut off by the three-line clamp.
 *
 * The clamp itself is CSS, and jsdom has no layout to measure it, so the
 * decision is made from the text: more than three explicit lines, or more
 * glyphs than three lines of the narrowest supported viewport can hold. Showing
 * 展开 a little too often costs one tap; hiding it while the clamp is cutting
 * text would hide the draft's own words.
 */
function bodyOverflows(body: string): boolean {
  return (
    body.split('\n').length > CLAMP_ROWS || body.length > CLAMP_ROWS * NARROW_LINE_GLYPHS
  );
}

/**
 * The status in words, as a Tag.
 *
 * 待确认 is the only state that needs a human, so it is the only one that takes
 * a status colour (`warning` — the same "needs your action" semantic the run
 * states use); 已采纳 is `success`; 已驳回 is a **decision, not a failure**, so
 * it reads at normal weight rather than in red (DESIGN §5 draws the same line
 * for `cancelled`, and red here would also spend the screen's ≤2-red budget).
 *
 * An unknown status is shown as the gateway's own word: the vocabulary belongs
 * to the store, and mapping a value this screen does not know onto 待确认 would
 * offer accept/reject on something a human may already have decided.
 */
function statusTag(draft: SkillDraft, t: ReturnType<typeof useT>) {
  if (draft.status === SKILL_DRAFT_PENDING) {
    return (
      <Tag color="warning" data-testid={`skill-draft-status-${draft.id}`}>
        {t('skills.drafts.status.pending')}
      </Tag>
    );
  }
  if (draft.status === SKILL_DRAFT_ACCEPTED) {
    return (
      <Tag color="success" data-testid={`skill-draft-status-${draft.id}`}>
        {t('skills.drafts.status.accepted')}
      </Tag>
    );
  }
  if (draft.status === SKILL_DRAFT_REJECTED) {
    return (
      <Tag data-testid={`skill-draft-status-${draft.id}`}>
        {t('skills.drafts.status.rejected')}
      </Tag>
    );
  }
  return (
    <Tag data-testid={`skill-draft-status-${draft.id}`}>
      {draft.status || t('skills.drafts.status.unknown')}
    </Tag>
  );
}

/** The fallback sentence for a failed decision the gateway did not explain. */
const DECISION_FAILED: MessageKey = 'skills.drafts.failed';

export function SkillDraftsScreen() {
  const t = useT();
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches the queue.
  const [attempt, setAttempt] = useState(0);
  // Which draft's decision is on the wire, and which way it went: both verbs of
  // every row are disabled meanwhile so a second decision cannot race the first.
  const [deciding, setDeciding] = useState<{ id: string; accept: boolean } | null>(null);
  const [decisionError, setDecisionError] = useState<string | null>(null);

  const readDrafts = useCallback(async (): Promise<LoadState> => {
    try {
      return { kind: 'ready', drafts: await loadSkillDrafts(), at: Date.now() };
    } catch (cause) {
      return { kind: 'error', message: messageOf(cause) };
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void readDrafts().then((next) => {
      if (!cancelled) setState(next);
    });
    return () => {
      cancelled = true;
    };
  }, [readDrafts, attempt]);

  const retry = () => {
    // Back to the honest loading state, then read again.
    setState({ kind: 'loading' });
    setAttempt((value) => value + 1);
  };

  /**
   * One decision on one draft: the write, then a read of the queue it produced —
   * the decided row the gateway answers with is not the queue, and the queue is
   * what this screen renders.
   */
  const decide = async (draft: SkillDraft, accept: boolean) => {
    if (deciding) return;
    setDeciding({ id: draft.id, accept });
    setDecisionError(null);
    try {
      await decideSkillDraft({ id: draft.id, accept, reviewer: REVIEWER });
      setState(await readDrafts());
    } catch (cause) {
      // Nothing was recorded, so there is nothing to re-read: the queue on
      // screen is still the last true answer, and the row keeps its verbs.
      setDecisionError(describeError(cause, t, DECISION_FAILED));
    } finally {
      setDeciding(null);
    }
  };

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('skills.drafts.title')}</h1>
        <p className="zk-screen__note">{t('skills.drafts.note')}</p>
        {/* The one honest line: accepting is a record, not an install. */}
        <p className="zk-screen__note" data-testid="skills-drafts-honest">
          {t('skills.drafts.honest')}
        </p>
      </header>

      {decisionError ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          closable
          title={decisionError}
          data-testid="skills-drafts-decision-error"
        />
      ) : null}

      {state.kind === 'loading' ? <Skeleton active title={false} paragraph={{ rows: 3 }} /> : null}

      {state.kind === 'error' ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          title={t('skills.drafts.loadFailed')}
          description={state.message}
          action={
            <Button size="small" onClick={retry} data-testid="skills-drafts-retry">
              {t('skills.drafts.retry')}
            </Button>
          }
        />
      ) : null}

      {state.kind === 'ready' && state.drafts.length === 0 ? (
        // The empty state says where drafts come from: an unexplained empty list
        // on a queue-driven screen reads as a broken feature.
        <div data-testid="skills-drafts-empty">
          <Empty description={t('skills.drafts.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
          <p className="zk-screen__note">{t('skills.drafts.emptyNote')}</p>
        </div>
      ) : null}

      {state.kind === 'ready' && state.drafts.length > 0 ? (
        // The order is the service's (newest first); the screen does not
        // re-sort, so what a reader sees is what the queue holds.
        <div data-testid="skills-drafts-list">
          <Listy
            virtual={false}
            items={state.drafts}
            rowKey="id"
            itemRender={(draft) => (
              <DraftRow
                draft={draft}
                now={state.at}
                // The row that sent a decision also shows which way it went.
                decisionPending={
                  deciding?.id === draft.id ? (deciding.accept ? 'accept' : 'reject') : null
                }
                decisionsBlocked={deciding !== null}
                onDecide={(accept) => void decide(draft, accept)}
              />
            )}
          />
        </div>
      ) : null}
    </div>
  );
}

/**
 * One draft: its status, the skill name, when it was proposed, the task it came
 * from, the summary, the two verbs (only while it is pending), and the body.
 */
function DraftRow({
  draft,
  now,
  decisionPending,
  decisionsBlocked,
  onDecide,
}: {
  draft: SkillDraft;
  now: number;
  /** Which verdict this row is currently recording, if any. */
  decisionPending: 'accept' | 'reject' | null;
  /** True while any row's decision is on the wire (both verbs wait their turn). */
  decisionsBlocked: boolean;
  onDecide: (accept: boolean) => void;
}) {
  const t = useT();
  // A draft the gateway did not stamp has no relative age; printing "" is better
  // than inventing "just now" for a record that carries no time.
  const stamp = formatRelativeTime(draft.createdAt, now, t);
  // Only an undecided draft can be moved on: a decision is final.
  const pending = draft.status === SKILL_DRAFT_PENDING;
  const blocked = decisionsBlocked || !pending;

  return (
    <article data-testid={`skill-draft-${draft.id}`}>
      <Flex wrap gap="small" align="center">
        {statusTag(draft, t)}
        <Typography.Text strong style={WRAP_LONG}>
          {draft.name}
        </Typography.Text>
        {stamp ? <Typography.Text type="secondary">{stamp}</Typography.Text> : null}
        {draft.taskId ? (
          <Link
            to={`/t/${encodeURIComponent(draft.taskId)}`}
            style={WRAP_LONG}
            data-testid={`skill-draft-task-${draft.id}`}
          >
            {t('skills.drafts.taskLink', { id: draft.taskId })}
          </Link>
        ) : null}
        {/* Who decided — only when the gateway names someone. A pending draft has
            no reviewer, and an unnamed one stays unnamed rather than inventing a
            name that failed to load. */}
        {draft.reviewer ? (
          <Typography.Text type="secondary" data-testid={`skill-draft-reviewer-${draft.id}`}>
            {t('skills.drafts.reviewer', { by: draft.reviewer })}
          </Typography.Text>
        ) : null}
      </Flex>

      {draft.summary ? (
        <Typography.Paragraph type="secondary" style={WRAP_LONG}>
          {draft.summary}
        </Typography.Paragraph>
      ) : null}

      {pending ? (
        <Flex wrap gap="small" align="center">
          <Button
            size="small"
            loading={decisionPending === 'accept'}
            disabled={blocked}
            onClick={() => onDecide(true)}
            data-testid={`skill-draft-accept-${draft.id}`}
          >
            {t('skills.drafts.accept')}
          </Button>
          {/* Opening the verdict is not the dangerous step; recording a
              rejection is, so the danger colour lives on the confirm button
              alone — the same split the settings screens use for deletes. */}
          <Popconfirm
            title={t('skills.drafts.rejectConfirm')}
            okText={t('skills.drafts.rejectOk')}
            cancelText={t('ui.cancel')}
            okButtonProps={{ danger: true }}
            disabled={blocked}
            onConfirm={() => onDecide(false)}
          >
            <Button
              size="small"
              loading={decisionPending === 'reject'}
              disabled={blocked}
              data-testid={`skill-draft-reject-${draft.id}`}
            >
              {t('skills.drafts.reject')}
            </Button>
          </Popconfirm>
        </Flex>
      ) : null}

      {draft.body ? <DraftBody draftId={draft.id} body={draft.body} /> : null}
    </article>
  );
}

/**
 * The draft body, clamped to {@link CLAMP_ROWS} lines until 展开 is pressed.
 *
 * The toggle is a `link` button rather than a paragraph suffix: it is
 * focusable, announced as a control (`aria-expanded`), and cannot be mistaken
 * for part of the draft's Markdown.
 */
function DraftBody({ draftId, body }: { draftId: string; body: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const overflows = bodyOverflows(body);

  return (
    <div>
      <Typography.Paragraph
        style={expanded ? BODY_BASE : BODY_CLAMPED}
        data-testid={`skill-draft-body-${draftId}`}
      >
        {body}
      </Typography.Paragraph>
      {overflows ? (
        <Button
          type="link"
          size="small"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
          data-testid={`skill-draft-toggle-${draftId}`}
        >
          {expanded ? t('skills.drafts.collapse') : t('skills.drafts.expand')}
        </Button>
      ) : null}
    </div>
  );
}