import { chromium } from 'playwright-core';
import { readdirSync } from 'node:fs';
const dir = new URL('./comps/', import.meta.url);
const outDir = new URL('./shots/', import.meta.url);
const files = readdirSync(dir).filter((f) => f.endsWith('.full.html'));

const browser = await chromium.launch({
  executablePath: process.env.PW_CHROME,
  args: ['--no-sandbox', '--force-color-profile=srgb', '--font-render-hinting=none'],
});
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
});
const page = await ctx.newPage();
for (const f of files) {
  const url = new URL(f, dir).href;
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(350);
  const name = f.replace('.full.html', '');
  const out = new URL(`${name}.png`, outDir).pathname;
  await page.screenshot({ path: out });
  console.log('shot', out);
}
await browser.close();
