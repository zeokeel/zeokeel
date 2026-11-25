/**
 * The role→action table, as the roster screen renders it.
 *
 * **The gateway is the enforcement point.** `contracts.Allowed`
 * (`internal/contracts/users.go`) is the single decision function; it denies by
 * default, and the gateway calls it on every privileged dispatch. This module
 * exists for one reason only: the screen must explain, in five rows and in the
 * user's language, *what a role decides*, and that explanation has to be the
 * same table — a legend that promises an action the gateway then refuses is
 * worse than no legend.
 *
 * So the matrix is data, it is asserted against `contracts.Allowed` by
 * `users-api.test.ts`, and it is display-only: nothing here gates a request, and
 * a client must never be the reason an action is allowed.
 *
 * The five actions are the whole surface a role decides. Anything finer — which
 * tool, which file, which host — stays with the approval gate and the sandbox,
 * because a role table that also encoded tool grants would be a second, silently
 * diverging policy.
 */
import type { UserRole } from '@/api/users';

/** One thing a person may do to the shared product (`contracts.UserAction`). */
export type UserAction =
  | 'dispatch'
  | 'approve'
  | 'manage-bots'
  | 'review-memory'
  | 'decide-budget';

/** The five actions, in the order the contract documents them. */
export const USER_ACTIONS: readonly UserAction[] = [
  'dispatch',
  'approve',
  'manage-bots',
  'review-memory',
  'decide-budget',
];

/**
 * One row of the table: the verdict for all three roles on the same row, so the
 * matrix reads as a matrix and a new action cannot grant a role by omission.
 */
interface RoleRow {
  action: UserAction;
  owner: boolean;
  manager: boolean;
  collaborator: boolean;
}

/**
 * The policy, verbatim from `internal/contracts/users.go` — same verdicts, same
 * reasons:
 *
 *  - dispatch: everyone. Giving a shared bot work is the point of sharing it,
 *    and dispatch alone spends nothing and edits no policy.
 *  - approve: owner + manager. Approving releases a write, a send or a delete;
 *    a collaborator may propose work but must not sign it off.
 *  - manage-bots: owner only. It changes *who may act and what they may touch*,
 *    so a manager who held it could grant themselves everything else.
 *  - review-memory: owner + manager. Approving a team-scope write publishes a
 *    belief to every bot; collaborators write suggestions, they do not approve
 *    their own.
 *  - decide-budget: owner + manager. Money is the least reversible resource and
 *    a budget is account-level.
 */
const ROLE_TABLE: readonly RoleRow[] = [
  { action: 'dispatch', owner: true, manager: true, collaborator: true },
  { action: 'approve', owner: true, manager: true, collaborator: false },
  { action: 'manage-bots', owner: true, manager: false, collaborator: false },
  { action: 'review-memory', owner: true, manager: true, collaborator: false },
  { action: 'decide-budget', owner: true, manager: true, collaborator: false },
];

/**
 * Whether the table says `role` may perform `action`.
 *
 * Deny by default, exactly like `contracts.Allowed`: an unknown role and an
 * unknown action both return false. This is the legend's source, not a gate.
 */
export function roleAllows(role: string, action: UserAction): boolean {
  const row = ROLE_TABLE.find((candidate) => candidate.action === action);
  if (row === undefined) return false;
  switch (role as UserRole | string) {
    case 'owner':
      return row.owner;
    case 'manager':
      return row.manager;
    case 'collaborator':
      return row.collaborator;
    default:
      return false;
  }
}

/** The roles the table says may perform `action`, in decreasing power. */
export function rolesFor(action: UserAction): UserRole[] {
  return (['owner', 'manager', 'collaborator'] as const).filter((role) =>
    roleAllows(role, action),
  );
}