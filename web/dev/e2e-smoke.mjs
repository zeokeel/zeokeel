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
      toolDone: '完成',
      usage: '入',
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
      toolDone: 'Done',
      usage: 'in',
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
  // Decision (b): the input area is `composer`, the textarea inside it is found
  // by role — `Sender`'s internals (and whether props land on the textarea or its
  // wrapper) are not our contract.
  const input = page.getByTestId('composer').getByRole('textbox');
  await input.waitFor({ timeout: 15_000 });
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const enabled = await page.evaluate(() => {
      const node = document.querySelector('[data-testid="composer"] textarea');
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
    const node = document.querySelector('[data-testid="composer"] textarea');
    return node instanceof HTMLTextAreaElement && !node.disabled;
  });
  if (!readyToSend) await fail('输入框一直不可用 / composer stayed disabled');
  // 3a. The slash menu, in a real browser — the baseline the Suggestion swap
  //     must match line for line. Located by **our own command text** (`/files`
  //     is the composer's own label), never by the menu's DOM structure, so the
  //     same three checks work for any renderer.
  //
  //     Order matters: the Enter check runs **first**, because Esc keeps the
  //     draft and a dismissed menu does not reopen for the same query — going
  //     back to the thread in between gives the next check a fresh composer.
  {
    const threadUrl = page.url();
    const box = page.getByTestId('composer').getByRole('textbox');
    await box.waitFor({ timeout: 15_000 });
    await box.click();
    await box.fill('/');
    const menuItem = page.getByText('files', { exact: false }).first();
    await menuItem.waitFor({ state: 'visible', timeout: 10_000 });
    step('slash.menu.open', { item: 'files' });

    // ↓↓ then Enter: reaching the files view proves **both** that the arrows
    // moved the highlight (otherwise Enter would run `new`) and that Enter ran
    // the highlighted command rather than sending the draft.
    await box.press('ArrowDown');
    await box.press('ArrowDown');
    await box.press('Enter');
    await page.waitForURL(/\/files/, { timeout: 15_000 }).catch(async () => {
      await fail('↓↓ + Enter 没有执行 files 命令（高亮或 Enter 语义破了）');
    });
    step('slash.menu.enter', { url: page.url() });

    // Back to the thread: `.zk-composer` is always there, `.zk-transcript` is not
    // (the inspector route keeps the panel, and an empty thread may not render a
    // transcript at all).
    await page.goto(threadUrl, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('.zk-composer', { timeout: 15_000 });
    const again = page.getByTestId('composer').getByRole('textbox');
    await again.waitFor({ timeout: 15_000 });
    await again.click();
    await again.fill('/');
    await page.getByText('files', { exact: false }).first().waitFor({ state: 'visible', timeout: 10_000 });
    await again.press('Escape');
    await page
      .getByText('files', { exact: false })
      .first()
      .waitFor({ state: 'hidden', timeout: 10_000 });
    step('slash.menu.esc', { hidden: true });
    await again.fill('');
  }

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
  /**
   * Structured assertions, not one blob of textContent.
   *
   * `textContent` includes nodes that are in the DOM but visually hidden, and a
   * collapse animation leaves the card's sections mounted for a frame — so the
   * same screen compared equal or unequal depending on *when* it was read. It did
   * exactly that: the English pass captured `Output ok Raw arguments` while the
   * Chinese one did not, although both screenshots are structurally identical
   * (tool row collapsed, one summary line, status, answer, usage).
   *
   * So assert the three things that matter, each through a stable element:
   */
  const settled = await page.evaluate(() => {
    const thought = document.querySelector('.zk-thought');
    const rows = [...document.querySelectorAll('.zk-transcript__row')];
    return {
      toolRow: thought?.textContent ?? '',
      lastRow: rows[rows.length - 1]?.textContent ?? '',
    };
  });
  // The tool name is the model's choice (`web_fetch` on the acceptance fixture,
  // `bash` on the mock), so the assertion is that **a** tool row rendered.
  if (!settled.toolRow.trim()) {
    await fail('这一轮没有渲染工具行');
  }
  if (!norm(settled.toolRow).includes(norm(copy.toolDone))) {
    await fail(`工具行没有完成态「${copy.toolDone}」：「${settled.toolRow.slice(0, 80)}」`);
  }
  if (settled.lastRow.trim().length < 8) {
    await fail(`结算后最后一行没有模型回答：「${settled.lastRow.slice(0, 80)}」`);
  }
  if (!norm(settled.lastRow).includes(norm(copy.usage))) {
    await fail(`最后一行没有用量标记「${copy.usage}」`);
  }
  step('run.settled', {
    toolVisible: settled.toolRow.slice(0, 40),
    toolStatus: copy.toolDone,
    answerVisible: true,
    usageVisible: copy.usage,
  });
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