import { Button, Typography } from 'antd';
import { useNavigate, useParams } from 'react-router';
import { useEscClaim } from '@/keyboard/keyboard-provider';
import { useT } from '@/i18n';
import { TeamMemberDetail } from '@/features/team/member-detail';

/**
 * R7 · a child session (sub-agent / teammate), with the breadcrumb back to its
 * parent.
 *
 * L3 of the Escape chain: Esc returns to the parent and does **not** deny the
 * parent's approval — the child claim sits above the approval level, which is
 * exactly the regression E6 pins down.
 */
export function ChildSessionScreen() {
  const t = useT();
  const { sessionId, childId } = useParams<{ sessionId: string; childId: string }>();
  const navigate = useNavigate();

  // L3 of the Escape chain: Esc returns to the parent and does **not** deny the
  // parent's approval — the child claim sits above the approval level (E6).
  useEscClaim({
    id: 'child-session',
    level: 'childSession',
    active: Boolean(sessionId),
    onEscape: () => {
      if (!sessionId) return false;
      navigate(`/c/${sessionId}`);
      return true;
    },
  });

  return (
    <div className="zk-thread">
      <div className="zk-child-header">
        <Button size="small" type="text" onClick={() => sessionId && navigate(`/c/${sessionId}`)}>
          {t('screen.child.back')}
        </Button>
        <Typography.Text strong>{t('screen.child.title')}</Typography.Text>
        <Typography.Text type="secondary">{t('screen.child.escBack')}</Typography.Text>
      </div>
      <div className="zk-thread__body">
        {childId ? <TeamMemberDetail memberId={childId} /> : null}
      </div>
    </div>
  );
}
