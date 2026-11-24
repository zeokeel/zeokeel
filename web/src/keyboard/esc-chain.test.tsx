import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * E1–E9 of the layered Escape arbiter (docs/WEB-CLIENT.md §4.3).
 *
 * These are the regressions the native UI shipped as a P0: Esc silently doing
 * nothing because focus was not where the handler was registered. Every case
 * therefore drives a *real* keydown on `document` with focus somewhere other
 * than the component that owns the meaning, and asserts the outcome — not the
 * presence of a handler.
 */

interface Harness {
  resolved: Array<{ id: string; decision: string }>;
  abortedStreams: number;
}

let harness: Harness;
let gateway: FakeGatewayHandle;

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });

const approval = (overrides: Record<string, unknown> = {}) => ({
  id: 'appr-1',
  tool: 'bash',
  summary: '运行构建脚本',
  sessionId: 's-1',
  arguments: '{"command":"npm run build"}',
  createdAt: '2026-11-23T00:00:00Z',
  expiresAt: '2999-01-01T00:00:00Z',
  ...overrides,
});

/** An SSE response that stays open until the client aborts it (a running turn). */
function openStream(signal: AbortSignal | null | undefined): Response {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(
        encoder.encode('event: reasoning.delta\ndata: {"turnId":"t","text":"想想…"}\n\n'),
      );
      signal?.addEventListener('abort', () => {
        harness.abortedStreams += 1;
        controller.error(new DOMException('Aborted', 'AbortError'));
      });
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

beforeEach(() => {
  harness = { resolved: [], abortedStreams: 0 };
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [] };
  state.approvals = [approval() as never];
  // R7's child route now renders a teammate's detail (the team backend replaced the
  // mock-only child-session endpoint); the member carries the text the test asserts.
  state.teamMembers = [
    {
      id: 'child-1',
      name: '研究员',
      status: 'active',
      runState: 'idle',
      model: 'fake-model',
      last: '子会话里的话',
      error: '',
    },
  ];
  state.teamActivities = [
    { id: 'act-1', memberId: 'child-1', kind: 'spawn', text: '派发研究员', at: '2026-11-23T00:00:00Z' },
  ];
  gateway = installFakeGateway(state);
  // `approval.resolve` is a WS command now; record what the card decided.
  const originalReceive = gateway.gateway.receive.bind(gateway.gateway);
  gateway.gateway.receive = (socket, message) => {
    const params = (message.params ?? {}) as { id?: unknown; decision?: unknown };
    if (message.method === 'approval.resolve') {
      harness.resolved.push({
        id: String(params.id ?? ''),
        decision: String(params.decision ?? ''),
      });
    }
    originalReceive(socket, message);
  };

  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);

    if (url.endsWith('/api/sessions/s-1/children/child-1')) {
      return json({
        id: 'child-1',
        parentId: 's-1',
        title: '子代理',
        messages: [{ id: 'm1', role: 'user', text: '子会话里的话' }],
      });
    }
    if (url.endsWith('/api/files/tree')) {
      return json({
        root: '/data/mygo',
        nodes: [
          { key: 'README.md', path: 'README.md', title: 'README.md', isLeaf: true },
        ],
      });
    }
    if (url.includes('/api/files/content')) {
      return json({ path: 'README.md', text: '# web', truncated: false });
    }
    if (url.includes('/api/chat/run')) return openStream(init?.signal);
    return new Response('not found', { status: 404 });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

async function renderAt(path: string) {
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

/** Dispatch an Esc and let React commit whatever the claim did. */
async function pressEscapeAndFlush(options: { repeat?: boolean } = {}) {
  let event: KeyboardEvent | undefined;
  await act(async () => {
    event = pressEscape(options);
  });
  return event!;
}

/** A real keydown on document, cancelable so `defaultPrevented` is meaningful. */
function pressEscape(options: { repeat?: boolean } = {}) {
  const event = new KeyboardEvent('keydown', {
    key: 'Escape',
    bubbles: true,
    cancelable: true,
    repeat: options.repeat ?? false,
  });
  document.dispatchEvent(event);
  return event;
}

async function waitForApprovalCard() {
  return screen.findAllByText('需要审批');
}

/**
 * The approval queue polls, so "no approvals" must be observed *after* the
 * first poll has landed: wait for the card, then clear the fixture and wait for
 * the next poll to drop it. Clearing first would race the in-flight response.
 */
async function clearApprovalsAndWait() {
  await screen.findAllByText('需要审批');
  gateway.gateway.state.approvals = [];
  gateway.gateway.broadcastApprovals([], ['appr-1']);
  await waitFor(() => expect(screen.queryAllByText('需要审批')).toHaveLength(0), {
    timeout: 4000,
  });
  await waitFor(() => expect(screen.getByRole('textbox')).not.toBeDisabled());
}

/** The sidebar's creation row, which focus can sit on without leaving the screen. */
function sidebarButton(): HTMLElement {
  const label = screen.getByText('新建会话');
  const button = label.closest('button');
  if (!button) throw new Error('creation row is not a button');
  return button;
}

describe('E1–E9 layered Escape arbiter', () => {
  it('E1: denies the oldest approval while focus is in the sidebar', async () => {
    await renderAt('/c/s-1');
    await waitForApprovalCard();

    const sidebar = sidebarButton();
    sidebar.focus();
    expect(document.activeElement).toBe(sidebar);

    const event = pressEscape();
    await waitFor(() => expect(harness.resolved).toEqual([{ id: 'appr-1', decision: 'deny' }]));
    expect(event.defaultPrevented).toBe(true);
  });

  it('E2: stops the running turn when nothing owns focus', async () => {
    const user = await renderAt('/c/s-1');
    await clearApprovalsAndWait();
    const textbox = await screen.findByRole('textbox');
    await user.click(textbox);
    await user.keyboard('开始{Enter}');
    await waitFor(() => expect(harness.abortedStreams).toBe(0));

    (document.activeElement as HTMLElement | null)?.blur();
    pressEscape();
    await waitFor(() => expect(harness.abortedStreams).toBe(1));
  });

  it('E3: an approval outranks stopping the run, even from the composer', async () => {
    const user = await renderAt('/c/s-1');
    await waitForApprovalCard();
    const textbox = await screen.findByRole('textbox');
    await user.click(textbox);

    pressEscape();
    await waitFor(() => expect(harness.resolved).toHaveLength(1));
    expect(harness.resolved[0]?.decision).toBe('deny');
    expect(harness.abortedStreams).toBe(0);
  });

  it('E4: the slash menu closes first and the run keeps going', async () => {
    const user = await renderAt('/c/s-1');
    await clearApprovalsAndWait();
    const textbox = await screen.findByRole('textbox');
    await user.click(textbox);

    await user.type(textbox, '开始{Enter}');
    // The composer is disabled while an approval is pending; it is gone here.
    await user.type(textbox, '/');
    expect(await screen.findByRole('listbox', { name: '斜杠命令' })).toBeInTheDocument();

    pressEscape();
    await waitFor(() =>
      expect(screen.queryByRole('listbox', { name: '斜杠命令' })).not.toBeInTheDocument(),
    );
    expect(harness.abortedStreams).toBe(0);

    // The run was still live: the next Esc stops it.
    pressEscape();
    await waitFor(() => expect(harness.abortedStreams).toBe(1));
  });

  it('E5: a dirty settings page opens the discard guard and does not close', async () => {
    const user = await renderAt('/settings/model');
    // The model catalog is a list editor now, so the dirtyable field on this page
    // is the system prompt (same page, same guard).
    const field = await screen.findByLabelText('系统提示词');
    await user.clear(field);
    await user.type(field, 'changed');

    pressEscape();
    expect(await screen.findByText('放弃未保存的更改？')).toBeInTheDocument();
    // The page is still there behind the guard.
    expect(screen.getByRole('heading', { name: '模型' })).toBeInTheDocument();
  });

  it('E6: a child session returns to the parent and does not deny its approval', async () => {
    await renderAt('/c/s-1/child/child-1');
    expect(await screen.findByText('子会话里的话')).toBeInTheDocument();

    pressEscape();
    // Back on the thread: the composer is present again.
    await waitFor(() => expect(screen.getByRole('textbox')).toBeInTheDocument());
    expect(harness.resolved).toHaveLength(0);
  });

  it('E7: with the palette open over a dirty settings page, Esc closes the palette only', async () => {
    const user = await renderAt('/settings/model');
    // The model catalog is a list editor now, so the dirtyable field on this page
    // is the system prompt (same page, same guard).
    const field = await screen.findByLabelText('系统提示词');
    await user.clear(field);
    await user.type(field, 'changed');

    await user.keyboard('{Meta>}k{/Meta}');
    expect(await screen.findByText('命令面板')).toBeInTheDocument();

    // The overlay level wins: this Esc closes the palette and must NOT reach
    // the dirty guard. antd keeps the dialog markup around while its close
    // animation runs, so the assertion is on the *effect*, not on the DOM:
    const first = await pressEscapeAndFlush();
    expect(first.defaultPrevented).toBe(true);
    expect(screen.queryByText('放弃未保存的更改？')).not.toBeInTheDocument();

    // A second Esc proves the palette is gone and the guard is next in line.
    await pressEscapeAndFlush();
    expect(await screen.findByText('放弃未保存的更改？')).toBeInTheDocument();
  });

  it('opens the find bar with Mod+F and the inspector with Mod+Alt+F (K11, K9)', async () => {
    const user = await renderAt('/c/s-1');
    await clearApprovalsAndWait();

    await user.keyboard('{Meta>}f{/Meta}');
    expect(await screen.findByRole('search', { name: '在会话中查找' })).toBeInTheDocument();

    // Esc closes the find bar (L2) and hands focus back to the composer.
    await pressEscapeAndFlush();
    expect(screen.queryByRole('search', { name: '在会话中查找' })).not.toBeInTheDocument();

    await user.keyboard('{Meta>}{Alt>}f{/Alt}{/Meta}');
    expect(await screen.findByRole('complementary', { name: '检查器' })).toBeInTheDocument();
  });

  it('E8: with nothing active, Esc is not consumed (the browser keeps it)', async () => {
    await renderAt('/c/s-1');
    await clearApprovalsAndWait();
    const textbox = await screen.findByRole('textbox');
    expect(textbox).toHaveValue('');

    const event = pressEscape();
    expect(event.defaultPrevented).toBe(false);
  });

  it('E9: holding Esc fires once (auto-repeat is ignored)', async () => {
    await renderAt('/c/s-1');
    await waitForApprovalCard();

    pressEscape({ repeat: true });
    // The arbiter drops repeats synchronously, so a microtask flush is enough —
    // no real sleep (that is what makes a negative assertion flaky).
    await Promise.resolve();
    expect(harness.resolved).toHaveLength(0);

    // A real press after the repeat still works.
    pressEscape();
    await waitFor(() => expect(harness.resolved).toHaveLength(1));
  });
});