/**
 * The team's shared note board (`team.notes`, `team.note.record` —
 * docs/TRANSPORT.md §5.1).
 *
 * It is a **request/response** surface, not a pushed domain: there is no
 * `team.notes` snapshot/delta stream, so the client reads once and reads again
 * after its own write. A poll would be the only way to notice a note another
 * teammate recorded, and inventing one is the polling loop AGENTS.md §3
 * forbids.
 *
 * Parameters are exactly the ones the generated contract allows
 * (`src/design/ws-contract.ts` — `team.notes` takes `{limit?}`,
 * `team.note.record` requires `title` with `id`/`taskId`/`body`/`author`
 * optional, and `team.note.review` requires `id`/`reviewer`/`accept` with `at`
 * optional), so a stray field fails here instead of on the wire.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/**
 * One shared note, as the gateway reports it.
 *
 * The gateway stores notes as memories of type `team-note`, so `id` is a
 * memory URI (`mem://…/team-note/<id>`) — a stable key, never shown as copy.
 * `taskId` ties the note to a work-board task; empty/absent means a
 * free-standing note. `at` is RFC 3339 and absent only when the gateway could
 * not stamp the record.
 */
export interface TeamNote {
  id: string;
  taskId?: string;
  title: string;
  body?: string;
  author?: string;
  at?: string;
  /**
   * The reviewer's verdict: `true` accepted, `false` rejected, **absent** until
   * someone reviewed it. Absent is not `false` here — "nobody has looked" and
   * "somebody said no" are different states and must read differently.
   */
  reviewed?: boolean;
  /** Who recorded the review, when the gateway names them. */
  reviewedBy?: string;
  /** When the review was recorded (RFC 3339), when the gateway stamped one. */
  reviewedAt?: string;
}

/** The write payload: `title` is the only required field (contract table). */
export interface RecordTeamNoteInput {
  title: string;
  body?: string;
  author?: string;
  /** Ties the note to a task; replaces that task's note (deterministic URI). */
  taskId?: string;
}

