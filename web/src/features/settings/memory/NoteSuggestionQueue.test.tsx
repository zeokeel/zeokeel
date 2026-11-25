import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { I18nProvider } from '@/i18n';

/**
 * The proposal queue in front of the shared board (`team.suggestions`,
 * `team.suggest`, `team.suggestion.promote`, `team.suggestion.dismiss`).
 *
 * A bot proposes a team rule; nothing reaches the board until a person promotes
 * it. These tests pin the four things that make that gate worth having:
 *  1. the queue is the service's own answer — every row states the rule, who
 *     proposed it and when, and it sits **above** the board it feeds;
 *  2. 采纳 sends exactly `{id, reviewer}` and then re-reads **both** lists,
 *     because a promotion changes both;
 *  3. 驳回 asks first (a Popconfirm) and sends nothing until it is confirmed;
 *  4. a person can propose too — exactly `{title, body, author}` — and a
 *     proposal re-reads the queue alone (it never touches the board);
 *  5. one write at a time: every verb on every row waits while one is in flight;
 *  6. a failed write keeps the rows and prints the gateway's own words, and the
 *     empty state says where proposals come from.
 */

const api = vi.hoisted(() => ({
  loadTeamNotes: vi.fn(),
  recordTeamNote: vi.fn(),
  reviewTeamNote: vi.fn(),
  loadNoteSuggestions: vi.fn(),
  suggestNote: vi.fn(),
  promoteSuggestion: vi.fn(),
  dismissSuggestion: vi.fn(),
}));

vi.mock('@/api/teamnotes', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/teamnotes')>();
  return { ...actual, ...api };
});

import { TeamMemoryScreen } from './TeamMemoryScreen';

beforeEach(() => {
  // jsdom reports en-US; this suite asserts the Chinese copy, so pin the browser
  // language the way a Chinese user's browser would report it.
  vi.stubGlobal('navigator', { ...navigator, language: 'zh-CN', languages: ['zh-CN'] });
  Object.values(api).forEach((fn) => fn.mockReset());
  // Both lists are read on mount: the board (existing) and the queue (new).
  api.loadTeamNotes.mockResolvedValue([]);
  api.loadNoteSuggestions.mockResolvedValue([]);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderScreen() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={['/settings/memory']}>
        <TeamMemoryScreen />
      </MemoryRouter>
    </I18nProvider>,
  );
}

/** Two minutes old, so the row must print a relative stamp, not a date. */
function twoMinutesAgo(): string {
  return new Date(Date.now() - 125_000).toISOString();
}

/** A proposal from a bot, with a body. */
function botSuggestion(id = 'sug-a') {
  return {
    id,
    title: '提交前先跑 go vet',
    body: '先 gofmt，再 go vet，最后 go test。',
    author: 'watchbot',
    at: twoMinutesAgo(),
  };
}

/** A proposal with nothing but the required field. */
function bareSuggestion(id = 'sug-b') {
  return { id, title: '周末不动主分支' };
}

