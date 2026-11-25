import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { I18nProvider } from '@/i18n';
import {
  expectNoHorizontalOverflow,
  expectTapTarget,
  expectWrapsOrEllipsis,
  installViewport,
} from '@/test/viewport';

/**
 * R27c · the shared-memory screen (`team.notes`, `team.note.record`).
 *
 * These tests pin what makes a shared board trustworthy:
 *  1. the list is the service's own answer — order included, nothing re-sorted
 *     or counted on this side;
 *  2. 记录 is one write followed by a **read of the board it produced** (a note
 *     can replace an earlier note for the same task, so the write's own answer
 *     is not the board), and it cannot be pressed with an empty title;
 *  3. a failed read names its cause and 重试 is a real second read; a failed
 *     write keeps the typed text and re-reads nothing;
 *  4. an unattributed note says 未知 and a note tied to a task links to
 *     `/t/:taskId`;
 *  5. a body cut to three lines can be expanded, and long tokens wrap;
 *  6. every row states its review (未复核 / 已复核 · who / 已驳回) as the list
 *     reports it, and only an unreviewed note offers 确认 / 驳回;
 *  7. a review is one write of exactly `{id, reviewer, accept}` followed by a
 *     read of the board it produced — disabled while in flight, and a failed
 *     review keeps the row and prints the gateway's own words.
 *
 * No timers are faked: the screen has no polling, so every assertion is about
 * one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  loadTeamNotes: vi.fn(),
  recordTeamNote: vi.fn(),
  reviewTeamNote: vi.fn(),
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

/** A note tied to a task, with an author. */
function noteWithTask() {
  return {
    id: 'note-a',
    taskId: 'T-7',
    title: '部署顺序',
    author: 'lead',
    body: '先 npm run build，再跑 scripts/verify.sh',
    at: twoMinutesAgo(),
  };
}

/** A note with no author, no task and no time — every optional field absent. */
function bareNote() {
  return { id: 'note-b', title: '无署名笔记', body: '一条短笔记' };
}

describe('TeamMemoryScreen — the list is the service answer', () => {
  it('renders every note in the order it was returned', async () => {
    api.loadTeamNotes.mockResolvedValue([noteWithTask(), bareNote()]);
    renderScreen();

    const list = await screen.findByTestId('memory-list');
    const rows = within(list).getAllByRole('article');
    expect(rows).toHaveLength(2);
    // Newest first is the service's ordering; the screen must not re-sort.
    expect(rows[0]).toHaveTextContent('部署顺序');
    expect(rows[1]).toHaveTextContent('无署名笔记');

    // Author, relative stamp and the note's own words, as reported.
    expect(rows[0]).toHaveTextContent('lead');
    expect(rows[0]).toHaveTextContent(/2 分钟前/);
    expect(rows[0]).toHaveTextContent('先 npm run build，再跑 scripts/verify.sh');

    // One read on mount; nothing polls.
    expect(api.loadTeamNotes).toHaveBeenCalledTimes(1);
  });

  it('says 未知 for a note the gateway did not attribute', async () => {
    api.loadTeamNotes.mockResolvedValue([bareNote()]);
    renderScreen();

    const row = await screen.findByTestId('memory-note-note-b');
    expect(row).toHaveTextContent('未知');
    // No `at` means no age: the screen does not invent "just now".
    expect(row).not.toHaveTextContent(/刚刚|分钟前|小时前|天前/);
  });

  it('shows the empty state when the board holds nothing', async () => {
    api.loadTeamNotes.mockResolvedValue([]);
    renderScreen();

    const empty = await screen.findByTestId('memory-empty');
    expect(empty).toHaveTextContent('还没有共享笔记。');
    expect(screen.queryByTestId('memory-list')).toBeNull();
  });
});

describe('TeamMemoryScreen — a task note deep-links to its task', () => {
  it('links /t/:taskId when the note carries one, and not otherwise', async () => {
    api.loadTeamNotes.mockResolvedValue([noteWithTask(), bareNote()]);
    renderScreen();

    const link = await screen.findByTestId('memory-note-task-note-a');
    expect(link).toHaveAttribute('href', '/t/T-7');
    expect(link).toHaveTextContent('任务 T-7');
    // A free-standing note offers no link to nowhere.
    expect(screen.queryByTestId('memory-note-task-note-b')).toBeNull();
  });
});

