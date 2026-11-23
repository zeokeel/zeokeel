import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { SETTING_KEYS, SETTING_KEY_LIST } from '@/design/settings-keys';

/**
 * The typo gate for settings keys.
 *
 * `settings.set` validates only the *shape* of a key, so a hand-written typo is
 * accepted by the gateway and read by nobody — the user changes a setting and
 * nothing happens, silently. The backend's constants are the only truth, so the
 * generated module is the only place a key literal may appear.
 */

const here = dirname(fileURLToPath(import.meta.url));
const sourceRoot = resolve(here, '..');
const generated = resolve(here, '../design/settings-keys.ts');

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...sourceFiles(full));
      continue;
    }
    if (!/\.(ts|tsx)$/.test(entry)) continue;
    if (/\.test\.(ts|tsx)$/.test(entry)) continue;
    out.push(full);
  }
  return out;
}

describe('settings keys', () => {
  it('generates a non-trivial, unique key list', () => {
    expect(SETTING_KEY_LIST.length).toBeGreaterThan(20);
    expect(new Set(SETTING_KEY_LIST).size).toBe(SETTING_KEY_LIST.length);
    // Spot checks against the backend's own constants.
    expect(SETTING_KEYS.agentPermission).toBe('agent.permission');
    expect(SETTING_KEYS.themeMode).toBe('ui.theme.mode');
    expect(SETTING_KEYS.chatFontSize).toBe('ui.chatFontSize');
  });

  it('allows no hand-written settings key literal in src/', () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(sourceRoot)) {
      if (file === generated) continue;
      const text = readFileSync(file, 'utf8');
      for (const key of SETTING_KEY_LIST) {
        if (text.includes(`'${key}'`) || text.includes(`"${key}"`)) {
          offenders.push(`${file.replace(`${sourceRoot}/`, '')}: ${key}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});