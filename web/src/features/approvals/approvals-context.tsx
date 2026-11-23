import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { isExpired, type ApprovalDecision, type ApprovalRequest } from '@/api/approvals';
import { resolveApproval, subscribeApprovals } from '@/api/gateway';
import { useEscClaim, useShortcut } from '@/keyboard/keyboard-provider';

interface ApprovalsApi {
  /** Oldest first; expired requests are already filtered out. */
  approvals: ApprovalRequest[];
  mode: string;
  permission: string;
  approvalTimeoutSeconds: number;
  loading: boolean;
  error: string | null;
  resolve: (id: string, decision: ApprovalDecision) => Promise<void>;
}

const ApprovalsContext = createContext<ApprovalsApi | null>(null);

/**
 * The approval queue: one source of truth for the inline card, the Inbox row
 * and the Escape / Mod+Enter decisions.
 *
 * Transport: the WS `approvals` domain (§6.2). The gateway pushes on every
 * `Approvals.Subscribe` change — there is no polling here any more.
 */
export function ApprovalsProvider({ children }: { children: ReactNode }) {
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [mode, setMode] = useState('workspace');
  const [permission, setPermission] = useState('workspace');
  const [timeoutSeconds, setTimeoutSeconds] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const unsubscribe = subscribeApprovals((snapshot) => {
      // Expiry is presentation, not protocol: a request past its deadline is
      // not offered for decision.
      setApprovals(snapshot.pending.filter((request) => !isExpired(request)));
      setMode(snapshot.mode);
      setPermission(snapshot.permission);
      setTimeoutSeconds(snapshot.approvalTimeoutSeconds);
      setLoading(false);
      setError(null);
    });
    return unsubscribe;
  }, []);

  const resolve = useCallback(async (id: string, decision: ApprovalDecision) => {
    // Optimistic removal: the card must disappear on the click; the
    // authoritative `approvals.delta` follows immediately.
    setApprovals((current) => current.filter((request) => request.id !== id));
    try {
      await resolveApproval(id, decision);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '审批未能提交');
    }
  }, []);

  const oldest = approvals[0];

  // L4 of the Escape chain: deny the oldest request, from anywhere, without
  // interrupting the run (docs/WEB-CLIENT.md §4.3).
  useEscClaim({
    id: 'deny-oldest-approval',
    level: 'approval',
    active: oldest !== undefined,
    onEscape: () => {
      if (!oldest) return false;
      void resolve(oldest.id, 'deny');
      return true;
    },
  });

  // A10: Cmd/Ctrl+Enter allows the request on screen. Deliberately modified —
  // a bare Enter must never decide an approval (A9).
  useShortcut({
    id: 'approve-oldest',
    mod: true,
    key: 'Enter',
    active: oldest !== undefined,
    run: () => {
      if (!oldest) return false;
      void resolve(oldest.id, 'allow');
      return true;
    },
  });

  const value = useMemo<ApprovalsApi>(
    () => ({
      approvals,
      mode,
      permission,
      approvalTimeoutSeconds: timeoutSeconds,
      loading,
      error,
      resolve,
    }),
    [approvals, mode, permission, timeoutSeconds, loading, error, resolve],
  );

  return <ApprovalsContext.Provider value={value}>{children}</ApprovalsContext.Provider>;
}

export function useApprovals(): ApprovalsApi {
  const value = useContext(ApprovalsContext);
  if (!value) throw new Error('useApprovals must be used inside <ApprovalsProvider>');
  return value;
}