/* eslint-disable no-restricted-syntax -- This module is colour *math*: it needs
   the concrete surface values the tokens resolve to, in order to measure and
   correct syntax palettes that arrive as inline styles. `syntax-theme.test.ts`
   reads tokens.css and fails if these two drift from `--zk-bg-strong`. */
import { oneDark, oneLight } from 'react-syntax-highlighter/dist/esm/styles/prism';

/**
 * The two code-block themes, corrected for the surface they are drawn on.
 *
 * `react-syntax-highlighter` writes token colours as **inline styles**, so no CSS
 * rule can fix them (two audits tried). Its palettes are also designed for a
 * white page: on this product's surfaces the number token measured 3.72:1 and,
 * after swapping to another stock light theme, 2.50:1. Instead of hunting for a
 * theme that happens to fit, every colour is checked against the surface it will
 * be drawn on and darkened (or lightened) until it passes AA.
 */
export const CODE_SURFACE_LIGHT = '#f2f4f7';
export const CODE_SURFACE_DARK = '#272736';
const MIN_CONTRAST = 4.5;

type Rgb = { r: number; g: number; b: number };

function parseColor(value: string): Rgb | null {
  const text = value.trim().toLowerCase();
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/.exec(text);
  if (hex) {
    const raw = hex[1].length === 3 ? hex[1].replace(/./g, (c) => c + c) : hex[1];
    return {
      r: parseInt(raw.slice(0, 2), 16),
      g: parseInt(raw.slice(2, 4), 16),
      b: parseInt(raw.slice(4, 6), 16),
    };
  }
  const rgb = /^rgba?\(([^)]+)\)$/.exec(text);
  if (rgb) {
    const [r, g, b] = rgb[1].split(',').map((part) => parseFloat(part));
    if ([r, g, b].every((part) => Number.isFinite(part))) return { r, g, b };
  }
  const hsl = /^hsla?\(([^)]+)\)$/.exec(text);
  if (hsl) {
    const [h, s, l] = hsl[1].split(',').map((part) => parseFloat(part));
    if ([h, s, l].every((part) => Number.isFinite(part))) return hslToRgb(h, s / 100, l / 100);
  }
  return null;
}

function hslToRgb(h: number, s: number, l: number): Rgb {
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const hp = (((h % 360) + 360) % 360) / 60;
  const x = c * (1 - Math.abs((hp % 2) - 1));
  const [r1, g1, b1] =
    hp < 1
      ? [c, x, 0]
      : hp < 2
        ? [x, c, 0]
        : hp < 3
          ? [0, c, x]
          : hp < 4
            ? [0, x, c]
            : hp < 5
              ? [x, 0, c]
              : [c, 0, x];
  const m = l - c / 2;
  return { r: (r1 + m) * 255, g: (g1 + m) * 255, b: (b1 + m) * 255 };
}

function relativeLuminance({ r, g, b }: Rgb): number {
  const channel = (value: number) => {
    const scaled = value / 255;
    return scaled <= 0.03928 ? scaled / 12.92 : ((scaled + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function contrastRatio(foreground: string, background: string): number {
  const front = parseColor(foreground);
  const back = parseColor(background);
  if (!front || !back) return 0;
  const light = Math.max(relativeLuminance(front), relativeLuminance(back));
  const dark = Math.min(relativeLuminance(front), relativeLuminance(back));
  return (light + 0.05) / (dark + 0.05);
}

/** Push a colour away from the surface until it reaches `min` (worst case: black/white). */
export function ensureContrast(color: string, surface: string, min = MIN_CONTRAST): string {
  if (contrastRatio(color, surface) >= min) return color;
  const start = parseColor(color);
  const back = parseColor(surface);
  if (!start || !back) return color;
  const towardsDark = relativeLuminance(back) > 0.5;
  const target = towardsDark ? { r: 0, g: 0, b: 0 } : { r: 255, g: 255, b: 255 };
  // Binary search along the straight line to the extreme: keeps the hue.
  let low = 0;
  let high = 1;
  let best = towardsDark ? '#000000' : '#ffffff';
  for (let step = 0; step < 24; step += 1) {
    const mid = (low + high) / 2;
    const candidate = {
      r: start.r + (target.r - start.r) * mid,
      g: start.g + (target.g - start.g) * mid,
      b: start.b + (target.b - start.b) * mid,
    };
    const hex = `#${[candidate.r, candidate.g, candidate.b]
      .map((value) => Math.round(value).toString(16).padStart(2, '0'))
      .join('')}`;
    if (contrastRatio(hex, surface) >= min) {
      best = hex;
      high = mid;
    } else {
      low = mid;
    }
  }
  return best;
}

/** Every token colour in `theme`, corrected for `surface`. */
export function correctedTheme(
  theme: Record<string, unknown>,
  surface: string,
): Record<string, unknown> {
  const output: Record<string, unknown> = {};
  for (const [selector, value] of Object.entries(theme)) {
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      const entry = { ...(value as Record<string, unknown>) };
      if (typeof entry.color === 'string') entry.color = ensureContrast(entry.color, surface);
      if (typeof entry.backgroundColor === 'string') {
        entry.backgroundColor = ensureContrast(entry.backgroundColor, surface, 1);
      }
      output[selector] = entry;
      continue;
    }
    output[selector] = value;
  }
  return output;
}

let lightCache: Record<string, unknown> | null = null;
let darkCache: Record<string, unknown> | null = null;

export function lightCodeTheme(): Record<string, unknown> {
  lightCache ??= correctedTheme(oneLight as Record<string, unknown>, CODE_SURFACE_LIGHT);
  return lightCache;
}

export function darkCodeTheme(): Record<string, unknown> {
  darkCache ??= correctedTheme(oneDark as Record<string, unknown>, CODE_SURFACE_DARK);
  return darkCache;
}
