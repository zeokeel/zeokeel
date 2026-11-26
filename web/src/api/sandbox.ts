/**
 * OS-sandbox status (`sandbox.status` — docs/TRANSPORT.md §5.1.5).
 *
 * Why this is a read model and not a log line: shell commands never park on an
 * approval card in this product (2026-11-26 permission model), so the OS runner
 * is the only thing that confines their file effects. On a host without one the
 * model runs commands with the process's own permissions, and the user has to be
 * able to see that — it is the difference between "the write fence is real" and
 * "the model was given a rule it can ignore" (docs/APPROVAL-SURFACES.md §S4.1).
 *
 * The gateway owns the vocabulary: `confinement` is `os` or `none`, and an empty
 * value means "no sandbox module is assembled" — which is **not** the same as
 * confined (there commands stay behind the approval gate instead).
 */
import { authorizationReady } from './gateway';
import { gatewayRpc } from './ws/rpc';

/** `confinement` values of `sandbox.status` (contracts.SandboxConfinement*). */
export const SANDBOX_CONFINEMENT_OS = 'os';
export const SANDBOX_CONFINEMENT_NONE = 'none';

/** The wire shape of `sandbox.status`. */
export interface SandboxStatus {
  /** Whether this host has a usable OS sandbox runner. */
  available: boolean;
  /** `runtime.GOOS` of the gateway process. */
  platform: string;
  /** Why the sandbox is unavailable (absent when available). */
  reason?: string;
  /** `os` / `none`, or empty for "no sandbox module assembled (unknown)". */
  confinement: string;
  /** True exactly when `confinement === 'none'`: no OS write constraint at all. */
  commandsUnconfined: boolean;
  /** The gateway's own sentence for the state (absent when confined). */
  note?: string;
}

/**
 * Read the sandbox status once. There is no delta stream for it, so callers read
 * on demand; a timer would be the polling loop AGENTS.md §3 forbids.
 */
export async function loadSandboxStatus(): Promise<SandboxStatus> {
  await authorizationReady();
  const result = await gatewayRpc().request<Partial<SandboxStatus>>('sandbox.status', {});
  const reason = typeof result.reason === 'string' && result.reason !== '' ? result.reason : undefined;
  const note = typeof result.note === 'string' && result.note !== '' ? result.note : undefined;
  return {
    available: result.available === true,
    platform: typeof result.platform === 'string' ? result.platform : '',
    ...(reason ? { reason } : {}),
    confinement: typeof result.confinement === 'string' ? result.confinement : '',
    // A missing flag must never read as "confined": derive it from the label the
    // gateway sent, and treat an unknown label as unknown rather than safe.
    commandsUnconfined: result.commandsUnconfined === true,
    ...(note ? { note } : {}),
  };
}