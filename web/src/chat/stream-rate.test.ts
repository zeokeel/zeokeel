import { describe, expect, it } from 'vitest';
import { formatRate, tokensPerSecond } from './stream-rate';

describe('stream rate', () => {
  it('estimates tokens per second from characters and elapsed time', () => {
    // 300 chars over 1s ≈ 100 tokens/s at 3 chars per token.
    expect(Math.round(tokensPerSecond({ chars: 300, elapsedMs: 1000 }))).toBe(100);
  });

  it('says nothing until enough text and time have arrived', () => {
    expect(tokensPerSecond({ chars: 0, elapsedMs: 1000 })).toBe(0);
    expect(tokensPerSecond({ chars: 100, elapsedMs: 200 })).toBe(0);
  });

  it('formats a rate the footer can show', () => {
    expect(formatRate({ chars: 3000, elapsedMs: 1000 })).toBe('1000');
    expect(formatRate({ chars: 300, elapsedMs: 1000 })).toBe('100');
    expect(formatRate({ chars: 36, elapsedMs: 1000 })).toBe('12');
    expect(formatRate({ chars: 30, elapsedMs: 6000 })).toBe('1.7');
    expect(formatRate({ chars: 3, elapsedMs: 5000 })).toBe('');
  });
});
