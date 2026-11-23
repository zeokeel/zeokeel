import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * Docs ↔ scripts consistency (task-18 follow-up, and the seventh time this class
 * of bug bit us: a doc promised `npm run e2e` that did not exist, along with the
 * fragment handshake, the frame newline and `prompt` vs `text`).
 *
 * The rule: any `npm run <name>` written in AGENTS.md or docs/**\/*.md must name
 * a script that exists in `web/package.json`. Only web-side npm commands are in
 * scope — `make acceptance-*`, `bash scripts/…` and Go commands belong to other
 * checkers.
 *
 * Exemptions (so illustrations never become false alarms — a false alarm teaches
 * people to ignore alarms):
 *
 *   npm run not-a-real-script <!-- npm-script:ignore -->
 *
 * or a whole block:
 *
 *   <!-- npm-scripts:ignore-begin -->
 *   … examples that intentionally name non-existent scripts …
 *   <!-- npm-scripts:ignore-end -->
 *
 * `DOCS_ROOT` points the scan at another tree; the verification of this test
 * itself uses it so injecting a bad line never edits a real document.
 */

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = process.env.DOCS_ROOT
  ? resolve(process.env.DOCS_ROOT)
  : resolve(here, '../..');
const packageJsonPath = resolve(here, '../package.json');

const IGNORE_LINE = 'npm-script:ignore';
const IGNORE_BEGIN = 'npm-scripts:ignore-begin';
const IGNORE_END = 'npm-scripts:ignore-end';

// Digits matter: `e2e` was silently truncated to `e` by an earlier regex.
const COMMAND = /npm run ([A-Za-z0-9:_-]+)/g;

interface Finding {
  file: string;
  line: number;
  name: string;
  text: string;
}

function markdownFiles(root: string): string[] {
  const out: string[] = [];
  const top = [resolve(root, 'AGENTS.md')];
  for (const file of top) {
    try {
      if (statSync(file).isFile()) out.push(file);
    } catch {
      // AGENTS.md is optional when DOCS_ROOT points at a fixture.
    }
  }
  const walk = (dir: string) => {
    let entries: string[] = [];
    try {
      entries = readdirSync(dir);
    } catch {
      return;
    }
    for (const entry of entries) {
      // docs/archive is explicitly "evidence, not contract" (AGENTS.md §0).
      if (entry === 'archive') continue;
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        walk(full);
        continue;
      }
      if (entry.endsWith('.md')) out.push(full);
    }
  };
  walk(resolve(root, 'docs'));
  return out;
}

/** Every `npm run <name>` outside an explicit exemption. */
export function documentedCommands(root: string): Finding[] {
  const findings: Finding[] = [];
  for (const file of markdownFiles(root)) {
    const lines = readFileSync(file, 'utf8').split('\n');
    let ignoring = false;
    lines.forEach((text, index) => {
      if (text.includes(IGNORE_BEGIN)) ignoring = true;
      if (!ignoring && !text.includes(IGNORE_LINE)) {
        for (const match of text.matchAll(COMMAND)) {
          findings.push({ file, line: index + 1, name: match[1], text: text.trim() });
        }
      }
      if (text.includes(IGNORE_END)) ignoring = false;
    });
  }
  return findings;
}

const scripts = () =>
  (JSON.parse(readFileSync(packageJsonPath, 'utf8')) as { scripts: Record<string, string> })
    .scripts;

describe('documented npm commands exist', () => {
  it('every `npm run <name>` in AGENTS.md / docs is a real web script', () => {
    const declared = new Set(Object.keys(scripts()));
    const findings = documentedCommands(repoRoot);
    // The scan must actually see documents; an empty scan proves nothing.
    expect(findings.length).toBeGreaterThan(5);

    const missing = findings
      .filter((finding) => !declared.has(finding.name))
      .map(
        (finding) =>
          `${finding.file.replace(`${repoRoot}/`, '')}:${finding.line} → npm run ${finding.name}`,
      );
    expect(missing).toEqual([]);
  });

  it('every script that names a file points at a file that exists', () => {
    const base = resolve(here, '..');
    const broken: string[] = [];
    for (const [name, command] of Object.entries(scripts())) {
      for (const match of command.matchAll(/node\s+(\.?[^\s]+\.m?js)/g)) {
        const target = resolve(base, match[1]);
        try {
          statSync(target);
        } catch {
          broken.push(`${name} → ${match[1]}`);
        }
      }
    }
    expect(broken).toEqual([]);
  });

  it('honours the exemption comments', () => {
    // A fixture tree with one bad line: ignored inline, ignored in a block, and
    // one that must still be caught — this is the test proving the checker bites.
    const root = resolve(here, '../.cache/docs-fixture');
    const { mkdirSync, writeFileSync, rmSync } = fsShim();
    rmSync(root, { recursive: true, force: true });
    mkdirSync(resolve(root, 'docs'), { recursive: true });
    writeFileSync(
      resolve(root, 'AGENTS.md'),
      [
        'npm run typecheck',
        'npm run not-a-script <!-- npm-script:ignore -->',
        '<!-- npm-scripts:ignore-begin -->',
        'npm run also-not-a-script',
        '<!-- npm-scripts:ignore-end -->',
        'npm run e2e',
      ].join('\n'),
    );
    writeFileSync(
      resolve(root, 'docs/plan.md'),
      ['## 反例', 'npm run definitely-missing'].join('\n'),
    );

    const names = documentedCommands(root).map((finding) => finding.name);
    expect(names).toEqual(['typecheck', 'e2e', 'definitely-missing']);
    const declared = new Set(Object.keys(scripts()));
    expect(names.filter((name) => !declared.has(name))).toEqual(['definitely-missing']);
    rmSync(root, { recursive: true, force: true });
  });
});

/** Small indirection so the fixture code reads clearly above. */
function fsShim() {
  // Imported lazily to keep the top of the file about the rule, not the fixture.
  // eslint-disable-next-line @typescript-eslint/no-require-imports -- sync fs in a test helper
  return require('node:fs') as typeof import('node:fs');
}