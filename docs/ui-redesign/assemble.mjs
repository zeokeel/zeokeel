import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
const dir = new URL('./comps/', import.meta.url);
const read = (f) => readFileSync(new URL(f, dir), 'utf8');
const icons = read('icons.html');
const shell = read('_shell.html');
const tokens = read('tokens.css');

// each body: comps/body-<name>.html with first line <!--active:X title:Y sub:Z-->
for (const file of readdirSync(dir).filter((f) => f.startsWith('body-'))) {
  const raw = read(file);
  const m = raw.match(/^<!--active:(\S+) title:(.+?) sub:(.*?)-->/);
  const active = m?.[1] ?? '';
  const body = raw.replace(/^<!--.*?-->\n?/, '');
  const navShell = shell.replace(`__${active}`, 'nav--on').replace(/__[A-Z]+/g, '');
  const html = `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<style>${tokens}</style></head><body>${icons}
<div class="app">${navShell}${body}</div></body></html>`;
  const out = file.replace(/^body-/, '').replace(/\.html$/, '.full.html');
  writeFileSync(new URL(out, dir), html);
  console.log('assembled', out, 'active=' + active);
}
