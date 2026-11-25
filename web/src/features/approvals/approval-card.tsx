import { useState } from 'react';
import { Button, Collapse, Select, Typography } from 'antd';
import {
  approvalRisk,
  approvalTarget,
  type ApprovalDecision,
  type ApprovalRequest,
} from '@/api/approvals';
import { MonoLine } from '@/chat/tools/sections';
import { toolMeta } from '@/chat/tools/meta';
import { StatusBadge } from '@/ui/status-badge';
import { useT } from '@/i18n';

/**
 * The inline approval card (docs/AGENT-SURFACES.md S4, visual language v2).
 *
 * Non-negotiables, each of which was a real incident or an explicit rule:
 *  - no default action and no initial focus: Enter never decides (A9);
 *  - Esc denies the oldest request through the arbiter, not through this card;
 *  - deny is a secondary button, allow is the card's single accent fill;
 *  - it is a named region with `aria-live="assertive"`, not a dialog.
 */
export function ApprovalCard({
  request,
  pending,
  sessionTitle,
  originSessionId,
  currentSessionId,
  onOpenSession,
  onResolve,
}: {
  request: ApprovalRequest;
  pending: number;
  sessionTitle?: string;
  originSessionId?: string;
  currentSessionId?: string;
  onOpenSession?: () => void;
  onResolve: (decision: ApprovalDecision) => void;
}) {
  const t = useT();
  const [scope, setScope] = useState<'once' | 'session'>('once');
  const meta = toolMeta(request.tool);
  const target = approvalTarget(request);
  const impact =
    approvalRisk(request) === 'write'
      ? t('approval.impact.write')
      : approvalRisk(request) === 'network'
        ? t('approval.impact.network')
        : approvalRisk(request) === 'read'
          ? t('approval.impact.read')
          : t('approval.impact.exec');
  const otherSession =
    originSessionId && currentSessionId && originSessionId !== currentSessionId;

  return (
    <section
      className="zk-approval"
      role="region"
      aria-label={`${t('approval.needDecision')} · ${t(meta.labelKey)}`}
      aria-live="assertive"
    >
      <div className="zk-approval__header">
        <StatusBadge tone="warning" label={t('approval.needDecision')} />
        <span className="zk-approval__tool">{t(meta.labelKey)}</span>
        <span className="zk-approval__id">{request.tool}</span>
        <span className="zk-approval__origin">
          {sessionTitle ? t('approval.fromSession', { title: sessionTitle }) : t('approval.fromThisSession')}
          {request.source === 'automation'
              ? ` · ${t('approval.source.automation')}`
              : ` · ${t('approval.source.user')}`}
          {otherSession && onOpenSession ? (
            <Button size="small" type="link" onClick={onOpenSession}>
              {t('approval.openSession')}
            </Button>
          ) : null}
          {pending > 1 ? <span className="zk-approval__queue">
            {t('approval.morePending', { count: pending - 1 })}
          </span> : null}
        </span>
      </div>

      <p className="zk-approval__impact">{impact}</p>

      {target ? (
        <div className="zk-approval__target">
          <span className="zk-approval__target-label">
            {target.kind === 'command'
              ? t('approval.target.command')
              : target.kind === 'url'
                ? t('approval.target.url')
                : t('approval.target.path')}
          </span>
          <MonoLine text={target.text} />
        </div>
      ) : null}

      {request.arguments ? (
        <Collapse
          ghost
          size="small"
          items={[
            {
              key: 'raw',
              label: t('approval.rawArguments'),
              children: (
                <pre className="zk-tool-io">
                  {request.arguments}
                </pre>
              ),
            },
          ]}
        />
      ) : null}

      <div className="zk-approval__footer">
        <div className="zk-approval__hints">
          <Typography.Text type="secondary">{t('approval.hint')}</Typography.Text>
        </div>
        <Select
          size="small"
          value={scope}
          onChange={(value) => setScope(value)}
          options={[
            { value: 'once', label: t('approval.scope.once') },
            { value: 'session', label: t('approval.allowAlways') },
          ]}
          aria-label={t('approval.scopeLabel')}
          popupMatchSelectWidth={false}
        />
        {/* No filled primary: an approval must have no visual default action
            (docs/WEB-CLIENT.md A9). Both choices are equally weighted. */}
        <Button data-testid="approval-deny" onClick={() => onResolve('deny')}>
          {t('approval.deny')}
        </Button>
        <Button
          data-testid="approval-allow"
          onClick={() => onResolve(scope === 'once' ? 'allow' : 'allow_always')}
        >
          {scope === 'once' ? t('approval.allowOnce') : t('approval.allowAlways')}
        </Button>
      </div>
    </section>
  );
}