/**
 * The bot entity (plan G1) over the WS control plane: `bots.snapshot` and
 * `bots.upsert` (docs/TRANSPORT.md §5.1).
 *
 * A Bot = 模板 + 记忆作用域 + 工具授予 + 默认模型. The two halves are different
 * kinds of thing, and this layer keeps them apart:
 *
 *  - `BotTemplate` is reusable and read-only **here**: templates are seeded by
 *    the backend (`contracts.BotTemplate`), and this build has no method that
 *    writes one. The screen therefore never pretends to offer an edit.
 *  - `BotInstance` is the persistent, owned record a task is assigned to. It
 *    stores the *resolved* policy it was created with, so a later template edit
 *    cannot silently widen an existing bot's grants.
 *
 * **`bots.upsert` on an existing bot is replacement-with-rules.**
 * `contracts.BotService.Update` takes the record's new state, and an empty value
 * does not always mean "unchanged":
 *
 *   - `defaultModel: ""` is a real value ("follow the session") and is written
 *     verbatim, as is `owner: ""` ("unowned");
 *   - `name` / `memoryScope` / `status: ""` keep what is stored;
 *   - `allowedTools` omitted keeps the stored grant, `[]` clears it.
 *
 * A caller who sends only the field they changed would therefore blank the
 * model or the owner of a bot they never looked at. That is why the screen sends
 * the **whole row** with one field changed (snapshot → modify → upsert, the
 * natural flow the gateway documents), and why {@link BotUpsertInput} carries
 * every mutable field rather than a patch.
 *
 * Neither method has a delta stream, so a screen reads once and reads again
 * after its own write. A poll would be the only way to notice another client's
 * change, and inventing one is the polling loop AGENTS.md §3 forbids.
 *
 * Parameters are exactly the ones the generated contract allows
 * (`src/design/ws-contract.ts`); an absent optional value is omitted rather than
 * sent as an empty string, because the two are different requests.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/**
 * The statuses a bot record can carry (`contracts.BotStatus*`).
 *
 * These are **protocol values**, not copy: the screen compares them and shows
 * its own words. `paused` is "offline on purpose" — it keeps identity, memory
 * and grants — and `retired` is terminal: nothing may reactivate it.
 */
export type BotStatus = 'active' | 'paused' | 'retired';

export const BOT_STATUSES: readonly BotStatus[] = ['active', 'paused', 'retired'];

/**
 * A reusable岗位 definition. Read-only on this surface: it says what the role
 * *is* (name, role, default model, tool whitelist, memory scope).
 *
 * `allowedTools` empty means "the role's read-only default set", never
 * "everything" — the screen says so rather than printing a zero.
 */
export interface BotTemplate {
  id: string;
  name: string;
  role?: string;
  description?: string;
  defaultModel?: string;
  /** The岗位说明 text; carried because the wire type has it, not edited here. */
  systemPrompt?: string;
  allowedTools?: string[];
  memoryScope?: string;
}

/** One persistent bot: a template plus its own identity, owner and policy. */
export interface BotInstance {
  id: string;
  templateId: string;
  name: string;
  owner?: string;
  defaultModel?: string;
  allowedTools?: string[];
  memoryScope?: string;
  /** One of {@link BOT_STATUSES}; an unknown value is passed through verbatim. */
  status: string;
  createdAt?: string;
  /** Absent means "never seen since creation" — not "zero milliseconds ago". */
  lastSeenAt?: string;
}

/** Both halves in one answer: a screen that shows one without the other cannot
 * answer "which bot can I dispatch to?". */
export interface BotsSnapshot {
  templates: BotTemplate[];
  instances: BotInstance[];
}

/**
 * One instance, whole. Every mutable field is here because a status change is
 * sent as the full row (see the file header): a partial payload is a request to
 * erase the fields it omits.
 */
