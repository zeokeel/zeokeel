import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { App } from '@/app';
import { I18nProvider } from '@/i18n';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * M6 · the roster (`users.snapshot` / `users.upsert` / `users.act`).
 *
 * These tests pin what makes the surface honest:
 *  1. the list is the gateway's own answer — name, id, role tag, created stamp —
 *     and the acting member is visibly marked;
 *  2. the screen says plainly that roles are a **policy table, not sign-in**;
 *     the five-row legend matches `contracts.Allowed` (roles decide dispatch,
 *     approve, manage bots, review memory and budget — nothing finer);
 *  3. a create sends exactly the three fields the contract requires, and a role
 *     change sends the **whole row** (there is no patch shape for a member);
 *  4. switching identity sends exactly `{id}` and then re-reads, so the marker
 *     on screen is the gateway's answer rather than the request's echo;
 *  5. one write is in flight at a time; a failed write keeps the list and prints
 *     the gateway's own words (the last-owner refusal is the case that matters);
 *  6. an empty roster is 还没有成员。, not an error and not a blank box;
 *  7. `/settings/users` is registered as a route and reached from its own
 *     sidebar entry, which stays selected while the screen is open.
 *
 * No timers are faked: the screen has no polling, so every assertion is about
 * one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  loadUsers: vi.fn(),
  upsertUser: vi.fn(),
  actAsUser: vi.fn(),
}));

vi.mock('@/api/users', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/users')>();
  // Keep the real types/constants (the role list drives the tag and the legend);
  // only the requests are replaced.
  return { ...actual, ...api };
});

import type { User, UsersSnapshot } from '@/api/users';
import { UsersScreen } from './UsersScreen';

function member(over: Partial<User> = {}): User {
  return {
    id: 'owner-1',
    name: '小明',
    role: 'owner',
    createdAt: '2026-11-01T00:00:00Z',
    ...over,
  };
}

/** The owner is acting; alice joined two minutes ago; bob only dispatches. */
function roster(over: Partial<UsersSnapshot> = {}): UsersSnapshot {
  return {
    users: [
      member(),
      member({
        id: 'alice',
        name: '阿丽',
        role: 'manager',
        createdAt: new Date(Date.now() - 2 * 60_000).toISOString(),
      }),
      member({ id: 'bob', name: '小博', role: 'collaborator', createdAt: undefined }),
    ],
    actingUserId: 'owner-1',
    actingRole: 'owner',
    ...over,
  };
}

function renderScreen() {
  return render(
    <I18nProvider>
      <UsersScreen />
    </I18nProvider>,
  );
}

/** Open an antd Select and return its options, so one can be picked by text. */
function optionsOf(select: HTMLElement): HTMLElement[] {
  fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
  return Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option'));
}

async function pickOption(
  user: ReturnType<typeof userEvent.setup>,
  select: HTMLElement,
  text: string,
) {
  await user.click(select.querySelector('.ant-select-selector') ?? select);
  const option = optionsOf(select).find((candidate) => candidate.textContent === text);
  expect(option, `option ${text} not found`).toBeDefined();
  fireEvent.click(option as HTMLElement);
}

