import { useEffect, useState } from 'react';
import type { Appearance } from './antd-theme';
import { SETTING_KEYS } from './settings-keys';
import { subscribeSettings } from '@/api/gateway';

/**
 * The effective appearance: the user's choice when there is one, the OS
 * preference otherwise (§9.1: the CSS token block and antd's algorithm must
 * agree, so both read this one value).
 *
 * The settings screen has offered 主题 (system / light / dark) and a legacy
 * `ui.theme.dark` switch since the shell was built, and nothing consumed either
 * — flipping them changed nothing, which is the "looks present, does nothing"
 * shape the design rules forbid (AUDIT-2026-11-24 C).
 */
export function useSystemAppearance(): Appearance {
  const [osAppearance, setOSAppearance] = useState<Appearance>(() => currentAppearance());
  const [choice, setChoice] = useState<Appearance | null>(null);

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => setOSAppearance(query.matches ? 'dark' : 'light');
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);

  useEffect(() => {
    const apply = (rows: Record<string, string>) => setChoice(resolveChoice(rows));
    const unsubscribe = subscribeSettings(apply);
    return unsubscribe;
  }, []);

  return choice ?? osAppearance;
}

/** resolveChoice maps the stored rows to an explicit appearance, if any. */
export function resolveChoice(rows: Record<string, string>): Appearance | null {
  const mode = (rows[SETTING_KEYS.themeMode] ?? '').trim().toLowerCase();
  if (mode === 'dark') return 'dark';
  if (mode === 'light') return 'light';
  if (mode === 'system') return null;
  // The legacy boolean only counts when no mode is stored.
  if (mode === '') {
    const dark = (rows[SETTING_KEYS.uiThemeDark] ?? '').trim().toLowerCase();
    if (dark === 'true' || dark === '1' || dark === 'yes') return 'dark';
    if (dark === 'false' || dark === '0' || dark === 'no') return 'light';
  }
  return null;
}

/**
 * A boolean media query, live.
 *
 * The shell uses it for the one structural breakpoint (narrow → the sidebar
 * becomes a drawer). jsdom has no matchMedia, so the fallback is "false" and
 * tests keep the desktop layout they were written against.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia(query).matches
      : false,
  );
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
    const media = window.matchMedia(query);
    const onChange = () => setMatches(media.matches);
    onChange();
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, [query]);
  return matches;
}

function currentAppearance(): Appearance {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'light';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}
