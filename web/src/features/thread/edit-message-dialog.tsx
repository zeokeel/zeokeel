import { Input } from 'antd';
import { useState } from 'react';
import { ConfirmDanger } from '@/ui/confirm-danger';
import { useT } from '@/i18n';

/**
 * Confirming an edit (B11, §4.6).
 *
 * Two rules, both borrowed from B15 because the consequence is the same shape:
 *  - **cancel holds the initial focus**, so a bare Enter never rewrites history;
 *  - the copy says out loud that the conversation after this message is
 *    discarded — that is the part a user cannot undo.
 *
 * Nothing is patched locally on success: the gateway rewrites the transcript and
 * pushes `msg.reset`, and the UI follows that (an optimistic edit would leave
 * rows on screen that no longer exist).
 */
export function EditMessageDialog({
  open,
  initialText,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  initialText: string;
  onCancel: () => void;
  onConfirm: (text: string) => void;
}) {
  const t = useT();
  const [text, setText] = useState(initialText);
  const empty = text.trim() === '';

  return (
    <ConfirmDanger
      open={open}
      title={t('thread.editTitle')}
      description={
        <div className="zk-edit-dialog">
          <p>{t('thread.editDescription')}</p>
          <Input.TextArea
            value={text}
            autoSize={{ minRows: 2, maxRows: 8 }}
            aria-label={t('thread.editTitle')}
            onChange={(event) => setText(event.target.value)}
          />
          {empty ? <p className="zk-msg-error">{t('thread.editEmpty')}</p> : null}
        </div>
      }
      confirmLabel={t('thread.editConfirm')}
      onCancel={onCancel}
      onConfirm={() => {
        if (empty) return;
        onConfirm(text);
      }}
    />
  );
}
