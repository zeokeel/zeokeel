#!/usr/bin/env node
// Generation-chain baseline + verification (task-18).
//
// The lesson this encodes (from S5): a baseline is only evidence when it is
// taken **before** the irreversible action and on a **frozen** tree. The Lead's
// S5 baseline was captured after the deletions had landed in the worktree, so
// comparing it to itself proved nothing for `internal/design`.
//
// Two rules this follows:
//  1. Red only when an *input the generator actually reads* disappears. A file
//     count that changed because a declared-unrelated package was removed is
//     legal and must NOT alarm — false alarms teach people to ignore alarms.
//  2. The input list is **parsed from the generator scripts**, not guessed from
//     directory names: `read('internal/design/tokens.go')` and the `readdirSync`
//     source of the icon set. Guessing directories is the same mistake in a new
//     place.
//
// Usage:
//   npm run gen:baseline   # capture (refuses on a dirty tree)
//   npm run gen:verify     # re-run the generators and compare
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, '..');
// `ZK_REPO_ROOT` lets the verification test itself run against a throwaway copy
// of the tree, so injecting a breakage never touches a real Go source file.
const repoRoot = process.env.ZK_REPO_ROOT ? resolve(process.env.ZK_REPO_ROOT) : resolve(webRoot, '..');
const baselinePath = resolve(webRoot, '.cache/gen-baseline.json');

// The generators, and the shapes of input they read. Keep this table next to the
// parsers so "which script's inputs are we tracking?" has one answer.
const GENERATORS = [
  {
    script: 'scripts/gen-tokens.mjs',
    outputs: ['src/design/tokens.css', 'src/design/antd-theme.ts'],
  },
  { script: 'scripts/gen-icons.mjs', outputs: ['src/design/icons.tsx'] },
];
// A fourth artefact is generated but from Go constants that live in many files
// (settings keys); it has its own script and is tracked the same way.
const SETTINGS_KEY_GENERATOR = {
  script: 'scripts/gen-settings-keys.mjs',
  outputs: ['src/design/settings-keys.ts'],
};
/** The WS contract is generated from `internal/gateway/contract.json`. */
const WS_CONTRACT_GENERATOR = {
  script: 'scripts/gen-ws-contract.mjs',
  outputs: ['src/design/ws-contract.ts'],
};
const ALL_GENERATORS = [...GENERATORS, SETTINGS_KEY_GENERATOR, WS_CONTRACT_GENERATOR];

const sha256 = (path) => createHash('sha256').update(readFileSync(path)).digest('hex');

const rel = (path) => relative(repoRoot, path);

/** Files a generator `read()`s by literal path. */
function literalInputs(source) {
  const pattern = /\bread\(\s*'([^']+)'\s*\)/g;
  return [...source.matchAll(pattern)].map((match) => match[1]);
}

/**
 * Directories a generator enumerates.
 *
 * Two shapes are in use, so both are parsed rather than assumed:
 *   readdirSync(resolve(repo, 'internal/icons/svg'))
 *   const sourceDir = resolve(repo, 'internal/icons/svg'); readdirSync(sourceDir)
 */
function directoryInputs(source) {
  const found = new Set();
  const inline = /readdirSync\(\s*resolve\(\s*repo\s*,\s*'([^']+)'\s*\)\s*\)/g;
  for (const match of source.matchAll(inline)) found.add(match[1]);

  const aliases = new Map();
  const assignment = /(?:const|let|var)\s+(\w+)\s*=\s*resolve\(\s*repo\s*,\s*'([^']+)'\s*\)/g;
  for (const match of source.matchAll(assignment)) aliases.set(match[1], match[2]);

  for (const match of source.matchAll(/readdirSync\(\s*(\w+)\s*\)/g)) {
    const target = aliases.get(match[1]);
    if (target) found.add(target);
  }
  return [...found];
}

/** settings-keys reads every non-test file under internal/contracts. */
function contractInputs(source) {
  if (!/readdirSync\(contractsDir\)/.test(source)) return [];
  return ['internal/contracts'];
}

function collectInputs() {
  const files = new Map(); // relative path → sha256
  const directories = new Map(); // relative dir → {sha256, files: {name: sha}}

  for (const generator of ALL_GENERATORS) {
    const scriptPath = resolve(webRoot, generator.script);
    const source = readFileSync(scriptPath, 'utf8');
    // Generator scripts are keyed `web:` (resolved from webRoot) so a baseline
    // captured against an exported tree stays verifiable from the real checkout.
    files.set(`web:${generator.script}`, sha256(scriptPath));

    const literals =
      generator.script === 'scripts/gen-settings-keys.mjs'
        ? contractInputs(source)
        : literalInputs(source);
    const enumerated = directoryInputs(source);
    if (literals.length === 0 && enumerated.length === 0) {
      // Never let a broken parser look like "this generator has no inputs".
      fail(`解析不到 ${generator.script} 的输入。修解析器，不要放宽检查。`);
    }

    for (const input of literals) {
      const absolute = resolve(repoRoot, input);
      if (statSync(absolute).isDirectory()) {
        directories.set(input, describeDirectory(absolute));
      } else {
        files.set(input, sha256(absolute));
      }
    }
    for (const directory of enumerated) {
      const absolute = resolve(repoRoot, directory);
      directories.set(directory, describeDirectory(absolute));
    }
  }
  return { files, directories };
}

