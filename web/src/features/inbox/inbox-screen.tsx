import { useEffect, useMemo, useState } from 'react';
import { Button, Empty, Tag } from 'antd';
import { useNavigate } from 'react-router';
import {
  buildInboxItems,
  inboxActionKind,
  type InboxKind,
} from '@/api/inbox';
import { subscribeArtifacts, subscribeAsk, subscribePlan } from '@/api/gateway';
import { useApprovals } from '@/features/approvals/approvals-context';
import { ZkIcon } from '@/design/icons';
import { useT } from '@/i18n';

/** Kind → copy key, so the label lives in the catalogs, not in the data layer. */
const KIND_KEY = {
  approval: 'inbox.kind.approval',
  ask: 'inbox.kind.ask',
  plan: 'inbox.kind.plan',
  artifact: 'inbox.kind.artifact',
  completion: 'inbox.kind.completion',
} as const;

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
  const t = useT();
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
          {items.length === 0
            ? t('inbox.empty')
            : t('inbox.count', { count: items.length })}
        </span>
      </div>

      {items.length === 0 ? (
        <Empty description={t('inbox.empty')} image={Empty.PRESENTED_IMAGE_SIMPLE} />
      ) : (
        <ul className="zk-inbox__list">
          {items.map((item) => (
            <li key={item.id} className="zk-inbox__row">
              <span className="zk-inbox__icon">
                <ZkIcon name={KIND_ICON[item.kind]} />
              </span>
              <div className="zk-inbox__body">
                <div className="zk-inbox__title">
                  <Tag variant="filled">{t(KIND_KEY[item.kind])}</Tag>
                  {/* A plan item has no title of its own; its kind is the title. */}
                  <span>{item.title || t(KIND_KEY[item.kind])}</span>
                </div>
                {item.options?.length ? (
                  <div className="zk-inbox__detail">
                    {t('inbox.askOptions', { options: item.options.join(' / ') })}
                  </div>
                ) : null}
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
                    {t('inbox.open')}
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
  const t = useT();
  return (
    <span className="zk-inbox__decision">
      <Button size="small" onClick={() => onDecide('deny')}>
        {t('inbox.deny')}
      </Button>
      <Button size="small" onClick={() => onDecide('allow')}>
        {t('inbox.allowOnce')}
      </Button>
    </span>
  );
}