import { useEffect, useMemo, useState } from 'react';
import { Button, Empty, Tag } from 'antd';
import { useNavigate } from 'react-router';
import {
  INBOX_KIND_LABEL,
  buildInboxItems,
  inboxActionKind,
  type InboxKind,
} from '@/api/inbox';
import { subscribeArtifacts, subscribeAsk, subscribePlan } from '@/api/gateway';
import { useApprovals } from '@/features/approvals/approvals-context';
import { ZkIcon } from '@/design/icons';

const KIND_ICON: Record<InboxKind, React.ComponentProps<typeof ZkIcon>['name']> = {
  approval: 'shield',
  ask: 'message',
  plan: 'sparkles',
  artifact: 'file',
  completion: 'check',
};

/**
 * R2 · Inbox — everything waiting for you, from the WS domains that already
 * own it (approvals / ask / plan / artifacts). No aggregate endpoint and no
 * polling: a new approval is the same `approvals.delta` the thread card shows.
 */
export function InboxScreen() {
  const navigate = useNavigate();
  const { approvals, resolve } = useApprovals();
  const [ask, setAsk] = useState<{ id: string; text: string; options?: string[] } | null>(null);
  const [plan, setPlan] = useState<{ state: string; content: string } | null>(null);
  const [artifacts, setArtifacts] = useState<
    Array<{ id: string; title: string; body: string; createdAt?: string }>
  >([]);

  useEffect(() => {
    const offAsk = subscribeAsk((pending) => setAsk(pending));
    const offPlan = subscribePlan((state) => setPlan(state));
    const offArtifacts = subscribeArtifacts((items) => setArtifacts(items));
    return () => {
      offAsk();
      offPlan();
      offArtifacts();
    };
  }, []);

  const items = useMemo(
    () => buildInboxItems({ approvals, ask, plan, artifacts }),
    [approvals, ask, plan, artifacts],
  );

  return (
    <div className="zk-inbox">
      {/* The top band already names the screen; this line is the count only. */}
      <div className="zk-inbox__header">
        <span className="zk-inbox__count">
          {items.length === 0 ? '没有等你处理的事' : `${items.length} 项等待你`}
        </span>
      </div>

      {items.length === 0 ? (
        <Empty description="没有等你处理的事" image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : (
        <ul className="zk-inbox__list">
          {items.map((item) => (
            <li key={item.id} className="zk-inbox__row">
              <span className="zk-inbox__icon">
                <ZkIcon name={KIND_ICON[item.kind]} />
              </span>
              <div className="zk-inbox__body">
                <div className="zk-inbox__title">
                  <Tag variant="filled">{INBOX_KIND_LABEL[item.kind]}</Tag>
                  <span>{item.title}</span>
                </div>
                {item.detail ? <div className="zk-inbox__detail">{item.detail}</div> : null}
              </div>
              <div className="zk-inbox__actions">
                {item.kind === 'approval' ? (
                  <ApprovalActions
                    onDecide={(decision) =>
                      void resolve(item.id.slice('approval:'.length), decision)
                    }
                  />
                ) : (
                  <Button
                    size="small"
                    type="text"
                    onClick={() => {
                      if (item.sessionId) navigate(`/c/${item.sessionId}`);
                      else if (inboxActionKind(item) === 'artifact') navigate('/inbox');
                    }}
                  >
                    查看
                  </Button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * In-place decisions, deliberately *unweighted*: neither action is filled, so
 * the Inbox never suggests a default (docs/WEB-CLIENT.md A9).
 */
function ApprovalActions({ onDecide }: { onDecide: (decision: 'allow' | 'deny') => void }) {
  return (
    <span className="zk-inbox__decision">
      <Button size="small" onClick={() => onDecide('deny')}>
        拒绝
      </Button>
      <Button size="small" onClick={() => onDecide('allow')}>
        允许一次
      </Button>
    </span>
  );
}