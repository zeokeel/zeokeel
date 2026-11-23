/**
 * Inbox items — the waiting-for-you sources in one queue (docs/WEB-CLIENT.md §1.4).
 *
 * The list is derived, not fetched: every row comes from a WS domain the client
 * already subscribes to (`approvals`, `ask`, `plan`, `artifacts`). There is no
 * `/api/inbox` aggregate and no second copy of any entity — an approval row here
 * *is* the thread's approval card.
 *
 * `completion` stays in the vocabulary but is never produced today: the gateway
 * has no completion-receipt domain yet, so inventing rows would lie.
 */
import type { ApprovalRequest, ArtifactItem, AskPending, PlanState } from './gateway';

export type InboxKind = 'approval' | 'ask' | 'plan' | 'artifact' | 'completion';

export interface InboxItem {
  id: string;
  kind: InboxKind;
  sessionId?: string;
  sessionTitle?: string;
  title: string;
  detail?: string;
  createdAt?: string;
  expiresAt?: string;
  /** Pending approvals are decided in place from the Inbox as well. */
  decision?: 'pending' | 'settled';
}

export const INBOX_KIND_LABEL: Record<InboxKind, string> = {
  approval: '待审批',
  ask: '在问你',
  plan: '计划待审阅',
  artifact: '产物待预览',
  completion: '完成回执',
};

/** Source order in the list: decisions first, receipts last. */
export const INBOX_KIND_ORDER: InboxKind[] = [
  'approval',
  'ask',
  'plan',
  'artifact',
  'completion',
];

export interface InboxSources {
  approvals: ApprovalRequest[];
  ask: AskPending | null;
  plan: PlanState | null;
  artifacts: ArtifactItem[];
}

/** Flatten the four domains into one list, ordered by kind. */
export function buildInboxItems(sources: InboxSources): InboxItem[] {
  const items: InboxItem[] = [];

  for (const approval of sources.approvals) {
    items.push({
      id: `approval:${approval.id}`,
      kind: 'approval',
      sessionId: approval.sessionId,
      title: approval.summary || approval.tool,
      detail: approval.tool,
      createdAt: approval.createdAt,
      expiresAt: approval.expiresAt,
      decision: 'pending',
    });
  }

  if (sources.ask) {
    items.push({
      id: `ask:${sources.ask.id}`,
      kind: 'ask',
      title: sources.ask.text,
      detail: sources.ask.options?.length
        ? `可选：${sources.ask.options.join(' / ')}`
        : undefined,
      createdAt: sources.ask.createdAt,
    });
  }

  if (sources.plan && sources.plan.state === 'reviewing') {
    items.push({
      id: 'plan:current',
      kind: 'plan',
      title: '计划待审阅',
      detail: sources.plan.content.slice(0, 200),
    });
  }

  for (const artifact of sources.artifacts) {
    items.push({
      id: `artifact:${artifact.id}`,
      kind: 'artifact',
      title: artifact.title,
      detail: artifact.body.slice(0, 200),
      createdAt: artifact.createdAt,
    });
  }

  return items.sort(
    (a, b) => INBOX_KIND_ORDER.indexOf(a.kind) - INBOX_KIND_ORDER.indexOf(b.kind),
  );
}

export type InboxAction = 'allow' | 'deny' | 'dismiss';

/** Which domain a row belongs to (drives its action). */
export function inboxActionKind(item: InboxItem): 'approval' | 'ask' | 'plan' | 'artifact' {
  const [kind] = item.id.split(':');
  if (kind === 'ask' || kind === 'plan' || kind === 'artifact') return kind;
  return 'approval';
}