/** A write that stays in flight until the test resolves it. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('the queue is the service answer, in front of the board', () => {
  it('renders each proposal with its author, its time and the rule itself', async () => {
    api.loadNoteSuggestions.mockResolvedValue([botSuggestion(), bareSuggestion()]);
    renderScreen();

    const list = await screen.findByTestId('memory-suggestions-list');
    const rows = within(list).getAllByRole('article');
    expect(rows).toHaveLength(2);
    // The service's order is kept: nothing is re-sorted on this side.
    expect(rows[0]).toHaveTextContent('提交前先跑 go vet');
    expect(rows[1]).toHaveTextContent('周末不动主分支');

    // Author and relative stamp, as reported.
    expect(rows[0]).toHaveTextContent('watchbot');
    expect(rows[0]).toHaveTextContent(/2 分钟前/);
    // A proposal the gateway did not attribute says 未知, like a note does.
    expect(rows[1]).toHaveTextContent('未知');

    // One read on mount; nothing polls.
    expect(api.loadNoteSuggestions).toHaveBeenCalledTimes(1);

    // The queue sits **above** the board it feeds.
    const queue = screen.getByTestId('memory-suggestions');
    const board = screen.getByTestId('memory-notes');
    expect(queue.compareDocumentPosition(board) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('clamps a long body to three lines with 展开 / 收起', async () => {
    const user = userEvent.setup();
    const long = 'https://example.test/a/very/long/path/that/never/breaks/' + 'a'.repeat(160);
    api.loadNoteSuggestions.mockResolvedValue([{ ...botSuggestion(), body: long }]);
    renderScreen();

    const toggle = await screen.findByTestId('memory-suggestion-toggle-sug-a');
    expect(toggle).toHaveTextContent('展开');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);
    expect(toggle).toHaveTextContent('收起');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });

  it('offers no toggle for a body that plainly fits', async () => {
    api.loadNoteSuggestions.mockResolvedValue([botSuggestion()]);
    renderScreen();

    await screen.findByTestId('memory-suggestion-sug-a');
    expect(screen.queryByTestId('memory-suggestion-toggle-sug-a')).toBeNull();
  });

  it('says proposals come from bots and that a person decides', async () => {
    renderScreen();

    const empty = await screen.findByTestId('memory-suggestions-empty');
    expect(empty).toHaveTextContent('没有待确认的建议。');
    expect(empty).toHaveTextContent('机器人可以提规则，但由人来决定。');
    // The propose form stays put: a person can propose into an empty queue too.
    expect(screen.getByTestId('memory-suggest-title')).toBeInTheDocument();
  });
});

describe('采纳 promotes one proposal and re-reads both lists', () => {
  it('sends exactly {id, reviewer} and reads the queue and the board again', async () => {
    const user = userEvent.setup();
    api.loadNoteSuggestions
      .mockResolvedValueOnce([botSuggestion('sug-a'), bareSuggestion('sug-b')])
      .mockResolvedValueOnce([bareSuggestion('sug-b')]);
    api.loadTeamNotes
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ id: 'note-new', title: '提交前先跑 go vet', author: 'lead' }]);
    api.promoteSuggestion.mockResolvedValue({ id: 'note-new', title: '提交前先跑 go vet' });
    renderScreen();

    await user.click(await screen.findByTestId('memory-suggestion-promote-sug-a'));

    await waitFor(() => expect(api.promoteSuggestion).toHaveBeenCalledTimes(1));
    // The exact wire payload: the contract's two fields, nothing else.
    expect(api.promoteSuggestion.mock.calls[0][0]).toEqual({ id: 'sug-a', reviewer: 'lead' });

    // The write, then a read of **both** lists it changed — in that order.
    await waitFor(() => expect(api.loadNoteSuggestions).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(api.loadTeamNotes).toHaveBeenCalledTimes(2));
    const write = api.promoteSuggestion.mock.invocationCallOrder[0];
    const queueRead = api.loadNoteSuggestions.mock.invocationCallOrder[1];
    const boardRead = api.loadTeamNotes.mock.invocationCallOrder[1];
    expect(write).toBeLessThan(queueRead);
    expect(write).toBeLessThan(boardRead);

    // The re-reads are what renders: the row is gone from the queue and the
    // note the product became is on the board.
    await waitFor(() =>
      expect(screen.queryByTestId('memory-suggestion-sug-a')).toBeNull(),
    );
    expect(await screen.findByTestId('memory-note-note-new')).toHaveTextContent(
      '提交前先跑 go vet',
    );
  });
});

describe('驳回 asks first, then dismisses the proposal', () => {
  it('sends nothing until the confirm, then re-reads both lists', async () => {
    const user = userEvent.setup();
    api.loadNoteSuggestions
      .mockResolvedValueOnce([botSuggestion('sug-a')])
      .mockResolvedValueOnce([]);
    api.dismissSuggestion.mockResolvedValue(undefined);
    renderScreen();

    await user.click(await screen.findByTestId('memory-suggestion-dismiss-sug-a'));
    // Opening the question is not an answer: no write yet.
    expect(api.dismissSuggestion).not.toHaveBeenCalled();

    await user.click(await screen.findByRole('button', { name: /确认驳回/ }));

    await waitFor(() => expect(api.dismissSuggestion).toHaveBeenCalledTimes(1));
    expect(api.dismissSuggestion.mock.calls[0][0]).toEqual({ id: 'sug-a', reviewer: 'lead' });
    // Dismissing changes the queue and answers for the board: both re-read.
    await waitFor(() => expect(api.loadNoteSuggestions).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(api.loadTeamNotes).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByTestId('memory-suggestion-sug-a')).toBeNull());
  });
});

describe('a person can propose too', () => {
  it('sends exactly {title, body, author} and re-reads the queue alone', async () => {
    const user = userEvent.setup();
    api.loadNoteSuggestions.mockResolvedValue([]).mockResolvedValueOnce([]).mockResolvedValue([
      { id: 'sug-new', title: '发布前写变更说明', body: '写在 CHANGELOG 里', author: 'lead' },
    ]);
    api.suggestNote.mockResolvedValue({ id: 'sug-new', title: '发布前写变更说明' });
    renderScreen();

    await screen.findByTestId('memory-suggestions-empty');
    await user.type(screen.getByTestId('memory-suggest-title'), '发布前写变更说明');
    await user.type(screen.getByTestId('memory-suggest-body'), '写在 CHANGELOG 里');
    await user.click(screen.getByTestId('memory-suggest-submit'));

    await waitFor(() =>
      expect(api.suggestNote).toHaveBeenCalledWith({
        title: '发布前写变更说明',
        body: '写在 CHANGELOG 里',
        author: 'lead',
      }),
    );

    // A proposal never touches the board, so the board is not re-read.
    await waitFor(() => expect(api.loadNoteSuggestions).toHaveBeenCalledTimes(2));
    expect(api.loadTeamNotes).toHaveBeenCalledTimes(1);
    // The write's own text is cleared; the author is kept for the next one.
    expect(screen.getByTestId('memory-suggest-title')).toHaveValue('');
    expect(screen.getByTestId('memory-suggest-body')).toHaveValue('');
    expect(screen.getByTestId('memory-suggest-author')).toHaveValue('lead');
    expect(await screen.findByTestId('memory-suggestion-sug-new')).toBeInTheDocument();
  });

  it('omits an empty body and uses the typed author', async () => {
    const user = userEvent.setup();
    api.loadNoteSuggestions.mockResolvedValue([]);
    api.suggestNote.mockResolvedValue({ id: 'sug-c', title: '只有标题' });
    renderScreen();

    await screen.findByTestId('memory-suggestions-empty');
    await user.clear(screen.getByTestId('memory-suggest-author'));
    await user.type(screen.getByTestId('memory-suggest-author'), 'memoryui');
    await user.type(screen.getByTestId('memory-suggest-title'), '只有标题');
    await user.click(screen.getByTestId('memory-suggest-submit'));

    await waitFor(() =>
      expect(api.suggestNote).toHaveBeenCalledWith({ title: '只有标题', author: 'memoryui' }),
    );
  });

  it('is disabled until the title holds something other than spaces', async () => {
    const user = userEvent.setup();
    renderScreen();

    const submit = await screen.findByTestId('memory-suggest-submit');
    expect(submit).toBeDisabled();

    const title = screen.getByTestId('memory-suggest-title');
    await user.type(title, '   ');
    expect(submit).toBeDisabled();

    await user.type(title, '有内容');
    expect(submit).toBeEnabled();
    expect(api.suggestNote).not.toHaveBeenCalled();
  });
});

describe('a failed write changes nothing on screen', () => {
  it('keeps every row and prints the gateway text', async () => {
    const user = userEvent.setup();
    api.loadNoteSuggestions.mockResolvedValue([botSuggestion('sug-a')]);
    api.promoteSuggestion.mockRejectedValueOnce(new Error('suggestion already promoted'));
    renderScreen();

    await user.click(await screen.findByTestId('memory-suggestion-promote-sug-a'));

    const alert = await screen.findByTestId('memory-suggestion-error');
    expect(alert).toHaveTextContent('suggestion already promoted');
    // Nothing was written, so nothing was re-read: the queue on screen is still
    // the last true answer and the row keeps its verbs.
    expect(api.loadNoteSuggestions).toHaveBeenCalledTimes(1);
    expect(api.loadTeamNotes).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('memory-suggestion-sug-a')).toBeInTheDocument();
    expect(screen.getByTestId('memory-suggestion-promote-sug-a')).toBeEnabled();
  });
});

describe('one write at a time', () => {
  it('disables every verb while a decision is in flight and sends it once', async () => {
    const user = userEvent.setup();
    const inFlight = deferred<unknown>();
    api.loadNoteSuggestions
      .mockResolvedValueOnce([botSuggestion('sug-a'), bareSuggestion('sug-b')])
      .mockResolvedValueOnce([bareSuggestion('sug-b')]);
    api.promoteSuggestion.mockReturnValue(inFlight.promise);
    renderScreen();

    const promote = await screen.findByTestId('memory-suggestion-promote-sug-a');
    await user.click(promote);

    expect(promote).toBeDisabled();
    expect(screen.getByTestId('memory-suggestion-dismiss-sug-a')).toBeDisabled();
    // Another row cannot start a second write over the first one.
    expect(screen.getByTestId('memory-suggestion-promote-sug-b')).toBeDisabled();
    expect(screen.getByTestId('memory-suggestion-dismiss-sug-b')).toBeDisabled();
    // Nor can the propose form.
    expect(screen.getByTestId('memory-suggest-submit')).toBeDisabled();

    await user.click(promote);
    expect(api.promoteSuggestion).toHaveBeenCalledTimes(1);

    inFlight.resolve(undefined);
    await waitFor(() => expect(api.loadNoteSuggestions).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByTestId('memory-suggestion-sug-a')).toBeNull());
  });
});