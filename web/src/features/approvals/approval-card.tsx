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

const IMPACT_BY_RISK: Record<string, string> = {
  exec: '这条命令会在工作区里执行，可能改动文件或发出网络请求。',
  write: '这次调用会写入工作区里的文件。',
  network: '这次调用会向外部发出网络请求。',
  read: '这次调用只读取内容。',
};

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
  const [scope, setScope] = useState<'once' | 'session'>('once');
  const meta = toolMeta(request.tool);
  const target = approvalTarget(request);
  const impact = IMPACT_BY_RISK[approvalRisk(request)];
  const otherSession =
    originSessionId && currentSessionId && originSessionId !== currentSessionId;

  return (
    <section
      className="zk-approval"
      role="region"
      aria-label={`需要审批 · ${meta.label}`}
      aria-live="assertive"
    >
      <div className="zk-approval__header">
        <StatusBadge tone="warning" label="需要审批" />
        <span className="zk-approval__tool">{meta.label}</span>
        <span className="zk-approval__id">{request.tool}</span>
        <span className="zk-approval__origin">
          {sessionTitle ? `来自会话：${sessionTitle}` : '来自本会话'}
          {request.source === 'automation' ? ' · 自动化' : ' · 用户'}
          {otherSession && onOpenSession ? (
            <Button size="small" type="link" onClick={onOpenSession}>
              打开该会话
            </Button>
          ) : null}
          {pending > 1 ? <span className="zk-approval__queue">还有 {pending - 1} 条待审批</span> : null}
        </span>
      </div>

      <p className="zk-approval__impact">{impact}</p>

      {target ? (
        <div className="zk-approval__target">
          <span className="zk-approval__target-label">
            {target.kind === 'command' ? '命令' : '路径'}
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
              label: '原始参数',
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
          <Typography.Text type="secondary">Esc 拒绝 · Cmd/Ctrl+Enter 允许</Typography.Text>
        </div>
        <Select
          size="small"
          value={scope}
          onChange={(value) => setScope(value)}
          options={[
            { value: 'once', label: '仅此一次' },
            { value: 'session', label: '本会话内始终允许' },
          ]}
          aria-label="允许范围"
          popupMatchSelectWidth={false}
        />
        {/* No filled primary: an approval must have no visual default action
            (docs/WEB-CLIENT.md A9). Both choices are equally weighted. */}
        <Button onClick={() => onResolve('deny')}>拒绝</Button>
        <Button onClick={() => onResolve(scope === 'once' ? 'allow' : 'allow_always')}>
          {scope === 'once' ? '允许一次' : '本会话内始终允许'}
        </Button>
      </div>
    </section>
  );
}