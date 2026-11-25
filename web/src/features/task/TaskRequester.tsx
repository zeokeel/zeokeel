/**
 * 提出人 — which human asked for this task, in one line of the header (M6).
 *
 * The board records the work and the lease records the executor; the gateway now
 * also records the person who asked for it (`tasks.requestedby.get`), and until
 * this line existed the screen never said who. It answers exactly one question
 * and does it in one line — deliberately *not* a sixth section: the five sections
 * of `/t/:taskId` are a contract (docs/plans/grokbot-interaction-design.md §5.4),
 * and an attribution is a caption on the subject, not a chapter.
 *
 * Three answers, and they are not interchangeable:
 *
 *   name      `提出人 alice` — somebody asked for it.
 *   `null`    `提出人未知`   — the gateway has nobody on record. An answer.
 *   rejected  **no line at all.** A read that failed proved nothing about the
 *             requester, so printing 提出人未知 would be a claim this read could
 *             not make. The failure is silent by design (the task is still
 *             readable without it) but its reason is not swallowed: it goes to
 *             `console.debug`, where a developer can find it and a person cannot
 *             be misled by it.
 *
 * It renders nothing until the read resolves, so there is no placeholder line
 * for real copy to shift: the header gains its caption once, in place.
 *
 * Copy comes from the catalogue (`task.detail.requester.*`): this file carries
 * no user-visible string of its own.
 */
import { useEffect, useState, type CSSProperties } from 'react';
import { loadTaskRequester } from '@/api/task';
import { useI18n } from '@/i18n';

/**
 * A name can be one unbroken token (an email-shaped handle, a long id): same
 * wrapping rule as the screen's own, so a 390px phone does not widen.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** The read, as a state. `failed` renders nothing — see the note above. */
type RequesterState =
  | { kind: 'loading' }
  | { kind: 'known'; name: string }
  /** The gateway answered, and nobody is on record. */
  | { kind: 'unknown' }
  | { kind: 'failed' };

export function TaskRequester({ taskId }: { taskId: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<RequesterState>({ kind: 'loading' });

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    // One read of its own: nothing here may make the task look unreadable.
    loadTaskRequester(taskId)
      .then((name) => {
        if (cancelled) return;
        setState(name ? { kind: 'known', name } : { kind: 'unknown' });
      })
      .catch((cause: unknown) => {
        if (cancelled) return;
        // Silent on screen, explicit in the console: "it did not load" is not
        // "nobody asked". The debug channel is the one a person never reads.
        // eslint-disable-next-line no-console -- a failed attribution is silent by design
        console.debug('task requester read failed', taskId, cause);
        setState({ kind: 'failed' });
      });
    return () => {
      cancelled = true;
    };
  }, [taskId]);

  // Nothing is claimed while the answer is unknown, and nothing is claimed when
  // the answer never came: no line, no placeholder, no layout shift.
  if (state.kind === 'loading' || state.kind === 'failed') return null;

  return (
    <p className="zk-screen__note" style={WRAP_LONG} data-testid="task-requester">
      {state.kind === 'known'
        ? t('task.detail.requester.known', { name: state.name })
        : t('task.detail.requester.unknown')}
    </p>
  );
}