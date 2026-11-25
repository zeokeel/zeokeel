/**
 * The proposal queue in front of the shared board — 待确认的建议
 * (`team.suggestions`, `team.suggest`, `team.suggestion.promote`,
 * `team.suggestion.dismiss`; docs/TRANSPORT.md §5.1).
 *
 * Why this section exists at all: the board's own review is review *after the
 * fact* — an unreviewed note is already visible to everyone. A **suggestion** is
 * a bot proposing a team rule that reaches nobody until a person promotes it,
 * which is what "团队建议经你批准后另一队友可见" means. This section sits above
 * the board for exactly that reason: the queue is the gate, the board is what
 * got through it.
 *
 * The surface is request/response — `team.suggestions` has no delta stream — so
 * the queue is read once on mount and **read again after the screen's own
 * write**. There is no timer: a poll would be the only way to notice a proposal
 * made elsewhere, and inventing one is the polling loop AGENTS.md §3 forbids.
 *
 * A promote changes **both** lists (the queue loses a row, the board gains the
 * note it became), and a dismiss answers for both as well: "the board did not
 * change" is the board's own answer to give, not this component's to assume. So
 * both verbs re-read the queue and then the board, through `onBoardChanged`.
 * Proposing only queues a rule — it never touches the board — so that one
 * re-reads the queue alone.
 *
 * One write at a time: while a promote, a dismiss or a proposal is on the wire,
 * every verb on every row and the propose button wait. A failed write keeps the
 * rows exactly as they were and prints the gateway's own words above the list
 * (`src/features/describe-error.ts`).
 *
 * Copy comes from the catalogue (`memory.suggest.*`) — this file carries no
 * user-visible string of its own.
 */
import { useCallback, useEffect, useState, type CSSProperties } from 'react';
import { Alert, Button, Empty, Flex, Input, Listy, Popconfirm, Skeleton, Typography } from 'antd';
import {
  dismissSuggestion,
  loadNoteSuggestions,
  promoteSuggestion,
  suggestNote,
  type TeamNoteSuggestion,
} from '@/api/teamnotes';
import { describeError } from '@/features/describe-error';
import { formatRelativeTime } from '@/features/inbox/state';
import { useT } from '@/i18n';

/**
 * The reviewer a decision is filed under. The Lead is the one this queue is
 * built for, and the gateway takes the reviewer as data (there is no ambient
 * identity on this surface), so the client states the name it files under.
 */
const REVIEWER = 'lead';

/**
 * The author a proposal is filed under when the field is left empty — the same
 * default the note form above uses, for the same reason: "nobody" is not an
 * author the gateway can render differently from a blank name.
 */
const DEFAULT_AUTHOR = 'lead';

/** Lines the body is clamped to before offering 展开. */
const CLAMP_ROWS = 3;

/**
 * Rough glyphs per line at the narrowest viewport the client supports (390px,
 * the phone audit's width) — see {@link bodyOverflows}.
 */
const NARROW_LINE_GLYPHS = 28;

/**
 * A rule, a body or an author can be one unbroken token (a path, a URL).
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
  | { kind: 'ready'; suggestions: TeamNoteSuggestion[]; at: number }
  | { kind: 'error'; message: string };

/**
 * Which write is on the wire, if any. All three writes share one gate: a
 * promotion, a dismissal and a proposal all change the same queue, so letting a
 * second one start would race the re-read of the first.
 */
type Write =
  | { kind: 'promote' | 'dismiss'; id: string }
  | { kind: 'propose' };

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/** Which verdict the given row is recording, or none — a proposal has no row. */
function rowPending(write: Write | null, id: string): 'promote' | 'dismiss' | null {
  if (!write || write.kind === 'propose') return null;
  return write.id === id ? write.kind : null;
}

/**
 * Whether a body is cut off by the three-line clamp.
 *
 * The clamp itself is CSS, and jsdom has no layout to measure it, so the
 * decision is made from the text: more than three explicit lines, or more
 * glyphs than three lines of the narrowest supported viewport can hold. Showing
 * 展开 a little too often costs one tap; hiding it while the clamp is cutting
 * text would hide the proposal's own words.
 */
function bodyOverflows(body: string): boolean {
  return body.split('\n').length > CLAMP_ROWS || body.length > CLAMP_ROWS * NARROW_LINE_GLYPHS;
}

