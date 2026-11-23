/**
 * A failure the UI can classify without reading prose.
 *
 * The api layer must not produce user-visible copy (task-26): a Chinese
 * `Error#message` thrown here cannot be translated later, and it makes "what
 * went wrong" a string comparison. Each failure gets a **code**, the screen
 * decides how to phrase it in the user's language.
 *
 * `detail` stays for things that are genuinely data — an HTTP status, a method
 * name — never for a sentence.
 */
export type ApiErrorCode =
  | 'method_unsupported' // the gateway does not offer this method (-32601)
  | 'auth_exchange_failed' // POST /auth answered something unusable
  | 'session_create_failed'
  | 'settings_write_failed';

export class ApiError extends Error {
  constructor(
    readonly code: ApiErrorCode,
    /** Machine-readable context: method name, HTTP status, … */
    readonly detail?: string,
  ) {
    // The message is for logs, not for the UI: it is the code plus its detail.
    super(detail ? `${code}: ${detail}` : code);
    this.name = 'ApiError';
  }
}

export function isApiError(value: unknown, code?: ApiErrorCode): value is ApiError {
  return value instanceof ApiError && (code === undefined || value.code === code);
}

/** JSON-RPC "method not found": a capability gap, not a transport fault. */
export function isMethodUnsupported(value: unknown): boolean {
  return (value as { code?: number } | null)?.code === -32601;
}