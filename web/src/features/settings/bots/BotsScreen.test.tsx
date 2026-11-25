import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { App } from '@/app';
import { I18nProvider } from '@/i18n';
import { defaultState, installFakeGateway, type FakeGatewayHandle } from '@/test/fake-gateway';

/**
 * R27d · the bots screen (templates + instances, `bots.snapshot` / `bots.upsert`).
 *
 * These tests pin what makes the surface trustworthy:
 *  1. both lists are the gateway's own answer — templates read-only, instances
 *     with the status their record carries and the last-seen stamp it reports
 *     (never a made-up "just now" for a bot nobody has seen);
 *  2. a create sends exactly the fields the form holds and nothing else;
 *  3. a status change sends the **whole row** with one field changed, because
 *     `bots.upsert` on an existing bot is replacement-with-rules — sending only
 *     the field that changed would blank the grant it did not mention;
 *  4. one write is in flight at a time, a failed write keeps the list it
 *     already has (and does not re-read), a successful one re-reads;
 *  5. an empty instance list is 还没有 Bot。, not an error and not a blank box;
 *  6. `/settings/bots` is registered as a route and reached from its own
 *     sidebar entry (R27d), which stays selected while the screen is open.
 *
 * No timers are faked: the screen has no polling, so every assertion is about
 * one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  loadBots: vi.fn(),
  upsertBot: vi.fn(),
}));

vi.mock('@/api/bots', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/bots')>();
  // Keep the real types/constants (the status list drives the tag); only the
  // requests are replaced.
  return { ...actual, ...api };
});

import type { BotInstance, BotTemplate, BotsSnapshot } from '@/api/bots';
import { BotsScreen } from './BotsScreen';

const TEMPLATES: BotTemplate[] = [
  {
    id: 'lead',
    name: 'Lead',
    role: 'Lead',
    description: 'Orchestrates the team.',
    defaultModel: 'deepseek-v4',
    memoryScope: 'team',
    allowedTools: ['task_board', 'team_spawn', 'fs.read'],
  },
  {
    id: 'researcher',
    name: '研究 Bot',
    role: 'Researcher',
    description: 'Digs through the sources.',
    defaultModel: 'deepseek-v4',
    memoryScope: 'agent',
    allowedTools: ['fs.read', 'browser_open'],
  },
];

function instance(over: Partial<BotInstance> = {}): BotInstance {
  return {
    id: 'bot-1',
    templateId: 'researcher',
    name: 'Researcher One',
    owner: 'me',
    defaultModel: 'deepseek-v4',
    allowedTools: ['fs.read', 'browser_open'],
    memoryScope: 'agent',
    status: 'active',
    createdAt: '2026-11-01T00:00:00Z',
    ...over,
  };
}

/** bot-1 was seen two minutes ago; bot-2 has never been seen; bot-3 is retired. */
function instances(): BotInstance[] {
  return [
    instance({ lastSeenAt: new Date(Date.now() - 2 * 60_000).toISOString() }),
    instance({
      id: 'bot-2',
      templateId: 'lead',
      name: 'Lead Two',
      owner: '',
      memoryScope: 'team',
      status: 'paused',
      allowedTools: ['task_board'],
      lastSeenAt: undefined,
    }),
    instance({ id: 'bot-3', name: 'Old One', status: 'retired', lastSeenAt: undefined }),
  ];
}

function snapshot(over: Partial<BotsSnapshot> = {}): BotsSnapshot {
  return { templates: TEMPLATES, instances: instances(), ...over };
}

function renderScreen() {
  return render(
    <I18nProvider>
      <BotsScreen />
    </I18nProvider>,
  );
}

/** Open an antd Select and return its options, so one can be picked by text. */
function optionsOf(select: HTMLElement): HTMLElement[] {
  fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
  return Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option'));
}