function describeDirectory(absolute) {
  const entries = readdirSync(absolute)
    .filter((name) => statSync(join(absolute, name)).isFile())
    .sort();
  const files = {};
  const digest = createHash('sha256');
  for (const name of entries) {
    const hash = sha256(join(absolute, name));
    files[name] = hash;
    digest.update(`${name}:${hash}\n`);
  }
  return { sha256: digest.digest('hex'), files };
}

function collectOutputs() {
  const outputs = {};
  for (const generator of ALL_GENERATORS) {
    for (const output of generator.outputs) {
      const absolute = resolve(webRoot, output);
      outputs[output] = existsSync(absolute) ? sha256(absolute) : null;
    }
  }
  return outputs;
}

function gitState() {
  // The ceiling keeps git from reporting an *enclosing* repository's state: a
  // fixture root nested inside the real checkout must not inherit its dirty
  // tree, and the real root must still see its own.
  const env = { ...process.env, GIT_CEILING_DIRECTORIES: dirname(repoRoot) };
  try {
    const head = execFileSync('git', ['rev-parse', '--short', 'HEAD'], {
      cwd: repoRoot,
      encoding: 'utf8',
      env,
    }).trim();
    const dirty = execFileSync('git', ['status', '--porcelain'], {
      cwd: repoRoot,
      encoding: 'utf8',
      env,
    }).trim();
    return { head, dirty };
  } catch {
    return { head: 'unknown', dirty: '' };
  }
}

function fail(message) {
  console.error(`gen-baseline: ${message}`);
  process.exit(2);
}

function runGenerators() {
  for (const generator of ALL_GENERATORS) {
    const scriptPath = resolve(webRoot, generator.script);
    try {
      execFileSync(process.execPath, [scriptPath], { cwd: webRoot, stdio: 'pipe' });
    } catch (error) {
      fail(`生成器失败: ${generator.script}\n${String(error.stderr ?? error)}`);
    }
  }
}

function capture() {
  const { head, dirty } = gitState();
  if (dirty !== '') {
    // Rule 2: a baseline measured while the tree moves is not a baseline.
    console.error('gen-baseline: 工作树不干净，拒绝采基线。先提交/暂存，让树冻结。');
    console.error(dirty.split('\n').slice(0, 10).join('\n'));
    process.exit(1);
  }

  const inputs = collectInputs();
  const outputs = collectOutputs();
  const baseline = {
    createdAt: new Date().toISOString(),
    // `ZK_BASELINE_HEAD` is for capturing from an exported archive of a commit
    // (`git archive HEAD | tar -x`), where git cannot answer for that tree.
    head: process.env.ZK_BASELINE_HEAD ?? head,
    cleanTree: true,
    // SHA-256 of every file the generators read, plus every enumerated
    // directory (name → hash), so "which input moved?" is answerable.
    inputs: {
      files: Object.fromEntries([...inputs.files].sort()),
      directories: Object.fromEntries(
        [...inputs.directories].map(([dir, value]) => [dir, value.sha256]),
      ),
      directoryFiles: Object.fromEntries(
        [...inputs.directories].map(([dir, value]) => [dir, value.files]),
      ),
    },
    outputs,
  };
  mkdirSync(dirname(baselinePath), { recursive: true });
  writeFileSync(baselinePath, `${JSON.stringify(baseline, null, 2)}\n`);
  console.log(
    `gen-baseline: 已记录 ${Object.keys(baseline.inputs.files).length} 个输入文件、` +
      `${Object.keys(baseline.inputs.directories).length} 个输入目录、` +
      `${Object.keys(baseline.outputs).length} 个生成物（HEAD ${head}）→ ${rel(baselinePath)}`,
  );
}

