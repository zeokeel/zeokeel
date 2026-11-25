/**
 * The roster (M6) over the WS control plane: `users.snapshot`, `users.upsert`
 * and `users.act` (docs/TRANSPORT.md §5.1, `src/design/ws-contract.ts`).
 *
 * **This is a policy table, not an authentication system.** There is no login,
 * no password, no session: the gateway trusts the browser on this machine, and
 * `users.act` records which member this browser is *acting as* in a settings row
 * (`gateway.actingUser`). A `User` therefore carries no credential and no
 * contact address — only the identity and the role the permission table needs
 * (`contracts.User`). The screen says this out loud rather than letting a roster
 * of people read like sign-in.
 *
 * **`users.upsert` takes the whole member.** The generated contract requires
 * `id`, `name` *and* `role` (no optional field, no patch shape), so changing one
 * field means sending the row as stored with that field changed — which is what
 * {@link UserUpsertInput} models. A role-only request would be rejected as
 * incomplete at the wire, and a name left empty is invalid on its own
 * (`contracts.User.Name` must not be empty).
 *
 * Neither method has a delta stream, so a screen reads once and reads again
 * after its own write. A poll would be the only way to notice another client's
 * change, and inventing one is the polling loop AGENTS.md §3 forbids.
 *
 * Parameters are exactly the ones the generated contract allows; nothing is
 * added here that the gateway would reject.
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/**
 * The role values (`contracts.UserRole*`), ordered by how much of the product
 * they may change — the order *is* part of the policy.
 *
 * These are protocol values, not copy: the screen maps them to its own words
 * ({@link UserRole} → catalogue keys) and shows a value this build does not know
 * verbatim rather than folding it into a default.
 */
export type UserRole = 'owner' | 'manager' | 'collaborator';

export const USER_ROLES: readonly UserRole[] = ['owner', 'manager', 'collaborator'];

/**
 * One member of the roster: a persistent actor a bot, a task or a memory note
 * can be attributed to.
 *
 * `role` is kept as the string the gateway sent, including a value this build
 * does not know: a role is a permission promise, and inventing a default would
 * either over-grant (`owner`) or hide the record's real state.
 *
 * `createdAt` is RFC 3339 and absent only when the gateway could not stamp the
 * record — "added at an unknown time", never "added now".
 */
export interface User {
  id: string;
  name: string;
  role: string;
  createdAt?: string;
}

/**
 * The roster plus the identity this browser acts as.
 *
 * `actingUserId`/`actingRole` are the gateway's answer, not the last request's
 * echo: the gateway falls back to the first owner when the stored setting names
 * nobody, so those two fields are the only authority on who is acting.
 */
export interface UsersSnapshot {
  users: User[];
  actingUserId?: string;
  actingRole?: string;
}

/**
 * One member, whole. Every field the contract requires is here because a change
 * is always the whole row (see the file header): there is no partial member.
 */
export interface UserUpsertInput {
  id: string;
  name: string;
  role: string;
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

/** One wire member, with the optional stamp present only when the record has it. */
function normalizeUser(row: Partial<User>): User {
  return {
    id: String(row.id ?? ''),
    name: String(row.name ?? ''),
    role: String(row.role ?? ''),
    ...(row.createdAt ? { createdAt: String(row.createdAt) } : {}),
  };
}

/** Every member and the acting identity (`users.snapshot`, no parameters). */
export async function loadUsers(): Promise<UsersSnapshot> {
  const result = await request<{
    users?: Array<Partial<User>>;
    actingUserId?: string;
    actingRole?: string;
  }>('users.snapshot', {});
  return {
    users: (result.users ?? []).map(normalizeUser),
    // Omitted rather than empty: "no acting identity" is the absence of the
    // pair, and `''` would read as a member whose id is blank.
    ...(result.actingUserId ? { actingUserId: String(result.actingUserId) } : {}),
    ...(result.actingRole ? { actingRole: String(result.actingRole) } : {}),
  };
}

/**
 * Create or replace one member (`users.upsert`), returning the stored record.
 *
 * Creating and updating are the same call — the gateway decides which by whether
 * the id is already stored — so every caller sends the whole row. Form values
 * are trimmed here because an id of `" carol "` is a different record, not a
 * stylistic variant, and the gateway's alphabet would refuse it anyway.
 */
export async function upsertUser(input: UserUpsertInput): Promise<User> {
  const params = {
    id: input.id.trim(),
    name: input.name.trim(),
    role: input.role.trim(),
  };
  const result = await request<Partial<User>>('users.upsert', params);
  // The identity is ours; the rest is the store's answer (it stamps `createdAt`).
  return normalizeUser({ ...params, ...result, id: params.id });
}

/**
 * Act as one member (`users.act`), returning the id the gateway now holds.
 *
 * The parameter is exactly `{id}`: the switch says *who*, and the gateway keeps
 * everything else about the record. The returned id is the gateway's echo, so a
 * caller can tell that the switch landed without trusting its own request.
 */
export async function actAsUser(id: string): Promise<string> {
  const result = await request<{ actingUserId?: string }>('users.act', { id });
  return String(result.actingUserId ?? id);
}