import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react';
import { escArbiter, installEscListener, type EscArbiter, type EscLevel } from './esc-arbiter';
import {
  installShortcutListener,
  shortcutArbiter,
  type ShortcutArbiter,
} from './shortcut-arbiter';

/**
 * Installs the app's keyboard listeners exactly once:
 *
 *  - the single document-capture Escape listener (esc-arbiter.ts), which reads
 *    a declarative claim stack instead of DOM focus;
 *  - the single document-capture chord listener for the shortcuts the client
 *    owns (Mod+K, Mod+F, Mod+Enter).
 *
 * Mount at the root. A second provider would create the second listener both
 * contracts forbid (docs/WEB-CLIENT.md §4.3 rule 1).
 */
const EscArbiterContext = createContext<EscArbiter>(escArbiter);
const ShortcutArbiterContext = createContext<ShortcutArbiter>(shortcutArbiter);

export function KeyboardProvider({
  children,
  esc = escArbiter,
  shortcuts = shortcutArbiter,
}: {
  children: ReactNode;
  esc?: EscArbiter;
  shortcuts?: ShortcutArbiter;
}) {
  useEffect(() => installEscListener(document, esc), [esc]);
  useEffect(() => installShortcutListener(document, shortcuts), [shortcuts]);
  return (
    <EscArbiterContext.Provider value={esc}>
      <ShortcutArbiterContext.Provider value={shortcuts}>
        {children}
      </ShortcutArbiterContext.Provider>
    </EscArbiterContext.Provider>
  );
}

export function useEscArbiter(): EscArbiter {
  return useContext(EscArbiterContext);
}

export function useShortcutArbiter(): ShortcutArbiter {
  return useContext(ShortcutArbiterContext);
}

/**
 * Declare "while I am active, Esc means this". Pushed on mount, popped on
 * unmount; `active` and `onEscape` are read live, so a changing draft does not
 * re-register the claim on every keystroke.
 */
export function useEscClaim(claim: {
  id: string;
  level: EscLevel;
  active: boolean;
  onEscape: () => boolean;
}): void {
  const arbiter = useEscArbiter();
  const { id, level } = claim;
  const activeRef = useRef(claim.active);
  const handlerRef = useRef(claim.onEscape);

  useEffect(() => {
    // Synced after commit, which is before any user keydown can read them;
    // writing refs during render is what the compiler lint forbids.
    activeRef.current = claim.active;
    handlerRef.current = claim.onEscape;
  });

  useEffect(
    () =>
      arbiter.push({
        id,
        level,
        active: () => activeRef.current,
        onEscape: () => handlerRef.current(),
      }),
    [arbiter, id, level],
  );
}

/** Declare a global chord while mounted (see shortcut-arbiter.ts for the rules). */
export function useShortcut(spec: {
  id: string;
  mod?: boolean;
  shift?: boolean;
  alt?: boolean;
  key: string;
  active: boolean;
  run: () => boolean;
}): void {
  const arbiter = useShortcutArbiter();
  const { id, mod, shift, alt, key } = spec;
  const activeRef = useRef(spec.active);
  const runRef = useRef(spec.run);

  useEffect(() => {
    activeRef.current = spec.active;
    runRef.current = spec.run;
  });

  useEffect(
    () =>
      arbiter.push({
        id,
        mod,
        shift,
        alt,
        key,
        active: () => activeRef.current,
        run: () => runRef.current(),
      }),
    [arbiter, id, mod, shift, alt, key],
  );
}