import { describe, expect, it } from 'vitest';
import { resolveChoice } from './use-system-appearance';
import { SETTING_KEYS } from './settings-keys';

/**
 * The appearance setting (AUDIT-2026-11-24 C): 主题 offered system/light/dark and
 * a legacy `ui.theme.dark` switch, and nothing consumed either — flipping them
 * changed nothing. resolveChoice is the single mapping the hook applies.
 */
describe('resolveChoice', () => {
  it('maps the explicit theme mode', () => {
    expect(resolveChoice({ [SETTING_KEYS.themeMode]: 'dark' })).toBe('dark');
    expect(resolveChoice({ [SETTING_KEYS.themeMode]: 'light' })).toBe('light');
    expect(resolveChoice({ [SETTING_KEYS.themeMode]: ' DARK ' })).toBe('dark');
  });

  it('follows the OS when the mode is system or unset', () => {
    expect(resolveChoice({ [SETTING_KEYS.themeMode]: 'system' })).toBeNull();
    expect(resolveChoice({})).toBeNull();
  });

  it('honours the legacy boolean only when no mode is stored', () => {
    expect(resolveChoice({ [SETTING_KEYS.uiThemeDark]: 'true' })).toBe('dark');
    expect(resolveChoice({ [SETTING_KEYS.uiThemeDark]: 'no' })).toBe('light');
    // An explicit mode wins over the legacy row.
    expect(
      resolveChoice({ [SETTING_KEYS.themeMode]: 'light', [SETTING_KEYS.uiThemeDark]: 'true' }),
    ).toBe('light');
  });
});
