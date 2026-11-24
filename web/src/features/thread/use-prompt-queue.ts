import { useCallback, useEffect, useState } from 'react';
import {
  enqueuePrompt,
  moveQueuedPrompt,
  refreshPrompts,
  removeQueuedPrompt,
  subscribePrompts,
} from '@/api/gateway';
import type { QueuedPrompt } from '@/api/prompt-queue';

export interface PromptQueueApi {
  items: QueuedPrompt[];
  error: string | null;
  /** Re-read the backend queue; resolves with the fresh list for callers that act on it. */
  refresh: () => Promise<QueuedPrompt[]>;
  enqueue: (text: string) => Promise<QueuedPrompt>;
  remove: (id: string) => Promise<void>;
  move: (id: string, index: number) => Promise<void>;
}

const messageOf = (cause: unknown, fallback: string) =>
  cause instanceof Error ? cause.message : fallback;

/**
 * The session's prompt inbox (A5/B13) on the WS `prompts:<sessionID>` domain.
 *
 * There is no second copy of the order: the list rendered is the domain's
 * snapshot + deltas. Enqueue and remove are commands; the gateway pushes the
 * delta that changes the list.
 */
export function usePromptQueue(sessionId: string): PromptQueueApi {
  const [items, setItems] = useState<QueuedPrompt[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;
    void subscribePrompts(sessionId, (next) => {
      if (cancelled) return;
      setItems(next);
      setError(null);
    })
      .then((stop) => {
        if (cancelled) stop();
        else unsubscribe = stop;
      })
      .catch((cause: unknown) => {
        if (!cancelled) setError(messageOf(cause, 'thread.queue.loadFailed'));
      });
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [sessionId]);

  const refresh = useCallback(async () => {
    try {
      const next = await refreshPrompts(sessionId);
      setItems(next);
      setError(null);
      return next;
    } catch (cause) {
      setError(messageOf(cause, 'thread.queue.loadFailed'));
      return [];
    }
  }, [sessionId]);

  const enqueue = useCallback(
    async (text: string) => {
      const { id } = await enqueuePrompt(sessionId, text);
      // Optimistic append so the row shows before the delta lands; the delta
      // replaces the whole list moments later.
      const prompt: QueuedPrompt = { id, sessionId, text };
      setItems((current) =>
        current.some((item) => item.id === id) ? current : [...current, prompt],
      );
      return prompt;
    },
    [sessionId],
  );

  const remove = useCallback(
    async (id: string) => {
      // Optimistic: the row must disappear on the click; a failed remove is
      // corrected by the next snapshot/delta.
      setItems((current) => current.filter((item) => item.id !== id));
      try {
        await removeQueuedPrompt(sessionId, id);
      } catch (cause) {
        setError(messageOf(cause, 'thread.queue.cancelFailed'));
        await refresh();
      }
    },
    [sessionId, refresh],
  );

  const move = useCallback(
    async (id: string, index: number) => {
      try {
        await moveQueuedPrompt(sessionId, id, index);
      } catch (cause) {
        setError(messageOf(cause, 'thread.queue.moveFailed'));
        // The domain is authoritative: a refused move (the queue drained, or a
        // race) is corrected by re-reading instead of guessing the order.
        await refresh();
      }
    },
    [sessionId, refresh],
  );

  return { items, error, refresh, enqueue, remove, move };
}