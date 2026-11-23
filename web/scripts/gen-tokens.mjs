#!/usr/bin/env node
// Generate web/src/design/tokens.css and web/src/design/antd-theme.ts from the
// Go design sources. Rule T1 of docs/WEB-CLIENT.md §9.1: the token file is a
// build product, never hand-edited, and the Go side stays the single source.
//
//   internal/brand/brand.go        metrics + font stacks
//   internal/brand/palette.go      the 29 palette roles (light + dark)
//   internal/design/tokens.go      semantic token names + the type scale
//   internal/design/components.go  spacing / radius / control heights
//
// The parser is deliberately strict: every value this file promises must be
// found in the Go source, otherwise the script exits non-zero instead of
// emitting a token file that has silently drifted.
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '../..');
const read = (rel) => readFileSync(resolve(repo, rel), 'utf8');
const fail = (msg) => {
  console.error(`gen-tokens: ${msg}`);
  process.exit(1);
};

const brand = read('internal/brand/brand.go');
const palette = read('internal/brand/palette.go');
const tokens = read('internal/design/tokens.go');
const components = read('internal/design/components.go');

// ---------------------------------------------------------------- brand.go --
const num = (src, re, what) => {
  const m = src.match(re);
  if (!m) fail(`could not read ${what}`);
  return Number(m[1]);
};
const str = (src, re, what) => {
  const m = src.match(re);
  if (!m) fail(`could not read ${what}`);
  return m[1];
};

const radiusBase = num(brand, /Radius\s+float32\s*=\s*([\d.]+)/, 'brand.Radius');
const spacingBase = num(brand, /Spacing\s+float32\s*=\s*([\d.]+)/, 'brand.Spacing');
const fontSizeBase = num(brand, /FontSize\s+float32\s*=\s*([\d.]+)/, 'brand.FontSize');
const systemFont = str(brand, /const SystemFont\s*=\s*`([^`]+)`/, 'brand.SystemFont');
const monoFont = str(brand, /const MonoFont\s*=\s*`([^`]+)`/, 'brand.MonoFont');

// ------------------------------------------------------------- palette.go --
const hexConsts = new Map();
for (const m of palette.matchAll(/^\tHex(\w+)\s*=\s*"(#[0-9A-Fa-f]{6})"/gm)) {
  hexConsts.set(`Hex${m[1]}`, m[2]);
}

const paletteFn = (name) => {
  const start = palette.indexOf(`func ${name}() Palette {`);
  if (start < 0) fail(`could not find ${name}`);
  const end = palette.indexOf('\n}\n', start);
  if (end < 0) fail(`could not find the end of ${name}`);
  return palette.slice(start, end);
};

const parseColor = (raw, field) => {
  const hexRef = raw.match(/^ui\.Hex\(Hex(\w+)\)/);
  if (hexRef) {
    const value = hexConsts.get(`Hex${hexRef[1]}`);
    if (!value) fail(`unknown hex constant Hex${hexRef[1]} in ${field}`);
    return value.toLowerCase();
  }
  const hexLit = raw.match(/^ui\.Hex\("(#[0-9A-Fa-f]{6})"\)/);
  if (hexLit) return hexLit[1].toLowerCase();
  const rgba = raw.match(/^ui\.RGBA\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*([\d.]+)\s*\)/);
  if (rgba) {
    const [, r, g, b, a] = rgba;
    return `rgba(${r}, ${g}, ${b}, ${Number(a)})`;
  }
  fail(`unparsed colour expression for ${field}: ${raw}`);
};

const parsePalette = (fn) => {
  const body = paletteFn(fn);
  const roles = new Map();
  for (const m of body.matchAll(/^\t\t(\w+):\s+(.+?),\s*(?:\/\/.*)?$/gm)) {
    roles.set(m[1], parseColor(m[2].trim(), m[1]));
  }
  if (roles.size < 20) fail(`${fn} parsed only ${roles.size} roles`);
  return roles;
};

