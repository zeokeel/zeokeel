import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  CODE_SURFACE_DARK,
  CODE_SURFACE_LIGHT,
  contrastRatio,
  darkCodeTheme,
  lightCodeTheme,
} from './syntax-theme';

/**
 * The renderer writes token colours as inline styles, so a wrong palette cannot be
 * fixed with CSS — two audits shipped a theme whose tokens measured 2.5–3.7:1.
 * These assertions are the measurement that was missing.
 */
describe('code themes', () => {
  it('measures against the surfaces the tokens actually define', () => {
    // The two surfaces are literals (colour math needs concrete values); this is
    // the link that keeps them from drifting away from tokens.css.
    const here = dirname(fileURLToPath(import.meta.url));
    const css = readFileSync(resolve(here, '../design/tokens.css'), 'utf8');
    const lightBlock = css.slice(css.indexOf(":root,"), css.indexOf(":root[data-theme='dark']"));
    const darkBlock = css.slice(css.indexOf(":root[data-theme='dark']"));
    const valueIn = (block: string) => /--zk-bg-strong:\s*([^;]+);/.exec(block)?.[1].trim();
    expect(valueIn(lightBlock)?.toLowerCase()).toBe(CODE_SURFACE_LIGHT);
    expect(valueIn(darkBlock)?.toLowerCase()).toBe(CODE_SURFACE_DARK);
  });

  // `::selection` entries carry `inherit`, and inline `background-color`s are
  // not text: only a parsable token colour is measured.
  const tokens = (theme: Record<string, unknown>) =>
    Object.entries(theme).filter(
      (entry): entry is [string, { color: string }] =>
        entry[1] !== null &&
        typeof entry[1] === 'object' &&
        !entry[0].includes('::selection') &&
        typeof (entry[1] as { color?: unknown }).color === 'string' &&
        /^(#[0-9a-f]{3,6}|rgb|hsl)/i.test((entry[1] as { color: string }).color),
    );

  it('every light token reaches 4.5:1 on the light surface', () => {
    const bad = tokens(lightCodeTheme())
      .map(([selector, entry]) => ({
        selector,
        ratio: contrastRatio(entry.color, CODE_SURFACE_LIGHT),
      }))
      .filter((row) => row.ratio < 4.5);
    expect(bad.map((row) => `${row.selector} ${row.ratio.toFixed(2)}`)).toEqual([]);
  });

  it('every dark token reaches 4.5:1 on the dark surface', () => {
    const bad = tokens(darkCodeTheme())
      .map(([selector, entry]) => ({
        selector,
        ratio: contrastRatio(entry.color, CODE_SURFACE_DARK),
      }))
      .filter((row) => row.ratio < 4.5);
    expect(bad.map((row) => `${row.selector} ${row.ratio.toFixed(2)}`)).toEqual([]);
  });

  it('keeps a token recognisable instead of turning everything black', () => {
    // At least one third of the palette must keep a non-grey hue: a correction
    // that flattened every colour would pass contrast and lose the highlighting.
    const colourful = tokens(lightCodeTheme()).filter(([, entry]) => {
      const match = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/.exec(entry.color);
      if (!match) return false;
      const [r, g, b] = match.slice(1).map((part) => parseInt(part, 16));
      return Math.max(r, g, b) - Math.min(r, g, b) > 40;
    });
    expect(colourful.length).toBeGreaterThan(tokens(lightCodeTheme()).length / 3);
  });
});
