/**
 * 待发送消息 — the outbound half of the `before_send` approval gate.
 *
 * A task dispatched with `approval=before_send` may start (the person approved
 * the *work*), but the message it wants to send when it settles is parked with
 * the task instead of leaving the product. Until this block existed, that hold
 * was invisible: the task looked finished, and the message simply never arrived
 * anywhere. So the block answers three questions in the place they are asked:
 * is something waiting (`tasks.send.pending`), what exactly would be said (the
 * text, verbatim), and the one verb that lets it go (`tasks.send.approve`).
 *
 * Three deliberate choices:
 *
 *  - **Verbatim.** The text is rendered as the gateway parked it — whitespace,
 *    newlines and all — inside a read-only block. It is what would be said in
 *    public; a paraphrase or a truncation here would be a different message than
 *    the one that ships. `pre-wrap` is what keeps the line breaks the gateway
 *    wrote.
 *  - **Hidden when nothing is held.** No title, no empty box, no "nothing
 *    waiting" line. A block that has nothing to say is scaffolding on every other
 *    task screen, and the absence of the block *is* the answer.
 *  - **A plain button, not the primary.** §5's primary is taking the task; this
 *    is a secondary decision about words that are already written. It sits behind
 *    a Popconfirm that states plainly that the message will be sent, because
 *    sending cannot be recalled.
 *
 * It is a read of its own, with its own failure: a hold that did not load reports
 * itself inside this block, in the gateway's own words, and never confuses "it
 * did not load" with "nothing is waiting". A failed *write* keeps the text and
 * the verb on screen and reports why next to them, because the message did not
 * leave; a successful write is followed by exactly one re-read of the hold, and
 * the receipt it answered with says which channel took the message.
 *
 * Copy comes from the catalogue (`task.detail.send.*`, plus the existing
 * `task.detail.retry` / `ui.cancel`): this file carries no user-visible string of
 * its own.
 */
import { useEffect, useState, type CSSProperties } from 'react';
import { Alert, Button, Popconfirm, Space, Typography } from 'antd';
import { approveSend, loadPendingSend, type SendReceipt } from '@/api/task';
import { useI18n } from '@/i18n';

/**
 * Who the approval is made by.
 *
 * Hard-coded for the same reason the screen's own `OWNER` is: the product has one
 * human today, and the gateway does not hand the shell an identity yet. When
 * multi-user lands this becomes the signed-in user — `approver` is already a
 * required parameter on the wire, so only this line changes.
 */
const APPROVER = 'lead';

/**
 * A parked message is free-form prose and can carry one unbroken token (a URL):
 * same wrapping rule as the screen's own, so a 390px phone does not widen.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/**
 * The words as the gateway wrote them: `pre-wrap` keeps the newlines (collapsing
 * them would change the message) and the block stays read-only — this is not an
 * editor, the text was decided elsewhere.
 */
const HELD_TEXT: CSSProperties = { ...WRAP_LONG, whiteSpace: 'pre-wrap', marginBottom: 0 };

/** The read, as a state. `none` means the gateway answered "nothing is held". */
type SendState =
  | { kind: 'loading' }
  | { kind: 'none' }
  | { kind: 'held'; text: string }
  | { kind: 'error'; message: string };

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