export function NoteSuggestionQueue({ onBoardChanged }: {
  /**
   * Re-read the board. Called after a promote *and* a dismiss: a promotion puts
   * a note on the board, and a dismissal is only "the board did not change" if
   * the board says so.
   */
  onBoardChanged: () => Promise<void>;
}) {
  const t = useT();
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that fetches the queue.
  const [attempt, setAttempt] = useState(0);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [author, setAuthor] = useState(DEFAULT_AUTHOR);
  const [write, setWrite] = useState<Write | null>(null);
  const [writeError, setWriteError] = useState<string | null>(null);

  const readQueue = useCallback(async (): Promise<LoadState> => {
    try {
      return { kind: 'ready', suggestions: await loadNoteSuggestions(), at: Date.now() };
    } catch (cause) {
      return { kind: 'error', message: messageOf(cause) };
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void readQueue().then((next) => {
      if (!cancelled) setState(next);
    });
    return () => {
      cancelled = true;
    };
  }, [readQueue, attempt]);

  const retry = () => {
    // Back to the honest loading state, then read again.
    setState({ kind: 'loading' });
    setAttempt((value) => value + 1);
  };

  const busy = write !== null;
  // A whitespace-only title is an empty title: the gateway would reject it, and
  // the button should not offer a write it knows will fail.
  const canPropose = title.trim().length > 0 && !busy;

  /**
   * One verdict on one proposal: the write, then a read of both lists it
   * changed — the queue, then the board. The single row the gateway answers
   * with is neither list.
   */
  const decide = async (suggestion: TeamNoteSuggestion, promote: boolean) => {
    if (write) return;
    setWrite({ kind: promote ? 'promote' : 'dismiss', id: suggestion.id });
    setWriteError(null);
    try {
      if (promote) {
        await promoteSuggestion({ id: suggestion.id, reviewer: REVIEWER });
      } else {
        await dismissSuggestion({ id: suggestion.id, reviewer: REVIEWER });
      }
      setState(await readQueue());
      await onBoardChanged();
    } catch (cause) {
      // Nothing was recorded, so there is nothing to re-read: the queue on
      // screen is still the last true answer, and the row keeps its verbs.
      setWriteError(describeError(cause, t, 'memory.suggest.failed'));
    } finally {
      setWrite(null);
    }
  };

  /**
   * Queue a proposal of one's own. It never touches the board, so only the
   * queue is read again.
   */
  const propose = async () => {
    const trimmedTitle = title.trim();
    if (!trimmedTitle || write) return;
    const trimmedBody = body.trim();
    setWrite({ kind: 'propose' });
    setWriteError(null);
    try {
      await suggestNote({
        title: trimmedTitle,
        ...(trimmedBody ? { body: trimmedBody } : {}),
        author: author.trim() || DEFAULT_AUTHOR,
      });
      // The title/body were the write's input, so they are cleared; the author
      // is kept — the same person usually proposes the next rule too.
      setTitle('');
      setBody('');
      setState(await readQueue());
    } catch (cause) {
      setWriteError(describeError(cause, t, 'memory.suggest.failed'));
    } finally {
      setWrite(null);
    }
  };

  return (
    <section className="zk-settings__section" data-testid="memory-suggestions">
      <div className="zk-screen__header--row">
        <div>
          <h2 className="zk-settings__section-title">{t('memory.suggest.title')}</h2>
          {/* The one line that says what the gate is for. */}
          <p className="zk-screen__note">{t('memory.suggest.note')}</p>
        </div>
      </div>

      {writeError ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          closable
          title={writeError}
          data-testid="memory-suggestion-error"
        />
      ) : null}

      {state.kind === 'loading' ? (
        <Skeleton active title={false} paragraph={{ rows: 2 }} />
      ) : null}

      {state.kind === 'error' ? (
        <Alert
          type="error"
          showIcon
          role="alert"
          title={t('memory.suggest.loadFailed')}
          description={state.message}
          action={
            <Button size="small" onClick={retry} data-testid="memory-suggestions-retry">
              {t('memory.suggest.retry')}
            </Button>
          }
        />
      ) : null}

      {state.kind === 'ready' && state.suggestions.length === 0 ? (
        // The empty state says where proposals come from: an unexplained empty
        // queue reads as a broken feature, and "no suggestions" must not read
        // as "no one may propose".
        <div data-testid="memory-suggestions-empty">
          <Empty
            description={t('memory.suggest.empty')}
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
          <p className="zk-screen__note">{t('memory.suggest.emptyNote')}</p>
        </div>
      ) : null}

      {state.kind === 'ready' && state.suggestions.length > 0 ? (
        // The order is the service's (newest first); the queue does not re-sort.
        <div data-testid="memory-suggestions-list">
          <Listy
            virtual={false}
            items={state.suggestions}
            rowKey="id"
            itemRender={(suggestion) => (
              <SuggestionRow
                suggestion={suggestion}
                now={state.at}
                // The row that sent a write also shows which way it went.
                pending={rowPending(write, suggestion.id)}
                verbsBlocked={busy}
                onDecide={(promote) => void decide(suggestion, promote)}
              />
            )}
          />
        </div>
      ) : null}

      {/* The compact propose form: a person may propose too, and what they write
          lands in the same queue a bot's proposal does. Not a filled primary —
          记录 above is this screen's only one. */}
      <Flex vertical gap="small">
        <Typography.Text type="secondary">{t('memory.suggest.propose.title')}</Typography.Text>
        <Input
          aria-label={t('memory.field.title')}
          placeholder={t('memory.suggest.propose.titlePlaceholder')}
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          data-testid="memory-suggest-title"
        />
        <Input.TextArea
          aria-label={t('memory.field.body')}
          placeholder={t('memory.suggest.propose.bodyPlaceholder')}
          value={body}
          onChange={(event) => setBody(event.target.value)}
          autoSize={{ minRows: 1, maxRows: 4 }}
          data-testid="memory-suggest-body"
        />
        <Flex gap="small" align="center" wrap>
          <Input
            aria-label={t('memory.field.author')}
            placeholder={t('memory.field.authorPlaceholder')}
            value={author}
            onChange={(event) => setAuthor(event.target.value)}
            style={SHRINKABLE}
            data-testid="memory-suggest-author"
          />
          <Button
            disabled={!canPropose}
            loading={write?.kind === 'propose'}
            onClick={() => void propose()}
            data-testid="memory-suggest-submit"
          >
            {t('memory.suggest.propose.submit')}
          </Button>
        </Flex>
      </Flex>
    </section>
  );
}

