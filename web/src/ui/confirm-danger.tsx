import { Modal, Button } from 'antd';
import type { ReactNode } from 'react';
import { useT } from '@/i18n';

/**
 * The danger confirmation (B15, AGENTS.md §3.9): the cancel button is the
 * focused default and the destructive action is never the default. Enter on a
 * freshly opened confirmation therefore cancels, not deletes.
 */
export function ConfirmDanger({
  open,
  title,
  description,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  description: ReactNode;
  confirmLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  return (
    <Modal
      open={open}
      title={title}
      onCancel={onCancel}
      keyboard={false}
      footer={[
        <Button key="cancel" autoFocus onClick={onCancel}>
          {t('ui.cancel')}
        </Button>,
        <Button key="confirm" danger onClick={onConfirm}>
          {confirmLabel ?? t('ui.delete')}
        </Button>,
      ]}
    >
      {description}
    </Modal>
  );
}