// verifyWithoutBaseline is the mode a fresh checkout runs: there is no frozen
// baseline to compare against (web/.cache is gitignored, and a committed
// baseline would go stale on every input change), so the check is the one that
// still means something anywhere — re-run the generators and prove the
// committed artefacts are byte-for-byte what they produce.
function verifyWithoutBaseline() {
  const before = collectOutputs();
  runGenerators();
  const after = collectOutputs();
  const problems = [];
  for (const [file, value] of Object.entries(after)) {
    const previous = before[file];
    if (previous === null) {
      problems.push(`生成物缺失（生成器会新写一份）: ${file}`);
      continue;
    }
    if (previous !== value) problems.push(`生成物与生成器不一致（手改过或忘了重跑生成器）: ${file}`);
  }
  if (problems.length > 0) {
    console.error('gen-verify: FAIL（无冻结基线，按"生成物 == 生成器输出"判定）');
    for (const problem of problems) console.error(`  ✗ ${problem}`);
    console.error('  修法: npm run gen:tokens && npm run gen:icons（或对应 gen:*），再提交生成物。');
    process.exit(1);
  }
  console.log('gen-verify: OK（无冻结基线：生成物与生成器输出逐字节一致）');
  for (const file of Object.keys(after)) console.log(`  ✓ ${file}`);
  console.log('  提示: 想额外校验"哪些输入变了"，先 npm run gen:baseline 冻结一份基线。');
}

function verify() {
  if (!existsSync(baselinePath)) {
    verifyWithoutBaseline();
    return;
  }
  const baseline = JSON.parse(readFileSync(baselinePath, 'utf8'));
  const problems = [];
  const notes = [];

  // --- inputs: missing is fatal, changed is reported with the output diff ----
  const changedInputs = [];
  const addedInputs = [];
  for (const [file, expected] of Object.entries(baseline.inputs.files)) {
    const absolute = file.startsWith('web:')
      ? resolve(webRoot, file.slice('web:'.length))
      : resolve(repoRoot, file);
    if (!existsSync(absolute)) {
      problems.push(`输入消失: ${file}`);
      continue;
    }
    if (sha256(absolute) !== expected) changedInputs.push(file);
  }
  for (const [dir, expected] of Object.entries(baseline.inputs.directories)) {
    const absolute = resolve(repoRoot, dir);
    if (!existsSync(absolute)) {
      problems.push(`输入目录消失: ${dir}`);
      continue;
    }
    const expectedFiles = baseline.inputs.directoryFiles?.[dir] ?? {};
    const current = describeDirectory(absolute);
    for (const name of Object.keys(expectedFiles)) {
      if (!(name in current.files)) {
        problems.push(`输入消失: ${dir}/${name}`);
        continue;
      }
      if (current.files[name] !== expectedFiles[name]) changedInputs.push(`${dir}/${name}`);
    }
    // Extra files are legal (a new icon added); only disappearance is fatal.
    const added = Object.keys(current.files).filter((name) => !(name in expectedFiles));
    if (added.length > 0) {
      notes.push(`新增输入文件（合法，但需要重新采基线）: ${added.join(', ')}`);
      addedInputs.push(...added.map((name) => `${dir}/${name}`));
    }
    if (current.sha256 !== expected) {
      // The directory digest changes for additions too, so it is not判断依据 on
      // its own; the per-file checks above are.
    }
  }

  // --- outputs: re-run the generators and compare byte for byte -------------
  runGenerators();
  const outputs = collectOutputs();
  const changedOutputs = [];
  for (const [file, expected] of Object.entries(baseline.outputs)) {
    const actual = outputs[file];
    if (actual === null) {
      problems.push(`生成物缺失: ${file}`);
      continue;
    }
    if (expected !== null && actual !== expected) changedOutputs.push(file);
  }

  if (changedOutputs.length > 0) {
    const cause =
      changedInputs.length > 0
        ? `输入已变: ${changedInputs.join(', ')}`
        : addedInputs.length > 0
          ? `新增输入: ${addedInputs.join(', ')}（产物随之变化，重新采基线即可）`
          : '输入未变但生成物不同（生成器不确定，或解析漏了某个输入）';
    problems.push(`生成物已变: ${changedOutputs.join(', ')} —— ${cause}`);
  } else if (changedInputs.length > 0) {
    notes.push(`输入变了但生成物未变（无用户可见影响）: ${changedInputs.join(', ')}`);
  }

  const lines = [
    `inputs: ${Object.keys(baseline.inputs.files).length} files, ` +
      `${Object.keys(baseline.inputs.directories).length} dirs`,
    `outputs: ${Object.keys(outputs).length}`,
    ...notes,
  ];
  if (problems.length > 0) {
    console.error('gen-verify: FAIL');
    for (const line of lines) console.error(`  ${line}`);
    for (const problem of problems) console.error(`  ✗ ${problem}`);
    process.exit(1);
  }
  console.log(`gen-verify: OK（HEAD 基线 ${baseline.head}）`);
  for (const line of lines) console.log(`  ${line}`);
  for (const file of Object.keys(outputs)) console.log(`  ✓ ${file}`);
}

if (process.argv.includes('--verify')) verify();
else capture();