export function PendingSend({
  taskId,
  onHeld,
}: {
  taskId: string;
  /**
   * Reports what the read (or the write) proved about the hold. The screen's §5
   * uses it for one thing: a settled task must not say 不需要你做什么 while a
   * message is waiting for exactly that person.
   */
  onHeld?: (held: boolean) => void;
}) {
  const { t } = useI18n();
  const [state, setState] = useState<SendState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that reads.
  const [attempt, setAttempt] = useState(0);
  /** One write in flight: the verb is disabled and a second click sends nothing. */
  const [busy, setBusy] = useState(false);
  /** A failed approval, shown next to the text it did not send. */
  const [writeError, setWriteError] = useState<string | null>(null);
  /**
   * The receipt of an approval just made. It outlives the text: "it went out via
   * 飞书" is the one thing left to say once the hold is empty.
   */
  const [receipt, setReceipt] = useState<SendReceipt | null>(null);

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    // A read of its own: nothing here may make the task look unreadable.
    loadPendingSend(taskId)
      .then((pending) => {
        if (cancelled) return;
        setState(pending ? { kind: 'held', text: pending.text } : { kind: 'none' });
        onHeld?.(pending !== null);
      })
      .catch((cause) => {
        // "It did not load" is not "nothing is waiting": stay silent about the
        // hold and say what happened instead. The parent is not told `false`,
        // because that would be a claim this read could not make.
        if (!cancelled) setState({ kind: 'error', message: messageOf(cause) });
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, attempt, onHeld]);

  const approve = async () => {
    // The button is disabled while a write is in the air; this guards the
    // popconfirm's own confirm event from ever becoming a second send.
    if (busy) return;
    setBusy(true);
    setWriteError(null);
    try {
      const written = await approveSend({ id: taskId, approver: APPROVER });
      setReceipt(written);
      // The write proves the hold is gone (it hands the text over), so the words
      // leave the screen now — a re-read that fails afterwards cannot put back a
      // message that has already left.
      setState({ kind: 'none' });
      onHeld?.(false);
      try {
        const next = await loadPendingSend(taskId);
        if (next) {
          // A newer message was parked while this one was leaving: it is the one
          // that would go out now, and it gets its own decision.
          setState({ kind: 'held', text: next.text });
          onHeld?.(true);
        }
      } catch {
        // The receipt stands. A hold parked since then is not lost — the next
        // open of this task reads it.
      }
    } catch (cause) {
      // The message did not leave: keep the words and print the gateway's own.
      setWriteError(messageOf(cause));
    } finally {
      setBusy(false);
    }
  };

  // Still loading: no scaffolding. A box that appears and then vanishes is worse
  // than one that arrives with its answer.
  if (state.kind === 'loading') return null;

  if (state.kind === 'error') {
    return (
      <div data-testid="task-pending-send">
        <Alert
          type="error"
          showIcon
          role="alert"
          title={t('task.detail.send.loadFailed')}
          description={state.message}
          action={
            <Button
              size="small"
              onClick={() => {
                // Back to the honest loading state, then read again — this read
                // alone; the task's own reads are not repeated.
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

  const held = state.kind === 'held' ? state.text : null;

  // Nothing waiting and nothing just sent: the block does not exist.
  if (held === null && !receipt) return null;

  return (
    <>
      {receipt ? (
        <div data-testid="task-send-receipt">
          {/* Which channel took it, in the gateway's word for that channel. A
              receipt without one still says the message left. */}
          <Alert
            type="success"
            showIcon
            title={
              receipt.channel
                ? t('task.detail.send.sent', { channel: receipt.channel })
                : t('task.detail.send.sentUnknown')
            }
          />
        </div>
      ) : null}

      {held !== null ? (
        <div data-testid="task-pending-send">
          <Space orientation="vertical" size={4} style={{ width: '100%' }}>
            <Typography.Text strong>{t('task.detail.send.title')}</Typography.Text>
            <Typography.Text type="secondary" style={WRAP_LONG}>
              {t('task.detail.send.hint')}
            </Typography.Text>

            {/* The words themselves, exactly as parked. Read-only, wrapped, and
                never summarised: this is what would be said in public. */}
            <div data-testid="task-send-text" style={HELD_TEXT}>
              {held}
            </div>

            {writeError ? (
              <Alert
                type="error"
                showIcon
                role="alert"
                title={t('task.detail.send.failed')}
                description={writeError}
              />
            ) : null}

            <div>
              <Popconfirm
                title={t('task.detail.send.confirm')}
                okText={t('task.detail.send.confirmOk')}
                cancelText={t('ui.cancel')}
                disabled={busy}
                onConfirm={() => void approve()}
              >
                {/* A plain button: §5's primary is taking the task, and this is
                    a second decision about words already written. */}
                <Button data-testid="task-send-approve" loading={busy} disabled={busy}>
                  {t('task.detail.send.approve')}
                </Button>
              </Popconfirm>
            </div>
          </Space>
        </div>
      ) : null}
    </>
  );
}