import { CopyOutlined, EllipsisOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Dropdown, Tooltip } from 'antd';
import { useState } from 'react';
import { useT } from '@/i18n';

/**
 * The `⋯` menu on an assistant message (B11).
 *
 * Two actions, and only the two the protocol actually has:
 *  - 复制 — pure client-side, always available;
 *  - 重新生成 — `POST /api/chat/run {regenerate:true}` (§4.6), i.e. the same
 *    single send path as a normal turn.
 *
 * 编辑 is **absent**, not disabled: there is no `message.edit` method anywhere in
 * the protocol, and a menu entry that can never work is worse than no entry. That
 * is why B11 is recorded as 部分 in ACCEPTANCE-P1P2.md.
 */
export function MessageActions({
  text,
  role,
  onRegenerate,
  regenerateDisabled = false,
  regenerateHint,
  onEdit,
  editDisabled = false,
  editHint,
}: {
  text: string;
  /** Which menu this row gets: a user message can be edited, an answer cannot. */
  role: 'user' | 'assistant';
  onRegenerate?: () => void;
  regenerateDisabled?: boolean;
  regenerateHint?: string;
  onEdit?: () => void;
  editDisabled?: boolean;
  editHint?: string;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard?.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // A denied clipboard is not worth an error surface: the menu simply stays.
      setCopied(false);
    }
  };

  const copyButton = (
    <Tooltip title={copied ? t('action.copied') : t('action.copy')}>
      <Button
        size="small"
        type="text"
        aria-label={t('action.copy')}
        icon={<CopyOutlined />}
        onClick={() => void copy()}
      />
    </Tooltip>
  );

  if (role === 'user') {
    // Only a user message has an edit target (§4.6: `edit_target_not_user`).
    return (
      <div className="zk-msg-actions">
        {copyButton}
        <Button
          size="small"
          type="text"
          disabled={editDisabled || !onEdit}
          title={editDisabled ? editHint : undefined}
          onClick={() => onEdit?.()}
        >
          {t('thread.edit')}
        </Button>
      </div>
    );
  }

  if (!onRegenerate) {
    return <div className="zk-msg-actions">{copyButton}</div>;
  }

  const item = (
    <Button
      size="small"
      type="text"
      disabled={regenerateDisabled}
      onClick={() => onRegenerate()}
      icon={<ReloadOutlined />}
    >
      {t('thread.regenerate')}
    </Button>
  );

  return (
    <div className="zk-msg-actions">
      {copyButton}
      <Dropdown
        trigger={['click']}
        menu={{
          items: [
            {
              key: 'regenerate',
              label: regenerateDisabled ? (
                <Tooltip title={regenerateHint}>{item}</Tooltip>
              ) : (
                item
              ),
            },
          ],
        }}
      >
        <Button size="small" type="text" aria-label={t('thread.moreActions')} icon={<EllipsisOutlined />} />
      </Dropdown>
    </div>
  );
}