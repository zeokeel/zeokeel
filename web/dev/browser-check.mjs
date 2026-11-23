#!/usr/bin/env node
// Real-browser check with headless Chromium — **mock only**.
//
// It uses mock fixtures on purpose (the 600-message session is found by title)
// to measure the virtualised transcript. For the real gateway use
// `dev/e2e-smoke.mjs`, which depends on UI text alone and no fixture data.
//
// Three things unit tests cannot see:
//  1. does the shell actually paint (no blank screen, no layout collapse);
//  2. how janky is scrolling a virtualised transcript (rAF interval p95);
//  3. does a long (600-message) conversation render a window instead of going
//     blank or jumping to the top.
//
// Run with the dev server and the mock up:
//   npm run mock            (127.0.0.1:8788)
//   npm run dev             (127.0.0.1:5178, proxies /api and /ws)
//   node dev/browser-check.mjs
//
// Screenshots land in .cache/ui-review/. Chromium is provided by the harness:
//   PLAYWRIGHT_BROWSERS_PATH=/data/mygo/.cache/ms-playwright
import { mkdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require('/data/mygo/.cache/pw/node_modules/playwright');

const BASE = process.env.E2E_BASE ?? 'http://127.0.0.1:5178';
const OUT = process.env.E2E_OUT ?? '/data/mygo/.cache/ui-review';

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function main() {
  mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
  const report = { base: BASE, steps: {}, consoleErrors: [] };

  page.on('console', (message) => {
    if (message.type() === 'error') report.consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => report.consoleErrors.push(String(error)));
  page.on('response', (response) => {
    if (response.status() >= 400) {
      report.consoleErrors.push(`HTTP ${response.status()} ${response.url()}`);
    }
  });

  // ---------------------------------------------------------------- now --
  await page.goto(`${BASE}/`, { waitUntil: 'networkidle' });
  await page.waitForSelector('.zk-sidebar', { timeout: 15_000 });
  // The session list must arrive over WS, not stay at "正在加载…".
  await page.waitForFunction(
    () => !document.querySelector('.zk-sidebar__empty')?.textContent?.includes('正在加载'),
    { timeout: 15_000 },
  );
  const rows = await page.$$eval('.zk-sidebar .ant-conversations-item, .zk-sidebar li', (nodes) =>
    nodes.map((node) => node.textContent?.trim() ?? ''),
  );
  report.steps.now = { sessionRows: rows.length, first: rows[0] ?? null };
  await page.screenshot({ path: `${OUT}/now.png`, fullPage: false });

  // -------------------------------------------------------------- inbox --
  await page.goto(`${BASE}/inbox`, { waitUntil: 'networkidle' });
  await page.waitForSelector('.zk-inbox', { timeout: 15_000 });
  // The row comes from `approvals.snapshot` over WS; give the domain a beat.
  await page
    .waitForFunction(() => document.querySelectorAll('.zk-inbox__row').length > 0, {
      timeout: 15_000,
    })
    .catch(() => undefined);
  report.steps.inbox = await page.evaluate(() => {
    const buttons = [...document.querySelectorAll('.zk-inbox button')];
    return {
      items: document.querySelectorAll('.zk-inbox__row').length,
      // Rule 9/A9: no filled primary anywhere in the approval area.
      filledActions: buttons.filter((button) =>
        button.className.includes('ant-btn-primary'),
      ).length,
    };
  });
  await page.screenshot({ path: `${OUT}/inbox.png` });

  // --------------------------------------------- long transcript + jank --
  const longId = await page.evaluate(async () => {
    const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`);
    const call = (id, method, params = {}) =>
      socket.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
    return await new Promise((resolve) => {
      let nextId = 1;
      socket.onopen = () => call(nextId++, 'initialize', { protocolVersion: 1 });
      socket.onmessage = (event) => {
        for (const line of String(event.data).split('\n')) {
          if (!line.trim()) continue;
          const message = JSON.parse(line);
          if (message.result?.epoch) {
            call(nextId++, 'ready');
            call(nextId++, 'session.list.snapshot');
          }
          if (message.result?.sessions) {
            const long = message.result.sessions.find((item) => item.title?.includes('长会话'));
            socket.close();
            resolve(long?.id ?? null);
          }
        }
      };
      setTimeout(() => resolve(null), 5000);
    });
  });
  report.steps.longSessionId = longId;

  if (longId) {
    await page.goto(`${BASE}/c/${longId}`, { waitUntil: 'networkidle' });
    await page.waitForSelector('.zk-transcript__row', { timeout: 15_000 });
    const rendered = await page.$$eval('.zk-transcript__row', (nodes) => nodes.length);
    const scrollHeight = await page.$eval('.zk-transcript__scroll', (node) => node.scrollHeight);
    report.steps.longSession = { renderedRows: rendered, scrollHeight };
    await page.screenshot({ path: `${OUT}/thread-long.png` });

    // Jank: scroll continuously for 3s and record rAF intervals.
    const jank = await page.evaluate(async () => {
      const scroller = document.querySelector('.zk-transcript__scroll');
      const samples = [];
      let last = performance.now();
      let running = true;
      const tick = (now) => {
        samples.push(now - last);
        last = now;
        if (running) requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
      const start = performance.now();
      while (performance.now() - start < 3000) {
        scroller.scrollTop += 40;
        await new Promise((resolve) => requestAnimationFrame(resolve));
      }
      running = false;
      await new Promise((resolve) => setTimeout(resolve, 50));
      const sorted = samples.slice(1).sort((a, b) => a - b);
      const at = (q) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))] ?? 0;
      return {
        frames: sorted.length,
        p50: Number(at(0.5).toFixed(2)),
        p95: Number(at(0.95).toFixed(2)),
        max: Number(sorted[sorted.length - 1]?.toFixed(2) ?? 0),
        scrolledTo: scroller.scrollTop,
      };
    });
    report.steps.longSession.jank = jank;

    // Not blank: after the scroll the window still holds rows, and they are the
    // ones near the new position (not the first message).
    report.steps.longSession.afterScroll = await page.evaluate(() => ({
      rows: document.querySelectorAll('.zk-transcript__row').length,
      firstRowId:
        document.querySelector('.zk-transcript__row')?.getAttribute('data-message-id') ?? null,
      hasText: (document.querySelector('.zk-transcript__row')?.textContent ?? '').length > 0,
    }));
  }

  // --------------------------------------------------------------- thread --
  const firstId = await page.evaluate(async () => {
    const response = await fetch('/api/sessions').catch(() => null);
    if (response?.ok) {
      const body = await response.json();
      return body.sessions?.[0]?.id ?? null;
    }
    return null;
  });
  if (firstId) {
    await page.goto(`${BASE}/c/${firstId}`, { waitUntil: 'networkidle' });
    await page.waitForSelector('.zk-transcript', { timeout: 15_000 });
    await page.screenshot({ path: `${OUT}/thread.png` });
  }

  writeFileSync(`${OUT}/report.json`, JSON.stringify(report, null, 2));
  await browser.close();
  console.log(JSON.stringify(report, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});