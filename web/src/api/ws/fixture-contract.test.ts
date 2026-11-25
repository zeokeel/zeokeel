import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { WS_METHODS, WS_PARAMS } from '@/design/ws-contract';

/**
 * Fixture ↔ contract (task-34, after `editMessageId` was silently dropped).
 *
 * All our alignment assertions asked "does the *client* match the contract?".
 * None asked "does the *stand-in* match the contract?", which is why a mock that
 * destructured `{sessionId, text, regenerate}` — and therefore ignored an edit —
 * kept 200+ tests green.
 *
 * Two directions, and one registration discipline:
 *  - every method's contract fields must be **read** by the mock;
 *  - a field or method a fixture deliberately does not implement must be
 *    **registered with a reason**, and the lists may only shrink.
 */

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '../../../..');
const mockSource = readFileSync(resolve(repoRoot, 'web/dev/mock-server.mjs'), 'utf8');
const fakeSource = readFileSync(resolve(repoRoot, 'web/src/test/fake-gateway.ts'), 'utf8');

/** What the client actually sends (`CLIENT_METHODS` in the alignment test). */
const alignmentSource = readFileSync(resolve(here, './alignment.test.ts'), 'utf8');
const CLIENT_METHODS = [
  ...(alignmentSource.match(/const CLIENT_METHODS = \[([\s\S]*?)\n\];/)?.[1] ?? '').matchAll(
    /'([^']+)'/g,
  ),
].map((match) => match[1]);

/**
 * Fields the **mock** deliberately does not read, with the reason. Each entry
 * must carry one — a silent entry would be the fourth hand-written list that
 * drifts.
 */
export const UNIMPLEMENTED_PARAMS: Record<string, Record<string, string>> = {
  'session.snapshot': {
    beforeMessageId: '未实现功能：历史分页，客户端不发（谁开始发谁先实现）',
    tail: '未实现功能：尾巴窗口大小，客户端不发（谁开始发谁先实现）',
  },
};

/**
 * Methods the **fake gateway** does not implement, with the reason. Registered so
 * "the client starts using it" fails here instead of degrading silently.
 */
export const UNIMPLEMENTED_METHODS: Record<string, string> = {
};

/**
 * Params the **mock** accepts but does not act on: its reply is `{}` regardless.
 * Registered separately from the fake gateway because these are the mock's own
 * no-op handlers — including `notify.action`, i.e. the toast's undo button has no
 * counterpart in the mock at all.
 */
export const MOCK_IGNORED_PARAMS: Record<string, Record<string, string>> = {
  'ask.cancel': {
    answer: '契约里 cancel 也带 answer，但取消没有答案可写；网关同样忽略它',
  },
  'inbox.dispatch': {
    note: '任务板没有分诊备注字段；真网关把 note 存进分诊记录，模板只建任务',
  },
  'inbox.triage': {
    note: '同上：mock 只改状态，不保存分诊备注',
  },
  initialize: {
    clientName: 'mock 不校验握手字段（真网关会按契约校验并拒绝非法值）',
    clientVersion: '同上：握手字段只做形状占位',
    protocolVersion: '同上：mock 固定按当前协议回复',
    capabilities: '同上：客户端声明的能力不参与 mock 的任何判断',
  },

};

/**
 * Fields read through a helper rather than `params.x` — the scan's false
 * positives, named with the helper so this cannot become a blanket exemption.
 */
const READ_VIA_HELPER: Record<string, Record<string, string>> = {
  'task.status': { sessionId: 'this.taskKey(params)' },
  'task.delete': { sessionId: 'this.taskKey(params)' },
  'tasks.snapshot': { sessionId: 'this.taskKey(params)' },
  'tasks.subscribe': { sessionId: 'this.taskKey(params)' },
};

function mockHandles(method: string): string | null {
  const index = mockSource.indexOf(`case '${method}'`);
  if (index < 0) return null;
  return mockSource.slice(index, index + 2_500);
}

function contractFields(method: string): string[] {
  const rule = (WS_PARAMS as Record<string, { required: readonly string[]; optional: readonly string[] }>)[
    method
  ];
  return [...(rule?.required ?? []), ...(rule?.optional ?? [])];
}

