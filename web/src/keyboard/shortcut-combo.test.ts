import { describe, expect, it } from 'vitest';
import { formatShortcut, parseShortcut } from './shortcut-combo';

describe('parseShortcut', () => {
  it('reads the two documented forms', () => {
    expect(parseShortcut('Mod+K')).toEqual({ mod: true, shift: false, alt: false, key: 'k' });
    expect(parseShortcut('CmdOrCtrl+Shift+Space')).toEqual({
      mod: true,
      shift: true,
      alt: false,
      key: ' ',
    });
  });

  it('accepts aliases and named keys', () => {
    expect(parseShortcut('Ctrl+Alt+P')).toEqual({ mod: true, shift: false, alt: true, key: 'p' });
    expect(parseShortcut('Mod+Enter')?.key).toBe('Enter');
  });

  it('refuses what it cannot bind', () => {
    expect(parseShortcut('')).toBeNull();
    expect(parseShortcut('K')).toEqual({ mod: false, shift: false, alt: false, key: 'k' });
    expect(parseShortcut('Mod+K+J')).toBeNull();
    expect(parseShortcut('Mod+')).toBeNull();
    expect(parseShortcut('Mod+SomeLongName')).toBeNull();
  });

  it('round-trips through its own formatter', () => {
    const combo = parseShortcut('Mod+Shift+P');
    expect(combo).not.toBeNull();
    expect(parseShortcut(formatShortcut(combo!))).toEqual(combo);
  });
});
