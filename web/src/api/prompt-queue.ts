/**
 * The session's prompt inbox (A5/B13) over the WS `prompts:<sessionID>` domain
 * (docs/TRANSPORT.md §6; contracts/promptqueue.go).
 *
 * The backend owns the queue — durable, ordered oldest-first. The client only
 * enqueues, removes, and reads the domain: there is no local copy and no REST
 * endpoint (one path, so the two can never drift apart again).
 *
 * The client drives the run (`POST /api/chat/run`, §4.6), so it claims the head
 * when a run settles instead of the gateway starting it.
 */
export interface QueuedPrompt {
  id: string;
  sessionId?: string;
  text: string;
  createdAt?: string;
}

/** Wire form of one queued prompt (`queuedPromptDTO`). */
export interface QueuedPromptWire {
  id: string;
  text: string;
  createdAt?: string;
}

/**
 * Fold one `prompts.delta` into the held list.
 *
 * A reorder arrives as `{reordered: true, order: [ids]}` — no added/removed at
 * all — so a client that only handles add/remove keeps the old order while the
 * backend has already changed it (the queue panel offered move buttons that
 * visibly did nothing).
 */
export function applyPromptsDelta(
  items: QueuedPrompt[],
  delta: {
    added?: QueuedPromptWire[];
    removed?: string[];
    reordered?: boolean;
    order?: string[];
  },
): QueuedPrompt[] {
  const removed = new Set(delta.removed ?? []);
  const added = delta.added ?? [];
  const kept = items.filter((item) => !removed.has(item.id));
  const byId = new Map(kept.map((item) => [item.id, item]));
  // The queue is ordered: appended prompts go to the end, not to the front.
  const order: QueuedPrompt[] = [];
  const seen = new Set<string>();
  for (const item of kept) {
    order.push(item);
    seen.add(item.id);
  }
  for (const item of added) {
    const value = byId.get(item.id) ?? { ...item };
    if (!seen.has(item.id)) {
      order.push(value);
      seen.add(item.id);
    }
  }
  if (delta.reordered && delta.order) {
    const position = new Map(delta.order.map((id, index) => [id, index]));
    // Entries the delta does not name keep their relative order after the ones
    // it does: a partial order must never drop a row from the list.
    return [...order]
      .map((item, index) => ({ item, index }))
      .sort((a, b) => {
        const left = position.get(a.item.id) ?? Number.MAX_SAFE_INTEGER;
        const right = position.get(b.item.id) ?? Number.MAX_SAFE_INTEGER;
        return left === right ? a.index - b.index : left - right;
      })
      .map((entry) => entry.item);
  }
  return order;
}
