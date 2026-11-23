import { describe, expect, it } from 'vitest';
import { ApiError, isApiError, isMethodUnsupported } from '@/api/errors';
import { describeError } from '@/features/describe-error';
import { translate } from '@/i18n';

/**
 * The api layer throws **codes**, never copy (task-26).
 *
 * A Chinese `message` thrown in `src/api` cannot be translated afterwards and
 * turns "what went wrong" into string matching. These tests pin the contract:
 * the code is machine-checkable, and the sentence is produced at the display
 * point, in the user's language.
 */
describe('ApiError', () => {
  it('carries a code and machine-readable detail, not a sentence', () => {
    const error = new ApiError('method_unsupported', 'settings.set');
    expect(error.code).toBe('method_unsupported');
    expect(error.detail).toBe('settings.set');
    // The message exists for logs; it is the code plus detail, not prose.
    expect(error.message).toBe('method_unsupported: settings.set');
    expect(isApiError(error)).toBe(true);
    expect(isApiError(error, 'method_unsupported')).toBe(true);
    expect(isApiError(error, 'auth_exchange_failed')).toBe(false);
  });

  it('recognises JSON-RPC method-not-found', () => {
    expect(isMethodUnsupported({ code: -32601 })).toBe(true);
    expect(isMethodUnsupported({ code: -32602 })).toBe(false);
    expect(isMethodUnsupported(null)).toBe(false);
  });
});

describe('describeError', () => {
  const zh = (key: Parameters<typeof translate>[1], params?: Record<string, string | number>) =>
    translate('zh', key, params);
  const en = (key: Parameters<typeof translate>[1], params?: Record<string, string | number>) =>
    translate('en', key, params);

  it('phrases each code in the user\'s language', () => {
    // Assertions here stay ASCII: `src/api` must contain no user-visible Chinese
    // at all, and that includes its tests (the catalogue owns the wording).
    const cause = new ApiError('method_unsupported', 'settings.set');
    const zhText = describeError(cause, zh, 'settings.saveFailed');
    expect(zhText).toContain('settings.set');
    expect(zhText).not.toBe(describeError(cause, en, 'settings.saveFailed'));
    expect(describeError(cause, en, 'settings.saveFailed')).toBe(
      'This gateway does not offer settings.set',
    );

    const auth = new ApiError('auth_exchange_failed', '500');
    expect(describeError(auth, zh, 'settings.saveFailed')).toContain('500');
    expect(describeError(auth, en, 'settings.saveFailed')).toBe('Authorization failed (HTTP 500)');
  });

  it('falls back to the screen own message for anything else', () => {
    expect(describeError(new Error('boom'), zh, 'settings.saveFailed')).toBe('boom');
    expect(describeError(undefined, zh, 'settings.saveFailed')).toBe(translate('zh', 'settings.saveFailed'));
  });
});
