import { Button } from 'antd';
import type { QueuedPrompt } from '@/api/prompt-queue';
import { ZkIcon } from '@/design/icons';

/**
 * "N 条待发送" above the composer: the queue the backend holds, one cancel per
 * row, and — when the session is idle — a way to send the head after an abort
 * (an abort never discards what the user already typed).
 */
export function PromptQueuePanel({
  items,
  running,
  onCancel,
  onSendHead,
}: {
  items: QueuedPrompt[];
  running: boolean;
  onCancel: (id: string) => void;
  onSendHead: () => void;
}) {
  if (items.length === 0) return null;

  return (
    <section className="zk-queue" aria-label="待发送队列">
      <div className="zk-queue__header">
        <span className="zk-queue__count">{items.length} 条待发送</span>
        {!running ? (
          <Button size="small" type="text" onClick={onSendHead}>
            发送队首
          </Button>
        ) : null}
      </div>
      <ul className="zk-queue__list">
        {items.map((item, index) => (
          <li key={item.id} className="zk-queue__row">
            <span className="zk-queue__index">#{index + 1}</span>
            <span className="zk-queue__text">{item.text}</span>
            <button
              type="button"
              className="zk-icon-button"
              aria-label={`取消待发送：${item.text}`}
              onClick={() => onCancel(item.id)}
            >
              <ZkIcon name="x" />
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}