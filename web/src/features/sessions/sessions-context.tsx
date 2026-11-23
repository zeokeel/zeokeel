import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import {
  createSession,
  hasCapability,
  onGatewayReady,
  subscribeSessions,
  type SessionSummary,
} from '@/api/gateway';

interface SessionsApi {
  sessions: SessionSummary[];
  loading: boolean;
  error: string | null;
  /** The gateway advertises `session.create` (task-13); otherwise the UI hides it. */
  canCreate: boolean;
  reload: () => Promise<void>;
  create: () => Promise<SessionSummary>;
}

const SessionsContext = createContext<SessionsApi | null>(null);

/**
 * The sidebar's session list, fed by the `session.list` domain
 * (docs/TRANSPORT.md §6.2): the first snapshot, then `session.list.delta`.
 *
 * No polling and no second copy: the domain store is the only state, so a
 * rename from another tab arrives as a delta.
 */
export function SessionsProvider({ children }: { children: ReactNode }) {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [canCreate, setCanCreate] = useState(false);

  useEffect(() => {
    const unsubscribe = subscribeSessions((list) => {
      setSessions(list);
      setLoading(false);
      setError(null);
    });
    const offReady = onGatewayReady(() => setCanCreate(hasCapability('session.create')));
    // A control plane that never answers must not read as "still loading":
    // settings/workspace can lag 2s, but a min without any snapshot is a fault.
    const timer = setTimeout(() => {
      setLoading((wasLoading) => {
        if (wasLoading) setError('shell.timeout');
        return false;
      });
    }, 20_000);
    return () => {
      unsubscribe();
      offReady();
      clearTimeout(timer);
    };
  }, []);

  const reload = useCallback(async () => {
    // A re-snapshot happens by itself on reconnect; this serves the explicit
    // "retry" in the error state.
    setLoading(true);
    try {
      const list = await new Promise<SessionSummary[]>((resolve) => {
        let stop = () => {};
        stop = subscribeSessions((value) => {
          stop();
          resolve(value);
        });
      });
      setSessions(list);
      setError(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'shell.sessions.failedShort');
    } finally {
      setLoading(false);
    }
  }, []);

  // C1: two clicks inside 600ms must create one session. The window is on the
  // action, not the button's disabled state, because the sidebar button and the
  // `/` composer both funnel here.
  const createGuard = useRef<{ at: number; promise: Promise<SessionSummary> } | null>(null);

  const create = useCallback(async () => {
    const now = Date.now();
    const guard = createGuard.current;
    if (guard && now - guard.at < 600) return guard.promise;
    // `createSession` re-snapshots the `session.list` domain (the gateway
    // rebuilds it before answering), so the new row arrives through the
    // subscription. Adding it here as well would show it twice.
    const promise = createSession();
    createGuard.current = { at: now, promise };
    return promise;
  }, []);

  const value = useMemo<SessionsApi>(
    () => ({ sessions, loading, error, canCreate, reload, create }),
    [sessions, loading, error, canCreate, reload, create],
  );

  return <SessionsContext.Provider value={value}>{children}</SessionsContext.Provider>;
}

export function useSessions(): SessionsApi {
  const value = useContext(SessionsContext);
  if (!value) throw new Error('useSessions must be used inside <SessionsProvider>');
  return value;
}