beforeEach(() => {
  // jsdom reports en-US; this suite asserts the Chinese copy, so pin the browser
  // language the way a Chinese user's browser would report it.
  vi.stubGlobal('navigator', { ...navigator, language: 'zh-CN', languages: ['zh-CN'] });
  Object.values(api).forEach((fn) => fn.mockReset());
  api.loadBots.mockResolvedValue(snapshot());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('BotsScreen — the two lists', () => {
  it('renders every template with its role, default model, scope and tool count', async () => {
    renderScreen();

    const researcher = await screen.findByTestId('bots-template-researcher');
    expect(researcher).toHaveTextContent('researcher');
    expect(researcher).toHaveTextContent('研究 Bot');
    expect(researcher).toHaveTextContent('Researcher');
    expect(researcher).toHaveTextContent('deepseek-v4');
    expect(researcher).toHaveTextContent('agent');
    expect(researcher).toHaveTextContent('2 个工具');

    // The templates half is read-only: no control may claim it edits a template.
    expect(within(researcher).queryByRole('button')).toBeNull();
    expect(within(screen.getByTestId('bots-templates')).queryByRole('textbox')).toBeNull();
  });

  it('renders instances with the status tag the record carries and its last-seen stamp', async () => {
    renderScreen();

    const active = await screen.findByTestId('bots-instance-bot-1');
    expect(within(active).getByTestId('bots-instance-status-bot-1')).toHaveTextContent('在线');
    expect(active).toHaveTextContent(/2 分钟前/);
    expect(active).toHaveTextContent('Researcher One');
    expect(active).toHaveTextContent('researcher');
    expect(active).toHaveTextContent('me');
    expect(active).toHaveTextContent('2 个工具');

    const paused = screen.getByTestId('bots-instance-bot-2');
    expect(within(paused).getByTestId('bots-instance-status-bot-2')).toHaveTextContent('暂停');
    // Nobody has seen this bot yet: the row says so instead of stamping "now".
    expect(paused).toHaveTextContent('从未');
    expect(screen.getByTestId('bots-resume-bot-2')).toBeInTheDocument();
    expect(screen.queryByTestId('bots-pause-bot-2')).toBeNull();

    const retired = screen.getByTestId('bots-instance-bot-3');
    expect(within(retired).getByTestId('bots-instance-status-bot-3')).toHaveTextContent('退休');
    // Retiring is terminal: a retired bot offers no status verb at all.
    expect(within(retired).queryByRole('button')).toBeNull();
  });

  it('says 还没有 Bot。 when the instance list is empty, and still shows the templates', async () => {
    api.loadBots.mockResolvedValue(snapshot({ instances: [] }));
    renderScreen();

    expect(await screen.findByText('还没有 Bot。')).toBeInTheDocument();
    expect(screen.getByTestId('bots-template-lead')).toBeInTheDocument();
  });
});

describe('BotsScreen — creating one instance', () => {
  it('sends exactly the fields the form holds, then re-reads the snapshot', async () => {
    const user = userEvent.setup();
    api.upsertBot.mockResolvedValue(instance({ id: 'bot-9' }));
    renderScreen();

    await screen.findByTestId('bots-instance-bot-1');

    await user.type(screen.getByTestId('bots-create-id'), 'bot-9');
    await user.type(screen.getByTestId('bots-create-name'), 'New Bot');
    await user.type(screen.getByTestId('bots-create-owner'), 'me');

    const select = screen.getByTestId('bots-create-template');
    fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
    const researcher = optionsOf(select).find((option) => option.textContent?.includes('researcher'));
    expect(researcher).toBeDefined();
    fireEvent.click(researcher as HTMLElement);

    await user.click(screen.getByTestId('bots-create-submit'));

    await waitFor(() =>
      expect(api.upsertBot).toHaveBeenCalledWith({
        id: 'bot-9',
        templateId: 'researcher',
        name: 'New Bot',
        owner: 'me',
      }),
    );
    // The write's own answer is not the list: the screen re-reads the snapshot.
    await waitFor(() => expect(api.loadBots).toHaveBeenCalledTimes(2));
    // The form is empty again, so the same id cannot be submitted twice by accident.
    expect(screen.getByTestId('bots-create-id')).toHaveValue('');
  });

  it('cannot submit without an id', async () => {
    renderScreen();
    await screen.findByTestId('bots-instance-bot-1');
    expect(screen.getByTestId('bots-create-submit')).toBeDisabled();
  });
});

describe('BotsScreen — status changes write the whole row', () => {
  it('pause sends the stored row with status paused and nothing else changed', async () => {
    const user = userEvent.setup();
    api.upsertBot.mockResolvedValue(instance({ status: 'paused' }));
    renderScreen();

    await screen.findByTestId('bots-instance-bot-1');
    await user.click(screen.getByTestId('bots-pause-bot-1'));

    // Whole row, one field changed: `bots.upsert` replaces an existing bot by
    // rule (empty model means "follow the session", an absent tool list keeps
    // the grant), so a partial write would either blank a field or silently
    // keep one the user did not see.
    expect(api.upsertBot).toHaveBeenCalledWith({
      id: 'bot-1',
      templateId: 'researcher',
      name: 'Researcher One',
      owner: 'me',
      defaultModel: 'deepseek-v4',
      allowedTools: ['fs.read', 'browser_open'],
      memoryScope: 'agent',
      status: 'paused',
    });
    await waitFor(() => expect(api.loadBots).toHaveBeenCalledTimes(2));
  });

  it('resume sends the stored row with status active', async () => {
    const user = userEvent.setup();
    api.upsertBot.mockResolvedValue(instance({ id: 'bot-2', status: 'active' }));
    renderScreen();

    await screen.findByTestId('bots-instance-bot-2');
    await user.click(screen.getByTestId('bots-resume-bot-2'));

    expect(api.upsertBot).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'bot-2', templateId: 'lead', status: 'active' }),
    );
  });

  it('allows one write in flight at a time', async () => {
    const user = userEvent.setup();
    let finish!: (value: BotInstance) => void;
    const pending = new Promise<BotInstance>((resolve) => {
      finish = resolve;
    });
    api.upsertBot.mockReturnValueOnce(pending);
    renderScreen();

    await screen.findByTestId('bots-instance-bot-1');
    await user.click(screen.getByTestId('bots-pause-bot-1'));

    // The other row's verb is disabled while this write is unanswered.
    await waitFor(() => expect(screen.getByTestId('bots-resume-bot-2')).toBeDisabled());
    expect(screen.getByTestId('bots-create-submit')).toBeDisabled();

    finish(instance({ status: 'paused' }));
    await waitFor(() => expect(screen.getByTestId('bots-resume-bot-2')).not.toBeDisabled());
  });
});

