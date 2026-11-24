/**
 * A user-configured shortcut string ↔ the arbiter's spec.
 *
 * The settings page has offered `ui.shortcut.commandPalette` / `toggleSidebar`
 * as free-text rows since the first build, and nothing read them: the palette
 * was bound to Mod+K in code (round-2 S13). A combo is written the way the
 * placeholder says (`Mod+K`, `CmdOrCtrl+Shift+Space`) and parsed here; an
 * unparsable value falls back to the default rather than disabling the shortcut,
 * because a shortcut that silently stops working is worse than one that is
 * still where it always was.
 */
export interface ShortcutCombo {
  mod: boolean;
  shift: boolean;
  alt: boolean;
  key: string;
}

const NAMED_KEYS: Record<string, string> = {
  space: ' ',
  enter: 'Enter',
  escape: 'Escape',
  esc: 'Escape',
  tab: 'Tab',
  backspace: 'Backspace',
  delete: 'Delete',
  arrowup: 'ArrowUp',
  arrowdown: 'ArrowDown',
  arrowleft: 'ArrowLeft',
  arrowright: 'ArrowRight',
  comma: ',',
  period: '.',
  slash: '/',
};

/** `Mod+K` / `CmdOrCtrl+Shift+Space` / `Ctrl+Alt+P`. Returns null when unusable. */
export function parseShortcut(text: string): ShortcutCombo | null {
  const parts = text
    .split('+')
    .map((part) => part.trim())
    .filter(Boolean);
  if (parts.length === 0) return null;

  const combo: ShortcutCombo = { mod: false, shift: false, alt: false, key: '' };
  for (const part of parts) {
    const lower = part.toLowerCase();
    if (lower === 'mod' || lower === 'cmdorctrl' || lower === 'ctrl' || lower === 'cmd' || lower === 'meta') {
      combo.mod = true;
      continue;
    }
    if (lower === 'shift') {
      combo.shift = true;
      continue;
    }
    if (lower === 'alt' || lower === 'option') {
      combo.alt = true;
      continue;
    }
    if (combo.key !== '') return null; // two non-modifier keys
    combo.key = NAMED_KEYS[lower] ?? (part.length === 1 ? part.toLowerCase() : '');
  }
  if (combo.key === '') return null;
  // A bare letter is not a shortcut: binding `K` makes the palette open while
  // the user types in the composer (round-3 wire audit). A modifier is required.
  if (!combo.mod && !combo.shift && !combo.alt) return null;
  return combo;
}

/** The human form of a combo, for the settings help line. */
export function formatShortcut(combo: ShortcutCombo): string {
  const parts: string[] = [];
  if (combo.mod) parts.push('Mod');
  if (combo.shift) parts.push('Shift');
  if (combo.alt) parts.push('Alt');
  parts.push(combo.key === ' ' ? 'Space' : combo.key.toUpperCase());
  return parts.join('+');
}

/** The settings row → a combo, falling back to `fallback` when unusable. */
export function comboFor(
  stored: string | undefined,
  fallback: string,
): ShortcutCombo {
  const parsed = stored ? parseShortcut(stored) : null;
  return parsed ?? parseShortcut(fallback) ?? { mod: true, shift: false, alt: false, key: 'k' };
}