describe('TeamMemoryScreen — 记录 writes once, then re-reads the board', () => {
  it('sends the exact payload and shows the board the re-read returned', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes
      .mockResolvedValueOnce([bareNote()])
      .mockResolvedValueOnce([noteWithTask(), bareNote()]);
    api.recordTeamNote.mockResolvedValue(noteWithTask());
    renderScreen();

    await screen.findByTestId('memory-list');
    await user.type(screen.getByTestId('memory-write-title'), '部署顺序');
    await user.type(screen.getByTestId('memory-write-body'), '先 npm run build，再跑 scripts/verify.sh');
    await user.click(screen.getByTestId('memory-write-submit'));

    // The exact wire payload: title/body trimmed, author defaulting to the Lead.
    await waitFor(() =>
      expect(api.recordTeamNote).toHaveBeenCalledWith({
        title: '部署顺序',
        body: '先 npm run build，再跑 scripts/verify.sh',
        author: 'lead',
      }),
    );

    // The write, then a read of the state it produced — in that order.
    await waitFor(() => expect(api.loadTeamNotes).toHaveBeenCalledTimes(2));
    const firstRead = api.loadTeamNotes.mock.invocationCallOrder[0];
    const write = api.recordTeamNote.mock.invocationCallOrder[0];
    const secondRead = api.loadTeamNotes.mock.invocationCallOrder[1];
    expect(firstRead).toBeLessThan(write);
    expect(write).toBeLessThan(secondRead);

    // The re-read's answer is what renders, not the write's response.
    expect(await screen.findByTestId('memory-note-note-a')).toHaveTextContent('部署顺序');
    // The form's text was the write's input; it does not stay behind.
    expect(screen.getByTestId('memory-write-title')).toHaveValue('');
    expect(screen.getByTestId('memory-write-body')).toHaveValue('');
    // The author is kept for the next note.
    expect(screen.getByTestId('memory-write-author')).toHaveValue('lead');
  });

  it('omits an empty body from the payload and uses the typed author', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes.mockResolvedValue([]);
    api.recordTeamNote.mockResolvedValue({ id: 'note-c', title: '只有标题' });
    renderScreen();

    await screen.findByTestId('memory-empty');
    await user.clear(screen.getByTestId('memory-write-author'));
    await user.type(screen.getByTestId('memory-write-author'), 'memoryui');
    await user.type(screen.getByTestId('memory-write-title'), '只有标题');
    await user.click(screen.getByTestId('memory-write-submit'));

    await waitFor(() =>
      expect(api.recordTeamNote).toHaveBeenCalledWith({
        title: '只有标题',
        author: 'memoryui',
      }),
    );
  });

  it('is disabled until the title holds something other than spaces', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes.mockResolvedValue([]);
    renderScreen();

    const submit = await screen.findByTestId('memory-write-submit');
    expect(submit).toBeDisabled();

    const title = screen.getByTestId('memory-write-title');
    await user.type(title, '   ');
    expect(submit).toBeDisabled();

    await user.type(title, '有内容');
    expect(submit).toBeEnabled();

    await user.clear(title);
    expect(submit).toBeDisabled();
    expect(api.recordTeamNote).not.toHaveBeenCalled();
  });

  it('reports a failed write, keeps the typed text, and re-reads nothing', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes.mockResolvedValue([]);
    api.recordTeamNote.mockRejectedValueOnce(new Error('title is required'));
    renderScreen();

    await screen.findByTestId('memory-empty');
    await user.type(screen.getByTestId('memory-write-title'), '会被拒绝');
    await user.click(screen.getByTestId('memory-write-submit'));

    expect(await screen.findByText('title is required')).toBeInTheDocument();
    // Nothing was recorded, so the board on screen is still the last true answer.
    expect(api.loadTeamNotes).toHaveBeenCalledTimes(1);
    // The user's text is not thrown away by the failure.
    expect(screen.getByTestId('memory-write-title')).toHaveValue('会被拒绝');
  });
});

describe('TeamMemoryScreen — a failed read is reported, never hidden', () => {
  it('names the cause and re-reads on 重试', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes.mockRejectedValueOnce(new Error('notes refused')).mockResolvedValue([bareNote()]);
    renderScreen();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('notes refused');

    await user.click(within(alert).getByRole('button', { name: /重\s*试/ }));

    expect(await screen.findByTestId('memory-note-note-b')).toBeInTheDocument();
    expect(api.loadTeamNotes).toHaveBeenCalledTimes(2);
  });
});

