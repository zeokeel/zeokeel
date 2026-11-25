import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { I18nProvider } from '@/i18n';

/**
 * R28 · the skill-drafts queue (`skilldrafts.snapshot`, `skilldrafts.decide`).
 *
 * These tests pin what makes the queue trustworthy:
 *  1. the list is the service's own answer — order included, nothing re-sorted;
 *  2. every row states its own status (待确认 / 已采纳 / 已驳回), and only a
 *     pending row carries 采纳 / 驳回 — a decision is final, so it is offered
 *     once;
 *  3. 采纳 is one write of exactly `{id, accept, reviewer}` followed by a **read
 *     of the queue it produced** (the decided row the gateway answers with is
 *     not the queue);
 *  4. 驳回 asks first (a Popconfirm: the verdict cannot be taken back) and
 *     sends `accept: false`;
 *  5. one decision at a time — both verbs of every row are disabled while a
 *     write is in flight, and a second click sends nothing;
 *  6. a failed decision keeps the row exactly as it was and prints the gateway's
 *     own words; nothing is re-read, because nothing changed;
 *  7. an empty queue says so, and says where drafts come from.
 *
 * No timers are faked: the screen has no polling, so every assertion is about
 * one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  loadSkillDrafts: vi.fn(),
  proposeSkillDraft: vi.fn(),
  decideSkillDraft: vi.fn(),
}));

vi.mock('@/api/skilldrafts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/skilldrafts')>();
  return { ...actual, ...api };
});

import { SkillDraftsScreen } from './SkillDraftsScreen';

beforeEach(() => {
  // jsdom reports en-US; this suite asserts the Chinese copy, so pin the browser
  // language the way a Chinese user's browser would report it.
  vi.stubGlobal('navigator', { ...navigator, language: 'zh-CN', languages: ['zh-CN'] });
  Object.values(api).forEach((fn) => fn.mockReset());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderScreen() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={['/skills/drafts']}>
        <SkillDraftsScreen />
      </MemoryRouter>
    </I18nProvider>,
  );
}

/** A draft nobody has decided on yet, proposed by a finished task. */
function pendingDraft(id = 'draft-p') {
  return {
    id,
    taskId: 'T-7',
    name: 'deploy-order',
    summary: '部署前的固定顺序',
    body: '## When to use\n先 npm run build，再跑 scripts/verify.sh',
    status: 'pending',
    createdAt: new Date(Date.now() - 125_000).toISOString(),
  };
}

/** A draft a human already decided on. */
function decidedDraft(id: string, status: 'accepted' | 'rejected') {
  return {
    id,
    name: `skill-${id}`,
    summary: '已经决定过的草稿',
    status,
    reviewer: 'lead',
  };
}

