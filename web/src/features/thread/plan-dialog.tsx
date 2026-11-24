import { useEffect, useState } from 'react';
import { Button, Input, Modal, Space, Typography } from 'antd';
import { exitPlan, subscribePlan, type PlanState } from '@/api/gateway';
import { useT } from '@/i18n';

/**
 * The plan review (native 「计划待审阅」): the model submitted a plan and the run is
 * parked until a human approves or cancels it.
 *
 * The gateway had `plan.exit` all along and the client never sent it, so a plan was
 * a dead end: the model waited, the user could only read the inbox row. Approve and
 * cancel go through `plan.exit`; 「继续规划」 sends the feedback as a normal message,
 * which is exactly what the native client did (there is no continue action — the
 * revision is a new turn).
 *
 * Esc deliberately does nothing here, matching the native hint: an accidental Esc
 * must not throw away a submitted plan.
 */
export function PlanDialog({ onContinue }: { onContinue?: (feedback: string) => void }) {
  const t = useT();
  const [plan, setPlan] = useState<PlanState | null>(null);
  const [feedback, setFeedback] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => subscribePlan((next) => setPlan(next)), []);

  const reviewing = plan?.state === 'reviewing' && Boolean(plan.content?.trim());
  useEffect(() => {
    if (reviewing) setFeedback('');
  }, [reviewing]);

  if (!reviewing || !plan) return null;

  const decide = async (action: 'approve' | 'cancel') => {
    setBusy(true);
    try {
      await exitPlan(action);
      setPlan((current) => (current ? { ...current, state: action === 'approve' ? 'approved' : 'cancelled' } : current));
      // `PlanService.Review` returns immediately (the native desktop bridge did the
      // same): approving records the decision, it does not resume a parked run. The
      // native client therefore started the execution as a normal turn — so does
      // this one, with a visible message the user could have typed.
      if (action === 'approve') onContinue?.(t('plan.approvedContinue'));
    } finally {
      setBusy(false);
    }
  };

  const cont = () => {
    const text = feedback.trim();
    if (!text || !onContinue) return;
    onContinue(text);
    setFeedback('');
    setPlan(null);
  };

  return (
    <Modal
      open
      closable={false}
      mask={{ closable: false }}
      title={t('plan.title')}
      footer={
        <Space>
          <Button loading={busy} onClick={() => void decide('cancel')}>
            {t('plan.cancel')}
          </Button>
          <Button disabled={!feedback.trim() || !onContinue} onClick={cont}>
            {t('plan.continue')}
          </Button>
          <Button type="primary" loading={busy} onClick={() => void decide('approve')} data-testid="plan-approve">
            {t('plan.approve')}
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', maxHeight: 380, overflow: 'auto' }}>
        {plan.content}
      </Typography.Paragraph>
      <Input
        aria-label={t('plan.field')}
        placeholder={t('plan.field')}
        value={feedback}
        onChange={(event) => setFeedback(event.target.value)}
        onPressEnter={cont}
      />
      <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
        {t('plan.hint')}
      </Typography.Paragraph>
    </Modal>
  );
}
