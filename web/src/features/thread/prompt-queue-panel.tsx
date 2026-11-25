import { useState } from 'react';
import { Button, Popover } from 'antd';
import { useEscClaim } from '@/keyboard/keyboard-provider';
import type { QueuedPrompt } from '@/api/prompt-queue';
import { ZkIcon } from '@/design/icons';
import { useT } from '@/i18n';

/**
 * The queue (B13): a **collapsed** chip above the composer that opens the list.
 *
 * Why a popover and not the always-on panel this replaces: a lingering queue
 * kept an extra block over the composer even when the user was idle, and it
 * fought the "≤2 accent fills per screen" budget. The function is unchanged —
 * the count is on the chip, the rows and their cancel buttons are one click
 * away, and 发送队首 appears only when the session is idle (an abort never
 * discards what the user already typed).
 */
export function PromptQueuePanel({
  items,
  running,
  error,
  onCancel,
  onSendHead,
  onMove,
}: {
  items: QueuedPrompt[];
  running: boolean;
  /** A failed enqueue/remove: shown here so the text is never silently lost. */
  error?: string | null;
  onCancel: (id: string) => void;
  onSendHead: () => void;
  /** Move one row to another zero-based position (`prompts.move`). */
  onMove: (id: string, index: number) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  // Escape closes this panel before it reaches the screen's "stop the turn":
  // with the panel open, Esc used to abort the run while the reader was looking
  // at the queue (round-4 loop audit F6). Declared before the early return so the
  // hook order never depends on the queue being non-empty.
  useEscClaim({
    id: 'queue-panel',
    level: 'overlay',
    active: open,
    onEscape: () => {
      setOpen(false);
      return true;
    },
  });
  if (items.length === 0) return null;

  const content = (
    <div className="zk-queue">
      {error ? (
        <p role="alert" className="zk-queue__error">
          {error}
        </p>
      ) : null}
      <ul className="zk-queue__list">
        {items.map((item, index) => (
          <li key={item.id} className="zk-queue__row">
            <span className="zk-queue__index">#{index + 1}</span>
            <span className="zk-queue__text">{item.text}</span>
            <span className="zk-queue__moves">
              <button
                type="button"
                className="zk-icon-button"
                aria-label={t('thread.queue.moveUp', { text: item.text })}
                disabled={index === 0}
                onClick={() => onMove(item.id, index - 1)}
                data-testid="queue-move-up"
              >
                <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
                  <path d="M6 9.5V3M3.2 5.2 6 2.4l2.8 2.8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>
              <button
                type="button"
                className="zk-icon-button"
                aria-label={t('thread.queue.moveDown', { text: item.text })}
                disabled={index === items.length - 1}
                onClick={() => onMove(item.id, index + 1)}
                data-testid="queue-move-down"
              >
                <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
                  <path d="M6 2.5V9M3.2 6.8 6 9.6l2.8-2.8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>
            </span>
            <button
              type="button"
              className="zk-icon-button"
              aria-label={t('thread.queue.cancel', { text: item.text })}
              onClick={() => onCancel(item.id)}
            >
              <ZkIcon name="x" />
            </button>
          </li>
        ))}
      </ul>
      {!running ? (
        <div className="zk-queue__footer">
          <Button size="small" type="text" onClick={onSendHead}>
            {t('thread.queue.sendHead')}
          </Button>
        </div>
      ) : null}
    </div>
  );

  return (
    <div className="zk-queue-chip" role="group" aria-label={t('thread.queue.title')}>
      <Popover
        trigger="click"
        placement="topLeft"
        content={content}
        title={t('thread.queue.title')}
        open={open}
        onOpenChange={setOpen}
      >
        <Button size="small" type="text">
          {t('thread.queue.count', { count: items.length })}
        </Button>
      </Popover>
    </div>
  );
}