/** One wire note, kept loose: the gateway omits empty fields (`omitempty`). */
interface TeamNoteWire {
  id?: unknown;
  taskId?: unknown;
  title?: unknown;
  body?: unknown;
  author?: unknown;
  at?: unknown;
  reviewed?: unknown;
  reviewedBy?: unknown;
  reviewedAt?: unknown;
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

/**
 * The wire shape → {@link TeamNote}. Absent fields stay absent rather than
 * becoming empty strings: "the gateway did not say who wrote it" and "the
 * author is the empty name" must render differently (`未知` vs a blank line).
 */
function fromWire(raw: TeamNoteWire): TeamNote {
  return {
    id: typeof raw.id === 'string' ? raw.id : '',
    ...(text(raw.taskId) ? { taskId: text(raw.taskId) } : {}),
    title: text(raw.title) ?? '',
    ...(text(raw.body) ? { body: text(raw.body) } : {}),
    ...(text(raw.author) ? { author: text(raw.author) } : {}),
    ...(text(raw.at) ? { at: text(raw.at) } : {}),
    // `reviewed` is a tri-state, so only a real boolean is carried across: a
    // missing field must stay missing instead of reading as "rejected".
    ...(typeof raw.reviewed === 'boolean' ? { reviewed: raw.reviewed } : {}),
    ...(text(raw.reviewedBy) ? { reviewedBy: text(raw.reviewedBy) } : {}),
    ...(text(raw.reviewedAt) ? { reviewedAt: text(raw.reviewedAt) } : {}),
  };
}

/**
 * One WS request, gated like every other bridge in this layer: the
 * fragment→cookie exchange must have run, or the socket's first frame is a
 * guaranteed 401 that looks like a broken feature (see `api/http.ts`).
 */
async function request<T>(method: string, params: Record<string, unknown>): Promise<T> {
  await authorizationReady();
  return gatewayRpc().request<T>(method, params);
}

/**
 * Read the board once, newest first as the service orders it. No delta will
 * follow — call again after a write to see the result.
 *
 * `limit` is passed through only when given; the gateway's own default (50,
 * capped at 200) is what a screen that sends nothing gets, and re-stating it
 * here would be a second default to keep in sync.
 */
export async function loadTeamNotes(limit?: number): Promise<TeamNote[]> {
  const params: Record<string, unknown> = {};
  if (limit !== undefined) params.limit = limit;

  const result = await request<{ notes?: TeamNoteWire[] }>('team.notes', params);
  return (result.notes ?? []).map(fromWire);
}

/**
 * Record one note (`team.note.record`). The gateway returns it with `id` and
 * `at` filled in; callers that want the new board still re-read it, because a
 * record may have replaced an earlier note for the same task.
 */
export async function recordTeamNote(input: RecordTeamNoteInput): Promise<TeamNote> {
  const params: Record<string, unknown> = { title: input.title };
  if (input.body) params.body = input.body;
  if (input.author) params.author = input.author;
  if (input.taskId) params.taskId = input.taskId;
  return fromWire(await request<TeamNoteWire>('team.note.record', params));
}

/**
 * The review payload: the contract's three required fields, and `at` only when
 * the caller has a stamp of its own (normally the gateway stamps the review).
 */
export interface ReviewTeamNoteInput {
  /** The note's memory URI, exactly as the list reported it. */
  id: string;
  /** Who is recording the review (`lead` for this client's only board). */
  reviewer: string;
  /** `true` accepts the note, `false` rejects it — the verdict is not a string. */
  accept: boolean;
  at?: string;
}

/**
 * Record one review of a note (`team.note.review`). The gateway answers with
 * the reviewed note; callers that want the board still re-read it, because the
 * single row answered for is not the board (a review may also have been
 * recorded elsewhere between the two calls).
 */
export async function reviewTeamNote(input: ReviewTeamNoteInput): Promise<TeamNote> {
  const params: Record<string, unknown> = {
    id: input.id,
    reviewer: input.reviewer,
    accept: input.accept,
  };
  if (input.at) params.at = input.at;
  return fromWire(await request<TeamNoteWire>('team.note.review', params));
}

/* --------------------------------------------------------- suggestions -- */

/**
 * One proposed team rule waiting for a person (`team.suggestions`).
 *
 * This is the queue **in front of** the board: a bot proposes a rule and
 * nothing reaches the shared board until a human promotes it — that is what
 * "团队建议经你批准后另一队友可见" means. `id` identifies the proposal, not the
 * note a promotion will create.
 */
export interface TeamNoteSuggestion {
  id: string;
  /** The one-line rule (the contract's only required field). */
  title: string;
  /** The explanation, when the proposer wrote one. */
  body?: string;
  /** Who proposed it — normally a bot's name. Absent is not an empty name. */
  author?: string;
  /** RFC 3339; absent only when the gateway could not stamp the record. */
  at?: string;
}

/** One wire suggestion, kept loose: the gateway omits empty fields (`omitempty`). */
interface TeamNoteSuggestionWire {
  id?: unknown;
  title?: unknown;
  body?: unknown;
  author?: unknown;
  at?: unknown;
}

/** Wire → {@link TeamNoteSuggestion}; absent fields stay absent, as for notes. */
function suggestionFromWire(raw: TeamNoteSuggestionWire): TeamNoteSuggestion {
  return {
    id: typeof raw.id === 'string' ? raw.id : '',
    title: text(raw.title) ?? '',
    ...(text(raw.body) ? { body: text(raw.body) } : {}),
    ...(text(raw.author) ? { author: text(raw.author) } : {}),
    ...(text(raw.at) ? { at: text(raw.at) } : {}),
  };
}

/**
 * Read the proposal queue once, newest first as the service orders it. No
 * delta follows — call again after a write.
 *
 * `limit` is passed through only when given, for the same reason as
 * {@link loadTeamNotes}: the gateway's own default is the one to keep.
 */
export async function loadNoteSuggestions(limit?: number): Promise<TeamNoteSuggestion[]> {
  const params: Record<string, unknown> = {};
  if (limit !== undefined) params.limit = limit;

  const result = await request<{ suggestions?: TeamNoteSuggestionWire[] }>(
    'team.suggestions',
    params,
  );
  return (result.suggestions ?? []).map(suggestionFromWire);
}

/** The propose payload: `title` required, `body`/`author` optional (contract). */
export interface SuggestNoteInput {
  title: string;
  body?: string;
  author?: string;
}

/**
 * Queue one proposal (`team.suggest`). It **never touches the board** — only a
 * promotion does — so a caller that proposes has nothing to re-read but the
 * queue itself.
 */
export async function suggestNote(input: SuggestNoteInput): Promise<TeamNoteSuggestion> {
  const params: Record<string, unknown> = { title: input.title };
  if (input.body) params.body = input.body;
  if (input.author) params.author = input.author;
  return suggestionFromWire(await request<TeamNoteSuggestionWire>('team.suggest', params));
}

/** The decision payload both verbs take: which proposal, and who decided. */
export interface SuggestionDecisionInput {
  /** The proposal's id, exactly as the queue reported it. */
  id: string;
  /** Who is deciding (`lead` for this client's queue). */
  reviewer: string;
}

/**
 * Promote one proposal onto the shared board (`team.suggestion.promote`). The
 * gateway answers with the note it created, and **both** lists changed: the
 * queue lost a row and the board gained one, so callers re-read both.
 */
export async function promoteSuggestion(input: SuggestionDecisionInput): Promise<TeamNote> {
  return fromWire(
    await request<TeamNoteWire>('team.suggestion.promote', {
      id: input.id,
      reviewer: input.reviewer,
    }),
  );
}

/**
 * Dismiss one proposal without creating a note (`team.suggestion.dismiss`). The
 * gateway answers `{ok:true}`; callers still re-read the queue and the board,
 * because "the board did not change" is the board's answer to give.
 */
export async function dismissSuggestion(input: SuggestionDecisionInput): Promise<void> {
  await request<{ ok?: unknown }>('team.suggestion.dismiss', {
    id: input.id,
    reviewer: input.reviewer,
  });
}
