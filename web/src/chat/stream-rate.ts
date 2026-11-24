/**
 * Live output rate (the DeepSeek footer's "232 tok/s").
 *
 * The gateway sends no token counts while a turn streams — only character deltas —
 * so the rate is a **measured estimate**: characters arriving since the first delta,
 * divided by the elapsed wall time, at roughly four characters per token for the
 * mixed CJK/ASCII text this client sees. It is labelled as an estimate (tok/s with
 * a `~` when it is not exact) rather than passed off as the provider's accounting:
 * the exact numbers arrive in the `usage` event at the end of the turn.
 */

/** Rough characters per token: CJK is ~1, Latin ~4; the mix averages near 3. */
export const CHARS_PER_TOKEN = 3;

export interface RateSample {
  /** Characters received during this turn. */
  chars: number;
  /** Milliseconds since the first delta of this turn. */
  elapsedMs: number;
}

/** Tokens per second from one sample; 0 while too little has arrived to mean anything. */
export function tokensPerSecond(sample: RateSample): number {
  if (sample.elapsedMs <= 0 || sample.chars <= 0) return 0;
  const seconds = sample.elapsedMs / 1000;
  if (seconds < 0.5) return 0;
  const tokens = sample.chars / CHARS_PER_TOKEN;
  return Math.max(0, tokens / seconds);
}

/** Short display form: 232, 12.5, and nothing below one token per second. */
export function formatRate(sample: RateSample): string {
  const rate = tokensPerSecond(sample);
  if (rate < 1) return '';
  return rate >= 10 ? String(Math.round(rate)) : rate.toFixed(1);
}
