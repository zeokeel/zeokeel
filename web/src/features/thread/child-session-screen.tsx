import { useEffect, useState } from 'react';
import { Button, Typography } from 'antd';
import { useNavigate, useParams } from 'react-router';
import { loadChildSession, type ChildSession } from '@/api/mock-surfaces';
import { Bubble } from '@ant-design/x';
import { useEscClaim } from '@/keyboard/keyboard-provider';
import { useT } from '@/i18n';

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
  const [child, setChild] = useState<ChildSession | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    if (!sessionId || !childId) return;
    (async () => {
      try {
        const value = await loadChildSession(sessionId, childId);
        if (!cancelled) setChild(value);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : t('screen.child.loadFailed'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId, childId]);

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
        <Button
          size="small"
          type="text"
          onClick={() => sessionId && navigate(`/c/${sessionId}`)}
        >
          {t('screen.child.back')}
        </Button>
        <Typography.Text strong>{child?.title ?? t('screen.child.title')}</Typography.Text>
        <Typography.Text type="secondary">{t('screen.child.escBack')}</Typography.Text>
      </div>
      <div className="zk-thread__body">
        {error ? (
          <div className="zk-msg-error" role="alert">
            {t('screen.child.loadFailed')}: {error}
          </div>
        ) : child === null ? (
          <div className="zk-empty">{t('screen.child.loading')}</div>
        ) : (
          <div className="zk-transcript">
            <div className="zk-transcript__scroll">
              {child.messages.map((message) => (
                <div key={message.id} className="zk-transcript__row">
                  <Bubble
                    placement={message.role === 'user' ? 'end' : 'start'}
                    variant={message.role === 'user' ? 'filled' : 'borderless'}
                    content={message.text}
                  />
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}