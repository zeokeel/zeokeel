import { useCallback, useState } from 'react';

/**
 * A draft that survives leaving the conversation.
 *
 * The composer kept the draft in component state, so switching to another
 * conversation and back — or reloading — lost whatever had been typed, which is
 * the one thing a chat input must never do (round-2 composer audit).
 *
 * The value is per session (`zk.draft.<sessionId>`) and cleared by the caller
 * when the message is sent. localStorage failures (private mode, quota) degrade
 * to the old in-memory behaviour instead of breaking the input.
 */
export interface DraftStore {
  value: string;
  set: (next: string) => void;
  clear: () => void;
}

function storageKey(sessionId: string): string {
  return `zk.draft.${sessionId}`;
}

function readDraft(sessionId: string): string {
  try {
    return typeof localStorage === 'undefined' ? '' : (localStorage.getItem(storageKey(sessionId)) ?? '');
  } catch {
    return '';
  }
}

function writeDraft(sessionId: string, value: string): void {
  try {
    if (typeof localStorage === 'undefined') return;
    if (value === '') localStorage.removeItem(storageKey(sessionId));
    else localStorage.setItem(storageKey(sessionId), value);
  } catch {
    // Not fatal: the draft just stops persisting.
  }
}

export function useDraft(sessionId: string): DraftStore {
  // The session id is part of the state rather than a dependency: switching
  // conversations re-reads during render (React's "reset state when an input
  // changes" pattern), which an effect could only do a frame later.
  const [state, setState] = useState<{ sessionId: string; value: string }>(() => ({
    sessionId,
    value: readDraft(sessionId),
  }));
  if (state.sessionId !== sessionId) {
    setState({ sessionId, value: readDraft(sessionId) });
  }

  const set = useCallback(
    (next: string) => {
      setState({ sessionId, value: next });
      writeDraft(sessionId, next);
    },
    [sessionId],
  );

  const clear = useCallback(() => {
    setState({ sessionId, value: '' });
    writeDraft(sessionId, '');
  }, [sessionId]);

  return { value: state.value, set, clear };
}
