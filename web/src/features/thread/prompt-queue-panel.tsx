import { Button, Popover } from 'antd';
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
}: {
  items: QueuedPrompt[];
  running: boolean;
  /** A failed enqueue/remove: shown here so the text is never silently lost. */
  error?: string | null;
  onCancel: (id: string) => void;
  onSendHead: () => void;
}) {
  const t = useT();
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
      >
        <Button size="small" type="text">
          {t('thread.queue.count', { count: items.length })}
        </Button>
      </Popover>
    </div>
  );
}