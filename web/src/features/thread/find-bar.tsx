import { Input } from 'antd';
import { ZkIcon } from '@/design/icons';

/**
 * L2 of the Escape chain and K11: a transcript-scoped find bar. `Mod+F` opens
 * it; Esc closes it through the arbiter and hands focus back to the composer.
 *
 * It stays inside the transcript on purpose — the browser's own find cannot see
 * virtualised rows, so pretending it works would be worse than owning it.
 */
export function FindBar({
  query,
  onQueryChange,
  matchCount,
  activeMatch,
  onNext,
  onPrev,
  onClose,
}: {
  query: string;
  onQueryChange: (value: string) => void;
  matchCount: number;
  activeMatch: number;
  onNext: () => void;
  onPrev: () => void;
  onClose: () => void;
}) {
  return (
    <div className="zk-find" role="search" aria-label="在会话中查找">
      <ZkIcon name="search" />
      <Input
        autoFocus
        size="small"
        value={query}
        onChange={(event) => onQueryChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            if (event.shiftKey) onPrev();
            else onNext();
          }
        }}
        placeholder="在会话中查找…"
        aria-label="查找内容"
        allowClear
      />
      <span className="zk-find__count" aria-live="polite">
        {query.trim() === '' ? '' : matchCount === 0 ? '无匹配' : `${activeMatch + 1}/${matchCount}`}
      </span>
      <button type="button" className="zk-icon-button" aria-label="上一个" onClick={onPrev}>
        <ZkIcon name="chevron-down" style={{ transform: 'rotate(180deg)' }} />
      </button>
      <button type="button" className="zk-icon-button" aria-label="下一个" onClick={onNext}>
        <ZkIcon name="chevron-down" />
      </button>
      <button type="button" className="zk-icon-button" aria-label="关闭查找" onClick={onClose}>
        <ZkIcon name="x" />
      </button>
    </div>
  );
}