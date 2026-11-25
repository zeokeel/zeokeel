/**
 * R27c · 共享记忆 — the team's shared note board (`team.notes`,
 * `team.note.record`, docs/TRANSPORT.md §5.1).
 *
 * Two halves and the gate between them:
 *
 *   写一条      title (required), body and author (optional; author defaults to
 *               the Lead, who is the one writing most notes)
 *   待确认的建议  proposals waiting for a person — `NoteSuggestionQueue`, which
 *               owns its own read and its own write. A bot proposes a rule and
 *               nothing reaches the board until someone promotes it, so a
 *               promotion re-reads the board through `refreshBoard` below.
 *   最近的笔记   the notes exactly as the service returned them, newest first
 *
 * The surface is request/response — neither method has a delta stream — so the
 * board is read once on mount and **read again after the screen's own write**.
 * There is no timer on purpose: a poll would be the only way to notice a note a
 * teammate recorded elsewhere, and inventing one is the polling loop AGENTS.md
 * §3 forbids. The write's own answer is not trusted as the new board either:
 * recording a note for a task *replaces* that task's note, so only a re-read
 * says what the board now holds.
 *
 * A failed *read* replaces the list with an error that names the cause and
 * offers 重试. A failed *write* is reported above the form it came from, because
 * nothing was recorded and the list on screen is still the last true answer.
 *
 * Each row also states its **review** (`team.note.review`): 未复核 until someone
 * looks, then 已复核 (with the reviewer's name when the gateway names one) or
 * 已驳回. Only an unreviewed note carries 确认 / 驳回 — a review is final, so a
 * reviewed row offers neither verb again. 驳回 asks first (a Popconfirm: the
 * verdict cannot be taken back), and both verbs are one write followed by the
 * same re-read 记录 does, disabled while that write is in flight. A failed
 * review keeps the row and the board exactly as they were, and prints the
 * gateway's own words above the list.
 *
 * Copy comes from the catalogue (`memory.*` for the write and the read,
 * `memory.review.*` for the verdict) — this file carries no user-visible string
 * of its own.
 */
import { useCallback, useEffect, useState, type CSSProperties } from 'react';
import { Alert, Button, Empty, Flex, Input, Listy, Popconfirm, Skeleton, Typography } from 'antd';
import { Link } from 'react-router';
import {
  loadTeamNotes,
  recordTeamNote,
  reviewTeamNote,
  type TeamNote,
} from '@/api/teamnotes';
import { useMediaQuery } from '@/design/use-system-appearance';
import { describeError } from '@/features/describe-error';
import { formatRelativeTime } from '@/features/inbox/state';
import { useT } from '@/i18n';
import { NoteSuggestionQueue } from './NoteSuggestionQueue';

/**
 * Below this width 记录 — the screen's only filled primary — gets antd's
 * `large` (40px): `middle`'s 32px is not a thumb target on a phone, and this
 * screen is the same full web client over the LAN (prototype screen 05).
 */
const NARROW_QUERY = '(max-width: 768px)';

/**
 * The author a note is filed under when the field is left empty. The Lead is
 * the writer that matters to a reader coming to this board, and "nobody" is not
 * an author the gateway can render differently from a blank name.
 */
const DEFAULT_AUTHOR = 'lead';

/**
 * The reviewer a review is filed under. Same person, same reason: the Lead is
 * the one who reads this board and the one whose notes it holds. The gateway
 * takes the reviewer as data (there is no ambient identity on this surface), so
 * the client states the name it is filing under rather than leaving it blank.
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
 * A note body, an author or a task id can be one unbroken token (a path, a URL).
 * `overflowWrap: anywhere` keeps it inside the row at 390px instead of widening
 * the page (`break-word` leaves the intrinsic width at the whole token).
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere', minWidth: 0 };

/** A flex child allowed to shrink below its content (`min-width: auto` refuses). */
const SHRINKABLE: CSSProperties = { flex: 1, minWidth: 0 };

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
  | { kind: 'ready'; notes: TeamNote[]; at: number }
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
 * text would hide the note's own words.
 */
function bodyOverflows(body: string): boolean {
  return (
    body.split('\n').length > CLAMP_ROWS || body.length > CLAMP_ROWS * NARROW_LINE_GLYPHS
  );
}

