import { Modal, Button } from 'antd';
import type { ReactNode } from 'react';
import { useT } from '@/i18n';
import { useEscClaim } from '@/keyboard/keyboard-provider';

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
  // antd's own Esc is off (the cancel button is the safe default and Enter must
  // not delete), so the dialog has to claim Esc itself. Without this claim Esc
  // fell through to the next level — on a thread that is L5 "stop the running
  // turn", so the dialog stayed open *and* the run was killed
  // (AUDIT-2026-11-24 C).
  useEscClaim({
    id: 'confirm-danger',
    level: 'overlay',
    active: open,
    onEscape: () => {
      onCancel();
      return true;
    },
  });
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