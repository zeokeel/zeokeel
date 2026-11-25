/**
 * Inbox rows derived from the interrupt domains (docs/WEB-CLIENT.md §1.4):
 * pending approvals, a queued `ask_user`, a plan in review, artifacts waiting to
 * be previewed.
 *
 * This shaping used to live in `src/api/inbox.ts`. That path now belongs to the
 * gateway's inbound **work** inbox (`inbox.*`, docs/TRANSPORT.md §5.1.13) — a
 * different surface with a different item shape — so this projection moved next
 * to the screen that renders it. Nothing here talks to the gateway: the four
 * domains already arrive over WS (`api/gateway.ts`).
 *
 * `completion` stays in the vocabulary but is never produced today: the gateway
 * has no completion-receipt domain yet, so inventing rows would lie.
 */
import type { ApprovalRequest, ArtifactItem, AskPending, PlanState } from '@/api/gateway';

export type InboxKind = 'approval' | 'ask' | 'plan' | 'artifact' | 'completion';

export interface InboxItem {
  id: string;
  kind: InboxKind;
  sessionId?: string;
  sessionTitle?: string;
  /**
   * Items carry **data**, not copy: the screen decides how a kind is labelled.
   * `options` is returned raw so the caller can phrase it in the user's language
   * instead of this layer baking a sentence into a string.
   */
  title: string;
  detail?: string;
  options?: string[];
  createdAt?: string;
  expiresAt?: string;
  /** Pending approvals are decided in place from the Inbox as well. */
  decision?: 'pending' | 'settled';
}

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
      ...(sources.ask.options?.length ? { options: sources.ask.options } : {}),
      createdAt: sources.ask.createdAt,
    });
  }

  if (sources.plan && sources.plan.state === 'reviewing') {
    items.push({
      id: 'plan:current',
      kind: 'plan',
      // No title of its own: the screen labels a plan item from its kind, so the
      // phrase lives in the catalogue rather than in this data layer.
      title: '',
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