const lightPalette = parsePalette('LightPalette');
const darkPalette = parsePalette('DarkPalette');

// -------------------------------------------------------------- tokens.go --
const mappingStart = tokens.indexOf('func fromPalette(');
if (mappingStart < 0) fail('could not find fromPalette');
const mappingEnd = tokens.indexOf('\n}\n', mappingStart);
const mappingBody = tokens.slice(mappingStart, mappingEnd);

const semantic = [];
for (const m of mappingBody.matchAll(/^\t\t(\w+):\s+p\.(\w+),\s*$/gm)) {
  semantic.push({ name: m[1], role: m[2] });
}
if (semantic.length < 20) fail(`fromPalette mapped only ${semantic.length} tokens`);

// TypeMetrics: case TypeXL: return 24, 32, 600 — plus the default 11/14/500.
const typeScale = [];
for (const m of tokens.matchAll(/case (Type\w+):\s*\n\s*return ([\d.]+), ([\d.]+), (\d+)/g)) {
  typeScale.push({ name: m[1], size: Number(m[2]), line: Number(m[3]), weight: Number(m[4]) });
}
const fallbackType = tokens.match(/default:\s*\n\s*return ([\d.]+), ([\d.]+), (\d+)/);
if (!fallbackType) fail('could not read the TypeMetrics default case');
typeScale.push({
  name: 'TypeXXXS',
  size: Number(fallbackType[1]),
  line: Number(fallbackType[2]),
  weight: Number(fallbackType[3]),
});
if (typeScale.length !== 8) fail(`expected 8 type roles, got ${typeScale.length}`);

// ---------------------------------------------------------- components.go --
const spacingSteps = new Map();
for (const m of components.matchAll(/^\t(Space\d+)\s+float32\s*=\s*(.+)$/gm)) {
  const expr = m[2].trim();
  if (expr === 'brand.Spacing') spacingSteps.set(m[1], spacingBase);
  else if (expr === '0') spacingSteps.set(m[1], 0);
  else {
    const scaled = expr.match(/^Space1\s*\*\s*(\d+)$/);
    if (!scaled) fail(`unparsed spacing expression ${m[1]} = ${expr}`);
    spacingSteps.set(m[1], spacingBase * Number(scaled[1]));
  }
}
if (spacingSteps.size === 0) fail('could not read the spacing scale');

const radiusSteps = new Map();
for (const m of components.matchAll(/^\t(Radius\w+)\s+float32\s*=\s*(.+)$/gm)) {
  const expr = m[2].trim();
  const base = expr.match(/^brand\.Radius$/);
  const literal = expr.match(/^([\d.]+)$/);
  if (base) radiusSteps.set(m[1], radiusBase);
  else if (literal) radiusSteps.set(m[1], Number(literal[1]));
  else fail(`unparsed radius expression ${m[1]} = ${expr}`);
}

// The shell measures this milestone uses; the other layout constants in
// components.go (palette/settings geometry) belong to screens not built yet.
const HEIGHT_ALLOWLIST = new Set([
  'ControlCompactHeight',
  'RowCompactHeight',
  'ControlHeight',
  'PageActionHeight',
  'RowDoubleHeight',
  'BandHeight',
  'BrandRowHeight',
]);
const controlHeights = new Map();
for (const m of components.matchAll(/^\t(\w*Height)\s+float32\s*=\s*([\d.]+)$/gm)) {
  if (HEIGHT_ALLOWLIST.has(m[1])) controlHeights.set(m[1], Number(m[2]));
}
if (!controlHeights.has('ControlHeight')) fail('could not read the control-height scale');

// ---------------------------------------------------------------- emitter --
const kebab = (s) =>
  s
    .replace(/([a-z])([A-Z])/g, '$1-$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1-$2')
    .replace(/([a-z])(\d)/g, '$1-$2')
    .toLowerCase();

