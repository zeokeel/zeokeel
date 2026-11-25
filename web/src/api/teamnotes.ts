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
 * (`src/design/ws-contract.ts` — `team.notes` takes `{limit?}`, and
 * `team.note.record` requires `title` with `id`/`taskId`/`body`/`author`
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