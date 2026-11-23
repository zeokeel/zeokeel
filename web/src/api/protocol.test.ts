import { describe, expect, it } from 'vitest';
import { parseRunEvent, RUN_EVENTS } from './protocol';

describe('parseRunEvent', () => {
  it.each(RUN_EVENTS)('accepts %s', (name) => {
    const event = parseRunEvent({ event: name, data: '{"turnId":"t1"}' });
    expect(event).toMatchObject({ name, rawName: name, data: { turnId: 't1' } });
  });

  it('reports an unlisted event as unknown, keeping its name', () => {
    const event = parseRunEvent({ event: 'message.stop', data: '{}' });
    expect(event).toMatchObject({ name: 'unknown', rawName: 'message.stop' });
  });

  it('reports malformed JSON as unknown rather than throwing', () => {
    const event = parseRunEvent({ event: 'message.delta', data: '{"text":' });
    expect(event).toMatchObject({ name: 'unknown', rawName: 'message.delta' });
    expect(event?.data).toBeUndefined();
  });

  it('passes through an already parsed data payload', () => {
    const event = parseRunEvent({ event: 'usage', data: { inputTokens: 3 } });
    expect(event?.data).toEqual({ inputTokens: 3 });
  });

  it('returns null for a chunk without an event name', () => {
    expect(parseRunEvent({ data: '{}' })).toBeNull();
    expect(parseRunEvent({ event: '', data: '{}' })).toBeNull();
    expect(parseRunEvent(null)).toBeNull();
    expect(parseRunEvent('nope')).toBeNull();
  });
});