describe('TeamMemoryScreen — a clamped body can be expanded', () => {
  const LONG =
    'https://example.test/a/very/long/path/that/never/breaks/' + 'a'.repeat(160);

  it('shows 展开 for a long body, and 收起 once expanded', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes.mockResolvedValue([{ id: 'note-long', title: '长笔记', body: LONG }]);
    renderScreen();

    const toggle = await screen.findByTestId('memory-note-toggle-note-long');
    expect(toggle).toHaveTextContent('展开');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);
    expect(toggle).toHaveTextContent('收起');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');

    await user.click(toggle);
    expect(toggle).toHaveTextContent('展开');
  });

  it('offers no toggle for a body that plainly fits', async () => {
    api.loadTeamNotes.mockResolvedValue([bareNote()]);
    renderScreen();

    await screen.findByTestId('memory-note-note-b');
    expect(screen.queryByTestId('memory-note-toggle-note-b')).toBeNull();
  });
});

describe('TeamMemoryScreen — a phone screen', () => {
  let uninstall = () => {};
  afterEach(() => uninstall());

  it('wraps a long body and keeps the primary action a thumb target', async () => {
    uninstall = installViewport(390);
    const longUrl = 'https://example.test/refund/' + 'a'.repeat(140);
    api.loadTeamNotes.mockResolvedValue([{ id: 'note-url', title: '链接', body: longUrl }]);
    renderScreen();

    const body = await screen.findByTestId('memory-note-body-note-url');
    // One unbroken token must wrap rather than widen the row at 390px.
    expectWrapsOrEllipsis(body, 'the note body');

    const host = document.querySelector('.zk-screen');
    expect(host).not.toBeNull();
    expectNoHorizontalOverflow(host as Element);

    // 记录 is the screen's only filled primary; at 390 it is antd `large` (40px).
    expectTapTarget(screen.getByTestId('memory-write-submit'));
  });
});
/* ------------------------------------------------------------------ review -- */

/** A review that stays in flight until the test resolves it. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

/** A note nobody has reviewed yet. */
function pendingNote(id = 'note-p') {
  return { id, title: '待复核的笔记', body: '需要别人看一眼' };
}

describe('TeamMemoryScreen — every row states its review', () => {
  it('says 未复核 for an unreviewed note and 已复核 · who for an accepted one', async () => {
    api.loadTeamNotes.mockResolvedValue([
      pendingNote('note-new'),
      {
        id: 'note-ok',
        title: '已通过的笔记',
        reviewed: true,
        reviewedBy: 'lead',
        reviewedAt: twoMinutesAgo(),
      },
      { id: 'note-no', title: '被驳回的笔记', reviewed: false, reviewedBy: 'lead' },
    ]);
    renderScreen();

    const unreviewed = await screen.findByTestId('memory-note-review-note-new');
    expect(unreviewed).toHaveTextContent(/^未复核$/);
    // Only an unreviewed note can be moved on.
    // antd inserts a space between two CJK glyphs in a button.
    expect(screen.getByTestId('memory-review-accept-note-new')).toHaveTextContent(/^确\s*认$/);
    expect(screen.getByTestId('memory-review-reject-note-new')).toHaveTextContent(/^驳\s*回$/);

    const accepted = screen.getByTestId('memory-note-review-note-ok');
    expect(accepted).toHaveTextContent('已复核');
    expect(accepted).toHaveTextContent('lead');
    // A review is final: a reviewed note offers neither verb again.
    expect(screen.queryByTestId('memory-review-accept-note-ok')).toBeNull();
    expect(screen.queryByTestId('memory-review-reject-note-ok')).toBeNull();

    const rejected = screen.getByTestId('memory-note-review-note-no');
    expect(rejected).toHaveTextContent(/^已驳回$/);
    expect(screen.queryByTestId('memory-review-accept-note-no')).toBeNull();
    expect(screen.queryByTestId('memory-review-reject-note-no')).toBeNull();
  });

  it('states 已复核 without inventing a reviewer the gateway did not name', async () => {
    api.loadTeamNotes.mockResolvedValue([{ id: 'note-anon', title: '匿名复核', reviewed: true }]);
    renderScreen();

    const state = await screen.findByTestId('memory-note-review-note-anon');
    expect(state).toHaveTextContent(/^已复核$/);
    expect(state).not.toHaveTextContent('未知');
  });
});