export interface BotUpsertInput {
  id: string;
  templateId: string;
  name?: string;
  owner?: string;
  defaultModel?: string;
  allowedTools?: string[];
  memoryScope?: string;
  status?: string;
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

/** Trimmed non-empty value, or `undefined` so the key is omitted from the request. */
function optional(value: string | undefined): string | undefined {
  if (value === undefined) return undefined;
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

/** Tool names are protocol values: kept verbatim, in the order the record lists them. */
function tools(value: string[] | undefined): string[] | undefined {
  return Array.isArray(value) ? value.map((name) => String(name)) : undefined;
}

/** One wire template, with its optional fields present only when it has them. */
function normalizeTemplate(row: Partial<BotTemplate>): BotTemplate {
  return {
    id: String(row.id ?? ''),
    name: String(row.name ?? ''),
    ...(row.role ? { role: String(row.role) } : {}),
    ...(row.description ? { description: String(row.description) } : {}),
    ...(row.defaultModel ? { defaultModel: String(row.defaultModel) } : {}),
    ...(row.systemPrompt ? { systemPrompt: String(row.systemPrompt) } : {}),
    ...(row.allowedTools ? { allowedTools: tools(row.allowedTools) } : {}),
    ...(row.memoryScope ? { memoryScope: String(row.memoryScope) } : {}),
  };
}

/**
 * One wire instance.
 *
 * `status` is kept as the string the gateway sent, including a value this build
 * does not know: a status the client cannot name is a fact about the record, and
 * inventing a default would hide it (`active` would even imply dispatchable).
 */
function normalizeInstance(row: Partial<BotInstance>): BotInstance {
  return {
    id: String(row.id ?? ''),
    templateId: String(row.templateId ?? ''),
    name: String(row.name ?? ''),
    status: String(row.status ?? ''),
    ...(row.owner ? { owner: String(row.owner) } : {}),
    ...(row.defaultModel ? { defaultModel: String(row.defaultModel) } : {}),
    ...(row.allowedTools ? { allowedTools: tools(row.allowedTools) } : {}),
    ...(row.memoryScope ? { memoryScope: String(row.memoryScope) } : {}),
    ...(row.createdAt ? { createdAt: String(row.createdAt) } : {}),
    ...(row.lastSeenAt ? { lastSeenAt: String(row.lastSeenAt) } : {}),
  };
}

/** Every template and every instance (`bots.snapshot`, no parameters). */
export async function loadBots(): Promise<BotsSnapshot> {
  const result = await request<{
    templates?: Array<Partial<BotTemplate>>;
    instances?: Array<Partial<BotInstance>>;
  }>('bots.snapshot', {});
  return {
    templates: (result.templates ?? []).map(normalizeTemplate),
    instances: (result.instances ?? []).map(normalizeInstance),
  };
}

/**
 * Create or replace one instance (`bots.upsert`), returning the stored record.
 *
 * Creating and updating are the same call — the gateway decides which by whether
 * the id is already stored — so the caller must always send a complete row for
 * an existing bot. See the file header for what an empty field would mean.
 */
export async function upsertBot(input: BotUpsertInput): Promise<BotInstance> {
  const params: Record<string, unknown> = { id: input.id, templateId: input.templateId };
  const name = optional(input.name);
  if (name !== undefined) params.name = name;
  // `owner` and `defaultModel` are sent even when empty *if the caller passed
  // them*, but an undefined one means "this call did not speak about it": the
  // screen always passes both for an existing row, so the distinction stays
  // with the caller rather than being invented here.
  if (input.owner !== undefined) params.owner = input.owner;
  if (input.defaultModel !== undefined) params.defaultModel = input.defaultModel;
  if (input.allowedTools !== undefined) params.allowedTools = [...input.allowedTools];
  const memoryScope = optional(input.memoryScope);
  if (memoryScope !== undefined) params.memoryScope = memoryScope;
  const status = optional(input.status);
  if (status !== undefined) params.status = status;

  const result = await request<Partial<BotInstance>>('bots.upsert', params);
  // The identity is ours; the rest is the store's answer. A gateway that answers
  // with nothing still leaves a usable row, so the request's own values fill in.
  return normalizeInstance({
    ...input,
    ...result,
    id: input.id,
    templateId: input.templateId,
    status: String(result.status ?? input.status ?? ''),
  });
}