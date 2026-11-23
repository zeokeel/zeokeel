/**
 * The layered Esc arbiter (docs/WEB-CLIENT.md §4.3) — the one place in the
 * client that listens for Escape.
 *
 * Why it exists at all: the native UI shipped a P0 where "Esc denies the
 * approval" silently stopped working because each surface registered its own
 * handler and Esc went wherever focus happened to be. The fix must never be
 * "component-local keydown" again, so this module owns a single
 * document-level capture listener and reads a declarative claim stack instead
 * of DOM focus.
 *
 * Only the levels this milestone ships are claimable; the full chain is named
 * here so task-5 slots in without re-litigating the order:
 *
 *   L1 overlay top → L1b settings dirty guard → L2 find bar → L3 child session
 *   → L4 deny oldest approval → L5 stop run → L6 clear draft
 */

// Higher value = higher priority. The numbers read top-down like the chain
// itself: an open overlay outranks denying an approval, which outranks
// stopping the run, which outranks clearing the draft.
export const ESC_LEVELS = {
  overlay: 70,
  settingsDirtyGuard: 60,
  findBar: 50,
  childSession: 40,
  approval: 30,
  stopRun: 20,
  clearDraft: 10,
} as const;

export type EscLevel = keyof typeof ESC_LEVELS;

export interface EscClaim {
  /** Stable id for debugging and for tests that assert which claim won. */
  id: string;
  level: EscLevel;
  /** Read at keydown time: a claim can stay mounted but inactive. */
  active: () => boolean;
  /** Consume Esc. Returning false means "I was not able to", so the next claim wins. */
  onEscape: () => boolean;
}

export type EscKey = Pick<
  KeyboardEvent,
  'key' | 'repeat' | 'defaultPrevented' | 'preventDefault' | 'stopPropagation'
>;

export class EscArbiter {
  private claims: EscClaim[] = [];
  /** Higher level wins; within a level the most recently pushed claim wins. */
  private sequence = 0;
  private order = new Map<string, number>();

  push(claim: EscClaim): () => void {
    const id = claim.id;
    this.claims = this.claims.filter((existing) => existing.id !== id);
    this.order.set(id, ++this.sequence);
    this.claims.push(claim);
    return () => this.remove(id);
  }

  remove(id: string): void {
    this.claims = this.claims.filter((claim) => claim.id !== id);
    this.order.delete(id);
  }

  /** The claims that could act right now, strongest first. */
  candidates(): EscClaim[] {
    return [...this.claims]
      .filter((claim) => claim.active())
      .sort((a, b) => {
        const level = ESC_LEVELS[b.level] - ESC_LEVELS[a.level];
        if (level !== 0) return level;
        return (this.order.get(b.id) ?? 0) - (this.order.get(a.id) ?? 0);
      });
  }

  /**
   * One keydown. Returns true when a claim consumed it (caller has already had
   * preventDefault + stopPropagation applied), false when Esc belongs to the
   * browser (E8: no preventDefault when nothing is active).
   */
  handleKey(key: EscKey): boolean {
    if (key.key !== 'Escape') return false;
    // E9: holding Esc must fire once, not once per repeat.
    if (key.repeat) return false;

    for (const claim of this.candidates()) {
      if (!claim.active()) continue;
      if (claim.onEscape()) {
        key.preventDefault();
        key.stopPropagation();
        return true;
      }
    }
    return false;
  }

  get size(): number {
    return this.claims.length;
  }
}

export const escArbiter = new EscArbiter();

/** Installs the single document capture listener. Returns its uninstaller. */
export function installEscListener(
  target: Pick<Document, 'addEventListener' | 'removeEventListener'>,
  arbiter: EscArbiter = escArbiter,
): () => void {
  const listener = (event: Event) => {
    arbiter.handleKey(event as KeyboardEvent);
  };
  target.addEventListener('keydown', listener, { capture: true });
  return () => target.removeEventListener('keydown', listener, { capture: true });
}