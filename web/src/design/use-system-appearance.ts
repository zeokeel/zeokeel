import { useEffect, useState } from 'react';
import type { Appearance } from './antd-theme';

/**
 * Follow the OS appearance. This milestone ships no appearance setting (that is
 * R15/appearance), and docs/WEB-CLIENT.md §9.1 requires the CSS token block and
 * antd's algorithm to agree — so both read the same media query here.
 */
export function useSystemAppearance(): Appearance {
  const [appearance, setAppearance] = useState<Appearance>(() => currentAppearance());

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => setAppearance(query.matches ? 'dark' : 'light');
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);

  return appearance;
}

function currentAppearance(): Appearance {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'light';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}