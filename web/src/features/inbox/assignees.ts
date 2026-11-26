/**
 * Who a dispatched inbox item can be handed to (`inbox.dispatch`'s `assignee`).
 *
 * A value here is a **preset id**, not free text: the backend's wake-up rule is
 * keyed on the lead role (`contracts.PresetLeadID`), and the board, the lease
 * layer and the agent gate all read that same vocabulary. Keeping the list in one
 * place means adding a role is a one-line change here instead of another
 * hard-coded string at a call site, and it keeps the client from inventing an
 * owner the gateway would only refuse.
 *
 * The labels reuse the role words the task detail already shows
 * (`task.detail.role.*`), so a role is called the same thing on every screen.
 *
 * What each value *does* is the backend's decision, not this list's: only the lead
 * role starts a run (`inbox.dispatch`, docs/TRANSPORT.md §5.1.13), the other two
 * record an owner for the lead to route to. The screen therefore offers the
 * product's roles and reports what actually happened from the answer's `dispatch`
 * record instead of promising a run on the client's side.
 */
import type { MessageKey } from '@/i18n';

export interface DispatchAssigneeOption {
  /** The preset id sent as `assignee` (contracts.Preset*ID). */
  readonly value: string;
  /** The catalogue key of its label. */
  readonly key: MessageKey;
}

/** Roles, in the order the work flows: orchestrate → execute → audit. */
export const DISPATCH_ASSIGNEES: readonly DispatchAssigneeOption[] = [
  { value: 'lead', key: 'task.detail.role.lead' },
  { value: 'executor', key: 'task.detail.role.executor' },
  { value: 'auditor', key: 'task.detail.role.auditor' },
];