describe('TeamMemoryScreen — 确认 records one review, then re-reads', () => {
  it('sends exactly {id, reviewer, accept} and shows the board the re-read returned', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes
      .mockResolvedValueOnce([pendingNote('note-p')])
      .mockResolvedValueOnce([
        { id: 'note-p', title: '待复核的笔记', reviewed: true, reviewedBy: 'lead' },
      ]);
    api.reviewTeamNote.mockResolvedValue({
      id: 'note-p',
      title: '待复核的笔记',
      reviewed: true,
      reviewedBy: 'lead',
    });
    renderScreen();

    const accept = await screen.findByTestId('memory-review-accept-note-p');
    await user.click(accept);

    await waitFor(() => expect(api.reviewTeamNote).toHaveBeenCalledTimes(1));
    // The exact wire payload: the contract's three required fields and nothing
    // the caller did not ask for (`at` stays the gateway's to stamp).
    expect(api.reviewTeamNote.mock.calls[0][0]).toEqual({
      id: 'note-p',
      reviewer: 'lead',
      accept: true,
    });

    // The write, then a read of the state it produced — in that order.
    await waitFor(() => expect(api.loadTeamNotes).toHaveBeenCalledTimes(2));
    const write = api.reviewTeamNote.mock.invocationCallOrder[0];
    const secondRead = api.loadTeamNotes.mock.invocationCallOrder[1];
    expect(write).toBeLessThan(secondRead);

    // The re-read is what renders, not the write's answer.
    expect(await screen.findByTestId('memory-note-review-note-p')).toHaveTextContent('已复核');
    expect(screen.queryByTestId('memory-review-accept-note-p')).toBeNull();
  });
});

describe('TeamMemoryScreen — 驳回 asks first, then records accept:false', () => {
  it('sends nothing until the confirm, then re-reads the board', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes
      .mockResolvedValueOnce([pendingNote('note-p')])
      .mockResolvedValueOnce([
        { id: 'note-p', title: '待复核的笔记', reviewed: false, reviewedBy: 'lead' },
      ]);
    api.reviewTeamNote.mockResolvedValue({
      id: 'note-p',
      title: '待复核的笔记',
      reviewed: false,
      reviewedBy: 'lead',
    });
    renderScreen();

    await user.click(await screen.findByTestId('memory-review-reject-note-p'));
    // Opening the question is not an answer: no write yet.
    expect(api.reviewTeamNote).not.toHaveBeenCalled();

    await user.click(await screen.findByRole('button', { name: /确认驳回/ }));

    await waitFor(() => expect(api.reviewTeamNote).toHaveBeenCalledTimes(1));
    expect(api.reviewTeamNote.mock.calls[0][0]).toEqual({
      id: 'note-p',
      reviewer: 'lead',
      accept: false,
    });
    await waitFor(() => expect(api.loadTeamNotes).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId('memory-note-review-note-p')).toHaveTextContent(/^已驳回$/);
  });
});

describe('TeamMemoryScreen — one review at a time', () => {
  it('disables both verbs while the review is in flight and sends it once', async () => {
    const user = userEvent.setup();
    const inFlight = deferred<unknown>();
    api.loadTeamNotes
      .mockResolvedValueOnce([pendingNote('note-p'), pendingNote('note-q')])
      .mockResolvedValueOnce([
        { id: 'note-p', title: '待复核的笔记', reviewed: true, reviewedBy: 'lead' },
        pendingNote('note-q'),
      ]);
    api.reviewTeamNote.mockReturnValue(inFlight.promise);
    renderScreen();

    const accept = await screen.findByTestId('memory-review-accept-note-p');
    await user.click(accept);

    expect(accept).toBeDisabled();
    expect(screen.getByTestId('memory-review-reject-note-p')).toBeDisabled();
    // Another row cannot start a second review over the first one.
    expect(screen.getByTestId('memory-review-accept-note-q')).toBeDisabled();
    await user.click(accept);
    expect(api.reviewTeamNote).toHaveBeenCalledTimes(1);

    inFlight.resolve(undefined);
    await waitFor(() => expect(api.loadTeamNotes).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId('memory-note-review-note-p')).toHaveTextContent('已复核');
  });
});

describe('TeamMemoryScreen — a failed review changes nothing on screen', () => {
  it('keeps the row and prints the gateway text', async () => {
    const user = userEvent.setup();
    api.loadTeamNotes.mockResolvedValue([pendingNote('note-p')]);
    api.reviewTeamNote.mockRejectedValueOnce(new Error('note is already reviewed'));
    renderScreen();

    await user.click(await screen.findByTestId('memory-review-accept-note-p'));

    const alert = await screen.findByTestId('memory-review-error');
    expect(alert).toHaveTextContent('note is already reviewed');
    // Nothing changed, so nothing was read again: the row is still the last
    // true answer and its verbs still work.
    expect(api.loadTeamNotes).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('memory-note-review-note-p')).toHaveTextContent(/^未复核$/);
    expect(screen.getByTestId('memory-review-accept-note-p')).toBeEnabled();
  });
});
