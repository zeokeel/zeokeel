import { useEffect, useState } from 'react';
import { subscribeSettings, type SettingsRows } from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';
import { comboFor, type ShortcutCombo } from './shortcut-combo';

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

export function readShortcutSettings(rows: SettingsRows): ShortcutSettings {
  return {
    palette: comboFor(rows[SETTING_KEYS.uiShortcutCommandPalette], DEFAULT_PALETTE_SHORTCUT),
    sidebar: comboFor(rows[SETTING_KEYS.uiShortcutToggleSidebar], DEFAULT_SIDEBAR_SHORTCUT),
  };
}

export function useShortcutSettings(): ShortcutSettings {
  const [rows, setRows] = useState<SettingsRows>({});
  useEffect(() => subscribeSettings(setRows), []);
  return readShortcutSettings(rows);
}
