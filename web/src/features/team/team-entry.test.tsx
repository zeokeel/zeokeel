import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * C6 — the team entry is a discoverability rule, not a data rule
 * (docs/WEB-CLIENT.md §8.3 C6, AGENT-SURFACES S9「零值常显」).
 *
 * The entry is always on screen and never carries a `0` count; opening it at
 * zero shows an empty surface. When the gateway projects the `team.*` domain the
 * list fills in and the entry keeps the same shape.
 */

let gateway: FakeGatewayHandle;

beforeEach(() => {
  gateway = installFakeGateway(defaultState());
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

describe('C6 — team entry', () => {
  it('is always visible and shows no zero count', async () => {
    render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole('textbox');

    // The label follows the settings row (`ui.language`), so wait for the
    // snapshot to land instead of racing the first paint.
    const entry = await screen.findByRole('menuitem', { name: /团队/ });
    expect(entry).toBeInTheDocument();
    // No badge of any kind on this entry — zero is not a number worth showing.
    expect(within(entry).queryByText('0')).toBeNull();
    expect(entry.querySelector('.ant-badge')).toBeNull();
  });

  it('opens an empty surface instead of hiding itself', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole('textbox');

    await user.click(await screen.findByRole('menuitem', { name: /团队/ }));
    expect(await screen.findByText('还没有队友或子任务')).toBeInTheDocument();
    // The entry stays selected: the surface exists, it is simply empty.
    await waitFor(() =>
      expect(screen.getByRole('menuitem', { name: /团队/ })).toHaveClass('ant-menu-item-selected'),
    );
  });
});