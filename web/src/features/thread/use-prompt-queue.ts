import { useCallback, useEffect, useState } from 'react';
import { hasCapability, onGatewayReady } from '@/api/gateway';
import {
  enqueuePrompt,
  listQueuedPrompts,
  removeQueuedPrompt,
  type QueuedPrompt,
} from '@/api/prompt-queue';

export interface PromptQueueApi {
  items: QueuedPrompt[];
  error: string | null;
  /**
   * True when this build cannot reach a queue: the gateway serves the inbox as
   * the WS `prompts` domain now, so the older REST bridge is dead weight there.
   * Sending while running must then say so instead of dropping the text.
   */
  disabled: boolean;
  /** Re-read the backend queue; resolves with the fresh list for callers that act on it. */
  refresh: () => Promise<QueuedPrompt[]>;
  enqueue: (text: string) => Promise<QueuedPrompt>;
  remove: (id: string) => Promise<void>;
}

const messageOf = (cause: unknown, fallback: string) =>
  cause instanceof Error ? cause.message : fallback;

/**
 * The session's prompt inbox (A5/B13). Mirrors the backend-owned queue of
 * contracts/promptqueue.go: this hook never invents an order or keeps a second
 * copy of the truth — it reads, enqueues and removes, and the list it renders
 * is what the gateway reports.
 */
export function usePromptQueue(sessionId: string): PromptQueueApi {
  const [items, setItems] = useState<QueuedPrompt[]>([]);
  const [error, setError] = useState<string | null>(null);
  // Capabilities only exist after `initialize`. Asking before that would send
  // an unauthenticated/unavailable request and collect a 401, so the queue
  // waits for the handshake before deciding anything.
  const [ready, setReady] = useState(false);
  useEffect(() => onGatewayReady(() => setReady(true)), []);
  // The REST bridge is the mock-era shape; a gateway that advertises the
  // `prompts` domain does not serve it (its `/api/*` GETs also answer 401 by
  // C3). Until the client is switched to `prompts.*`, the honest state is
  // "disabled", not a stream of failing requests.
  const disabled = ready && hasCapability('prompts');

  const refresh = useCallback(async () => {
    if (disabled) return [];
    try {
      const next = await listQueuedPrompts(sessionId);
      setItems(next);
      setError(null);
      return next;
    } catch (cause) {
      setError(messageOf(cause, '待发送队列加载失败'));
      return [];
    }
  }, [sessionId]);

  useEffect(() => {
    if (!ready || disabled) return;
    let cancelled = false;
    (async () => {
      try {
        const next = await listQueuedPrompts(sessionId);
        if (!cancelled) setItems(next);
      } catch (cause) {
        if (!cancelled) setError(messageOf(cause, '待发送队列加载失败'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId, disabled, ready]);

  const enqueue = useCallback(
    async (text: string) => {
      if (disabled) throw new Error('待发送队列尚未接线到网关');
      const prompt = await enqueuePrompt(sessionId, text);
      setItems((current) => [...current, prompt]);
      return prompt;
    },
    [sessionId, disabled],
  );

  const remove = useCallback(
    async (id: string) => {
      // Optimistic: the row must disappear on the click; a failed delete
      // re-appears on the next read.
      setItems((current) => current.filter((item) => item.id !== id));
      try {
        await removeQueuedPrompt(sessionId, id);
      } finally {
        await refresh().catch(() => []);
      }
    },
    [sessionId, refresh],
  );

  return { items, error, disabled, refresh, enqueue, remove };
}