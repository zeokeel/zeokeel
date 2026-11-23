import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { applyContextDelta, EMPTY_CONTEXT, normalizeContext, windowUsage } from '@/api/context';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * B6 — the 上下文 tab (docs/TRANSPORT.md §5.1.9).
 *
 * Three rules, each with a test that fails if someone "improves" it:
 *  1. the tab exists **only** when `initialize` advertised `context`;
 *  2. `found:false` is an honest empty state — no zeros, no gauges;
 *  3. `context.delta` **replaces** the whole view; merging fields would invent a
 *     view the backend never produced.
 */

let gateway: FakeGatewayHandle | null = null;

beforeEach(() => {
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway?.dispose();
  gateway = null;
});

const view = () => ({
  revision: 7,
  snapshot: {
    model: 'fakemodel/tier-1',
    window: 200_000,
    systemPromptTokens: 1820,
    systemPromptBytes: 7280,
    sections: [{ id: 'agents-md', label: 'AGENTS.md', bytes: 5120, tokens: 1280 }],
    tools: [],
    history: { messages: 2, user: 1, assistant: 1, tool: 0 },
    injected: {},
    estimatedPrompt: 1820,
  },
  requests: [],
  events: [],
  used: 2140,
  window: 200_000,
  costKnown: false,
});

function renderAt(path: string, contexts: Record<string, unknown | null> = {}, caps?: string[]) {
  const state = defaultState();
  state.sessions = [{ id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' }];
  state.messages = { 's-1': [] };
  state.contexts = { 's-1': view(), ...contexts };
  if (caps) state.capabilities = caps;
  gateway = installFakeGateway(state);
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

const WITH_CONTEXT = [
  'session.list',
  'session.snapshot',
  'session.delta',
  'session.subscribe',
  'session.create',
  'approvals',
  'ask',
  'plan',
  'artifacts',
  'settings',
  'settings.set',
  'workspace',
  'run.cancel',
  'prompts',
  'foreground',
  'context',
];

const WITHOUT_CONTEXT = WITH_CONTEXT.filter((name) => name !== 'context');

describe('context model', () => {
  it('treats a delta as a replacement, not a merge', () => {
    const previous = normalizeContext({
      epoch: 'e',
      rev: 1,
      sessionId: 's',
      found: true,
      context: view(),
    });
    const replaced = applyContextDelta(previous, {
      found: true,
      context: { ...view(), revision: 8, used: 999 },
    });
    expect(replaced.context?.revision).toBe(8);
    expect(replaced.context?.used).toBe(999);

    // A delta that says "not found" clears the view; it does not keep the old one.
    expect(applyContextDelta(replaced, { found: false })).toEqual(EMPTY_CONTEXT);
  });

  it('reports no usage ratio when the window is unknown', () => {
    expect(windowUsage(null)).toBeNull();
    expect(windowUsage({ ...view(), window: 0 } as never)).toBeNull();
    expect(windowUsage(view() as never)).toBeCloseTo(2140 / 200_000);
  });
});

describe('B6 — the context tab', () => {
  it('does not render the tab when the gateway did not advertise context', async () => {
    renderAt('/c/s-1', {}, WITHOUT_CONTEXT);
    await screen.findByRole('textbox');
    expect(screen.queryByRole('tab', { name: '上下文' })).toBeNull();
  });

  it('renders the tab and the folded view when it is advertised', async () => {
    renderAt('/c/s-1', {}, WITH_CONTEXT);
    const tab = await screen.findByRole('tab', { name: '上下文' });
    expect(tab).toBeInTheDocument();

    tab.focus();
    (tab as HTMLElement).click();
    // The screen fetches on mount; wait for the section table to fill in.
    await waitFor(() => expect(document.querySelector('.zk-context__sections')).not.toBeNull());
    expect(await screen.findByText('AGENTS.md')).toBeInTheDocument();
    expect(screen.getByText('#7')).toBeInTheDocument();
    // The copy never claims to be live — the gateway folds every 2s.
    expect(screen.getByText(/每 2 秒重新折叠/)).toBeInTheDocument();
  });

  it('shows an honest empty state for found:false, never a zero', async () => {
    renderAt('/c/s-1/context', { 's-1': null }, WITH_CONTEXT);
    expect(await screen.findByText('还没有上下文记录')).toBeInTheDocument();
    // No invented numbers: no ratio, no percent.
    expect(screen.queryByText(/\d+\s*\/\s*\d+/)).toBeNull();
    expect(screen.queryByText(/%/)).toBeNull();
  });

  it('re-renders from a pushed delta (whole-view replacement)', async () => {
    renderAt('/c/s-1/context', {}, WITH_CONTEXT);
    await waitFor(() => expect(document.querySelector('.zk-context__sections')).not.toBeNull());
    await screen.findByText('AGENTS.md');

    gateway?.gateway.notify('context.delta', {
      sessionId: 's-1',
      baseRev: 1,
      rev: 2,
      found: true,
      context: {
        ...view(),
        revision: 9,
        snapshot: {
          ...view().snapshot,
          sections: [{ id: 'memory', label: 'MEMORY.md', bytes: 100, tokens: 20 }],
        },
      },
    });

    await waitFor(() => expect(screen.getByText('MEMORY.md')).toBeInTheDocument());
    // The previous section list is gone: a replacement, not an append.
    expect(screen.queryByText('AGENTS.md')).toBeNull();
    expect(screen.getByText('#9')).toBeInTheDocument();
  });
});