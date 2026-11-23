import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * The mock's run endpoint must read `editMessageId` (`task-31`).
 *
 * It used to destructure `{sessionId, text, regenerate}` only, so an edit was
 * silently served as an ordinary send: green tests, no feature. This assertion
 * is about the **shape the mock accepts**, not about behaviour (the integration
 * file covers behaviour) — it fails the moment the field stops being read.
 */
const here = dirname(fileURLToPath(import.meta.url));
const mock = readFileSync(resolve(here, '../../dev/mock-server.mjs'), 'utf8');

describe('mock run endpoint', () => {
  it('destructures editMessageId from the body', () => {
    const destructure = /const\s*\{[^}]*editMessageId[^}]*\}\s*=\s*body/.exec(mock);
    expect(destructure, 'mock 的 run 端点没有解构 editMessageId').toBeTruthy();
  });

  it('knows the five edit refusals by their sentinel names', () => {
    for (const name of [
      'edit_target_not_found',
      'edit_target_not_user',
      'edit_target_has_attachments',
      'edit_target_compacted',
      'edit_target_moved',
    ]) {
      expect(mock, `mock 缺少拒绝对应 ${name}`).toContain(name);
    }
  });

  it('truncates the tail when an edit replaces a message', () => {
    // The replacement and the splice are one decision: an edit rewrites history.
    expect(mock).toMatch(/row\.text\s*=\s*text/);
    expect(mock).toMatch(/splice\(editIndex \+ 1\)/);
  });
});
