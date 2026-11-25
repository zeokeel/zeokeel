/**
 * Task skill drafts (`skilldrafts.snapshot`, `skilldrafts.propose`,
 * `skilldrafts.decide` — docs/TRANSPORT.md §5.1).
 *
 * A finished task can propose a six-section SKILL.md draft (G12: "这单活干完了，
 * 要不要把它变成技能"). The gateway stores those proposals and reports them
 * newest first; this module is only the wire half — what a draft *means* to a
 * reader is the screen's business.
 *
 * The surface is **request/response**, not a pushed domain: `skilldrafts`
 * carries no snapshot/delta stream (`skilldrafts.snapshot` is a plain read that
 * happens to share the name), so the client reads once and reads again after
 * its own decision. A poll would be the only way to notice a draft proposed
 * while the screen is open, and inventing one is the polling loop AGENTS.md §3
 * forbids.
 *
 * Parameters are exactly the ones the generated contract allows
 * (`src/design/ws-contract.ts` — `skilldrafts.snapshot` takes `{limit?}`,
 * `skilldrafts.propose` requires `name` with `taskId`/`summary`/`body`
 * optional, and `skilldrafts.decide` requires `id`/`accept`/`reviewer`), so a
 * stray field fails here instead of on the wire.
 *
 * `accept` is a real boolean, not a string: the gateway refuses a decide call
 * that omits it (it decodes into `*bool` precisely so "missing" cannot read as
 * "rejected"), and a client that sent `"false"` would be accepting.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/**
 * The three states a draft can be in, as the store owns them
 * (`internal/contracts/skilldraft.go`): a draft is written `pending`, moves to
 * `accepted` or `rejected` on a human decision, and a rejection is terminal.
 *
 * Kept as plain strings in {@link SkillDraft.status} rather than a closed union:
 * the vocabulary belongs to the gateway, and an unknown value must render as
 * the gateway's own word instead of being silently mapped onto one of the
 * three — mapping it to `pending` would offer accept/reject on something a
 * human may already have decided.
 */
export const SKILL_DRAFT_PENDING = 'pending';
export const SKILL_DRAFT_ACCEPTED = 'accepted';
export const SKILL_DRAFT_REJECTED = 'rejected';

/**
 * One draft, as the gateway reports it.
 *
 * `id` is the store's opaque key (random — one task may propose more than one
 * skill), and is the only field a decide call needs. `taskId` is the finished
 * task the draft came from; absent means it was not derived from a task. `body`
 * is the six-section Markdown a human would edit before the draft is accepted,
 * and `createdAt` is RFC 3339, absent only when the gateway could not stamp it.
 */
export interface SkillDraft {
  id: string;
  taskId?: string;
  /** The skill slug the draft would become (`[a-z0-9-]{2,64}`). */
  name: string;
  /** The one-line "when to use" excerpt the list shows. */
  summary?: string;
  /** The rendered six-section Markdown, kept byte for byte by a decision. */
  body?: string;
  /** {@link SKILL_DRAFT_PENDING} / `_ACCEPTED` / `_REJECTED` (or a new value). */
  status: string;
  /** Who decided, when someone has; empty/absent while pending. */
  reviewer?: string;
  createdAt?: string;
}

/** The propose payload: `name` is the only required field (contract table). */
export interface ProposeSkillDraftInput {
  name: string;
  taskId?: string;
  summary?: string;
  body?: string;
}

/** The decide payload: the contract's three required fields, nothing else. */
export interface DecideSkillDraftInput {
  /** The draft's opaque store id, exactly as the list reported it. */
  id: string;
  /** `true` accepts the draft, `false` rejects it — the verdict is not a string. */
  accept: boolean;
  /** Who is recording the decision (`lead` for this client's only queue). */
  reviewer: string;
}

/** One wire draft, kept loose: the gateway omits empty fields (`omitempty`). */
interface SkillDraftWire {
  id?: unknown;
  taskId?: unknown;
  name?: unknown;
  summary?: unknown;
  body?: unknown;
  status?: unknown;
  reviewer?: unknown;
  createdAt?: unknown;
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

/**
 * The wire shape → {@link SkillDraft}. Absent optional fields stay absent
 * rather than becoming empty strings: "the gateway did not name a reviewer" and
 * "the reviewer is the empty name" must not read the same, and a body that was
 * never rendered must fall through as "no body" instead of an empty paragraph.
 */
function fromWire(raw: SkillDraftWire): SkillDraft {
  return {
    id: typeof raw.id === 'string' ? raw.id : '',
    ...(text(raw.taskId) ? { taskId: text(raw.taskId) } : {}),
    name: text(raw.name) ?? '',
    ...(text(raw.summary) ? { summary: text(raw.summary) } : {}),
    ...(text(raw.body) ? { body: text(raw.body) } : {}),
    // An unreadable status is stated as unknown, never defaulted: the screen
    // offers no verb unless this is exactly `pending`.
    status: text(raw.status) ?? '',
    ...(text(raw.reviewer) ? { reviewer: text(raw.reviewer) } : {}),
    ...(text(raw.createdAt) ? { createdAt: text(raw.createdAt) } : {}),
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
 * Read the queue once, newest first as the service orders it. No delta will
 * follow — call again after a decision to see the result.
 *
 * `limit` is passed through only when given; the gateway's own default (50,
 * capped at 200) is what a screen that sends nothing gets, and re-stating it
 * here would be a second default to keep in sync.
 */
export async function loadSkillDrafts(limit?: number): Promise<SkillDraft[]> {
  const params: Record<string, unknown> = {};
  if (limit !== undefined) params.limit = limit;

  const result = await request<{ drafts?: SkillDraftWire[] }>('skilldrafts.snapshot', params);
  return (result.drafts ?? []).map(fromWire);
}

/**
 * Propose one draft (`skilldrafts.propose`). Normally the product files this
 * itself when a task is accepted; the client keeps the call so a surface with
 * more context (a task screen) can propose without duplicating the wire shape.
 *
 * The gateway answers with the stored draft — `id`, `status: pending` and the
 * rendered body are its to fill in, so the caller-supplied `name` is the only
 * thing to trust in the answer.
 */
export async function proposeSkillDraft(input: ProposeSkillDraftInput): Promise<SkillDraft> {
  const params: Record<string, unknown> = { name: input.name };
  if (input.taskId) params.taskId = input.taskId;
  if (input.summary) params.summary = input.summary;
  if (input.body) params.body = input.body;
  return fromWire(await request<SkillDraftWire>('skilldrafts.propose', params));
}

/**
 * Record one decision (`skilldrafts.decide`). Accepting records the decision
 * only — it does **not** install the skill; writing `<dataDir>/skills/<slug>`
 * stays the skills editor's job, fed by a human.
 *
 * The gateway answers with the decided draft; callers that want the queue still
 * re-read it, because the single row answered for is not the queue (a decision
 * may also have been recorded elsewhere between the two calls).
 */
export async function decideSkillDraft(input: DecideSkillDraftInput): Promise<SkillDraft> {
  return fromWire(
    await request<SkillDraftWire>('skilldrafts.decide', {
      id: input.id,
      accept: input.accept,
      reviewer: input.reviewer,
    }),
  );
}