/**
 * The row's review state in words: 未复核, 已复核 (· who), or 已驳回.
 *
 * `reviewed` is a tri-state — absent means "nobody has looked", which is not
 * the same statement as a rejection — and an accepted note whose reviewer the
 * gateway did not name says 已复核 alone rather than inventing 未知 for a
 * reviewer (the author's 未知 would read as a name that failed to load).
 */
function reviewStateLabel(note: TeamNote, t: ReturnType<typeof useT>): string {
  if (note.reviewed === undefined) return t('memory.review.pending');
  if (!note.reviewed) return t('memory.review.rejected');
  // The catalogue interpolates `{by}`; the screen does not re-format a
  // translated sentence it did not build.
  if (!note.reviewedBy) return t('memory.review.accepted');
  return t('memory.review.acceptedBy', { by: note.reviewedBy });
}

/**
 * What to print when a review fails: the gateway's own words, which are not
 * ours to translate (`src/features/describe-error.ts`), and only when it threw
 * something with no text at all the screen's own sentence.
 */
function reviewFailureText(cause: unknown, t: ReturnType<typeof useT>): string {
  return cause instanceof Error && cause.message ? cause.message : t('memory.review.failed');
}

export function TeamMemoryScreen() {
  const t = useT();
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches the board.
  const [attempt, setAttempt] = useState(0);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [author, setAuthor] = useState(DEFAULT_AUTHOR);
  const [saving, setSaving] = useState(false);
  const [writeError, setWriteError] = useState<string | null>(null);
  // Which note's verdict is on the wire, and which way it went: both verbs are
  // disabled meanwhile so a second review cannot race the first.
  const [reviewing, setReviewing] = useState<{ id: string; accept: boolean } | null>(null);
  const [reviewError, setReviewError] = useState<string | null>(null);
  const narrow = useMediaQuery(NARROW_QUERY);

  const readNotes = useCallback(async (): Promise<LoadState> => {
    try {
      return { kind: 'ready', notes: await loadTeamNotes(), at: Date.now() };
    } catch (cause) {
      return { kind: 'error', message: messageOf(cause) };
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void readNotes().then((next) => {
      if (!cancelled) setState(next);
    });
    return () => {
      cancelled = true;
    };
  }, [readNotes, attempt]);

  const retry = () => {
    // Back to the honest loading state, then read again.
    setState({ kind: 'loading' });
    setAttempt((value) => value + 1);
  };

  /**
   * Re-read the board on request. The suggestion queue calls this after a
   * promote (which puts a note on the board) **and** after a dismiss: "the board
   * did not change" is the board's own answer to give, not the queue's to
   * assume.
   */
  const refreshBoard = useCallback(async () => {
    setState(await readNotes());
  }, [readNotes]);

  // A whitespace-only title is an empty title: the gateway would reject it, and
  // the button should not offer a write it knows will fail.
  const canSubmit = title.trim().length > 0 && !saving;

  const submit = async () => {
    const trimmedTitle = title.trim();
    if (!trimmedTitle || saving) return;
    const trimmedBody = body.trim();
    setSaving(true);
    setWriteError(null);
    try {
      await recordTeamNote({
        title: trimmedTitle,
        ...(trimmedBody ? { body: trimmedBody } : {}),
        author: author.trim() || DEFAULT_AUTHOR,
      });
      // The note is recorded; what follows is a read of the board it produced.
      // The title/body are the write's input, so they are cleared; the author
      // is kept — the same person usually writes the next note too.
      setTitle('');
      setBody('');
      setState(await readNotes());
    } catch (cause) {
      setWriteError(describeError(cause, t, 'memory.saveFailed'));
    } finally {
      setSaving(false);
    }
  };

  /**
   * One verdict on one note: the write, then the same re-read 记录 does — the
   * reviewed row the gateway answers with is not the board, and the board is
   * what this screen renders.
   */
  const decide = async (note: TeamNote, accept: boolean) => {
    if (reviewing) return;
    setReviewing({ id: note.id, accept });
    setReviewError(null);
    try {
      await reviewTeamNote({ id: note.id, reviewer: REVIEWER, accept });
      setState(await readNotes());
    } catch (cause) {
      // Nothing was recorded, so there is nothing to re-read: the list on
      // screen is still the last true answer, and the row keeps its verbs.
      setReviewError(reviewFailureText(cause, t));
    } finally {
      setReviewing(null);
    }
  };

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('memory.title')}</h1>
        <p className="zk-screen__note">{t('memory.note')}</p>
      </header>

      <Flex vertical gap="large">
        <section className="zk-settings__section" data-testid="memory-write">
          <div className="zk-screen__header--row">
            <div>
              <h2 className="zk-settings__section-title">{t('memory.write.title')}</h2>
              <p className="zk-screen__note">{t('memory.write.note')}</p>
            </div>
          </div>

          <Flex vertical gap="small">
            <Input
              aria-label={t('memory.field.title')}
              placeholder={t('memory.field.titlePlaceholder')}
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              data-testid="memory-write-title"
            />
            <Input.TextArea
              aria-label={t('memory.field.body')}
              placeholder={t('memory.field.bodyPlaceholder')}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              autoSize={{ minRows: 2, maxRows: 6 }}
              data-testid="memory-write-body"
            />
            <Flex gap="small" align="center" wrap>
              <Input
                aria-label={t('memory.field.author')}
                placeholder={t('memory.field.authorPlaceholder')}
                value={author}
                onChange={(event) => setAuthor(event.target.value)}
                style={SHRINKABLE}
                data-testid="memory-write-author"
              />
              {/* The screen's only filled primary. */}
              <Button
                type="primary"
                size={narrow ? 'large' : undefined}
                disabled={!canSubmit}
                loading={saving}
                onClick={() => void submit()}
                data-testid="memory-write-submit"
              >
                {t('memory.write.submit')}
              </Button>
            </Flex>
          </Flex>

          {writeError ? (
            <Alert type="error" showIcon role="alert" closable title={writeError} />
          ) : null}
        </section>

        {/* The gate in front of the board: a bot's rule reaches nobody until a
            person promotes it here. Above the board on purpose — this queue is
            what the board below is fed by. */}
        <NoteSuggestionQueue onBoardChanged={refreshBoard} />

        <section className="zk-settings__section" data-testid="memory-notes">
          <div className="zk-screen__header--row">
            <div>
              <h2 className="zk-settings__section-title">{t('memory.list.title')}</h2>
              <p className="zk-screen__note">{t('memory.list.note')}</p>
            </div>
          </div>

          {reviewError ? (
            <Alert
              type="error"
              showIcon
              role="alert"
              closable
              title={reviewError}
              data-testid="memory-review-error"
            />
          ) : null}

          {state.kind === 'loading' ? (
            <Skeleton active title={false} paragraph={{ rows: 3 }} />
          ) : null}

          {state.kind === 'error' ? (
            <Alert
              type="error"
              showIcon
              role="alert"
              title={t('memory.loadFailed')}
              description={state.message}
              action={
                <Button size="small" onClick={retry} data-testid="memory-retry">
                  {t('memory.retry')}
                </Button>
              }
            />
          ) : null}

          {state.kind === 'ready' && state.notes.length === 0 ? (
            <div data-testid="memory-empty">
              <Empty
                description={t('memory.empty')}
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            </div>
          ) : null}

          {state.kind === 'ready' && state.notes.length > 0 ? (
            // The order is the service's (newest first); the screen does not
            // re-sort, so what a reader sees is what the board holds.
            <div data-testid="memory-list">
              <Listy
                virtual={false}
                items={state.notes}
                rowKey="id"
                itemRender={(note) => (
                  <NoteRow
                    note={note}
                    now={state.at}
                    // Both verbs of every row are off while one review is on the
                    // wire; the row that sent it also shows what it is doing.
                    reviewPending={
                      reviewing?.id === note.id
                        ? reviewing.accept
                          ? 'accept'
                          : 'reject'
                        : null
                    }
                    reviewsBlocked={reviewing !== null}
                    onReview={(accept) => void decide(note, accept)}
                  />
                )}
              />
            </div>
          ) : null}
        </section>
      </Flex>
    </div>
  );
}

