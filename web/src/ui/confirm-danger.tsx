import { Modal, Button } from 'antd';
import type { ReactNode } from 'react';

/**
 * The danger confirmation (B15, AGENTS.md §3.9): the cancel button is the
 * focused default and the destructive action is never the default. Enter on a
 * freshly opened confirmation therefore cancels, not deletes.
 */
export function ConfirmDanger({
  open,
  title,
  description,
  confirmLabel = '删除',
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
  return (
    <Modal
      open={open}
      title={title}
      onCancel={onCancel}
      keyboard={false}
      footer={[
        <Button key="cancel" autoFocus onClick={onCancel}>
          取消
        </Button>,
        <Button key="confirm" danger onClick={onConfirm}>
          {confirmLabel}
        </Button>,
      ]}
    >
      {description}
    </Modal>
  );
}