describe('registration lists stay honest', () => {
  it('gives every registered gap a reason', () => {
    for (const [method, fields] of Object.entries(UNIMPLEMENTED_PARAMS)) {
      for (const [field, reason] of Object.entries(fields)) {
        expect(reason.trim().length, `${method}.${field} 缺少原因`).toBeGreaterThan(4);
      }
    }
    for (const [method, reason] of Object.entries(UNIMPLEMENTED_METHODS)) {
      expect(reason.trim().length, `${method} 缺少原因`).toBeGreaterThan(4);
    }
  });

  it('gives every mock-ignored param a reason', () => {
    for (const [method, fields] of Object.entries(MOCK_IGNORED_PARAMS)) {
      for (const [field, reason] of Object.entries(fields)) {
        expect(reason.trim().length, `${method}.${field} 缺少原因`).toBeGreaterThan(4);
        expect(contractFields(method), `${method}.${field} 不在契约里`).toContain(field);
      }
    }
  });

  it('registers only fields the contract actually declares', () => {
    for (const [method, fields] of Object.entries(UNIMPLEMENTED_PARAMS)) {
      const declared = contractFields(method);
      for (const field of Object.keys(fields)) {
        expect(declared, `${method}.${field} 不在契约里`).toContain(field);
      }
    }
  });

  it('mentions every registered gap in the fixture README section', () => {
    // "The list may grow only together with the documentation": a new entry must
    // appear in `web/README.md`, or this assertion is where the growth stops.
    const readme = readFileSync(resolve(repoRoot, 'web/README.md'), 'utf8');
    for (const [method, fields] of Object.entries(UNIMPLEMENTED_PARAMS)) {
      for (const field of Object.keys(fields)) {
        expect(readme, `README 未提到 ${method}.${field}`).toContain(`${method}.${field}`);
      }
    }
    for (const method of Object.keys(UNIMPLEMENTED_METHODS)) {
      expect(readme, `README 未提到 ${method}`).toContain(method);
    }
  });

  it('does not let the lists grow silently', () => {
    // A count is the cheapest way to make growth a deliberate act: raise it *and*
    // update the README in the same change.
    // Shrunk from 3 to 1 when the gateway dropped the phantom `sinceRev` fields:
    // the registry follows the contract down, which is the point of it.
    expect(Object.keys(UNIMPLEMENTED_PARAMS).length).toBeLessThanOrEqual(1);
    // 9 → 8 → 4: `notify.action` (with the delete/undo loop) and the four global
    // subscribes are implemented; each removal is forced by the "handled methods
    // must not stay registered" assertion below.
    // Empty: every method the client sends is implemented by the fake. Growth is
    // still blocked — a new entry has to justify itself and update the README.
    expect(Object.keys(UNIMPLEMENTED_METHODS).length).toBeLessThanOrEqual(0);
  });
});

describe('the mock reads what the contract declares', () => {
  it.each(WS_METHODS.filter((method) => mockHandles(method) !== null))(
    '%s',
    (method) => {
      const body = mockHandles(method)!;
      const unread = contractFields(method).filter((field) => {
        if (UNIMPLEMENTED_PARAMS[method]?.[field]) return false;
        if (MOCK_IGNORED_PARAMS[method]?.[field]) return false;
        return !new RegExp(`params\\.${field}\\b|params\\[['"]${field}['"]\\]`).test(body);
      });
      expect(unread, `${method} 的 mock 分支没有读取这些契约字段`).toEqual([]);
    },
  );
});

describe('the fake gateway is not looser than the client needs', () => {
  it('handles or registers every method the client sends', () => {
    const handled = new Set(
      [...fakeSource.matchAll(/method === '([a-z][A-Za-z0-9.]*)'/g)].map((match) => match[1]),
    );
    const registered = new Set(Object.keys(UNIMPLEMENTED_METHODS));
    const unhandled = CLIENT_METHODS.filter(
      (method) => !handled.has(method) && !registered.has(method),
    );
    expect(unhandled, '客户端在用、fake 不实现也没登记').toEqual([]);
  });

  it('has no registration for a method it actually handles', () => {
    const handled = new Set(
      [...fakeSource.matchAll(/method === '([a-z][A-Za-z0-9.]*)'/g)].map((match) => match[1]),
    );
    const stale = Object.keys(UNIMPLEMENTED_METHODS).filter((method) => handled.has(method));
    expect(stale, '已实现却还登记为未实现').toEqual([]);
  });

  it('reads the fields the helper exceptions claim', () => {
    for (const [method, fields] of Object.entries(READ_VIA_HELPER)) {
      const index = fakeSource.indexOf(`method === '${method}'`);
      expect(index, `fake 没有处理 ${method}`).toBeGreaterThan(-1);
      const body = fakeSource.slice(index, index + 1_200);
      for (const [field, helper] of Object.entries(fields)) {
        expect(body, `${method}.${field} 没有经 ${helper} 读取`).toContain(helper);
      }
    }
  });
});