// Palette roles that have no semantic token of their own but that the shell
// still needs (the focus ring and scrim are non-text indicators, §9.4).
const EXTRA_TOKENS = [
  ['focus', 'Focus'],
  ['scrim', 'Scrim'],
  ['selection', 'Selection'],
  ['scrollbar', 'Scrollbar'],
  ['border-subtle', 'BorderSubtle'],
];

const colorDecls = (roles) => {
  const semanticLines = semantic.map(({ name, role }) => {
    const value = roles.get(role);
    if (!value) fail(`palette role ${role} (token ${name}) is missing`);
    return `  --zk-${kebab(name)}: ${value};`;
  });
  const extraLines = EXTRA_TOKENS.map(([token, role]) => {
    const value = roles.get(role);
    if (!value) fail(`palette role ${role} (token --zk-${token}) is missing`);
    return `  --zk-${token}: ${value};`;
  });
  return [...semanticLines, ...extraLines].join('\n');
};

const metricDecls = [
  `  --zk-font-family: ${systemFont};`,
  `  --zk-font-mono: ${monoFont};`,
  `  --zk-font-size: ${fontSizeBase}px;`,
  ...typeScale.map(
    (t) =>
      `  --zk-${kebab(t.name)}: ${t.size}px; --zk-${kebab(t.name)}-line: ${t.line}px; ` +
      `--zk-${kebab(t.name)}-weight: ${t.weight};`,
  ),
  ...[...spacingSteps.entries()].map(([name, px]) => `  --zk-${kebab(name)}: ${px}px;`),
  ...[...radiusSteps.entries()].map(([name, px]) => `  --zk-${kebab(name)}: ${px}px;`),
  ...[...controlHeights.entries()].map(([name, px]) => `  --zk-${kebab(name)}: ${px}px;`),
].join('\n');

const css = `/* GENERATED by web/scripts/gen-tokens.mjs — do not edit.
 *
 * Source: internal/brand/{brand,palette}.go, internal/design/{tokens,components}.go
 * Regenerate: npm run gen:tokens   (docs/WEB-CLIENT.md §9.1 rule T1)
 *
 * This is the only file in web/src allowed to carry colour literals (§9.2).
 * Both appearances are emitted; the dark block follows the system preference
 * because this milestone ships no appearance setting yet (that is R15).
 */

:root {
${colorDecls(lightPalette)}
${metricDecls}
}

@media (prefers-color-scheme: dark) {
  :root {
${colorDecls(darkPalette).replace(/^/gm, '  ').replace(/^ {2}/, '')}
  }
}
`;

const ts = `/* GENERATED by web/scripts/gen-tokens.mjs — do not edit.
 *
 * Source: internal/brand/palette.go (accent), internal/brand/brand.go (fonts).
 * Regenerate: npm run gen:tokens
 *
 * Consumed by src/design/theme.ts, which passes only \`algorithm\`,
 * \`colorPrimary\` and \`fontFamily\` into antd's ConfigProvider
 * (docs/plans/antd-x-component-map.md §5). Everything else stays antd's.
 */

export type Appearance = 'light' | 'dark';

export const accent: Record<Appearance, string> = {
  light: '${lightPalette.get('Accent')}',
  dark: '${darkPalette.get('Accent')}',
};

export const fontFamily = \`${systemFont}\`;

export const monoFontFamily = \`${monoFont}\`;
`;

writeFileSync(resolve(repo, 'web/src/design/tokens.css'), css);
writeFileSync(resolve(repo, 'web/src/design/antd-theme.ts'), ts);
console.log(
  `gen-tokens: wrote tokens.css (${semantic.length} colour tokens, ${typeScale.length} type roles, ` +
    `${spacingSteps.size} space steps, ${radiusSteps.size} radii, ${controlHeights.size} control heights) ` +
    `and antd-theme.ts`,
);