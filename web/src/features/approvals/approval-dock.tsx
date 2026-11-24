import { useNavigate } from 'react-router';
import { useApprovals } from './approvals-context';
import { ApprovalCard } from './approval-card';
import { useSessions } from '@/features/sessions/sessions-context';

/**
 * The composer dock for the oldest pending approval (docs/AGENT-SURFACES.md S4:
 * inline card above the input, one at a time, never a stack of modals).
 *
 * Rendered on the thread *and* on `/`, so a request that arrives while the user
 * is on the landing screen is still visible, with its provenance line and an
 * action that opens the requesting conversation.
 */
export function ApprovalDock({ currentSessionId }: { currentSessionId?: string }) {
  const navigate = useNavigate();
  const { sessions } = useSessions();
  const { approvals, resolve, error } = useApprovals();
  const oldest = approvals[0];
  if (!oldest) {
    // A failed decision with an empty queue still has to say so: the request was
    // put back, but if the delta already removed it the card is gone and the
    // error would otherwise have no renderer (AUDIT-2026-11-24 B-6).
    return error ? (
      <div className="zk-dock">
        <p role="alert" className="zk-dock__error">
          {error}
        </p>
      </div>
    ) : null;
  }

  const originTitle = oldest.sessionId
    ? sessions.find((item) => item.id === oldest.sessionId)?.title
    : undefined;

  return (
    <div className="zk-dock">
      <ApprovalCard
        // Keyed by the request: without it React reuses the card for the next
        // approval in the queue and keeps the previous one's "once / this
        // session" choice, so the user's habitual click silently grants a
        // session-wide permission (AUDIT-2026-11-24 B-5).
        key={oldest.id}
        request={oldest}
        pending={approvals.length}
        sessionTitle={originTitle}
        originSessionId={oldest.sessionId}
        currentSessionId={currentSessionId}
        onOpenSession={oldest.sessionId ? () => navigate(`/c/${oldest.sessionId}`) : undefined}
        onResolve={(decision) => void resolve(oldest.id, decision)}
      />
      {error ? (
        <p role="alert" className="zk-dock__error">
          {error}
        </p>
      ) : null}
    </div>
  );
}