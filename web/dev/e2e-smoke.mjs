#!/usr/bin/env node
// Real-browser smoke, run twice: once in Chinese, once in English.
//
// No fixtures, no REST calls, no poking the control plane: this drives what a
// user does — 新建会话 → 发一轮 → （若停下）允许 → 等这一轮结束 → 刷新恢复.
// It works against the mock and against a real `--product serve` gateway.
//
// **Controls are located by `data-testid`, assertions are made on text.** That
// split matters: before this, the script located the 新建会话 button by its
// Chinese label, so an English UI could not be exercised at all — and it was
// not, until the language row shipped and the smoke failed on a browser whose
// locale is en-US. Locating by id and asserting by copy keeps both checks: the
// control is findable *and* its words are the right ones.
//
//   npm run e2e                                  # mock + dev server (dev-token)
//   E2E_BASE=http://127.0.0.1:9932 E2E_TOKEN=<token> node dev/e2e-smoke.mjs
//
// Each pass first **switches the language through the settings UI** (the row
// behind B5), so the passes are order-independent and the switch itself is
// exercised instead of assumed.
import { mkdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require('/data/mygo/.cache/pw/node_modules/playwright');

const BASE = process.env.E2E_BASE ?? 'http://127.0.0.1:5178';
// `npm run e2e` out of the box targets the mock (`npm run mock` + `npm run dev`),
// whose dev token is `dev-token`. A real gateway prints its own token.
const DEFAULT_TOKEN = BASE === 'http://127.0.0.1:5178' ? 'dev-token' : '';
const OUT = process.env.E2E_OUT ?? '/data/mygo/.cache/ui-review';
const PROMPT = process.env.E2E_PROMPT ?? '你好，用一句话介绍这个工作区。';

/**
 * The copy each pass asserts. This is the test's *expectation table*: when the
 * catalogue changes, the smoke names the language and the string that broke.
 */
const PASSES = [
  {
    tag: 'zh',
    locale: 'zh-CN',
    optionId: 'zh',
    copy: {
      newSession: '新建会话',
      inbox: '收件箱',
      runningHint: 'Enter 排队发送',
      idleHint: 'Enter 或 Cmd/Ctrl+Enter 发送',
      needApproval: '需要审批',
      allowOnce: '允许一次',
      deny: '拒绝',
    },
  },
  {
    tag: 'en',
    locale: 'en-US',
    optionId: 'en',
    copy: {
      newSession: 'New session',
      inbox: 'Inbox',
      runningHint: 'Enter queues it',
      idleHint: 'Enter or Cmd/Ctrl+Enter to send',
      needApproval: 'Needs approval',
      allowOnce: 'Allow once',
      deny: 'Deny',
    },
  },
];

/**
 * antd inserts a space between two CJK characters in button labels (`拒 绝`), so
 * comparisons ignore whitespace. The words are still compared — only the
 * typographic spacing is normalised.
 */
const norm = (text) => (text ?? '').replace(/\s+/g, '');

function makeReporter(tag) {
  const report = { tag, steps: [], ok: false, errors: [] };
  const log = (line) => console.log(`[${tag}] ${line}`);
  return {
    report,
    step(name, detail) {
      report.steps.push({ name, ...detail });
      log(`[step] ${name} ${detail ? JSON.stringify(detail) : ''}`);
    },
  };
}

async function runPass(browser, pass) {
  const { tag, locale, optionId, copy } = pass;
  const { report, step } = makeReporter(tag);
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 }, locale });
  page.on('console', (message) => {
    if (message.type() === 'error') report.errors.push(message.text());
  });
  page.on('pageerror', (error) => report.errors.push(String(error)));

  const fail = async (reason) => {
    await page
      .screenshot({ path: `${OUT}/smoke-failed-${tag}.png` })
      .catch(() => undefined);
    throw new Error(`[${tag}] ${reason}`);
  };

  // 1. Open and wait for a live control plane.
  const token = (process.env.E2E_TOKEN ?? DEFAULT_TOKEN).trim();
  const startUrl = token ? `${BASE}/#token=${token}` : `${BASE}/`;
  await page.goto(startUrl, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.zk-sidebar', { timeout: 20_000 });
  const connection = await page
    .waitForFunction(() => !document.querySelector('.zk-connection'), { timeout: 20_000 })
    .then(() => 'online')
    .catch(() => 'offline');
  const fragmentErased = !page.url().includes('token=');
  step('connection', { state: connection, fragmentErased, locale });
  if (connection !== 'online') await fail('控制面没有连上 / control plane never came online');
  if (token && !fragmentErased) await fail('fragment 没有被抹掉');

  // 2. Switch the language through settings — the row behind B5, exercised the
  //    way a user does it.
  await page.goto(`${BASE}/settings/appearance`, { waitUntil: 'domcontentloaded' });
  const option = page.locator(`[data-testid="settings-option-ui.language-${optionId}"]`);
  await option.waitFor({ timeout: 15_000 });

  // **Both initial states are legal.** On a warm store `ui.language` already has
  // a value; on a cold one the row is absent and the product falls back to
  // `navigator.language`. Asserting "a value must appear" made this pass depend
  // on a database that happened to be warm — green locally, red in CI.
  const stored = await page.evaluate(() => {
    const field = document.querySelector('[data-testid="settings-field-ui.language"]');
    const checked = field?.querySelector('input[type=radio]:checked');
    return checked instanceof HTMLInputElement ? checked.value : null;
  });
  step('language.initial', { lang: optionId, storedValue: stored });

  // Write only when the stored value differs — on a cold store that is exactly
  // the case (`stored === null`), so the write path is exercised in CI too. When
  // the row already holds this language there is nothing to save and Save stays
  // disabled by design.
  if (stored !== optionId) {
    await option.click();
    const saveButton = page.locator('[data-testid="settings-save-button"]');
    await saveButton.waitFor({ timeout: 10_000 });
    const canSave = await page.evaluate(
      () => {
        const button = document.querySelector('[data-testid="settings-save-button"]');
        return !(button instanceof HTMLButtonElement && button.disabled);
      },
    );
    if (canSave) {
      await saveButton.click();
      step('language.written', { lang: optionId });
    } else {
      // Recorded, not swallowed: when the chosen option is the language the UI
      // already renders (cold store + `zh-CN` browser → Chinese), selecting it
      // changes nothing, so the form is legitimately not dirty and Save is
      // disabled. Nothing to write, nothing to fail — but the step is logged so
      // a reader can tell "no write" from "write failed".
      step('language.write.unavailable', { lang: optionId, storedValue: stored });
    }
  }
  // Whatever the path taken, the shell must now render the target language —
  // this is the assertion, the branch above is only how we got here.
  await page
    .waitForFunction(
      (label) =>
        (document.querySelector('[data-testid="new-session"]')?.textContent ?? '')
          .replace(/\s+/g, '') === label,
      norm(copy.newSession),
      { timeout: 15_000 },
    )
    .catch(async () => {
      const text = await page.locator('[data-testid="new-session"]').first().textContent();
      await fail(`切到 ${tag} 后侧栏按钮文案是「${text}」，期望「${copy.newSession}」`);
    });
  step('language.switched', { lang: optionId, newSessionLabel: copy.newSession });

  // The navigation must follow too — that is the "whole site" claim.
  const inboxText = await page.locator('.ant-menu-item').first().textContent();
  if (!norm(inboxText).includes(norm(copy.inbox))) {
    await fail(`导航第一项是「${inboxText}」，期望包含「${copy.inbox}」`);
  }
  step('copy.follows', { inbox: copy.inbox });

  // 3. Create a session from the sidebar.
  await page.locator('[data-testid="new-session"]').first().click();
  await page.waitForURL(/\/c\/[^/]+$/, { timeout: 15_000 });
  step('session.create', { url: page.url() });

  // 3b. A pending approval from an earlier run blocks the composer: decide it.
  const pendingAllow = page.locator('[data-testid="approval-allow"]');
  const pendingAllowed = await pendingAllow
    .first()
    .waitFor({ state: 'visible', timeout: 3_000 })
    .then(() => true)
    .catch(() => false);
  if (pendingAllowed) {
    await pendingAllow.first().click();
    await page
      .waitForFunction((label) => !document.body.textContent?.includes(label), copy.needApproval, {
        timeout: 30_000,
      })
      .catch(() => undefined);
    step('approval.preexisting', { resolved: 'allow', label: copy.allowOnce });
  }

  // 4. Wait until the composer accepts input. It stays disabled while an
  //    approval is pending, and an approval can arrive at any time (a previous
  //    run parks one): decide it, then retry — that is what a user does.
  //    `.last()`: during a route transition the outgoing composer is still in
  //    the DOM for a frame; the newest one is the live composer.
  const input = page.locator('[data-testid="composer-input"]').last();
  await input.waitFor({ timeout: 15_000 });
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const enabled = await page.evaluate(() => {
      const nodes = [...document.querySelectorAll('[data-testid="composer-input"]')];
      const node = nodes[nodes.length - 1];
      return node instanceof HTMLTextAreaElement && !node.disabled;
    });
    if (enabled) break;
    const blocker = page.locator('[data-testid="approval-allow"]').first();
    if ((await blocker.count()) === 0) {
      await page.waitForTimeout(500);
      continue;
    }
    await blocker.click();
    await page
      .waitForFunction((label) => !document.body.textContent?.includes(label), copy.needApproval, {
        timeout: 30_000,
      })
      .catch(() => undefined);
    step('approval.unblocked', { attempt: attempt + 1 });
  }
  const readyToSend = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll('[data-testid="composer-input"]')];
    const node = nodes[nodes.length - 1];
    return node instanceof HTMLTextAreaElement && !node.disabled;
  });
  if (!readyToSend) await fail('输入框一直不可用 / composer stayed disabled');
  await input.click();
  await input.fill(PROMPT);
  await input.press('Enter');
  await page
    .waitForFunction((hint) => document.body.textContent?.includes(hint), copy.runningHint, {
      timeout: 20_000,
    })
    .catch(() => fail(`运行提示没有出现（期望含「${copy.runningHint}」）`));
  step('run.started', { prompt: PROMPT });

  // 5. If the run parked on an approval, assert **both verbs in this language**
  //    before allowing — the wording is the assertion, not a formality.
  const allow = page.locator('[data-testid="approval-allow"]');
  const deny = page.locator('[data-testid="approval-deny"]');
  const approvalVisible = await allow
    .first()
    .waitFor({ state: 'visible', timeout: 20_000 })
    .then(() => true)
    .catch(() => false);
  if (approvalVisible) {
    const allowText = (await allow.first().textContent())?.trim();
    const denyText = (await deny.first().textContent())?.trim();
    if (!norm(allowText).includes(norm(copy.allowOnce))) {
      await fail(`允许按钮是「${allowText}」，期望「${copy.allowOnce}」`);
    }
    if (!norm(denyText).includes(norm(copy.deny))) {
      await fail(`拒绝按钮是「${denyText}」，期望「${copy.deny}」`);
    }
    const filled = await page.evaluate(
      () =>
        [...document.querySelectorAll('.zk-approval button')].filter((button) =>
          button.className.includes('ant-btn-primary'),
        ).length,
    );
    if (filled > 0) await fail('审批卡出现了填充主按钮');
    await allow.first().click();
    await page
      .waitForFunction((label) => !document.body.textContent?.includes(label), copy.needApproval, {
        timeout: 30_000,
      })
      .catch(() => undefined);
    step('approval.allowed', {
      allowVerb: allowText,
      denyVerb: denyText,
      filledPrimaryButtons: filled,
    });
  } else {
    step('approval.none', { note: '这一轮没有停在审批' });
  }

  // 6. Wait for the turn to settle.
  await page
    .waitForFunction((hint) => document.body.textContent?.includes(hint), copy.idleHint, {
      timeout: 180_000,
    })
    .catch(() => fail(`这一轮 180 秒内没有结束（期望提示含「${copy.idleHint}」）`));
  const assistantText = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.zk-transcript__row')];
    return rows[rows.length - 1]?.textContent?.slice(0, 160) ?? '';
  });
  step('run.settled', { lastRow: assistantText });
  await page.screenshot({ path: `${OUT}/smoke-settled-${tag}.png` });

  // 7. Refresh: the conversation returns from the snapshot, still in the chosen
  //    language (a reload must not fall back to the browser's locale).
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.zk-transcript', { timeout: 20_000 });
  const restored = await page
    .waitForFunction((prompt) => document.body.textContent?.includes(prompt), PROMPT, {
      timeout: 20_000,
    })
    .then(() => true)
    .catch(() => false);
  if (!restored) await fail('刷新后找不到刚才那条消息（快照恢复失败）');
  const afterReloadLabel = await page.locator('[data-testid="new-session"]').first().textContent();
  if (!norm(afterReloadLabel).includes(norm(copy.newSession))) {
    await fail(`刷新后语言回退了：「${afterReloadLabel}」`);
  }
  step('restore.after-reload', { url: page.url(), language: optionId });

  report.ok = true;
  await page.close();
  return report;
}

async function main() {
  mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const reports = [];
  let failure = null;
  for (const pass of PASSES) {
    try {
      reports.push(await runPass(browser, pass));
    } catch (error) {
      failure = error;
      reports.push({ tag: pass.tag, ok: false, error: String(error?.message ?? error) });
      break; // a broken pass stops the run: the next would only echo it
    }
  }
  await browser.close();

  const ok = reports.length === PASSES.length && reports.every((item) => item.ok);
  const summary = { base: BASE, prompt: PROMPT, ok, passes: reports };
  writeFileSync(`${OUT}/smoke-report.json`, JSON.stringify(summary, null, 2));
  if (!ok) {
    console.error(`[smoke] FAILED ${failure?.message ?? 'one pass did not complete'}`);
    console.log(JSON.stringify(summary, null, 2));
    process.exit(1);
  }
  console.log(`[smoke] ${reports.length} passes ok (${reports.map((p) => p.tag).join(', ')})`);
}

main().catch((error) => {
  writeFileSync(
    `${OUT}/smoke-report.json`,
    JSON.stringify({ ok: false, errors: [String(error?.message ?? error)] }, null, 2),
  );
  console.error(`[smoke] FAILED: ${error?.message ?? error}`);
  process.exit(1);
});