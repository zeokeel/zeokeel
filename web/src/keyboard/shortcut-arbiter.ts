/**
 * Global keyboard chords that are *not* Escape.
 *
 * Esc has its own layered arbiter (esc-arbiter.ts). This module covers the
 * browser chords the client deliberately owns (docs/WEB-CLIENT.md §4.1) with
 * the same discipline: one document-capture listener, a declarative registry,
 * and no component-local `addEventListener`.
 *
 * Reserved keys are never registered here: Cmd/Ctrl+N, T, W and `.`, and Tab.
 * Only chords the client may legitimately claim live in this file — today
 * Mod+K (palette), Mod+F (transcript find), Mod+Enter (approve the oldest
 * request).
 */

export interface ShortcutSpec {
  id: string;
  /** Cmd on macOS, Ctrl elsewhere. */
  mod?: boolean;
  shift?: boolean;
  /** Alt/Option. Note: on macOS Alt+letter can produce a different `event.key`. */
  alt?: boolean;
  /** `KeyboardEvent.key`, case-insensitive (`Enter`, `k`, `f`). */
  key: string;
  /** Read at keydown time. */
  active: () => boolean;
  /** Return true when the chord was consumed. */
  run: () => boolean;
}

export class ShortcutArbiter {
  private specs: ShortcutSpec[] = [];
  private order = new Map<string, number>();
  private sequence = 0;

  push(spec: ShortcutSpec): () => void {
    this.specs = this.specs.filter((existing) => existing.id !== spec.id);
    this.sequence += 1;
    this.order.set(spec.id, this.sequence);
    this.specs.push(spec);
    return () => this.remove(spec.id);
  }

  remove(id: string): void {
    this.specs = this.specs.filter((spec) => spec.id !== id);
    this.order.delete(id);
  }

  /** Most recently registered match for this event, if any is active. */
  match(event: KeyboardEvent): ShortcutSpec | undefined {
    const key = event.key.toLowerCase();
    const matches = this.specs.filter(
      (spec) =>
        spec.key.toLowerCase() === key &&
        Boolean(spec.mod) === (event.metaKey || event.ctrlKey) &&
        Boolean(spec.shift) === event.shiftKey &&
        Boolean(spec.alt) === event.altKey,
    );
    return matches
      .filter((spec) => spec.active())
      .sort((a, b) => (this.order.get(b.id) ?? 0) - (this.order.get(a.id) ?? 0))[0];
  }

  handleKey(event: KeyboardEvent): boolean {
    if (event.repeat) return false;
    const spec = this.match(event);
    if (!spec) return false;
    if (!spec.run()) return false;
    event.preventDefault();
    event.stopPropagation();
    return true;
  }
}

export const shortcutArbiter = new ShortcutArbiter();

export function installShortcutListener(
  target: Pick<Document, 'addEventListener' | 'removeEventListener'>,
  arbiter: ShortcutArbiter = shortcutArbiter,
): () => void {
  const listener = (event: Event) => {
    arbiter.handleKey(event as KeyboardEvent);
  };
  target.addEventListener('keydown', listener, { capture: true });
  return () => target.removeEventListener('keydown', listener, { capture: true });
}