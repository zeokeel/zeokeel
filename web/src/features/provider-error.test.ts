import { describe, expect, it } from 'vitest';
import { classifyProviderFailure } from './provider-error';

/**
 * Provider failures arrive as the model client's own words — a JSON body inside a
 * sentence, in English, wrapped with our internal code. v0.3.0 showed them raw in
 * a Chinese UI. The classifier turns the two **real** messages from the Linux
 * build into something a user can act on, and keeps the original for the details.
 */

const NO_KEY =
  'rig: provider "openai" stream: openai: chat completions failed: 401 Unauthorized: { "error": { "message": "You didn\'t provide an API key. …" } }（-32603）';

const BLACKHOLE =
  'rig: provider "openai" stream: openai: chat completions request: Post "https://api.openai.com/v1/chat/completions": dial tcp 108.160.166.42:443: i/o timeout（-32603）';

describe('classifyProviderFailure', () => {
  it('recognises the "no API key" 401 and keeps the provider name', () => {
    const failure = classifyProviderFailure(NO_KEY);
    expect(failure.kind).toBe('noCredential');
    expect(failure.provider).toBe('openai');
    // The original text is kept whole: the details panel shows *our* code's words.
    expect(failure.detail).toContain('You didn\'t provide an API key');
  });

  it('recognises a 401 that is not about a missing key', () => {
    expect(classifyProviderFailure('openai: 401 Unauthorized: invalid token（-32603）').kind).toBe(
      'unauthorized',
    );
  });

  it('recognises an unreachable network from the real timeout', () => {
    const failure = classifyProviderFailure(BLACKHOLE);
    expect(failure.kind).toBe('unreachable');
    expect(failure.detail).toContain('i/o timeout');
  });

  it.each([
    'dial tcp 1.2.3.4:443: connect: connection refused',
    'no such host',
    'context deadline exceeded',
  ])('recognises %s as unreachable', (text) => {
    expect(classifyProviderFailure(text).kind).toBe('unreachable');
  });

  it('strips our internal code from the end only', () => {
    const failure = classifyProviderFailure('boom（-32603）');
    expect(failure.detail).toBe('boom');
  });

  it('does not damage a parenthesised negative number inside the text', () => {
    // The strip must be narrow: a bare `(-21)` in prose is not an internal code.
    const failure = classifyProviderFailure('temperature must be ≥ 0, got (-21) at index 3');
    expect(failure.detail).toContain('(-21)');
  });

  it('falls back to unknown and still keeps the whole message', () => {
    const failure = classifyProviderFailure('something else entirely');
    expect(failure.kind).toBe('unknown');
    expect(failure.detail).toBe('something else entirely');
  });
});
