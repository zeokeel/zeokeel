import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '@/app';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * P1/P2 acceptance items that had no regression yet (docs/WEB-CLIENT.md §8.2/§8.3):
 *   B2  Esc inside settings closes the page; a dirty page guards first
 *   B3  the settings search locates the field, not just the page
 *   C1  two clicks on 新建会话 inside 600ms create one session
 *   C3  every palette command is reachable by typing its title
 *   B16 Mod+B folds the sidebar and expanding keeps the open conversation
 *
 * B7/B8 (narrow windows) and C2 (selection tint) are CSS, checked by the
 * Playwright run: jsdom has no layout, so a test here would assert nothing.
 */

let gateway: FakeGatewayHandle;

beforeEach(() => {
  const state = defaultState();
  state.sessions = [
    { id: 's-1', title: '会话一', updatedAt: '2026-11-23T00:00:00Z' },
    { id: 's-2', title: '会话二', updatedAt: '2026-11-23T00:00:00Z' },
  ];
  state.messages = { 's-1': [], 's-2': [] };
  gateway = installFakeGateway(state);
  vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  gateway.dispose();
});

function renderAt(path: string) {
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
  return user;
}

async function pressEscape() {
  const event = new KeyboardEvent('keydown', {
    key: 'Escape',
    bubbles: true,
    cancelable: true,
  });
  document.dispatchEvent(event);
  return event;
}

describe('B2 — Esc leaves a clean settings page, guards a dirty one', () => {
  it('closes the page when nothing was edited', async () => {
    renderAt('/settings/appearance');
    await screen.findByRole('heading', { name: '外观' });

    const event = await pressEscape();
    // The claim handled it (so the browser does not also react) …
    expect(event.defaultPrevented).toBe(true);
    // … and the page is gone.
    await waitFor(() => expect(screen.queryByRole('heading', { name: '外观' })).toBeNull());
  });

  it('shows the guard instead of closing once a field changed', async () => {
    const user = renderAt('/settings/appearance');
    const field = await screen.findByLabelText('对话字号');
    await user.clear(field);
    await user.type(field, '16');

    await pressEscape();
    expect(await screen.findByText('放弃未保存的更改？')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '外观' })).toBeInTheDocument();
  });
});

describe('B3 — the settings search locates a field', () => {
  it('finds a field by its label and jumps to its page with a focus marker', async () => {
    const user = renderAt('/settings/model');
    const search = await screen.findByLabelText('搜索设置页');

    await user.type(search, '主题');
    // The hit is the 外观 page (it owns the 主题 radio), not every page.
    const item = await screen.findByRole('menuitem', { name: /外观/ });
    await user.click(item);

    const heading = await screen.findByRole('heading', { name: '外观' });
    expect(heading).toBeInTheDocument();
    // The radio group's label is not a form control, so locate the row by its
    // label element (the section title is also called 主题) and assert the
    // row-level marker the page sets.
    const label = document.querySelector('label.zk-field__label[for="zk-field-ui.theme.mode"]');
    expect(label).toBeTruthy();
    const row = label?.closest('.zk-field') as HTMLElement;
    expect(row).toBeTruthy();
    expect(row.dataset.fieldKey).toBe('ui.theme.mode');
    expect(row.dataset.focused).toBe('true');
  });
});

describe('C1 — double-clicking 新建会话 creates one session', () => {
  it('treats two clicks inside 600ms as one action', async () => {
    // The window is proven against a controlled clock, not against wall time:
    // two `user.click`s can straddle 600ms on a loaded machine, which is exactly
    // the "real time proving time behaviour" flake this avoids.
    let now = 1_000;
    const clock = vi.spyOn(Date, 'now').mockImplementation(() => now);

    const user = renderAt('/');
    const button = await screen.findByRole('button', { name: '新建会话' });

    await user.click(button);
    now += 120; // a fast second click, well inside the window
    await user.click(button);

    const creates = () =>
      gateway.gateway.methodsCalled().filter((name) => name === 'session.create');
    await waitFor(() => expect(creates()).toHaveLength(1));

    // Past the window the guard releases: the next click creates again.
    now += 600;
    await user.click(button);
    await waitFor(() => expect(creates()).toHaveLength(2));

    clock.mockRestore();
  });
});

describe('C3 — every palette command is searchable', () => {
  it('reaches each command by typing its title', async () => {
    const user = renderAt('/');
    await screen.findByRole('textbox');

    await user.keyboard('{Meta>}k{/Meta}');
    const dialog = await screen.findByText('命令面板');

    // Scope to the palette listbox: the sidebar ships a 新建会话 button too.
    const palette = document.querySelector('[role="listbox"][aria-label="命令"]') as HTMLElement;
    expect(palette).toBeTruthy();

    for (const title of ['新建会话', '前往 收件箱', '前往 设置']) {
      const input = screen.getByLabelText('命令搜索');
      await user.clear(input);
      await user.type(input, title.replace('前往 ', ''));
      const options = within(
        document.querySelector('[role="listbox"][aria-label="命令"]') as HTMLElement,
      ).getAllByRole('option');
      expect(options.some((option) => option.textContent?.includes(title))).toBe(true);
    }
    expect(dialog).toBeInTheDocument();
  });
});

describe('B16 — Mod+B folds the sidebar without losing the open conversation', () => {
  it('collapses to a rail and restores the active session on expand', async () => {
    const user = renderAt('/c/s-1');
    await screen.findByRole('textbox');
    const sidebar = document.querySelector('.zk-sidebar') as HTMLElement;
    expect(sidebar.dataset.collapsed).toBe('false');

    await user.keyboard('{Meta>}b{/Meta}');
    await waitFor(() => expect(sidebar.dataset.collapsed).toBe('true'));
    // The rail keeps one dot per session and marks the open one.
    const dots = sidebar.querySelectorAll('.zk-sidebar__rail-dot');
    expect(dots).toHaveLength(2);
    expect(sidebar.querySelector('[data-active="true"]')).toBeTruthy();

    await user.keyboard('{Meta>}b{/Meta}');
    await waitFor(() => expect(sidebar.dataset.collapsed).toBe('false'));
    // Expanding must not have reset the conversation.
    expect(await screen.findByRole('textbox')).toBeInTheDocument();
    expect(screen.getAllByText('会话一').length).toBeGreaterThan(0);
  });
});