describe('BotsScreen — failures', () => {
  it('keeps the list and the rows on a failed write, and re-reads nothing', async () => {
    const user = userEvent.setup();
    api.upsertBot.mockRejectedValueOnce(new Error('bot store refused the write'));
    renderScreen();

    await screen.findByTestId('bots-instance-bot-1');
    await user.click(screen.getByTestId('bots-pause-bot-1'));

    // The gateway's own words: this layer does not invent a diagnosis.
    expect(await screen.findByText('bot store refused the write')).toBeInTheDocument();
    expect(screen.getByTestId('bots-instance-bot-1')).toBeInTheDocument();
    expect(screen.getByTestId('bots-instance-bot-2')).toBeInTheDocument();
    expect(api.loadBots).toHaveBeenCalledTimes(1);
  });

  it('names a failed read and retries it for real', async () => {
    const user = userEvent.setup();
    api.loadBots.mockRejectedValueOnce(new Error('snapshot failed'));
    renderScreen();

    expect(await screen.findByText('snapshot failed')).toBeInTheDocument();
    // antd puts a space between two CJK glyphs, so the verb is found by test id.
    await user.click(screen.getByTestId('bots-retry'));

    expect(await screen.findByTestId('bots-instance-bot-1')).toBeInTheDocument();
    expect(api.loadBots).toHaveBeenCalledTimes(2);
  });
});
/**
 * R27d's wiring: the route is registered, and the sidebar entry is both
 * discoverable and selected on it — a route nobody can reach is not a surface.
 */
describe('BotsScreen — the route and its sidebar entry', () => {
  let gateway: FakeGatewayHandle | null = null;

  beforeEach(() => {
    gateway = installFakeGateway(defaultState());
    vi.stubGlobal('fetch', async () => new Response('not found', { status: 404 }));
  });

  afterEach(() => {
    gateway?.dispose();
    gateway = null;
  });

  it('renders the screen at /settings/bots and keeps 机器人 selected', async () => {
    render(
      <MemoryRouter initialEntries={['/settings/bots']}>
        <App />
      </MemoryRouter>,
    );

    // The screen really is the one on this URL (the list is the mocked read).
    expect(await screen.findByTestId('bots-instance-bot-1')).toBeInTheDocument();
    const entry = screen.getByRole('menuitem', { name: /机器人/ });
    await waitFor(() => expect(entry).toHaveClass('ant-menu-item-selected'));
  });
});