/**
 * One proposal: the rule, who proposed it, when, the two verbs, and its body.
 */
function SuggestionRow({
  suggestion,
  now,
  pending,
  verbsBlocked,
  onDecide,
}: {
  suggestion: TeamNoteSuggestion;
  now: number;
  /** Which verdict this row is currently recording, if any. */
  pending: 'promote' | 'dismiss' | null;
  /** True while any write is on the wire (every verb waits its turn). */
  verbsBlocked: boolean;
  onDecide: (promote: boolean) => void;
}) {
  const t = useT();
  // A proposal the gateway did not stamp has no relative age; printing "" is
  // better than inventing "just now" for a record that carries no time.
  const stamp = formatRelativeTime(suggestion.at, now, t);

  return (
    <article data-testid={`memory-suggestion-${suggestion.id}`}>
      <Typography.Text strong style={WRAP_LONG}>
        {suggestion.title}
      </Typography.Text>

      <Flex wrap gap="small" align="baseline">
        {/* An unattributed proposal says 未知: a blank line would read as a name
            that failed to load. */}
        <Typography.Text type="secondary">
          {suggestion.author ?? t('memory.unknownAuthor')}
        </Typography.Text>
        {stamp ? <Typography.Text type="secondary">{stamp}</Typography.Text> : null}
      </Flex>

      <Flex wrap gap="small" align="center">
        <Button
          size="small"
          loading={pending === 'promote'}
          disabled={verbsBlocked}
          onClick={() => onDecide(true)}
          data-testid={`memory-suggestion-promote-${suggestion.id}`}
        >
          {t('memory.suggest.promote')}
        </Button>
        {/* Opening the verdict is not the dangerous step; dismissing is, so the
            danger colour lives on the confirm button alone — the same split the
            board's 确认 / 驳回 pair uses. */}
        <Popconfirm
          title={t('memory.suggest.dismissConfirm')}
          okText={t('memory.suggest.dismissOk')}
          cancelText={t('ui.cancel')}
          okButtonProps={{ danger: true }}
          disabled={verbsBlocked}
          onConfirm={() => onDecide(false)}
        >
          <Button
            size="small"
            loading={pending === 'dismiss'}
            disabled={verbsBlocked}
            data-testid={`memory-suggestion-dismiss-${suggestion.id}`}
          >
            {t('memory.suggest.dismiss')}
          </Button>
        </Popconfirm>
      </Flex>

      {suggestion.body ? (
        <SuggestionBody suggestionId={suggestion.id} body={suggestion.body} />
      ) : null}
    </article>
  );
}

/**
 * The proposal body, clamped to {@link CLAMP_ROWS} lines until 展开 is pressed.
 *
 * The toggle is a `link` button rather than a paragraph suffix: it is focusable,
 * announced as a control (`aria-expanded`), and cannot be mistaken for part of
 * the proposal's text.
 */
function SuggestionBody({ suggestionId, body }: { suggestionId: string; body: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const overflows = bodyOverflows(body);

  return (
    <div>
      <Typography.Paragraph
        style={expanded ? BODY_BASE : BODY_CLAMPED}
        data-testid={`memory-suggestion-body-${suggestionId}`}
      >
        {body}
      </Typography.Paragraph>
      {overflows ? (
        <Button
          type="link"
          size="small"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
          data-testid={`memory-suggestion-toggle-${suggestionId}`}
        >
          {expanded ? t('memory.collapse') : t('memory.expand')}
        </Button>
      ) : null}
    </div>
  );
}