/** A write that stays in flight until the test resolves it. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('SkillDraftsScreen — the list is the service answer', () => {
  it('renders the three statuses newest first, as returned', async () => {
    api.loadSkillDrafts.mockResolvedValue([
      pendingDraft('draft-p'),
      decidedDraft('draft-a', 'accepted'),
      decidedDraft('draft-r', 'rejected'),
    ]);
    renderScreen();

    const list = await screen.findByTestId('skills-drafts-list');
    const rows = within(list).getAllByRole('article');
    expect(rows).toHaveLength(3);

    // Newest first is the service's ordering; the screen must not re-sort.
    expect(rows[0]).toHaveTextContent('deploy-order');
    expect(rows[1]).toHaveTextContent('skill-draft-a');
    expect(rows[2]).toHaveTextContent('skill-draft-r');

    // Each row states its own status, and only the pending one offers a verb.
    expect(screen.getByTestId('skill-draft-status-draft-p')).toHaveTextContent(/^待确认$/);
    expect(screen.getByTestId('skill-draft-status-draft-a')).toHaveTextContent(/^已采纳$/);
    expect(screen.getByTestId('skill-draft-status-draft-r')).toHaveTextContent(/^已驳回$/);

    expect(screen.getByTestId('skill-draft-accept-draft-p')).toBeInTheDocument();
    expect(screen.queryByTestId('skill-draft-accept-draft-a')).toBeNull();
    expect(screen.queryByTestId('skill-draft-accept-draft-r')).toBeNull();
    expect(screen.queryByTestId('skill-draft-reject-draft-a')).toBeNull();
    expect(screen.queryByTestId('skill-draft-reject-draft-r')).toBeNull();

    // One read on mount; nothing polls.
    expect(api.loadSkillDrafts).toHaveBeenCalledTimes(1);
  });

  it('prints the summary, and names the reviewer only on a decided draft', async () => {
    api.loadSkillDrafts.mockResolvedValue([
      decidedDraft('draft-a', 'accepted'),
      pendingDraft('draft-p'),
    ]);
    renderScreen();

    const row = await screen.findByTestId('skill-draft-draft-a');
    expect(row).toHaveTextContent('已经决定过的草稿');
    // A decision states who made it — the gateway's name, not one invented here.
    expect(row).toHaveTextContent('lead');

    // A pending draft has nobody to name: the gateway reported no reviewer.
    await screen.findByTestId('skill-draft-draft-p');
    expect(screen.queryByTestId('skill-draft-reviewer-draft-p')).toBeNull();
  });

  it('shows the empty state and says where drafts come from', async () => {
    api.loadSkillDrafts.mockResolvedValue([]);
    renderScreen();

    const empty = await screen.findByTestId('skills-drafts-empty');
    expect(empty).toHaveTextContent('还没有技能草稿。');
    // The empty state explains the mechanism instead of leaving a dead end.
    expect(empty).toHaveTextContent(/任务被采纳后/);
    expect(screen.queryByTestId('skills-drafts-list')).toBeNull();
  });
});

describe('SkillDraftsScreen — a draft from a task deep-links to it', () => {
  it('links /t/:taskId when the draft carries one, and not otherwise', async () => {
    api.loadSkillDrafts.mockResolvedValue([
      pendingDraft('draft-p'),
      { id: 'draft-free', name: 'free-skill', status: 'pending' },
    ]);
    renderScreen();

    const link = await screen.findByTestId('skill-draft-task-draft-p');
    expect(link).toHaveAttribute('href', '/t/T-7');
    expect(link).toHaveTextContent('任务 T-7');
    // A draft not derived from a task offers no link to nowhere.
    expect(screen.queryByTestId('skill-draft-task-draft-free')).toBeNull();
  });
});

describe('SkillDraftsScreen — the body is clamped to three lines', () => {
  it('shows 展开 for a long body, and 收起 once expanded', async () => {
    const user = userEvent.setup();
    api.loadSkillDrafts.mockResolvedValue([
      { ...pendingDraft('draft-long'), body: 'x'.repeat(400) },
    ]);
    renderScreen();

    const toggle = await screen.findByTestId('skill-draft-toggle-draft-long');
    expect(toggle).toHaveTextContent('展开');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);
    expect(toggle).toHaveTextContent('收起');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });

  it('offers no toggle for a body that plainly fits', async () => {
    api.loadSkillDrafts.mockResolvedValue([
      { id: 'draft-short', name: 'short-skill', summary: '一句话', status: 'pending' },
    ]);
    renderScreen();

    await screen.findByTestId('skill-draft-draft-short');
    expect(screen.queryByTestId('skill-draft-toggle-draft-short')).toBeNull();
  });
});

describe('SkillDraftsScreen — 采纳 records one decision, then re-reads', () => {
  it('sends exactly {id, accept:true, reviewer:lead} and shows the queue the re-read returned', async () => {
    const user = userEvent.setup();
    api.loadSkillDrafts
      .mockResolvedValueOnce([pendingDraft('draft-p')])
      .mockResolvedValueOnce([decidedDraft('draft-p', 'accepted')]);
    api.decideSkillDraft.mockResolvedValue(decidedDraft('draft-p', 'accepted'));
    renderScreen();

    await user.click(await screen.findByTestId('skill-draft-accept-draft-p'));

    await waitFor(() => expect(api.decideSkillDraft).toHaveBeenCalledTimes(1));
    // The exact wire payload: the contract's three fields and nothing else.
    expect(api.decideSkillDraft.mock.calls[0][0]).toEqual({
      id: 'draft-p',
      accept: true,
      reviewer: 'lead',
    });

    // The write, then a read of the state it produced — in that order.
    await waitFor(() => expect(api.loadSkillDrafts).toHaveBeenCalledTimes(2));
    const write = api.decideSkillDraft.mock.invocationCallOrder[0];
    const secondRead = api.loadSkillDrafts.mock.invocationCallOrder[1];
    expect(write).toBeLessThan(secondRead);

    // The re-read is what renders, not the write's answer.
    expect(await screen.findByTestId('skill-draft-status-draft-p')).toHaveTextContent(/^已采纳$/);
    expect(screen.queryByTestId('skill-draft-accept-draft-p')).toBeNull();
  });
});

describe('SkillDraftsScreen — 驳回 asks first, then sends accept:false', () => {
  it('sends nothing until the confirm, then re-reads the queue', async () => {
    const user = userEvent.setup();
    api.loadSkillDrafts
      .mockResolvedValueOnce([pendingDraft('draft-p')])
      .mockResolvedValueOnce([decidedDraft('draft-p', 'rejected')]);
    api.decideSkillDraft.mockResolvedValue(decidedDraft('draft-p', 'rejected'));
    renderScreen();

    await user.click(await screen.findByTestId('skill-draft-reject-draft-p'));
    // Opening the question is not an answer: no write yet.
    expect(api.decideSkillDraft).not.toHaveBeenCalled();

    await user.click(await screen.findByRole('button', { name: /确认驳回/ }));

    await waitFor(() => expect(api.decideSkillDraft).toHaveBeenCalledTimes(1));
    expect(api.decideSkillDraft.mock.calls[0][0]).toEqual({
      id: 'draft-p',
      accept: false,
      reviewer: 'lead',
    });
    await waitFor(() => expect(api.loadSkillDrafts).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId('skill-draft-status-draft-p')).toHaveTextContent(/^已驳回$/);
  });
});

describe('SkillDraftsScreen — one decision at a time', () => {
  it('disables both verbs while the write is in flight and sends it once', async () => {
    const user = userEvent.setup();
    const inFlight = deferred<unknown>();
    api.loadSkillDrafts
      .mockResolvedValueOnce([pendingDraft('draft-p'), pendingDraft('draft-q')])
      .mockResolvedValueOnce([decidedDraft('draft-p', 'accepted'), pendingDraft('draft-q')]);
    api.decideSkillDraft.mockReturnValue(inFlight.promise);
    renderScreen();

    const accept = await screen.findByTestId('skill-draft-accept-draft-p');
    await user.click(accept);

    expect(accept).toBeDisabled();
    expect(screen.getByTestId('skill-draft-reject-draft-p')).toBeDisabled();
    // Another row cannot start a second decision over the first one.
    expect(screen.getByTestId('skill-draft-accept-draft-q')).toBeDisabled();
    expect(screen.getByTestId('skill-draft-reject-draft-q')).toBeDisabled();
    await user.click(accept);
    expect(api.decideSkillDraft).toHaveBeenCalledTimes(1);

    inFlight.resolve(undefined);
    await waitFor(() => expect(api.loadSkillDrafts).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId('skill-draft-status-draft-p')).toHaveTextContent(/^已采纳$/);
  });
});

describe('SkillDraftsScreen — a failed decision changes nothing on screen', () => {
  it('keeps the row and prints the gateway text', async () => {
    const user = userEvent.setup();
    api.loadSkillDrafts.mockResolvedValue([pendingDraft('draft-p')]);
    api.decideSkillDraft.mockRejectedValueOnce(new Error('draft is already rejected'));
    renderScreen();

    await user.click(await screen.findByTestId('skill-draft-accept-draft-p'));

    const alert = await screen.findByTestId('skills-drafts-decision-error');
    expect(alert).toHaveTextContent('draft is already rejected');
    // Nothing changed, so nothing was read again: the row is still the last true
    // answer and its verbs still work.
    expect(api.loadSkillDrafts).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('skill-draft-status-draft-p')).toHaveTextContent(/^待确认$/);
    expect(screen.getByTestId('skill-draft-accept-draft-p')).toBeEnabled();
  });
});

describe('SkillDraftsScreen — a failed read is reported, never hidden', () => {
  it('names the cause and re-reads on 重试', async () => {
    const user = userEvent.setup();
    api.loadSkillDrafts
      .mockRejectedValueOnce(new Error('skill drafts unavailable'))
      .mockResolvedValue([pendingDraft('draft-p')]);
    renderScreen();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('skill drafts unavailable');

    await user.click(within(alert).getByRole('button', { name: /重\s*试/ }));

    expect(await screen.findByTestId('skill-draft-draft-p')).toBeInTheDocument();
    expect(api.loadSkillDrafts).toHaveBeenCalledTimes(2);
  });
});