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
  'approvals.subscribe': '未实现：客户端目前只用 approvals.snapshot + delta',
  'ask.answer': '未实现：jsdom 测试里提问由 state.ask 直接构造，不经回答命令',
  'ask.cancel': '未实现：同上，取消由测试直接重置 state.ask 而不发命令',
  'ask.subscribe': '未实现：提问用快照 + 广播驱动，没有订阅通道',
  'plan.exit': '未实现：测试直接改 state.plan',
  'plan.subscribe': '同 ask：用快照 + 广播驱动',
  'session.active.get': '未实现：客户端尚未使用',
  'settings.subscribe': '未实现：settings.snapshot + delta 已覆盖',
};

/**
 * Params the **mock** accepts but does not act on: its reply is `{}` regardless.
 * Registered separately from the fake gateway because these are the mock's own
 * no-op handlers — including `notify.action`, i.e. the toast's undo button has no
 * counterpart in the mock at all.
 */
export const MOCK_IGNORED_PARAMS: Record<string, Record<string, string>> = {
  initialize: {
    clientName: 'mock 不校验握手字段（真网关会按契约校验并拒绝非法值）',
    clientVersion: '同上：握手字段只做形状占位',
    protocolVersion: '同上：mock 固定按当前协议回复',
    capabilities: '同上：客户端声明的能力不参与 mock 的任何判断',
  },
  'ask.answer': {
    id: 'mock 的 no-op 处理器：即使有提问也直接回 {}，不落库',
    answer: '同上：回答内容被忽略，提问状态不会前进',
  },
  'ask.cancel': {
    id: 'mock 的 no-op 处理器：不回写提问状态',
    answer: '同上：这个字段在 cancel 语义下本就多余，一并忽略',
  },
  'plan.exit': { action: 'mock 的 no-op 处理器：不改变计划状态' },
  'notify.action': {
    id: 'mock 的 no-op 处理器：toast 的撤销按钮没有对应动作实现',
    actionId: '同上：动作标识被忽略，撤销不会真的发生',
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
    // 9 → 8: `notify.action` is implemented (task-38), and `task.delete` now
    // offers a real undo through it.
    expect(Object.keys(UNIMPLEMENTED_METHODS).length).toBeLessThanOrEqual(8);
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