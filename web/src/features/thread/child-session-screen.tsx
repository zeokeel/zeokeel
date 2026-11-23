import { useEffect, useState } from 'react';
import { Button, Typography } from 'antd';
import { useNavigate, useParams } from 'react-router';
import { loadChildSession, type ChildSession } from '@/api/mock-surfaces';
import { Bubble } from '@ant-design/x';
import { useEscClaim } from '@/keyboard/keyboard-provider';

/**
 * R7 · a child session (sub-agent / teammate), with the breadcrumb back to its
 * parent.
 *
 * L3 of the Escape chain: Esc returns to the parent and does **not** deny the
 * parent's approval — the child claim sits above the approval level, which is
 * exactly the regression E6 pins down.
 */
export function ChildSessionScreen() {
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
        if (!cancelled) setError(cause instanceof Error ? cause.message : '子会话加载失败');
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
          ← 返回父会话
        </Button>
        <Typography.Text strong>{child?.title ?? '子会话'}</Typography.Text>
        <Typography.Text type="secondary">Esc 返回</Typography.Text>
      </div>
      <div className="zk-thread__body">
        {error ? (
          <div className="zk-msg-error" role="alert">
            子会话加载失败：{error}
          </div>
        ) : child === null ? (
          <div className="zk-empty">正在加载子会话…</div>
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