beforeEach(() => {
  // jsdom reports en-US; this suite asserts the Chinese copy, so pin the browser
  // language the way a Chinese user's browser would report it.
  vi.stubGlobal('navigator', { ...navigator, language: 'zh-CN', languages: ['zh-CN'] });
  Object.values(api).forEach((fn) => fn.mockReset());
  api.loadUsers.mockResolvedValue(roster());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('UsersScreen — the roster', () => {
  it('renders every member with its id, its role tag and its created stamp', async () => {
    renderScreen();

    const owner = await screen.findByTestId('users-row-owner-1');
    expect(owner).toHaveTextContent('小明');
    expect(owner).toHaveTextContent('owner-1');
    expect(within(owner).getByTestId('users-role-tag-owner-1')).toHaveTextContent('所有者');

    const alice = screen.getByTestId('users-row-alice');
    expect(within(alice).getByTestId('users-role-tag-alice')).toHaveTextContent('管理者');
    expect(alice).toHaveTextContent(/2 分钟前/);

    const bob = screen.getByTestId('users-row-bob');
    expect(within(bob).getByTestId('users-role-tag-bob')).toHaveTextContent('协作成员');
    // A record the gateway did not stamp gets no invented "just now".
    expect(bob).not.toHaveTextContent(/分钟前/);
  });

  it('marks the acting member and offers 以他/她身份行动 on the others', async () => {
    renderScreen();

    await screen.findByTestId('users-row-owner-1');
    const owner = screen.getByTestId('users-row-owner-1');
    expect(within(owner).getByTestId('users-acting-owner-1')).toHaveTextContent('当前身份');
    // Nobody may "act as" the person they already are.
    expect(within(owner).queryByTestId('users-act-owner-1')).toBeNull();

    // Another member carries the switch, with the wording the task asks for.
    const alice = screen.getByTestId('users-row-alice');
    expect(within(alice).getByTestId('users-act-alice')).toHaveTextContent('以他/她身份行动');
    expect(within(alice).queryByTestId('users-acting-alice')).toBeNull();
    // The header names the identity once, so the state is not only a row mark.
    expect(screen.getByTestId('users-acting-summary')).toHaveTextContent('小明');
  });

  it('says 还没有成员。 for an empty roster, and still shows the add form', async () => {
    api.loadUsers.mockResolvedValue({ users: [] });
    renderScreen();

    expect(await screen.findByText('还没有成员。')).toBeInTheDocument();
    expect(screen.getByTestId('users-create-id')).toBeInTheDocument();
    // No acting summary: there is nobody to act as, and saying otherwise would
    // read as a broken switch rather than an empty roster.
    expect(screen.queryByTestId('users-acting-summary')).toBeNull();
  });
});

describe('UsersScreen — the honest note and the legend', () => {
  it('states that roles are a policy table, not sign-in', async () => {
    renderScreen();

    await screen.findByTestId('users-row-owner-1');
    // The exact claim the product has to make instead of looking like auth.
    expect(screen.getByTestId('users-note')).toHaveTextContent('角色是权限策略，不是登录');
    expect(screen.getByTestId('users-note')).toHaveTextContent('没有身份验证');
  });

  it('renders the five capabilities and the roles each one allows', async () => {
    renderScreen();

    await screen.findByTestId('users-row-owner-1');
    expect(screen.getByTestId('users-legend').querySelectorAll('li')).toHaveLength(5);

    // Dispatch: all three. Approve / review memory / budget: owner + manager.
    expect(screen.getByTestId('users-capability-dispatch')).toHaveTextContent('全部角色');
    for (const action of ['approve', 'review-memory', 'decide-budget']) {
      const row = screen.getByTestId(`users-capability-${action}`);
      expect(row).toHaveTextContent('所有者');
      expect(row).toHaveTextContent('管理者');
      expect(row).not.toHaveTextContent('协作成员');
    }
    // Managing bots: the owner only — the action that could grant itself all
    // the others.
    const manage = screen.getByTestId('users-capability-manage-bots');
    expect(manage).toHaveTextContent('所有者');
    expect(manage).not.toHaveTextContent('管理者');
  });
});

describe('UsersScreen — adding a member', () => {
  it('sends exactly {id,name,role}, then re-reads', async () => {
    const user = userEvent.setup();
    api.upsertUser.mockResolvedValue(member({ id: 'carol', name: '卡罗', role: 'collaborator' }));
    renderScreen();

    await screen.findByTestId('users-row-owner-1');
    await user.type(screen.getByTestId('users-create-id'), 'carol');
    await user.type(screen.getByTestId('users-create-name'), '卡罗');
    await pickOption(user, screen.getByTestId('users-create-role'), '协作成员');
    await user.click(screen.getByTestId('users-create-submit'));

    await waitFor(() =>
      expect(api.upsertUser).toHaveBeenCalledWith({
        id: 'carol',
        name: '卡罗',
        role: 'collaborator',
      }),
    );
    // The write's answer is not the list: the screen re-reads the snapshot.
    await waitFor(() => expect(api.loadUsers).toHaveBeenCalledTimes(2));
    // The form is empty again, so the same id cannot be submitted twice by accident.
    expect(screen.getByTestId('users-create-id')).toHaveValue('');
    expect(screen.getByTestId('users-create-name')).toHaveValue('');
  });

  it('cannot submit without both an id and a name', async () => {
    const user = userEvent.setup();
    renderScreen();

    await screen.findByTestId('users-row-owner-1');
    const submit = screen.getByTestId('users-create-submit');
    expect(submit).toBeDisabled();

    await user.type(screen.getByTestId('users-create-id'), 'carol');
    // A nameless member cannot be shown or attributed to (contracts.User.Name),
    // so the form does not offer the attempt.
    expect(submit).toBeDisabled();

    await user.type(screen.getByTestId('users-create-name'), '卡罗');
    expect(submit).not.toBeDisabled();
  });
});

describe('UsersScreen — changing a role', () => {
  it('sends the whole row with the new role, and re-reads', async () => {
    const user = userEvent.setup();
    api.upsertUser.mockResolvedValue(member({ id: 'bob', name: '小博', role: 'manager' }));
    renderScreen();

    await screen.findByTestId('users-row-bob');
    await pickOption(user, screen.getByTestId('users-role-bob'), '管理者');

    // Whole row: `users.upsert` requires id, name and role (there is no patch
    // shape), so a role-only request would be rejected as incomplete — and if
    // it were accepted it would blank the name of the member being edited.
    expect(api.upsertUser).toHaveBeenCalledWith({
      id: 'bob',
      name: '小博',
      role: 'manager',
    });
    expect(api.upsertUser).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(api.loadUsers).toHaveBeenCalledTimes(2));
  });
});

describe('UsersScreen — switching identity', () => {
  it('sends exactly the id, then re-reads so the marker is the gateway’s answer', async () => {
    const user = userEvent.setup();
    api.loadUsers.mockResolvedValueOnce(roster()).mockResolvedValueOnce({
      ...roster(),
      actingUserId: 'alice',
      actingRole: 'manager',
    });
    api.actAsUser.mockResolvedValue('alice');
    renderScreen();

    await screen.findByTestId('users-row-alice');
    await user.click(screen.getByTestId('users-act-alice'));

    expect(api.actAsUser).toHaveBeenCalledWith('alice');
    expect(api.actAsUser).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(api.loadUsers).toHaveBeenCalledTimes(2));
    // The marker moved because the snapshot said so, not because we clicked.
    await waitFor(() =>
      expect(screen.getByTestId('users-row-alice')).toContainElement(
        screen.getByTestId('users-acting-alice'),
      ),
    );
    expect(screen.queryByTestId('users-acting-owner-1')).toBeNull();
  });

  it('allows one write in flight at a time', async () => {
    const user = userEvent.setup();
    let finish!: (value: string) => void;
    const pending = new Promise<string>((resolve) => {
      finish = resolve;
    });
    api.actAsUser.mockReturnValueOnce(pending);
    renderScreen();

    await screen.findByTestId('users-row-alice');
    await user.click(screen.getByTestId('users-act-alice'));

    // Every verb on the page is disabled while this write is unanswered: they
    // all rewrite the same roster, so a second one would race this one's re-read.
    await waitFor(() => expect(screen.getByTestId('users-act-bob')).toBeDisabled());
    expect(screen.getByTestId('users-create-submit')).toBeDisabled();
    expect(screen.getByTestId('users-role-bob')).toHaveClass('ant-select-disabled');

    finish('alice');
    await waitFor(() => expect(screen.getByTestId('users-act-bob')).not.toBeDisabled());
  });
});