/**
 * One note: title, who wrote it, when, the task it belongs to, its body, and
 * the review state that decides whether anyone has answered for it yet.
 */
function NoteRow({
  note,
  now,
  reviewPending,
  reviewsBlocked,
  onReview,
}: {
  note: TeamNote;
  now: number;
  /** Which verdict this row is currently recording, if any. */
  reviewPending: 'accept' | 'reject' | null;
  /** True while any row's review is on the wire (both verbs wait their turn). */
  reviewsBlocked: boolean;
  onReview: (accept: boolean) => void;
}) {
  const t = useT();
  // A note the gateway did not stamp has no relative age; printing "" is
  // better than inventing "just now" for a record that carries no time.
  const stamp = formatRelativeTime(note.at, now, t);
  // Absent is "nobody has looked", not `false`: only an answered verdict is
  // final, and only an unanswered note can be moved on.
  const answered = note.reviewed !== undefined;
  const disabled = reviewsBlocked || answered;

  return (
    <article data-testid={`memory-note-${note.id}`}>
      <Typography.Text strong style={WRAP_LONG}>
        {note.title}
      </Typography.Text>

      <Flex wrap gap="small" align="baseline">
        {/* An unattributed note says 未知: a blank line would read as a name
            that failed to load. */}
        <Typography.Text type="secondary">
          {note.author ?? t('memory.unknownAuthor')}
        </Typography.Text>
        {stamp ? <Typography.Text type="secondary">{stamp}</Typography.Text> : null}
        {note.taskId ? (
          <Link
            to={`/t/${encodeURIComponent(note.taskId)}`}
            style={WRAP_LONG}
            data-testid={`memory-note-task-${note.id}`}
          >
            {t('memory.taskLink', { id: note.taskId })}
          </Link>
        ) : null}
      </Flex>

      {/* The verdict, then (only while there is none) the two verbs that give
          one.

          Two levels and no colour: an unanswered note says 未复核 in secondary
          because nothing has happened to it yet, while a settled verdict —
          accepted or rejected — is a fact about the note and reads at normal
          weight. A rejection is deliberately not red: it is a decision, not a
          failure (DESIGN §5 draws the same line for `cancelled`), and one red
          mark per rejected row would also spend the screen's ≤2-red budget
          (§3.7 / DS-13). The two verbs exist only on an unanswered note, so
          "has buttons" already means "waiting for a reviewer". */}
      <Flex wrap gap="small" align="center">
        <Typography.Text
          type={answered ? undefined : 'secondary'}
          data-testid={`memory-note-review-${note.id}`}
        >
          {reviewStateLabel(note, t)}
        </Typography.Text>
        {answered ? null : (
          <>
            <Button
              size="small"
              loading={reviewPending === 'accept'}
              disabled={disabled}
              onClick={() => onReview(true)}
              data-testid={`memory-review-accept-${note.id}`}
            >
              {t('memory.review.accept')}
            </Button>
            {/* Opening the verdict is not the dangerous step; recording a
                rejection is, so the danger colour lives on the confirm button
                alone — the same split the settings screens use for deletes. */}
            <Popconfirm
              title={t('memory.review.rejectConfirm')}
              okText={t('memory.review.rejectOk')}
              cancelText={t('ui.cancel')}
              okButtonProps={{ danger: true }}
              disabled={disabled}
              onConfirm={() => onReview(false)}
            >
              <Button
                size="small"
                loading={reviewPending === 'reject'}
                disabled={disabled}
                data-testid={`memory-review-reject-${note.id}`}
              >
                {t('memory.review.reject')}
              </Button>
            </Popconfirm>
          </>
        )}
      </Flex>

      {note.body ? <NoteBody noteId={note.id} body={note.body} /> : null}
    </article>
  );
}

/**
 * The note body, clamped to {@link CLAMP_ROWS} lines until 展开 is pressed.
 *
 * The toggle is a `link` button rather than a paragraph suffix: it is
 * focusable, announced as a control (`aria-expanded`), and cannot be mistaken
 * for part of the note's text.
 */
function NoteBody({ noteId, body }: { noteId: string; body: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const overflows = bodyOverflows(body);

  return (
    <div>
      <Typography.Paragraph
        style={expanded ? BODY_BASE : BODY_CLAMPED}
        data-testid={`memory-note-body-${noteId}`}
      >
        {body}
      </Typography.Paragraph>
      {overflows ? (
        <Button
          type="link"
          size="small"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
          data-testid={`memory-note-toggle-${noteId}`}
        >
          {expanded ? t('memory.collapse') : t('memory.expand')}
        </Button>
      ) : null}
    </div>
  );
}