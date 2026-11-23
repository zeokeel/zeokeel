#!/usr/bin/env node
// One-command cold-store browser smoke: fixture up → both language passes → teardown.
//
// Why this exists: `dev/e2e-smoke.mjs` does **not** start a gateway. It reads
// `E2E_BASE` / `E2E_TOKEN` and assumes both are already live, so the answer to
// "how do I reproduce the cold run?" cannot be a single `node dev/e2e-smoke.mjs`
// line — that only works against the already-running mock. Reporting it as one
// line was wrong: the command has to be the command.
//
// What "cold" means here: a fresh `mktemp -d` data directory handed to the
// acceptance fixture, i.e. no settings rows at all — the state a CI machine
// starts from and the one where `ui.language` does not exist yet.
//
//   npm run e2e:cold                  # the command to copy
//   E2E_COLD_KEEP=1 npm run e2e:cold  # keep the data dir for inspection
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, '..');
const repoRoot = resolve(webRoot, '..');
const fixture = resolve(repoRoot, 'scripts/acceptance/serve-with-fake-model.sh');

const dataDir = mkdtempSync(join(tmpdir(), 'zk-e2e-cold-'));
const port = 9986 + Math.floor(Math.random() * 40);
const keep = process.env.E2E_COLD_KEEP === '1';

const log = (line) => console.log(`[e2e:cold] ${line}`);

if (process.stdin.isTTY) {
  log(`data dir: ${dataDir}`);
  log(`port: ${port}`);
}

const fixtureProcess = spawn(
  'bash',
  [fixture, '--model', 'acceptance-tool', '--port', String(port)],
  {
    cwd: repoRoot,
    env: { ...process.env, ACCEPTANCE_DATA_DIR: dataDir },
    stdio: ['ignore', 'pipe', 'pipe'],
  },
);

let fixtureOutput = '';
fixtureProcess.stdout.on('data', (chunk) => {
  fixtureOutput += String(chunk);
});
fixtureProcess.stderr.on('data', (chunk) => {
  fixtureOutput += String(chunk);
});

/** Wait for `gateway.json` — the fixture writes it once the gateway is listening. */
async function waitForGateway(timeoutMs = 120_000) {
  const info = join(dataDir, 'gateway.json');
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (Date.now() > deadline) {
      throw new Error(`网关没有在 ${timeoutMs / 1000}s 内起来。夹具输出：\n${fixtureOutput}`);
    }
    try {
      const parsed = JSON.parse(readFileSync(info, 'utf8'));
      if (parsed.port && parsed.token) return { port: parsed.port, token: parsed.token };
    } catch {
      // not written yet
    }
    if (fixtureProcess.exitCode !== null) {
      throw new Error(`夹具退出了（code ${fixtureProcess.exitCode}）。输出：\n${fixtureOutput}`);
    }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 150));
  }
}

async function main() {
  let exitCode = 1;
  try {
    log('starting the acceptance fixture on a fresh data directory');
    const { port: gatewayPort, token } = await waitForGateway();
    log(`gateway up on 127.0.0.1:${gatewayPort}`);

    const smoke = spawn(
      process.execPath,
      [resolve(here, 'e2e-smoke.mjs')],
      {
        cwd: webRoot,
        env: {
          ...process.env,
          // The fixture prints a URL with the token; the smoke takes them apart.
          E2E_BASE: `http://127.0.0.1:${gatewayPort}`,
          E2E_TOKEN: token,
        },
        stdio: 'inherit',
      },
    );
    exitCode = await new Promise((resolvePromise) => {
      smoke.on('exit', (code) => resolvePromise(code ?? 1));
    });
  } catch (error) {
    console.error(`[e2e:cold] FAILED: ${error?.message ?? error}`);
    exitCode = 1;
  } finally {
    fixtureProcess.kill('SIGTERM');
    if (keep) log(`kept the data dir: ${dataDir}`);
    else rmSync(dataDir, { recursive: true, force: true });
  }
  process.exit(exitCode);
}

void main();