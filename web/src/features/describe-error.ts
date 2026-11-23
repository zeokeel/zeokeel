import type { MessageKey } from '@/i18n';
import { isApiError } from '@/api/errors';

/**
 * One place where an api failure becomes a sentence.
 *
 * The api layer throws **codes** (`ApiError`), never copy: a Chinese `message`
 * thrown in `src/api` cannot be translated afterwards, and it turns "what went
 * wrong" into string matching. Screens call this at the point of display.
 */
export function describeError(
  cause: unknown,
  t: (key: MessageKey, params?: Record<string, string | number>) => string,
  fallback: MessageKey,
): string {
  if (isApiError(cause)) {
    if (cause.code === 'method_unsupported') {
      return t('error.methodUnsupported', { method: cause.detail ?? '' });
    }
    if (cause.code === 'auth_exchange_failed') {
      return t('error.authExchangeFailed', { status: cause.detail ?? '' });
    }
    if (cause.code === 'session_create_failed') return t('error.sessionCreateFailed');
    if (cause.code === 'settings_write_failed') return t('error.settingsWriteFailed');
  }
  // A transport/protocol failure: the gateway's own text is the only detail we
  // have, and it is not ours to translate.
  if (cause instanceof Error && cause.message) return cause.message;
  return t(fallback);
}
