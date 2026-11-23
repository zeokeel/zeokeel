/**
 * Domain stores for the WS state plane (docs/TRANSPORT.md §6).
 *
 * One `SyncedDomain` per wire domain: it holds the last snapshot + revision,
 * folds deltas, and asks for a fresh snapshot whenever the delta cannot be
 * applied (baseRev mismatch, `resync:true`, or a new epoch). The components only
 * ever read `value()` — they never see a revision.
 */

export interface DomainDelta {
  baseRev?: number;
  rev?: number;
  resync?: boolean;
  [key: string]: unknown;
}

export interface Snapshot<T> {
  value: T;
  rev: number;
  epoch: string;
}

export class SyncedDomain<T> {
  private current: Snapshot<T> | null = null;
  private listeners = new Set<(value: T) => void>();
  private inflight: Promise<void> | null = null;
  /** A delta arrived that could not be applied: re-load after the current one. */
  private needsResync = false;

  constructor(
    readonly name: string,
    private readonly loader: () => Promise<Snapshot<T>>,
    private readonly reducer: (value: T, delta: DomainDelta) => T,
  ) {}

  value(): T | null {
    return this.current?.value ?? null;
  }

  rev(): number {
    return this.current?.rev ?? 0;
  }

  subscribe(listener: (value: T) => void): () => void {
    this.listeners.add(listener);
    if (this.current) listener(this.current.value);
    return () => this.listeners.delete(listener);
  }

  /** Fetch the full snapshot; concurrent callers share the same request. */
  async reload(): Promise<void> {
    if (!this.inflight) {
      this.inflight = this.runLoad();
      try {
        await this.inflight;
      } finally {
        this.inflight = null;
      }
    } else {
      await this.inflight;
    }
    if (this.needsResync) {
      this.needsResync = false;
      await this.reload();
    }
  }

  private async runLoad(): Promise<void> {
    const snapshot = await this.loader();
    this.current = snapshot;
    this.emit();
  }

  /**
   * Apply one delta, or reload when it cannot be applied. Returns what happened
   * so a caller (and the tests) can assert the resync policy.
   */
  applyDelta(delta: DomainDelta): 'applied' | 'resync' {
    if (!this.current || delta.resync === true || (delta.baseRev ?? 0) !== this.current.rev) {
      // Idle: the reload below is the resync. Busy: queue one after it so the
      // snapshot is not taken from under the in-flight request.
      if (this.inflight) this.needsResync = true;
      void this.reload();
      return 'resync';
    }
    this.current = {
      value: this.reducer(this.current.value, delta),
      rev: delta.rev ?? this.current.rev,
      epoch: this.current.epoch,
    };
    this.emit();
    return 'applied';
  }

  private emit(): void {
    if (!this.current) return;
    const value = this.current.value;
    this.listeners.forEach((listener) => listener(value));
  }
}

/** Merge an item-set delta keyed by `id` (approvals, artifacts). */
export function applyItemSet<T extends { id: string }>(
  items: T[],
  delta: DomainDelta,
): T[] {
  const removed = new Set((delta.removed as string[] | undefined) ?? []);
  const added = (delta.added as T[] | undefined) ?? [];
  const byId = new Map<string, T>();
  for (const item of items) {
    if (!removed.has(item.id)) byId.set(item.id, item);
  }
  for (const item of added) byId.set(item.id, item);
  // The gateway sends additions in order; keep that order and append anything
  // it did not mention.
  const ordered: T[] = [];
  const seen = new Set<string>();
  for (const item of added) {
    const value = byId.get(item.id);
    if (value && !seen.has(item.id)) {
      ordered.push(value);
      seen.add(item.id);
    }
  }
  for (const item of items) {
    if (!seen.has(item.id) && byId.has(item.id)) {
      ordered.push(byId.get(item.id)!);
      seen.add(item.id);
    }
  }
  return ordered;
}

/** Merge a session-list-shaped delta (added / changed / removed). */
export function applyListDelta<T extends { id: string }>(
  items: T[],
  delta: DomainDelta,
): T[] {
  const removed = new Set((delta.removed as string[] | undefined) ?? []);
  const added = (delta.added as T[] | undefined) ?? [];
  const changed = (delta.changed as T[] | undefined) ?? [];
  const byId = new Map<string, T>();
  for (const item of items) {
    if (!removed.has(item.id)) byId.set(item.id, item);
  }
  for (const item of [...added, ...changed]) byId.set(item.id, item);
  const ordered: T[] = [];
  const seen = new Set<string>();
  for (const item of [...added, ...changed]) {
    if (!seen.has(item.id)) {
      ordered.push(byId.get(item.id)!);
      seen.add(item.id);
    }
  }
  for (const item of items) {
    if (!seen.has(item.id) && byId.has(item.id)) {
      ordered.push(byId.get(item.id)!);
      seen.add(item.id);
    }
  }
  return ordered;
}

/** Merge a settings delta: changed rows plus removed keys. */
export function applySettingsDelta(
  rows: Record<string, string>,
  delta: DomainDelta,
): Record<string, string> {
  const next = { ...rows };
  for (const key of (delta.removed as string[] | undefined) ?? []) delete next[key];
  const changed = (delta.rows as Record<string, string> | undefined) ?? {};
  Object.assign(next, changed);
  return next;
}