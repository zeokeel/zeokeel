import { useEffect, useState } from 'react';
import { subscribeSettings, type SettingsRows } from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';
import { comboFor, formatShortcut, type ShortcutCombo } from './shortcut-combo';

/**
 * The two configurable shortcuts, read live from the settings domain.
 *
 * `ui.shortcut.commandPalette` (default Mod+K) and `ui.shortcut.toggleSidebar`
 * (default Mod+B) are editable rows; before this the bindings were hard-coded and
 * an edit changed nothing (round-2 S13). An unparsable value falls back to the
 * default instead of unbinding the key.
 */
export interface ShortcutSettings {
  palette: ShortcutCombo;
  sidebar: ShortcutCombo;
}

export const DEFAULT_PALETTE_SHORTCUT = 'Mod+K';
export const DEFAULT_SIDEBAR_SHORTCUT = 'Mod+B';

/** Two rows bound to the same combo: the second one would register later and win,
 *  silently disabling the first (round-3 wire audit). Reported so the settings
 *  page can say so, and the sidebar falls back to its default. */
export interface ShortcutConflict {
  key: string;
}

export function readShortcutSettings(rows: SettingsRows): ShortcutSettings & {
  conflict: ShortcutConflict | null;
} {
  const palette = comboFor(rows[SETTING_KEYS.uiShortcutCommandPalette], DEFAULT_PALETTE_SHORTCUT);
  const sidebar = comboFor(rows[SETTING_KEYS.uiShortcutToggleSidebar], DEFAULT_SIDEBAR_SHORTCUT);
  const same =
    palette.mod === sidebar.mod && palette.shift === sidebar.shift && palette.alt === sidebar.alt && palette.key === sidebar.key;
  return {
    palette,
    sidebar: same ? (comboFor(undefined, DEFAULT_SIDEBAR_SHORTCUT)) : sidebar,
    conflict: same ? { key: formatShortcut(palette) } : null,
  };
}

export function useShortcutSettings(): ShortcutSettings & { conflict: ShortcutConflict | null } {
  const [rows, setRows] = useState<SettingsRows>({});
  useEffect(() => subscribeSettings(setRows), []);
  return readShortcutSettings(rows);
}
