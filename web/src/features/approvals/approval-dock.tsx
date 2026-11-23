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
  const { approvals, resolve } = useApprovals();
  const oldest = approvals[0];
  if (!oldest) return null;

  const originTitle = oldest.sessionId
    ? sessions.find((item) => item.id === oldest.sessionId)?.title
    : undefined;

  return (
    <div className="zk-dock">
      <ApprovalCard
        request={oldest}
        pending={approvals.length}
        sessionTitle={originTitle}
        originSessionId={oldest.sessionId}
        currentSessionId={currentSessionId}
        onOpenSession={oldest.sessionId ? () => navigate(`/c/${oldest.sessionId}`) : undefined}
        onResolve={(decision) => void resolve(oldest.id, decision)}
      />
    </div>
  );
}