describe('UsersScreen — failures', () => {
  it('keeps the list and prints the gateway’s words, including the last-owner refusal', async () => {
    const user = userEvent.setup();
    api.upsertUser.mockRejectedValueOnce(
      new Error('users: refusing to demote the last owner'),
    );
    renderScreen();

    await screen.findByTestId('users-row-owner-1');
    await pickOption(user, screen.getByTestId('users-role-owner-1'), '管理者');

    // Verbatim: this layer does not invent a diagnosis, and the refusal is the
    // gateway's own argument for why the change did not happen.
    expect(await screen.findByText('users: refusing to demote the last owner')).toBeInTheDocument();
    // Nothing changed, so the list is still the last true answer — and no read
    // was issued to replace it.
    expect(screen.getByTestId('users-row-owner-1')).toBeInTheDocument();
    expect(screen.getByTestId('users-row-alice')).toBeInTheDocument();
    expect(screen.getByTestId('users-row-bob')).toBeInTheDocument();
    expect(api.loadUsers).toHaveBeenCalledTimes(1);
  });

  it('names a failed read and retries it for real', async () => {
    const user = userEvent.setup();
    api.loadUsers.mockRejectedValueOnce(new Error('snapshot failed'));
    renderScreen();

    expect(await screen.findByText('snapshot failed')).toBeInTheDocument();
    await user.click(screen.getByTestId('users-retry'));

    expect(await screen.findByTestId('users-row-owner-1')).toBeInTheDocument();
    expect(api.loadUsers).toHaveBeenCalledTimes(2);
  });
});

/**
 * The wiring: the route is registered, and the sidebar entry is both
 * discoverable and selected on it — a route nobody can reach is not a surface.
 */
describe('UsersScreen — the route and its sidebar entry', () => {
  let gateway: FakeGatewayHandle | null = null;

  beforeEach(() => {
    gateway = installFakeGateway(defaultState());
    vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
  });

  afterEach(() => {
    gateway?.dispose();
    gateway = null;
  });

  it('renders the screen at /settings/users and keeps 成员 selected', async () => {
    render(
      <MemoryRouter initialEntries={['/settings/users']}>
        <App />
      </MemoryRouter>,
    );

    expect(await screen.findByTestId('users-row-owner-1')).toBeInTheDocument();
    const entry = screen.getByRole('menuitem', { name: /成员/ });
    await waitFor(() => expect(entry).toHaveClass('ant-menu-item-selected'));
  });
});