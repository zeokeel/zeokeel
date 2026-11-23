import { Input } from 'antd';
import { ZkIcon } from '@/design/icons';
import { useT } from '@/i18n';

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
  const t = useT();
  return (
    <div className="zk-find" role="search" aria-label={t('thread.findLabel')}>
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
        placeholder={t('thread.findLabel')}
        aria-label={t('thread.findContent')}
        allowClear
      />
      <span className="zk-find__count" aria-live="polite">
        {query.trim() === '' ? '' : matchCount === 0 ? t('thread.noMatch') : `${activeMatch + 1}/${matchCount}`}
      </span>
      <button type="button" className="zk-icon-button" aria-label={t('thread.previousMatch')} onClick={onPrev}>
        <ZkIcon name="chevron-down" style={{ transform: 'rotate(180deg)' }} />
      </button>
      <button type="button" className="zk-icon-button" aria-label={t('thread.nextMatch')} onClick={onNext}>
        <ZkIcon name="chevron-down" />
      </button>
      <button type="button" className="zk-icon-button" aria-label={t('thread.closeFind')} onClick={onClose}>
        <ZkIcon name="x" />
      </button>
    </div>
  );
}