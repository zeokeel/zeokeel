#!/usr/bin/env node
// Minimal real-gateway smoke run: UI text and roles only.
//
// No fixtures, no REST calls, no poking the control plane: this drives exactly
// what a user does — 新建会话 → 发一轮 → （若停下）允许 → 等这一轮结束 → 刷新恢复.
// It works against the mock and against a real `--product serve` gateway, and it
// never assumes a particular session title, id or message text from a fixture.
//
//   node dev/e2e-smoke.mjs                       # against http://127.0.0.1:5178
//   E2E_BASE=http://127.0.0.1:9932 E2E_TOKEN=<token> node dev/e2e-smoke.mjs
//
// `E2E_TOKEN` is the process token from the gateway's startup line
// (`http://127.0.0.1:9932/#token=…`). With it the first navigation carries the
// fragment, which is what the client exchanges for its cookie (§3.1).
//   E2E_PROMPT='跑一下测试' node dev/e2e-smoke.mjs
//
// Exit code 0 only when every step held; the JSON report says which step failed
// and what the screen looked like at that moment.
import { mkdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require('/data/mygo/.cache/pw/node_modules/playwright');

const BASE = process.env.E2E_BASE ?? 'http://127.0.0.1:5178';
const OUT = process.env.E2E_OUT ?? '/data/mygo/.cache/ui-review';
const PROMPT = process.env.E2E_PROMPT ?? '你好，用一句话介绍这个工作区。';

const RUNNING_HINT = 'Enter 排队发送';
const IDLE_HINT = 'Enter 或 Cmd/Ctrl+Enter 发送';

const report = { base: BASE, prompt: PROMPT, steps: [], ok: false, errors: [] };

function step(name, detail) {
  report.steps.push({ name, ...detail });
  console.log(`[step] ${name} ${detail ? JSON.stringify(detail) : ''}`);
}

async function main() {
  mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
  page.on('console', (message) => {
    if (message.type() === 'error') report.errors.push(message.text());
  });
  page.on('pageerror', (error) => report.errors.push(String(error)));

  const fail = async (reason) => {
    await page.screenshot({ path: `${OUT}/smoke-failed.png` }).catch(() => undefined);
    throw new Error(reason);
  };

  // 1. Open the app and wait for a live control plane. The fragment token is
  //    what the client exchanges for the session cookie before connecting.
  const token = process.env.E2E_TOKEN?.trim();
  const startUrl = token ? `${BASE}/#token=${token}` : `${BASE}/`;
  await page.goto(startUrl, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.zk-sidebar', { timeout: 20_000 });
  const connection = await page
    .waitForFunction(
      () => !document.querySelector('.zk-connection'),
      { timeout: 20_000 },
    )
    .then(() => 'online')
    .catch(() => 'offline');
  const fragmentErased = !page.url().includes('token=');
  step('connection', { state: connection, fragmentErased });
  if (connection !== 'online') await fail('控制面没有连上（顶带仍在显示重连）');
  if (token && !fragmentErased) await fail('地址栏仍带 #token=，fragment 没有被抹掉');

  // 2. Create a session from the sidebar (session.create).
  const createButton = page.getByRole('button', { name: '新建会话' });
  if ((await createButton.count()) === 0) await fail('侧栏没有「新建会话」按钮');
  await createButton.first().click();
  await page.waitForURL(/\/c\/[^/]+$/, { timeout: 15_000 });
  const sessionUrl = page.url();
  step('session.create', { url: sessionUrl });

  // 2b. A pending approval from an earlier run blocks the composer
  //     ("先处理上面的审批…"). Decide it first: that is what a user does.
  const pendingAllow = page.getByRole('button', { name: '允许一次' });
  const pendingAllowed = await pendingAllow
    .first()
    .waitFor({ state: 'visible', timeout: 3_000 })
    .then(() => true)
    .catch(() => false);
  if (pendingAllowed) {
    await pendingAllow.first().click();
    await page
      .waitForFunction(() => !document.body.textContent?.includes('需要审批'), {
        timeout: 30_000,
      })
      .catch(() => undefined);
    step('approval.preexisting', { resolved: 'allow' });
  }

  // 3. Send one turn from the composer (SSE).
  const textbox = page.getByRole('textbox', { name: '消息输入框' });
  await textbox.waitFor({ timeout: 15_000 });
  await page
    .waitForFunction(
      () => {
        const node = document.querySelector('textarea[aria-label="消息输入框"]');
        return node instanceof HTMLTextAreaElement && !node.disabled;
      },
      { timeout: 20_000 },
    )
    .catch(async () => {
      await fail('输入框一直不可用（仍有审批或有运行没结束）');
    });
  await textbox.click();
  await textbox.fill(PROMPT);
  await textbox.press('Enter');
  await page.waitForFunction(
    (hint) => document.body.textContent?.includes(hint),
    RUNNING_HINT,
    { timeout: 20_000 },
  );
  step('run.started', { prompt: PROMPT });

  // 4. If the run stopped for approval, allow it (bare Enter must never decide).
  const allow = page.getByRole('button', { name: '允许一次' });
  const approvalVisible = await allow
    .first()
    .waitFor({ state: 'visible', timeout: 20_000 })
    .then(() => true)
    .catch(() => false);
  if (approvalVisible) {
    const filled = await page.evaluate(() =>
      [...document.querySelectorAll('.zk-approval button')].filter((button) =>
        button.className.includes('ant-btn-primary'),
      ).length,
    );
    if (filled > 0) await fail('审批卡出现了填充主按钮（无默认动作被破坏）');
    await allow.first().click();
    await page
      .waitForFunction(() => !document.body.textContent?.includes('需要审批'), {
        timeout: 30_000,
      })
      .catch(() => undefined);
    step('approval.allowed', { filledPrimaryButtons: filled });
  } else {
    step('approval.none', { note: '这一轮没有停在审批' });
  }

  // 5. Wait for the turn to settle: the composer hint returns to idle.
  await page
    .waitForFunction((hint) => document.body.textContent?.includes(hint), IDLE_HINT, {
      timeout: 180_000,
    })
    .catch(async () => {
      await fail('这一轮 180 秒内没有结束（既没完成也没停）');
    });
  const assistantText = await page.evaluate(() => {
    const bubbles = [...document.querySelectorAll('.zk-transcript__row')];
    const assistant = bubbles.filter((node) => node.textContent && node.textContent.length > 0);
    return assistant[assistant.length - 1]?.textContent?.slice(0, 200) ?? '';
  });
  step('run.settled', { lastRow: assistantText });
  await page.screenshot({ path: `${OUT}/smoke-settled.png` });

  // 6. Refresh: the conversation must come back from the snapshot.
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.zk-transcript', { timeout: 20_000 });
  const restored = await page
    .waitForFunction(
      (prompt) => document.body.textContent?.includes(prompt),
      PROMPT,
      { timeout: 20_000 },
    )
    .then(() => true)
    .catch(() => false);
  if (!restored) await fail('刷新后找不到刚才那条消息（快照恢复失败）');
  step('restore.after-reload', { url: page.url() });
  await page.screenshot({ path: `${OUT}/smoke-restored.png` });

  report.ok = true;
  await browser.close();
  writeFileSync(`${OUT}/smoke-report.json`, JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
}

main().catch(async (error) => {
  report.errors.push(String(error?.message ?? error));
  writeFileSync(`${OUT}/smoke-report.json`, JSON.stringify(report, null, 2));
  console.error(`[smoke] FAILED: ${error?.message ?? error}`);
  console.log(JSON.stringify(report, null, 2));
  process.exit(1);
});