import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
const dir = new URL('./comps/', import.meta.url);
const read = (f) => readFileSync(new URL(f, dir), 'utf8');
const readOpt = (f) => { try { return read(f); } catch { return ''; } };
const icons = read('icons.html');
const side = read('_ops-side.html');
const tokens = read('tokens.css');

// ops bodies: comps/ops-<name>.html with <!--active:X title:Y sub:Z-->
for (const file of readdirSync(dir).filter((f) => f.startsWith('ops-') && f.endsWith('.html'))) {
  const raw = read(file);
  const m = raw.match(/^<!--active:(\S+) title:(.+?) sub:(.*?)-->/);
  const active = m?.[1] ?? '';
  const body = raw.replace(/^<!--.*?-->\n?/, '');
  const navSide = side.replace(`__${active}`, 'nav--on').replace(/__[A-Z]+/g, '');
  const html = `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<style>${tokens}${readOpt(file.replace('.html', '.css'))}</style></head><body>${icons}
<div class="app">${navSide}${body}</div></body></html>`;
  const out = file.replace(/^ops-/, '').replace(/\.html$/, '.full.html');
  writeFileSync(new URL(out, dir), html);
  console.log('assembled', out, 'active=' + active);
}
